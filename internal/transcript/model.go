// Package transcript defines Belay Local's native transcript sidecar model.
package transcript

import (
	"encoding/json"
	"time"
)

type Role string

const (
	RoleUser              Role = "user"
	RoleAssistant         Role = "assistant"
	RoleToolCall          Role = "tool_call"
	RoleToolResult        Role = "tool_result"
	RoleSystem            Role = "system"
	RoleCompactionSummary Role = "compaction_summary"
)

type SessionCoverage string

const (
	CoverageComplete SessionCoverage = "complete"
	CoveragePartial  SessionCoverage = "partial"
	CoverageLive     SessionCoverage = "live"
)

type Payload struct {
	Text                 string          `json:"text"`
	ToolInput            json.RawMessage `json:"tool_input,omitempty"`
	ToolResult           string          `json:"tool_result"`
	RawCommand           string          `json:"raw_command"`
	CWD                  string          `json:"cwd"`
	GitBranch            string          `json:"git_branch"`
	ParentToolUseID      string          `json:"parent_tool_use_id"`
	ToolCallID           string          `json:"tool_call_id"`
	ToolIsError          *bool           `json:"tool_is_error,omitempty"`
	ExitCode             *int            `json:"exit_code,omitempty"`
	DurationMS           *int64          `json:"duration_ms,omitempty"`
	SupplementalEvidence bool            `json:"supplemental_evidence,omitempty"`
	SourceFileID         string          `json:"source_file_id"`
	ParserVersion        string          `json:"parser_version"`
	PriceTableVersion    string          `json:"price_table_version"`
	JSONLByteOffset      int64           `json:"jsonl_byte_offset"`
}

type Turn struct {
	TurnID           string    `json:"turn_id"`
	SourceRecordKey  string    `json:"source_record_key"`
	SessionKey       string    `json:"session_key"`
	TurnIndex        int64     `json:"turn_index"`
	OccurredAt       time.Time `json:"occurred_at"`
	Role             Role      `json:"role"`
	ToolName         string    `json:"tool_name"`
	Model            string    `json:"model"`
	InputTokens      *int64    `json:"input_tokens"`
	OutputTokens     *int64    `json:"output_tokens"`
	CacheReadTokens  *int64    `json:"cache_read_tokens"`
	CacheWriteTokens *int64    `json:"cache_write_tokens"`
	CostUSD          *float64  `json:"cost_usd"`
	Payload          Payload   `json:"payload"`
}

type Session struct {
	SessionKey             string          `json:"session_key"`
	Agent                  string          `json:"agent"`
	NativeSessionID        string          `json:"native_session_id"`
	ProjectPath            string          `json:"project_path"`
	GitRemoteURL           string          `json:"git_remote_url"`
	ProjectIdentity        string          `json:"project_identity"`
	StartedAt              time.Time       `json:"started_at"`
	EndedAt                time.Time       `json:"ended_at"`
	WallDurationMS         int64           `json:"wall_duration_ms"`
	TotalInputTokens       *int64          `json:"total_input_tokens"`
	TotalOutputTokens      *int64          `json:"total_output_tokens"`
	TotalTokens            *int64          `json:"total_tokens"`
	TotalCacheReadTokens   *int64          `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens  *int64          `json:"total_cache_write_tokens"`
	TotalCostUSD           *float64        `json:"total_cost_usd"`
	TurnCount              int             `json:"turn_count"`
	UserTurnCount          int             `json:"user_turn_count"`
	AssistantTurnCount     int             `json:"assistant_turn_count"`
	ToolCallCount          int             `json:"tool_call_count"`
	ToolResultCount        int             `json:"tool_result_count"`
	SystemTurnCount        int             `json:"system_turn_count"`
	CompactionSummaryCount int             `json:"compaction_summary_count"`
	Coverage               SessionCoverage `json:"coverage"`
}

type SessionQuery struct {
	Limit           int             `json:"limit"`
	Agent           string          `json:"agent,omitempty"`
	ProjectIdentity string          `json:"project_identity,omitempty"`
	Coverage        SessionCoverage `json:"coverage,omitempty"`
}

type CoverageCounts struct {
	CanonicalSessions                     int `json:"canonical_sessions"`
	TranscriptSessions                    int `json:"transcript_sessions"`
	WithTranscript                        int `json:"with_transcript"`
	WithoutTranscript                     int `json:"without_transcript"`
	Complete                              int `json:"complete"`
	Partial                               int `json:"partial"`
	Live                                  int `json:"live"`
	CanonicalCompleteWithTranscript       int `json:"canonical_complete_with_transcript"`
	CanonicalLiveWithTranscript           int `json:"canonical_live_with_transcript"`
	CanonicalCompleteOrLiveWithTranscript int `json:"canonical_complete_or_live_with_transcript"`
	CanonicalPartial                      int `json:"canonical_partial"`
	CanonicalWithoutTranscript            int `json:"canonical_without_transcript"`
	TranscriptOnly                        int `json:"transcript_only"`
}
