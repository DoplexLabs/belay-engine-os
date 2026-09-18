package impact

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

func TestDeriveMeasuresBoundSessionAndMatchedBaseline(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	current := sessionInput(
		"ses_current",
		"codex",
		now.Add(-time.Hour),
		now,
		10,
		"implement",
		"issue_retry",
		[]transcript.Turn{
			userTurn("ses_current", 10, now.Add(-50*time.Minute), "No, use the existing package."),
			commandTurn("ses_current", 11, now.Add(-40*time.Minute), "go test ./internal/example"),
			failedTurn("ses_current", 12, now.Add(-39*time.Minute)),
			editTurn("ses_current", 13, now.Add(-30*time.Minute), "internal/example/example.go"),
			assistantTurn("ses_current", 14, now.Add(-20*time.Minute), "Implemented."),
			commandTurn("ses_current", 15, now.Add(-10*time.Minute), "go test ./internal/example"),
			tokenTurn("ses_current", 16, now, 80, 20, 0.20),
		},
	)
	current.TaskOutcomeState = experience.TaskOutcomeSucceeded
	current.OutcomeCoverageComplete = true
	priorOne := sessionInput(
		"ses_prior_one",
		"codex",
		now.Add(-4*time.Hour),
		now.Add(-3*time.Hour),
		0,
		"implement",
		"issue_retry",
		[]transcript.Turn{
			userTurn("ses_prior_one", 0, now.Add(-4*time.Hour), "Wrong, try again."),
			userTurn("ses_prior_one", 1, now.Add(-230*time.Minute), "Stop."),
			failedTurn("ses_prior_one", 2, now.Add(-220*time.Minute)),
			tokenTurn("ses_prior_one", 3, now.Add(-3*time.Hour), 150, 50, 0.40),
		},
	)
	priorTwo := sessionInput(
		"ses_prior_two",
		"codex",
		now.Add(-6*time.Hour),
		now.Add(-5*time.Hour),
		0,
		"implement",
		"issue_retry",
		[]transcript.Turn{
			userTurn("ses_prior_two", 0, now.Add(-6*time.Hour), "No, not that."),
			userTurn("ses_prior_two", 1, now.Add(-350*time.Minute), "Undo this."),
			failedTurn("ses_prior_two", 2, now.Add(-340*time.Minute)),
			failedTurn("ses_prior_two", 3, now.Add(-330*time.Minute)),
			failedTurn("ses_prior_two", 4, now.Add(-320*time.Minute)),
			tokenTurn("ses_prior_two", 5, now.Add(-5*time.Hour), 250, 50, 0.60),
		},
	)

	application := deliveredApplication(t, current.Session, now.Add(-45*time.Minute))
	got, err := Derive(Input{
		Application: application,
		Current:     current,
		Prior:       []SessionInput{priorTwo, priorOne},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SchemaVersion ||
		got.DerivationVersion != DerivationVersion ||
		got.ObservationID == "" ||
		got.InputHash == "" ||
		got.Evidence.StartTurn != 10 ||
		got.Evidence.EndTurn != 16 {
		t.Fatalf("observation identity/evidence = %+v", got)
	}
	if got.Current.ExplicitCorrections != 1 ||
		got.Current.FailedToolResults != 1 ||
		got.Current.Tokens != 100 ||
		got.Current.CostUSD == nil ||
		!near(*got.Current.CostUSD, 0.20) ||
		got.Current.VerificationAfterFinalEdit != VerificationObserved ||
		got.Current.CompletionWithoutVerification {
		t.Fatalf("current metrics = %+v", got.Current)
	}
	if got.Comparison.State != ComparisonMatched ||
		got.Comparison.ComparableSessions != 2 ||
		!reflect.DeepEqual(
			got.Comparison.SessionKeys,
			[]string{"ses_prior_one", "ses_prior_two"},
		) {
		t.Fatalf("comparison = %+v", got.Comparison)
	}
	if value := got.Comparison.BaselineMedian.ExplicitCorrections; value == nil || !near(*value, 2) {
		t.Fatalf("baseline corrections = %v", value)
	}
	if value := got.Comparison.BaselineMedian.FailedToolResults; value == nil || !near(*value, 2) {
		t.Fatalf("baseline failures = %v", value)
	}
	if value := got.Comparison.Delta.ExplicitCorrections; value == nil || !near(*value, -1) {
		t.Fatalf("correction delta = %v", value)
	}
	if value := got.Comparison.Delta.TokensPercent; value == nil || !near(*value, -60) {
		t.Fatalf("token delta = %v", value)
	}
	if !got.Coverage.OutcomeComplete || got.Coverage.PriorComplete != 2 {
		t.Fatalf("coverage = %+v", got.Coverage)
	}
}

func TestDeriveFiltersIneligiblePriorSessionsAndPreservesUnknowns(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	current := sessionInput(
		"ses_current",
		"claude-code",
		now.Add(-time.Hour),
		now,
		5,
		"audit",
		"issue_scope",
		[]transcript.Turn{
			userTurn("ses_current", 5, now.Add(-time.Hour), "No problem, continue."),
			editTurn("ses_current", 6, now.Add(-30*time.Minute), "a.go"),
			assistantTurn("ses_current", 7, now, "Done."),
		},
	)
	current.TaskOutcomeState = experience.TaskOutcomeUnknown
	eligible := sessionInput(
		"ses_eligible",
		"claude-code",
		now.Add(-3*time.Hour),
		now.Add(-2*time.Hour),
		0,
		"audit",
		"issue_scope",
		[]transcript.Turn{
			tokenTurn("ses_eligible", 0, now.Add(-3*time.Hour), 10, 5, 0.03),
		},
	)
	wrongHarness := cloneSessionInput(eligible)
	wrongHarness.Session.SessionKey = "ses_wrong_harness"
	wrongHarness.Session.Agent = "codex"
	wrongHarness.Turns[0].SessionKey = wrongHarness.Session.SessionKey
	live := cloneSessionInput(eligible)
	live.Session.SessionKey = "ses_live"
	live.Session.Coverage = transcript.CoverageLive
	live.Turns[0].SessionKey = live.Session.SessionKey

	got, err := Derive(Input{
		Application: deliveredApplication(t, current.Session, now.Add(-45*time.Minute)),
		Current:     current,
		Prior:       []SessionInput{eligible, wrongHarness, live},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Current.ExplicitCorrections != 0 {
		t.Fatalf("friendly no classified as correction: %+v", got.Current)
	}
	if got.Current.VerificationAfterFinalEdit != VerificationNotObserved ||
		!got.Current.CompletionWithoutVerification {
		t.Fatalf("completion/verification = %+v", got.Current)
	}
	if got.Comparison.State != ComparisonInsufficientBaseline ||
		got.Comparison.ComparableSessions != 1 ||
		!reflect.DeepEqual(got.Comparison.SessionKeys, []string{"ses_eligible"}) {
		t.Fatalf("comparison = %+v", got.Comparison)
	}
	if got.Comparison.Delta != (MetricDeltas{}) {
		t.Fatalf("insufficient comparison has deltas: %+v", got.Comparison.Delta)
	}
}

func TestDeriveRejectsLiveCurrentSession(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	current := sessionInput(
		"ses_live",
		"codex",
		now.Add(-time.Hour),
		time.Time{},
		0,
		"",
		"",
		[]transcript.Turn{
			tokenTurn("ses_live", 0, now, 1, 1, 0.01),
		},
	)
	current.Session.Coverage = transcript.CoverageLive
	current.TaskOutcomeState = experience.TaskOutcomeUnknown
	_, err := Derive(Input{
		Application: deliveredApplication(t, current.Session, now),
		Current:     current,
	})
	if err == nil {
		t.Fatal("live current session was accepted")
	}
}

func deliveredApplication(
	t *testing.T,
	session transcript.Session,
	deliveredAt time.Time,
) experience.Application {
	t.Helper()
	value := experience.Application{
		SchemaVersion: experience.ApplicationSchemaVersion,
		Experience: experience.ExperienceRef{
			ExperienceID: "exp_test",
			Version:      1,
		},
		ProjectIdentity:    session.ProjectIdentity,
		SessionKey:         session.SessionKey,
		DeliveryKind:       experience.DeliveryMissionPack,
		DeliveryState:      experience.DeliveryDelivered,
		DeliveredAt:        &deliveredAt,
		OpportunityState:   experience.OpportunityObserved,
		ApplicabilityState: experience.ApplicabilityApplicable,
		VerifierState:      experience.VerifierSatisfied,
		TaskOutcomeState:   experience.TaskOutcomeUnknown,
	}
	value.ApplicationID = value.DeterministicID()
	return value
}

func sessionInput(
	key, agent string,
	started, ended time.Time,
	windowStart int64,
	taskFamily, fingerprint string,
	turns []transcript.Turn,
) SessionInput {
	return SessionInput{
		Session: transcript.Session{
			SessionKey:      key,
			Agent:           agent,
			ProjectIdentity: "project-test",
			StartedAt:       started,
			EndedAt:         ended,
			Coverage:        transcript.CoverageComplete,
		},
		Turns:            turns,
		WindowStartTurn:  windowStart,
		TaskFamily:       taskFamily,
		IssueFingerprint: fingerprint,
		TaskOutcomeState: experience.TaskOutcomeUnknown,
	}
}

func cloneSessionInput(value SessionInput) SessionInput {
	value.Turns = append([]transcript.Turn(nil), value.Turns...)
	return value
}

func userTurn(key string, index int64, at time.Time, text string) transcript.Turn {
	return transcript.Turn{
		SessionKey: key,
		TurnIndex:  index,
		OccurredAt: at,
		Role:       transcript.RoleUser,
		Payload:    transcript.Payload{Text: text},
	}
}

func assistantTurn(key string, index int64, at time.Time, text string) transcript.Turn {
	return transcript.Turn{
		SessionKey: key,
		TurnIndex:  index,
		OccurredAt: at,
		Role:       transcript.RoleAssistant,
		Payload:    transcript.Payload{Text: text},
	}
}

func commandTurn(key string, index int64, at time.Time, command string) transcript.Turn {
	return transcript.Turn{
		SessionKey: key,
		TurnIndex:  index,
		OccurredAt: at,
		Role:       transcript.RoleToolCall,
		ToolName:   "exec_command",
		Payload:    transcript.Payload{RawCommand: command},
	}
}

func failedTurn(key string, index int64, at time.Time) transcript.Turn {
	exit := 1
	return transcript.Turn{
		SessionKey: key,
		TurnIndex:  index,
		OccurredAt: at,
		Role:       transcript.RoleToolResult,
		Payload: transcript.Payload{
			ExitCode:   &exit,
			ToolResult: "failed",
		},
	}
}

func editTurn(
	key string,
	index int64,
	at time.Time,
	path string,
) transcript.Turn {
	body, _ := json.Marshal(map[string]string{"file_path": path})
	return transcript.Turn{
		SessionKey: key,
		TurnIndex:  index,
		OccurredAt: at,
		Role:       transcript.RoleToolCall,
		ToolName:   "apply_patch",
		Payload:    transcript.Payload{ToolInput: body},
	}
}

func tokenTurn(
	key string,
	index int64,
	at time.Time,
	input, output int64,
	cost float64,
) transcript.Turn {
	return transcript.Turn{
		SessionKey:   key,
		TurnIndex:    index,
		OccurredAt:   at,
		Role:         transcript.RoleAssistant,
		InputTokens:  &input,
		OutputTokens: &output,
		CostUSD:      &cost,
	}
}

func near(left, right float64) bool {
	return math.Abs(left-right) < 0.000001
}
