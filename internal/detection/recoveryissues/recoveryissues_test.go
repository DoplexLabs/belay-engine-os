package recoveryissues

import (
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestAnalyzeProjectsQualifiedRecoveryWithTraceableCost(t *testing.T) {
	base := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	project := issueintel.Project{
		Identity: "project-recovery",
		Path:     "/project-recovery",
	}
	session := transcript.Session{
		SessionKey:      "ses_recovery",
		Agent:           "codex",
		ProjectIdentity: project.Identity,
		ProjectPath:     project.Path,
		StartedAt:       base,
		EndedAt:         base.Add(3 * time.Minute),
		Coverage:        transcript.CoverageComplete,
	}
	failureCall := recoveryTurn(
		session.SessionKey,
		0,
		base,
		transcript.RoleToolCall,
	)
	failureCall.Payload.ToolCallID = "call-failure"
	failureCall.Payload.RawCommand = "go test ./internal/pipeline"
	failureResult := recoveryTurn(
		session.SessionKey,
		1,
		base.Add(time.Minute),
		transcript.RoleToolResult,
	)
	failureResult.Payload.ToolCallID = "call-failure"
	failed := 1
	failureResult.Payload.ExitCode = &failed
	failureResult.Payload.ToolResult = "build cache: permission denied"
	successCall := recoveryTurn(
		session.SessionKey,
		2,
		base.Add(2*time.Minute),
		transcript.RoleToolCall,
	)
	successCall.Payload.ToolCallID = "call-success"
	successCall.Payload.RawCommand =
		"env GOCACHE=/tmp/belay-cache go test ./internal/pipeline"
	successResult := recoveryTurn(
		session.SessionKey,
		3,
		base.Add(3*time.Minute),
		transcript.RoleToolResult,
	)
	successResult.Payload.ToolCallID = "call-success"
	passed := 0
	successResult.Payload.ExitCode = &passed
	successResult.Payload.ToolResult = "ok"
	inputTokens := int64(25)
	cost := 0.1
	successCall.InputTokens = &inputTokens
	successCall.CostUSD = &cost
	turns := []transcript.Turn{
		failureCall,
		failureResult,
		successCall,
		successResult,
	}
	session.TurnCount = len(turns)

	episode, err := evidenceepisode.NewFailureRepair(
		evidenceepisode.FailureRepairInput{
			ProjectIdentity: project.Identity,
			Session:         session,
			SessionTurns:    turns,
			FailureCall:     failureCall,
			FailureResult:   failureResult,
			SuccessCall:     successCall,
			SuccessResult:   successResult,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	issues := Analyze(
		project,
		[]issueintel.Session{{Metadata: session, Turns: turns}},
		[]evidenceepisode.Episode{episode},
		base.Add(24*time.Hour),
	)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v", issues)
	}
	issue := issues[0]
	if issue.DetectorID != issueintel.DetectorFailureRepaired ||
		issue.SessionCount != 1 ||
		issue.Cost.WastedTokens != inputTokens ||
		issue.Cost.WastedUSD == nil ||
		*issue.Cost.WastedUSD != cost ||
		issue.Cost.WastedMinutes != 3 ||
		len(issue.Excerpts) != 4 ||
		len(issue.EpisodeRefs) != 1 ||
		issue.SuggestedFix.TargetFile != "AGENTS.md" {
		t.Fatalf("recovery issue = %+v", issue)
	}

	if got := Analyze(
		project,
		nil,
		[]evidenceepisode.Episode{episode},
		base,
	); len(got) != 0 {
		t.Fatalf("missing session produced issues = %+v", got)
	}
}

func recoveryTurn(
	sessionKey string,
	index int64,
	occurredAt time.Time,
	role transcript.Role,
) transcript.Turn {
	return transcript.Turn{
		TurnID:     "turn-" + string(role),
		SessionKey: sessionKey,
		TurnIndex:  index,
		OccurredAt: occurredAt,
		Role:       role,
		ToolName:   "exec_command",
		Payload: transcript.Payload{
			SourceFileID:    "source",
			JSONLByteOffset: index,
		},
	}
}
