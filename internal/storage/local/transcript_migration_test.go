package local

import (
	"context"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestMigration014TranscriptSchemaConstraintsAndIndexes(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)

	for _, table := range []string{"transcript_sessions", "transcript_turns"} {
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
		"transcript_sessions_project_idx",
		"transcript_sessions_coverage_idx",
		"transcript_turns_session_order_idx",
		"transcript_turns_session_role_idx",
		"transcript_turns_session_tool_idx",
		"transcript_turns_occurred_idx",
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
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO transcript_sessions (
			session_key, agent, native_session_id, project_path, git_remote_url,
			project_identity, total_tokens, coverage, created_at, updated_at
		) VALUES (
			'ses_invalid_total', 'codex', 'native-invalid', '/tmp/project', '',
			'/tmp/project', -1, 'complete', ?, ?
		)`,
		formatProjectionTime(time.Date(2026, 9, 9, 14, 59, 0, 0, time.UTC)),
		formatProjectionTime(time.Date(2026, 9, 9, 14, 59, 0, 0, time.UTC)),
	); err == nil {
		t.Fatal("migration accepted negative total_tokens")
	}

	session := transcriptTestSession("ses_migration_constraints", transcript.CoverageComplete)
	turn := transcriptTestTurn(
		"turn-valid",
		"source-valid",
		session.SessionKey,
		0,
		time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC),
		transcript.RoleUser,
		transcript.Payload{JSONLByteOffset: 0},
	)
	if _, err := store.AppendTranscriptBatch(ctx, session, []transcript.Turn{turn}); err != nil {
		t.Fatal(err)
	}

	invalidRows := []struct {
		name      string
		turnID    string
		sourceKey string
		role      string
		input     any
		cost      any
		encoding  string
	}{
		{
			name: "role", turnID: "turn-invalid-role", sourceKey: "source-invalid-role",
			role: "unknown", encoding: payloadEncodingAESGCM,
		},
		{
			name: "tokens", turnID: "turn-invalid-tokens", sourceKey: "source-invalid-tokens",
			role: string(transcript.RoleUser), input: -1, encoding: payloadEncodingAESGCM,
		},
		{
			name: "cost", turnID: "turn-invalid-cost", sourceKey: "source-invalid-cost",
			role: string(transcript.RoleUser), cost: -0.01, encoding: payloadEncodingAESGCM,
		},
		{
			name: "encoding", turnID: "turn-invalid-encoding", sourceKey: "source-invalid-encoding",
			role: string(transcript.RoleUser), encoding: "plaintext.v0",
		},
	}
	for _, test := range invalidRows {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.db.ExecContext(ctx, `
				INSERT INTO transcript_turns (
					turn_id, source_record_key, session_key, turn_index,
					occurred_at, role, tool_name, model, input_tokens,
					cost_usd, payload, payload_encoding, created_at
				) VALUES (?, ?, ?, 1, ?, ?, '', '', ?, ?, X'01', ?, ?)`,
				test.turnID,
				test.sourceKey,
				session.SessionKey,
				formatProjectionTime(turn.OccurredAt),
				test.role,
				test.input,
				test.cost,
				test.encoding,
				formatProjectionTime(turn.OccurredAt),
			)
			if err == nil {
				t.Fatalf("migration accepted invalid %s", test.name)
			}
		})
	}

	payload, encoding := transcriptTurnPayloadRow(t, store, turn.TurnID)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO transcript_turns (
			turn_id, source_record_key, session_key, turn_index,
			occurred_at, role, tool_name, model, payload,
			payload_encoding, created_at
		) VALUES (?, ?, ?, 2, ?, ?, '', '', ?, ?, ?)`,
		"turn-duplicate-source",
		turn.SourceRecordKey,
		session.SessionKey,
		formatProjectionTime(turn.OccurredAt),
		transcript.RoleUser,
		payload,
		encoding,
		formatProjectionTime(turn.OccurredAt),
	); err == nil {
		t.Fatal("migration accepted duplicate source_record_key")
	}
}
