package localapp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

type issueFixTestStore struct {
	issue   issueintel.Issue
	insight issueintel.InsightRecord
	fixes   map[string]issueintel.FixRecord
}

func (s *issueFixTestStore) GetCostIssue(
	context.Context,
	string,
) (issueintel.Issue, error) {
	return s.issue, nil
}

func (s *issueFixTestStore) GetProjectInsight(
	context.Context,
	string,
) (issueintel.InsightRecord, error) {
	if s.insight.InsightID == "" {
		return issueintel.InsightRecord{}, sql.ErrNoRows
	}
	return s.insight, nil
}

func (s *issueFixTestStore) SaveCostIssueFix(
	_ context.Context,
	record issueintel.FixRecord,
) error {
	s.fixes[record.FixID] = record
	return nil
}

func (s *issueFixTestStore) GetCostIssueFix(
	_ context.Context,
	fixID string,
) (issueintel.FixRecord, error) {
	record, ok := s.fixes[fixID]
	if !ok {
		return issueintel.FixRecord{}, sql.ErrNoRows
	}
	return record, nil
}

func (s *issueFixTestStore) RecordCostIssueFixApplied(
	_ context.Context,
	fixID, appliedPath, contentSHA256, gitCommit string,
	appliedAt time.Time,
) (issueintel.FixRecord, error) {
	record := s.fixes[fixID]
	record.State = "applied"
	record.AppliedPath = appliedPath
	record.ContentSHA256 = contentSHA256
	record.GitCommit = gitCommit
	record.AppliedAt = &appliedAt
	s.fixes[fixID] = record
	return record, nil
}

func TestCostIssueFixServiceProposesConfigOnlyDiffWithoutWriting(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "AGENTS.md")
	const original = "# Existing instructions\n"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newIssueFixTestStore(root, "harness_rule", "AGENTS.md")
	service, err := NewCostIssueFixService(store)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	}
	record, err := service.ProposeFix(
		context.Background(),
		store.issue.IssueID,
		"harness_rule",
		"AGENTS.md",
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != original {
		t.Fatalf("proposal changed target: %q", body)
	}
	if record.State != "proposed" ||
		!strings.Contains(record.UnifiedDiff, "+++ b/AGENTS.md") ||
		!strings.Contains(record.UnifiedDiff, store.issue.SuggestedFix.Rationale) {
		t.Fatalf("proposal = %+v", record)
	}
}

func TestCostIssueFixServiceRejectsEscapesKindsAndSymlinks(t *testing.T) {
	root := t.TempDir()
	store := newIssueFixTestStore(root, "harness_rule", "AGENTS.md")
	service, err := NewCostIssueFixService(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		kind   string
		target string
	}{
		{kind: "permission_allowlist", target: "AGENTS.md"},
		{kind: "harness_rule", target: "../source.go"},
		{kind: "harness_rule", target: "source.go"},
	} {
		if _, err := service.ProposeFix(
			context.Background(),
			store.issue.IssueID,
			test.kind,
			test.target,
		); err == nil {
			t.Fatalf("accepted kind/target %q/%q", test.kind, test.target)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProposeFix(
		context.Background(),
		store.issue.IssueID,
		"harness_rule",
		"AGENTS.md",
	); err == nil {
		t.Fatal("accepted symlink fix target")
	}
}

func TestCostIssueFixServiceBuildsPermissionEntryAndValidatesAppliedHash(
	t *testing.T,
) {
	root := t.TempDir()
	target := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newIssueFixTestStore(
		root,
		"permission_allowlist",
		".claude/settings.json",
	)
	store.issue.Fingerprint = "Bash\x00go test ./..."
	service, err := NewCostIssueFixService(store)
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.ProposeFix(
		context.Background(),
		store.issue.IssueID,
		"permission_allowlist",
		".claude/settings.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(record.UnifiedDiff, "Bash(go test ./...)") {
		t.Fatalf("permission diff = %q", record.UnifiedDiff)
	}
	appliedBody := []byte(`{
  "permissions": {
    "allow": ["Bash(go test ./...)"]
  }
}
`)
	if err := os.WriteFile(target, appliedBody, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(appliedBody)
	hash := hex.EncodeToString(sum[:])
	applied, err := service.RecordApplied(
		context.Background(),
		record.FixID,
		".claude/settings.json",
		hash,
		"abc123",
	)
	if err != nil || applied.State != "applied" {
		t.Fatalf("applied/error = %+v/%v", applied, err)
	}
	if _, err := service.RecordApplied(
		context.Background(),
		record.FixID,
		".claude/settings.json",
		strings.Repeat("0", 64),
		"",
	); err == nil {
		t.Fatal("accepted a hash that does not match the target")
	}
	status, err := service.Status(context.Background(), record.FixID)
	if err != nil || !status.Applied ||
		status.VerificationState != "deferred" ||
		status.StillPresent != nil ||
		status.RecurrenceSinceFix != nil {
		t.Fatalf("status/error = %+v/%v", status, err)
	}
}

func newIssueFixTestStore(
	root, kind, target string,
) *issueFixTestStore {
	return &issueFixTestStore{
		issue: issueintel.Issue{
			IssueID:     "csi_fix_test",
			DetectorID:  issueintel.DetectorRetryLoop,
			Fingerprint: "fingerprint",
			Project: issueintel.Project{
				Identity: "project-fix-test",
				Path:     root,
			},
			SuggestedFix: issueintel.SuggestedFix{
				Kind:       kind,
				TargetFile: target,
				Rationale:  "Run the focused verification command before claiming completion.",
			},
		},
		fixes: make(map[string]issueintel.FixRecord),
	}
}
