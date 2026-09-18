// Package numbat defines the strict, versioned process boundary between stock
// Numbat and Belay. It deliberately contains no persistence or presentation
// code.
package numbat

const (
	SchemaVersion  = "0.3.0"
	AdapterVersion = "numbat-0.3.0/v1"
	ResearchCommit = "f0778c09dc48281aa93a3887d05096c0a1f3f9f7"
)

type Endpoint struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Username string `json:"username"`
	UID      string `json:"uid"`
	DeviceID string `json:"device_id,omitempty"`
}

type Evidence struct {
	ArtifactType string `json:"artifact_type"`
	LocalPath    string `json:"local_path,omitempty"`
	Line         int    `json:"line,omitempty"`
	RowID        int64  `json:"rowid,omitempty"`
	JSONPointer  string `json:"json_pointer,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
}

type EventRecord struct {
	SchemaVersion string   `json:"schema_version"`
	RecordType    string   `json:"record_type"`
	RunID         string   `json:"run_id"`
	Endpoint      Endpoint `json:"endpoint"`
	CaseID        string   `json:"case_id,omitempty"`
	EventID       string   `json:"event_id"`
	SourceAgent   string   `json:"source_agent"`
	SourceType    string   `json:"source_type"`
	Timestamp     string   `json:"timestamp,omitempty"`
	ProjectPath   string   `json:"project_path,omitempty"`
	SessionID     string   `json:"session_id,omitempty"`
	Actor         string   `json:"actor,omitempty"`
	EventType     string   `json:"event_type"`

	ToolName         string `json:"tool_name,omitempty"`
	Command          string `json:"command,omitempty"`
	FilePath         string `json:"file_path,omitempty"`
	Decision         string `json:"decision,omitempty"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
	DiffSHA256       string `json:"diff_sha256,omitempty"`
	DiffBytes        int    `json:"diff_bytes,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	DurationMS       *int64 `json:"duration_ms,omitempty"`
	ApprovalRequired *bool  `json:"approval_required,omitempty"`
	ApprovalDecision string `json:"approval_decision,omitempty"`
	ApprovalReason   string `json:"approval_reason,omitempty"`
	MCPServer        string `json:"mcp_server,omitempty"`
	MCPTool          string `json:"mcp_tool,omitempty"`
	URL              string `json:"url,omitempty"`
	Model            string `json:"model,omitempty"`
	ModelProvider    string `json:"model_provider,omitempty"`
	GitBranch        string `json:"git_branch,omitempty"`
	Entrypoint       string `json:"entrypoint,omitempty"`
	CLIVersion       string `json:"cli_version,omitempty"`
	SubAgent         string `json:"sub_agent,omitempty"`

	ContentPreview          string   `json:"content_preview,omitempty"`
	ContentPreviewTruncated bool     `json:"content_preview_truncated,omitempty"`
	Content                 string   `json:"content,omitempty"`
	ContentBytes            int      `json:"content_bytes,omitempty"`
	ContentTruncated        bool     `json:"content_truncated,omitempty"`
	Tags                    []string `json:"tags,omitempty"`
	Confidence              string   `json:"confidence"`
	Evidence                Evidence `json:"evidence"`
}

type FindingRecord struct {
	SchemaVersion   string   `json:"schema_version"`
	RecordType      string   `json:"record_type"`
	RunID           string   `json:"run_id"`
	Endpoint        Endpoint `json:"endpoint"`
	FindingID       string   `json:"finding_id"`
	CaseID          string   `json:"case_id,omitempty"`
	Timestamp       string   `json:"timestamp,omitempty"`
	DetectedAt      string   `json:"detected_at"`
	RuleID          string   `json:"rule_id"`
	RuleVersion     string   `json:"rule_version"`
	Severity        string   `json:"severity"`
	SourceAgent     string   `json:"source_agent"`
	SourceType      string   `json:"source_type"`
	ProjectPathHash string   `json:"project_path_hash,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	Model           string   `json:"model,omitempty"`
	ModelProvider   string   `json:"model_provider,omitempty"`
	SubAgent        string   `json:"sub_agent,omitempty"`
	Title           string   `json:"title"`

	ObservedEventType               string     `json:"observed_event_type,omitempty"`
	ObservedActor                   string     `json:"observed_actor,omitempty"`
	ObservedCommand                 string     `json:"observed_command,omitempty"`
	ObservedFilePath                string     `json:"observed_file_path,omitempty"`
	ObservedURL                     string     `json:"observed_url,omitempty"`
	ObservedMCPServer               string     `json:"observed_mcp_server,omitempty"`
	ObservedMCPTool                 string     `json:"observed_mcp_tool,omitempty"`
	ObservedContentPreview          string     `json:"observed_content_preview,omitempty"`
	ObservedContentPreviewTruncated bool       `json:"observed_content_preview_truncated,omitempty"`
	Tags                            []string   `json:"tags,omitempty"`
	EvidenceRefs                    []Evidence `json:"evidence_refs"`
	CitedEventIDs                   []string   `json:"cited_event_ids"`
	Redacted                        bool       `json:"redacted"`
	Confidence                      string     `json:"confidence"`
}

type ScanSummaryRecord struct {
	SchemaVersion     string   `json:"schema_version"`
	RecordType        string   `json:"record_type"`
	RunID             string   `json:"run_id"`
	Endpoint          Endpoint `json:"endpoint"`
	Status            string   `json:"status"`
	ArtifactsScanned  int      `json:"artifacts_scanned"`
	EventsEmitted     int      `json:"events_emitted"`
	FindingsEmitted   int      `json:"findings_emitted"`
	IndicatorsEmitted int      `json:"indicators_emitted"`
	Diagnostics       int      `json:"diagnostics"`
	HTTPBatchesSent   *int     `json:"http_batches_sent,omitempty"`
	HTTPRecordsSent   *int     `json:"http_records_sent,omitempty"`
	HTTPLastStatus    *int     `json:"http_last_status,omitempty"`
	HTTPFailed        *bool    `json:"http_failed,omitempty"`
}

type DiagnosticRecord struct {
	SchemaVersion string   `json:"schema_version"`
	RecordType    string   `json:"record_type"`
	RunID         string   `json:"run_id"`
	Endpoint      Endpoint `json:"endpoint"`
	Timestamp     string   `json:"timestamp"`
	Level         string   `json:"level"`
	Message       string   `json:"message"`
}

type IndicatorRecord struct {
	SchemaVersion         string   `json:"schema_version"`
	RecordType            string   `json:"record_type"`
	RunID                 string   `json:"run_id"`
	Endpoint              Endpoint `json:"endpoint"`
	Type                  string   `json:"type"`
	Value                 string   `json:"value"`
	Count                 int      `json:"count"`
	FirstSeen             string   `json:"first_seen,omitempty"`
	LastSeen              string   `json:"last_seen,omitempty"`
	SourceAgent           string   `json:"source_agent,omitempty"`
	SampleEventID         string   `json:"sample_event_id,omitempty"`
	SampleSessionID       string   `json:"sample_session_id,omitempty"`
	SampleProjectPathHash string   `json:"sample_project_path_hash,omitempty"`
}

type EnforcementRecord struct {
	SchemaVersion   string   `json:"schema_version"`
	RecordType      string   `json:"record_type"`
	RunID           string   `json:"run_id"`
	Endpoint        Endpoint `json:"endpoint"`
	DecisionID      string   `json:"decision_id"`
	CaseID          string   `json:"case_id,omitempty"`
	Timestamp       string   `json:"timestamp"`
	Decision        string   `json:"decision"`
	Mode            string   `json:"mode"`
	Reason          string   `json:"reason"`
	SourceAgent     string   `json:"source_agent"`
	SourceType      string   `json:"source_type"`
	SessionID       string   `json:"session_id,omitempty"`
	Model           string   `json:"model,omitempty"`
	ModelProvider   string   `json:"model_provider,omitempty"`
	SubAgent        string   `json:"sub_agent,omitempty"`
	ToolName        string   `json:"tool_name,omitempty"`
	ToolCallID      string   `json:"tool_call_id,omitempty"`
	ActionEventIDs  []string `json:"action_event_ids"`
	FindingIDs      []string `json:"finding_ids,omitempty"`
	RuleIDs         []string `json:"rule_ids"`
	DenyRuleID      string   `json:"deny_rule_id,omitempty"`
	DenyRuleVersion string   `json:"deny_rule_version,omitempty"`
}
