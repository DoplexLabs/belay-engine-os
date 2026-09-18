package local

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestAppendOnlyEnforcementOutsideControlledPrune(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000010",
		"session-append-only",
		1,
		time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC),
	)
	if _, err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	finding := Finding{
		FindingID:     "finding-append-only",
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    event.OccurredAt,
		RuleID:        "rule.append-only",
		RuleVersion:   "1",
		Severity:      "low",
		SourceAgent:   "codex",
		Confidence:    "high",
		CitedEventIDs: []string{event.EventID},
	}
	if _, err := store.RecordFinding(ctx, finding); err != nil {
		t.Fatalf("RecordFinding() error = %v", err)
	}

	statements := []string{
		"UPDATE events SET action = 'changed' WHERE event_id = '" + event.EventID + "'",
		"DELETE FROM events WHERE event_id = '" + event.EventID + "'",
		"UPDATE findings SET severity = 'high' WHERE finding_id = '" + finding.FindingID + "'",
		"DELETE FROM findings WHERE finding_id = '" + finding.FindingID + "'",
	}
	for _, statement := range statements {
		if _, err := store.db.ExecContext(ctx, statement); err == nil {
			t.Errorf("ordinary mutation unexpectedly succeeded: %s", statement)
		}
	}

	result, err := store.Prune(ctx, RetentionPolicy{MaxEventCount: 1}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if result.PrunedEventCount != 0 || result.PrunedFindingCount != 0 {
		t.Fatalf("non-eligible prune removed records: %+v", result)
	}
	injected := errors.New("injected mutation failure")
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationRetentionPrune, func() error {
		return injected
	}); !errors.Is(err, injected) {
		t.Fatalf("mutation authorization failure = %v", err)
	}
	var transactionAuthorization int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM temp.belay_mutation_authorization",
	).Scan(&transactionAuthorization); err != nil {
		t.Fatal(err)
	}
	if transactionAuthorization != 0 {
		t.Fatal("mutation authorization remained active in failed transaction")
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM events WHERE event_id = ?",
		event.EventID,
	); err == nil {
		t.Fatal("failed transaction leaked mutation authorization")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var activeAuthorization int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM temp.belay_mutation_authorization",
	).Scan(&activeAuthorization); err != nil {
		t.Fatal(err)
	}
	if activeAuthorization != 0 {
		t.Fatal("mutation authorization remained active after failure")
	}
}

func TestPruneUsesDeterministicOldestFirstEventOrdering(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	early := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	latest := late.Add(time.Hour)
	events := []struct {
		id       string
		sequence int64
		at       time.Time
	}{
		{"00000000-0000-7000-8000-000000000013", 2, early},
		{"00000000-0000-7000-8000-000000000015", 1, latest},
		{"00000000-0000-7000-8000-000000000012", 1, early},
		{"00000000-0000-7000-8000-000000000014", 1, late},
		{"00000000-0000-7000-8000-000000000011", 1, early},
	}
	for _, item := range events {
		event := storageTestEvent(item.id, "session-prune-order", item.sequence, item.at)
		if _, err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent(%s) error = %v", item.id, err)
		}
	}

	result, err := store.Prune(ctx, RetentionPolicy{MaxEventCount: 2}, latest.Add(time.Hour))
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if result.PrunedEventCount != 3 {
		t.Fatalf("pruned events = %d, want 3", result.PrunedEventCount)
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT event_id FROM events
		ORDER BY occurred_at, source_sequence, event_id`)
	if err != nil {
		t.Fatalf("query remaining events: %v", err)
	}
	defer rows.Close()
	var remaining []string
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			t.Fatalf("scan remaining event: %v", err)
		}
		remaining = append(remaining, eventID)
	}
	want := []string{
		"00000000-0000-7000-8000-000000000014",
		"00000000-0000-7000-8000-000000000015",
	}
	if !slices.Equal(remaining, want) {
		t.Fatalf("remaining events = %q, want %q", remaining, want)
	}
	if _, err := store.db.ExecContext(
		ctx,
		"DELETE FROM events WHERE event_id = ?",
		want[0],
	); err == nil {
		t.Fatal("ordinary delete succeeded after controlled prune completed")
	}
}

func TestRetentionAgeAndByteBoundsCoverEventsAndFindings(t *testing.T) {
	t.Run("age", func(t *testing.T) {
		ctx := context.Background()
		store := openStorageTestStore(t)
		now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
		old := storageTestEvent(
			"00000000-0000-7000-8000-000000000020",
			"session-age",
			1,
			now.Add(-48*time.Hour),
		)
		recent := storageTestEvent(
			"00000000-0000-7000-8000-000000000021",
			"session-age",
			2,
			now.Add(-time.Hour),
		)
		if _, err := store.AppendEvent(ctx, old); err != nil {
			t.Fatalf("append old event: %v", err)
		}
		if _, err := store.AppendEvent(ctx, recent); err != nil {
			t.Fatalf("append recent event: %v", err)
		}
		if _, err := store.RecordFinding(ctx, Finding{
			FindingID:     "finding-old",
			SourceRunID:   old.Source.RunID,
			SessionKey:    old.Session.Key,
			DetectedAt:    old.OccurredAt,
			RuleID:        "rule.age",
			RuleVersion:   "1",
			Severity:      "low",
			SourceAgent:   "codex",
			Confidence:    "high",
			CitedEventIDs: []string{old.EventID},
		}); err != nil {
			t.Fatalf("record old finding: %v", err)
		}
		result, err := store.Prune(ctx, RetentionPolicy{MaxAge: 24 * time.Hour}, now)
		if err != nil {
			t.Fatalf("Prune() error = %v", err)
		}
		if result.PrunedEventCount != 1 || result.PrunedFindingCount != 1 {
			t.Fatalf("age prune = %+v", result)
		}
	})

	t.Run("bytes", func(t *testing.T) {
		ctx := context.Background()
		store := openStorageTestStore(t)
		now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
		for index, id := range []string{
			"00000000-0000-7000-8000-000000000030",
			"00000000-0000-7000-8000-000000000031",
		} {
			event := storageTestEvent(id, "session-bytes", int64(index+1), now.Add(time.Duration(index)*time.Minute))
			if _, err := store.AppendEvent(ctx, event); err != nil {
				t.Fatalf("AppendEvent() error = %v", err)
			}
		}
		items, err := readRetentionItems(ctx, store.db)
		if err != nil {
			t.Fatalf("readRetentionItems() error = %v", err)
		}
		var total int64
		for _, item := range items {
			total += item.bytes
		}
		policy := RetentionPolicy{MaxPayloadBytes: total - items[0].bytes}
		result, err := store.Prune(ctx, policy, now.Add(time.Hour))
		if err != nil {
			t.Fatalf("Prune() error = %v", err)
		}
		if result.PrunedEventCount != 1 ||
			result.AfterPayloadBytes > policy.MaxPayloadBytes {
			t.Fatalf("byte prune = %+v", result)
		}
	})
}

func TestRetentionAgePrunesTranscriptTurnsAndRefreshesSessions(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 9, 15, 0, 0, 0, time.UTC)
	session := transcriptTestSession(
		"ses_retention_transcript_age",
		transcript.CoverageComplete,
	)
	oldTokens := int64(10)
	recentTokens := int64(20)
	old := transcriptTestTurn(
		"turn-retention-age-old",
		"source-retention-age-old",
		session.SessionKey,
		0,
		now.Add(-48*time.Hour),
		transcript.RoleUser,
		transcript.Payload{
			Text:            "old retained transcript payload",
			JSONLByteOffset: 10,
		},
	)
	old.InputTokens = &oldTokens
	recent := transcriptTestTurn(
		"turn-retention-age-recent",
		"source-retention-age-recent",
		session.SessionKey,
		1,
		now.Add(-time.Hour),
		transcript.RoleAssistant,
		transcript.Payload{
			Text:            "recent retained transcript payload",
			JSONLByteOffset: 20,
		},
	)
	recent.OutputTokens = &recentTokens
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{old, recent},
	); err != nil {
		t.Fatal(err)
	}
	emptyAfterPrune := transcriptTestSession(
		"ses_retention_transcript_empty",
		transcript.CoveragePartial,
	)
	onlyOld := transcriptTestTurn(
		"turn-retention-age-only-old",
		"source-retention-age-only-old",
		emptyAfterPrune.SessionKey,
		0,
		now.Add(-72*time.Hour),
		transcript.RoleSystem,
		transcript.Payload{
			Text:            "entire session should be removed",
			JSONLByteOffset: 30,
		},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		emptyAfterPrune,
		[]transcript.Turn{onlyOld},
	); err != nil {
		t.Fatal(err)
	}

	countOnly, err := store.RetentionDiagnostics(
		ctx,
		RetentionPolicy{MaxEventCount: 1},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if countOnly.EligibleTranscriptTurnCount != 0 {
		t.Fatalf("MaxEventCount selected transcript turns: %+v", countOnly)
	}

	policy := RetentionPolicy{MaxAge: 24 * time.Hour}
	diagnostics, err := store.RetentionDiagnostics(ctx, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.CurrentTranscriptTurnCount != 3 ||
		diagnostics.EligibleTranscriptTurnCount != 2 ||
		diagnostics.CurrentTranscriptPayloadBytes <= 0 ||
		diagnostics.EligibleTranscriptPayloadBytes <= 0 ||
		diagnostics.CurrentPayloadBytes !=
			diagnostics.CurrentTranscriptPayloadBytes ||
		diagnostics.EligiblePayloadBytes !=
			diagnostics.EligibleTranscriptPayloadBytes {
		t.Fatalf("transcript age diagnostics = %+v", diagnostics)
	}

	result, err := store.Prune(ctx, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.PrunedTranscriptTurnCount != 2 ||
		result.AfterTranscriptTurnCount != 1 ||
		result.PrunedTranscriptPayloadBytes !=
			diagnostics.EligibleTranscriptPayloadBytes ||
		result.AfterTranscriptPayloadBytes !=
			diagnostics.CurrentTranscriptPayloadBytes-
				diagnostics.EligibleTranscriptPayloadBytes ||
		result.PrunedPayloadBytes != result.PrunedTranscriptPayloadBytes {
		t.Fatalf("transcript age prune = %+v", result)
	}
	turns, err := store.QueryTranscriptTurns(ctx, session.SessionKey, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 ||
		turns[0].TurnID != recent.TurnID ||
		turns[0].TurnIndex != 0 {
		t.Fatalf("retained transcript turns = %+v", turns)
	}
	gotSession, err := store.GetTranscriptSession(ctx, session.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.TurnCount != 1 ||
		gotSession.TotalTokens == nil ||
		*gotSession.TotalTokens != recentTokens ||
		!gotSession.StartedAt.Equal(recent.OccurredAt) ||
		!gotSession.EndedAt.Equal(recent.OccurredAt) {
		t.Fatalf("retained transcript session = %+v", gotSession)
	}
	if _, err := store.GetTranscriptSession(
		ctx,
		emptyAfterPrune.SessionKey,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty transcript session error = %v, want sql.ErrNoRows", err)
	}
}

func TestRetentionPayloadByteBoundCountsTranscriptCiphertext(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 9, 16, 0, 0, 0, time.UTC)
	session := transcriptTestSession(
		"ses_retention_transcript_bytes",
		transcript.CoverageLive,
	)
	turns := []transcript.Turn{
		transcriptTestTurn(
			"turn-retention-bytes-old",
			"source-retention-bytes-old",
			session.SessionKey,
			0,
			now.Add(-time.Hour),
			transcript.RoleUser,
			transcript.Payload{
				Text:            "old transcript payload with enough content to encrypt",
				JSONLByteOffset: 10,
			},
		),
		transcriptTestTurn(
			"turn-retention-bytes-new",
			"source-retention-bytes-new",
			session.SessionKey,
			1,
			now,
			transcript.RoleAssistant,
			transcript.Payload{
				Text:            "new transcript payload with enough content to encrypt",
				JSONLByteOffset: 20,
			},
		),
	}
	if _, err := store.AppendTranscriptBatch(ctx, session, turns); err != nil {
		t.Fatal(err)
	}
	items, err := readRetentionItems(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	var total, oldestBytes int64
	for _, item := range items {
		if item.recordType != "transcript_turn" {
			continue
		}
		total += item.bytes
		if item.recordID == turns[0].TurnID {
			oldestBytes = item.bytes
		}
	}
	if total <= 0 || oldestBytes <= 0 {
		t.Fatalf("transcript ciphertext bytes = total %d, oldest %d", total, oldestBytes)
	}
	policy := RetentionPolicy{MaxPayloadBytes: total - oldestBytes}
	result, err := store.Prune(ctx, policy, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if result.PrunedTranscriptTurnCount != 1 ||
		result.PrunedEventCount != 0 ||
		result.AfterTranscriptTurnCount != 1 ||
		result.AfterTranscriptPayloadBytes > policy.MaxPayloadBytes ||
		result.AfterPayloadBytes > policy.MaxPayloadBytes {
		t.Fatalf("transcript byte prune = %+v", result)
	}
	retained, err := store.QueryTranscriptTurns(ctx, session.SessionKey, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) != 1 ||
		retained[0].TurnID != turns[1].TurnID ||
		retained[0].TurnIndex != 0 {
		t.Fatalf("retained byte-bound transcript turns = %+v", retained)
	}
}

func TestRetentionByteBoundAccountsForImmediateFindingCascade(t *testing.T) {
	now := time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC)
	items := []retentionItem{
		{
			recordType: "event",
			recordID:   "event-oldest",
			occurredAt: now.Add(-3 * time.Hour),
			sequence:   1,
			bytes:      40,
		},
		{
			recordType: "event",
			recordID:   "event-second",
			occurredAt: now.Add(-2 * time.Hour),
			sequence:   2,
			bytes:      40,
		},
		{
			recordType: "finding",
			recordID:   "finding-for-oldest",
			occurredAt: now.Add(-time.Hour),
			bytes:      60,
		},
	}
	diagnostics := evaluateRetention(
		RetentionPolicy{MaxPayloadBytes: 40},
		now,
		items,
		map[string][]string{
			"event-oldest": {"finding-for-oldest"},
		},
	)
	if diagnostics.EligibleEventCount != 1 ||
		diagnostics.EligibleFindingCount != 1 ||
		diagnostics.EligiblePayloadBytes != 100 {
		t.Fatalf("cascade-aware byte selection = %+v", diagnostics)
	}
}

func TestPruneAtomicallyCascadesCanonicallyCitingFindings(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 8, 16, 0, 0, 0, time.UTC)
	old := storageTestEvent(
		"00000000-0000-7000-8000-000000000050",
		"session-cascade",
		1,
		now.Add(-time.Hour),
	)
	recent := storageTestEvent(
		"00000000-0000-7000-8000-000000000051",
		"session-cascade",
		2,
		now,
	)
	if _, err := store.AppendEvent(ctx, old); err != nil {
		t.Fatalf("append old event: %v", err)
	}
	if _, err := store.AppendEvent(ctx, recent); err != nil {
		t.Fatalf("append recent event: %v", err)
	}
	if _, err := store.RecordFinding(ctx, Finding{
		FindingID:     "finding-cascade",
		SourceRunID:   old.Source.RunID,
		SessionKey:    old.Session.Key,
		DetectedAt:    now.Add(time.Hour),
		RuleID:        "rule.cascade",
		RuleVersion:   "1",
		Severity:      "medium",
		SourceAgent:   "codex",
		Confidence:    "high",
		CitedEventIDs: []string{old.EventID},
	}); err != nil {
		t.Fatalf("RecordFinding() error = %v", err)
	}

	diagnostics, err := store.RetentionDiagnostics(
		ctx,
		RetentionPolicy{MaxEventCount: 1},
		now.Add(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("RetentionDiagnostics() error = %v", err)
	}
	if diagnostics.EligibleEventCount != 1 || diagnostics.EligibleFindingCount != 1 {
		t.Fatalf("cascade diagnostics = %+v", diagnostics)
	}
	result, err := store.Prune(
		ctx,
		RetentionPolicy{MaxEventCount: 1},
		now.Add(2*time.Hour),
	)
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if result.PrunedEventCount != 1 || result.PrunedFindingCount != 1 {
		t.Fatalf("cascade prune = %+v", result)
	}
	for _, table := range []string{"events", "findings", "finding_event_citations"} {
		count, err := store.Count(ctx, table)
		if err != nil {
			t.Fatalf("Count(%s) error = %v", table, err)
		}
		want := 0
		if table == "events" {
			want = 1
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d", table, count, want)
		}
	}
}

func TestPruneReportsCommittedDeletionWhenCompactionIsBusy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "prune-busy.sqlite")
	store, err := Open(path, newMemoryKeyProvider())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	if _, err := store.db.ExecContext(ctx, "PRAGMA busy_timeout = 50"); err != nil {
		t.Fatalf("set short busy timeout: %v", err)
	}
	now := time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC)
	for index, eventID := range []string{
		"00000000-0000-7000-8000-000000000060",
		"00000000-0000-7000-8000-000000000061",
	} {
		event := storageTestEvent(
			eventID,
			"session-prune-busy",
			int64(index+1),
			now.Add(time.Duration(index)*time.Minute),
		)
		if _, err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}

	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatalf("sqliteDSN() error = %v", err)
	}
	readerDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open concurrent reader: %v", err)
	}
	readerTx, err := readerDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		_ = readerDB.Close()
		t.Fatalf("begin concurrent reader: %v", err)
	}
	var snapshotCount int
	if err := readerTx.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&snapshotCount); err != nil {
		_ = readerTx.Rollback()
		_ = readerDB.Close()
		t.Fatalf("establish reader snapshot: %v", err)
	}

	result, err := store.Prune(ctx, RetentionPolicy{MaxEventCount: 1}, now.Add(time.Hour))
	if err != nil {
		_ = readerTx.Rollback()
		_ = readerDB.Close()
		t.Fatalf("Prune() error after commit = %v", err)
	}
	if result.PrunedEventCount != 1 ||
		result.AfterEventCount != 1 ||
		result.Maintenance.State != "pending" ||
		!result.Maintenance.Retryable {
		_ = readerTx.Rollback()
		_ = readerDB.Close()
		t.Fatalf("busy prune result = %+v", result)
	}
	if count, err := store.Count(ctx, "events"); err != nil || count != 1 {
		_ = readerTx.Rollback()
		_ = readerDB.Close()
		t.Fatalf("committed event count = %d, error = %v", count, err)
	}
	if err := readerTx.Rollback(); err != nil {
		_ = readerDB.Close()
		t.Fatalf("release reader: %v", err)
	}
	if err := readerDB.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("restore busy timeout: %v", err)
	}
	retry, err := store.Prune(ctx, RetentionPolicy{MaxEventCount: 1}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("maintenance retry Prune() error = %v", err)
	}
	if retry.PrunedEventCount != 0 || retry.Maintenance.State != "complete" {
		t.Fatalf("maintenance retry result = %+v", retry)
	}
}

func TestRetentionDoesNotConsultTeamsAcknowledgementState(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 8, 15, 0, 0, 0, time.UTC)
	for index, id := range []string{
		"00000000-0000-7000-8000-000000000040",
		"00000000-0000-7000-8000-000000000041",
	} {
		event := storageTestEvent(id, "session-teams-independent", int64(index+1), now.Add(time.Duration(index)*time.Minute))
		if _, err := store.AppendEvent(ctx, event); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `
		CREATE TABLE test_teams_state (
			singleton INTEGER PRIMARY KEY,
			acknowledged INTEGER NOT NULL
		);
		INSERT INTO test_teams_state(singleton, acknowledged) VALUES (1, 0)`,
	); err != nil {
		t.Fatalf("create Teams-state sentinel: %v", err)
	}
	policy := RetentionPolicy{MaxEventCount: 1}
	beforeAck, err := store.RetentionDiagnostics(ctx, policy, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("RetentionDiagnostics() before ack error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"UPDATE test_teams_state SET acknowledged = 1 WHERE singleton = 1",
	); err != nil {
		t.Fatalf("update Teams-state sentinel: %v", err)
	}
	afterAck, err := store.RetentionDiagnostics(ctx, policy, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("RetentionDiagnostics() after ack error = %v", err)
	}
	if beforeAck.EligibleEventCount != afterAck.EligibleEventCount ||
		beforeAck.EligiblePayloadBytes != afterAck.EligiblePayloadBytes {
		t.Fatalf("retention changed with Teams state: before=%+v after=%+v", beforeAck, afterAck)
	}
	result, err := store.Prune(ctx, policy, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("Prune() error = %v", err)
	}
	if result.PrunedEventCount != 1 {
		t.Fatalf("Teams-independent prune = %+v", result)
	}
}

func TestRetentionRequiresAnExplicitBound(t *testing.T) {
	store := openStorageTestStore(t)
	if _, err := store.RetentionDiagnostics(
		context.Background(),
		RetentionPolicy{},
		time.Now().UTC(),
	); err == nil {
		t.Fatal("zero retention policy unexpectedly acquired a default")
	}
}

func TestRetentionKeepsFreshOrphanedAnalysisRevisionsForProtectionFloor(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Now().UTC()
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000070",
		"session-analysis-floor",
		1,
		now.Add(-2*time.Hour),
	)
	if _, err := store.AppendEventResolved(ctx, event); err != nil {
		t.Fatal(err)
	}
	var originalRevision string
	if err := store.db.QueryRowContext(ctx, `
		SELECT revision_id
		FROM session_analysis_revisions
		WHERE session_key = ? AND visible_until_generation IS NULL`,
		event.Session.Key,
	).Scan(&originalRevision); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE session_analysis_revisions
			SET created_at = ?, updated_at = ?
			WHERE revision_id = ?`,
			formatProjectionTime(now.Add(-4*time.Hour)),
			formatProjectionTime(now.Add(-4*time.Hour)),
			originalRevision,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		now.Add(30*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	if count, err := store.Count(ctx, "events"); err != nil || count != 0 {
		t.Fatalf("event count = %d, error %v", count, err)
	}
	if count, err := store.Count(ctx, "session_analysis_revisions"); err != nil || count == 0 {
		t.Fatalf("fresh analysis revision count = %d, error %v", count, err)
	}
	var originalRetained int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM session_analysis_revisions WHERE revision_id = ?`,
		originalRevision,
	).Scan(&originalRetained); err != nil || originalRetained != 1 {
		t.Fatalf("recently closed old revision retained = %d, error %v", originalRetained, err)
	}
	if _, err := store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		now.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if count, err := store.Count(ctx, "session_analysis_revisions"); err != nil || count != 0 {
		t.Fatalf("expired analysis revision count = %d, error %v", count, err)
	}
}

func TestRetentionProtectsIssueRevisionFromMostRecentClose(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Now().UTC()
	event := storageTestEvent(
		"00000000-0000-7000-8000-000000000182",
		"session-issue-close-floor",
		1,
		now.Add(-4*time.Hour),
	)
	appendResult, err := store.AppendEventResolved(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, issueID, _ := store.DeriveIssueIdentity(
		"v1", "detector", event.Session.Key, "recent-close",
	)
	firstCommit, err := store.ReplaceSessionProjection(ctx, SessionProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: appendResult.ReadGeneration,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		BelayOccurrences: []model.IssueOccurrence{testIssueOccurrence(
			event.Session.Key,
			"codex",
			event.EventID,
			fingerprint,
			issueID,
			"low",
			"high",
			event.OccurredAt,
			model.ScopeUnscoped,
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var revisionID string
	if err := store.db.QueryRowContext(ctx, `
		SELECT revision_id FROM issue_occurrences
		WHERE session_key = ? AND visible_until_generation IS NULL`,
		event.Session.Key,
	).Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationProjectionRebuild, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE issue_occurrences
			SET created_at = ?, updated_at = ?
			WHERE revision_id = ?`,
			formatProjectionTime(now.Add(-4*time.Hour)),
			formatProjectionTime(now.Add(-4*time.Hour)),
			revisionID,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	target, err := store.MarkSessionDirty(ctx, event.Session.Key, "close_floor_test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceSessionProjection(ctx, SessionProjectionReplacement{
		SessionKey:        event.Session.Key,
		ClaimedGeneration: target,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
	}); err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Now().UTC()

	if _, err := store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		now.Add(30*time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM issue_occurrences WHERE revision_id = ?",
		revisionID,
	).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("recently closed issue retained = %d, error %v", retained, err)
	}
	protectedQuery := issueQueryForSnapshot(
		t, store, firstCommit.ProjectionGeneration, issuedAt,
	)
	page, err := store.QueryIssues(ctx, protectedQuery)
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("protected issue snapshot = %+v, error %v", page, err)
	}

	if _, err := store.Prune(
		ctx,
		RetentionPolicy{MaxAge: time.Hour},
		now.Add(2*time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM issue_occurrences WHERE revision_id = ?",
		revisionID,
	).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("expired closed issue retained = %d, error %v", retained, err)
	}
}

func openStorageTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(
		t.TempDir()+"/belay.sqlite",
		newMemoryKeyProvider(),
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
