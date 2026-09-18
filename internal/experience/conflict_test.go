package experience

import (
	"reflect"
	"testing"
)

func TestSemanticProposalConflictClassifications(t *testing.T) {
	t.Run("no conflict", func(t *testing.T) {
		proposal := conflictTestProposal(t)
		proposal.ProjectIdentity = "git@example.test:doplexlabs/other.git"
		proposal.Proposal.Scope.ProjectIdentity = proposal.ProjectIdentity
		proposal.ProposalID = proposal.DeterministicID()

		result, err := PreflightSemanticProposalConflict(
			proposal,
			[]Experience{validExperience(t, LifecycleActive)},
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.State != ConflictNoConflict ||
			len(result.ConflictingExperienceRefs) != 0 ||
			!reflect.DeepEqual(
				result.ReasonCodes,
				[]ConflictReasonCode{ConflictReasonDifferentProject},
			) {
			t.Fatalf("no-conflict result = %+v", result)
		}
	})

	t.Run("possible overlap", func(t *testing.T) {
		proposal := conflictTestProposal(t)
		active := validExperience(t, LifecycleActive)

		result, err := PreflightSemanticProposalConflict(
			proposal,
			[]Experience{active},
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.State != ConflictPossibleOverlap ||
			!reflect.DeepEqual(
				result.ConflictingExperienceRefs,
				[]ExperienceRef{{
					ExperienceID: active.ExperienceID,
					Version:      active.Version,
				}},
			) ||
			!reflect.DeepEqual(
				result.ReasonCodes,
				[]ConflictReasonCode{ConflictReasonOverlappingScope},
			) {
			t.Fatalf("overlap result = %+v", result)
		}
	})

	t.Run("possible contradiction", func(t *testing.T) {
		proposal := conflictTestProposal(t)
		proposal.Proposal.Verifier = Verifier{
			Kind: VerifierFileModified,
			File: &FileVerifierSpec{
				Path: "generated/api/client.go",
			},
		}
		proposal.ProposalID = proposal.DeterministicID()
		active := validExperience(t, LifecycleActive)

		result, err := PreflightSemanticProposalConflict(
			proposal,
			[]Experience{active},
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.State != ConflictPossibleContradiction ||
			!reflect.DeepEqual(
				result.ReasonCodes,
				[]ConflictReasonCode{
					ConflictReasonPatternVerifierOpposition,
				},
			) {
			t.Fatalf("contradiction result = %+v", result)
		}
	})
}

func TestSemanticProposalConflictIsConservative(t *testing.T) {
	proposal := conflictTestProposal(t)
	proposal.Proposal.Guidance.Instruction =
		"Always modify the generated client."
	proposal.Proposal.Verifier = Verifier{
		Kind: VerifierFileModified,
		File: &FileVerifierSpec{
			Path: "internal/client.go",
		},
	}
	proposal.Proposal.Scope = Scope{
		Kind:            ScopeProject,
		ProjectIdentity: proposal.ProjectIdentity,
		RepositoryPaths: []string{"internal/**"},
	}
	proposal.ProposalID = proposal.DeterministicID()

	active := validExperience(t, LifecycleActive)
	active.Guidance.Instruction = "Never modify generated files."
	active.Scope.Kind = ScopeSession
	active.Scope.SessionKey = "ses_conflict"
	active.ContentHash = active.CanonicalContentHash()
	active.Governance.Approval.ProposedContentHash = active.ContentHash
	active.Governance.Approval.ApprovedContentHash = active.ContentHash

	result, err := PreflightSemanticProposalConflict(
		proposal,
		[]Experience{active},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != ConflictPossibleOverlap ||
		!reflect.DeepEqual(
			result.ReasonCodes,
			[]ConflictReasonCode{ConflictReasonOverlappingScope},
		) {
		t.Fatalf(
			"wording, project/session scope, or unmatched path inferred contradiction: %+v",
			result,
		)
	}

	disjoint := active
	disjoint.Scope.Kind = ScopeProject
	disjoint.Scope.SessionKey = ""
	disjoint.Scope.Harnesses = []Harness{HarnessCodex}
	proposal.Proposal.Scope.Harnesses = []Harness{HarnessClaude}
	disjoint.ContentHash = disjoint.CanonicalContentHash()
	disjoint.Governance.Approval.ProposedContentHash = disjoint.ContentHash
	disjoint.Governance.Approval.ApprovedContentHash = disjoint.ContentHash
	proposal.ProposalID = proposal.DeterministicID()

	result, err = PreflightSemanticProposalConflict(
		proposal,
		[]Experience{disjoint},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != ConflictNoConflict ||
		!reflect.DeepEqual(
			result.ReasonCodes,
			[]ConflictReasonCode{ConflictReasonDisjointHarnesses},
		) {
		t.Fatalf("exact harness disjointness result = %+v", result)
	}
}

func conflictTestProposal(t *testing.T) SemanticProposal {
	t.Helper()
	candidate := validCandidate(t)
	value := SemanticProposal{
		SchemaVersion:   SemanticProposalSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Proposal:        candidate.Proposal,
		Provenance: SemanticProposalProvenance{
			Harness:       HarnessClaude,
			Model:         "claude-test",
			PromptVersion: SemanticProposalPromptVersion,
			InputHash:     sha256Value("conflict input"),
			OutputHash:    sha256Value("conflict output"),
			GeneratedAt:   candidate.CreatedAt,
		},
		Authority: AuthorityNone,
	}
	value.ProposalID = value.DeterministicID()
	if err := value.Validate(); err != nil {
		t.Fatalf("conflict test proposal is invalid: %v", err)
	}
	return value
}
