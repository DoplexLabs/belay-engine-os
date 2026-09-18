package transcript

import (
	"encoding/json"
	"strings"
	"time"

	belaytranscript "github.com/DoplexLabs/belay-engine/internal/transcript"
)

type claudeRecord struct {
	Type                    string          `json:"type"`
	Subtype                 string          `json:"subtype"`
	SessionID               string          `json:"sessionId"`
	Timestamp               string          `json:"timestamp"`
	CWD                     string          `json:"cwd"`
	GitBranch               string          `json:"gitBranch"`
	UUID                    string          `json:"uuid"`
	AgentID                 string          `json:"agentId"`
	SourceToolAssistantUUID string          `json:"sourceToolAssistantUUID"`
	Content                 json.RawMessage `json:"content"`
	GitRemoteURL            string          `json:"gitRemoteURL"`
	RepositoryURL           string          `json:"repositoryUrl"`
	Message                 struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *claudeUsage    `json:"usage"`
	} `json:"message"`
}

type claudeUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheCreation            struct {
		Ephemeral5MInputTokens *int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1HInputTokens *int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type claudeContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   *bool           `json:"is_error"`
}

func (p *parser) parseClaude(raw jsonObject) {
	var record claudeRecord
	if err := unmarshalObject(raw, &record); err != nil {
		p.result.Issues++
		return
	}
	if record.SessionID != "" {
		if !p.result.State.NativeIdentityConfirmed {
			p.result.State.NativeSessionID = record.SessionID
			p.result.State.NativeIdentityConfirmed = true
		} else if p.result.State.NativeSessionID != record.SessionID {
			p.result.Issues++
		}
	}
	if record.CWD != "" {
		p.result.State.ProjectPath = record.CWD
	}
	if record.GitBranch != "" {
		p.result.State.GitBranch = record.GitBranch
	}
	if record.GitRemoteURL != "" {
		p.result.State.GitRemoteURL = record.GitRemoteURL
	} else if record.RepositoryURL != "" {
		p.result.State.GitRemoteURL = record.RepositoryURL
	}
	occurredAt := parseTime(record.Timestamp)
	parentToolID := p.parentToolUseID("", record.SourceToolAssistantUUID)
	switch record.Type {
	case "assistant":
		p.parseClaudeAssistant(record, occurredAt, parentToolID)
	case "user":
		p.parseClaudeUser(record, occurredAt, parentToolID)
	case "system":
		p.parseClaudeSystem(record, occurredAt, parentToolID)
	}
}

func (p *parser) parseClaudeAssistant(
	record claudeRecord,
	occurredAt time.Time,
	parentToolID string,
) {
	if record.Message.Model != "" {
		p.result.State.Model = record.Message.Model
	}
	var blocks []claudeContentBlock
	if err := json.Unmarshal(record.Message.Content, &blocks); err != nil {
		var text string
		if json.Unmarshal(record.Message.Content, &text) == nil && text != "" {
			blocks = []claudeContentBlock{{Type: "text", Text: text}}
		} else {
			p.result.Issues++
			return
		}
	}
	first := -1
	toolIDs := make([]string, 0)
	for _, block := range blocks {
		var index int
		switch block.Type {
		case "text":
			index = p.emit(
				occurredAt,
				belaytranscript.RoleAssistant,
				"",
				record.Message.Model,
				belaytranscript.Payload{
					Text:            block.Text,
					CWD:             record.CWD,
					GitBranch:       record.GitBranch,
					ParentToolUseID: parentToolID,
				},
			)
			p.rememberBillable(index)
		case "tool_use":
			toolIDs = append(toolIDs, block.ID)
			input := p.scrubJSON(block.Input)
			index = p.emit(
				occurredAt,
				belaytranscript.RoleToolCall,
				block.Name,
				record.Message.Model,
				belaytranscript.Payload{
					ToolInput:       input,
					RawCommand:      rawCommand(input),
					CWD:             record.CWD,
					GitBranch:       record.GitBranch,
					ParentToolUseID: parentToolID,
					ToolCallID:      block.ID,
				},
			)
			if block.ID != "" {
				p.result.State.CallTools[block.ID] = block.Name
			}
			p.rememberBillable(index)
		default:
			continue
		}
		if first < 0 {
			first = index
		}
	}
	if first < 0 && record.Message.Usage != nil {
		first = p.emit(
			occurredAt,
			belaytranscript.RoleAssistant,
			"",
			record.Message.Model,
			belaytranscript.Payload{
				CWD:             record.CWD,
				GitBranch:       record.GitBranch,
				ParentToolUseID: parentToolID,
			},
		)
		p.rememberBillable(first)
	}
	if first >= 0 && record.Message.Usage != nil {
		p.attachUsage(first, record.Message.Usage.value(), record.Message.Model)
	}
	if record.UUID != "" && len(toolIDs) == 1 && toolIDs[0] != "" {
		p.result.AssistantToolUseByUUID[record.UUID] = toolIDs[0]
	}
}

func (p *parser) parseClaudeUser(
	record claudeRecord,
	occurredAt time.Time,
	parentToolID string,
) {
	var text string
	if json.Unmarshal(record.Message.Content, &text) == nil {
		if text != "" {
			p.emit(
				occurredAt,
				belaytranscript.RoleUser,
				"",
				"",
				belaytranscript.Payload{
					Text:            text,
					CWD:             record.CWD,
					GitBranch:       record.GitBranch,
					ParentToolUseID: parentToolID,
				},
			)
		}
		return
	}
	var blocks []claudeContentBlock
	if err := json.Unmarshal(record.Message.Content, &blocks); err != nil {
		p.result.Issues++
		return
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			p.emit(
				occurredAt,
				belaytranscript.RoleUser,
				"",
				"",
				belaytranscript.Payload{
					Text:            block.Text,
					CWD:             record.CWD,
					GitBranch:       record.GitBranch,
					ParentToolUseID: parentToolID,
				},
			)
		case "tool_result":
			toolName := p.result.State.CallTools[block.ToolUseID]
			p.emit(
				occurredAt,
				belaytranscript.RoleToolResult,
				toolName,
				"",
				belaytranscript.Payload{
					ToolResult:      p.claudeToolResult(block.Content),
					CWD:             record.CWD,
					GitBranch:       record.GitBranch,
					ParentToolUseID: p.parentToolUseID("", record.SourceToolAssistantUUID),
					ToolCallID:      block.ToolUseID,
					ToolIsError:     block.IsError,
				},
			)
		}
	}
}

func (p *parser) parseClaudeSystem(
	record claudeRecord,
	occurredAt time.Time,
	parentToolID string,
) {
	var content string
	_ = json.Unmarshal(record.Content, &content)
	if content == "" {
		return
	}
	role := belaytranscript.RoleSystem
	if record.Subtype == "compact_boundary" {
		role = belaytranscript.RoleCompactionSummary
	}
	p.emit(
		occurredAt,
		role,
		"",
		"",
		belaytranscript.Payload{
			Text:            content,
			CWD:             record.CWD,
			GitBranch:       record.GitBranch,
			ParentToolUseID: parentToolID,
		},
	)
}

func (value *claudeUsage) value() usage {
	return usage{
		InputTokens:      value.InputTokens,
		OutputTokens:     value.OutputTokens,
		CacheReadTokens:  value.CacheReadInputTokens,
		CacheWriteTokens: value.CacheCreationInputTokens,
		CacheWrite5M:     value.CacheCreation.Ephemeral5MInputTokens,
		CacheWrite1H:     value.CacheCreation.Ephemeral1HInputTokens,
	}
}

func (p *parser) claudeToolResult(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []claudeContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var result []string
		for _, block := range blocks {
			if block.Text != "" {
				result = append(result, block.Text)
			}
		}
		return strings.Join(result, "\n")
	}
	return p.compactJSON(raw)
}

func unmarshalObject(raw jsonObject, target any) error {
	body, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}
