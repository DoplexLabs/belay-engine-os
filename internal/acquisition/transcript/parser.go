package transcript

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	belaytranscript "github.com/DoplexLabs/belay-engine/internal/transcript"
)

const ParserVersion = "belay.native-transcript.v3"

type parser struct {
	source          Source
	options         ParseOptions
	result          Result
	line            []byte
	offset          int64
	block           int
	lastBillable    int
	billableByTurn  map[string]int
	tokenUsageTurns map[string]bool
	resultByCall    map[string]int
	pendingMessages []codexEventMessage
	sequence        int64
}

func Parse(
	ctx context.Context,
	source Source,
	reader io.Reader,
	startOffset int64,
	options ParseOptions,
) (Result, error) {
	if startOffset < 0 {
		return Result{}, errors.New("negative transcript offset")
	}
	if options.Boundary.SessionKey == nil ||
		options.Boundary.ScrubSecrets == nil {
		return Result{}, errors.New("transcript boundary is required")
	}
	state := options.State
	if state.NativeSessionID == "" {
		state.NativeSessionID = source.NativeSessionID
	}
	if state.CallTools == nil {
		state.CallTools = make(map[string]string)
	}
	if state.ThreadParentTool == nil {
		state.ThreadParentTool = make(map[string]string)
	}
	if state.ResultCalls == nil {
		state.ResultCalls = make(map[string]bool)
	}
	if state.ResponseMessages == nil {
		state.ResponseMessages = make(map[string]bool)
	}
	if state.EventMessageFallbacks == nil {
		state.EventMessageFallbacks = make(map[string]bool)
	}
	if state.PendingCodexUsage == nil {
		state.PendingCodexUsage = make(map[string]PendingCodexUsage)
	}
	if state.FinalizedCodexUsage == nil {
		state.FinalizedCodexUsage = make(map[string]bool)
	}
	value := &parser{
		source:          source,
		options:         options,
		lastBillable:    -1,
		billableByTurn:  make(map[string]int),
		tokenUsageTurns: make(map[string]bool),
		resultByCall:    make(map[string]int),
		result: Result{
			State:                  state,
			EndOffset:              startOffset,
			Complete:               true,
			AssistantToolUseByUUID: make(map[string]string),
		},
	}
	buffer := bufio.NewReaderSize(reader, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		lineStart := value.result.EndOffset
		line, err := buffer.ReadBytes('\n')
		switch {
		case err == nil:
		case errors.Is(err, io.EOF) && len(line) == 0:
			value.applyCodexMessageFallbacks()
			if options.FinalizePendingUsage {
				value.finalizePendingCodexUsage()
			}
			return value.result, nil
		case errors.Is(err, io.EOF):
			value.result.Complete = false
			return value.result, nil
		default:
			return Result{}, fmt.Errorf("read transcript JSONL: %w", err)
		}
		value.result.EndOffset += int64(len(line))
		lineText := strings.TrimSuffix(string(line), "\n")
		lineText = strings.TrimSuffix(lineText, "\r")
		if strings.TrimSpace(lineText) == "" {
			continue
		}
		var record jsonObject
		if err := json.Unmarshal([]byte(lineText), &record); err != nil {
			value.result.Issues++
			continue
		}
		value.line = []byte(lineText)
		value.offset = lineStart
		value.block = 0
		switch source.Agent {
		case AgentClaude:
			value.parseClaude(record)
		case AgentCodex:
			value.parseCodex(record)
		default:
			return Result{}, errors.New("unsupported transcript agent")
		}
	}
}

func (p *parser) emit(
	occurredAt time.Time,
	role belaytranscript.Role,
	toolName string,
	model string,
	payload belaytranscript.Payload,
) int {
	return p.emitWithIdentity("", occurredAt, role, toolName, model, payload)
}

func (p *parser) emitWithIdentity(
	logicalIdentity string,
	occurredAt time.Time,
	role belaytranscript.Role,
	toolName string,
	model string,
	payload belaytranscript.Payload,
) int {
	if occurredAt.IsZero() || p.result.State.NativeSessionID == "" {
		p.result.Issues++
		return -1
	}
	if p.source.Agent == AgentCodex &&
		strings.TrimSpace(payload.ParentToolUseID) == "" {
		payload.ParentToolUseID = p.parentToolUseID("", "")
	}
	block := p.block
	p.block++
	version := p.options.SourceRecordKeyVersion
	if version == "" {
		version = "native-transcript.v1"
	}
	identity := logicalIdentity
	if identity == "" {
		lineDigest := sha256.Sum256(p.line)
		identity = strings.Join([]string{
			version,
			p.source.Agent,
			p.source.Path,
			strconv.FormatInt(p.offset, 10),
			strconv.Itoa(block),
			hex.EncodeToString(lineDigest[:]),
		}, "\x00")
	} else {
		identity = strings.Join([]string{
			version,
			p.source.Agent,
			p.source.Path,
			logicalIdentity,
		}, "\x00")
	}
	sum := sha256.Sum256([]byte(identity))
	sourceSum := sha256.Sum256([]byte(p.source.Agent + "\x00" + p.source.Path))
	payload.Text = p.scrub(payload.Text)
	payload.ToolInput = p.scrubJSON(payload.ToolInput)
	payload.ToolResult = p.truncateToolResult(payload.ToolResult)
	payload.RawCommand = p.scrub(payload.RawCommand)
	payload.CWD = p.scrub(payload.CWD)
	payload.GitBranch = p.scrub(payload.GitBranch)
	payload.ParentToolUseID = p.scrub(payload.ParentToolUseID)
	payload.ToolCallID = p.scrub(payload.ToolCallID)
	payload.SourceFileID = "src_" + hex.EncodeToString(sourceSum[:16])
	payload.ParserVersion = ParserVersion
	payload.PriceTableVersion = PriceTableVersion
	payload.JSONLByteOffset = p.offset
	turn := belaytranscript.Turn{
		TurnID:          "trn_" + hex.EncodeToString(sum[:16]),
		SourceRecordKey: version + ":" + hex.EncodeToString(sum[:]),
		SessionKey: p.options.Boundary.SessionKey(
			p.source.Agent,
			p.result.State.NativeSessionID,
		),
		OccurredAt: occurredAt.UTC(),
		TurnIndex:  p.sequence,
		Role:       role,
		ToolName:   p.scrub(toolName),
		Model:      p.scrub(model),
		Payload:    payload,
	}
	p.sequence++
	p.result.Turns = append(p.result.Turns, turn)
	return len(p.result.Turns) - 1
}

func (p *parser) attachUsage(index int, value usage, model string) {
	if index < 0 || index >= len(p.result.Turns) {
		return
	}
	turn := &p.result.Turns[index]
	turn.Model = p.scrub(model)
	turn.InputTokens = normalizedInputTokens(value)
	turn.OutputTokens = cloneInt64(value.OutputTokens)
	turn.CacheReadTokens = cloneInt64(value.CacheReadTokens)
	turn.CacheWriteTokens = cloneInt64(value.CacheWriteTokens)
	turn.CostUSD = calculateCost(model, value)
}

func normalizedInputTokens(value usage) *int64 {
	if value.InputTokens == nil {
		return nil
	}
	result := *value.InputTokens
	if value.InputIncludesRead {
		result -= int64(count(value.CacheReadTokens))
		result -= int64(count(value.CacheWriteTokens))
		if result < 0 {
			result = 0
		}
	}
	return &result
}

func (p *parser) parentToolUseID(explicit, sourceAssistantUUID string) string {
	if explicit != "" {
		return explicit
	}
	if sourceAssistantUUID != "" {
		if value := p.options.ParentToolUses[sourceAssistantUUID]; value != "" {
			return value
		}
	}
	if value := p.result.State.ThreadParentTool[p.result.State.CurrentThreadID]; value != "" {
		return value
	}
	// Child Codex rollouts do not always retain the originating
	// CollabAgentToolCall ID. Preserve an explicit stable parent linkage so
	// delegated turns cannot be mistaken for top-level user feedback.
	if parentThreadID := strings.TrimSpace(
		p.result.State.ParentThreadID,
	); parentThreadID != "" {
		return "codex-parent-thread:" + parentThreadID
	}
	return ""
}

func (p *parser) rememberBillable(index int) {
	if index < 0 {
		return
	}
	p.lastBillable = index
	if p.result.State.CurrentTurnID != "" {
		p.billableByTurn[p.result.State.CurrentTurnID] = index
	}
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func stringField(object jsonObject, key string) string {
	var result string
	_ = json.Unmarshal(object[key], &result)
	return result
}

func objectField(object jsonObject, key string) jsonObject {
	var result jsonObject
	_ = json.Unmarshal(object[key], &result)
	return result
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
