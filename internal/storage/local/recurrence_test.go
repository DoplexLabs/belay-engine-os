package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestRecurrenceQualificationUsesStrictNormalizedInstant(t *testing.T) {
	for _, test := range []struct {
		name   string
		offset time.Duration
		want   int
	}{
		{name: "earlier late import", offset: -time.Nanosecond, want: 0},
		{name: "equal instant", offset: 0, want: 0},
		{name: "strictly later", offset: time.Nanosecond, want: 1},
		{name: "timezone equivalent later", offset: time.Second, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, annotation := newRecurrenceFixture(t)
			eventAt := fixture.now.Add(test.offset)
			projectRecurrenceOccurrence(t, fixture, eventAt, 901)
			claim, err := fixture.store.ClaimRecurrenceJob(
				context.Background(),
				*fixture.now,
				time.Minute,
			)
			if err != nil {
				t.Fatal(err)
			}
			result, err := fixture.store.ProcessRecurrenceBatch(
				context.Background(),
				claim,
				100,
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Observed != test.want || !result.Completed {
				t.Fatalf("batch = %+v, want observed %d and complete", result, test.want)
			}
			var count int
			if err := fixture.store.db.QueryRowContext(
				context.Background(),
				`SELECT COUNT(*) FROM fix_recurrence_observations
				WHERE annotation_id = ?`,
				annotation.Annotation.AnnotationID,
			).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != test.want {
				t.Fatalf("observations = %d, want %d", count, test.want)
			}
		})
	}
}

func TestRecurrenceBatchingRepeatProjectionAndSnapshotRepositories(t *testing.T) {
	fixture := newFixTestFixture(
		t,
		1,
		model.ScopeResolved,
		"belay",
		"command_failure",
		false,
	)
	ctx := context.Background()
	const attempts = 201
	var firstAnnotation string
	for index := range attempts {
		result, err := fixture.store.RecordFixAnnotation(
			ctx,
			fixture.input(fmt.Sprintf(
				"00000000-0000-4000-8000-%012d",
				10_000+index,
			)),
		)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			firstAnnotation = result.Annotation.AnnotationID
		}
	}
	projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 902)
	claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []int{100, 100, 1} {
		result, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100)
		if err != nil {
			t.Fatal(err)
		}
		if result.Processed != want || result.Observed != want ||
			result.Completed != (index == 2) {
			t.Fatalf("batch %d = %+v, want processed/observed %d", index, result, want)
		}
	}
	var observations int
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM fix_recurrence_observations",
	).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if observations != attempts {
		t.Fatalf("observations = %d, want %d", observations, attempts)
	}

	// A repeat projection carries the same stable occurrence identity. It must
	// complete without duplicating or treating changed revision provenance as
	// an identity collision.
	projectRecurrenceOccurrence(t, fixture, fixture.now.Add(2*time.Minute), 903)
	repeat, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := fixture.store.ProcessRecurrenceBatch(ctx, repeat, 100)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Observed != 0 {
		t.Fatalf("repeat projection inserted %d observations", repeated.Observed)
	}
	for !repeated.Completed {
		repeated, err = fixture.store.ProcessRecurrenceBatch(ctx, repeat, 100)
		if err != nil {
			t.Fatal(err)
		}
		if repeated.Observed != 0 {
			t.Fatalf("repeat projection inserted %d observations", repeated.Observed)
		}
	}
	if ready, err := fixture.store.CompleteFixMonitoringCatchup(ctx); err != nil || !ready {
		t.Fatalf("CompleteFixMonitoringCatchup() = %v, %v", ready, err)
	}

	detail, err := fixture.store.QueryIssueFixMonitoring(
		ctx,
		model.FixMonitoringDetailQuery{
			IssueID: fixture.issueID,
			Limit:   1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Data) != 1 || !detail.HasMore || detail.Position == nil ||
		detail.Data[0].FixRecurrenceState != model.FixRecurrenceMatchingEvidence {
		t.Fatalf("detail page = %+v", detail)
	}
	second, err := fixture.store.QueryIssueFixMonitoring(
		ctx,
		model.FixMonitoringDetailQuery{
			IssueID:  fixture.issueID,
			Limit:    1,
			Snapshot: detail.Snapshot,
			Cursor:   detail.Position,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 ||
		second.Data[0].AnnotationID == detail.Data[0].AnnotationID {
		t.Fatalf("second detail page = %+v", second)
	}
	observed, err := fixture.store.QueryFixRecurrenceObservations(
		ctx,
		model.FixRecurrenceObservationQuery{
			IssueID:      fixture.issueID,
			AnnotationID: firstAnnotation,
			Limit:        20,
			Snapshot:     detail.Snapshot,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Data) != 1 ||
		observed.Data[0].EvidenceCurrentlyRetained !=
			model.FixRecurrenceEvidenceAvailable ||
		len(observed.Data[0].RetainedEventIDs) != 1 {
		t.Fatalf("observation page = %+v", observed)
	}
}

func TestTopLevelMonitoringEnrichesOnlyBoundedDriverPage(t *testing.T) {
	fixture, _ := newRecurrenceFixture(t)
	ctx := context.Background()
	for index := range 205 {
		if _, err := fixture.store.RecordFixAnnotation(
			ctx,
			fixture.input(fmt.Sprintf(
				"00000000-0000-4000-8000-%012d",
				40_000+index,
			)),
		); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := fixture.store.CompleteFixMonitoringCatchup(ctx); err != nil || !ready {
		t.Fatalf("CompleteFixMonitoringCatchup() = %v, %v", ready, err)
	}
	first, err := fixture.store.QueryFixMonitoring(
		ctx,
		model.FixMonitoringQuery{Limit: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 1 {
		t.Fatalf("initial bounded page = %+v", first)
	}
	driverID := first.Data[0].AnnotationID
	var nonDriverID string
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT annotation_id
		FROM fix_annotations
		WHERE annotation_id <> ?
		ORDER BY sequence
		LIMIT 1`,
		driverID,
	).Scan(&nonDriverID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `
		UPDATE fix_monitoring_subjects
		SET captured_at = 'not-a-timestamp'
		WHERE annotation_id = ?`,
		nonDriverID,
	); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(fixture.path, OpenOptions{
		KeyProvider: fixture.provider,
		Clock:       func() time.Time { return *fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	page, err := reopened.QueryFixMonitoring(
		ctx,
		model.FixMonitoringQuery{Limit: 1},
	)
	if err != nil {
		t.Fatalf("bounded top-level query enriched an out-of-page attempt: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].AnnotationID != driverID {
		t.Fatalf("bounded driver changed: %+v, want %q", page, driverID)
	}
}

func TestTopLevelMonitoringDriverUsesIndexedBoundedEventProbes(t *testing.T) {
	fixture, _ := newRecurrenceFixture(t)
	ctx := context.Background()
	for index := range 500 {
		if _, err := fixture.store.RecordFixAnnotation(
			ctx,
			fixture.input(fmt.Sprintf(
				"00000000-0000-4000-8000-%012d",
				50_000+index,
			)),
		); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := fixture.store.CompleteFixMonitoringCatchup(ctx); err != nil || !ready {
		t.Fatalf("CompleteFixMonitoringCatchup() = %v, %v", ready, err)
	}
	page, err := fixture.store.QueryFixMonitoring(
		ctx,
		model.FixMonitoringQuery{Limit: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	query := model.FixMonitoringQuery{Limit: 1}
	statement, args := monitoringSummarySeedQuery(page.Snapshot, query)
	rows, err := fixture.store.db.QueryContext(
		ctx,
		"EXPLAIN QUERY PLAN "+statement,
		args...,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	detail := plan.String()
	for _, required := range []string{
		"events_monitoring_session_time_idx",
		"fix_recurrence_observations_snapshot_idx",
	} {
		if !strings.Contains(detail, required) {
			t.Fatalf("monitoring driver plan lacks %q:\n%s", required, detail)
		}
	}
	for _, forbidden := range []string{
		"SCAN later_event",
		"SCAN pending_event",
		"attempt_event_sessions",
	} {
		if strings.Contains(detail, forbidden) {
			t.Fatalf("monitoring driver plan contains unbounded %q:\n%s", forbidden, detail)
		}
	}
	if !strings.Contains(statement, "LIMIT ?") {
		t.Fatalf("monitoring driver query has no page bound")
	}
}

func TestRecurrenceRetractionLeaseCASAndReadiness(t *testing.T) {
	t.Run("retraction before insert", func(t *testing.T) {
		fixture, annotation := newRecurrenceFixture(t)
		if _, err := fixture.store.RetractFixAnnotation(
			context.Background(),
			FixRetractionInput{
				IssueID:        fixture.issueID,
				AnnotationID:   annotation.Annotation.AnnotationID,
				Reason:         model.FixRetractionSuperseded,
				RecordedVia:    model.FixRecordedViaLocalUI,
				IdempotencyKey: "00000000-0000-4000-8000-000000030001",
			},
		); err != nil {
			t.Fatal(err)
		}
		projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 904)
		claim, err := fixture.store.ClaimRecurrenceJob(
			context.Background(),
			*fixture.now,
			time.Minute,
		)
		if err != nil {
			t.Fatal(err)
		}
		result, err := fixture.store.ProcessRecurrenceBatch(
			context.Background(),
			claim,
			100,
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Observed != 0 {
			t.Fatalf("retracted attempt observed = %+v", result)
		}
	})

	t.Run("expired lease rejects stale worker", func(t *testing.T) {
		fixture, _ := newRecurrenceFixture(t)
		projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 905)
		first, err := fixture.store.ClaimRecurrenceJob(
			context.Background(),
			*fixture.now,
			time.Second,
		)
		if err != nil {
			t.Fatal(err)
		}
		second, err := fixture.store.ClaimRecurrenceJob(
			context.Background(),
			fixture.now.Add(2*time.Second),
			time.Minute,
		)
		if err != nil {
			t.Fatal(err)
		}
		if first.ClaimGeneration == second.ClaimGeneration ||
			first.ClaimToken == second.ClaimToken {
			t.Fatalf("reclaimed claim did not rotate identity: %#v %#v", first, second)
		}
		if _, err := fixture.store.ProcessRecurrenceBatch(
			context.Background(),
			first,
			100,
		); !errors.Is(err, ErrStaleRecurrenceJob) {
			t.Fatalf("stale batch error = %v", err)
		}
		if _, err := fixture.store.ProcessRecurrenceBatch(
			context.Background(),
			second,
			100,
		); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("readiness fails closed", func(t *testing.T) {
		fixture, _ := newRecurrenceFixture(t)
		if _, err := fixture.store.QueryFixMonitoring(
			context.Background(),
			model.FixMonitoringQuery{},
		); !errors.Is(err, model.ErrFixMonitoringCatchingUp) {
			t.Fatalf("catching-up read error = %v", err)
		}
		if err := fixture.store.FailFixMonitoringCatchup(
			context.Background(),
			"test_failure",
			fixture.now.Add(time.Minute),
		); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.QueryFixMonitoring(
			context.Background(),
			model.FixMonitoringQuery{},
		); !errors.Is(err, model.ErrFixMonitoringCatchupFailed) {
			t.Fatalf("failed read error = %v", err)
		}
	})
}

func TestRecurrenceConcurrentRetractionAndWorkerRemainConsistent(t *testing.T) {
	fixture, annotation := newRecurrenceFixture(t)
	ctx := context.Background()
	projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 934)
	claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenWithOptions(fixture.path, OpenOptions{
		KeyProvider: fixture.provider,
		Clock:       func() time.Time { return *fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	start := make(chan struct{})
	type result struct {
		err error
	}
	workerResult := make(chan result, 1)
	retractionResult := make(chan result, 1)
	go func() {
		<-start
		_, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100)
		workerResult <- result{err: err}
	}()
	go func() {
		<-start
		_, err := second.RetractFixAnnotation(ctx, FixRetractionInput{
			IssueID:        fixture.issueID,
			AnnotationID:   annotation.Annotation.AnnotationID,
			Reason:         model.FixRetractionSuperseded,
			RecordedVia:    model.FixRecordedViaLocalUI,
			IdempotencyKey: "00000000-0000-4000-8000-000000030002",
		})
		retractionResult <- result{err: err}
	}()
	close(start)
	if result := <-workerResult; result.err != nil {
		t.Fatalf("concurrent worker error = %v", result.err)
	}
	if result := <-retractionResult; result.err != nil {
		t.Fatalf("concurrent retraction error = %v", result.err)
	}
	var observations, retractions int
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM fix_recurrence_observations
		WHERE annotation_id = ?`,
		annotation.Annotation.AnnotationID,
	).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM fix_annotation_retractions
		WHERE annotation_id = ?`,
		annotation.Annotation.AnnotationID,
	).Scan(&retractions); err != nil {
		t.Fatal(err)
	}
	if observations < 0 || observations > 1 || retractions != 1 {
		t.Fatalf(
			"concurrent observation/retraction count = %d/%d, want <=1/1",
			observations,
			retractions,
		)
	}
	if ready, err := fixture.store.CompleteFixMonitoringCatchup(ctx); err != nil || !ready {
		t.Fatalf("CompleteFixMonitoringCatchup() = %v, %v", ready, err)
	}
	detail, err := fixture.store.QueryIssueFixMonitoring(
		ctx,
		model.FixMonitoringDetailQuery{IssueID: fixture.issueID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Data) != 1 ||
		detail.Data[0].State != model.FixStateRetracted ||
		detail.Data[0].HistoricalMatchingEvidenceCount != observations ||
		detail.Data[0].FixRecurrenceCount != 0 {
		t.Fatalf("concurrent retraction detail = %+v", detail)
	}
}

func TestRecurrenceClaimRecoversAfterStoreRestart(t *testing.T) {
	fixture, annotation := newRecurrenceFixture(t)
	ctx := context.Background()
	projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 935)
	stale, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(fixture.path, OpenOptions{
		KeyProvider: fixture.provider,
		Clock:       func() time.Time { return *fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reclaimed, err := reopened.ClaimRecurrenceJob(
		ctx,
		fixture.now.Add(2*time.Second),
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.ClaimGeneration <= stale.ClaimGeneration ||
		reclaimed.ClaimToken == stale.ClaimToken {
		t.Fatalf("restart reclaim did not rotate CAS identity: %#v %#v", stale, reclaimed)
	}
	if _, err := reopened.ProcessRecurrenceBatch(
		ctx,
		stale,
		100,
	); !errors.Is(err, ErrStaleRecurrenceJob) {
		t.Fatalf("pre-restart claim error = %v, want stale", err)
	}
	if result, err := reopened.ProcessRecurrenceBatch(
		ctx,
		reclaimed,
		100,
	); err != nil || result.Observed != 1 || !result.Completed {
		t.Fatalf("reclaimed batch = %+v, %v", result, err)
	}
	var observations int
	if err := reopened.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM fix_recurrence_observations
		WHERE annotation_id = ?`,
		annotation.Annotation.AnnotationID,
	).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if observations != 1 {
		t.Fatalf("restart observations = %d, want 1", observations)
	}
}

func TestRecurrenceMutationGuardsRejectUnauthorizedReplacement(t *testing.T) {
	fixture, _ := newRecurrenceFixture(t)
	projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 906)
	ctx := context.Background()
	claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100); err != nil {
		t.Fatal(err)
	}
	second, err := Open(fixture.path, fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	statements := []string{
		"UPDATE fix_monitoring_subjects SET category = category",
		"DELETE FROM fix_monitoring_subjects",
		"INSERT OR REPLACE INTO fix_monitoring_subjects SELECT * FROM fix_monitoring_subjects",
		"UPDATE session_analysis_capabilities SET detector_version = detector_version",
		"DELETE FROM session_analysis_capabilities",
		"INSERT OR REPLACE INTO session_analysis_capabilities SELECT * FROM session_analysis_capabilities",
		"UPDATE fix_recurrence_jobs SET state = state",
		"DELETE FROM fix_recurrence_jobs",
		"UPDATE fix_recurrence_job_events SET event_kind = event_kind",
		"DELETE FROM fix_recurrence_job_events",
		"INSERT OR REPLACE INTO fix_recurrence_job_events SELECT * FROM fix_recurrence_job_events",
		"UPDATE fix_recurrence_observations SET issue_id = issue_id",
		"DELETE FROM fix_recurrence_observations",
		"INSERT OR REPLACE INTO fix_recurrence_observations SELECT * FROM fix_recurrence_observations",
		"UPDATE fix_recurrence_observation_events SET event_id = event_id",
		"DELETE FROM fix_recurrence_observation_events",
	}
	for storeIndex, store := range []*Store{fixture.store, second} {
		for _, statement := range statements {
			if _, err := store.db.ExecContext(ctx, statement); err == nil {
				t.Fatalf(
					"store %d unauthorized recurrence mutation succeeded: %s",
					storeIndex,
					statement,
				)
			}
		}
	}
}

func TestRecurrenceTablesDoNotReflectRawObservationCanary(t *testing.T) {
	fixture, _ := newRecurrenceFixture(t)
	ctx := context.Background()
	const canary = "belay-private-command-output-canary-7c4a"
	eventAt := fixture.now.Add(time.Minute)
	eventID := "00000000-0000-7000-8000-000000000936"
	event := storageTestEvent(eventID, fixture.sessionID, 936, eventAt)
	event.Observation.Summary = canary
	appended, err := fixture.store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := testIssueOccurrence(
		fixture.sessionID,
		"codex",
		eventID,
		fixture.fingerprint,
		fixture.issueID,
		"medium",
		"high",
		eventAt,
		model.ScopeResolved,
	)
	watermark := eventAt.UnixNano()
	if _, err := fixture.store.ReplaceSessionProjection(
		ctx,
		SessionProjectionReplacement{
			SessionKey: fixture.sessionID,
			ClaimedGeneration: dirtyTargetGeneration(
				t, fixture.store, fixture.sessionID,
			),
			Status:                  model.AnalysisCurrent,
			ScopeQuality:            model.ScopeResolved,
			AnalysisThroughOrderNS:  &watermark,
			AnalyzedEventGeneration: appended.ReadGeneration,
			BelayOccurrences:        []model.IssueOccurrence{occurrence},
			Capabilities: []model.AnalysisCapability{{
				SessionID:              fixture.sessionID,
				FingerprintScopeID:     occurrence.FingerprintScopeID,
				Origin:                 "belay",
				DetectorID:             occurrence.Provenance.DetectorID,
				DetectorVersion:        occurrence.Provenance.DetectorVersion,
				FingerprintVersion:     occurrence.FingerprintVersion,
				NegativeComparisonMode: model.FixNegativeComparisonSupported,
				AnalysisThroughOrderNS: &watermark,
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		`SELECT COUNT(*) FROM fix_monitoring_subjects WHERE
			instr(COALESCE(category, '') || COALESCE(title_code, '') ||
				COALESCE(anchor_harness, '') || detector_id ||
				detector_version || fingerprint_version, ?) > 0`,
		`SELECT COUNT(*) FROM fix_recurrence_jobs WHERE
			instr(job_id || session_id || revision_id || state, ?) > 0`,
		`SELECT COUNT(*) FROM fix_recurrence_job_events WHERE
			instr(job_id || event_kind, ?) > 0`,
		`SELECT COUNT(*) FROM fix_recurrence_observations WHERE
			instr(recurrence_id || annotation_id || issue_id || revision_id ||
				occurrence_id || session_id || fingerprint_id ||
				fingerprint_version || origin || detector_id ||
				detector_version, ?) > 0`,
	}
	for _, query := range queries {
		var count int
		if err := fixture.store.db.QueryRowContext(ctx, query, canary).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("recurrence table reflected raw canary for query %q", query)
		}
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	assertSQLiteFilesExclude(t, fixture.path, canary)
}

func TestRecurrenceConcurrentStoresClaimOneJob(t *testing.T) {
	fixture, _ := newRecurrenceFixture(t)
	projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 916)
	second, err := Open(fixture.path, fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	type claimResult struct {
		claim model.FixRecurrenceJobClaim
		err   error
	}
	results := make(chan claimResult, 2)
	for _, store := range []*Store{fixture.store, second} {
		go func(store *Store) {
			claim, err := store.ClaimRecurrenceJob(
				context.Background(),
				*fixture.now,
				time.Minute,
			)
			results <- claimResult{claim: claim, err: err}
		}(store)
	}
	var winner model.FixRecurrenceJobClaim
	wins, empty := 0, 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			wins++
			winner = result.claim
		case errors.Is(result.err, ErrNoRecurrenceJob):
			empty++
		default:
			t.Fatalf("concurrent claim error = %v", result.err)
		}
	}
	if wins != 1 || empty != 1 {
		t.Fatalf("concurrent claim wins/empty = %d/%d, want 1/1", wins, empty)
	}
	if result, err := fixture.store.ProcessRecurrenceBatch(
		context.Background(),
		winner,
		100,
	); err != nil || result.Observed != 1 {
		t.Fatalf("winning claim batch = %+v, %v", result, err)
	}
}

func TestRecurrenceCoverageIsSnapshotStableAndNumbatIsPositiveOnly(t *testing.T) {
	t.Run("complete Belay absence and later dirty event", func(t *testing.T) {
		fixture, _ := newRecurrenceFixture(t)
		ctx := context.Background()
		appendResult := appendRecurrenceEvent(
			t,
			fixture,
			fixture.sessionID,
			fixture.now.Add(time.Minute),
			908,
		)
		watermark := fixture.now.Add(time.Minute).UnixNano()
		if _, err := fixture.store.ReplaceSessionProjection(
			ctx,
			SessionProjectionReplacement{
				SessionKey: fixture.sessionID,
				ClaimedGeneration: dirtyTargetGeneration(
					t, fixture.store, fixture.sessionID,
				),
				Status:                  model.AnalysisCurrent,
				ScopeQuality:            model.ScopeResolved,
				AnalysisThroughOrderNS:  &watermark,
				AnalyzedEventGeneration: appendResult.ReadGeneration,
				Capabilities: []model.AnalysisCapability{{
					SessionID:              fixture.sessionID,
					FingerprintScopeID:     "psc_" + strings.Repeat("a", 52),
					Origin:                 "belay",
					DetectorID:             "explicit_command_failure",
					DetectorVersion:        "1.0.0",
					FingerprintVersion:     "failure.v1",
					NegativeComparisonMode: model.FixNegativeComparisonSupported,
					AnalysisThroughOrderNS: &watermark,
				}},
			},
		); err != nil {
			t.Fatal(err)
		}
		claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100); err != nil {
			t.Fatal(err)
		}
		if ready, err := fixture.store.CompleteFixMonitoringCatchup(ctx); err != nil || !ready {
			t.Fatalf("CompleteFixMonitoringCatchup() = %v, %v", ready, err)
		}
		first, err := fixture.store.QueryIssueFixMonitoring(
			ctx,
			model.FixMonitoringDetailQuery{IssueID: fixture.issueID},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Data) != 1 ||
			first.Data[0].FixRecurrenceState != model.FixRecurrenceNoLaterMatch {
			t.Fatalf("complete absence detail = %+v", first)
		}
		appendRecurrenceEvent(
			t,
			fixture,
			fixture.sessionID,
			fixture.now.Add(2*time.Minute),
			909,
		)
		stable, err := fixture.store.QueryIssueFixMonitoring(
			ctx,
			model.FixMonitoringDetailQuery{
				IssueID:  fixture.issueID,
				Snapshot: first.Snapshot,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if stable.Data[0].FixRecurrenceState != model.FixRecurrenceNoLaterMatch {
			t.Fatalf("old snapshot changed after new event: %+v", stable.Data[0])
		}
		fresh, err := fixture.store.QueryIssueFixMonitoring(
			ctx,
			model.FixMonitoringDetailQuery{IssueID: fixture.issueID},
		)
		if err != nil {
			t.Fatal(err)
		}
		if fresh.Data[0].FixRecurrenceState != model.FixRecurrenceMonitoringIncomplete {
			t.Fatalf("fresh dirty coverage = %+v", fresh.Data[0])
		}
		future := first.Snapshot
		future.EventGeneration += 1_000
		if _, err := fixture.store.QueryIssueFixMonitoring(
			ctx,
			model.FixMonitoringDetailQuery{
				IssueID:  fixture.issueID,
				Snapshot: future,
			},
		); !errors.Is(err, model.ErrFixMonitoringSnapshotInvalid) {
			t.Fatalf("future snapshot error = %v", err)
		}
	})

	t.Run("Numbat absence is comparison unavailable", func(t *testing.T) {
		fixture := newFixTestFixture(
			t,
			1,
			model.ScopeResolved,
			"numbat",
			"numbat_finding",
			false,
		)
		ctx := context.Background()
		if _, err := fixture.store.RecordFixAnnotation(
			ctx,
			fixture.input("00000000-0000-4000-8000-000000050001"),
		); err != nil {
			t.Fatal(err)
		}
		appended := appendRecurrenceEvent(
			t,
			fixture,
			fixture.sessionID,
			fixture.now.Add(time.Minute),
			910,
		)
		watermark := fixture.now.Add(time.Minute).UnixNano()
		if _, err := fixture.store.ReplaceSessionProjection(
			ctx,
			SessionProjectionReplacement{
				SessionKey: fixture.sessionID,
				ClaimedGeneration: dirtyTargetGeneration(
					t, fixture.store, fixture.sessionID,
				),
				Status:                  model.AnalysisCurrent,
				ScopeQuality:            model.ScopeResolved,
				AnalysisThroughOrderNS:  &watermark,
				AnalyzedEventGeneration: appended.ReadGeneration,
				Capabilities: []model.AnalysisCapability{{
					SessionID:              fixture.sessionID,
					FingerprintScopeID:     "psc_" + strings.Repeat("a", 52),
					Origin:                 "numbat",
					DetectorID:             "numbat_finding",
					DetectorVersion:        "1",
					FingerprintVersion:     "failure.v1",
					NegativeComparisonMode: model.FixNegativeComparisonPositiveOnly,
					AnalysisThroughOrderNS: &watermark,
				}},
			},
		); err != nil {
			t.Fatal(err)
		}
		claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100); err != nil {
			t.Fatal(err)
		}
		if ready, err := fixture.store.CompleteFixMonitoringCatchup(ctx); err != nil || !ready {
			t.Fatalf("CompleteFixMonitoringCatchup() = %v, %v", ready, err)
		}
		detail, err := fixture.store.QueryIssueFixMonitoring(
			ctx,
			model.FixMonitoringDetailQuery{IssueID: fixture.issueID},
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Data) != 1 ||
			detail.Data[0].FixRecurrenceState !=
				model.FixRecurrenceComparisonUnavailable ||
			detail.Data[0].FutureComparisonUnavailableReason == nil ||
			*detail.Data[0].FutureComparisonUnavailableReason !=
				model.FixComparisonSourcePositiveOnly {
			t.Fatalf("Numbat monitoring detail = %+v", detail)
		}
	})
}

func TestMonitoringSnapshotExpiresAtomicallyWhenSessionScopeChanges(t *testing.T) {
	fixture, _ := newRecurrenceFixture(t)
	ctx := context.Background()
	appended := appendRecurrenceEvent(
		t,
		fixture,
		fixture.sessionID,
		fixture.now.Add(time.Minute),
		917,
	)
	watermark := fixture.now.Add(time.Minute).UnixNano()
	if _, err := fixture.store.ReplaceSessionProjection(
		ctx,
		SessionProjectionReplacement{
			SessionKey: fixture.sessionID,
			ClaimedGeneration: dirtyTargetGeneration(
				t, fixture.store, fixture.sessionID,
			),
			Status:                  model.AnalysisCurrent,
			ScopeQuality:            model.ScopeResolved,
			AnalysisThroughOrderNS:  &watermark,
			AnalyzedEventGeneration: appended.ReadGeneration,
			Capabilities: []model.AnalysisCapability{{
				SessionID:              fixture.sessionID,
				FingerprintScopeID:     "psc_" + strings.Repeat("a", 52),
				Origin:                 "belay",
				DetectorID:             "explicit_command_failure",
				DetectorVersion:        "1.0.0",
				FingerprintVersion:     "failure.v1",
				NegativeComparisonMode: model.FixNegativeComparisonSupported,
				AnalysisThroughOrderNS: &watermark,
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100); err != nil {
		t.Fatal(err)
	}
	if ready, err := fixture.store.CompleteFixMonitoringCatchup(ctx); err != nil || !ready {
		t.Fatalf("CompleteFixMonitoringCatchup() = %v, %v", ready, err)
	}
	page, err := fixture.store.QueryIssueFixMonitoring(
		ctx,
		model.FixMonitoringDetailQuery{IssueID: fixture.issueID},
	)
	if err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT retention_generation
		FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.UpsertSessionScope(
		ctx,
		fixture.sessionID,
		ProjectScope{
			ID:                   "psc_" + strings.Repeat("b", 52),
			NormalizationVersion: projectScopeNormalizationVersion,
			Quality:              model.ScopeResolved,
		},
	); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT retention_generation
		FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("scope mutation retention generation = %d, want %d", after, before+1)
	}
	if _, err := fixture.store.QueryIssueFixMonitoring(
		ctx,
		model.FixMonitoringDetailQuery{
			IssueID:  fixture.issueID,
			Snapshot: page.Snapshot,
		},
	); !errors.Is(err, model.ErrFixMonitoringSnapshotExpired) {
		t.Fatalf("old monitoring snapshot error = %v, want expired", err)
	}
}

func TestRecurrenceExactScopeVersionAndSessionQualification(t *testing.T) {
	for _, test := range []struct {
		name      string
		sessionID string
		scopeID   string
		version   string
		want      int
		identity  int
	}{
		{
			name:      "other session exact match",
			sessionID: "recurrence-other-session",
			scopeID:   "psc_" + strings.Repeat("a", 52),
			version:   "failure.v1",
			want:      1,
			identity:  911,
		},
		{
			name:      "scope mismatch",
			sessionID: "recurrence-scope-mismatch",
			scopeID:   "psc_" + strings.Repeat("b", 52),
			version:   "failure.v1",
			want:      0,
			identity:  912,
		},
		{
			name:      "fingerprint version mismatch",
			sessionID: "recurrence-version-mismatch",
			scopeID:   "psc_" + strings.Repeat("a", 52),
			version:   "failure.v2",
			want:      0,
			identity:  913,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, annotation := newRecurrenceFixture(t)
			ctx := context.Background()
			eventAt := fixture.now.Add(time.Minute)
			appended := appendRecurrenceEvent(
				t,
				fixture,
				test.sessionID,
				eventAt,
				test.identity,
			)
			occurrence := testIssueOccurrence(
				test.sessionID,
				"codex",
				fmt.Sprintf("00000000-0000-7000-8000-%012d", test.identity),
				fixture.fingerprint,
				fixture.issueID,
				"medium",
				"high",
				eventAt,
				model.ScopeResolved,
			)
			occurrence.FingerprintScopeID = test.scopeID
			occurrence.FingerprintVersion = test.version
			occurrence.Provenance.FingerprintVersion = test.version
			watermark := eventAt.UnixNano()
			target := dirtyTargetGeneration(t, fixture.store, test.sessionID)
			if _, err := fixture.store.ReplaceSessionProjection(
				ctx,
				SessionProjectionReplacement{
					SessionKey:              test.sessionID,
					ClaimedGeneration:       target,
					Status:                  model.AnalysisCurrent,
					ScopeQuality:            model.ScopeResolved,
					AnalysisThroughOrderNS:  &watermark,
					AnalyzedEventGeneration: appended.ReadGeneration,
					BelayOccurrences:        []model.IssueOccurrence{occurrence},
				},
			); err != nil {
				t.Fatal(err)
			}
			claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			result, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100)
			if err != nil {
				t.Fatal(err)
			}
			if result.Observed != test.want {
				t.Fatalf("observed = %d, want %d", result.Observed, test.want)
			}
			if test.want == 1 {
				var same int
				if err := fixture.store.db.QueryRowContext(ctx, `
					SELECT same_session_as_anchor
					FROM fix_recurrence_observations
					WHERE annotation_id = ?`,
					annotation.Annotation.AnnotationID,
				).Scan(&same); err != nil {
					t.Fatal(err)
				}
				if same != 0 {
					t.Fatalf("other-session observation marked same-session = %d", same)
				}
			}
		})
	}
}

func TestRecurrenceSameSessionObservationIsExplicit(t *testing.T) {
	fixture, annotation := newRecurrenceFixture(t)
	projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 918)
	claim, err := fixture.store.ClaimRecurrenceJob(
		context.Background(),
		*fixture.now,
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.store.ProcessRecurrenceBatch(
		context.Background(),
		claim,
		100,
	); err != nil || result.Observed != 1 {
		t.Fatalf("ProcessRecurrenceBatch() = %+v, %v", result, err)
	}
	var same int
	if err := fixture.store.db.QueryRowContext(context.Background(), `
		SELECT same_session_as_anchor
		FROM fix_recurrence_observations
		WHERE annotation_id = ?`,
		annotation.Annotation.AnnotationID,
	).Scan(&same); err != nil {
		t.Fatal(err)
	}
	if same != 1 {
		t.Fatalf("same-session observation marker = %d, want 1", same)
	}
}

func TestRetentionPreservesNonCompleteZeroOccurrenceJobRevisions(t *testing.T) {
	for _, state := range []string{"pending", "claimed", "failed"} {
		t.Run(state, func(t *testing.T) {
			fixture, _ := newRecurrenceFixture(t)
			ctx := context.Background()
			appended := appendRecurrenceEvent(
				t,
				fixture,
				fixture.sessionID,
				fixture.now.Add(time.Minute),
				920+len(state),
			)
			watermark := fixture.now.Add(time.Minute).UnixNano()
			commit, err := fixture.store.ReplaceSessionProjection(
				ctx,
				SessionProjectionReplacement{
					SessionKey: fixture.sessionID,
					ClaimedGeneration: dirtyTargetGeneration(
						t, fixture.store, fixture.sessionID,
					),
					Status:                  model.AnalysisCurrent,
					ScopeQuality:            model.ScopeResolved,
					AnalysisThroughOrderNS:  &watermark,
					AnalyzedEventGeneration: appended.ReadGeneration,
					Capabilities: []model.AnalysisCapability{{
						SessionID:              fixture.sessionID,
						FingerprintScopeID:     "psc_" + strings.Repeat("a", 52),
						Origin:                 "belay",
						DetectorID:             "explicit_command_failure",
						DetectorVersion:        "1.0.0",
						FingerprintVersion:     "failure.v1",
						NegativeComparisonMode: model.FixNegativeComparisonSupported,
						AnalysisThroughOrderNS: &watermark,
					}},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			var jobID, revisionID string
			if err := fixture.store.db.QueryRowContext(ctx, `
				SELECT job_id, revision_id
				FROM fix_recurrence_jobs
				WHERE projection_generation = ?`,
				commit.ProjectionGeneration,
			).Scan(&jobID, &revisionID); err != nil {
				t.Fatal(err)
			}
			if state != "pending" {
				claim, err := fixture.store.ClaimRecurrenceJob(
					ctx,
					*fixture.now,
					time.Hour,
				)
				if err != nil {
					t.Fatal(err)
				}
				if claim.JobID != jobID {
					t.Fatalf("claimed job = %q, want %q", claim.JobID, jobID)
				}
				if state == "failed" {
					if err := fixture.store.FailRecurrenceJob(
						ctx,
						claim,
						"injected_failure",
						fixture.now.Add(time.Hour),
					); err != nil {
						t.Fatal(err)
					}
					if err := fixture.store.Close(); err != nil {
						t.Fatal(err)
					}
					reopened, err := OpenWithOptions(fixture.path, OpenOptions{
						KeyProvider: fixture.provider,
						Clock:       func() time.Time { return *fixture.now },
					})
					if err != nil {
						t.Fatal(err)
					}
					fixture.store = reopened
					t.Cleanup(func() { _ = reopened.Close() })
				}
			}
			if _, err := fixture.store.Prune(
				ctx,
				RetentionPolicy{MaxAge: time.Hour},
				fixture.now.Add(4*time.Hour),
			); err != nil {
				t.Fatalf("Prune() with %s zero-occurrence job: %v", state, err)
			}
			var revisions, jobs int
			if err := fixture.store.db.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM session_analysis_revisions
				WHERE revision_id = ?`,
				revisionID,
			).Scan(&revisions); err != nil {
				t.Fatal(err)
			}
			if err := fixture.store.db.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM fix_recurrence_jobs
				WHERE job_id = ? AND state = ?`,
				jobID,
				state,
			).Scan(&jobs); err != nil {
				t.Fatal(err)
			}
			if revisions != 1 || jobs != 1 {
				t.Fatalf(
					"%s retained revision/job = %d/%d, want 1/1",
					state,
					revisions,
					jobs,
				)
			}
		})
	}
}

func TestRecurrenceRetentionPinsPendingWorkAndPreservesPositiveLedger(t *testing.T) {
	fixture, annotation := newRecurrenceFixture(t)
	ctx := context.Background()
	laterEventID := "00000000-0000-7000-8000-000000000907"
	projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 907)
	var retentionBefore int64
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT retention_generation
		FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&retentionBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		fixture.now.Add(3*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	var eventCount, pendingJobs int
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM events WHERE event_id = ?",
		laterEventID,
	).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM fix_recurrence_jobs
		WHERE state <> 'complete'`,
	).Scan(&pendingJobs); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 || pendingJobs != 1 {
		t.Fatalf("pending retention event/jobs = %d/%d, want 1/1",
			eventCount, pendingJobs)
	}
	claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.store.ProcessRecurrenceBatch(
		ctx,
		claim,
		100,
	); err != nil || result.Observed != 1 {
		t.Fatalf("ProcessRecurrenceBatch() = %+v, %v", result, err)
	}
	if _, err := fixture.store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		fixture.now.Add(5*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	var observations, citations int
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM fix_recurrence_observations
		WHERE annotation_id = ?`,
		annotation.Annotation.AnnotationID,
	).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM fix_recurrence_observation_events",
	).Scan(&citations); err != nil {
		t.Fatal(err)
	}
	var retentionAfter int64
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT retention_generation
		FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&retentionAfter); err != nil {
		t.Fatal(err)
	}
	if observations != 1 || citations != 0 || retentionAfter <= retentionBefore {
		t.Fatalf("retained observation/citations/generation = %d/%d/%d, before %d",
			observations, citations, retentionAfter, retentionBefore)
	}
	if ready, err := fixture.store.CompleteFixMonitoringCatchup(ctx); err != nil || !ready {
		t.Fatalf("CompleteFixMonitoringCatchup() after retention = %v, %v", ready, err)
	}
	history, err := fixture.store.QueryIssueFixMonitoring(
		ctx,
		model.FixMonitoringDetailQuery{IssueID: fixture.issueID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if history.CurrentIssue != nil || len(history.Data) != 1 ||
		history.Data[0].FixRecurrenceState != model.FixRecurrenceMatchingEvidence ||
		history.Data[0].RecurrenceEvidence.Pruned != 1 {
		t.Fatalf("history-only monitoring = %+v", history)
	}
}

func TestCompletedRecurrenceJobRetentionCascadesItsEventLedger(t *testing.T) {
	fixture, _ := newRecurrenceFixture(t)
	ctx := context.Background()
	commit := projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 919)
	claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT job_id FROM fix_recurrence_jobs
		WHERE projection_generation = ?`,
		commit.ProjectionGeneration,
	).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		fixture.now.Add(5*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	var retentionBefore int64
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT retention_generation
		FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&retentionBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.ExecContext(ctx, `
		CREATE TEMP TRIGGER fail_completed_job_retention_generation
		BEFORE UPDATE OF retention_generation ON issue_projection_metadata
		BEGIN
			SELECT RAISE(ABORT, 'forced retention generation failure');
		END`); err != nil {
		t.Fatal(err)
	}
	// The first prune closes the active revision at its own transaction time.
	// A later maintenance pass may remove that now-closed revision and its
	// completed recurrence job after the protection floor has elapsed.
	if _, err := fixture.store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		fixture.now.Add(7*time.Hour),
	); err == nil {
		t.Fatal("completed-job prune succeeded despite generation failure")
	}
	var rolledBackJobs, rolledBackEvents int
	var retentionAfterFailure int64
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM fix_recurrence_jobs WHERE job_id = ?",
		jobID,
	).Scan(&rolledBackJobs); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM fix_recurrence_job_events WHERE job_id = ?",
		jobID,
	).Scan(&rolledBackEvents); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT retention_generation
		FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&retentionAfterFailure); err != nil {
		t.Fatal(err)
	}
	if rolledBackJobs != 1 || rolledBackEvents == 0 ||
		retentionAfterFailure != retentionBefore {
		t.Fatalf(
			"failed prune jobs/events/generation = %d/%d/%d, want 1/>0/%d",
			rolledBackJobs,
			rolledBackEvents,
			retentionAfterFailure,
			retentionBefore,
		)
	}
	if _, err := fixture.store.db.ExecContext(ctx,
		"DROP TRIGGER fail_completed_job_retention_generation",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		fixture.now.Add(7*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	var jobs, events, observations, sessionEvents int
	var jobState, revisionUpdated string
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM fix_recurrence_jobs WHERE job_id = ?",
		jobID,
	).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM fix_recurrence_job_events WHERE job_id = ?",
		jobID,
	).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM fix_recurrence_observations",
	).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM events WHERE session_key = ?",
		fixture.sessionID,
	).Scan(&sessionEvents); err != nil {
		t.Fatal(err)
	}
	var retentionAfter int64
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT retention_generation
		FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&retentionAfter); err != nil {
		t.Fatal(err)
	}
	_ = fixture.store.db.QueryRowContext(ctx, `
		SELECT frj.state, sar.updated_at
		FROM fix_recurrence_jobs frj
		JOIN session_analysis_revisions sar ON sar.revision_id = frj.revision_id
		WHERE frj.job_id = ?`,
		jobID,
	).Scan(&jobState, &revisionUpdated)
	if jobs != 0 || events != 0 || observations != 1 ||
		retentionAfter != retentionBefore+1 {
		t.Fatalf(
			"completed retention jobs/events/observations/session-events/generation state/updated = %d/%d/%d/%d/%d %q/%q, want 0/0/1/0/%d",
			jobs,
			events,
			observations,
			sessionEvents,
			retentionAfter,
			jobState,
			revisionUpdated,
			retentionBefore+1,
		)
	}
}

func TestRecurrenceCatchupRecoversClosedHistoricalMatchBeforeCurrentNoMatch(t *testing.T) {
	fixture, annotation := newRecurrenceFixture(t)
	ctx := context.Background()
	projectRecurrenceOccurrence(t, fixture, fixture.now.Add(time.Minute), 914)

	// Simulate a pre-011 store by removing feature-owned jobs while retaining
	// immutable completed analysis revisions and occurrence citations.
	deleteRecurrenceJobsForCatchupTest(t, fixture.store)

	appended := appendRecurrenceEvent(
		t,
		fixture,
		fixture.sessionID,
		fixture.now.Add(2*time.Minute),
		915,
	)
	watermark := fixture.now.Add(2 * time.Minute).UnixNano()
	if _, err := fixture.store.ReplaceSessionProjection(
		ctx,
		SessionProjectionReplacement{
			SessionKey: fixture.sessionID,
			ClaimedGeneration: dirtyTargetGeneration(
				t, fixture.store, fixture.sessionID,
			),
			Status:                  model.AnalysisCurrent,
			ScopeQuality:            model.ScopeResolved,
			AnalysisThroughOrderNS:  &watermark,
			AnalyzedEventGeneration: appended.ReadGeneration,
			Capabilities: []model.AnalysisCapability{{
				SessionID:              fixture.sessionID,
				FingerprintScopeID:     "psc_" + strings.Repeat("a", 52),
				Origin:                 "belay",
				DetectorID:             "explicit_command_failure",
				DetectorVersion:        "1.0.0",
				FingerprintVersion:     "failure.v1",
				NegativeComparisonMode: model.FixNegativeComparisonSupported,
				AnalysisThroughOrderNS: &watermark,
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	deleteRecurrenceJobsForCatchupTest(t, fixture.store)

	if err := fixture.store.PrepareFixMonitoringCatchup(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		claim, err := fixture.store.ClaimRecurrenceJob(ctx, *fixture.now, time.Minute)
		if errors.Is(err, ErrNoRecurrenceJob) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		for {
			batch, err := fixture.store.ProcessRecurrenceBatch(ctx, claim, 100)
			if err != nil {
				t.Fatal(err)
			}
			if batch.Completed {
				break
			}
		}
	}
	var count int
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM fix_recurrence_observations
		WHERE annotation_id = ?`,
		annotation.Annotation.AnnotationID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("historical catch-up observations = %d, want 1", count)
	}
}

func TestFixAnnotationPersistsUnavailableMonitoringSubjectWithoutGuessingScope(t *testing.T) {
	fixture := newFixTestFixture(
		t,
		1,
		model.ScopeResolved,
		"belay",
		"command_failure",
		false,
	)
	ctx := context.Background()
	tx, err := fixture.store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE issue_occurrences
			SET fingerprint_scope_id = NULL
			WHERE issue_id = ?`,
			fixture.issueID,
		)
		return err
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	annotation, err := fixture.store.RecordFixAnnotation(
		ctx,
		fixture.input("00000000-0000-4000-8000-000000060001"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	var scope sql.NullString
	var baseline sql.NullInt64
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT scope_capture_status, fingerprint_scope_id,
			monitor_from_order_ns
		FROM fix_monitoring_subjects
		WHERE annotation_id = ?`,
		annotation.Annotation.AnnotationID,
	).Scan(&status, &scope, &baseline); err != nil {
		t.Fatal(err)
	}
	if status != "unavailable" || scope.Valid || baseline.Valid {
		t.Fatalf("unavailable subject = %q/%v/%v", status, scope, baseline)
	}
}

func newRecurrenceFixture(
	t *testing.T,
) (fixTestFixture, FixAnnotationResult) {
	t.Helper()
	fixture := newFixTestFixture(
		t,
		1,
		model.ScopeResolved,
		"belay",
		"command_failure",
		false,
	)
	annotation, err := fixture.store.RecordFixAnnotation(
		context.Background(),
		fixture.input("00000000-0000-4000-8000-000000020001"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, annotation
}

func projectRecurrenceOccurrence(
	t *testing.T,
	fixture fixTestFixture,
	occurredAt time.Time,
	identity int,
) ProjectionCommitResult {
	t.Helper()
	eventID := fmt.Sprintf(
		"00000000-0000-7000-8000-%012d",
		identity,
	)
	event := storageTestEvent(
		eventID,
		fixture.sessionID,
		int64(identity),
		occurredAt,
	)
	event.Source.RecordID = fmt.Sprintf("recurrence-source-%d", identity)
	event.Source.DeduplicationKey = fmt.Sprintf("sha256:%064x", identity+20_000)
	appended, err := fixture.store.AppendEventResolved(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := testIssueOccurrence(
		fixture.sessionID,
		"codex",
		eventID,
		fixture.fingerprint,
		fixture.issueID,
		"medium",
		"high",
		occurredAt,
		model.ScopeResolved,
	)
	watermark := occurredAt.UTC().UnixNano()
	target := dirtyTargetGeneration(t, fixture.store, fixture.sessionID)
	result, err := fixture.store.ReplaceSessionProjection(
		context.Background(),
		SessionProjectionReplacement{
			SessionKey:              fixture.sessionID,
			ClaimedGeneration:       target,
			Status:                  model.AnalysisCurrent,
			ScopeQuality:            model.ScopeResolved,
			AnalysisThroughOrderNS:  &watermark,
			AnalyzedEventGeneration: appended.ReadGeneration,
			BelayOccurrences:        []model.IssueOccurrence{occurrence},
			Capabilities: []model.AnalysisCapability{{
				SessionID:              fixture.sessionID,
				FingerprintScopeID:     occurrence.FingerprintScopeID,
				Origin:                 "belay",
				DetectorID:             occurrence.Provenance.DetectorID,
				DetectorVersion:        occurrence.Provenance.DetectorVersion,
				FingerprintVersion:     occurrence.FingerprintVersion,
				NegativeComparisonMode: model.FixNegativeComparisonSupported,
				AnalysisThroughOrderNS: &watermark,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func appendRecurrenceEvent(
	t *testing.T,
	fixture fixTestFixture,
	sessionID string,
	occurredAt time.Time,
	identity int,
) AppendEventResult {
	t.Helper()
	eventID := fmt.Sprintf(
		"00000000-0000-7000-8000-%012d",
		identity,
	)
	event := storageTestEvent(
		eventID,
		sessionID,
		int64(identity),
		occurredAt,
	)
	event.Source.RecordID = fmt.Sprintf("recurrence-source-%d", identity)
	event.Source.DeduplicationKey = fmt.Sprintf("sha256:%064x", identity+20_000)
	appended, err := fixture.store.AppendEventResolved(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	return appended
}

func dirtyTargetGeneration(t *testing.T, store *Store, sessionID string) int64 {
	t.Helper()
	var target int64
	if err := store.db.QueryRowContext(context.Background(), `
		SELECT target_generation FROM dirty_sessions WHERE session_key = ?`,
		sessionID,
	).Scan(&target); err != nil {
		t.Fatal(err)
	}
	return target
}

func deleteRecurrenceJobsForCatchupTest(t *testing.T, store *Store) {
	t.Helper()
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(
		context.Background(),
		tx,
		mutationRetentionPrune,
		func() error {
			_, err := tx.ExecContext(
				context.Background(),
				"DELETE FROM fix_recurrence_jobs",
			)
			return err
		},
	); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
