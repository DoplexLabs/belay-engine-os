package local

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestMigration017FreshSchemaAndMutationGuards(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)

	for _, table := range []string{
		"experience_candidates",
		"experience_semantic_proposals",
		"experience_semantic_decisions",
		"experiences",
		"experience_transitions",
		"experience_evidence",
		"trajectory_edges",
		"outcome_observations",
		"experience_applications",
		"experience_generations",
		"experience_review_actions",
		"mission_pack_previews",
		"mission_pack_receipts",
		"mission_pack_receipt_applications",
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

	var migrations, triggers int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_temp_schema
		WHERE type = 'trigger' AND name LIKE 'belay_guard_%'`,
	).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if migrations != 30 || triggers != 125 {
		t.Fatalf("migrations/triggers = %d/%d, want 30/125", migrations, triggers)
	}

	var decisionProjectIndex string
	if err := store.db.QueryRowContext(ctx, `
		SELECT sql
		FROM sqlite_schema
		WHERE type = 'index'
			AND name = 'experience_semantic_decisions_project_idx'`,
	).Scan(&decisionProjectIndex); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(decisionProjectIndex, "project_identity") {
		t.Fatalf(
			"semantic decision project index = %q",
			decisionProjectIndex,
		)
	}

	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO experience_candidates (
			candidate_id, project_identity, family, lifecycle_state,
			instruction_authority, created_at, payload, payload_encoding,
			inserted_at
		) VALUES (
			'exc_guard', 'project', 'correction', 'candidate', 'none',
			'2026-09-10T12:00:00Z', X'00', 'aes256gcm.v1',
			'2026-09-10T12:00:00Z'
		)`); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("direct candidate insert error = %v, want mutation guard rejection", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO experience_semantic_proposals (
			proposal_id, candidate_id, project_identity, experience_type,
			scope_kind, harness, prompt_version, input_hash, output_hash,
			generated_at, payload, payload_encoding, inserted_at
		) VALUES (
			'exs_guard', 'exc_guard', 'project', 'preference', 'project',
			'claude', 'belay.experience-prompt.v1',
			'sha256:0000000000000000000000000000000000000000000000000000000000000000',
			'sha256:0000000000000000000000000000000000000000000000000000000000000000',
			'2026-09-10T12:00:00Z', X'00', 'aes256gcm.v1',
			'2026-09-10T12:00:00Z'
		)`); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf(
			"direct semantic proposal insert error = %v, want mutation guard rejection",
			err,
		)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO experience_semantic_decisions (
			decision_id, candidate_id, project_identity, disposition,
			reason_code, proposal_id, harness, prompt_version, input_hash,
			output_hash, generated_at, payload, payload_encoding, inserted_at
		) VALUES (
			'exd_guard', 'exc_guard', 'project', 'defer',
			'insufficient_context', NULL, 'claude',
			'belay.experience-prompt.v1',
			'sha256:0000000000000000000000000000000000000000000000000000000000000000',
			'sha256:0000000000000000000000000000000000000000000000000000000000000000',
			'2026-09-10T12:00:00Z', X'00', 'aes256gcm.v1',
			'2026-09-10T12:00:00Z'
		)`); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf(
			"direct semantic decision insert error = %v, want mutation guard rejection",
			err,
		)
	}
}

func TestMigration017Upgrades016WithoutChangingExistingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "migration-016.sqlite")
	db, err := openGuardedSQLite(mustSQLiteDSN(t, path))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	version := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") ||
			entry.Name() == "017_experience_foundation.sql" ||
			entry.Name() == "018_trajectory_derivation_state.sql" ||
			entry.Name() == "019_experience_semantic_proposals.sql" ||
			entry.Name() == "020_experience_semantic_decisions.sql" ||
			entry.Name() == "021_mission_pack_previews.sql" ||
			entry.Name() == "022_experience_review_actions.sql" ||
			entry.Name() == "023_mission_pack_receipt_applications.sql" ||
			entry.Name() == "024_experience_impact_observations.sql" ||
			entry.Name() == "025_issue_cost_attribution.sql" ||
			entry.Name() == "026_habit_debriefs.sql" ||
			entry.Name() == "027_transcript_price_versions.sql" ||
			entry.Name() == "028_cost_issue_analysis_version.sql" ||
			entry.Name() == "029_session_identity_observations.sql" ||
			entry.Name() == "030_evidence_episodes.sql" {
			continue
		}
		version++
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", entry.Name(), err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO schema_migrations(version, applied_at)
			VALUES (?, ?)`,
			version,
			formatProjectionTime(time.Date(2026, 9, 10, 9, 0, version, 0, time.UTC)),
		); err != nil {
			t.Fatalf("record %s: %v", entry.Name(), err)
		}
	}
	if version != 16 {
		t.Fatalf("pre-upgrade migration count = %d, want 16", version)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO import_runs (
			source_run_id, status, complete, first_seen_at, updated_at
		) VALUES ('run_before_017', 'complete', 1, ?, ?)`,
		formatProjectionTime(time.Date(2026, 9, 10, 9, 30, 0, 0, time.UTC)),
		formatProjectionTime(time.Date(2026, 9, 10, 9, 31, 0, 0, time.UTC)),
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path, newMemoryKeyProvider())
	if err != nil {
		t.Fatalf("Open() upgraded migration 016 store: %v", err)
	}
	defer store.Close()

	var migrations, existing int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM import_runs
		WHERE source_run_id = 'run_before_017'
			AND status = 'complete' AND complete = 1`,
	).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if migrations != 30 || existing != 1 {
		t.Fatalf("post-upgrade migrations/existing rows = %d/%d, want 30/1", migrations, existing)
	}
}
