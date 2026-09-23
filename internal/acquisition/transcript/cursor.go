package transcript

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	belaytranscript "github.com/DoplexLabs/belay-engine/internal/transcript"
)

// maxCursorRecordBytes bounds a single Cursor JSONL record. A longer record is
// skipped with a diagnostic rather than parsed, mirroring the record cap the
// pinned Numbat Cursor extractor applies. Payload-level caps (tool input and
// tool result) are the package-wide ones every agent shares and are applied by
// emit, so a Cursor turn can never store more than maxToolInputBytes /
// maxToolResultBytes either.
const maxCursorRecordBytes = 16 << 20

// cursorRecord is a tolerant view of one record in a Cursor agent transcript.
//
// Cursor's at-rest transcript format is undocumented and has varied between
// builds: the records are written live, have been observed to carry values the
// product already redacted, and spell the same field several ways. So every
// field is resolved across the spellings observed in practice rather than bound
// to one rigid schema, anything unrecognised is ignored so an unrelated schema
// addition never breaks a parse, and nothing is ever synthesized — a value
// absent from the record stays empty.
type cursorRecord struct {
	// Type and Role both discriminate the record across builds (a
	// "type":"user"/"assistant"/"tool" tag and a message-style "role"). Type
	// wins when both are present.
	Type string `json:"type"`
	Role string `json:"role"`

	// Identity and context, each read across the observed spellings.
	ConversationID string          `json:"conversationId"`
	SessionID      string          `json:"sessionId"`
	Timestamp      json.RawMessage `json:"timestamp"`
	CreatedAt      json.RawMessage `json:"createdAt"`
	CWD            string          `json:"cwd"`
	WorkspacePath  string          `json:"workspacePath"`
	ProjectPath    string          `json:"projectPath"`
	Model          string          `json:"model"`

	// Content is the body of a prompt or an assistant message: a plain string,
	// an array of typed blocks, or absent. Current builds wrap the same body in
	// a message envelope instead.
	Content cursorContent `json:"content"`
	Message cursorMessage `json:"message"`

	// Text is the flat body some records carry instead of content.
	Text string `json:"text"`

	// A tool invocation may be framed as a single top-level toolCall, a
	// toolCalls list, or a block inside content; its output as toolResult,
	// output, or a block inside content.
	ToolCall   *cursorToolCall  `json:"toolCall"`
	ToolCalls  []cursorToolCall `json:"toolCalls"`
	ToolResult *cursorToolOut   `json:"toolResult"`
	Output     *cursorToolOut   `json:"output"`
}

// cursorMessage is the message envelope current Cursor builds wrap a body in. A
// scalar message (some bookkeeping records carry one) decodes to an empty
// envelope instead of failing the record.
type cursorMessage struct {
	Model   string        `json:"model"`
	Content cursorContent `json:"content"`
}

func (m *cursorMessage) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	type wire cursorMessage
	return json.Unmarshal(data, (*wire)(m))
}

type cursorBlockKind uint8

const (
	cursorBlockText cursorBlockKind = iota
	cursorBlockCall
	cursorBlockResult
)

// cursorContentItem is one normalized block of a content body, tagged with the
// bucket it came from so the mapper can replay the body in source order (a call
// stays ahead of the result it is closed by, in the same record).
type cursorContentItem struct {
	kind cursorBlockKind
	text string
	call cursorToolCall
	out  cursorToolOut
}

type cursorContent struct {
	items []cursorContentItem
}

func (c *cursorContent) hasData() bool { return len(c.items) > 0 }

// UnmarshalJSON normalizes the content encodings Cursor emits:
//
//	"content": "a plain prompt"              → one text block
//	"content": [{"type":"text"}, …]          → the blocks in source order, with
//	                                           tool_use/tool_call and
//	                                           tool_result blocks lifted out
//
// A missing, null, or object-shaped content yields an empty body rather than an
// error, so a partial record still parses.
func (c *cursorContent) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return err
		}
		if strings.TrimSpace(value) != "" {
			c.items = append(c.items, cursorContentItem{text: value})
		}
		return nil
	case '[':
		var blocks []json.RawMessage
		if err := json.Unmarshal(trimmed, &blocks); err != nil {
			return err
		}
		for _, block := range blocks {
			c.appendBlock(block)
		}
		return nil
	default:
		return nil
	}
}

func (c *cursorContent) appendBlock(block json.RawMessage) {
	var probe struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(block, &probe) != nil {
		return
	}
	switch probe.Type {
	case "tool_call", "tool_use", "toolCall", "function_call":
		var call cursorToolCall
		if json.Unmarshal(block, &call) == nil {
			c.items = append(c.items, cursorContentItem{
				kind: cursorBlockCall,
				call: call,
			})
		}
	case "tool_result", "tool_output", "toolResult", "function_call_output":
		var out cursorToolOut
		if json.Unmarshal(block, &out) == nil {
			c.items = append(c.items, cursorContentItem{
				kind: cursorBlockResult,
				out:  out,
			})
		}
	default:
		// Anything else with prose (a text block, or a block whose type this
		// build spells differently) contributes its text; a block with no text
		// — reasoning, an attachment, a redaction placeholder — contributes
		// nothing rather than a fabricated turn.
		if strings.TrimSpace(probe.Text) != "" {
			c.items = append(c.items, cursorContentItem{text: probe.Text})
		}
	}
}

// cursorToolCall is a tolerant view of a tool invocation.
type cursorToolCall struct {
	Name   string                     `json:"name"`
	Tool   string                     `json:"tool"`
	CallID string                     `json:"callId"`
	ToolID string                     `json:"toolCallId"`
	ID     string                     `json:"id"`
	Input  map[string]json.RawMessage `json:"input"`
	Args   map[string]json.RawMessage `json:"args"`
	Params map[string]json.RawMessage `json:"parameters"`
}

func (c *cursorToolCall) name() string { return firstCursorValue(c.Name, c.Tool) }

func (c *cursorToolCall) callID() string {
	return firstCursorValue(c.CallID, c.ToolID, c.ID)
}

// input returns the argument object across spellings, re-encoded so emit can
// scrub and cap it like any other tool input. Map keys are marshalled sorted,
// so the encoding is deterministic.
func (c *cursorToolCall) input() json.RawMessage {
	for _, fields := range []map[string]json.RawMessage{c.Input, c.Args, c.Params} {
		if len(fields) == 0 {
			continue
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			continue
		}
		return encoded
	}
	return nil
}

// cursorToolOut is a tolerant view of a tool result. The exit code and the
// error flag are read only when the record states them structurally; a prose
// body is never scraped for a failure signal or an exit status.
type cursorToolOut struct {
	CallID   string          `json:"callId"`
	ToolID   string          `json:"toolCallId"`
	ExitCode *int            `json:"exitCode"`
	ExitAlt  *int            `json:"exit_code"`
	IsError  *bool           `json:"isError"`
	IsErrAlt *bool           `json:"is_error"`
	Status   string          `json:"status"`
	Content  json.RawMessage `json:"content"`
	Output   json.RawMessage `json:"output"`
	Result   json.RawMessage `json:"result"`
	Text     string          `json:"text"`
	Stdout   string          `json:"stdout"`

	// plain holds the body of a result framed as a bare string rather than an
	// object ("toolResult":"…"), which some builds emit.
	plain string
}

func (o *cursorToolOut) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return err
		}
		o.plain = value
		return nil
	}
	if trimmed[0] != '{' {
		return nil
	}
	type wire cursorToolOut
	return json.Unmarshal(trimmed, (*wire)(o))
}

func (o *cursorToolOut) callID() string { return firstCursorValue(o.CallID, o.ToolID) }

func (o *cursorToolOut) exitCode() *int {
	if o.ExitCode != nil {
		return o.ExitCode
	}
	return o.ExitAlt
}

// errored reports the structurally-stated failure flag, and whether the record
// stated one at all. A non-zero exit code alone is not a failure — a command may
// exit non-zero by design — so the exit code rides on the turn for a detector to
// judge, matching the explicit-only policy of the Claude and Codex parsers.
func (o *cursorToolOut) errored() (bool, bool) {
	if o.IsError != nil {
		return *o.IsError, true
	}
	if o.IsErrAlt != nil {
		return *o.IsErrAlt, true
	}
	switch strings.ToLower(strings.TrimSpace(o.Status)) {
	case "":
		return false, false
	case "error", "failed", "failure":
		return true, true
	default:
		return false, true
	}
}

func (r *cursorRecord) kind() string {
	if r.Type != "" {
		return strings.ToLower(strings.TrimSpace(r.Type))
	}
	return strings.ToLower(strings.TrimSpace(r.Role))
}

func (r *cursorRecord) sessionID() string {
	return firstCursorValue(r.ConversationID, r.SessionID)
}

func (r *cursorRecord) project() string {
	return firstCursorValue(r.CWD, r.WorkspacePath, r.ProjectPath)
}

func (r *cursorRecord) model() string {
	return firstCursorValue(r.Model, r.Message.Model)
}

// content returns the record's body: older transcripts keep it at the top
// level, current builds under message.
func (r *cursorRecord) content() *cursorContent {
	if r.Content.hasData() {
		return &r.Content
	}
	return &r.Message.Content
}

// topLevelResults returns the results framed at the top level of the record.
// They close a call issued by an earlier record, so the mapper emits them first.
func (r *cursorRecord) topLevelResults() []cursorToolOut {
	var result []cursorToolOut
	if r.ToolResult != nil {
		result = append(result, *r.ToolResult)
	}
	if r.Output != nil {
		result = append(result, *r.Output)
	}
	return result
}

// topLevelCalls returns the calls framed at the top level of the record. They
// are issued by this turn and closed by a later one, so the mapper emits them
// last.
func (r *cursorRecord) topLevelCalls() []cursorToolCall {
	var result []cursorToolCall
	result = append(result, r.ToolCalls...)
	if r.ToolCall != nil {
		result = append(result, *r.ToolCall)
	}
	return result
}

// occurredAt resolves the record timestamp across the spellings and encodings
// Cursor has used.
func (r *cursorRecord) occurredAt() time.Time {
	if value := cursorTimestamp(r.Timestamp); !value.IsZero() {
		return value
	}
	return cursorTimestamp(r.CreatedAt)
}

// cursorTimestamp decodes a timestamp written either as an RFC 3339 string or
// as a numeric epoch. Cursor has used both, and unlike Numbat — whose event
// model carries the timestamp as an RFC 3339 string it refuses to guess — Belay
// stores a time.Time and drops any turn without one, so a numeric epoch is
// decoded rather than discarded. The unit is inferred from magnitude, which is
// unambiguous for every instant this decade.
func cursorTimestamp(raw json.RawMessage) time.Time {
	if len(bytes.TrimSpace(raw)) == 0 {
		return time.Time{}
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return parseTime(strings.TrimSpace(text))
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return time.Time{}
	}
	value, err := number.Int64()
	if err != nil {
		float, floatErr := number.Float64()
		if floatErr != nil {
			return time.Time{}
		}
		value = int64(float)
	}
	switch {
	case value <= 0:
		return time.Time{}
	case value < 1e11: // seconds
		return time.Unix(value, 0).UTC()
	case value < 1e14: // milliseconds
		return time.UnixMilli(value).UTC()
	case value < 1e17: // microseconds
		return time.UnixMicro(value).UTC()
	default: // nanoseconds
		return time.Unix(0, value).UTC()
	}
}

// cursorRole maps a record kind onto Belay's turn roles. A kind that carries no
// standalone prose body ("tool", "turn_ended", an unnamed bookkeeping record)
// reports ok=false; its forensic signal is the tool call or result the mapper
// handles separately.
func cursorRole(kind string) (belaytranscript.Role, bool) {
	switch kind {
	case "user", "human", "prompt":
		return belaytranscript.RoleUser, true
	case "assistant", "ai", "model":
		return belaytranscript.RoleAssistant, true
	case "system":
		return belaytranscript.RoleSystem, true
	default:
		return "", false
	}
}

// parseCursor maps one Cursor agent-transcript record onto Belay turns.
//
// A record can carry a message body, tool calls and tool results at once, so
// the emit order follows the source: top-level results (which close an earlier
// record's call) lead, then the content body in block order, then top-level
// calls (which a later record closes).
func (p *parser) parseCursor(raw jsonObject) {
	if len(p.line) > maxCursorRecordBytes {
		p.result.Issues++
		return
	}
	var record cursorRecord
	if err := unmarshalObject(raw, &record); err != nil {
		p.result.Issues++
		return
	}
	if sessionID := record.sessionID(); sessionID != "" {
		if !p.result.State.NativeIdentityConfirmed {
			p.result.State.NativeSessionID = sessionID
			p.result.State.NativeIdentityConfirmed = true
		} else if p.result.State.NativeSessionID != sessionID {
			p.result.Issues++
		}
	}
	if project := record.project(); project != "" {
		p.result.State.ProjectPath = project
	}
	if model := record.model(); model != "" {
		p.result.State.Model = model
	}
	if record.Content.hasData() && record.Message.Content.hasData() {
		// Two bodies in one record is a build the format notes do not describe.
		// Count it so coverage degrades to partial, and read the top-level body.
		p.result.Issues++
	}
	occurredAt := record.occurredAt()
	role, hasRole := cursorRole(record.kind())

	results := record.topLevelResults()
	for index := range results {
		p.emitCursorToolResult(occurredAt, &results[index])
	}
	content := record.content()
	for index := range content.items {
		item := &content.items[index]
		switch item.kind {
		case cursorBlockText:
			if hasRole {
				p.emitCursorText(occurredAt, role, item.text, record.model())
			}
		case cursorBlockCall:
			p.emitCursorToolCall(occurredAt, record.model(), &item.call)
		case cursorBlockResult:
			p.emitCursorToolResult(occurredAt, &item.out)
		}
	}
	if !content.hasData() && hasRole && strings.TrimSpace(record.Text) != "" {
		p.emitCursorText(occurredAt, role, record.Text, record.model())
	}
	calls := record.topLevelCalls()
	for index := range calls {
		p.emitCursorToolCall(occurredAt, record.model(), &calls[index])
	}
}

// emitCursorText emits one prose turn. The model is only ever the one this
// record names: the session model seen on an earlier record is state, not
// evidence about this turn, so it is never stamped here.
func (p *parser) emitCursorText(
	occurredAt time.Time,
	role belaytranscript.Role,
	text string,
	model string,
) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if role != belaytranscript.RoleAssistant {
		model = ""
	}
	p.emit(
		occurredAt,
		role,
		"",
		model,
		belaytranscript.Payload{
			Text:            text,
			CWD:             p.result.State.ProjectPath,
			ParentToolUseID: p.cursorParentToolUseID(),
		},
	)
}

func (p *parser) emitCursorToolCall(
	occurredAt time.Time,
	model string,
	call *cursorToolCall,
) {
	name := call.name()
	if name == "" {
		return
	}
	input := call.input()
	if callID := call.callID(); callID != "" {
		p.result.State.CallTools[callID] = name
	}
	p.emit(
		occurredAt,
		belaytranscript.RoleToolCall,
		name,
		model,
		belaytranscript.Payload{
			ToolInput:       input,
			RawCommand:      cursorRawCommand(input),
			CWD:             p.result.State.ProjectPath,
			ParentToolUseID: p.cursorParentToolUseID(),
			ToolCallID:      call.callID(),
		},
	)
}

func (p *parser) emitCursorToolResult(occurredAt time.Time, out *cursorToolOut) {
	callID := out.callID()
	isError, stated := out.errored()
	payload := belaytranscript.Payload{
		ToolResult:      p.cursorToolResultText(out),
		CWD:             p.result.State.ProjectPath,
		ParentToolUseID: p.cursorParentToolUseID(),
		ToolCallID:      callID,
		ExitCode:        out.exitCode(),
	}
	if stated {
		payload.ToolIsError = &isError
	}
	p.emit(
		occurredAt,
		belaytranscript.RoleToolResult,
		p.result.State.CallTools[callID],
		"",
		payload,
	)
}

// cursorParentToolUseID links a subagent thread's turns back to the thread that
// spawned them. Cursor's nested threads carry no attested field naming the
// originating tool call, so — exactly as for a child Codex rollout — Belay
// stamps a stable, explicitly-namespaced linkage instead of leaving the field
// empty, so a delegated turn is never mistaken for top-level user feedback.
// A top-level thread gets no parent.
func (p *parser) cursorParentToolUseID() string {
	if p.source.Primary {
		return ""
	}
	name := trimJSONLSuffix(filepath.Base(p.source.Path))
	if name == "" || name == "." {
		return ""
	}
	return "cursor-subagent-thread:" + name
}

// cursorToolResultText pulls the result body out of the field this build framed
// it in. Nothing is invented: a result that carries no body (an exit-code-only
// record) yields an empty string.
func (p *parser) cursorToolResultText(out *cursorToolOut) string {
	if out.plain != "" {
		return out.plain
	}
	for _, raw := range []json.RawMessage{out.Content, out.Output, out.Result} {
		if text := p.cursorResultBody(raw); text != "" {
			return text
		}
	}
	return firstCursorValue(out.Text, out.Stdout)
}

func (p *parser) cursorResultBody(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return p.compactJSON(raw)
}

// cursorRawCommand lifts the shell command line out of a tool input, across the
// argument spellings Cursor's shell tools use.
func cursorRawCommand(input json.RawMessage) string {
	if value := rawCommand(input); value != "" {
		return value
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	return cursorFirstInputString(fields, "commandLine", "command_line", "script")
}

// cursorInputString reads a tool input field as a string, coercing a JSON
// number to its literal form so a numeric argument is not silently dropped.
func cursorInputString(input map[string]json.RawMessage, key string) string {
	raw, ok := input[key]
	if !ok {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func cursorFirstInputString(input map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := cursorInputString(input, key); value != "" {
			return value
		}
	}
	return ""
}

func firstCursorValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
