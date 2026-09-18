package transcript

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	belaytranscript "github.com/DoplexLabs/belay-engine/internal/transcript"
)

type codexEnvelope struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type codexEventMessage struct {
	OccurredAt time.Time
	Offset     int64
	Role       belaytranscript.Role
	Text       string
	Signature  string
}

type codexUsage struct {
	InputTokens           *int64 `json:"input_tokens"`
	OutputTokens          *int64 `json:"output_tokens"`
	CachedInputTokens     *int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens *int64 `json:"cache_write_input_tokens"`
	ReasoningOutputTokens *int64 `json:"reasoning_output_tokens"`
}

func (p *parser) parseCodex(raw jsonObject) {
	var record codexEnvelope
	if err := unmarshalObject(raw, &record); err != nil {
		p.result.Issues++
		return
	}
	var payload jsonObject
	if len(record.Payload) > 0 && json.Unmarshal(record.Payload, &payload) != nil {
		p.result.Issues++
		return
	}
	occurredAt := parseTime(record.Timestamp)
	switch record.Type {
	case "session_meta":
		p.parseCodexSessionMeta(payload)
	case "turn_context":
		p.parseCodexTurnContext(payload)
	case "response_item":
		p.parseCodexResponseItem(payload, occurredAt)
	case "token_usage_record":
		p.parseCodexTokenUsage(payload, occurredAt)
	case "event_msg":
		p.parseCodexEvent(payload, occurredAt)
	case "compacted":
		p.parseCodexCompaction(payload, occurredAt)
	}
}

func (p *parser) parseCodexSessionMeta(payload jsonObject) {
	id := stringField(payload, "id")
	parentThreadID := stringField(payload, "parent_thread_id")
	p.result.State.ParentThreadID = parentThreadID
	if parentThreadID == "" && id != "" &&
		!p.result.State.NativeIdentityConfirmed {
		p.result.State.NativeSessionID = id
		p.result.State.NativeIdentityConfirmed = true
	}
	if parentThreadID == "" && id != "" &&
		p.result.State.NativeSessionID != id {
		p.result.Issues++
	}
	if id != "" {
		p.result.State.CurrentThreadID = id
	}
	if cwd := stringField(payload, "cwd"); cwd != "" {
		p.result.State.ProjectPath = cwd
	}
	git := objectField(payload, "git")
	if remote := stringField(git, "repository_url"); remote != "" {
		p.result.State.GitRemoteURL = remote
	}
	if branch := stringField(git, "branch"); branch != "" {
		p.result.State.GitBranch = branch
	}
}

func (p *parser) parseCodexTurnContext(payload jsonObject) {
	if model := stringField(payload, "model"); model != "" {
		p.result.State.Model = model
	}
	if cwd := stringField(payload, "cwd"); cwd != "" {
		p.result.State.ProjectPath = cwd
	}
	if turnID := stringField(payload, "turn_id"); turnID != "" {
		p.result.State.CurrentTurnID = turnID
	}
}

func (p *parser) parseCodexResponseItem(payload jsonObject, occurredAt time.Time) {
	itemType := stringField(payload, "type")
	callID := stringField(payload, "call_id")
	parentToolID := p.parentToolUseID("", "")
	switch itemType {
	case "message":
		role := stringField(payload, "role")
		text := codexMessageText(payload["content"])
		if text == "" {
			return
		}
		mappedRole := belaytranscript.RoleSystem
		switch role {
		case "user":
			mappedRole = belaytranscript.RoleUser
		case "assistant":
			mappedRole = belaytranscript.RoleAssistant
		case "developer", "system":
			mappedRole = belaytranscript.RoleSystem
		default:
			return
		}
		signature := p.codexMessageSignature(mappedRole, text)
		p.result.State.ResponseMessages[signature] = true
		delete(p.result.State.EventMessageFallbacks, signature)
		index := p.emitWithIdentity(
			"codex-message:"+signature,
			occurredAt,
			mappedRole,
			"",
			p.result.State.Model,
			belaytranscript.Payload{
				Text:            text,
				CWD:             p.result.State.ProjectPath,
				GitBranch:       p.result.State.GitBranch,
				ParentToolUseID: parentToolID,
			},
		)
		if mappedRole == belaytranscript.RoleAssistant {
			p.rememberBillable(index)
		}
	case "function_call", "custom_tool_call", "tool_search_call":
		name := stringField(payload, "name")
		if name == "" && itemType == "tool_search_call" {
			name = "tool_search"
		}
		input := p.codexToolInput(payload)
		index := p.emit(
			occurredAt,
			belaytranscript.RoleToolCall,
			name,
			p.result.State.Model,
			belaytranscript.Payload{
				ToolInput:       input,
				RawCommand:      rawCommand(input),
				CWD:             p.result.State.ProjectPath,
				GitBranch:       p.result.State.GitBranch,
				ParentToolUseID: parentToolID,
				ToolCallID:      callID,
			},
		)
		if callID != "" {
			p.result.State.CallTools[callID] = name
		}
		p.rememberBillable(index)
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		result := codexResult(payload["output"])
		if existing, ok := p.resultByCall[callID]; ok {
			if p.result.Turns[existing].Payload.ToolResult == "" {
				p.result.Turns[existing].Payload.ToolResult = p.truncateToolResult(result)
			}
			return
		}
		if callID != "" && p.result.State.ResultCalls[callID] {
			return
		}
		index := p.emit(
			occurredAt,
			belaytranscript.RoleToolResult,
			p.result.State.CallTools[callID],
			"",
			belaytranscript.Payload{
				ToolResult:      result,
				CWD:             p.result.State.ProjectPath,
				GitBranch:       p.result.State.GitBranch,
				ParentToolUseID: p.parentToolUseID("", ""),
				ToolCallID:      callID,
			},
		)
		if callID != "" {
			p.resultByCall[callID] = index
			p.result.State.ResultCalls[callID] = true
		}
	}
}

func (p *parser) parseCodexTokenUsage(payload jsonObject, occurredAt time.Time) {
	turnID := stringField(payload, "turn_id")
	var raw codexUsage
	if json.Unmarshal(payload["usage"], &raw) != nil {
		p.result.Issues++
		return
	}
	delete(p.result.State.PendingCodexUsage, turnID)
	if p.result.State.FinalizedCodexUsage[turnID] {
		p.tokenUsageTurns[turnID] = true
		return
	}
	p.emitCodexUsage(
		turnID,
		occurredAt,
		p.offset,
		p.result.State.Model,
		p.result.State.ProjectPath,
		p.result.State.GitBranch,
		p.parentToolUseID("", ""),
		raw.value(),
	)
	p.tokenUsageTurns[turnID] = true
}

func (p *parser) parseCodexEvent(payload jsonObject, occurredAt time.Time) {
	eventType := stringField(payload, "type")
	switch eventType {
	case "task_started":
		if turnID := stringField(payload, "turn_id"); turnID != "" {
			p.result.State.CurrentTurnID = turnID
		}
	case "item_completed":
		p.parseCodexCompletedItem(payload, occurredAt)
	case "mcp_tool_call_end":
		p.parseCodexMCPResult(payload, occurredAt)
	case "token_count":
		p.parseCodexTokenCount(payload, occurredAt)
	case "user_message", "agent_message":
		p.parseCodexEventMessage(payload, occurredAt, eventType)
	}
}

func (p *parser) parseCodexCompletedItem(
	event jsonObject,
	occurredAt time.Time,
) {
	raw := event["item"]
	var item jsonObject
	if json.Unmarshal(raw, &item) != nil {
		p.result.Issues++
		return
	}
	itemType := stringField(item, "type")
	id := stringField(item, "id")
	parentToolID := p.parentToolUseID("", "")
	switch itemType {
	case "CommandExecution":
		command := stringSlice(item["command"])
		result := firstNonempty(
			stringField(item, "formatted_output"),
			joinOutput(stringField(item, "stdout"), stringField(item, "stderr")),
			stringField(item, "aggregated_output"),
		)
		exitCode := intField(item, "exit_code")
		durationMS := durationMilliseconds(item)
		if durationMS == nil {
			durationMS = durationMilliseconds(event)
		}
		if durationMS == nil {
			started := int64Field(event, "started_at_ms")
			completed := int64Field(event, "completed_at_ms")
			if started != nil && completed != nil && *completed >= *started {
				value := *completed - *started
				durationMS = &value
			}
		}
		if existing, ok := p.resultByCall[id]; ok {
			turn := &p.result.Turns[existing]
			turn.Payload.RawCommand = p.scrub(strings.Join(command, " "))
			turn.Payload.CWD = p.scrub(firstNonempty(stringField(item, "cwd"), turn.Payload.CWD))
			turn.Payload.ToolResult = p.truncateToolResult(result)
			turn.Payload.ExitCode = exitCode
			turn.Payload.DurationMS = durationMS
			turn.Payload.ToolCallID = p.scrub(id)
			if id != "" {
				p.result.State.ResultCalls[id] = true
			}
			return
		}
		if id != "" && p.result.State.ResultCalls[id] {
			if exitCode != nil || durationMS != nil {
				p.emit(
					occurredAt,
					belaytranscript.RoleToolResult,
					firstNonempty(p.result.State.CallTools[id], "exec_command"),
					"",
					belaytranscript.Payload{
						RawCommand:           strings.Join(command, " "),
						CWD:                  firstNonempty(stringField(item, "cwd"), p.result.State.ProjectPath),
						GitBranch:            p.result.State.GitBranch,
						ParentToolUseID:      p.parentToolUseID("", ""),
						ToolCallID:           id,
						ExitCode:             exitCode,
						DurationMS:           durationMS,
						SupplementalEvidence: true,
					},
				)
			}
			return
		}
		index := p.emit(
			occurredAt,
			belaytranscript.RoleToolResult,
			firstNonempty(p.result.State.CallTools[id], "exec_command"),
			"",
			belaytranscript.Payload{
				ToolResult:      result,
				RawCommand:      strings.Join(command, " "),
				CWD:             firstNonempty(stringField(item, "cwd"), p.result.State.ProjectPath),
				GitBranch:       p.result.State.GitBranch,
				ParentToolUseID: p.parentToolUseID("", ""),
				ToolCallID:      id,
				ExitCode:        exitCode,
				DurationMS:      durationMS,
			},
		)
		if id != "" {
			p.resultByCall[id] = index
			p.result.State.ResultCalls[id] = true
		}
	case "FileChange":
		p.emit(
			occurredAt,
			belaytranscript.RoleToolResult,
			"apply_patch",
			"",
			belaytranscript.Payload{
				ToolResult:      codexResult(item["changes"]),
				CWD:             p.result.State.ProjectPath,
				GitBranch:       p.result.State.GitBranch,
				ParentToolUseID: parentToolID,
			},
		)
	case "McpToolCall":
		result := codexResult(item["result"])
		server := stringField(item, "server")
		tool := stringField(item, "tool")
		p.emit(
			occurredAt,
			belaytranscript.RoleToolResult,
			strings.Trim(strings.Join([]string{server, tool}, "/"), "/"),
			"",
			belaytranscript.Payload{
				ToolResult:      result,
				CWD:             p.result.State.ProjectPath,
				GitBranch:       p.result.State.GitBranch,
				ParentToolUseID: p.parentToolUseID("", ""),
			},
		)
	case "CollabAgentToolCall":
		tool := stringField(item, "tool")
		input := p.scrubJSON(item["prompt"])
		if len(input) == 0 {
			input = p.scrubJSON(raw)
		}
		p.emit(
			occurredAt,
			belaytranscript.RoleToolCall,
			tool,
			p.result.State.Model,
			belaytranscript.Payload{
				ToolInput:       input,
				CWD:             p.result.State.ProjectPath,
				GitBranch:       p.result.State.GitBranch,
				ParentToolUseID: parentToolID,
				ToolCallID:      id,
			},
		)
		for _, threadID := range stringSlice(item["receiver_thread_ids"]) {
			if threadID != "" && id != "" {
				p.result.State.ThreadParentTool[threadID] = id
			}
		}
	}
}

func (p *parser) parseCodexMCPResult(payload jsonObject, occurredAt time.Time) {
	callID := stringField(payload, "call_id")
	invocation := objectField(payload, "invocation")
	server := stringField(invocation, "server")
	tool := stringField(invocation, "tool")
	name := strings.Trim(strings.Join([]string{server, tool}, "/"), "/")
	if name == "" {
		name = p.result.State.CallTools[callID]
	}
	result := codexResult(payload["result"])
	if existing, ok := p.resultByCall[callID]; ok {
		p.result.Turns[existing].Payload.ToolResult = p.truncateToolResult(result)
		if callID != "" {
			p.result.State.ResultCalls[callID] = true
		}
		return
	}
	if callID != "" && p.result.State.ResultCalls[callID] {
		return
	}
	index := p.emit(
		occurredAt,
		belaytranscript.RoleToolResult,
		name,
		"",
		belaytranscript.Payload{
			ToolResult:      result,
			CWD:             p.result.State.ProjectPath,
			GitBranch:       p.result.State.GitBranch,
			ParentToolUseID: p.parentToolUseID("", ""),
			ToolCallID:      callID,
		},
	)
	if callID != "" {
		p.resultByCall[callID] = index
		p.result.State.ResultCalls[callID] = true
	}
}

func (p *parser) parseCodexTokenCount(payload jsonObject, occurredAt time.Time) {
	info := objectField(payload, "info")
	var raw codexUsage
	if json.Unmarshal(info["last_token_usage"], &raw) != nil {
		return
	}
	turnID := p.result.State.CurrentTurnID
	if turnID == "" || p.tokenUsageTurns[turnID] ||
		p.result.State.FinalizedCodexUsage[turnID] {
		return
	}
	value := raw.value()
	p.result.State.PendingCodexUsage[turnID] = PendingCodexUsage{
		TurnID:           turnID,
		OccurredAt:       occurredAt,
		JSONLByteOffset:  p.offset,
		Model:            p.result.State.Model,
		CWD:              p.result.State.ProjectPath,
		GitBranch:        p.result.State.GitBranch,
		ParentToolUseID:  p.parentToolUseID("", ""),
		InputTokens:      cloneInt64(value.InputTokens),
		OutputTokens:     cloneInt64(value.OutputTokens),
		CacheReadTokens:  cloneInt64(value.CacheReadTokens),
		CacheWriteTokens: cloneInt64(value.CacheWriteTokens),
	}
}

func (p *parser) parseCodexCompaction(payload jsonObject, occurredAt time.Time) {
	message := stringField(payload, "message")
	if message == "" {
		return
	}
	p.emit(
		occurredAt,
		belaytranscript.RoleCompactionSummary,
		"",
		"",
		belaytranscript.Payload{
			Text:            message,
			CWD:             p.result.State.ProjectPath,
			GitBranch:       p.result.State.GitBranch,
			ParentToolUseID: p.parentToolUseID("", ""),
		},
	)
}

func (p *parser) parseCodexEventMessage(
	payload jsonObject,
	occurredAt time.Time,
	eventType string,
) {
	text := firstNonempty(
		stringField(payload, "message"),
		stringField(payload, "text"),
	)
	if text == "" {
		return
	}
	role := belaytranscript.RoleAssistant
	if eventType == "user_message" {
		role = belaytranscript.RoleUser
	}
	signature := p.codexMessageSignature(role, text)
	if p.result.State.ResponseMessages[signature] {
		return
	}
	p.pendingMessages = append(p.pendingMessages, codexEventMessage{
		OccurredAt: occurredAt,
		Offset:     p.offset,
		Role:       role,
		Text:       text,
		Signature:  signature,
	})
}

func (p *parser) applyCodexMessageFallbacks() {
	for _, message := range p.pendingMessages {
		if p.result.State.ResponseMessages[message.Signature] {
			continue
		}
		p.offset = message.Offset
		p.emitWithIdentity(
			"codex-message:"+message.Signature,
			message.OccurredAt,
			message.Role,
			"",
			p.result.State.Model,
			belaytranscript.Payload{
				Text:            message.Text,
				CWD:             p.result.State.ProjectPath,
				GitBranch:       p.result.State.GitBranch,
				ParentToolUseID: p.parentToolUseID("", ""),
			},
		)
		p.result.State.EventMessageFallbacks[message.Signature] = true
	}
}

func (p *parser) codexMessageSignature(
	role belaytranscript.Role,
	text string,
) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		p.result.State.CurrentTurnID,
		string(role),
		text,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (p *parser) emitCodexUsage(
	turnID string,
	occurredAt time.Time,
	offset int64,
	model, cwd, branch, parentToolID string,
	value usage,
) {
	p.offset = offset
	index := p.emitWithIdentity(
		"codex-usage:"+turnID,
		occurredAt,
		belaytranscript.RoleAssistant,
		"",
		model,
		belaytranscript.Payload{
			CWD:             cwd,
			GitBranch:       branch,
			ParentToolUseID: parentToolID,
		},
	)
	p.attachUsage(index, value, model)
}

func (p *parser) finalizePendingCodexUsage() {
	for turnID, pending := range p.result.State.PendingCodexUsage {
		p.emitCodexUsage(
			turnID,
			pending.OccurredAt,
			pending.JSONLByteOffset,
			pending.Model,
			pending.CWD,
			pending.GitBranch,
			pending.ParentToolUseID,
			usage{
				InputTokens:       pending.InputTokens,
				OutputTokens:      pending.OutputTokens,
				CacheReadTokens:   pending.CacheReadTokens,
				CacheWriteTokens:  pending.CacheWriteTokens,
				InputIncludesRead: true,
			},
		)
		p.result.State.FinalizedCodexUsage[turnID] = true
		delete(p.result.State.PendingCodexUsage, turnID)
	}
}

func FinalizePendingCodexUsage(
	source Source,
	state State,
	boundary Boundary,
) Result {
	if state.FinalizedCodexUsage == nil {
		state.FinalizedCodexUsage = make(map[string]bool)
	}
	value := &parser{
		source:  source,
		options: ParseOptions{Boundary: boundary},
		result: Result{
			State: state,
		},
	}
	value.finalizePendingCodexUsage()
	return value.result
}

func (value codexUsage) value() usage {
	return usage{
		InputTokens:       value.InputTokens,
		OutputTokens:      value.OutputTokens,
		CacheReadTokens:   value.CachedInputTokens,
		CacheWriteTokens:  value.CacheWriteInputTokens,
		ReasoningTokens:   value.ReasoningOutputTokens,
		InputIncludesRead: true,
	}
}

func codexMessageText(raw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var result []string
	for _, block := range blocks {
		if block.Type == "input_text" || block.Type == "output_text" {
			result = append(result, block.Text)
		}
	}
	return strings.Join(result, "\n")
}

func (p *parser) codexToolInput(payload jsonObject) json.RawMessage {
	for _, key := range []string{"arguments", "input"} {
		raw := payload[key]
		if len(raw) == 0 {
			continue
		}
		var encoded string
		if json.Unmarshal(raw, &encoded) == nil {
			if json.Valid([]byte(encoded)) {
				return p.scrubJSON(json.RawMessage(encoded))
			}
			wrapped, _ := json.Marshal(map[string]string{"value": encoded})
			return p.scrubJSON(wrapped)
		}
		return p.scrubJSON(raw)
	}
	return nil
}

func codexResult(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var buffer bytes.Buffer
	if json.Compact(&buffer, raw) == nil {
		return buffer.String()
	}
	return string(raw)
}

func rawCommand(raw json.RawMessage) string {
	var input map[string]any
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	for _, key := range []string{"cmd", "command"} {
		switch value := input[key].(type) {
		case string:
			return value
		case []any:
			parts := make([]string, 0, len(value))
			for _, part := range value {
				if text, ok := part.(string); ok {
					parts = append(parts, text)
				}
			}
			return strings.Join(parts, " ")
		}
	}
	return ""
}

func stringSlice(raw json.RawMessage) []string {
	var result []string
	if json.Unmarshal(raw, &result) == nil {
		return result
	}
	var values []any
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func joinOutput(stdout, stderr string) string {
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

func intField(object jsonObject, key string) *int {
	var value int
	if json.Unmarshal(object[key], &value) != nil {
		return nil
	}
	return &value
}

func int64Field(object jsonObject, key string) *int64 {
	var value int64
	if json.Unmarshal(object[key], &value) != nil {
		return nil
	}
	return &value
}

func durationMilliseconds(object jsonObject) *int64 {
	if value := int64Field(object, "duration_ms"); value != nil {
		return value
	}
	duration := objectField(object, "duration")
	var seconds, nanos int64
	_ = json.Unmarshal(duration["secs"], &seconds)
	_ = json.Unmarshal(duration["nanos"], &nanos)
	if seconds == 0 && nanos == 0 {
		_ = json.Unmarshal(duration["seconds"], &seconds)
		_ = json.Unmarshal(duration["nanoseconds"], &nanos)
	}
	if seconds == 0 && nanos == 0 {
		return nil
	}
	value := seconds*1000 + nanos/1_000_000
	return &value
}
