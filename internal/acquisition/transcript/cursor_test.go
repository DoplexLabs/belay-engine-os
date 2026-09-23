package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	belaytranscript "github.com/DoplexLabs/belay-engine/internal/transcript"
)

const cursorFixtureSession = "cur-11111111-1111-4111-8111-111111111111"

func parseCursorBody(t *testing.T, source Source, body string) Result {
	t.Helper()
	if source.Agent == "" {
		source.Agent = AgentCursor
	}
	if source.Path == "" {
		source.Path = "/synthetic/.cursor/projects/hash/agent-transcripts/thread.jsonl"
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	result, err := Parse(
		context.Background(),
		source,
		strings.NewReader(body),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// The full fixture pins the parsed turn sequence for every record shape Belay
// claims to support, and proves that a malformed line is a counted diagnostic
// rather than a hard failure.
func TestParseCursorFixtureCoversEveryRecordShape(t *testing.T) {
	path := transcriptFixture(t, "cursor", "main.jsonl")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Parse(
		context.Background(),
		Source{
			Agent:           AgentCursor,
			Path:            path,
			NativeSessionID: cursorFixtureSession,
			Primary:         true,
			ProjectKey:      "synthetic-cursor-hash",
		},
		bytes.NewReader(body),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.EndOffset != int64(len(body)) {
		t.Fatalf("parse result = %+v", result)
	}
	if result.Issues != 1 {
		t.Fatalf("issues = %d, want 1 (the malformed line only)", result.Issues)
	}
	type want struct {
		role     belaytranscript.Role
		tool     string
		text     string
		callID   string
		occurred string
	}
	wants := []want{
		{belaytranscript.RoleUser, "", "inspect [redacted]", "", "2026-08-01T10:00:00Z"},
		{belaytranscript.RoleAssistant, "", "I will run the check.", "", "2026-08-01T10:00:01Z"},
		{belaytranscript.RoleToolCall, "Shell", "", "cursor-call-1", "2026-08-01T10:00:01Z"},
		{belaytranscript.RoleToolResult, "Shell", "", "cursor-call-1", "2026-08-01T10:00:01Z"},
		{belaytranscript.RoleAssistant, "", "Envelope prose.", "", "2026-08-01T10:00:02Z"},
		{belaytranscript.RoleToolCall, "Read", "", "cursor-call-2", "2026-08-01T10:00:02Z"},
		{belaytranscript.RoleToolResult, "Read", "", "cursor-call-2", "2026-08-01T10:00:03Z"},
		{belaytranscript.RoleAssistant, "", "Flat text body.", "", "2026-08-01T10:00:04Z"},
		{belaytranscript.RoleToolCall, "web_fetch", "", "cursor-call-3", "2026-08-01T10:00:05Z"},
		{belaytranscript.RoleToolResult, "", "", "", "2026-08-01T10:00:06Z"},
	}
	if len(result.Turns) != len(wants) {
		for index, turn := range result.Turns {
			t.Logf("turn %d = %s %q %q", index, turn.Role, turn.ToolName, turn.Payload.Text)
		}
		t.Fatalf("turn count = %d, want %d", len(result.Turns), len(wants))
	}
	for index, expected := range wants {
		turn := result.Turns[index]
		if turn.Role != expected.role ||
			turn.ToolName != expected.tool ||
			turn.Payload.Text != expected.text ||
			turn.Payload.ToolCallID != expected.callID {
			t.Fatalf(
				"turn %d = {%s %q text=%q call=%q}, want {%s %q text=%q call=%q}",
				index,
				turn.Role,
				turn.ToolName,
				turn.Payload.Text,
				turn.Payload.ToolCallID,
				expected.role,
				expected.tool,
				expected.text,
				expected.callID,
			)
		}
		if got := turn.OccurredAt.Format(time.RFC3339); got != expected.occurred {
			t.Fatalf("turn %d occurred at %s, want %s", index, got, expected.occurred)
		}
		if turn.Payload.ParserVersion != ParserVersion ||
			turn.Payload.PriceTableVersion != PriceTableVersion ||
			turn.Payload.SourceFileID == "" {
			t.Fatalf("turn %d provenance = %+v", index, turn.Payload)
		}
		if turn.SessionKey != testSessionKey(AgentCursor, cursorFixtureSession) {
			t.Fatalf("turn %d session key = %q", index, turn.SessionKey)
		}
		if turn.Payload.ParentToolUseID != "" {
			t.Fatalf("top-level thread turn %d has a parent: %q", index, turn.Payload.ParentToolUseID)
		}
	}
	// The evidence locator Belay records is the record's byte offset; every turn
	// derived from one record shares it, and offsets advance with the file.
	lineStarts := cursorLineStarts(body)
	wantOffsets := []int64{
		lineStarts[0], lineStarts[1], lineStarts[1], lineStarts[1],
		lineStarts[2], lineStarts[2], lineStarts[3], lineStarts[4],
		lineStarts[5], lineStarts[6],
	}
	for index, offset := range wantOffsets {
		if result.Turns[index].Payload.JSONLByteOffset != offset {
			t.Fatalf(
				"turn %d byte offset = %d, want %d",
				index,
				result.Turns[index].Payload.JSONLByteOffset,
				offset,
			)
		}
	}
	shellResult := result.Turns[3]
	if shellResult.Payload.ExitCode == nil || *shellResult.Payload.ExitCode != 0 {
		t.Fatalf("shell result exit code = %v, want 0", shellResult.Payload.ExitCode)
	}
	// The record states an exit code but no error flag, and an absent value is
	// never invented: a clean exit 0 does not become an explicit "not an error".
	if shellResult.Payload.ToolIsError != nil {
		t.Fatalf("shell result error flag = %v, want nil", shellResult.Payload.ToolIsError)
	}
	readResult := result.Turns[6]
	if readResult.Payload.ToolIsError == nil || !*readResult.Payload.ToolIsError {
		t.Fatalf("read result error flag = %v, want explicit true", readResult.Payload.ToolIsError)
	}
	if readResult.Payload.ExitCode != nil {
		t.Fatalf("read result exit code = %v, want nil (absent stays empty)", readResult.Payload.ExitCode)
	}
	if got := result.Turns[9].Payload.ToolResult; got != "fetched 2 bytes" {
		t.Fatalf("bare-string result body = %q", got)
	}
	if got := result.Turns[2].Payload.RawCommand; got != "printf [redacted]" {
		t.Fatalf("shell raw command = %q", got)
	}
	if got := result.Turns[1].Model; got != "synthetic-cursor-model" {
		t.Fatalf("assistant model = %q", got)
	}
	// The flat-text record names no model, and the model seen earlier in the
	// file is not evidence about this turn, so the field stays empty.
	if got := result.Turns[7].Model; got != "" {
		t.Fatalf("flat-text turn model = %q, want empty", got)
	}
	if result.State.ProjectPath != "/synthetic/cursor-project" {
		t.Fatalf("project path = %q", result.State.ProjectPath)
	}
	// Cursor carries no usage in any attested shape, so nothing is charged.
	for index, turn := range result.Turns {
		if turn.InputTokens != nil || turn.OutputTokens != nil || turn.CostUSD != nil {
			t.Fatalf("turn %d invented usage: %+v", index, turn)
		}
	}
	encoded, err := json.Marshal(result.Turns)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"cursorsupersecret",
		"cursoranothersecret",
		"cursor-private-result",
		"cursor-private-read",
		"PRIVATE_CURSOR_REASONING_CANARY",
	} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("stored Cursor turns contain %q", forbidden)
		}
	}
}

// A nested subagent thread is the same format, and its turns carry an explicit
// parent linkage so they are never read as top-level user feedback.
func TestParseCursorSubagentThreadCarriesParentLinkage(t *testing.T) {
	path := transcriptFixture(t, "cursor", "subagent.jsonl")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Parse(
		context.Background(),
		Source{
			Agent:           AgentCursor,
			Path:            path,
			NativeSessionID: cursorFixtureSession,
			Primary:         false,
			ProjectKey:      "synthetic-cursor-hash",
		},
		bytes.NewReader(body),
		0,
		testParseOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 1 || result.Issues != 0 {
		t.Fatalf("subagent parse = %+v", result)
	}
	turn := result.Turns[0]
	if turn.Role != belaytranscript.RoleAssistant ||
		turn.Payload.Text != "Cursor subagent evidence" {
		t.Fatalf("subagent turn = %+v", turn)
	}
	if turn.Payload.ParentToolUseID != "cursor-subagent-thread:subagent" {
		t.Fatalf("subagent parent linkage = %q", turn.Payload.ParentToolUseID)
	}
	// workspacePath stands in for cwd when the build spells it that way.
	if turn.Payload.CWD != "/synthetic/cursor-project" {
		t.Fatalf("subagent cwd = %q", turn.Payload.CWD)
	}
}

// Field-variant coverage: each shape below is one of the spellings Cursor has
// used, and each must land on the same turn model.
func TestParseCursorFieldVariants(t *testing.T) {
	for _, test := range []struct {
		name  string
		body  string
		check func(*testing.T, Result)
	}{
		{
			name: "role_and_session_id_spellings",
			body: `{"role":"user","sessionId":"sess-1","timestamp":"2026-08-01T10:00:00Z","projectPath":"/synthetic/p","content":"hi"}`,
			check: func(t *testing.T, result Result) {
				if result.State.NativeSessionID != "sess-1" ||
					result.State.ProjectPath != "/synthetic/p" {
					t.Fatalf("state = %+v", result.State)
				}
				if len(result.Turns) != 1 ||
					result.Turns[0].Role != belaytranscript.RoleUser {
					t.Fatalf("turns = %+v", result.Turns)
				}
			},
		},
		{
			name: "numeric_second_epoch",
			body: `{"type":"user","sessionId":"s","timestamp":1785578400,"content":"hi"}`,
			check: func(t *testing.T, result Result) {
				if len(result.Turns) != 1 {
					t.Fatalf("turns = %+v", result.Turns)
				}
				if got := result.Turns[0].OccurredAt.Format(time.RFC3339); got != "2026-08-01T10:00:00Z" {
					t.Fatalf("second epoch = %s", got)
				}
			},
		},
		{
			name: "numeric_microsecond_epoch",
			body: `{"type":"user","sessionId":"s","createdAt":1785578400000000,"content":"hi"}`,
			check: func(t *testing.T, result Result) {
				if got := result.Turns[0].OccurredAt.Format(time.RFC3339); got != "2026-08-01T10:00:00Z" {
					t.Fatalf("microsecond epoch = %s", got)
				}
			},
		},
		{
			name: "missing_timestamp_is_a_diagnostic_not_a_guess",
			body: `{"type":"user","sessionId":"s","content":"hi"}`,
			check: func(t *testing.T, result Result) {
				if len(result.Turns) != 0 || result.Issues != 1 {
					t.Fatalf("timeless record = %+v", result)
				}
			},
		},
		{
			name: "top_level_tool_calls_list",
			body: `{"type":"assistant","sessionId":"s","timestamp":"2026-08-01T10:00:00Z","toolCalls":[{"tool":"Read","id":"c1","parameters":{"path":"/synthetic/a"}}]}`,
			check: func(t *testing.T, result Result) {
				if len(result.Turns) != 1 ||
					result.Turns[0].Role != belaytranscript.RoleToolCall ||
					result.Turns[0].ToolName != "Read" ||
					result.Turns[0].Payload.ToolCallID != "c1" {
					t.Fatalf("turns = %+v", result.Turns)
				}
				if !json.Valid(result.Turns[0].Payload.ToolInput) {
					t.Fatalf("tool input = %s", result.Turns[0].Payload.ToolInput)
				}
			},
		},
		{
			name: "top_level_result_precedes_same_record_content",
			body: `{"type":"assistant","sessionId":"s","timestamp":"2026-08-01T10:00:00Z","toolResult":{"callId":"earlier","content":"closed"},"content":[{"type":"text","text":"next"}]}`,
			check: func(t *testing.T, result Result) {
				if len(result.Turns) != 2 ||
					result.Turns[0].Role != belaytranscript.RoleToolResult ||
					result.Turns[1].Role != belaytranscript.RoleAssistant {
					t.Fatalf("emit order = %+v", result.Turns)
				}
			},
		},
		{
			name: "redacted_value_is_carried_verbatim",
			body: `{"type":"assistant","sessionId":"s","timestamp":"2026-08-01T10:00:00Z","content":[{"type":"text","text":"[redacted]"}]}`,
			check: func(t *testing.T, result Result) {
				if len(result.Turns) != 1 || result.Turns[0].Payload.Text != "[redacted]" {
					t.Fatalf("redacted turn = %+v", result.Turns)
				}
			},
		},
		{
			name: "bookkeeping_records_emit_nothing",
			body: `{"type":"turn_ended","sessionId":"s","timestamp":"2026-08-01T10:00:00Z","status":"success"}
{"type":"system","sessionId":"s","timestamp":"2026-08-01T10:00:01Z","message":"scalar"}`,
			check: func(t *testing.T, result Result) {
				if len(result.Turns) != 0 || result.Issues != 0 {
					t.Fatalf("bookkeeping = %+v", result)
				}
			},
		},
		{
			name: "two_content_bodies_are_counted_and_top_level_wins",
			body: `{"role":"assistant","sessionId":"s","timestamp":"2026-08-01T10:00:00Z","content":"top level","message":{"content":[{"type":"text","text":"enveloped"}]}}`,
			check: func(t *testing.T, result Result) {
				if len(result.Turns) != 1 ||
					result.Turns[0].Payload.Text != "top level" ||
					result.Issues != 1 {
					t.Fatalf("ambiguous body = %+v", result)
				}
			},
		},
		{
			name: "unknown_kind_with_tool_call_still_maps_the_call",
			body: `{"type":"agent_step","sessionId":"s","timestamp":"2026-08-01T10:00:00Z","toolCall":{"name":"Shell","callId":"c","input":{"commandLine":"ls -la"}}}`,
			check: func(t *testing.T, result Result) {
				if len(result.Turns) != 1 ||
					result.Turns[0].Role != belaytranscript.RoleToolCall ||
					result.Turns[0].Payload.RawCommand != "ls -la" {
					t.Fatalf("turns = %+v", result.Turns)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.check(t, parseCursorBody(t, Source{Primary: true}, test.body))
		})
	}
}

// An over-long record is skipped with a diagnostic, and the records around it
// still parse — the tolerant-parse contract the other agents follow.
func TestParseCursorSkipsOversizedRecord(t *testing.T) {
	oversized := `{"type":"user","sessionId":"s","timestamp":"2026-08-01T10:00:00Z","content":"` +
		strings.Repeat("x", maxCursorRecordBytes) + `"}`
	body := `{"type":"user","sessionId":"s","timestamp":"2026-08-01T10:00:00Z","content":"before"}` +
		"\n" + oversized + "\n" +
		`{"type":"user","sessionId":"s","timestamp":"2026-08-01T10:00:02Z","content":"after"}` + "\n"
	result := parseCursorBody(t, Source{Primary: true}, body)
	if len(result.Turns) != 2 || result.Issues != 1 {
		t.Fatalf("oversized record handling = turns %d issues %d", len(result.Turns), result.Issues)
	}
	if result.Turns[0].Payload.Text != "before" || result.Turns[1].Payload.Text != "after" {
		t.Fatalf("surrounding turns = %+v", result.Turns)
	}
}

// The shared payload caps apply to Cursor exactly as to the other agents.
func TestParseCursorAppliesSharedPayloadCaps(t *testing.T) {
	big := strings.Repeat("A", maxToolResultBytes*2)
	body := `{"type":"assistant","sessionId":"s","timestamp":"2026-08-01T10:00:00Z","content":[` +
		`{"type":"tool_use","name":"Shell","callId":"c","input":{"command":"` + big + `"}},` +
		`{"type":"tool_result","callId":"c","content":"` + big + `"}]}` + "\n"
	result := parseCursorBody(t, Source{Primary: true}, body)
	if len(result.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(result.Turns))
	}
	input := result.Turns[0].Payload.ToolInput
	if len(input) > maxToolInputBytes || !json.Valid(input) {
		t.Fatalf("tool input = %d bytes, valid=%v", len(input), json.Valid(input))
	}
	if got := len(result.Turns[1].Payload.ToolResult); got > maxToolResultBytes {
		t.Fatalf("tool result = %d bytes, want <= %d", got, maxToolResultBytes)
	}
}

func cursorLineStarts(body []byte) []int64 {
	starts := []int64{0}
	for index, value := range body {
		if value == '\n' {
			starts = append(starts, int64(index+1))
		}
	}
	return starts
}
