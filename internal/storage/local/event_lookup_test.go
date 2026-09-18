package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestLookupSessionEventsIsBoundedExactOrderedAndEncrypted(t *testing.T) {
	const payloadCanary = "LOOKUP_PAYLOAD_CANARY_71bd"
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 8, 21, 0, 0, 0, time.UTC)
	first := storageTestEvent(
		"00000000-0000-7000-8000-000000000911",
		"session-event-lookup",
		1,
		base,
	)
	first.Observation.Summary = payloadCanary
	second := storageTestEvent(
		"00000000-0000-7000-8000-000000000912",
		first.Session.Key,
		2,
		base.Add(time.Second),
	)
	crossSession := storageTestEvent(
		"00000000-0000-7000-8000-000000000913",
		"session-event-lookup-other",
		1,
		base.Add(2*time.Second),
	)
	for _, event := range []model.Event{second, first, crossSession} {
		if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
			t.Fatalf("AppendEvent(%q) = (%t, %v)", event.EventID, inserted, err)
		}
	}
	var encrypted []byte
	if err := store.db.QueryRowContext(ctx,
		"SELECT canonical_json FROM events WHERE event_id = ?",
		first.EventID,
	).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(payloadCanary)) {
		t.Fatal("stored event lookup payload is not encrypted")
	}

	missing := "00000000-0000-7000-8000-000000000914"
	result, err := store.LookupSessionEvents(ctx, model.EventLookupQuery{
		SessionID: first.Session.Key,
		EventIDs: []string{
			second.EventID,
			first.EventID,
			second.EventID,
			missing,
			crossSession.EventID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestedCount != 4 ||
		result.FoundCount != 2 ||
		result.MissingCount != 2 ||
		len(result.Data) != 2 {
		t.Fatalf("lookup result = %+v", result)
	}
	if result.Data[0].EventID != first.EventID ||
		result.Data[1].EventID != second.EventID {
		t.Fatalf("lookup order = %q, %q", result.Data[0].EventID, result.Data[1].EventID)
	}
	if result.Data[0].Observation.Summary != payloadCanary {
		t.Fatalf("decrypted event summary = %q", result.Data[0].Observation.Summary)
	}
	if len(result.MissingEventIDs) != 2 ||
		result.MissingEventIDs[0] != missing ||
		result.MissingEventIDs[1] != crossSession.EventID {
		t.Fatalf("missing IDs = %v", result.MissingEventIDs)
	}
	if !result.DataThrough.Equal(crossSession.ObservedAt) {
		t.Fatalf("data through = %s, want %s", result.DataThrough, crossSession.ObservedAt)
	}
}

func TestLookupSessionEventsRejectsInvalidAndOversizedRequests(t *testing.T) {
	store := openStorageTestStore(t)
	ctx := context.Background()
	invalidID := "PRIVATE_INVALID_EVENT_ID"
	tests := []model.EventLookupQuery{
		{},
		{SessionID: "session", EventIDs: []string{}},
		{
			SessionID: strings.Repeat("s", 257),
			EventIDs:  []string{"00000000-0000-7000-8000-000000000915"},
		},
		{SessionID: "session", EventIDs: []string{invalidID}},
	}
	for _, query := range tests {
		if _, err := store.LookupSessionEvents(ctx, query); err == nil {
			t.Fatalf("invalid query accepted: %+v", query)
		} else if strings.Contains(err.Error(), invalidID) {
			t.Fatalf("event lookup error reflected input: %v", err)
		}
	}

	oversized := make([]string, model.MaxEventLookupIDs+1)
	for index := range oversized {
		oversized[index] = fmt.Sprintf(
			"00000000-0000-7000-8000-%012x",
			index,
		)
	}
	if _, err := store.LookupSessionEvents(ctx, model.EventLookupQuery{
		SessionID: "session",
		EventIDs:  oversized,
	}); err == nil {
		t.Fatal("oversized event lookup accepted")
	}

	missingOnly, err := store.LookupSessionEvents(ctx, model.EventLookupQuery{
		SessionID: "session",
		EventIDs:  []string{"00000000-0000-7000-8000-000000000916"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if missingOnly.Data == nil ||
		missingOnly.MissingEventIDs == nil ||
		missingOnly.FoundCount != 0 ||
		missingOnly.MissingCount != 1 {
		t.Fatalf("missing-only lookup = %+v", missingOnly)
	}
}

func TestVisitSessionEventsStreamsInCanonicalOrderAndStopsOnVisitorError(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	base := time.Date(2026, 9, 8, 22, 0, 0, 0, time.UTC)
	events := []model.Event{
		storageTestEvent("00000000-0000-7000-8000-000000000921", "visitor-session", 2, base.Add(time.Second)),
		storageTestEvent("00000000-0000-7000-8000-000000000920", "visitor-session", 1, base),
	}
	for _, event := range events {
		if inserted, err := store.AppendEvent(ctx, event); err != nil || !inserted {
			t.Fatalf("AppendEvent() = %t, %v", inserted, err)
		}
	}
	var visited []string
	summary, err := store.VisitSessionEvents(ctx, model.EventLookupQuery{
		SessionID: "visitor-session",
		EventIDs:  []string{events[0].EventID, events[1].EventID},
	}, func(event model.Event) error {
		visited = append(visited, event.EventID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.FoundCount != 2 ||
		len(visited) != 2 ||
		visited[0] != events[1].EventID ||
		visited[1] != events[0].EventID {
		t.Fatalf("visitor order/summary = %v/%+v", visited, summary)
	}

	stop := errors.New("stop visitor")
	calls := 0
	if _, err := store.VisitSessionEvents(ctx, model.EventLookupQuery{
		SessionID: "visitor-session",
		EventIDs:  []string{events[0].EventID, events[1].EventID},
	}, func(model.Event) error {
		calls++
		return stop
	}); !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("visitor stop = calls %d, error %v", calls, err)
	}
}
