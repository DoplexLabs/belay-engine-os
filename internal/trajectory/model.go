// Package trajectory defines Belay's pure causal-trajectory and outcome
// contracts. It connects existing records without replacing them.
package trajectory

import "time"

const (
	EdgeSchemaVersion    = "belay.trajectory-edge.v1"
	OutcomeSchemaVersion = "belay.outcome.v1"
)

type NodeKind string

const (
	NodeCanonicalEvent NodeKind = "canonical_event"
	NodeTranscriptTurn NodeKind = "transcript_turn"
	NodeOutcome        NodeKind = "outcome_observation"
	NodeExperience     NodeKind = "experience"
	NodeApplication    NodeKind = "experience_application"
	NodeGitObject      NodeKind = "git_object"
)

func (value NodeKind) Valid() bool {
	switch value {
	case NodeCanonicalEvent, NodeTranscriptTurn, NodeOutcome, NodeExperience, NodeApplication, NodeGitObject:
		return true
	default:
		return false
	}
}

type NodeRef struct {
	Kind              NodeKind `json:"kind"`
	EventID           string   `json:"event_id,omitempty"`
	SessionKey        string   `json:"session_key,omitempty"`
	TurnIndex         *int64   `json:"turn_index,omitempty"`
	OutcomeID         string   `json:"outcome_id,omitempty"`
	ExperienceID      string   `json:"experience_id,omitempty"`
	ExperienceVersion int      `json:"experience_version,omitempty"`
	ApplicationID     string   `json:"application_id,omitempty"`
	GitObject         string   `json:"git_object,omitempty"`
}

type EdgeRelation string

const (
	RelationRespondsTo            EdgeRelation = "responds_to"
	RelationInvokes               EdgeRelation = "invokes"
	RelationReturnsFor            EdgeRelation = "returns_for"
	RelationPrecedes              EdgeRelation = "precedes"
	RelationModifies              EdgeRelation = "modifies"
	RelationVerifies              EdgeRelation = "verifies"
	RelationCorrects              EdgeRelation = "corrects"
	RelationClaimsCompletionAfter EdgeRelation = "claims_completion_after"
	RelationCompactsAfter         EdgeRelation = "compacts_after"
	RelationRepairs               EdgeRelation = "repairs"
	RelationReverts               EdgeRelation = "reverts"
	RelationParentOf              EdgeRelation = "parent_of"
)

func (value EdgeRelation) Valid() bool {
	switch value {
	case RelationRespondsTo,
		RelationInvokes,
		RelationReturnsFor,
		RelationPrecedes,
		RelationModifies,
		RelationVerifies,
		RelationCorrects,
		RelationClaimsCompletionAfter,
		RelationCompactsAfter,
		RelationRepairs,
		RelationReverts,
		RelationParentOf:
		return true
	default:
		return false
	}
}

type EvidenceClass string

const (
	EvidenceObserved               EvidenceClass = "observed"
	EvidenceDeterministicInference EvidenceClass = "deterministic_inference"
	EvidenceSemanticHypothesis     EvidenceClass = "semantic_hypothesis"
)

func (value EvidenceClass) Valid() bool {
	return value == EvidenceObserved || value == EvidenceDeterministicInference || value == EvidenceSemanticHypothesis
}

type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

func (value Confidence) Valid() bool {
	return value == ConfidenceHigh || value == ConfidenceMedium || value == ConfidenceLow
}

type Edge struct {
	SchemaVersion     string        `json:"schema_version"`
	EdgeID            string        `json:"edge_id"`
	ProjectIdentity   string        `json:"project_identity"`
	SessionKey        string        `json:"session_key"`
	From              NodeRef       `json:"from_ref"`
	Relation          EdgeRelation  `json:"relation"`
	To                NodeRef       `json:"to_ref"`
	EvidenceClass     EvidenceClass `json:"evidence_class"`
	Confidence        Confidence    `json:"confidence"`
	DerivationVersion string        `json:"derivation_version"`
	SourceRefs        []NodeRef     `json:"source_refs"`
	OccurredAt        time.Time     `json:"occurred_at"`
}

type OutcomeKind string

const (
	OutcomeVerificationPass   OutcomeKind = "verification_pass"
	OutcomeVerificationFail   OutcomeKind = "verification_fail"
	OutcomeCorrection         OutcomeKind = "correction"
	OutcomeExplicitAcceptance OutcomeKind = "explicit_acceptance"
	OutcomeCommit             OutcomeKind = "commit"
	OutcomeRevert             OutcomeKind = "revert"
	OutcomeRepair             OutcomeKind = "repair"
	OutcomeRecurrence         OutcomeKind = "recurrence"
	OutcomeAbandoned          OutcomeKind = "abandoned"
)

func (value OutcomeKind) Valid() bool {
	switch value {
	case OutcomeVerificationPass,
		OutcomeVerificationFail,
		OutcomeCorrection,
		OutcomeExplicitAcceptance,
		OutcomeCommit,
		OutcomeRevert,
		OutcomeRepair,
		OutcomeRecurrence,
		OutcomeAbandoned:
		return true
	default:
		return false
	}
}

type OutcomeResult string

const (
	ResultSucceeded OutcomeResult = "succeeded"
	ResultFailed    OutcomeResult = "failed"
	ResultObserved  OutcomeResult = "observed"
	ResultUnknown   OutcomeResult = "unknown"
)

func (value OutcomeResult) Valid() bool {
	return value == ResultSucceeded || value == ResultFailed || value == ResultObserved || value == ResultUnknown
}

type Outcome struct {
	SchemaVersion     string        `json:"schema_version"`
	OutcomeID         string        `json:"outcome_id"`
	ProjectIdentity   string        `json:"project_identity"`
	SessionKey        string        `json:"session_key"`
	OccurredAt        time.Time     `json:"occurred_at"`
	Kind              OutcomeKind   `json:"kind"`
	Result            OutcomeResult `json:"result"`
	EvidenceClass     EvidenceClass `json:"evidence_class"`
	Confidence        Confidence    `json:"confidence"`
	SourceRefs        []NodeRef     `json:"source_refs"`
	DerivationVersion string        `json:"derivation_version"`
}
