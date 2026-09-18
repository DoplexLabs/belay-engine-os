package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverHonorsHarnessHomesAndGroupsClaudeSubagents(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude-home")
	codexRoot := filepath.Join(root, "codex-home")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", codexRoot)

	sessionID := "11111111-1111-4111-8111-111111111111"
	project := filepath.Join(claudeRoot, "projects", "-synthetic-project")
	parent := filepath.Join(project, sessionID+".jsonl")
	subagent := filepath.Join(
		project,
		sessionID,
		"subagents",
		"agent-synthetic.jsonl",
	)
	codex := filepath.Join(
		codexRoot,
		"sessions",
		"2026",
		"08",
		"01",
		"rollout-2026-08-01T10-00-00-22222222-2222-4222-8222-222222222222.jsonl",
	)
	for _, path := range []string{parent, subagent, codex} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("sources = %+v, want 3", got)
	}
	var claude []Source
	for _, source := range got {
		switch source.Agent {
		case AgentClaude:
			claude = append(claude, source)
		case AgentCodex:
			if source.NativeSessionID != "22222222-2222-4222-8222-222222222222" ||
				!source.Primary {
				t.Fatalf("Codex source = %+v", source)
			}
		default:
			t.Fatalf("unexpected source = %+v", source)
		}
	}
	if len(claude) != 2 ||
		claude[0].GroupKey != claude[1].GroupKey ||
		!claude[0].Primary ||
		claude[1].Primary ||
		claude[0].NativeSessionID != sessionID ||
		claude[1].NativeSessionID != sessionID {
		t.Fatalf("Claude grouping = %+v", claude)
	}
}

func TestDiscoverMissingHarnessHomesIsEmpty(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "missing-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	got, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("sources = %+v, want none", got)
	}
}
