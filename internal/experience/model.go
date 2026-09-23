package experience

import "time"

const (
	CandidateSchemaVersion           = "belay.experience-candidate.v1"
	SemanticProposalSchemaVersion    = "belay.experience-semantic-proposal.v1"
	SemanticDecisionSchemaVersion    = "belay.experience-semantic-decision.v1"
	SemanticProposalPromptVersionV1  = "belay.experience-prompt.v1"
	SemanticProposalPromptVersionV2  = "belay.experience-prompt.v2"
	SemanticProposalPromptVersionV3  = "belay.experience-prompt.v3"
	SemanticProposalPromptVersionV4  = "belay.experience-prompt.v4"
	SemanticProposalPromptVersionV5  = "belay.experience-prompt.v5"
	SemanticProposalPromptVersionV6  = "belay.experience-prompt.v6"
	SemanticProposalPromptVersionV7  = "belay.experience-prompt.v7"
	SemanticProposalPromptVersionV8  = "belay.experience-prompt.v8"
	SemanticProposalPromptVersionV9  = "belay.experience-prompt.v9"
	SemanticProposalPromptVersionV10 = "belay.experience-prompt.v10"
	SemanticProposalPromptVersionV11 = "belay.experience-prompt.v11"
	SemanticProposalPromptVersionV12 = "belay.experience-prompt.v12"
	SemanticProposalPromptVersion    = SemanticProposalPromptVersionV12
	ExperienceSchemaVersion          = "belay.experience.v1"
	ApplicationSchemaVersion         = "belay.experience-application.v1"
	EvaluationSchemaVersion          = "belay.experience-evaluation.v1"
)

type Candidate struct {
	SchemaVersion    string               `json:"schema_version"`
	CandidateID      string               `json:"candidate_id"`
	Family           CandidateFamily      `json:"family"`
	ProjectIdentity  string               `json:"project_identity"`
	ObservedBehavior string               `json:"observed_behavior"`
	UserFeedback     string               `json:"user_feedback,omitempty"`
	Evidence         EvidenceSet          `json:"evidence"`
	OutcomeRefs      []string             `json:"outcome_refs,omitempty"`
	EpisodeRefs      []string             `json:"episode_refs,omitempty"`
	ExistingRefs     []ExperienceRef      `json:"existing_experience_refs,omitempty"`
	Proposal         ExperienceProposal   `json:"proposed_experience"`
	Provenance       Provenance           `json:"provenance"`
	Authority        InstructionAuthority `json:"instruction_authority"`
	LifecycleState   LifecycleState       `json:"lifecycle_state"`
	CreatedAt        time.Time            `json:"created_at"`
}

type ExperienceProposal struct {
	Type                       ExperienceType   `json:"type"`
	Scope                      Scope            `json:"scope"`
	Applicability              Applicability    `json:"applicability"`
	Guidance                   Guidance         `json:"guidance"`
	Verifier                   Verifier         `json:"verifier"`
	EvidenceSupport            *EvidenceSupport `json:"evidence_support,omitempty"`
	Confidence                 *float64         `json:"confidence,omitempty"`
	SemanticCompilationPending bool             `json:"semantic_compilation_pending,omitempty"`
}

type EvidenceSupport struct {
	GuidanceRefs  []string   `json:"guidance_refs"`
	ExceptionRefs [][]string `json:"exception_refs"`
	VerifierRefs  []string   `json:"verifier_refs"`
}

type SemanticProposal struct {
	SchemaVersion   string                     `json:"schema_version"`
	ProposalID      string                     `json:"proposal_id"`
	CandidateID     string                     `json:"candidate_id"`
	ProjectIdentity string                     `json:"project_identity"`
	Proposal        ExperienceProposal         `json:"proposal"`
	Provenance      SemanticProposalProvenance `json:"provenance"`
	Authority       InstructionAuthority       `json:"instruction_authority"`
}

type SemanticProposalProvenance struct {
	Harness       Harness   `json:"harness"`
	Model         string    `json:"model"`
	PromptVersion string    `json:"prompt_version"`
	InputHash     string    `json:"input_hash"`
	OutputHash    string    `json:"output_hash"`
	GeneratedAt   time.Time `json:"generated_at"`
}

type SemanticDecision struct {
	SchemaVersion   string                     `json:"schema_version"`
	DecisionID      string                     `json:"decision_id"`
	CandidateID     string                     `json:"candidate_id"`
	ProjectIdentity string                     `json:"project_identity"`
	Disposition     SemanticDisposition        `json:"disposition"`
	ReasonCode      SemanticDecisionReasonCode `json:"reason_code"`
	Explanation     string                     `json:"explanation"`
	Confidence      float64                    `json:"confidence"`
	ProposalID      string                     `json:"proposal_id,omitempty"`
	Provenance      SemanticProposalProvenance `json:"provenance"`
}

type SemanticResult struct {
	Proposal *SemanticProposal `json:"proposal,omitempty"`
	Decision SemanticDecision  `json:"decision"`
}

type SemanticDispositionCounts struct {
	Proposed int
	Rejected int
	Deferred int
}

type Experience struct {
	SchemaVersion     string         `json:"schema_version"`
	ExperienceID      string         `json:"experience_id"`
	OriginCandidateID string         `json:"origin_candidate_id"`
	Version           int            `json:"version"`
	Type              ExperienceType `json:"type"`
	Scope             Scope          `json:"scope"`
	Applicability     Applicability  `json:"applicability"`
	Guidance          Guidance       `json:"guidance"`
	Verifier          Verifier       `json:"verifier"`
	Evidence          EvidenceSet    `json:"evidence"`
	EpisodeRefs       []string       `json:"episode_refs,omitempty"`
	Provenance        Provenance     `json:"provenance"`
	Governance        Governance     `json:"governance"`
	ContentHash       string         `json:"content_hash"`
	PreviousVersion   *ExperienceRef `json:"previous_version,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
}

type ExperienceRef struct {
	ExperienceID string `json:"experience_id"`
	Version      int    `json:"version"`
}

type Scope struct {
	Kind            ScopeKind `json:"kind"`
	ProjectIdentity string    `json:"project_identity"`
	SessionKey      string    `json:"session_key,omitempty"`
	RepositoryPaths []string  `json:"repository_paths,omitempty"`
	TaskFamilies    []string  `json:"task_families,omitempty"`
	Harnesses       []Harness `json:"harnesses,omitempty"`
	Models          []string  `json:"models,omitempty"`
}

type Applicability struct {
	SemanticDescription     string                   `json:"semantic_description"`
	DeterministicConditions []DeterministicCondition `json:"deterministic_conditions,omitempty"`
	Exclusions              []string                 `json:"exclusions,omitempty"`
	ExpiresAt               *time.Time               `json:"expires_at,omitempty"`
}

type DeterministicCondition struct {
	Kind   DeterministicConditionKind `json:"kind"`
	Values []string                   `json:"values"`
}

type Guidance struct {
	Instruction          string               `json:"instruction"`
	Rationale            string               `json:"rationale"`
	Exceptions           []string             `json:"exceptions,omitempty"`
	InterventionStrength InterventionStrength `json:"intervention_strength"`
}

type EvidenceSet struct {
	EvidenceSetID string               `json:"evidence_set_id"`
	Availability  EvidenceAvailability `json:"availability"`
	Refs          []EvidenceRef        `json:"refs"`
}

type EvidenceRef struct {
	Kind       EvidenceSourceKind `json:"kind"`
	SessionKey string             `json:"session_key,omitempty"`
	TurnIndex  *int64             `json:"turn_index,omitempty"`
	TurnRole   EvidenceTurnRole   `json:"turn_role,omitempty"`
	ToolName   string             `json:"tool_name,omitempty"`
	EventID    string             `json:"event_id,omitempty"`
	OutcomeID  string             `json:"outcome_id,omitempty"`
	Path       string             `json:"path,omitempty"`
	SHA256     string             `json:"sha256,omitempty"`
	RecordedBy string             `json:"recorded_by,omitempty"`
	OccurredAt *time.Time         `json:"occurred_at,omitempty"`
	Excerpt    string             `json:"excerpt,omitempty"`
}

type Provenance struct {
	ExtractorVersion  string    `json:"extractor_version"`
	Harness           Harness   `json:"harness,omitempty"`
	Model             string    `json:"model,omitempty"`
	PromptVersion     string    `json:"prompt_version,omitempty"`
	InputHash         string    `json:"input_hash"`
	SourceCandidateID string    `json:"source_candidate_id,omitempty"`
	GeneratedAt       time.Time `json:"generated_at"`
}

type Governance struct {
	LifecycleState LifecycleState       `json:"lifecycle_state"`
	Authority      InstructionAuthority `json:"instruction_authority"`
	Approval       *ApprovalProvenance  `json:"approval,omitempty"`
	StateReason    string               `json:"state_reason,omitempty"`
}

type ApprovalProvenance struct {
	ApprovedBy          string       `json:"approved_by"`
	ApprovedAt          time.Time    `json:"approved_at"`
	Mode                ApprovalMode `json:"approval_mode"`
	CandidateID         string       `json:"candidate_id"`
	ProposedContentHash string       `json:"proposed_content_hash"`
	ApprovedContentHash string       `json:"approved_content_hash"`
}

type ExperienceMeasurement struct {
	Experience          ExperienceRef `json:"experience"`
	Opportunities       int           `json:"opportunities"`
	Deliveries          int           `json:"deliveries"`
	Satisfied           int           `json:"satisfied"`
	Violated            int           `json:"violated"`
	Unknown             int           `json:"unknown"`
	ExplicitCorrections int           `json:"explicit_corrections"`
	LastAppliedAt       *time.Time    `json:"last_applied_at,omitempty"`
	LastValidatedAt     *time.Time    `json:"last_validated_at,omitempty"`
}

type LifecycleTransition struct {
	TransitionID   string              `json:"transition_id"`
	Experience     ExperienceRef       `json:"experience"`
	FromState      LifecycleState      `json:"from_state"`
	ToState        LifecycleState      `json:"to_state"`
	ReasonCode     string              `json:"reason_code"`
	ActorKind      TransitionActorKind `json:"actor_kind"`
	ActorID        string              `json:"actor_id"`
	OccurredAt     time.Time           `json:"occurred_at"`
	SourceEvidence []EvidenceRef       `json:"source_evidence,omitempty"`
}

type Application struct {
	SchemaVersion      string             `json:"schema_version"`
	ApplicationID      string             `json:"application_id"`
	Experience         ExperienceRef      `json:"experience"`
	ProjectIdentity    string             `json:"project_identity"`
	SessionKey         string             `json:"session_key,omitempty"`
	DeliveryKind       DeliveryKind       `json:"delivery_kind"`
	DeliveryState      DeliveryState      `json:"delivery_state"`
	DeliveredAt        *time.Time         `json:"delivered_at,omitempty"`
	OpportunityState   OpportunityState   `json:"opportunity_state"`
	ApplicabilityState ApplicabilityState `json:"applicability_state"`
	VerifierState      VerifierState      `json:"verifier_state"`
	TaskOutcomeState   TaskOutcomeState   `json:"task_outcome_state"`
	SourceEvidence     []EvidenceRef      `json:"source_evidence,omitempty"`
}

type Evaluation struct {
	SchemaVersion      string                `json:"schema_version"`
	EvaluationID       string                `json:"evaluation_id"`
	ApplicationID      string                `json:"application_id"`
	Experience         ExperienceRef         `json:"experience"`
	EvaluatedAt        time.Time             `json:"evaluated_at"`
	OpportunityState   OpportunityState      `json:"opportunity_state"`
	ApplicabilityState ApplicabilityState    `json:"applicability_state"`
	VerifierState      VerifierState         `json:"verifier_state"`
	TaskOutcomeState   TaskOutcomeState      `json:"task_outcome_state"`
	Coverage           []CoverageRequirement `json:"coverage"`
	SourceEvidence     []EvidenceRef         `json:"source_evidence"`
	DerivationVersion  string                `json:"derivation_version"`
}
