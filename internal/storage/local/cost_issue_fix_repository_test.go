package local

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

func TestCostIssueFixIsEncryptedGuardedAndAppliedIdempotently(t *testing.T) {
	const canary = "FIX_SECRET_CANARY_94e1"
	ctx := context.Background()
	store := openStorageTestStore(t)
	proposedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	record := issueintel.FixRecord{
		FixID:   "fix_storage_test",
		IssueID: "csi_storage_test",
		Project: issueintel.Project{
			Identity: "project-storage-test",
			Path:     "/private/project",
		},
		Kind:        "harness_rule",
		TargetFile:  "AGENTS.md",
		RuleText:    canary,
		UnifiedDiff: "--- a/AGENTS.md\n+++ b/AGENTS.md\n+" + canary + "\n",
		State:       "proposed",
		ProposedAt:  proposedAt,
	}
	if err := store.SaveCostIssueFix(ctx, record); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := store.db.QueryRowContext(
		ctx,
		"SELECT payload FROM cost_issue_fixes WHERE fix_id = ?",
		record.FixID,
	).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(canary)) {
		t.Fatal("cost issue fix payload was stored in plaintext")
	}
	got, err := store.GetCostIssueFix(ctx, record.FixID)
	if err != nil || got.RuleText != canary {
		t.Fatalf("stored fix/error = %+v/%v", got, err)
	}
	if _, err := store.db.ExecContext(
		ctx,
		"UPDATE cost_issue_fixes SET state = 'applied' WHERE fix_id = ?",
		record.FixID,
	); err == nil {
		t.Fatal("direct cost issue fix update bypassed mutation guard")
	}
	appliedAt := proposedAt.Add(time.Hour)
	hash := strings.Repeat("a", 64)
	applied, err := store.RecordCostIssueFixApplied(
		ctx,
		record.FixID,
		"AGENTS.md",
		hash,
		"abc123",
		appliedAt,
	)
	if err != nil || applied.State != "applied" ||
		applied.ContentSHA256 != hash {
		t.Fatalf("applied fix/error = %+v/%v", applied, err)
	}
	repeated, err := store.RecordCostIssueFixApplied(
		ctx,
		record.FixID,
		"AGENTS.md",
		hash,
		"abc123",
		appliedAt.Add(time.Hour),
	)
	if err != nil || repeated.AppliedAt == nil ||
		!repeated.AppliedAt.Equal(appliedAt) {
		t.Fatalf("idempotent application/error = %+v/%v", repeated, err)
	}
	if _, err := store.RecordCostIssueFixApplied(
		ctx,
		record.FixID,
		"AGENTS.md",
		strings.Repeat("b", 64),
		"abc123",
		appliedAt,
	); err == nil {
		t.Fatal("accepted different evidence for an applied fix")
	}
}
