package local

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestMigration012FreshPersistsRandomEpochAndReadyCurrentState(t *testing.T) {
	randomBytes := bytes.Repeat([]byte{0x42}, 4096)
	store, err := OpenWithOptions(
		filepath.Join(t.TempDir(), "fresh.sqlite"),
		OpenOptions{
			KeyProvider: newMemoryKeyProvider(),
			Random:      bytes.NewReader(randomBytes),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var migrations, summaries, coverage int
	var epoch, readiness string
	var current, build, materialized, oldest int64
	if err := store.db.QueryRow(`
		SELECT COUNT(*) FROM schema_migrations`,
	).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`
		SELECT cursor_epoch, readiness, build_generation,
			materialized_generation, oldest_materialized_generation
		FROM issue_summary_metadata WHERE singleton = 1`,
	).Scan(&epoch, &readiness, &build, &materialized, &oldest); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`
		SELECT current_generation FROM issue_projection_metadata
		WHERE singleton = 1`,
	).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(
		"SELECT COUNT(*) FROM issue_summary_revisions",
	).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(
		"SELECT COUNT(*) FROM issue_analysis_coverage_revisions",
	).Scan(&coverage); err != nil {
		t.Fatal(err)
	}
	wantEpoch := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	if migrations != 27 ||
		epoch != wantEpoch ||
		readiness != "ready" ||
		build != current ||
		materialized != current ||
		oldest != current ||
		summaries != 0 ||
		coverage != 1 {
		t.Fatalf(
			"fresh migration = migrations=%d epoch=%q readiness=%q generations=%d/%d/%d/%d summaries=%d coverage=%d",
			migrations, epoch, readiness, current, build, materialized, oldest,
			summaries, coverage,
		)
	}
}

func TestMigration012UpgradeMaterializesCurrentStateAndReopenKeepsEpoch(t *testing.T) {
	path, provider, issueIDs, generation := createIssueSummaryMigrationFixture(t)
	removeMigration012(t, path)

	store, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	epoch := mustIssueCursorEpoch(t, store)
	assertIssueSummaryReady(t, store, generation, len(issueIDs))
	assertIssueSummaryRelations(t, store, len(issueIDs))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := mustIssueCursorEpoch(t, reopened); got != epoch {
		t.Fatalf("reopened epoch = %q, want %q", got, epoch)
	}
	assertIssueSummaryReady(t, reopened, generation, len(issueIDs))
}

func TestMigration012ResumesAfterDDLBetweenBatchesAndBeforeReady(t *testing.T) {
	t.Run("after DDL", func(t *testing.T) {
		path, provider, issueIDs, generation := createIssueSummaryMigrationFixture(t)
		removeMigration012(t, path)
		raw := openRawMigrationDB(t, path)
		applyMigration012DDL(t, raw)
		raw.Close()

		store, err := Open(path, provider)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		assertIssueSummaryReady(t, store, generation, len(issueIDs))
	})

	t.Run("between batches", func(t *testing.T) {
		path, provider, issueIDs, generation := createIssueSummaryMigrationFixture(t)
		raw := openRawMigrationDB(t, path)
		var firstRow int64
		var firstIssue string
		if err := raw.QueryRow(`
			SELECT rowid, issue_id FROM issue_occurrences
			WHERE visible_until_generation IS NULL
			ORDER BY rowid LIMIT 1`,
		).Scan(&firstRow, &firstIssue); err != nil {
			t.Fatal(err)
		}
		now := formatProjectionTime(time.Now().UTC())
		for _, operation := range []struct {
			statement string
			args      []any
		}{
			{"DELETE FROM issue_summary_revisions WHERE issue_id <> ?", []any{firstIssue}},
			{"DELETE FROM local_migration_progress WHERE migration_version = 12", nil},
			{`UPDATE issue_summary_metadata
			 SET readiness = 'building', build_generation = ?,
				materialized_generation = 0, oldest_materialized_generation = 0
			 WHERE singleton = 1`, []any{generation}},
			{`INSERT INTO local_migration_progress
				(migration_version, phase, after_sequence, complete, updated_at)
			 VALUES (12, 'prepare', 0, 1, ?)`, []any{now}},
			{`INSERT INTO local_migration_progress
				(migration_version, phase, after_sequence, complete, updated_at)
			 VALUES (12, 'summaries', ?, 0, ?)`, []any{firstRow, now}},
		} {
			if _, err := raw.Exec(operation.statement, operation.args...); err != nil {
				t.Fatal(err)
			}
		}
		raw.Close()

		store, err := Open(path, provider)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		assertIssueSummaryReady(t, store, generation, len(issueIDs))
	})

	t.Run("before ready", func(t *testing.T) {
		path, provider, issueIDs, generation := createIssueSummaryMigrationFixture(t)
		raw := openRawMigrationDB(t, path)
		now := formatProjectionTime(time.Now().UTC())
		if _, err := raw.Exec(`
			DELETE FROM local_migration_progress
			WHERE migration_version = 12 AND phase = 'ready';
			UPDATE issue_summary_metadata
			SET readiness = 'building', build_generation = ?,
				materialized_generation = 0, oldest_materialized_generation = 0;
			INSERT OR REPLACE INTO local_migration_progress
				(migration_version, phase, after_sequence, complete, updated_at)
			VALUES
				(12, 'prepare', 0, 1, ?),
				(12, 'summaries', 0, 1, ?),
				(12, 'coverage', 0, 1, ?)`,
			generation, now, now, now,
		); err != nil {
			t.Fatal(err)
		}
		raw.Close()

		store, err := Open(path, provider)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		assertIssueSummaryReady(t, store, generation, len(issueIDs))
	})
}

func TestMigration012RepairsOlderBinaryGenerationDriftWithoutRotatingEpoch(
	t *testing.T,
) {
	path, provider, issueIDs, generation := createIssueSummaryMigrationFixture(t)
	store, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	epoch := mustIssueCursorEpoch(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	raw := openRawMigrationDB(t, path)
	if _, err := raw.Exec(`
		UPDATE issue_projection_metadata
		SET current_generation = current_generation + 1
		WHERE singleton = 1`,
	); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	reopened, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertIssueSummaryReady(t, reopened, generation+1, len(issueIDs))
	if got := mustIssueCursorEpoch(t, reopened); got != epoch {
		t.Fatalf("drift repair rotated epoch: %q != %q", got, epoch)
	}
}

func TestMigration012GuardsMaterializedState(t *testing.T) {
	store := openStorageTestStore(t)
	if _, err := store.db.Exec(`
		UPDATE issue_summary_metadata SET readiness = 'failed'
		WHERE singleton = 1`,
	); err == nil {
		t.Fatal("unauthorized issue summary metadata update succeeded")
	}
	if _, err := store.db.Exec(
		"DELETE FROM issue_analysis_coverage_revisions",
	); err == nil {
		t.Fatal("unauthorized issue coverage deletion succeeded")
	}
}

func createIssueSummaryMigrationFixture(
	t *testing.T,
) (string, *memoryKeyProvider, []string, int64) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "summary.sqlite")
	provider := newMemoryKeyProvider()
	store, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	var issueIDs []string
	var generation int64
	for index := 0; index < 2; index++ {
		sessionID := "session-summary-" + string(rune('a'+index))
		eventID := "00000000-0000-7000-8000-00000000095" + string(rune('0'+index))
		event := storageTestEvent(eventID, sessionID, 1, now.Add(time.Duration(index)*time.Minute))
		_, err := store.AppendEventResolved(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, issueID, err := store.DeriveIssueIdentity(
			"failure.v1", "explicit_command_failure", "scope-"+sessionID, "failure",
		)
		if err != nil {
			t.Fatal(err)
		}
		occurrence := testIssueOccurrence(
			sessionID, "codex", eventID, fingerprint, issueID,
			"medium", "high", event.OccurredAt, model.ScopeResolved,
		)
		commit, err := store.ReplaceIssueProjection(ctx, ProjectionReplacement{
			SessionKey:        sessionID,
			ClaimedGeneration: dirtyTargetGeneration(t, store, sessionID),
			Status:            model.AnalysisCurrent,
			ScopeQuality:      model.ScopeResolved,
			Occurrences:       []model.IssueOccurrence{occurrence},
		})
		if err != nil {
			t.Fatal(err)
		}
		generation = commit.ProjectionGeneration
		issueIDs = append(issueIDs, issueID)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	return path, provider, issueIDs, generation
}

func removeMigration012(t *testing.T, path string) {
	t.Helper()
	raw := openRawMigrationDB(t, path)
	for _, statement := range []string{
		"DROP TABLE issue_summary_harnesses",
		"DROP TABLE issue_summary_sessions",
		"DROP TABLE issue_summary_revisions",
		"DROP TABLE issue_analysis_coverage_revisions",
		"DROP TABLE issue_projection_generation_times",
		"DROP TABLE issue_summary_metadata",
		"DELETE FROM local_migration_progress WHERE migration_version = 12",
		"DELETE FROM schema_migrations WHERE version = 12",
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("remove migration 012 with %q: %v", statement, err)
		}
	}
	raw.Close()
}

func applyMigration012DDL(t *testing.T, raw *sql.DB) {
	t.Helper()
	body, err := migrationFiles.ReadFile("migrations/012_issue_summary_projection.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(string(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`
		INSERT INTO schema_migrations(version, applied_at) VALUES (12, ?)`,
		formatProjectionTime(time.Now().UTC()),
	); err != nil {
		t.Fatal(err)
	}
}

func openRawMigrationDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA foreign_keys = ON"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	return raw
}

func assertIssueSummaryReady(
	t *testing.T,
	store *Store,
	generation int64,
	summaryCount int,
) {
	t.Helper()
	var readiness string
	var build, materialized, oldest int64
	var summaries, coverage int
	if err := store.db.QueryRow(`
		SELECT readiness, build_generation, materialized_generation,
			oldest_materialized_generation
		FROM issue_summary_metadata WHERE singleton = 1`,
	).Scan(&readiness, &build, &materialized, &oldest); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(
		"SELECT COUNT(*) FROM issue_summary_revisions",
	).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(
		"SELECT COUNT(*) FROM issue_analysis_coverage_revisions",
	).Scan(&coverage); err != nil {
		t.Fatal(err)
	}
	if readiness != "ready" ||
		build != generation ||
		materialized != generation ||
		oldest != generation ||
		summaries != summaryCount ||
		coverage < 1 {
		t.Fatalf(
			"summary readiness=%q generations=%d/%d/%d want=%d summaries=%d/%d coverage=%d",
			readiness, build, materialized, oldest, generation,
			summaries, summaryCount, coverage,
		)
	}
}

func assertIssueSummaryRelations(t *testing.T, store *Store, want int) {
	t.Helper()
	for _, table := range []string{"issue_summary_harnesses", "issue_summary_sessions"} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d", table, count, want)
		}
	}
}
