package localapp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliHarnessHome creates a private HOME for a test and returns it. The Cursor
// and Antigravity CLI detectors read marker directories under HOME, so every
// test starts from a home with none of them.
func cliHarnessHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	return home
}

func prependSemanticPath(t *testing.T, directory string) {
	t.Helper()
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCursorCLIDetectionRequiresDistinctNameOrCursorHome(t *testing.T) {
	home := cliHarnessHome(t)
	harnessDir := t.TempDir()
	prependSemanticPath(t, harnessDir)

	if harness, ok := InstalledSemanticHarness("cursor"); ok || harness != SemanticHarnessCursor {
		t.Fatalf("no CLI: InstalledSemanticHarness(cursor) = %q, %v", harness, ok)
	}

	writeSemanticHarness(t, harnessDir, "agent", "exit 0\n")
	if _, ok := InstalledSemanticHarness("cursor"); ok {
		t.Fatal("generic `agent` accepted without a real ~/.cursor directory")
	}
	if _, ok := InstalledSemanticHarness("auto"); ok {
		t.Fatal("auto selected the generic `agent` without ~/.cursor")
	}

	realCursor := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Mkdir(realCursor, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realCursor, filepath.Join(home, ".cursor")); err != nil {
		t.Fatal(err)
	}
	if _, ok := InstalledSemanticHarness("cursor"); ok {
		t.Fatal("symlinked ~/.cursor counted as a Cursor install")
	}
	if err := os.Remove(filepath.Join(home, ".cursor")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	harness, ok := InstalledSemanticHarness("cursor-agent")
	if !ok || harness != SemanticHarnessCursor {
		t.Fatalf("real ~/.cursor: InstalledSemanticHarness = %q, %v", harness, ok)
	}
	if harness, ok := InstalledSemanticHarness("auto"); !ok || harness != SemanticHarnessCursor {
		t.Fatalf("auto = %q, %v, want cursor", harness, ok)
	}

	// The distinctive legacy name needs no marker directory.
	legacyDir := t.TempDir()
	writeSemanticHarness(t, legacyDir, "cursor-agent", "exit 0\n")
	if err := os.RemoveAll(filepath.Join(home, ".cursor")); err != nil {
		t.Fatal(err)
	}
	if _, ok := InstalledSemanticHarness("cursor"); ok {
		t.Fatal("removed ~/.cursor still detected")
	}
	prependSemanticPath(t, legacyDir)
	if path, ok := cursorAgentPath(); !ok || filepath.Base(path) != "cursor-agent" {
		t.Fatalf("cursorAgentPath() = %q, %v, want cursor-agent", path, ok)
	}
}

func TestAntigravityCLIDetectionRejectsIDELauncherAndRequiresDataDir(t *testing.T) {
	home := cliHarnessHome(t)
	harnessDir := t.TempDir()
	prependSemanticPath(t, harnessDir)
	writeSemanticHarness(t, harnessDir, "agy", "exit 0\n")

	if harness, ok := InstalledSemanticHarness("antigravity"); ok ||
		harness != SemanticHarnessAntigravity {
		t.Fatalf("no data dir: InstalledSemanticHarness = %q, %v", harness, ok)
	}
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, preferred := range []string{"antigravity", "agy", "auto"} {
		harness, ok := InstalledSemanticHarness(preferred)
		if !ok || harness != SemanticHarnessAntigravity {
			t.Fatalf("InstalledSemanticHarness(%q) = %q, %v", preferred, harness, ok)
		}
	}

	// The IDE ships an `agy` launcher that resolves into its bundle or its
	// home install root; neither is the CLI.
	for _, launcherDir := range []string{
		filepath.Join(t.TempDir(), "Antigravity.app", "Contents", "Resources", "app", "bin"),
		filepath.Join(t.TempDir(), ".antigravity", "antigravity", "bin"),
	} {
		if err := os.MkdirAll(launcherDir, 0o700); err != nil {
			t.Fatal(err)
		}
		writeSemanticHarness(t, launcherDir, "antigravity", "exit 0\n")
		linkDir := t.TempDir()
		if err := os.Symlink(
			filepath.Join(launcherDir, "antigravity"),
			filepath.Join(linkDir, "agy"),
		); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", linkDir+string(os.PathListSeparator)+harnessDir)
		if _, ok := InstalledSemanticHarness("antigravity"); ok {
			t.Fatalf("IDE launcher under %s accepted as the Antigravity CLI", launcherDir)
		}
	}
}

func TestRunInstalledSemanticHarnessDrivesCursorCLIReadOnly(t *testing.T) {
	home := cliHarnessHome(t)
	if err := os.Mkdir(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	harnessDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args")
	stdinPath := filepath.Join(t.TempDir(), "stdin")
	replyPath := filepath.Join(t.TempDir(), "reply")
	fence := "```"
	reply := `{"type":"result","subtype":"success","is_error":false,"duration_ms":5,` +
		`"result":"Here you go:\n` + fence + `json\n{\"clusters\":[],\"fixes\":[{\"issue_id\":\"csi_test\",` +
		`\"rule_text\":\"Run tests before claiming completion.\",\"target_file\":\"AGENTS.md\",` +
		`\"confidence\":0.8}]}\n` + fence + `","session_id":"s"}` + "\n"
	if err := os.WriteFile(replyPath, []byte(reply), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BELAY_TEST_REPLY_PATH", replyPath)
	writeSemanticHarness(t, harnessDir, "agent", `
for arg in "$@"; do
	printf '<%s>\n' "$arg" >> "$BELAY_TEST_ARGS_PATH"
done
cat > "$BELAY_TEST_STDIN_PATH"
cat "$BELAY_TEST_REPLY_PATH"
`)
	prependSemanticPath(t, harnessDir)
	t.Setenv("BELAY_TEST_ARGS_PATH", argsPath)
	t.Setenv("BELAY_TEST_STDIN_PATH", stdinPath)
	prompt := []byte("CURSOR_LARGE_PROMPT_MARKER_" + strings.Repeat("z", 512<<10))
	_, schema, _, err := semanticPrompt(SemanticHarnessCursor, semanticTestInput())
	if err != nil {
		t.Fatal(err)
	}

	result, err := RunInstalledSemanticHarness(
		context.Background(),
		SemanticHarnessCursor,
		prompt,
		schema,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "" || len(result.Result.Fixes) != 1 ||
		result.Result.Fixes[0].TargetFile != "AGENTS.md" {
		t.Fatalf("cursor result = %+v", result)
	}
	assertSemanticPromptTransport(t, argsPath, stdinPath, prompt)
	args := readSemanticTestFile(t, argsPath)
	for _, required := range []string{
		"<-p>",
		"<" + cursorSemanticInstruction + ">",
		"<--output-format>",
		"<json>",
		"<--mode>",
		"<ask>",
		"<--trust>",
		"<--workspace>",
	} {
		if !bytes.Contains(args, []byte(required)) {
			t.Fatalf("Cursor args missing %q:\n%s", required, args)
		}
	}
	for _, forbidden := range []string{"<--force>", "<--yolo>", "<-f>"} {
		if bytes.Contains(args, []byte(forbidden)) {
			t.Fatalf("Cursor args carry write permission %q:\n%s", forbidden, args)
		}
	}
}

func TestRunInstalledSemanticHarnessRejectsBadCursorReplies(t *testing.T) {
	home := cliHarnessHome(t)
	if err := os.Mkdir(filepath.Join(home, ".cursor"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, schema, _, err := semanticPrompt(SemanticHarnessCursor, semanticTestInput())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "flagged error",
			reply: `{"type":"result","subtype":"error","is_error":true,"result":"boom"}`,
			want:  "failed semantic run",
		},
		{
			name:  "schema violation",
			reply: `{"type":"result","subtype":"success","is_error":false,"result":"{\"clusters\":[],\"fixes\":[{\"issue_id\":\"csi_test\",\"rule_text\":\"x\",\"target_file\":\"README.md\",\"confidence\":0.5}]}"}`,
			want:  "does not match the output schema",
		},
		{
			name:  "no document",
			reply: `{"type":"result","subtype":"success","is_error":false,"result":"I could not do that."}`,
			want:  "contains no JSON object",
		},
		{
			name:  "trailing document",
			reply: `{"type":"result","subtype":"success","is_error":false,"result":"{\"clusters\":[],\"fixes\":[]} {\"clusters\":[],\"fixes\":[]}"}`,
			want:  "trailing JSON",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harnessDir := t.TempDir()
			writeSemanticHarness(t, harnessDir, "agent", "cat > /dev/null\nprintf '%s\\n' '"+test.reply+"'\n")
			prependSemanticPath(t, harnessDir)
			_, err := RunInstalledSemanticHarness(
				context.Background(),
				SemanticHarnessCursor,
				[]byte("prompt"),
				schema,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunInstalledSemanticHarnessDrivesAntigravityCLIWithSchemaFile(t *testing.T) {
	home := cliHarnessHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	harnessDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "args")
	stdinPath := filepath.Join(t.TempDir(), "stdin")
	schemaCopyPath := filepath.Join(t.TempDir(), "schema")
	writeSemanticHarness(t, harnessDir, "agy", `
expect=""
schema_path=""
for arg in "$@"; do
	printf '<%s>\n' "$arg" >> "$BELAY_TEST_ARGS_PATH"
	if [ "$expect" = "schema" ]; then
		schema_path="$arg"
		expect=""
	elif [ "$arg" = "--json-schema" ]; then
		expect="schema"
	fi
done
cat > "$BELAY_TEST_STDIN_PATH"
cp "$schema_path" "$BELAY_TEST_SCHEMA_PATH"
printf '%s\n' '{"type":"init","conversation_id":"c1"}'
printf '%s\n' '{"type":"step_update","step":1}'
printf '%s\n' '{"type":"result","status":"SUCCESS","conversation_id":"c1","response":"done","structured_output":{"clusters":[],"fixes":[{"issue_id":"csi_test","rule_text":"Run tests before claiming completion.","target_file":".agents/rules/belay.md","confidence":0.9}]},"usage":{"input_tokens":1}}'
`)
	prependSemanticPath(t, harnessDir)
	t.Setenv("BELAY_TEST_ARGS_PATH", argsPath)
	t.Setenv("BELAY_TEST_STDIN_PATH", stdinPath)
	t.Setenv("BELAY_TEST_SCHEMA_PATH", schemaCopyPath)
	prompt := []byte("AGY_LARGE_PROMPT_MARKER_" + strings.Repeat("w", 512<<10))
	_, schema, _, err := semanticPrompt(SemanticHarnessAntigravity, semanticTestInput())
	if err != nil {
		t.Fatal(err)
	}

	result, err := RunInstalledSemanticHarness(
		context.Background(),
		SemanticHarnessAntigravity,
		prompt,
		schema,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "" || len(result.Result.Fixes) != 1 ||
		result.Result.Fixes[0].TargetFile != antigravityProjectRuleFile {
		t.Fatalf("antigravity result = %+v", result)
	}
	if got := readSemanticTestFile(t, schemaCopyPath); !bytes.Equal(got, schema) {
		t.Fatalf("schema file = %q, want %q", got, schema)
	}
	// The prompt travels as exactly one stream-json user event on stdin.
	stdin := readSemanticTestFile(t, stdinPath)
	var event struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdin))
	if err := decoder.Decode(&event); err != nil {
		t.Fatalf("stdin is not a stream-json event: %v", err)
	}
	if decoder.More() {
		t.Fatal("stdin carried more than one event")
	}
	if event.Type != "user" || event.Message.Role != "user" ||
		event.Message.Content != string(prompt) {
		t.Fatalf("stdin event = %q/%q, content length %d", event.Type, event.Message.Role, len(event.Message.Content))
	}
	args := readSemanticTestFile(t, argsPath)
	if bytes.Contains(args, []byte("AGY_LARGE_PROMPT_MARKER_")) {
		t.Fatalf("prompt appeared in argv: %s", args)
	}
	for _, required := range []string{
		"<--input-format>",
		"<stream-json>",
		"<--output-format>",
		"<--json-schema>",
		"<--sandbox>",
	} {
		if !bytes.Contains(args, []byte(required)) {
			t.Fatalf("Antigravity args missing %q:\n%s", required, args)
		}
	}
	for _, forbidden := range []string{"<--dangerously-skip-permissions>", "<-p>"} {
		if bytes.Contains(args, []byte(forbidden)) {
			t.Fatalf("Antigravity args carry %q:\n%s", forbidden, args)
		}
	}
}

func TestDecodeAntigravitySemanticPayloadShapes(t *testing.T) {
	_, schema, _, err := semanticPrompt(SemanticHarnessAntigravity, semanticTestInput())
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"clusters":[],"fixes":[]}`
	fenced, err := json.Marshal("Sure:\n```json\n" + valid + "\n```")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "single indented json envelope",
			body: "{\n  \"status\": \"SUCCESS\",\n  \"response\": \"ok\",\n  \"structured_output\": " + valid + "\n}\n",
		},
		{
			name: "response text fallback",
			body: `{"type":"result","status":"SUCCESS","response":` + string(fenced) + `}` + "\n",
		},
		{
			name: "error status",
			body: `{"type":"result","status":"ERROR","error":"quota","response":""}` + "\n",
			want: "status ERROR",
		},
		{
			name: "error text without status",
			body: `{"type":"result","error":"quota"}` + "\n",
			want: "status ERROR",
		},
		{
			name: "schema violation",
			body: `{"type":"result","status":"SUCCESS","structured_output":{"clusters":[],"fixes":[{"issue_id":"csi_nope","rule_text":"x","target_file":"CLAUDE.md","confidence":0.5}]}}` + "\n",
			want: "does not match the output schema",
		},
		{
			name: "no result event",
			body: `{"type":"init"}` + "\n" + `{"type":"step_update"}` + "\n",
			want: "decode Antigravity CLI semantic response",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := decodeAntigravitySemanticPayload([]byte(test.body), schema)
			if test.want == "" {
				if err != nil || string(result.body) != valid {
					t.Fatalf("result = %q, err = %v", result.body, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSemanticHarnessDescribesAntigravityAndPrefersItsRuleFile(t *testing.T) {
	if !SemanticHarnessAntigravity.Valid() {
		t.Fatal("antigravity is not a valid semantic harness")
	}
	if got := semanticHarnessLabel(SemanticHarnessAntigravity); got != "Antigravity" {
		t.Fatalf("semanticHarnessLabel(antigravity) = %q", got)
	}
	for harness, want := range map[SemanticHarness]string{
		SemanticHarnessClaude:      "CLAUDE.md",
		SemanticHarnessCodex:       "AGENTS.md",
		SemanticHarnessCursor:      "AGENTS.md",
		SemanticHarnessAntigravity: antigravityProjectRuleFile,
	} {
		if got := semanticPreferredTarget(harness); got != want {
			t.Fatalf("semanticPreferredTarget(%q) = %q, want %q", harness, got, want)
		}
	}
	prompt, schema, hash, err := semanticPrompt(
		SemanticHarnessAntigravity,
		semanticTestInput(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(prompt, []byte("in this Antigravity analysis, prefer .agents/rules/belay.md")) ||
		!bytes.Contains(prompt, []byte(".codex/rules/default.rules, and .agents/rules/belay.md")) {
		t.Fatalf("antigravity prompt = %s", prompt)
	}
	if !bytes.Contains(schema, []byte(`".agents/rules/belay.md"`)) {
		t.Fatalf("schema target enum lacks the Antigravity rule file: %s", schema)
	}
	_, _, claudeHash, err := semanticPrompt(SemanticHarnessClaude, semanticTestInput())
	if err != nil {
		t.Fatal(err)
	}
	if hash == claudeHash {
		t.Fatal("antigravity and claude prompts shared a provenance hash")
	}
	if !validInsightTarget(antigravityProjectRuleFile) ||
		validInsightTarget(".agent/rules/belay.md") {
		t.Fatal("validInsightTarget does not accept exactly Belay's Antigravity rule file")
	}
}
