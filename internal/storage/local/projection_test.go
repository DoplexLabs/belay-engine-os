package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestMigration005AndAppendEventResolved(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	for _, table := range []string{
		"session_scopes",
		"event_enrichments",
		"dirty_sessions",
		"session_analysis_revisions",
		"issue_occurrences",
		"issue_occurrence_events",
		"issue_projection_metadata",
		"analysis_diagnostics",
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sqlite_schema
			WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&count); err != nil {
			t.Fatalf("inspect migration table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("migration table %s count = %d", table, count)
		}
	}
	var migrations int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrations); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrations != 30 {
		t.Fatalf("migration count = %d, want 30", migrations)
	}

	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000101",
		"session-append-result",
		1,
		time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC),
	)
	first, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatalf("AppendEventResolved() error = %v", err)
	}
	if !first.Inserted || first.EventID != event.EventID || first.ReadGeneration < 1 {
		t.Fatalf("first append result = %+v", first)
	}
	replay := event
	replay.EventID = "00000000-0000-7000-8000-000000000102"
	second, err := store.AppendEventResolved(ctx, replay)
	if err != nil {
		t.Fatalf("AppendEventResolved() replay error = %v", err)
	}
	if second.Inserted ||
		second.EventID != first.EventID ||
		second.ReadGeneration != first.ReadGeneration {
		t.Fatalf("replay append result = %+v, want canonical %+v", second, first)
	}
	var target int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT target_generation FROM dirty_sessions WHERE session_key = ?`,
		event.Session.Key,
	).Scan(&target); err != nil {
		t.Fatalf("read dirty generation: %v", err)
	}
	if target != 1 {
		t.Fatalf("replay dirty target = %d, want 1", target)
	}
}

func TestScopeAndEnrichmentTransitionsAreDirtyAndPrivate(t *testing.T) {
	const commandCanary = "PRIVATE_COMMAND_CANARY_19cb"
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "belay.sqlite")
	store, err := Open(databasePath, newMemoryKeyProvider())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000110",
		"session-enrichment",
		1,
		time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatalf("append event: %v", err)
	}

	scope, err := store.DeriveProjectScope(t.TempDir())
	if err != nil {
		t.Fatalf("derive scope: %v", err)
	}
	scopeResult, err := store.UpsertSessionScope(ctx, event.Session.Key, scope)
	if err != nil {
		t.Fatalf("UpsertSessionScope() error = %v", err)
	}
	if !scopeResult.Changed || scopeResult.TargetGeneration != 2 {
		t.Fatalf("scope result = %+v", scopeResult)
	}
	unchanged, err := store.UpsertSessionScope(ctx, event.Session.Key, scope)
	if err != nil {
		t.Fatalf("repeat UpsertSessionScope() error = %v", err)
	}
	if unchanged.Changed || unchanged.TargetGeneration != 0 {
		t.Fatalf("unchanged scope result = %+v", unchanged)
	}
	conflictingScope, err := store.DeriveProjectScope(t.TempDir())
	if err != nil {
		t.Fatalf("derive conflicting scope: %v", err)
	}
	conflict, err := store.UpsertSessionScope(ctx, event.Session.Key, conflictingScope)
	if err != nil {
		t.Fatalf("conflicting UpsertSessionScope() error = %v", err)
	}
	if conflict.Scope.Quality != model.ScopeConflict ||
		conflict.Scope.ProjectScopeID != "" ||
		conflict.TargetGeneration != 3 {
		t.Fatalf("scope conflict = %+v", conflict)
	}

	signature, err := store.DeriveCommandSignature("tool --token=" + commandCanary)
	if err != nil {
		t.Fatalf("derive command signature: %v", err)
	}
	enrichment := model.EventEnrichment{
		CommandSignatureID: signature,
		CommandClass:       "test",
		PermissionClass:    "permission.shell",
		Version:            "enrichment.v1",
	}
	enriched, err := store.UpsertEventEnrichment(ctx, event.EventID, enrichment)
	if err != nil {
		t.Fatalf("UpsertEventEnrichment() error = %v", err)
	}
	if !enriched.Changed || enriched.TargetGeneration != 4 {
		t.Fatalf("enrichment result = %+v", enriched)
	}
	repeated, err := store.UpsertEventEnrichment(ctx, event.EventID, enrichment)
	if err != nil {
		t.Fatalf("repeat UpsertEventEnrichment() error = %v", err)
	}
	if repeated.Changed {
		t.Fatalf("repeat enrichment changed = %+v", repeated)
	}
	got, err := store.EventEnrichments(ctx, event.Session.Key)
	if err != nil {
		t.Fatalf("EventEnrichments() error = %v", err)
	}
	if got[event.EventID] != enrichment {
		t.Fatalf("enrichment round trip = %+v", got[event.EventID])
	}
	var payload []byte
	if err := store.db.QueryRowContext(ctx, `
		SELECT enrichment_payload FROM event_enrichments WHERE event_id = ?`,
		event.EventID,
	).Scan(&payload); err != nil {
		t.Fatalf("read enrichment payload: %v", err)
	}
	if bytes.Contains(payload, []byte(commandCanary)) {
		t.Fatal("encrypted enrichment payload contains raw command")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertSQLiteFilesExclude(t, databasePath, commandCanary)
}

func TestRevisionedIssueProjectionSnapshotAggregationAndFailure(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	sessionIDs := []string{"session-issue-a", "session-issue-b"}
	eventIDs := []string{
		"00000000-0000-7000-8000-000000000120",
		"00000000-0000-7000-8000-000000000121",
	}
	scope, err := store.DeriveProjectScope(t.TempDir())
	if err != nil {
		t.Fatalf("derive shared scope: %v", err)
	}
	var targets []int64
	for index, sessionID := range sessionIDs {
		event := storageTestEvent(eventIDs[index], sessionID, 1, base.Add(time.Duration(index)*time.Minute))
		event.Source.Agent = []string{"codex", "claude-code"}[index]
		if _, err := store.AppendEventResolved(ctx, event); err != nil {
			t.Fatalf("append %s: %v", sessionID, err)
		}
		scopeResult, err := store.UpsertSessionScope(ctx, sessionID, scope)
		if err != nil {
			t.Fatalf("scope %s: %v", sessionID, err)
		}
		targets = append(targets, scopeResult.TargetGeneration)
	}
	signature, err := store.DeriveCommandSignature("go test ./...")
	if err != nil {
		t.Fatalf("derive signature: %v", err)
	}
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"failure.v1",
		"explicit_command_failure",
		scope.ID,
		signature,
	)
	if err != nil {
		t.Fatalf("derive issue identity: %v", err)
	}

	firstCommit, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        sessionIDs[0],
		ClaimedGeneration: targets[0],
		Status:            model.AnalysisCurrent,
		ScopeQuality:      scope.Quality,
		Occurrences: []model.IssueOccurrence{
			testIssueOccurrence(
				sessionIDs[0],
				"codex",
				eventIDs[0],
				fingerprint,
				issueID,
				"low",
				"high",
				base,
				scope.Quality,
			),
		},
	})
	if err != nil {
		t.Fatalf("first ReplaceIssueProjection() error = %v", err)
	}
	oldQuery := issueQueryForSnapshot(
		t, store, firstCommit.ProjectionGeneration, time.Now().UTC(),
	)
	oldPage, err := store.QueryIssues(ctx, oldQuery)
	if err != nil {
		t.Fatalf("QueryIssues(old) error = %v", err)
	}
	if len(oldPage.Data) != 1 ||
		oldPage.Data[0].OccurrenceCount != 1 ||
		oldPage.Data[0].SessionCount != 1 {
		t.Fatalf("old snapshot = %+v", oldPage)
	}

	secondCommit, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        sessionIDs[1],
		ClaimedGeneration: targets[1],
		Status:            model.AnalysisCurrent,
		ScopeQuality:      scope.Quality,
		Occurrences: []model.IssueOccurrence{
			testIssueOccurrence(
				sessionIDs[1],
				"claude-code",
				eventIDs[1],
				fingerprint,
				issueID,
				"medium",
				"medium",
				base.Add(time.Minute),
				scope.Quality,
			),
		},
	})
	if err != nil {
		t.Fatalf("second ReplaceIssueProjection() error = %v", err)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{Recurrence: "repeated"},
	})
	if err != nil {
		t.Fatalf("QueryIssues() error = %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("current issues = %+v", page)
	}
	summary := page.Data[0]
	if summary.OccurrenceCount != 2 ||
		summary.SessionCount != 2 ||
		summary.Severity != "medium" ||
		summary.Confidence != "medium" ||
		len(summary.Harnesses) != 2 ||
		summary.AnalysisStatus != model.AnalysisCurrent {
		t.Fatalf("aggregated summary = %+v", summary)
	}
	oldQuery.IssuedAt = time.Now().UTC()
	oldAgain, err := store.QueryIssues(ctx, oldQuery)
	if err != nil {
		t.Fatalf("QueryIssues(old again) error = %v", err)
	}
	if oldAgain.Data[0].OccurrenceCount != 1 {
		t.Fatalf("old snapshot changed after second commit: %+v", oldAgain.Data[0])
	}

	occurrencePage, err := store.QueryIssueOccurrences(ctx, model.IssueOccurrenceQuery{
		IssueID: issueID,
		Limit:   1,
	})
	if err != nil {
		t.Fatalf("QueryIssueOccurrences() error = %v", err)
	}
	if len(occurrencePage.Data) != 1 || !occurrencePage.HasMore {
		t.Fatalf("occurrence first page = %+v", occurrencePage)
	}
	cursor := &model.IssueOccurrencePosition{
		LastObserved: occurrencePage.Data[0].LastObservedAt,
		OccurrenceID: occurrencePage.Data[0].OccurrenceID,
	}
	nextOccurrences, err := store.QueryIssueOccurrences(ctx, model.IssueOccurrenceQuery{
		IssueID:             issueID,
		Limit:               1,
		CursorEpoch:         occurrencePage.CursorEpoch,
		Snapshot:            occurrencePage.Snapshot,
		RetentionGeneration: occurrencePage.RetentionGeneration,
		IssuedAt:            occurrencePage.IssuedAt,
		Cursor:              cursor,
	})
	if err != nil {
		t.Fatalf("QueryIssueOccurrences(next) error = %v", err)
	}
	if len(nextOccurrences.Data) != 1 || nextOccurrences.HasMore {
		t.Fatalf("occurrence next page = %+v", nextOccurrences)
	}

	if _, err := store.PublishAnalysisFailure(
		ctx,
		sessionIDs[0],
		targets[0],
		"detector_timeout",
		time.Now().UTC().Add(time.Minute),
	); err != nil {
		t.Fatalf("PublishAnalysisFailure() error = %v", err)
	}
	failedPage, err := store.QueryIssues(ctx, model.IssueQuery{})
	if err != nil {
		t.Fatalf("QueryIssues(failed) error = %v", err)
	}
	if failedPage.Data[0].AnalysisStatus != model.AnalysisFailed ||
		failedPage.Analysis.FailedSessions != 1 ||
		failedPage.Analysis.Complete {
		t.Fatalf("failed analysis projection = %+v", failedPage)
	}
	currentOnly, err := store.QueryIssues(ctx, model.IssueQuery{
		Filter: model.IssueFilter{AnalysisStatus: model.AnalysisCurrent},
	})
	if err != nil {
		t.Fatalf("QueryIssues(current status) error = %v", err)
	}
	if len(currentOnly.Data) != 0 {
		t.Fatalf("pre-aggregation status filter leaked mixed issue: %+v", currentOnly.Data)
	}
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        sessionIDs[1],
		ClaimedGeneration: targets[1] - 1,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      scope.Quality,
	}); !errors.Is(err, ErrStaleProjection) {
		t.Fatalf("stale projection error = %v", err)
	}
	if secondCommit.ProjectionGeneration <= firstCommit.ProjectionGeneration {
		t.Fatalf("projection generations = first %d second %d",
			firstCommit.ProjectionGeneration,
			secondCommit.ProjectionGeneration,
		)
	}
}

func TestIssueSnapshotExpiryAndMutationAuthorization(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000130",
		"session-expiry",
		1,
		time.Date(2026, 9, 8, 13, 0, 0, 0, time.UTC),
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatalf("append event: %v", err)
	}
	var current int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT current_generation FROM issue_projection_metadata`,
	).Scan(&current); err != nil {
		t.Fatalf("read current generation: %v", err)
	}
	expired := issueQueryForSnapshot(
		t,
		store,
		current,
		time.Now().UTC().Add(-issueCursorLifetime-time.Second),
	)
	if _, err := store.QueryIssues(ctx, expired); !errors.Is(err, ErrIssueSnapshotExpired) {
		t.Fatalf("expired snapshot error = %v", err)
	}
	future := issueQueryForSnapshot(
		t, store, current, time.Now().UTC().Add(time.Minute),
	)
	if _, err := store.QueryIssues(ctx, future); !errors.Is(err, model.ErrIssueSnapshotInvalid) {
		t.Fatalf("future-issued snapshot error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE session_analysis_revisions
		SET status = 'failed'
		WHERE session_key = ?`,
		event.Session.Key,
	); err == nil {
		t.Fatal("unauthorized analysis revision update succeeded")
	}
}

func TestIssueListCursorUsesStableSnapshotOrdering(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	observedAt := time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000140",
		"session-issue-cursor",
		1,
		observedAt,
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatalf("append event: %v", err)
	}
	highFingerprint, highIssue, err := store.DeriveIssueIdentity(
		"v1", "high_detector", event.Session.Key, "high",
	)
	if err != nil {
		t.Fatalf("derive high issue: %v", err)
	}
	lowFingerprint, lowIssue, err := store.DeriveIssueIdentity(
		"v1", "low_detector", event.Session.Key, "low",
	)
	if err != nil {
		t.Fatalf("derive low issue: %v", err)
	}
	high := testIssueOccurrence(
		event.Session.Key,
		"codex",
		event.EventID,
		highFingerprint,
		highIssue,
		"high",
		"high",
		observedAt,
		model.ScopeUnscoped,
	)
	high.Provenance.DetectorID = "high_detector"
	high.TitleCode = "high_detector"
	low := testIssueOccurrence(
		event.Session.Key,
		"codex",
		event.EventID,
		lowFingerprint,
		lowIssue,
		"low",
		"high",
		observedAt.Add(time.Minute),
		model.ScopeUnscoped,
	)
	low.Provenance.DetectorID = "low_detector"
	low.TitleCode = "low_detector"
	commit, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: 1,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{low, high},
	})
	if err != nil {
		t.Fatalf("ReplaceIssueProjection() error = %v", err)
	}
	first, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 1})
	if err != nil {
		t.Fatalf("QueryIssues(first) error = %v", err)
	}
	if len(first.Data) != 1 || !first.HasMore || first.Data[0].IssueID != highIssue {
		t.Fatalf("first issue page = %+v", first)
	}
	cursor := &model.IssuePosition{
		SeverityRank: 4,
		Repeated:     false,
		LastObserved: first.Data[0].LastObservedAt,
		IssueID:      first.Data[0].IssueID,
	}
	secondQuery := issueQueryForSnapshot(
		t, store, commit.ProjectionGeneration, first.IssuedAt,
	)
	secondQuery.Limit = 1
	secondQuery.Cursor = cursor
	second, err := store.QueryIssues(ctx, secondQuery)
	if err != nil {
		t.Fatalf("QueryIssues(second) error = %v", err)
	}
	if len(second.Data) != 1 || second.HasMore || second.Data[0].IssueID != lowIssue {
		t.Fatalf("second issue page = %+v", second)
	}
}

func TestRetentionPrunesIssueDependenciesAndExpiresOldSnapshot(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 15, 0, 0, 0, time.UTC)
	store, err := OpenWithOptions(
		filepath.Join(t.TempDir(), "retention.sqlite"),
		OpenOptions{
			KeyProvider: newMemoryKeyProvider(),
			Clock:       func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	oldEvent := storageTestEvent(
		"00000000-0000-7000-8000-000000000150",
		"session-issue-retention",
		1,
		now.Add(-time.Hour),
	)
	recentEvent := storageTestEvent(
		"00000000-0000-7000-8000-000000000151",
		oldEvent.Session.Key,
		2,
		now,
	)
	if _, err := store.AppendEventResolved(ctx, oldEvent); err != nil {
		t.Fatalf("append old event: %v", err)
	}
	if _, err := store.AppendEventResolved(ctx, recentEvent); err != nil {
		t.Fatalf("append recent event: %v", err)
	}
	signature, err := store.DeriveCommandSignature("go test ./...")
	if err != nil {
		t.Fatalf("derive signature: %v", err)
	}
	enrichmentResult, err := store.UpsertEventEnrichment(ctx, oldEvent.EventID, model.EventEnrichment{
		CommandSignatureID: signature,
		CommandClass:       "test",
		PermissionClass:    "permission.unknown",
		Version:            "v1",
	})
	if err != nil {
		t.Fatalf("upsert enrichment: %v", err)
	}
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"v1", "explicit_command_failure", oldEvent.Session.Key, signature,
	)
	if err != nil {
		t.Fatalf("derive issue: %v", err)
	}
	commit, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        oldEvent.Session.Key,
		ClaimedGeneration: enrichmentResult.TargetGeneration,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences: []model.IssueOccurrence{
			testIssueOccurrence(
				oldEvent.Session.Key,
				"codex",
				oldEvent.EventID,
				fingerprint,
				issueID,
				"low",
				"high",
				oldEvent.OccurredAt,
				model.ScopeUnscoped,
			),
		},
	})
	if err != nil {
		t.Fatalf("replace issue projection: %v", err)
	}
	before, err := store.RetentionDiagnostics(
		ctx,
		RetentionPolicy{MaxEventCount: 1},
		now.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("RetentionDiagnostics() error = %v", err)
	}
	if before.CurrentPayloadBytes <= 0 {
		t.Fatalf("retention bytes = %+v", before)
	}
	result, err := store.Prune(
		ctx,
		RetentionPolicy{MaxEventCount: 1},
		now.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if result.PrunedEventCount != 1 || result.PrunedPayloadBytes <= 0 {
		t.Fatalf("retention result = %+v", result)
	}
	if count, err := store.Count(ctx, "issue_occurrences"); err != nil || count != 0 {
		t.Fatalf("issue occurrence count = %d, error = %v", count, err)
	}
	if count, err := store.Count(ctx, "event_enrichments"); err != nil || count != 0 {
		t.Fatalf("event enrichment count = %d, error = %v", count, err)
	}
	retainedQuery := issueQueryForSnapshot(
		t, store, commit.ProjectionGeneration, time.Now().UTC(),
	)
	retainedQuery.RetentionGeneration--
	if _, err := store.QueryIssues(ctx, retainedQuery); !errors.Is(
		err,
		ErrIssueSnapshotExpired,
	) {
		t.Fatalf("retained snapshot error = %v", err)
	}
	current, err := store.QueryIssues(ctx, model.IssueQuery{})
	if err != nil {
		t.Fatalf("QueryIssues(current) error = %v", err)
	}
	if len(current.Data) != 0 ||
		current.Analysis.PendingSessions != 1 ||
		current.Analysis.Complete {
		t.Fatalf("current retained projection = %+v", current)
	}
}

func testIssueOccurrence(
	sessionID string,
	harness string,
	eventID string,
	fingerprint string,
	issueID string,
	severity string,
	confidence string,
	observedAt time.Time,
	scopeQuality model.ScopeQuality,
) model.IssueOccurrence {
	return model.IssueOccurrence{
		IssueID:            issueID,
		FingerprintID:      fingerprint,
		FingerprintVersion: "failure.v1",
		Origin:             "belay",
		SessionID:          sessionID,
		Harness:            harness,
		Provenance: model.DetectorProvenance{
			DetectorID:         "explicit_command_failure",
			DetectorVersion:    "1.0.0",
			FingerprintVersion: "failure.v1",
			ProjectionVersion:  "projection.v1",
		},
		Category:         "command_failure",
		TitleCode:        "explicit_command_failure",
		Severity:         severity,
		Confidence:       confidence,
		ScopeQuality:     scopeQuality,
		FirstObservedAt:  observedAt,
		LastObservedAt:   observedAt,
		EvidenceComplete: true,
		FingerprintScopeID: func() string {
			if scopeQuality == model.ScopeResolved ||
				scopeQuality == model.ScopeLexical {
				return "psc_" + strings.Repeat("a", 52)
			}
			return ""
		}(),
		Evidence: model.IssueEvidence{
			CitedEventIDs: []string{eventID},
			Dimensions:    []string{"opaque-dimension"},
		},
	}
}

func issueQueryForSnapshot(
	t *testing.T,
	store *Store,
	snapshot int64,
	issuedAt time.Time,
) model.IssueQuery {
	t.Helper()
	return model.IssueQuery{
		CursorEpoch:         mustIssueCursorEpoch(t, store),
		Snapshot:            snapshot,
		RetentionGeneration: mustRetentionGeneration(t, store),
		IssuedAt:            issuedAt,
	}
}

func TestAnalysisDiagnosticIsBoundedAndDeduplicated(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	for range 2 {
		if err := store.RecordAnalysisDiagnostic(
			ctx,
			"ses_opaque",
			"detector.v1",
			"timeout",
		); err != nil {
			t.Fatalf("RecordAnalysisDiagnostic() error = %v", err)
		}
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `
		SELECT occurrence_count FROM analysis_diagnostics
		WHERE session_key = 'ses_opaque'`,
	).Scan(&count); err != nil {
		t.Fatalf("read diagnostic count: %v", err)
	}
	if count != 2 {
		t.Fatalf("diagnostic count = %d, want 2", count)
	}
	if err := store.RecordAnalysisDiagnostic(
		ctx,
		"ses_opaque",
		"detector",
		fmt.Sprintf("bad %s", "payload"),
	); err == nil {
		t.Fatal("free-form diagnostic code unexpectedly accepted")
	}
}

func TestProjectionStorageRejectsUnsafePlaintextMetadata(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000160",
		"session-unsafe-metadata",
		1,
		time.Date(2026, 9, 8, 16, 0, 0, 0, time.UTC),
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if _, err := store.UpsertEventEnrichment(ctx, event.EventID, model.EventEnrichment{
		CommandClass:    "raw command --secret",
		PermissionClass: "permission.unknown",
		Version:         "v1",
	}); err == nil {
		t.Fatal("unsafe command class unexpectedly accepted")
	}
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"v1", "detector", event.Session.Key, "fixed",
	)
	if err != nil {
		t.Fatalf("derive issue identity: %v", err)
	}
	occurrence := testIssueOccurrence(
		event.Session.Key,
		"codex",
		event.EventID,
		fingerprint,
		issueID,
		"low",
		"high",
		event.OccurredAt,
		model.ScopeUnscoped,
	)
	occurrence.TitleCode = "raw title with spaces"
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: 1,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{occurrence},
	}); err == nil {
		t.Fatal("unsafe issue metadata unexpectedly accepted")
	}
}

func TestReplaceSessionProjectionPublishesBothOriginsAtomically(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000170",
		"session-atomic-projection",
		1,
		time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC),
	)
	appended, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	belayFingerprint, belayIssue, _ := store.DeriveIssueIdentity(
		"v1", "detector", event.Session.Key, "belay",
	)
	numbatFingerprint, numbatIssue, _ := store.DeriveIssueIdentity(
		"v1", "numbat_finding", event.Session.Key, "numbat",
	)
	belay := testIssueOccurrence(
		event.Session.Key, "codex", event.EventID, belayFingerprint, belayIssue,
		"low", "high", event.OccurredAt, model.ScopeUnscoped,
	)
	numbat := testIssueOccurrence(
		event.Session.Key, "codex", event.EventID, numbatFingerprint, numbatIssue,
		"medium", "high", event.OccurredAt, model.ScopeUnscoped,
	)
	numbat.Origin = "numbat"
	numbat.Provenance.DetectorID = "numbat_finding"
	commit, err := store.ReplaceSessionProjection(ctx, SessionProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: appended.ReadGeneration,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		BelayOccurrences:  []model.IssueOccurrence{belay},
		NumbatOccurrences: []model.IssueOccurrence{numbat},
	})
	if err != nil {
		t.Fatal(err)
	}
	var generations, revisions int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT visible_from_generation), COUNT(*)
		FROM issue_occurrences WHERE session_key = ?`,
		event.Session.Key,
	).Scan(&generations, &revisions); err != nil {
		t.Fatal(err)
	}
	if generations != 1 || revisions != 2 || commit.OccurrenceCount != 2 {
		t.Fatalf("atomic projection = generations %d revisions %d commit %+v",
			generations, revisions, commit)
	}
	var state string
	if err := store.db.QueryRowContext(ctx,
		"SELECT state FROM dirty_sessions WHERE session_key = ?",
		event.Session.Key,
	).Scan(&state); err != nil || state != "current" {
		t.Fatalf("dirty state = %q, error %v", state, err)
	}
}

func TestEventEnrichmentReplayUpgradeAndConflict(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000171",
		"session-enrichment-conflict",
		1,
		time.Date(2026, 9, 8, 17, 1, 0, 0, time.UTC),
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertEventEnrichment(ctx, event.EventID, model.EventEnrichment{
		CommandClass: "unknown", PermissionClass: "permission.unknown", Version: "v1",
	}); err != nil {
		t.Fatal(err)
	}
	signature, _ := store.DeriveCommandSignature("go test ./...")
	richer := model.EventEnrichment{
		CommandSignatureID: signature,
		CommandClass:       "test",
		PermissionClass:    "permission.shell",
		Version:            "v1",
	}
	if result, err := store.UpsertEventEnrichment(ctx, event.EventID, richer); err != nil || !result.Changed {
		t.Fatalf("richer replay = %+v, %v", result, err)
	}
	other, _ := store.DeriveCommandSignature("go test ./other")
	_, err := store.UpsertEventEnrichment(ctx, event.EventID, model.EventEnrichment{
		CommandSignatureID: other,
		CommandClass:       "build",
		PermissionClass:    "permission.file",
		Version:            "v2",
	})
	var conflict *EventEnrichmentConflictError
	if !errors.Is(err, ErrEventEnrichmentConflict) || !errors.As(err, &conflict) {
		t.Fatalf("conflict error = %T %v", err, err)
	}
	if _, err := store.UpsertEventEnrichment(ctx, event.EventID, model.EventEnrichment{
		CommandSignatureID: signature,
		CommandClass:       "build",
		PermissionClass:    "permission.shell",
		Version:            "v2",
	}); !errors.Is(err, ErrEventEnrichmentConflict) {
		t.Fatalf("class conflict error = %v", err)
	}
	got, err := store.EventEnrichments(ctx, event.Session.Key)
	if err != nil || got[event.EventID] != richer {
		t.Fatalf("persisted enrichment = %+v, error %v", got[event.EventID], err)
	}
}

func TestIssueCoverageIncludesEventSessionsWithoutAnalysisRevision(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000172",
		"session-missing-analysis",
		1,
		time.Date(2026, 9, 8, 17, 2, 0, 0, time.UTC),
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		_, err := tx.ExecContext(ctx,
			"DELETE FROM session_analysis_revisions WHERE session_key = ?",
			event.Session.Key,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Analysis.PendingSessions != 1 || page.Analysis.Complete {
		t.Fatalf("coverage = %+v", page.Analysis)
	}
	if _, err := store.QueryIssues(ctx, model.IssueQuery{
		Snapshot: page.Snapshot,
	}); !errors.Is(err, model.ErrIssueSnapshotInvalid) {
		t.Fatalf("snapshot without issued_at error = %v", err)
	}
}

func TestRetentionProtectsFreshIssueCursorAndAnalysisRevision(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Now().UTC()
	oldEvent := storageTestEvent(
		"00000000-0000-7000-8000-000000000173",
		"session-fresh-retention",
		1,
		now.Add(-2*time.Hour),
	)
	recentEvent := storageTestEvent(
		"00000000-0000-7000-8000-000000000174",
		oldEvent.Session.Key,
		2,
		now.Add(-time.Hour),
	)
	if _, err := store.AppendEventResolved(ctx, oldEvent); err != nil {
		t.Fatal(err)
	}
	appendResult, err := store.AppendEventResolved(ctx, recentEvent)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, issueID, _ := store.DeriveIssueIdentity(
		"v1", "detector", oldEvent.Session.Key, "fresh",
	)
	commit, err := store.ReplaceSessionProjection(ctx, SessionProjectionReplacement{
		SessionKey:        oldEvent.Session.Key,
		ClaimedGeneration: appendResult.ReadGeneration,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		BelayOccurrences: []model.IssueOccurrence{testIssueOccurrence(
			oldEvent.Session.Key, "codex", oldEvent.EventID, fingerprint, issueID,
			"low", "high", oldEvent.OccurredAt, model.ScopeUnscoped,
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Now().UTC()
	if _, err := store.Prune(
		ctx,
		RetentionPolicy{MaxEventCount: 1},
		now.Add(30*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	var citedEventCount int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM events WHERE event_id = ?",
		oldEvent.EventID,
	).Scan(&citedEventCount); err != nil || citedEventCount != 1 {
		t.Fatalf("fresh dependent event count = %d, error %v", citedEventCount, err)
	}
	if count, _ := store.Count(ctx, "session_analysis_revisions"); count < 1 {
		t.Fatal("fresh analysis revisions were pruned")
	}
	protectedQuery := issueQueryForSnapshot(
		t, store, commit.ProjectionGeneration, issuedAt,
	)
	page, err := store.QueryIssues(ctx, protectedQuery)
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("fresh cursor = %+v, error %v", page, err)
	}
}

func TestProjectionTimestampsUseFixedWidthUTCEncoding(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000175",
		"session-fixed-time",
		1,
		time.Date(2026, 9, 8, 17, 5, 0, 123, time.UTC),
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}
	var updatedAt string
	if err := store.db.QueryRowContext(ctx,
		"SELECT updated_at FROM dirty_sessions WHERE session_key = ?",
		event.Session.Key,
	).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if len(updatedAt) != len("2006-01-02T15:04:05.000000000Z") ||
		updatedAt[len(updatedAt)-1] != 'Z' {
		t.Fatalf("projection timestamp = %q", updatedAt)
	}
}
