package readmodel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const (
	defaultFixMonitoringLimit = 20
	maxFixMonitoringLimit     = 100
)

var (
	fixAnnotationIDPattern = regexp.MustCompile(`^fxa_[a-z2-7]{52}$`)
	fixRecurrenceIDPattern = regexp.MustCompile(`^fxo_[a-z2-7]{52}$`)
)

type FixMonitoringListRequest struct {
	Limit            int
	Cursor           string
	State            string
	ChangeKind       string
	Severity         string
	Harness          string
	RecordedAfter    *time.Time
	IssueID          string
	IncludeRetracted bool
}

type FixMonitoringDetailRequest struct {
	IssueID    string
	Limit      int
	Cursor     string
	ViewCursor string
}

type FixRecurrenceObservationListRequest struct {
	IssueID               string
	AnnotationID          string
	Limit                 int
	Cursor                string
	ObservationViewCursor string
}

type FixMonitoringCoverage struct {
	ComparableCurrent   int        `json:"comparable_current"`
	ComparablePending   int        `json:"comparable_pending"`
	ComparableFailed    int        `json:"comparable_failed"`
	ComparableTruncated int        `json:"comparable_truncated"`
	AnalysisThrough     *time.Time `json:"analysis_through"`
	Complete            bool       `json:"complete"`
}

type FixMonitoringSummary struct {
	IssueID                           string                `json:"issue_id"`
	AnnotationID                      string                `json:"annotation_id"`
	TitleCode                         *string               `json:"title_code"`
	Severity                          *string               `json:"severity"`
	ChangeKind                        model.FixChangeKind   `json:"change_kind"`
	RecordedAt                        time.Time             `json:"recorded_at"`
	MonitorFrom                       time.Time             `json:"monitor_from"`
	FixRecurrenceState                string                `json:"fix_recurrence_state"`
	FixRecurrenceCount                int                   `json:"fix_recurrence_count"`
	SameAnchorSessionObservationCount int                   `json:"same_anchor_session_observation_count"`
	OtherSessionObservationCount      int                   `json:"other_session_observation_count"`
	HistoricalMatchingEvidenceCount   int                   `json:"historical_matching_evidence_count"`
	ActiveAttemptCount                int                   `json:"active_attempt_count"`
	ObservedAttemptCount              int                   `json:"observed_attempt_count"`
	AnalysisComplete                  bool                  `json:"analysis_complete"`
	CountIsLowerBound                 bool                  `json:"count_is_lower_bound"`
	FutureComparisonAvailable         bool                  `json:"future_comparison_available"`
	FutureComparisonUnavailableReason *string               `json:"future_comparison_unavailable_reason"`
	LastRecurrenceObservedAt          *time.Time            `json:"last_recurrence_observed_at"`
	Coverage                          FixMonitoringCoverage `json:"coverage"`
}

type FixMonitoringList struct {
	SchemaVersion        string                 `json:"schema_version"`
	Data                 []FixMonitoringSummary `json:"data"`
	ReturnedCount        int                    `json:"returned_count"`
	Limit                int                    `json:"limit"`
	HasMore              bool                   `json:"has_more"`
	NextCursor           *string                `json:"next_cursor"`
	MonitoringViewCursor string                 `json:"monitoring_view_cursor"`
	EvidenceEvaluatedAt  time.Time              `json:"evidence_evaluated_at"`
}

type FixMonitoringSubject struct {
	TitleCode          *string `json:"title_code"`
	Severity           *string `json:"severity"`
	Confidence         *string `json:"confidence"`
	AnchorHarness      *string `json:"anchor_harness"`
	Origin             string  `json:"origin"`
	DetectorID         string  `json:"detector_id"`
	DetectorVersion    string  `json:"detector_version"`
	FingerprintVersion string  `json:"fingerprint_version"`
}

type FixRecurrenceEvidenceCounts struct {
	Available int `json:"available"`
	Partial   int `json:"partial"`
	Pruned    int `json:"pruned"`
	Unknown   int `json:"unknown"`
}

type FixAttemptMonitoring struct {
	AnnotationID                      string                      `json:"annotation_id"`
	IssueID                           string                      `json:"issue_id"`
	Subject                           FixMonitoringSubject        `json:"subject"`
	ChangeKind                        model.FixChangeKind         `json:"change_kind"`
	RecordedAt                        time.Time                   `json:"recorded_at"`
	MonitorFrom                       time.Time                   `json:"monitor_from"`
	State                             string                      `json:"state"`
	RetractionReason                  *string                     `json:"retraction_reason"`
	RetractedAt                       *time.Time                  `json:"retracted_at"`
	FixRecurrenceState                string                      `json:"fix_recurrence_state"`
	FixRecurrenceCount                int                         `json:"fix_recurrence_count"`
	SameAnchorSessionObservationCount int                         `json:"same_anchor_session_observation_count"`
	OtherSessionObservationCount      int                         `json:"other_session_observation_count"`
	HistoricalMatchingEvidenceCount   int                         `json:"historical_matching_evidence_count"`
	AnalysisComplete                  bool                        `json:"analysis_complete"`
	CountIsLowerBound                 bool                        `json:"count_is_lower_bound"`
	FutureComparisonAvailable         bool                        `json:"future_comparison_available"`
	FutureComparisonUnavailableReason *string                     `json:"future_comparison_unavailable_reason"`
	LastRecurrenceObservedAt          *time.Time                  `json:"last_recurrence_observed_at"`
	Coverage                          FixMonitoringCoverage       `json:"coverage"`
	AnchorEvidenceCurrentlyRetained   string                      `json:"anchor_evidence_currently_retained"`
	RecurrenceEvidence                FixRecurrenceEvidenceCounts `json:"recurrence_evidence"`
	ObservationViewCursor             string                      `json:"observation_view_cursor"`
}

type FixMonitoringDetail struct {
	SchemaVersion         string                 `json:"schema_version"`
	IssueID               string                 `json:"issue_id"`
	CurrentIssueAvailable bool                   `json:"current_issue_available"`
	CurrentIssue          *model.IssueSummary    `json:"current_issue"`
	Data                  []FixAttemptMonitoring `json:"data"`
	ReturnedCount         int                    `json:"returned_count"`
	Limit                 int                    `json:"limit"`
	HasMore               bool                   `json:"has_more"`
	NextCursor            *string                `json:"next_cursor"`
	MonitoringViewCursor  string                 `json:"monitoring_view_cursor"`
	EvidenceEvaluatedAt   time.Time              `json:"evidence_evaluated_at"`
}

type FixRecurrenceObservation struct {
	RecurrenceID              string    `json:"recurrence_id"`
	OccurrenceID              string    `json:"occurrence_id"`
	SessionID                 string    `json:"session_id"`
	FingerprintVersion        string    `json:"fingerprint_version"`
	Origin                    string    `json:"origin"`
	DetectorID                string    `json:"detector_id"`
	DetectorVersion           string    `json:"detector_version"`
	FirstQualifyingEventAt    time.Time `json:"first_qualifying_event_at"`
	LastQualifyingEventAt     time.Time `json:"last_qualifying_event_at"`
	QualifyingCitationCount   int       `json:"qualifying_citation_count"`
	RetainedEventIDs          []string  `json:"retained_event_ids"`
	RetainedEventCount        *int      `json:"retained_event_count"`
	MissingEventCount         *int      `json:"missing_event_count"`
	EvidenceComplete          bool      `json:"evidence_complete"`
	EvidenceTruncated         *bool     `json:"evidence_truncated"`
	EvidenceCurrentlyRetained string    `json:"evidence_currently_retained"`
	SameSessionAsAnchor       bool      `json:"same_session_as_anchor"`
	ObservedAt                time.Time `json:"observed_at"`
}

type FixRecurrenceObservationList struct {
	SchemaVersion       string                     `json:"schema_version"`
	IssueID             string                     `json:"issue_id"`
	AnnotationID        string                     `json:"annotation_id"`
	Data                []FixRecurrenceObservation `json:"data"`
	ReturnedCount       int                        `json:"returned_count"`
	Limit               int                        `json:"limit"`
	HasMore             bool                       `json:"has_more"`
	NextCursor          *string                    `json:"next_cursor"`
	EvidenceEvaluatedAt time.Time                  `json:"evidence_evaluated_at"`
}

type fixMonitoringCursorFilter struct {
	State            string              `json:"state,omitempty"`
	ChangeKind       model.FixChangeKind `json:"change_kind,omitempty"`
	Severity         string              `json:"severity,omitempty"`
	Harness          string              `json:"harness,omitempty"`
	RecordedAfter    string              `json:"recorded_after,omitempty"`
	IssueID          string              `json:"issue_id,omitempty"`
	IncludeRetracted bool                `json:"include_retracted,omitempty"`
}

type fixMonitoringCursorEnvelope struct {
	Version      int                         `json:"v"`
	Kind         string                      `json:"k"`
	Snapshot     model.FixMonitoringSnapshot `json:"s"`
	Limit        int                         `json:"l,omitempty"`
	Filter       *fixMonitoringCursorFilter  `json:"f,omitempty"`
	IssueID      string                      `json:"i,omitempty"`
	AnnotationID string                      `json:"a,omitempty"`
	StateRank    int                         `json:"r,omitempty"`
	SeverityRank int                         `json:"e,omitempty"`
	PositionTime string                      `json:"t,omitempty"`
	PositionID   string                      `json:"p,omitempty"`
}

func (s *Service) ListFixMonitoring(
	ctx context.Context,
	request FixMonitoringListRequest,
) (FixMonitoringList, error) {
	if s.fixMonitoringRepository == nil {
		return FixMonitoringList{}, errors.New("fix monitoring repository is unavailable")
	}
	var query model.FixMonitoringQuery
	var cursorFilter fixMonitoringCursorFilter
	if strings.TrimSpace(request.Cursor) != "" {
		if request.Limit != 0 || request.State != "" || request.ChangeKind != "" ||
			request.Severity != "" || request.Harness != "" ||
			request.RecordedAfter != nil || request.IssueID != "" ||
			request.IncludeRetracted {
			return FixMonitoringList{}, invalidRequest("cursor continuation accepts no filters")
		}
		cursor, err := decodeFixMonitoringCursor(
			strings.TrimSpace(request.Cursor),
			"fix_monitoring_list",
			s.nowUTC(),
		)
		if err != nil {
			return FixMonitoringList{}, err
		}
		if cursor.Filter == nil || cursor.Limit < 1 ||
			cursor.Limit > maxFixMonitoringLimit ||
			cursor.StateRank < 1 || cursor.StateRank > 6 ||
			cursor.SeverityRank < 0 || cursor.SeverityRank > 5 ||
			!issueIDPattern.MatchString(cursor.PositionID) ||
			cursor.IssueID != "" || cursor.AnnotationID != "" {
			return FixMonitoringList{}, invalidCursor()
		}
		positionTime, err := parseUTCTime(cursor.PositionTime)
		if err != nil {
			return FixMonitoringList{}, err
		}
		filter, err := cursor.Filter.model()
		if err != nil {
			return FixMonitoringList{}, err
		}
		if fixMonitoringFilterCursor(filter) != *cursor.Filter {
			return FixMonitoringList{}, invalidCursor()
		}
		cursorFilter = *cursor.Filter
		query = model.FixMonitoringQuery{
			Filter:   filter,
			Limit:    cursor.Limit,
			Snapshot: cursor.Snapshot,
			Cursor: &model.FixMonitoringPosition{
				StateRank:    cursor.StateRank,
				SeverityRank: cursor.SeverityRank,
				ActivityAt:   positionTime,
				IssueID:      cursor.PositionID,
			},
		}
	} else {
		normalized, filter, err := normalizeFixMonitoringRequest(request)
		if err != nil {
			return FixMonitoringList{}, err
		}
		request = normalized
		query = model.FixMonitoringQuery{
			Filter: filter,
			Limit:  request.Limit,
		}
		cursorFilter = fixMonitoringFilterCursor(filter)
	}
	page, err := s.fixMonitoringRepository.QueryFixMonitoring(ctx, query)
	if err != nil {
		return FixMonitoringList{}, mapFixMonitoringRepositoryError(err)
	}
	if err := validateFixMonitoringPage(page, query); err != nil {
		return FixMonitoringList{}, err
	}
	data := make([]FixMonitoringSummary, len(page.Data))
	for index := range page.Data {
		data[index] = monitoringSummaryDTO(page.Data[index])
	}
	nextCursor, err := fixMonitoringListNextCursor(page, query.Limit, cursorFilter)
	if err != nil {
		return FixMonitoringList{}, err
	}
	viewCursor, err := encodeFixMonitoringCursor(fixMonitoringCursorEnvelope{
		Version:  cursorVersion,
		Kind:     "fix_monitoring_view",
		Snapshot: page.Snapshot,
	})
	if err != nil {
		return FixMonitoringList{}, err
	}
	return FixMonitoringList{
		SchemaVersion:        model.FixMonitoringSchemaVersion,
		Data:                 nonNil(data),
		ReturnedCount:        len(data),
		Limit:                query.Limit,
		HasMore:              page.HasMore,
		NextCursor:           nextCursor,
		MonitoringViewCursor: viewCursor,
		EvidenceEvaluatedAt:  page.EvidenceEvaluatedAt.UTC(),
	}, nil
}

func (s *Service) GetIssueFixMonitoring(
	ctx context.Context,
	request FixMonitoringDetailRequest,
) (FixMonitoringDetail, error) {
	request.IssueID = strings.ToLower(strings.TrimSpace(request.IssueID))
	request.Cursor = strings.TrimSpace(request.Cursor)
	request.ViewCursor = strings.TrimSpace(request.ViewCursor)
	if !issueIDPattern.MatchString(request.IssueID) {
		return FixMonitoringDetail{}, invalidRequest("issue_id is malformed")
	}
	if request.Cursor != "" && request.ViewCursor != "" {
		return FixMonitoringDetail{}, invalidRequest("cursor and view_cursor are mutually exclusive")
	}
	query := model.FixMonitoringDetailQuery{IssueID: request.IssueID}
	routeMismatch := false
	switch {
	case request.Cursor != "":
		if request.Limit != 0 {
			return FixMonitoringDetail{}, invalidRequest("cursor continuation accepts no limit")
		}
		cursor, err := decodeFixMonitoringCursor(
			request.Cursor,
			"fix_monitoring_detail",
			s.nowUTC(),
		)
		if err != nil {
			return FixMonitoringDetail{}, err
		}
		routeMismatch = cursor.IssueID != request.IssueID
		if cursor.Limit < 1 ||
			cursor.Limit > maxFixMonitoringLimit ||
			!fixAnnotationIDPattern.MatchString(cursor.PositionID) ||
			cursor.Filter != nil || cursor.AnnotationID != "" ||
			cursor.StateRank != 0 || cursor.SeverityRank != 0 {
			return FixMonitoringDetail{}, invalidCursor()
		}
		positionTime, err := parseUTCTime(cursor.PositionTime)
		if err != nil {
			return FixMonitoringDetail{}, err
		}
		query.Limit = cursor.Limit
		query.Snapshot = cursor.Snapshot
		query.Cursor = &model.FixMonitoringDetailPosition{
			RecordedAt:   positionTime,
			AnnotationID: cursor.PositionID,
		}
	case request.ViewCursor != "":
		cursor, err := decodeFixMonitoringCursor(
			request.ViewCursor,
			"fix_monitoring_view",
			s.nowUTC(),
		)
		if err != nil {
			return FixMonitoringDetail{}, err
		}
		routeMismatch = cursor.IssueID != "" &&
			cursor.IssueID != request.IssueID
		if cursor.Limit != 0 || cursor.Filter != nil ||
			cursor.AnnotationID != "" || cursor.StateRank != 0 ||
			cursor.SeverityRank != 0 || cursor.PositionTime != "" ||
			cursor.PositionID != "" {
			return FixMonitoringDetail{}, invalidCursor()
		}
		query.Limit = boundedLimit(
			request.Limit,
			defaultFixMonitoringLimit,
			maxFixMonitoringLimit,
		)
		query.Snapshot = cursor.Snapshot
	default:
		query.Limit = boundedLimit(
			request.Limit,
			defaultFixMonitoringLimit,
			maxFixMonitoringLimit,
		)
	}
	if s.fixMonitoringRepository == nil {
		return FixMonitoringDetail{}, errors.New("fix monitoring repository is unavailable")
	}
	page, err := s.fixMonitoringRepository.QueryIssueFixMonitoring(ctx, query)
	if err != nil {
		return FixMonitoringDetail{}, mapBoundFixMonitoringRepositoryError(
			err,
			routeMismatch,
		)
	}
	if routeMismatch {
		return FixMonitoringDetail{}, notFound()
	}
	if err := validateFixMonitoringDetailPage(page, query); err != nil {
		return FixMonitoringDetail{}, err
	}
	currentIssue := page.CurrentIssue
	if currentIssue != nil {
		normalizeIssueSummary(currentIssue)
	}
	data := make([]FixAttemptMonitoring, len(page.Data))
	for index := range page.Data {
		observationViewCursor, err := encodeFixMonitoringCursor(
			fixMonitoringCursorEnvelope{
				Version:      cursorVersion,
				Kind:         "fix_monitoring_observation_view",
				Snapshot:     page.Snapshot,
				IssueID:      request.IssueID,
				AnnotationID: page.Data[index].AnnotationID,
			},
		)
		if err != nil {
			return FixMonitoringDetail{}, err
		}
		data[index] = fixAttemptMonitoringDTO(
			page.Data[index],
			observationViewCursor,
		)
	}
	nextCursor, err := fixMonitoringDetailNextCursor(page, query.Limit)
	if err != nil {
		return FixMonitoringDetail{}, err
	}
	viewCursor, err := encodeFixMonitoringCursor(fixMonitoringCursorEnvelope{
		Version:  cursorVersion,
		Kind:     "fix_monitoring_view",
		Snapshot: page.Snapshot,
		IssueID:  request.IssueID,
	})
	if err != nil {
		return FixMonitoringDetail{}, err
	}
	return FixMonitoringDetail{
		SchemaVersion:         model.FixMonitoringSchemaVersion,
		IssueID:               request.IssueID,
		CurrentIssueAvailable: currentIssue != nil,
		CurrentIssue:          currentIssue,
		Data:                  nonNil(data),
		ReturnedCount:         len(data),
		Limit:                 query.Limit,
		HasMore:               page.HasMore,
		NextCursor:            nextCursor,
		MonitoringViewCursor:  viewCursor,
		EvidenceEvaluatedAt:   page.EvidenceEvaluatedAt.UTC(),
	}, nil
}

func (s *Service) ListFixRecurrenceObservations(
	ctx context.Context,
	request FixRecurrenceObservationListRequest,
) (FixRecurrenceObservationList, error) {
	request.IssueID = strings.ToLower(strings.TrimSpace(request.IssueID))
	request.AnnotationID = strings.ToLower(strings.TrimSpace(request.AnnotationID))
	request.Cursor = strings.TrimSpace(request.Cursor)
	request.ObservationViewCursor = strings.TrimSpace(request.ObservationViewCursor)
	if !issueIDPattern.MatchString(request.IssueID) ||
		!fixAnnotationIDPattern.MatchString(request.AnnotationID) {
		return FixRecurrenceObservationList{}, invalidRequest("monitoring route identifier is malformed")
	}
	if (request.Cursor == "") == (request.ObservationViewCursor == "") {
		return FixRecurrenceObservationList{}, invalidRequest("exactly one monitoring cursor is required")
	}
	query := model.FixRecurrenceObservationQuery{
		IssueID:      request.IssueID,
		AnnotationID: request.AnnotationID,
	}
	routeMismatch := false
	if request.Cursor != "" {
		if request.Limit != 0 {
			return FixRecurrenceObservationList{}, invalidRequest("cursor continuation accepts no limit")
		}
		cursor, err := decodeFixMonitoringCursor(
			request.Cursor,
			"fix_monitoring_observations",
			s.nowUTC(),
		)
		if err != nil {
			return FixRecurrenceObservationList{}, err
		}
		routeMismatch = cursor.IssueID != request.IssueID ||
			cursor.AnnotationID != request.AnnotationID
		if cursor.Limit < 1 || cursor.Limit > maxFixMonitoringLimit ||
			!fixRecurrenceIDPattern.MatchString(cursor.PositionID) ||
			cursor.Filter != nil || cursor.StateRank != 0 ||
			cursor.SeverityRank != 0 {
			return FixRecurrenceObservationList{}, invalidCursor()
		}
		positionTime, err := parseUTCTime(cursor.PositionTime)
		if err != nil {
			return FixRecurrenceObservationList{}, err
		}
		query.Limit = cursor.Limit
		query.Snapshot = cursor.Snapshot
		query.Cursor = &model.FixRecurrenceObservationPosition{
			FirstQualifyingEventAt: positionTime,
			RecurrenceID:           cursor.PositionID,
		}
	} else {
		cursor, err := decodeFixMonitoringCursor(
			request.ObservationViewCursor,
			"fix_monitoring_observation_view",
			s.nowUTC(),
		)
		if err != nil {
			return FixRecurrenceObservationList{}, err
		}
		routeMismatch = cursor.IssueID != request.IssueID ||
			cursor.AnnotationID != request.AnnotationID
		if cursor.Limit != 0 || cursor.Filter != nil ||
			cursor.StateRank != 0 || cursor.SeverityRank != 0 ||
			cursor.PositionTime != "" || cursor.PositionID != "" {
			return FixRecurrenceObservationList{}, invalidCursor()
		}
		query.Limit = boundedLimit(
			request.Limit,
			defaultFixMonitoringLimit,
			maxFixMonitoringLimit,
		)
		query.Snapshot = cursor.Snapshot
	}
	if s.fixMonitoringRepository == nil {
		return FixRecurrenceObservationList{}, errors.New("fix monitoring repository is unavailable")
	}
	page, err := s.fixMonitoringRepository.QueryFixRecurrenceObservations(ctx, query)
	if err != nil {
		return FixRecurrenceObservationList{}, mapBoundFixMonitoringRepositoryError(
			err,
			routeMismatch,
		)
	}
	if routeMismatch {
		return FixRecurrenceObservationList{}, notFound()
	}
	if err := validateFixRecurrenceObservationPage(page, query); err != nil {
		return FixRecurrenceObservationList{}, err
	}
	data := make([]FixRecurrenceObservation, len(page.Data))
	for index := range page.Data {
		value, err := fixRecurrenceObservationDTO(page.Data[index])
		if err != nil {
			return FixRecurrenceObservationList{}, err
		}
		data[index] = value
	}
	nextCursor, err := fixRecurrenceObservationNextCursor(page, query.Limit)
	if err != nil {
		return FixRecurrenceObservationList{}, err
	}
	return FixRecurrenceObservationList{
		SchemaVersion:       model.FixMonitoringSchemaVersion,
		IssueID:             request.IssueID,
		AnnotationID:        request.AnnotationID,
		Data:                nonNil(data),
		ReturnedCount:       len(data),
		Limit:               query.Limit,
		HasMore:             page.HasMore,
		NextCursor:          nextCursor,
		EvidenceEvaluatedAt: page.EvidenceEvaluatedAt.UTC(),
	}, nil
}

func normalizeFixMonitoringRequest(
	request FixMonitoringListRequest,
) (FixMonitoringListRequest, model.FixMonitoringFilter, error) {
	request.Limit = boundedLimit(
		request.Limit,
		defaultFixMonitoringLimit,
		maxFixMonitoringLimit,
	)
	request.State = strings.ToLower(strings.TrimSpace(request.State))
	request.ChangeKind = strings.ToLower(strings.TrimSpace(request.ChangeKind))
	request.Severity = strings.ToLower(strings.TrimSpace(request.Severity))
	request.Harness = strings.TrimSpace(request.Harness)
	request.IssueID = strings.ToLower(strings.TrimSpace(request.IssueID))
	request.RecordedAfter = utcTime(request.RecordedAfter)
	if request.State != "" && !model.ValidFixRecurrenceState(request.State) {
		return request, model.FixMonitoringFilter{}, invalidRequest("state is unsupported")
	}
	changeKind := model.FixChangeKind(request.ChangeKind)
	if changeKind != "" && !changeKind.Valid() {
		return request, model.FixMonitoringFilter{}, invalidRequest("change_kind is unsupported")
	}
	switch request.Severity {
	case "", "info", "low", "medium", "high", "critical":
	default:
		return request, model.FixMonitoringFilter{}, invalidRequest("severity is unsupported")
	}
	if len(request.Harness) > 128 {
		return request, model.FixMonitoringFilter{}, invalidRequest("harness must not exceed 128 bytes")
	}
	if request.IssueID != "" && !issueIDPattern.MatchString(request.IssueID) {
		return request, model.FixMonitoringFilter{}, invalidRequest("issue_id is malformed")
	}
	return request, model.FixMonitoringFilter{
		State:            request.State,
		ChangeKind:       changeKind,
		Severity:         request.Severity,
		Harness:          request.Harness,
		RecordedAfter:    request.RecordedAfter,
		IssueID:          request.IssueID,
		IncludeRetracted: request.IncludeRetracted,
	}, nil
}

func fixMonitoringFilterCursor(filter model.FixMonitoringFilter) fixMonitoringCursorFilter {
	return fixMonitoringCursorFilter{
		State:            filter.State,
		ChangeKind:       filter.ChangeKind,
		Severity:         filter.Severity,
		Harness:          filter.Harness,
		RecordedAfter:    timeString(filter.RecordedAfter),
		IssueID:          filter.IssueID,
		IncludeRetracted: filter.IncludeRetracted,
	}
}

func (filter fixMonitoringCursorFilter) model() (model.FixMonitoringFilter, error) {
	request := FixMonitoringListRequest{
		Limit:            defaultFixMonitoringLimit,
		State:            filter.State,
		ChangeKind:       string(filter.ChangeKind),
		Severity:         filter.Severity,
		Harness:          filter.Harness,
		IssueID:          filter.IssueID,
		IncludeRetracted: filter.IncludeRetracted,
	}
	if filter.RecordedAfter != "" {
		value, err := parseUTCTime(filter.RecordedAfter)
		if err != nil {
			return model.FixMonitoringFilter{}, err
		}
		request.RecordedAfter = &value
	}
	_, result, err := normalizeFixMonitoringRequest(request)
	return result, err
}

func monitoringSummaryDTO(value model.FixMonitoringSummary) FixMonitoringSummary {
	return FixMonitoringSummary{
		IssueID:                           value.IssueID,
		AnnotationID:                      value.AnnotationID,
		TitleCode:                         cloneStringPointer(value.Subject.TitleCode),
		Severity:                          cloneStringPointer(value.Subject.Severity),
		ChangeKind:                        value.ChangeKind,
		RecordedAt:                        value.RecordedAt.UTC(),
		MonitorFrom:                       value.MonitorFrom.UTC(),
		FixRecurrenceState:                presentationRecurrenceState(value.FixRecurrenceState),
		FixRecurrenceCount:                value.FixRecurrenceCount,
		SameAnchorSessionObservationCount: value.SameAnchorSessionObservationCount,
		OtherSessionObservationCount:      value.OtherSessionObservationCount,
		HistoricalMatchingEvidenceCount:   value.HistoricalMatchingEvidenceCount,
		ActiveAttemptCount:                value.ActiveAttemptCount,
		ObservedAttemptCount:              value.ObservedAttemptCount,
		AnalysisComplete:                  value.AnalysisComplete,
		CountIsLowerBound:                 value.CountIsLowerBound,
		FutureComparisonAvailable:         value.FutureComparisonAvailable,
		FutureComparisonUnavailableReason: presentationUnavailableReason(
			value.FutureComparisonUnavailableReason,
		),
		LastRecurrenceObservedAt: utcTime(value.LastRecurrenceObservedAt),
		Coverage:                 monitoringCoverageDTO(value.Coverage),
	}
}

func fixAttemptMonitoringDTO(
	value model.FixAttemptMonitoring,
	observationViewCursor string,
) FixAttemptMonitoring {
	return FixAttemptMonitoring{
		AnnotationID: value.AnnotationID,
		IssueID:      value.IssueID,
		Subject: FixMonitoringSubject{
			TitleCode:          cloneStringPointer(value.Subject.TitleCode),
			Severity:           cloneStringPointer(value.Subject.Severity),
			Confidence:         cloneStringPointer(value.Subject.Confidence),
			AnchorHarness:      cloneStringPointer(value.Subject.AnchorHarness),
			Origin:             value.Subject.Origin,
			DetectorID:         value.Subject.DetectorID,
			DetectorVersion:    value.Subject.DetectorVersion,
			FingerprintVersion: value.Subject.FingerprintVersion,
		},
		ChangeKind:                        value.ChangeKind,
		RecordedAt:                        value.RecordedAt.UTC(),
		MonitorFrom:                       value.MonitorFrom.UTC(),
		State:                             value.State,
		RetractionReason:                  cloneStringPointer(value.RetractionReason),
		RetractedAt:                       utcTime(value.RetractedAt),
		FixRecurrenceState:                presentationRecurrenceState(value.FixRecurrenceState),
		FixRecurrenceCount:                value.FixRecurrenceCount,
		SameAnchorSessionObservationCount: value.SameAnchorSessionObservationCount,
		OtherSessionObservationCount:      value.OtherSessionObservationCount,
		HistoricalMatchingEvidenceCount:   value.HistoricalMatchingEvidenceCount,
		AnalysisComplete:                  value.AnalysisComplete,
		CountIsLowerBound:                 value.CountIsLowerBound,
		FutureComparisonAvailable:         value.FutureComparisonAvailable,
		FutureComparisonUnavailableReason: presentationUnavailableReason(
			value.FutureComparisonUnavailableReason,
		),
		LastRecurrenceObservedAt: utcTime(value.LastRecurrenceObservedAt),
		Coverage:                 monitoringCoverageDTO(value.Coverage),
		AnchorEvidenceCurrentlyRetained: presentationEvidenceState(
			value.AnchorEvidenceCurrentlyRetained,
		),
		RecurrenceEvidence: FixRecurrenceEvidenceCounts{
			Available: value.RecurrenceEvidence.Available,
			Partial:   value.RecurrenceEvidence.Partial,
			Pruned:    value.RecurrenceEvidence.Pruned,
			Unknown:   value.RecurrenceEvidence.Unknown,
		},
		ObservationViewCursor: observationViewCursor,
	}
}

func fixRecurrenceObservationDTO(
	value model.FixRecurrenceObservation,
) (FixRecurrenceObservation, error) {
	if !fixRecurrenceIDPattern.MatchString(value.RecurrenceID) ||
		value.QualifyingCitationCount < 1 ||
		len(value.RetainedEventIDs) > model.MaxEventLookupIDs {
		return FixRecurrenceObservation{}, errors.New("fix monitoring repository returned an invalid observation")
	}
	evidenceState := presentationEvidenceState(value.EvidenceCurrentlyRetained)
	eventIDs := append([]string(nil), value.RetainedEventIDs...)
	if evidenceState != model.FixRecurrenceEvidenceUnknown {
		for _, eventID := range eventIDs {
			if !model.IsCanonicalUUIDv7(eventID) {
				return FixRecurrenceObservation{}, errors.New("fix monitoring repository returned an invalid observation event")
			}
		}
	}
	retainedCount := cloneIntPointer(value.RetainedEventCount)
	missingCount := cloneIntPointer(value.MissingEventCount)
	truncated := cloneBoolPointer(value.EvidenceTruncated)
	if evidenceState == model.FixRecurrenceEvidenceUnknown {
		eventIDs = []string{}
		retainedCount = nil
		missingCount = nil
		truncated = nil
	} else if retainedCount == nil || missingCount == nil || truncated == nil {
		return FixRecurrenceObservation{}, errors.New("fix monitoring repository returned incomplete evidence metadata")
	}
	return FixRecurrenceObservation{
		RecurrenceID:              value.RecurrenceID,
		OccurrenceID:              value.OccurrenceID,
		SessionID:                 value.SessionID,
		FingerprintVersion:        value.FingerprintVersion,
		Origin:                    value.Origin,
		DetectorID:                value.DetectorID,
		DetectorVersion:           value.DetectorVersion,
		FirstQualifyingEventAt:    value.FirstQualifyingEventAt.UTC(),
		LastQualifyingEventAt:     value.LastQualifyingEventAt.UTC(),
		QualifyingCitationCount:   value.QualifyingCitationCount,
		RetainedEventIDs:          nonNil(eventIDs),
		RetainedEventCount:        retainedCount,
		MissingEventCount:         missingCount,
		EvidenceComplete:          value.EvidenceComplete,
		EvidenceTruncated:         truncated,
		EvidenceCurrentlyRetained: evidenceState,
		SameSessionAsAnchor:       value.SameSessionAsAnchor,
		ObservedAt:                value.ObservedAt.UTC(),
	}, nil
}

func monitoringCoverageDTO(value model.FixMonitoringCoverage) FixMonitoringCoverage {
	return FixMonitoringCoverage{
		ComparableCurrent:   value.ComparableCurrent,
		ComparablePending:   value.ComparablePending,
		ComparableFailed:    value.ComparableFailed,
		ComparableTruncated: value.ComparableTruncated,
		AnalysisThrough:     utcTime(value.AnalysisThrough),
		Complete:            value.Complete,
	}
}

func presentationRecurrenceState(value string) string {
	if model.ValidFixRecurrenceState(value) {
		return value
	}
	return "unknown"
}

func presentationEvidenceState(value string) string {
	if model.ValidFixEvidenceState(value) {
		return value
	}
	return model.FixRecurrenceEvidenceUnknown
}

func presentationUnavailableReason(value *string) *string {
	if value == nil {
		return nil
	}
	switch *value {
	case model.FixComparisonScopeUnavailable,
		model.FixComparisonSourcePositiveOnly,
		model.FixComparisonCapabilityUnavailable,
		model.FixComparisonBaselineTimeUnavailable,
		model.FixComparisonFingerprintUnsupported:
		return cloneStringPointer(value)
	default:
		fallback := model.FixComparisonCapabilityUnavailable
		return &fallback
	}
}

func fixMonitoringListNextCursor(
	page model.FixMonitoringPage,
	limit int,
	filter fixMonitoringCursorFilter,
) (*string, error) {
	if !page.HasMore {
		return nil, nil
	}
	if page.Position == nil {
		return nil, errors.New("fix monitoring repository returned no list continuation")
	}
	value, err := encodeFixMonitoringCursor(fixMonitoringCursorEnvelope{
		Version:      cursorVersion,
		Kind:         "fix_monitoring_list",
		Snapshot:     page.Snapshot,
		Limit:        limit,
		Filter:       &filter,
		StateRank:    page.Position.StateRank,
		SeverityRank: page.Position.SeverityRank,
		PositionTime: page.Position.ActivityAt.UTC().Format(time.RFC3339Nano),
		PositionID:   page.Position.IssueID,
	})
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func fixMonitoringDetailNextCursor(
	page model.FixMonitoringDetailPage,
	limit int,
) (*string, error) {
	if !page.HasMore {
		return nil, nil
	}
	if page.Position == nil {
		return nil, errors.New("fix monitoring repository returned no detail continuation")
	}
	value, err := encodeFixMonitoringCursor(fixMonitoringCursorEnvelope{
		Version:      cursorVersion,
		Kind:         "fix_monitoring_detail",
		Snapshot:     page.Snapshot,
		Limit:        limit,
		IssueID:      page.IssueID,
		PositionTime: page.Position.RecordedAt.UTC().Format(time.RFC3339Nano),
		PositionID:   page.Position.AnnotationID,
	})
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func fixRecurrenceObservationNextCursor(
	page model.FixRecurrenceObservationPage,
	limit int,
) (*string, error) {
	if !page.HasMore {
		return nil, nil
	}
	if page.Position == nil {
		return nil, errors.New("fix monitoring repository returned no observation continuation")
	}
	value, err := encodeFixMonitoringCursor(fixMonitoringCursorEnvelope{
		Version:      cursorVersion,
		Kind:         "fix_monitoring_observations",
		Snapshot:     page.Snapshot,
		Limit:        limit,
		IssueID:      page.IssueID,
		AnnotationID: page.AnnotationID,
		PositionTime: page.Position.FirstQualifyingEventAt.UTC().Format(time.RFC3339Nano),
		PositionID:   page.Position.RecurrenceID,
	})
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func validateFixMonitoringPage(
	page model.FixMonitoringPage,
	query model.FixMonitoringQuery,
) error {
	if err := validateFixMonitoringSnapshot(page.Snapshot, time.Time{}, false); err != nil ||
		len(page.Data) > query.Limit ||
		page.EvidenceEvaluatedAt.IsZero() ||
		(page.HasMore && (len(page.Data) == 0 || page.Position == nil)) {
		return errors.New("fix monitoring repository returned invalid list metadata")
	}
	for _, item := range page.Data {
		if !issueIDPattern.MatchString(item.IssueID) ||
			!fixAnnotationIDPattern.MatchString(item.AnnotationID) {
			return errors.New("fix monitoring repository returned invalid list identity")
		}
	}
	if !query.Snapshot.Empty() && page.Snapshot != query.Snapshot {
		return errors.New("fix monitoring repository changed snapshots")
	}
	return nil
}

func validateFixMonitoringDetailPage(
	page model.FixMonitoringDetailPage,
	query model.FixMonitoringDetailQuery,
) error {
	if err := validateFixMonitoringSnapshot(page.Snapshot, time.Time{}, false); err != nil ||
		page.IssueID != query.IssueID ||
		len(page.Data) > query.Limit ||
		page.EvidenceEvaluatedAt.IsZero() ||
		(page.CurrentIssue != nil && page.CurrentIssue.IssueID != query.IssueID) ||
		(page.HasMore && (len(page.Data) == 0 || page.Position == nil)) {
		return errors.New("fix monitoring repository returned invalid detail metadata")
	}
	for _, item := range page.Data {
		if item.IssueID != query.IssueID ||
			!fixAnnotationIDPattern.MatchString(item.AnnotationID) {
			return errors.New("fix monitoring repository returned invalid detail identity")
		}
	}
	if !query.Snapshot.Empty() && page.Snapshot != query.Snapshot {
		return errors.New("fix monitoring repository changed snapshots")
	}
	return nil
}

func validateFixRecurrenceObservationPage(
	page model.FixRecurrenceObservationPage,
	query model.FixRecurrenceObservationQuery,
) error {
	if err := validateFixMonitoringSnapshot(page.Snapshot, time.Time{}, false); err != nil ||
		page.IssueID != query.IssueID ||
		page.AnnotationID != query.AnnotationID ||
		len(page.Data) > query.Limit ||
		page.EvidenceEvaluatedAt.IsZero() ||
		(page.HasMore && (len(page.Data) == 0 || page.Position == nil)) {
		return errors.New("fix monitoring repository returned invalid observation metadata")
	}
	if page.Snapshot != query.Snapshot {
		return errors.New("fix monitoring repository changed snapshots")
	}
	return nil
}

func encodeFixMonitoringCursor(cursor fixMonitoringCursorEnvelope) (string, error) {
	body, err := json.Marshal(cursor)
	if err != nil || len(body) > maxCursorBytes {
		return "", errors.New("encode fix monitoring cursor")
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeFixMonitoringCursor(
	value string,
	kind string,
	now time.Time,
) (fixMonitoringCursorEnvelope, error) {
	if value == "" || len(value) > maxCursorBytes*2 {
		return fixMonitoringCursorEnvelope{}, invalidCursor()
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(body) == 0 || len(body) > maxCursorBytes {
		return fixMonitoringCursorEnvelope{}, invalidCursor()
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var cursor fixMonitoringCursorEnvelope
	if err := decoder.Decode(&cursor); err != nil {
		return fixMonitoringCursorEnvelope{}, invalidCursor()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fixMonitoringCursorEnvelope{}, invalidCursor()
	}
	if cursor.Version != cursorVersion || cursor.Kind != kind {
		return fixMonitoringCursorEnvelope{}, invalidCursor()
	}
	if err := validateFixMonitoringSnapshot(cursor.Snapshot, now, true); err != nil {
		return fixMonitoringCursorEnvelope{}, err
	}
	return cursor, nil
}

func validateFixMonitoringSnapshot(
	snapshot model.FixMonitoringSnapshot,
	now time.Time,
	rejectFuture bool,
) error {
	if snapshot.Empty() ||
		snapshot.ProjectionGeneration < 0 ||
		snapshot.EventGeneration < 0 ||
		snapshot.RetentionGeneration < 1 ||
		snapshot.AnnotationSequence < 0 ||
		snapshot.RetractionSequence < 0 ||
		snapshot.JobSequence < 0 ||
		snapshot.JobEventSequence < 0 ||
		snapshot.ObservationSequence < 0 ||
		snapshot.IssuedAt.IsZero() ||
		snapshot.IssuedAt.Location() != time.UTC {
		return invalidCursor()
	}
	if rejectFuture && snapshot.IssuedAt.After(now.UTC()) {
		return invalidCursor()
	}
	return nil
}

func mapFixMonitoringRepositoryError(err error) error {
	switch {
	case errors.Is(err, model.ErrFixMonitoringSnapshotExpired):
		return cursorExpired()
	case errors.Is(err, model.ErrFixMonitoringSnapshotInvalid):
		return invalidCursor()
	case errors.Is(err, model.ErrFixMonitoringNotFound):
		return notFound()
	case errors.Is(err, model.ErrFixMonitoringCatchingUp):
		return ErrMonitoringCatchingUp
	case errors.Is(err, model.ErrFixMonitoringCatchupFailed):
		return ErrMonitoringCatchupFailed
	default:
		return err
	}
}

func mapBoundFixMonitoringRepositoryError(err error, routeMismatch bool) error {
	mapped := mapFixMonitoringRepositoryError(err)
	switch {
	case errors.Is(mapped, ErrMonitoringCatchingUp),
		errors.Is(mapped, ErrMonitoringCatchupFailed),
		errors.Is(mapped, ErrCursorExpired),
		errors.Is(mapped, ErrInvalidCursor):
		return mapped
	case routeMismatch:
		return notFound()
	default:
		return mapped
	}
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
