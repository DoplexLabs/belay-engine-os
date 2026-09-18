package derive

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestMutationVerificationPositivePassUsesProjectConfig(t *testing.T) {
	base := time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC)
	session := fixtureSession("ses_verify_pass", transcript.CoverageComplete)
	zero := 0
	got, err := Session(Input{
		Session: session,
		Turns: []transcript.Turn{
			mutationFixtureTurn(session.SessionKey, 0, base, "edit-1", "internal/a.go"),
			resultFixtureTurn(session.SessionKey, 1, base.Add(time.Second), "edit-1", nil),
			commandFixtureTurn(session.SessionKey, 2, base.Add(2*time.Second), "verify-1", "make verify"),
			resultFixtureTurn(session.SessionKey, 3, base.Add(3*time.Second), "verify-1", &zero),
		},
		CanonicalEvents: []model.Event{
			fixtureEvent("evt_edit_pass", session.SessionKey, 1, base, "file.write", "edit-1"),
			fixtureEvent("evt_verify_pass", session.SessionKey, 2, base.Add(2*time.Second), "command.exec", "verify-1"),
		},
		ProjectConfig: issueintel.ProjectConfig{
			VerificationCommands: []string{"make verify"},
		},
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Coverage.FullyDerived || len(got.Diagnostics) != 0 {
		t.Fatalf("coverage/diagnostics = %+v/%+v", got.Coverage, got.Diagnostics)
	}
	if relationCounts(got.Edges)[trajectory.RelationModifies] != 1 ||
		relationCounts(got.Edges)[trajectory.RelationVerifies] != 1 {
		t.Fatalf("relations = %+v", relationCounts(got.Edges))
	}
	if len(got.Outcomes) != 1 ||
		got.Outcomes[0].Kind != trajectory.OutcomeVerificationPass ||
		got.Outcomes[0].Result != trajectory.ResultSucceeded {
		t.Fatalf("outcomes = %+v", got.Outcomes)
	}
	assertVerificationOutcomeEvidence(t, got.Outcomes[0], 2, 3)
	verifyEdge := edgeWithRelation(t, got.Edges, trajectory.RelationVerifies)
	if verifyEdge.EvidenceClass != trajectory.EvidenceDeterministicInference ||
		len(verifyEdge.SourceRefs) != 3 ||
		verifyEdge.From.SessionKey != session.SessionKey ||
		verifyEdge.To.EventID != "evt_edit_pass" {
		t.Fatalf("verification edge = %+v", verifyEdge)
	}
}

func TestMutationVerificationExplicitFail(t *testing.T) {
	base := time.Date(2026, 9, 10, 17, 10, 0, 0, time.UTC)
	session := fixtureSession("ses_verify_fail", transcript.CoverageComplete)
	exitCode := 2
	got, err := Session(Input{
		Session: session,
		Turns: []transcript.Turn{
			mutationFixtureTurn(session.SessionKey, 0, base, "edit-1", "internal/a.go"),
			commandFixtureTurn(session.SessionKey, 1, base.Add(time.Second), "verify-1", "go test ./..."),
			resultFixtureTurn(session.SessionKey, 2, base.Add(2*time.Second), "verify-1", &exitCode),
		},
		CanonicalEvents: []model.Event{
			fixtureEvent("evt_edit_fail", session.SessionKey, 1, base, "file.write", "edit-1"),
			fixtureEvent("evt_verify_fail", session.SessionKey, 2, base.Add(time.Second), "command.exec", "verify-1"),
		},
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Outcomes) != 1 ||
		got.Outcomes[0].Kind != trajectory.OutcomeVerificationFail ||
		got.Outcomes[0].Result != trajectory.ResultFailed {
		t.Fatalf("outcomes = %+v", got.Outcomes)
	}
	assertVerificationOutcomeEvidence(t, got.Outcomes[0], 1, 2)
}

func TestMutationVerificationRejectsOrdinaryCommand(t *testing.T) {
	base := time.Date(2026, 9, 10, 17, 20, 0, 0, time.UTC)
	session := fixtureSession("ses_verify_false_positive", transcript.CoverageComplete)
	zero := 0
	got, err := Session(Input{
		Session: session,
		Turns: []transcript.Turn{
			mutationFixtureTurn(session.SessionKey, 0, base, "edit-1", "internal/a.go"),
			commandFixtureTurn(session.SessionKey, 1, base.Add(time.Second), "inspect-1", "git status --short"),
			resultFixtureTurn(session.SessionKey, 2, base.Add(2*time.Second), "inspect-1", &zero),
		},
		CanonicalEvents: []model.Event{
			fixtureEvent("evt_edit_false", session.SessionKey, 1, base, "file.write", "edit-1"),
			fixtureEvent("evt_inspect_false", session.SessionKey, 2, base.Add(time.Second), "command.exec", "inspect-1"),
		},
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if relationCounts(got.Edges)[trajectory.RelationVerifies] != 0 ||
		len(got.Outcomes) != 0 {
		t.Fatalf("ordinary command produced verification evidence: %+v", got)
	}
}

func TestVerificationOutcomeRequiresSingleExplicitExitCode(t *testing.T) {
	base := time.Date(2026, 9, 10, 17, 30, 0, 0, time.UTC)
	tests := []struct {
		name       string
		results    []transcript.Turn
		diagnostic DiagnosticCode
	}{
		{
			name:       "missing result",
			diagnostic: DiagnosticMissingVerificationResult,
		},
		{
			name: "missing exit code",
			results: []transcript.Turn{
				resultFixtureTurn("ses_missing_exit_code", 2, base.Add(2*time.Second), "verify-1", nil),
			},
			diagnostic: DiagnosticUnknownVerificationResult,
		},
		{
			name: "ambiguous result",
			results: []transcript.Turn{
				resultFixtureTurn("ses_ambiguous_result", 2, base.Add(2*time.Second), "verify-1", intPointer(0)),
				resultFixtureTurn("ses_ambiguous_result", 3, base.Add(3*time.Second), "verify-1", intPointer(1)),
			},
			diagnostic: DiagnosticAmbiguousVerificationResult,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessionKey := "ses_" + compactFixtureName(test.name)
			session := fixtureSession(sessionKey, transcript.CoverageComplete)
			turns := []transcript.Turn{
				mutationFixtureTurn(sessionKey, 0, base, "edit-1", "internal/a.go"),
				commandFixtureTurn(sessionKey, 1, base.Add(time.Second), "verify-1", "go test ./..."),
			}
			for _, value := range test.results {
				value.SessionKey = sessionKey
				turns = append(turns, value)
			}
			got, err := Session(Input{
				Session: session,
				Turns:   turns,
				CanonicalEvents: []model.Event{
					fixtureEvent("evt_edit_"+compactFixtureName(test.name), sessionKey, 1, base, "file.write", "edit-1"),
					fixtureEvent("evt_verify_"+compactFixtureName(test.name), sessionKey, 2, base.Add(time.Second), "command.exec", "verify-1"),
				},
				TranscriptTurnsComplete: true,
				CanonicalEventsComplete: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Outcomes) != 0 ||
				relationCounts(got.Edges)[trajectory.RelationVerifies] != 1 ||
				!hasDiagnostic(got.Diagnostics, test.diagnostic) ||
				got.Coverage.FullyDerived {
				t.Fatalf("unknown verification result = %+v", got)
			}
		})
	}
}

func TestMutationVerificationFallsBackToExplicitTranscriptMutation(t *testing.T) {
	base := time.Date(2026, 9, 10, 17, 40, 0, 0, time.UTC)
	session := fixtureSession("ses_mutation_missing", transcript.CoverageComplete)
	notError := false
	result := resultFixtureTurn(
		session.SessionKey,
		2,
		base.Add(2*time.Second),
		"verify-1",
		nil,
	)
	result.Payload.ToolIsError = &notError
	got, err := Session(Input{
		Session: session,
		Turns: []transcript.Turn{
			mutationFixtureTurn(session.SessionKey, 0, base, "edit-1", "internal/a.go"),
			commandFixtureTurn(session.SessionKey, 1, base.Add(time.Second), "verify-1", "go test ./..."),
			result,
		},
		CanonicalEvents: []model.Event{
			fixtureEvent("evt_tool_only", session.SessionKey, 1, base, "tool.call", "edit-1"),
			{
				EventID:    "evt_file_without_id",
				OccurredAt: base,
				Source:     model.Source{Sequence: 2},
				Session:    model.SessionRef{Key: session.SessionKey},
				Observation: model.Observation{
					Type:     "file.write",
					Resource: &model.Resource{Kind: "file", Name: "internal/a.go"},
				},
			},
		},
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if relationCounts(got.Edges)[trajectory.RelationModifies] != 0 ||
		relationCounts(got.Edges)[trajectory.RelationVerifies] != 1 ||
		len(got.Outcomes) != 1 ||
		got.Outcomes[0].Kind != trajectory.OutcomeVerificationPass ||
		hasDiagnostic(got.Diagnostics, DiagnosticMissingMutation) {
		t.Fatalf("transcript mutation fallback = %+v", got)
	}
	verifyEdge := edgeWithRelation(t, got.Edges, trajectory.RelationVerifies)
	if verifyEdge.To.Kind != trajectory.NodeTranscriptTurn ||
		verifyEdge.To.TurnIndex == nil ||
		*verifyEdge.To.TurnIndex != 0 ||
		len(verifyEdge.SourceRefs) != 2 {
		t.Fatalf("transcript mutation verification edge = %+v", verifyEdge)
	}
}

func TestMutationVerificationScopesSharedIDsBySession(t *testing.T) {
	base := time.Date(2026, 9, 10, 17, 50, 0, 0, time.UTC)
	firstSession := fixtureSession("ses_c3_shared_a", transcript.CoverageComplete)
	secondSession := fixtureSession("ses_c3_shared_b", transcript.CoverageComplete)
	firstEvents := []model.Event{
		fixtureEvent("evt_c3_edit_a", firstSession.SessionKey, 1, base, "file.write", "shared-edit"),
		fixtureEvent("evt_c3_verify_a", firstSession.SessionKey, 2, base.Add(time.Second), "command.exec", "shared-verify"),
	}
	secondEvents := []model.Event{
		fixtureEvent("evt_c3_edit_b", secondSession.SessionKey, 1, base, "file.write", "shared-edit"),
		fixtureEvent("evt_c3_verify_b", secondSession.SessionKey, 2, base.Add(time.Second), "command.exec", "shared-verify"),
	}
	first := deriveSharedVerificationSession(t, firstSession, append(firstEvents, secondEvents...), base)
	second := deriveSharedVerificationSession(t, secondSession, append(firstEvents, secondEvents...), base)
	for _, test := range []struct {
		result     Result
		sessionKey string
		mutationID string
	}{
		{result: first, sessionKey: firstSession.SessionKey, mutationID: "evt_c3_edit_a"},
		{result: second, sessionKey: secondSession.SessionKey, mutationID: "evt_c3_edit_b"},
	} {
		for _, relation := range []trajectory.EdgeRelation{
			trajectory.RelationModifies,
			trajectory.RelationVerifies,
		} {
			edge := edgeWithRelation(t, test.result.Edges, relation)
			if edge.SessionKey != test.sessionKey ||
				edge.To.EventID != test.mutationID {
				t.Fatalf("cross-session %s edge = %+v", relation, edge)
			}
		}
	}
}

func deriveSharedVerificationSession(
	t *testing.T,
	session transcript.Session,
	events []model.Event,
	base time.Time,
) Result {
	t.Helper()
	zero := 0
	got, err := Session(Input{
		Session: session,
		Turns: []transcript.Turn{
			mutationFixtureTurn(session.SessionKey, 0, base, "shared-edit", "internal/shared.go"),
			commandFixtureTurn(session.SessionKey, 1, base.Add(time.Second), "shared-verify", "go test ./..."),
			resultFixtureTurn(session.SessionKey, 2, base.Add(2*time.Second), "shared-verify", &zero),
		},
		CanonicalEvents:         events,
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mutationFixtureTurn(
	sessionKey string,
	index int64,
	occurredAt time.Time,
	callID string,
	path string,
) transcript.Turn {
	input, _ := json.Marshal(map[string]string{"file_path": path})
	value := fixtureTurn(
		sessionKey,
		index,
		occurredAt,
		transcript.RoleToolCall,
		"",
		callID,
		"",
	)
	value.ToolName = "apply_patch"
	value.Payload.ToolInput = input
	return value
}

func commandFixtureTurn(
	sessionKey string,
	index int64,
	occurredAt time.Time,
	callID string,
	command string,
) transcript.Turn {
	value := fixtureTurn(
		sessionKey,
		index,
		occurredAt,
		transcript.RoleToolCall,
		"",
		callID,
		"",
	)
	value.ToolName = "exec_command"
	value.Payload.RawCommand = command
	return value
}

func resultFixtureTurn(
	sessionKey string,
	index int64,
	occurredAt time.Time,
	callID string,
	exitCode *int,
) transcript.Turn {
	value := fixtureTurn(
		sessionKey,
		index,
		occurredAt,
		transcript.RoleToolResult,
		"",
		callID,
		"",
	)
	value.Payload.ExitCode = exitCode
	return value
}

func edgeWithRelation(
	t *testing.T,
	edges []trajectory.Edge,
	relation trajectory.EdgeRelation,
) trajectory.Edge {
	t.Helper()
	var matches []trajectory.Edge
	for _, edge := range edges {
		if edge.Relation == relation {
			matches = append(matches, edge)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("%s edges = %+v", relation, matches)
	}
	return matches[0]
}

func compactFixtureName(value string) string {
	switch value {
	case "missing result":
		return "missing_result"
	case "missing exit code":
		return "missing_exit_code"
	case "ambiguous result":
		return "ambiguous_result"
	default:
		return value
	}
}

func intPointer(value int) *int {
	return &value
}

func assertVerificationOutcomeEvidence(
	t *testing.T,
	outcome trajectory.Outcome,
	callIndex int64,
	resultIndex int64,
) {
	t.Helper()
	if outcome.EvidenceClass != trajectory.EvidenceDeterministicInference ||
		outcome.Confidence != trajectory.ConfidenceHigh ||
		len(outcome.SourceRefs) != 2 ||
		outcome.SourceRefs[0].Kind != trajectory.NodeTranscriptTurn ||
		outcome.SourceRefs[0].TurnIndex == nil ||
		*outcome.SourceRefs[0].TurnIndex != callIndex ||
		outcome.SourceRefs[1].Kind != trajectory.NodeTranscriptTurn ||
		outcome.SourceRefs[1].TurnIndex == nil ||
		*outcome.SourceRefs[1].TurnIndex != resultIndex {
		t.Fatalf("verification outcome evidence = %+v", outcome)
	}
}
