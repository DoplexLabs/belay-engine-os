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

type missionPackReceiptSessionQuery struct {
	projectIdentity string
	harness         experience.Harness
	acceptedAt      time.Time
	expiresAt       time.Time
	limit           int
}

type missionPackReceiptResolution struct {
	receiptID   string
	sessionKeys []string
	observedAt  time.Time
}

type missionPackReceiptReconciliationStore struct {
	receipts       []local.MissionPackReceipt
	queryErr       error
	sessionKeys    map[string][]string
	sessionErrs    map[string]error
	resolveErrs    map[string]error
	receiptLimit   int
	sessionQueries []missionPackReceiptSessionQuery
	resolutions    []missionPackReceiptResolution
}

func (store *missionPackReceiptReconciliationStore) QueryUnresolvedMissionPackReceipts(
	_ context.Context,
	limit int,
) ([]local.MissionPackReceipt, error) {
	store.receiptLimit = limit
	return append([]local.MissionPackReceipt(nil), store.receipts...),
		store.queryErr
}

func (store *missionPackReceiptReconciliationStore) QueryCompatibleTranscriptSessionKeys(
	_ context.Context,
	projectIdentity string,
	harness experience.Harness,
	acceptedAt time.Time,
	expiresAt time.Time,
	limit int,
) ([]string, error) {
	store.sessionQueries = append(
		store.sessionQueries,
		missionPackReceiptSessionQuery{
			projectIdentity: projectIdentity,
			harness:         harness,
			acceptedAt:      acceptedAt,
			expiresAt:       expiresAt,
			limit:           limit,
		},
	)
	receiptID := ""
	for _, receipt := range store.receipts {
		if receipt.ProjectIdentity == projectIdentity &&
			receipt.Harness == harness &&
			receipt.AcceptedAt.Equal(acceptedAt) &&
			receipt.ExpiresAt.Equal(expiresAt) {
			receiptID = receipt.ReceiptID
			break
		}
	}
	return append([]string(nil), store.sessionKeys[receiptID]...),
		store.sessionErrs[receiptID]
}

func (store *missionPackReceiptReconciliationStore) ResolveMissionPackReceipt(
	_ context.Context,
	receiptID string,
	sessionKeys []string,
	observedAt time.Time,
) (local.MissionPackReceipt, error) {
	store.resolutions = append(
		store.resolutions,
		missionPackReceiptResolution{
			receiptID:   receiptID,
			sessionKeys: append([]string(nil), sessionKeys...),
			observedAt:  observedAt,
		},
	)
	return local.MissionPackReceipt{}, store.resolveErrs[receiptID]
}

func TestMissionPackReceiptReconciliationZeroBeforeExpiryStaysPending(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	receipt := reconciliationReceipt("mpr_zero", now.Add(-time.Minute), now.Add(time.Minute))
	store := newMissionPackReceiptReconciliationStore(receipt)
	coordinator := newMissionPackReceiptReconciliationCoordinator(t, store)

	if err := coordinator.Reconcile(context.Background(), now, 7); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if store.receiptLimit != 7 {
		t.Fatalf("receipt query limit = %d, want 7", store.receiptLimit)
	}
	assertReceiptSessionQuery(t, store, receipt)
	assertReceiptResolution(t, store, receipt.ReceiptID, nil, now)
}

func TestMissionPackReceiptReconciliationExpiresWithoutSessionQuery(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	receipt := reconciliationReceipt("mpr_expired", now.Add(-time.Minute), now)
	store := newMissionPackReceiptReconciliationStore(receipt)
	coordinator := newMissionPackReceiptReconciliationCoordinator(t, store)

	if err := coordinator.Reconcile(context.Background(), now, 1); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(store.sessionQueries) != 0 {
		t.Fatalf("session queries = %+v, want none", store.sessionQueries)
	}
	assertReceiptResolution(t, store, receipt.ReceiptID, nil, now)
}

func TestMissionPackReceiptReconciliationBindsOneCompatibleSession(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	receipt := reconciliationReceipt("mpr_one", now.Add(-time.Minute), now.Add(time.Minute))
	store := newMissionPackReceiptReconciliationStore(receipt)
	store.sessionKeys[receipt.ReceiptID] = []string{"ses_one"}
	coordinator := newMissionPackReceiptReconciliationCoordinator(t, store)

	if err := coordinator.Reconcile(context.Background(), now, 1); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	assertReceiptSessionQuery(t, store, receipt)
	assertReceiptResolution(
		t,
		store,
		receipt.ReceiptID,
		[]string{"ses_one"},
		now,
	)
}

func TestMissionPackReceiptReconciliationPassesTwoKeysAsAmbiguous(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	receipt := reconciliationReceipt(
		"mpr_ambiguous",
		now.Add(-time.Minute),
		now.Add(time.Minute),
	)
	store := newMissionPackReceiptReconciliationStore(receipt)
	store.sessionKeys[receipt.ReceiptID] = []string{"ses_a", "ses_b"}
	coordinator := newMissionPackReceiptReconciliationCoordinator(t, store)

	if err := coordinator.Reconcile(context.Background(), now, 1); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	assertReceiptSessionQuery(t, store, receipt)
	assertReceiptResolution(
		t,
		store,
		receipt.ReceiptID,
		[]string{"ses_a", "ses_b"},
		now,
	)
}

func TestMissionPackReceiptReconciliationContinuesAndJoinsErrors(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	queryFailure := reconciliationReceipt(
		"mpr_query_failure",
		now.Add(-3*time.Minute),
		now.Add(time.Minute),
	)
	resolveFailure := reconciliationReceipt(
		"mpr_resolve_failure",
		now.Add(-2*time.Minute),
		now.Add(time.Minute),
	)
	success := reconciliationReceipt(
		"mpr_success",
		now.Add(-time.Minute),
		now.Add(time.Minute),
	)
	queryErr := errors.New("session query failed")
	resolveErr := errors.New("receipt resolution failed")
	store := newMissionPackReceiptReconciliationStore(
		success,
		queryFailure,
		resolveFailure,
	)
	store.sessionErrs[queryFailure.ReceiptID] = queryErr
	store.sessionKeys[resolveFailure.ReceiptID] = []string{"ses_resolve"}
	store.resolveErrs[resolveFailure.ReceiptID] = resolveErr
	store.sessionKeys[success.ReceiptID] = []string{"ses_success"}
	coordinator := newMissionPackReceiptReconciliationCoordinator(t, store)

	err := coordinator.Reconcile(context.Background(), now, 3)
	if !errors.Is(err, queryErr) || !errors.Is(err, resolveErr) {
		t.Fatalf("Reconcile() error = %v, want joined query and resolve errors", err)
	}
	if !strings.Contains(err.Error(), queryFailure.ReceiptID) ||
		!strings.Contains(err.Error(), resolveFailure.ReceiptID) {
		t.Fatalf("Reconcile() error = %v, want receipt identities", err)
	}
	gotIDs := make([]string, 0, len(store.resolutions))
	for _, resolution := range store.resolutions {
		gotIDs = append(gotIDs, resolution.receiptID)
	}
	wantIDs := []string{resolveFailure.ReceiptID, success.ReceiptID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("resolved receipt IDs = %v, want %v", gotIDs, wantIDs)
	}
}

func TestMissionPackReceiptReconciliationRejectsNilDependencies(
	t *testing.T,
) {
	if _, err := NewMissionPackReceiptReconciliationCoordinator(nil); err == nil {
		t.Fatal("NewMissionPackReceiptReconciliationCoordinator(nil) error = nil")
	}
	store := newMissionPackReceiptReconciliationStore()
	coordinator := newMissionPackReceiptReconciliationCoordinator(t, store)
	if err := coordinator.Reconcile(nil, time.Now(), 1); err == nil {
		t.Fatal("Reconcile(nil context) error = nil")
	}
}

func reconciliationReceipt(
	receiptID string,
	acceptedAt time.Time,
	expiresAt time.Time,
) local.MissionPackReceipt {
	return local.MissionPackReceipt{
		ReceiptID:       receiptID,
		ProjectIdentity: "git@example.test:doplexlabs/belay.git",
		Harness:         experience.HarnessCodex,
		AcceptedAt:      acceptedAt,
		ExpiresAt:       expiresAt,
		BindingState:    local.ReceiptPending,
	}
}

func newMissionPackReceiptReconciliationStore(
	receipts ...local.MissionPackReceipt,
) *missionPackReceiptReconciliationStore {
	return &missionPackReceiptReconciliationStore{
		receipts:    receipts,
		sessionKeys: make(map[string][]string),
		sessionErrs: make(map[string]error),
		resolveErrs: make(map[string]error),
	}
}

func newMissionPackReceiptReconciliationCoordinator(
	t *testing.T,
	store MissionPackReceiptReconciliationRepository,
) *MissionPackReceiptReconciliationCoordinator {
	t.Helper()
	coordinator, err := NewMissionPackReceiptReconciliationCoordinator(store)
	if err != nil {
		t.Fatalf("NewMissionPackReceiptReconciliationCoordinator() error = %v", err)
	}
	return coordinator
}

func assertReceiptSessionQuery(
	t *testing.T,
	store *missionPackReceiptReconciliationStore,
	receipt local.MissionPackReceipt,
) {
	t.Helper()
	want := []missionPackReceiptSessionQuery{{
		projectIdentity: receipt.ProjectIdentity,
		harness:         receipt.Harness,
		acceptedAt:      receipt.AcceptedAt,
		expiresAt:       receipt.ExpiresAt,
		limit:           missionPackReceiptSessionProbeLimit,
	}}
	if !reflect.DeepEqual(store.sessionQueries, want) {
		t.Fatalf("session queries = %+v, want %+v", store.sessionQueries, want)
	}
}

func assertReceiptResolution(
	t *testing.T,
	store *missionPackReceiptReconciliationStore,
	receiptID string,
	sessionKeys []string,
	observedAt time.Time,
) {
	t.Helper()
	want := []missionPackReceiptResolution{{
		receiptID:   receiptID,
		sessionKeys: sessionKeys,
		observedAt:  observedAt,
	}}
	if !reflect.DeepEqual(store.resolutions, want) {
		t.Fatalf("resolutions = %+v, want %+v", store.resolutions, want)
	}
}
