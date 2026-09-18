package local

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestMissionPackPreviewPersistenceEncryptionReplayAndConflict(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	preview := storageMissionPackPreview(
		"mpk_preview_encryption",
		1,
		[]experience.ExperienceRef{{
			ExperienceID: "exp_preview_encryption",
			Version:      1,
		}},
	)

	inserted, err := store.InsertMissionPackPreview(ctx, preview)
	if err != nil || !inserted {
		t.Fatalf("InsertMissionPackPreview() = %v, %v", inserted, err)
	}
	replayed, err := store.InsertMissionPackPreview(ctx, preview)
	if err != nil || replayed {
		t.Fatalf("replayed InsertMissionPackPreview() = %v, %v", replayed, err)
	}
	got, err := store.GetMissionPackPreview(ctx, preview.PackID)
	if err != nil || !reflect.DeepEqual(got, preview) {
		t.Fatalf("GetMissionPackPreview() = %+v, %v", got, err)
	}
	refreshed := preview
	refreshed.GeneratedAt = preview.GeneratedAt.Add(time.Minute)
	refreshed.ExpiresAt = preview.ExpiresAt.Add(time.Minute)
	if inserted, err := store.InsertMissionPackPreview(
		ctx,
		refreshed,
	); err != nil || inserted {
		t.Fatalf("refreshed InsertMissionPackPreview() = %v, %v", inserted, err)
	}
	got, err = store.GetMissionPackPreview(ctx, preview.PackID)
	if err != nil || !reflect.DeepEqual(got, refreshed) {
		t.Fatalf("refreshed GetMissionPackPreview() = %+v, %v", got, err)
	}
	if inserted, err := store.InsertMissionPackPreview(
		ctx,
		preview,
	); err != nil || inserted {
		t.Fatalf("stale replay InsertMissionPackPreview() = %v, %v", inserted, err)
	}
	got, err = store.GetMissionPackPreview(ctx, preview.PackID)
	if err != nil || !reflect.DeepEqual(got, refreshed) {
		t.Fatalf("stale replay changed preview = %+v, %v", got, err)
	}

	var payload []byte
	if err := store.db.QueryRowContext(ctx, `
		SELECT payload
		FROM mission_pack_previews
		WHERE pack_id = ?`,
		preview.PackID,
	).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(preview.ProjectIdentity)) ||
		bytes.Contains(payload, []byte(preview.ExperienceRefs[0].ExperienceID)) ||
		bytes.Contains(payload, []byte(preview.TaskHintHash)) {
		t.Fatal("mission pack preview payload was not encrypted")
	}

	conflict := preview
	conflict.ProjectIdentity = "git@example.test:doplexlabs/conflict.git"
	if _, err := store.InsertMissionPackPreview(
		ctx,
		conflict,
	); !errors.Is(err, ErrMissionPackPreviewConflict) {
		t.Fatalf("preview conflict error = %v", err)
	}
	if _, err := store.GetMissionPackPreview(
		ctx,
		"mpk_preview_missing",
	); !errors.Is(err, ErrMissionPackPreviewNotFound) {
		t.Fatalf("missing preview error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO mission_pack_previews (
			pack_id, expires_at, payload, payload_encoding, created_at
		) VALUES (
			'mpk_preview_guard', '2026-09-10T23:00:00Z',
			X'00', 'aes256gcm.v1', '2026-09-10T22:00:00Z'
		)`); err == nil {
		t.Fatal("direct mission pack preview insertion bypassed mutation guard")
	}
}

func TestAcceptMissionPackPreviewCreatesAtomicReceiptAndReplays(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	compiledAt := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	value := generationExperience(
		t,
		"mission pack acceptance member",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt.Add(8*time.Hour),
	)
	insertGenerationExperience(t, store, value)
	compiled, err := store.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	preview := storageMissionPackPreview(
		"mpk_preview_accept",
		compiled.Generation.Generation,
		[]experience.ExperienceRef{ref},
	)
	if err := store.RegisterMissionPackPreview(
		ctx,
		preview.PackID,
		preview.ProjectIdentity,
		preview.Harness,
		preview.Generation,
		preview.ExperienceRefs,
		preview.TaskHintHash,
		preview.GeneratedAt,
		preview.ExpiresAt,
	); err != nil {
		t.Fatal(err)
	}

	acceptedAt := preview.GeneratedAt.Add(time.Minute)
	receipt, err := store.AcceptMissionPackPreview(
		ctx,
		preview.PackID,
		acceptedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ReceiptID != missionPackReceiptID(preview.PackID) ||
		receipt.PackID != preview.PackID ||
		receipt.ProjectIdentity != preview.ProjectIdentity ||
		receipt.Harness != preview.Harness ||
		receipt.Generation != preview.Generation ||
		!reflect.DeepEqual(receipt.ExperienceRefs, preview.ExperienceRefs) ||
		!receipt.AcceptedAt.Equal(acceptedAt) ||
		!receipt.ExpiresAt.Equal(acceptedAt.Add(5*time.Minute)) ||
		receipt.TaskHintHash != preview.TaskHintHash ||
		receipt.BindingState != ReceiptPending {
		t.Fatalf("accepted receipt = %+v", receipt)
	}
	persisted, err := store.GetMissionPackReceipt(ctx, receipt.ReceiptID)
	if err != nil || !reflect.DeepEqual(persisted, receipt) {
		t.Fatalf("persisted receipt = %+v, %v", persisted, err)
	}

	replayed, err := store.AcceptMissionPackPreview(
		ctx,
		preview.PackID,
		preview.ExpiresAt.Add(time.Hour),
	)
	if err != nil || !reflect.DeepEqual(replayed, receipt) {
		t.Fatalf("replayed receipt = %+v, %v", replayed, err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM mission_pack_receipts
		WHERE pack_id = ?`,
		preview.PackID,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipt count/error = %d/%v", count, err)
	}
}

func TestAcceptMissionPackPreviewRejectsInvalidOrStalePreviewAtomically(
	t *testing.T,
) {
	ctx := context.Background()

	t.Run("missing", func(t *testing.T) {
		store := openStorageTestStore(t)
		if _, err := store.AcceptMissionPackPreview(
			ctx,
			"mpk_preview_missing_acceptance",
			time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC),
		); !errors.Is(err, ErrMissionPackPreviewNotFound) {
			t.Fatalf("missing acceptance error = %v", err)
		}
	})

	t.Run("expired and empty", func(t *testing.T) {
		store := openStorageTestStore(t)
		expired := storageMissionPackPreview(
			"mpk_preview_expired",
			1,
			[]experience.ExperienceRef{{
				ExperienceID: "exp_preview_expired",
				Version:      1,
			}},
		)
		if _, err := store.InsertMissionPackPreview(ctx, expired); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcceptMissionPackPreview(
			ctx,
			expired.PackID,
			expired.ExpiresAt,
		); !errors.Is(err, ErrMissionPackPreviewExpired) {
			t.Fatalf("expired acceptance error = %v", err)
		}

		empty := storageMissionPackPreview("mpk_preview_empty", 1, nil)
		if _, err := store.InsertMissionPackPreview(ctx, empty); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcceptMissionPackPreview(
			ctx,
			empty.PackID,
			empty.GeneratedAt.Add(time.Minute),
		); err == nil {
			t.Fatal("empty preview was accepted")
		}
		assertNoMissionPackReceipt(t, store)
	})

	t.Run("inactive generation", func(t *testing.T) {
		store := openStorageTestStore(t)
		compiledAt := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
		value := generationExperience(
			t,
			"inactive mission pack generation",
			storageProjectIdentity,
			experience.LifecycleActive,
			compiledAt.Add(8*time.Hour),
		)
		insertGenerationExperience(t, store, value)
		first, err := store.CompileActiveExperienceGeneration(
			ctx,
			storageProjectIdentity,
			compiledAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		second := first.Generation
		second.Generation++
		second.CompiledHash = storageSHA256("replacement generation")
		second.CompiledAt = compiledAt.Add(time.Minute)
		second.ActivatedAt = compiledAt.Add(time.Minute)
		if _, err := store.ActivateExperienceGeneration(ctx, second); err != nil {
			t.Fatal(err)
		}
		preview := storageMissionPackPreview(
			"mpk_preview_inactive_generation",
			first.Generation.Generation,
			[]experience.ExperienceRef{{
				ExperienceID: value.ExperienceID,
				Version:      value.Version,
			}},
		)
		if _, err := store.InsertMissionPackPreview(ctx, preview); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcceptMissionPackPreview(
			ctx,
			preview.PackID,
			preview.GeneratedAt.Add(time.Minute),
		); err == nil {
			t.Fatal("inactive generation preview was accepted")
		}
		assertNoMissionPackReceipt(t, store)
	})

	t.Run("nonmember experience", func(t *testing.T) {
		store := openStorageTestStore(t)
		compiledAt := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
		member := generationExperience(
			t,
			"mission pack generation member",
			storageProjectIdentity,
			experience.LifecycleActive,
			compiledAt.Add(8*time.Hour),
		)
		insertGenerationExperience(t, store, member)
		compiled, err := store.CompileActiveExperienceGeneration(
			ctx,
			storageProjectIdentity,
			compiledAt,
		)
		if err != nil {
			t.Fatal(err)
		}
		nonmember := generationExperience(
			t,
			"mission pack generation nonmember",
			storageProjectIdentity,
			experience.LifecycleActive,
			compiledAt.Add(8*time.Hour),
		)
		insertGenerationExperience(t, store, nonmember)
		preview := storageMissionPackPreview(
			"mpk_preview_nonmember",
			compiled.Generation.Generation,
			[]experience.ExperienceRef{{
				ExperienceID: nonmember.ExperienceID,
				Version:      nonmember.Version,
			}},
		)
		if _, err := store.InsertMissionPackPreview(ctx, preview); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcceptMissionPackPreview(
			ctx,
			preview.PackID,
			preview.GeneratedAt.Add(time.Minute),
		); err == nil {
			t.Fatal("nonmember experience preview was accepted")
		}
		assertNoMissionPackReceipt(t, store)
	})
}

func TestMissionPackReceiptAndCompatibleTranscriptQueries(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	compiledAt := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	value := generationExperience(
		t,
		"mission pack receipt query member",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt.Add(8*time.Hour),
	)
	insertGenerationExperience(t, store, value)
	secondValue := generationExperience(
		t,
		"mission pack receipt query second member",
		storageProjectIdentity,
		experience.LifecycleActive,
		compiledAt.Add(9*time.Hour),
	)
	insertGenerationExperience(t, store, secondValue)
	compiled, err := store.CompileActiveExperienceGeneration(
		ctx,
		storageProjectIdentity,
		compiledAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	secondRef := experience.ExperienceRef{
		ExperienceID: secondValue.ExperienceID,
		Version:      secondValue.Version,
	}
	first := storageMissionPackReceipt(value)
	first.ReceiptID = "mpr_query_first"
	first.PackID = "mpk_query_first"
	first.Generation = compiled.Generation.Generation
	first.ExperienceRefs = []experience.ExperienceRef{ref, secondRef}
	first.AcceptedAt = time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	first.ExpiresAt = first.AcceptedAt.Add(5 * time.Minute)
	second := first
	second.ReceiptID = "mpr_query_second"
	second.PackID = "mpk_query_second"
	second.AcceptedAt = first.AcceptedAt.Add(time.Minute)
	second.ExpiresAt = second.AcceptedAt.Add(5 * time.Minute)
	cancelled := first
	cancelled.ReceiptID = "mpr_query_cancelled"
	cancelled.PackID = "mpk_query_cancelled"
	cancelled.AcceptedAt = first.AcceptedAt.Add(2 * time.Minute)
	cancelled.ExpiresAt = cancelled.AcceptedAt.Add(5 * time.Minute)
	for _, receipt := range []MissionPackReceipt{second, cancelled, first} {
		if inserted, err := store.RecordPendingMissionPackReceipt(
			ctx,
			receipt,
		); err != nil || !inserted {
			t.Fatalf("RecordPendingMissionPackReceipt() = %v, %v", inserted, err)
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	identityErr := withMutationTx(
		ctx,
		tx,
		mutationMissionPackReceipt,
		func() error {
			_, err := tx.ExecContext(ctx, `
				UPDATE mission_pack_receipt_applications
				SET created_at = ?
				WHERE receipt_id = ? AND experience_id = ?
					AND experience_version = ?`,
				formatProjectionTime(first.AcceptedAt.Add(-time.Hour)),
				first.ReceiptID,
				ref.ExperienceID,
				ref.Version,
			)
			return err
		},
	)
	_ = tx.Rollback()
	if identityErr == nil ||
		!strings.Contains(identityErr.Error(), "identity is immutable") {
		t.Fatalf(
			"receipt application identity update error = %v",
			identityErr,
		)
	}
	if _, err := store.CancelMissionPackReceipt(
		ctx,
		cancelled.ReceiptID,
	); err != nil {
		t.Fatal(err)
	}
	unresolved, err := store.QueryUnresolvedMissionPackReceipts(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unresolved) != 2 ||
		unresolved[0].ReceiptID != first.ReceiptID ||
		unresolved[1].ReceiptID != second.ReceiptID {
		t.Fatalf("unresolved receipts = %+v", unresolved)
	}
	bound, err := store.ResolveMissionPackReceipt(
		ctx,
		first.ReceiptID,
		[]string{"ses_materialized"},
		first.AcceptedAt.Add(time.Minute),
	)
	if err != nil || bound.BindingState != ReceiptBound {
		t.Fatalf("bound receipt = %+v, %v", bound, err)
	}
	secondBound, err := store.ResolveMissionPackReceipt(
		ctx,
		second.ReceiptID,
		[]string{"ses_materialized_second"},
		second.AcceptedAt.Add(time.Minute),
	)
	if err != nil || secondBound.BindingState != ReceiptBound {
		t.Fatalf("second bound receipt = %+v, %v", secondBound, err)
	}
	needingApplications, err :=
		store.QueryBoundMissionPackReceiptsNeedingApplications(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(needingApplications) != 1 ||
		needingApplications[0].ReceiptID != first.ReceiptID {
		t.Fatalf(
			"receipts needing applications = %+v, want %q",
			needingApplications,
			first.ReceiptID,
		)
	}
	deliveredAt := first.AcceptedAt
	application := experience.Application{
		SchemaVersion:      experience.ApplicationSchemaVersion,
		Experience:         ref,
		ProjectIdentity:    first.ProjectIdentity,
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
		first.ReceiptID,
		ref,
		application.ApplicationID,
	); err != nil {
		t.Fatal(err)
	}
	needingApplications, err =
		store.QueryBoundMissionPackReceiptsNeedingApplications(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(needingApplications) != 1 ||
		needingApplications[0].ReceiptID != first.ReceiptID {
		t.Fatalf(
			"partially materialized receipts = %+v, want %q",
			needingApplications,
			first.ReceiptID,
		)
	}
	secondApplication := application
	secondApplication.Experience = secondRef
	secondApplication.ApplicationID = secondApplication.DeterministicID()
	if inserted, err := store.InsertExperienceApplication(
		ctx,
		secondApplication,
	); err != nil || !inserted {
		t.Fatalf(
			"InsertExperienceApplication(second) = %v, %v",
			inserted,
			err,
		)
	}
	if err := store.LinkMissionPackReceiptApplication(
		ctx,
		first.ReceiptID,
		secondRef,
		secondApplication.ApplicationID,
	); err != nil {
		t.Fatal(err)
	}
	needingApplications, err =
		store.QueryBoundMissionPackReceiptsNeedingApplications(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(needingApplications) != 1 ||
		needingApplications[0].ReceiptID != second.ReceiptID {
		t.Fatalf(
			"receipts after first completion = %+v, want %q",
			needingApplications,
			second.ReceiptID,
		)
	}

	windowStart := first.AcceptedAt
	windowEnd := first.ExpiresAt
	appendMissionPackTranscriptTurn(
		t, store, "ses_compatible_first", "codex",
		storageProjectIdentity, windowStart,
	)
	appendMissionPackTranscriptTurn(
		t, store, "ses_compatible_second", "codex",
		storageProjectIdentity, windowStart.Add(time.Minute),
	)
	appendMissionPackTranscriptTurn(
		t, store, "ses_wrong_harness", "claude",
		storageProjectIdentity, windowStart.Add(2*time.Minute),
	)
	appendMissionPackTranscriptTurn(
		t, store, "ses_wrong_project", "codex",
		"git@example.test:doplexlabs/other.git",
		windowStart.Add(3*time.Minute),
	)
	appendMissionPackTranscriptTurn(
		t, store, "ses_at_exclusive_end", "codex",
		storageProjectIdentity, windowEnd,
	)
	keys, err := store.QueryCompatibleTranscriptSessionKeys(
		ctx,
		storageProjectIdentity,
		experience.HarnessCodex,
		windowStart,
		windowEnd,
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		keys,
		[]string{"ses_compatible_first", "ses_compatible_second"},
	) {
		t.Fatalf("compatible transcript session keys = %v", keys)
	}
	if _, err := store.QueryUnresolvedMissionPackReceipts(
		ctx,
		0,
	); err == nil {
		t.Fatal("zero unresolved receipt query limit was accepted")
	}
	if _, err := store.QueryBoundMissionPackReceiptsNeedingApplications(
		ctx,
		0,
	); err == nil {
		t.Fatal("zero materialization receipt query limit was accepted")
	}
	if _, err := store.QueryCompatibleTranscriptSessionKeys(
		ctx,
		storageProjectIdentity,
		experience.HarnessCodex,
		windowStart,
		windowEnd,
		0,
	); err == nil {
		t.Fatal("zero compatible transcript query limit was accepted")
	}
}

func storageMissionPackPreview(
	packID string,
	generation int64,
	refs []experience.ExperienceRef,
) MissionPackPreview {
	generatedAt := time.Date(2026, 9, 10, 21, 0, 0, 0, time.UTC)
	return MissionPackPreview{
		PackID:          packID,
		ProjectIdentity: storageProjectIdentity,
		Harness:         experience.HarnessCodex,
		Generation:      generation,
		ExperienceRefs:  append([]experience.ExperienceRef(nil), refs...),
		GeneratedAt:     generatedAt,
		ExpiresAt:       generatedAt.Add(10 * time.Minute),
		TaskHintHash:    storageSHA256("mission pack task hint"),
	}
}

func assertNoMissionPackReceipt(t *testing.T, store *Store) {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`
		SELECT COUNT(*)
		FROM mission_pack_receipts`,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("mission pack receipt count/error = %d/%v", count, err)
	}
}

func appendMissionPackTranscriptTurn(
	t *testing.T,
	store *Store,
	sessionKey string,
	agent string,
	projectIdentity string,
	occurredAt time.Time,
) {
	t.Helper()
	session := transcriptTestSession(sessionKey, transcript.CoverageComplete)
	session.Agent = agent
	session.ProjectIdentity = projectIdentity
	session.GitRemoteURL = projectIdentity
	turn := transcriptTestTurn(
		"turn-"+sessionKey,
		"source-"+sessionKey,
		sessionKey,
		0,
		occurredAt,
		transcript.RoleUser,
		transcript.Payload{
			Text:            "mission pack transcript activity",
			JSONLByteOffset: 1,
		},
	)
	if _, err := store.AppendTranscriptBatch(
		context.Background(),
		session,
		[]transcript.Turn{turn},
	); err != nil {
		t.Fatal(err)
	}
}
