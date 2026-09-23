package localapp

import (
	"strings"
	"testing"
)

// TestBelaySkillCoversCursorInvocation keeps the managed skill accurate for
// the Cursor Agent install, which reads the same SKILL.md as Claude Code and
// Codex.
func TestBelaySkillCoversCursorInvocation(t *testing.T) {
	for _, required := range []string{
		"In\nCursor, invoke it with `/belay` in Agent chat.",
		"Cursor: `/belay start` or `/belay start --issue <issue_id>`.",
		"`harness: cursor` in Cursor",
		"Cursor: `/belay status` or `/belay status <receipt_id>`.",
		"Cursor: `/belay learn`.",
		"`cursor` in Cursor, and",
		"Cursor: `/belay pause`.",
		"Cursor: `/belay <issue_id>`.",
	} {
		if !strings.Contains(belaySkillBody, required) {
			t.Errorf("skill is missing Cursor guidance %q", required)
		}
	}
	// Every harness command line must keep its Cursor counterpart.
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
	if strings.Count(belaySkillBody, "Cursor") < 8 {
		t.Errorf(
			"skill names Cursor only %d times",
			strings.Count(belaySkillBody, "Cursor"),
		)
	}
	// Cursor has no CLAUDE.md, and the skill must never promise one.
	if strings.Contains(belaySkillBody, "CLAUDE.md") {
		t.Error("skill names a harness-specific instruction file")
	}
}
