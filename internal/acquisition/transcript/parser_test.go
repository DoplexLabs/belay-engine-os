package transcript

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	belaytranscript "github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestParseClaudeFixtureScrubsChargesOnceAndJoinsSession(t *testing.T) {
	path := transcriptFixture(t, "claude", "main.jsonl")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "11111111-1111-4111-8111-111111111111"
	result, err := Parse(
		context.Background(),
		Source{
			Agent:           AgentClaude,
			Path:            path,
			NativeSessionID: sessionID,
			Primary:         true,
		},
		bytes.NewReader(body),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Issues != 0 || result.EndOffset != int64(len(body)) {
		t.Fatalf("parse result = %+v", result)
	}
	if len(result.Turns) != 6 {
		t.Fatalf("turn count = %d, want 6", len(result.Turns))
	}
	wantSession := testSessionKey(AgentClaude, sessionID)
	charged := 0
	var chargedCost float64
	foundToolResult := false
	for _, turn := range result.Turns {
		if turn.SessionKey != wantSession {
			t.Fatalf("session key = %q, want %q", turn.SessionKey, wantSession)
		}
		if turn.Payload.ParserVersion != ParserVersion ||
			turn.Payload.PriceTableVersion != PriceTableVersion ||
			turn.Payload.SourceFileID == "" {
			t.Fatalf("turn provenance = %+v", turn.Payload)
		}
		if turn.InputTokens != nil && *turn.InputTokens == 1_000_000 {
			charged++
			if turn.CostUSD == nil {
				t.Fatal("known Claude model cost is nil")
			}
			chargedCost = *turn.CostUSD
		}
		if turn.Role == belaytranscript.RoleToolResult {
			foundToolResult = true
			if turn.Payload.ToolIsError == nil || *turn.Payload.ToolIsError {
				t.Fatalf("Claude is_error not retained: %+v", turn.Payload)
			}
		}
	}
	if charged != 1 || chargedCost != 46.75 || !foundToolResult {
		t.Fatalf("charged turns/cost = %d/%v, want 1/46.75", charged, chargedCost)
	}
	encoded, err := json.Marshal(result.Turns)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"supersecretvalue",
		"anothersecretvalue",
		"private-result-value",
		"PRIVATE_REASONING_CANARY",
		"PRIVATE_ATTACHMENT_CANARY",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("stored turns contain %q", forbidden)
		}
	}
	if got := result.AssistantToolUseByUUID["claude-assistant-tool-parent"]; got != "tool-parent-1" {
		t.Fatalf("assistant tool map = %q", got)
	}
	firstLineEnd := int64(bytes.IndexByte(body, '\n') + 1)
	var assistantOffset, toolOffset int64 = -1, -1
	for _, turn := range result.Turns {
		switch turn.Role {
		case belaytranscript.RoleAssistant:
			if turn.Payload.Text == "I will inspect it." {
				assistantOffset = turn.Payload.JSONLByteOffset
			}
		case belaytranscript.RoleToolCall:
			toolOffset = turn.Payload.JSONLByteOffset
			if len(turn.Payload.ToolInput) > maxToolInputBytes ||
				!json.Valid(turn.Payload.ToolInput) {
				t.Fatalf("tool input invalid or oversized: %d", len(turn.Payload.ToolInput))
			}
		}
	}
	if assistantOffset != firstLineEnd || toolOffset != firstLineEnd {
		t.Fatalf(
			"assistant/tool offsets = %d/%d, want %d",
			assistantOffset,
			toolOffset,
			firstLineEnd,
		)
	}
}

func TestParseClaudeSubagentUsesExactParentToolID(t *testing.T) {
	mainPath := transcriptFixture(t, "claude", "main.jsonl")
	mainBody, _ := os.ReadFile(mainPath)
	sessionID := "11111111-1111-4111-8111-111111111111"
	main, err := Parse(
		context.Background(),
		Source{Agent: AgentClaude, Path: mainPath, NativeSessionID: sessionID},
		bytes.NewReader(mainBody),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	subPath := transcriptFixture(t, "claude", "subagent.jsonl")
	subBody, _ := os.ReadFile(subPath)
	sub, err := Parse(
		context.Background(),
		Source{Agent: AgentClaude, Path: subPath, NativeSessionID: sessionID},
		bytes.NewReader(subBody),
		0,
		testParseOptions(ParseOptions{
			ParentToolUses: main.AssistantToolUseByUUID,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub.Turns) != 1 ||
		sub.Turns[0].Payload.ParentToolUseID != "tool-parent-1" {
		t.Fatalf("subagent turns = %+v", sub.Turns)
	}
}

func TestParseCodexUsesDeltaUsageAndSuppressesDuplicateRepresentations(t *testing.T) {
	path := transcriptFixture(
		t,
		"codex",
		"rollout-2026-08-01T10-00-00-22222222-2222-4222-8222-222222222222.jsonl",
	)
	body, _ := os.ReadFile(path)
	sessionID := "22222222-2222-4222-8222-222222222222"
	result, err := Parse(
		context.Background(),
		Source{Agent: AgentCodex, Path: path, NativeSessionID: sessionID},
		bytes.NewReader(body),
		0,
		testParseOptions(ParseOptions{FinalizePendingUsage: true}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Issues != 0 || len(result.Turns) != 6 ||
		len(result.State.EventMessageFallbacks) != 0 {
		t.Fatalf("Codex result = %+v", result)
	}
	var billed, toolResults, compactions int
	for _, turn := range result.Turns {
		if turn.InputTokens != nil {
			billed++
			if *turn.InputTokens != 70 ||
				turn.OutputTokens == nil || *turn.OutputTokens != 20 ||
				turn.CacheReadTokens == nil || *turn.CacheReadTokens != 25 ||
				turn.CacheWriteTokens == nil || *turn.CacheWriteTokens != 5 {
				t.Fatalf("Codex delta usage = %+v", turn)
			}
			if turn.CostUSD == nil || *turn.CostUSD != 0.0017875 {
				t.Fatalf("Codex exact model cost = %v, want 0.0017875", turn.CostUSD)
			}
		}
		if turn.Role == belaytranscript.RoleToolResult {
			toolResults++
			if !strings.Contains(turn.Payload.ToolResult, "structured head") ||
				!strings.Contains(turn.Payload.ToolResult, "structured tail") {
				t.Fatalf("structured command result was not retained: %+v", turn)
			}
			if turn.Payload.ExitCode == nil || *turn.Payload.ExitCode != 1 ||
				turn.Payload.DurationMS == nil || *turn.Payload.DurationMS != 731 {
				t.Fatalf("structured command evidence = %+v", turn.Payload)
			}
		}
		if turn.Role == belaytranscript.RoleCompactionSummary {
			compactions++
		}
	}
	if billed != 1 || toolResults != 1 || compactions != 1 {
		t.Fatalf(
			"billed/results/compactions = %d/%d/%d",
			billed,
			toolResults,
			compactions,
		)
	}
	encoded, _ := json.Marshal(result.Turns)
	for _, forbidden := range []string{
		"PRIVATE_CODEX_REASONING_CANARY",
		"private-codex-key",
		"private-output",
		"private-error",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("stored Codex turns contain %q", forbidden)
		}
	}
}

func TestParseCodexTokenCountFallbackUsesLastDeltaOnly(t *testing.T) {
	path := transcriptFixture(t, "codex", "token-count-only.jsonl")
	body, _ := os.ReadFile(path)
	result, err := Parse(
		context.Background(),
		Source{
			Agent:           AgentCodex,
			Path:            path,
			NativeSessionID: "33333333-3333-4333-8333-333333333333",
		},
		bytes.NewReader(body),
		0,
		testParseOptions(ParseOptions{FinalizePendingUsage: true}),
	)
	if err != nil {
		t.Fatal(err)
	}
	var billed *belaytranscript.Turn
	for index := range result.Turns {
		if result.Turns[index].InputTokens != nil {
			billed = &result.Turns[index]
		}
	}
	if len(result.Turns) != 2 ||
		billed == nil ||
		*billed.InputTokens != 4 ||
		billed.CostUSD != nil {
		t.Fatalf("fallback turn = %+v", result.Turns)
	}
}

func TestParseCodexChildRolloutParentLinkage(t *testing.T) {
	const (
		groupSessionID = "56565656-5656-4656-8656-565656565656"
		childThreadID  = "child-thread-1"
		parentThreadID = "parent-thread-1"
	)
	childBody := strings.Join([]string{
		`{"timestamp":"2026-09-10T10:00:00Z","type":"session_meta","payload":{"id":"` + childThreadID + `","parent_thread_id":"` + parentThreadID + `","cwd":"/synthetic/codex"}}`,
		`{"timestamp":"2026-09-10T10:00:00.100Z","type":"turn_context","payload":{"turn_id":"turn-child","model":"openai.gpt-5.6"}}`,
		`{"timestamp":"2026-09-10T10:00:00.200Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Inspect the delegated task."}]}}`,
		`{"timestamp":"2026-09-10T10:00:00.300Z","type":"response_item","payload":{"type":"function_call","call_id":"call-child","name":"exec_command","arguments":"{\"cmd\":\"pwd\"}"}}`,
		`{"timestamp":"2026-09-10T10:00:00.400Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-child","output":"ok"}}`,
		"",
	}, "\n")
	parse := func(
		t *testing.T,
		body string,
		sourceSessionID string,
		options ParseOptions,
	) Result {
		t.Helper()
		result, err := Parse(
			context.Background(),
			Source{
				Agent:           AgentCodex,
				Path:            "/synthetic/codex-parent.jsonl",
				NativeSessionID: sourceSessionID,
			},
			strings.NewReader(body),
			0,
			testParseOptions(options),
		)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	t.Run("stable fallback", func(t *testing.T) {
		result := parse(
			t,
			childBody,
			groupSessionID,
			ParseOptions{},
		)
		want := "codex-parent-thread:" + parentThreadID
		if len(result.Turns) != 3 ||
			result.State.ParentThreadID != parentThreadID {
			t.Fatalf("child rollout result = %+v", result)
		}
		for _, turn := range result.Turns {
			if turn.Payload.ParentToolUseID != want {
				t.Fatalf(
					"child turn parent = %q, want %q: %+v",
					turn.Payload.ParentToolUseID,
					want,
					turn,
				)
			}
		}
		encoded, err := json.Marshal(result.State)
		if err != nil {
			t.Fatal(err)
		}
		var persisted State
		if err := json.Unmarshal(encoded, &persisted); err != nil ||
			persisted.ParentThreadID != parentThreadID {
			t.Fatalf(
				"persisted parent thread state = %+v/%v",
				persisted,
				err,
			)
		}
	})

	t.Run("exact mapping wins", func(t *testing.T) {
		result := parse(
			t,
			childBody,
			groupSessionID,
			ParseOptions{State: State{
				ThreadParentTool: map[string]string{
					childThreadID: "collab-tool-call-1",
				},
			}},
		)
		if len(result.Turns) != 3 {
			t.Fatalf("exact-mapping turns = %+v", result.Turns)
		}
		for _, turn := range result.Turns {
			if turn.Payload.ParentToolUseID != "collab-tool-call-1" {
				t.Fatalf("exact parent mapping lost: %+v", turn)
			}
		}
	})

	t.Run("top level remains unparented", func(t *testing.T) {
		topLevelID := "67676767-6767-4767-8767-676767676767"
		body := strings.Join([]string{
			`{"timestamp":"2026-09-10T10:10:00Z","type":"session_meta","payload":{"id":"` + topLevelID + `","cwd":"/synthetic/codex"}}`,
			`{"timestamp":"2026-09-10T10:10:00.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Top-level request."}]}}`,
			"",
		}, "\n")
		result := parse(t, body, topLevelID, ParseOptions{})
		if len(result.Turns) != 1 ||
			result.State.ParentThreadID != "" ||
			result.Turns[0].Payload.ParentToolUseID != "" {
			t.Fatalf("top-level rollout was parented: %+v", result)
		}
	})
}

func TestCodexLiveUsageWaitsForExactDeltaAcrossPolls(t *testing.T) {
	const sessionID = "34343434-3434-4434-8434-343434343434"
	first := strings.Join([]string{
		`{"timestamp":"2026-08-01T11:10:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/synthetic/codex"}}`,
		`{"timestamp":"2026-08-01T11:10:00.100Z","type":"turn_context","payload":{"turn_id":"turn-live","cwd":"/synthetic/codex","model":"openai.gpt-5.6-luna"}}`,
		`{"timestamp":"2026-08-01T11:10:00.200Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"output_tokens":10,"cached_input_tokens":20,"cache_write_input_tokens":5}}}}`,
		"",
	}, "\n")
	source := Source{
		Agent:           AgentCodex,
		Path:            "/synthetic/live.jsonl",
		NativeSessionID: sessionID,
	}
	initial, err := Parse(
		context.Background(),
		source,
		strings.NewReader(first),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Turns) != 0 ||
		len(initial.State.PendingCodexUsage) != 1 {
		t.Fatalf("initial live usage = %+v", initial)
	}
	second := `{"timestamp":"2026-08-01T11:10:00.300Z","type":"token_usage_record","payload":{"turn_id":"turn-live","usage":{"input_tokens":100,"output_tokens":10,"cached_input_tokens":20,"cache_write_input_tokens":5}}}` + "\n"
	exact, err := Parse(
		context.Background(),
		source,
		strings.NewReader(second),
		int64(len(first)),
		testParseOptions(ParseOptions{State: initial.State}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(exact.Turns) != 1 ||
		exact.Turns[0].InputTokens == nil ||
		*exact.Turns[0].InputTokens != 75 ||
		len(exact.State.PendingCodexUsage) != 0 {
		t.Fatalf("exact live usage = %+v", exact)
	}
	replay, err := Parse(
		context.Background(),
		source,
		strings.NewReader(second),
		int64(len(first)),
		testParseOptions(ParseOptions{State: initial.State}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.Turns) != 1 ||
		replay.Turns[0].SourceRecordKey != exact.Turns[0].SourceRecordKey {
		t.Fatalf("usage replay identity = %+v / %+v", exact.Turns, replay.Turns)
	}
}

func TestCodexEventMessageFallbackOnlyWithoutEquivalentResponse(t *testing.T) {
	const sessionID = "35353535-3535-4535-8535-353535353535"
	source := Source{
		Agent:           AgentCodex,
		Path:            "/synthetic/event-only.jsonl",
		NativeSessionID: sessionID,
	}
	eventOnly := strings.Join([]string{
		`{"timestamp":"2026-08-01T11:20:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/synthetic/codex"}}`,
		`{"timestamp":"2026-08-01T11:20:00.100Z","type":"turn_context","payload":{"turn_id":"turn-message","model":"openai.gpt-5.4"}}`,
		`{"timestamp":"2026-08-01T11:20:00.200Z","type":"event_msg","payload":{"type":"agent_message","message":"fallback text"}}`,
		"",
	}, "\n")
	fallback, err := Parse(
		context.Background(),
		source,
		strings.NewReader(eventOnly),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(fallback.Turns) != 1 ||
		fallback.Turns[0].Payload.Text != "fallback text" ||
		len(fallback.State.EventMessageFallbacks) != 1 {
		t.Fatalf("event fallback = %+v", fallback)
	}

	responseThenEvent := strings.Join([]string{
		`{"timestamp":"2026-08-01T11:20:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/synthetic/codex"}}`,
		`{"timestamp":"2026-08-01T11:20:00.100Z","type":"turn_context","payload":{"turn_id":"turn-message","model":"openai.gpt-5.4"}}`,
		`{"timestamp":"2026-08-01T11:20:00.150Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fallback text"}]}}`,
		`{"timestamp":"2026-08-01T11:20:00.200Z","type":"event_msg","payload":{"type":"agent_message","message":"fallback text"}}`,
		"",
	}, "\n")
	deduped, err := Parse(
		context.Background(),
		source,
		strings.NewReader(responseThenEvent),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(deduped.Turns) != 1 ||
		len(deduped.State.EventMessageFallbacks) != 0 {
		t.Fatalf("equivalent message dedupe = %+v", deduped)
	}
}

func TestCodexResultDeduplicationSurvivesCursorBoundary(t *testing.T) {
	const sessionID = "abababab-abab-4bab-8bab-abababababab"
	first := strings.Join([]string{
		`{"timestamp":"2026-08-01T11:30:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","session_id":"` + sessionID + `","cwd":"/synthetic/codex"}}`,
		`{"timestamp":"2026-08-01T11:30:00.100Z","type":"turn_context","payload":{"turn_id":"turn-boundary","cwd":"/synthetic/codex","model":"unknown"}}`,
		`{"timestamp":"2026-08-01T11:30:00.200Z","type":"response_item","payload":{"type":"function_call","call_id":"call-boundary","name":"exec_command","arguments":"{\"cmd\":\"false\"}"}}`,
		`{"timestamp":"2026-08-01T11:30:00.300Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-boundary","output":"failed"}}`,
		"",
	}, "\n")
	source := Source{
		Agent:           AgentCodex,
		Path:            "/synthetic/codex-boundary.jsonl",
		NativeSessionID: sessionID,
	}
	initial, err := Parse(
		context.Background(),
		source,
		strings.NewReader(first),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	second := `{"timestamp":"2026-08-01T11:30:00.400Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"call-boundary","command":["false"],"cwd":"/synthetic/codex","formatted_output":"failed"}}}` + "\n"
	continued, err := Parse(
		context.Background(),
		source,
		strings.NewReader(second),
		int64(len(first)),
		testParseOptions(ParseOptions{State: initial.State}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Turns) != 2 || len(continued.Turns) != 0 {
		t.Fatalf(
			"initial/continued turn counts = %d/%d",
			len(initial.Turns),
			len(continued.Turns),
		)
	}
}

func TestCodexStructuredEvidenceSurvivesCursorBoundary(t *testing.T) {
	const sessionID = "bcbcbcbc-bcbc-4cbc-8cbc-bcbcbcbcbcbc"
	first := strings.Join([]string{
		`{"timestamp":"2026-08-01T11:40:00Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/synthetic/codex"}}`,
		`{"timestamp":"2026-08-01T11:40:00.100Z","type":"turn_context","payload":{"turn_id":"turn-evidence","model":"unknown"}}`,
		`{"timestamp":"2026-08-01T11:40:00.200Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-evidence","output":"failed"}}`,
		"",
	}, "\n")
	source := Source{
		Agent:           AgentCodex,
		Path:            "/synthetic/codex-evidence.jsonl",
		NativeSessionID: sessionID,
	}
	initial, err := Parse(
		context.Background(),
		source,
		strings.NewReader(first),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	second := `{"timestamp":"2026-08-01T11:40:00.300Z","type":"event_msg","payload":{"type":"item_completed","started_at_ms":1000,"completed_at_ms":1750,"item":{"type":"CommandExecution","id":"call-evidence","command":["false"],"exit_code":9}}}` + "\n"
	continued, err := Parse(
		context.Background(),
		source,
		strings.NewReader(second),
		int64(len(first)),
		testParseOptions(ParseOptions{State: initial.State}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(continued.Turns) != 1 ||
		!continued.Turns[0].Payload.SupplementalEvidence ||
		continued.Turns[0].Payload.ExitCode == nil ||
		*continued.Turns[0].Payload.ExitCode != 9 ||
		continued.Turns[0].Payload.DurationMS == nil ||
		*continued.Turns[0].Payload.DurationMS != 750 ||
		continued.Turns[0].Payload.ToolCallID != "call-evidence" {
		t.Fatalf("supplemental evidence = %+v", continued.Turns)
	}
}

func TestToolPayloadCapsPreserveJSONAndResultHeadTail(t *testing.T) {
	largeInput := strings.Repeat("input-prefix-", 1<<20) + "input-tail"
	largeResult := "result-head-" + strings.Repeat("x", 12<<20) + "-result-tail"
	line1, _ := json.Marshal(map[string]any{
		"type":      "assistant",
		"sessionId": "44444444-4444-4444-8444-444444444444",
		"uuid":      "large-tool",
		"timestamp": "2026-08-01T12:00:00Z",
		"cwd":       "/synthetic/large",
		"message": map[string]any{
			"role":  "assistant",
			"model": "unknown",
			"content": []any{map[string]any{
				"type": "tool_use",
				"id":   "large-call",
				"name": "Large",
				"input": map[string]any{
					"value": largeInput,
				},
			}},
			"usage": map[string]any{
				"input_tokens":                0,
				"output_tokens":               0,
				"cache_read_input_tokens":     0,
				"cache_creation_input_tokens": 0,
			},
		},
	})
	line2, _ := json.Marshal(map[string]any{
		"type":      "user",
		"sessionId": "44444444-4444-4444-8444-444444444444",
		"uuid":      "large-result",
		"timestamp": "2026-08-01T12:00:01Z",
		"cwd":       "/synthetic/large",
		"message": map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type":        "tool_result",
				"tool_use_id": "large-call",
				"content":     largeResult,
			}},
		},
	})
	body := append(append(append([]byte{}, line1...), '\n'), line2...)
	body = append(body, '\n')
	result, err := Parse(
		context.Background(),
		Source{
			Agent:           AgentClaude,
			Path:            "/synthetic/large.jsonl",
			NativeSessionID: "44444444-4444-4444-8444-444444444444",
		},
		bytes.NewReader(body),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(result.Turns))
	}
	if len(result.Turns[0].Payload.ToolInput) > maxToolInputBytes ||
		!json.Valid(result.Turns[0].Payload.ToolInput) {
		t.Fatalf("tool input bytes = %d", len(result.Turns[0].Payload.ToolInput))
	}
	gotResult := result.Turns[1].Payload.ToolResult
	if len(gotResult) > maxToolResultBytes ||
		!strings.HasPrefix(gotResult, "result-head-") ||
		!strings.HasSuffix(gotResult, "-result-tail") ||
		!strings.Contains(gotResult, "[belay truncated") {
		t.Fatalf("truncated result shape invalid: bytes=%d", len(gotResult))
	}
}

func TestParseWaitsForFinalNewlineAndConsumesMalformedCompleteLine(t *testing.T) {
	line := `{"type":"user","sessionId":"55555555-5555-4555-8555-555555555555","timestamp":"2026-08-01T13:00:00Z","cwd":"/synthetic","message":{"role":"user","content":"hello"}}`
	source := Source{
		Agent:           AgentClaude,
		Path:            "/synthetic/incomplete.jsonl",
		NativeSessionID: "55555555-5555-4555-8555-555555555555",
	}
	incomplete, err := Parse(
		context.Background(),
		source,
		strings.NewReader(line),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if incomplete.Complete || incomplete.EndOffset != 0 || len(incomplete.Turns) != 0 {
		t.Fatalf("incomplete result = %+v", incomplete)
	}
	complete, err := Parse(
		context.Background(),
		source,
		strings.NewReader("{bad}\n"),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !complete.Complete || complete.EndOffset != 6 ||
		complete.Issues != 1 || len(complete.Turns) != 0 {
		t.Fatalf("malformed result = %+v", complete)
	}
}

func TestNativeJSONSessionIDWinsOverFilenameHint(t *testing.T) {
	const (
		filenameHint = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		nativeID     = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	)
	line := claudeFixtureLine(nativeID)
	result, err := Parse(
		context.Background(),
		Source{
			Agent:           AgentClaude,
			Path:            "/synthetic/" + filenameHint + ".jsonl",
			NativeSessionID: filenameHint,
		},
		strings.NewReader(line+"\n"),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.NativeSessionID != nativeID ||
		len(result.Turns) != 1 ||
		result.Turns[0].SessionKey != testSessionKey(AgentClaude, nativeID) {
		t.Fatalf("native identity result = %+v", result)
	}
}

func TestPriceTableExactIDsOnly(t *testing.T) {
	oneMillion := int64(1_000_000)
	known := calculateCost("claude-haiku-4-5-20251001", usage{
		InputTokens:      &oneMillion,
		OutputTokens:     &oneMillion,
		CacheReadTokens:  &oneMillion,
		CacheWriteTokens: &oneMillion,
	})
	if known == nil || *known != 7.35 {
		t.Fatalf("known cost = %v, want 7.35", known)
	}
	for _, alias := range []string{"openai.gpt-5.6-sol/luna", "openai.gpt-6-astra-preview"} {
		if got := calculateCost(alias, usage{InputTokens: &oneMillion}); got != nil {
			t.Fatalf("custom alias %q cost = %v, want nil", alias, got)
		}
	}
}

func TestCodexPriceTiersAndCacheWriteDoesNotDoubleCharge(t *testing.T) {
	output := int64(1_000_000)
	cached := int64(100_000)
	cacheWrite := int64(50_000)
	for _, test := range []struct {
		name  string
		model string
		input int64
		want  float64
	}{
		{
			name:  "boundary remains short",
			model: "openai.gpt-5.6-sol",
			input: 272_000,
			want:  20 + float64(122_000)*4/1_000_000 + 0.04 + 0.25,
		},
		{
			name:  "above boundary is long",
			model: "openai.gpt-5.6-sol",
			input: 272_001,
			want:  30 + float64(122_001)*8/1_000_000 + 0.08 + 0.50,
		},
		{
			name:  "luna exact id",
			model: "openai.gpt-5.6-luna",
			input: 200_000,
			want:  1.2 + 0.01 + 0.002 + 0.0125,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := calculateCost(test.model, usage{
				InputTokens:       &test.input,
				OutputTokens:      &output,
				CacheReadTokens:   &cached,
				CacheWriteTokens:  &cacheWrite,
				InputIncludesRead: true,
			})
			if got == nil || math.Abs(*got-test.want) > 1e-12 {
				t.Fatalf("cost = %v, want %.12f", got, test.want)
			}
		})
	}
}

func TestCurrentModelCatalogAndExplicitAliases(t *testing.T) {
	oneMillion := int64(1_000_000)
	for _, test := range []struct {
		model string
		want  float64
	}{
		{model: "claude-sonnet-5", want: 22.05},
		{model: "claude-opus-5", want: 36.75},
		{model: "claude-fable-5", want: 73.5},
		{model: "openai.gpt-5.6-luna", want: 1.67},
		{model: "gpt-5.6-luna", want: 1.67},
		{model: "openai.gpt-6-astra", want: 122},
	} {
		t.Run(test.model, func(t *testing.T) {
			got := calculateCost(test.model, usage{
				InputTokens:      &oneMillion,
				OutputTokens:     &oneMillion,
				CacheReadTokens:  &oneMillion,
				CacheWriteTokens: &oneMillion,
			})
			if got == nil || math.Abs(*got-test.want) > 1e-12 {
				t.Fatalf("cost = %v, want %.12f", got, test.want)
			}
		})
	}
	if got := calculateCost("claude-sonnet-5-preview", usage{
		InputTokens: &oneMillion,
	}); got != nil {
		t.Fatalf("unverified alias cost = %v, want nil", got)
	}
}

func testParseOptions(values ...ParseOptions) ParseOptions {
	var result ParseOptions
	if len(values) > 0 {
		result = values[0]
	}
	result.Boundary = Boundary{
		SessionKey: testSessionKey,
		ScrubSecrets: func(value string) (string, int) {
			removed := 0
			for _, pattern := range testSecretPatterns {
				matches := pattern.FindAllStringIndex(value, -1)
				removed += len(matches)
				value = pattern.ReplaceAllString(value, "[redacted]")
			}
			return value, removed
		},
	}
	return result
}

func testSessionKey(agent, nativeSessionID string) string {
	sum := sha256.Sum256([]byte(agent + "\x00" + nativeSessionID + "\x00"))
	return "ses_" + hex.EncodeToString(sum[:16])
}

var testSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|passwd|authorization|cookie)\s*[:=]\s*[^\s,;]+`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

func claudeFixtureLine(sessionID string) string {
	return `{"type":"user","sessionId":"` + sessionID +
		`","timestamp":"2026-08-01T13:30:00Z","cwd":"/synthetic","message":{"role":"user","content":"hello"}}`
}

func transcriptFixture(t *testing.T, parts ...string) string {
	t.Helper()
	all := append([]string{"..", "..", "..", "testdata", "transcript"}, parts...)
	path, err := filepath.Abs(filepath.Join(all...))
	if err != nil {
		t.Fatal(err)
	}
	return path
}
