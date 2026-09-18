package local

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestLegacyCommandCanonicalJSONIsSanitizedAtEveryReadBoundary(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC)
	sessionID := "session-legacy-command-sanitization"
	tests := []struct {
		name         string
		eventID      string
		eventType    string
		summary      string
		resourceName string
		wantSummary  string
		wantResource string
		canary       string
	}{
		{
			name:         "environment secret",
			eventID:      "00000000-0000-7000-8000-000000001401",
			eventType:    "command.exec",
			summary:      "AWS_SECRET_ACCESS_KEY=LEGACY_ENV_SECRET go test ./...",
			resourceName: "AWS_SECRET_ACCESS_KEY=LEGACY_RESOURCE_SECRET",
			canary:       "LEGACY_ENV_SECRET",
		},
		{
			name:         "unknown executable",
			eventID:      "00000000-0000-7000-8000-000000001402",
			eventType:    "command.exec",
			summary:      "private-secret-tool --help",
			resourceName: "private-secret-tool",
			canary:       "private-secret-tool",
		},
		{
			name:         "shell syntax",
			eventID:      "00000000-0000-7000-8000-000000001403",
			eventType:    "command.result",
			summary:      "go test ./...; echo LEGACY_SHELL_SECRET",
			resourceName: "go;echo",
			canary:       "LEGACY_SHELL_SECRET",
		},
		{
			name:         "unsafe option omitted",
			eventID:      "00000000-0000-7000-8000-000000001404",
			eventType:    "command.result",
			summary:      "go test --password=LEGACY_OPTION_SECRET --help",
			resourceName: "LEGACY_RESOURCE_SECRET",
			wantSummary:  "go --help",
			wantResource: "go",
			canary:       "LEGACY_OPTION_SECRET",
		},
		{
			name:         "valid safe legacy summary",
			eventID:      "00000000-0000-7000-8000-000000001405",
			eventType:    "command.exec",
			summary:      "go --count -race",
			resourceName: "LEGACY_MISMATCHED_RESOURCE_SECRET",
			wantSummary:  "go --count -race",
			wantResource: "go",
			canary:       "LEGACY_MISMATCHED_RESOURCE_SECRET",
		},
		{
			name:         "unsafe resource only",
			eventID:      "00000000-0000-7000-8000-000000001406",
			eventType:    "command.exec",
			resourceName: "TOKEN=LEGACY_RESOURCE_ONLY_SECRET",
			canary:       "LEGACY_RESOURCE_ONLY_SECRET",
		},
	}

	encryptedBefore := make(map[string][]byte, len(tests))
	eventIDs := make([]string, 0, len(tests))
	for index, test := range tests {
		event := storageTestEvent(
			test.eventID,
			sessionID,
			int64(index+1),
			base.Add(time.Duration(index)*time.Second),
		)
		event.Observation.Type = test.eventType
		event.Observation.Action = "command"
		event.Observation.Summary = test.summary
		event.Observation.Resource = &model.Resource{
			Kind: "command",
			Name: test.resourceName,
		}
		encryptedBefore[test.eventID] = seedLegacyCanonicalEvent(t, store, event)
		eventIDs = append(eventIDs, test.eventID)
	}

	timeline, err := store.QuerySessionTimeline(ctx, model.TimelineQuery{
		SessionID: sessionID,
		Limit:     len(tests),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyCommandReadResults(t, "timeline", timeline.Data, tests)

	activity, err := store.QueryActivityPage(ctx, model.ActivityQuery{
		Filter: model.ActivityFilter{Limit: len(tests)},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyCommandReadResults(t, "activity", activity.Data, tests)

	lookup, err := store.LookupSessionEvents(ctx, model.EventLookupQuery{
		SessionID: sessionID,
		EventIDs:  eventIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyCommandReadResults(t, "lookup", lookup.Data, tests)

	for _, test := range tests {
		var encryptedAfter []byte
		if err := store.db.QueryRowContext(ctx, `
			SELECT canonical_json FROM events WHERE event_id = ?`,
			test.eventID,
		).Scan(&encryptedAfter); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encryptedAfter, encryptedBefore[test.eventID]) {
			t.Fatalf("%s canonical payload was rewritten during read", test.name)
		}
	}
}

func seedLegacyCanonicalEvent(
	t *testing.T,
	store *Store,
	event model.Event,
) []byte {
	t.Helper()
	ctx := context.Background()
	base := event
	base.Observation.Summary = ""
	base.Observation.Resource = nil
	if inserted, err := store.AppendEvent(ctx, base); err != nil || !inserted {
		t.Fatalf("append legacy seed event = %t, %v", inserted, err)
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := store.cipher.seal(
		"event",
		event.EventID,
		"canonical_json",
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := withMutationTx(ctx, tx, mutationPayloadUpgrade, func() error {
		_, err := tx.ExecContext(ctx, `
			UPDATE events
			SET canonical_json = ?, canonical_encoding = ?
			WHERE event_id = ?`,
			envelope,
			payloadEncodingAESGCM,
			event.EventID,
		)
		return err
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), envelope...)
}

func assertLegacyCommandReadResults(
	t *testing.T,
	surface string,
	events []model.Event,
	tests []struct {
		name         string
		eventID      string
		eventType    string
		summary      string
		resourceName string
		wantSummary  string
		wantResource string
		canary       string
	},
) {
	t.Helper()
	if len(events) != len(tests) {
		t.Fatalf("%s event count = %d, want %d", surface, len(events), len(tests))
	}
	byID := make(map[string]model.Event, len(events))
	for _, event := range events {
		byID[event.EventID] = event
	}
	for _, test := range tests {
		event, ok := byID[test.eventID]
		if !ok {
			t.Fatalf("%s missing event %s", surface, test.eventID)
		}
		if event.Observation.Summary != test.wantSummary {
			t.Fatalf(
				"%s %s summary = %q, want %q",
				surface,
				test.name,
				event.Observation.Summary,
				test.wantSummary,
			)
		}
		if test.wantResource == "" {
			if event.Observation.Resource != nil {
				t.Fatalf(
					"%s %s resource = %+v, want neutral",
					surface,
					test.name,
					event.Observation.Resource,
				)
			}
		} else if event.Observation.Resource == nil ||
			event.Observation.Resource.Kind != "command" ||
			event.Observation.Resource.Name != test.wantResource {
			t.Fatalf(
				"%s %s resource = %+v, want command/%s",
				surface,
				test.name,
				event.Observation.Resource,
				test.wantResource,
			)
		}
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		for _, prohibited := range []string{
			test.canary,
			test.resourceName,
		} {
			if prohibited != "" && bytes.Contains(body, []byte(prohibited)) {
				t.Fatalf(
					"%s %s exposed prohibited legacy bytes %q",
					surface,
					test.name,
					prohibited,
				)
			}
		}
	}
}
