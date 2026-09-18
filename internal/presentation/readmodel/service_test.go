package readmodel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestSessionCursorUsesStableSnapshotAcrossLateEvents(t *testing.T) {
	ctx := context.Background()
	store := openReadStore(t)
	service := readmodel.New(store)
	tied := readTestTime()

	appendReadEvent(t, store, readEvent("event-a", "session-a", 1, tied, "session.end", "succeeded"))
	appendReadEvent(t, store, readEvent("event-b", "session-b", 1, tied, "session.end", "succeeded"))
	appendReadEvent(t, store, readEvent("event-c", "session-c", 1, tied.Add(-time.Minute), "session.end", "succeeded"))

	first, err := service.ListSessionsPage(ctx, readmodel.SessionListRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(first.Data); !slices.Equal(got, []string{"session-a"}) {
		t.Fatalf("first page = %v, want session-a", got)
	}
	if first.NextCursor == nil || !first.HasMore {
		t.Fatal("first page did not expose a continuation cursor")
	}

	// These inserts would sort ahead of the remaining rows without the
	// ingestion snapshot carried by the cursor.
	appendReadEvent(t, store, readEvent("event-b-late", "session-b", 2, tied.Add(time.Hour), "tool.call", "unknown"))
	appendReadEvent(t, store, readEvent("event-d", "session-d", 1, tied.Add(2*time.Hour), "session.end", "failed"))

	second, err := service.ListSessionsPage(ctx, readmodel.SessionListRequest{
		Limit:  1,
		Cursor: *first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(second.Data); !slices.Equal(got, []string{"session-b"}) {
		t.Fatalf("second page = %v, want snapshot-stable session-b", got)
	}
	if second.DataThrough != first.DataThrough {
		t.Fatalf("data_through changed across one cursor snapshot: %s -> %s",
			first.DataThrough, second.DataThrough)
	}

	third, err := service.ListSessionsPage(ctx, readmodel.SessionListRequest{
		Limit:  1,
		Cursor: *second.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionIDs(third.Data); !slices.Equal(got, []string{"session-c"}) {
		t.Fatalf("third page = %v, want session-c", got)
	}
	if third.HasMore || third.NextCursor != nil {
		t.Fatal("final snapshot page falsely reports more rows")
	}
}

func TestSessionFiltersAndMalformedCursor(t *testing.T) {
	ctx := context.Background()
	store := openReadStore(t)
	service := readmodel.New(store)
	base := readTestTime()

	historical := readEvent("historical-end", "session-historical", 1, base, "session.end", "succeeded")
	historical.Source.Agent = "codex"
	historical.Historical.IsHistorical = true
	appendReadEvent(t, store, historical)

	live := readEvent("live-start", "session-live", 1, base.Add(time.Hour), "session.start", "unknown")
	live.Source.Agent = "claude"
	live.Historical.IsHistorical = false
	live.Historical.ReconstructionSource = ""
	appendReadEvent(t, store, live)

	mixedStart := readEvent("mixed-start", "session-mixed", 1, base.Add(2*time.Hour), "session.start", "unknown")
	mixedStart.Historical.IsHistorical = true
	appendReadEvent(t, store, mixedStart)
	mixedEnd := readEvent("mixed-end", "session-mixed", 2, base.Add(3*time.Hour), "session.end", "failed")
	mixedEnd.Historical.IsHistorical = false
	mixedEnd.Historical.ReconstructionSource = ""
	appendReadEvent(t, store, mixedEnd)

	tests := []struct {
		name    string
		request readmodel.SessionListRequest
		want    []string
	}{
		{"harness", readmodel.SessionListRequest{Harness: "CLAUDE"}, []string{"session-live"}},
		{"raw outcome", readmodel.SessionListRequest{Outcome: "incomplete"}, []string{"session-live"}},
		{"history", readmodel.SessionListRequest{History: "mixed"}, []string{"session-mixed"}},
		{"safe query", readmodel.SessionListRequest{Query: "HISTORICAL"}, []string{"session-historical"}},
		{"overlap range", readmodel.SessionListRequest{
			OccurredAfter:  timePointer(base.Add(90 * time.Minute)),
			OccurredBefore: timePointer(base.Add(4 * time.Hour)),
		}, []string{"session-mixed"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := service.ListSessionsPage(ctx, test.request)
			if err != nil {
				t.Fatal(err)
			}
			if got := sessionIDs(response.Data); !slices.Equal(got, test.want) {
				t.Fatalf("sessions = %v, want %v", got, test.want)
			}
		})
	}

	_, err := service.ListSessionsPage(ctx, readmodel.SessionListRequest{Cursor: "not-a-cursor"})
	if !errors.Is(err, readmodel.ErrInvalidCursor) {
		t.Fatalf("malformed cursor error = %v, want ErrInvalidCursor", err)
	}

	first, err := service.ListSessionsPage(ctx, readmodel.SessionListRequest{Limit: 1})
	if err != nil || first.NextCursor == nil {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	_, err = service.ListSessionsPage(ctx, readmodel.SessionListRequest{
		Limit:   1,
		Cursor:  *first.NextCursor,
		Harness: "codex",
	})
	if !errors.Is(err, readmodel.ErrInvalidCursor) {
		t.Fatalf("cursor reused with changed filters = %v, want ErrInvalidCursor", err)
	}
}

func TestTimelineCursorUsesDeterministicTieBreakAndSnapshot(t *testing.T) {
	ctx := context.Background()
	store := openReadStore(t)
	service := readmodel.New(store)
	sessionID := "session-timeline"
	at := readTestTime()
	appendReadEvent(t, store, readEvent("event-a", sessionID, 1, at, "tool.call", "unknown"))
	appendReadEvent(t, store, readEvent("event-b", sessionID, 1, at, "tool.result", "unknown"))
	appendReadEvent(t, store, readEvent("event-c", sessionID, 2, at.Add(time.Second), "session.end", "succeeded"))

	first, err := service.GetSessionTimelinePage(ctx, readmodel.TimelineRequest{
		SessionID: sessionID,
		Limit:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 1 || first.Data[0].EventID != "event-a" ||
		first.NextCursor == nil {
		t.Fatalf("first timeline page = %+v", first)
	}

	appendReadEvent(t, store, readEvent("event-0-late", sessionID, 1, at, "file.read", "unknown"))
	second, err := service.GetSessionTimelinePage(ctx, readmodel.TimelineRequest{
		SessionID: sessionID,
		Limit:     1,
		Cursor:    *first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 || second.Data[0].EventID != "event-b" {
		t.Fatalf("second timeline page = %+v, want snapshot-stable event-b", second)
	}
}

func TestSessionOverviewIsTruthfulBoundedAndMetadataOnly(t *testing.T) {
	ctx := context.Background()
	store := openReadStore(t)
	service := readmodel.New(store)
	sessionID := "session-overview"
	base := readTestTime()
	const prohibited = "PROMPT_COMPLETION_FILE_CONTENT_CANARY"

	events := []model.Event{
		readEvent("overview-command", sessionID, 1, base, "command.exec", "unknown"),
		readEvent("overview-command-result", sessionID, 2, base.Add(time.Second), "command.result", "failed"),
		readEvent("overview-tool", sessionID, 3, base.Add(2*time.Second), "tool.call", "unknown"),
		readEvent("overview-file-read", sessionID, 4, base.Add(3*time.Second), "file.read", "unknown"),
		readEvent("overview-file-write", sessionID, 5, base.Add(4*time.Second), "file.write", "unknown"),
		readEvent("overview-file-delete", sessionID, 6, base.Add(5*time.Second), "file.delete", "unknown"),
		readEvent("overview-network", sessionID, 7, base.Add(6*time.Second), "network.indicator", "unknown"),
		readEvent("overview-permission-request", sessionID, 8, base.Add(7*time.Second), "permission.requested", "unknown"),
		readEvent("overview-permission-approved", sessionID, 9, base.Add(8*time.Second), "permission.approved", "succeeded"),
		readEvent("overview-permission-denied", sessionID, 10, base.Add(9*time.Second), "permission.denied", "failed"),
	}
	events[0].Observation.Resource = &model.Resource{Kind: "command", Name: "go"}
	events[0].Observation.Summary = prohibited
	events[2].Observation.Resource = &model.Resource{Kind: "tool", Name: "read_file"}
	events[3].Observation.Resource = &model.Resource{Kind: "file", Name: "src/main.go"}
	events[4].Observation.Resource = &model.Resource{Kind: "file", Name: "src/main.go"}
	events[5].Observation.Resource = &model.Resource{Kind: "file", Name: "old.go"}
	events[6].Observation.Resource = &model.Resource{Kind: "network", Name: "https://example.test"}
	events[6].Coverage = model.Coverage{Depth: "tool_call", Confidence: "medium"}
	for _, event := range events {
		appendReadEvent(t, store, event)
	}
	if inserted, err := store.RecordFinding(ctx, local.Finding{
		FindingID:     "finding-overview",
		SourceRunID:   events[0].Source.RunID,
		SessionKey:    sessionID,
		DetectedAt:    base.Add(10 * time.Second),
		RuleID:        "rule-safe",
		RuleVersion:   "1",
		Severity:      "medium",
		SourceAgent:   "codex",
		Confidence:    "high",
		CitedEventIDs: []string{events[0].EventID},
	}); err != nil || !inserted {
		t.Fatalf("RecordFinding() = (%t, %v)", inserted, err)
	}

	detail, err := service.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	overview := detail.Data.Overview
	if overview == nil {
		t.Fatal("session detail has no overview")
	}
	wantCounts := model.SessionInsightCounts{
		Commands:                 1,
		ToolCalls:                1,
		FileReads:                1,
		FileWrites:               1,
		FileDeletes:              1,
		NetworkIndicators:        1,
		PermissionEvents:         3,
		ExplicitFailedEvents:     2,
		SourceUnreportedOutcomes: 7,
		Findings:                 1,
	}
	if overview.Counts != wantCounts {
		t.Fatalf("overview counts = %+v, want %+v", overview.Counts, wantCounts)
	}
	if detail.Data.Outcome != "incomplete" ||
		overview.Outcome.Source != "absence_of_session_end" ||
		overview.Outcome.Explanation != "The agent reported that the session ended but did not report an outcome." {
		t.Fatalf("incomplete outcome explanation = %+v", overview.Outcome)
	}
	if !slices.Equal(overview.ObservedCoverage.Depths, []string{"artifact", "tool_call"}) ||
		!slices.Equal(overview.ObservedCoverage.Confidences, []string{"high", "medium"}) {
		t.Fatalf("observed coverage = %+v", overview.ObservedCoverage)
	}
	if len(overview.SalientResources) == 0 ||
		overview.SalientResources[0] != (model.SalientResource{
			Kind: "file", Name: "src/main.go", EventCount: 2,
		}) {
		t.Fatalf("salient resources = %+v", overview.SalientResources)
	}
	body, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(prohibited)) {
		t.Fatal("session overview leaked event summary content")
	}

	unknownEnd := readEvent(
		"unknown-end",
		"session-unknown-end",
		1,
		base.Add(time.Hour),
		"session.end",
		"unknown",
	)
	appendReadEvent(t, store, unknownEnd)
	unknownDetail, err := service.GetSession(ctx, unknownEnd.Session.Key)
	if err != nil {
		t.Fatal(err)
	}
	if unknownDetail.Data.Overview == nil ||
		unknownDetail.Data.Overview.Outcome.Source != "session.end" ||
		unknownDetail.Data.Overview.Outcome.Value != "unknown" {
		t.Fatalf("unknown terminal explanation = %+v", unknownDetail.Data.Overview)
	}
}

func TestSessionOverviewBoundsSalientResources(t *testing.T) {
	store := openReadStore(t)
	sessionID := "session-many-resources"
	base := readTestTime()
	for index := 0; index < 25; index++ {
		event := readEvent(
			fmt.Sprintf("resource-%02d", index),
			sessionID,
			int64(index+1),
			base.Add(time.Duration(index)*time.Second),
			"file.read",
			"unknown",
		)
		event.Observation.Resource = &model.Resource{
			Kind: "file",
			Name: fmt.Sprintf("file-%02d.go", index),
		}
		appendReadEvent(t, store, event)
	}
	detail, err := readmodel.New(store).GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(detail.Data.Overview.SalientResources); got != 20 {
		t.Fatalf("salient resources = %d, want 20", got)
	}
	if !detail.Data.Overview.SalientResourcesTruncated {
		t.Fatal("resource truncation was not disclosed")
	}
}

func TestActivityResourceFilterScansPastNonMatchingWindow(t *testing.T) {
	store := openReadStore(t)
	base := readTestTime()
	match := readEvent("old-file-match", "session-activity", 1, base, "file.read", "unknown")
	match.Observation.Resource = &model.Resource{Kind: "file", Name: "safe.go"}
	appendReadEvent(t, store, match)
	for index := 0; index < 300; index++ {
		appendReadEvent(t, store, readEvent(
			fmt.Sprintf("newer-%03d", index),
			fmt.Sprintf("session-newer-%03d", index),
			1,
			base.Add(time.Duration(index+1)*time.Second),
			"tool.call",
			"unknown",
		))
	}

	response, err := readmodel.New(store).QueryActivityPage(
		context.Background(),
		readmodel.ActivityRequest{
			Filter: model.ActivityFilter{ResourceKind: "file", Limit: 1},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].EventID != match.EventID {
		t.Fatalf("resource-filtered activity = %+v, want old file match", response.Data)
	}
	if response.HasMore || response.NextCursor != nil {
		t.Fatal("single exhaustive resource match reported truncation")
	}
}

func TestFindingPaginationIsCompleteFilteredAndSnapshotStable(t *testing.T) {
	ctx := context.Background()
	store := openReadStore(t)
	service := readmodel.New(store)
	base := readTestTime()
	event := readEvent("finding-citation", "session-findings", 1, base, "tool.call", "unknown")
	otherEvent := readEvent("other-finding-citation", "session-other", 1, base, "tool.call", "unknown")
	appendReadEvent(t, store, event)
	appendReadEvent(t, store, otherEvent)

	recordReadFinding(t, store, event, "finding-a", base.Add(time.Minute), "low")
	recordReadFinding(t, store, event, "finding-b", base.Add(time.Minute), "medium")
	recordReadFinding(t, store, event, "finding-c", base.Add(time.Minute), "medium")
	for index := 0; index < 125; index++ {
		recordReadFinding(
			t,
			store,
			otherEvent,
			fmt.Sprintf("finding-newer-other-%03d", index),
			base.Add(2*time.Hour+time.Duration(index)*time.Second),
			"medium",
		)
	}

	first, err := service.ListFindingsPage(ctx, readmodel.FindingListRequest{
		Limit:     1,
		Severity:  "medium",
		SessionID: event.Session.Key,
		Since:     timePointer(base),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Data) != 1 || first.Data[0].FindingID != "finding-c" ||
		!first.HasMore || first.NextCursor == nil {
		t.Fatalf("first finding page = %+v", first)
	}

	recordReadFinding(t, store, event, "finding-z-late", base.Add(time.Hour), "medium")
	second, err := service.ListFindingsPage(ctx, readmodel.FindingListRequest{
		Limit:     1,
		Cursor:    *first.NextCursor,
		Severity:  "medium",
		SessionID: event.Session.Key,
		Since:     timePointer(base),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 || second.Data[0].FindingID != "finding-b" {
		t.Fatalf("second finding page = %+v, want snapshot-stable finding-b", second)
	}
	if second.HasMore || second.NextCursor != nil {
		t.Fatal("complete filtered finding page falsely reports more rows")
	}

	_, err = service.ListFindingsPage(ctx, readmodel.FindingListRequest{
		Limit:     1,
		Cursor:    *first.NextCursor,
		Severity:  "medium",
		SessionID: otherEvent.Session.Key,
		Since:     timePointer(base),
	})
	if !errors.Is(err, readmodel.ErrInvalidCursor) {
		t.Fatalf("cursor reused with changed session filter = %v, want ErrInvalidCursor", err)
	}

	_, err = service.ListFindingsPage(ctx, readmodel.FindingListRequest{
		SessionID: strings.Repeat("x", 257),
	})
	if !errors.Is(err, readmodel.ErrInvalidRequest) {
		t.Fatalf("oversized session filter = %v, want ErrInvalidRequest", err)
	}
}

func openReadStore(t *testing.T) *local.Store {
	t.Helper()
	store, err := local.Open(filepath.Join(t.TempDir(), "belay.sqlite"), &readKeyProvider{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type readKeyProvider struct {
	key []byte
}

func (provider *readKeyProvider) Load(context.Context, string) ([]byte, error) {
	if len(provider.key) == 0 {
		return nil, local.ErrKeyNotFound
	}
	return append([]byte(nil), provider.key...), nil
}

func (provider *readKeyProvider) Create(context.Context, string) ([]byte, error) {
	if len(provider.key) != 0 {
		return nil, local.ErrKeyAlreadyExists
	}
	provider.key = bytes.Repeat([]byte{0x5a}, 32)
	return append([]byte(nil), provider.key...), nil
}

func readEvent(
	eventID string,
	sessionID string,
	sequence int64,
	occurredAt time.Time,
	eventType string,
	outcome string,
) model.Event {
	return model.Event{
		SchemaVersion:  model.EventSchemaVersion,
		EventID:        eventID,
		InstallationID: "inst_read_test",
		OccurredAt:     occurredAt.UTC(),
		ObservedAt:     occurredAt.Add(time.Second).UTC(),
		Source: model.Source{
			Engine:           "numbat",
			EngineVersion:    "0.3.0-test",
			SchemaVersion:    "0.3.0",
			RecordType:       "event",
			RunID:            "run-read-test",
			RecordID:         "source-" + eventID,
			Kind:             "artifact",
			Agent:            "codex",
			AdapterVersion:   "numbat-0.3.0/v1",
			DeduplicationKey: "sha256:" + fmt.Sprintf("%064x", eventID),
			Sequence:         sequence,
		},
		Session: model.SessionRef{Key: sessionID},
		Observation: model.Observation{
			Type:    eventType,
			Actor:   "tool",
			Action:  strings.Split(eventType, ".")[0],
			Outcome: outcome,
		},
		Coverage: model.Coverage{Depth: "artifact", Confidence: "high"},
		Redaction: model.Redaction{
			PolicyVersion: model.RedactionVersion,
		},
		Historical: model.Historical{
			IsHistorical:         true,
			ReconstructionSource: "test",
		},
	}
}

func appendReadEvent(t *testing.T, store *local.Store, event model.Event) {
	t.Helper()
	inserted, err := store.AppendEvent(context.Background(), event)
	if err != nil || !inserted {
		t.Fatalf("AppendEvent(%q) = (%t, %v)", event.EventID, inserted, err)
	}
}

func recordReadFinding(
	t *testing.T,
	store *local.Store,
	event model.Event,
	findingID string,
	detectedAt time.Time,
	severity string,
) {
	t.Helper()
	inserted, err := store.RecordFinding(context.Background(), local.Finding{
		FindingID:     findingID,
		SourceRunID:   event.Source.RunID,
		SessionKey:    event.Session.Key,
		DetectedAt:    detectedAt,
		RuleID:        "rule-" + findingID,
		RuleVersion:   "1",
		Severity:      severity,
		SourceAgent:   event.Source.Agent,
		Confidence:    "high",
		CitedEventIDs: []string{event.EventID},
	})
	if err != nil || !inserted {
		t.Fatalf("RecordFinding(%q) = (%t, %v)", findingID, inserted, err)
	}
}

func sessionIDs(sessions []model.SessionSummary) []string {
	result := make([]string, len(sessions))
	for index, session := range sessions {
		result[index] = session.SessionID
	}
	return result
}

func readTestTime() time.Time {
	return time.Date(2026, time.September, 8, 12, 0, 0, 0, time.UTC)
}

func timePointer(value time.Time) *time.Time {
	return &value
}
