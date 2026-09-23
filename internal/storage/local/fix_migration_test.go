package local

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMigration010FreshSchemaConstraintsAndIndexes(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	for _, table := range []string{
		"fix_annotations",
		"fix_annotation_events",
		"fix_annotation_retractions",
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
	for _, index := range []string{
		"fix_annotations_issue_order_idx",
		"fix_annotations_fingerprint_monitor_idx",
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sqlite_schema
			WHERE type = 'index' AND name = ?`,
			index,
		).Scan(&count); err != nil || count != 1 {
			t.Fatalf("index %s count/error = %d/%v", index, count, err)
		}
	}
	var migrations int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrations); err != nil || migrations != 30 {
		t.Fatalf("migration count/error = %d/%v, want 30", migrations, err)
	}

	now := formatProjectionTime(time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO fix_annotations (
			annotation_id, request_fingerprint, issue_id, anchor_revision_id,
			anchor_occurrence_id, anchor_session_id, fingerprint_id,
			fingerprint_version, origin, detector_id, detector_version,
			scope_quality, issue_snapshot_generation, anchor_analysis_generation,
			anchor_first_observed_at, anchor_last_observed_at,
			baseline_citation_count, change_kind, change_catalog_version,
			recorded_via, recorded_at, monitor_from
		) VALUES (
			'fxa_invalid', 'fxp_invalid', 'iss_invalid', 'ior_invalid',
			'occ_invalid', 'session', 'ifp_invalid', 'v1', 'belay', 'detector',
			'v1', 'resolved', 1, 1, ?, ?, 0, 'free_text',
			'fix-change.v1', 'local_ui', ?, ?
		)`,
		now,
		now,
		now,
		now,
	); err == nil {
		t.Fatal("migration accepted an unbounded change kind")
	}
}

func TestMigration010UpgradesMigration009AndInstallsConditionalGuards(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "migration-009.sqlite")
	db, err := openGuardedSQLite(mustSQLiteDSN(t, path))
	if err != nil {
		t.Fatal(err)
	}
	migrations := []string{
		"001_initial.sql",
		"002_encrypted_payloads_and_lifecycle.sql",
		"003_stable_read_order.sql",
		"004_findings_session_filter.sql",
		"005_issue_projection.sql",
		"006_projection_timestamp_encoding.sql",
		"007_numbat_finding_project_scope_hint.sql",
		"008_connection_local_mutation_guards.sql",
		"009_issue_attention_metadata.sql",
	}
	for index, name := range migrations {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO schema_migrations(version, applied_at)
			VALUES (?, ?)`,
			index+1,
			formatProjectionTime(time.Date(2026, 9, 8, 12, 0, index, 0, time.UTC)),
		); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path, newMemoryKeyProvider())
	if err != nil {
		t.Fatalf("Open() upgraded migration 009 store: %v", err)
	}
	defer store.Close()
	var migrationsApplied, triggerCount int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrationsApplied); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_temp_schema
		WHERE type = 'trigger' AND name LIKE 'belay_guard_%'`,
	).Scan(&triggerCount); err != nil {
		t.Fatal(err)
	}
	if migrationsApplied != 30 || triggerCount != 125 {
		t.Fatalf(
			"upgraded migrations/triggers = %d/%d, want 30/125",
			migrationsApplied,
			triggerCount,
		)
	}
	var recursiveTriggers int
	if err := store.db.QueryRowContext(
		ctx,
		"PRAGMA recursive_triggers",
	).Scan(&recursiveTriggers); err != nil {
		t.Fatal(err)
	}
	if recursiveTriggers != 1 {
		t.Fatalf("upgraded recursive_triggers = %d, want 1", recursiveTriggers)
	}

	now := formatProjectionTime(time.Date(2026, 9, 8, 12, 30, 0, 0, time.UTC))
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO fix_annotations (
			annotation_id, request_fingerprint, issue_id, anchor_revision_id,
			anchor_occurrence_id, anchor_session_id, fingerprint_id,
			fingerprint_version, origin, detector_id, detector_version,
			scope_quality, issue_snapshot_generation, anchor_analysis_generation,
			anchor_first_observed_at, anchor_last_observed_at,
			baseline_citation_count, change_kind, change_catalog_version,
			recorded_via, recorded_at, monitor_from
		) VALUES (
			'fxa_upgrade_guard', 'fxp_upgrade_guard', 'iss_upgrade_guard',
			'ior_upgrade_guard', 'occ_upgrade_guard', 'session-upgrade-guard',
			'ifp_upgrade_guard', '1', 'belay', 'explicit_command_failure', '1',
			'resolved', 1, 1, ?, ?, 0, 'code_change', 'fix-change.v1',
			'local_ui', ?, ?
		)`,
		now,
		now,
		now,
		now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO fix_annotation_retractions (
			retraction_id, request_fingerprint, annotation_id, reason,
			recorded_via, retracted_at
		) VALUES (
			'fxr_upgrade_guard', 'fxp_upgrade_retraction_guard',
			'fxa_upgrade_guard', 'recorded_by_mistake', 'local_ui', ?
		)`,
		now,
	); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []struct {
		table    string
		idColumn string
		id       string
	}{
		{"fix_annotations", "annotation_id", "fxa_upgrade_guard"},
		{"fix_annotation_retractions", "retraction_id", "fxr_upgrade_guard"},
	} {
		query := "INSERT OR REPLACE INTO " + replacement.table +
			" SELECT * FROM " + replacement.table +
			" WHERE " + replacement.idColumn + " = ?"
		if _, err := store.db.ExecContext(ctx, query, replacement.id); err == nil {
			t.Fatalf("upgraded store allowed replacement of %s", replacement.table)
		}
	}
}
