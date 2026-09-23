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
	cursorRoot := filepath.Join(root, "cursor-home")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", codexRoot)
	t.Setenv("BELAY_CURSOR_HOME", cursorRoot)

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
	cursorProject := filepath.Join(
		cursorRoot,
		"projects",
		"7f3a9c2b5e1d",
		"agent-transcripts",
	)
	cursorConversation := "33333333-3333-4333-8333-333333333333"
	cursorThread := filepath.Join(
		cursorProject,
		"thread-"+cursorConversation+".jsonl",
	)
	cursorSubagent := filepath.Join(
		cursorProject,
		"thread-"+cursorConversation,
		"subagents",
		"agent-synthetic.jsonl",
	)
	for _, path := range []string{parent, subagent, codex, cursorThread, cursorSubagent} {
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
	if len(got) != 5 {
		t.Fatalf("sources = %+v, want 5", got)
	}
	var claude, cursor []Source
	for _, source := range got {
		switch source.Agent {
		case AgentClaude:
			claude = append(claude, source)
		case AgentCodex:
			if source.NativeSessionID != "22222222-2222-4222-8222-222222222222" ||
				!source.Primary {
				t.Fatalf("Codex source = %+v", source)
			}
		case AgentCursor:
			cursor = append(cursor, source)
		default:
			t.Fatalf("unexpected source = %+v", source)
		}
	}
	if len(cursor) != 2 ||
		cursor[0].GroupKey != cursor[1].GroupKey ||
		!cursor[0].Primary ||
		cursor[1].Primary ||
		cursor[0].NativeSessionID != cursorConversation ||
		cursor[1].NativeSessionID != cursorConversation ||
		cursor[0].ProjectKey != "7f3a9c2b5e1d" ||
		cursor[1].ProjectKey != "7f3a9c2b5e1d" {
		t.Fatalf("Cursor grouping = %+v", cursor)
	}
	if cursor[0].Path != cursorThread || cursor[1].Path != cursorSubagent {
		t.Fatalf("Cursor paths = %+v", cursor)
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
	t.Setenv("BELAY_CURSOR_HOME", filepath.Join(root, "missing-cursor"))
	got, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("sources = %+v, want none", got)
	}
}

// Cursor's project directory is an opaque hash, so two projects that happen to
// name a conversation the same way must not share a group, and a JSONL that is
// not below agent-transcripts is not a Cursor transcript at all.
func TestDiscoverCursorSeparatesProjectsAndIgnoresOtherJSONL(t *testing.T) {
	root := t.TempDir()
	cursorRoot := filepath.Join(root, "cursor-home")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "missing-claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex"))
	t.Setenv("BELAY_CURSOR_HOME", cursorRoot)

	first := filepath.Join(
		cursorRoot, "projects", "aaaa", "agent-transcripts", "chat.jsonl",
	)
	second := filepath.Join(
		cursorRoot, "projects", "bbbb", "agent-transcripts", "chat.jsonl",
	)
	outside := filepath.Join(cursorRoot, "projects", "aaaa", "index.jsonl")
	notJSONL := filepath.Join(
		cursorRoot, "projects", "aaaa", "agent-transcripts", "meta.txt",
	)
	for _, path := range []string{first, second, outside, notJSONL} {
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
	if len(got) != 2 {
		t.Fatalf("sources = %+v, want the two agent-transcripts files", got)
	}
	if got[0].GroupKey == got[1].GroupKey {
		t.Fatalf("same-named conversations in different projects share a group: %+v", got)
	}
	for _, source := range got {
		if source.Agent != AgentCursor ||
			source.NativeSessionID != "chat" ||
			!source.Primary {
			t.Fatalf("Cursor source = %+v", source)
		}
	}
}
