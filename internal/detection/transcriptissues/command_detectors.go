package transcriptissues

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type commandAttempt struct {
	callIndex   int
	resultIndex int
	class       string
	normalized  string
	raw         string
	errorSig    string
	call        transcript.Turn
	result      transcript.Turn
}

type fileEdit struct {
	index int
	turn  transcript.Turn
	hash  string
}

func detectRetryLoops(
	ctx context.Context,
	project preparedProject,
) ([]observation, error) {
	var result []observation
	for sessionIndex, session := range project.sessions {
		if sessionIndex&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		byCommand := make(map[string][]commandAttempt)
		for _, attempt := range failedCommandAttempts(session, project.config) {
			key := attempt.class + "\x00" + attempt.normalized
			byCommand[key] = append(byCommand[key], attempt)
		}
		for _, attempts := range byCommand {
			sort.Slice(attempts, func(i, j int) bool {
				return attempts[i].resultIndex < attempts[j].resultIndex
			})
			bestStart, bestEnd := 0, 0
			for start, end := 0, 0; end < len(attempts); end++ {
				for attempts[end].resultIndex-attempts[start].resultIndex > 30 {
					start++
				}
				if end-start+1 > bestEnd-bestStart {
					bestStart, bestEnd = start, end+1
				}
			}
			if bestEnd-bestStart >= 3 {
				result = append(
					result,
					retryLoopObservations(
						project,
						session,
						attempts[bestStart:bestEnd],
					)...,
				)
			}
		}
	}
	return result, nil
}

func retryLoopObservations(
	project preparedProject,
	session preparedSession,
	attempts []commandAttempt,
) []observation {
	first := attempts[0]
	last := attempts[len(attempts)-1]
	errorSig := first.errorSig
	if errorSig == "" {
		errorSig = "structured command failure"
	}
	fingerprint := project.project.Identity + "\x00" + first.class + "\x00" + errorSig
	cost := windowCost(session.turns, first.resultIndex, last.resultIndex)
	excerpts := make([]issueintel.Excerpt, 0, maxExcerpts)
	for _, attempt := range attempts {
		if len(excerpts) == maxExcerpts {
			break
		}
		excerpts = append(excerpts, excerptFromTurn(attempt.result))
	}
	result := make([]observation, 0, len(attempts))
	for index, attempt := range attempts {
		valueCost := issueintel.Cost{}
		var valueSpans []costSpan
		valueExcerpts := []issueintel.Excerpt{excerptFromTurn(attempt.result)}
		if index == 0 {
			valueCost = cost
			valueSpans = []costSpan{
				windowSpan(session, first.resultIndex, last.resultIndex),
			}
			valueExcerpts = excerpts
		}
		result = append(result, observation{
			detectorID:  issueintel.DetectorRetryLoop,
			fingerprint: fingerprint,
			subject:     commandSubject(first.normalized),
			cost:        valueCost,
			costSpans:   valueSpans,
			session:     sessionRef(session),
			firstSeen:   first.result.OccurredAt,
			lastSeen:    last.result.OccurredAt,
			occurredAt:  attempt.result.OccurredAt,
			excerpts:    valueExcerpts,
			fix: issueintel.SuggestedFix{
				Kind:       "harness_rule",
				TargetFile: instructionTarget(project),
				Rationale: fmt.Sprintf(
					"Stop retrying %s after two identical failures; inspect and address `%s` before trying again.",
					commandSubject(first.normalized),
					errorSig,
				),
			},
		})
	}
	return result
}

func detectRecurringErrors(
	ctx context.Context,
	project preparedProject,
) ([]observation, error) {
	type sessionErrors struct {
		session  preparedSession
		attempts map[string][]commandAttempt
	}
	bySignature := make(map[string][]sessionErrors)
	for index, session := range project.sessions {
		if index&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		grouped := make(map[string][]commandAttempt)
		for _, attempt := range failedCommandAttempts(session, project.config) {
			if attempt.errorSig != "" {
				grouped[attempt.errorSig] = append(grouped[attempt.errorSig], attempt)
			}
		}
		for signature, attempts := range grouped {
			bySignature[signature] = append(bySignature[signature], sessionErrors{
				session:  session,
				attempts: map[string][]commandAttempt{signature: attempts},
			})
		}
	}

	var result []observation
	for signature, sessions := range bySignature {
		if len(sessions) < 2 {
			continue
		}
		for _, value := range sessions {
			attempts := value.attempts[signature]
			first := attempts[0]
			last := attempts[len(attempts)-1]
			cost := issueintel.Cost{}
			spans := make([]costSpan, 0, len(attempts))
			for _, attempt := range attempts {
				cost = mergeCost(
					cost,
					windowCost(
						value.session.turns,
						attempt.callIndex,
						attempt.resultIndex,
					),
				)
				spans = append(
					spans,
					windowSpan(
						value.session,
						attempt.callIndex,
						attempt.resultIndex,
					),
				)
			}
			excerpts := make([]issueintel.Excerpt, 0, maxExcerpts)
			for _, attempt := range attempts {
				if len(excerpts) == maxExcerpts {
					break
				}
				excerpts = append(excerpts, excerptFromTurn(attempt.result))
			}
			for index, attempt := range attempts {
				valueCost := issueintel.Cost{}
				var valueSpans []costSpan
				valueExcerpts := []issueintel.Excerpt{
					excerptFromTurn(attempt.result),
				}
				if index == 0 {
					valueCost = cost
					valueSpans = spans
					valueExcerpts = excerpts
				}
				result = append(result, observation{
					detectorID:  issueintel.DetectorRecurringError,
					fingerprint: project.project.Identity + "\x00" + signature,
					subject:     "`" + signature + "`",
					cost:        valueCost,
					costSpans:   valueSpans,
					session:     sessionRef(value.session),
					firstSeen:   first.result.OccurredAt,
					lastSeen:    last.result.OccurredAt,
					occurredAt:  attempt.result.OccurredAt,
					excerpts:    valueExcerpts,
					fix: issueintel.SuggestedFix{
						Kind:       "harness_rule",
						TargetFile: instructionTarget(project),
						Rationale:  "Add a project rule that recognizes this error signature and applies the known repair before repeating the failing command.",
					},
				})
			}
		}
	}
	return result, nil
}

func detectPermissionChurn(
	ctx context.Context,
	project preparedProject,
) ([]observation, error) {
	type approval struct {
		session   preparedSession
		callIndex int
		userIndex int
		call      transcript.Turn
		user      transcript.Turn
		pattern   string
	}
	approvals := make(map[string][]approval)
	denials := make(map[string]bool)
	for sessionIndex, session := range project.sessions {
		if sessionIndex&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		for index, turn := range session.turns {
			if turn.Role != transcript.RoleUser {
				continue
			}
			text := strings.TrimSpace(turn.Payload.Text)
			if !isApprovalText(text) && !isDenialText(text) {
				continue
			}
			call, ok := nearestPriorToolCall(session.turns, index)
			if !ok {
				continue
			}
			callIndex := turnSliceIndex(session.turns, call.TurnID)
			pattern := permissionPattern(call, project.config)
			if pattern == "" {
				continue
			}
			if isDenialText(text) {
				denials[pattern] = true
				continue
			}
			approvals[pattern] = append(approvals[pattern], approval{
				session:   session,
				callIndex: callIndex,
				userIndex: index,
				call:      call,
				user:      turn,
				pattern:   pattern,
			})
		}
	}

	var result []observation
	for pattern, values := range approvals {
		if len(values) < 5 || denials[pattern] {
			continue
		}
		target := ".codex/rules/default.rules"
		claude := 0
		for _, value := range values {
			if value.session.metadata.Agent == "claude-code" {
				claude++
			}
		}
		if claude*2 >= len(values) {
			target = ".claude/settings.json"
		}
		for _, value := range values {
			first, last := observedBounds(value.call, value.user)
			result = append(result, observation{
				detectorID:  issueintel.DetectorPermissionChurn,
				fingerprint: pattern,
				subject:     "`" + permissionPatternLabel(pattern) + "`",
				cost: windowCost(
					value.session.turns,
					value.callIndex,
					value.userIndex,
				),
				costSpans: []costSpan{
					windowSpan(
						value.session,
						value.callIndex,
						value.userIndex,
					),
				},
				session:    sessionRef(value.session),
				firstSeen:  first,
				lastSeen:   last,
				occurredAt: value.user.OccurredAt,
				excerpts: []issueintel.Excerpt{
					excerptFromTurn(value.call),
					excerptFromTurn(value.user),
				},
				fix: issueintel.SuggestedFix{
					Kind:       "permission_allowlist",
					TargetFile: target,
					Rationale:  "Add this repeatedly approved tool pattern to the narrowest project permission allowlist because no denial was observed.",
				},
			})
		}
	}
	return result, nil
}

func detectFileThrash(
	ctx context.Context,
	project preparedProject,
) ([]observation, error) {
	type edit struct {
		index int
		turn  transcript.Turn
		hash  string
	}
	var result []observation
	for sessionIndex, session := range project.sessions {
		if sessionIndex&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		byFile := make(map[string][]fileEdit)
		for index, turn := range session.turns {
			for _, file := range editedFiles(turn) {
				byFile[file] = append(byFile[file], fileEdit{
					index: index,
					turn:  turn,
					hash:  editContentHash(turn),
				})
			}
		}
		for file, edits := range byFile {
			cycle := editHashCycle(edits)
			if len(edits) < 4 && !cycle {
				continue
			}
			cost := selectedTurnCost(edits)
			excerpts := make([]issueintel.Excerpt, 0, maxExcerpts)
			for _, edit := range edits {
				if len(excerpts) == maxExcerpts {
					break
				}
				excerpts = append(excerpts, excerptFromTurn(edit.turn))
			}
			for index, edit := range edits {
				valueCost := issueintel.Cost{}
				var valueSpans []costSpan
				valueExcerpts := []issueintel.Excerpt{excerptFromTurn(edit.turn)}
				if index == 0 {
					valueCost = cost
					valueSpans = make([]costSpan, 0, len(edits))
					for _, selected := range edits {
						billable := associatedBillableIndex(
							session.turns,
							selected.index,
						)
						if billable < 0 {
							billable = selected.index
						}
						valueSpans = append(
							valueSpans,
							windowSpan(
								session,
								billable,
								billable,
							),
						)
					}
					valueExcerpts = excerpts
				}
				result = append(result, observation{
					detectorID:  issueintel.DetectorFileThrash,
					fingerprint: file,
					subject:     "`" + file + "`",
					cost:        valueCost,
					costSpans:   valueSpans,
					session:     sessionRef(session),
					firstSeen:   edits[0].turn.OccurredAt,
					lastSeen:    edits[len(edits)-1].turn.OccurredAt,
					occurredAt:  edit.turn.OccurredAt,
					excerpts:    valueExcerpts,
					fix: issueintel.SuggestedFix{
						Kind:       "harness_rule",
						TargetFile: instructionTarget(project),
						Rationale:  "Require the agent to inspect and plan the complete file change before editing, then verify once after the coherent edit.",
					},
				})
			}
		}
	}
	return result, nil
}

func failedCommandAttempts(
	session preparedSession,
	config issueintel.ProjectConfig,
) []commandAttempt {
	var result []commandAttempt
	for callIndex, call := range session.turns {
		class, normalized, raw, ok := commandInfo(call, config)
		if !ok || call.Role != transcript.RoleToolCall {
			continue
		}
		resultIndex, toolResult, ok := matchingToolResult(
			session.turns,
			callIndex,
			call,
		)
		if !ok || !turnFailed(toolResult) {
			continue
		}
		result = append(result, commandAttempt{
			callIndex:   callIndex,
			resultIndex: resultIndex,
			class:       class,
			normalized:  normalized,
			raw:         raw,
			errorSig:    normalizedErrorSignature(toolResult),
			call:        call,
			result:      toolResult,
		})
	}
	return result
}

func matchingToolResult(
	turns []transcript.Turn,
	callIndex int,
	call transcript.Turn,
) (int, transcript.Turn, bool) {
	callID := strings.TrimSpace(call.Payload.ToolCallID)
	for index := callIndex + 1; index < len(turns) && index-callIndex <= 8; index++ {
		turn := turns[index]
		if turn.Role == transcript.RoleToolResult {
			resultID := strings.TrimSpace(turn.Payload.ToolCallID)
			callTool := strings.TrimSpace(call.ToolName)
			resultTool := strings.TrimSpace(turn.ToolName)
			if callID != "" && resultID != "" {
				if resultID == callID {
					return index, turn, true
				}
				continue
			}
			if callTool != "" && resultTool != "" &&
				!strings.EqualFold(callTool, resultTool) {
				continue
			}
			if callID == "" && resultID != "" {
				continue
			}
			if callID == "" || resultID == "" {
				return index, turn, true
			}
		}
		if turn.Role == transcript.RoleToolCall && callID == "" {
			break
		}
	}
	return 0, transcript.Turn{}, false
}

func permissionPattern(
	call transcript.Turn,
	config issueintel.ProjectConfig,
) string {
	tool := strings.ToLower(strings.TrimSpace(call.ToolName))
	if tool == "" {
		return ""
	}
	_, normalized, _, ok := commandInfo(call, config)
	if !ok {
		normalized = normalizeCommand(string(call.Payload.ToolInput))
	}
	if normalized == "" {
		normalized = editContentHash(call)
	}
	return tool + "\x00" + normalized
}

func permissionPatternLabel(pattern string) string {
	tool, _, _ := strings.Cut(pattern, "\x00")
	if tool == "" {
		return "the same tool pattern"
	}
	return tool
}

func turnSliceIndex(turns []transcript.Turn, turnID string) int {
	for index := range turns {
		if turns[index].TurnID == turnID {
			return index
		}
	}
	return 0
}

func editHashCycle(edits []fileEdit) bool {
	for index := 0; index+2 < len(edits); index++ {
		if edits[index].hash != "" &&
			edits[index].hash == edits[index+2].hash &&
			edits[index].hash != edits[index+1].hash {
			return true
		}
	}
	return false
}

func selectedTurnCost(edits []fileEdit) issueintel.Cost {
	result := issueintel.Cost{}
	usd := 0.0
	known := false
	for _, edit := range edits {
		result.WastedTokens += turnTokens(edit.turn)
		if edit.turn.CostUSD != nil {
			usd += *edit.turn.CostUSD
			known = true
		} else if turnTokens(edit.turn) > 0 {
			result.LowerBound = true
		}
	}
	if known {
		result.WastedUSD = &usd
	}
	return result
}
