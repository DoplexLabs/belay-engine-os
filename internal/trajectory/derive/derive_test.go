package derive

import (
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestSessionDerivesCorrelationsAndRealCorrection(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	session := fixtureSession("ses_positive", transcript.CoverageComplete)
	turns := []transcript.Turn{
		fixtureTurn(session.SessionKey, 0, base, transcript.RoleToolCall, "", "parent-call", ""),
		fixtureTurn(session.SessionKey, 1, base.Add(time.Second), transcript.RoleToolCall, "", "child-call", "parent-call"),
		fixtureTurn(session.SessionKey, 2, base.Add(2*time.Second), transcript.RoleToolResult, "", "child-call", ""),
		fixtureTurn(session.SessionKey, 3, base.Add(3*time.Second), transcript.RoleUser, "No, use go test not go build.", "", ""),
	}
	events := []model.Event{
		fixtureEvent("evt_parent", session.SessionKey, 1, base, "tool.call", "parent-call"),
		fixtureEvent("evt_child", session.SessionKey, 2, base.Add(time.Second), "command.exec", "child-call"),
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
	if !got.Coverage.FullyDerived || len(got.Diagnostics) != 0 {
		t.Fatalf("coverage/diagnostics = %+v/%+v", got.Coverage, got.Diagnostics)
	}
	wantRelations := map[trajectory.EdgeRelation]int{
		trajectory.RelationInvokes:    2,
		trajectory.RelationReturnsFor: 1,
		trajectory.RelationParentOf:   1,
		trajectory.RelationRespondsTo: 1,
		trajectory.RelationCorrects:   1,
	}
	if gotRelations := relationCounts(got.Edges); !reflect.DeepEqual(gotRelations, wantRelations) {
		t.Fatalf("relations = %+v, want %+v", gotRelations, wantRelations)
	}
	if len(got.Outcomes) != 1 ||
		got.Outcomes[0].Kind != trajectory.OutcomeCorrection ||
		got.Outcomes[0].DerivationVersion != Version {
		t.Fatalf("outcomes = %+v", got.Outcomes)
	}
	for _, edge := range got.Edges {
		if edge.DerivationVersion != Version {
			t.Fatalf("edge derivation version = %q", edge.DerivationVersion)
		}
		if (edge.Relation == trajectory.RelationRespondsTo ||
			edge.Relation == trajectory.RelationCorrects) &&
			edge.EvidenceClass != trajectory.EvidenceDeterministicInference {
			t.Fatalf(
				"%s evidence class = %q, want deterministic inference",
				edge.Relation,
				edge.EvidenceClass,
			)
		}
		if err := edge.Validate(); err != nil {
			t.Fatalf("derived edge invalid: %v", err)
		}
	}
	if err := got.Outcomes[0].Validate(); err != nil {
		t.Fatalf("derived outcome invalid: %v", err)
	}

	replayed, err := Session(Input{
		Session:                 session,
		Turns:                   turns,
		CanonicalEvents:         events,
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, replayed) {
		t.Fatal("deterministic rerun changed derivation result")
	}
}

func TestSessionTreatsAgainAsHighConfidenceCorrection(t *testing.T) {
	base := time.Date(2026, 9, 10, 12, 30, 0, 0, time.UTC)
	session := fixtureSession("ses_again", transcript.CoverageComplete)
	got, err := Session(Input{
		Session: session,
		Turns: []transcript.Turn{
			fixtureTurn(
				session.SessionKey,
				0,
				base,
				transcript.RoleAssistant,
				"I changed the generated file.",
				"",
				"",
			),
			fixtureTurn(
				session.SessionKey,
				1,
				base.Add(time.Second),
				transcript.RoleUser,
				"Again, edit the source file instead.",
				"",
				"",
			),
		},
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Edges) != 2 || len(got.Outcomes) != 1 {
		t.Fatalf("again correction result = %+v", got)
	}
	for _, edge := range got.Edges {
		if edge.EvidenceClass != trajectory.EvidenceDeterministicInference {
			t.Fatalf(
				"%s evidence class = %q, want deterministic inference",
				edge.Relation,
				edge.EvidenceClass,
			)
		}
	}
	if got.Outcomes[0].Kind != trajectory.OutcomeCorrection ||
		got.Outcomes[0].EvidenceClass !=
			trajectory.EvidenceDeterministicInference {
		t.Fatalf("again correction outcome = %+v", got.Outcomes[0])
	}
}

func TestSessionExcludesFalsePositiveAndMachineEnvelopeCorrections(t *testing.T) {
	base := time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name           string
		text           string
		parentID       string
		wantExcluded   int
		wantCorrection bool
	}{
		{name: "acknowledgement", text: "No problem, thanks."},
		{
			name:         "machine envelope",
			text:         "<task-notification>Wrong result from background task</task-notification>",
			wantExcluded: 1,
		},
		{
			name:         "delegated correction",
			text:         "No, use the schema source instead.",
			parentID:     "delegating-tool-call",
			wantExcluded: 1,
		},
		{
			name:         "mode raw query envelope",
			text:         "MODE: planning\nRAW QUERY:\nWrong, use the schema source.",
			wantExcluded: 1,
		},
		{
			name:         "task json envelope",
			text:         `{"description":"Wrong result","prompt":"No, use the schema source."}`,
			wantExcluded: 1,
		},
		{
			name:           "ordinary mode raw query prose",
			text:           "No, mode and raw query are labels; use the schema source.",
			wantCorrection: true,
		},
		{
			name: "short turn without correction",
			text: "Please continue.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := fixtureSession("ses_"+test.name, transcript.CoverageComplete)
			got, err := Session(Input{
				Session: session,
				Turns: []transcript.Turn{
					fixtureTurn(session.SessionKey, 0, base, transcript.RoleAssistant, "Done.", "", ""),
					fixtureTurn(
						session.SessionKey,
						1,
						base.Add(time.Second),
						transcript.RoleUser,
						test.text,
						"",
						test.parentID,
					),
				},
				TranscriptTurnsComplete: true,
				CanonicalEventsComplete: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantEdges := 0
			wantOutcomes := 0
			if test.wantCorrection {
				wantEdges = 2
				wantOutcomes = 1
			}
			if len(got.Edges) != wantEdges ||
				len(got.Outcomes) != wantOutcomes ||
				got.Coverage.MachineEnvelopesExcluded != test.wantExcluded {
				t.Fatalf("result = %+v", got)
			}
		})
	}
}

func TestSessionReportsPartialLinksWithoutFabricatingEdges(t *testing.T) {
	base := time.Date(2026, 9, 10, 14, 0, 0, 0, time.UTC)
	session := fixtureSession("ses_partial", transcript.CoveragePartial)
	got, err := Session(Input{
		Session: session,
		Turns: []transcript.Turn{
			fixtureTurn(session.SessionKey, 0, base, transcript.RoleToolResult, "", "missing-call", "missing-parent"),
		},
		TranscriptTurnsComplete: false,
		CanonicalEventsComplete: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Edges) != 0 || len(got.Outcomes) != 0 || got.Coverage.FullyDerived {
		t.Fatalf("partial result fabricated evidence: %+v", got)
	}
	for _, code := range []DiagnosticCode{
		DiagnosticPartialTranscript,
		DiagnosticPartialCanonicalEvents,
		DiagnosticMissingToolCall,
		DiagnosticMissingParentToolCall,
	} {
		if !hasDiagnostic(got.Diagnostics, code) {
			t.Fatalf("missing diagnostic %q in %+v", code, got.Diagnostics)
		}
	}
}

func TestSessionScopesSharedToolCallIDsBySession(t *testing.T) {
	base := time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC)
	firstSession := fixtureSession("ses_shared_a", transcript.CoverageComplete)
	secondSession := fixtureSession("ses_shared_b", transcript.CoverageComplete)
	firstEvent := fixtureEvent("evt_shared_a", firstSession.SessionKey, 1, base, "tool.call", "shared-call")
	secondEvent := fixtureEvent("evt_shared_b", secondSession.SessionKey, 1, base, "tool.call", "shared-call")

	first, err := Session(Input{
		Session: firstSession,
		Turns: []transcript.Turn{
			fixtureTurn(firstSession.SessionKey, 0, base, transcript.RoleToolCall, "", "shared-call", ""),
		},
		CanonicalEvents:         []model.Event{firstEvent, secondEvent},
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Session(Input{
		Session: secondSession,
		Turns: []transcript.Turn{
			fixtureTurn(secondSession.SessionKey, 0, base, transcript.RoleToolCall, "", "shared-call", ""),
		},
		CanonicalEvents:         []model.Event{firstEvent, secondEvent},
		TranscriptTurnsComplete: true,
		CanonicalEventsComplete: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Edges) != 1 || first.Edges[0].To.EventID != firstEvent.EventID ||
		len(second.Edges) != 1 || second.Edges[0].To.EventID != secondEvent.EventID ||
		first.Edges[0].EdgeID == second.Edges[0].EdgeID {
		t.Fatalf("cross-session correlation: first=%+v second=%+v", first.Edges, second.Edges)
	}
	if !hasDiagnostic(first.Diagnostics, DiagnosticForeignCanonicalEvent) ||
		!hasDiagnostic(second.Diagnostics, DiagnosticForeignCanonicalEvent) {
		t.Fatalf("foreign-session exclusions not diagnosed: %+v / %+v", first.Diagnostics, second.Diagnostics)
	}
}

func fixtureSession(
	sessionKey string,
	coverage transcript.SessionCoverage,
) transcript.Session {
	return transcript.Session{
		SessionKey:      sessionKey,
		Agent:           "codex",
		ProjectIdentity: "git@example.test:doplexlabs/belay.git",
		Coverage:        coverage,
	}
}

func fixtureTurn(
	sessionKey string,
	index int64,
	occurredAt time.Time,
	role transcript.Role,
	text string,
	callID string,
	parentID string,
) transcript.Turn {
	return transcript.Turn{
		TurnID:          "turn-" + sessionKey + "-" + occurredAt.Format("150405.000000000"),
		SourceRecordKey: "source-" + sessionKey + "-" + occurredAt.Format("150405.000000000"),
		SessionKey:      sessionKey,
		TurnIndex:       index,
		OccurredAt:      occurredAt,
		Role:            role,
		Payload: transcript.Payload{
			Text:            text,
			ToolCallID:      callID,
			ParentToolUseID: parentID,
		},
	}
}

func fixtureEvent(
	eventID string,
	sessionKey string,
	sequence int64,
	occurredAt time.Time,
	eventType string,
	callID string,
) model.Event {
	return model.Event{
		EventID:    eventID,
		OccurredAt: occurredAt,
		Source:     model.Source{Sequence: sequence},
		Session:    model.SessionRef{Key: sessionKey},
		Observation: model.Observation{
			Type:    eventType,
			Details: &model.Details{ToolCallID: callID},
		},
	}
}

func relationCounts(edges []trajectory.Edge) map[trajectory.EdgeRelation]int {
	result := make(map[trajectory.EdgeRelation]int)
	for _, edge := range edges {
		result[edge.Relation]++
	}
	return result
}

func hasDiagnostic(values []Diagnostic, code DiagnosticCode) bool {
	for _, value := range values {
		if value.Code == code {
			return true
		}
	}
	return false
}
