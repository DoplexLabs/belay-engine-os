package localhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestRealStoreIssueDefaultsSnapshotExpiryAndExactEventLookup(t *testing.T) {
	ctx := context.Background()
	readNow := time.Date(2026, 9, 8, 21, 0, 0, 0, time.UTC)
	storeNow := readNow
	store, err := local.OpenWithOptions(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		local.OpenOptions{
			KeyProvider: &httpIntegrationKeyProvider{},
			Clock:       func() time.Time { return storeNow },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first := httpIntegrationEvent(
		"01890f2e-6d4b-7c8a-9b0c-123456789a01",
		"session-http-integration",
		1,
		readNow.Add(-3*time.Minute),
	)
	second := httpIntegrationEvent(
		"01890f2e-6d4b-7c8a-9b0c-123456789a02",
		first.Session.Key,
		2,
		readNow.Add(-2*time.Minute),
	)
	crossSession := httpIntegrationEvent(
		"01890f2e-6d4b-7c8a-9b0c-123456789a03",
		"session-http-integration-other",
		1,
		readNow.Add(-time.Minute),
	)
	var generation int64
	for _, event := range []model.Event{second, first, crossSession} {
		result, err := store.AppendEventResolved(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		if event.Session.Key == first.Session.Key {
			generation = result.ReadGeneration
		}
	}

	stable := httpIntegrationOccurrence(
		t,
		store,
		first,
		"stable_command_failure",
		"command_failure",
		false,
	)
	experimental := httpIntegrationOccurrence(
		t,
		store,
		first,
		"repeated_command_attempts",
		"attention",
		true,
	)
	evidenceGap := httpIntegrationOccurrence(
		t,
		store,
		first,
		"verification_not_observed",
		"evidence_gap",
		false,
	)
	if _, err := store.ReplaceIssueProjection(ctx, local.ProjectionReplacement{
		SessionKey:        first.Session.Key,
		ClaimedGeneration: generation,
		Status:            model.AnalysisCurrent,
		ScopeQuality:      model.ScopeUnscoped,
		Occurrences:       []model.IssueOccurrence{stable, experimental, evidenceGap},
	}); err != nil {
		t.Fatal(err)
	}

	server, err := New(readmodel.New(
		store,
		readmodel.WithIssueRepository(store),
		readmodel.WithIssueCursorCodec(store),
		readmodel.WithClock(func() time.Time { return readNow }),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	requestJSON := func(path string, target any) int {
		t.Helper()
		request := issueHTTPRequest(http.MethodGet, "http://127.0.0.1"+path, true)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if target != nil {
			if err := json.NewDecoder(response.Body).Decode(target); err != nil {
				t.Fatalf("%s decode: %v; body=%s", path, err, response.Body.String())
			}
		}
		return response.Code
	}

	var defaultIssues readmodel.IssueList
	if status := requestJSON("/v1/issues", &defaultIssues); status != http.StatusOK {
		t.Fatalf("default issues status = %d", status)
	}
	if len(defaultIssues.Data) != 1 ||
		defaultIssues.Data[0].IssueID != stable.IssueID ||
		defaultIssues.Data[0].Experimental {
		t.Fatalf("migrated default issues = %+v", defaultIssues.Data)
	}

	var gaps readmodel.IssueList
	if status := requestJSON(
		"/v1/issues?attention_kind=evidence_gap",
		&gaps,
	); status != http.StatusOK {
		t.Fatalf("evidence gaps status = %d", status)
	}
	if len(gaps.Data) != 1 || gaps.Data[0].IssueID != evidenceGap.IssueID {
		t.Fatalf("evidence gaps = %+v", gaps.Data)
	}

	var inclusiveIssues readmodel.IssueList
	if status := requestJSON(
		"/v1/issues?attention_kind=issue&experimental=include",
		&inclusiveIssues,
	); status != http.StatusOK {
		t.Fatalf("inclusive issues status = %d", status)
	}
	if len(inclusiveIssues.Data) != 2 {
		t.Fatalf("inclusive issues = %+v", inclusiveIssues.Data)
	}
	inclusiveIDs := []string{
		inclusiveIssues.Data[0].IssueID,
		inclusiveIssues.Data[1].IssueID,
	}
	slices.Sort(inclusiveIDs)
	wantInclusive := []string{stable.IssueID, experimental.IssueID}
	slices.Sort(wantInclusive)
	if !slices.Equal(inclusiveIDs, wantInclusive) {
		t.Fatalf("inclusive issues = %+v", inclusiveIssues.Data)
	}

	storeNow = readNow.Add(16 * time.Minute)
	var expired problem
	if status := requestJSON(
		"/v1/issues/"+stable.IssueID+"/occurrences?view_cursor="+defaultIssues.ViewCursor,
		&expired,
	); status != http.StatusGone {
		t.Fatalf("expired detail status = %d problem=%+v", status, expired)
	}
	if expired.Type != "belay.local/cursor-expired" {
		t.Fatalf("expired detail problem = %+v", expired)
	}

	missing := "01890f2e-6d4b-7c8a-9b0c-123456789a04"
	var lookup readmodel.EventLookup
	if status := requestJSON(
		"/v1/sessions/"+first.Session.Key+"/events/lookup"+
			"?event_id="+second.EventID+
			"&event_id="+first.EventID+
			"&event_id="+second.EventID+
			"&event_id="+missing+
			"&event_id="+crossSession.EventID,
		&lookup,
	); status != http.StatusOK {
		t.Fatalf("exact lookup status = %d", status)
	}
	if lookup.RequestedCount != 4 ||
		lookup.FoundCount != 2 ||
		lookup.MissingCount != 2 ||
		len(lookup.Data) != 2 ||
		lookup.Data[0].EventID != first.EventID ||
		lookup.Data[1].EventID != second.EventID ||
		!slices.Equal(
			lookup.MissingEventIDs,
			[]string{missing, crossSession.EventID},
		) {
		t.Fatalf("exact lookup = %+v", lookup)
	}
}

type httpIntegrationKeyProvider struct {
	storeID string
	key     []byte
}

func (provider *httpIntegrationKeyProvider) Load(
	_ context.Context,
	storeID string,
) ([]byte, error) {
	if provider.storeID != storeID || len(provider.key) == 0 {
		return nil, local.ErrKeyNotFound
	}
	return append([]byte(nil), provider.key...), nil
}

func (provider *httpIntegrationKeyProvider) Create(
	_ context.Context,
	storeID string,
) ([]byte, error) {
	if len(provider.key) != 0 {
		return nil, local.ErrKeyAlreadyExists
	}
	provider.storeID = storeID
	provider.key = bytes.Repeat([]byte{0x42}, 32)
	return append([]byte(nil), provider.key...), nil
}

func httpIntegrationEvent(
	eventID string,
	sessionID string,
	sequence int64,
	occurredAt time.Time,
) model.Event {
	return model.Event{
		SchemaVersion:  model.EventSchemaVersion,
		EventID:        eventID,
		InstallationID: "inst_http_integration",
		OccurredAt:     occurredAt,
		ObservedAt:     occurredAt.Add(time.Second),
		Source: model.Source{
			Engine:           "numbat",
			EngineVersion:    "test",
			SchemaVersion:    "test",
			RecordType:       "event",
			RunID:            "run-http-integration",
			RecordID:         "record-" + eventID,
			Kind:             "artifact",
			Agent:            "codex",
			AdapterVersion:   "test",
			DeduplicationKey: "dedupe-" + eventID,
			Sequence:         sequence,
		},
		Session: model.SessionRef{Key: sessionID},
		Observation: model.Observation{
			Type:    "tool.result",
			Actor:   "tool",
			Action:  "command",
			Outcome: "unknown",
		},
		Coverage: model.Coverage{
			Depth:      "artifact",
			Confidence: "high",
		},
		Redaction:  model.Redaction{PolicyVersion: model.RedactionVersion},
		Historical: model.Historical{IsHistorical: true},
	}
}

func httpIntegrationOccurrence(
	t *testing.T,
	store *local.Store,
	event model.Event,
	detectorID string,
	category string,
	experimental bool,
) model.IssueOccurrence {
	t.Helper()
	fingerprintID, issueID, err := store.DeriveIssueIdentity(
		"1",
		detectorID,
		event.Session.Key,
		detectorID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return model.IssueOccurrence{
		IssueID:            issueID,
		FingerprintID:      fingerprintID,
		FingerprintVersion: "1",
		Origin:             "belay",
		SessionID:          event.Session.Key,
		Harness:            "codex",
		Provenance: model.DetectorProvenance{
			DetectorID:         detectorID,
			DetectorVersion:    "1.0.0",
			FingerprintVersion: "1",
			ProjectionVersion:  "belay.detectors.v1",
		},
		Category:           category,
		TitleCode:          "issue." + detectorID,
		Severity:           "low",
		Confidence:         "high",
		ScopeQuality:       model.ScopeUnscoped,
		FirstObservedAt:    event.OccurredAt,
		LastObservedAt:     event.OccurredAt,
		EvidenceComplete:   true,
		Experimental:       experimental,
		AnalysisStatus:     model.AnalysisCurrent,
		AnalysisGeneration: 1,
		Evidence: model.IssueEvidence{
			CitedEventIDs: []string{event.EventID},
		},
	}
}
