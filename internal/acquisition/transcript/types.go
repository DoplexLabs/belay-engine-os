// Package transcript reads native Claude Code, Codex and Cursor JSONL
// transcripts into Belay Local's encrypted transcript sidecar model.
package transcript

import (
	"encoding/json"
	"time"

	belaytranscript "github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	AgentClaude = "claude-code"
	AgentCodex  = "codex"
	AgentCursor = "cursor"
)

type Source struct {
	Agent           string
	Path            string
	GroupKey        string
	NativeSessionID string
	Primary         bool
	// ProjectKey is an opaque per-project key taken from the harness directory
	// layout when the layout names one. Cursor stores transcripts under
	// ~/.cursor/projects/<hash>/, and that <hash> is not documented as a
	// reversible encoding of the workspace path, so Belay never decodes it: it
	// is carried only to keep transcripts of different projects in distinct
	// groups. The real working directory comes from the records themselves.
	ProjectKey string
}

type State struct {
	NativeSessionID         string                       `json:"native_session_id,omitempty"`
	NativeIdentityConfirmed bool                         `json:"native_identity_confirmed,omitempty"`
	ProjectPath             string                       `json:"project_path,omitempty"`
	GitRemoteURL            string                       `json:"git_remote_url,omitempty"`
	GitBranch               string                       `json:"git_branch,omitempty"`
	Model                   string                       `json:"model,omitempty"`
	CurrentTurnID           string                       `json:"current_turn_id,omitempty"`
	CurrentThreadID         string                       `json:"current_thread_id,omitempty"`
	ParentThreadID          string                       `json:"parent_thread_id,omitempty"`
	CallTools               map[string]string            `json:"call_tools,omitempty"`
	ThreadParentTool        map[string]string            `json:"thread_parent_tool,omitempty"`
	ResultCalls             map[string]bool              `json:"result_calls,omitempty"`
	ResponseMessages        map[string]bool              `json:"response_messages,omitempty"`
	EventMessageFallbacks   map[string]bool              `json:"event_message_fallbacks,omitempty"`
	PendingCodexUsage       map[string]PendingCodexUsage `json:"pending_codex_usage,omitempty"`
	FinalizedCodexUsage     map[string]bool              `json:"finalized_codex_usage,omitempty"`
}

type PendingCodexUsage struct {
	TurnID           string    `json:"turn_id"`
	OccurredAt       time.Time `json:"occurred_at"`
	JSONLByteOffset  int64     `json:"jsonl_byte_offset"`
	Model            string    `json:"model,omitempty"`
	CWD              string    `json:"cwd,omitempty"`
	GitBranch        string    `json:"git_branch,omitempty"`
	ParentToolUseID  string    `json:"parent_tool_use_id,omitempty"`
	InputTokens      *int64    `json:"input_tokens,omitempty"`
	OutputTokens     *int64    `json:"output_tokens,omitempty"`
	CacheReadTokens  *int64    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64    `json:"cache_write_tokens,omitempty"`
}

type Boundary struct {
	SessionKey   func(agent, nativeSessionID string) string
	ScrubSecrets func(string) (string, int)
}

type ParseOptions struct {
	State                  State
	ParentToolUses         map[string]string
	Boundary               Boundary
	FinalizePendingUsage   bool
	SourceRecordKeyVersion string
}

type Result struct {
	State                  State
	Turns                  []belaytranscript.Turn
	EndOffset              int64
	Complete               bool
	Issues                 int
	AssistantToolUseByUUID map[string]string
}

type usage struct {
	InputTokens       *int64
	OutputTokens      *int64
	CacheReadTokens   *int64
	CacheWriteTokens  *int64
	CacheWrite5M      *int64
	CacheWrite1H      *int64
	ReasoningTokens   *int64
	InputIncludesRead bool
}

type jsonObject map[string]json.RawMessage
