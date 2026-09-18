package local

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestMissionPackReceiptApplicationProgressExactReadAndCorruptionProbe(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	value := insertStatusRepositoryExperience(t, store, "status progress")
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	compiledAt := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)
	generation := ExperienceGeneration{
		ProjectIdentity: value.Scope.ProjectIdentity,
		Generation:      1,
		CompiledHash:    storageSHA256("status progress generation"),
		ExperienceRefs:  []experience.ExperienceRef{ref},
		State:           GenerationActive,
		CompiledAt:      compiledAt,
		ActivatedAt:     compiledAt,
	}
	if _, err := store.ActivateExperienceGeneration(ctx, generation); err != nil {
		t.Fatal(err)
	}
	receipt := storageMissionPackReceipt(value)
	if _, err := store.RecordPendingMissionPackReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}

	rows, err := store.GetMissionPackReceiptApplicationProgress(
		ctx,
		receipt.ReceiptID,
	)
	if err != nil || len(rows) != 1 ||
		rows[0].ReceiptID != receipt.ReceiptID ||
		rows[0].Experience != ref ||
		!rows[0].ProgressPresent ||
		rows[0].ApplicationID != "" {
		t.Fatalf("unlinked progress = %+v, %v", rows, err)
	}

	bound, err := store.ResolveMissionPackReceipt(
		ctx,
		receipt.ReceiptID,
		[]string{"ses_status_progress"},
		receipt.AcceptedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	deliveredAt := receipt.AcceptedAt
	application := experience.Application{
		SchemaVersion:      experience.ApplicationSchemaVersion,
		Experience:         ref,
		ProjectIdentity:    receipt.ProjectIdentity,
		SessionKey:         bound.BoundSessionKey,
		DeliveryKind:       experience.DeliveryMissionPack,
		DeliveryState:      experience.DeliveryDelivered,
		DeliveredAt:        &deliveredAt,
		OpportunityState:   experience.OpportunityUnknown,
		ApplicabilityState: experience.ApplicabilityUnknown,
		VerifierState:      experience.VerifierNotEvaluated,
		TaskOutcomeState:   experience.TaskOutcomeNotObserved,
	}
	application.ApplicationID = application.DeterministicID()
	if inserted, err := store.InsertExperienceApplication(
		ctx,
		application,
	); err != nil || !inserted {
		t.Fatalf("InsertExperienceApplication() = %v, %v", inserted, err)
	}
	if err := store.LinkMissionPackReceiptApplication(
		ctx,
		receipt.ReceiptID,
		ref,
		application.ApplicationID,
	); err != nil {
		t.Fatal(err)
	}
	rows, err = store.GetMissionPackReceiptApplicationProgress(
		ctx,
		receipt.ReceiptID,
	)
	if err != nil || len(rows) != 1 ||
		!rows[0].ProgressPresent ||
		rows[0].ApplicationID != application.ApplicationID {
		t.Fatalf("linked progress = %+v, %v", rows, err)
	}

	foreign := insertStatusRepositoryExperience(
		t,
		store,
		"status foreign progress",
	)
	statusRepositoryMutation(t, store, func(tx *sql.Tx) error {
		now := formatProjectionTime(time.Now().UTC())
		_, err := tx.ExecContext(ctx, `
			INSERT INTO mission_pack_receipt_applications (
				receipt_id, experience_id, experience_version,
				application_id, linked_at, created_at, updated_at
			) VALUES (?, ?, ?, NULL, NULL, ?, ?)`,
			receipt.ReceiptID,
			foreign.ExperienceID,
			foreign.Version,
			now,
			now,
		)
		return err
	})
	if _, err := store.GetMissionPackReceiptApplicationProgress(
		ctx,
		receipt.ReceiptID,
	); !errors.Is(err, ErrMissionPackReceiptApplicationCorrupt) {
		t.Fatalf("corrupt progress error = %v", err)
	}
}

func insertStatusRepositoryExperience(
	t *testing.T,
	store *Store,
	seed string,
) experience.Experience {
	t.Helper()
	candidate := storageExperienceCandidate(seed)
	if inserted, err := store.InsertExperienceCandidate(
		context.Background(),
		candidate,
	); err != nil || !inserted {
		t.Fatalf("InsertExperienceCandidate() = %v, %v", inserted, err)
	}
	value := storageExperience(candidate, experience.LifecycleActive)
	if inserted, err := store.InsertExperience(
		context.Background(),
		value,
	); err != nil || !inserted {
		t.Fatalf("InsertExperience() = %v, %v", inserted, err)
	}
	return value
}

func statusRepositoryMutation(
	t *testing.T,
	store *Store,
	mutate func(*sql.Tx) error,
) {
	t.Helper()
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = withMutationTx(
		context.Background(),
		tx,
		mutationMissionPackReceipt,
		func() error { return mutate(tx) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
