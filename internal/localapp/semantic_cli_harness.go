package localapp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// This file drives the two IDE-adjacent CLIs that can run Belay's semantic
// pass: the Cursor CLI (`agent`, formerly `cursor-agent`) and the Antigravity
// CLI (`agy`). Neither is the harness that recorded the sessions being
// analyzed; either is simply an installed, signed-in model runner, exactly
// like Claude Code and Codex are in this role. Both paths were built against
// the vendors' headless-mode documentation and fake-CLI tests; see the docs
// for the validation status.

const (
	// cursorAgentExecutable is the Cursor CLI's current binary name. It is a
	// generic word, so it is accepted only next to a real ~/.cursor directory.
	cursorAgentExecutable = "agent"
	// cursorAgentLegacyExecutable is the name older Cursor CLI installs
	// provide; it is distinctive enough to accept on its own.
	cursorAgentLegacyExecutable = "cursor-agent"
	// antigravityCLIExecutable is the Antigravity CLI binary. The Antigravity
	// IDE ships a launcher of the same name that only opens the IDE, so the
	// resolved executable is checked against the IDE's install locations.
	antigravityCLIExecutable = "agy"
	// antigravityCLIDataDir is the Antigravity CLI's own application data
	// directory under the home directory; the IDE uses ~/.gemini/antigravity.
	antigravityCLIDataDir = "antigravity-cli"
	// antigravityIDEBundleName is the macOS application bundle the IDE
	// launcher resolves into.
	antigravityIDEBundleName = "Antigravity.app"
	// antigravityIDEHomeDir is the IDE's home-directory install root, where
	// its launcher symlinks live.
	antigravityIDEHomeDir = ".antigravity"
)

// cursorSemanticInstruction is the only prompt text the Cursor CLI receives
// on its command line. The analysis prompt itself travels on stdin, which the
// Cursor CLI attaches to the request, so the prompt never appears in argv.
const cursorSemanticInstruction = "Belay Local analysis. The complete task is " +
	"provided on standard input. Follow it exactly, do not use tools or " +
	"inspect files, and reply with only the JSON document it specifies: no " +
	"prose and no code fences."

// cursorAgentPath reports the Cursor CLI executable when one is installed.
// `cursor-agent` is accepted on its own. `agent` is a generic name, so it is
// accepted only when ~/.cursor exists as a real directory.
func cursorAgentPath() (string, bool) {
	if path, err := exec.LookPath(cursorAgentLegacyExecutable); err == nil &&
		usableExecutable(path) {
		return path, true
	}
	path, err := exec.LookPath(cursorAgentExecutable)
	if err != nil || !usableExecutable(path) {
		return "", false
	}
	if !homeSubdirectoryPresent(".cursor") {
		return "", false
	}
	return path, true
}

// antigravityCLIPath reports the Antigravity CLI executable when one is
// installed. The IDE's `agy` launcher is rejected by where it resolves to,
// and the CLI's own data directory must exist so an unrelated `agy` on PATH
// is never driven.
func antigravityCLIPath() (string, bool) {
	path, err := exec.LookPath(antigravityCLIExecutable)
	if err != nil || !usableExecutable(path) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || antigravityIDELauncher(resolved) {
		return "", false
	}
	if !homeSubdirectoryPresent(".gemini", antigravityCLIDataDir) {
		return "", false
	}
	return path, true
}

// antigravityIDELauncher reports whether a resolved executable path lives in
// the Antigravity IDE's bundle or home install root.
func antigravityIDELauncher(resolved string) bool {
	for _, element := range strings.Split(filepath.ToSlash(resolved), "/") {
		if strings.EqualFold(element, antigravityIDEBundleName) ||
			element == antigravityIDEHomeDir {
			return true
		}
	}
	return false
}

// homeSubdirectoryPresent reports whether the named path under the home
// directory exists as a real directory; a symlink does not count.
func homeSubdirectoryPresent(elements ...string) bool {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return false
	}
	return realDirectory(filepath.Join(append([]string{home}, elements...)...))
}

// cursorSemanticArgs is the Cursor CLI command line. `--mode ask` is Cursor's
// read-only mode, `--trust` skips the workspace-trust prompt for the private
// empty workspace Belay creates, and the JSON envelope carries the reply.
func cursorSemanticArgs(workspace string) []string {
	return []string{
		"-p", cursorSemanticInstruction,
		"--output-format", "json",
		"--mode", "ask",
		"--trust",
		"--workspace", workspace,
	}
}

// antigravitySemanticArgs is the Antigravity CLI command line. The prompt is
// delivered as one stream-json user message on stdin because `-p` reads only
// its argument, the schema is passed as a file so no argument-size limit
// applies, and `--sandbox` keeps the terminal sandbox on. Tools that would
// need approval are soft-denied in headless mode; Belay never grants them.
func antigravitySemanticArgs(schemaPath string) []string {
	return []string{
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--json-schema", schemaPath,
		"--sandbox",
	}
}

// antigravityStreamJSONPrompt frames the prompt as the single stream-json
// user event the Antigravity CLI reads from stdin.
func antigravityStreamJSONPrompt(prompt []byte) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": string(prompt),
		},
	})
	if err != nil {
		return nil, errors.New("encode Antigravity semantic prompt")
	}
	return buffer.Bytes(), nil
}

// decodeCursorSemanticPayload reads the Cursor CLI JSON envelope. Cursor has
// no schema-bound output, so the JSON document is lifted out of the reply
// text and validated against the output schema before it is trusted. The
// envelope names no model, so none is recorded.
func decodeCursorSemanticPayload(
	body []byte,
	schema []byte,
) (semanticRawHarnessResult, error) {
	var envelope struct {
		Type    string          `json:"type"`
		Subtype string          `json:"subtype"`
		IsError *bool           `json:"is_error"`
		Result  json.RawMessage `json:"result"`
	}
	if json.Unmarshal(bytes.TrimSpace(body), &envelope) != nil {
		return semanticRawHarnessResult{}, errors.New(
			"decode Cursor CLI semantic response",
		)
	}
	if (envelope.IsError != nil && *envelope.IsError) ||
		(envelope.Subtype != "" && envelope.Subtype != "success") {
		return semanticRawHarnessResult{}, errors.New(
			"Cursor CLI reported a failed semantic run",
		)
	}
	document, err := semanticDocumentFromReply(envelope.Result)
	if err != nil {
		return semanticRawHarnessResult{}, fmt.Errorf(
			"Cursor CLI semantic response: %w",
			err,
		)
	}
	if err := validateSemanticOutput(schema, document); err != nil {
		return semanticRawHarnessResult{}, fmt.Errorf(
			"Cursor CLI semantic response: %w",
			err,
		)
	}
	return semanticRawHarnessResult{body: document}, nil
}

// antigravityResultEvent is the tolerant view of one Antigravity CLI output
// object: the final `result` event of a stream-json run, or the single
// envelope of a json run. Only the fields Belay needs are read.
type antigravityResultEvent struct {
	Type             string          `json:"type"`
	Status           string          `json:"status"`
	Error            string          `json:"error"`
	Response         json.RawMessage `json:"response"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

func (event antigravityResultEvent) isResult() bool {
	return event.Type == "result" ||
		event.Status != "" ||
		len(event.StructuredOutput) > 0
}

// decodeAntigravitySemanticPayload reads the Antigravity CLI output. The
// schema-bound `structured_output` of the last result event is preferred; a
// reply that carries only response text is parsed like a Cursor reply. Either
// way the document is validated against the output schema locally, and a
// non-success status fails the run. No model is named in the envelope.
func decodeAntigravitySemanticPayload(
	body []byte,
	schema []byte,
) (semanticRawHarnessResult, error) {
	event, found := lastAntigravityResult(body)
	if !found {
		return semanticRawHarnessResult{}, errors.New(
			"decode Antigravity CLI semantic response",
		)
	}
	status := strings.ToUpper(strings.TrimSpace(event.Status))
	if (status != "" && status != "SUCCESS") || strings.TrimSpace(event.Error) != "" {
		if status == "" {
			status = "ERROR"
		}
		return semanticRawHarnessResult{}, fmt.Errorf(
			"Antigravity CLI reported semantic run status %s",
			status,
		)
	}
	var document []byte
	if len(bytes.TrimSpace(event.StructuredOutput)) > 0 &&
		string(bytes.TrimSpace(event.StructuredOutput)) != "null" {
		document = append([]byte(nil), event.StructuredOutput...)
	} else {
		var err error
		document, err = semanticDocumentFromReply(event.Response)
		if err != nil {
			return semanticRawHarnessResult{}, fmt.Errorf(
				"Antigravity CLI semantic response: %w",
				err,
			)
		}
	}
	if err := validateSemanticOutput(schema, document); err != nil {
		return semanticRawHarnessResult{}, fmt.Errorf(
			"Antigravity CLI semantic response: %w",
			err,
		)
	}
	return semanticRawHarnessResult{body: document}, nil
}

// lastAntigravityResult finds the result event in Antigravity CLI output,
// which is either one JSON object (json mode, possibly indented) or
// newline-delimited objects (stream-json mode).
func lastAntigravityResult(body []byte) (antigravityResultEvent, bool) {
	var single antigravityResultEvent
	if json.Unmarshal(bytes.TrimSpace(body), &single) == nil && single.isResult() {
		return single, true
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64<<10), maxSemanticCommandOutput+1)
	var last antigravityResultEvent
	found := false
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var event antigravityResultEvent
		if json.Unmarshal(line, &event) != nil || !event.isResult() {
			continue
		}
		last, found = event, true
	}
	return last, found
}

// semanticDocumentFromReply lifts the JSON object out of a reply that may be
// a JSON string of prose-wrapped or fence-wrapped JSON, or already an object.
func semanticDocumentFromReply(reply json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(reply)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, errors.New("reply carries no document")
	}
	if trimmed[0] == '{' {
		return append([]byte(nil), trimmed...), nil
	}
	var text string
	if json.Unmarshal(trimmed, &text) != nil {
		return nil, errors.New("reply is neither an object nor text")
	}
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end < start {
		return nil, errors.New("reply text contains no JSON object")
	}
	return []byte(text[start : end+1]), nil
}

// validateSemanticOutput checks a harness reply against the JSON schema Belay
// asked it to satisfy. Claude Code and Codex enforce the schema themselves;
// the CLI harnesses return text, so this check stands in for that guarantee
// before the downstream decoders see the document.
func validateSemanticOutput(schema []byte, document []byte) error {
	var root jsonschema.Schema
	if err := json.Unmarshal(schema, &root); err != nil {
		return errors.New("semantic output schema is unreadable")
	}
	resolved, err := root.Resolve(nil)
	if err != nil {
		return errors.New("semantic output schema did not resolve")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	var instance any
	if err := decoder.Decode(&instance); err != nil {
		return errors.New("reply is not a JSON document")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return errors.New("reply contains trailing JSON")
	}
	if err := resolved.Validate(instance); err != nil {
		return errors.New("reply does not match the output schema")
	}
	return nil
}
