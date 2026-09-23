package experience

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxIdentifierBytes = 256
	maxPathBytes       = 512
	maxCommandBytes    = 16 * 1024
	maxGuidanceBytes   = 2 * 1024
	maxRationaleBytes  = 8 * 1024
	maxDecisionBytes   = 1024
	maxExcerptBytes    = 16 * 1024
	maxListItems       = 128
)

func (value Candidate) Validate() error {
	if value.SchemaVersion != CandidateSchemaVersion {
		return errors.New("candidate schema version is invalid")
	}
	if !value.Family.Valid() {
		return errors.New("candidate family is invalid")
	}
	if err := validateIdentifier("candidate project identity", value.ProjectIdentity); err != nil {
		return err
	}
	if value.Authority != AuthorityNone {
		return errors.New("candidate instruction authority must be none")
	}
	if value.LifecycleState != LifecycleCandidate {
		return errors.New("candidate lifecycle state must be candidate")
	}
	if value.CreatedAt.IsZero() {
		return errors.New("candidate created_at is required")
	}
	if err := validateText("candidate observed behavior", value.ObservedBehavior, maxRationaleBytes, true); err != nil {
		return err
	}
	if value.Family == CandidateCorrection && strings.TrimSpace(value.UserFeedback) == "" {
		return errors.New("correction candidate user feedback is required")
	}
	if err := validateText("candidate user feedback", value.UserFeedback, maxExcerptBytes, false); err != nil {
		return err
	}
	if err := value.Evidence.Validate(); err != nil {
		return fmt.Errorf("candidate evidence: %w", err)
	}
	for _, ref := range value.OutcomeRefs {
		if err := validateIdentifier("candidate outcome reference", ref); err != nil {
			return err
		}
	}
	if len(value.EpisodeRefs) > maxListItems {
		return errors.New("candidate episode references exceed item limit")
	}
	for _, ref := range value.EpisodeRefs {
		if err := validateIdentifier("candidate episode reference", ref); err != nil {
			return err
		}
	}
	for _, ref := range value.ExistingRefs {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("candidate existing experience reference: %w", err)
		}
	}
	if err := value.Proposal.Validate(); err != nil {
		return fmt.Errorf("candidate proposal: %w", err)
	}
	if value.Proposal.Scope.ProjectIdentity != value.ProjectIdentity {
		return errors.New("candidate proposal scope must match candidate project")
	}
	if err := value.Provenance.Validate(); err != nil {
		return fmt.Errorf("candidate provenance: %w", err)
	}
	if value.Proposal.SemanticCompilationPending {
		if value.Proposal.Confidence != nil {
			return errors.New("semantic-pending candidate cannot claim proposal confidence")
		}
		if value.Proposal.Guidance.InterventionStrength != InterventionObserve {
			return errors.New("semantic-pending candidate must remain observe-only")
		}
		if value.Provenance.Harness != "" ||
			strings.TrimSpace(value.Provenance.Model) != "" ||
			strings.TrimSpace(value.Provenance.PromptVersion) != "" {
			return errors.New("semantic-pending candidate cannot claim harness or prompt provenance")
		}
	} else if !value.Provenance.Harness.Valid() || strings.TrimSpace(value.Provenance.PromptVersion) == "" {
		return errors.New("candidate proposal requires harness and prompt provenance")
	}
	if value.CandidateID == "" || value.CandidateID != value.DeterministicID() {
		return errors.New("candidate ID does not match deterministic content")
	}
	return nil
}

func (value ExperienceProposal) Validate() error {
	if !value.Type.Valid() {
		return errors.New("experience type is invalid")
	}
	if err := value.Scope.Validate(); err != nil {
		return err
	}
	if err := value.Applicability.Validate(); err != nil {
		return err
	}
	if err := value.Guidance.Validate(); err != nil {
		return err
	}
	if err := value.Verifier.Validate(); err != nil {
		return err
	}
	if value.EvidenceSupport != nil {
		if err := value.EvidenceSupport.Validate(len(value.Guidance.Exceptions)); err != nil {
			return err
		}
	}
	if value.Confidence != nil && !validUnitInterval(*value.Confidence) {
		return errors.New("proposal confidence must be between zero and one")
	}
	return nil
}

func (value SemanticProposal) Validate() error {
	if value.SchemaVersion != SemanticProposalSchemaVersion {
		return errors.New("semantic proposal schema version is invalid")
	}
	if err := validateIdentifier("semantic proposal candidate ID", value.CandidateID); err != nil {
		return err
	}
	if err := validateIdentifier("semantic proposal project identity", value.ProjectIdentity); err != nil {
		return err
	}
	if value.Authority != AuthorityNone {
		return errors.New("semantic proposal instruction authority must be none")
	}
	if err := value.Proposal.Validate(); err != nil {
		return fmt.Errorf("semantic proposal content: %w", err)
	}
	if value.Proposal.SemanticCompilationPending {
		return errors.New("semantic proposal cannot remain pending compilation")
	}
	if value.Proposal.Confidence == nil {
		return errors.New("semantic proposal confidence is required")
	}
	if value.Proposal.Scope.ProjectIdentity != value.ProjectIdentity {
		return errors.New("semantic proposal scope must match proposal project")
	}
	if err := value.Provenance.Validate(); err != nil {
		return fmt.Errorf("semantic proposal provenance: %w", err)
	}
	if value.ProposalID == "" || value.ProposalID != value.DeterministicID() {
		return errors.New("semantic proposal ID does not match deterministic content")
	}
	return nil
}

func (value SemanticProposalProvenance) Validate() error {
	if !value.Harness.Valid() {
		return errors.New("semantic proposal harness is invalid")
	}
	if err := validateIdentifier("semantic proposal model", value.Model); err != nil {
		return err
	}
	if err := validateIdentifier("semantic proposal prompt version", value.PromptVersion); err != nil {
		return err
	}
	switch value.PromptVersion {
	case SemanticProposalPromptVersionV1,
		SemanticProposalPromptVersionV2,
		SemanticProposalPromptVersionV3,
		SemanticProposalPromptVersionV4,
		SemanticProposalPromptVersionV5,
		SemanticProposalPromptVersionV6,
		SemanticProposalPromptVersionV7,
		SemanticProposalPromptVersionV8,
		SemanticProposalPromptVersionV9,
		SemanticProposalPromptVersionV10,
		SemanticProposalPromptVersionV11,
		SemanticProposalPromptVersionV12:
	default:
		return errors.New("semantic proposal prompt version is unsupported")
	}
	if !validSHA256(value.InputHash) || !validSHA256(value.OutputHash) {
		return errors.New("semantic proposal input and output hashes must be sha256")
	}
	if value.GeneratedAt.IsZero() {
		return errors.New("semantic proposal generated_at is required")
	}
	return nil
}

func (value SemanticDecision) Validate() error {
	if value.SchemaVersion != SemanticDecisionSchemaVersion {
		return errors.New("semantic decision schema version is invalid")
	}
	if err := validateIdentifier("semantic decision candidate ID", value.CandidateID); err != nil {
		return err
	}
	if err := validateIdentifier(
		"semantic decision project identity",
		value.ProjectIdentity,
	); err != nil {
		return err
	}
	if !value.Disposition.Valid() {
		return errors.New("semantic decision disposition is invalid")
	}
	if !value.ReasonCode.Valid() {
		return errors.New("semantic decision reason code is invalid")
	}
	if err := validateOneLine(
		"semantic decision explanation",
		value.Explanation,
		maxDecisionBytes,
		true,
	); err != nil {
		return err
	}
	if !validUnitInterval(value.Confidence) {
		return errors.New("semantic decision confidence must be between zero and one")
	}
	switch value.Disposition {
	case SemanticDispositionPropose:
		if value.ReasonCode != SemanticReasonReusableSupported {
			return errors.New("propose decision requires reusable-supported reason")
		}
		if err := validateIdentifier("semantic decision proposal ID", value.ProposalID); err != nil {
			return err
		}
	case SemanticDispositionReject:
		if value.ProposalID != "" {
			return errors.New("reject decision cannot reference a proposal")
		}
		switch value.ReasonCode {
		case SemanticReasonTemporaryOrTaskSpecific,
			SemanticReasonUnsafeOrOverbroad,
			SemanticReasonNotReusable:
		default:
			return errors.New("reject decision reason is incompatible")
		}
	case SemanticDispositionDefer:
		if value.ProposalID != "" {
			return errors.New("defer decision cannot reference a proposal")
		}
		if value.ReasonCode != SemanticReasonInsufficientContext {
			return errors.New("defer decision requires insufficient-context reason")
		}
	}
	if err := value.Provenance.Validate(); err != nil {
		return fmt.Errorf("semantic decision provenance: %w", err)
	}
	if value.DecisionID == "" || value.DecisionID != value.DeterministicID() {
		return errors.New("semantic decision ID does not match deterministic content")
	}
	return nil
}

func validUnitInterval(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func (value SemanticResult) Validate() error {
	if err := value.Decision.Validate(); err != nil {
		return err
	}
	if value.Decision.Disposition != SemanticDispositionPropose {
		if value.Proposal != nil {
			return errors.New("non-propose semantic result cannot contain a proposal")
		}
		return nil
	}
	if value.Proposal == nil {
		return errors.New("propose semantic result requires a proposal")
	}
	if err := value.Proposal.Validate(); err != nil {
		return err
	}
	if value.Proposal.CandidateID != value.Decision.CandidateID ||
		value.Proposal.ProjectIdentity != value.Decision.ProjectIdentity ||
		value.Proposal.ProposalID != value.Decision.ProposalID ||
		value.Proposal.Provenance != value.Decision.Provenance {
		return errors.New("semantic result proposal does not match decision")
	}
	return nil
}

func (value Experience) Validate() error {
	if value.SchemaVersion != ExperienceSchemaVersion {
		return errors.New("experience schema version is invalid")
	}
	if err := validateIdentifier("experience ID", value.ExperienceID); err != nil {
		return err
	}
	if err := validateIdentifier("experience origin candidate ID", value.OriginCandidateID); err != nil {
		return err
	}
	if value.Version < 1 {
		return errors.New("experience version must be positive")
	}
	if !value.Type.Valid() {
		return errors.New("experience type is invalid")
	}
	if err := value.Scope.Validate(); err != nil {
		return err
	}
	if err := value.Applicability.Validate(); err != nil {
		return err
	}
	if err := value.Guidance.Validate(); err != nil {
		return err
	}
	if err := value.Verifier.Validate(); err != nil {
		return err
	}
	if err := value.Evidence.Validate(); err != nil {
		return fmt.Errorf("experience evidence: %w", err)
	}
	if len(value.EpisodeRefs) > maxListItems {
		return errors.New("experience episode references exceed item limit")
	}
	for _, ref := range value.EpisodeRefs {
		if err := validateIdentifier("experience episode reference", ref); err != nil {
			return err
		}
	}
	if err := value.Provenance.Validate(); err != nil {
		return fmt.Errorf("experience provenance: %w", err)
	}
	if err := value.Governance.Validate(value.ContentHash); err != nil {
		return err
	}
	if value.CreatedAt.IsZero() {
		return errors.New("experience created_at is required")
	}
	if value.Version == 1 && value.PreviousVersion != nil {
		return errors.New("experience version one cannot have a previous version reference")
	}
	if value.Version > 1 {
		if value.PreviousVersion == nil {
			return errors.New("later experience version requires a previous version reference")
		}
		if err := value.PreviousVersion.Validate(); err != nil {
			return fmt.Errorf("experience previous version: %w", err)
		}
		if value.PreviousVersion.ExperienceID != value.ExperienceID {
			return errors.New("previous version reference must use the same experience ID")
		}
		if value.PreviousVersion.Version != value.Version-1 {
			return errors.New("previous version reference must identify exactly version-1")
		}
	}
	if value.ContentHash == "" || value.ContentHash != value.CanonicalContentHash() {
		return errors.New("experience content hash does not match canonical content")
	}
	if strings.TrimSpace(value.Provenance.SourceCandidateID) == "" {
		return errors.New("experience provenance requires a source candidate ID")
	}
	if value.Version == 1 && value.Provenance.SourceCandidateID != value.OriginCandidateID {
		return errors.New("experience version one source candidate must match origin candidate")
	}
	wantID := DeriveExperienceID(value.Scope.ProjectIdentity, value.OriginCandidateID)
	if value.ExperienceID != wantID {
		return errors.New("experience ID does not match origin candidate")
	}
	if value.Governance.Approval.CandidateID != value.Provenance.SourceCandidateID {
		return errors.New("approval candidate must match version provenance source candidate")
	}
	return nil
}

func (value EvidenceSupport) Validate(exceptionCount int) error {
	if len(value.GuidanceRefs) == 0 ||
		len(value.GuidanceRefs) > maxListItems ||
		len(value.VerifierRefs) == 0 ||
		len(value.VerifierRefs) > maxListItems ||
		len(value.ExceptionRefs) != exceptionCount {
		return errors.New("proposal evidence support is incomplete or exceeds limit")
	}
	groups := make([][]string, 0, len(value.ExceptionRefs)+2)
	groups = append(groups, value.GuidanceRefs, value.VerifierRefs)
	groups = append(groups, value.ExceptionRefs...)
	for _, refs := range groups {
		if len(refs) == 0 || len(refs) > maxListItems {
			return errors.New("proposal evidence support group is empty or exceeds limit")
		}
		seen := make(map[string]bool, len(refs))
		for _, ref := range refs {
			if err := validateIdentifier("proposal evidence support reference", ref); err != nil {
				return err
			}
			if seen[ref] {
				return errors.New("proposal evidence support contains duplicate references")
			}
			seen[ref] = true
		}
	}
	return nil
}

func (value ExperienceRef) Validate() error {
	if err := validateIdentifier("experience reference ID", value.ExperienceID); err != nil {
		return err
	}
	if value.Version < 1 {
		return errors.New("experience reference version must be positive")
	}
	return nil
}

func (value Scope) Validate() error {
	if !value.Kind.Valid() {
		return errors.New("scope kind is invalid")
	}
	if err := validateIdentifier("scope project identity", value.ProjectIdentity); err != nil {
		return err
	}
	if value.Kind == ScopeSession {
		if err := validateIdentifier("session scope key", value.SessionKey); err != nil {
			return err
		}
	} else if value.SessionKey != "" {
		return errors.New("project scope cannot include a session key")
	}
	if len(value.RepositoryPaths) > maxListItems ||
		len(value.TaskFamilies) > maxListItems ||
		len(value.Harnesses) > maxListItems ||
		len(value.Models) > maxListItems {
		return errors.New("scope list exceeds item limit")
	}
	for _, item := range value.RepositoryPaths {
		if err := validateRelativePattern(item); err != nil {
			return fmt.Errorf("scope repository path: %w", err)
		}
	}
	for _, item := range value.TaskFamilies {
		if err := validateIdentifier("scope task family", item); err != nil {
			return err
		}
	}
	for _, item := range value.Harnesses {
		if !item.Valid() {
			return errors.New("scope harness is invalid")
		}
	}
	for _, item := range value.Models {
		if err := validateIdentifier("scope model", item); err != nil {
			return err
		}
	}
	return nil
}

func (value Applicability) Validate() error {
	if err := validateText("applicability semantic description", value.SemanticDescription, maxRationaleBytes, true); err != nil {
		return err
	}
	if len(value.DeterministicConditions) > maxListItems || len(value.Exclusions) > maxListItems {
		return errors.New("applicability list exceeds item limit")
	}
	for _, condition := range value.DeterministicConditions {
		if err := condition.Validate(); err != nil {
			return err
		}
	}
	for _, exclusion := range value.Exclusions {
		if err := validateText("applicability exclusion", exclusion, maxRationaleBytes, true); err != nil {
			return err
		}
	}
	if value.ExpiresAt != nil && value.ExpiresAt.IsZero() {
		return errors.New("applicability expiration cannot be zero")
	}
	return nil
}

func (value DeterministicCondition) Validate() error {
	if !value.Kind.Valid() {
		return errors.New("deterministic condition kind is invalid")
	}
	if len(value.Values) == 0 || len(value.Values) > maxListItems {
		return errors.New("deterministic condition values are required and bounded")
	}
	for _, item := range value.Values {
		if value.Kind == ConditionPathPattern {
			if err := validateRelativePattern(item); err != nil {
				return fmt.Errorf("deterministic path condition: %w", err)
			}
			continue
		}
		if err := validateIdentifier("deterministic condition value", item); err != nil {
			return err
		}
	}
	return nil
}

func (value Guidance) Validate() error {
	if err := validateOneLine("guidance instruction", value.Instruction, maxGuidanceBytes, true); err != nil {
		return err
	}
	if err := validateText("guidance rationale", value.Rationale, maxRationaleBytes, true); err != nil {
		return err
	}
	if !value.InterventionStrength.Valid() {
		return errors.New("intervention strength is invalid")
	}
	if value.InterventionStrength == InterventionDeny {
		return errors.New("learned experience cannot use deny intervention")
	}
	if len(value.Exceptions) > maxListItems {
		return errors.New("guidance exceptions exceed item limit")
	}
	for _, item := range value.Exceptions {
		if err := validateOneLine("guidance exception", item, maxGuidanceBytes, true); err != nil {
			return err
		}
	}
	return nil
}

func (value EvidenceSet) Validate() error {
	if !value.Availability.Valid() {
		return errors.New("evidence availability is invalid")
	}
	if len(value.Refs) == 0 || len(value.Refs) > maxListItems {
		return errors.New("evidence references are required and bounded")
	}
	for _, ref := range value.Refs {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	if value.EvidenceSetID == "" || value.EvidenceSetID != value.DeterministicID() {
		return errors.New("evidence set ID does not match deterministic references")
	}
	return nil
}

func (value EvidenceRef) Validate() error {
	if !value.Kind.Valid() {
		return errors.New("evidence source kind is invalid")
	}
	if value.Excerpt != "" {
		if err := validateText("evidence excerpt", value.Excerpt, maxExcerptBytes, false); err != nil {
			return err
		}
	}
	if value.OccurredAt != nil && value.OccurredAt.IsZero() {
		return errors.New("evidence occurred_at cannot be zero")
	}
	switch value.Kind {
	case EvidenceTranscriptTurn:
		if err := validateIdentifier("evidence session key", value.SessionKey); err != nil {
			return err
		}
		if value.TurnIndex == nil || *value.TurnIndex < 0 {
			return errors.New("transcript evidence requires a non-negative turn index")
		}
		if value.TurnRole != "" && !value.TurnRole.Valid() {
			return errors.New("transcript evidence turn role is invalid")
		}
		if value.ToolName != "" {
			if value.TurnRole != EvidenceTurnToolCall &&
				value.TurnRole != EvidenceTurnToolResult {
				return errors.New("evidence tool name requires a tool turn role")
			}
			if err := validateText("evidence tool name", value.ToolName, 256, false); err != nil {
				return err
			}
		}
		if value.EventID != "" || value.OutcomeID != "" || value.Path != "" || value.SHA256 != "" || value.RecordedBy != "" {
			return errors.New("transcript evidence contains fields for another source kind")
		}
	case EvidenceCanonicalEvent:
		if err := validateIdentifier("evidence event ID", value.EventID); err != nil {
			return err
		}
		if value.SessionKey != "" || value.TurnIndex != nil || value.TurnRole != "" || value.ToolName != "" || value.OutcomeID != "" || value.Path != "" || value.SHA256 != "" || value.RecordedBy != "" {
			return errors.New("canonical event evidence contains fields for another source kind")
		}
	case EvidenceOutcomeObservation:
		if err := validateIdentifier("evidence outcome ID", value.OutcomeID); err != nil {
			return err
		}
		if value.SessionKey != "" || value.TurnIndex != nil || value.TurnRole != "" || value.ToolName != "" || value.EventID != "" || value.Path != "" || value.SHA256 != "" || value.RecordedBy != "" {
			return errors.New("outcome evidence contains fields for another source kind")
		}
	case EvidenceWorkspaceHash:
		if err := validateRelativePath(value.Path); err != nil {
			return fmt.Errorf("workspace evidence path: %w", err)
		}
		if !validSHA256(value.SHA256) {
			return errors.New("workspace evidence requires a sha256 digest")
		}
		if value.SessionKey != "" || value.TurnIndex != nil || value.TurnRole != "" || value.ToolName != "" || value.EventID != "" || value.OutcomeID != "" || value.RecordedBy != "" {
			return errors.New("workspace evidence contains fields for another source kind")
		}
	case EvidenceUserRecorded:
		if err := validateIdentifier("user-recorded evidence actor", value.RecordedBy); err != nil {
			return err
		}
		if value.SessionKey != "" || value.TurnIndex != nil || value.TurnRole != "" || value.ToolName != "" || value.EventID != "" || value.OutcomeID != "" || value.Path != "" || value.SHA256 != "" {
			return errors.New("user-recorded evidence contains fields for another source kind")
		}
	}
	return nil
}

func (value Provenance) Validate() error {
	if err := validateIdentifier("provenance extractor version", value.ExtractorVersion); err != nil {
		return err
	}
	if !validSHA256(value.InputHash) {
		return errors.New("provenance input hash must be sha256")
	}
	if value.GeneratedAt.IsZero() {
		return errors.New("provenance generated_at is required")
	}
	if value.Harness != "" && !value.Harness.Valid() {
		return errors.New("provenance harness is invalid")
	}
	if value.Model != "" || value.PromptVersion != "" {
		if !value.Harness.Valid() {
			return errors.New("model or prompt provenance requires a supported harness")
		}
	}
	if value.PromptVersion != "" {
		if err := validateIdentifier("provenance prompt version", value.PromptVersion); err != nil {
			return err
		}
	}
	if value.Model != "" {
		if err := validateIdentifier("provenance model", value.Model); err != nil {
			return err
		}
	}
	if value.SourceCandidateID != "" {
		if err := validateIdentifier("provenance source candidate ID", value.SourceCandidateID); err != nil {
			return err
		}
	}
	return nil
}

func (value Governance) Validate(contentHash string) error {
	switch value.LifecycleState {
	case LifecycleApproved,
		LifecycleActive,
		LifecyclePaused,
		LifecycleContradicted,
		LifecycleSuperseded,
		LifecycleExpired:
	default:
		return errors.New("lifecycle state is invalid for immutable experience")
	}
	if value.Authority != AuthorityUserApproved {
		return errors.New("immutable experience requires user-approved authority")
	}
	if value.Approval == nil {
		return errors.New("immutable experience requires approval provenance")
	}
	return value.Approval.Validate(contentHash)
}

func (value ApprovalProvenance) Validate(contentHash string) error {
	if err := validateIdentifier("approval actor", value.ApprovedBy); err != nil {
		return err
	}
	if value.ApprovedAt.IsZero() {
		return errors.New("approval timestamp is required")
	}
	if !value.Mode.Valid() {
		return errors.New("approval mode is invalid")
	}
	if err := validateIdentifier("approval candidate ID", value.CandidateID); err != nil {
		return err
	}
	if !validSHA256(value.ProposedContentHash) || !validSHA256(value.ApprovedContentHash) {
		return errors.New("approval content hashes must be sha256")
	}
	if value.ApprovedContentHash != contentHash {
		return errors.New("approval hash does not match experience content")
	}
	return nil
}

func (value ExperienceMeasurement) Validate() error {
	if err := value.Experience.Validate(); err != nil {
		return err
	}
	if value.Opportunities < 0 || value.Deliveries < 0 || value.Satisfied < 0 ||
		value.Violated < 0 || value.Unknown < 0 || value.ExplicitCorrections < 0 {
		return errors.New("experience measurements cannot be negative")
	}
	if value.LastAppliedAt != nil && value.LastAppliedAt.IsZero() {
		return errors.New("measurement last_applied_at cannot be zero")
	}
	if value.LastValidatedAt != nil && value.LastValidatedAt.IsZero() {
		return errors.New("measurement last_validated_at cannot be zero")
	}
	return nil
}

func (value LifecycleTransition) Validate() error {
	if err := value.Experience.Validate(); err != nil {
		return err
	}
	if !ValidLifecycleTransition(value.FromState, value.ToState) {
		return fmt.Errorf("lifecycle transition %q to %q is invalid", value.FromState, value.ToState)
	}
	if err := validateIdentifier("transition reason code", value.ReasonCode); err != nil {
		return err
	}
	if !value.ActorKind.Valid() {
		return errors.New("transition actor kind is invalid")
	}
	if err := validateIdentifier("transition actor ID", value.ActorID); err != nil {
		return err
	}
	if value.OccurredAt.IsZero() {
		return errors.New("transition occurred_at is required")
	}
	for _, ref := range value.SourceEvidence {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	if value.TransitionID == "" || value.TransitionID != value.DeterministicID() {
		return errors.New("transition ID does not match deterministic content")
	}
	return nil
}

func ValidLifecycleTransition(from, to LifecycleState) bool {
	switch from {
	case LifecycleApproved:
		return to == LifecycleActive || to == LifecyclePaused || to == LifecycleSuperseded || to == LifecycleExpired
	case LifecycleActive:
		return to == LifecyclePaused || to == LifecycleContradicted || to == LifecycleSuperseded || to == LifecycleExpired
	case LifecyclePaused:
		return to == LifecycleActive || to == LifecycleContradicted || to == LifecycleSuperseded || to == LifecycleExpired
	case LifecycleContradicted:
		return to == LifecycleActive || to == LifecyclePaused || to == LifecycleSuperseded
	default:
		return false
	}
}

func (value Application) Validate() error {
	if value.SchemaVersion != ApplicationSchemaVersion {
		return errors.New("application schema version is invalid")
	}
	if err := value.Experience.Validate(); err != nil {
		return err
	}
	if err := validateIdentifier("application project identity", value.ProjectIdentity); err != nil {
		return err
	}
	if !value.DeliveryKind.Valid() || !value.DeliveryState.Valid() {
		return errors.New("application delivery kind or state is invalid")
	}
	if !value.OpportunityState.Valid() || !value.ApplicabilityState.Valid() ||
		!value.VerifierState.Valid() || !value.TaskOutcomeState.Valid() {
		return errors.New("application evaluation state is invalid")
	}
	if value.DeliveryState == DeliveryDelivered {
		if value.DeliveredAt == nil || value.DeliveredAt.IsZero() {
			return errors.New("delivered application requires delivered_at")
		}
		if err := validateIdentifier("application session key", value.SessionKey); err != nil {
			return err
		}
	} else if value.DeliveredAt != nil {
		return errors.New("non-delivered application cannot include delivered_at")
	}
	for _, ref := range value.SourceEvidence {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	if value.ApplicationID == "" || value.ApplicationID != value.DeterministicID() {
		return errors.New("application ID does not match deterministic content")
	}
	return nil
}

func (value Evaluation) Validate() error {
	if value.SchemaVersion != EvaluationSchemaVersion {
		return errors.New("evaluation schema version is invalid")
	}
	if err := validateIdentifier("evaluation application ID", value.ApplicationID); err != nil {
		return err
	}
	if err := value.Experience.Validate(); err != nil {
		return err
	}
	if value.EvaluatedAt.IsZero() {
		return errors.New("evaluation evaluated_at is required")
	}
	if !value.OpportunityState.Valid() || !value.ApplicabilityState.Valid() ||
		!value.VerifierState.Valid() || !value.TaskOutcomeState.Valid() {
		return errors.New("evaluation state is invalid")
	}
	if len(value.Coverage) > maxListItems ||
		len(value.Coverage) == 0 &&
			value.VerifierState != VerifierUnknown {
		return errors.New(
			"evaluation coverage is required unless verifier state is unknown, and is bounded",
		)
	}
	for _, requirement := range value.Coverage {
		if !requirement.Valid() {
			return errors.New("evaluation coverage requirement is invalid")
		}
	}
	if len(value.SourceEvidence) == 0 || len(value.SourceEvidence) > maxListItems {
		return errors.New("evaluation source evidence is required and bounded")
	}
	for _, ref := range value.SourceEvidence {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	if err := validateIdentifier("evaluation derivation version", value.DerivationVersion); err != nil {
		return err
	}
	if value.EvaluationID == "" || value.EvaluationID != value.DeterministicID() {
		return errors.New("evaluation ID does not match deterministic content")
	}
	return nil
}

func (value Verifier) Validate() error {
	if !value.Kind.Valid() {
		return errors.New("verifier kind is invalid")
	}
	if len(value.CoverageRequirements) > maxListItems {
		return errors.New("verifier coverage requirements exceed item limit")
	}
	for _, requirement := range value.CoverageRequirements {
		if !requirement.Valid() {
			return errors.New("verifier coverage requirement is invalid")
		}
	}
	if value.absenceBased() && len(value.CoverageRequirements) == 0 {
		return errors.New("absence-based verifier requires explicit coverage requirements")
	}
	specCount := 0
	for _, present := range []bool{
		value.Command != nil,
		value.File != nil,
		value.PathPattern != nil,
		value.VerificationAfterLastEdit != nil,
		value.NoRepeatFailure != nil,
		value.UserCorrectionAbsent != nil,
		value.ObservationOnly != nil,
	} {
		if present {
			specCount++
		}
	}
	if specCount != 1 {
		return errors.New("verifier must declare exactly one typed specification")
	}
	switch value.Kind {
	case VerifierCommandObserved, VerifierCommandSucceeded:
		if value.Command == nil {
			return errors.New("command verifier requires command specification")
		}
		return value.Command.Validate()
	case VerifierFileNotModified, VerifierFileModified:
		if value.File == nil {
			return errors.New("file verifier requires file specification")
		}
		return value.File.Validate()
	case VerifierPathPatternNotModified:
		if value.PathPattern == nil {
			return errors.New("path-pattern verifier requires path-pattern specification")
		}
		return value.PathPattern.Validate()
	case VerifierVerificationAfterLastEdit:
		if value.VerificationAfterLastEdit == nil {
			return errors.New("verification-after-edit verifier requires its typed specification")
		}
		return value.VerificationAfterLastEdit.Validate()
	case VerifierNoRepeatFailure:
		if value.NoRepeatFailure == nil {
			return errors.New("no-repeat-failure verifier requires its typed specification")
		}
		return value.NoRepeatFailure.Validate()
	case VerifierUserCorrectionAbsent:
		if value.UserCorrectionAbsent == nil {
			return errors.New("user-correction-absent verifier requires its typed specification")
		}
		return value.UserCorrectionAbsent.Validate()
	case VerifierObservationOnly:
		if value.ObservationOnly == nil {
			return errors.New("observation-only verifier requires its typed specification")
		}
		return value.ObservationOnly.Validate()
	default:
		return errors.New("verifier kind is invalid")
	}
}

func (value CommandVerifierSpec) Validate() error {
	if err := validateOneLine("verifier command", value.Command, maxCommandBytes, true); err != nil {
		return err
	}
	if err := validateIdentifier("verifier command scrubbing version", value.ScrubbingVersion); err != nil {
		return err
	}
	if value.CommandClass != "" {
		if err := validateIdentifier("verifier command class", value.CommandClass); err != nil {
			return err
		}
	}
	return nil
}

func (value FileVerifierSpec) Validate() error {
	return validateRelativePath(value.Path)
}

func (value PathPatternVerifierSpec) Validate() error {
	if len(value.Patterns) == 0 || len(value.Patterns) > maxListItems {
		return errors.New("path-pattern verifier patterns are required and bounded")
	}
	for _, pattern := range value.Patterns {
		if err := validateRelativePattern(pattern); err != nil {
			return err
		}
	}
	return nil
}

func (value VerificationAfterLastEditSpec) Validate() error {
	if len(value.CommandClasses) > maxListItems {
		return errors.New("verification command classes exceed item limit")
	}
	for _, class := range value.CommandClasses {
		if err := validateIdentifier("verification command class", class); err != nil {
			return err
		}
	}
	return nil
}

func (value NoRepeatFailureSpec) Validate() error {
	if err := validateIdentifier("failure command class", value.CommandClass); err != nil {
		return err
	}
	if err := validateOneLine("normalized failure pattern", value.NormalizedPattern, maxGuidanceBytes, true); err != nil {
		return err
	}
	if value.WindowTurns < 1 || value.WindowTurns > 1000 {
		return errors.New("failure window turns must be between one and 1000")
	}
	return nil
}

func (value UserCorrectionAbsentSpec) Validate() error {
	if len(value.MarkerFamilies) > maxListItems {
		return errors.New("correction marker families exceed item limit")
	}
	for _, marker := range value.MarkerFamilies {
		if err := validateIdentifier("correction marker family", marker); err != nil {
			return err
		}
	}
	return nil
}

func (value ObservationOnlySpec) Validate() error {
	return validateText("observation-only explanation", value.Explanation, maxRationaleBytes, true)
}

func validateIdentifier(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value != strings.TrimSpace(value) || len(value) > maxIdentifierBytes || !utf8.ValidString(value) || hasControl(value) {
		return fmt.Errorf("%s is invalid", name)
	}
	return nil
}

func validateOneLine(name, value string, limit int, required bool) error {
	if err := validateText(name, value, limit, required); err != nil {
		return err
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s must be one line", name)
	}
	return nil
}

func validateText(name, value string, limit int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value == "" {
		return nil
	}
	if value != strings.TrimSpace(value) || len(value) > limit || !utf8.ValidString(value) || hasDisallowedControl(value) {
		return fmt.Errorf("%s is invalid or exceeds its byte limit", name)
	}
	return nil
}

func validateRelativePath(value string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || len(value) > maxPathBytes {
		return errors.New("relative path is required and bounded")
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || strings.ContainsRune(value, '\x00') {
		return errors.New("path must use relative slash-separated form")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned != value || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return errors.New("path must be clean and cannot traverse parents")
	}
	if strings.ContainsAny(value, "*?[]{}") || hasControl(value) {
		return errors.New("file path cannot contain glob or control characters")
	}
	return nil
}

func validateRelativePattern(value string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || len(value) > maxPathBytes {
		return errors.New("relative path pattern is required and bounded")
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || strings.ContainsRune(value, '\x00') {
		return errors.New("path pattern must use relative slash-separated form")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("path pattern contains an invalid segment")
		}
		for _, character := range segment {
			if (character >= 'a' && character <= 'z') ||
				(character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') ||
				strings.ContainsRune("_-.@+*?", character) {
				continue
			}
			return errors.New("path pattern contains unsupported characters")
		}
	}
	return nil
}

func validSHA256(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil && strings.ToLower(value) == value
}

func hasControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func hasDisallowedControl(value string) bool {
	for _, character := range value {
		if (character < 0x20 && character != '\n' && character != '\t') || character == 0x7f {
			return true
		}
	}
	return false
}

func utcTime(value time.Time) time.Time {
	return value.UTC().Round(0)
}
