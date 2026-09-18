package local

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestExperienceSemanticProposalEncryptedRoundTripIdempotenceAndConflict(
	t *testing.T,
) {
	const canary = "SEMANTIC_PROPOSAL_SECRET_CANARY"
	ctx := context.Background()
	store := openStorageTestStore(t)
	candidate := storageExperienceCandidate("semantic proposal")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	proposal := storageSemanticProposal(candidate, canary)
	inserted, replayed, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{proposal},
	)
	if err != nil || inserted != 1 || replayed != 0 {
		t.Fatalf("insert proposal = %d/%d/%v", inserted, replayed, err)
	}

	var payload []byte
	if err := store.db.QueryRowContext(ctx, `
		SELECT payload
		FROM experience_semantic_proposals
		WHERE proposal_id = ?`,
		proposal.ProposalID,
	).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(canary)) {
		t.Fatal("semantic proposal guidance was stored in plaintext")
	}
	got, err := store.GetExperienceSemanticProposal(
		ctx,
		proposal.ProposalID,
	)
	if err != nil ||
		got.Proposal.Guidance.Instruction != canary ||
		got.Provenance != proposal.Provenance {
		t.Fatalf("semantic proposal round trip = %+v/%v", got, err)
	}
	exists, err := store.HasExperienceSemanticProposal(
		ctx,
		candidate.CandidateID,
		proposal.Provenance.Harness,
		proposal.Provenance.PromptVersion,
	)
	if err != nil || !exists {
		t.Fatalf("semantic proposal existence = %v/%v", exists, err)
	}
	values, err := store.QueryExperienceSemanticProposals(
		ctx,
		candidate.ProjectIdentity,
		10,
	)
	if err != nil || len(values) != 1 ||
		values[0].ProposalID != proposal.ProposalID {
		t.Fatalf("semantic proposal query = %+v/%v", values, err)
	}

	inserted, replayed, err = store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{proposal},
	)
	if err != nil || inserted != 0 || replayed != 1 {
		t.Fatalf("replay proposal = %d/%d/%v", inserted, replayed, err)
	}

	conflict := proposal
	conflict.Provenance.GeneratedAt = conflict.Provenance.GeneratedAt.Add(
		time.Second,
	)
	if conflict.DeterministicID() != proposal.ProposalID {
		t.Fatal("generated_at unexpectedly changed proposal identity")
	}
	_, _, err = store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{conflict},
	)
	if !errors.Is(err, ErrExperienceSemanticProposalConflict) {
		t.Fatalf("semantic proposal conflict error = %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `
		UPDATE experience_semantic_proposals
		SET prompt_version = 'changed'
		WHERE proposal_id = ?`,
		proposal.ProposalID,
	); err == nil {
		t.Fatal("direct semantic proposal update bypassed mutation guard")
	}
	if _, err := store.db.ExecContext(ctx, `
		DELETE FROM experience_semantic_proposals
		WHERE proposal_id = ?`,
		proposal.ProposalID,
	); err == nil {
		t.Fatal("direct semantic proposal delete bypassed mutation guard")
	}
}

func TestExperienceSemanticProposalBatchRollsBackOnMissingCandidate(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	candidate := storageExperienceCandidate("semantic batch")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	first := storageSemanticProposal(candidate, "Use the cited workflow.")
	missingCandidate := storageExperienceCandidate("missing semantic candidate")
	second := storageSemanticProposal(
		missingCandidate,
		"Do not persist this proposal.",
	)

	if _, _, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{first, second},
	); err == nil {
		t.Fatal("semantic batch with missing candidate was accepted")
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM experience_semantic_proposals`,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back semantic proposal count = %d/%v", count, err)
	}
}

func TestExperienceSemanticProposalUniquenessIsScopedByHarnessAndPrompt(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	candidate := storageExperienceCandidate("semantic source identity")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	claude := storageSemanticProposal(candidate, "Use the cited workflow.")
	if _, _, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{claude},
	); err != nil {
		t.Fatal(err)
	}

	codex := claude
	codex.Provenance.Harness = experience.HarnessCodex
	codex.Provenance.Model = "codex-test"
	codex.Provenance.InputHash = storageSHA256("codex semantic input")
	codex.Provenance.OutputHash = storageSHA256("codex semantic output")
	codex.ProposalID = codex.DeterministicID()
	inserted, replayed, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{codex},
	)
	if err != nil || inserted != 1 || replayed != 0 {
		t.Fatalf("codex proposal = %d/%d/%v", inserted, replayed, err)
	}

	for _, harness := range []experience.Harness{
		experience.HarnessClaude,
		experience.HarnessCodex,
	} {
		exists, err := store.HasExperienceSemanticProposal(
			ctx,
			candidate.CandidateID,
			harness,
			claude.Provenance.PromptVersion,
		)
		if err != nil || !exists {
			t.Fatalf("%s proposal existence = %v/%v", harness, exists, err)
		}
	}

	conflict := claude
	conflict.Proposal.Guidance.Instruction = "Use different guidance."
	conflict.ProposalID = conflict.DeterministicID()
	if conflict.ProposalID == claude.ProposalID {
		t.Fatal("changed content did not change proposal identity")
	}
	if _, _, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{conflict},
	); !errors.Is(err, ErrExperienceSemanticProposalConflict) {
		t.Fatalf("same source tuple conflict = %v", err)
	}

	values, err := store.QueryExperienceSemanticProposals(
		ctx,
		candidate.ProjectIdentity,
		10,
	)
	if err != nil || len(values) != 2 {
		t.Fatalf("harness-scoped proposals = %d/%v", len(values), err)
	}
}

func TestExperienceSemanticResultsEncryptedBranchesReplayAndMutationGuards(
	t *testing.T,
) {
	const canary = "SEMANTIC_DECISION_SECRET_CANARY"
	ctx := context.Background()
	store := openStorageTestStore(t)
	proposedCandidate := storageExperienceCandidate("semantic result propose")
	rejectedCandidate := storageExperienceCandidate("semantic result reject")
	deferredCandidate := storageExperienceCandidate("semantic result defer")
	for _, candidate := range []experience.Candidate{
		proposedCandidate,
		rejectedCandidate,
		deferredCandidate,
	} {
		if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	results := []experience.SemanticResult{
		storageSemanticResult(
			proposedCandidate,
			experience.SemanticDispositionPropose,
			experience.SemanticReasonReusableSupported,
			canary,
		),
		storageSemanticResult(
			rejectedCandidate,
			experience.SemanticDispositionReject,
			experience.SemanticReasonNotReusable,
			"The cited behavior is not reusable.",
		),
		storageSemanticResult(
			deferredCandidate,
			experience.SemanticDispositionDefer,
			experience.SemanticReasonInsufficientContext,
			"The cited evidence is insufficient.",
		),
	}
	inserted, replayed, err := store.StoreExperienceSemanticResults(ctx, results)
	if err != nil ||
		inserted != (experience.SemanticDispositionCounts{
			Proposed: 1,
			Rejected: 1,
			Deferred: 1,
		}) ||
		replayed != (experience.SemanticDispositionCounts{}) {
		t.Fatalf("store semantic branches = %+v/%+v/%v", inserted, replayed, err)
	}

	var decisionProject string
	var decisionPayload []byte
	if err := store.db.QueryRowContext(ctx, `
		SELECT project_identity, payload
		FROM experience_semantic_decisions
		WHERE decision_id = ?`,
		results[0].Decision.DecisionID,
	).Scan(&decisionProject, &decisionPayload); err != nil {
		t.Fatal(err)
	}
	if decisionProject != proposedCandidate.ProjectIdentity {
		t.Fatalf("semantic decision project index = %q", decisionProject)
	}
	if bytes.Contains(decisionPayload, []byte(canary)) {
		t.Fatal("semantic decision explanation was stored in plaintext")
	}
	if bytes.Contains(
		decisionPayload,
		[]byte(proposedCandidate.ProjectIdentity),
	) {
		t.Fatal("semantic decision payload was stored in plaintext")
	}
	decision, err := store.GetExperienceSemanticDecision(
		ctx,
		results[0].Decision.DecisionID,
	)
	if err != nil ||
		decision.Explanation != canary ||
		decision.ProjectIdentity != proposedCandidate.ProjectIdentity ||
		decision.ProposalID != results[0].Proposal.ProposalID {
		t.Fatalf("semantic decision round trip = %+v/%v", decision, err)
	}
	for _, result := range results {
		exists, err := store.HasExperienceSemanticResult(
			ctx,
			result.Decision.CandidateID,
			result.Decision.Provenance.Harness,
			result.Decision.Provenance.PromptVersion,
		)
		if err != nil || !exists {
			t.Fatalf("semantic result existence = %v/%v", exists, err)
		}
	}
	var proposalCount int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM experience_semantic_proposals`,
	).Scan(&proposalCount); err != nil || proposalCount != 1 {
		t.Fatalf("semantic proposal count = %d/%v", proposalCount, err)
	}

	inserted, replayed, err = store.StoreExperienceSemanticResults(ctx, results)
	if err != nil ||
		inserted != (experience.SemanticDispositionCounts{}) ||
		replayed != (experience.SemanticDispositionCounts{
			Proposed: 1,
			Rejected: 1,
			Deferred: 1,
		}) {
		t.Fatalf("replay semantic branches = %+v/%+v/%v", inserted, replayed, err)
	}
	codex := results[1]
	codex.Decision.Provenance.Harness = experience.HarnessCodex
	codex.Decision.Provenance.Model = "codex-test"
	codex.Decision.Provenance.InputHash = storageSHA256("codex decision input")
	codex.Decision.Provenance.OutputHash = storageSHA256("codex decision output")
	codex.Decision.DecisionID = codex.Decision.DeterministicID()
	inserted, replayed, err = store.StoreExperienceSemanticResults(
		ctx,
		[]experience.SemanticResult{codex},
	)
	if err != nil ||
		inserted.Rejected != 1 ||
		replayed != (experience.SemanticDispositionCounts{}) {
		t.Fatalf("codex semantic result = %+v/%+v/%v", inserted, replayed, err)
	}
	for _, harness := range []experience.Harness{
		experience.HarnessClaude,
		experience.HarnessCodex,
	} {
		exists, err := store.HasExperienceSemanticResult(
			ctx,
			rejectedCandidate.CandidateID,
			harness,
			results[1].Decision.Provenance.PromptVersion,
		)
		if err != nil || !exists {
			t.Fatalf("%s semantic result existence = %v/%v", harness, exists, err)
		}
	}

	if _, err := store.db.ExecContext(ctx, `
		UPDATE experience_semantic_decisions
		SET reason_code = 'unsafe_or_overbroad'
		WHERE decision_id = ?`,
		results[1].Decision.DecisionID,
	); err == nil {
		t.Fatal("direct semantic decision update bypassed mutation guard")
	}
	if _, err := store.db.ExecContext(ctx, `
		DELETE FROM experience_semantic_decisions
		WHERE decision_id = ?`,
		results[2].Decision.DecisionID,
	); err == nil {
		t.Fatal("direct semantic decision delete bypassed mutation guard")
	}
	if _, err := store.db.ExecContext(ctx, `
		DROP TRIGGER temp.belay_guard_experience_semantic_decisions_update`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE experience_semantic_decisions
		SET project_identity = 'git@example.test:doplexlabs/other.git'
		WHERE decision_id = ?`,
		results[0].Decision.DecisionID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetExperienceSemanticDecision(
		ctx,
		results[0].Decision.DecisionID,
	); err == nil || !strings.Contains(err.Error(), "index does not match payload") {
		t.Fatalf("semantic decision index mismatch error = %v", err)
	}
}

func TestExperienceSemanticResultRejectsMismatchedDecisionProject(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	candidate := storageExperienceCandidate("semantic decision project mismatch")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	result := storageSemanticResult(
		candidate,
		experience.SemanticDispositionDefer,
		experience.SemanticReasonInsufficientContext,
		"The cited evidence is insufficient.",
	)
	result.Decision.ProjectIdentity = "git@example.test:doplexlabs/other.git"
	result.Decision.DecisionID = result.Decision.DeterministicID()
	if _, _, err := store.StoreExperienceSemanticResults(
		ctx,
		[]experience.SemanticResult{result},
	); err == nil || !strings.Contains(err.Error(), "candidate project mismatch") {
		t.Fatalf("mismatched decision project error = %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM experience_semantic_decisions`,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("mismatched decision row count = %d/%v", count, err)
	}
}

func TestExperienceSemanticResultBatchRollsBackOnConflict(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	existingCandidate := storageExperienceCandidate("semantic result conflict")
	newCandidate := storageExperienceCandidate("semantic result rollback")
	for _, candidate := range []experience.Candidate{existingCandidate, newCandidate} {
		if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	existing := storageSemanticResult(
		existingCandidate,
		experience.SemanticDispositionReject,
		experience.SemanticReasonNotReusable,
		"The cited behavior is not reusable.",
	)
	if _, _, err := store.StoreExperienceSemanticResults(
		ctx,
		[]experience.SemanticResult{existing},
	); err != nil {
		t.Fatal(err)
	}
	conflict := existing
	conflict.Decision.Explanation = "Conflicting immutable content."
	conflict.Decision.DecisionID = conflict.Decision.DeterministicID()
	newResult := storageSemanticResult(
		newCandidate,
		experience.SemanticDispositionPropose,
		experience.SemanticReasonReusableSupported,
		"Use the cited reusable workflow.",
	)
	if _, _, err := store.StoreExperienceSemanticResults(
		ctx,
		[]experience.SemanticResult{newResult, conflict},
	); !errors.Is(err, ErrExperienceSemanticResultConflict) {
		t.Fatalf("semantic result conflict = %v", err)
	}
	var decisions, proposals int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM experience_semantic_decisions
		WHERE candidate_id = ?`,
		newCandidate.CandidateID,
	).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM experience_semantic_proposals
		WHERE candidate_id = ?`,
		newCandidate.CandidateID,
	).Scan(&proposals); err != nil {
		t.Fatal(err)
	}
	if decisions != 0 || proposals != 0 {
		t.Fatalf("rolled-back semantic rows = decisions %d proposals %d", decisions, proposals)
	}
}

func TestLegacySemanticProposalCountsAsExistingResult(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	candidate := storageExperienceCandidate("legacy semantic proposal")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	proposal := storageSemanticProposal(candidate, "Use the cited workflow.")
	if _, _, err := store.InsertExperienceSemanticProposals(
		ctx,
		[]experience.SemanticProposal{proposal},
	); err != nil {
		t.Fatal(err)
	}
	exists, err := store.HasExperienceSemanticResult(
		ctx,
		candidate.CandidateID,
		proposal.Provenance.Harness,
		proposal.Provenance.PromptVersion,
	)
	if err != nil || !exists {
		t.Fatalf("legacy proposal result existence = %v/%v", exists, err)
	}
}

func storageSemanticProposal(
	candidate experience.Candidate,
	guidance string,
) experience.SemanticProposal {
	proposal := candidate.Proposal
	proposal.Guidance.Instruction = guidance
	proposal.SemanticCompilationPending = false
	if proposal.Confidence == nil {
		confidence := 0.9
		proposal.Confidence = &confidence
	}
	value := experience.SemanticProposal{
		SchemaVersion:   experience.SemanticProposalSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Proposal:        proposal,
		Provenance: experience.SemanticProposalProvenance{
			Harness:       experience.HarnessClaude,
			Model:         "claude-test",
			PromptVersion: "belay.experience-prompt.v1",
			InputHash:     storageSHA256("semantic proposal input"),
			OutputHash:    storageSHA256("semantic proposal output"),
			GeneratedAt: time.Date(
				2026,
				9,
				10,
				16,
				0,
				0,
				0,
				time.UTC,
			),
		},
		Authority: experience.AuthorityNone,
	}
	value.ProposalID = value.DeterministicID()
	return value
}

func storageSemanticResult(
	candidate experience.Candidate,
	disposition experience.SemanticDisposition,
	reason experience.SemanticDecisionReasonCode,
	explanation string,
) experience.SemanticResult {
	proposal := storageSemanticProposal(candidate, "Use the cited workflow.")
	decision := experience.SemanticDecision{
		SchemaVersion:   experience.SemanticDecisionSchemaVersion,
		CandidateID:     candidate.CandidateID,
		ProjectIdentity: candidate.ProjectIdentity,
		Disposition:     disposition,
		ReasonCode:      reason,
		Explanation:     explanation,
		Confidence:      0.9,
		Provenance:      proposal.Provenance,
	}
	result := experience.SemanticResult{Decision: decision}
	if disposition == experience.SemanticDispositionPropose {
		result.Proposal = &proposal
		result.Decision.ProposalID = proposal.ProposalID
	}
	result.Decision.DecisionID = result.Decision.DeterministicID()
	return result
}
