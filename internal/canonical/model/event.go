// Package model defines Belay's immutable, minimized event contract. It has no
// knowledge of acquisition, persistence, or presentation implementations.
package model

import "time"

const (
	EventSchemaVersion = "belay.event.v1"
	RedactionVersion   = "belay.redaction.v1"
	MaxEventLookupIDs  = 50
)

type Event struct {
	SchemaVersion  string      `json:"schema_version"`
	EventID        string      `json:"event_id"`
	InstallationID string      `json:"installation_id"`
	OccurredAt     time.Time   `json:"occurred_at"`
	ObservedAt     time.Time   `json:"observed_at"`
	Source         Source      `json:"source"`
	Session        SessionRef  `json:"session"`
	Observation    Observation `json:"observation"`
	Coverage       Coverage    `json:"coverage"`
	Redaction      Redaction   `json:"redaction"`
	Historical     Historical  `json:"historical"`
}

type Source struct {
	Engine           string `json:"engine"`
	EngineVersion    string `json:"engine_version"`
	SchemaVersion    string `json:"schema_version"`
	RecordType       string `json:"record_type"`
	RunID            string `json:"run_id,omitempty"`
	RecordID         string `json:"record_id"`
	Kind             string `json:"kind"`
	Agent            string `json:"agent"`
	AdapterVersion   string `json:"adapter_version"`
	DeduplicationKey string `json:"deduplication_key"`
	Sequence         int64  `json:"sequence"`
}

type SessionRef struct {
	Key string `json:"key"`
}

type Observation struct {
	Type       string    `json:"type"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	Outcome    string    `json:"outcome"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	DurationMS *int64    `json:"duration_ms,omitempty"`
	Summary    string    `json:"summary,omitempty"`
	Resource   *Resource `json:"resource,omitempty"`
	Details    *Details  `json:"details,omitempty"`
}

type Resource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Details struct {
	ToolCallID       string   `json:"tool_call_id,omitempty"`
	Decision         string   `json:"decision,omitempty"`
	ApprovalRequired *bool    `json:"approval_required,omitempty"`
	ApprovalDecision string   `json:"approval_decision,omitempty"`
	MCPServer        string   `json:"mcp_server,omitempty"`
	MCPTool          string   `json:"mcp_tool,omitempty"`
	Model            string   `json:"model,omitempty"`
	ModelProvider    string   `json:"model_provider,omitempty"`
	CLIVersion       string   `json:"cli_version,omitempty"`
	SubAgent         string   `json:"sub_agent,omitempty"`
	DiffSHA256       string   `json:"diff_sha256,omitempty"`
	DiffBytes        int      `json:"diff_bytes,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

type Coverage struct {
	Depth      string `json:"depth"`
	Confidence string `json:"confidence"`
}

type Redaction struct {
	PolicyVersion  string `json:"policy_version"`
	FieldsRemoved  int    `json:"fields_removed"`
	SecretsRemoved int    `json:"secrets_removed"`
}

type Historical struct {
	IsHistorical         bool   `json:"is_historical"`
	ReconstructionSource string `json:"reconstruction_source,omitempty"`
}

type SessionSummary struct {
	SessionID  string           `json:"session_id"`
	Harness    string           `json:"harness"`
	StartedAt  time.Time        `json:"started_at"`
	EndedAt    time.Time        `json:"ended_at"`
	EventCount int              `json:"event_count"`
	Outcome    string           `json:"outcome"`
	Historical bool             `json:"historical"`
	History    string           `json:"history"`
	Overview   *SessionOverview `json:"overview,omitempty"`
	// Project, DurationMS, and CostUSD are joined from the encrypted
	// transcript sidecar when one exists for the session. Project is a safe
	// display label, never a path or remote.
	Project    string   `json:"project,omitempty"`
	DurationMS int64    `json:"duration_ms,omitempty"`
	CostUSD    *float64 `json:"cost_usd,omitempty"`
}

type SessionOverview struct {
	Counts                    SessionInsightCounts `json:"counts"`
	SalientResources          []SalientResource    `json:"salient_resources"`
	SalientResourcesTruncated bool                 `json:"salient_resources_truncated"`
	ObservedCoverage          ObservedCoverage     `json:"observed_coverage"`
	Outcome                   OutcomeExplanation   `json:"outcome"`
}

type SessionInsightCounts struct {
	Commands                 int `json:"commands"`
	ToolCalls                int `json:"tool_calls"`
	FileReads                int `json:"file_reads"`
	FileWrites               int `json:"file_writes"`
	FileDeletes              int `json:"file_deletes"`
	NetworkIndicators        int `json:"network_indicators"`
	PermissionEvents         int `json:"permission_events"`
	ExplicitFailedEvents     int `json:"explicit_failed_events"`
	SourceUnreportedOutcomes int `json:"source_unreported_outcomes"`
	Findings                 int `json:"findings"`
}

type SalientResource struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	EventCount int    `json:"event_count"`
}

type ObservedCoverage struct {
	Depths      []string `json:"depths"`
	Confidences []string `json:"confidences"`
}

type OutcomeExplanation struct {
	Value       string `json:"value"`
	Source      string `json:"source"`
	Explanation string `json:"explanation"`
}

type FindingSummary struct {
	FindingID        string    `json:"finding_id"`
	SessionID        string    `json:"session_id"`
	ProjectScopeHint string    `json:"-"`
	DetectedAt       time.Time `json:"detected_at"`
	RuleID           string    `json:"rule_id"`
	RuleVersion      string    `json:"rule_version"`
	Severity         string    `json:"severity"`
	Harness          string    `json:"harness"`
	Confidence       string    `json:"confidence"`
	CitedEventIDs    []string  `json:"cited_event_ids"`
}

type ActivityFilter struct {
	OccurredAfter  *time.Time
	OccurredBefore *time.Time
	Harness        string
	ResourceKind   string
	Outcome        string
	Limit          int
}

type SessionQuery struct {
	Limit           int
	Snapshot        int64
	CursorEndedAt   *time.Time
	CursorSessionID string
	Harness         string
	Outcome         string
	History         string
	OccurredAfter   *time.Time
	OccurredBefore  *time.Time
	Search          string
}

type SessionPage struct {
	Data        []SessionSummary
	Snapshot    int64
	DataThrough time.Time
}

type EventPosition struct {
	OccurredAt     time.Time
	SourceSequence int64
	EventID        string
}

type TimelineQuery struct {
	SessionID string
	Limit     int
	Snapshot  int64
	Cursor    *EventPosition
}

type EventPage struct {
	Data        []Event
	Snapshot    int64
	DataThrough time.Time
}

type EventLookupQuery struct {
	SessionID string
	EventIDs  []string
}

type EventLookupResult struct {
	Data            []Event   `json:"data"`
	RequestedCount  int       `json:"requested_count"`
	FoundCount      int       `json:"found_count"`
	MissingCount    int       `json:"missing_count"`
	MissingEventIDs []string  `json:"missing_event_ids"`
	DataThrough     time.Time `json:"data_through"`
}

type EventLookupSummary struct {
	RequestedCount  int       `json:"requested_count"`
	FoundCount      int       `json:"found_count"`
	MissingCount    int       `json:"missing_count"`
	MissingEventIDs []string  `json:"missing_event_ids"`
	DataThrough     time.Time `json:"data_through"`
}

type ActivityQuery struct {
	Filter   ActivityFilter
	Snapshot int64
	Cursor   *EventPosition
}

type FindingFilter struct {
	DetectedAfter *time.Time
	Severity      string
	SessionID     string
	Limit         int
}

type FindingPosition struct {
	DetectedAt time.Time
	FindingID  string
}

type FindingQuery struct {
	Filter   FindingFilter
	Snapshot int64
	Cursor   *FindingPosition
}

type FindingPage struct {
	Data        []FindingSummary
	Snapshot    int64
	DataThrough time.Time
}

type LocalStats struct {
	EventCount     int            `json:"event_count"`
	SessionCount   int            `json:"session_count"`
	FindingCount   int            `json:"finding_count"`
	HarnessCounts  map[string]int `json:"harness_counts"`
	OutcomeCounts  map[string]int `json:"outcome_counts"`
	HistoricalRuns int            `json:"historical_sessions"`
}
