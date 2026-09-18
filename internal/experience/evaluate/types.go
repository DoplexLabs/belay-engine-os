// Package evaluate deterministically evaluates one delivered experience
// application from bounded, already-normalized facts.
package evaluate

import (
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const (
	Version            = "belay.experience-evaluate.v1"
	MaxFacts           = 4096
	MaxEvidencePerFact = 8
	MaxResultEvidence  = 64
	MaxResultGaps      = 32
)

type Component string

const (
	ComponentOpportunity   Component = "opportunity"
	ComponentApplicability Component = "applicability"
	ComponentVerifier      Component = "verifier"
	ComponentTaskOutcome   Component = "task_outcome"
)

type GapCode string

const (
	GapCoverageMissing          GapCode = "coverage_missing"
	GapCoverageNotDeclared      GapCode = "coverage_not_declared"
	GapFactsIncomplete          GapCode = "facts_incomplete"
	GapAmbiguousMatch           GapCode = "ambiguous_match"
	GapUnsupportedMatch         GapCode = "unsupported_match"
	GapNoOpportunityObservation GapCode = "no_opportunity_observation"
	GapNoApplicableObservation  GapCode = "no_applicability_observation"
	GapSemanticExclusion        GapCode = "semantic_exclusion_not_deterministic"
	GapNoEditObserved           GapCode = "no_edit_observed"
	GapNoTaskOutcomeObservation GapCode = "no_task_outcome_observation"
	GapObservationOnly          GapCode = "observation_only"
	GapEvidenceTruncated        GapCode = "evidence_truncated"
)

type Gap struct {
	Component     Component                             `json:"component"`
	Code          GapCode                               `json:"code"`
	Coverage      experience.CoverageRequirement        `json:"coverage,omitempty"`
	ConditionKind experience.DeterministicConditionKind `json:"condition_kind,omitempty"`
}

type Coverage struct {
	TranscriptComplete          bool               `json:"transcript_complete"`
	CanonicalEventsComplete     bool               `json:"canonical_events_complete"`
	WorkspaceStateCaptured      bool               `json:"workspace_state_captured"`
	OutcomeObservationsComplete bool               `json:"outcome_observations_complete"`
	Evidence                    []CoverageEvidence `json:"evidence,omitempty"`
}

func (value Coverage) Complete(
	requirement experience.CoverageRequirement,
) bool {
	switch requirement {
	case experience.CoverageTranscriptComplete:
		return value.TranscriptComplete
	case experience.CoverageCanonicalComplete:
		return value.CanonicalEventsComplete
	case experience.CoverageWorkspaceCaptured:
		return value.WorkspaceStateCaptured
	case experience.CoverageOutcomesComplete:
		return value.OutcomeObservationsComplete
	default:
		return false
	}
}

type CoverageEvidence struct {
	Requirement experience.CoverageRequirement `json:"requirement"`
	Evidence    []experience.EvidenceRef       `json:"evidence"`
}

type CoverageStatus struct {
	Requirement experience.CoverageRequirement `json:"requirement"`
	Complete    bool                           `json:"complete"`
}

type Context struct {
	ProjectIdentity        string                                  `json:"project_identity"`
	SessionKey             string                                  `json:"session_key"`
	Harness                experience.Harness                      `json:"harness,omitempty"`
	Model                  string                                  `json:"model,omitempty"`
	TaskFamilies           []string                                `json:"task_families,omitempty"`
	RepositoryPaths        []string                                `json:"repository_paths,omitempty"`
	CompleteConditionKinds []experience.DeterministicConditionKind `json:"complete_condition_kinds,omitempty"`
	LastTurnIndex          *int64                                  `json:"last_turn_index,omitempty"`
	AsOf                   time.Time                               `json:"as_of"`
	Evidence               []experience.EvidenceRef                `json:"evidence,omitempty"`
}

type NormalizedVerifier struct {
	DeclaredCommand         string `json:"declared_command,omitempty"`
	CommandSignature        string `json:"command_signature,omitempty"`
	CommandScrubbingVersion string `json:"command_scrubbing_version,omitempty"`
}

type FactResult string

const (
	FactResultUnknown   FactResult = "unknown"
	FactResultSucceeded FactResult = "succeeded"
	FactResultFailed    FactResult = "failed"
)

func (value FactResult) Valid() bool {
	return value == FactResultUnknown ||
		value == FactResultSucceeded ||
		value == FactResultFailed
}

type FactMeta struct {
	Order     int64                    `json:"order"`
	Ambiguous bool                     `json:"ambiguous,omitempty"`
	Evidence  []experience.EvidenceRef `json:"evidence"`
}

type OpportunityFact struct {
	FactMeta
	State experience.OpportunityState `json:"state"`
}

type ConditionFact struct {
	FactMeta
	Kind   experience.DeterministicConditionKind `json:"kind"`
	Values []string                              `json:"values"`
}

type CommandFact struct {
	FactMeta
	Signature string     `json:"signature"`
	Class     string     `json:"class,omitempty"`
	Result    FactResult `json:"result"`
}

type FileChangeFact struct {
	FactMeta
	Path string `json:"path"`
}

type VerificationFact struct {
	FactMeta
	CommandClass string     `json:"command_class"`
	Result       FactResult `json:"result"`
}

type FailureFact struct {
	FactMeta
	CommandClass      string `json:"command_class"`
	NormalizedPattern string `json:"normalized_pattern"`
}

type CorrectionFact struct {
	FactMeta
	MarkerFamily string `json:"marker_family"`
}

type TaskOutcomeFact struct {
	FactMeta
	State experience.TaskOutcomeState `json:"state"`
}

type Facts struct {
	Opportunities []OpportunityFact  `json:"opportunities,omitempty"`
	Conditions    []ConditionFact    `json:"conditions,omitempty"`
	Commands      []CommandFact      `json:"commands,omitempty"`
	FileChanges   []FileChangeFact   `json:"file_changes,omitempty"`
	Verifications []VerificationFact `json:"verifications,omitempty"`
	Failures      []FailureFact      `json:"failures,omitempty"`
	Corrections   []CorrectionFact   `json:"corrections,omitempty"`
	TaskOutcomes  []TaskOutcomeFact  `json:"task_outcomes,omitempty"`
}

type Input struct {
	Application        experience.Application `json:"application"`
	Experience         experience.Experience  `json:"experience"`
	Context            Context                `json:"context"`
	Coverage           Coverage               `json:"coverage"`
	NormalizedVerifier NormalizedVerifier     `json:"normalized_verifier"`
	Facts              Facts                  `json:"facts"`
}

type CitedEvidence struct {
	Component Component              `json:"component"`
	Ref       experience.EvidenceRef `json:"ref"`
}

type Result struct {
	DerivationVersion  string                        `json:"derivation_version"`
	OpportunityState   experience.OpportunityState   `json:"opportunity_state"`
	ApplicabilityState experience.ApplicabilityState `json:"applicability_state"`
	VerifierState      experience.VerifierState      `json:"verifier_state"`
	TaskOutcomeState   experience.TaskOutcomeState   `json:"task_outcome_state"`
	Coverage           []CoverageStatus              `json:"coverage,omitempty"`
	Gaps               []Gap                         `json:"gaps,omitempty"`
	Evidence           []CitedEvidence               `json:"evidence,omitempty"`
}
