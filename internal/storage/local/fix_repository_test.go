package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

type fixTestFixture struct {
	store       *Store
	path        string
	provider    *memoryKeyProvider
	now         *time.Time
	issueID     string
	fingerprint string
	sessionID   string
	eventIDs    []string
	snapshot    int64
	cursorEpoch string
	retention   int64
}

func TestFixAnnotationCreateReplayConflictAndProjectionIsolation(t *testing.T) {
	ctx := context.Background()
	fixture := newFixTestFixture(t, 2, model.ScopeResolved, "belay", "command_failure", false)
	claims := fixture.claims()
	input := FixAnnotationInput{
		Claims:         claims,
		ChangeKind:     model.FixChangeCode,
		RecordedVia:    model.FixRecordedViaLocalUI,
		IdempotencyKey: "00000000-0000-4000-8000-000000000001",
	}
	var generation, dirtyCount int64
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT current_generation FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dirty_sessions",
	).Scan(&dirtyCount); err != nil {
		t.Fatal(err)
	}

	created, err := fixture.store.RecordFixAnnotation(ctx, input)
	if err != nil {
		t.Fatalf("RecordFixAnnotation() error = %v", err)
	}
	if created.Replayed ||
		created.Annotation.IssueID != fixture.issueID ||
		created.Annotation.AnchorSessionID != fixture.sessionID ||
		created.Annotation.FingerprintID != fixture.fingerprint ||
		created.Annotation.IssueSnapshotGeneration != fixture.snapshot ||
		created.Annotation.AnchorAnalysisGeneration <= 0 ||
		created.Annotation.EvidenceCurrentlyRetained != model.FixEvidenceAvailable ||
		created.Annotation.State != model.FixStateActive ||
		!created.Annotation.RecordedAt.Equal(*fixture.now) ||
		!created.Annotation.MonitorFrom.Equal(*fixture.now) {
		t.Fatalf("created annotation = %+v", created)
	}
	var citationCount int
	var recordedAt, monitorFrom string
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT baseline_citation_count, recorded_at, monitor_from
		FROM fix_annotations WHERE annotation_id = ?`,
		created.Annotation.AnnotationID,
	).Scan(&citationCount, &recordedAt, &monitorFrom); err != nil {
		t.Fatal(err)
	}
	if citationCount != 2 ||
		len(recordedAt) != len(projectionTimestampLayout) ||
		recordedAt != monitorFrom {
		t.Fatalf(
			"stored citation/timestamps = %d/%q/%q",
			citationCount,
			recordedAt,
			monitorFrom,
		)
	}

	*fixture.now = fixture.now.Add(issueCursorLifetime + time.Minute)
	replay, err := fixture.store.RecordFixAnnotation(ctx, input)
	if err != nil {
		t.Fatalf("expired replay error = %v", err)
	}
	if !replay.Replayed || replay.Annotation.AnnotationID != created.Annotation.AnnotationID {
		t.Fatalf("expired replay = %+v", replay)
	}
	conflicting := input
	conflicting.ChangeKind = model.FixChangeDependency
	if _, err := fixture.store.RecordFixAnnotation(ctx, conflicting); !errors.Is(
		err,
		ErrFixIdempotencyConflict,
	) {
		t.Fatalf("conflicting replay error = %v", err)
	}
	_, otherIssueID, err := fixture.store.DeriveIssueIdentity(
		"failure.v1",
		"explicit_command_failure",
		"different-fix-scope",
		"failure",
	)
	if err != nil {
		t.Fatal(err)
	}
	mismatchedIssue := input
	mismatchedIssue.Claims.IssueID = otherIssueID
	if _, err := fixture.store.RecordFixAnnotation(ctx, mismatchedIssue); !errors.Is(
		err,
		ErrFixIdempotencyConflict,
	) {
		t.Fatalf("issue-mismatched replay error = %v", err)
	}
	expiredNew := input
	expiredNew.IdempotencyKey = "00000000-0000-4000-8000-000000000002"
	if _, err := fixture.store.RecordFixAnnotation(ctx, expiredNew); !errors.Is(
		err,
		model.ErrIssueSnapshotExpired,
	) {
		t.Fatalf("new expired write error = %v", err)
	}

	var afterGeneration, afterDirtyCount int64
	if err := fixture.store.db.QueryRowContext(ctx, `
		SELECT current_generation FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&afterGeneration); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dirty_sessions",
	).Scan(&afterDirtyCount); err != nil {
		t.Fatal(err)
	}
	if afterGeneration != generation || afterDirtyCount != dirtyCount {
		t.Fatalf(
			"fix write changed projection state generation/dirty = %d/%d, want %d/%d",
			afterGeneration,
			afterDirtyCount,
			generation,
			dirtyCount,
		)
	}
}

func TestFixActionEpochAndRetentionAreCheckedBeforeReplay(t *testing.T) {
	ctx := context.Background()
	fixture := newFixTestFixture(
		t, 1, model.ScopeResolved, "belay", "command_failure", false,
	)
	input := fixture.input("00000000-0000-4000-8000-000000000099")
	if _, err := fixture.store.RecordFixAnnotation(ctx, input); err != nil {
		t.Fatal(err)
	}

	staleEpoch := input
	staleEpoch.Claims.CursorEpoch =
		"ice_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := fixture.store.RecordFixAnnotation(ctx, staleEpoch); !errors.Is(
		err,
		model.ErrIssueSnapshotExpired,
	) {
		t.Fatalf("stale epoch replay error = %v", err)
	}

	staleRetention := input
	staleRetention.Claims.RetentionGeneration++
	if _, err := fixture.store.RecordFixAnnotation(ctx, staleRetention); !errors.Is(
		err,
		model.ErrIssueSnapshotExpired,
	) {
		t.Fatalf("stale retention replay error = %v", err)
	}
}

func TestFixAnnotationExpiryBoundary(t *testing.T) {
	ctx := context.Background()

	atBoundary := newFixTestFixture(
		t,
		1,
		model.ScopeResolved,
		"belay",
		"command_failure",
		false,
	)
	boundaryInput := atBoundary.input("00000000-0000-4000-8000-000000000011")
	*atBoundary.now = boundaryInput.Claims.ExpiresAt
	if _, err := atBoundary.store.RecordFixAnnotation(ctx, boundaryInput); err != nil {
		t.Fatalf("write at exact expiry boundary error = %v", err)
	}

	afterBoundary := newFixTestFixture(
		t,
		1,
		model.ScopeResolved,
		"belay",
		"command_failure",
		false,
	)
	expiredInput := afterBoundary.input("00000000-0000-4000-8000-000000000012")
	*afterBoundary.now = expiredInput.Claims.ExpiresAt.Add(time.Nanosecond)
	if _, err := afterBoundary.store.RecordFixAnnotation(
		ctx,
		expiredInput,
	); !errors.Is(err, model.ErrIssueSnapshotExpired) {
		t.Fatalf("write after expiry boundary error = %v", err)
	}
}

func TestFixEligibilityAggregateFirstAndLatestAnchor(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "aggregate.sqlite")
	store, err := OpenWithOptions(path, OpenOptions{
		KeyProvider: newMemoryKeyProvider(),
		Clock:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"failure.v1",
		"explicit_command_failure",
		"shared-resolved-scope",
		"failure",
	)
	if err != nil {
		t.Fatal(err)
	}
	type sessionProjection struct {
		session string
		at      time.Time
		status  model.AnalysisStatus
		eventID string
	}
	projections := []sessionProjection{
		{
			session: "session-aggregate-old-truncated",
			at:      now.Add(-2 * time.Hour),
			status:  model.AnalysisTruncated,
			eventID: "00000000-0000-7000-8000-000000000301",
		},
		{
			session: "session-aggregate-latest-current",
			at:      now.Add(-time.Hour),
			status:  model.AnalysisCurrent,
			eventID: "00000000-0000-7000-8000-000000000302",
		},
	}
	for _, projection := range projections {
		event := storageTestEvent(projection.eventID, projection.session, 1, projection.at)
		if _, err := store.AppendEventResolved(ctx, event); err != nil {
			t.Fatal(err)
		}
		occurrence := testIssueOccurrence(
			projection.session,
			"codex",
			event.EventID,
			fingerprint,
			issueID,
			"medium",
			"high",
			projection.at,
			model.ScopeResolved,
		)
		if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
			SessionKey:        projection.session,
			ClaimedGeneration: 1,
			Status:            projection.status,
			ScopeQuality:      model.ScopeResolved,
			Occurrences:       []model.IssueOccurrence{occurrence},
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{
			AttentionKind: model.AttentionKindAll,
			Experimental:  model.ExperimentalInclude,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	eligibility, err := store.EvaluateFixEligibility(ctx, model.FixEligibilityQuery{
		IssueID:             issueID,
		CursorEpoch:         mustIssueCursorEpoch(t, store),
		Snapshot:            page.Snapshot,
		RetentionGeneration: mustRetentionGeneration(t, store),
		IssuedAt:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if eligibility.Eligible ||
		eligibility.Reason != model.FixEligibilityAnalysisNotCurrent {
		t.Fatalf("aggregate eligibility = %+v", eligibility)
	}

	currentOnly := newFixTestFixture(
		t,
		1,
		model.ScopeLexical,
		"numbat",
		"upstream_finding",
		false,
	)
	eligible, err := currentOnly.store.EvaluateFixEligibility(
		ctx,
		model.FixEligibilityQuery{
			IssueID:             currentOnly.issueID,
			CursorEpoch:         currentOnly.cursorEpoch,
			Snapshot:            currentOnly.snapshot,
			RetentionGeneration: currentOnly.retention,
			IssuedAt:            *currentOnly.now,
		},
	)
	if err != nil || !eligible.Eligible {
		t.Fatalf("Numbat lexical eligibility = (%+v, %v)", eligible, err)
	}
}

func TestFixAnnotationSelectsLatestVisibleOccurrence(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 15, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "latest-anchor.sqlite")
	store, err := OpenWithOptions(path, OpenOptions{
		KeyProvider: newMemoryKeyProvider(),
		Clock:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"failure.v1",
		"explicit_command_failure",
		"latest-anchor-scope",
		"failure",
	)
	if err != nil {
		t.Fatal(err)
	}
	type occurrenceFixture struct {
		sessionID string
		eventID   string
		observed  time.Time
	}
	fixtures := []occurrenceFixture{
		{
			sessionID: "session-anchor-older",
			eventID:   "00000000-0000-7000-8000-000000000321",
			observed:  now.Add(-2 * time.Hour),
		},
		{
			sessionID: "session-anchor-latest",
			eventID:   "00000000-0000-7000-8000-000000000322",
			observed:  now.Add(-time.Hour),
		},
	}
	var snapshot int64
	for _, item := range fixtures {
		event := storageTestEvent(item.eventID, item.sessionID, 1, item.observed)
		event.Source.RecordID = item.sessionID
		event.Source.DeduplicationKey = fmt.Sprintf(
			"sha256:%064x",
			len(item.sessionID)+int(item.observed.Unix()),
		)
		if _, err := store.AppendEventResolved(ctx, event); err != nil {
			t.Fatal(err)
		}
		occurrence := testIssueOccurrence(
			item.sessionID,
			"codex",
			item.eventID,
			fingerprint,
			issueID,
			"medium",
			"high",
			item.observed,
			model.ScopeResolved,
		)
		commit, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
			SessionKey:        item.sessionID,
			ClaimedGeneration: 1,
			Status:            model.AnalysisCurrent,
			ScopeQuality:      model.ScopeResolved,
			Occurrences:       []model.IssueOccurrence{occurrence},
		})
		if err != nil {
			t.Fatal(err)
		}
		snapshot = commit.ProjectionGeneration
	}
	result, err := store.RecordFixAnnotation(ctx, FixAnnotationInput{
		Claims: model.FixActionClaims{
			Version:             model.FixActionTokenVersion,
			CursorEpoch:         mustIssueCursorEpoch(t, store),
			IssueID:             issueID,
			Snapshot:            snapshot,
			RetentionGeneration: mustRetentionGeneration(t, store),
			IssuedAt:            now,
			ExpiresAt:           now.Add(issueCursorLifetime),
		},
		ChangeKind:     model.FixChangeCode,
		RecordedVia:    model.FixRecordedViaLocalUI,
		IdempotencyKey: "00000000-0000-4000-8000-000000000013",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Annotation.AnchorSessionID != "session-anchor-latest" ||
		!result.Annotation.AnchorLastObservedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("latest anchor = %+v", result.Annotation)
	}
}

func TestFixEligibilityRejectsUnsupportedIssueStates(t *testing.T) {
	tests := []struct {
		name         string
		scope        model.ScopeQuality
		category     string
		experimental bool
		status       model.AnalysisStatus
		wantReason   string
	}{
		{
			name:       "unscoped",
			scope:      model.ScopeUnscoped,
			category:   "command_failure",
			status:     model.AnalysisCurrent,
			wantReason: model.FixEligibilityScopeUnavailable,
		},
		{
			name:       "conflict",
			scope:      model.ScopeConflict,
			category:   "command_failure",
			status:     model.AnalysisCurrent,
			wantReason: model.FixEligibilityScopeUnavailable,
		},
		{
			name:         "experimental",
			scope:        model.ScopeResolved,
			category:     "attention",
			experimental: true,
			status:       model.AnalysisCurrent,
			wantReason:   model.FixEligibilityExperimentalSignal,
		},
		{
			name:       "evidence-gap",
			scope:      model.ScopeResolved,
			category:   "evidence_gap",
			status:     model.AnalysisCurrent,
			wantReason: model.FixEligibilityEvidenceGap,
		},
		{
			name:       "truncated",
			scope:      model.ScopeResolved,
			category:   "command_failure",
			status:     model.AnalysisTruncated,
			wantReason: model.FixEligibilityAnalysisNotCurrent,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixTestFixtureWithStatus(
				t,
				1,
				test.scope,
				"belay",
				test.category,
				test.experimental,
				test.status,
			)
			got, err := fixture.store.EvaluateFixEligibility(
				context.Background(),
				model.FixEligibilityQuery{
					IssueID:             fixture.issueID,
					CursorEpoch:         fixture.cursorEpoch,
					Snapshot:            fixture.snapshot,
					RetentionGeneration: fixture.retention,
					IssuedAt:            *fixture.now,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.Eligible || got.Reason != test.wantReason {
				t.Fatalf("eligibility = %+v, want reason %q", got, test.wantReason)
			}
			if _, err := fixture.store.RecordFixAnnotation(
				context.Background(),
				fixture.input("00000000-0000-4000-8000-000000000101"),
			); !errors.Is(err, ErrFixIneligible) {
				t.Fatalf("RecordFixAnnotation() error = %v", err)
			}
		})
	}
}

func TestFixEligibilityReturnsNotFoundWhenNoOccurrenceIsVisible(t *testing.T) {
	fixture := newFixTestFixture(
		t,
		1,
		model.ScopeResolved,
		"belay",
		"command_failure",
		false,
	)
	_, absentIssueID, err := fixture.store.DeriveIssueIdentity(
		"failure.v1",
		"explicit_command_failure",
		"absent-fix-eligibility-scope",
		"failure",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.EvaluateFixEligibility(
		context.Background(),
		model.FixEligibilityQuery{
			IssueID:             absentIssueID,
			CursorEpoch:         fixture.cursorEpoch,
			Snapshot:            fixture.snapshot,
			RetentionGeneration: fixture.retention,
			IssuedAt:            *fixture.now,
		},
	)
	if !errors.Is(err, ErrFixIssueNotFound) {
		t.Fatalf("EvaluateFixEligibility() error = %v, want ErrFixIssueNotFound", err)
	}
}

func TestConcurrentFixAnnotationCreationAndConflict(t *testing.T) {
	t.Run("identical", func(t *testing.T) {
		fixture := newFixTestFixture(t, 1, model.ScopeResolved, "belay", "command_failure", false)
		second, err := OpenWithOptions(fixture.path, OpenOptions{
			KeyProvider: fixture.provider,
			Clock:       func() time.Time { return *fixture.now },
		})
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		input := fixture.input("00000000-0000-4000-8000-000000000201")
		results := make(chan FixAnnotationResult, 2)
		errs := make(chan error, 2)
		var wait sync.WaitGroup
		for _, store := range []*Store{fixture.store, second} {
			wait.Add(1)
			go func() {
				defer wait.Done()
				result, err := store.RecordFixAnnotation(context.Background(), input)
				results <- result
				errs <- err
			}()
		}
		wait.Wait()
		close(results)
		close(errs)
		created, replayed := 0, 0
		for err := range errs {
			if err != nil {
				t.Errorf("concurrent identical write error = %v", err)
			}
		}
		for result := range results {
			if result.Replayed {
				replayed++
			} else {
				created++
			}
		}
		if created != 1 || replayed != 1 {
			t.Fatalf("concurrent create/replay = %d/%d, want 1/1", created, replayed)
		}
		if count, err := fixture.store.Count(context.Background(), "fix_annotations"); err != nil ||
			count != 1 {
			t.Fatalf("annotation count/error = %d/%v", count, err)
		}
	})

	t.Run("conflicting", func(t *testing.T) {
		fixture := newFixTestFixture(t, 1, model.ScopeResolved, "belay", "command_failure", false)
		second, err := OpenWithOptions(fixture.path, OpenOptions{
			KeyProvider: fixture.provider,
			Clock:       func() time.Time { return *fixture.now },
		})
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		firstInput := fixture.input("00000000-0000-4000-8000-000000000202")
		secondInput := firstInput
		secondInput.ChangeKind = model.FixChangeProjectRule
		errs := make(chan error, 2)
		var wait sync.WaitGroup
		for index, store := range []*Store{fixture.store, second} {
			input := firstInput
			if index == 1 {
				input = secondInput
			}
			wait.Add(1)
			go func() {
				defer wait.Done()
				_, err := store.RecordFixAnnotation(context.Background(), input)
				errs <- err
			}()
		}
		wait.Wait()
		close(errs)
		successes, conflicts := 0, 0
		for err := range errs {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrFixIdempotencyConflict):
				conflicts++
			default:
				t.Errorf("unexpected concurrent conflict error = %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("concurrent success/conflict = %d/%d, want 1/1", successes, conflicts)
		}
	})
}

func TestFixRetractionReplayConflictAndConcurrentAlreadyRetracted(t *testing.T) {
	ctx := context.Background()
	fixture := newFixTestFixture(t, 1, model.ScopeResolved, "belay", "command_failure", false)
	created, err := fixture.store.RecordFixAnnotation(
		ctx,
		fixture.input("00000000-0000-4000-8000-000000000401"),
	)
	if err != nil {
		t.Fatal(err)
	}
	input := FixRetractionInput{
		IssueID:        fixture.issueID,
		AnnotationID:   created.Annotation.AnnotationID,
		Reason:         model.FixRetractionRecordedByMistake,
		RecordedVia:    model.FixRecordedViaLocalUI,
		IdempotencyKey: "00000000-0000-4000-8000-000000000402",
	}
	first, err := fixture.store.RetractFixAnnotation(ctx, input)
	if err != nil || first.Replayed {
		t.Fatalf("first retraction = (%+v, %v)", first, err)
	}
	replay, err := fixture.store.RetractFixAnnotation(ctx, input)
	if err != nil || !replay.Replayed ||
		replay.Retraction.RetractionID != first.Retraction.RetractionID {
		t.Fatalf("retraction replay = (%+v, %v)", replay, err)
	}
	conflicting := input
	conflicting.Reason = model.FixRetractionSuperseded
	if _, err := fixture.store.RetractFixAnnotation(ctx, conflicting); !errors.Is(
		err,
		ErrFixIdempotencyConflict,
	) {
		t.Fatalf("retraction conflict error = %v", err)
	}
	another := input
	another.IdempotencyKey = "00000000-0000-4000-8000-000000000403"
	if _, err := fixture.store.RetractFixAnnotation(ctx, another); !errors.Is(
		err,
		ErrFixAlreadyRetracted,
	) {
		t.Fatalf("second retraction error = %v", err)
	}
	page, err := fixture.store.QueryFixAnnotations(ctx, model.FixAnnotationQuery{
		IssueID: fixture.issueID,
	})
	if err != nil || len(page.Data) != 1 ||
		page.Data[0].State != model.FixStateRetracted ||
		page.Data[0].RetractionReason != string(model.FixRetractionRecordedByMistake) {
		t.Fatalf("retracted history = (%+v, %v)", page, err)
	}
}

func TestConcurrentFixRetractions(t *testing.T) {
	tests := []struct {
		name       string
		firstKey   string
		secondKey  string
		first      model.FixRetractionReason
		second     model.FixRetractionReason
		wantReplay int
		wantError  error
	}{
		{
			name:       "identical request replays",
			firstKey:   "00000000-0000-4000-8000-000000000411",
			secondKey:  "00000000-0000-4000-8000-000000000411",
			first:      model.FixRetractionRecordedByMistake,
			second:     model.FixRetractionRecordedByMistake,
			wantReplay: 1,
		},
		{
			name:      "same key changed request conflicts",
			firstKey:  "00000000-0000-4000-8000-000000000412",
			secondKey: "00000000-0000-4000-8000-000000000412",
			first:     model.FixRetractionRecordedByMistake,
			second:    model.FixRetractionSuperseded,
			wantError: ErrFixIdempotencyConflict,
		},
		{
			name:      "different request cannot retract twice",
			firstKey:  "00000000-0000-4000-8000-000000000413",
			secondKey: "00000000-0000-4000-8000-000000000414",
			first:     model.FixRetractionRecordedByMistake,
			second:    model.FixRetractionSuperseded,
			wantError: ErrFixAlreadyRetracted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixTestFixture(
				t,
				1,
				model.ScopeResolved,
				"belay",
				"command_failure",
				false,
			)
			created, err := fixture.store.RecordFixAnnotation(
				context.Background(),
				fixture.input("00000000-0000-4000-8000-000000000410"),
			)
			if err != nil {
				t.Fatal(err)
			}
			secondStore, err := OpenWithOptions(fixture.path, OpenOptions{
				KeyProvider: fixture.provider,
				Clock:       func() time.Time { return *fixture.now },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer secondStore.Close()
			inputs := []FixRetractionInput{
				{
					IssueID:        fixture.issueID,
					AnnotationID:   created.Annotation.AnnotationID,
					Reason:         test.first,
					RecordedVia:    model.FixRecordedViaLocalUI,
					IdempotencyKey: test.firstKey,
				},
				{
					IssueID:        fixture.issueID,
					AnnotationID:   created.Annotation.AnnotationID,
					Reason:         test.second,
					RecordedVia:    model.FixRecordedViaLocalUI,
					IdempotencyKey: test.secondKey,
				},
			}
			results := make(chan FixRetractionResult, 2)
			errs := make(chan error, 2)
			var wait sync.WaitGroup
			for index, store := range []*Store{fixture.store, secondStore} {
				input := inputs[index]
				wait.Add(1)
				go func() {
					defer wait.Done()
					result, err := store.RetractFixAnnotation(context.Background(), input)
					results <- result
					errs <- err
				}()
			}
			wait.Wait()
			close(results)
			close(errs)
			successes, replayed, expectedErrors := 0, 0, 0
			for err := range errs {
				switch {
				case err == nil:
					successes++
				case test.wantError != nil && errors.Is(err, test.wantError):
					expectedErrors++
				default:
					t.Errorf("unexpected concurrent retraction error = %v", err)
				}
			}
			for result := range results {
				if result.Replayed {
					replayed++
				}
			}
			if test.wantError == nil {
				if successes != 2 || replayed != test.wantReplay {
					t.Fatalf(
						"success/replay = %d/%d, want 2/%d",
						successes,
						replayed,
						test.wantReplay,
					)
				}
			} else if successes != 1 || expectedErrors != 1 {
				t.Fatalf(
					"success/expected-error = %d/%d, want 1/1",
					successes,
					expectedErrors,
				)
			}
			if count, err := fixture.store.Count(
				context.Background(),
				"fix_annotation_retractions",
			); err != nil || count != 1 {
				t.Fatalf("retraction count/error = %d/%v", count, err)
			}
		})
	}
}

func TestFixHistoryDualSnapshotsAndEvidenceStates(t *testing.T) {
	ctx := context.Background()
	fixture := newFixTestFixture(t, 2, model.ScopeResolved, "belay", "command_failure", false)
	inputs := []FixAnnotationInput{
		fixture.input("00000000-0000-4000-8000-000000000501"),
		fixture.input("00000000-0000-4000-8000-000000000502"),
		fixture.input("00000000-0000-4000-8000-000000000503"),
	}
	var created []model.FixAnnotation
	for index, input := range inputs {
		*fixture.now = fixture.now.Add(time.Minute)
		result, err := fixture.store.RecordFixAnnotation(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, result.Annotation)
		if index == 1 {
			inputs[index].ChangeKind = model.FixChangeDependency
		}
	}
	firstPage, err := fixture.store.QueryFixAnnotations(ctx, model.FixAnnotationQuery{
		IssueID: fixture.issueID,
		Limit:   1,
	})
	if err != nil || len(firstPage.Data) != 1 || !firstPage.HasMore {
		t.Fatalf("first history page = (%+v, %v)", firstPage, err)
	}
	if _, err := fixture.store.QueryFixAnnotations(ctx, model.FixAnnotationQuery{
		IssueID:            fixture.issueID,
		Limit:              1,
		AnnotationSnapshot: firstPage.AnnotationSnapshot,
		RetractionSnapshot: firstPage.RetractionSnapshot,
		Cursor: &model.FixAnnotationPosition{
			IssueID:      "iss_" + strings.Repeat("a", 52),
			RecordedAt:   firstPage.Data[0].RecordedAt,
			AnnotationID: firstPage.Data[0].AnnotationID,
		},
	}); !errors.Is(err, ErrFixHistorySnapshotInvalid) {
		t.Fatalf("cross-issue history cursor error = %v", err)
	}
	*fixture.now = fixture.now.Add(time.Minute)
	if _, err := fixture.store.RecordFixAnnotation(
		ctx,
		fixture.input("00000000-0000-4000-8000-000000000504"),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RetractFixAnnotation(ctx, FixRetractionInput{
		IssueID:        fixture.issueID,
		AnnotationID:   created[1].AnnotationID,
		Reason:         model.FixRetractionSuperseded,
		RecordedVia:    model.FixRecordedViaLocalUI,
		IdempotencyKey: "00000000-0000-4000-8000-000000000505",
	}); err != nil {
		t.Fatal(err)
	}
	nextPage, err := fixture.store.QueryFixAnnotations(ctx, model.FixAnnotationQuery{
		IssueID:            fixture.issueID,
		Limit:              1,
		AnnotationSnapshot: firstPage.AnnotationSnapshot,
		RetractionSnapshot: firstPage.RetractionSnapshot,
		Cursor: &model.FixAnnotationPosition{
			IssueID:      fixture.issueID,
			RecordedAt:   firstPage.Data[0].RecordedAt,
			AnnotationID: firstPage.Data[0].AnnotationID,
		},
	})
	if err != nil || len(nextPage.Data) != 1 ||
		nextPage.Data[0].AnnotationID != created[1].AnnotationID ||
		nextPage.Data[0].State != model.FixStateActive {
		t.Fatalf("snapshot-stable next page = (%+v, %v)", nextPage, err)
	}
	fresh, err := fixture.store.QueryFixAnnotations(ctx, model.FixAnnotationQuery{
		IssueID: fixture.issueID,
		Limit:   10,
	})
	if err != nil || len(fresh.Data) != 4 {
		t.Fatalf("fresh history = (%+v, %v)", fresh, err)
	}
	foundRetracted := false
	for _, annotation := range fresh.Data {
		if annotation.AnnotationID == created[1].AnnotationID {
			foundRetracted = annotation.State == model.FixStateRetracted
		}
	}
	if !foundRetracted {
		t.Fatalf("fresh history omitted retraction: %+v", fresh.Data)
	}

	if err := deleteRetainedEventForFixTest(ctx, fixture.store, fixture.eventIDs[0]); err != nil {
		t.Fatal(err)
	}
	partial, err := fixture.store.QueryFixAnnotations(ctx, model.FixAnnotationQuery{
		IssueID: fixture.issueID,
		Limit:   10,
	})
	if err != nil || partial.Data[0].EvidenceCurrentlyRetained != model.FixEvidencePartial {
		t.Fatalf("partial evidence history = (%+v, %v)", partial, err)
	}
	if err := deleteRetainedEventForFixTest(ctx, fixture.store, fixture.eventIDs[1]); err != nil {
		t.Fatal(err)
	}
	pruned, err := fixture.store.QueryFixAnnotations(ctx, model.FixAnnotationQuery{
		IssueID: fixture.issueID,
		Limit:   10,
	})
	if err != nil || pruned.Data[0].EvidenceCurrentlyRetained != model.FixEvidencePruned {
		t.Fatalf("pruned evidence history = (%+v, %v)", pruned, err)
	}

	unknownFixture := newFixTestFixture(t, 0, model.ScopeResolved, "belay", "command_failure", false)
	unknownCreated, err := unknownFixture.store.RecordFixAnnotation(
		ctx,
		unknownFixture.input("00000000-0000-4000-8000-000000000506"),
	)
	if err != nil ||
		unknownCreated.Annotation.EvidenceCurrentlyRetained != model.FixEvidenceUnknown {
		t.Fatalf("unknown evidence annotation = (%+v, %v)", unknownCreated, err)
	}
}

func TestFixAnnotationsSurviveOrdinaryRetentionWithoutRetainingEvidence(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	fixture := newFixTestFixtureAt(
		t,
		1,
		model.ScopeResolved,
		"belay",
		"command_failure",
		false,
		model.AnalysisCurrent,
		now,
		now.Add(-48*time.Hour),
	)
	created, err := fixture.store.RecordFixAnnotation(
		ctx,
		fixture.input("00000000-0000-4000-8000-000000000601"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RetractFixAnnotation(ctx, FixRetractionInput{
		IssueID:        fixture.issueID,
		AnnotationID:   created.Annotation.AnnotationID,
		Reason:         model.FixRetractionSuperseded,
		RecordedVia:    model.FixRecordedViaLocalUI,
		IdempotencyKey: "00000000-0000-4000-8000-000000000602",
	}); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		now.Add(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if result.PrunedEventCount == 0 {
		t.Fatalf("retention did not prune source evidence: %+v", result)
	}
	for _, table := range []string{"fix_annotations", "fix_annotation_retractions"} {
		if count, err := fixture.store.Count(ctx, table); err != nil || count != 1 {
			t.Fatalf("%s count/error after prune = %d/%v", table, count, err)
		}
	}
	if count, err := fixture.store.Count(ctx, "fix_annotation_events"); err != nil || count != 0 {
		t.Fatalf("fix citation count/error after prune = %d/%v", count, err)
	}
	page, err := fixture.store.QueryFixAnnotations(ctx, model.FixAnnotationQuery{
		IssueID: fixture.issueID,
	})
	if err != nil || len(page.Data) != 1 ||
		page.Data[0].State != model.FixStateRetracted ||
		page.Data[0].EvidenceCurrentlyRetained != model.FixEvidencePruned {
		t.Fatalf("retained fix history = (%+v, %v)", page, err)
	}
}

func TestFixAnnotationsAreRemovedByFullStoreReset(t *testing.T) {
	ctx := context.Background()
	fixture := newFixTestFixture(
		t,
		1,
		model.ScopeResolved,
		"belay",
		"command_failure",
		false,
	)
	if _, err := fixture.store.RecordFixAnnotation(
		ctx,
		fixture.input("00000000-0000-4000-8000-000000000611"),
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(fixture.path + suffix); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	reset, err := OpenWithOptions(fixture.path, OpenOptions{
		KeyProvider: newMemoryKeyProvider(),
		Clock:       func() time.Time { return *fixture.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reset.Close()
	if count, err := reset.Count(ctx, "fix_annotations"); err != nil || count != 0 {
		t.Fatalf("reset annotation count/error = %d/%v", count, err)
	}
}

func TestFixMutationGuardsAndPrivateIdempotencyMaterial(t *testing.T) {
	ctx := context.Background()
	fixture := newFixTestFixture(t, 1, model.ScopeResolved, "belay", "command_failure", false)
	key := "00000000-0000-4000-8000-000000000701"
	created, err := fixture.store.RecordFixAnnotation(ctx, fixture.input(key))
	if err != nil {
		t.Fatal(err)
	}
	retractionKey := "00000000-0000-4000-8000-000000000702"
	retraction, err := fixture.store.RetractFixAnnotation(ctx, FixRetractionInput{
		IssueID:        fixture.issueID,
		AnnotationID:   created.Annotation.AnnotationID,
		Reason:         model.FixRetractionRecordedByMistake,
		RecordedVia:    model.FixRecordedViaLocalUI,
		IdempotencyKey: retractionKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.store.db.SetMaxOpenConns(3)
	var connections []*sql.Conn
	for range 3 {
		connection, err := fixture.store.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
	}
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for index, connection := range connections {
		for _, statement := range []struct {
			query string
			id    string
		}{
			{"UPDATE fix_annotations SET change_kind = 'other' WHERE annotation_id = ?", created.Annotation.AnnotationID},
			{"DELETE FROM fix_annotations WHERE annotation_id = ?", created.Annotation.AnnotationID},
			{"INSERT OR REPLACE INTO fix_annotations SELECT * FROM fix_annotations WHERE annotation_id = ?", created.Annotation.AnnotationID},
			{"UPDATE fix_annotation_retractions SET reason = 'other' WHERE retraction_id = ?", retraction.Retraction.RetractionID},
			{"DELETE FROM fix_annotation_retractions WHERE retraction_id = ?", retraction.Retraction.RetractionID},
			{"INSERT OR REPLACE INTO fix_annotation_retractions SELECT * FROM fix_annotation_retractions WHERE retraction_id = ?", retraction.Retraction.RetractionID},
		} {
			if _, err := connection.ExecContext(ctx, statement.query, statement.id); err == nil {
				t.Fatalf("connection %d allowed append-only mutation %q", index, statement.query)
			}
		}
	}
	for _, connection := range connections {
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
	connections = nil
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	assertSQLiteFilesExclude(t, fixture.path, key, retractionKey)
}

func newFixTestFixture(
	t *testing.T,
	citationCount int,
	scope model.ScopeQuality,
	origin string,
	category string,
	experimental bool,
) fixTestFixture {
	return newFixTestFixtureWithStatus(
		t,
		citationCount,
		scope,
		origin,
		category,
		experimental,
		model.AnalysisCurrent,
	)
}

func newFixTestFixtureWithStatus(
	t *testing.T,
	citationCount int,
	scope model.ScopeQuality,
	origin string,
	category string,
	experimental bool,
	status model.AnalysisStatus,
) fixTestFixture {
	now := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	return newFixTestFixtureAt(
		t,
		citationCount,
		scope,
		origin,
		category,
		experimental,
		status,
		now,
		now.Add(-time.Hour),
	)
}

func newFixTestFixtureAt(
	t *testing.T,
	citationCount int,
	scope model.ScopeQuality,
	origin string,
	category string,
	experimental bool,
	status model.AnalysisStatus,
	now time.Time,
	occurredAt time.Time,
) fixTestFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fix.sqlite")
	provider := newMemoryKeyProvider()
	current := now
	store, err := OpenWithOptions(path, OpenOptions{
		KeyProvider: provider,
		Clock:       func() time.Time { return current },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	sessionID := "session-fix-" + fmt.Sprint(citationCount) + "-" + string(scope) + "-" + origin
	eventCount := citationCount
	if eventCount == 0 {
		eventCount = 1
	}
	eventIDs := make([]string, 0, citationCount)
	var targetGeneration int64
	for index := range eventCount {
		eventID := fmt.Sprintf(
			"00000000-0000-7000-8000-%012d",
			800+index+citationCount*10,
		)
		event := storageTestEvent(
			eventID,
			sessionID,
			int64(index+1),
			occurredAt.Add(time.Duration(index)*time.Second),
		)
		event.Source.RecordID = fmt.Sprintf("fix-source-%d-%d", citationCount, index)
		event.Source.DeduplicationKey = fmt.Sprintf(
			"sha256:%064x",
			9000+citationCount*100+index,
		)
		appended, err := store.AppendEventResolved(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		targetGeneration = appended.ReadGeneration
		if index < citationCount {
			eventIDs = append(eventIDs, eventID)
		}
	}
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"failure.v1",
		"explicit_command_failure",
		"project-scope-"+origin+"-"+string(scope),
		"failure",
	)
	if err != nil {
		t.Fatal(err)
	}
	occurrence := testIssueOccurrence(
		sessionID,
		"codex",
		"",
		fingerprint,
		issueID,
		"medium",
		"high",
		occurredAt,
		scope,
	)
	occurrence.Evidence.CitedEventIDs = eventIDs
	occurrence.Category = category
	occurrence.Experimental = experimental
	occurrence.Origin = origin
	if origin == "numbat" {
		occurrence.Provenance.DetectorID = "numbat_finding"
		occurrence.TitleCode = "numbat_finding"
	}
	commit, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        sessionID,
		Origin:            origin,
		ClaimedGeneration: targetGeneration,
		Status:            status,
		ScopeQuality:      scope,
		Occurrences:       []model.IssueOccurrence{occurrence},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixTestFixture{
		store:       store,
		path:        path,
		provider:    provider,
		now:         &current,
		issueID:     issueID,
		fingerprint: fingerprint,
		sessionID:   sessionID,
		eventIDs:    eventIDs,
		snapshot:    commit.ProjectionGeneration,
		cursorEpoch: mustIssueCursorEpoch(t, store),
		retention:   mustRetentionGeneration(t, store),
	}
}

func (fixture fixTestFixture) claims() model.FixActionClaims {
	return model.FixActionClaims{
		Version:             model.FixActionTokenVersion,
		CursorEpoch:         fixture.cursorEpoch,
		IssueID:             fixture.issueID,
		Snapshot:            fixture.snapshot,
		RetentionGeneration: fixture.retention,
		IssuedAt:            *fixture.now,
		ExpiresAt:           fixture.now.Add(issueCursorLifetime),
	}
}

func mustIssueCursorEpoch(t *testing.T, store *Store) string {
	t.Helper()
	epoch, err := store.currentIssueCursorEpoch()
	if err != nil {
		t.Fatal(err)
	}
	return epoch
}

func mustRetentionGeneration(t *testing.T, store *Store) int64 {
	t.Helper()
	var generation int64
	if err := store.db.QueryRow(`
		SELECT retention_generation
		FROM issue_projection_metadata WHERE singleton = 1`,
	).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

func (fixture fixTestFixture) input(key string) FixAnnotationInput {
	return FixAnnotationInput{
		Claims:         fixture.claims(),
		ChangeKind:     model.FixChangeCode,
		RecordedVia:    model.FixRecordedViaLocalUI,
		IdempotencyKey: key,
	}
}

func deleteRetainedEventForFixTest(ctx context.Context, store *Store, eventID string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationRetentionPrune, func() error {
		_, err := tx.ExecContext(ctx, "DELETE FROM events WHERE event_id = ?", eventID)
		return err
	}); err != nil {
		return err
	}
	return tx.Commit()
}
