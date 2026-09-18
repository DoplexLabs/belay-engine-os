package model

import "time"

const (
	AttentionFamilyKindExactIssue     = "exact_issue"
	AttentionFamilyKindMappedUpstream = "mapped_upstream"
)

type AttentionFamilyScopeCounts struct {
	Resolved int `json:"resolved"`
	Lexical  int `json:"lexical"`
	Unscoped int `json:"unscoped"`
	Conflict int `json:"conflict"`
}

type AttentionFamilySummary struct {
	FamilyID              string                     `json:"family_id"`
	GroupKey              string                     `json:"-"`
	Kind                  string                     `json:"kind"`
	MappingKey            string                     `json:"mapping_key,omitempty"`
	MappingVersion        string                     `json:"mapping_version,omitempty"`
	GroupingVersion       string                     `json:"grouping_version"`
	RepresentativeIssueID string                     `json:"representative_issue_id"`
	Representative        IssueSummary               `json:"-"`
	AttentionKind         string                     `json:"attention_kind"`
	Severity              string                     `json:"severity"`
	Confidence            string                     `json:"confidence"`
	FirstObservedAt       time.Time                  `json:"first_observed_at"`
	LastObservedAt        time.Time                  `json:"last_observed_at"`
	SupportingIssueCount  int                        `json:"supporting_issue_count"`
	OccurrenceCount       int                        `json:"occurrence_count"`
	SessionCount          int                        `json:"session_count"`
	Harnesses             []string                   `json:"harnesses"`
	Scope                 AttentionFamilyScopeCounts `json:"scope"`
	AnalysisStatus        AnalysisStatus             `json:"analysis_status"`
	EvidenceComplete      bool                       `json:"evidence_complete"`
	RetainedHistoryOnly   bool                       `json:"retained_history_only"`
	Experimental          bool                       `json:"experimental"`
}

type AttentionFamilyFilter struct {
	Severity       string
	Harness        string
	Origin         string
	AnalysisStatus AnalysisStatus
	ObservedAfter  *time.Time
	AttentionKind  string
	Experimental   string
}

type AttentionFamilyPosition struct {
	SeverityRank         int
	SupportingIssueCount int
	LastObserved         time.Time
	GroupKey             string
}

type AttentionFamilyQuery struct {
	Filter              AttentionFamilyFilter
	Limit               int
	CursorEpoch         string
	Snapshot            int64
	RetentionGeneration int64
	IssuedAt            time.Time
	Cursor              *AttentionFamilyPosition
}

type AttentionFamilyPage struct {
	Data                []AttentionFamilySummary
	Analysis            IssueAnalysisCoverage
	CursorEpoch         string
	Snapshot            int64
	RetentionGeneration int64
	IssuedAt            time.Time
	HasMore             bool
}

type AttentionFamilyMemberPosition struct {
	AnalysisStatusRank int
	LastObserved       time.Time
	IssueID            string
}

type AttentionFamilyMemberQuery struct {
	FamilyID            string
	GroupKey            string
	Filter              AttentionFamilyFilter
	Limit               int
	CursorEpoch         string
	Snapshot            int64
	RetentionGeneration int64
	IssuedAt            time.Time
	Cursor              *AttentionFamilyMemberPosition
}

const AttentionFamilyMemberSessionSelectionLatest = "latest_matching_session"

type AttentionFamilyMemberRecord struct {
	IssueSummary
	SessionKey          string     `json:"session_key,omitempty"`
	SessionSelection    string     `json:"session_selection"`
	SessionStartedAt    *time.Time `json:"session_started_at,omitempty"`
	SessionLastActiveAt *time.Time `json:"session_last_active_at,omitempty"`
	CitedEventCount     int        `json:"cited_event_count"`
	EvidenceFirstAt     *time.Time `json:"evidence_first_at,omitempty"`
	EvidenceLastAt      *time.Time `json:"evidence_last_at,omitempty"`
}

type AttentionFamilyMemberPage struct {
	Family              AttentionFamilySummary
	Data                []AttentionFamilyMemberRecord
	Analysis            IssueAnalysisCoverage
	CursorEpoch         string
	Snapshot            int64
	RetentionGeneration int64
	IssuedAt            time.Time
	HasMore             bool
	Found               bool
}
