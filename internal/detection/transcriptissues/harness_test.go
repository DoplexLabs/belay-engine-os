package transcriptissues

import (
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

// Cursor reads AGENTS.md and has no CLAUDE.md equivalent, so Cursor sessions
// must count toward AGENTS.md rather than falling through unweighted and
// letting a single Claude session decide the target.
func TestInstructionTargetCountsCursorWithAgentsFile(t *testing.T) {
	for _, test := range []struct {
		name   string
		agents []string
		want   string
	}{
		{"cursor only", []string{"cursor", "cursor"}, "AGENTS.md"},
		{"cursor outweighs claude", []string{"claude-code", "cursor", "cursor"}, "AGENTS.md"},
		{"claude outweighs cursor", []string{"claude-code", "claude-code", "cursor"}, "CLAUDE.md"},
		{"cursor joins codex", []string{"claude-code", "codex", "cursor"}, "AGENTS.md"},
		{"claude only", []string{"claude-code"}, "CLAUDE.md"},
		{"unknown agents fall back", []string{"mystery"}, "AGENTS.md"},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := preparedProject{}
			for _, agent := range test.agents {
				project.sessions = append(project.sessions, preparedSession{
					metadata: transcript.Session{Agent: agent},
				})
			}
			if got := instructionTarget(project); got != test.want {
				t.Fatalf("instructionTarget(%v) = %q, want %q", test.agents, got, test.want)
			}
		})
	}
}
