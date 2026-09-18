package local

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionOutcomeRequiresTerminalEvidence(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "belay.sqlite"), newMemoryKeyProvider())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	base := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		sessionID       string
		terminalOutcome string
		wantOutcome     string
		wantSource      string
		wantExplanation string
	}{
		{
			sessionID:       "session-incomplete",
			wantOutcome:     "incomplete",
			wantSource:      "absence_of_session_end",
			wantExplanation: "The agent reported that the session ended but did not report an outcome.",
		},
		{
			sessionID:       "session-succeeded",
			terminalOutcome: "succeeded",
			wantOutcome:     "succeeded",
			wantSource:      "session.end",
			wantExplanation: "The agent reported that this session completed successfully.",
		},
		{
			sessionID:       "session-failed",
			terminalOutcome: "failed",
			wantOutcome:     "failed",
			wantSource:      "session.end",
			wantExplanation: "The agent reported that this session ended with a failure.",
		},
		{
			sessionID:       "session-interrupted",
			terminalOutcome: "interrupted",
			wantOutcome:     "interrupted",
			wantSource:      "session.end",
			wantExplanation: "The agent reported that this session was interrupted.",
		},
		{
			sessionID:       "session-unknown",
			terminalOutcome: "unknown",
			wantOutcome:     "unknown",
			wantSource:      "session.end",
			wantExplanation: "The agent did not report how this session ended.",
		},
	}

	unique := 1
	for index, test := range tests {
		appendSessionOutcomeEvent(t, ctx, store, test.sessionID, unique, 1, base.Add(time.Duration(index)*time.Minute), "session.start", "unknown")
		unique++
		// A successful intermediate action must not be mistaken for successful
		// session completion.
		appendSessionOutcomeEvent(t, ctx, store, test.sessionID, unique, 2, base.Add(time.Duration(index)*time.Minute+time.Second), "command.result", "succeeded")
		unique++
		if test.terminalOutcome != "" {
			appendSessionOutcomeEvent(t, ctx, store, test.sessionID, unique, 3, base.Add(time.Duration(index)*time.Minute+2*time.Second), "session.end", test.terminalOutcome)
			unique++
		}
	}

	sessions, _, err := store.ListSessions(ctx, 100)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	got := make(map[string]string, len(sessions))
	for _, session := range sessions {
		got[session.SessionID] = session.Outcome
	}
	for _, test := range tests {
		if got[test.sessionID] != test.wantOutcome {
			t.Errorf("%s outcome = %q, want %q", test.sessionID, got[test.sessionID], test.wantOutcome)
		}
		detail, _, err := store.GetSession(ctx, test.sessionID)
		if err != nil {
			t.Fatalf("GetSession(%q) error = %v", test.sessionID, err)
		}
		if detail.Outcome != test.wantOutcome {
			t.Errorf("GetSession(%q) outcome = %q, want %q", test.sessionID, detail.Outcome, test.wantOutcome)
		}
		if detail.Overview == nil {
			t.Fatalf("GetSession(%q) overview is nil", test.sessionID)
		}
		if detail.Overview.Outcome.Value != test.wantOutcome ||
			detail.Overview.Outcome.Source != test.wantSource ||
			detail.Overview.Outcome.Explanation != test.wantExplanation {
			t.Errorf("GetSession(%q) outcome explanation = %+v, want value=%q source=%q explanation=%q",
				test.sessionID,
				detail.Overview.Outcome,
				test.wantOutcome,
				test.wantSource,
				test.wantExplanation,
			)
		}
	}

	fallback := outcomeExplanation("PRIVATE_UNKNOWN_OUTCOME")
	if fallback.Value != "unknown" ||
		fallback.Source != "session.end" ||
		fallback.Explanation != "The agent reported that the session ended but did not report an outcome." {
		t.Fatalf("fallback outcome explanation = %+v", fallback)
	}
}

func appendSessionOutcomeEvent(
	t *testing.T,
	ctx context.Context,
	store *Store,
	sessionID string,
	unique int,
	sequence int64,
	occurredAt time.Time,
	eventType string,
	outcome string,
) {
	t.Helper()
	eventID := fmt.Sprintf("00000000-0000-7000-8000-%012d", unique)
	event := storageTestEvent(eventID, sessionID, sequence, occurredAt)
	event.Source.RecordID = fmt.Sprintf("source-%d", unique)
	event.Source.DeduplicationKey = fmt.Sprintf("sha256:%064x", unique)
	event.Observation.Type = eventType
	event.Observation.Outcome = outcome
	if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
		t.Fatalf("AppendEvent(%q, %q) = (%t, %v), want inserted", sessionID, eventType, inserted, err)
	}
}
