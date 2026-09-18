package readmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const issueCursorVersion = 2

type IssueCursorCodec interface {
	SealIssueCursor([]byte) (string, error)
	OpenIssueCursor(string) ([]byte, error)
}

type issueCursorFilters struct {
	Severity       string `json:"severity,omitempty"`
	Category       string `json:"category,omitempty"`
	Harness        string `json:"harness,omitempty"`
	Origin         string `json:"origin,omitempty"`
	AnalysisStatus string `json:"analysis_status,omitempty"`
	ObservedAfter  string `json:"observed_after,omitempty"`
	Recurrence     string `json:"recurrence,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	FingerprintID  string `json:"fingerprint_id,omitempty"`
	AttentionKind  string `json:"attention_kind"`
	Experimental   string `json:"experimental"`
}

type issueCursorEnvelope struct {
	Version             int                 `json:"v"`
	Kind                string              `json:"k"`
	CursorEpoch         string              `json:"e"`
	Snapshot            int64               `json:"s"`
	RetentionGeneration int64               `json:"g"`
	IssuedAt            string              `json:"a"`
	Limit               int                 `json:"l,omitempty"`
	Filters             *issueCursorFilters `json:"f,omitempty"`
	IssueID             string              `json:"i,omitempty"`
	Time                string              `json:"t,omitempty"`
	PositionID          string              `json:"p,omitempty"`
	SeverityRank        int                 `json:"r,omitempty"`
	Repeated            bool                `json:"d,omitempty"`
}

func (s *Service) sealIssueCursor(cursor issueCursorEnvelope) (string, error) {
	if s == nil || s.issueCursorCodec == nil {
		return "", capabilityUnavailable()
	}
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", errors.New("encode issue cursor")
	}
	value, err := s.issueCursorCodec.SealIssueCursor(body)
	if err != nil {
		if errors.Is(err, model.ErrIssueCursorInvalid) {
			return "", invalidCursor()
		}
		return "", err
	}
	return value, nil
}

func (s *Service) openIssueCursor(
	value string,
	allowedLegacyKinds ...string,
) (issueCursorEnvelope, error) {
	value = strings.TrimSpace(value)
	if isLegacyIssueCursor(value, allowedLegacyKinds...) {
		return issueCursorEnvelope{}, cursorExpired()
	}
	if s == nil || s.issueCursorCodec == nil {
		return issueCursorEnvelope{}, capabilityUnavailable()
	}
	body, err := s.issueCursorCodec.OpenIssueCursor(value)
	if err != nil {
		if errors.Is(err, model.ErrIssueCursorInvalid) {
			return issueCursorEnvelope{}, invalidCursor()
		}
		return issueCursorEnvelope{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var cursor issueCursorEnvelope
	if err := decoder.Decode(&cursor); err != nil {
		return issueCursorEnvelope{}, invalidCursor()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return issueCursorEnvelope{}, invalidCursor()
	}
	if cursor.Version != issueCursorVersion {
		return issueCursorEnvelope{}, invalidCursor()
	}
	return cursor, nil
}

func isLegacyIssueCursor(value string, allowedKinds ...string) bool {
	cursor, err := decodeCursorEnvelope(value)
	if err != nil || cursor.Version != cursorVersion {
		return false
	}
	for _, kind := range allowedKinds {
		if cursor.Kind == kind {
			return true
		}
	}
	return false
}

func issueCursorFiltersFromRequest(request IssueListRequest) issueCursorFilters {
	return issueCursorFilters{
		Severity:       request.Severity,
		Category:       request.Category,
		Harness:        request.Harness,
		Origin:         request.Origin,
		AnalysisStatus: request.AnalysisStatus,
		ObservedAfter:  timeString(request.ObservedAfter),
		Recurrence:     request.Recurrence,
		SessionID:      request.SessionID,
		FingerprintID:  request.FingerprintID,
		AttentionKind:  request.AttentionKind,
		Experimental:   request.Experimental,
	}
}

func issueListRequestFromCursor(cursor issueCursorEnvelope) (IssueListRequest, error) {
	if cursor.Filters == nil {
		return IssueListRequest{}, invalidCursor()
	}
	request := IssueListRequest{
		Limit:          cursor.Limit,
		Severity:       cursor.Filters.Severity,
		Category:       cursor.Filters.Category,
		Harness:        cursor.Filters.Harness,
		Origin:         cursor.Filters.Origin,
		AnalysisStatus: cursor.Filters.AnalysisStatus,
		Recurrence:     cursor.Filters.Recurrence,
		SessionID:      cursor.Filters.SessionID,
		FingerprintID:  cursor.Filters.FingerprintID,
		AttentionKind:  cursor.Filters.AttentionKind,
		Experimental:   cursor.Filters.Experimental,
	}
	if cursor.Filters.ObservedAfter != "" {
		parsed, err := parseUTCTime(cursor.Filters.ObservedAfter)
		if err != nil {
			return IssueListRequest{}, invalidCursor()
		}
		request.ObservedAfter = &parsed
	}
	if request.Limit < 1 || request.Limit > maxIssueLimit {
		return IssueListRequest{}, invalidCursor()
	}
	if err := validateIssueListRequest(request); err != nil {
		return IssueListRequest{}, invalidCursor()
	}
	return request, nil
}

func validateIssueCursorSnapshot(cursor issueCursorEnvelope, now time.Time) error {
	if cursor.CursorEpoch == "" ||
		cursor.Snapshot <= 0 ||
		cursor.RetentionGeneration < 1 {
		return invalidCursor()
	}
	return validateCursorIssuedAt(cursor.IssuedAt, now)
}

func validateIssueCursorPositionTime(value string) (time.Time, error) {
	parsed, err := parseUTCTime(value)
	if err != nil {
		return time.Time{}, invalidCursor()
	}
	return parsed, nil
}

func validateIssuePageSnapshot(
	epoch string,
	snapshot int64,
	retentionGeneration int64,
	issuedAt time.Time,
) error {
	if epoch == "" ||
		snapshot <= 0 ||
		retentionGeneration < 1 ||
		issuedAt.IsZero() ||
		issuedAt.Location() != time.UTC {
		return errors.New("issue repository returned invalid snapshot metadata")
	}
	return nil
}

func sameIssueSnapshot(
	epoch string,
	snapshot int64,
	retentionGeneration int64,
	issuedAt time.Time,
	expectedEpoch string,
	expectedSnapshot int64,
	expectedRetentionGeneration int64,
	expectedIssuedAt time.Time,
) bool {
	return epoch == expectedEpoch &&
		snapshot == expectedSnapshot &&
		retentionGeneration == expectedRetentionGeneration &&
		issuedAt.Equal(expectedIssuedAt)
}

func (s *Service) issueNextCursor(
	data []model.IssueSummary,
	hasMore bool,
	request IssueListRequest,
	page model.IssuePage,
) (*string, error) {
	if !hasMore {
		return nil, nil
	}
	if len(data) == 0 {
		return nil, errors.New("issue repository reported an unusable continuation")
	}
	last := data[len(data)-1]
	rank := severityRank(last.Severity)
	if !issueIDPattern.MatchString(last.IssueID) ||
		rank == 0 ||
		last.LastObservedAt.IsZero() {
		return nil, errors.New("issue repository returned an invalid position")
	}
	filters := issueCursorFiltersFromRequest(request)
	encoded, err := s.sealIssueCursor(issueCursorEnvelope{
		Version:             issueCursorVersion,
		Kind:                "issues",
		CursorEpoch:         page.CursorEpoch,
		Snapshot:            page.Snapshot,
		RetentionGeneration: page.RetentionGeneration,
		IssuedAt:            page.IssuedAt.UTC().Format(time.RFC3339Nano),
		Limit:               request.Limit,
		Filters:             &filters,
		Time:                last.LastObservedAt.UTC().Format(time.RFC3339Nano),
		PositionID:          last.IssueID,
		SeverityRank:        rank,
		Repeated:            last.SessionCount >= 2,
	})
	if err != nil {
		return nil, err
	}
	return &encoded, nil
}

func (s *Service) occurrenceNextCursor(
	data []model.IssueOccurrence,
	hasMore bool,
	issueID string,
	limit int,
	page model.IssuePage,
) (*string, error) {
	if !hasMore {
		return nil, nil
	}
	if len(data) == 0 {
		return nil, errors.New("issue repository reported an unusable occurrence continuation")
	}
	last := data[len(data)-1]
	if last.OccurrenceID == "" ||
		len(last.OccurrenceID) > 256 ||
		last.LastObservedAt.IsZero() {
		return nil, errors.New("issue repository returned an invalid occurrence position")
	}
	encoded, err := s.sealIssueCursor(issueCursorEnvelope{
		Version:             issueCursorVersion,
		Kind:                "issue_occurrences",
		CursorEpoch:         page.CursorEpoch,
		Snapshot:            page.Snapshot,
		RetentionGeneration: page.RetentionGeneration,
		IssuedAt:            page.IssuedAt.UTC().Format(time.RFC3339Nano),
		Limit:               limit,
		IssueID:             issueID,
		Time:                last.LastObservedAt.UTC().Format(time.RFC3339Nano),
		PositionID:          last.OccurrenceID,
	})
	if err != nil {
		return nil, err
	}
	return &encoded, nil
}
