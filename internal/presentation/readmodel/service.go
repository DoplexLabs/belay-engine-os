// Package readmodel is the presentation boundary shared by Local HTTP and MCP
// adapters. It owns the public filtering and opaque cursor semantics while
// reading through a repository interface rather than SQLite.
package readmodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/initialization"
)

const SchemaVersion = "belay.read.v1"
const IssueProjectionVersion = "belay.issue.v1"

const (
	defaultSessionLimit  = 20
	maxSessionLimit      = 100
	defaultTimelineLimit = 100
	maxTimelineLimit     = 500
	defaultActivityLimit = 50
	maxActivityLimit     = 200
	defaultFindingLimit  = 20
	maxFindingLimit      = 100
	defaultIssueLimit    = 20
	maxIssueLimit        = 100
	issueCursorLifetime  = 15 * time.Minute
	cursorVersion        = 1
	maxCursorBytes       = 2048
)

var (
	ErrInvalidCursor           = errors.New("invalid read cursor")
	ErrInvalidRequest          = errors.New("invalid read request")
	ErrCursorExpired           = errors.New("read cursor expired")
	ErrNotFound                = errors.New("read resource not found")
	ErrCapabilityUnavailable   = errors.New("read capability unavailable")
	ErrMonitoringCatchingUp    = errors.New("fix monitoring catch-up is in progress")
	ErrMonitoringCatchupFailed = errors.New("fix monitoring catch-up failed")

	issueIDPattern       = regexp.MustCompile(`^iss_[a-z2-7]{52}$`)
	fingerprintIDPattern = regexp.MustCompile(`^ifp_[a-z2-7]{52}$`)
	categoryPattern      = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
)

type Repository interface {
	QuerySessions(context.Context, model.SessionQuery) (model.SessionPage, error)
	GetSession(context.Context, string) (model.SessionSummary, time.Time, error)
	QuerySessionTimeline(context.Context, model.TimelineQuery) (model.EventPage, error)
	QueryActivityPage(context.Context, model.ActivityQuery) (model.EventPage, error)
	QueryFindings(context.Context, model.FindingQuery) (model.FindingPage, error)
	GetStats(context.Context) (model.LocalStats, time.Time, error)
}

type IssueRepository interface {
	QueryIssues(context.Context, model.IssueQuery) (model.IssuePage, error)
	QueryIssueOccurrences(
		context.Context,
		model.IssueOccurrenceQuery,
	) (model.IssueOccurrencePage, error)
	LookupSessionEvents(
		context.Context,
		model.EventLookupQuery,
	) (model.EventLookupResult, error)
	VisitSessionEvents(
		context.Context,
		model.EventLookupQuery,
		func(model.Event) error,
	) (model.EventLookupSummary, error)
}

type AttentionFamilyRepository interface {
	QueryAttentionFamilies(
		context.Context,
		model.AttentionFamilyQuery,
	) (model.AttentionFamilyPage, error)
	QueryAttentionFamilyMembers(
		context.Context,
		model.AttentionFamilyMemberQuery,
	) (model.AttentionFamilyMemberPage, error)
}

type FixMonitoringRepository interface {
	QueryFixMonitoring(
		context.Context,
		model.FixMonitoringQuery,
	) (model.FixMonitoringPage, error)
	QueryIssueFixMonitoring(
		context.Context,
		model.FixMonitoringDetailQuery,
	) (model.FixMonitoringDetailPage, error)
	QueryFixRecurrenceObservations(
		context.Context,
		model.FixRecurrenceObservationQuery,
	) (model.FixRecurrenceObservationPage, error)
}

type Service struct {
	repository                Repository
	issueRepository           IssueRepository
	attentionFamilyRepository AttentionFamilyRepository
	issueCursorCodec          IssueCursorCodec
	fixMonitoringRepository   FixMonitoringRepository
	transcriptRepository      TranscriptRepository
	costIssueRepository       CostIssueRepository
	insightRepository         InsightRepository
	reportRepository          UsageReportRepository
	userInsightRepository     UserInsightRepository
	sessionProjectRepository  SessionProjectRepository
	habitDebriefRepository    HabitDebriefRepository
	userInsightHarness        func() (string, bool)
	initializationProvider    initialization.Provider
	now                       func() time.Time
}

type Option func(*Service)

func WithIssueRepository(repository IssueRepository) Option {
	return func(service *Service) {
		service.issueRepository = repository
		if familyRepository, ok := repository.(AttentionFamilyRepository); ok {
			service.attentionFamilyRepository = familyRepository
		}
	}
}

func WithAttentionFamilyRepository(repository AttentionFamilyRepository) Option {
	return func(service *Service) {
		service.attentionFamilyRepository = repository
	}
}

func WithIssueCursorCodec(codec IssueCursorCodec) Option {
	return func(service *Service) {
		service.issueCursorCodec = codec
	}
}

func WithFixMonitoringRepository(repository FixMonitoringRepository) Option {
	return func(service *Service) {
		service.fixMonitoringRepository = repository
	}
}

func WithInitializationProvider(provider initialization.Provider) Option {
	return func(service *Service) {
		service.initializationProvider = provider
	}
}

func WithClock(clock func() time.Time) Option {
	return func(service *Service) {
		if clock != nil {
			service.now = clock
		}
	}
}

type SessionListRequest struct {
	Limit          int
	Cursor         string
	Harness        string
	Outcome        string
	History        string
	OccurredAfter  *time.Time
	OccurredBefore *time.Time
	Query          string
}

type TimelineRequest struct {
	SessionID string
	Limit     int
	Cursor    string
}

type ActivityRequest struct {
	Filter model.ActivityFilter
	Cursor string
}

type FindingListRequest struct {
	Limit     int
	Cursor    string
	Since     *time.Time
	Severity  string
	SessionID string
}

type IssueListRequest struct {
	Limit          int
	Cursor         string
	Severity       string
	Category       string
	Harness        string
	Origin         string
	AnalysisStatus string
	ObservedAfter  *time.Time
	Recurrence     string
	SessionID      string
	FingerprintID  string
	AttentionKind  string
	Experimental   string
}

type IssueDetailRequest struct {
	IssueID    string
	Limit      int
	Cursor     string
	ViewCursor string
}

type EventLookupRequest struct {
	SessionID string
	EventIDs  []string
}

type SessionList struct {
	SchemaVersion  string                 `json:"schema_version"`
	Data           []model.SessionSummary `json:"data"`
	NextCursor     *string                `json:"next_cursor"`
	HasMore        bool                   `json:"has_more"`
	ReturnedCount  int                    `json:"returned_count"`
	Limit          int                    `json:"limit"`
	FiltersApplied bool                   `json:"filters_applied"`
	DataThrough    time.Time              `json:"data_through"`
}

type SessionTimeline struct {
	SchemaVersion string        `json:"schema_version"`
	SessionID     string        `json:"session_id"`
	Data          []model.Event `json:"data"`
	NextCursor    *string       `json:"next_cursor"`
	HasMore       bool          `json:"has_more"`
	ReturnedCount int           `json:"returned_count"`
	Limit         int           `json:"limit"`
	DataThrough   time.Time     `json:"data_through"`
}

type SessionDetail struct {
	SchemaVersion string               `json:"schema_version"`
	Data          model.SessionSummary `json:"data"`
	DataThrough   time.Time            `json:"data_through"`
}

type ActivityList struct {
	SchemaVersion string        `json:"schema_version"`
	Data          []model.Event `json:"data"`
	NextCursor    *string       `json:"next_cursor"`
	HasMore       bool          `json:"has_more"`
	ReturnedCount int           `json:"returned_count"`
	Limit         int           `json:"limit"`
	DataThrough   time.Time     `json:"data_through"`
}

type FindingList struct {
	SchemaVersion string                 `json:"schema_version"`
	Data          []model.FindingSummary `json:"data"`
	NextCursor    *string                `json:"next_cursor"`
	HasMore       bool                   `json:"has_more"`
	ReturnedCount int                    `json:"returned_count"`
	Limit         int                    `json:"limit"`
	DataThrough   time.Time              `json:"data_through"`
}

type StatsResponse struct {
	SchemaVersion  string                 `json:"schema_version"`
	MetricVersion  string                 `json:"metric_version"`
	Data           model.LocalStats       `json:"data"`
	DataThrough    time.Time              `json:"data_through"`
	Initialization *initialization.Status `json:"initialization"`
}

type InitializationResponse struct {
	SchemaVersion  string                `json:"schema_version"`
	Initialization initialization.Status `json:"initialization"`
}

type IssueList struct {
	SchemaVersion     string                      `json:"schema_version"`
	ProjectionVersion string                      `json:"projection_version"`
	Data              []model.IssueSummary        `json:"data"`
	Analysis          model.IssueAnalysisCoverage `json:"analysis"`
	Selection         IssueSelection              `json:"selection"`
	ViewCursor        string                      `json:"view_cursor"`
	NextCursor        *string                     `json:"next_cursor"`
	HasMore           bool                        `json:"has_more"`
	ReturnedCount     int                         `json:"returned_count"`
	Limit             int                         `json:"limit"`
}

type IssueDetailData struct {
	Issue       model.IssueSummary      `json:"issue"`
	Occurrences []model.IssueOccurrence `json:"occurrences"`
}

type IssueDetail struct {
	SchemaVersion          string                      `json:"schema_version"`
	ProjectionVersion      string                      `json:"projection_version"`
	Data                   IssueDetailData             `json:"data"`
	Catalog                IssueCatalogMetadata        `json:"catalog"`
	GlobalAnalysisCoverage model.IssueAnalysisCoverage `json:"global_analysis_coverage"`
	ViewCursor             string                      `json:"view_cursor"`
	NextCursor             *string                     `json:"next_cursor"`
	HasMore                bool                        `json:"has_more"`
	ReturnedCount          int                         `json:"returned_count"`
	Limit                  int                         `json:"limit"`
}

type EventLookup struct {
	SchemaVersion   string        `json:"schema_version"`
	Data            []model.Event `json:"data"`
	RequestedCount  int           `json:"requested_count"`
	FoundCount      int           `json:"found_count"`
	MissingCount    int           `json:"missing_count"`
	MissingEventIDs []string      `json:"missing_event_ids"`
	DataThrough     time.Time     `json:"data_through"`
}

type cursorEnvelope struct {
	Version      int    `json:"v"`
	Kind         string `json:"k"`
	Snapshot     int64  `json:"s"`
	Fingerprint  string `json:"f"`
	Time         string `json:"t,omitempty"`
	Sequence     int64  `json:"q,omitempty"`
	ID           string `json:"i,omitempty"`
	IssuedAt     string `json:"a,omitempty"`
	SeverityRank int    `json:"r,omitempty"`
	Repeated     bool   `json:"p,omitempty"`
}

func New(repository Repository, options ...Option) *Service {
	service := &Service{
		repository: repository,
		now:        time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *Service) RequireIssueEvidenceCapabilities() error {
	if s == nil || s.issueRepository == nil || s.issueCursorCodec == nil {
		return capabilityUnavailable()
	}
	return nil
}

// DecodeIssueViewCursor converts the private rowless issue-view cursor into
// neutral claims for non-presentation services. It preserves the cursor's
// structural and freshness validation without exposing its envelope.
func (s *Service) DecodeIssueViewCursor(value string) (model.IssueViewClaims, error) {
	cursor, err := s.openIssueCursor(
		strings.TrimSpace(value),
		"issue_view",
	)
	if err != nil {
		return model.IssueViewClaims{}, err
	}
	if cursor.Kind != "issue_view" ||
		cursor.Filters != nil ||
		cursor.Limit != 0 ||
		cursor.IssueID != "" ||
		cursor.Time != "" ||
		cursor.PositionID != "" ||
		cursor.SeverityRank != 0 ||
		cursor.Repeated {
		return model.IssueViewClaims{}, invalidCursor()
	}
	if err := validateIssueCursorSnapshot(cursor, s.nowUTC()); err != nil {
		return model.IssueViewClaims{}, err
	}
	issuedAt, _ := parseUTCTime(cursor.IssuedAt)
	return model.IssueViewClaims{
		CursorEpoch:         cursor.CursorEpoch,
		Snapshot:            cursor.Snapshot,
		RetentionGeneration: cursor.RetentionGeneration,
		IssuedAt:            issuedAt,
	}, nil
}

func (s *Service) ListSessions(ctx context.Context, limit int) (SessionList, error) {
	return s.ListSessionsPage(ctx, SessionListRequest{Limit: limit})
}

func (s *Service) ListSessionsPage(ctx context.Context, request SessionListRequest) (SessionList, error) {
	request.Limit = boundedLimit(request.Limit, defaultSessionLimit, maxSessionLimit)
	request.Harness = strings.TrimSpace(request.Harness)
	request.Outcome = strings.ToLower(strings.TrimSpace(request.Outcome))
	request.History = strings.ToLower(strings.TrimSpace(request.History))
	request.Query = strings.TrimSpace(request.Query)
	request.OccurredAfter = utcTime(request.OccurredAfter)
	request.OccurredBefore = utcTime(request.OccurredBefore)
	if err := validateSessionRequest(request); err != nil {
		return SessionList{}, err
	}
	fingerprint := sessionFingerprint(request)
	query := model.SessionQuery{
		Limit:          request.Limit + 1,
		Harness:        request.Harness,
		Outcome:        request.Outcome,
		History:        request.History,
		OccurredAfter:  request.OccurredAfter,
		OccurredBefore: request.OccurredBefore,
		Search:         request.Query,
	}
	if request.Cursor != "" {
		cursor, err := decodeCursor(request.Cursor, "sessions", fingerprint)
		if err != nil {
			return SessionList{}, err
		}
		cursorTime, _ := time.Parse(time.RFC3339Nano, cursor.Time)
		query.Snapshot = cursor.Snapshot
		query.CursorEndedAt = &cursorTime
		query.CursorSessionID = cursor.ID
	}
	page, err := s.repository.QuerySessions(ctx, query)
	if err != nil {
		return SessionList{}, err
	}
	data, hasMore := boundedPage(page.Data, request.Limit)
	nextCursor, err := sessionNextCursor(data, hasMore, page.Snapshot, fingerprint)
	if err != nil {
		return SessionList{}, err
	}
	s.decorateSessionProjects(ctx, data)
	return SessionList{
		SchemaVersion:  SchemaVersion,
		Data:           nonNil(data),
		NextCursor:     nextCursor,
		HasMore:        hasMore,
		ReturnedCount:  len(data),
		Limit:          request.Limit,
		FiltersApplied: true,
		DataThrough:    page.DataThrough,
	}, nil
}

func (s *Service) GetSession(ctx context.Context, sessionID string) (SessionDetail, error) {
	data, dataThrough, err := s.repository.GetSession(ctx, sessionID)
	if err == nil {
		rows := []model.SessionSummary{data}
		s.decorateSessionProjects(ctx, rows)
		data = rows[0]
	}
	return SessionDetail{
		SchemaVersion: SchemaVersion,
		Data:          data,
		DataThrough:   dataThrough,
	}, err
}

func (s *Service) GetSessionTimeline(
	ctx context.Context,
	sessionID string,
	limit int,
) (SessionTimeline, error) {
	return s.GetSessionTimelinePage(ctx, TimelineRequest{
		SessionID: sessionID,
		Limit:     limit,
	})
}

func (s *Service) GetSessionTimelinePage(
	ctx context.Context,
	request TimelineRequest,
) (SessionTimeline, error) {
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.Limit = boundedLimit(request.Limit, defaultTimelineLimit, maxTimelineLimit)
	if request.SessionID == "" || len(request.SessionID) > 256 {
		return SessionTimeline{}, invalidRequest("session_id is required and must not exceed 256 bytes")
	}
	fingerprint := stableFingerprint(struct {
		SessionID string `json:"session_id"`
	}{SessionID: request.SessionID})
	query := model.TimelineQuery{
		SessionID: request.SessionID,
		Limit:     request.Limit + 1,
	}
	if request.Cursor != "" {
		cursor, err := decodeCursor(request.Cursor, "session_events", fingerprint)
		if err != nil {
			return SessionTimeline{}, err
		}
		cursorTime, _ := time.Parse(time.RFC3339Nano, cursor.Time)
		query.Snapshot = cursor.Snapshot
		query.Cursor = &model.EventPosition{
			OccurredAt:     cursorTime,
			SourceSequence: cursor.Sequence,
			EventID:        cursor.ID,
		}
	}
	page, err := s.repository.QuerySessionTimeline(ctx, query)
	if err != nil {
		return SessionTimeline{}, err
	}
	data, hasMore := boundedPage(page.Data, request.Limit)
	nextCursor, err := eventNextCursor(
		data,
		hasMore,
		page.Snapshot,
		"session_events",
		fingerprint,
	)
	if err != nil {
		return SessionTimeline{}, err
	}
	return SessionTimeline{
		SchemaVersion: SchemaVersion,
		SessionID:     request.SessionID,
		Data:          nonNil(data),
		NextCursor:    nextCursor,
		HasMore:       hasMore,
		ReturnedCount: len(data),
		Limit:         request.Limit,
		DataThrough:   page.DataThrough,
	}, nil
}

func (s *Service) QueryActivity(
	ctx context.Context,
	filter model.ActivityFilter,
) (ActivityList, error) {
	return s.QueryActivityPage(ctx, ActivityRequest{Filter: filter})
}

func (s *Service) QueryActivityPage(
	ctx context.Context,
	request ActivityRequest,
) (ActivityList, error) {
	request.Filter.Limit = boundedLimit(
		request.Filter.Limit,
		defaultActivityLimit,
		maxActivityLimit,
	)
	request.Filter.Harness = strings.TrimSpace(request.Filter.Harness)
	request.Filter.ResourceKind = strings.ToLower(strings.TrimSpace(request.Filter.ResourceKind))
	request.Filter.Outcome = strings.ToLower(strings.TrimSpace(request.Filter.Outcome))
	request.Filter.OccurredAfter = utcTime(request.Filter.OccurredAfter)
	request.Filter.OccurredBefore = utcTime(request.Filter.OccurredBefore)
	if err := validateActivityRequest(request); err != nil {
		return ActivityList{}, err
	}
	fingerprint := activityFingerprint(request.Filter)
	query := model.ActivityQuery{Filter: request.Filter}
	query.Filter.Limit++
	if request.Cursor != "" {
		cursor, err := decodeCursor(request.Cursor, "activity", fingerprint)
		if err != nil {
			return ActivityList{}, err
		}
		cursorTime, _ := time.Parse(time.RFC3339Nano, cursor.Time)
		query.Snapshot = cursor.Snapshot
		query.Cursor = &model.EventPosition{
			OccurredAt:     cursorTime,
			SourceSequence: cursor.Sequence,
			EventID:        cursor.ID,
		}
	}
	page, err := s.repository.QueryActivityPage(ctx, query)
	if err != nil {
		return ActivityList{}, err
	}
	data, hasMore := boundedPage(page.Data, request.Filter.Limit)
	nextCursor, err := eventNextCursor(
		data,
		hasMore,
		page.Snapshot,
		"activity",
		fingerprint,
	)
	if err != nil {
		return ActivityList{}, err
	}
	return ActivityList{
		SchemaVersion: SchemaVersion,
		Data:          nonNil(data),
		NextCursor:    nextCursor,
		HasMore:       hasMore,
		ReturnedCount: len(data),
		Limit:         request.Filter.Limit,
		DataThrough:   page.DataThrough,
	}, nil
}

func (s *Service) ListFindings(ctx context.Context, limit int) (FindingList, error) {
	return s.ListFindingsPage(ctx, FindingListRequest{Limit: limit})
}

func (s *Service) ListFindingsPage(
	ctx context.Context,
	request FindingListRequest,
) (FindingList, error) {
	request.Limit = boundedLimit(request.Limit, defaultFindingLimit, maxFindingLimit)
	request.Severity = strings.ToLower(strings.TrimSpace(request.Severity))
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.Since = utcTime(request.Since)
	if err := validateFindingRequest(request); err != nil {
		return FindingList{}, err
	}
	fingerprint := stableFingerprint(struct {
		Since     string `json:"since"`
		Severity  string `json:"severity"`
		SessionID string `json:"session_id"`
	}{
		Since:     timeString(request.Since),
		Severity:  request.Severity,
		SessionID: request.SessionID,
	})
	query := model.FindingQuery{
		Filter: model.FindingFilter{
			DetectedAfter: request.Since,
			Severity:      request.Severity,
			SessionID:     request.SessionID,
			Limit:         request.Limit + 1,
		},
	}
	if request.Cursor != "" {
		cursor, err := decodeCursor(request.Cursor, "findings", fingerprint)
		if err != nil {
			return FindingList{}, err
		}
		cursorTime, _ := time.Parse(time.RFC3339Nano, cursor.Time)
		query.Snapshot = cursor.Snapshot
		query.Cursor = &model.FindingPosition{
			DetectedAt: cursorTime,
			FindingID:  cursor.ID,
		}
	}
	page, err := s.repository.QueryFindings(ctx, query)
	if err != nil {
		return FindingList{}, err
	}
	data, hasMore := boundedPage(page.Data, request.Limit)
	var nextCursor *string
	if hasMore {
		last := data[len(data)-1]
		encoded, err := encodeCursor(cursorEnvelope{
			Version:     cursorVersion,
			Kind:        "findings",
			Snapshot:    page.Snapshot,
			Fingerprint: fingerprint,
			Time:        last.DetectedAt.UTC().Format(time.RFC3339Nano),
			ID:          last.FindingID,
		})
		if err != nil {
			return FindingList{}, err
		}
		nextCursor = &encoded
	}
	return FindingList{
		SchemaVersion: SchemaVersion,
		Data:          nonNil(data),
		NextCursor:    nextCursor,
		HasMore:       hasMore,
		ReturnedCount: len(data),
		Limit:         request.Limit,
		DataThrough:   page.DataThrough,
	}, nil
}

func (s *Service) ListIssues(
	ctx context.Context,
	request IssueListRequest,
) (IssueList, error) {
	if err := s.RequireIssueEvidenceCapabilities(); err != nil {
		return IssueList{}, err
	}
	var cursor *issueCursorEnvelope
	if strings.TrimSpace(request.Cursor) != "" {
		if request.Limit != 0 ||
			request.Severity != "" ||
			request.Category != "" ||
			request.Harness != "" ||
			request.Origin != "" ||
			request.AnalysisStatus != "" ||
			request.ObservedAfter != nil ||
			request.Recurrence != "" ||
			request.SessionID != "" ||
			request.FingerprintID != "" ||
			request.AttentionKind != "" ||
			request.Experimental != "" {
			return IssueList{}, invalidRequest("cursor continuation accepts only cursor")
		}
		decoded, err := s.openIssueCursor(request.Cursor, "issues")
		if err != nil {
			return IssueList{}, err
		}
		if decoded.Kind != "issues" ||
			decoded.IssueID != "" ||
			decoded.PositionID == "" ||
			!issueIDPattern.MatchString(decoded.PositionID) ||
			decoded.SeverityRank < 1 ||
			decoded.SeverityRank > 5 ||
			decoded.Time == "" {
			return IssueList{}, invalidCursor()
		}
		if err := validateIssueCursorSnapshot(decoded, s.nowUTC()); err != nil {
			return IssueList{}, err
		}
		if _, err := validateIssueCursorPositionTime(decoded.Time); err != nil {
			return IssueList{}, err
		}
		recovered, err := issueListRequestFromCursor(decoded)
		if err != nil {
			return IssueList{}, err
		}
		request = recovered
		cursor = &decoded
	} else {
		if request.Limit < 0 || request.Limit > maxIssueLimit {
			return IssueList{}, invalidRequest("limit must be between one and 100")
		}
		request = normalizeIssueListRequest(request)
		if err := validateIssueListRequest(request); err != nil {
			return IssueList{}, err
		}
	}

	query := model.IssueQuery{
		Filter: model.IssueFilter{
			Severity:       request.Severity,
			Category:       request.Category,
			Harness:        request.Harness,
			Origin:         request.Origin,
			AnalysisStatus: model.AnalysisStatus(request.AnalysisStatus),
			ObservedAfter:  request.ObservedAfter,
			Recurrence:     request.Recurrence,
			SessionID:      request.SessionID,
			FingerprintID:  request.FingerprintID,
			AttentionKind:  request.AttentionKind,
			Experimental:   request.Experimental,
		},
		Limit: request.Limit,
	}
	if cursor != nil {
		cursorTime, _ := validateIssueCursorPositionTime(cursor.Time)
		issuedAt, _ := time.Parse(time.RFC3339Nano, cursor.IssuedAt)
		query.CursorEpoch = cursor.CursorEpoch
		query.Snapshot = cursor.Snapshot
		query.RetentionGeneration = cursor.RetentionGeneration
		query.IssuedAt = issuedAt
		query.Cursor = &model.IssuePosition{
			SeverityRank: cursor.SeverityRank,
			Repeated:     cursor.Repeated,
			LastObserved: cursorTime,
			IssueID:      cursor.PositionID,
		}
	}

	page, err := s.issueRepository.QueryIssues(ctx, query)
	if err != nil {
		return IssueList{}, mapIssueRepositoryError(err)
	}
	if err := validateIssuePageSnapshot(
		page.CursorEpoch,
		page.Snapshot,
		page.RetentionGeneration,
		page.IssuedAt,
	); err != nil {
		return IssueList{}, err
	}
	if cursor != nil && !sameIssueSnapshot(
		page.CursorEpoch,
		page.Snapshot,
		page.RetentionGeneration,
		page.IssuedAt,
		cursor.CursorEpoch,
		cursor.Snapshot,
		cursor.RetentionGeneration,
		query.IssuedAt,
	) {
		return IssueList{}, errors.New("issue repository changed snapshots during list continuation")
	}
	data, hasMore := boundedIssuePage(page.Data, request.Limit, page.HasMore)
	normalizeIssueSummaries(data)
	nextCursor, err := s.issueNextCursor(
		data,
		hasMore,
		request,
		page,
	)
	if err != nil {
		return IssueList{}, err
	}
	viewCursor, err := s.sealIssueCursor(issueCursorEnvelope{
		Version:             issueCursorVersion,
		Kind:                "issue_view",
		CursorEpoch:         page.CursorEpoch,
		Snapshot:            page.Snapshot,
		RetentionGeneration: page.RetentionGeneration,
		IssuedAt:            page.IssuedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return IssueList{}, err
	}
	return IssueList{
		SchemaVersion:     SchemaVersion,
		ProjectionVersion: IssueProjectionVersion,
		Data:              nonNil(data),
		Analysis:          page.Analysis,
		Selection:         normalizedIssueSelection(request),
		ViewCursor:        viewCursor,
		NextCursor:        nextCursor,
		HasMore:           hasMore,
		ReturnedCount:     len(data),
		Limit:             request.Limit,
	}, nil
}

func (s *Service) GetIssue(
	ctx context.Context,
	request IssueDetailRequest,
) (IssueDetail, error) {
	if err := s.RequireIssueEvidenceCapabilities(); err != nil {
		return IssueDetail{}, err
	}
	request.IssueID = strings.ToLower(strings.TrimSpace(request.IssueID))
	request.Cursor = strings.TrimSpace(request.Cursor)
	request.ViewCursor = strings.TrimSpace(request.ViewCursor)
	if !issueIDPattern.MatchString(request.IssueID) {
		return IssueDetail{}, invalidRequest("issue_id is malformed")
	}
	if request.Cursor != "" && request.ViewCursor != "" {
		return IssueDetail{}, invalidRequest("cursor and view_cursor are mutually exclusive")
	}
	if request.Cursor != "" && request.Limit != 0 {
		return IssueDetail{}, invalidRequest("cursor continuation accepts no limit")
	}
	if request.Limit < 0 || request.Limit > maxIssueLimit {
		return IssueDetail{}, invalidRequest("limit must be between one and 100")
	}

	limit := request.Limit
	if limit == 0 {
		limit = defaultIssueLimit
	}
	cursorEpoch := ""
	snapshot := int64(0)
	retentionGeneration := int64(0)
	issuedAt := time.Time{}
	var position *model.IssueOccurrencePosition
	if request.Cursor != "" {
		cursor, err := s.openIssueCursor(request.Cursor, "issue_occurrences")
		if err != nil {
			return IssueDetail{}, err
		}
		if cursor.Kind != "issue_occurrences" ||
			cursor.Filters != nil ||
			cursor.IssueID != request.IssueID ||
			cursor.Limit < 1 ||
			cursor.Limit > maxIssueLimit ||
			cursor.PositionID == "" ||
			len(cursor.PositionID) > 256 ||
			cursor.Time == "" ||
			cursor.SeverityRank != 0 ||
			cursor.Repeated {
			return IssueDetail{}, invalidCursor()
		}
		if err := validateIssueCursorSnapshot(cursor, s.nowUTC()); err != nil {
			return IssueDetail{}, err
		}
		cursorTime, err := validateIssueCursorPositionTime(cursor.Time)
		if err != nil {
			return IssueDetail{}, err
		}
		issuedAt, _ = time.Parse(time.RFC3339Nano, cursor.IssuedAt)
		cursorEpoch = cursor.CursorEpoch
		snapshot = cursor.Snapshot
		retentionGeneration = cursor.RetentionGeneration
		limit = cursor.Limit
		position = &model.IssueOccurrencePosition{
			LastObserved: cursorTime,
			OccurrenceID: cursor.PositionID,
		}
	} else if request.ViewCursor != "" {
		cursor, err := s.openIssueCursor(request.ViewCursor, "issue_view")
		if err != nil {
			return IssueDetail{}, err
		}
		if cursor.Kind != "issue_view" ||
			cursor.Filters != nil ||
			cursor.Limit != 0 ||
			cursor.IssueID != "" ||
			cursor.Time != "" ||
			cursor.PositionID != "" ||
			cursor.SeverityRank != 0 ||
			cursor.Repeated {
			return IssueDetail{}, invalidCursor()
		}
		if err := validateIssueCursorSnapshot(cursor, s.nowUTC()); err != nil {
			return IssueDetail{}, err
		}
		issuedAt, _ = time.Parse(time.RFC3339Nano, cursor.IssuedAt)
		cursorEpoch = cursor.CursorEpoch
		snapshot = cursor.Snapshot
		retentionGeneration = cursor.RetentionGeneration
	}

	summaryPage, err := s.issueRepository.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			IssueID:       request.IssueID,
			AttentionKind: model.AttentionKindAll,
			Experimental:  model.ExperimentalInclude,
		},
		Limit:               1,
		CursorEpoch:         cursorEpoch,
		Snapshot:            snapshot,
		RetentionGeneration: retentionGeneration,
		IssuedAt:            issuedAt,
	})
	if err != nil {
		return IssueDetail{}, mapIssueRepositoryError(err)
	}
	if err := validateIssuePageSnapshot(
		summaryPage.CursorEpoch,
		summaryPage.Snapshot,
		summaryPage.RetentionGeneration,
		summaryPage.IssuedAt,
	); err != nil {
		return IssueDetail{}, err
	}
	if snapshot != 0 && !sameIssueSnapshot(
		summaryPage.CursorEpoch,
		summaryPage.Snapshot,
		summaryPage.RetentionGeneration,
		summaryPage.IssuedAt,
		cursorEpoch,
		snapshot,
		retentionGeneration,
		issuedAt,
	) {
		return IssueDetail{}, errors.New("issue repository changed snapshots during detail read")
	}
	if len(summaryPage.Data) == 0 {
		return IssueDetail{}, notFound()
	}
	if summaryPage.Data[0].IssueID != request.IssueID {
		return IssueDetail{}, errors.New("issue repository returned an inconsistent summary")
	}
	summary := summaryPage.Data[0]
	normalizeIssueSummary(&summary)

	occurrencePage, err := s.issueRepository.QueryIssueOccurrences(
		ctx,
		model.IssueOccurrenceQuery{
			IssueID:             request.IssueID,
			Limit:               limit,
			CursorEpoch:         summaryPage.CursorEpoch,
			Snapshot:            summaryPage.Snapshot,
			RetentionGeneration: summaryPage.RetentionGeneration,
			IssuedAt:            summaryPage.IssuedAt,
			Cursor:              position,
		},
	)
	if err != nil {
		return IssueDetail{}, mapIssueRepositoryError(err)
	}
	if !sameIssueSnapshot(
		occurrencePage.CursorEpoch,
		occurrencePage.Snapshot,
		occurrencePage.RetentionGeneration,
		occurrencePage.IssuedAt,
		summaryPage.CursorEpoch,
		summaryPage.Snapshot,
		summaryPage.RetentionGeneration,
		summaryPage.IssuedAt,
	) {
		return IssueDetail{}, errors.New("issue repository changed snapshots during detail read")
	}
	occurrences, hasMore := boundedOccurrencePage(
		occurrencePage.Data,
		limit,
		occurrencePage.HasMore,
	)
	normalizeIssueOccurrences(occurrences)
	nextCursor, err := s.occurrenceNextCursor(
		occurrences,
		hasMore,
		request.IssueID,
		limit,
		summaryPage,
	)
	if err != nil {
		return IssueDetail{}, err
	}
	viewCursor, err := s.sealIssueCursor(issueCursorEnvelope{
		Version:             issueCursorVersion,
		Kind:                "issue_view",
		CursorEpoch:         summaryPage.CursorEpoch,
		Snapshot:            summaryPage.Snapshot,
		RetentionGeneration: summaryPage.RetentionGeneration,
		IssuedAt:            summaryPage.IssuedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return IssueDetail{}, err
	}
	return IssueDetail{
		SchemaVersion:     SchemaVersion,
		ProjectionVersion: IssueProjectionVersion,
		Data: IssueDetailData{
			Issue:       summary,
			Occurrences: nonNil(occurrences),
		},
		Catalog:                issueCatalog(summary),
		GlobalAnalysisCoverage: summaryPage.Analysis,
		ViewCursor:             viewCursor,
		NextCursor:             nextCursor,
		HasMore:                hasMore,
		ReturnedCount:          len(occurrences),
		Limit:                  limit,
	}, nil
}

func (s *Service) LookupSessionEvents(
	ctx context.Context,
	request EventLookupRequest,
) (EventLookup, error) {
	sessionID, eventIDs, err := normalizeEventLookupRequest(
		request.SessionID,
		request.EventIDs,
	)
	if err != nil {
		return EventLookup{}, err
	}
	if s.issueRepository == nil {
		return EventLookup{}, capabilityUnavailable()
	}
	result, err := s.issueRepository.LookupSessionEvents(
		ctx,
		model.EventLookupQuery{
			SessionID: sessionID,
			EventIDs:  eventIDs,
		},
	)
	if err != nil {
		return EventLookup{}, err
	}
	return EventLookup{
		SchemaVersion:   SchemaVersion,
		Data:            nonNil(result.Data),
		RequestedCount:  result.RequestedCount,
		FoundCount:      result.FoundCount,
		MissingCount:    result.MissingCount,
		MissingEventIDs: nonNil(result.MissingEventIDs),
		DataThrough:     result.DataThrough,
	}, nil
}

func (s *Service) GetStats(ctx context.Context) (StatsResponse, error) {
	data, dataThrough, err := s.repository.GetStats(ctx)
	response := StatsResponse{
		SchemaVersion: SchemaVersion,
		MetricVersion: "belay.metrics.local.v1",
		Data:          data,
		DataThrough:   dataThrough,
	}
	if err != nil || s.initializationProvider == nil {
		return response, err
	}
	status := s.initializationProvider.InitializationStatus()
	if !initialization.Valid(status) {
		return StatsResponse{}, errors.New("initialization provider returned invalid status")
	}
	response.Initialization = &status
	return response, nil
}

func (s *Service) GetInitialization() (InitializationResponse, error) {
	if s == nil || s.initializationProvider == nil {
		return InitializationResponse{}, capabilityUnavailable()
	}
	status := s.initializationProvider.InitializationStatus()
	if !initialization.Valid(status) {
		return InitializationResponse{}, errors.New("initialization provider returned invalid status")
	}
	return InitializationResponse{
		SchemaVersion:  initialization.SchemaVersion,
		Initialization: status,
	}, nil
}

func sessionNextCursor(
	data []model.SessionSummary,
	hasMore bool,
	snapshot int64,
	fingerprint string,
) (*string, error) {
	if !hasMore {
		return nil, nil
	}
	last := data[len(data)-1]
	encoded, err := encodeCursor(cursorEnvelope{
		Version:     cursorVersion,
		Kind:        "sessions",
		Snapshot:    snapshot,
		Fingerprint: fingerprint,
		Time:        last.EndedAt.UTC().Format(time.RFC3339Nano),
		ID:          last.SessionID,
	})
	if err != nil {
		return nil, err
	}
	return &encoded, nil
}

func eventNextCursor(
	data []model.Event,
	hasMore bool,
	snapshot int64,
	kind string,
	fingerprint string,
) (*string, error) {
	if !hasMore {
		return nil, nil
	}
	last := data[len(data)-1]
	encoded, err := encodeCursor(cursorEnvelope{
		Version:     cursorVersion,
		Kind:        kind,
		Snapshot:    snapshot,
		Fingerprint: fingerprint,
		Time:        last.OccurredAt.UTC().Format(time.RFC3339Nano),
		Sequence:    last.Source.Sequence,
		ID:          last.EventID,
	})
	if err != nil {
		return nil, err
	}
	return &encoded, nil
}

func normalizeIssueListRequest(request IssueListRequest) IssueListRequest {
	if request.Limit == 0 {
		request.Limit = defaultIssueLimit
	}
	request.Cursor = strings.TrimSpace(request.Cursor)
	request.Severity = strings.ToLower(strings.TrimSpace(request.Severity))
	request.Category = strings.ToLower(strings.TrimSpace(request.Category))
	request.Harness = strings.TrimSpace(request.Harness)
	request.Origin = strings.ToLower(strings.TrimSpace(request.Origin))
	request.AnalysisStatus = strings.ToLower(strings.TrimSpace(request.AnalysisStatus))
	request.ObservedAfter = utcTime(request.ObservedAfter)
	request.Recurrence = strings.ToLower(strings.TrimSpace(request.Recurrence))
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.FingerprintID = strings.ToLower(strings.TrimSpace(request.FingerprintID))
	request.AttentionKind = strings.ToLower(strings.TrimSpace(request.AttentionKind))
	request.Experimental = strings.ToLower(strings.TrimSpace(request.Experimental))
	if request.AttentionKind == "" {
		request.AttentionKind = model.AttentionKindIssue
	}
	if request.Experimental == "" {
		request.Experimental = model.ExperimentalStable
	}
	return request
}

func validateIssueListRequest(request IssueListRequest) error {
	switch request.Severity {
	case "", "info", "low", "medium", "high", "critical":
	default:
		return invalidRequest("severity is unsupported")
	}
	if len(request.Severity) > 16 {
		return invalidRequest("severity must not exceed 16 bytes")
	}
	if request.Category != "" && !categoryPattern.MatchString(request.Category) {
		return invalidRequest("category is malformed")
	}
	if len(request.Harness) > 128 {
		return invalidRequest("harness must not exceed 128 bytes")
	}
	switch request.Origin {
	case "", "belay", "numbat":
	default:
		return invalidRequest("origin is unsupported")
	}
	switch request.AnalysisStatus {
	case "", string(model.AnalysisCurrent), string(model.AnalysisPending),
		string(model.AnalysisFailed), string(model.AnalysisTruncated):
	default:
		return invalidRequest("analysis_status is unsupported")
	}
	switch request.Recurrence {
	case "", "single", "repeated":
	default:
		return invalidRequest("recurrence is unsupported")
	}
	if len(request.SessionID) > 256 {
		return invalidRequest("session_id must not exceed 256 bytes")
	}
	if request.FingerprintID != "" &&
		!fingerprintIDPattern.MatchString(request.FingerprintID) {
		return invalidRequest("fingerprint_id is malformed")
	}
	switch request.AttentionKind {
	case model.AttentionKindIssue, model.AttentionKindEvidenceGap,
		model.AttentionKindAll:
	default:
		return invalidRequest("attention_kind is unsupported")
	}
	switch request.Experimental {
	case model.ExperimentalStable, model.ExperimentalInclude,
		model.ExperimentalOnly:
	default:
		return invalidRequest("experimental is unsupported")
	}
	return nil
}

func severityRank(severity string) int {
	switch strings.ToLower(severity) {
	case "info":
		return 1
	case "low":
		return 2
	case "medium":
		return 3
	case "high":
		return 4
	case "critical":
		return 5
	default:
		return 0
	}
}

func boundedIssuePage(
	data []model.IssueSummary,
	limit int,
	repositoryHasMore bool,
) ([]model.IssueSummary, bool) {
	if len(data) > limit {
		return data[:limit], true
	}
	return data, repositoryHasMore
}

func boundedOccurrencePage(
	data []model.IssueOccurrence,
	limit int,
	repositoryHasMore bool,
) ([]model.IssueOccurrence, bool) {
	if len(data) > limit {
		return data[:limit], true
	}
	return data, repositoryHasMore
}

func normalizeIssueSummaries(data []model.IssueSummary) {
	for index := range data {
		normalizeIssueSummary(&data[index])
	}
}

func normalizeIssueSummary(summary *model.IssueSummary) {
	summary.Harnesses = nonNil(summary.Harnesses)
}

func normalizeIssueOccurrences(data []model.IssueOccurrence) {
	for index := range data {
		data[index].Evidence.CitedEventIDs = nonNil(data[index].Evidence.CitedEventIDs)
		data[index].Evidence.Dimensions = nonNil(data[index].Evidence.Dimensions)
	}
}

func validateSessionRequest(request SessionListRequest) error {
	if len(request.Harness) > 128 {
		return invalidRequest("harness must not exceed 128 bytes")
	}
	if len(request.Query) > 128 {
		return invalidRequest("query must not exceed 128 bytes")
	}
	if request.Outcome != "" && !validProjectionOutcome(request.Outcome) {
		return invalidRequest("outcome must be incomplete, succeeded, failed, interrupted, or unknown")
	}
	if request.History != "" &&
		request.History != "historical" &&
		request.History != "live" &&
		request.History != "mixed" {
		return invalidRequest("history must be historical, live, or mixed")
	}
	return validateWindow(request.OccurredAfter, request.OccurredBefore)
}

func validateActivityRequest(request ActivityRequest) error {
	if len(request.Filter.Harness) > 128 {
		return invalidRequest("harness must not exceed 128 bytes")
	}
	if len(request.Filter.ResourceKind) > 64 {
		return invalidRequest("resource_kind must not exceed 64 bytes")
	}
	if request.Filter.Outcome != "" && !validEventOutcome(request.Filter.Outcome) {
		return invalidRequest("outcome must be succeeded, failed, interrupted, or unknown")
	}
	return validateWindow(request.Filter.OccurredAfter, request.Filter.OccurredBefore)
}

func validateFindingRequest(request FindingListRequest) error {
	if len(request.Severity) > 32 {
		return invalidRequest("severity must not exceed 32 bytes")
	}
	if len(request.SessionID) > 256 {
		return invalidRequest("session_id must not exceed 256 bytes")
	}
	return nil
}

func validProjectionOutcome(value string) bool {
	switch value {
	case "incomplete", "succeeded", "failed", "interrupted", "unknown":
		return true
	default:
		return false
	}
}

func validEventOutcome(value string) bool {
	switch value {
	case "succeeded", "failed", "interrupted", "unknown":
		return true
	default:
		return false
	}
}

func validateWindow(after, before *time.Time) error {
	if after != nil && before != nil && after.After(*before) {
		return invalidRequest("occurred_after must not be after occurred_before")
	}
	return nil
}

func sessionFingerprint(request SessionListRequest) string {
	return stableFingerprint(struct {
		Harness        string `json:"harness"`
		Outcome        string `json:"outcome"`
		History        string `json:"history"`
		OccurredAfter  string `json:"occurred_after"`
		OccurredBefore string `json:"occurred_before"`
		Query          string `json:"query"`
	}{
		Harness:        strings.ToLower(request.Harness),
		Outcome:        request.Outcome,
		History:        request.History,
		OccurredAfter:  timeString(request.OccurredAfter),
		OccurredBefore: timeString(request.OccurredBefore),
		Query:          strings.ToLower(request.Query),
	})
}

func activityFingerprint(filter model.ActivityFilter) string {
	return stableFingerprint(struct {
		OccurredAfter  string `json:"occurred_after"`
		OccurredBefore string `json:"occurred_before"`
		Harness        string `json:"harness"`
		ResourceKind   string `json:"resource_kind"`
		Outcome        string `json:"outcome"`
	}{
		OccurredAfter:  timeString(filter.OccurredAfter),
		OccurredBefore: timeString(filter.OccurredBefore),
		Harness:        strings.ToLower(filter.Harness),
		ResourceKind:   filter.ResourceKind,
		Outcome:        filter.Outcome,
	})
}

func stableFingerprint(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func encodeCursor(cursor cursorEnvelope) (string, error) {
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", errors.New("encode read cursor")
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeCursor(value, kind, fingerprint string) (cursorEnvelope, error) {
	cursor, err := decodeCursorEnvelope(value)
	if err != nil {
		return cursorEnvelope{}, err
	}
	if cursor.IssuedAt != "" ||
		cursor.SeverityRank != 0 ||
		cursor.Repeated {
		return cursorEnvelope{}, invalidCursor()
	}
	if cursor.Version != cursorVersion ||
		cursor.Kind != kind ||
		cursor.Snapshot <= 0 ||
		cursor.Fingerprint != fingerprint ||
		cursor.ID == "" ||
		len(cursor.ID) > 256 {
		return cursorEnvelope{}, invalidCursor()
	}
	if _, err := parseUTCTime(cursor.Time); err != nil {
		return cursorEnvelope{}, invalidCursor()
	}
	return cursor, nil
}

func decodeCursorEnvelope(value string) (cursorEnvelope, error) {
	if value == "" || len(value) > maxCursorBytes {
		return cursorEnvelope{}, invalidCursor()
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(body) == 0 || len(body) > maxCursorBytes {
		return cursorEnvelope{}, invalidCursor()
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var cursor cursorEnvelope
	if err := decoder.Decode(&cursor); err != nil {
		return cursorEnvelope{}, invalidCursor()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return cursorEnvelope{}, invalidCursor()
	}
	return cursor, nil
}

func parseUTCTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC {
		return time.Time{}, invalidCursor()
	}
	return parsed, nil
}

func validateCursorIssuedAt(value string, now time.Time) error {
	issuedAt, err := parseUTCTime(value)
	if err != nil {
		return invalidCursor()
	}
	now = now.UTC()
	if issuedAt.After(now) {
		return invalidCursor()
	}
	if now.Sub(issuedAt) > issueCursorLifetime {
		return cursorExpired()
	}
	return nil
}

func invalidCursor() error {
	return fmt.Errorf("%w: opaque cursor is malformed or does not match the request", ErrInvalidCursor)
}

func invalidRequest(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, message)
}

func cursorExpired() error {
	return fmt.Errorf("%w: opaque cursor lifetime elapsed", ErrCursorExpired)
}

func capabilityUnavailable() error {
	return fmt.Errorf(
		"%w: issue repository and authenticated issue cursor codec are required",
		ErrCapabilityUnavailable,
	)
}

func notFound() error {
	return fmt.Errorf("%w: requested issue is absent", ErrNotFound)
}

func mapIssueRepositoryError(err error) error {
	switch {
	case errors.Is(err, model.ErrIssueSnapshotExpired):
		return cursorExpired()
	case errors.Is(err, model.ErrIssueSnapshotInvalid):
		return invalidCursor()
	default:
		return err
	}
}

func boundedLimit(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func boundedPage[T any](data []T, limit int) ([]T, bool) {
	if len(data) <= limit {
		return data, false
	}
	return data[:limit], true
}

func nonNil[T any](data []T) []T {
	if data == nil {
		return []T{}
	}
	return data
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}

func timeString(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (s *Service) nowUTC() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}
