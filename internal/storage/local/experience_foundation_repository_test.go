package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

func TestExperienceFoundationEncryptedRoundTrips(t *testing.T) {
	const canary = "PRIVATE_EXPERIENCE_CANARY_8f7d"
	ctx := context.Background()
	store := openStorageTestStore(t)

	candidate := storageExperienceCandidate(canary)
	if inserted, err := store.InsertExperienceCandidate(ctx, candidate); err != nil || !inserted {
		t.Fatalf("InsertExperienceCandidate() = %v, %v", inserted, err)
	}
	gotCandidate, err := store.GetExperienceCandidate(ctx, candidate.CandidateID)
	if err != nil || !reflect.DeepEqual(gotCandidate, candidate) {
		t.Fatalf("GetExperienceCandidate() = %+v, %v", gotCandidate, err)
	}

	value := storageExperience(candidate, experience.LifecycleActive)
	if inserted, err := store.InsertExperience(ctx, value); err != nil || !inserted {
		t.Fatalf("InsertExperience() = %v, %v", inserted, err)
	}
	gotExperience, err := store.GetExperience(ctx, experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	})
	if err != nil || !reflect.DeepEqual(gotExperience.Experience, value) ||
		gotExperience.CurrentLifecycle != experience.LifecycleActive ||
		gotExperience.ActivatedAt == nil ||
		!gotExperience.ActivatedAt.Equal(value.Governance.Approval.ApprovedAt) ||
		!optionalTimeEqual(gotExperience.ExpiresAt, value.Applicability.ExpiresAt) {
		t.Fatalf("GetExperience() = %+v, %v", gotExperience, err)
	}
	var activatedAt, expiresAt string
	if err := store.db.QueryRowContext(ctx, `
		SELECT activated_at, expires_at
		FROM experiences
		WHERE experience_id = ? AND version = ?`,
		value.ExperienceID,
		value.Version,
	).Scan(&activatedAt, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if activatedAt != formatProjectionTime(value.Governance.Approval.ApprovedAt) ||
		expiresAt != formatProjectionTime(*value.Applicability.ExpiresAt) {
		t.Fatalf("experience activation/expiry indexes = %q/%q", activatedAt, expiresAt)
	}
	gotEvidence, err := store.GetExperienceEvidence(ctx, experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	})
	if err != nil || !reflect.DeepEqual(gotEvidence, value.Evidence) {
		t.Fatalf("GetExperienceEvidence() = %+v, %v", gotEvidence, err)
	}

	edge := storageTrajectoryEdge()
	if inserted, err := store.InsertTrajectoryEdge(ctx, edge); err != nil || !inserted {
		t.Fatalf("InsertTrajectoryEdge() = %v, %v", inserted, err)
	}
	if got, err := store.GetTrajectoryEdge(ctx, edge.EdgeID); err != nil || !reflect.DeepEqual(got, edge) {
		t.Fatalf("GetTrajectoryEdge() = %+v, %v", got, err)
	}

	outcome := storageTrajectoryOutcome()
	if inserted, err := store.InsertOutcome(ctx, outcome); err != nil || !inserted {
		t.Fatalf("InsertOutcome() = %v, %v", inserted, err)
	}
	if got, err := store.GetOutcome(ctx, outcome.OutcomeID); err != nil || !reflect.DeepEqual(got, outcome) {
		t.Fatalf("GetOutcome() = %+v, %v", got, err)
	}

	application := storageExperienceApplication(value)
	if inserted, err := store.InsertExperienceApplication(ctx, application); err != nil || !inserted {
		t.Fatalf("InsertExperienceApplication() = %v, %v", inserted, err)
	}
	if got, err := store.GetExperienceApplication(ctx, application.ApplicationID); err != nil ||
		!reflect.DeepEqual(got, application) {
		t.Fatalf("GetExperienceApplication() = %+v, %v", got, err)
	}

	generation := storageExperienceGeneration(1)
	if got, err := store.ActivateExperienceGeneration(ctx, generation); err != nil ||
		!reflect.DeepEqual(got, generation) {
		t.Fatalf("ActivateExperienceGeneration() = %+v, %v", got, err)
	}
	if got, err := store.GetActiveExperienceGeneration(ctx, generation.ProjectIdentity); err != nil ||
		!reflect.DeepEqual(got, generation) {
		t.Fatalf("GetActiveExperienceGeneration() = %+v, %v", got, err)
	}

	receipt := storageMissionPackReceipt(value)
	if inserted, err := store.RecordPendingMissionPackReceipt(ctx, receipt); err != nil || !inserted {
		t.Fatalf("RecordPendingMissionPackReceipt() = %v, %v", inserted, err)
	}
	if got, err := store.GetMissionPackReceipt(ctx, receipt.ReceiptID); err != nil ||
		!reflect.DeepEqual(got, receipt) {
		t.Fatalf("GetMissionPackReceipt() = %+v, %v", got, err)
	}

	for _, query := range []struct {
		table string
		where string
		arg   any
	}{
		{"experience_candidates", "candidate_id = ?", candidate.CandidateID},
		{"experiences", "experience_id = ? AND version = 1", value.ExperienceID},
		{"experience_evidence", "experience_id = ? AND experience_version = 1", value.ExperienceID},
		{"trajectory_edges", "edge_id = ?", edge.EdgeID},
		{"outcome_observations", "outcome_id = ?", outcome.OutcomeID},
		{"experience_applications", "application_id = ?", application.ApplicationID},
		{"mission_pack_receipts", "receipt_id = ?", receipt.ReceiptID},
	} {
		var payload []byte
		if err := store.db.QueryRowContext(
			ctx,
			"SELECT payload FROM "+query.table+" WHERE "+query.where,
			query.arg,
		).Scan(&payload); err != nil {
			t.Fatalf("read %s ciphertext: %v", query.table, err)
		}
		if bytes.Contains(payload, []byte(canary)) {
			t.Fatalf("%s payload exposed plaintext canary", query.table)
		}
	}
}

func TestExperienceVersionConflictAndTransitionAtomicity(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	candidate := storageExperienceCandidate("atomicity")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleActive)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}

	conflict := value
	conflict.Guidance.Rationale = "Conflicting immutable content."
	conflict.ContentHash = conflict.CanonicalContentHash()
	conflict.Governance.Approval.ProposedContentHash = conflict.ContentHash
	conflict.Governance.Approval.ApprovedContentHash = conflict.ContentHash
	if _, err := store.InsertExperience(ctx, conflict); !errors.Is(err, ErrExperienceVersionConflict) {
		t.Fatalf("conflicting immutable version error = %v", err)
	}

	ref := experience.ExperienceRef{ExperienceID: value.ExperienceID, Version: 1}
	first := experience.LifecycleTransition{
		Experience: ref,
		FromState:  experience.LifecycleActive,
		ToState:    experience.LifecyclePaused,
		ReasonCode: "user_paused",
		ActorKind:  experience.ActorUser,
		ActorID:    "local_user",
		OccurredAt: value.CreatedAt.Add(time.Minute),
	}
	first.TransitionID = first.DeterministicID()
	if inserted, err := store.AppendExperienceTransition(ctx, first); err != nil || !inserted {
		t.Fatalf("AppendExperienceTransition() = %v, %v", inserted, err)
	}

	stale := experience.LifecycleTransition{
		Experience: ref,
		FromState:  experience.LifecycleActive,
		ToState:    experience.LifecycleContradicted,
		ReasonCode: "stale_writer",
		ActorKind:  experience.ActorDeterministicWorker,
		ActorID:    "worker_v1",
		OccurredAt: first.OccurredAt.Add(time.Minute),
	}
	stale.TransitionID = stale.DeterministicID()
	if _, err := store.AppendExperienceTransition(ctx, stale); !errors.Is(err, ErrExperienceLifecycleConflict) {
		t.Fatalf("stale transition error = %v", err)
	}
	stored, err := store.GetExperience(ctx, ref)
	if err != nil || stored.CurrentLifecycle != experience.LifecyclePaused ||
		stored.ActivatedAt != nil {
		t.Fatalf("lifecycle after stale transition = %+v, %v", stored, err)
	}
	transitions, err := store.ListExperienceTransitions(ctx, ref, 10)
	if err != nil || len(transitions) != 1 || transitions[0].TransitionID != first.TransitionID {
		t.Fatalf("transitions after stale write = %+v, %v", transitions, err)
	}

	resume := experience.LifecycleTransition{
		Experience: ref,
		FromState:  experience.LifecyclePaused,
		ToState:    experience.LifecycleActive,
		ReasonCode: "user_resumed",
		ActorKind:  experience.ActorUser,
		ActorID:    "local_user",
		OccurredAt: stale.OccurredAt.Add(time.Minute),
	}
	resume.TransitionID = resume.DeterministicID()
	if inserted, err := store.AppendExperienceTransition(ctx, resume); err != nil || !inserted {
		t.Fatalf("resume transition = %v, %v", inserted, err)
	}
	stored, err = store.GetExperience(ctx, ref)
	if err != nil || stored.CurrentLifecycle != experience.LifecycleActive ||
		stored.ActivatedAt == nil || !stored.ActivatedAt.Equal(resume.OccurredAt) {
		t.Fatalf("lifecycle after resume = %+v, %v", stored, err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = withMutationTx(ctx, tx, mutationExperienceRegistry, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE experiences
			SET content_hash = ?
			WHERE experience_id = ? AND version = ?`,
			storageSHA256("mutated"),
			ref.ExperienceID,
			ref.Version,
		)
		return err
	})
	_ = tx.Rollback()
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("authorized immutable update error = %v", err)
	}
}

func TestExperienceEvaluationUpdatesOnlyEvaluationState(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	candidate := storageExperienceCandidate("evaluation")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleActive)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}
	application := storageExperienceApplication(value)
	if _, err := store.InsertExperienceApplication(ctx, application); err != nil {
		t.Fatal(err)
	}

	evaluation := storageExperienceEvaluation(application, candidate.Evidence.Refs[0])
	updated, applied, err := store.ApplyExperienceEvaluation(ctx, evaluation)
	if err != nil || !applied {
		t.Fatalf("ApplyExperienceEvaluation() = %+v, %v, %v", updated, applied, err)
	}
	if updated.ApplicationID != application.ApplicationID ||
		updated.Experience != application.Experience ||
		updated.ProjectIdentity != application.ProjectIdentity ||
		updated.SessionKey != application.SessionKey ||
		updated.DeliveryKind != application.DeliveryKind ||
		updated.DeliveryState != application.DeliveryState ||
		!optionalTimeEqual(updated.DeliveredAt, application.DeliveredAt) ||
		updated.OpportunityState != evaluation.OpportunityState ||
		updated.ApplicabilityState != evaluation.ApplicabilityState ||
		updated.VerifierState != evaluation.VerifierState ||
		updated.TaskOutcomeState != evaluation.TaskOutcomeState {
		t.Fatalf("updated application changed identity/delivery fields: %+v", updated)
	}
	persisted, err := store.GetExperienceApplication(ctx, application.ApplicationID)
	if err != nil || !reflect.DeepEqual(persisted, updated) {
		t.Fatalf("persisted evaluated application = %+v, %v", persisted, err)
	}
	var evaluatedAt string
	if err := store.db.QueryRowContext(ctx, `
		SELECT evaluated_at FROM experience_applications
		WHERE application_id = ?`,
		application.ApplicationID,
	).Scan(&evaluatedAt); err != nil {
		t.Fatal(err)
	}
	if evaluatedAt != formatProjectionTime(evaluation.EvaluatedAt) {
		t.Fatalf("evaluated_at = %q, want %q", evaluatedAt, formatProjectionTime(evaluation.EvaluatedAt))
	}
	if replay, applied, err := store.ApplyExperienceEvaluation(ctx, evaluation); err != nil ||
		applied || !reflect.DeepEqual(replay, updated) {
		t.Fatalf("evaluation replay = %+v, %v, %v", replay, applied, err)
	}
	conflicting := evaluation
	conflicting.SourceEvidence = []experience.EvidenceRef{
		evaluation.SourceEvidence[1],
		evaluation.SourceEvidence[0],
	}
	conflicting.EvaluationID = conflicting.DeterministicID()
	if conflicting.EvaluationID != evaluation.EvaluationID {
		t.Fatal("evidence ordering unexpectedly changed deterministic evaluation ID")
	}
	if _, _, err := store.ApplyExperienceEvaluation(ctx, conflicting); !errors.Is(
		err,
		ErrExperienceEvaluationConflict,
	) {
		t.Fatalf("conflicting evaluation identity error = %v", err)
	}
	if inserted, err := store.InsertExperienceApplication(ctx, application); err != nil || inserted {
		t.Fatalf("application replay after evaluation = %v, %v", inserted, err)
	}

	stale := evaluation
	stale.EvaluatedAt = evaluation.EvaluatedAt.Add(-time.Minute)
	stale.VerifierState = experience.VerifierViolated
	stale.EvaluationID = stale.DeterministicID()
	if _, _, err := store.ApplyExperienceEvaluation(ctx, stale); !errors.Is(err, ErrExperienceEvaluationStale) {
		t.Fatalf("stale evaluation error = %v", err)
	}

	mismatched := evaluation
	mismatched.Experience = experience.ExperienceRef{
		ExperienceID: "exp_mismatched",
		Version:      1,
	}
	mismatched.EvaluatedAt = evaluation.EvaluatedAt.Add(time.Minute)
	mismatched.EvaluationID = mismatched.DeterministicID()
	if _, _, err := store.ApplyExperienceEvaluation(ctx, mismatched); err == nil ||
		!strings.Contains(err.Error(), "does not match application") {
		t.Fatalf("mismatched evaluation error = %v", err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = withMutationTx(ctx, tx, mutationExperienceApplication, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE experience_applications
			SET delivery_state = 'not_delivered'
			WHERE application_id = ?`,
			application.ApplicationID,
		)
		return err
	})
	_ = tx.Rollback()
	if err == nil || !strings.Contains(err.Error(), "delivery identity is immutable") {
		t.Fatalf("application delivery mutation error = %v", err)
	}
}

func TestExperienceFoundationRejectsIndexPayloadMismatch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index-mismatch.sqlite")
	provider := newMemoryKeyProvider()
	store, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	candidate := storageExperienceCandidate("index mismatch")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	oversized := storageExperienceCandidate("oversized payload")
	if _, err := store.InsertExperienceCandidate(ctx, oversized); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", mustSQLiteDSN(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `
		UPDATE experience_candidates
		SET project_identity = 'git@example.test:tampered/project.git'
		WHERE candidate_id = ?`,
		candidate.CandidateID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `
		UPDATE experience_candidates
		SET payload = zeroblob(?)
		WHERE candidate_id = ?`,
		maxExperiencePayloadBytes+sealedPayloadOverheadAllowance+1,
		oversized.CandidateID,
	); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.GetExperienceCandidate(ctx, candidate.CandidateID); err == nil ||
		!strings.Contains(err.Error(), "index does not match payload") {
		t.Fatalf("mismatched candidate index error = %v", err)
	}
	if _, err := store.GetExperienceCandidate(ctx, oversized.CandidateID); err == nil ||
		!strings.Contains(err.Error(), "payload exceeds safety limit") {
		t.Fatalf("oversized candidate payload error = %v", err)
	}
}

func TestExperienceFoundationRejectsMissingIntegrityReferences(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)

	candidate := storageExperienceCandidate("missing origin")
	orphan := storageExperience(candidate, experience.LifecycleActive)
	if _, err := store.InsertExperience(ctx, orphan); err == nil ||
		!strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("missing origin candidate error = %v", err)
	}

	missingGeneration := storageExperienceGeneration(2)
	missingGeneration.State = GenerationInactive
	previous := int64(99)
	missingGeneration.PreviousGeneration = &previous
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = withMutationTx(ctx, tx, mutationExperienceGeneration, func() error {
		return store.writeGenerationTx(ctx, tx, missingGeneration, true)
	})
	_ = tx.Rollback()
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("missing previous generation error = %v", err)
	}
}

func TestExperienceGenerationRollbackAndReceiptAmbiguity(t *testing.T) {
	ctx := context.Background()
	clockTime := time.Date(2026, 9, 10, 16, 0, 0, 0, time.UTC)
	store, err := OpenWithOptions(
		filepath.Join(t.TempDir(), "state.sqlite"),
		OpenOptions{
			KeyProvider: newMemoryKeyProvider(),
			Clock:       func() time.Time { return clockTime },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first := storageExperienceGeneration(1)
	if _, err := store.ActivateExperienceGeneration(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := storageExperienceGeneration(2)
	second.CompiledAt = first.CompiledAt.Add(time.Minute)
	second.ActivatedAt = second.CompiledAt
	activated, err := store.ActivateExperienceGeneration(ctx, second)
	if err != nil || activated.PreviousGeneration == nil || *activated.PreviousGeneration != 1 {
		t.Fatalf("second activation = %+v, %v", activated, err)
	}
	clockTime = clockTime.Add(time.Minute)
	rolledBack, err := store.RollbackExperienceGeneration(ctx, first.ProjectIdentity)
	if err != nil || rolledBack.Generation != 1 ||
		rolledBack.PreviousGeneration == nil || *rolledBack.PreviousGeneration != 2 {
		t.Fatalf("rollback = %+v, %v", rolledBack, err)
	}
	var rows, active int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*), SUM(CASE WHEN state = 'active' THEN 1 ELSE 0 END)
		FROM experience_generations WHERE project_identity = ?`,
		first.ProjectIdentity,
	).Scan(&rows, &active); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || active != 1 {
		t.Fatalf("generation rows/active = %d/%d, want 2/1", rows, active)
	}

	candidate := storageExperienceCandidate("receipt ambiguity")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleActive)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}
	missingExperience := storageMissionPackReceipt(value)
	missingExperience.ReceiptID = "mpr_missing_experience"
	missingExperience.ExperienceRefs[0].ExperienceID = "exp_missing"
	if _, err := store.RecordPendingMissionPackReceipt(ctx, missingExperience); err == nil ||
		!strings.Contains(err.Error(), "experience was not found") {
		t.Fatalf("missing receipt experience error = %v", err)
	}
	inactiveGeneration := storageMissionPackReceipt(value)
	inactiveGeneration.ReceiptID = "mpr_inactive_generation"
	inactiveGeneration.Generation = 2
	if _, err := store.RecordPendingMissionPackReceipt(ctx, inactiveGeneration); err == nil ||
		!strings.Contains(err.Error(), "generation is not active") {
		t.Fatalf("inactive receipt generation error = %v", err)
	}
	duplicateRefs := storageMissionPackReceipt(value)
	duplicateRefs.ReceiptID = "mpr_duplicate_refs"
	duplicateRefs.ExperienceRefs = append(
		duplicateRefs.ExperienceRefs,
		duplicateRefs.ExperienceRefs[0],
	)
	if _, err := store.RecordPendingMissionPackReceipt(ctx, duplicateRefs); err == nil ||
		!strings.Contains(err.Error(), "duplicate experience reference") {
		t.Fatalf("duplicate receipt experience error = %v", err)
	}
	pausedCandidate := storageExperienceCandidate("paused receipt experience")
	if _, err := store.InsertExperienceCandidate(ctx, pausedCandidate); err != nil {
		t.Fatal(err)
	}
	pausedExperience := storageExperience(pausedCandidate, experience.LifecyclePaused)
	if _, err := store.InsertExperience(ctx, pausedExperience); err != nil {
		t.Fatal(err)
	}
	inactiveExperience := storageMissionPackReceipt(pausedExperience)
	inactiveExperience.ReceiptID = "mpr_inactive_experience"
	if _, err := store.RecordPendingMissionPackReceipt(ctx, inactiveExperience); err == nil ||
		!strings.Contains(err.Error(), "experience is not active") {
		t.Fatalf("inactive receipt experience error = %v", err)
	}
	receipt := storageMissionPackReceipt(value)
	if _, err := store.RecordPendingMissionPackReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	pending, err := store.QueryPendingMissionPackReceipts(
		ctx,
		receipt.ProjectIdentity,
		receipt.Harness,
		receipt.AcceptedAt.Add(time.Minute),
		10,
	)
	if err != nil || len(pending) != 1 || pending[0].ReceiptID != receipt.ReceiptID {
		t.Fatalf("pending receipts = %+v, %v", pending, err)
	}
	resolved, err := store.ResolveMissionPackReceipt(
		ctx,
		receipt.ReceiptID,
		[]string{"ses_second", "ses_first", "ses_first"},
		receipt.AcceptedAt.Add(time.Minute),
	)
	if err != nil || resolved.BindingState != ReceiptAmbiguous || resolved.BoundSessionKey != "" {
		t.Fatalf("ambiguous receipt = %+v, %v", resolved, err)
	}
	persisted, err := store.GetMissionPackReceipt(ctx, receipt.ReceiptID)
	if err != nil || persisted.BindingState != ReceiptAmbiguous || persisted.BoundSessionKey != "" {
		t.Fatalf("persisted ambiguous receipt = %+v, %v", persisted, err)
	}
}

func storageExperienceCandidate(canary string) experience.Candidate {
	turn := int64(12)
	evidence := experience.EvidenceSet{
		Availability: experience.EvidenceAvailable,
		Refs: []experience.EvidenceRef{{
			Kind:       experience.EvidenceTranscriptTurn,
			SessionKey: "ses_storage",
			TurnIndex:  &turn,
			Excerpt:    canary,
		}},
	}
	evidence.EvidenceSetID = evidence.DeterministicID()
	confidence := 0.9
	value := experience.Candidate{
		SchemaVersion:    experience.CandidateSchemaVersion,
		Family:           experience.CandidateCorrection,
		ProjectIdentity:  storageProjectIdentity,
		ObservedBehavior: "The assistant edited a generated file directly: " + canary,
		UserFeedback:     "Do not edit generated files directly.",
		Evidence:         evidence,
		Proposal: experience.ExperienceProposal{
			Type: experience.ExperienceConstraint,
			Scope: experience.Scope{
				Kind:            experience.ScopeProject,
				ProjectIdentity: storageProjectIdentity,
				RepositoryPaths: []string{"generated/**"},
				Harnesses:       []experience.Harness{experience.HarnessClaude},
			},
			Applicability: experience.Applicability{
				SemanticDescription: "Tasks that modify generated files.",
				DeterministicConditions: []experience.DeterministicCondition{{
					Kind:   experience.ConditionPathPattern,
					Values: []string{"generated/**"},
				}},
				ExpiresAt: timePointer(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)),
			},
			Guidance: experience.Guidance{
				Instruction:          "Edit the source schema instead.",
				Rationale:            "Generated files are overwritten.",
				InterventionStrength: experience.InterventionAdvise,
			},
			Verifier: experience.Verifier{
				Kind:                 experience.VerifierPathPatternNotModified,
				CoverageRequirements: []experience.CoverageRequirement{experience.CoverageWorkspaceCaptured},
				PathPattern:          &experience.PathPatternVerifierSpec{Patterns: []string{"generated/**"}},
			},
			Confidence: &confidence,
		},
		Provenance: experience.Provenance{
			ExtractorVersion: "candidate.det.v1",
			Harness:          experience.HarnessClaude,
			Model:            "claude-test",
			PromptVersion:    "candidate-prompt.v1",
			InputHash:        storageSHA256("candidate input"),
			GeneratedAt:      time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		},
		Authority:      experience.AuthorityNone,
		LifecycleState: experience.LifecycleCandidate,
		CreatedAt:      time.Date(2026, 9, 10, 12, 0, 1, 0, time.UTC),
	}
	value.CandidateID = value.DeterministicID()
	return value
}

func storageExperience(
	candidate experience.Candidate,
	state experience.LifecycleState,
) experience.Experience {
	value := experience.Experience{
		SchemaVersion:     experience.ExperienceSchemaVersion,
		OriginCandidateID: candidate.CandidateID,
		Version:           1,
		Type:              candidate.Proposal.Type,
		Scope:             candidate.Proposal.Scope,
		Applicability:     candidate.Proposal.Applicability,
		Guidance:          candidate.Proposal.Guidance,
		Verifier:          candidate.Proposal.Verifier,
		Evidence:          candidate.Evidence,
		Provenance: experience.Provenance{
			ExtractorVersion:  "approval.v1",
			InputHash:         storageSHA256("approval input"),
			SourceCandidateID: candidate.CandidateID,
			GeneratedAt:       time.Date(2026, 9, 10, 12, 5, 0, 0, time.UTC),
		},
		Governance: experience.Governance{
			LifecycleState: state,
			Authority:      experience.AuthorityUserApproved,
		},
		CreatedAt: time.Date(2026, 9, 10, 12, 5, 0, 0, time.UTC),
	}
	value.ExperienceID = experience.DeriveExperienceID(
		value.Scope.ProjectIdentity,
		value.OriginCandidateID,
	)
	value.ContentHash = value.CanonicalContentHash()
	value.Governance.Approval = &experience.ApprovalProvenance{
		ApprovedBy:          "local_user",
		ApprovedAt:          value.CreatedAt,
		Mode:                experience.ApprovalAsProposed,
		CandidateID:         candidate.CandidateID,
		ProposedContentHash: value.ContentHash,
		ApprovedContentHash: value.ContentHash,
	}
	return value
}

func storageTrajectoryEdge() trajectory.Edge {
	first := int64(1)
	second := int64(2)
	value := trajectory.Edge{
		SchemaVersion:     trajectory.EdgeSchemaVersion,
		ProjectIdentity:   storageProjectIdentity,
		SessionKey:        "ses_storage",
		From:              trajectory.NodeRef{Kind: trajectory.NodeTranscriptTurn, SessionKey: "ses_storage", TurnIndex: &first},
		Relation:          trajectory.RelationRespondsTo,
		To:                trajectory.NodeRef{Kind: trajectory.NodeTranscriptTurn, SessionKey: "ses_storage", TurnIndex: &second},
		EvidenceClass:     trajectory.EvidenceObserved,
		Confidence:        trajectory.ConfidenceHigh,
		DerivationVersion: "trajectory.det.v1",
		SourceRefs: []trajectory.NodeRef{
			{Kind: trajectory.NodeTranscriptTurn, SessionKey: "ses_storage", TurnIndex: &first},
			{Kind: trajectory.NodeTranscriptTurn, SessionKey: "ses_storage", TurnIndex: &second},
		},
		OccurredAt: time.Date(2026, 9, 10, 12, 10, 0, 0, time.UTC),
	}
	value.EdgeID = value.DeterministicID()
	return value
}

func storageTrajectoryOutcome() trajectory.Outcome {
	turn := int64(8)
	value := trajectory.Outcome{
		SchemaVersion:   trajectory.OutcomeSchemaVersion,
		ProjectIdentity: storageProjectIdentity,
		SessionKey:      "ses_storage",
		OccurredAt:      time.Date(2026, 9, 10, 12, 15, 0, 0, time.UTC),
		Kind:            trajectory.OutcomeVerificationPass,
		Result:          trajectory.ResultSucceeded,
		EvidenceClass:   trajectory.EvidenceObserved,
		Confidence:      trajectory.ConfidenceHigh,
		SourceRefs: []trajectory.NodeRef{{
			Kind:       trajectory.NodeTranscriptTurn,
			SessionKey: "ses_storage",
			TurnIndex:  &turn,
		}},
		DerivationVersion: "outcome.det.v1",
	}
	value.OutcomeID = value.DeterministicID()
	return value
}

func storageExperienceApplication(value experience.Experience) experience.Application {
	ref := experience.ExperienceRef{ExperienceID: value.ExperienceID, Version: value.Version}
	application := experience.Application{
		SchemaVersion:      experience.ApplicationSchemaVersion,
		Experience:         ref,
		ProjectIdentity:    value.Scope.ProjectIdentity,
		DeliveryKind:       experience.DeliveryMissionPack,
		DeliveryState:      experience.DeliveryPending,
		OpportunityState:   experience.OpportunityUnknown,
		ApplicabilityState: experience.ApplicabilityUnknown,
		VerifierState:      experience.VerifierNotEvaluated,
		TaskOutcomeState:   experience.TaskOutcomeNotObserved,
	}
	application.ApplicationID = application.DeterministicID()
	return application
}

func storageExperienceEvaluation(
	application experience.Application,
	source experience.EvidenceRef,
) experience.Evaluation {
	secondSource := source
	secondTurn := *source.TurnIndex + 1
	secondSource.TurnIndex = &secondTurn
	secondSource.Excerpt = "Verification completed successfully."
	value := experience.Evaluation{
		SchemaVersion:      experience.EvaluationSchemaVersion,
		ApplicationID:      application.ApplicationID,
		Experience:         application.Experience,
		EvaluatedAt:        time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
		OpportunityState:   experience.OpportunityObserved,
		ApplicabilityState: experience.ApplicabilityApplicable,
		VerifierState:      experience.VerifierSatisfied,
		TaskOutcomeState:   experience.TaskOutcomeSucceeded,
		Coverage:           []experience.CoverageRequirement{experience.CoverageWorkspaceCaptured},
		SourceEvidence:     []experience.EvidenceRef{source, secondSource},
		DerivationVersion:  "evaluation.det.v1",
	}
	value.EvaluationID = value.DeterministicID()
	return value
}

func storageExperienceGeneration(generation int64) ExperienceGeneration {
	at := time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC)
	refs := []experience.ExperienceRef{
		{
			ExperienceID: experience.DeriveExperienceID(
				storageProjectIdentity,
				storageExperienceCandidate(
					"PRIVATE_EXPERIENCE_CANARY_8f7d",
				).CandidateID,
			),
			Version: 1,
		},
		{
			ExperienceID: experience.DeriveExperienceID(
				storageProjectIdentity,
				storageExperienceCandidate(
					"receipt ambiguity",
				).CandidateID,
			),
			Version: 1,
		},
	}
	sort.Slice(refs, func(first, second int) bool {
		if refs[first].ExperienceID != refs[second].ExperienceID {
			return refs[first].ExperienceID < refs[second].ExperienceID
		}
		return refs[first].Version < refs[second].Version
	})
	return ExperienceGeneration{
		ProjectIdentity: storageProjectIdentity,
		Generation:      generation,
		CompiledHash:    storageSHA256("generation " + string(rune('0'+generation))),
		ExperienceRefs:  refs,
		State:           GenerationActive,
		CompiledAt:      at,
		ActivatedAt:     at,
	}
}

func storageMissionPackReceipt(value experience.Experience) MissionPackReceipt {
	accepted := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	return MissionPackReceipt{
		ReceiptID:       "mpr_storage",
		PackID:          "mpk_storage",
		ProjectIdentity: value.Scope.ProjectIdentity,
		Harness:         experience.HarnessCodex,
		ExperienceRefs: []experience.ExperienceRef{{
			ExperienceID: value.ExperienceID,
			Version:      value.Version,
		}},
		Generation:   1,
		AcceptedAt:   accepted,
		ExpiresAt:    accepted.Add(10 * time.Minute),
		TaskHintHash: storageSHA256("task hint"),
		BindingState: ReceiptPending,
	}
}

func storageSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func timePointer(value time.Time) *time.Time {
	return &value
}

const storageProjectIdentity = "git@example.test:doplexlabs/belay-engine.git"
