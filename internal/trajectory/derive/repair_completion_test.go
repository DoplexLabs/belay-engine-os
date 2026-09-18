package derive

import (
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestRepairRequiresExplicitFailedThenSuccessfulAlternateCommand(
	t *testing.T,
) {
	base := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	session := fixtureSession("ses_repair_positive", transcript.CoverageComplete)
	failed := 3
	succeeded := 0
	got, err := Session(Input{
		Session: session,
		Turns: []transcript.Turn{
			commandFixtureTurn(session.SessionKey, 0, base, "failed-call", "go test -v ./internal/x"),
			func() transcript.Turn {
				turn := resultFixtureTurn(session.SessionKey, 1, base.Add(time.Second), "failed-call", &failed)
				turn.Payload.ToolResult = "failed to initialize build cache: permission denied"
				return turn
			}(),
			commandFixtureTurn(session.SessionKey, 2, base.Add(2*time.Second), "success-call", "env GOCACHE=/tmp/belay-cache go test -v ./internal/x"),
			resultFixtureTurn(session.SessionKey, 3, base.Add(3*time.Second), "success-call", &succeeded),
		},
		CanonicalEvents: []model.Event{
			fixtureEvent("evt_repair_failed", session.SessionKey, 1, base, "command.exec", "failed-call"),
			fixtureEvent("evt_repair_success", session.SessionKey, 2, base.Add(2*time.Second), "command.exec", "success-call"),
		},
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	repairEdge := edgeWithRelation(t, got.Edges, trajectory.RelationRepairs)
	if repairEdge.EvidenceClass != trajectory.EvidenceDeterministicInference ||
		repairEdge.From.TurnIndex == nil ||
		*repairEdge.From.TurnIndex != 2 ||
		repairEdge.To.TurnIndex == nil ||
		*repairEdge.To.TurnIndex != 0 {
		t.Fatalf("repair edge = %+v", repairEdge)
	}
	assertFourTurnRefs(t, repairEdge.SourceRefs, []int64{0, 1, 2, 3})
	var repairs []trajectory.Outcome
	for _, outcome := range got.Outcomes {
		if outcome.Kind == trajectory.OutcomeRepair {
			repairs = append(repairs, outcome)
		}
	}
	if len(repairs) != 1 ||
		repairs[0].Result != trajectory.ResultSucceeded ||
		repairs[0].EvidenceClass != trajectory.EvidenceDeterministicInference ||
		repairs[0].Confidence != trajectory.ConfidenceHigh {
		t.Fatalf("repair outcomes = %+v", repairs)
	}
	assertFourTurnRefs(t, repairs[0].SourceRefs, []int64{0, 1, 2, 3})
	if relationCounts(got.Edges)[trajectory.RelationReverts] != 0 {
		t.Fatalf("unexpected revert edge = %+v", got.Edges)
	}
}

func TestRepairFalsePositiveBoundaries(t *testing.T) {
	base := time.Date(2026, 9, 10, 18, 10, 0, 0, time.UTC)
	failed := 1
	succeeded := 0
	tests := []struct {
		name  string
		turns func(string) []transcript.Turn
	}{
		{
			name: "same normalized command retried",
			turns: func(sessionKey string) []transcript.Turn {
				return []transcript.Turn{
					commandFixtureTurn(sessionKey, 0, base, "failed-call", "run-task 123 --port 4312 /tmp/run-a"),
					resultFixtureTurn(sessionKey, 1, base.Add(time.Second), "failed-call", &failed),
					commandFixtureTurn(sessionKey, 2, base.Add(2*time.Second), "success-call", "run-task 456 --port 9921 /private/tmp/run-b"),
					resultFixtureTurn(sessionKey, 3, base.Add(3*time.Second), "success-call", &succeeded),
				}
			},
		},
		{
			name: "unrelated compound command succeeds",
			turns: func(sessionKey string) []transcript.Turn {
				return []transcript.Turn{
					commandFixtureTurn(sessionKey, 0, base, "failed-call", "rg -n missing-pattern internal"),
					resultFixtureTurn(sessionKey, 1, base.Add(time.Second), "failed-call", &failed),
					commandFixtureTurn(
						sessionKey,
						2,
						base.Add(2*time.Second),
						"success-call",
						"wc -l report.txt && awk '{print $1}' report.txt && rg TODO .",
					),
					resultFixtureTurn(sessionKey, 3, base.Add(3*time.Second), "success-call", &succeeded),
				}
			},
		},
		{
			name: "success after user interruption",
			turns: func(sessionKey string) []transcript.Turn {
				return []transcript.Turn{
					commandFixtureTurn(sessionKey, 0, base, "failed-call", "run-task primary"),
					resultFixtureTurn(sessionKey, 1, base.Add(time.Second), "failed-call", &failed),
					fixtureTurn(sessionKey, 2, base.Add(2*time.Second), transcript.RoleUser, "Try another approach.", "", ""),
					commandFixtureTurn(sessionKey, 3, base.Add(3*time.Second), "success-call", "run-task fallback"),
					resultFixtureTurn(sessionKey, 4, base.Add(4*time.Second), "success-call", &succeeded),
				}
			},
		},
		{
			name: "success alone",
			turns: func(sessionKey string) []transcript.Turn {
				return []transcript.Turn{
					commandFixtureTurn(sessionKey, 0, base, "success-call", "run-task fallback"),
					resultFixtureTurn(sessionKey, 1, base.Add(time.Second), "success-call", &succeeded),
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := fixtureSession(
				"ses_repair_"+compactFixtureName(test.name),
				transcript.CoverageComplete,
			)
			turns := test.turns(session.SessionKey)
			var events []model.Event
			sequence := int64(1)
			for _, turn := range turns {
				if turn.Role != transcript.RoleToolCall {
					continue
				}
				events = append(events, fixtureEvent(
					"evt_"+compactFixtureName(test.name)+"_"+turn.Payload.ToolCallID,
					session.SessionKey,
					sequence,
					turn.OccurredAt,
					"command.exec",
					turn.Payload.ToolCallID,
				))
				sequence++
			}
			got, err := Session(Input{
				Session:                 session,
				Turns:                   turns,
				CanonicalEvents:         events,
				TranscriptTurnsComplete: true,
				CanonicalEventsComplete: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if relationCounts(got.Edges)[trajectory.RelationRepairs] != 0 ||
				outcomeCount(got.Outcomes, trajectory.OutcomeRepair) != 0 {
				t.Fatalf("false repair = %+v", got)
			}
		})
	}
}

func TestCompletionClaimLinksOnlyAfterLatestExactMutation(t *testing.T) {
	base := time.Date(2026, 9, 10, 18, 20, 0, 0, time.UTC)
	session := fixtureSession("ses_completion_positive", transcript.CoverageComplete)
	got, err := Session(Input{
		Session: session,
		Turns: []transcript.Turn{
			mutationFixtureTurn(session.SessionKey, 0, base, "edit-call", "internal/a.go"),
			fixtureTurn(session.SessionKey, 1, base.Add(time.Second), transcript.RoleAssistant, "Implemented and complete.", "", ""),
		},
		CanonicalEvents: []model.Event{
			fixtureEvent("evt_completion_edit", session.SessionKey, 1, base, "file.write", "edit-call"),
		},
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	edge := edgeWithRelation(
		t,
		got.Edges,
		trajectory.RelationClaimsCompletionAfter,
	)
	if edge.EvidenceClass != trajectory.EvidenceDeterministicInference ||
		edge.From.TurnIndex == nil ||
		*edge.From.TurnIndex != 1 ||
		edge.To.EventID != "evt_completion_edit" ||
		len(edge.SourceRefs) != 3 {
		t.Fatalf("completion edge = %+v", edge)
	}
}

func TestCompletionClaimFalsePositives(t *testing.T) {
	base := time.Date(2026, 9, 10, 18, 30, 0, 0, time.UTC)
	tests := []struct {
		name   string
		turns  func(string) []transcript.Turn
		events func(string) []model.Event
	}{
		{
			name: "completion before edit",
			turns: func(sessionKey string) []transcript.Turn {
				return []transcript.Turn{
					fixtureTurn(sessionKey, 0, base, transcript.RoleAssistant, "Done.", "", ""),
					mutationFixtureTurn(sessionKey, 1, base.Add(time.Second), "edit-call", "internal/a.go"),
				}
			},
			events: func(sessionKey string) []model.Event {
				return []model.Event{
					fixtureEvent("evt_completion_after_claim", sessionKey, 1, base.Add(time.Second), "file.write", "edit-call"),
				}
			},
		},
		{
			name: "ordinary assistant text",
			turns: func(sessionKey string) []transcript.Turn {
				return []transcript.Turn{
					mutationFixtureTurn(sessionKey, 0, base, "edit-call", "internal/a.go"),
					fixtureTurn(sessionKey, 1, base.Add(time.Second), transcript.RoleAssistant, "I am still investigating.", "", ""),
				}
			},
			events: func(sessionKey string) []model.Event {
				return []model.Event{
					fixtureEvent("evt_completion_ordinary", sessionKey, 1, base, "file.write", "edit-call"),
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := fixtureSession(
				"ses_completion_"+compactFixtureName(test.name),
				transcript.CoverageComplete,
			)
			got, err := Session(Input{
				Session:                 session,
				Turns:                   test.turns(session.SessionKey),
				CanonicalEvents:         test.events(session.SessionKey),
				TranscriptTurnsComplete: true,
				CanonicalEventsComplete: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if relationCounts(got.Edges)[trajectory.RelationClaimsCompletionAfter] != 0 {
				t.Fatalf("false completion edge = %+v", got.Edges)
			}
		})
	}
}

func TestCompactionRequiresImmediatePredecessor(t *testing.T) {
	base := time.Date(2026, 9, 10, 18, 40, 0, 0, time.UTC)
	t.Run("positive", func(t *testing.T) {
		session := fixtureSession("ses_compaction_positive", transcript.CoverageComplete)
		got, err := Session(Input{
			Session: session,
			Turns: []transcript.Turn{
				fixtureTurn(session.SessionKey, 0, base, transcript.RoleAssistant, "Working.", "", ""),
				fixtureTurn(session.SessionKey, 1, base.Add(time.Second), transcript.RoleCompactionSummary, "summary", "", ""),
			},
			TranscriptTurnsComplete: true,
			CanonicalEventsComplete: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		edge := edgeWithRelation(
			t,
			got.Edges,
			trajectory.RelationCompactsAfter,
		)
		if edge.EvidenceClass != trajectory.EvidenceDeterministicInference ||
			edge.From.TurnIndex == nil ||
			*edge.From.TurnIndex != 1 ||
			edge.To.TurnIndex == nil ||
			*edge.To.TurnIndex != 0 {
			t.Fatalf("compaction edge = %+v", edge)
		}
	})
	t.Run("first turn", func(t *testing.T) {
		session := fixtureSession("ses_compaction_first", transcript.CoverageComplete)
		got, err := Session(Input{
			Session: session,
			Turns: []transcript.Turn{
				fixtureTurn(session.SessionKey, 0, base, transcript.RoleCompactionSummary, "summary", "", ""),
			},
			TranscriptTurnsComplete: true,
			CanonicalEventsComplete: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if relationCounts(got.Edges)[trajectory.RelationCompactsAfter] != 0 {
			t.Fatalf("first-turn compaction edge = %+v", got.Edges)
		}
	})
}

func assertFourTurnRefs(
	t *testing.T,
	refs []trajectory.NodeRef,
	indexes []int64,
) {
	t.Helper()
	if len(refs) != len(indexes) {
		t.Fatalf("source refs = %+v, want indexes %v", refs, indexes)
	}
	for index, want := range indexes {
		if refs[index].Kind != trajectory.NodeTranscriptTurn ||
			refs[index].TurnIndex == nil ||
			*refs[index].TurnIndex != want {
			t.Fatalf("source refs = %+v, want indexes %v", refs, indexes)
		}
	}
}

func outcomeCount(
	outcomes []trajectory.Outcome,
	kind trajectory.OutcomeKind,
) int {
	count := 0
	for _, outcome := range outcomes {
		if outcome.Kind == kind {
			count++
		}
	}
	return count
}
