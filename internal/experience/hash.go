package experience

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func (value Candidate) DeterministicID() string {
	refs := normalizedEvidenceRefs(value.Evidence.Refs)
	outcomes := normalizedStrings(value.OutcomeRefs)
	episodes := normalizedStrings(value.EpisodeRefs)
	existing := append([]ExperienceRef(nil), value.ExistingRefs...)
	sort.Slice(existing, func(i, j int) bool {
		if existing[i].ExperienceID == existing[j].ExperienceID {
			return existing[i].Version < existing[j].Version
		}
		return existing[i].ExperienceID < existing[j].ExperienceID
	})
	return deterministicID("exc_", struct {
		SchemaVersion    string
		Family           CandidateFamily
		ProjectIdentity  string
		ObservedBehavior string
		UserFeedback     string
		Evidence         []EvidenceRef
		OutcomeRefs      []string
		EpisodeRefs      []string
		ExistingRefs     []ExperienceRef
	}{
		SchemaVersion:    CandidateSchemaVersion,
		Family:           value.Family,
		ProjectIdentity:  strings.TrimSpace(value.ProjectIdentity),
		ObservedBehavior: strings.TrimSpace(value.ObservedBehavior),
		UserFeedback:     strings.TrimSpace(value.UserFeedback),
		Evidence:         refs,
		OutcomeRefs:      outcomes,
		EpisodeRefs:      episodes,
		ExistingRefs:     existing,
	})
}

func (value SemanticProposal) DeterministicID() string {
	return deterministicID("exs_", struct {
		SchemaVersion   string
		CandidateID     string
		ProjectIdentity string
		Proposal        ExperienceProposal
		Harness         Harness
		Model           string
		PromptVersion   string
		InputHash       string
		OutputHash      string
	}{
		SchemaVersion:   SemanticProposalSchemaVersion,
		CandidateID:     strings.TrimSpace(value.CandidateID),
		ProjectIdentity: strings.TrimSpace(value.ProjectIdentity),
		Proposal: ExperienceProposal{
			Type:          value.Proposal.Type,
			Scope:         normalizedScope(value.Proposal.Scope),
			Applicability: normalizedApplicability(value.Proposal.Applicability),
			Guidance:      normalizedGuidance(value.Proposal.Guidance),
			Verifier:      normalizedVerifier(value.Proposal.Verifier),
			EvidenceSupport: normalizedEvidenceSupport(
				value.Proposal.EvidenceSupport,
			),
			Confidence: value.Proposal.Confidence,
		},
		Harness:       value.Provenance.Harness,
		Model:         strings.TrimSpace(value.Provenance.Model),
		PromptVersion: strings.TrimSpace(value.Provenance.PromptVersion),
		InputHash:     strings.TrimSpace(value.Provenance.InputHash),
		OutputHash:    strings.TrimSpace(value.Provenance.OutputHash),
	})
}

func normalizedEvidenceSupport(value *EvidenceSupport) *EvidenceSupport {
	if value == nil {
		return nil
	}
	result := &EvidenceSupport{
		GuidanceRefs: normalizeSupportRefs(value.GuidanceRefs),
		VerifierRefs: normalizeSupportRefs(value.VerifierRefs),
		ExceptionRefs: make(
			[][]string,
			len(value.ExceptionRefs),
		),
	}
	for index := range value.ExceptionRefs {
		result.ExceptionRefs[index] = normalizeSupportRefs(
			value.ExceptionRefs[index],
		)
	}
	return result
}

func normalizeSupportRefs(values []string) []string {
	result := append([]string(nil), values...)
	for index := range result {
		result[index] = strings.TrimSpace(result[index])
	}
	sort.Strings(result)
	return compactComparable(result)
}

func (value SemanticDecision) DeterministicID() string {
	return deterministicID("exd_", struct {
		SchemaVersion   string
		CandidateID     string
		ProjectIdentity string
		Disposition     SemanticDisposition
		ReasonCode      SemanticDecisionReasonCode
		Explanation     string
		Confidence      float64
		ProposalID      string
		Harness         Harness
		Model           string
		PromptVersion   string
		InputHash       string
		OutputHash      string
	}{
		SchemaVersion:   SemanticDecisionSchemaVersion,
		CandidateID:     strings.TrimSpace(value.CandidateID),
		ProjectIdentity: strings.TrimSpace(value.ProjectIdentity),
		Disposition:     value.Disposition,
		ReasonCode:      value.ReasonCode,
		Explanation:     strings.TrimSpace(value.Explanation),
		Confidence:      value.Confidence,
		ProposalID:      strings.TrimSpace(value.ProposalID),
		Harness:         value.Provenance.Harness,
		Model:           strings.TrimSpace(value.Provenance.Model),
		PromptVersion:   strings.TrimSpace(value.Provenance.PromptVersion),
		InputHash:       strings.TrimSpace(value.Provenance.InputHash),
		OutputHash:      strings.TrimSpace(value.Provenance.OutputHash),
	})
}

func DeriveExperienceID(projectIdentity, originCandidateID string) string {
	return deterministicID("exp_", struct {
		SchemaVersion     string
		ProjectIdentity   string
		OriginCandidateID string
	}{
		SchemaVersion:     ExperienceSchemaVersion,
		ProjectIdentity:   strings.TrimSpace(projectIdentity),
		OriginCandidateID: strings.TrimSpace(originCandidateID),
	})
}

func (value ExperienceProposal) CanonicalContentHash() string {
	payload := struct {
		Type          ExperienceType
		Scope         Scope
		Applicability Applicability
		Guidance      Guidance
		Verifier      Verifier
	}{
		Type:          value.Type,
		Scope:         normalizedScope(value.Scope),
		Applicability: normalizedApplicability(value.Applicability),
		Guidance:      normalizedGuidance(value.Guidance),
		Verifier:      normalizedVerifier(value.Verifier),
	}
	return contentHash(payload)
}

func (value Experience) CanonicalContentHash() string {
	return ExperienceProposal{
		Type:          value.Type,
		Scope:         value.Scope,
		Applicability: value.Applicability,
		Guidance:      value.Guidance,
		Verifier:      value.Verifier,
	}.CanonicalContentHash()
}

func (value EvidenceSet) DeterministicID() string {
	return deterministicID("evs_", struct {
		Availability EvidenceAvailability
		Refs         []EvidenceRef
	}{
		Availability: value.Availability,
		Refs:         normalizedEvidenceRefs(value.Refs),
	})
}

func (value LifecycleTransition) DeterministicID() string {
	return deterministicID("ext_", struct {
		Experience ExperienceRef
		From       LifecycleState
		To         LifecycleState
		Reason     string
		ActorKind  TransitionActorKind
		ActorID    string
		OccurredAt time.Time
		Evidence   []EvidenceRef
	}{
		Experience: value.Experience,
		From:       value.FromState,
		To:         value.ToState,
		Reason:     strings.TrimSpace(value.ReasonCode),
		ActorKind:  value.ActorKind,
		ActorID:    strings.TrimSpace(value.ActorID),
		OccurredAt: utcTime(value.OccurredAt),
		Evidence:   normalizedEvidenceRefs(value.SourceEvidence),
	})
}

func (value Application) DeterministicID() string {
	return deterministicID("exa_", struct {
		SchemaVersion   string
		Experience      ExperienceRef
		ProjectIdentity string
		SessionKey      string
		DeliveryKind    DeliveryKind
		DeliveryState   DeliveryState
		DeliveredAt     *time.Time
	}{
		SchemaVersion:   ApplicationSchemaVersion,
		Experience:      value.Experience,
		ProjectIdentity: strings.TrimSpace(value.ProjectIdentity),
		SessionKey:      strings.TrimSpace(value.SessionKey),
		DeliveryKind:    value.DeliveryKind,
		DeliveryState:   value.DeliveryState,
		DeliveredAt:     normalizedTimePointer(value.DeliveredAt),
	})
}

func (value Evaluation) DeterministicID() string {
	return deterministicID("exv_", struct {
		SchemaVersion    string
		ApplicationID    string
		Experience       ExperienceRef
		EvaluatedAt      time.Time
		OpportunityState OpportunityState
		Applicability    ApplicabilityState
		Verifier         VerifierState
		TaskOutcome      TaskOutcomeState
		Coverage         []CoverageRequirement
		Evidence         []EvidenceRef
		Derivation       string
	}{
		SchemaVersion:    EvaluationSchemaVersion,
		ApplicationID:    strings.TrimSpace(value.ApplicationID),
		Experience:       value.Experience,
		EvaluatedAt:      utcTime(value.EvaluatedAt),
		OpportunityState: value.OpportunityState,
		Applicability:    value.ApplicabilityState,
		Verifier:         value.VerifierState,
		TaskOutcome:      value.TaskOutcomeState,
		Coverage:         normalizedCoverage(value.Coverage),
		Evidence:         normalizedEvidenceRefs(value.SourceEvidence),
		Derivation:       strings.TrimSpace(value.DerivationVersion),
	})
}

func deterministicID(prefix string, value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return prefix + strings.ToLower(idEncoding.EncodeToString(sum[:]))
}

func contentHash(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizedScope(value Scope) Scope {
	value.ProjectIdentity = strings.TrimSpace(value.ProjectIdentity)
	value.SessionKey = strings.TrimSpace(value.SessionKey)
	value.RepositoryPaths = normalizedStrings(value.RepositoryPaths)
	value.TaskFamilies = normalizedStrings(value.TaskFamilies)
	value.Models = normalizedStrings(value.Models)
	value.Harnesses = append([]Harness(nil), value.Harnesses...)
	sort.Slice(value.Harnesses, func(i, j int) bool { return value.Harnesses[i] < value.Harnesses[j] })
	value.Harnesses = compactComparable(value.Harnesses)
	return value
}

func normalizedApplicability(value Applicability) Applicability {
	value.SemanticDescription = strings.TrimSpace(value.SemanticDescription)
	value.Exclusions = normalizedStrings(value.Exclusions)
	value.DeterministicConditions = append([]DeterministicCondition(nil), value.DeterministicConditions...)
	for index := range value.DeterministicConditions {
		value.DeterministicConditions[index].Values = normalizedStrings(value.DeterministicConditions[index].Values)
	}
	sort.Slice(value.DeterministicConditions, func(i, j int) bool {
		left, _ := json.Marshal(value.DeterministicConditions[i])
		right, _ := json.Marshal(value.DeterministicConditions[j])
		return string(left) < string(right)
	})
	value.DeterministicConditions = compactConditions(value.DeterministicConditions)
	if value.ExpiresAt != nil {
		normalized := utcTime(*value.ExpiresAt)
		value.ExpiresAt = &normalized
	}
	return value
}

func normalizedGuidance(value Guidance) Guidance {
	value.Instruction = strings.TrimSpace(value.Instruction)
	value.Rationale = strings.TrimSpace(value.Rationale)
	value.Exceptions = normalizedStrings(value.Exceptions)
	return value
}

func normalizedVerifier(value Verifier) Verifier {
	value.CoverageRequirements = normalizedCoverage(value.CoverageRequirements)
	if value.PathPattern != nil {
		copyValue := *value.PathPattern
		copyValue.Patterns = normalizedStrings(copyValue.Patterns)
		value.PathPattern = &copyValue
	}
	if value.VerificationAfterLastEdit != nil {
		copyValue := *value.VerificationAfterLastEdit
		copyValue.CommandClasses = normalizedStrings(copyValue.CommandClasses)
		value.VerificationAfterLastEdit = &copyValue
	}
	if value.UserCorrectionAbsent != nil {
		copyValue := *value.UserCorrectionAbsent
		copyValue.MarkerFamilies = normalizedStrings(copyValue.MarkerFamilies)
		value.UserCorrectionAbsent = &copyValue
	}
	return value
}

func normalizedCoverage(values []CoverageRequirement) []CoverageRequirement {
	result := append([]CoverageRequirement(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return compactComparable(result)
}

func normalizedEvidenceRefs(values []EvidenceRef) []EvidenceRef {
	result := append([]EvidenceRef(nil), values...)
	for index := range result {
		result[index].SessionKey = strings.TrimSpace(result[index].SessionKey)
		result[index].TurnRole = EvidenceTurnRole(
			strings.TrimSpace(string(result[index].TurnRole)),
		)
		result[index].ToolName = strings.TrimSpace(result[index].ToolName)
		result[index].EventID = strings.TrimSpace(result[index].EventID)
		result[index].OutcomeID = strings.TrimSpace(result[index].OutcomeID)
		result[index].Path = strings.TrimSpace(result[index].Path)
		result[index].SHA256 = strings.TrimSpace(result[index].SHA256)
		result[index].RecordedBy = strings.TrimSpace(result[index].RecordedBy)
		result[index].Excerpt = strings.TrimSpace(result[index].Excerpt)
		result[index].OccurredAt = normalizedTimePointer(result[index].OccurredAt)
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := json.Marshal(result[i])
		right, _ := json.Marshal(result[j])
		return string(left) < string(right)
	})
	return result
}

func normalizedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return compactComparable(result)
}

func normalizedTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := utcTime(*value)
	return &normalized
}

func compactComparable[T comparable](values []T) []T {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func compactConditions(values []DeterministicCondition) []DeterministicCondition {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		previous, _ := json.Marshal(result[len(result)-1])
		current, _ := json.Marshal(value)
		if string(previous) != string(current) {
			result = append(result, value)
		}
	}
	return result
}
