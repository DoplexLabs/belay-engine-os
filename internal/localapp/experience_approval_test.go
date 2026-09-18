package localapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

type experienceApprovalServiceTestStore struct {
	*experienceReviewTestStore
	issuedClaims experience.ApprovalTokenClaims
	decoded      experience.ApprovalTokenClaims
	approveInput local.ExperienceApprovalInput
	decodeErr    error
	approveErr   error
}

func (store *experienceApprovalServiceTestStore) IssueExperienceApprovalToken(
	claims experience.ApprovalTokenClaims,
) (string, error) {
	store.issuedClaims = claims
	return "signed-experience-approval", nil
}

func (store *experienceApprovalServiceTestStore) DecodeExperienceApprovalToken(
	_ string,
) (experience.ApprovalTokenClaims, error) {
	return store.decoded, store.decodeErr
}

func (store *experienceApprovalServiceTestStore) ApproveExperienceProposal(
	_ context.Context,
	input local.ExperienceApprovalInput,
) (local.ExperienceApprovalResult, error) {
	store.approveInput = input
	return local.ExperienceApprovalResult{
		Experience: experience.Experience{
			ExperienceID: "exp_approved",
		},
	}, store.approveErr
}

func TestExperienceApprovalServicePreparesBoundTokenAndApprovesRoute(t *testing.T) {
	now := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	candidate := experienceSemanticTestCandidate("approval service")
	candidate.OutcomeRefs = nil
	candidate.CandidateID = candidate.DeterministicID()
	proposal := experienceReviewProposal(t, candidate)
	store := &experienceApprovalServiceTestStore{
		experienceReviewTestStore: &experienceReviewTestStore{
			candidate: candidate,
			proposal:  proposal,
			outcomes:  map[string]trajectory.Outcome{},
		},
	}
	service, err := NewExperienceApprovalService(
		store,
		WithExperienceApprovalClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Prepare(
		context.Background(),
		proposal.ProposalID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ActionToken != "signed-experience-approval" ||
		!preview.ExpiresAt.Equal(now.Add(experienceApprovalPreviewLifetime)) ||
		store.issuedClaims.CandidateID != candidate.CandidateID ||
		store.issuedClaims.ProposalID != proposal.ProposalID ||
		store.issuedClaims.ProjectIdentity != candidate.ProjectIdentity ||
		store.issuedClaims.ProposedContentHash !=
			proposal.Proposal.CanonicalContentHash() ||
		store.issuedClaims.EvidenceGeneration !=
			candidate.Provenance.InputHash {
		t.Fatalf("approval preview/claims = %+v/%+v", preview, store.issuedClaims)
	}

	store.decoded = store.issuedClaims
	result, err := service.ApproveAsProposed(
		context.Background(),
		proposal.ProposalID,
		preview.ActionToken,
		"local_user",
	)
	if err != nil ||
		result.Experience.ExperienceID != "exp_approved" ||
		store.approveInput.Claims != store.issuedClaims ||
		store.approveInput.ApprovedBy != "local_user" ||
		!store.approveInput.ApprovedAt.Equal(now) {
		t.Fatalf(
			"approval result/input = %+v/%+v/%v",
			result,
			store.approveInput,
			err,
		)
	}
}

func TestExperienceApprovalServiceRejectsExpiredAndRouteMismatchedToken(
	t *testing.T,
) {
	candidate := experienceSemanticTestCandidate("approval errors")
	candidate.OutcomeRefs = nil
	candidate.CandidateID = candidate.DeterministicID()
	proposal := experienceReviewProposal(t, candidate)
	store := &experienceApprovalServiceTestStore{
		experienceReviewTestStore: &experienceReviewTestStore{
			candidate: candidate,
			proposal:  proposal,
			outcomes:  map[string]trajectory.Outcome{},
		},
		decodeErr: local.ErrExperienceApprovalTokenExpired,
	}
	service, err := NewExperienceApprovalService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveAsProposed(
		context.Background(),
		proposal.ProposalID,
		"expired",
		"local_user",
	); !errors.Is(err, ErrExperienceApprovalExpired) {
		t.Fatalf("expired approval error = %v", err)
	}
	store.decodeErr = nil
	store.decoded = experience.ApprovalTokenClaims{
		ProposalID: "exs_other",
	}
	if _, err := service.ApproveAsProposed(
		context.Background(),
		proposal.ProposalID,
		"signed",
		"local_user",
	); !errors.Is(err, ErrExperienceApprovalInvalidRequest) {
		t.Fatalf("route-mismatched approval error = %v", err)
	}
	edited := proposal.Proposal
	edited.Guidance.Instruction = "Use the user-reviewed procedure."
	if _, err := service.ApproveWithContent(
		context.Background(),
		ExperienceApprovalWithContentRequest{
			ProposalID:      proposal.ProposalID,
			ActionToken:     "signed",
			Actor:           "local_user",
			Mode:            experience.ApprovalUserEdited,
			ApprovedContent: edited,
		},
	); !errors.Is(err, ErrExperienceApprovalInvalidRequest) {
		t.Fatalf("edited route-mismatched approval error = %v", err)
	}
	store.decoded.ProposalID = proposal.ProposalID
	store.approveErr = local.ErrExperienceApprovalStale
	if _, err := service.ApproveAsProposed(
		context.Background(),
		proposal.ProposalID,
		"signed",
		"local_user",
	); !errors.Is(err, local.ErrExperienceApprovalStale) {
		t.Fatalf("stale review-state approval error = %v", err)
	}
}

func TestExperienceApprovalServiceRoutesUserProvidedContent(t *testing.T) {
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	candidate := experienceSemanticTestCandidate("edited approval service")
	candidate.OutcomeRefs = nil
	candidate.CandidateID = candidate.DeterministicID()
	proposal := experienceReviewProposal(t, candidate)
	claims := experience.ApprovalTokenClaims{
		Version:             experience.ApprovalTokenVersion,
		CandidateID:         candidate.CandidateID,
		ProposalID:          proposal.ProposalID,
		ProjectIdentity:     candidate.ProjectIdentity,
		SemanticInputHash:   proposal.Provenance.InputHash,
		ProposedContentHash: proposal.Proposal.CanonicalContentHash(),
		EvidenceGeneration:  candidate.Provenance.InputHash,
		IssuedAt:            now.Add(-time.Minute),
		ExpiresAt:           now.Add(14 * time.Minute),
	}
	store := &experienceApprovalServiceTestStore{
		experienceReviewTestStore: &experienceReviewTestStore{
			candidate: candidate,
			proposal:  proposal,
			outcomes:  map[string]trajectory.Outcome{},
		},
		decoded: claims,
	}
	service, err := NewExperienceApprovalService(
		store,
		WithExperienceApprovalClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	narrowed := proposal.Proposal
	narrowed.Scope.RepositoryPaths = []string{"internal/**"}
	result, err := service.ApproveWithContent(
		context.Background(),
		ExperienceApprovalWithContentRequest{
			ProposalID:      proposal.ProposalID,
			ActionToken:     "signed",
			Actor:           "local_user",
			Mode:            experience.ApprovalNarrowed,
			ApprovedContent: narrowed,
		},
	)
	if err != nil ||
		result.Experience.ExperienceID != "exp_approved" ||
		store.approveInput.Claims != claims ||
		store.approveInput.ApprovedBy != "local_user" ||
		store.approveInput.Mode != experience.ApprovalNarrowed ||
		store.approveInput.ApprovedProposal.CanonicalContentHash() !=
			narrowed.CanonicalContentHash() ||
		!store.approveInput.ApprovedAt.Equal(now) {
		t.Fatalf(
			"edited approval result/input = %+v/%+v/%v",
			result,
			store.approveInput,
			err,
		)
	}
}
