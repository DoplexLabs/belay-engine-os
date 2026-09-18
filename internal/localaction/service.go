// Package localaction orchestrates browser-only Local fix-attempt actions.
// It contains no HTTP, browser, MCP, or readmodel dependencies.
package localaction

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
	actionTokenLifetime = 15 * time.Minute
)

var (
	ErrInvalidRequest      = errors.New("invalid fix action request")
	ErrCursorExpired       = errors.New("fix action cursor expired")
	ErrNotFound            = errors.New("fix action resource not found")
	ErrIneligibleIssue     = errors.New("issue is ineligible for fix action")
	ErrIdempotencyConflict = errors.New("fix action idempotency conflict")
	ErrAlreadyRetracted    = errors.New("fix action is already retracted")

	issueIDPattern      = regexp.MustCompile(`^iss_[a-z2-7]{52}$`)
	annotationIDPattern = regexp.MustCompile(`^fxa_[a-z2-7]{52}$`)
)

type Repository interface {
	EvaluateFixEligibility(
		context.Context,
		model.FixEligibilityQuery,
	) (model.FixEligibility, error)
	RecordFixAnnotation(
		context.Context,
		local.FixAnnotationInput,
	) (local.FixAnnotationResult, error)
	RetractFixAnnotation(
		context.Context,
		local.FixRetractionInput,
	) (local.FixRetractionResult, error)
	QueryFixAnnotations(
		context.Context,
		model.FixAnnotationQuery,
	) (model.FixAnnotationPage, error)
}

type TokenCodec interface {
	IssueFixActionToken(model.FixActionClaims) (string, error)
	DecodeFixActionToken(string) (model.FixActionClaims, error)
}

type Service struct {
	repository Repository
	tokens     TokenCodec
	now        func() time.Time
}

type Option func(*Service)

func WithClock(clock func() time.Time) Option {
	return func(service *Service) {
		if clock != nil {
			service.now = clock
		}
	}
}

type FixEligibility struct {
	Eligible             bool
	Reason               string
	ActionToken          *string
	ExpiresAt            *time.Time
	ChangeCatalogVersion string
}

type FixAttemptPage struct {
	Data                []model.FixAnnotation
	NextCursor          *string
	HasMore             bool
	ReturnedCount       int
	Limit               int
	EvidenceEvaluatedAt time.Time
}

type FixAttemptResult struct {
	Annotation model.FixAnnotation
	Replayed   bool
}

type FixRetractionResult struct {
	Retraction model.FixRetraction
	Replayed   bool
}

func New(repository Repository, tokens TokenCodec, options ...Option) (*Service, error) {
	if repository == nil {
		return nil, errors.New("local action service requires a repository")
	}
	if tokens == nil {
		return nil, errors.New("local action service requires a token codec")
	}
	service := &Service{
		repository: repository,
		tokens:     tokens,
		now:        time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func (s *Service) PrepareFixAttempt(
	ctx context.Context,
	issueID string,
	view model.IssueViewClaims,
) (FixEligibility, error) {
	issueID = normalizeIssueID(issueID)
	view.IssuedAt = view.IssuedAt.UTC()
	if !issueIDPattern.MatchString(issueID) ||
		view.CursorEpoch == "" ||
		view.Snapshot <= 0 ||
		view.RetentionGeneration <= 0 ||
		view.IssuedAt.IsZero() {
		return FixEligibility{}, ErrInvalidRequest
	}
	now := s.now().UTC()
	if view.IssuedAt.After(now) {
		return FixEligibility{}, ErrInvalidRequest
	}
	expiresAt := view.IssuedAt.Add(actionTokenLifetime)
	if now.After(expiresAt) {
		return FixEligibility{}, ErrCursorExpired
	}
	eligibility, err := s.repository.EvaluateFixEligibility(
		ctx,
		model.FixEligibilityQuery{
			IssueID:             issueID,
			CursorEpoch:         view.CursorEpoch,
			Snapshot:            view.Snapshot,
			RetentionGeneration: view.RetentionGeneration,
			IssuedAt:            view.IssuedAt,
		},
	)
	if err != nil {
		return FixEligibility{}, mapRepositoryError(err)
	}
	result := FixEligibility{
		Eligible:             eligibility.Eligible,
		Reason:               eligibility.Reason,
		ChangeCatalogVersion: model.FixChangeCatalogVersion,
	}
	if !eligibility.Eligible {
		return result, nil
	}
	claims := model.FixActionClaims{
		Version:             model.FixActionTokenVersion,
		CursorEpoch:         eligibility.CursorEpoch,
		IssueID:             issueID,
		Snapshot:            eligibility.Snapshot,
		RetentionGeneration: eligibility.RetentionGeneration,
		IssuedAt:            view.IssuedAt,
		ExpiresAt:           expiresAt,
	}
	token, err := s.tokens.IssueFixActionToken(claims)
	if err != nil {
		if errors.Is(err, local.ErrFixActionTokenInvalid) {
			return FixEligibility{}, ErrInvalidRequest
		}
		if errors.Is(err, local.ErrFixActionTokenExpired) {
			return FixEligibility{}, ErrCursorExpired
		}
		return FixEligibility{}, err
	}
	result.ActionToken = &token
	result.ExpiresAt = &expiresAt
	return result, nil
}

func (s *Service) RecordFixAttempt(
	ctx context.Context,
	issueID string,
	actionToken string,
	changeKind model.FixChangeKind,
	idempotencyKey string,
) (FixAttemptResult, error) {
	issueID = normalizeIssueID(issueID)
	actionToken = strings.TrimSpace(actionToken)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !issueIDPattern.MatchString(issueID) ||
		actionToken == "" ||
		!changeKind.Valid() ||
		!model.IsCanonicalUUIDv4(idempotencyKey) {
		return FixAttemptResult{}, ErrInvalidRequest
	}

	// Authenticate structure/MAC and bind the signed issue to the route before
	// storage is allowed to perform durable idempotency replay. Expiry remains
	// intentionally deferred to storage for new writes only.
	claims, err := s.tokens.DecodeFixActionToken(actionToken)
	if err != nil {
		if errors.Is(err, local.ErrFixActionTokenInvalid) {
			return FixAttemptResult{}, ErrInvalidRequest
		}
		if errors.Is(err, local.ErrFixActionTokenExpired) {
			return FixAttemptResult{}, ErrCursorExpired
		}
		return FixAttemptResult{}, err
	}
	if claims.IssueID != issueID {
		return FixAttemptResult{}, ErrInvalidRequest
	}
	result, err := s.repository.RecordFixAnnotation(
		ctx,
		local.FixAnnotationInput{
			Claims:         claims,
			ChangeKind:     changeKind,
			RecordedVia:    model.FixRecordedViaLocalUI,
			IdempotencyKey: idempotencyKey,
		},
	)
	if err != nil {
		return FixAttemptResult{}, mapRepositoryError(err)
	}
	return FixAttemptResult{
		Annotation: result.Annotation,
		Replayed:   result.Replayed,
	}, nil
}

func (s *Service) RetractFixAttempt(
	ctx context.Context,
	issueID string,
	annotationID string,
	reason model.FixRetractionReason,
	idempotencyKey string,
) (FixRetractionResult, error) {
	issueID = normalizeIssueID(issueID)
	annotationID = strings.ToLower(strings.TrimSpace(annotationID))
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !issueIDPattern.MatchString(issueID) ||
		!annotationIDPattern.MatchString(annotationID) ||
		!reason.Valid() ||
		!model.IsCanonicalUUIDv4(idempotencyKey) {
		return FixRetractionResult{}, ErrInvalidRequest
	}
	result, err := s.repository.RetractFixAnnotation(
		ctx,
		local.FixRetractionInput{
			IssueID:        issueID,
			AnnotationID:   annotationID,
			Reason:         reason,
			RecordedVia:    model.FixRecordedViaLocalUI,
			IdempotencyKey: idempotencyKey,
		},
	)
	if err != nil {
		return FixRetractionResult{}, mapRepositoryError(err)
	}
	return FixRetractionResult{
		Retraction: result.Retraction,
		Replayed:   result.Replayed,
	}, nil
}

func (s *Service) ListFixAttempts(
	ctx context.Context,
	issueID string,
	limit int,
	cursor string,
) (FixAttemptPage, error) {
	issueID = normalizeIssueID(issueID)
	cursor = strings.TrimSpace(cursor)
	if !issueIDPattern.MatchString(issueID) {
		return FixAttemptPage{}, ErrInvalidRequest
	}
	limit = boundedLimit(limit)
	query := model.FixAnnotationQuery{
		IssueID: issueID,
		Limit:   limit,
	}
	if cursor != "" {
		decoded, err := decodeHistoryCursor(cursor, issueID)
		if err != nil {
			return FixAttemptPage{}, err
		}
		query.AnnotationSnapshot = decoded.AnnotationSnapshot
		query.RetractionSnapshot = decoded.RetractionSnapshot
		query.Cursor = &model.FixAnnotationPosition{
			IssueID:      issueID,
			RecordedAt:   decoded.RecordedAt,
			AnnotationID: decoded.AnnotationID,
		}
	}
	page, err := s.repository.QueryFixAnnotations(ctx, query)
	if err != nil {
		return FixAttemptPage{}, mapRepositoryError(err)
	}
	data := page.Data
	if data == nil {
		data = []model.FixAnnotation{}
	}
	var nextCursor *string
	if page.HasMore {
		if len(data) == 0 {
			return FixAttemptPage{}, errors.New("fix repository returned an unusable continuation")
		}
		last := data[len(data)-1]
		encoded, err := encodeHistoryCursor(historyCursor{
			IssueID:            issueID,
			AnnotationSnapshot: page.AnnotationSnapshot,
			RetractionSnapshot: page.RetractionSnapshot,
			RecordedAt:         last.RecordedAt.UTC(),
			AnnotationID:       last.AnnotationID,
		})
		if err != nil {
			return FixAttemptPage{}, err
		}
		nextCursor = &encoded
	}
	return FixAttemptPage{
		Data:                data,
		NextCursor:          nextCursor,
		HasMore:             page.HasMore,
		ReturnedCount:       len(data),
		Limit:               limit,
		EvidenceEvaluatedAt: page.EvidenceEvaluatedAt,
	}, nil
}

func normalizeIssueID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func boundedLimit(value int) int {
	if value <= 0 {
		return defaultHistoryLimit
	}
	if value > maxHistoryLimit {
		return maxHistoryLimit
	}
	return value
}

func mapRepositoryError(err error) error {
	switch {
	case errors.Is(err, local.ErrFixInvalidInput),
		errors.Is(err, local.ErrFixHistorySnapshotInvalid),
		errors.Is(err, model.ErrIssueSnapshotInvalid):
		return ErrInvalidRequest
	case errors.Is(err, model.ErrIssueSnapshotExpired):
		return ErrCursorExpired
	case errors.Is(err, local.ErrFixIssueNotFound),
		errors.Is(err, local.ErrFixAnnotationNotFound):
		return ErrNotFound
	case errors.Is(err, local.ErrFixIneligible):
		return ErrIneligibleIssue
	case errors.Is(err, local.ErrFixIdempotencyConflict):
		return ErrIdempotencyConflict
	case errors.Is(err, local.ErrFixAlreadyRetracted):
		return ErrAlreadyRetracted
	default:
		return err
	}
}
