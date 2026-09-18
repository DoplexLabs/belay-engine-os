package localhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestRealStoreFixMonitoringRestartRetentionAndHistoryOnly(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC)
	storeNow := base
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	provider := &httpIntegrationKeyProvider{}
	openStore := func() *local.Store {
		t.Helper()
		store, err := local.OpenWithOptions(
			path,
			local.OpenOptions{
				KeyProvider: provider,
				Clock:       func() time.Time { return storeNow },
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	newHandler := func(store *local.Store) http.Handler {
		t.Helper()
		server, err := New(
			readmodel.New(
				store,
				readmodel.WithFixMonitoringRepository(store),
				readmodel.WithClock(func() time.Time { return storeNow }),
			),
			"launch-secret",
		)
		if err != nil {
			t.Fatal(err)
		}
		return server.Handler()
	}

	store := openStore()
	initialEvent := httpIntegrationEvent(
		"01890f2e-6d4b-7c8a-9b0c-123456789c01",
		"session-monitoring-restart",
		1,
		base.Add(-2*time.Hour),
	)
	initialAppend, err := store.AppendEventResolved(ctx, initialEvent)
	if err != nil {
		t.Fatal(err)
	}
	initialOccurrence := httpIntegrationOccurrence(
		t,
		store,
		initialEvent,
		"explicit_command_failure",
		"command_failure",
		false,
	)
	initialOccurrence.ScopeQuality = model.ScopeResolved
	_, err = store.ReplaceIssueProjection(
		ctx,
		local.ProjectionReplacement{
			SessionKey:        initialEvent.Session.Key,
			ClaimedGeneration: initialAppend.ReadGeneration,
			Status:            model.AnalysisCurrent,
			ScopeQuality:      model.ScopeResolved,
			Occurrences:       []model.IssueOccurrence{initialOccurrence},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	issuePage, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			IssueID:       initialOccurrence.IssueID,
			AttentionKind: model.AttentionKindAll,
			Experimental:  model.ExperimentalInclude,
		},
		Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	annotation, err := store.RecordFixAnnotation(
		ctx,
		local.FixAnnotationInput{
			Claims: model.FixActionClaims{
				Version:             model.FixActionTokenVersion,
				CursorEpoch:         issuePage.CursorEpoch,
				IssueID:             initialOccurrence.IssueID,
				Snapshot:            issuePage.Snapshot,
				RetentionGeneration: issuePage.RetentionGeneration,
				IssuedAt:            issuePage.IssuedAt,
				ExpiresAt:           issuePage.IssuedAt.Add(15 * time.Minute),
			},
			ChangeKind:     model.FixChangeCode,
			RecordedVia:    model.FixRecordedViaLocalUI,
			IdempotencyKey: "72345678-1234-4234-9234-123456789abc",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	storeNow = base.Add(2 * time.Minute)
	laterEvent := httpIntegrationEvent(
		"01890f2e-6d4b-7c8a-9b0c-123456789c02",
		initialEvent.Session.Key,
		2,
		base.Add(time.Minute),
	)
	laterAppend, err := store.AppendEventResolved(ctx, laterEvent)
	if err != nil {
		t.Fatal(err)
	}
	laterOccurrence := httpIntegrationOccurrence(
		t,
		store,
		laterEvent,
		"explicit_command_failure",
		"command_failure",
		false,
	)
	laterOccurrence.ScopeQuality = model.ScopeResolved
	if _, err := store.ReplaceIssueProjection(
		ctx,
		local.ProjectionReplacement{
			SessionKey:        laterEvent.Session.Key,
			ClaimedGeneration: laterAppend.ReadGeneration,
			Status:            model.AnalysisCurrent,
			ScopeQuality:      model.ScopeResolved,
			Occurrences:       []model.IssueOccurrence{laterOccurrence},
		},
	); err != nil {
		t.Fatal(err)
	}
	drainRecurrenceJobsForHTTPTest(t, store, storeNow)
	if ready, err := store.CompleteFixMonitoringCatchup(ctx); err != nil || !ready {
		t.Fatalf("CompleteFixMonitoringCatchup() = %v, %v", ready, err)
	}

	handler := newHandler(store)
	var list readmodel.FixMonitoringList
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/fix-monitoring",
		&list,
	); status != http.StatusOK ||
		len(list.Data) != 1 ||
		list.Data[0].FixRecurrenceState != model.FixRecurrenceMatchingEvidence ||
		list.Data[0].FixRecurrenceCount != 1 {
		t.Fatalf("monitoring list status=%d response=%+v", status, list)
	}
	var detail readmodel.FixMonitoringDetail
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/issues/"+initialOccurrence.IssueID+
			"/fix-monitoring?view_cursor="+list.MonitoringViewCursor,
		&detail,
	); status != http.StatusOK ||
		!detail.CurrentIssueAvailable ||
		len(detail.Data) != 1 ||
		detail.Data[0].AnnotationID != annotation.Annotation.AnnotationID {
		t.Fatalf("monitoring detail status=%d response=%+v", status, detail)
	}
	var observations readmodel.FixRecurrenceObservationList
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/issues/"+initialOccurrence.IssueID+
			"/fixes/"+annotation.Annotation.AnnotationID+
			"/recurrences?observation_view_cursor="+
			detail.Data[0].ObservationViewCursor,
		&observations,
	); status != http.StatusOK ||
		len(observations.Data) != 1 ||
		observations.Data[0].EvidenceCurrentlyRetained !=
			model.FixRecurrenceEvidenceAvailable ||
		len(observations.Data[0].RetainedEventIDs) != 1 {
		t.Fatalf("observations status=%d response=%+v", status, observations)
	}
	preRetentionView := detail.MonitoringViewCursor

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = nil
	handler = nil
	storeNow = base.Add(3 * time.Minute)
	reopened := openStore()
	handler = newHandler(reopened)
	var restarted readmodel.FixMonitoringDetail
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/issues/"+initialOccurrence.IssueID+"/fix-monitoring",
		&restarted,
	); status != http.StatusOK ||
		len(restarted.Data) != 1 ||
		restarted.Data[0].FixRecurrenceState !=
			model.FixRecurrenceMatchingEvidence {
		t.Fatalf("restart monitoring status=%d response=%+v", status, restarted)
	}

	if _, err := reopened.RetractFixAnnotation(
		ctx,
		local.FixRetractionInput{
			IssueID:        initialOccurrence.IssueID,
			AnnotationID:   annotation.Annotation.AnnotationID,
			Reason:         model.FixRetractionSuperseded,
			RecordedVia:    model.FixRecordedViaLocalUI,
			IdempotencyKey: "82345678-1234-4234-9234-123456789abc",
		},
	); err != nil {
		t.Fatal(err)
	}
	var defaultAfterRetraction readmodel.FixMonitoringList
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/fix-monitoring",
		&defaultAfterRetraction,
	); status != http.StatusOK || len(defaultAfterRetraction.Data) != 0 {
		t.Fatalf("default retracted list status=%d response=%+v",
			status, defaultAfterRetraction)
	}
	var retractedList readmodel.FixMonitoringList
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/fix-monitoring?include_retracted=true",
		&retractedList,
	); status != http.StatusOK ||
		len(retractedList.Data) != 1 ||
		retractedList.Data[0].FixRecurrenceState != model.FixRecurrenceRetracted ||
		retractedList.Data[0].HistoricalMatchingEvidenceCount != 1 {
		t.Fatalf("retracted list status=%d response=%+v", status, retractedList)
	}

	prune, err := reopened.Prune(
		ctx,
		local.RetentionPolicy{MaxAge: time.Hour},
		base.Add(6*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if prune.PrunedEventCount == 0 {
		t.Fatal("retention test did not prune recurrence evidence")
	}
	var expired problem
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/issues/"+initialOccurrence.IssueID+
			"/fix-monitoring?view_cursor="+preRetentionView,
		&expired,
	); status != http.StatusGone ||
		expired.Type != "belay.local/cursor-expired" {
		t.Fatalf("retention cursor status=%d problem=%+v", status, expired)
	}

	var historyList readmodel.FixMonitoringList
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/fix-monitoring?include_retracted=true",
		&historyList,
	); status != http.StatusOK || len(historyList.Data) != 1 {
		t.Fatalf("history list status=%d response=%+v", status, historyList)
	}
	var history readmodel.FixMonitoringDetail
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/issues/"+initialOccurrence.IssueID+
			"/fix-monitoring?view_cursor="+historyList.MonitoringViewCursor,
		&history,
	); status != http.StatusOK ||
		history.CurrentIssueAvailable ||
		history.CurrentIssue != nil ||
		len(history.Data) != 1 ||
		history.Data[0].HistoricalMatchingEvidenceCount != 1 {
		t.Fatalf("history-only detail status=%d response=%+v", status, history)
	}
	var prunedEvidence readmodel.FixRecurrenceObservationList
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/issues/"+initialOccurrence.IssueID+
			"/fixes/"+annotation.Annotation.AnnotationID+
			"/recurrences?observation_view_cursor="+
			history.Data[0].ObservationViewCursor,
		&prunedEvidence,
	); status != http.StatusOK ||
		len(prunedEvidence.Data) != 1 ||
		prunedEvidence.Data[0].EvidenceCurrentlyRetained !=
			model.FixRecurrenceEvidencePruned ||
		len(prunedEvidence.Data[0].RetainedEventIDs) != 0 {
		t.Fatalf("pruned evidence status=%d response=%+v",
			status, prunedEvidence)
	}

	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	reopened = nil
	handler = nil
	storeNow = base.Add(4 * time.Minute)
	finalStore := openStore()
	defer finalStore.Close()
	finalHandler := newHandler(finalStore)
	var durable readmodel.FixMonitoringDetail
	if status := monitoringIntegrationGET(
		t,
		finalHandler,
		"/v1/issues/"+initialOccurrence.IssueID+"/fix-monitoring",
		&durable,
	); status != http.StatusOK ||
		len(durable.Data) != 1 ||
		durable.Data[0].FixRecurrenceState != model.FixRecurrenceRetracted ||
		durable.Data[0].HistoricalMatchingEvidenceCount != 1 {
		t.Fatalf("durable history status=%d response=%+v", status, durable)
	}
}

func drainRecurrenceJobsForHTTPTest(
	t *testing.T,
	store *local.Store,
	now time.Time,
) {
	t.Helper()
	ctx := context.Background()
	for {
		claim, err := store.ClaimRecurrenceJob(ctx, now, time.Minute)
		if errors.Is(err, local.ErrNoRecurrenceJob) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		for {
			result, err := store.ProcessRecurrenceBatch(ctx, claim, 100)
			if err != nil {
				t.Fatal(err)
			}
			claim.AttemptAfterSequence = result.NextSequence
			if result.Completed {
				break
			}
		}
	}
}

func monitoringIntegrationGET(
	t *testing.T,
	handler http.Handler,
	path string,
	value any,
) int {
	t.Helper()
	request := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1"+path,
		true,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if value != nil {
		if err := json.NewDecoder(response.Body).Decode(value); err != nil {
			t.Fatalf("decode %s: %v body=%s", path, err, response.Body.String())
		}
	}
	return response.Code
}

func TestRealStoreMonitoringReadinessErrorsAreScoped(t *testing.T) {
	storeNow := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	store, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		local.OpenOptions{
			KeyProvider: &httpIntegrationKeyProvider{},
			Clock:       func() time.Time { return storeNow },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := func() http.Handler {
		server, err := New(
			readmodel.New(
				store,
				readmodel.WithFixMonitoringRepository(store),
			),
			"launch-secret",
		)
		if err != nil {
			t.Fatal(err)
		}
		return server.Handler()
	}()
	var catchingUp problem
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/fix-monitoring",
		&catchingUp,
	); status != http.StatusServiceUnavailable ||
		catchingUp.Type != "belay.local/monitoring-catchup-in-progress" {
		t.Fatalf("catching-up status=%d problem=%+v", status, catchingUp)
	}
	if err := store.FailFixMonitoringCatchup(
		context.Background(),
		"integration_failure",
		storeNow.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	var failed problem
	if status := monitoringIntegrationGET(
		t,
		handler,
		"/v1/fix-monitoring",
		&failed,
	); status != http.StatusServiceUnavailable ||
		failed.Type != "belay.local/monitoring-catchup-failed" ||
		strings.Contains(
			mustMarshalJSON(t, failed),
			"integration_failure",
		) {
		t.Fatalf("failed status=%d problem=%+v", status, failed)
	}
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
