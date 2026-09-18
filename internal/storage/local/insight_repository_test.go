package local

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

func TestProjectInsightIsEncryptedReplaceableAndGuarded(t *testing.T) {
	const canary = "INSIGHT_SECRET_CANARY_7f13"
	ctx := context.Background()
	store := openStorageTestStore(t)
	record := issueintel.InsightRecord{
		InsightID: "ins_first",
		Project: issueintel.Project{
			Identity: "project-insight",
			Path:     "/private/project",
		},
		Harness:       "claude",
		Model:         "claude-test",
		PromptVersion: "prompt-v1",
		InputHash:     "hash-v1",
		GeneratedAt:   time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		Result: issueintel.InsightResult{
			Fixes: []issueintel.InsightFix{{
				IssueID:    "csi_test",
				RuleText:   canary,
				TargetFile: "CLAUDE.md",
				Confidence: 0.9,
			}},
		},
		Sanitization: issueintel.InsightSanitization{
			DroppedClusters:          1,
			UnknownClusterCandidates: 1,
		},
	}
	if err := store.ReplaceProjectInsight(ctx, record); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := store.db.QueryRowContext(
		ctx,
		"SELECT payload FROM insights WHERE insight_id = ?",
		record.InsightID,
	).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(canary)) {
		t.Fatal("insight payload was stored in plaintext")
	}
	got, err := store.GetProjectInsight(ctx, record.Project.Identity)
	if err != nil ||
		got.Result.Fixes[0].RuleText != canary ||
		got.Sanitization.DroppedClusters != 1 ||
		got.Sanitization.UnknownClusterCandidates != 1 {
		t.Fatalf("insight/error = %+v/%v", got, err)
	}
	if _, err := store.db.ExecContext(
		ctx,
		"DELETE FROM insights WHERE insight_id = ?",
		record.InsightID,
	); err == nil {
		t.Fatal("direct insight deletion bypassed mutation guard")
	}
	record.InsightID = "ins_second"
	record.InputHash = "hash-v2"
	if err := store.ReplaceProjectInsight(ctx, record); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM insights",
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("insight replacement count/error = %d/%v", count, err)
	}
}
