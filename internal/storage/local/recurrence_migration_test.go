package local

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestMigration011FreshSchemaAndCompletedBackfill(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	for _, table := range []string{
		"local_migration_progress",
		"fix_monitoring_metadata",
		"fix_monitoring_subjects",
		"session_analysis_capabilities",
		"fix_recurrence_jobs",
		"fix_recurrence_job_events",
		"fix_recurrence_observations",
		"fix_recurrence_observation_events",
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sqlite_schema
			WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %s count/error = %d/%v", table, count, err)
		}
	}
	for table, columns := range map[string][]string{
		"events":                     {"occurred_at_order_ns"},
		"issue_occurrences":          {"fingerprint_scope_id"},
		"session_analysis_revisions": {"analysis_through_order_ns", "analyzed_event_generation"},
		"issue_projection_metadata":  {"retention_generation"},
	} {
		for _, column := range columns {
			var count int
			if err := store.db.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM pragma_table_info(?)
				WHERE name = ?`,
				table,
				column,
			).Scan(&count); err != nil || count != 1 {
				t.Fatalf("column %s.%s count/error = %d/%v",
					table, column, count, err)
			}
		}
	}
	var completed int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM local_migration_progress
		WHERE migration_version = 11 AND complete = 1`,
	).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 4 {
		t.Fatalf("completed migration phases = %d, want 4", completed)
	}
}

func TestMigration011CompletedBackfillDoesNotBlockOpenOnActiveWriter(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "completed-backfill.sqlite")
	provider := newMemoryKeyProvider()
	first, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	var completed int
	if err := first.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM local_migration_progress
		WHERE migration_version = 11 AND complete = 1`,
	).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 4 {
		t.Fatalf("completed migration phases = %d, want 4", completed)
	}

	tx, err := first.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := withMutationTx(ctx, tx, mutationRecurrenceWorker, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE fix_monitoring_metadata
			SET updated_at = updated_at
			WHERE singleton = 1`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path, provider)
	if err != nil {
		t.Fatalf("Open() with completed recurrence migration blocked on active writer: %v", err)
	}
	defer second.Close()
}

func TestMigration011ResumesNormalizedEventBackfillBeforeOpenReturns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "resume.sqlite")
	provider := newMemoryKeyProvider()
	eventAt := time.Date(2026, 9, 8, 12, 34, 56, 123456789, time.FixedZone("offset", -7*60*60))
	store, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000009911",
		"migration-resume-session",
		1,
		eventAt,
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationPayloadUpgrade, func() error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE events SET occurred_at_order_ns = NULL
			WHERE event_id = ?`,
			event.EventID,
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE local_migration_progress
			SET after_sequence = 0, complete = 0
			WHERE migration_version = 11 AND phase = 'event_order'`)
		return err
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, provider)
	if err != nil {
		t.Fatalf("Open() did not resume migration: %v", err)
	}
	defer reopened.Close()
	var order int64
	if err := reopened.db.QueryRowContext(ctx, `
		SELECT occurred_at_order_ns FROM events WHERE event_id = ?`,
		event.EventID,
	).Scan(&order); err != nil {
		t.Fatal(err)
	}
	if order != eventAt.UTC().UnixNano() {
		t.Fatalf("normalized order = %d, want %d", order, eventAt.UTC().UnixNano())
	}
	var complete int
	if err := reopened.db.QueryRowContext(ctx, `
		SELECT complete FROM local_migration_progress
		WHERE migration_version = 11 AND phase = 'event_order'`,
	).Scan(&complete); err != nil || complete != 1 {
		t.Fatalf("event backfill completion = %d/%v", complete, err)
	}
}

func TestMigration011FreshProjectionPersistsExactAnalysisWatermark(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	eventAt := time.Date(2026, 9, 8, 20, 0, 0, 123, time.UTC)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000009912",
		"migration-fresh-watermark",
		1,
		eventAt,
	)
	appended, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	watermark := eventAt.UnixNano()
	commit, err := store.ReplaceSessionProjection(ctx, SessionProjectionReplacement{
		SessionKey:              event.Session.Key,
		ClaimedGeneration:       dirtyTargetGeneration(t, store, event.Session.Key),
		Status:                  model.AnalysisCurrent,
		ScopeQuality:            model.ScopeUnscoped,
		AnalysisThroughOrderNS:  &watermark,
		AnalyzedEventGeneration: appended.ReadGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	var storedWatermark sql.NullInt64
	var storedGeneration int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT analysis_through_order_ns, analyzed_event_generation
		FROM session_analysis_revisions
		WHERE visible_from_generation = ?`,
		commit.ProjectionGeneration,
	).Scan(&storedWatermark, &storedGeneration); err != nil {
		t.Fatal(err)
	}
	if !storedWatermark.Valid ||
		storedWatermark.Int64 != watermark ||
		storedGeneration != appended.ReadGeneration {
		t.Fatalf(
			"fresh analysis watermark/generation = %v/%d, want %d/%d",
			storedWatermark,
			storedGeneration,
			watermark,
			appended.ReadGeneration,
		)
	}
}

func TestMigration011LegacyAnalysisWatermarksRemainUnknownAcrossResume(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-watermark.sqlite")
	provider := newMemoryKeyProvider()
	store, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC)
	first := storageTestEvent(
		"00000000-0000-7000-8000-000000009913",
		"migration-legacy-watermark",
		1,
		firstAt,
	)
	appended, err := store.AppendEventResolved(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	firstWatermark := firstAt.UnixNano()
	if _, err := store.ReplaceSessionProjection(ctx, SessionProjectionReplacement{
		SessionKey:              first.Session.Key,
		ClaimedGeneration:       dirtyTargetGeneration(t, store, first.Session.Key),
		Status:                  model.AnalysisCurrent,
		ScopeQuality:            model.ScopeUnscoped,
		AnalysisThroughOrderNS:  &firstWatermark,
		AnalyzedEventGeneration: appended.ReadGeneration,
	}); err != nil {
		t.Fatal(err)
	}
	second := storageTestEvent(
		"00000000-0000-7000-8000-000000009914",
		first.Session.Key,
		2,
		firstAt.Add(time.Hour),
	)
	if _, err := store.AppendEventResolved(ctx, second); err != nil {
		t.Fatal(err)
	}
	var firstRowID int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT MIN(rowid) FROM session_analysis_revisions
		WHERE session_key = ?`,
		first.Session.Key,
	).Scan(&firstRowID); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE session_analysis_revisions
			SET analysis_through_order_ns = NULL, analyzed_event_generation = 0
			WHERE session_key = ?`,
			first.Session.Key,
		); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE local_migration_progress
			SET after_sequence = ?, complete = 0
			WHERE migration_version = 11 AND phase = 'analysis_watermark'`,
			firstRowID,
		)
		return err
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var revisions, nonzeroGenerations, nonnullWatermarks int
	if err := reopened.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			SUM(CASE WHEN analyzed_event_generation <> 0 THEN 1 ELSE 0 END),
			SUM(CASE WHEN analysis_through_order_ns IS NOT NULL THEN 1 ELSE 0 END)
		FROM session_analysis_revisions
		WHERE session_key = ?`,
		first.Session.Key,
	).Scan(&revisions, &nonzeroGenerations, &nonnullWatermarks); err != nil {
		t.Fatal(err)
	}
	if revisions < 2 || nonzeroGenerations != 0 || nonnullWatermarks != 0 {
		t.Fatalf(
			"legacy revisions/nonzero-generation/non-null-watermark = %d/%d/%d",
			revisions,
			nonzeroGenerations,
			nonnullWatermarks,
		)
	}
}

func TestMigration011BackfillsExactNumbatHintsAndMonitoringSubjects(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "numbat-multi-hint.sqlite")
	provider := newMemoryKeyProvider()
	now := time.Date(2026, 9, 8, 18, 30, 0, 0, time.UTC)
	store, err := OpenWithOptions(path, OpenOptions{
		KeyProvider: provider,
		Clock:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "migration-numbat-multi-hint"
	scopes := make([]ProjectScope, 2)
	findings := make([]Finding, 2)
	occurrences := make([]model.IssueOccurrence, 2)
	issueIDs := make([]string, 2)
	var target int64
	for index := range 2 {
		scope, err := store.DeriveNumbatProjectScopeHash(
			strings.Repeat(string(rune('a'+index)), 64),
		)
		if err != nil {
			t.Fatal(err)
		}
		scopes[index] = scope
		eventAt := now.Add(time.Duration(index) * time.Minute)
		eventID := "00000000-0000-7000-8000-00000000992" + string(rune('0'+index))
		event := storageTestEvent(eventID, sessionID, int64(index+1), eventAt)
		appended, err := store.AppendEventResolved(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		target = appended.ReadGeneration
		findings[index] = Finding{
			FindingID:        "migration-numbat-finding-" + string(rune('a'+index)),
			SourceRunID:      event.Source.RunID,
			SessionKey:       sessionID,
			ProjectScopeHint: scope.ID,
			DetectedAt:       eventAt,
			RuleID:           "migration.numbat." + string(rune('a'+index)),
			RuleVersion:      "1",
			Severity:         "medium",
			SourceAgent:      "codex",
			Confidence:       "high",
			CitedEventIDs:    []string{eventID},
		}
		if inserted, err := store.RecordFinding(ctx, findings[index]); err != nil || !inserted {
			t.Fatalf("RecordFinding(%d) = %v/%v", index, inserted, err)
		}
		fingerprint, issueID, err := store.DeriveIssueIdentity(
			"failure.v1",
			"numbat_finding",
			scope.ID,
			"rule_id",
			findings[index].RuleID,
		)
		if err != nil {
			t.Fatal(err)
		}
		issueIDs[index] = issueID
		occurrence := testIssueOccurrence(
			sessionID,
			"codex",
			eventID,
			fingerprint,
			issueID,
			"medium",
			"high",
			eventAt,
			model.ScopeLexical,
		)
		occurrence.Origin = "numbat"
		occurrence.OriginRecordID = findings[index].FindingID
		occurrence.Provenance.DetectorID = "numbat_finding"
		occurrence.TitleCode = "numbat_finding"
		occurrence.FingerprintScopeID = scope.ID
		occurrences[index] = occurrence
	}
	commit, err := store.ReplaceSessionProjection(ctx, SessionProjectionReplacement{
		SessionKey:              sessionID,
		ClaimedGeneration:       target,
		Status:                  model.AnalysisCurrent,
		ScopeQuality:            model.ScopeLexical,
		AnalyzedEventGeneration: target,
		NumbatOccurrences:       occurrences,
	})
	if err != nil {
		t.Fatal(err)
	}
	annotationIDs := make([]string, 2)
	for index, issueID := range issueIDs {
		result, err := store.RecordFixAnnotation(ctx, FixAnnotationInput{
			Claims: model.FixActionClaims{
				Version:             model.FixActionTokenVersion,
				CursorEpoch:         mustIssueCursorEpoch(t, store),
				IssueID:             issueID,
				Snapshot:            commit.ProjectionGeneration,
				RetentionGeneration: mustRetentionGeneration(t, store),
				IssuedAt:            now,
				ExpiresAt:           now.Add(issueCursorLifetime),
			},
			ChangeKind:     model.FixChangeCode,
			RecordedVia:    model.FixRecordedViaLocalUI,
			IdempotencyKey: "00000000-0000-4000-8000-00000000993" + string(rune('0'+index)),
		})
		if err != nil {
			t.Fatal(err)
		}
		annotationIDs[index] = result.Annotation.AnnotationID
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx,
		"UPDATE issue_occurrences SET fingerprint_scope_id = NULL",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "DELETE FROM fix_monitoring_subjects"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `
		UPDATE local_migration_progress
		SET after_sequence = 0, complete = 0
		WHERE migration_version = 11
			AND phase IN ('occurrence_scope', 'monitoring_subject')`,
	); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(path, OpenOptions{
		KeyProvider: provider,
		Clock:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for index := range 2 {
		var occurrenceScope, subjectScope, mode string
		if err := reopened.db.QueryRowContext(ctx, `
			SELECT fingerprint_scope_id
			FROM issue_occurrences
			WHERE origin_record_id = ?`,
			findings[index].FindingID,
		).Scan(&occurrenceScope); err != nil {
			t.Fatal(err)
		}
		if err := reopened.db.QueryRowContext(ctx, `
			SELECT fingerprint_scope_id, negative_comparison_mode
			FROM fix_monitoring_subjects
			WHERE annotation_id = ?`,
			annotationIDs[index],
		).Scan(&subjectScope, &mode); err != nil {
			t.Fatal(err)
		}
		if occurrenceScope != scopes[index].ID ||
			subjectScope != scopes[index].ID ||
			mode != model.FixNegativeComparisonPositiveOnly {
			t.Fatalf(
				"Numbat hint %d occurrence/subject/mode = %q/%q/%q",
				index,
				occurrenceScope,
				subjectScope,
				mode,
			)
		}
	}
}
