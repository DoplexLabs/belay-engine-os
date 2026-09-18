package detection

import (
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

var testEpoch = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func testEvent(id string, sequence int64, eventType string) model.Event {
	return model.Event{
		SchemaVersion: model.EventSchemaVersion,
		EventID:       id,
		OccurredAt:    testEpoch.Add(time.Duration(sequence) * time.Second),
		ObservedAt:    testEpoch.Add(time.Duration(sequence) * time.Second),
		Source: model.Source{
			Kind:     "hook",
			Agent:    "codex",
			Sequence: sequence,
		},
		Session: model.SessionRef{Key: "session-1"},
		Observation: model.Observation{
			Type:  eventType,
			Actor: "agent",
		},
		Coverage: model.Coverage{
			Depth:      "tool_call",
			Confidence: ConfidenceHigh,
		},
	}
}

func withOutcome(event model.Event, outcome string, exitCode *int) model.Event {
	event.Observation.Outcome = outcome
	event.Observation.ExitCode = exitCode
	return event
}

func withToolCall(event model.Event, toolCallID string) model.Event {
	event.Observation.Details = &model.Details{ToolCallID: toolCallID}
	return event
}

func testInput(events ...model.Event) SessionInput {
	return SessionInput{
		SessionID:    "session-1",
		ProjectScope: "project-1",
		ScopeQuality: "exact",
		Events:       events,
		Enrichments:  make(map[string]EventEnrichment),
	}
}

func enrichCommand(
	input *SessionInput,
	eventID string,
	signature string,
	class string,
) {
	input.Enrichments[eventID] = EventEnrichment{
		CommandSignatureID: signature,
		CommandClass:       class,
		Version:            "1",
	}
}

func findMatch(t *testing.T, result CatalogResult, detectorID string) (Match, bool) {
	t.Helper()
	for _, match := range result.Matches {
		if match.DetectorID == detectorID {
			return match, true
		}
	}
	return Match{}, false
}

func requireCurrent(t *testing.T, result CatalogResult) {
	t.Helper()
	if result.Status != StatusCurrent {
		t.Fatalf("status = %q, failures = %+v", result.Status, result.Failures)
	}
}

func intPointer(value int) *int {
	return &value
}
