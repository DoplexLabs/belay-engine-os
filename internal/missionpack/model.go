// Package missionpack defines and builds Belay Local's deterministic Mission
// Pack projection. It deliberately has no storage, filesystem, process,
// network, or harness dependencies.
package missionpack

import (
	"errors"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

var (
	ErrProjectNotFound = errors.New("Mission Pack project not found")
	ErrProjectMismatch = errors.New("Mission Pack project mismatch")
)

const (
	SchemaVersion          = "belay.mission-pack.v1"
	GeneratorVersion       = "mission-pack.det.v6"
	legacyGeneratorVersion = "mission-pack.det.v3"

	MaxKnownTraps           = 3
	MaxOperatingRules       = 3
	MaxVerificationCommands = 3
	MaxContextFacts         = 5
	MaxChecklistItems       = 6
	MaxSourcesPerItem       = 2
	MaxRenderedMarkdown     = 12 * 1024
	MaxEstimatedTokens      = 1500
)

type Intent string

const (
	IntentGeneral   Intent = "general"
	IntentDebug     Intent = "debug"
	IntentImplement Intent = "implement"
	IntentRefactor  Intent = "refactor"
	IntentReview    Intent = "review"
	IntentRelease   Intent = "release"
)

type Harness string

const (
	HarnessClaude Harness = "claude"
	HarnessCodex  Harness = "codex"
)

const (
	AnalysisStatusCurrent = "current"
	AnalysisStatusPending = "pending"
	AnalysisStatusStale   = "stale"

	TranscriptCoverageComplete = "complete"
	TranscriptCoveragePartial  = "partial"
	TranscriptCoverageLive     = "live"
	TranscriptCoverageUnknown  = "unknown"

	CanonicalFactFileWritten       = "file_written"
	CanonicalFactCommandExecutable = "command_executable"
	CanonicalFactMCPTool           = "mcp_tool"
	CanonicalFactTool              = "tool"
)

type Request struct {
	CWD         string    `json:"-"`
	IssueID     string    `json:"-"`
	Intent      Intent    `json:"-"`
	Harness     Harness   `json:"-"`
	TaskHint    string    `json:"-"`
	GeneratedAt time.Time `json:"-"`
}

type Pack struct {
	SchemaVersion        string           `json:"schema_version"`
	GeneratorVersion     string           `json:"generator_version"`
	PackID               string           `json:"pack_id"`
	GeneratedAt          time.Time        `json:"generated_at"`
	Project              Project          `json:"project"`
	Intent               Intent           `json:"intent"`
	Harness              Harness          `json:"harness,omitempty"`
	Status               string           `json:"status"`
	Trust                Trust            `json:"trust"`
	SourceState          SourceState      `json:"source_state"`
	ExperienceGeneration int64            `json:"experience_generation,omitempty"`
	Experiences          []ExperienceItem `json:"experiences,omitempty"`
	Context              Context          `json:"context"`
	KnownTraps           []GuidanceItem   `json:"known_traps"`
	OperatingRules       []GuidanceItem   `json:"operating_rules"`
	Verification         []CommandItem    `json:"verification"`
	Completion           []ChecklistItem  `json:"completion_checklist"`
	Warnings             []Warning        `json:"warnings"`
	EstimatedTokens      int              `json:"estimated_tokens"`
	Truncated            bool             `json:"truncated"`
	RenderedMarkdown     string           `json:"rendered_markdown"`
}

type Project struct {
	Label        string `json:"label"`
	IdentityKind string `json:"identity_kind"`
	Branch       string `json:"branch,omitempty"`
	Worktree     string `json:"worktree,omitempty"`
}

type ResolvedProject struct {
	Identity     string
	IdentityKind string
	Label        string
	Path         string
}

type WorkspaceSnapshot struct {
	Branch    string
	Worktree  string
	Harnesses []string
}

type Trust struct {
	InstructionAuthority string `json:"instruction_authority"`
	GuidanceState        string `json:"guidance_state"`
	EvidenceState        string `json:"evidence_state"`
	ActivationRequired   bool   `json:"activation_required"`
}

type SourceState struct {
	TranscriptGeneration int64     `json:"transcript_generation"`
	AnalyzedGeneration   int64     `json:"analyzed_generation"`
	AnalysisStatus       string    `json:"analysis_status"`
	DataThrough          time.Time `json:"data_through"`
}

type Context struct {
	Harnesses []string      `json:"harnesses"`
	Facts     []ContextFact `json:"facts"`
}

type SourceRef struct {
	Kind            string     `json:"kind"`
	IssueID         string     `json:"issue_id,omitempty"`
	InsightID       string     `json:"insight_id,omitempty"`
	CandidateID     string     `json:"candidate_id,omitempty"`
	SessionKey      string     `json:"session_key,omitempty"`
	TurnIndex       *int64     `json:"turn_index,omitempty"`
	EventID         string     `json:"event_id,omitempty"`
	ProjectFile     string     `json:"project_file,omitempty"`
	SourceSHA256    string     `json:"source_sha256,omitempty"`
	SourceFileID    string     `json:"source_file_id,omitempty"`
	JSONLByteOffset *int64     `json:"jsonl_byte_offset,omitempty"`
	ObservedAt      *time.Time `json:"observed_at,omitempty"`
}

type ExperienceItem struct {
	ExperienceID  string          `json:"experience_id"`
	Version       int             `json:"version"`
	Type          string          `json:"type"`
	Guidance      string          `json:"guidance"`
	Applicability string          `json:"applicability"`
	Exceptions    []string        `json:"exceptions,omitempty"`
	Rationale     string          `json:"rationale"`
	Verifier      VerifierSummary `json:"verifier"`
	Authority     string          `json:"authority"`
	Sources       []SourceRef     `json:"sources"`
}

type VerifierSummary struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

type GuidanceItem struct {
	ID               string      `json:"id"`
	Kind             string      `json:"kind"`
	Title            string      `json:"title"`
	Guidance         string      `json:"guidance,omitempty"`
	TargetFile       string      `json:"target_file,omitempty"`
	Confidence       *float64    `json:"confidence,omitempty"`
	SessionCount     int         `json:"session_count,omitempty"`
	WastedMinutes    float64     `json:"wasted_minutes,omitempty"`
	WastedTokens     int64       `json:"wasted_tokens,omitempty"`
	WastedUSD        *float64    `json:"wasted_usd,omitempty"`
	CostLowerBound   bool        `json:"cost_lower_bound,omitempty"`
	RequiresApproval bool        `json:"requires_approval"`
	Sources          []SourceRef `json:"sources"`
}

type CommandItem struct {
	ID               string      `json:"id"`
	Command          string      `json:"command"`
	Class            string      `json:"class,omitempty"`
	Configured       bool        `json:"configured"`
	Observed         bool        `json:"observed"`
	SuccessCount     int         `json:"success_count,omitempty"`
	LastSuccess      *time.Time  `json:"last_success,omitempty"`
	RequiresApproval bool        `json:"requires_approval"`
	Sources          []SourceRef `json:"sources"`
}

type ContextFact struct {
	ID           string      `json:"id"`
	Kind         string      `json:"kind"`
	Summary      string      `json:"summary"`
	SessionCount int         `json:"session_count,omitempty"`
	ObservedAt   *time.Time  `json:"observed_at,omitempty"`
	Sources      []SourceRef `json:"sources"`
}

type ChecklistItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Coverage struct {
	TranscriptStatus          string `json:"transcript_status"`
	CanonicalContextAvailable bool   `json:"canonical_context_available"`
}

type ObservedCommand struct {
	Command      string
	Class        string
	SuccessCount int
	LastSuccess  time.Time
	Sources      []SourceRef
}

type DiscoveredCommand struct {
	Command      string
	Class        string
	SourceFile   string
	SourceSHA256 string
}

type CanonicalFact struct {
	FactID       string
	Kind         string
	Value        string
	SessionCount int
	ObservedAt   time.Time
	Sources      []SourceRef
}

type ProjectSelector struct {
	IssueID        string
	RemoteIdentity string
	ProjectRoot    string
	ProjectPath    string
}

type Limits struct {
	IssueID      string
	Issues       int
	Sessions     int
	CommandTurns int
	Events       int
}

type EvidenceSession struct {
	SessionKey  string
	Harness     string
	ProjectPath string
	Coverage    string
	StartedAt   time.Time
	EndedAt     time.Time
}

type SuccessfulCommand struct {
	Command     string
	SucceededAt time.Time
	Source      SourceRef
}

type EvidenceSnapshot struct {
	SourceState        SourceState
	Issues             []issueintel.Issue
	Insight            *issueintel.InsightRecord
	Candidates         map[string]issueintel.CorrectionCandidate
	Sessions           []EvidenceSession
	SuccessfulCommands []SuccessfulCommand
	Facts              []CanonicalFact
	Coverage           Coverage
	InsightStale       bool
}

type BuildInput struct {
	Request              Request
	Project              ResolvedProject
	Workspace            WorkspaceSnapshot
	SourceState          SourceState
	Issues               []issueintel.Issue
	Insight              *issueintel.InsightRecord
	Candidates           map[string]issueintel.CorrectionCandidate
	Commands             []ObservedCommand
	ProjectFiles         []DiscoveredCommand
	Facts                []CanonicalFact
	Coverage             Coverage
	InsightStale         bool
	ExperienceGeneration int64
	Experiences          []ExperienceItem
}
