package analysis

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/detection"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestRecurrenceWorkerStartupCoordinatesAnalysisAndReadiness(t *testing.T) {
	store := openAnalysisStore(t)
	appendAnalysisEvent(t, store, "recurrence-startup", 1)
	worker := NewRecurrenceWorker(store)
	report, err := worker.Startup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 0 || report.Stale != 0 {
		t.Fatalf("startup report = %+v", report)
	}
	readiness, err := store.FixMonitoringReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if readiness != model.FixMonitoringReadinessReady {
		t.Fatalf("readiness = %q, want ready", readiness)
	}
}

func TestRecurrenceWorkerCancellationDoesNotPersistFailure(t *testing.T) {
	store := openAnalysisStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewRecurrenceWorker(store).Startup(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Startup(canceled) error = %v", err)
	}
	readiness, readErr := store.FixMonitoringReadiness(context.Background())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if readiness == model.FixMonitoringReadinessFailed {
		t.Fatal("cancellation was persisted as catch-up failure")
	}
}

func TestRecurrenceWorkerRecoversFailedReadinessOnRestart(t *testing.T) {
	store := openAnalysisStore(t)
	if err := store.FailFixMonitoringCatchup(
		context.Background(),
		"injected_failure",
		time.Now().UTC().Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	worker := NewRecurrenceWorker(store)
	if _, err := worker.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertFixMonitoringReadiness(
		t,
		store,
		model.FixMonitoringReadinessFailed,
	)
	if _, err := worker.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	readiness, err := store.FixMonitoringReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if readiness != model.FixMonitoringReadinessReady {
		t.Fatalf("recovered readiness = %q, want ready", readiness)
	}
}

func TestRecurrenceWorkerPeriodicRecoveryRetriesDueAnalysisBeforeReadiness(
	t *testing.T,
) {
	store := openAnalysisStore(t)
	ctx := context.Background()
	sessionID := "recurrence-analysis-retry"
	appendAnalysisEvent(t, store, sessionID, 1)
	scope, err := store.DeriveProjectScope(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSessionScope(ctx, sessionID, scope); err != nil {
		t.Fatal(err)
	}
	var failures atomic.Int32
	failures.Store(1)
	catalog, err := detection.NewCatalog(
		flakyRecurrenceWorkerDetector{remainingFailures: &failures},
	)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	worker := NewRecurrenceWorker(store)
	worker.now = func() time.Time { return startedAt }
	worker.reconciler = NewReconcilerWithCatalog(store, catalog)
	worker.reconciler.now = func() time.Time { return startedAt }

	if _, err := worker.Startup(ctx); !errors.Is(
		err,
		ErrFixMonitoringCatchupPending,
	) {
		t.Fatalf("Startup() error = %v, want pending catch-up", err)
	}
	assertFixMonitoringReadiness(
		t,
		store,
		model.FixMonitoringReadinessCatchingUp,
	)
	if _, err := store.QueryFixMonitoring(
		ctx,
		model.FixMonitoringQuery{},
	); !errors.Is(err, model.ErrFixMonitoringCatchingUp) {
		t.Fatalf("monitoring read before analysis retry error = %v", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := worker.Recover(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Recover(canceled) error = %v", err)
	}
	assertFixMonitoringReadiness(
		t,
		store,
		model.FixMonitoringReadinessCatchingUp,
	)

	retryAt := startedAt.Add(2 * time.Second)
	restarted := NewRecurrenceWorker(store)
	restarted.now = func() time.Time { return retryAt }
	restarted.reconciler = NewReconcilerWithCatalog(store, catalog)
	restarted.reconciler.now = func() time.Time { return retryAt }
	report, err := restarted.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 0 {
		t.Fatalf("periodic recovery report = %+v", report)
	}
	assertFixMonitoringReadiness(
		t,
		store,
		model.FixMonitoringReadinessReady,
	)
	if failures.Load() != 0 {
		t.Fatalf("remaining injected analysis failures = %d", failures.Load())
	}
	if _, err := store.QueryFixMonitoring(
		ctx,
		model.FixMonitoringQuery{},
	); err != nil {
		t.Fatalf("monitoring read remained blocked after analysis retry: %v", err)
	}
}

func TestRecurrenceWorkerPeriodicDrainCompletesReadinessAfterTransientFailure(
	t *testing.T,
) {
	fixture := newRecurrenceWorkerFixture(t)
	ctx := context.Background()
	worker := NewRecurrenceWorker(fixture.store)
	worker.now = func() time.Time { return fixture.claimAt }
	processCalls := 0
	worker.processBatch = func(
		ctx context.Context,
		claim model.FixRecurrenceJobClaim,
		limit int,
	) (model.FixRecurrenceBatchResult, error) {
		processCalls++
		if processCalls == 1 {
			return model.FixRecurrenceBatchResult{}, errors.New(
				"injected transient recurrence failure",
			)
		}
		return fixture.store.ProcessRecurrenceBatch(ctx, claim, limit)
	}

	first, err := worker.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Failed != 1 || first.Completed != 0 {
		t.Fatalf("first drain report = %+v", first)
	}
	assertFixMonitoringReadiness(
		t,
		fixture.store,
		model.FixMonitoringReadinessCatchingUp,
	)
	if _, err := fixture.store.QueryFixMonitoring(
		ctx,
		model.FixMonitoringQuery{},
	); !errors.Is(err, model.ErrFixMonitoringCatchingUp) {
		t.Fatalf("monitoring read before retry error = %v", err)
	}

	worker.now = func() time.Time {
		return fixture.claimAt.Add(2 * time.Second)
	}
	second, err := worker.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Failed != 0 || second.Completed != 1 ||
		second.Observations != 1 {
		t.Fatalf("retry drain report = %+v", second)
	}
	assertFixMonitoringReadiness(
		t,
		fixture.store,
		model.FixMonitoringReadinessReady,
	)
	page, err := fixture.store.QueryFixMonitoring(
		ctx,
		model.FixMonitoringQuery{},
	)
	if err != nil {
		t.Fatalf("monitoring read remained blocked after retry: %v", err)
	}
	if len(page.Data) != 1 ||
		page.Data[0].FixRecurrenceState != model.FixRecurrenceMatchingEvidence {
		t.Fatalf("monitoring page after retry = %+v", page)
	}
}

func TestRecurrenceWorkerRecoversCrashBeforeBatchCommit(t *testing.T) {
	fixture := newRecurrenceWorkerFixture(t)
	ctx := context.Background()
	claim, err := fixture.store.ClaimRecurrenceJob(
		ctx,
		fixture.claimAt,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim.JobID == "" {
		t.Fatal("claimed recurrence job has no ID")
	}

	worker := NewRecurrenceWorker(fixture.store)
	worker.now = func() time.Time {
		return fixture.claimAt.Add(time.Minute)
	}
	report, err := worker.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Completed != 1 || report.Observations != 1 {
		t.Fatalf("reclaimed pre-commit job report = %+v", report)
	}
	assertFixMonitoringReadiness(
		t,
		fixture.store,
		model.FixMonitoringReadinessReady,
	)
	assertRecurrenceObservationCount(t, fixture.store, fixture.issueID, 1)
}

func TestRecurrenceWorkerRecoversCrashAfterNonterminalBatchCommit(t *testing.T) {
	fixture := newRecurrenceWorkerFixtureWithAttempts(t, 201)
	ctx := context.Background()
	claim, err := fixture.store.ClaimRecurrenceJob(
		ctx,
		fixture.claimAt,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Completed || batch.Processed != 100 || batch.Observed != 100 ||
		batch.NextSequence <= claim.AttemptAfterSequence {
		t.Fatalf("committed recurrence batch = %+v", batch)
	}
	assertFixMonitoringReadiness(
		t,
		fixture.store,
		model.FixMonitoringReadinessCatchingUp,
	)

	worker := NewRecurrenceWorker(fixture.store)
	worker.now = func() time.Time {
		return fixture.claimAt.Add(time.Minute)
	}
	report, err := worker.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Claimed != 1 || report.Batches != 2 ||
		report.Processed != 101 || report.Completed != 1 ||
		report.Observations != 101 {
		t.Fatalf("post-commit recovery did not resume durable cursor: %+v", report)
	}
	assertFixMonitoringReadiness(
		t,
		fixture.store,
		model.FixMonitoringReadinessReady,
	)
	assertRecurrenceObservationTotals(t, fixture.store, fixture.issueID, 201)
	empty, err := worker.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if empty != (RecurrenceReport{}) {
		t.Fatalf("completed recovery was not idempotent: %+v", empty)
	}
	assertRecurrenceObservationTotals(t, fixture.store, fixture.issueID, 201)
}

func TestRecurrenceRetryDelayIsBounded(t *testing.T) {
	if got := recurrenceRetryDelay(0); got != time.Second {
		t.Fatalf("initial retry = %s", got)
	}
	if got := recurrenceRetryDelay(100); got != 64*time.Second {
		t.Fatalf("bounded retry = %s", got)
	}
}

type recurrenceWorkerFixture struct {
	store   *local.Store
	claimAt time.Time
	issueID string
}

func newRecurrenceWorkerFixture(t *testing.T) recurrenceWorkerFixture {
	return newRecurrenceWorkerFixtureWithAttempts(t, 1)
}

func newRecurrenceWorkerFixtureWithAttempts(
	t *testing.T,
	attemptCount int,
) recurrenceWorkerFixture {
	t.Helper()
	if attemptCount < 1 {
		t.Fatal("recurrence worker fixture requires at least one attempt")
	}
	ctx := context.Background()
	store := openAnalysisStore(t)
	sessionID := "recurrence-worker-" + fmt.Sprint(time.Now().UnixNano())
	first := appendAnalysisEvent(t, store, sessionID, 1)
	scope, err := store.DeriveProjectScope(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSessionScope(ctx, sessionID, scope); err != nil {
		t.Fatal(err)
	}
	catalog, err := detection.NewCatalog(recurrenceWorkerDetector{})
	if err != nil {
		t.Fatal(err)
	}
	reconciler := NewReconcilerWithCatalog(store, catalog)
	if _, err := reconciler.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	drainRecurrenceJobsBeforeAnnotation(t, store, time.Now().UTC())

	issues, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues.Data) != 1 {
		t.Fatalf("initial issue page = %+v", issues)
	}
	var lastAnnotation model.FixAnnotation
	for index := range attemptCount {
		annotation, err := store.RecordFixAnnotation(
			ctx,
			local.FixAnnotationInput{
				Claims: model.FixActionClaims{
					Version:             model.FixActionTokenVersion,
					CursorEpoch:         issues.CursorEpoch,
					IssueID:             issues.Data[0].IssueID,
					Snapshot:            issues.Snapshot,
					RetentionGeneration: issues.RetentionGeneration,
					IssuedAt:            issues.IssuedAt,
					ExpiresAt:           issues.IssuedAt.Add(15 * time.Minute),
				},
				ChangeKind:  model.FixChangeCode,
				RecordedVia: model.FixRecordedViaLocalUI,
				IdempotencyKey: fmt.Sprintf(
					"00000000-0000-4000-8000-%012d",
					40_001+index,
				),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		lastAnnotation = annotation.Annotation
	}

	later := first
	later.EventID = "event-" + sessionID + "-later"
	later.OccurredAt = lastAnnotation.MonitorFrom.Add(time.Second)
	later.ObservedAt = later.OccurredAt
	later.Source.RecordID = "record-" + sessionID + "-later"
	later.Source.DeduplicationKey = "dedup-" + sessionID + "-later"
	later.Source.Sequence = 2
	if _, err := store.AppendEventResolved(ctx, later); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	return recurrenceWorkerFixture{
		store:   store,
		claimAt: time.Now().UTC(),
		issueID: issues.Data[0].IssueID,
	}
}

type recurrenceWorkerDetector struct{}

func (recurrenceWorkerDetector) ID() string                 { return "recurrence_worker_test" }
func (recurrenceWorkerDetector) Version() string            { return "1" }
func (recurrenceWorkerDetector) FingerprintVersion() string { return "1" }
func (recurrenceWorkerDetector) Evaluate(
	_ context.Context,
	input detection.SessionInput,
) (detection.DetectorResult, error) {
	event := input.Events[len(input.Events)-1]
	return detection.DetectorResult{
		AbsenceCapability: detection.AbsenceSupported,
		Matches: []detection.Match{{
			DetectorID:         "recurrence_worker_test",
			DetectorVersion:    "1",
			FingerprintVersion: "1",
			Category:           "test_issue",
			TitleCode:          "issue.test",
			Severity:           "low",
			Confidence:         "high",
			FirstObservedAt:    event.OccurredAt,
			LastObservedAt:     event.OccurredAt,
			CitedEventIDs:      []string{event.EventID},
			EvidenceComplete:   true,
			Fingerprint: []detection.FingerprintDimension{{
				Name:  "test_signature",
				Value: "same",
			}},
		}},
	}, nil
}

type flakyRecurrenceWorkerDetector struct {
	remainingFailures *atomic.Int32
}

func (flakyRecurrenceWorkerDetector) ID() string {
	return "flaky_recurrence_worker_test"
}

func (flakyRecurrenceWorkerDetector) Version() string { return "1" }

func (flakyRecurrenceWorkerDetector) FingerprintVersion() string {
	return "1"
}

func (d flakyRecurrenceWorkerDetector) Evaluate(
	ctx context.Context,
	input detection.SessionInput,
) (detection.DetectorResult, error) {
	if d.remainingFailures != nil &&
		d.remainingFailures.CompareAndSwap(1, 0) {
		return detection.DetectorResult{}, errors.New(
			"injected transient analysis failure",
		)
	}
	result, err := (recurrenceWorkerDetector{}).Evaluate(ctx, input)
	if err != nil {
		return result, err
	}
	for index := range result.Matches {
		result.Matches[index].DetectorID = d.ID()
		result.Matches[index].DetectorVersion = d.Version()
		result.Matches[index].FingerprintVersion = d.FingerprintVersion()
	}
	return result, nil
}

func drainRecurrenceJobsBeforeAnnotation(
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
			batch, err := store.ProcessRecurrenceBatch(ctx, claim, 100)
			if err != nil {
				t.Fatal(err)
			}
			claim.AttemptAfterSequence = batch.NextSequence
			if batch.Completed {
				break
			}
		}
	}
}

func assertFixMonitoringReadiness(
	t *testing.T,
	store *local.Store,
	want string,
) {
	t.Helper()
	readiness, err := store.FixMonitoringReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if readiness != want {
		t.Fatalf("readiness = %q, want %q", readiness, want)
	}
}

func assertRecurrenceObservationCount(
	t *testing.T,
	store *local.Store,
	issueID string,
	want int,
) {
	t.Helper()
	page, err := store.QueryFixMonitoring(
		context.Background(),
		model.FixMonitoringQuery{
			Filter: model.FixMonitoringFilter{IssueID: issueID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].FixRecurrenceCount != want {
		t.Fatalf("monitoring page = %+v, want recurrence count %d", page, want)
	}
}

func assertRecurrenceObservationTotals(
	t *testing.T,
	store *local.Store,
	issueID string,
	want int,
) {
	t.Helper()
	page, err := store.QueryFixMonitoring(
		context.Background(),
		model.FixMonitoringQuery{
			Filter: model.FixMonitoringFilter{IssueID: issueID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 ||
		page.Data[0].ObservedAttemptCount != want ||
		page.Data[0].HistoricalMatchingEvidenceCount != want {
		t.Fatalf(
			"monitoring page = %+v, want %d observed attempts/evidence rows",
			page,
			want,
		)
	}
}
