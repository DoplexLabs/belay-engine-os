package model

import "time"

const (
	FixSchemaVersion        = "belay.fix.v1"
	FixActionTokenVersion   = "belay.fix-action.v2"
	FixActionTokenVersionV1 = "belay.fix-action.v1"
	FixChangeCatalogVersion = "fix-change.v1"
	FixRecordedViaLocalUI   = "local_ui"

	FixStateActive    = "active"
	FixStateRetracted = "retracted"

	FixEvidenceAvailable = "available"
	FixEvidencePartial   = "partial"
	FixEvidencePruned    = "pruned"
	FixEvidenceUnknown   = "unknown"

	FixEligibilityEligible           = "eligible"
	FixEligibilityAnalysisNotCurrent = "analysis_not_current"
	FixEligibilityExperimentalSignal = "experimental_signal"
	FixEligibilityEvidenceGap        = "evidence_gap"
	FixEligibilityScopeUnavailable   = "scope_unavailable"
)

type FixChangeKind string

const (
	FixChangeCode             FixChangeKind = "code_change"
	FixChangeConfiguration    FixChangeKind = "configuration_change"
	FixChangeDependency       FixChangeKind = "dependency_change"
	FixChangePermission       FixChangeKind = "permission_change"
	FixChangeEnvironment      FixChangeKind = "environment_change"
	FixChangeAgentInstruction FixChangeKind = "agent_instruction"
	FixChangeProjectRule      FixChangeKind = "project_rule"
	FixChangeMonitorHook      FixChangeKind = "monitor_hook"
	FixChangeOther            FixChangeKind = "other"
)

func (kind FixChangeKind) Valid() bool {
	switch kind {
	case FixChangeCode,
		FixChangeConfiguration,
		FixChangeDependency,
		FixChangePermission,
		FixChangeEnvironment,
		FixChangeAgentInstruction,
		FixChangeProjectRule,
		FixChangeMonitorHook,
		FixChangeOther:
		return true
	default:
		return false
	}
}

type FixRetractionReason string

const (
	FixRetractionRecordedByMistake FixRetractionReason = "recorded_by_mistake"
	FixRetractionSuperseded        FixRetractionReason = "superseded"
	FixRetractionOther             FixRetractionReason = "other"
)

func (reason FixRetractionReason) Valid() bool {
	switch reason {
	case FixRetractionRecordedByMistake, FixRetractionSuperseded, FixRetractionOther:
		return true
	default:
		return false
	}
}

type IssueViewClaims struct {
	CursorEpoch         string    `json:"cursor_epoch"`
	Snapshot            int64     `json:"snapshot"`
	RetentionGeneration int64     `json:"retention_generation"`
	IssuedAt            time.Time `json:"issued_at"`
}

type FixActionClaims struct {
	Version             string    `json:"version"`
	CursorEpoch         string    `json:"cursor_epoch"`
	IssueID             string    `json:"issue_id"`
	Snapshot            int64     `json:"snapshot"`
	RetentionGeneration int64     `json:"retention_generation"`
	IssuedAt            time.Time `json:"issued_at"`
	ExpiresAt           time.Time `json:"expires_at"`
}

type FixEligibilityQuery struct {
	IssueID             string
	CursorEpoch         string
	Snapshot            int64
	RetentionGeneration int64
	IssuedAt            time.Time
}

type FixEligibility struct {
	Eligible            bool   `json:"eligible"`
	Reason              string `json:"reason"`
	IssueID             string `json:"issue_id"`
	CursorEpoch         string `json:"cursor_epoch"`
	Snapshot            int64  `json:"snapshot"`
	RetentionGeneration int64  `json:"retention_generation"`
}

type FixAnnotation struct {
	AnnotationID              string        `json:"annotation_id"`
	IssueID                   string        `json:"issue_id"`
	AnchorRevisionID          string        `json:"anchor_revision_id"`
	AnchorOccurrenceID        string        `json:"anchor_occurrence_id"`
	AnchorSessionID           string        `json:"anchor_session_id"`
	FingerprintID             string        `json:"fingerprint_id"`
	FingerprintVersion        string        `json:"fingerprint_version"`
	Origin                    string        `json:"origin"`
	DetectorID                string        `json:"detector_id"`
	DetectorVersion           string        `json:"detector_version"`
	ScopeQuality              ScopeQuality  `json:"scope_quality"`
	IssueSnapshotGeneration   int64         `json:"issue_snapshot_generation"`
	AnchorAnalysisGeneration  int64         `json:"anchor_analysis_generation"`
	AnchorFirstObservedAt     time.Time     `json:"anchor_first_observed_at"`
	AnchorLastObservedAt      time.Time     `json:"anchor_last_observed_at"`
	ChangeKind                FixChangeKind `json:"change_kind"`
	ChangeCatalogVersion      string        `json:"change_catalog_version"`
	RecordedVia               string        `json:"recorded_via"`
	RecordedAt                time.Time     `json:"recorded_at"`
	MonitorFrom               time.Time     `json:"monitor_from"`
	EvidenceCurrentlyRetained string        `json:"evidence_currently_retained"`
	State                     string        `json:"state"`
	RetractionReason          string        `json:"retraction_reason,omitempty"`
	RetractedAt               *time.Time    `json:"retracted_at,omitempty"`
}

type FixRetraction struct {
	RetractionID string              `json:"retraction_id"`
	AnnotationID string              `json:"annotation_id"`
	IssueID      string              `json:"issue_id"`
	Reason       FixRetractionReason `json:"reason"`
	RecordedVia  string              `json:"recorded_via"`
	RetractedAt  time.Time           `json:"retracted_at"`
}

type FixAnnotationPosition struct {
	IssueID      string
	RecordedAt   time.Time
	AnnotationID string
}

type FixAnnotationQuery struct {
	IssueID            string
	Limit              int
	AnnotationSnapshot int64
	RetractionSnapshot int64
	Cursor             *FixAnnotationPosition
}

type FixAnnotationPage struct {
	Data                []FixAnnotation `json:"data"`
	AnnotationSnapshot  int64           `json:"annotation_snapshot"`
	RetractionSnapshot  int64           `json:"retraction_snapshot"`
	HasMore             bool            `json:"has_more"`
	EvidenceEvaluatedAt time.Time       `json:"evidence_evaluated_at"`
}

func IsCanonicalUUIDv4(value string) bool {
	if len(value) != 36 ||
		value[8] != '-' ||
		value[13] != '-' ||
		value[18] != '-' ||
		value[23] != '-' ||
		value[14] != '4' {
		return false
	}
	switch value[19] {
	case '8', '9', 'a', 'b':
	default:
		return false
	}
	for index := range value {
		switch index {
		case 8, 13, 18, 23:
			continue
		}
		character := value[index]
		if !('0' <= character && character <= '9') &&
			!('a' <= character && character <= 'f') {
			return false
		}
	}
	return true
}
