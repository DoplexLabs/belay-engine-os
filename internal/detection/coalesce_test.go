package detection

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestHistoricalLiveCoalescingPreservesAllEvidence(t *testing.T) {
	hook := withOutcome(testEvent("hook", 1, "command.result"), "failed", intPointer(1))
	hook = withToolCall(hook, "call-1")
	artifact := hook
	artifact.EventID = "artifact"
	artifact.Source.Kind = "artifact"
	artifact.Source.Sequence = 2
	artifact.Historical.IsHistorical = true
	artifact.Coverage.Depth = "artifact"
	artifact.Coverage.Confidence = ConfidenceMedium
	input := testInput(artifact, hook)
	enrichCommand(&input, "artifact", "sig-a", CommandClassOther)
	enrichCommand(&input, "hook", "sig-a", CommandClassOther)

	session, err := prepareSession(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.events) != 1 {
		t.Fatalf("coalesced events = %d, want 1", len(session.events))
	}
	if session.events[0].event.EventID != "hook" {
		t.Fatalf("retained event = %q, want higher-coverage hook", session.events[0].event.EventID)
	}
	if got := session.events[0].evidenceIDs; len(got) != 2 ||
		got[0] != "artifact" ||
		got[1] != "hook" {
		t.Fatalf("evidence IDs = %v", got)
	}

	result := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, result)
	match, ok := findMatch(t, result, "explicit_command_failure")
	if !ok || len(match.CitedEventIDs) != 2 {
		t.Fatalf("coalesced match = %+v, present = %v", match, ok)
	}
}

func TestHistoricalLiveCoalescingCannotInflateRepetitionThreshold(t *testing.T) {
	for _, actualAttempts := range []int{2, 4} {
		t.Run(fmt.Sprintf("%d actual attempts", actualAttempts), func(t *testing.T) {
			var events []model.Event
			input := testInput()
			for index := 0; index < actualAttempts; index++ {
				at := testEpoch.Add(time.Duration(index*3) * time.Second)
				hookID := fmt.Sprintf("hook-%d", index)
				artifactID := fmt.Sprintf("artifact-%d", index)
				hook := withToolCall(
					testEvent(hookID, int64(index*2+1), "command.exec"),
					fmt.Sprintf("call-%d", index),
				)
				hook.OccurredAt = at
				artifact := hook
				artifact.EventID = artifactID
				artifact.Source.Kind = "artifact"
				artifact.Source.Sequence = int64(index*2 + 2)
				artifact.Historical.IsHistorical = true
				artifact.Coverage.Depth = "artifact"
				events = append(events, hook, artifact)
				enrichCommand(&input, hookID, "sig-repeat", CommandClassOther)
				enrichCommand(&input, artifactID, "sig-repeat", CommandClassOther)
			}
			input.Events = events
			result := DefaultCatalog().Run(context.Background(), input)
			requireCurrent(t, result)
			_, ok := findMatch(t, result, "repeated_command_attempts")
			if ok != (actualAttempts == 4) {
				t.Fatalf("repetition present = %v for %d physical attempts; matches = %+v",
					ok, actualAttempts, result.Matches)
			}
		})
	}
}

func TestHistoricalLiveToolFailuresCoalesceWithoutInflatingSeverity(t *testing.T) {
	var events []model.Event
	for index := 0; index < 2; index++ {
		hook := withToolResource(
			withOutcome(
				testEvent(fmt.Sprintf("hook-%d", index), int64(index*2+1), "tool.result"),
				"failed",
				nil,
			),
			"read_file",
		)
		artifact := withHistorical(hook)
		artifact.EventID = fmt.Sprintf("artifact-%d", index)
		artifact.Source.Sequence = int64(index*2 + 2)
		events = append(events, artifact, hook)
	}

	result := DefaultCatalog().Run(context.Background(), testInput(events...))
	requireCurrent(t, result)
	match, ok := findMatch(t, result, "explicit_tool_failure")
	if !ok ||
		match.Severity != SeverityLow ||
		len(match.CitedEventIDs) != 4 {
		t.Fatalf("coalesced tool failure = %+v, present = %v", match, ok)
	}
}

func TestHistoricalLiveUnsignedResultsCoalesceAndPair(t *testing.T) {
	hookExec := withToolCall(testEvent("hook-exec", 1, "command.exec"), "call-1")
	artifactExec := hookExec
	artifactExec.EventID = "artifact-exec"
	artifactExec.Source.Kind = "artifact"
	artifactExec.Source.Sequence = 2
	artifactExec.Historical.IsHistorical = true
	artifactExec.Coverage.Depth = "artifact"

	hookResult := withToolCall(
		withOutcome(testEvent("hook-result", 3, "command.result"), "failed", intPointer(1)),
		"call-1",
	)
	artifactResult := hookResult
	artifactResult.EventID = "artifact-result"
	artifactResult.Source.Kind = "artifact"
	artifactResult.Source.Sequence = 4
	artifactResult.Historical.IsHistorical = true
	artifactResult.Coverage.Depth = "artifact"

	terminal := testEvent("terminal", 5, "session.end")
	terminal.Coverage.Depth = "lifecycle"
	input := testInput(
		artifactExec,
		hookExec,
		artifactResult,
		hookResult,
		terminal,
	)
	enrichCommand(&input, "artifact-exec", "sig-test", CommandClassTest)
	enrichCommand(&input, "hook-exec", "sig-test", CommandClassTest)

	session, err := prepareSession(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.events) != 3 {
		t.Fatalf("coalesced events = %d, want exec/result/terminal", len(session.events))
	}
	attempts, err := commandAttempts(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 ||
		attempts[0].trust != trustToolCallPair ||
		attempts[0].outcome != outcomeFailed ||
		len(attempts[0].events) != 2 {
		t.Fatalf("coalesced command attempt = %+v", attempts)
	}

	result := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, result)
	d1, ok := findMatch(t, result, "explicit_command_failure")
	if !ok || d1.Confidence != ConfidenceHigh || len(d1.CitedEventIDs) != 4 {
		t.Fatalf("coalesced D1 = %+v, present = %v", d1, ok)
	}
	d5, ok := findMatch(
		t,
		result,
		"unresolved_verification_failure_at_completion",
	)
	if !ok || d5.Confidence != ConfidenceHigh || len(d5.CitedEventIDs) != 5 {
		t.Fatalf("coalesced D5 = %+v, present = %v", d5, ok)
	}
}

func TestAmbiguousUnsignedResultDuplicatesRemainSeparate(t *testing.T) {
	exec := withToolCall(testEvent("exec", 1, "command.exec"), "call-reused")
	artifactResult, hookResult := historicalLivePair("command.result")
	artifactResult = withToolCall(
		withOutcome(artifactResult, "failed", intPointer(1)),
		"call-reused",
	)
	hookResult = withToolCall(
		withOutcome(hookResult, "failed", intPointer(1)),
		"call-reused",
	)
	secondHookResult := hookResult
	secondHookResult.EventID = "hook-result-2"
	secondHookResult.Source.Sequence = 3

	input := testInput(exec, artifactResult, hookResult, secondHookResult)
	enrichCommand(&input, "exec", "sig-test", CommandClassTest)
	session, err := prepareSession(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.events) != 4 {
		t.Fatalf("ambiguous unsigned results coalesced: %+v", session.events)
	}
	attempts, err := commandAttempts(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 ||
		attempts[0].trust != trustExecObservation ||
		attempts[0].outcome != outcomeUnknown {
		t.Fatalf("ambiguous results paired with exec: %+v", attempts)
	}

	result := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, result)
	if _, ok := findMatch(t, result, "explicit_command_failure"); ok {
		t.Fatalf("ambiguous unsigned results produced D1: %+v", result.Matches)
	}
}

func TestCoalescingRequiresExactNonEmptyIdentity(t *testing.T) {
	tests := []struct {
		name   string
		events func() (model.Event, model.Event)
		enrich func(*SessionInput)
	}{
		{
			name: "missing command signature",
			events: func() (model.Event, model.Event) {
				return historicalLivePair("command.exec")
			},
			enrich: func(*SessionInput) {},
		},
		{
			name: "different command signature",
			events: func() (model.Event, model.Event) {
				return historicalLivePair("command.exec")
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "artifact", "sig-a", CommandClassOther)
				enrichCommand(input, "hook", "sig-b", CommandClassOther)
			},
		},
		{
			name: "missing permission class",
			events: func() (model.Event, model.Event) {
				return historicalLivePair("permission.denied")
			},
			enrich: func(*SessionInput) {},
		},
		{
			name: "different canonical resource",
			events: func() (model.Event, model.Event) {
				artifact, hook := historicalLivePair("file.write")
				artifact.Observation.Resource = &model.Resource{Kind: "file", Name: "resource-a"}
				hook.Observation.Resource = &model.Resource{Kind: "file", Name: "resource-b"}
				return artifact, hook
			},
			enrich: func(*SessionInput) {},
		},
		{
			name: "different actor",
			events: func() (model.Event, model.Event) {
				artifact, hook := historicalLivePair("command.exec")
				hook.Observation.Actor = "user"
				return artifact, hook
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "artifact", "sig-a", CommandClassOther)
				enrichCommand(input, "hook", "sig-a", CommandClassOther)
			},
		},
		{
			name: "different explicit outcome",
			events: func() (model.Event, model.Event) {
				artifact, hook := historicalLivePair("command.result")
				artifact.Observation.Outcome = "failed"
				hook.Observation.Outcome = "succeeded"
				return artifact, hook
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "artifact", "sig-a", CommandClassOther)
				enrichCommand(input, "hook", "sig-a", CommandClassOther)
			},
		},
		{
			name: "different exit code",
			events: func() (model.Event, model.Event) {
				artifact, hook := historicalLivePair("command.result")
				artifact.Observation.ExitCode = intPointer(1)
				hook.Observation.ExitCode = intPointer(2)
				return artifact, hook
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "artifact", "sig-a", CommandClassOther)
				enrichCommand(input, "hook", "sig-a", CommandClassOther)
			},
		},
		{
			name: "different tool call IDs",
			events: func() (model.Event, model.Event) {
				artifact, hook := historicalLivePair("command.exec")
				artifact = withToolCall(artifact, "call-a")
				hook = withToolCall(hook, "call-b")
				return artifact, hook
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "artifact", "sig-a", CommandClassOther)
				enrichCommand(input, "hook", "sig-a", CommandClassOther)
			},
		},
		{
			name: "outside one second window",
			events: func() (model.Event, model.Event) {
				artifact, hook := historicalLivePair("command.exec")
				hook.OccurredAt = artifact.OccurredAt.Add(time.Second + time.Nanosecond)
				return artifact, hook
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "artifact", "sig-a", CommandClassOther)
				enrichCommand(input, "hook", "sig-a", CommandClassOther)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			artifact, hook := test.events()
			input := testInput(artifact, hook)
			test.enrich(&input)
			session, err := prepareSession(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if len(session.events) != 2 {
				t.Fatalf("events coalesced without exact identity: %+v", session.events)
			}
		})
	}
}

func TestAmbiguousCoalescingGroupRemainsSeparate(t *testing.T) {
	artifact, hookA := historicalLivePair("command.exec")
	hookB := hookA
	hookB.EventID = "hook-b"
	hookB.Source.Sequence = 3
	input := testInput(artifact, hookA, hookB)
	for _, id := range []string{"artifact", "hook", "hook-b"} {
		enrichCommand(&input, id, "sig-a", CommandClassOther)
	}
	session, err := prepareSession(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.events) != 3 {
		t.Fatalf("ambiguous observations were coalesced: %+v", session.events)
	}
}

func historicalLivePair(eventType string) (model.Event, model.Event) {
	hook := testEvent("hook", 1, eventType)
	artifact := hook
	artifact.EventID = "artifact"
	artifact.Source.Kind = "artifact"
	artifact.Source.Sequence = 2
	artifact.Historical.IsHistorical = true
	artifact.Coverage.Depth = "artifact"
	return artifact, hook
}
