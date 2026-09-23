package detection

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestBuiltinCatalogIsFixed(t *testing.T) {
	entries := DefaultCatalog().Entries()
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.DetectorID)
		if entry.DetectorVersion != builtinVersion ||
			entry.FingerprintVersion != "1" ||
			entry.Category == "" ||
			entry.TitleCode == "" ||
			entry.SuggestedActionType == "" {
			t.Fatalf("incomplete catalog entry: %+v", entry)
		}
	}
	want := []string{
		"explicit_command_failure",
		"explicit_tool_failure",
		"repeated_command_attempts",
		"explicit_permission_denial",
		"retained_verification_gap_after_changes",
		"unresolved_verification_failure_at_completion",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detector order = %v, want %v", got, want)
	}
	if !entries[2].Experimental {
		t.Fatal("repeated command detector must remain experimental")
	}
	for index, entry := range entries {
		if index != 2 && entry.Experimental {
			t.Fatalf("%s unexpectedly experimental", entry.DetectorID)
		}
	}
}

func TestExplicitToolFailureRequiresSafeStableIdentity(t *testing.T) {
	tests := []struct {
		name       string
		events     []model.Event
		wantMatch  bool
		severity   string
		citations  int
		identity   string
		historical bool
	}{
		{
			name: "MCP identity is preferred and normalized",
			events: []model.Event{
				withToolIdentity(
					withOutcome(testEvent("result-1", 1, "tool.result"), "failed", nil),
					"GitHub.Server",
					"Create_Issue",
				),
			},
			wantMatch: true,
			severity:  SeverityLow,
			citations: 1,
			identity:  "mcp:github.server/create_issue",
		},
		{
			name: "safe minimized tool resource is accepted",
			events: []model.Event{
				withToolResource(
					withOutcome(testEvent("result-1", 1, "tool.result"), "failed", nil),
					"read_file",
				),
			},
			wantMatch: true,
			severity:  SeverityLow,
			citations: 1,
			identity:  "tool:read_file",
		},
		{
			name: "three failures group and increase severity",
			events: []model.Event{
				withToolResource(withOutcome(testEvent("result-1", 1, "tool.result"), "failed", nil), "read_file"),
				withToolResource(withOutcome(testEvent("result-2", 2, "tool.result"), "failed", nil), "read_file"),
				withToolResource(withOutcome(testEvent("result-3", 3, "tool.result"), "failed", nil), "read_file"),
			},
			wantMatch: true,
			severity:  SeverityMedium,
			citations: 3,
			identity:  "tool:read_file",
		},
		{
			name: "historical positive is retained",
			events: []model.Event{
				withHistorical(withToolResource(
					withOutcome(testEvent("result-1", 1, "tool.result"), "failed", nil),
					"read_file",
				)),
			},
			wantMatch:  true,
			severity:   SeverityLow,
			citations:  1,
			identity:   "tool:read_file",
			historical: true,
		},
		{
			name: "missing identity is ignored",
			events: []model.Event{
				withOutcome(testEvent("result-1", 1, "tool.result"), "failed", nil),
			},
		},
		{
			name: "path-like identity is ignored",
			events: []model.Event{
				withToolResource(
					withOutcome(testEvent("result-1", 1, "tool.result"), "failed", nil),
					"/Users/private/tool",
				),
			},
		},
		{
			name: "tool call ID alone is ignored",
			events: []model.Event{
				withToolCall(
					withOutcome(testEvent("result-1", 1, "tool.result"), "failed", nil),
					"private-call-id",
				),
			},
		},
		{
			name: "unknown outcome is not a failure",
			events: []model.Event{
				withToolResource(testEvent("result-1", 1, "tool.result"), "read_file"),
			},
		},
		{
			name: "contradictory canonical outcome fails closed",
			events: []model.Event{
				withToolResource(
					withOutcome(testEvent("result-1", 1, "tool.result"), "failed", intPointer(0)),
					"read_file",
				),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := DefaultCatalog().Run(context.Background(), testInput(test.events...))
			requireCurrent(t, result)
			match, ok := findMatch(t, result, "explicit_tool_failure")
			if ok != test.wantMatch {
				t.Fatalf("match present = %v, want %v; matches = %+v", ok, test.wantMatch, result.Matches)
			}
			if !ok {
				return
			}
			if match.Severity != test.severity ||
				match.Confidence != ConfidenceHigh ||
				len(match.CitedEventIDs) != test.citations {
				t.Fatalf("unexpected match: %+v", match)
			}
			foundIdentity := false
			for _, dimension := range match.Fingerprint {
				if dimension.Name == "tool_identity" {
					foundIdentity = dimension.Value == test.identity
				}
				if strings.Contains(dimension.Value, "private-call-id") ||
					strings.Contains(dimension.Value, "/Users/") {
					t.Fatalf("unsafe fingerprint dimension: %+v", match.Fingerprint)
				}
			}
			if !foundIdentity {
				t.Fatalf("tool identity missing from fingerprint: %+v", match.Fingerprint)
			}
			if test.historical {
				for _, applicability := range result.Applicability {
					if applicability.DetectorID == "explicit_tool_failure" &&
						(applicability.AbsenceCapability != AbsenceIncomplete ||
							applicability.UnavailableReason != "tool_failure_absence_unsupported") {
						t.Fatalf("historical absence was overclaimed: %+v", applicability)
					}
				}
			}
		})
	}
}

func TestExplicitCommandFailureThresholdPairingAndUnknowns(t *testing.T) {
	tests := []struct {
		name       string
		events     []model.Event
		enrich     func(*SessionInput)
		wantMatch  bool
		severity   string
		confidence string
		citations  int
	}{
		{
			name: "single explicit failure",
			events: []model.Event{
				withOutcome(testEvent("result-1", 1, "command.result"), "failed", nil),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "result-1", "sig-a", CommandClassOther)
			},
			wantMatch:  true,
			severity:   SeverityLow,
			confidence: ConfidenceLow,
			citations:  1,
		},
		{
			name: "three failures increase severity",
			events: []model.Event{
				withOutcome(testEvent("result-1", 1, "command.result"), "failed", nil),
				withOutcome(testEvent("result-2", 2, "command.result"), "failed", nil),
				withOutcome(testEvent("result-3", 3, "command.result"), "failed", nil),
			},
			enrich: func(input *SessionInput) {
				for index := 1; index <= 3; index++ {
					enrichCommand(input, fmt.Sprintf("result-%d", index), "sig-a", CommandClassOther)
				}
			},
			wantMatch:  true,
			severity:   SeverityMedium,
			confidence: ConfidenceLow,
			citations:  3,
		},
		{
			name: "exec and result with tool ID count once",
			events: []model.Event{
				withToolCall(testEvent("exec", 1, "command.exec"), "call-1"),
				withToolCall(
					withOutcome(testEvent("result", 2, "command.result"), "failed", intPointer(1)),
					"call-1",
				),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "exec", "sig-a", CommandClassOther)
				enrichCommand(input, "result", "sig-a", CommandClassOther)
			},
			wantMatch:  true,
			severity:   SeverityLow,
			confidence: ConfidenceHigh,
			citations:  2,
		},
		{
			name: "adjacent exec and result without tool ID count once",
			events: []model.Event{
				testEvent("exec", 1, "command.exec"),
				withOutcome(testEvent("result", 2, "command.result"), "failed", nil),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "exec", "sig-a", CommandClassOther)
				enrichCommand(input, "result", "sig-a", CommandClassOther)
			},
			wantMatch:  true,
			severity:   SeverityLow,
			confidence: ConfidenceHigh,
			citations:  2,
		},
		{
			name: "unknown outcome does not fire",
			events: []model.Event{
				testEvent("result", 1, "command.result"),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "result", "sig-a", CommandClassOther)
			},
		},
		{
			name: "conflicting exit and outcome remains unknown",
			events: []model.Event{
				withOutcome(testEvent("result", 1, "command.result"), "succeeded", intPointer(1)),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "result", "sig-a", CommandClassOther)
			},
		},
		{
			name: "contradictory paired observations remain unknown",
			events: []model.Event{
				withToolCall(
					withOutcome(testEvent("exec", 1, "command.exec"), "failed", nil),
					"call-1",
				),
				withToolCall(
					withOutcome(testEvent("result", 2, "command.result"), "succeeded", nil),
					"call-1",
				),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "exec", "sig-a", CommandClassOther)
				enrichCommand(input, "result", "sig-a", CommandClassOther)
			},
		},
		{
			name: "internally contradictory paired observation remains unknown",
			events: []model.Event{
				withToolCall(
					withOutcome(testEvent("exec", 1, "command.exec"), "succeeded", intPointer(1)),
					"call-1",
				),
				withToolCall(
					withOutcome(testEvent("result", 2, "command.result"), "failed", nil),
					"call-1",
				),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "exec", "sig-a", CommandClassOther)
				enrichCommand(input, "result", "sig-a", CommandClassOther)
			},
		},
		{
			name: "missing signature does not fire",
			events: []model.Event{
				withOutcome(testEvent("result", 1, "command.result"), "failed", nil),
			},
			enrich: func(*SessionInput) {},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := testInput(test.events...)
			test.enrich(&input)
			result := DefaultCatalog().Run(context.Background(), input)
			requireCurrent(t, result)
			match, ok := findMatch(t, result, "explicit_command_failure")
			if ok != test.wantMatch {
				t.Fatalf("match present = %v, want %v; matches = %+v", ok, test.wantMatch, result.Matches)
			}
			if !ok {
				return
			}
			if match.Severity != test.severity ||
				match.Confidence != test.confidence ||
				len(match.CitedEventIDs) != test.citations {
				t.Fatalf("unexpected match: %+v", match)
			}
		})
	}
}

func TestRepeatedCommandAttemptsThresholdAndConfidence(t *testing.T) {
	for _, test := range []struct {
		name           string
		attempts       int
		paired         bool
		wantMatch      bool
		wantConfidence string
	}{
		{name: "below threshold", attempts: 3},
		{name: "four unpaired", attempts: 4, wantMatch: true, wantConfidence: ConfidenceLow},
		{name: "four paired", attempts: 4, paired: true, wantMatch: true, wantConfidence: ConfidenceMedium},
	} {
		t.Run(test.name, func(t *testing.T) {
			var events []model.Event
			input := testInput()
			for index := 0; index < test.attempts; index++ {
				if test.paired {
					execID := fmt.Sprintf("exec-%d", index)
					resultID := fmt.Sprintf("result-%d", index)
					toolID := fmt.Sprintf("call-%d", index)
					events = append(
						events,
						withToolCall(testEvent(execID, int64(index*2+1), "command.exec"), toolID),
						withToolCall(testEvent(resultID, int64(index*2+2), "command.result"), toolID),
					)
					enrichCommand(&input, execID, "sig-repeat", CommandClassOther)
					enrichCommand(&input, resultID, "sig-repeat", CommandClassOther)
				} else {
					id := fmt.Sprintf("exec-%d", index)
					events = append(events, testEvent(id, int64(index+1), "command.exec"))
					enrichCommand(&input, id, "sig-repeat", CommandClassOther)
				}
			}
			input.Events = events
			result := DefaultCatalog().Run(context.Background(), input)
			requireCurrent(t, result)
			match, ok := findMatch(t, result, "repeated_command_attempts")
			if ok != test.wantMatch {
				t.Fatalf("match present = %v, want %v; matches = %+v", ok, test.wantMatch, result.Matches)
			}
			if ok && (match.Severity != SeverityInfo ||
				match.Confidence != test.wantConfidence ||
				!match.Experimental) {
				t.Fatalf("unexpected match: %+v", match)
			}
		})
	}
}

func TestRepeatedCommandAttemptsRequireSameSignature(t *testing.T) {
	var events []model.Event
	input := testInput()
	for index := 0; index < 6; index++ {
		id := fmt.Sprintf("exec-%d", index)
		events = append(events, testEvent(id, int64(index+1), "command.exec"))
		enrichCommand(&input, id, fmt.Sprintf("sig-%d", index), CommandClassOther)
	}
	input.Events = events
	result := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, result)
	if _, ok := findMatch(t, result, "repeated_command_attempts"); ok {
		t.Fatalf("distinct signatures produced repetition: %+v", result.Matches)
	}
}

func TestReusedToolCallIDsDoNotInflateAttemptThresholds(t *testing.T) {
	input := testInput()
	for index := 0; index < 3; index++ {
		execID := fmt.Sprintf("exec-%d", index)
		resultID := fmt.Sprintf("result-%d", index)
		input.Events = append(
			input.Events,
			withToolCall(testEvent(execID, int64(index+1), "command.exec"), "reused-call"),
			withToolCall(
				withOutcome(
					testEvent(resultID, int64(index+4), "command.result"),
					"failed",
					nil,
				),
				"reused-call",
			),
		)
		enrichCommand(&input, execID, "sig-repeat", CommandClassOther)
		enrichCommand(&input, resultID, "sig-repeat", CommandClassOther)
	}

	result := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, result)
	if _, ok := findMatch(t, result, "repeated_command_attempts"); ok {
		t.Fatalf("ambiguous results inflated repetition threshold: %+v", result.Matches)
	}
	failure, ok := findMatch(t, result, "explicit_command_failure")
	if !ok ||
		failure.Severity != SeverityLow ||
		failure.Confidence != ConfidenceLow {
		t.Fatalf("ambiguous failures inflated D1 severity: %+v", result.Matches)
	}
}

func TestResultWithoutCommandInheritsOnlyFromTrustedPair(t *testing.T) {
	tests := []struct {
		name       string
		agent      string
		toolCallID string
		wantTrust  commandAttemptTrust
	}{
		{
			name:       "Codex tool-call pair",
			agent:      "codex",
			toolCallID: "call_codex_123",
			wantTrust:  trustToolCallPair,
		},
		{
			name:      "Claude adjacent no-ID pair",
			agent:     "claude-code",
			wantTrust: trustAdjacentPair,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exec := testEvent("exec", 1, "command.exec")
			exec.Source.Agent = test.agent
			resultEvent := withOutcome(
				testEvent("result", 2, "command.result"),
				"failed",
				intPointer(1),
			)
			resultEvent.Source.Agent = test.agent
			if test.toolCallID != "" {
				exec = withToolCall(exec, test.toolCallID)
				resultEvent = withToolCall(resultEvent, test.toolCallID)
			}
			terminal := testEvent("terminal", 3, "session.end")
			terminal.Source.Agent = test.agent
			terminal.Coverage.Depth = "lifecycle"

			input := testInput(exec, resultEvent, terminal)
			enrichCommand(&input, "exec", "sig-test", CommandClassTest)
			input.Enrichments["result"] = EventEnrichment{
				CommandClass: CommandClassOther,
				Version:      "1",
			}
			session, err := prepareSession(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			attempts, err := commandAttempts(context.Background(), session)
			if err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 1 ||
				attempts[0].signature != "sig-test" ||
				attempts[0].class != CommandClassTest ||
				attempts[0].trust != test.wantTrust ||
				len(attempts[0].events) != 2 {
				t.Fatalf("inherited attempt = %+v", attempts)
			}

			catalogResult := DefaultCatalog().Run(context.Background(), input)
			requireCurrent(t, catalogResult)
			failure, ok := findMatch(
				t,
				catalogResult,
				"explicit_command_failure",
			)
			if !ok || failure.Confidence != ConfidenceHigh {
				t.Fatalf("paired D1 = %+v, present = %v", failure, ok)
			}
			if _, ok := findMatch(
				t,
				catalogResult,
				"unresolved_verification_failure_at_completion",
			); !ok {
				t.Fatalf("paired result did not produce D5: %+v", catalogResult.Matches)
			}
		})
	}
}

func TestUnpairedResultsCannotProduceTrustedClaims(t *testing.T) {
	tests := []struct {
		name   string
		input  SessionInput
		wantD1 bool
	}{
		{
			name: "result without identity",
			input: testInput(
				withOutcome(
					testEvent("result", 1, "command.result"),
					"failed",
					intPointer(1),
				),
				testEvent("terminal", 2, "session.end"),
			),
		},
		{
			name: "standalone signed result",
			input: func() SessionInput {
				input := testInput(
					withOutcome(
						testEvent("result", 1, "command.result"),
						"failed",
						intPointer(1),
					),
					testEvent("terminal", 2, "session.end"),
				)
				enrichCommand(&input, "result", "sig-test", CommandClassTest)
				return input
			}(),
			wantD1: true,
		},
		{
			name: "reused tool-call ID",
			input: func() SessionInput {
				firstExec := withToolCall(
					testEvent("exec-1", 1, "command.exec"),
					"reused",
				)
				secondExec := withToolCall(
					testEvent("exec-2", 2, "command.exec"),
					"reused",
				)
				resultEvent := withToolCall(
					withOutcome(
						testEvent("result", 3, "command.result"),
						"failed",
						intPointer(1),
					),
					"reused",
				)
				input := testInput(
					firstExec,
					secondExec,
					resultEvent,
					testEvent("terminal", 4, "session.end"),
				)
				enrichCommand(&input, "exec-1", "sig-test", CommandClassTest)
				enrichCommand(&input, "exec-2", "sig-test", CommandClassTest)
				return input
			}(),
		},
		{
			name: "non-adjacent no-ID result",
			input: func() SessionInput {
				exec := testEvent("exec", 1, "command.exec")
				unrelated := testEvent("unrelated", 2, "file.read")
				resultEvent := withOutcome(
					testEvent("result", 3, "command.result"),
					"failed",
					intPointer(1),
				)
				input := testInput(
					exec,
					unrelated,
					resultEvent,
					testEvent("terminal", 4, "session.end"),
				)
				enrichCommand(&input, "exec", "sig-test", CommandClassTest)
				return input
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := DefaultCatalog().Run(context.Background(), test.input)
			requireCurrent(t, result)
			d1, ok := findMatch(t, result, "explicit_command_failure")
			if ok != test.wantD1 {
				t.Fatalf("D1 present = %v, want %v; %+v", ok, test.wantD1, result.Matches)
			}
			if ok && d1.Confidence == ConfidenceHigh {
				t.Fatalf("unpaired result produced high-confidence D1: %+v", d1)
			}
			if _, ok := findMatch(
				t,
				result,
				"unresolved_verification_failure_at_completion",
			); ok {
				t.Fatalf("unpaired result produced D5: %+v", result.Matches)
			}
		})
	}
}

func TestExplicitPermissionDenial(t *testing.T) {
	known := testEvent("known", 1, "permission.denied")
	unknown := testEvent("unknown", 2, "permission.denied")
	input := testInput(known, unknown)
	input.Enrichments["known"] = EventEnrichment{PermissionClass: "filesystem.write"}

	result := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, result)
	var matches []Match
	for _, match := range result.Matches {
		if match.DetectorID == "explicit_permission_denial" {
			matches = append(matches, match)
		}
	}
	if len(matches) != 2 {
		t.Fatalf("permission matches = %+v", matches)
	}
	confidenceByClass := make(map[string]string)
	for _, match := range matches {
		for _, dimension := range match.Fingerprint {
			if dimension.Name == "permission_class" {
				confidenceByClass[dimension.Value] = match.Confidence
			}
		}
	}
	if confidenceByClass["filesystem.write"] != ConfidenceHigh ||
		confidenceByClass["permission.unknown"] != ConfidenceMedium {
		t.Fatalf("confidence by class = %v", confidenceByClass)
	}
}

func TestRetainedVerificationGapHistoricalAndLiveSemantics(t *testing.T) {
	build := func(agent string, historical bool) SessionInput {
		mutation := testEvent("mutation", 1, "file.write")
		mutation.Source.Agent = agent
		terminal := testEvent("terminal", 2, "session.end")
		terminal.Source.Agent = agent
		terminal.Coverage.Depth = "lifecycle"
		if historical {
			mutation = withHistorical(mutation)
			terminal = withHistorical(terminal)
		}
		return testInput(mutation, terminal)
	}

	for _, test := range []struct {
		name       string
		input      SessionInput
		confidence string
		absence    AbsenceCapability
	}{
		{
			name:       "compatible Codex live",
			input:      build("codex", false),
			confidence: ConfidenceMedium,
			absence:    AbsenceSupported,
		},
		{
			name:       "compatible Claude live",
			input:      build("claude-code", false),
			confidence: ConfidenceMedium,
			absence:    AbsenceSupported,
		},
		{
			name:       "compatible Cursor live",
			input:      build("cursor", false),
			confidence: ConfidenceMedium,
			absence:    AbsenceSupported,
		},
		{
			name:       "historical retained evidence",
			input:      build("codex", true),
			confidence: ConfidenceLow,
			absence:    AbsenceIncomplete,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := DefaultCatalog().Run(context.Background(), test.input)
			requireCurrent(t, result)
			match, ok := findMatch(t, result, "retained_verification_gap_after_changes")
			if !ok ||
				match.Severity != SeverityInfo ||
				match.Confidence != test.confidence ||
				!reflect.DeepEqual(match.CitedEventIDs, []string{"mutation", "terminal"}) {
				t.Fatalf("retained gap = %+v, present = %v", match, ok)
			}
			for _, applicability := range result.Applicability {
				if applicability.DetectorID == "retained_verification_gap_after_changes" &&
					applicability.AbsenceCapability != test.absence {
					t.Fatalf("applicability = %+v, want %q", applicability, test.absence)
				}
			}
		})
	}
}

// TestRetainedVerificationGapStaysSilentForUnverifiedAntigravityHooks pins
// that a live Antigravity session with a mutation and a lifecycle terminal
// produces no absence-based match: Belay has not verified that Antigravity's
// Numbat hooks deliver lifecycle terminals or approval streams.
func TestRetainedVerificationGapStaysSilentForUnverifiedAntigravityHooks(
	t *testing.T,
) {
	mutation := testEvent("mutation", 1, "file.write")
	mutation.Source.Agent = "antigravity"
	terminal := testEvent("terminal", 2, "session.end")
	terminal.Source.Agent = "antigravity"
	terminal.Coverage.Depth = "lifecycle"

	result := DefaultCatalog().Run(
		context.Background(),
		testInput(mutation, terminal),
	)
	requireCurrent(t, result)
	if match, ok := findMatch(
		t,
		result,
		"retained_verification_gap_after_changes",
	); ok {
		t.Fatalf("unverified Antigravity session produced %+v", match)
	}
}

func TestRetainedVerificationGapRequiresFinalMutationBeforeTerminal(t *testing.T) {
	tests := []struct {
		name   string
		events []model.Event
	}{
		{
			name: "missing terminal",
			events: []model.Event{
				testEvent("mutation", 1, "file.write"),
			},
		},
		{
			name: "missing mutation",
			events: []model.Event{
				func() model.Event {
					event := testEvent("terminal", 1, "session.end")
					event.Coverage.Depth = "lifecycle"
					return event
				}(),
			},
		},
		{
			name: "mutation after terminal",
			events: []model.Event{
				func() model.Event {
					event := testEvent("terminal", 1, "session.end")
					event.Coverage.Depth = "lifecycle"
					return event
				}(),
				testEvent("mutation", 2, "file.write"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := DefaultCatalog().Run(context.Background(), testInput(test.events...))
			requireCurrent(t, result)
			if _, ok := findMatch(t, result, "retained_verification_gap_after_changes"); ok {
				t.Fatalf("gap fired without ordered boundaries: %+v", result.Matches)
			}
		})
	}
}

func TestRecognizedVerificationAfterFinalMutationSuppressesRetainedGap(t *testing.T) {
	classes := []string{
		CommandClassTest,
		CommandClassBuild,
		CommandClassTypecheck,
		CommandClassLint,
		CommandClassFormatCheck,
	}
	for _, class := range classes {
		for _, eventType := range []string{"command.exec", "command.result"} {
			t.Run(class+"/"+eventType, func(t *testing.T) {
				earlierMutation := testEvent("earlier-mutation", 1, "file.write")
				earlierVerification := testEvent("earlier-verification", 2, eventType)
				finalMutation := testEvent("final-mutation", 3, "file.delete")
				verification := testEvent("verification", 4, eventType)
				terminal := testEvent("terminal", 5, "session.end")
				terminal.Coverage.Depth = "lifecycle"
				input := testInput(
					earlierMutation,
					earlierVerification,
					finalMutation,
					verification,
					terminal,
				)
				enrichCommand(&input, "earlier-verification", "sig-other", CommandClassOther)
				enrichCommand(&input, "verification", "sig-verify", class)
				result := DefaultCatalog().Run(context.Background(), input)
				requireCurrent(t, result)
				if _, ok := findMatch(
					t,
					result,
					"retained_verification_gap_after_changes",
				); ok {
					t.Fatalf("recognized verification did not suppress gap: %+v", result.Matches)
				}
			})
		}
	}
}

func TestVerificationBeforeFinalMutationDoesNotSuppressRetainedGap(t *testing.T) {
	verification := testEvent("verification", 1, "command.exec")
	finalMutation := testEvent("final-mutation", 2, "file.write")
	terminal := testEvent("terminal", 3, "session.end")
	terminal.Coverage.Depth = "lifecycle"
	input := testInput(verification, finalMutation, terminal)
	enrichCommand(&input, "verification", "sig-test", CommandClassTest)

	result := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, result)
	match, ok := findMatch(t, result, "retained_verification_gap_after_changes")
	if !ok || !reflect.DeepEqual(
		match.CitedEventIDs,
		[]string{"final-mutation", "terminal"},
	) {
		t.Fatalf("retained gap = %+v, present = %v", match, ok)
	}
}

func TestUnresolvedVerificationFailureAtCompletion(t *testing.T) {
	tests := []struct {
		name      string
		events    []model.Event
		enrich    func(*SessionInput)
		wantMatch bool
	}{
		{
			name: "failed verification followed by terminal",
			events: []model.Event{
				withToolCall(testEvent("exec", 1, "command.exec"), "call-1"),
				withToolCall(
					withOutcome(testEvent("failed", 2, "command.result"), "failed", nil),
					"call-1",
				),
				func() model.Event {
					event := testEvent("terminal", 3, "session.end")
					event.Coverage.Depth = "lifecycle"
					return event
				}(),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "exec", "sig-test", CommandClassTest)
			},
			wantMatch: true,
		},
		{
			name: "later same signature success resolves",
			events: []model.Event{
				withToolCall(testEvent("exec-fail", 1, "command.exec"), "call-1"),
				withToolCall(
					withOutcome(testEvent("failed", 2, "command.result"), "failed", nil),
					"call-1",
				),
				withToolCall(testEvent("exec-pass", 3, "command.exec"), "call-2"),
				withToolCall(
					withOutcome(testEvent("passed", 4, "command.result"), "succeeded", nil),
					"call-2",
				),
				testEvent("terminal", 5, "session.end"),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "exec-fail", "sig-test", CommandClassTest)
				enrichCommand(input, "exec-pass", "sig-test", CommandClassTest)
			},
		},
		{
			name: "different signature success does not resolve",
			events: []model.Event{
				withToolCall(testEvent("exec-fail", 1, "command.exec"), "call-1"),
				withToolCall(
					withOutcome(testEvent("failed", 2, "command.result"), "failed", nil),
					"call-1",
				),
				withToolCall(testEvent("exec-pass", 3, "command.exec"), "call-2"),
				withToolCall(
					withOutcome(testEvent("passed", 4, "command.result"), "succeeded", nil),
					"call-2",
				),
				testEvent("terminal", 5, "session.end"),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "exec-fail", "sig-test-a", CommandClassTest)
				enrichCommand(input, "exec-pass", "sig-test-b", CommandClassTest)
			},
			wantMatch: true,
		},
		{
			name: "no terminal",
			events: []model.Event{
				withOutcome(testEvent("failed", 1, "command.result"), "failed", nil),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "failed", "sig-test", CommandClassTest)
			},
		},
		{
			name: "non verification failure",
			events: []model.Event{
				withOutcome(testEvent("failed", 1, "command.result"), "failed", nil),
				testEvent("terminal", 2, "session.end"),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "failed", "sig-search", CommandClassOther)
			},
		},
		{
			name: "unknown result",
			events: []model.Event{
				testEvent("unknown", 1, "command.result"),
				testEvent("terminal", 2, "session.end"),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "unknown", "sig-test", CommandClassTest)
			},
		},
		{
			name: "conflicting paired command classes",
			events: []model.Event{
				withToolCall(testEvent("exec", 1, "command.exec"), "call-1"),
				withToolCall(
					withOutcome(testEvent("result", 2, "command.result"), "failed", nil),
					"call-1",
				),
				testEvent("terminal", 3, "session.end"),
			},
			enrich: func(input *SessionInput) {
				enrichCommand(input, "exec", "sig-test", CommandClassTest)
				enrichCommand(input, "result", "sig-test", CommandClassOther)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := testInput(test.events...)
			test.enrich(&input)
			result := DefaultCatalog().Run(context.Background(), input)
			requireCurrent(t, result)
			match, ok := findMatch(t, result, "unresolved_verification_failure_at_completion")
			if ok != test.wantMatch {
				t.Fatalf("match present = %v, want %v; matches = %+v", ok, test.wantMatch, result.Matches)
			}
			if ok && (match.Severity != SeverityHigh || match.Confidence != ConfidenceHigh) {
				t.Fatalf("unexpected D5 match: %+v", match)
			}
		})
	}
}

func TestUnresolvedVerificationRequiresResultBeforeTerminal(t *testing.T) {
	exec := withToolCall(testEvent("exec", 1, "command.exec"), "call-1")
	terminal := testEvent("terminal", 2, "session.end")
	terminal.Coverage.Depth = "lifecycle"
	resultEvent := withToolCall(
		withOutcome(testEvent("result", 3, "command.result"), "failed", intPointer(1)),
		"call-1",
	)
	input := testInput(exec, terminal, resultEvent)
	enrichCommand(&input, "exec", "sig-test", CommandClassTest)

	result := DefaultCatalog().Run(context.Background(), input)
	requireCurrent(t, result)
	if _, ok := findMatch(t, result, "explicit_command_failure"); !ok {
		t.Fatalf("paired failure itself was not detected: %+v", result.Matches)
	}
	if _, ok := findMatch(
		t,
		result,
		"unresolved_verification_failure_at_completion",
	); ok {
		t.Fatalf("result after terminal produced D5: %+v", result.Matches)
	}
}

func TestNegativeCorpusDoesNotOverclaim(t *testing.T) {
	tests := []struct {
		name      string
		input     SessionInput
		forbidden []string
	}{
		{
			name: "TDD failure followed by pass",
			input: func() SessionInput {
				input := testInput(
					withOutcome(testEvent("fail", 1, "command.result"), "failed", nil),
					withOutcome(testEvent("pass", 2, "command.result"), "succeeded", nil),
					testEvent("terminal", 3, "session.end"),
				)
				enrichCommand(&input, "fail", "sig-test", CommandClassTest)
				enrichCommand(&input, "pass", "sig-test", CommandClassTest)
				return input
			}(),
			forbidden: []string{"unresolved_verification_failure_at_completion"},
		},
		{
			name: "search miss is not unresolved verification",
			input: func() SessionInput {
				input := testInput(
					withOutcome(testEvent("grep", 1, "command.result"), "failed", intPointer(1)),
					testEvent("terminal", 2, "session.end"),
				)
				enrichCommand(&input, "grep", "sig-grep", CommandClassOther)
				return input
			}(),
			forbidden: []string{"unresolved_verification_failure_at_completion"},
		},
		{
			name: "file mutation without complete matrix is not evidence gap",
			input: testInput(
				testEvent("mutation", 1, "file.write"),
				testEvent("terminal", 2, "session.end"),
			),
			forbidden: []string{"retained_verification_gap_after_changes"},
		},
		{
			name: "deliberate permission denial is only attention signal",
			input: testInput(
				testEvent("denied", 1, "permission.denied"),
			),
			forbidden: []string{
				"explicit_command_failure",
				"retained_verification_gap_after_changes",
				"unresolved_verification_failure_at_completion",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := DefaultCatalog().Run(context.Background(), test.input)
			requireCurrent(t, result)
			for _, detectorID := range test.forbidden {
				if _, ok := findMatch(t, result, detectorID); ok {
					t.Fatalf("%s overclaimed: %+v", detectorID, result.Matches)
				}
			}
		})
	}
}

func withToolIdentity(event model.Event, server, tool string) model.Event {
	event.Observation.Details = &model.Details{
		MCPServer: server,
		MCPTool:   tool,
	}
	event.Observation.Resource = &model.Resource{
		Kind: "mcp",
		Name: server + "/" + tool,
	}
	return event
}

func withToolResource(event model.Event, name string) model.Event {
	event.Observation.Resource = &model.Resource{Kind: "tool", Name: name}
	return event
}

func withHistorical(event model.Event) model.Event {
	event.Source.Kind = "artifact"
	event.Historical.IsHistorical = true
	event.Coverage.Depth = "artifact"
	return event
}

func TestHarnessProvidesHookCoverageGatesAbsenceOnKnownHarnesses(t *testing.T) {
	for _, test := range []struct {
		agent string
		want  bool
	}{
		{agent: "codex", want: true},
		{agent: "claude-code", want: true},
		{agent: "cursor", want: true},
		{agent: "claude", want: false},
		{agent: "future-agent", want: false},
		{agent: "", want: false},
		// Antigravity hooks are unverified for lifecycle terminals and
		// approval streams; absence-based detectors must stay silent.
		{agent: "antigravity", want: false},
	} {
		t.Run(test.agent, func(t *testing.T) {
			if got := harnessProvidesHookCoverage(test.agent); got != test.want {
				t.Fatalf("harnessProvidesHookCoverage(%q) = %v, want %v", test.agent, got, test.want)
			}
		})
	}
}

func TestPermissionAbsenceCapabilityCoversCursorHooks(t *testing.T) {
	for _, test := range []struct {
		name  string
		agent string
		want  AbsenceCapability
	}{
		{name: "cursor hook", agent: "cursor", want: AbsenceSupported},
		{name: "codex hook", agent: "codex", want: AbsenceSupported},
		{name: "antigravity hook", agent: "antigravity", want: AbsenceNotApplicable},
		{name: "unknown harness hook", agent: "future-agent", want: AbsenceNotApplicable},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := testEvent("permission", 1, "permission.denied")
			event.Source.Agent = test.agent
			event.Source.Kind = "hook"
			session, err := prepareSession(context.Background(), testInput(event))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := permissionAbsenceCapability(session)
			if got != test.want {
				t.Fatalf("permissionAbsenceCapability(%q) = %q, want %q", test.agent, got, test.want)
			}
		})
	}
}
