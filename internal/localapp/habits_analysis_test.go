package localapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
	"github.com/DoplexLabs/belay-engine/internal/userinsights"
)

type habitTestStore struct {
	sessions []transcript.Session
	turns    map[string][]transcript.Turn
	debriefs map[string]userinsights.DebriefRecord
}

func (s *habitTestStore) GetTranscriptSession(_ context.Context, key string) (transcript.Session, error) {
	for _, session := range s.sessions {
		if session.SessionKey == key {
			return session, nil
		}
	}
	return transcript.Session{}, errors.New("missing")
}

func (s *habitTestStore) QueryTranscriptTurns(_ context.Context, key string, _ int) ([]transcript.Turn, error) {
	return s.turns[key], nil
}

func (s *habitTestStore) QueryTranscriptSessions(_ context.Context, _ transcript.SessionQuery) ([]transcript.Session, error) {
	return s.sessions, nil
}

func (s *habitTestStore) QuerySessionTimeline(_ context.Context, _ model.TimelineQuery) (model.EventPage, error) {
	return model.EventPage{}, nil
}

func (s *habitTestStore) GetHabitDebrief(_ context.Context, key string) (userinsights.DebriefRecord, error) {
	record, ok := s.debriefs[key]
	if !ok {
		return userinsights.DebriefRecord{}, errors.New("not found")
	}
	return record, nil
}

func (s *habitTestStore) ReplaceHabitDebrief(_ context.Context, record userinsights.DebriefRecord) error {
	s.debriefs[record.SessionKey] = record
	return nil
}

func habitTestOutput() []byte {
	body, _ := json.Marshal(map[string]any{
		"headline":     "Checks came late.",
		"task_summary": "Refactor billing.",
		"phases":       []map[string]any{{"label": "All", "from_minute": 0, "to_minute": 10, "what_happened": "Work.", "verdict": "partly_wasted"}},
		"insights": []map[string]any{{"kind": "verification", "title": "Ask for checks first", "what_you_did": "Waited.", "what_it_cost": "Ten minutes.",
			"ideal_path": "Name the checks.", "say_this_instead": "Run go test ./... after each change.", "evidence_turns": []int{0}, "confidence": 0.9}},
		"keep_doing":          []map[string]any{},
		"prompt_length_read":  map[string]any{"verdict": "about_right", "explanation": "Fine.", "rewrite": "Same."},
		"next_session_opener": "Refactor billing; run go test after each phase.",
	})
	return body
}

func newHabitTestStore() *habitTestStore {
	start := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	prompt := transcript.Turn{TurnID: "t0", SessionKey: "ses_a", TurnIndex: 0, OccurredAt: start, Role: transcript.RoleUser}
	prompt.Payload.Text = "Refactor billing in /Users/private/app."
	reply := transcript.Turn{TurnID: "t1", SessionKey: "ses_a", TurnIndex: 1, OccurredAt: start.Add(time.Minute), Role: transcript.RoleAssistant}
	reply.Payload.Text = "Done."
	correction := transcript.Turn{TurnID: "t2", SessionKey: "ses_a", TurnIndex: 2, OccurredAt: start.Add(2 * time.Minute), Role: transcript.RoleUser}
	correction.Payload.Text = "No, run the tests."
	return &habitTestStore{
		sessions: []transcript.Session{
			{SessionKey: "ses_a", Agent: "claude-code", ProjectPath: "/Users/private/app", ProjectIdentity: "/Users/private/app",
				StartedAt: start, EndedAt: start.Add(10 * time.Minute), Coverage: transcript.CoverageComplete, UserTurnCount: 2},
			{SessionKey: "ses_live", Agent: "codex", ProjectIdentity: "/Users/private/app", Coverage: transcript.CoverageLive, UserTurnCount: 5},
		},
		turns:    map[string][]transcript.Turn{"ses_a": {prompt, reply, correction}},
		debriefs: map[string]userinsights.DebriefRecord{},
	}
}

func TestHabitDebriefServiceGeneratesThroughHarnessAndCaches(t *testing.T) {
	store := newHabitTestStore()
	runs := 0
	runner := func(_ context.Context, harness SemanticHarness, prompt []byte, schema []byte) (ExperienceSemanticHarnessResult, error) {
		runs++
		text := string(prompt)
		if harness != SemanticHarnessClaude || !json.Valid(schema) ||
			strings.Contains(text, "/Users/private") ||
			!strings.Contains(text, "Refactor billing") ||
			!strings.Contains(text, "debriefing the HUMAN developer") {
			t.Fatalf("unexpected prompt or schema: %s", text[:200])
		}
		return ExperienceSemanticHarnessResult{Output: habitTestOutput(), Model: "claude-test"}, nil
	}
	service, err := NewHabitDebriefService(store,
		WithHabitDebriefRunner(runner),
		WithHabitDebriefHarnessLookup(func(string) (SemanticHarness, bool) { return SemanticHarnessClaude, true }),
		WithHabitDebriefClock(func() time.Time { return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.Generate(context.Background(), "ses_a", false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Harness != "claude" || record.Model != "claude-test" || record.PromptVersion != userinsights.DebriefPromptVersion ||
		record.Debrief.Headline != "Checks came late." || record.ProjectIdentity != "/Users/private/app" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if _, err := service.Generate(context.Background(), "ses_a", false); err != nil || runs != 1 {
		t.Fatalf("a current debrief must be reused without rerunning the harness: runs=%d err=%v", runs, err)
	}
	if _, err := service.Generate(context.Background(), "ses_a", true); err != nil || runs != 2 {
		t.Fatalf("refresh must rerun the harness: runs=%d err=%v", runs, err)
	}
	if _, err := service.Generate(context.Background(), "ses_live", false); !errors.Is(err, ErrHabitSessionNotReady) {
		t.Fatalf("live sessions must not be debriefed: %v", err)
	}
	if _, err := service.Generate(context.Background(), "ses_missing", false); !errors.Is(err, ErrHabitSessionNotFound) {
		t.Fatalf("missing sessions must be reported: %v", err)
	}
	report, err := service.AnalyzeHabitSessionsOnce(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if report.Considered != 2 || report.Current != 1 || report.Skipped != 1 || report.Generated != 0 {
		t.Fatalf("unexpected batch report: %+v", report)
	}
}

func TestHabitDebriefServiceReportsHarnessAndGenerationFailures(t *testing.T) {
	store := newHabitTestStore()
	service, _ := NewHabitDebriefService(store,
		WithHabitDebriefHarnessLookup(func(string) (SemanticHarness, bool) { return "", false }),
	)
	if _, err := service.Generate(context.Background(), "ses_a", false); !errors.Is(err, ErrHabitHarnessUnavailable) {
		t.Fatalf("expected harness unavailable, got %v", err)
	}
	if _, ok := service.Harness(); ok {
		t.Fatal("harness should be reported unavailable")
	}
	failing, _ := NewHabitDebriefService(store,
		WithHabitDebriefRunner(func(context.Context, SemanticHarness, []byte, []byte) (ExperienceSemanticHarnessResult, error) {
			return ExperienceSemanticHarnessResult{Output: []byte(`{"headline":"only"}`)}, nil
		}),
		WithHabitDebriefHarnessLookup(func(string) (SemanticHarness, bool) { return SemanticHarnessCodex, true }),
	)
	if _, err := failing.Generate(context.Background(), "ses_a", false); !errors.Is(err, ErrHabitGenerationFailed) {
		t.Fatalf("invalid harness output must fail generation, got %v", err)
	}
	if len(store.debriefs) != 0 {
		t.Fatal("failed generations must not be stored")
	}
}

func TestHabitDebriefPrecomputeOnceWritesOneMissingDebriefWithBackoff(t *testing.T) {
	store := newHabitTestStore()
	start := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	second := transcript.Turn{TurnID: "b0", SessionKey: "ses_b", TurnIndex: 0, OccurredAt: start, Role: transcript.RoleUser}
	second.Payload.Text = "Fix the flaky test."
	secondReply := transcript.Turn{TurnID: "b1", SessionKey: "ses_b", TurnIndex: 1, OccurredAt: start.Add(time.Minute), Role: transcript.RoleAssistant}
	secondReply.Payload.Text = "Fixed."
	secondAgain := transcript.Turn{TurnID: "b2", SessionKey: "ses_b", TurnIndex: 2, OccurredAt: start.Add(2 * time.Minute), Role: transcript.RoleUser}
	secondAgain.Payload.Text = "Thanks."
	store.sessions = append([]transcript.Session{{
		SessionKey: "ses_b", Agent: "claude-code", ProjectIdentity: "/Users/private/app",
		StartedAt: start, EndedAt: start.Add(5 * time.Minute), Coverage: transcript.CoverageComplete, UserTurnCount: 2,
	}}, store.sessions...)
	store.turns["ses_b"] = []transcript.Turn{second, secondReply, secondAgain}
	failing := map[string]bool{"ses_b": true}
	runs := 0
	runner := func(_ context.Context, _ SemanticHarness, prompt []byte, _ []byte) (ExperienceSemanticHarnessResult, error) {
		runs++
		if failing["ses_b"] && strings.Contains(string(prompt), "flaky") {
			return ExperienceSemanticHarnessResult{}, errors.New("harness busy")
		}
		return ExperienceSemanticHarnessResult{Output: habitTestOutput(), Model: "claude-test"}, nil
	}
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	service, _ := NewHabitDebriefService(store,
		WithHabitDebriefRunner(runner),
		WithHabitDebriefHarnessLookup(func(string) (SemanticHarness, bool) { return SemanticHarnessClaude, true }),
		WithHabitDebriefClock(func() time.Time { return now }),
	)
	failures := map[string]time.Time{}
	if generated, err := service.PrecomputeOnce(context.Background(), 3, failures); generated || err == nil {
		t.Fatalf("first pass should fail on the newest session: generated=%v err=%v", generated, err)
	}
	if _, ok := failures["ses_b"]; !ok || runs != 1 {
		t.Fatalf("failure should be recorded: %v runs=%d", failures, runs)
	}
	if generated, err := service.PrecomputeOnce(context.Background(), 3, failures); !generated || err != nil {
		t.Fatalf("second pass should skip the failed session and write the next one: generated=%v err=%v", generated, err)
	}
	if _, ok := store.debriefs["ses_a"]; !ok || runs != 2 {
		t.Fatalf("expected ses_a debrief after backoff skip: %v runs=%d", store.debriefs, runs)
	}
	if generated, err := service.PrecomputeOnce(context.Background(), 3, failures); generated || err != nil || runs != 2 {
		t.Fatalf("nothing eligible should remain inside the backoff: generated=%v err=%v runs=%d", generated, err, runs)
	}
	failing["ses_b"] = false
	now = now.Add(2 * time.Hour)
	if generated, err := service.PrecomputeOnce(context.Background(), 3, failures); !generated || err != nil {
		t.Fatalf("after the backoff the failed session should be retried: generated=%v err=%v", generated, err)
	}
	if _, ok := failures["ses_b"]; ok {
		t.Fatal("a successful retry should clear the failure record")
	}
	unavailable, _ := NewHabitDebriefService(store,
		WithHabitDebriefHarnessLookup(func(string) (SemanticHarness, bool) { return "", false }),
	)
	if generated, err := unavailable.PrecomputeOnce(context.Background(), 3, failures); generated || err != nil {
		t.Fatalf("no harness must mean a quiet no-op: generated=%v err=%v", generated, err)
	}
}
