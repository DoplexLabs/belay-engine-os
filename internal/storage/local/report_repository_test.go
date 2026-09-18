package local

import (
	"context"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestUsageSnapshotAggregatesTraceableTranscriptSessions(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	start := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	store.clock = func() time.Time {
		return time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	}
	session := transcript.Session{
		SessionKey:      "ses_report_one",
		Agent:           "codex",
		NativeSessionID: "report-one",
		ProjectPath:     "/private/report",
		ProjectIdentity: "project-report",
		Coverage:        transcript.CoverageComplete,
	}
	tokens := int64(100)
	cost := 1.25
	if _, err := store.AppendTranscriptBatch(ctx, session, []transcript.Turn{
		{
			TurnID:          "turn_report_one",
			SourceRecordKey: "report:one",
			SessionKey:      session.SessionKey,
			TurnIndex:       0,
			OccurredAt:      start,
			Role:            transcript.RoleAssistant,
			InputTokens:     &tokens,
			CostUSD:         &cost,
			Payload: transcript.Payload{
				Text:          "done",
				SourceFileID:  "source-report",
				ParserVersion: "test",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	unknownSession := transcript.Session{
		SessionKey:      "ses_report_two",
		Agent:           "claude-code",
		NativeSessionID: "report-two",
		ProjectPath:     "/private/report",
		ProjectIdentity: "project-report",
		Coverage:        transcript.CoverageComplete,
	}
	if _, err := store.AppendTranscriptBatch(
		ctx,
		unknownSession,
		[]transcript.Turn{{
			TurnID:          "turn_report_two",
			SourceRecordKey: "report:two",
			SessionKey:      unknownSession.SessionKey,
			TurnIndex:       0,
			OccurredAt:      start.AddDate(0, 0, 6),
			Role:            transcript.RoleUser,
			Payload: transcript.Payload{
				Text:          "help",
				SourceFileID:  "source-report",
				ParserVersion: "test",
			},
		}},
	); err != nil {
		t.Fatal(err)
	}
	duplicateCanonical := storageTestEvent(
		"00000000-0000-7000-8000-000000009001",
		session.SessionKey,
		1,
		start.Add(time.Hour),
	)
	duplicateCanonical.Source.DeduplicationKey =
		"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if inserted, err := store.AppendEvent(ctx, duplicateCanonical); err != nil ||
		!inserted {
		t.Fatalf("append duplicate canonical session = %t, %v", inserted, err)
	}
	canonicalOnly := storageTestEvent(
		"00000000-0000-7000-8000-000000009002",
		"ses_report_three",
		1,
		start.AddDate(0, 0, 1),
	)
	canonicalOnly.Source.DeduplicationKey =
		"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if inserted, err := store.AppendEvent(ctx, canonicalOnly); err != nil ||
		!inserted {
		t.Fatalf("append canonical-only session = %t, %v", inserted, err)
	}
	snapshot, err := store.ReadUsageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Totals.SessionCount != 3 ||
		snapshot.Totals.TotalTokens != 100 ||
		snapshot.Totals.TotalCostUSD != 1.25 ||
		!snapshot.Totals.WallDurationLowerBound ||
		!snapshot.Totals.TokensLowerBound ||
		!snapshot.Totals.CostLowerBound ||
		len(snapshot.Weeks) != 12 ||
		!snapshot.Weeks[11].WeekStart.Equal(start.Truncate(24*time.Hour)) ||
		snapshot.Weeks[10].SessionCount != 0 ||
		snapshot.Weeks[11].SessionCount != 3 ||
		snapshot.Coverage.TranscriptSessions != 2 {
		t.Fatalf("usage snapshot = %+v", snapshot)
	}
}

func TestReadCostIssueTotalsReturnsEmptyAggregate(t *testing.T) {
	store := openStorageTestStore(t)
	totals, err := store.ReadCostIssueTotals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if totals.AttributedUSD != 0 || totals.LowerBound ||
		totals.IssueCount != 0 {
		t.Fatalf("issue cost totals = %+v", totals)
	}
}

func TestUsageSnapshotKeepsKnownCostWhenSessionAlsoHasUnpricedUsage(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	store.clock = func() time.Time {
		return time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	}
	session := transcript.Session{
		SessionKey:      "ses_report_mixed_price",
		Agent:           "codex",
		NativeSessionID: "mixed-price",
		ProjectPath:     "/private/report",
		ProjectIdentity: "project-report",
		Coverage:        transcript.CoverageComplete,
	}
	start := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	tokens := int64(100)
	knownCost := 1.25
	turns := []transcript.Turn{
		{
			TurnID:          "turn_report_priced",
			SourceRecordKey: "report:priced",
			SessionKey:      session.SessionKey,
			TurnIndex:       0,
			OccurredAt:      start,
			Role:            transcript.RoleAssistant,
			InputTokens:     &tokens,
			CostUSD:         &knownCost,
			Payload: transcript.Payload{
				SourceFileID:  "source-report",
				ParserVersion: "test",
			},
		},
		{
			TurnID:          "turn_report_unpriced",
			SourceRecordKey: "report:unpriced",
			SessionKey:      session.SessionKey,
			TurnIndex:       1,
			OccurredAt:      start.Add(time.Minute),
			Role:            transcript.RoleAssistant,
			InputTokens:     &tokens,
			Payload: transcript.Payload{
				SourceFileID:  "source-report",
				ParserVersion: "test",
			},
		},
	}
	if _, err := store.AppendTranscriptBatch(ctx, session, turns); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ReadUsageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Totals.TotalCostUSD != knownCost ||
		!snapshot.Totals.CostLowerBound {
		t.Fatalf("mixed-price totals = %+v", snapshot.Totals)
	}
}
