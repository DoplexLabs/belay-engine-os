package localapp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type experienceConflictTestStore struct {
	values       []local.StoredExperience
	queryCount   int
	queryProject string
	queryLimit   int
}

func (store *experienceConflictTestStore) QueryActiveExperiences(
	_ context.Context,
	projectIdentity string,
	limit int,
) ([]local.StoredExperience, error) {
	store.queryCount++
	store.queryProject = projectIdentity
	store.queryLimit = limit
	return append([]local.StoredExperience(nil), store.values...), nil
}

func TestPreflightExperienceConflictIsReadOnly(
	t *testing.T,
) {
	proposal := localappConflictProposal(t, "proposal")
	active := localappConflictExperience(t, "active")

	store := &experienceConflictTestStore{
		values: []local.StoredExperience{
			{
				Experience:       active,
				CurrentLifecycle: experience.LifecycleActive,
			},
		},
	}
	originalProposal, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	originalValues, err := json.Marshal(store.values)
	if err != nil {
		t.Fatal(err)
	}

	result, err := PreflightExperienceConflict(
		context.Background(),
		store,
		proposal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != experience.ConflictPossibleOverlap ||
		!reflect.DeepEqual(
			result.ConflictingExperienceRefs,
			[]experience.ExperienceRef{{
				ExperienceID: active.ExperienceID,
				Version:      active.Version,
			}},
		) {
		t.Fatalf("active-only conflict result = %+v", result)
	}
	if store.queryCount != 1 ||
		store.queryProject != proposal.ProjectIdentity ||
		store.queryLimit != maxExperienceConflictQuery {
		t.Fatalf(
			"conflict query = count %d project %q limit %d",
			store.queryCount,
			store.queryProject,
			store.queryLimit,
		)
	}
	afterProposal, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	afterValues, err := json.Marshal(store.values)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterProposal, originalProposal) ||
		!reflect.DeepEqual(afterValues, originalValues) ||
		proposal.Authority != experience.AuthorityNone {
		t.Fatal("conflict preflight mutated proposal or stored experiences")
	}
}

func TestPreflightExperienceConflictMarksActiveQueryCapUncertainty(
	t *testing.T,
) {
	proposal := localappConflictProposal(t, "capped-proposal")
	proposal.Proposal.Scope.Harnesses = []experience.Harness{
		experience.HarnessClaude,
	}
	proposal.ProposalID = proposal.DeterministicID()
	active := localappConflictExperience(t, "capped-active")
	active.Scope.Harnesses = []experience.Harness{
		experience.HarnessCodex,
	}
	active.ContentHash = active.CanonicalContentHash()
	active.Governance.Approval.ProposedContentHash = active.ContentHash
	active.Governance.Approval.ApprovedContentHash = active.ContentHash

	values := make(
		[]local.StoredExperience,
		maxExperienceConflictQuery,
	)
	for index := range values {
		values[index] = local.StoredExperience{
			Experience:       active,
			CurrentLifecycle: experience.LifecycleActive,
		}
	}
	store := &experienceConflictTestStore{values: values}
	result, err := PreflightExperienceConflict(
		context.Background(),
		store,
		proposal,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != experience.ConflictPossibleOverlap ||
		len(result.ConflictingExperienceRefs) != 0 ||
		!containsConflictReason(
			result.ReasonCodes,
			experience.ConflictReasonActiveQueryCapReached,
		) {
		t.Fatalf("query-cap uncertainty result = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("query-cap uncertainty is invalid: %v", err)
	}
	withoutCap := result
	withoutCap.ReasonCodes = []experience.ConflictReasonCode{
		experience.ConflictReasonDisjointHarnesses,
	}
	if err := withoutCap.Validate(); err == nil {
		t.Fatal("reference-free overlap without query-cap reason was accepted")
	}
}

func containsConflictReason(
	values []experience.ConflictReasonCode,
	want experience.ConflictReasonCode,
) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func localappConflictProposal(
	t *testing.T,
	seed string,
) experience.SemanticProposal {
	t.Helper()
	candidate := experienceSemanticTestCandidate(seed)
	confidence := 0.9
	proposal := candidate.Proposal
	proposal.Confidence = &confidence
	proposal.SemanticCompilationPending = false
	proposal.Guidance = experience.Guidance{
		Instruction:          "Do not modify generated files.",
		Rationale:            "The cited correction requires source edits.",
		InterventionStrength: experience.InterventionAdvise,
	}
	proposal.Verifier = experience.Verifier{
		Kind:                 experience.VerifierPathPatternNotModified,
		CoverageRequirements: []experience.CoverageRequirement{experience.CoverageWorkspaceCaptured},
		PathPattern: &experience.PathPatternVerifierSpec{
			Patterns: []string{"generated/**"},
		},
	}
	value := experience.SemanticProposal{
		SchemaVersion:   experience.SemanticProposalSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Proposal:        proposal,
		Provenance: experience.SemanticProposalProvenance{
			Harness:       experience.HarnessClaude,
			Model:         "claude-test",
			PromptVersion: experience.SemanticProposalPromptVersion,
			InputHash:     sha256Prefixed([]byte("conflict input " + seed)),
			OutputHash:    sha256Prefixed([]byte("conflict output " + seed)),
			GeneratedAt: time.Date(
				2026,
				9,
				10,
				19,
				0,
				0,
				0,
				time.UTC,
			),
		},
		Authority: experience.AuthorityNone,
	}
	value.ProposalID = value.DeterministicID()
	if err := value.Validate(); err != nil {
		t.Fatalf("localapp conflict proposal is invalid: %v", err)
	}
	return value
}

func localappConflictExperience(
	t *testing.T,
	seed string,
) experience.Experience {
	t.Helper()
	proposal := localappConflictProposal(t, seed)
	candidate := experienceSemanticTestCandidate(seed)
	createdAt := time.Date(2026, 9, 10, 19, 5, 0, 0, time.UTC)
	value := experience.Experience{
		SchemaVersion:     experience.ExperienceSchemaVersion,
		OriginCandidateID: candidate.CandidateID,
		Version:           1,
		Type:              proposal.Proposal.Type,
		Scope:             proposal.Proposal.Scope,
		Applicability:     proposal.Proposal.Applicability,
		Guidance:          proposal.Proposal.Guidance,
		Verifier:          proposal.Proposal.Verifier,
		Evidence:          candidate.Evidence,
		Provenance: experience.Provenance{
			ExtractorVersion:  "approval.v1",
			InputHash:         sha256Prefixed([]byte("approval " + seed)),
			SourceCandidateID: candidate.CandidateID,
			GeneratedAt:       createdAt,
		},
		Governance: experience.Governance{
			LifecycleState: experience.LifecycleActive,
			Authority:      experience.AuthorityUserApproved,
		},
		CreatedAt: createdAt,
	}
	value.ExperienceID = experience.DeriveExperienceID(
		value.Scope.ProjectIdentity,
		value.OriginCandidateID,
	)
	value.ContentHash = value.CanonicalContentHash()
	value.Governance.Approval = &experience.ApprovalProvenance{
		ApprovedBy:          "local_user",
		ApprovedAt:          createdAt,
		Mode:                experience.ApprovalAsProposed,
		CandidateID:         candidate.CandidateID,
		ProposedContentHash: value.ContentHash,
		ApprovedContentHash: value.ContentHash,
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("localapp conflict experience is invalid: %v", err)
	}
	return value
}
