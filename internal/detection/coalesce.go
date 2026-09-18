package detection

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const (
	coalesceWindow   = time.Second
	maxCoalesceGroup = 256
)

type coalesceCandidate struct {
	index            int
	sourceKind       string
	toolCallID       string
	commandSignature string
	at               time.Time
}

func coalesceEvents(ctx context.Context, events []preparedEvent) []preparedEvent {
	groups := make(map[string][]coalesceCandidate)
	for index, item := range events {
		if contextCheck(ctx, index) != nil {
			return events
		}
		identity := coalesceIdentity(item)
		sourceKind := coalesceSource(item)
		if identity == "" || sourceKind == "" {
			continue
		}
		groups[identity] = append(groups[identity], coalesceCandidate{
			index:            index,
			sourceKind:       sourceKind,
			toolCallID:       eventToolCallID(item.event),
			commandSignature: item.enrichment.CommandSignatureID,
			at:               item.event.OccurredAt,
		})
	}

	paired := make(map[int]int)
	for _, candidates := range groups {
		if len(candidates) > maxCoalesceGroup {
			continue
		}
		for _, candidate := range candidates {
			if _, ok := paired[candidate.index]; ok {
				continue
			}
			matches := uniqueOppositeCandidates(candidate, candidates)
			if len(matches) != 1 {
				continue
			}
			opposite := matches[0]
			if len(uniqueOppositeCandidates(opposite, candidates)) != 1 {
				continue
			}
			paired[candidate.index] = opposite.index
			paired[opposite.index] = candidate.index
		}
	}

	result := make([]preparedEvent, 0, len(events))
	consumed := make(map[int]bool)
	for index, item := range events {
		if consumed[index] {
			continue
		}
		opposite, ok := paired[index]
		if !ok {
			result = append(result, item)
			continue
		}
		consumed[index] = true
		consumed[opposite] = true
		result = append(result, mergeCoalesced(item, events[opposite]))
	}
	sortPreparedEvents(result)
	return result
}

func uniqueOppositeCandidates(
	candidate coalesceCandidate,
	candidates []coalesceCandidate,
) []coalesceCandidate {
	result := make([]coalesceCandidate, 0, 2)
	for _, other := range candidates {
		if other.index == candidate.index || other.sourceKind == candidate.sourceKind {
			continue
		}
		if durationAbsolute(other.at.Sub(candidate.at)) > coalesceWindow {
			continue
		}
		if candidate.toolCallID != "" &&
			other.toolCallID != "" &&
			candidate.toolCallID != other.toolCallID {
			continue
		}
		if candidate.commandSignature != "" &&
			other.commandSignature != "" &&
			candidate.commandSignature != other.commandSignature {
			continue
		}
		result = append(result, other)
		if len(result) > 1 {
			return result
		}
	}
	return result
}

func coalesceSource(item preparedEvent) string {
	switch item.event.Source.Kind {
	case "artifact":
		return "historical"
	case "hook":
		return "live"
	default:
		return ""
	}
}

func coalesceIdentity(item preparedEvent) string {
	event := item.event
	base := strings.Join([]string{
		event.Session.Key,
		event.Observation.Type,
		event.Observation.Actor,
		strings.ToLower(strings.TrimSpace(event.Observation.Outcome)),
		coalesceExitCode(event),
	}, "\x00")
	switch event.Observation.Type {
	case "command.exec":
		if item.enrichment.CommandSignatureID == "" {
			return ""
		}
		return base + "\x00command\x00" + item.enrichment.CommandSignatureID
	case "command.result":
		if toolCallID := eventToolCallID(event); toolCallID != "" {
			return base + "\x00command-result-tool-call\x00" + toolCallID
		}
		if item.enrichment.CommandSignatureID == "" {
			return ""
		}
		return base + "\x00command\x00" + item.enrichment.CommandSignatureID
	case "permission.requested", "permission.approved", "permission.denied":
		if item.enrichment.PermissionClass == "" {
			return ""
		}
		return base + "\x00permission\x00" + item.enrichment.PermissionClass
	case "file.read", "file.write", "file.delete":
		resource := event.Observation.Resource
		if resource == nil || resource.Kind == "" || resource.Name == "" {
			return ""
		}
		return base + "\x00resource\x00" + resource.Kind + "\x00" + resource.Name
	case "tool.result":
		identity := safeToolIdentity(event)
		if identity == "" {
			return ""
		}
		return base + "\x00tool-result\x00" + identity
	default:
		return ""
	}
}

func mergeCoalesced(left, right preparedEvent) preparedEvent {
	retained := left
	other := right
	if coverageRank(right) > coverageRank(left) {
		retained = right
		other = left
	}
	retained.evidenceIDs = append(
		append([]string(nil), retained.evidenceIDs...),
		other.evidenceIDs...,
	)
	sort.Strings(retained.evidenceIDs)
	retained.evidenceIDs = compactStrings(retained.evidenceIDs)
	retained.enrichment = mergeEnrichment(retained.enrichment, other.enrichment)
	retained.historical = retained.historical || other.historical
	retained.live = retained.live || other.live
	return retained
}

func coalesceExitCode(item model.Event) string {
	if item.Observation.ExitCode == nil {
		return "exit:none"
	}
	return "exit:" + strconv.Itoa(*item.Observation.ExitCode)
}

func coverageRank(item preparedEvent) int {
	depth := 0
	switch item.event.Coverage.Depth {
	case "full":
		depth = 50
	case "tool_call":
		depth = 40
	case "lifecycle":
		depth = 30
	case "otlp":
		depth = 20
	case "artifact":
		depth = 10
	}
	confidence := 0
	switch item.event.Coverage.Confidence {
	case ConfidenceHigh:
		confidence = 3
	case ConfidenceMedium:
		confidence = 2
	case ConfidenceLow:
		confidence = 1
	}
	return depth + confidence
}

func mergeEnrichment(primary, secondary EventEnrichment) EventEnrichment {
	if primary.CommandSignatureID == "" {
		primary.CommandSignatureID = secondary.CommandSignatureID
	}
	if primary.CommandClass == "" {
		primary.CommandClass = secondary.CommandClass
	}
	if primary.PermissionClass == "" {
		primary.PermissionClass = secondary.PermissionClass
	}
	if primary.Version == "" {
		primary.Version = secondary.Version
	}
	return primary
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func durationAbsolute(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
