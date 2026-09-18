package detection

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

var (
	errInvalidSession = errors.New("invalid detector session input")
	errSessionLimit   = errors.New("detector session event limit exceeded")
)

type preparedEvent struct {
	event       model.Event
	enrichment  EventEnrichment
	evidenceIDs []string
	historical  bool
	live        bool
}

type preparedSession struct {
	sessionID    string
	projectScope string
	scopeQuality string
	events       []preparedEvent
}

func prepareSession(ctx context.Context, input SessionInput) (preparedSession, error) {
	if ctx == nil || strings.TrimSpace(input.SessionID) == "" {
		return preparedSession{}, errInvalidSession
	}
	if len(input.Events) > MaxSessionEvents {
		return preparedSession{}, errSessionLimit
	}
	events := make([]preparedEvent, 0, len(input.Events))
	for index := range input.Events {
		if err := contextCheck(ctx, index); err != nil {
			return preparedSession{}, err
		}
		event := input.Events[index]
		if event.Session.Key != "" && event.Session.Key != input.SessionID {
			return preparedSession{}, errInvalidSession
		}
		events = append(events, preparedEvent{
			event:       event,
			enrichment:  input.Enrichments[event.EventID],
			evidenceIDs: []string{event.EventID},
			historical:  event.Historical.IsHistorical || event.Source.Kind == "artifact",
			live:        !event.Historical.IsHistorical && event.Source.Kind == "hook",
		})
	}
	sortPreparedEvents(events)
	events = coalesceEvents(ctx, events)
	return preparedSession{
		sessionID:    input.SessionID,
		projectScope: strings.TrimSpace(input.ProjectScope),
		scopeQuality: strings.ToLower(strings.TrimSpace(input.ScopeQuality)),
		events:       events,
	}, nil
}

func sortPreparedEvents(events []preparedEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		left := events[i].event
		right := events[j].event
		if left.Source.Sequence != right.Source.Sequence {
			return left.Source.Sequence < right.Source.Sequence
		}
		if !left.OccurredAt.Equal(right.OccurredAt) {
			return left.OccurredAt.Before(right.OccurredAt)
		}
		return left.EventID < right.EventID
	})
}

func (session preparedSession) publicInput() SessionInput {
	events := make([]model.Event, 0, len(session.events))
	enrichments := make(map[string]EventEnrichment, len(session.events))
	for _, item := range session.events {
		events = append(events, item.event)
		enrichments[item.event.EventID] = item.enrichment
	}
	return SessionInput{
		SessionID:    session.sessionID,
		ProjectScope: session.projectScope,
		ScopeQuality: session.scopeQuality,
		Events:       events,
		Enrichments:  enrichments,
	}
}

func contextCheck(ctx context.Context, index int) error {
	if index&63 != 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func eventToolCallID(event model.Event) string {
	if event.Observation.Details == nil {
		return ""
	}
	return strings.TrimSpace(event.Observation.Details.ToolCallID)
}

func eventTimeBounds(events []preparedEvent) (time.Time, time.Time) {
	if len(events) == 0 {
		return time.Time{}, time.Time{}
	}
	first := events[0].event.OccurredAt
	last := first
	for _, item := range events[1:] {
		if item.event.OccurredAt.Before(first) {
			first = item.event.OccurredAt
		}
		if item.event.OccurredAt.After(last) {
			last = item.event.OccurredAt
		}
	}
	return first, last
}

func scopeDimensions(session preparedSession) []FingerprintDimension {
	if session.projectScope != "" &&
		session.scopeQuality != "unscoped" &&
		session.scopeQuality != "conflict" {
		return []FingerprintDimension{{
			Name:  "project_scope_id",
			Value: session.projectScope,
		}}
	}
	return []FingerprintDimension{{
		Name:  "session_id",
		Value: session.sessionID,
	}}
}

func dimensions(session preparedSession, values ...FingerprintDimension) []FingerprintDimension {
	result := append([]FingerprintDimension(nil), scopeDimensions(session)...)
	result = append(result, values...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].Value < result[j].Value
	})
	return result
}

func citationIDs(events ...preparedEvent) ([]string, bool) {
	seen := make(map[string]struct{})
	var result []string
	complete := true
	for _, item := range events {
		ids := append([]string(nil), item.evidenceIDs...)
		sort.Strings(ids)
		for _, eventID := range ids {
			if eventID == "" {
				continue
			}
			if _, ok := seen[eventID]; ok {
				continue
			}
			seen[eventID] = struct{}{}
			if len(result) == MaxCitations {
				complete = false
				continue
			}
			result = append(result, eventID)
		}
	}
	return result, complete
}

func isVerificationClass(value string) bool {
	switch value {
	case CommandClassTest,
		CommandClassBuild,
		CommandClassTypecheck,
		CommandClassLint,
		CommandClassFormatCheck:
		return true
	default:
		return false
	}
}
