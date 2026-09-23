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

func TestCostIssueFixServiceProposesCursorProjectRuleWithFrontMatter(
	t *testing.T,
) {
	root := t.TempDir()
	store := newIssueFixTestStore(root, "harness_rule", cursorProjectRuleFile)
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
		cursorProjectRuleFile,
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != "proposed" ||
		!strings.Contains(
			record.UnifiedDiff,
			"+++ b/"+cursorProjectRuleFile,
		) ||
		!strings.Contains(record.UnifiedDiff, "+alwaysApply: true") ||
		!strings.Contains(
			record.UnifiedDiff,
			store.issue.SuggestedFix.Rationale,
		) {
		t.Fatalf("Cursor rule proposal = %+v", record)
	}
	if _, err := os.Stat(
		filepath.Join(root, ".cursor", "rules", "belay.mdc"),
	); !os.IsNotExist(err) {
		t.Fatalf("proposal wrote the Cursor rule file: %v", err)
	}
}

func TestFixTargetsStayInsideHarnessConfiguration(t *testing.T) {
	for _, target := range []string{
		"CLAUDE.md",
		"AGENTS.md",
		".claude/settings.json",
		".claude/hooks/pre.sh",
		".codex/config.toml",
		".codex/rules/default.rules",
		".cursorrules",
		cursorProjectRuleFile,
		antigravityProjectRuleFile,
	} {
		if got, err := normalizeFixTarget(target); err != nil ||
			got != target {
			t.Fatalf("normalizeFixTarget(%q) = %q, %v", target, got, err)
		}
	}
	for _, target := range []string{
		".cursor/rules/other.mdc",
		".cursor/mcp.json",
		".agents/rules/other.md",
		".agent/rules/belay.md",
		".agents/rules/belay.mdc",
		"GEMINI.md",
		"internal/example.go",
		"../AGENTS.md",
	} {
		if _, err := normalizeFixTarget(target); err == nil {
			t.Fatalf("normalizeFixTarget accepted %q", target)
		}
	}
}

func TestCursorProjectRuleRejectsPermissionFixes(t *testing.T) {
	issue := issueintel.Issue{
		SuggestedFix: issueintel.SuggestedFix{
			Kind: "permission_allowlist",
		},
	}
	if _, err := applyRuleToConfig(
		cursorProjectRuleFile,
		nil,
		"Allow the verified command.",
		issue,
	); err == nil {
		t.Fatal("Cursor rule accepted a permission fix")
	}
}

func TestCostIssueFixServiceProposesAntigravityProjectRuleWithFrontMatter(
	t *testing.T,
) {
	root := t.TempDir()
	store := newIssueFixTestStore(
		root,
		"harness_rule",
		antigravityProjectRuleFile,
	)
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
		antigravityProjectRuleFile,
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != "proposed" ||
		!strings.Contains(
			record.UnifiedDiff,
			"+++ b/"+antigravityProjectRuleFile,
		) ||
		!strings.Contains(record.UnifiedDiff, "+trigger: always_on") ||
		!strings.Contains(
			record.UnifiedDiff,
			"+- "+store.issue.SuggestedFix.Rationale,
		) {
		t.Fatalf("Antigravity rule proposal = %+v", record)
	}
	if strings.Contains(record.UnifiedDiff, "alwaysApply") ||
		strings.Contains(record.UnifiedDiff, "description:") {
		t.Fatalf("Antigravity rule proposal used Cursor front matter: %s", record.UnifiedDiff)
	}
	if _, err := os.Stat(
		filepath.Join(root, ".agents", "rules", "belay.md"),
	); !os.IsNotExist(err) {
		t.Fatalf("proposal wrote the Antigravity rule file: %v", err)
	}
}

func TestAntigravityProjectRuleSeedsFrontMatterOnlyWhenEmpty(t *testing.T) {
	issue := issueintel.Issue{
		SuggestedFix: issueintel.SuggestedFix{Kind: "harness_rule"},
	}
	fresh, err := applyRuleToConfig(
		antigravityProjectRuleFile,
		nil,
		"Run the focused verification command.",
		issue,
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(fresh) != antigravityRuleFrontMatter+
		"\n- Run the focused verification command.\n" {
		t.Fatalf("fresh Antigravity rule file = %q", fresh)
	}
	if antigravityRuleFrontMatter != "---\ntrigger: always_on\n---\n" {
		t.Fatalf(
			"Antigravity front matter = %q",
			antigravityRuleFrontMatter,
		)
	}

	existing := []byte(
		"---\ntrigger: glob\nglobs: internal/**\n---\n- Existing rule.",
	)
	updated, err := applyRuleToConfig(
		antigravityProjectRuleFile,
		existing,
		"Run the focused verification command.",
		issue,
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != string(existing)+
		"\n\n- Run the focused verification command.\n" {
		t.Fatalf("existing Antigravity rule file = %q", updated)
	}
	if strings.Count(string(updated), "trigger:") != 1 {
		t.Fatalf("existing front matter was rewritten: %q", updated)
	}
}

func TestAntigravityProjectRuleRejectsPermissionFixes(t *testing.T) {
	issue := issueintel.Issue{
		SuggestedFix: issueintel.SuggestedFix{
			Kind: "permission_allowlist",
		},
	}
	if _, err := applyRuleToConfig(
		antigravityProjectRuleFile,
		nil,
		"Allow the verified command.",
		issue,
	); err == nil {
		t.Fatal("Antigravity rule accepted a permission fix")
	}
}
