package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/pricing"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestAppendTranscriptBatchReplayEncryptionNullsAggregatesAndOrdering(
	t *testing.T,
) {
	const payloadCanary = "TRANSCRIPT_PAYLOAD_CANARY_7d3e"
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	store, err := Open(path, newMemoryKeyProvider())
	if err != nil {
		t.Fatal(err)
	}
	session := transcriptTestSession("ses_transcript_replay", transcript.CoverageComplete)
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	inputTokens := int64(12)
	outputTokens := int64(5)
	cacheReadTokens := int64(7)
	cacheWriteTokens := int64(3)
	costUSD := 0.42
	turns := []transcript.Turn{
		transcriptTestTurn(
			"turn-c",
			"source-c",
			session.SessionKey,
			2,
			base.Add(2*time.Minute),
			transcript.RoleToolResult,
			transcript.Payload{
				ToolResult:      "result",
				JSONLByteOffset: 300,
			},
		),
		transcriptTestTurn(
			"turn-a",
			"source-a",
			session.SessionKey,
			0,
			base,
			transcript.RoleUser,
			transcript.Payload{
				Text:            payloadCanary,
				JSONLByteOffset: 100,
			},
		),
		transcriptTestTurn(
			"turn-b",
			"source-b",
			session.SessionKey,
			1,
			base.Add(time.Minute),
			transcript.RoleAssistant,
			transcript.Payload{
				Text:            "assistant response",
				ToolInput:       json.RawMessage(`{"path":"safe"}`),
				RawCommand:      "go test ./...",
				CWD:             "/tmp/project",
				GitBranch:       "main",
				ParentToolUseID: "parent-1",
				JSONLByteOffset: 200,
			},
		),
	}
	turns[2].Model = "test-model"
	turns[2].InputTokens = &inputTokens
	turns[2].OutputTokens = &outputTokens
	turns[2].CacheReadTokens = &cacheReadTokens
	turns[2].CacheWriteTokens = &cacheWriteTokens
	turns[2].CostUSD = &costUSD

	inserted, err := store.AppendTranscriptBatch(ctx, session, turns)
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 3 {
		t.Fatalf("inserted turns = %d, want 3", inserted)
	}
	replayed, err := store.AppendTranscriptBatch(ctx, session, turns)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != 0 {
		t.Fatalf("replayed turns inserted = %d, want 0", replayed)
	}

	var ciphertext []byte
	var encoding string
	var unknownInput, unknownCost int
	if err := store.db.QueryRowContext(ctx, `
		SELECT payload, payload_encoding,
			input_tokens IS NULL, cost_usd IS NULL
		FROM transcript_turns
		WHERE turn_id = 'turn-a'`,
	).Scan(&ciphertext, &encoding, &unknownInput, &unknownCost); err != nil {
		t.Fatal(err)
	}
	if encoding != payloadEncodingAESGCM ||
		bytes.Contains(ciphertext, []byte(payloadCanary)) {
		t.Fatal("transcript payload was not stored as ciphertext")
	}
	if unknownInput != 1 || unknownCost != 1 {
		t.Fatalf("unknown input/cost null markers = %d/%d, want 1/1", unknownInput, unknownCost)
	}
	if _, err := store.cipher.open(
		"transcript_turn",
		"wrong-turn-id",
		"payload",
		encoding,
		ciphertext,
	); err == nil {
		t.Fatal("transcript payload authenticated with the wrong turn ID")
	}

	gotTurns, err := store.QueryTranscriptTurns(ctx, session.SessionKey, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotTurns) != 3 ||
		gotTurns[0].TurnID != "turn-a" ||
		gotTurns[1].TurnID != "turn-b" ||
		gotTurns[2].TurnID != "turn-c" {
		t.Fatalf("transcript turn order = %+v", gotTurns)
	}
	if gotTurns[0].CostUSD != nil ||
		gotTurns[0].InputTokens != nil ||
		gotTurns[0].Payload.Text != payloadCanary {
		t.Fatalf("unknown/decrypted turn = %+v", gotTurns[0])
	}

	gotSession, err := store.GetTranscriptSession(ctx, session.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.TurnCount != 3 ||
		gotSession.UserTurnCount != 1 ||
		gotSession.AssistantTurnCount != 1 ||
		gotSession.ToolResultCount != 1 ||
		gotSession.WallDurationMS != 120000 ||
		gotSession.TotalInputTokens == nil ||
		*gotSession.TotalInputTokens != inputTokens ||
		gotSession.TotalOutputTokens == nil ||
		*gotSession.TotalOutputTokens != outputTokens ||
		gotSession.TotalTokens == nil ||
		*gotSession.TotalTokens != inputTokens+outputTokens+
			cacheReadTokens+cacheWriteTokens ||
		gotSession.TotalCacheReadTokens == nil ||
		*gotSession.TotalCacheReadTokens != cacheReadTokens ||
		gotSession.TotalCacheWriteTokens == nil ||
		*gotSession.TotalCacheWriteTokens != cacheWriteTokens ||
		gotSession.TotalCostUSD == nil ||
		!closeFloat64(*gotSession.TotalCostUSD, costUSD) ||
		!gotSession.StartedAt.Equal(base) ||
		!gotSession.EndedAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("transcript session aggregate = %+v", gotSession)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	assertSQLiteFilesExclude(t, path, payloadCanary)
}

func TestOpenRepricesExistingTranscriptTurnsWithCurrentCatalog(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "belay.sqlite")
	keys := newMemoryKeyProvider()
	store, err := Open(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	session := transcriptTestSession("ses_reprice", transcript.CoverageComplete)
	oneMillion := int64(1_000_000)
	turn := transcriptTestTurn(
		"turn-reprice",
		"source-reprice",
		session.SessionKey,
		0,
		time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
		transcript.RoleAssistant,
		transcript.Payload{
			PriceTableVersion: "belay.local.prices.v2",
		},
	)
	turn.Model = "claude-sonnet-5"
	turn.InputTokens = &oneMillion
	turn.OutputTokens = &oneMillion
	turn.CacheReadTokens = &oneMillion
	turn.CacheWriteTokens = &oneMillion
	if _, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{turn},
	); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetTranscriptSession(ctx, session.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if before.TotalCostUSD != nil {
		t.Fatalf("pre-reprice cost = %v, want nil", before.TotalCostUSD)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE transcript_pricing_metadata
		SET price_table_version = 'belay.local.prices.v2'
		WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after, err := store.GetTranscriptSession(ctx, session.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if after.TotalCostUSD == nil ||
		!closeFloat64(*after.TotalCostUSD, 22.05) {
		t.Fatalf("repriced session cost = %v, want 22.05", after.TotalCostUSD)
	}
	var version string
	if err := store.db.QueryRowContext(ctx, `
		SELECT price_table_version
		FROM transcript_turns
		WHERE turn_id = ?`,
		turn.TurnID,
	).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != pricing.Version {
		t.Fatalf("price table version = %q", version)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT price_table_version
		FROM transcript_pricing_metadata
		WHERE singleton = 1`,
	).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != pricing.Version {
		t.Fatalf("applied price table version = %q", version)
	}
}

func TestAppendTranscriptBatchRecomputesAggregateFromRetainedTurns(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	session := transcriptTestSession("ses_transcript_recompute", transcript.CoveragePartial)
	base := time.Date(2026, 9, 9, 13, 0, 0, 0, time.UTC)
	firstTokens := int64(10)
	secondTokens := int64(20)
	firstCost := 0.10
	secondCost := 0.20
	first := transcriptTestTurn(
		"turn-retained",
		"source-retained",
		session.SessionKey,
		0,
		base,
		transcript.RoleUser,
		transcript.Payload{JSONLByteOffset: 10},
	)
	first.InputTokens = &firstTokens
	first.CostUSD = &firstCost
	second := transcriptTestTurn(
		"turn-pruned",
		"source-pruned",
		session.SessionKey,
		1,
		base.Add(time.Minute),
		transcript.RoleAssistant,
		transcript.Payload{JSONLByteOffset: 20},
	)
	second.InputTokens = &secondTokens
	second.CostUSD = &secondCost
	if inserted, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{first, second},
	); err != nil || inserted != 2 {
		t.Fatalf("initial append = %d/%v", inserted, err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationTranscriptRetention, func() error {
		_, err := tx.ExecContext(
			ctx,
			"DELETE FROM transcript_turns WHERE turn_id = ?",
			second.TurnID,
		)
		return err
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if inserted, err := store.AppendTranscriptBatch(ctx, session, nil); err != nil || inserted != 0 {
		t.Fatalf("aggregate refresh = %d/%v", inserted, err)
	}
	got, err := store.GetTranscriptSession(ctx, session.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.TurnCount != 1 ||
		got.AssistantTurnCount != 0 ||
		got.WallDurationMS != 0 ||
		got.TotalInputTokens == nil ||
		*got.TotalInputTokens != firstTokens ||
		got.TotalTokens == nil ||
		*got.TotalTokens != firstTokens ||
		got.TotalCostUSD == nil ||
		!closeFloat64(*got.TotalCostUSD, firstCost) ||
		!got.StartedAt.Equal(base) ||
		!got.EndedAt.Equal(base) {
		t.Fatalf("recomputed aggregate = %+v", got)
	}
}

func TestAppendTranscriptBatchReindexesLateHistoricalAndSubagentTurns(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	session := transcriptTestSession(
		"ses_transcript_late_history",
		transcript.CoverageLive,
	)
	base := time.Date(2026, 9, 9, 13, 15, 0, 0, time.UTC)
	live := transcriptTestTurn(
		"turn-live",
		"source-z-live",
		session.SessionKey,
		0,
		base.Add(2*time.Minute),
		transcript.RoleAssistant,
		transcript.Payload{JSONLByteOffset: 300},
	)
	if inserted, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{live},
	); err != nil || inserted != 1 {
		t.Fatalf("append live turn = %d/%v", inserted, err)
	}

	historicalB := transcriptTestTurn(
		"turn-history-b",
		"source-b-history",
		session.SessionKey,
		0,
		base,
		transcript.RoleToolResult,
		transcript.Payload{
			ParentToolUseID: "parent",
			JSONLByteOffset: 200,
		},
	)
	historicalA := transcriptTestTurn(
		"turn-history-a",
		"source-a-history",
		session.SessionKey,
		99,
		base,
		transcript.RoleAssistant,
		transcript.Payload{
			ParentToolUseID: "parent",
			JSONLByteOffset: 100,
		},
	)
	if inserted, err := store.AppendTranscriptBatch(
		ctx,
		session,
		[]transcript.Turn{historicalB, historicalA},
	); err != nil || inserted != 2 {
		t.Fatalf("append historical turns = %d/%v", inserted, err)
	}

	got, err := store.QueryTranscriptTurns(ctx, session.SessionKey, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"turn-history-a", "turn-history-b", "turn-live"}
	if len(got) != len(wantIDs) {
		t.Fatalf("turn count = %d, want %d", len(got), len(wantIDs))
	}
	for index, wantID := range wantIDs {
		if got[index].TurnID != wantID || got[index].TurnIndex != int64(index) {
			t.Fatalf("turn %d = %+v, want ID %s and index %d", index, got[index], wantID, index)
		}
	}
}

func TestReindexTranscriptSessionUsesMaterializedSetBasedPlan(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	rows, err := store.db.QueryContext(
		ctx,
		"EXPLAIN QUERY PLAN "+reindexTranscriptSessionSQL,
		"ses_transcript_plan",
	)
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
	planText := strings.ToUpper(plan.String())
	if strings.Contains(planText, "CORRELATED") {
		t.Fatalf("reindex plan contains a per-row correlated lookup:\n%s", plan.String())
	}
	for _, required := range []string{
		"MATERIALIZE RANKED",
		"SCAN RANKED",
		"USING INTEGER PRIMARY KEY",
	} {
		if !strings.Contains(planText, required) {
			t.Fatalf("reindex plan lacks %q:\n%s", required, plan.String())
		}
	}
}

func TestTranscriptSessionCostRequiresEveryBillableTurnPriced(t *testing.T) {
	tests := []struct {
		name       string
		unknown    bool
		wantCost   *float64
		sessionKey string
	}{
		{
			name:       "all known",
			wantCost:   float64Pointer(0.375),
			sessionKey: "ses_transcript_cost_known",
		},
		{
			name:       "mixed known and unknown",
			unknown:    true,
			sessionKey: "ses_transcript_cost_mixed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := openStorageTestStore(t)
			session := transcriptTestSession(
				test.sessionKey,
				transcript.CoverageComplete,
			)
			base := time.Date(2026, 9, 9, 13, 20, 0, 0, time.UTC)
			tokens := int64(10)
			firstCost := 0.125
			secondCost := 0.25
			first := transcriptTestTurn(
				"turn-"+test.sessionKey+"-first",
				"source-"+test.sessionKey+"-first",
				session.SessionKey,
				0,
				base,
				transcript.RoleAssistant,
				transcript.Payload{JSONLByteOffset: 10},
			)
			first.OutputTokens = &tokens
			first.CostUSD = &firstCost
			second := transcriptTestTurn(
				"turn-"+test.sessionKey+"-second",
				"source-"+test.sessionKey+"-second",
				session.SessionKey,
				1,
				base.Add(time.Minute),
				transcript.RoleAssistant,
				transcript.Payload{JSONLByteOffset: 20},
			)
			second.OutputTokens = &tokens
			if !test.unknown {
				second.CostUSD = &secondCost
			}
			metadataOnly := transcriptTestTurn(
				"turn-"+test.sessionKey+"-metadata",
				"source-"+test.sessionKey+"-metadata",
				session.SessionKey,
				2,
				base.Add(2*time.Minute),
				transcript.RoleSystem,
				transcript.Payload{JSONLByteOffset: 30},
			)
			if _, err := store.AppendTranscriptBatch(
				ctx,
				session,
				[]transcript.Turn{first, second, metadataOnly},
			); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetTranscriptSession(ctx, session.SessionKey)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantCost == nil {
				if got.TotalCostUSD != nil {
					t.Fatalf("total cost = %v, want nil", *got.TotalCostUSD)
				}
			} else if got.TotalCostUSD == nil ||
				!closeFloat64(*got.TotalCostUSD, *test.wantCost) {
				t.Fatalf("total cost = %v, want %v", got.TotalCostUSD, *test.wantCost)
			}
		})
	}
}

func TestAppendTranscriptBatchRejectsSourceIdentityConflictAtomically(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 9, 13, 30, 0, 0, time.UTC)
	firstSession := transcriptTestSession(
		"ses_transcript_identity_first",
		transcript.CoverageComplete,
	)
	firstTurn := transcriptTestTurn(
		"turn-transcript-identity-first",
		"source-transcript-shared",
		firstSession.SessionKey,
		0,
		base,
		transcript.RoleUser,
		transcript.Payload{JSONLByteOffset: 10},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		firstSession,
		[]transcript.Turn{firstTurn},
	); err != nil {
		t.Fatal(err)
	}

	secondSession := transcriptTestSession(
		"ses_transcript_identity_second",
		transcript.CoverageComplete,
	)
	conflicting := transcriptTestTurn(
		"turn-transcript-identity-second",
		firstTurn.SourceRecordKey,
		secondSession.SessionKey,
		0,
		base,
		transcript.RoleUser,
		transcript.Payload{JSONLByteOffset: 10},
	)
	if _, err := store.AppendTranscriptBatch(
		ctx,
		secondSession,
		[]transcript.Turn{conflicting},
	); err == nil {
		t.Fatal("conflicting transcript source identity was accepted")
	}
	var secondSessions int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM transcript_sessions
		WHERE session_key = ?`,
		secondSession.SessionKey,
	).Scan(&secondSessions); err != nil {
		t.Fatal(err)
	}
	if secondSessions != 0 {
		t.Fatalf("conflicting batch left %d session rows", secondSessions)
	}
}

func TestTranscriptCoverageCountsCanonicalAndNativeSessions(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	canonicalSessions := []string{
		"ses_coverage_complete",
		"ses_coverage_partial",
		"ses_coverage_live",
		"ses_coverage_missing",
	}
	for index, sessionKey := range canonicalSessions {
		event := storageTestEvent(
			[]string{
				"00000000-0000-7000-8000-000000001401",
				"00000000-0000-7000-8000-000000001402",
				"00000000-0000-7000-8000-000000001403",
				"00000000-0000-7000-8000-000000001404",
			}[index],
			sessionKey,
			1,
			base.Add(time.Duration(index)*time.Minute),
		)
		if _, err := store.AppendEventResolved(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	for index, item := range []struct {
		sessionKey string
		coverage   transcript.SessionCoverage
	}{
		{"ses_coverage_complete", transcript.CoverageComplete},
		{"ses_coverage_partial", transcript.CoveragePartial},
		{"ses_coverage_live", transcript.CoverageLive},
		{"ses_coverage_transcript_only", transcript.CoveragePartial},
	} {
		session := transcriptTestSession(item.sessionKey, item.coverage)
		turn := transcriptTestTurn(
			"turn-coverage-"+item.sessionKey,
			"source-coverage-"+item.sessionKey,
			item.sessionKey,
			0,
			base.Add(time.Duration(index)*time.Minute),
			transcript.RoleSystem,
			transcript.Payload{JSONLByteOffset: int64(index * 100)},
		)
		if _, err := store.AppendTranscriptBatch(ctx, session, []transcript.Turn{turn}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.TranscriptCoverage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.CanonicalSessions != 4 ||
		got.TranscriptSessions != 4 ||
		got.WithTranscript != 2 ||
		got.WithoutTranscript != 1 ||
		got.Complete != 1 ||
		got.Partial != 1 ||
		got.Live != 1 ||
		got.CanonicalCompleteWithTranscript != 1 ||
		got.CanonicalLiveWithTranscript != 1 ||
		got.CanonicalCompleteOrLiveWithTranscript != 2 ||
		got.CanonicalPartial != 1 ||
		got.CanonicalWithoutTranscript != 1 ||
		got.TranscriptOnly != 1 {
		t.Fatalf("transcript coverage = %+v", got)
	}
	if got.WithTranscript+got.Partial+got.WithoutTranscript !=
		got.CanonicalSessions {
		t.Fatalf("canonical coverage partition is not exhaustive: %+v", got)
	}
	partial, err := store.GetTranscriptSession(ctx, "ses_coverage_partial")
	if err != nil {
		t.Fatal(err)
	}
	if partial.TotalTokens != nil {
		t.Fatalf("all-unknown session total tokens = %v, want nil", partial.TotalTokens)
	}
}

func float64Pointer(value float64) *float64 {
	return &value
}

func closeFloat64(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9
}

func TestQueryTranscriptSessionsIsBoundedFilteredAndDeterministicallyRecent(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 9, 16, 0, 0, 0, time.UTC)
	fixtures := []struct {
		sessionKey string
		at         time.Time
		coverage   transcript.SessionCoverage
	}{
		{"ses_query_b", base.Add(time.Minute), transcript.CoverageComplete},
		{"ses_query_older", base, transcript.CoveragePartial},
		{"ses_query_a", base.Add(time.Minute), transcript.CoverageComplete},
	}
	for _, fixture := range fixtures {
		session := transcriptTestSession(fixture.sessionKey, fixture.coverage)
		turn := transcriptTestTurn(
			"turn-"+fixture.sessionKey,
			"source-"+fixture.sessionKey,
			fixture.sessionKey,
			0,
			fixture.at,
			transcript.RoleSystem,
			transcript.Payload{JSONLByteOffset: 0},
		)
		if _, err := store.AppendTranscriptBatch(ctx, session, []transcript.Turn{turn}); err != nil {
			t.Fatal(err)
		}
	}

	recent, err := store.QueryTranscriptSessions(ctx, transcript.SessionQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 ||
		recent[0].SessionKey != "ses_query_a" ||
		recent[1].SessionKey != "ses_query_b" {
		t.Fatalf("recent transcript sessions = %+v", recent)
	}
	filtered, err := store.QueryTranscriptSessions(ctx, transcript.SessionQuery{
		Limit:           maxTranscriptSessionLimit + 100,
		Agent:           "codex",
		ProjectIdentity: "git@example.invalid:owner/project.git",
		Coverage:        transcript.CoveragePartial,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].SessionKey != "ses_query_older" {
		t.Fatalf("filtered transcript sessions = %+v", filtered)
	}
}

func TestListTranscriptProjectIdentitiesIsDistinctStableFilteredAndBounded(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	projects := make([]string, 0, 34)
	for index := 29; index >= 0; index-- {
		projects = append(projects, fmt.Sprintf("project-%02d", index))
	}
	projects = append(projects, "project-01", " project-01 ")
	for index, projectIdentity := range projects {
		sessionKey := fmt.Sprintf("ses_project_identity_%d", index)
		session := transcriptTestSession(sessionKey, transcript.CoverageComplete)
		session.ProjectIdentity = projectIdentity
		session.GitRemoteURL = projectIdentity
		turn := transcriptTestTurn(
			"turn-"+sessionKey,
			"source-"+sessionKey,
			sessionKey,
			0,
			base.Add(time.Duration(index)*time.Minute),
			transcript.RoleSystem,
			transcript.Payload{JSONLByteOffset: int64(index)},
		)
		if _, err := store.AppendTranscriptBatch(
			ctx,
			session,
			[]transcript.Turn{turn},
		); err != nil {
			t.Fatal(err)
		}
	}
	limited, err := store.ListTranscriptProjectIdentities(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(limited, ","), "project-00,project-01"; got != want {
		t.Fatalf("limited project identities = %q, want %q", got, want)
	}
	defaulted, err := store.ListTranscriptProjectIdentities(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaulted) != defaultTranscriptProjectLimit ||
		defaulted[0] != "project-00" ||
		defaulted[len(defaulted)-1] != "project-24" {
		t.Fatalf("default project identities = %#v", defaulted)
	}
	all, err := store.ListTranscriptProjectIdentities(ctx, maxTranscriptProjectLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 30 ||
		all[0] != "project-00" ||
		all[len(all)-1] != "project-29" {
		t.Fatalf("all project identities = %#v", all)
	}
	if _, err := store.ListTranscriptProjectIdentities(
		ctx,
		maxTranscriptProjectLimit+1,
	); err == nil {
		t.Fatal("project identity lookup accepted limit above 101")
	}
}

func transcriptTestSession(
	sessionKey string,
	coverage transcript.SessionCoverage,
) transcript.Session {
	return transcript.Session{
		SessionKey:      sessionKey,
		Agent:           "codex",
		NativeSessionID: "native-" + sessionKey,
		ProjectPath:     "/tmp/project",
		GitRemoteURL:    "git@example.invalid:owner/project.git",
		ProjectIdentity: "git@example.invalid:owner/project.git",
		Coverage:        coverage,
	}
}

func transcriptTestTurn(
	turnID string,
	sourceRecordKey string,
	sessionKey string,
	turnIndex int64,
	occurredAt time.Time,
	role transcript.Role,
	payload transcript.Payload,
) transcript.Turn {
	return transcript.Turn{
		TurnID:          turnID,
		SourceRecordKey: sourceRecordKey,
		SessionKey:      sessionKey,
		TurnIndex:       turnIndex,
		OccurredAt:      occurredAt,
		Role:            role,
		Payload:         payload,
	}
}

func transcriptTurnPayloadRow(
	t *testing.T,
	store *Store,
	turnID string,
) ([]byte, string) {
	t.Helper()
	var payload []byte
	var encoding string
	if err := store.db.QueryRow(`
		SELECT payload, payload_encoding
		FROM transcript_turns
		WHERE turn_id = ?`,
		turnID,
	).Scan(&payload, &encoding); err != nil {
		t.Fatal(err)
	}
	return payload, encoding
}
