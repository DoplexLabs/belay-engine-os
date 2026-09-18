package localapp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type missionPackApplicationMaterializationStore struct {
	receipts   []local.MissionPackReceipt
	queryErr   error
	insertErrs map[string]error
	apps       map[string]experience.Application
	progress   map[string]map[experience.ExperienceRef]string
	queryLimit int
}

func (store *missionPackApplicationMaterializationStore) QueryBoundMissionPackReceiptsNeedingApplications(
	_ context.Context,
	limit int,
) ([]local.MissionPackReceipt, error) {
	store.queryLimit = limit
	if store.queryErr != nil {
		return nil, store.queryErr
	}
	result := make([]local.MissionPackReceipt, 0, len(store.receipts))
	for _, receipt := range store.receipts {
		links, initialized := store.progress[receipt.ReceiptID]
		incomplete := !initialized
		if initialized {
			for _, ref := range receipt.ExperienceRefs {
				if links[ref] == "" {
					incomplete = true
					break
				}
			}
		}
		if incomplete {
			result = append(result, receipt)
		}
	}
	return result, nil
}

func (store *missionPackApplicationMaterializationStore) EnsureMissionPackReceiptApplicationProgress(
	_ context.Context,
	receipt local.MissionPackReceipt,
) error {
	if _, found := store.progress[receipt.ReceiptID]; !found {
		store.progress[receipt.ReceiptID] =
			make(map[experience.ExperienceRef]string, len(receipt.ExperienceRefs))
	}
	for _, ref := range receipt.ExperienceRefs {
		if _, found := store.progress[receipt.ReceiptID][ref]; !found {
			store.progress[receipt.ReceiptID][ref] = ""
		}
	}
	return nil
}

func (store *missionPackApplicationMaterializationStore) InsertExperienceApplication(
	_ context.Context,
	application experience.Application,
) (bool, error) {
	if err := store.insertErrs[application.Experience.ExperienceID]; err != nil {
		return false, err
	}
	if existing, found := store.apps[application.ApplicationID]; found {
		if !reflect.DeepEqual(existing, application) {
			return false, local.ErrExperienceApplicationConflict
		}
		return false, nil
	}
	store.apps[application.ApplicationID] = application
	return true, nil
}

func (store *missionPackApplicationMaterializationStore) LinkMissionPackReceiptApplication(
	_ context.Context,
	receiptID string,
	ref experience.ExperienceRef,
	applicationID string,
) error {
	links, found := store.progress[receiptID]
	if !found {
		return errors.New("application progress is not initialized")
	}
	application, found := store.apps[applicationID]
	if !found ||
		application.Experience != ref {
		return local.ErrMissionPackReceiptApplicationConflict
	}
	if existing := links[ref]; existing != "" && existing != applicationID {
		return local.ErrMissionPackReceiptApplicationConflict
	}
	links[ref] = applicationID
	return nil
}

func TestMissionPackApplicationMaterializationCreatesOneApplicationPerRefAndReplays(
	t *testing.T,
) {
	acceptedAt := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	receipt := materializationReceipt(
		"mpr_materialize",
		acceptedAt,
		"exp_first",
		"exp_second",
	)
	store := newMissionPackApplicationMaterializationStore(receipt)
	coordinator := newMissionPackApplicationMaterializationCoordinator(t, store)

	report, err := coordinator.Materialize(context.Background(), 7)
	if err != nil {
		t.Fatalf("Materialize() error = %v", err)
	}
	if report != (MissionPackApplicationMaterializationReport{
		ReceiptsAttempted:    1,
		ApplicationsInserted: 2,
	}) {
		t.Fatalf("Materialize() report = %+v", report)
	}
	if store.queryLimit != 7 || len(store.apps) != 2 {
		t.Fatalf(
			"query limit/applications = %d/%d, want 7/2",
			store.queryLimit,
			len(store.apps),
		)
	}
	for _, ref := range receipt.ExperienceRefs {
		want := deliveredMissionPackApplication(receipt, ref)
		got, found := store.apps[want.ApplicationID]
		if !found || !reflect.DeepEqual(got, want) {
			t.Fatalf("application for %+v = %+v, want %+v", ref, got, want)
		}
		if got.DeliveredAt == nil || !got.DeliveredAt.Equal(acceptedAt) ||
			got.SessionKey != receipt.BoundSessionKey ||
			got.DeliveryState != experience.DeliveryDelivered ||
			got.OpportunityState != experience.OpportunityUnknown ||
			got.ApplicabilityState != experience.ApplicabilityUnknown ||
			got.VerifierState != experience.VerifierNotEvaluated ||
			got.TaskOutcomeState != experience.TaskOutcomeNotObserved {
			t.Fatalf("application fields = %+v", got)
		}
	}

	replay, err := coordinator.Materialize(context.Background(), 7)
	if err != nil {
		t.Fatalf("Materialize() replay error = %v", err)
	}
	if replay != (MissionPackApplicationMaterializationReport{}) {
		t.Fatalf("Materialize() replay report = %+v", replay)
	}
	if len(store.apps) != 2 {
		t.Fatalf("applications after replay = %d, want 2", len(store.apps))
	}
}

func TestMissionPackApplicationMaterializationRetriesOnlyIncompleteReceipt(
	t *testing.T,
) {
	acceptedAt := time.Date(2026, 9, 10, 20, 30, 0, 0, time.UTC)
	receipt := materializationReceipt(
		"mpr_partial_retry",
		acceptedAt,
		"exp_inserted_first",
		"exp_fails_once",
	)
	insertErr := errors.New("temporary application persistence failure")
	store := newMissionPackApplicationMaterializationStore(receipt)
	store.insertErrs["exp_fails_once"] = insertErr
	coordinator := newMissionPackApplicationMaterializationCoordinator(t, store)

	first, err := coordinator.Materialize(context.Background(), 1)
	if !errors.Is(err, insertErr) {
		t.Fatalf("Materialize() first error = %v, want %v", err, insertErr)
	}
	if first != (MissionPackApplicationMaterializationReport{
		ReceiptsAttempted:    1,
		ApplicationsInserted: 1,
	}) {
		t.Fatalf("Materialize() first report = %+v", first)
	}
	if len(store.apps) != 1 {
		t.Fatalf("applications after first pass = %d, want 1", len(store.apps))
	}

	delete(store.insertErrs, "exp_fails_once")
	second, err := coordinator.Materialize(context.Background(), 1)
	if err != nil {
		t.Fatalf("Materialize() second error = %v", err)
	}
	if second != (MissionPackApplicationMaterializationReport{
		ReceiptsAttempted:    1,
		ApplicationsInserted: 1,
		ApplicationsReplayed: 1,
	}) {
		t.Fatalf("Materialize() second report = %+v", second)
	}
	if len(store.apps) != 2 {
		t.Fatalf("applications after second pass = %d, want 2", len(store.apps))
	}

	complete, err := coordinator.Materialize(context.Background(), 1)
	if err != nil {
		t.Fatalf("Materialize() completion query error = %v", err)
	}
	if complete != (MissionPackApplicationMaterializationReport{}) {
		t.Fatalf("Materialize() completed report = %+v", complete)
	}
}

func TestMissionPackApplicationMaterializationSkipsInvalidReceiptsAndContinues(
	t *testing.T,
) {
	acceptedAt := time.Date(2026, 9, 10, 21, 0, 0, 0, time.UTC)
	pending := materializationReceipt("mpr_pending", acceptedAt, "exp_pending")
	pending.BindingState = local.ReceiptPending
	pending.BoundSessionKey = ""
	ambiguous := materializationReceipt("mpr_ambiguous", acceptedAt, "exp_ambiguous")
	ambiguous.BindingState = local.ReceiptAmbiguous
	ambiguous.BoundSessionKey = ""
	expired := materializationReceipt("mpr_expired", acceptedAt, "exp_expired")
	expired.BindingState = local.ReceiptExpired
	expired.BoundSessionKey = ""
	malformed := materializationReceipt("mpr_malformed", acceptedAt, "exp_malformed")
	malformed.ProjectIdentity = ""
	partialFailure := materializationReceipt(
		"mpr_partial_failure",
		acceptedAt.Add(time.Minute),
		"exp_fails",
		"exp_survives",
	)
	success := materializationReceipt(
		"mpr_later_success",
		acceptedAt.Add(2*time.Minute),
		"exp_later",
	)
	insertErr := errors.New("application persistence failed")
	store := newMissionPackApplicationMaterializationStore(
		pending,
		ambiguous,
		expired,
		malformed,
		partialFailure,
		success,
	)
	store.insertErrs["exp_fails"] = insertErr
	coordinator := newMissionPackApplicationMaterializationCoordinator(t, store)

	report, err := coordinator.Materialize(context.Background(), 6)
	if err == nil || !errors.Is(err, insertErr) {
		t.Fatalf("Materialize() error = %v, want joined insert error", err)
	}
	for _, receiptID := range []string{
		pending.ReceiptID,
		ambiguous.ReceiptID,
		expired.ReceiptID,
		malformed.ReceiptID,
		partialFailure.ReceiptID,
	} {
		if !strings.Contains(err.Error(), receiptID) {
			t.Fatalf("Materialize() error = %v, want receipt %q", err, receiptID)
		}
	}
	if report != (MissionPackApplicationMaterializationReport{
		ReceiptsAttempted:    6,
		ApplicationsInserted: 2,
	}) {
		t.Fatalf("Materialize() report = %+v", report)
	}
	if len(store.apps) != 2 {
		t.Fatalf("applications = %d, want 2", len(store.apps))
	}
	for _, forbidden := range []string{
		"exp_pending",
		"exp_ambiguous",
		"exp_expired",
		"exp_malformed",
		"exp_fails",
	} {
		for _, application := range store.apps {
			if application.Experience.ExperienceID == forbidden {
				t.Fatalf("unexpected application for %q", forbidden)
			}
		}
	}
}

func TestMissionPackApplicationMaterializationFailsClosedOnIdentityConflict(
	t *testing.T,
) {
	acceptedAt := time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC)
	receipt := materializationReceipt(
		"mpr_conflict",
		acceptedAt,
		"exp_conflict",
	)
	store := newMissionPackApplicationMaterializationStore(receipt)
	expected := deliveredMissionPackApplication(
		receipt,
		receipt.ExperienceRefs[0],
	)
	conflicting := expected
	conflicting.SourceEvidence = []experience.EvidenceRef{{
		Kind:       experience.EvidenceTranscriptTurn,
		SessionKey: receipt.BoundSessionKey,
	}}
	store.apps[expected.ApplicationID] = conflicting
	coordinator := newMissionPackApplicationMaterializationCoordinator(t, store)

	report, err := coordinator.Materialize(context.Background(), 1)
	if !errors.Is(err, local.ErrExperienceApplicationConflict) {
		t.Fatalf("Materialize() error = %v, want identity conflict", err)
	}
	if report != (MissionPackApplicationMaterializationReport{
		ReceiptsAttempted: 1,
	}) {
		t.Fatalf("Materialize() report = %+v", report)
	}
	if got := store.apps[expected.ApplicationID]; !reflect.DeepEqual(
		got,
		conflicting,
	) {
		t.Fatalf("conflicting application was overwritten: %+v", got)
	}
}

func materializationReceipt(
	receiptID string,
	acceptedAt time.Time,
	experienceIDs ...string,
) local.MissionPackReceipt {
	refs := make([]experience.ExperienceRef, 0, len(experienceIDs))
	for _, experienceID := range experienceIDs {
		refs = append(refs, experience.ExperienceRef{
			ExperienceID: experienceID,
			Version:      1,
		})
	}
	return local.MissionPackReceipt{
		ReceiptID:       receiptID,
		PackID:          "mpk_" + receiptID,
		ProjectIdentity: "git@example.test:doplexlabs/belay.git",
		Harness:         experience.HarnessCodex,
		ExperienceRefs:  refs,
		Generation:      1,
		AcceptedAt:      acceptedAt,
		ExpiresAt:       acceptedAt.Add(10 * time.Minute),
		BindingState:    local.ReceiptBound,
		BoundSessionKey: "ses_materialized",
	}
}

func newMissionPackApplicationMaterializationStore(
	receipts ...local.MissionPackReceipt,
) *missionPackApplicationMaterializationStore {
	return &missionPackApplicationMaterializationStore{
		receipts:   receipts,
		insertErrs: make(map[string]error),
		apps:       make(map[string]experience.Application),
		progress:   make(map[string]map[experience.ExperienceRef]string),
	}
}

func newMissionPackApplicationMaterializationCoordinator(
	t *testing.T,
	store MissionPackApplicationMaterializationRepository,
) *MissionPackApplicationMaterializationCoordinator {
	t.Helper()
	coordinator, err := NewMissionPackApplicationMaterializationCoordinator(store)
	if err != nil {
		t.Fatalf(
			"NewMissionPackApplicationMaterializationCoordinator() error = %v",
			err,
		)
	}
	return coordinator
}
