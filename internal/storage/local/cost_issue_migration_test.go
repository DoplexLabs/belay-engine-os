package local

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestMigration015CostIssueSchemaConstraintsIndexesAndBackfill(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	provider := newMemoryKeyProvider()
	store, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{
		"transcript_project_analysis_state",
		"cost_issues",
		"correction_candidates",
		"project_issue_cost_totals",
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
		"transcript_project_analysis_dirty_idx",
		"cost_issues_rank_idx",
		"cost_issues_project_rank_idx",
		"cost_issues_detector_rank_idx",
		"correction_candidates_project_idx",
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
	).Scan(&migrations); err != nil || migrations != 27 {
		t.Fatalf("migration count/error = %d/%v, want 27", migrations, err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = withMutationTx(ctx, tx, mutationCostIssueAnalysis, func() error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO cost_issues (
				issue_id, detector_id, fingerprint, project_identity,
				wasted_minutes, wasted_tokens, wasted_usd,
				wasted_usd_known, lower_bound, session_count,
				first_seen, last_seen, payload, payload_encoding,
				created_at, updated_at
			) VALUES (
				'issue-invalid-known', 'retry_loop', 'fingerprint', 'project',
				1, 1, NULL, 1, 0, 1, ?, ?, X'01', ?, ?, ?
			)`,
			formatProjectionTime(time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)),
			formatProjectionTime(time.Date(2026, 9, 9, 10, 1, 0, 0, time.UTC)),
			payloadEncodingAESGCM,
			formatProjectionTime(time.Date(2026, 9, 9, 10, 2, 0, 0, time.UTC)),
			formatProjectionTime(time.Date(2026, 9, 9, 10, 2, 0, 0, time.UTC)),
		)
		return err
	})
	if err == nil {
		_ = tx.Rollback()
		t.Fatal("migration accepted known USD with NULL value")
	}
	_ = tx.Rollback()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO cost_issues (
			issue_id, detector_id, fingerprint, project_identity,
			wasted_minutes, wasted_tokens, wasted_usd,
			wasted_usd_known, lower_bound, session_count,
			first_seen, last_seen, payload, payload_encoding,
			created_at, updated_at
		) VALUES (
			'issue-unauthorized', 'retry_loop', 'fingerprint', 'project',
			1, 1, 1, 1, 0, 1, ?, ?, X'01', ?, ?, ?
		)`,
		formatProjectionTime(time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)),
		formatProjectionTime(time.Date(2026, 9, 9, 10, 1, 0, 0, time.UTC)),
		payloadEncodingAESGCM,
		formatProjectionTime(time.Date(2026, 9, 9, 10, 2, 0, 0, time.UTC)),
		formatProjectionTime(time.Date(2026, 9, 9, 10, 2, 0, 0, time.UTC)),
	); err == nil {
		t.Fatal("mutation guards accepted direct cost issue insertion")
	}

	session := transcriptTestSession(
		"ses_cost_issue_migration",
		transcript.CoverageComplete,
	)
	turn := transcriptTestTurn(
		"turn-cost-issue-migration",
		"source-cost-issue-migration",
		session.SessionKey,
		0,
		time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC),
		transcript.RoleUser,
		transcript.Payload{Text: "migration", JSONLByteOffset: 1},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{turn},
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP TABLE correction_candidates",
		"DROP TABLE cost_issues",
		"DROP TABLE transcript_project_analysis_state",
		"DELETE FROM schema_migrations WHERE version = 15",
	} {
		if _, err := raw.ExecContext(ctx, statement); err != nil {
			raw.Close()
			t.Fatalf("prepare migration 015 upgrade with %q: %v", statement, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var transcriptGeneration, analyzedGeneration int64
	if err := reopened.db.QueryRowContext(ctx, `
		SELECT transcript_generation, analyzed_generation
		FROM transcript_project_analysis_state
		WHERE project_identity = ?`,
		session.ProjectIdentity,
	).Scan(&transcriptGeneration, &analyzedGeneration); err != nil {
		t.Fatal(err)
	}
	if transcriptGeneration != 1 || analyzedGeneration != 0 {
		t.Fatalf(
			"backfilled generations = %d/%d, want 1/0",
			transcriptGeneration,
			analyzedGeneration,
		)
	}
	if _, err := reopened.db.ExecContext(ctx, `
		UPDATE transcript_project_analysis_state
		SET analyzed_generation = transcript_generation
		WHERE project_identity = ?`,
		session.ProjectIdentity,
	); err == nil {
		t.Fatal("mutation guards accepted direct project-state update")
	}
}
