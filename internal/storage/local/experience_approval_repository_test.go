package local

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestApproveExperienceProposalAtomicallyCreatesApprovedVersionAndReplays(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	candidate := storageExperienceCandidate("approval transaction")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	proposal := storageSemanticProposal(
		candidate,
		"Use the evidence-backed procedure.",
	)
	proposal.Provenance.PromptVersion =
		experience.SemanticProposalPromptVersion
	proposal.ProposalID = proposal.DeterministicID()
	if _, _, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{proposal},
	); err != nil {
		t.Fatal(err)
	}
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
	input := ExperienceApprovalInput{
		Claims:     claims,
		ApprovedBy: "local_user",
		ApprovedAt: now,
	}
	got, err := store.ApproveExperienceProposal(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Replayed ||
		got.Experience.OriginCandidateID != candidate.CandidateID ||
		got.Experience.ContentHash != claims.ProposedContentHash ||
		got.Experience.Governance.LifecycleState !=
			experience.LifecycleApproved ||
		got.Experience.Governance.Authority !=
			experience.AuthorityUserApproved ||
		got.Experience.Governance.Approval == nil ||
		got.Experience.Governance.Approval.Mode !=
			experience.ApprovalAsProposed {
		t.Fatalf("approved experience = %+v", got)
	}
	stored, err := store.GetExperience(ctx, experience.ExperienceRef{
		ExperienceID: got.Experience.ExperienceID,
		Version:      1,
	})
	if err != nil ||
		stored.CurrentLifecycle != experience.LifecycleApproved {
		t.Fatalf("stored approved experience = %+v/%v", stored, err)
	}
	replay := input
	replay.ApprovedAt = now.Add(time.Second)
	store.clock = func() time.Time { return replay.ApprovedAt }
	replayed, err := store.ApproveExperienceProposal(ctx, replay)
	if err != nil || !replayed.Replayed ||
		replayed.Experience.ExperienceID != got.Experience.ExperienceID {
		t.Fatalf("approval replay = %+v/%v", replayed, err)
	}
}

func TestApproveExperienceProposalRejectsStaleEvidenceGeneration(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 18, 30, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	candidate := storageExperienceCandidate("stale approval")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	proposal := storageSemanticProposal(candidate, "Use the cited workflow.")
	proposal.Provenance.PromptVersion =
		experience.SemanticProposalPromptVersion
	proposal.ProposalID = proposal.DeterministicID()
	if _, _, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{proposal},
	); err != nil {
		t.Fatal(err)
	}
	claims := experience.ApprovalTokenClaims{
		Version:             experience.ApprovalTokenVersion,
		CandidateID:         candidate.CandidateID,
		ProposalID:          proposal.ProposalID,
		ProjectIdentity:     candidate.ProjectIdentity,
		SemanticInputHash:   proposal.Provenance.InputHash,
		ProposedContentHash: proposal.Proposal.CanonicalContentHash(),
		EvidenceGeneration:  storageSHA256("stale evidence generation"),
		IssuedAt:            now.Add(-time.Minute),
		ExpiresAt:           now.Add(14 * time.Minute),
	}
	if _, err := store.ApproveExperienceProposal(
		ctx,
		ExperienceApprovalInput{
			Claims:     claims,
			ApprovedBy: "local_user",
			ApprovedAt: now,
		},
	); !errors.Is(err, ErrExperienceApprovalStale) {
		t.Fatalf("stale approval error = %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM experiences`,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale approval experience count = %d/%v", count, err)
	}
}

func TestApproveExperienceProposalRejectsTokenInvalidatedByReviewAction(
	t *testing.T,
) {
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 19, 30, 0, 0, time.UTC)

	for _, disposition := range []ExperienceReviewDisposition{
		ExperienceReviewDefer,
		ExperienceReviewReject,
	} {
		t.Run(string(disposition), func(t *testing.T) {
			store := openStorageTestStore(t)
			store.clock = func() time.Time { return now }
			_, _, claims := storageExperienceReviewFixture(
				t,
				store,
				"stale "+string(disposition)+" approval",
				now,
			)
			reviewAt := claims.IssuedAt.Add(time.Second)
			review := ExperienceReviewActionInput{
				Claims:      claims,
				Actor:       "user",
				Disposition: disposition,
				OccurredAt:  reviewAt,
			}
			if disposition == ExperienceReviewDefer {
				availableAfter := reviewAt.Add(7 * 24 * time.Hour)
				review.AvailableAfter = &availableAfter
			}
			if _, err := store.RecordExperienceReviewAction(
				ctx,
				review,
			); err != nil {
				t.Fatal(err)
			}

			if _, err := store.ApproveExperienceProposal(
				ctx,
				ExperienceApprovalInput{
					Claims:     claims,
					ApprovedBy: "user",
					ApprovedAt: now,
				},
			); !errors.Is(err, ErrExperienceApprovalStale) {
				t.Fatalf(
					"approval after %s error = %v",
					disposition,
					err,
				)
			}
			var count int
			if err := store.db.QueryRowContext(
				ctx,
				`SELECT COUNT(*) FROM experiences`,
			).Scan(&count); err != nil || count != 0 {
				t.Fatalf(
					"approval after %s experience count = %d/%v",
					disposition,
					count,
					err,
				)
			}

			freshClaims := claims
			freshClaims.IssuedAt = reviewAt.Add(time.Second)
			freshClaims.ExpiresAt = freshClaims.IssuedAt.Add(15 * time.Minute)
			_, freshErr := store.ApproveExperienceProposal(
				ctx,
				ExperienceApprovalInput{
					Claims:     freshClaims,
					ApprovedBy: "user",
					ApprovedAt: now,
				},
			)
			if disposition == ExperienceReviewDefer {
				if freshErr != nil {
					t.Fatalf(
						"fresh approval after explicit defer review = %v",
						freshErr,
					)
				}
			} else if !errors.Is(freshErr, ErrExperienceApprovalStale) {
				t.Fatalf(
					"fresh approval after reject error = %v",
					freshErr,
				)
			}
		})
	}
}

func TestApproveExperienceProposalNarrowsScopeAndRejectsBroadening(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	candidate := storageExperienceCandidate("narrowed approval")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	proposal := storageSemanticProposal(
		candidate,
		"Use the evidence-backed procedure.",
	)
	proposal.Proposal.Scope.RepositoryPaths = []string{
		"generated/**",
		"schemas/**",
	}
	proposal.Provenance.PromptVersion =
		experience.SemanticProposalPromptVersion
	proposal.ProposalID = proposal.DeterministicID()
	if _, _, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{proposal},
	); err != nil {
		t.Fatal(err)
	}
	claims := storageExperienceApprovalRepositoryClaims(
		now,
		candidate,
		proposal,
	)

	broadened := proposal.Proposal
	broadened.Scope.RepositoryPaths = []string{
		"generated/**",
		"schemas/**",
		"other/**",
	}
	if _, err := store.ApproveExperienceProposal(
		ctx,
		ExperienceApprovalInput{
			Claims:           claims,
			ApprovedBy:       "local_user",
			ApprovedAt:       now,
			Mode:             experience.ApprovalNarrowed,
			ApprovedProposal: broadened,
		},
	); !errors.Is(err, ErrExperienceApprovalInvalidContent) {
		t.Fatalf("broadened approval error = %v", err)
	}

	narrowed := proposal.Proposal
	narrowed.Scope.RepositoryPaths = []string{"generated/**"}
	got, err := store.ApproveExperienceProposal(
		ctx,
		ExperienceApprovalInput{
			Claims:           claims,
			ApprovedBy:       "local_user",
			ApprovedAt:       now,
			Mode:             experience.ApprovalNarrowed,
			ApprovedProposal: narrowed,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	approval := got.Experience.Governance.Approval
	if got.Replayed ||
		got.Experience.ContentHash != narrowed.CanonicalContentHash() ||
		got.Experience.Scope.RepositoryPaths[0] != "generated/**" ||
		len(got.Experience.Scope.RepositoryPaths) != 1 ||
		approval == nil ||
		approval.Mode != experience.ApprovalNarrowed ||
		approval.ProposedContentHash !=
			proposal.Proposal.CanonicalContentHash() ||
		approval.ApprovedContentHash != narrowed.CanonicalContentHash() {
		t.Fatalf("narrowed approval = %+v", got)
	}
}

func TestApproveExperienceProposalUserEditedHashesReplayAndConflict(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 19, 30, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	candidate := storageExperienceCandidate("user-edited approval")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	proposal := storageSemanticProposal(
		candidate,
		"Use the proposed procedure.",
	)
	proposal.Provenance.PromptVersion =
		experience.SemanticProposalPromptVersion
	proposal.ProposalID = proposal.DeterministicID()
	if _, _, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{proposal},
	); err != nil {
		t.Fatal(err)
	}
	claims := storageExperienceApprovalRepositoryClaims(
		now,
		candidate,
		proposal,
	)

	if _, err := store.ApproveExperienceProposal(
		ctx,
		ExperienceApprovalInput{
			Claims:           claims,
			ApprovedBy:       "local_user",
			ApprovedAt:       now,
			Mode:             experience.ApprovalUserEdited,
			ApprovedProposal: proposal.Proposal,
		},
	); !errors.Is(err, ErrExperienceApprovalInvalidContent) {
		t.Fatalf("unchanged user-edited approval error = %v", err)
	}

	edited := proposal.Proposal
	edited.Guidance.Instruction = "Use the reviewed procedure."
	input := ExperienceApprovalInput{
		Claims:           claims,
		ApprovedBy:       "local_user",
		ApprovedAt:       now,
		Mode:             experience.ApprovalUserEdited,
		ApprovedProposal: edited,
	}
	got, err := store.ApproveExperienceProposal(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	approval := got.Experience.Governance.Approval
	if got.Replayed ||
		got.Experience.Guidance.Instruction !=
			edited.Guidance.Instruction ||
		got.Experience.ContentHash != edited.CanonicalContentHash() ||
		approval == nil ||
		approval.Mode != experience.ApprovalUserEdited ||
		approval.ProposedContentHash !=
			proposal.Proposal.CanonicalContentHash() ||
		approval.ApprovedContentHash != edited.CanonicalContentHash() {
		t.Fatalf("user-edited approval = %+v", got)
	}

	replayAt := now.Add(time.Second)
	store.clock = func() time.Time { return replayAt }
	input.ApprovedAt = replayAt
	replayed, err := store.ApproveExperienceProposal(ctx, input)
	if err != nil || !replayed.Replayed ||
		replayed.Experience.ContentHash != got.Experience.ContentHash {
		t.Fatalf("user-edited replay = %+v/%v", replayed, err)
	}

	different := edited
	different.Guidance.Instruction = "Use a different reviewed procedure."
	input.ApprovedProposal = different
	if _, err := store.ApproveExperienceProposal(
		ctx,
		input,
	); !errors.Is(err, ErrExperienceApprovalConflict) {
		t.Fatalf("different edited approval error = %v", err)
	}
}

func storageExperienceApprovalRepositoryClaims(
	now time.Time,
	candidate experience.Candidate,
	proposal experience.SemanticProposal,
) experience.ApprovalTokenClaims {
	return experience.ApprovalTokenClaims{
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
}
