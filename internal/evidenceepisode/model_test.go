package evidenceepisode

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestNewFailureRepairIsDeterministicAndRejectsSameCommand(t *testing.T) {
	base := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	session := transcript.Session{
		SessionKey:      "ses_episode",
		ProjectIdentity: "project",
		Coverage:        transcript.CoverageComplete,
	}
	failureCall := episodeTurn("ses_episode", 1, base, transcript.RoleToolCall)
	failureCall.Payload.ToolCallID = "call-failed"
	failureCall.Payload.RawCommand = "go test ./internal/pipeline"
	failureResult := episodeTurn("ses_episode", 2, base.Add(time.Minute), transcript.RoleToolResult)
	failureResult.Payload.ToolCallID = "call-failed"
	failed := 1
	failureResult.Payload.ExitCode = &failed
	failureResult.Payload.ToolResult = "build cache: permission denied"
	successCall := episodeTurn("ses_episode", 3, base.Add(2*time.Minute), transcript.RoleToolCall)
	successCall.Payload.ToolCallID = "call-success"
	successCall.Payload.RawCommand = "env GOCACHE=/tmp/cache go test ./internal/pipeline"
	successResult := episodeTurn("ses_episode", 4, base.Add(3*time.Minute), transcript.RoleToolResult)
	successResult.Payload.ToolCallID = "call-success"
	passed := 0
	successResult.Payload.ExitCode = &passed
	input := FailureRepairInput{
		ProjectIdentity: "project",
		Session:         session,
		SessionTurns:    []transcript.Turn{failureCall, failureResult, successCall, successResult},
		FailureCall:     failureCall,
		FailureResult:   failureResult,
		SuccessCall:     successCall,
		SuccessResult:   successResult,
		OutcomeRefs:     []string{"out_repair"},
	}
	first, err := NewFailureRepair(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFailureRepair(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.EpisodeID != second.EpisodeID ||
		first.InputHash != second.InputHash ||
		first.Cost.WastedMinutes != 3 {
		t.Fatalf("episodes differ: %+v %+v", first, second)
	}

	input.SuccessCall = failureCall
	input.SuccessCall.TurnIndex = 3
	input.SuccessCall.OccurredAt = base.Add(2 * time.Minute)
	if _, err := NewFailureRepair(input); err == nil {
		t.Fatal("same failed and successful command qualified as a repair")
	}
}

func TestNewMutationVerificationRequiresEditBeforeExplicitSuccess(t *testing.T) {
	base := time.Date(2026, 9, 18, 13, 0, 0, 0, time.UTC)
	session := transcript.Session{
		SessionKey:      "ses_mutation_verification",
		ProjectIdentity: "project",
		Coverage:        transcript.CoverageComplete,
	}
	edit := episodeTurn(session.SessionKey, 1, base, transcript.RoleToolCall)
	edit.ToolName = "Edit"
	edit.Payload.ToolInput = json.RawMessage(
		`{"file_path":"internal/pipeline/importer.go"}`,
	)
	verify := episodeTurn(
		session.SessionKey,
		2,
		base.Add(time.Minute),
		transcript.RoleToolCall,
	)
	verify.Payload.ToolCallID = "verify-call"
	verify.Payload.RawCommand = "go test ./internal/pipeline"
	result := episodeTurn(
		session.SessionKey,
		3,
		base.Add(2*time.Minute),
		transcript.RoleToolResult,
	)
	result.Payload.ToolCallID = "verify-call"
	notError := false
	result.Payload.ToolIsError = &notError
	ref := func(index int64) trajectory.NodeRef {
		value := index
		return trajectory.NodeRef{
			Kind:       trajectory.NodeTranscriptTurn,
			SessionKey: session.SessionKey,
			TurnIndex:  &value,
		}
	}
	input := MutationVerificationInput{
		ProjectIdentity:       session.ProjectIdentity,
		Session:               session,
		SessionTurns:          []transcript.Turn{result, edit, verify},
		MutationRefs:          []trajectory.NodeRef{ref(1)},
		SourceRefs:            []trajectory.NodeRef{ref(1), ref(2), ref(3)},
		VerificationCallRef:   ref(2),
		VerificationResultRef: ref(3),
		OutcomeRefs:           []string{"out_verification"},
		VerifierCommand:       verify.Payload.RawCommand,
		VerifierCommandClass:  "go test",
	}
	first, err := NewMutationVerification(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewMutationVerification(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.EpisodeID != second.EpisodeID ||
		first.Kind != KindMutationVerification ||
		first.FirstTurn != 1 ||
		first.LastTurn != 3 {
		t.Fatalf("mutation-verification episodes differ: %+v %+v", first, second)
	}

	input.MutationRefs = []trajectory.NodeRef{ref(2)}
	if _, err := NewMutationVerification(input); err == nil {
		t.Fatal("verification command was accepted as mutation evidence")
	}
}

func episodeTurn(
	sessionKey string,
	index int64,
	occurredAt time.Time,
	role transcript.Role,
) transcript.Turn {
	return transcript.Turn{
		TurnID:     "turn",
		SessionKey: sessionKey,
		TurnIndex:  index,
		OccurredAt: occurredAt,
		Role:       role,
		Payload: transcript.Payload{
			ToolCallID: "call",
		},
	}
}
