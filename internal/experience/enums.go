// Package experience defines Belay's pure, versioned experience contracts.
// It deliberately has no persistence, filesystem, process, network, HTTP, or
// MCP dependencies.
package experience

type ExperienceType string

const (
	ExperiencePreference ExperienceType = "preference"
	ExperienceProcedure  ExperienceType = "procedure"
	ExperienceWarning    ExperienceType = "warning"
	ExperienceConstraint ExperienceType = "constraint"
	ExperienceFact       ExperienceType = "fact"
)

func (value ExperienceType) Valid() bool {
	switch value {
	case ExperiencePreference, ExperienceProcedure, ExperienceWarning, ExperienceConstraint, ExperienceFact:
		return true
	default:
		return false
	}
}

type CandidateFamily string

const (
	CandidateCorrection          CandidateFamily = "correction"
	CandidateSuccessfulProcedure CandidateFamily = "successful_procedure"
	CandidateFailedApproach      CandidateFamily = "failed_approach"
)

func (value CandidateFamily) Valid() bool {
	switch value {
	case CandidateCorrection, CandidateSuccessfulProcedure, CandidateFailedApproach:
		return true
	default:
		return false
	}
}

type EvidenceTurnRole string

const (
	EvidenceTurnUser              EvidenceTurnRole = "user"
	EvidenceTurnAssistant         EvidenceTurnRole = "assistant"
	EvidenceTurnToolCall          EvidenceTurnRole = "tool_call"
	EvidenceTurnToolResult        EvidenceTurnRole = "tool_result"
	EvidenceTurnSystem            EvidenceTurnRole = "system"
	EvidenceTurnCompactionSummary EvidenceTurnRole = "compaction_summary"
)

func (value EvidenceTurnRole) Valid() bool {
	switch value {
	case EvidenceTurnUser,
		EvidenceTurnAssistant,
		EvidenceTurnToolCall,
		EvidenceTurnToolResult,
		EvidenceTurnSystem,
		EvidenceTurnCompactionSummary:
		return true
	default:
		return false
	}
}

type LifecycleState string

const (
	LifecycleCandidate    LifecycleState = "candidate"
	LifecycleApproved     LifecycleState = "approved"
	LifecycleActive       LifecycleState = "active"
	LifecyclePaused       LifecycleState = "paused"
	LifecycleContradicted LifecycleState = "contradicted"
	LifecycleSuperseded   LifecycleState = "superseded"
	LifecycleExpired      LifecycleState = "expired"
	LifecycleRejected     LifecycleState = "rejected"
)

func (value LifecycleState) Valid() bool {
	switch value {
	case LifecycleCandidate,
		LifecycleApproved,
		LifecycleActive,
		LifecyclePaused,
		LifecycleContradicted,
		LifecycleSuperseded,
		LifecycleExpired,
		LifecycleRejected:
		return true
	default:
		return false
	}
}

type InterventionStrength string

const (
	InterventionObserve             InterventionStrength = "observe"
	InterventionAdvise              InterventionStrength = "advise"
	InterventionClarify             InterventionStrength = "clarify"
	InterventionRequireVerification InterventionStrength = "require_verification"
	InterventionDeny                InterventionStrength = "deny"
)

func (value InterventionStrength) Valid() bool {
	switch value {
	case InterventionObserve,
		InterventionAdvise,
		InterventionClarify,
		InterventionRequireVerification,
		InterventionDeny:
		return true
	default:
		return false
	}
}

type InstructionAuthority string

const (
	AuthorityNone         InstructionAuthority = "none"
	AuthorityUserApproved InstructionAuthority = "user_approved"
)

func (value InstructionAuthority) Valid() bool {
	return value == AuthorityNone || value == AuthorityUserApproved
}

type ScopeKind string

const (
	ScopeProject ScopeKind = "project"
	ScopeSession ScopeKind = "session"
)

func (value ScopeKind) Valid() bool {
	return value == ScopeProject || value == ScopeSession
}

type Harness string

const (
	HarnessClaude Harness = "claude"
	HarnessCodex  Harness = "codex"
)

func (value Harness) Valid() bool {
	return value == HarnessClaude || value == HarnessCodex
}

type SemanticDisposition string

const (
	SemanticDispositionPropose SemanticDisposition = "propose"
	SemanticDispositionReject  SemanticDisposition = "reject"
	SemanticDispositionDefer   SemanticDisposition = "defer"
)

func (value SemanticDisposition) Valid() bool {
	switch value {
	case SemanticDispositionPropose,
		SemanticDispositionReject,
		SemanticDispositionDefer:
		return true
	default:
		return false
	}
}

type SemanticDecisionReasonCode string

const (
	SemanticReasonReusableSupported       SemanticDecisionReasonCode = "reusable_supported"
	SemanticReasonTemporaryOrTaskSpecific SemanticDecisionReasonCode = "temporary_or_task_specific"
	SemanticReasonInsufficientContext     SemanticDecisionReasonCode = "insufficient_context"
	SemanticReasonUnsafeOrOverbroad       SemanticDecisionReasonCode = "unsafe_or_overbroad"
	SemanticReasonNotReusable             SemanticDecisionReasonCode = "not_reusable"
)

func (value SemanticDecisionReasonCode) Valid() bool {
	switch value {
	case SemanticReasonReusableSupported,
		SemanticReasonTemporaryOrTaskSpecific,
		SemanticReasonInsufficientContext,
		SemanticReasonUnsafeOrOverbroad,
		SemanticReasonNotReusable:
		return true
	default:
		return false
	}
}

type DeterministicConditionKind string

const (
	ConditionEventType          DeterministicConditionKind = "event_type"
	ConditionToolName           DeterministicConditionKind = "tool_name"
	ConditionCommandClass       DeterministicConditionKind = "command_class"
	ConditionPathPattern        DeterministicConditionKind = "path_pattern"
	ConditionLifecyclePhase     DeterministicConditionKind = "lifecycle_phase"
	ConditionPriorEventSequence DeterministicConditionKind = "prior_event_sequence"
)

func (value DeterministicConditionKind) Valid() bool {
	switch value {
	case ConditionEventType,
		ConditionToolName,
		ConditionCommandClass,
		ConditionPathPattern,
		ConditionLifecyclePhase,
		ConditionPriorEventSequence:
		return true
	default:
		return false
	}
}

type EvidenceSourceKind string

const (
	EvidenceTranscriptTurn     EvidenceSourceKind = "transcript_turn"
	EvidenceCanonicalEvent     EvidenceSourceKind = "canonical_event"
	EvidenceOutcomeObservation EvidenceSourceKind = "outcome_observation"
	EvidenceWorkspaceHash      EvidenceSourceKind = "workspace_hash"
	EvidenceUserRecorded       EvidenceSourceKind = "user_recorded_outcome"
)

func (value EvidenceSourceKind) Valid() bool {
	switch value {
	case EvidenceTranscriptTurn,
		EvidenceCanonicalEvent,
		EvidenceOutcomeObservation,
		EvidenceWorkspaceHash,
		EvidenceUserRecorded:
		return true
	default:
		return false
	}
}

type EvidenceAvailability string

const (
	EvidenceAvailable        EvidenceAvailability = "available"
	EvidencePartial          EvidenceAvailability = "partial"
	EvidencePruned           EvidenceAvailability = "pruned"
	EvidenceRetainedSnapshot EvidenceAvailability = "retained_snapshot"
)

func (value EvidenceAvailability) Valid() bool {
	switch value {
	case EvidenceAvailable, EvidencePartial, EvidencePruned, EvidenceRetainedSnapshot:
		return true
	default:
		return false
	}
}

type ApprovalMode string

const (
	ApprovalAsProposed ApprovalMode = "as_proposed"
	ApprovalNarrowed   ApprovalMode = "narrowed"
	ApprovalUserEdited ApprovalMode = "user_edited"
)

func (value ApprovalMode) Valid() bool {
	return value == ApprovalAsProposed || value == ApprovalNarrowed || value == ApprovalUserEdited
}

type TransitionActorKind string

const (
	ActorUser                TransitionActorKind = "user"
	ActorDeterministicWorker TransitionActorKind = "deterministic_worker"
)

func (value TransitionActorKind) Valid() bool {
	return value == ActorUser || value == ActorDeterministicWorker
}

type DeliveryKind string

const (
	DeliveryMissionPack DeliveryKind = "mission_pack"
	DeliveryHook        DeliveryKind = "hook"
)

func (value DeliveryKind) Valid() bool {
	return value == DeliveryMissionPack || value == DeliveryHook
}

type DeliveryState string

const (
	DeliveryPending      DeliveryState = "pending"
	DeliveryDelivered    DeliveryState = "delivered"
	DeliveryNotDelivered DeliveryState = "not_delivered"
	DeliveryUnknown      DeliveryState = "unknown"
)

func (value DeliveryState) Valid() bool {
	switch value {
	case DeliveryPending, DeliveryDelivered, DeliveryNotDelivered, DeliveryUnknown:
		return true
	default:
		return false
	}
}

type OpportunityState string

const (
	OpportunityObserved    OpportunityState = "observed"
	OpportunityNotObserved OpportunityState = "not_observed"
	OpportunityUnknown     OpportunityState = "unknown"
)

func (value OpportunityState) Valid() bool {
	return value == OpportunityObserved || value == OpportunityNotObserved || value == OpportunityUnknown
}

type ApplicabilityState string

const (
	ApplicabilityApplicable    ApplicabilityState = "applicable"
	ApplicabilityNotApplicable ApplicabilityState = "not_applicable"
	ApplicabilityUnknown       ApplicabilityState = "unknown"
)

func (value ApplicabilityState) Valid() bool {
	return value == ApplicabilityApplicable || value == ApplicabilityNotApplicable || value == ApplicabilityUnknown
}

type VerifierState string

const (
	VerifierNotEvaluated VerifierState = "not_evaluated"
	VerifierSatisfied    VerifierState = "satisfied"
	VerifierViolated     VerifierState = "violated"
	VerifierUnknown      VerifierState = "unknown"
)

func (value VerifierState) Valid() bool {
	switch value {
	case VerifierNotEvaluated, VerifierSatisfied, VerifierViolated, VerifierUnknown:
		return true
	default:
		return false
	}
}

type TaskOutcomeState string

const (
	TaskOutcomeNotObserved TaskOutcomeState = "not_observed"
	TaskOutcomeSucceeded   TaskOutcomeState = "succeeded"
	TaskOutcomeFailed      TaskOutcomeState = "failed"
	TaskOutcomeUnknown     TaskOutcomeState = "unknown"
)

func (value TaskOutcomeState) Valid() bool {
	switch value {
	case TaskOutcomeNotObserved, TaskOutcomeSucceeded, TaskOutcomeFailed, TaskOutcomeUnknown:
		return true
	default:
		return false
	}
}
