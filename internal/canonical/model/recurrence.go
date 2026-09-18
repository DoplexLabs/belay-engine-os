package model

import (
	"errors"
	"time"
)

const (
	FixMonitoringSchemaVersion = "belay.fix-monitoring.v1"

	FixRecurrenceMatchingEvidence      = "matching_evidence_observed"
	FixRecurrenceMonitoringIncomplete  = "monitoring_incomplete"
	FixRecurrenceAwaitingEvidence      = "awaiting_later_evidence"
	FixRecurrenceNoLaterMatch          = "no_later_match_observed"
	FixRecurrenceComparisonUnavailable = "comparison_unavailable"
	FixRecurrenceRetracted             = "retracted"

	FixRecurrenceEvidenceAvailable = "available"
	FixRecurrenceEvidencePartial   = "partial"
	FixRecurrenceEvidencePruned    = "pruned"
	FixRecurrenceEvidenceUnknown   = "unknown"

	FixComparisonScopeUnavailable        = "scope_unavailable"
	FixComparisonSourcePositiveOnly      = "source_positive_only"
	FixComparisonCapabilityUnavailable   = "capability_unavailable"
	FixComparisonBaselineTimeUnavailable = "baseline_time_unavailable"
	FixComparisonFingerprintUnsupported  = "fingerprint_version_unsupported"

	FixNegativeComparisonSupported    = "supported"
	FixNegativeComparisonPositiveOnly = "positive_only"

	FixMonitoringReadinessCatchingUp = "catching_up"
	FixMonitoringReadinessReady      = "ready"
	FixMonitoringReadinessFailed     = "failed"
)

var (
	ErrFixMonitoringSnapshotInvalid = errors.New("fix monitoring snapshot is invalid")
	ErrFixMonitoringSnapshotExpired = errors.New("fix monitoring snapshot has expired")
	ErrFixMonitoringNotFound        = errors.New("fix monitoring resource was not found")
	ErrFixMonitoringCatchingUp      = errors.New("fix monitoring catch-up is in progress")
	ErrFixMonitoringCatchupFailed   = errors.New("fix monitoring catch-up failed")
)

type FixMonitoringSubject struct {
	AnnotationID           string    `json:"annotation_id"`
	FingerprintScopeID     string    `json:"-"`
	ScopeCaptureStatus     string    `json:"scope_capture_status"`
	Category               *string   `json:"category"`
	TitleCode              *string   `json:"title_code"`
	Severity               *string   `json:"severity"`
	Confidence             *string   `json:"confidence"`
	AnchorHarness          *string   `json:"anchor_harness"`
	Origin                 string    `json:"origin"`
	DetectorID             string    `json:"detector_id"`
	DetectorVersion        string    `json:"detector_version"`
	FingerprintVersion     string    `json:"fingerprint_version"`
	NegativeComparisonMode string    `json:"negative_comparison_mode"`
	MonitorFromOrderNS     *int64    `json:"-"`
	CapturedAt             time.Time `json:"captured_at"`
}

type AnalysisCapability struct {
	SessionID              string `json:"session_id"`
	FingerprintScopeID     string `json:"-"`
	Origin                 string `json:"origin"`
	DetectorID             string `json:"detector_id"`
	DetectorVersion        string `json:"detector_version"`
	FingerprintVersion     string `json:"fingerprint_version"`
	NegativeComparisonMode string `json:"negative_comparison_mode"`
	AnalysisThroughOrderNS *int64 `json:"-"`
}

type FixRecurrenceJobClaim struct {
	JobID                string
	SessionID            string
	RevisionID           string
	ProjectionGeneration int64
	AnnotationSnapshot   int64
	AttemptAfterSequence int64
	ClaimGeneration      int64
	ClaimToken           string
	AttemptCount         int
}

type FixRecurrenceBatchResult struct {
	Processed    int
	Observed     int
	Completed    bool
	NextSequence int64
}

type FixRecurrenceObservation struct {
	RecurrenceID              string    `json:"recurrence_id"`
	AnnotationID              string    `json:"annotation_id"`
	IssueID                   string    `json:"issue_id"`
	OccurrenceID              string    `json:"occurrence_id"`
	SessionID                 string    `json:"session_id"`
	FingerprintID             string    `json:"fingerprint_id"`
	FingerprintVersion        string    `json:"fingerprint_version"`
	Origin                    string    `json:"origin"`
	DetectorID                string    `json:"detector_id"`
	DetectorVersion           string    `json:"detector_version"`
	AnalysisGeneration        int64     `json:"analysis_generation"`
	ProjectionGeneration      int64     `json:"projection_generation"`
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

type FixMonitoringCoverage struct {
	ComparableCurrent   int        `json:"comparable_current"`
	ComparablePending   int        `json:"comparable_pending"`
	ComparableFailed    int        `json:"comparable_failed"`
	ComparableTruncated int        `json:"comparable_truncated"`
	AnalysisThrough     *time.Time `json:"analysis_through"`
	Complete            bool       `json:"complete"`
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
	ChangeKind                        FixChangeKind               `json:"change_kind"`
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
}

type FixMonitoringSummary struct {
	FixAttemptMonitoring
	ActiveAttemptCount   int `json:"active_attempt_count"`
	ObservedAttemptCount int `json:"observed_attempt_count"`
}

type FixMonitoringSnapshot struct {
	ProjectionGeneration int64     `json:"projection_generation"`
	EventGeneration      int64     `json:"event_generation"`
	RetentionGeneration  int64     `json:"retention_generation"`
	AnnotationSequence   int64     `json:"annotation_sequence"`
	RetractionSequence   int64     `json:"retraction_sequence"`
	JobSequence          int64     `json:"job_sequence"`
	JobEventSequence     int64     `json:"job_event_sequence"`
	ObservationSequence  int64     `json:"observation_sequence"`
	IssuedAt             time.Time `json:"issued_at"`
}

func (snapshot FixMonitoringSnapshot) Empty() bool {
	return snapshot == (FixMonitoringSnapshot{})
}

type FixMonitoringFilter struct {
	State            string
	ChangeKind       FixChangeKind
	Severity         string
	Harness          string
	RecordedAfter    *time.Time
	IssueID          string
	IncludeRetracted bool
}

type FixMonitoringPosition struct {
	StateRank    int
	SeverityRank int
	ActivityAt   time.Time
	IssueID      string
}

type FixMonitoringQuery struct {
	Filter   FixMonitoringFilter
	Limit    int
	Snapshot FixMonitoringSnapshot
	Cursor   *FixMonitoringPosition
}

type FixMonitoringPage struct {
	Data                []FixMonitoringSummary `json:"data"`
	Snapshot            FixMonitoringSnapshot  `json:"snapshot"`
	HasMore             bool                   `json:"has_more"`
	Position            *FixMonitoringPosition `json:"-"`
	EvidenceEvaluatedAt time.Time              `json:"evidence_evaluated_at"`
}

type FixMonitoringDetailPosition struct {
	RecordedAt   time.Time
	AnnotationID string
}

type FixMonitoringDetailQuery struct {
	IssueID  string
	Limit    int
	Snapshot FixMonitoringSnapshot
	Cursor   *FixMonitoringDetailPosition
}

type FixMonitoringDetailPage struct {
	IssueID             string                       `json:"issue_id"`
	CurrentIssue        *IssueSummary                `json:"current_issue"`
	Data                []FixAttemptMonitoring       `json:"data"`
	Snapshot            FixMonitoringSnapshot        `json:"snapshot"`
	HasMore             bool                         `json:"has_more"`
	Position            *FixMonitoringDetailPosition `json:"-"`
	EvidenceEvaluatedAt time.Time                    `json:"evidence_evaluated_at"`
}

type FixRecurrenceObservationPosition struct {
	FirstQualifyingEventAt time.Time
	RecurrenceID           string
}

type FixRecurrenceObservationQuery struct {
	IssueID      string
	AnnotationID string
	Limit        int
	Snapshot     FixMonitoringSnapshot
	Cursor       *FixRecurrenceObservationPosition
}

type FixRecurrenceObservationPage struct {
	IssueID             string                            `json:"issue_id"`
	AnnotationID        string                            `json:"annotation_id"`
	Data                []FixRecurrenceObservation        `json:"data"`
	Snapshot            FixMonitoringSnapshot             `json:"snapshot"`
	HasMore             bool                              `json:"has_more"`
	Position            *FixRecurrenceObservationPosition `json:"-"`
	EvidenceEvaluatedAt time.Time                         `json:"evidence_evaluated_at"`
}

func ValidFixRecurrenceState(value string) bool {
	switch value {
	case FixRecurrenceMatchingEvidence,
		FixRecurrenceMonitoringIncomplete,
		FixRecurrenceAwaitingEvidence,
		FixRecurrenceNoLaterMatch,
		FixRecurrenceComparisonUnavailable,
		FixRecurrenceRetracted:
		return true
	default:
		return false
	}
}

func ValidFixEvidenceState(value string) bool {
	switch value {
	case FixRecurrenceEvidenceAvailable,
		FixRecurrenceEvidencePartial,
		FixRecurrenceEvidencePruned,
		FixRecurrenceEvidenceUnknown:
		return true
	default:
		return false
	}
}
