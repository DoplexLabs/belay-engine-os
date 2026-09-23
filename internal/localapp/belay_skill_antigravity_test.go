package localapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
)

// TestBelaySkillCoversAntigravityInvocation keeps the managed skill accurate
// for the Antigravity install, which reads the same SKILL.md as Claude Code,
// Codex, and Cursor from ~/.gemini/config/skills/belay.
func TestBelaySkillCoversAntigravityInvocation(t *testing.T) {
	for _, required := range []string{
		"in Claude Code, Codex, Cursor, or Antigravity.",
		"In Antigravity, invoke it with\n`/belay` in the agent chat.",
		"Antigravity: `/belay start` or `/belay start --issue <issue_id>`.",
		"`harness: antigravity` in Antigravity",
		"Antigravity: `/belay status` or `/belay status <receipt_id>`.",
		"Antigravity: `/belay learn`.",
		"`antigravity` in Antigravity; never infer another harness.",
		"Antigravity: `/belay pause`.",
		"Antigravity: `/belay <issue_id>`.",
	} {
		if !strings.Contains(belaySkillBody, required) {
			t.Errorf("skill is missing Antigravity guidance %q", required)
		}
	}
	// Every harness command line must keep its Antigravity counterpart.
	for _, claudeLine := range []string{
		"Claude Code: `/belay start`",
		"Claude Code: `/belay status`",
		"Claude Code: `/belay learn`",
		"Claude Code: `/belay pause`",
		"Claude Code: `/belay <issue_id>`",
	} {
		if !strings.Contains(belaySkillBody, claudeLine) {
			t.Errorf("skill lost Claude Code invocation %q", claudeLine)
		}
	}
	if strings.Count(belaySkillBody, "Antigravity") < 9 {
		t.Errorf(
			"skill names Antigravity only %d times",
			strings.Count(belaySkillBody, "Antigravity"),
		)
	}
	// Antigravity's SKILL.md frontmatter requires a description; the
	// ownership marker is how Belay recognizes its own install.
	if !strings.HasPrefix(belaySkillBody, "---\n") ||
		!strings.Contains(belaySkillBody, "\ndescription: ") {
		t.Error("skill frontmatter lost its description")
	}
	if !strings.Contains(belaySkillBody, "<!-- managed-by: belay-local -->") {
		t.Error("skill lost the Belay ownership marker")
	}
}

func TestInstallBelaySkillsInstallsIntoAntigravityConfigRoot(t *testing.T) {
	home := t.TempDir()
	redirectHome(t, home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	inventory := belaySkillTestInventory(false, false, false, true)
	results, err := InstallBelaySkills(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 ||
		results[3].Agent != "antigravity" ||
		!results[3].Detected ||
		results[3].Status != "installed" ||
		!results[3].Changed {
		t.Fatalf("antigravity results = %+v", results)
	}
	for _, other := range results[:3] {
		if other.Status != "unavailable" || other.Changed {
			t.Fatalf("undetected harness result = %+v, want unavailable", other)
		}
	}
	path := filepath.Join(home, ".gemini", "config", "skills", "belay", "SKILL.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != belaySkillBody {
		t.Fatalf("installed antigravity skill at %s differs from embed", path)
	}
	assertBelaySkillInvariants(t, string(body))
	for _, foreign := range []string{
		filepath.Join(home, ".cursor"),
		filepath.Join(home, ".gemini", "skills"),
		filepath.Join(home, ".gemini", "antigravity"),
	} {
		if _, err := os.Lstat(foreign); !os.IsNotExist(err) {
			t.Fatalf("antigravity install touched %q (stat error %v)", foreign, err)
		}
	}
	results, err = InstallBelaySkills(inventory)
	if err != nil || results[3].Status != "unchanged" || results[3].Changed {
		t.Fatalf("idempotent antigravity results/error = %+v/%v", results, err)
	}
}

// Antigravity documents no configuration-root override, so a GEMINI_HOME or
// ANTIGRAVITY_HOME-style environment variable must not move the install away
// from the real home directory.
func TestAntigravitySkillRootIgnoresEnvironmentOverrides(t *testing.T) {
	home := t.TempDir()
	redirectHome(t, home)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	for _, name := range []string{
		"ANTIGRAVITY_HOME",
		"ANTIGRAVITY_CONFIG_DIR",
		"GEMINI_HOME",
		"GEMINI_CONFIG_DIR",
		"GEMINI_CLI_HOME",
	} {
		t.Setenv(name, elsewhere)
	}
	root, err := skillConfigRoot(numbat.AgentAntigravity)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".gemini", "config"); root != want {
		t.Fatalf("antigravity skill root = %q, want %q", root, want)
	}
}
