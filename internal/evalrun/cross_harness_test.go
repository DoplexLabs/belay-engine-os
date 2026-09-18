package evalrun

import (
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestCodexEventsTranscriptRetainsExactSuccessfulCommandEvidence(
	t *testing.T,
) {
	body := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-real"}`,
		`{"type":"item.completed","item":{"id":"message-1","type":"agent_message","text":"I will verify first."}}`,
		`{"type":"item.completed","item":{"id":"command-1","type":"command_execution","command":"/bin/zsh -lc 'go test ./...'","aggregated_output":"ok example.com/proof","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"message-2","type":"agent_message","text":"Tests passed."}}`,
		`{"type":"turn.completed","usage":{"input_tokens":120,"cached_input_tokens":20,"cache_write_input_tokens":100,"output_tokens":30,"reasoning_output_tokens":5}}`,
	}, "\n")
	session, turns, err := codexEventsTranscript(
		"/tmp/project",
		time.Date(2026, 9, 12, 7, 0, 0, 0, time.UTC),
		[]byte(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	if session.NativeSessionID != "thread-real" ||
		session.TotalInputTokens == nil ||
		*session.TotalInputTokens != 120 ||
		session.TotalOutputTokens == nil ||
		*session.TotalOutputTokens != 30 ||
		session.TotalCacheWriteTokens == nil ||
		*session.TotalCacheWriteTokens != 100 ||
		len(turns) != 4 {
		t.Fatalf("session/turns = %+v/%+v", session, turns)
	}
	if turns[1].Role != transcript.RoleToolCall ||
		turns[1].Payload.RawCommand != "go test ./..." ||
		turns[2].Role != transcript.RoleToolResult ||
		turns[2].Payload.ExitCode == nil ||
		*turns[2].Payload.ExitCode != 0 ||
		turns[2].Payload.ToolCallID != turns[1].Payload.ToolCallID {
		t.Fatalf("command evidence = %+v/%+v", turns[1], turns[2])
	}
}

func TestComparativeSummaryPreservesNeutralCompiledResult(t *testing.T) {
	cost := 0.1
	runs := []ComparativeRun{
		{
			Baseline:               BaselineRetrievalOnly,
			Repetition:             1,
			TaskSuccess:            true,
			VerificationCompliance: true,
			CostUSD:                &cost,
		},
		{
			Baseline:               BaselineCompiledBelay,
			Repetition:             1,
			TaskSuccess:            true,
			VerificationCompliance: true,
			CostUSD:                &cost,
		},
	}
	got := summarizeComparativeRuns(runs)
	if got.CompiledVsRetrieval != "neutral" ||
		got.Decision != "do_not_advance_runtime_gate" ||
		len(got.ByBaseline) != 2 {
		t.Fatalf("summary = %+v", got)
	}
}
