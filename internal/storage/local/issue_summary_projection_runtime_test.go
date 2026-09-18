package local

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestIssueSummaryRuntimeStatusCoalescingAndCompaction(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	store, err := OpenWithOptions(
		filepath.Join(t.TempDir(), "projection.sqlite"),
		OpenOptions{
			KeyProvider: newMemoryKeyProvider(),
			Clock:       func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000a21",
		"session-runtime-projection",
		1,
		now,
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}
	fingerprint, issueID, err := store.DeriveIssueIdentity(
		"failure.v1", "explicit_command_failure", event.Session.Key, "dimension",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: dirtyTargetGeneration(t, store, event.Session.Key),
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences: []model.IssueOccurrence{testIssueOccurrence(
			event.Session.Key, "codex", event.EventID, fingerprint, issueID,
			"medium", "high", now, model.ScopeUnscoped,
		)},
	}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	firstPending, err := store.MarkSessionDirty(
		ctx, event.Session.Key, "runtime_test_pending",
	)
	if err != nil {
		t.Fatal(err)
	}
	summaryCount := tableCount(t, store, "issue_summary_revisions")
	coverageCount := tableCount(t, store, "issue_analysis_coverage_revisions")

	for index := 0; index < 50; index++ {
		if _, err := store.MarkSessionDirty(
			ctx, event.Session.Key, "runtime_test_pending",
		); err != nil {
			t.Fatal(err)
		}
	}
	if got := tableCount(t, store, "issue_summary_revisions"); got != summaryCount {
		t.Fatalf("unchanged summary churn created revisions: %d != %d", got, summaryCount)
	}
	if got := tableCount(t, store, "issue_analysis_coverage_revisions"); got != coverageCount {
		t.Fatalf("unchanged coverage churn created revisions: %d != %d", got, coverageCount)
	}
	assertIssueMaterializedCurrent(t, store)

	if _, err := store.PublishAnalysisFailure(
		ctx,
		event.Session.Key,
		firstPending+50,
		"runtime_test_failure",
		now.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 ||
		page.Data[0].AnalysisStatus != model.AnalysisFailed ||
		page.Analysis.FailedSessions != 1 {
		t.Fatalf("failed status materialization = %+v", page)
	}

	now = now.Add(17 * time.Minute)
	if _, err := store.MarkSessionDirty(
		ctx, event.Session.Key, "runtime_test_compact",
	); err != nil {
		t.Fatal(err)
	}
	var oldest int64
	if err := store.db.QueryRow(`
		SELECT oldest_materialized_generation
		FROM issue_summary_metadata WHERE singleton = 1`,
	).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if oldest == 0 {
		t.Fatal("closed issue revisions were not compacted")
	}
	var orphanRelations int
	if err := store.db.QueryRow(`
		SELECT COUNT(*)
		FROM issue_summary_sessions iss
		WHERE NOT EXISTS (
			SELECT 1 FROM issue_summary_revisions sr
			WHERE sr.summary_revision_id = iss.summary_revision_id
		)`,
	).Scan(&orphanRelations); err != nil {
		t.Fatal(err)
	}
	if orphanRelations != 0 {
		t.Fatalf("orphaned summary relations = %d", orphanRelations)
	}
	assertIssueMaterializedCurrent(t, store)
}

func TestQueryIssuesScale5000Groups100000OccurrencesIsSummaryBounded(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := formatProjectionTime(time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC))

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		if _, err := tx.ExecContext(ctx, `
			WITH RECURSIVE seq(n) AS (
				VALUES(1)
				UNION ALL SELECT n + 1 FROM seq WHERE n < 5000
			)
			INSERT INTO issue_summary_revisions (
				summary_revision_id, issue_id, fingerprint_id,
				fingerprint_version, origin, detector_id, detector_version,
				category, title_code, severity, severity_rank, confidence,
				scope_quality, first_observed_at, last_observed_at,
				occurrence_count, session_count, repeated, analysis_status,
				evidence_complete, retained_history_only, experimental,
				visible_from_generation, created_at
			)
			SELECT
				printf('scale-revision-%05d', n),
				printf('scale-issue-%05d', n),
				printf('scale-fingerprint-%05d', n),
				'v1', 'belay', 'scale_detector', 'v1',
				'command_failure', 'issue.explicit_command_failure',
				'medium', 3, 'high', 'unscoped', ?, ?,
				20, 20, 1, 'current', 1, 0, 0, 0, ?
			FROM seq`,
			now, now, now,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			WITH digits(d) AS (
				VALUES(0),(1),(2),(3),(4),(5),(6),(7),(8),(9)
			),
			seq(n) AS (
				SELECT a.d + 10*b.d + 100*c.d + 1000*d.d + 10000*e.d + 1
				FROM digits a
				CROSS JOIN digits b
				CROSS JOIN digits c
				CROSS JOIN digits d
				CROSS JOIN digits e
			)
			INSERT INTO issue_occurrences (
				revision_id, occurrence_id, issue_id, fingerprint_id,
				fingerprint_version, origin, session_key, harness,
				detector_id, detector_version, projection_version, category,
				title_code, severity, confidence, scope_quality,
				first_observed_at, last_observed_at, evidence_complete,
				retained_history_only, experimental, analysis_status,
				analysis_generation, evidence_payload, evidence_encoding,
				visible_from_generation, created_at, updated_at
			)
			SELECT
				printf('scale-occurrence-revision-%06d', n),
				printf('scale-occurrence-%06d', n),
				printf('scale-issue-%05d', ((n - 1) % 5000) + 1),
				printf('scale-fingerprint-%05d', ((n - 1) % 5000) + 1),
				'v1', 'belay', printf('scale-session-%06d', n), 'codex',
				'scale_detector', 'v1', 'v1', 'command_failure',
				'issue.explicit_command_failure', 'medium', 'high', 'unscoped',
				?, ?, 1, 0, 0, 'current', 1, X'00', 'test', 1, ?, ?
			FROM seq`,
			now, now, now, now,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if got := tableCount(t, store, "issue_occurrences"); got != 100000 {
		t.Fatalf("scale occurrence count = %d", got)
	}
	page, err := store.QueryIssues(ctx, model.IssueQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 20 || !page.HasMore {
		t.Fatalf("scale page = returned %d has_more=%v", len(page.Data), page.HasMore)
	}

	rows, err := store.db.QueryContext(ctx, `
		EXPLAIN QUERY PLAN
		SELECT sr.issue_id
		FROM issue_summary_revisions sr INDEXED BY issue_summary_order_snapshot_idx
		WHERE sr.visible_from_generation <= 0
			AND (sr.visible_until_generation IS NULL OR sr.visible_until_generation > 0)
		ORDER BY sr.severity_rank DESC, sr.repeated DESC,
			sr.last_observed_at DESC, sr.issue_id ASC
		LIMIT 21`)
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
	if strings.Contains(plan.String(), "issue_occurrences") ||
		!strings.Contains(plan.String(), "issue_summary_order_snapshot_idx") {
		t.Fatalf("unexpected issue list query plan:\n%s", plan.String())
	}
}

func TestQueryIssuesCancellation(t *testing.T) {
	store := openStorageTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.QueryIssues(ctx, model.IssueQuery{}); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("cancelled issue query error = %v", err)
	}
}

func TestIssueMaterializedSnapshotValidatesEpochRetentionAndGeneration(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	fresh, err := store.QueryIssues(ctx, model.IssueQuery{})
	if err != nil {
		t.Fatal(err)
	}
	valid := model.IssueQuery{
		CursorEpoch:         fresh.CursorEpoch,
		Snapshot:            fresh.Snapshot,
		RetentionGeneration: fresh.RetentionGeneration,
		IssuedAt:            fresh.IssuedAt,
	}
	if _, err := store.QueryIssues(ctx, valid); err != nil {
		t.Fatalf("valid materialized snapshot error = %v", err)
	}
	staleEpoch := valid
	staleEpoch.CursorEpoch += "stale"
	if _, err := store.QueryIssues(ctx, staleEpoch); !errors.Is(
		err,
		model.ErrIssueSnapshotExpired,
	) {
		t.Fatalf("stale epoch error = %v", err)
	}
	staleRetention := valid
	staleRetention.RetentionGeneration++
	if _, err := store.QueryIssues(ctx, staleRetention); !errors.Is(
		err,
		model.ErrIssueSnapshotExpired,
	) {
		t.Fatalf("stale retention error = %v", err)
	}
	missingEpoch := valid
	missingEpoch.CursorEpoch = ""
	if _, err := store.QueryIssues(ctx, missingEpoch); !errors.Is(
		err,
		model.ErrIssueSnapshotInvalid,
	) {
		t.Fatalf("missing epoch error = %v", err)
	}

	if _, err := store.db.ExecContext(ctx, `
		UPDATE issue_projection_metadata
		SET current_generation = current_generation + 1
		WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.currentIssueCursorEpoch(); !errors.Is(
		err,
		model.ErrIssueSnapshotExpired,
	) {
		t.Fatalf("generation-drift epoch error = %v", err)
	}
	if _, err := store.QueryIssues(ctx, model.IssueQuery{}); err == nil {
		t.Fatal("generation drift did not fail issue reads closed")
	}
}

func tableCount(t *testing.T, store *Store, table string) int {
	t.Helper()
	allowed := map[string]bool{
		"issue_summary_revisions":           true,
		"issue_analysis_coverage_revisions": true,
		"issue_occurrences":                 true,
	}
	if !allowed[table] {
		t.Fatalf("unsupported test count table %q", table)
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertIssueMaterializedCurrent(t *testing.T, store *Store) {
	t.Helper()
	var current, materialized int64
	if err := store.db.QueryRow(`
		SELECT ipm.current_generation, ism.materialized_generation
		FROM issue_projection_metadata ipm
		JOIN issue_summary_metadata ism ON ism.singleton = ipm.singleton
		WHERE ipm.singleton = 1`,
	).Scan(&current, &materialized); err != nil {
		t.Fatal(err)
	}
	if materialized != current {
		t.Fatalf("materialized generation = %d, current = %d", materialized, current)
	}
}
