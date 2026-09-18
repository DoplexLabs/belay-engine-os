package localapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
)

func TestInstallBelaySkillsUsesHarnessConfigRootsAndIsIdempotent(t *testing.T) {
	claudeRoot := filepath.Join(t.TempDir(), "claude")
	codexRoot := filepath.Join(t.TempDir(), "codex")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeRoot)
	t.Setenv("CODEX_HOME", codexRoot)
	inventory := belaySkillTestInventory(true, true)
	results, err := InstallBelaySkills(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 ||
		results[0].Status != "installed" ||
		results[1].Status != "installed" {
		t.Fatalf("install results = %+v", results)
	}
	for _, root := range []string{claudeRoot, codexRoot} {
		path := filepath.Join(root, "skills", "belay", "SKILL.md")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != belaySkillBody {
			t.Fatalf("installed skill body at %s differs from embed", path)
		}
		assertBelaySkillInvariants(t, string(body))
	}
	results, err = InstallBelaySkills(inventory)
	if err != nil || results[0].Status != "unchanged" ||
		results[1].Status != "unchanged" {
		t.Fatalf("idempotent results/error = %+v/%v", results, err)
	}
}

func assertBelaySkillInvariants(t *testing.T, body string) {
	t.Helper()

	required := []string{
		"Invoke the skill with `/belay` in Claude Code and `$belay` in Codex.",
		"Claude Code: `/belay start` or `/belay start --issue <issue_id>`.",
		"Codex: `$belay start` or `$belay start --issue <issue_id>`.",
		"`readmodel.rendered_markdown` as the canonical preview",
		"Use this Mission Pack for this session?",
		"explicit, unambiguous yes",
		"`experiences` is nonempty",
		"`authority: user_approved`",
		"`record_mission_pack_accepted` with only the exact structured `pack_id`",
		"only after that call\n    succeeds",
		"Belay could not activate the Mission Pack for\n    this session",
		"must not create a receipt",
		"legacy or no-experience pack",
		"never call `record_mission_pack_accepted`",
		"Claude Code: `/belay status`",
		"Codex: `$belay status`",
		"`get_mission_pack_status` only with the exact hidden `receipt_id`",
		"exact receipt ID explicitly supplied by the user",
		"say status is unavailable",
		"Never search for, infer, or guess a receipt",
		"at most two evidence\n   excerpts",
		"Never describe verifier satisfaction as task success",
		"Claude Code: `/belay learn`",
		"Codex: `$belay learn`",
		"`list_experience_proposals` with that cwd, harness, and `limit: 5`",
		"Belay found no new guidance to review.",
		"Make no mutation until the choice is explicit and\n   unambiguous",
		"`resolve_experience_proposal` once with the\n   first item's exact hidden `proposal_id`",
		"`approval_mode: as_proposed`",
		"change only what the user explicitly requested",
		"ask for a second explicit confirmation",
		"Activate this guidance now?",
		"Only an explicit yes may continue",
		"transition committed but delivery is pending",
		"Do not replay\n    the lifecycle mutation",
		"Claude Code: `/belay pause`",
		"Codex: `$belay pause`",
		"`list_active_experiences` with that cwd and `limit: 5`",
		"Do not pass or\n   infer a harness filter",
		"Belay found no active guidance to\n   pause.",
		"Show at most five plain-language choices",
		"Never\n   display experience IDs",
		"Do not infer a latest, closest, or\n   default item",
		"exact hidden `experience` reference",
		"Only after an explicit, unambiguous selection",
		"Preparing is read-only and does not pause",
		"Pause this guidance now?",
		"separate\n   explicit confirmation immediately before apply",
		"No explicit, unambiguous yes means no mutation",
		"`apply_experience_lifecycle` once with the same exact hidden experience",
		"Never\n   replay the spent lifecycle token",
		"absent from\n   newly generated Mission Packs",
		"Claude Code: `/belay <issue_id>`. Codex: `$belay <issue_id>`.",
		"Keep this workflow unchanged",
		"`propose_fix`",
		"`record_fix_applied`",
	}
	for _, snippet := range required {
		if !strings.Contains(body, snippet) {
			t.Errorf("skill is missing behavioral invariant %q", snippet)
		}
	}

	for _, forbidden := range []string{
		"evidence IDs",
		"action tokens:",
		"proposal IDs:",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("skill exposes forbidden Learn detail %q", forbidden)
		}
	}
}

func TestInstallBelaySkillsSkipsUndetectedHarnesses(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	results, err := InstallBelaySkills(belaySkillTestInventory(false, true))
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "unavailable" ||
		results[1].Status != "installed" {
		t.Fatalf("results = %+v", results)
	}
}

func TestInstallBelaySkillsDoesNotOverwriteForeignSkillOrFollowSymlink(
	t *testing.T,
) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "claude"))
	skillDirectory := filepath.Join(root, "skills", "belay")
	if err := os.MkdirAll(skillDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(skillDirectory, "SKILL.md")
	if err := os.WriteFile(
		target,
		[]byte("---\nname: custom\n---\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	results, err := InstallBelaySkills(belaySkillTestInventory(true, false))
	if err == nil || results[0].Status != "conflict" {
		t.Fatalf("foreign skill results/error = %+v/%v", results, err)
	}
	body, readErr := os.ReadFile(target)
	if readErr != nil || !strings.Contains(string(body), "name: custom") {
		t.Fatalf("foreign skill changed/error = %q/%v", body, readErr)
	}

	symlinkRoot := filepath.Join(t.TempDir(), "codex-link")
	outside := t.TempDir()
	if err := os.Symlink(outside, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", symlinkRoot)
	results, err = InstallBelaySkills(belaySkillTestInventory(true, false))
	if err == nil || results[0].Status != "failed" {
		t.Fatalf("symlink results/error = %+v/%v", results, err)
	}
}

func belaySkillTestInventory(
	codex, claude bool,
) numbat.Inventory {
	inventory := numbat.Inventory{
		LaunchTargets: make(map[numbat.Agent]numbat.InventoryRow),
	}
	if codex {
		inventory.LaunchTargets[numbat.AgentCodex] = numbat.InventoryRow{
			Agent:    "codex",
			Present:  true,
			Detected: true,
		}
	}
	if claude {
		inventory.LaunchTargets[numbat.AgentClaude] = numbat.InventoryRow{
			Agent:    "claude",
			Present:  true,
			Detected: true,
		}
	}
	return inventory
}
