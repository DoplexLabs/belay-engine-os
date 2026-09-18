package local

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestMutationGuardsRemainValidAcrossStoresAndConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	provider := newMemoryKeyProvider()
	first, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	stores := []*Store{first, second}
	errs := make(chan error, len(stores)*5)
	var wait sync.WaitGroup
	for storeIndex, store := range stores {
		for eventIndex := range 5 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				unique := storeIndex*10 + eventIndex + 1
				event := storageTestEvent(
					fmt.Sprintf("00000000-0000-7000-8000-%012d", unique),
					fmt.Sprintf("session-multi-store-%d", storeIndex),
					int64(eventIndex+1),
					time.Date(2026, 9, 8, 18, 0, unique, 0, time.UTC),
				)
				event.Source.RecordID = fmt.Sprintf("multi-store-%d", unique)
				event.Source.DeduplicationKey = fmt.Sprintf("sha256:%064x", unique)
				if _, err := store.AppendEventResolved(ctx, event); err != nil {
					errs <- err
				}
			}()
		}
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent append error = %v", err)
	}
	if t.Failed() {
		return
	}
	for index, store := range stores {
		if _, err := store.db.ExecContext(ctx,
			"UPDATE events SET action = 'unauthorized' WHERE session_key = ?",
			fmt.Sprintf("session-multi-store-%d", index),
		); err == nil {
			t.Fatalf("store %d allowed unauthorized update", index)
		}
		if _, err := store.MarkSessionDirty(
			ctx,
			fmt.Sprintf("session-multi-store-%d", index),
			"multi_store_test",
		); err != nil {
			t.Fatalf("store %d authorized projection mutation failed: %v", index, err)
		}
	}
}

func TestMutationGuardsAreInstalledOnEveryPooledConnection(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000180",
		"session-pooled-guards",
		1,
		time.Date(2026, 9, 8, 19, 0, 0, 0, time.UTC),
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}
	store.db.SetMaxOpenConns(4)

	connections := make([]*sql.Conn, 0, 4)
	for range 4 {
		connection, err := store.db.Conn(ctx)
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
		var recursiveTriggers int
		if err := connection.QueryRowContext(
			ctx,
			"PRAGMA recursive_triggers",
		).Scan(&recursiveTriggers); err != nil {
			t.Fatalf("connection %d inspect recursive triggers: %v", index, err)
		}
		if recursiveTriggers != 1 {
			t.Fatalf(
				"connection %d recursive_triggers = %d, want 1",
				index,
				recursiveTriggers,
			)
		}
		var triggerCount int
		if err := connection.QueryRowContext(ctx, `
				SELECT COUNT(*)
			FROM sqlite_temp_schema
			WHERE type = 'trigger' AND name LIKE 'belay_guard_%'`,
		).Scan(&triggerCount); err != nil {
			t.Fatalf("connection %d inspect guards: %v", index, err)
		}
		if triggerCount != 116 {
			t.Fatalf("connection %d guard count = %d, want 116", index, triggerCount)
		}
		if _, err := connection.ExecContext(ctx,
			"UPDATE events SET action = 'unauthorized' WHERE event_id = ?",
			event.EventID,
		); err == nil {
			t.Fatalf("connection %d allowed unauthorized update", index)
		}
		if _, err := connection.ExecContext(ctx,
			"DELETE FROM events WHERE event_id = ?",
			event.EventID,
		); err == nil {
			t.Fatalf("connection %d allowed unauthorized delete", index)
		}
	}
}

func TestMigrationDropsLegacyPersistentMutationTriggers(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	var persistent int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM main.sqlite_schema
		WHERE type = 'trigger'
			AND name IN (
				'events_no_update',
				'events_no_delete',
				'findings_no_update',
				'findings_no_delete',
				'issue_occurrences_guard_update',
				'issue_occurrences_guard_delete',
				'session_analysis_revisions_guard_update',
				'session_analysis_revisions_guard_delete',
				'analysis_diagnostics_guard_update',
				'analysis_diagnostics_guard_delete'
			)`,
	).Scan(&persistent); err != nil {
		t.Fatal(err)
	}
	if persistent != 0 {
		t.Fatalf("persistent mutation trigger count = %d, want 0", persistent)
	}
}

func TestTranscriptMutationGuardsRequireTranscriptPurpose(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	session := transcriptTestSession(
		"ses_transcript_mutation_guards",
		transcript.CoverageLive,
	)
	turn := transcriptTestTurn(
		"turn-transcript-mutation-guards",
		"source-transcript-mutation-guards",
		session.SessionKey,
		0,
		time.Date(2026, 9, 9, 19, 30, 0, 0, time.UTC),
		transcript.RoleSystem,
		transcript.Payload{JSONLByteOffset: 0},
	)
	if _, err := store.AppendTranscriptBatch(ctx, session, []transcript.Turn{turn}); err != nil {
		t.Fatal(err)
	}

	for _, statement := range []string{
		"UPDATE transcript_turns SET turn_index = turn_index + 1",
		"DELETE FROM transcript_turns",
		"UPDATE transcript_sessions SET turn_count = 0",
		"DELETE FROM transcript_sessions",
	} {
		if _, err := store.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("unauthorized transcript mutation succeeded: %s", statement)
		}
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationTranscriptRetention, func() error {
		_, err := tx.ExecContext(
			ctx,
			"DELETE FROM transcript_sessions WHERE session_key = ?",
			session.SessionKey,
		)
		return err
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var sessions, turns int
	if err := store.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM transcript_sessions),
			(SELECT COUNT(*) FROM transcript_turns)`,
	).Scan(&sessions, &turns); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || turns != 0 {
		t.Fatalf("retention delete left sessions/turns = %d/%d", sessions, turns)
	}
}
