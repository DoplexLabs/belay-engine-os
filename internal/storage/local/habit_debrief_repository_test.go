package local

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/userinsights"
)

func TestHabitDebriefRoundTripAndReplacement(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "belay.sqlite"), newMemoryKeyProvider())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	record := userinsights.DebriefRecord{
		SessionKey:      "ses_habit",
		ProjectIdentity: "/Users/private/project",
		Harness:         "claude",
		Model:           "claude-test",
		PromptVersion:   userinsights.DebriefPromptVersion,
		InputHash:       "hash-1",
		GeneratedAt:     time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC),
		Debrief: userinsights.Debrief{
			Headline: "Testing came late.",
			Insights: []userinsights.Insight{{
				Kind: userinsights.InsightVerification, Title: "Ask for checks first",
				SayThisInstead: "Run go test ./... after each phase.", EvidenceTurns: []int64{3},
			}},
		},
	}
	if _, err := store.GetHabitDebrief(ctx, record.SessionKey); !errors.Is(err, ErrHabitDebriefNotFound) {
		t.Fatalf("expected not found before insert, got %v", err)
	}
	if err := store.ReplaceHabitDebrief(ctx, record); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetHabitDebrief(ctx, record.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SchemaVersion != userinsights.DebriefSchemaVersion ||
		stored.Debrief.Headline != record.Debrief.Headline ||
		stored.InputHash != "hash-1" || !stored.GeneratedAt.Equal(record.GeneratedAt) {
		t.Fatalf("unexpected stored record: %+v", stored)
	}
	var rawPayload []byte
	if err := store.db.QueryRow("SELECT payload FROM habit_debriefs WHERE session_key = ?", record.SessionKey).Scan(&rawPayload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawPayload), "Testing came late") {
		t.Fatal("habit debrief payload must be encrypted at rest")
	}
	record.InputHash = "hash-2"
	record.Debrief.Headline = "Second generation."
	if err := store.ReplaceHabitDebrief(ctx, record); err != nil {
		t.Fatal(err)
	}
	records, err := store.QueryHabitDebriefs(ctx, []string{record.SessionKey, "ses_missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[record.SessionKey].InputHash != "hash-2" ||
		records[record.SessionKey].Debrief.Headline != "Second generation." {
		t.Fatalf("replacement did not take effect: %+v", records)
	}
	if err := store.DeleteHabitDebrief(ctx, record.SessionKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetHabitDebrief(ctx, record.SessionKey); !errors.Is(err, ErrHabitDebriefNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
	if err := store.ReplaceHabitDebrief(ctx, userinsights.DebriefRecord{SessionKey: "x"}); err == nil {
		t.Fatal("invalid records must be rejected")
	}
}
