package experience

import (
	"errors"
	"reflect"
	"time"
)

const ApprovalTokenVersion = "belay.experience-approval-token.v1"

type ApprovalTokenClaims struct {
	Version             string    `json:"version"`
	CandidateID         string    `json:"candidate_id"`
	ProposalID          string    `json:"proposal_id"`
	ProjectIdentity     string    `json:"project_identity"`
	SemanticInputHash   string    `json:"semantic_input_hash"`
	ProposedContentHash string    `json:"proposed_content_hash"`
	EvidenceGeneration  string    `json:"evidence_generation"`
	IssuedAt            time.Time `json:"issued_at"`
	ExpiresAt           time.Time `json:"expires_at"`
}

func (value ApprovalTokenClaims) Validate() error {
	if value.Version != ApprovalTokenVersion {
		return errors.New("experience approval token version is invalid")
	}
	for name, identifier := range map[string]string{
		"candidate ID":     value.CandidateID,
		"proposal ID":      value.ProposalID,
		"project identity": value.ProjectIdentity,
	} {
		if err := validateIdentifier(
			"experience approval "+name,
			identifier,
		); err != nil {
			return err
		}
	}
	if !validSHA256(value.SemanticInputHash) ||
		!validSHA256(value.ProposedContentHash) ||
		!validSHA256(value.EvidenceGeneration) {
		return errors.New("experience approval token hashes must be sha256")
	}
	if value.IssuedAt.IsZero() ||
		value.ExpiresAt.IsZero() ||
		!value.ExpiresAt.After(value.IssuedAt) {
		return errors.New("experience approval token time window is invalid")
	}
	return nil
}

// ValidateApprovedProposal applies the approval-mode-specific content contract
// before immutable experience persistence. Authority and lifecycle are not part
// of ExperienceProposal and are assigned only by the persistence boundary.
func ValidateApprovedProposal(
	original ExperienceProposal,
	approved ExperienceProposal,
	mode ApprovalMode,
) error {
	if mode != ApprovalNarrowed && mode != ApprovalUserEdited {
		return errors.New("edited experience approval mode is invalid")
	}
	if err := approved.Validate(); err != nil {
		return err
	}
	if approved.SemanticCompilationPending {
		return errors.New("approved experience cannot remain pending compilation")
	}
	if approved.Scope.ProjectIdentity != original.Scope.ProjectIdentity {
		return errors.New("approved experience project identity must remain unchanged")
	}
	if mode == ApprovalUserEdited {
		if approved.CanonicalContentHash() ==
			original.CanonicalContentHash() {
			return errors.New("user-edited approval must change experience content")
		}
		return nil
	}
	return validateNarrowedProposal(original, approved)
}

func validateNarrowedProposal(
	original ExperienceProposal,
	approved ExperienceProposal,
) error {
	originalScope := normalizedScope(original.Scope)
	approvedScope := normalizedScope(approved.Scope)
	originalApplicability := normalizedApplicability(original.Applicability)
	approvedApplicability := normalizedApplicability(approved.Applicability)

	if approved.Type != original.Type ||
		approvedScope.Kind != originalScope.Kind ||
		approvedScope.ProjectIdentity != originalScope.ProjectIdentity ||
		approvedScope.SessionKey != originalScope.SessionKey ||
		!reflect.DeepEqual(
			normalizedGuidance(approved.Guidance),
			normalizedGuidance(original.Guidance),
		) ||
		!reflect.DeepEqual(
			normalizedVerifier(approved.Verifier),
			normalizedVerifier(original.Verifier),
		) ||
		approvedApplicability.SemanticDescription !=
			originalApplicability.SemanticDescription ||
		!reflect.DeepEqual(
			approvedApplicability.DeterministicConditions,
			originalApplicability.DeterministicConditions,
		) ||
		!reflect.DeepEqual(
			approvedApplicability.Exclusions,
			originalApplicability.Exclusions,
		) {
		return errors.New("narrowed approval contains a non-scope edit")
	}

	strictlyNarrower := false
	for _, comparison := range []struct {
		original []string
		approved []string
	}{
		{
			original: originalScope.RepositoryPaths,
			approved: approvedScope.RepositoryPaths,
		},
		{
			original: originalScope.TaskFamilies,
			approved: approvedScope.TaskFamilies,
		},
		{
			original: originalScope.Models,
			approved: approvedScope.Models,
		},
	} {
		valid, strict := narrowedComparableSet(
			comparison.original,
			comparison.approved,
		)
		if !valid {
			return errors.New("narrowed approval broadens a scope list")
		}
		strictlyNarrower = strictlyNarrower || strict
	}
	valid, strict := narrowedComparableSet(
		originalScope.Harnesses,
		approvedScope.Harnesses,
	)
	if !valid {
		return errors.New("narrowed approval broadens a scope list")
	}
	strictlyNarrower = strictlyNarrower || strict

	if !narrowedExpiry(
		originalApplicability.ExpiresAt,
		approvedApplicability.ExpiresAt,
	) {
		return errors.New("narrowed approval broadens applicability expiration")
	}
	if !strictlyNarrower {
		return errors.New("narrowed approval must restrict at least one scope list")
	}
	return nil
}

func narrowedComparableSet[T comparable](
	original []T,
	approved []T,
) (valid bool, strict bool) {
	if len(original) == 0 {
		return true, len(approved) > 0
	}
	if len(approved) == 0 {
		return false, false
	}
	allowed := make(map[T]struct{}, len(original))
	for _, value := range original {
		allowed[value] = struct{}{}
	}
	for _, value := range approved {
		if _, found := allowed[value]; !found {
			return false, false
		}
	}
	return true, len(approved) < len(original)
}

func narrowedExpiry(original, approved *time.Time) bool {
	if original == nil {
		return true
	}
	if approved == nil {
		return false
	}
	return !approved.After(*original)
}
