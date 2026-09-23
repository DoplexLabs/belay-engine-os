package transcriptissues

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func sessionRef(session preparedSession) issueintel.SessionRef {
	return issueintel.SessionRef{
		SessionKey: session.metadata.SessionKey,
		Agent:      session.metadata.Agent,
		StartedAt:  session.metadata.StartedAt,
		EndedAt:    session.metadata.EndedAt,
	}
}

func turnTokens(turn transcript.Turn) int64 {
	var result int64
	if turn.InputTokens != nil {
		result += *turn.InputTokens
	}
	if turn.OutputTokens != nil {
		result += *turn.OutputTokens
	}
	if turn.CacheReadTokens != nil {
		result += *turn.CacheReadTokens
	}
	if turn.CacheWriteTokens != nil {
		result += *turn.CacheWriteTokens
	}
	return result
}

func windowSpan(session preparedSession, start, end int) costSpan {
	return costSpan{
		session: sessionRef(session),
		start:   start,
		end:     end,
	}
}

func associatedBillableIndex(turns []transcript.Turn, index int) int {
	if index < 0 || index >= len(turns) {
		return -1
	}
	if turnTokens(turns[index]) > 0 {
		return index
	}
	start, end := 0, len(turns)-1
	for candidate := index - 1; candidate >= 0; candidate-- {
		if turns[candidate].Role == transcript.RoleUser ||
			turns[candidate].Role == transcript.RoleCompactionSummary {
			start = candidate + 1
			break
		}
	}
	for candidate := index + 1; candidate < len(turns); candidate++ {
		if turns[candidate].Role == transcript.RoleUser ||
			turns[candidate].Role == transcript.RoleCompactionSummary {
			end = candidate - 1
			break
		}
	}
	for distance := 1; index-distance >= start || index+distance <= end; distance++ {
		before := index - distance
		if before >= start && turnTokens(turns[before]) > 0 {
			return before
		}
		after := index + distance
		if after <= end && turnTokens(turns[after]) > 0 {
			return after
		}
	}
	return -1
}

func windowCost(
	turns []transcript.Turn,
	start, end int,
) issueintel.Cost {
	if start < 0 {
		start = 0
	}
	if end >= len(turns) {
		end = len(turns) - 1
	}
	if start > end || len(turns) == 0 {
		return issueintel.Cost{LowerBound: true}
	}
	result := issueintel.Cost{}
	usd := 0.0
	usdKnown := false
	for index := start; index <= end; index++ {
		result.WastedTokens += turnTokens(turns[index])
		if turns[index].CostUSD != nil {
			usd += *turns[index].CostUSD
			usdKnown = true
		} else if turnTokens(turns[index]) > 0 {
			result.LowerBound = true
		}
	}
	result.WastedMinutes = activeMinutes(turns, start, end)
	if usdKnown {
		result.WastedUSD = &usd
	}
	return result
}

func activeMinutes(turns []transcript.Turn, start, end int) float64 {
	if start < 0 {
		start = 0
	}
	if end >= len(turns) {
		end = len(turns) - 1
	}
	if start >= end || len(turns) == 0 {
		return 0
	}
	var result time.Duration
	for index := start + 1; index <= end; index++ {
		left := turns[index-1].OccurredAt
		right := turns[index].OccurredAt
		if left.IsZero() || !right.After(left) {
			continue
		}
		gap := right.Sub(left)
		if gap <= maxActiveGap {
			result += gap
		}
	}
	return result.Minutes()
}

func attributedCost(
	project preparedProject,
	costSpans []costSpan,
	timeSpans []costSpan,
) issueintel.Cost {
	if len(costSpans) == 0 {
		zero := 0.0
		return issueintel.Cost{WastedUSD: &zero}
	}
	sessions := make(map[string]preparedSession, len(project.sessions))
	for _, session := range project.sessions {
		sessions[session.metadata.SessionKey] = session
	}
	indexesBySession := make(map[string]map[int]bool)
	for _, span := range costSpans {
		session, ok := sessions[span.session.SessionKey]
		if !ok || len(session.turns) == 0 {
			continue
		}
		start, end := span.start, span.end
		if start < 0 {
			start = 0
		}
		if end >= len(session.turns) {
			end = len(session.turns) - 1
		}
		if start > end {
			continue
		}
		indexes := indexesBySession[span.session.SessionKey]
		if indexes == nil {
			indexes = make(map[int]bool, end-start+1)
			indexesBySession[span.session.SessionKey] = indexes
		}
		for index := start; index <= end; index++ {
			indexes[index] = true
		}
	}

	result := issueintel.Cost{}
	usd := 0.0
	usdKnown := false
	for sessionKey, selected := range indexesBySession {
		session := sessions[sessionKey]
		indexes := make([]int, 0, len(selected))
		for index := range selected {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		for _, index := range indexes {
			turn := session.turns[index]
			result.WastedTokens += turnTokens(turn)
			if turn.CostUSD != nil {
				usd += *turn.CostUSD
				usdKnown = true
			} else if turnTokens(turn) > 0 {
				result.LowerBound = true
			}
		}
	}
	if len(timeSpans) == 0 {
		timeSpans = costSpans
	}
	result.WastedMinutes = attributedMinutes(project, timeSpans)
	if usdKnown {
		result.WastedUSD = &usd
	} else if !result.LowerBound {
		result.WastedUSD = &usd
	}
	return result
}

func attributedMinutes(
	project preparedProject,
	spans []costSpan,
) float64 {
	sessions := make(map[string]preparedSession, len(project.sessions))
	for _, session := range project.sessions {
		sessions[session.metadata.SessionKey] = session
	}
	type bounds struct {
		start int
		end   int
	}
	bySession := make(map[string][]bounds)
	for _, span := range spans {
		session, ok := sessions[span.session.SessionKey]
		if !ok || len(session.turns) == 0 {
			continue
		}
		start, end := span.start, span.end
		if start < 0 {
			start = 0
		}
		if end >= len(session.turns) {
			end = len(session.turns) - 1
		}
		if start <= end {
			bySession[span.session.SessionKey] = append(
				bySession[span.session.SessionKey],
				bounds{start: start, end: end},
			)
		}
	}
	var result float64
	for sessionKey, ranges := range bySession {
		sort.Slice(ranges, func(i, j int) bool {
			if ranges[i].start != ranges[j].start {
				return ranges[i].start < ranges[j].start
			}
			return ranges[i].end < ranges[j].end
		})
		session := sessions[sessionKey]
		current := ranges[0]
		for _, next := range ranges[1:] {
			if next.start <= current.end+1 {
				if next.end > current.end {
					current.end = next.end
				}
				continue
			}
			result += activeMinutes(session.turns, current.start, current.end)
			current = next
		}
		result += activeMinutes(session.turns, current.start, current.end)
	}
	return result
}

func excerptFromTurn(turn transcript.Turn) issueintel.Excerpt {
	text := strings.TrimSpace(turn.Payload.Text)
	if text == "" {
		text = strings.TrimSpace(turn.Payload.RawCommand)
	}
	if text == "" {
		text = strings.TrimSpace(turn.Payload.ToolResult)
	}
	if text == "" && len(turn.Payload.ToolInput) > 0 {
		var compact bytes.Buffer
		if json.Compact(&compact, turn.Payload.ToolInput) == nil {
			text = compact.String()
		} else {
			text = string(turn.Payload.ToolInput)
		}
	}
	return issueintel.Excerpt{
		Citation: issueintel.Citation{
			SessionKey:      turn.SessionKey,
			TurnIndex:       turn.TurnIndex,
			OccurredAt:      turn.OccurredAt,
			SourceFileID:    turn.Payload.SourceFileID,
			JSONLByteOffset: turn.Payload.JSONLByteOffset,
		},
		Role:     turn.Role,
		ToolName: turn.ToolName,
		Text:     text,
	}
}

func mergeCost(left, right issueintel.Cost) issueintel.Cost {
	result := issueintel.Cost{
		WastedMinutes: left.WastedMinutes + right.WastedMinutes,
		WastedTokens:  left.WastedTokens + right.WastedTokens,
		LowerBound:    left.LowerBound || right.LowerBound,
	}
	usd := 0.0
	known := false
	for _, value := range []*float64{left.WastedUSD, right.WastedUSD} {
		if value != nil {
			usd += *value
			known = true
		}
	}
	if known {
		result.WastedUSD = &usd
	}
	if left.WastedUSD == nil || right.WastedUSD == nil {
		result.LowerBound = true
	}
	return result
}

func observedBounds(turns ...transcript.Turn) (time.Time, time.Time) {
	var first, last time.Time
	for _, turn := range turns {
		if turn.OccurredAt.IsZero() {
			continue
		}
		if first.IsZero() || turn.OccurredAt.Before(first) {
			first = turn.OccurredAt
		}
		if last.IsZero() || turn.OccurredAt.After(last) {
			last = turn.OccurredAt
		}
	}
	return first, last
}

// instructionTarget names the file a project's standing instructions belong in,
// by which harness the project's sessions mostly come from. Cursor is counted
// with Codex here: Cursor reads AGENTS.md, and it has no CLAUDE.md-equivalent
// of its own, so a Cursor-heavy project should be steered to AGENTS.md rather
// than left to the fallback by accident.
func instructionTarget(project preparedProject) string {
	claude, agents := 0, 0
	for _, session := range project.sessions {
		switch session.metadata.Agent {
		case "claude-code":
			claude++
		case "codex", "cursor":
			agents++
		}
	}
	if claude > agents {
		return "CLAUDE.md"
	}
	return "AGENTS.md"
}
