// Package issueintel defines Belay Local's cost-ranked transcript issue model.
package issueintel

import (
	"time"

	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	DetectorRetryLoop                  = "retry_loop"
	DetectorRecurringError             = "recurring_error"
	DetectorRepeatedCorrection         = "repeated_correction"
	DetectorDoneWithoutVerification    = "done_without_verification"
	DetectorPermissionChurn            = "permission_churn"
	DetectorColdStartCost              = "cold_start_cost"
	DetectorFileThrash                 = "file_thrash"
	DetectorCompactionBeforeCompletion = "compaction_before_completion"
)

type Cost struct {
	WastedMinutes float64  `json:"wasted_minutes"`
	WastedTokens  int64    `json:"wasted_tokens"`
	WastedUSD     *float64 `json:"wasted_usd"`
	LowerBound    bool     `json:"lower_bound"`
}

type Project struct {
	Identity string `json:"identity"`
	Path     string `json:"path"`
}

type SessionRef struct {
	SessionKey string    `json:"session_key"`
	Agent      string    `json:"agent"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at"`
}

type Citation struct {
	SessionKey      string    `json:"session_key"`
	TurnIndex       int64     `json:"turn_index"`
	OccurredAt      time.Time `json:"occurred_at"`
	SourceFileID    string    `json:"source_file_id"`
	JSONLByteOffset int64     `json:"jsonl_byte_offset"`
}

type Excerpt struct {
	Citation Citation        `json:"citation"`
	Role     transcript.Role `json:"role"`
	ToolName string          `json:"tool_name,omitempty"`
	Text     string          `json:"text"`
}

type TrendWeek struct {
	WeekStart time.Time `json:"week_start"`
	Count     int       `json:"count"`
}

type SuggestedFix struct {
	Kind       string `json:"kind"`
	TargetFile string `json:"target_file"`
	Rationale  string `json:"rationale"`
}

type Issue struct {
	IssueID      string       `json:"issue_id"`
	DetectorID   string       `json:"detector_id"`
	Fingerprint  string       `json:"fingerprint"`
	Headline     string       `json:"headline"`
	Cost         Cost         `json:"cost"`
	SessionCount int          `json:"session_count"`
	Sessions     []SessionRef `json:"sessions"`
	FirstSeen    time.Time    `json:"first_seen"`
	LastSeen     time.Time    `json:"last_seen"`
	Trend        []TrendWeek  `json:"trend"`
	Excerpts     []Excerpt    `json:"excerpts"`
	Project      Project      `json:"project"`
	SuggestedFix SuggestedFix `json:"suggested_fix"`
}

type ProjectConfig struct {
	VerificationCommands  []string `json:"verification_commands,omitempty"`
	HasClaudeInstructions bool     `json:"has_claude_instructions"`
	HasCodexInstructions  bool     `json:"has_codex_instructions"`
}

type Session struct {
	Metadata transcript.Session `json:"metadata"`
	Turns    []transcript.Turn  `json:"turns"`
}

type ProjectInput struct {
	Project  Project       `json:"project"`
	Config   ProjectConfig `json:"config"`
	Sessions []Session     `json:"sessions"`
	Now      time.Time     `json:"now"`
}

type CorrectionCandidate struct {
	CandidateID string    `json:"candidate_id"`
	Project     Project   `json:"project"`
	Citation    Citation  `json:"citation"`
	Text        string    `json:"text"`
	Marker      string    `json:"marker,omitempty"`
	ShortTurn   bool      `json:"short_turn"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type Analysis struct {
	Issues               []Issue               `json:"issues"`
	CorrectionCandidates []CorrectionCandidate `json:"correction_candidates"`
	AttributedCost       Cost                  `json:"attributed_cost"`
}

type Query struct {
	Limit           int
	ProjectIdentity string
	DetectorID      string
}

type SemanticInput struct {
	Project              Project               `json:"project"`
	Issues               []Issue               `json:"issues"`
	CorrectionCandidates []CorrectionCandidate `json:"correction_candidates"`
}

type InsightCluster struct {
	CandidateIDs []string `json:"candidate_ids"`
	Topic        string   `json:"topic"`
	RuleText     string   `json:"rule_text"`
	TargetFile   string   `json:"target_file"`
	Confidence   float64  `json:"confidence"`
}

type InsightFix struct {
	IssueID    string  `json:"issue_id"`
	RuleText   string  `json:"rule_text"`
	TargetFile string  `json:"target_file"`
	Confidence float64 `json:"confidence"`
}

type InsightResult struct {
	Clusters []InsightCluster `json:"clusters"`
	Fixes    []InsightFix     `json:"fixes"`
}

type InsightSanitization struct {
	DroppedClusters            int `json:"dropped_clusters,omitempty"`
	DroppedFixes               int `json:"dropped_fixes,omitempty"`
	InvalidClusterFields       int `json:"invalid_cluster_fields,omitempty"`
	UnknownClusterCandidates   int `json:"unknown_cluster_candidates,omitempty"`
	AmbiguousClusterCandidates int `json:"ambiguous_cluster_candidates,omitempty"`
	InvalidFixFields           int `json:"invalid_fix_fields,omitempty"`
	UnknownFixIssues           int `json:"unknown_fix_issues,omitempty"`
	AmbiguousFixIssues         int `json:"ambiguous_fix_issues,omitempty"`
}

type InsightRecord struct {
	InsightID     string              `json:"insight_id"`
	Project       Project             `json:"project"`
	Harness       string              `json:"harness"`
	Model         string              `json:"model,omitempty"`
	PromptVersion string              `json:"prompt_version"`
	InputHash     string              `json:"input_hash"`
	GeneratedAt   time.Time           `json:"generated_at"`
	Result        InsightResult       `json:"result"`
	Sanitization  InsightSanitization `json:"sanitization,omitempty"`
}

type FixRecord struct {
	FixID         string     `json:"fix_id"`
	IssueID       string     `json:"issue_id"`
	Project       Project    `json:"project"`
	Kind          string     `json:"kind"`
	TargetFile    string     `json:"target_file"`
	RuleText      string     `json:"rule_text"`
	UnifiedDiff   string     `json:"unified_diff"`
	State         string     `json:"state"`
	ContentSHA256 string     `json:"content_sha256,omitempty"`
	AppliedPath   string     `json:"applied_path,omitempty"`
	GitCommit     string     `json:"git_commit,omitempty"`
	ProposedAt    time.Time  `json:"proposed_at"`
	AppliedAt     *time.Time `json:"applied_at,omitempty"`
}

type FixStatus struct {
	Fix                 FixRecord `json:"fix"`
	Applied             bool      `json:"applied"`
	VerificationState   string    `json:"verification_state"`
	StillPresent        *bool     `json:"still_present"`
	Reverted            *bool     `json:"reverted"`
	RecurrenceSinceFix  *int      `json:"recurrence_since_applied"`
	WeeklyCostBeforeUSD *float64  `json:"weekly_cost_before_usd"`
	WeeklyCostAfterUSD  *float64  `json:"weekly_cost_after_usd"`
}

type UsageTotals struct {
	SessionCount           int        `json:"session_count"`
	WallDurationMS         int64      `json:"wall_duration_ms"`
	WallDurationLowerBound bool       `json:"wall_duration_lower_bound"`
	TotalTokens            int64      `json:"total_tokens"`
	TokensLowerBound       bool       `json:"tokens_lower_bound"`
	TotalCostUSD           float64    `json:"total_cost_usd"`
	CostLowerBound         bool       `json:"cost_lower_bound"`
	Harnesses              []string   `json:"harnesses"`
	FirstActivityAt        *time.Time `json:"first_activity_at,omitempty"`
	LastActivityAt         *time.Time `json:"last_activity_at,omitempty"`
}

type UsageWeek struct {
	WeekStart              time.Time `json:"week_start"`
	SessionCount           int       `json:"session_count"`
	WallDurationMS         int64     `json:"wall_duration_ms"`
	WallDurationLowerBound bool      `json:"wall_duration_lower_bound"`
	TotalTokens            int64     `json:"total_tokens"`
	TokensLowerBound       bool      `json:"tokens_lower_bound"`
	TotalCostUSD           float64   `json:"total_cost_usd"`
	CostLowerBound         bool      `json:"cost_lower_bound"`
}

type UsageSnapshot struct {
	Totals   UsageTotals               `json:"totals"`
	Weeks    []UsageWeek               `json:"weeks"`
	Coverage transcript.CoverageCounts `json:"coverage"`
}

type IssueCostTotals struct {
	AttributedUSD float64 `json:"attributed_usd"`
	LowerBound    bool    `json:"lower_bound"`
	IssueCount    int     `json:"issue_count"`
}
