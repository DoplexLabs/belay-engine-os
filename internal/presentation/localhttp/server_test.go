package localhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/initialization"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

type testRepository struct{}

func (testRepository) QuerySessions(_ context.Context, query model.SessionQuery) (model.SessionPage, error) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	data := []model.SessionSummary{{
		SessionID:  "session-1",
		Harness:    "codex",
		StartedAt:  now.Add(-time.Minute),
		EndedAt:    now,
		EventCount: 1,
		Outcome:    "succeeded",
		History:    "live",
	}}
	if len(data) > query.Limit {
		data = data[:query.Limit]
	}
	return model.SessionPage{Data: data, Snapshot: 1, DataThrough: now}, nil
}

func (testRepository) GetSession(context.Context, string) (model.SessionSummary, time.Time, error) {
	page, _ := testRepository{}.QuerySessions(context.Background(), model.SessionQuery{Limit: 1})
	return page.Data[0], page.DataThrough, nil
}

func (testRepository) QuerySessionTimeline(_ context.Context, query model.TimelineQuery) (model.EventPage, error) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	return model.EventPage{
		Data:        []model.Event{{EventID: "event-1", OccurredAt: now, Source: model.Source{Sequence: 1}}},
		Snapshot:    1,
		DataThrough: now,
	}, nil
}

func (testRepository) QueryActivityPage(context.Context, model.ActivityQuery) (model.EventPage, error) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	return model.EventPage{
		Data:        []model.Event{{EventID: "event-1", OccurredAt: now, Source: model.Source{Sequence: 1}}},
		Snapshot:    1,
		DataThrough: now,
	}, nil
}

func (testRepository) QueryFindings(context.Context, model.FindingQuery) (model.FindingPage, error) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	return model.FindingPage{
		Data:        []model.FindingSummary{{FindingID: "finding-1", DetectedAt: now}},
		Snapshot:    1,
		DataThrough: now,
	}, nil
}

func (testRepository) GetStats(context.Context) (model.LocalStats, time.Time, error) {
	return model.LocalStats{EventCount: 1}, time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC), nil
}

type pagingRepository struct {
	testRepository
	sessionLimit  int
	timelineLimit int
}

type recordingRepository struct {
	testRepository
	sessionQuery model.SessionQuery
	findingQuery model.FindingQuery
}

func (r *recordingRepository) QuerySessions(
	ctx context.Context,
	query model.SessionQuery,
) (model.SessionPage, error) {
	r.sessionQuery = query
	return r.testRepository.QuerySessions(ctx, query)
}

func (r *recordingRepository) QueryFindings(
	ctx context.Context,
	query model.FindingQuery,
) (model.FindingPage, error) {
	r.findingQuery = query
	return r.testRepository.QueryFindings(ctx, query)
}

func (r *pagingRepository) QuerySessions(_ context.Context, query model.SessionQuery) (model.SessionPage, error) {
	r.sessionLimit = query.Limit
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	sessions := make([]model.SessionSummary, query.Limit)
	for index := range sessions {
		sessions[index] = model.SessionSummary{
			SessionID:  fmt.Sprintf("session-%d", index),
			Harness:    "codex",
			StartedAt:  now.Add(-time.Minute),
			EndedAt:    now,
			EventCount: 1,
			Outcome:    "incomplete",
		}
	}
	return model.SessionPage{
		Data:        sessions,
		Snapshot:    1,
		DataThrough: now,
	}, nil
}

func (r *pagingRepository) QuerySessionTimeline(_ context.Context, query model.TimelineQuery) (model.EventPage, error) {
	r.timelineLimit = query.Limit
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	events := make([]model.Event, query.Limit)
	for index := range events {
		events[index] = model.Event{
			EventID:    fmt.Sprintf("event-%d", index),
			OccurredAt: now.Add(time.Duration(index) * time.Second),
			Source:     model.Source{Sequence: int64(index)},
			Session:    model.SessionRef{Key: query.SessionID},
		}
	}
	return model.EventPage{
		Data:        events,
		Snapshot:    1,
		DataThrough: now,
	}, nil
}

func TestHandlerRequiresLoopbackAndLaunchToken(t *testing.T) {
	server, err := New(readmodel.New(testRepository{}), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/sessions", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/sessions", nil)
	request.RemoteAddr = "203.0.113.4:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d, want %d", response.Code, http.StatusForbidden)
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/sessions", nil)
	request.RemoteAddr = "[::1]:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := response.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("missing Content-Security-Policy")
	}
	var completePage readmodel.SessionList
	if err := json.NewDecoder(response.Body).Decode(&completePage); err != nil {
		t.Fatalf("decode complete session page: %v", err)
	}
	if completePage.HasMore || completePage.ReturnedCount != 1 || completePage.Limit != 20 {
		t.Fatalf("complete session page metadata = %+v", completePage)
	}
}

func TestStaticBrowserDoesNotRequireToken(t *testing.T) {
	server, err := New(readmodel.New(testRepository{}), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("static status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestRuntimeRouteDefaultAndConfiguredExperience(t *testing.T) {
	tests := []struct {
		name       string
		options    []Option
		experience Experience
	}{
		{
			name:       "default",
			experience: ExperienceCurrent,
		},
		{
			name:       "value first",
			options:    []Option{WithExperience(ExperienceValueFirst)},
			experience: ExperienceValueFirst,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, err := New(
				readmodel.New(testRepository{}),
				"launch-secret",
				test.options...,
			)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(
				http.MethodGet,
				"http://127.0.0.1/v1/runtime",
				nil,
			)
			request.RemoteAddr = "127.0.0.1:1234"
			request.Header.Set("Authorization", "Bearer launch-secret")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf(
					"status = %d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
			want := fmt.Sprintf(
				"{\"schema_version\":\"%s\",\"experience\":\"%s\"}\n",
				RuntimeSchemaVersion,
				test.experience,
			)
			if response.Body.String() != want {
				t.Fatalf(
					"runtime response = %q, want %q",
					response.Body.String(),
					want,
				)
			}
		})
	}
}

func TestRuntimeRouteRequiresAuthorizationAndLoopback(t *testing.T) {
	server, err := New(readmodel.New(testRepository{}), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/runtime",
		nil,
	)
	unauthorized.RemoteAddr = "127.0.0.1:1234"
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.Code)
	}

	nonLoopback := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/runtime",
		nil,
	)
	nonLoopback.RemoteAddr = "203.0.113.4:1234"
	nonLoopback.Header.Set("Authorization", "Bearer launch-secret")
	nonLoopbackResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(nonLoopbackResponse, nonLoopback)
	if nonLoopbackResponse.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d", nonLoopbackResponse.Code)
	}
}

func TestRuntimeExperienceOptionRejectsInvalidValue(t *testing.T) {
	_, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithExperience(Experience("PRIVATE_MODE_CANARY")),
	)
	if !errors.Is(err, ErrInvalidExperience) {
		t.Fatalf("New() error = %v, want invalid experience", err)
	}
	if strings.Contains(err.Error(), "PRIVATE_MODE_CANARY") {
		t.Fatalf("New() error reflected invalid value: %q", err)
	}
}

func TestInitializationRouteIsAuthenticatedFixedAndRejectsQueryParameters(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	tracker := initialization.NewTracker(true, func() time.Time { return now })
	server, err := New(
		readmodel.New(
			testRepository{},
			readmodel.WithInitializationProvider(tracker),
		),
		"launch-secret",
	)
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/initialization",
		nil,
	)
	unauthorized.RemoteAddr = "127.0.0.1:1234"
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.Code)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/initialization",
		nil,
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body readmodel.InitializationResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != initialization.SchemaVersion ||
		body.Initialization.State != initialization.StateInitializing ||
		body.Initialization.CompletedAt != nil ||
		body.Initialization.ErrorCode != nil {
		t.Fatalf("initialization response = %+v", body)
	}

	for _, rawQuery := range []string{"state=ready", "private=RAW_ERROR_CANARY"} {
		withQuery := httptest.NewRequest(
			http.MethodGet,
			"http://127.0.0.1/v1/initialization?"+rawQuery,
			nil,
		)
		withQuery.RemoteAddr = "127.0.0.1:1234"
		withQuery.Header.Set("Authorization", "Bearer launch-secret")
		queryResponse := httptest.NewRecorder()
		server.Handler().ServeHTTP(queryResponse, withQuery)
		if queryResponse.Code != http.StatusBadRequest {
			t.Fatalf("query %q status = %d", rawQuery, queryResponse.Code)
		}
		if strings.Contains(queryResponse.Body.String(), "RAW_ERROR_CANARY") {
			t.Fatalf("query response reflected input: %s", queryResponse.Body.String())
		}
	}
}

func TestInitializationRouteDegradedStateIsPayloadFree(t *testing.T) {
	tracker := initialization.NewTracker(true, time.Now)
	tracker.MarkHistoricalScanIncomplete()
	server, err := New(
		readmodel.New(
			testRepository{},
			readmodel.WithInitializationProvider(tracker),
		),
		"launch-secret",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/initialization",
		nil,
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(
		response.Body.String(),
		`"error_code":"historical_scan_incomplete"`,
	) ||
		strings.Contains(response.Body.String(), "/Users/") {
		t.Fatalf("degraded response = %s", response.Body.String())
	}
}

func TestStartRejectsNonLoopbackAddress(t *testing.T) {
	server, err := New(readmodel.New(testRepository{}), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Start(context.Background(), "0.0.0.0:0"); err == nil {
		t.Fatal("Start accepted a non-loopback address")
	}
}

func TestSessionAndTimelineResponsesExposeTruncation(t *testing.T) {
	repository := &pagingRepository{}
	server, err := New(readmodel.New(repository), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/sessions?limit=100", nil)
	sessionRequest.RemoteAddr = "127.0.0.1:1234"
	sessionRequest.Header.Set("Authorization", "Bearer launch-secret")
	sessionResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusOK {
		t.Fatalf("session status = %d; body=%s", sessionResponse.Code, sessionResponse.Body.String())
	}
	var sessionPage readmodel.SessionList
	if err := json.NewDecoder(sessionResponse.Body).Decode(&sessionPage); err != nil {
		t.Fatalf("decode session page: %v", err)
	}
	if repository.sessionLimit != 101 {
		t.Fatalf("repository session limit = %d, want 101 look-ahead rows", repository.sessionLimit)
	}
	if !sessionPage.HasMore || sessionPage.ReturnedCount != 100 || sessionPage.Limit != 100 || len(sessionPage.Data) != 100 {
		t.Fatalf("session page = %+v, want 100 rows with has_more", sessionPage)
	}
	if sessionPage.NextCursor == nil || *sessionPage.NextCursor == "" {
		t.Fatal("session next_cursor is absent for a truncated page")
	}

	timelineRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/v1/sessions/session-1/events?limit=500", nil)
	timelineRequest.RemoteAddr = "127.0.0.1:1234"
	timelineRequest.Header.Set("Authorization", "Bearer launch-secret")
	timelineResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(timelineResponse, timelineRequest)
	if timelineResponse.Code != http.StatusOK {
		t.Fatalf("timeline status = %d; body=%s", timelineResponse.Code, timelineResponse.Body.String())
	}
	var timelinePage readmodel.SessionTimeline
	if err := json.NewDecoder(timelineResponse.Body).Decode(&timelinePage); err != nil {
		t.Fatalf("decode timeline page: %v", err)
	}
	if repository.timelineLimit != 501 {
		t.Fatalf("repository timeline limit = %d, want 501 look-ahead rows", repository.timelineLimit)
	}
	if !timelinePage.HasMore || timelinePage.ReturnedCount != 500 || timelinePage.Limit != 500 || len(timelinePage.Data) != 500 {
		t.Fatalf("timeline page = %+v, want 500 rows with has_more", timelinePage)
	}
	if timelinePage.NextCursor == nil || *timelinePage.NextCursor == "" {
		t.Fatal("timeline next_cursor is absent for a truncated page")
	}
}

func TestSessionFiltersReachSharedReadModelAndMalformedCursorIsRejected(t *testing.T) {
	repository := &recordingRepository{}
	server, err := New(readmodel.New(repository), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/sessions?limit=5&harness=codex&outcome=incomplete&history=live&query=session&occurred_after=2026-09-08T10:00:00Z&occurred_before=2026-09-08T13:00:00Z",
		nil,
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("filtered status = %d; body=%s", response.Code, response.Body.String())
	}
	query := repository.sessionQuery
	if query.Limit != 6 ||
		query.Harness != "codex" ||
		query.Outcome != "incomplete" ||
		query.History != "live" ||
		query.Search != "session" ||
		query.OccurredAfter == nil ||
		query.OccurredBefore == nil {
		t.Fatalf("repository session query = %+v", query)
	}

	request = httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/sessions?cursor=PRIVATE_CURSOR_CANARY",
		nil,
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed cursor status = %d, want 400", response.Code)
	}
	if strings.Contains(response.Body.String(), "PRIVATE_CURSOR_CANARY") {
		t.Fatal("problem response reflected the malformed cursor")
	}
}

func TestFindingFiltersReachSharedReadModel(t *testing.T) {
	repository := &recordingRepository{}
	server, err := New(readmodel.New(repository), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/findings?limit=5&session_id=session-1&severity=medium&since=2026-09-08T10:00:00Z",
		nil,
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("filtered status = %d; body=%s", response.Code, response.Body.String())
	}
	query := repository.findingQuery
	if query.Filter.Limit != 6 ||
		query.Filter.SessionID != "session-1" ||
		query.Filter.Severity != "medium" ||
		query.Filter.DetectedAfter == nil {
		t.Fatalf("repository finding query = %+v", query)
	}
}

func TestBrowserEventOutcomePresentationContract(t *testing.T) {
	body, err := fs.ReadFile(assetFiles, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	eventRow := sourceSection(t, source, "  function createEventRow(event, findings) {", "  function createEvidenceDetails(")
	sessionLabels := sourceSection(t, source, "  function sessionOutcomeLabel(outcome) {", "  function sessionOutcomeExplanation(")

	for _, required := range []string{
		`if (explicitOutcomes.has(outcome)) {`,
		`"status-badge"`,
		`if (outcome === "unknown") {`,
		`"Outcome · Not reported"`,
	} {
		if !strings.Contains(eventRow, required) {
			t.Fatalf("event-row rendering is missing %q", required)
		}
	}
	if strings.Index(eventRow, `if (explicitOutcomes.has(outcome)) {`) >
		strings.Index(eventRow, `"status-badge"`) {
		t.Error("event outcome badge is not guarded by the explicit-outcome check")
	}
	if strings.Count(eventRow, `"status-badge"`) != 1 {
		t.Error("event rows must have exactly one conditionally rendered outcome badge")
	}
	if strings.Index(eventRow, `if (outcome === "unknown") {`) >
		strings.Index(eventRow, `"Outcome · Not reported"`) {
		t.Error("source-unreported metadata is not guarded by the unknown-outcome check")
	}
	if !strings.Contains(source,
		`const explicitOutcomes = new Set(["succeeded", "failed", "interrupted"]);`) {
		t.Error("explicit event outcome allowlist changed")
	}

	for _, required := range []string{
		`if (outcome === "unknown") return "Outcome not reported";`,
		`if (outcome === "incomplete") return "Outcome not reported";`,
	} {
		if !strings.Contains(sessionLabels, required) {
			t.Fatalf("session outcome labels are missing %q", required)
		}
	}
	if strings.Contains(source, "terminal event") ||
		strings.Contains(source, "terminal session event") {
		t.Error("browser retains customer-facing terminal-event jargon")
	}
	if strings.Contains(sessionLabels, `"Succeeded"`) {
		t.Error("unknown or incomplete session outcomes must never be coerced to success")
	}
	if strings.Contains(source, "innerHTML") {
		t.Error("browser asset must render event-derived content as text, never HTML")
	}
}

func TestBrowserLaunchUXContract(t *testing.T) {
	appBody, err := fs.ReadFile(assetFiles, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	indexBody, err := fs.ReadFile(assetFiles, "assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	styleBody, err := fs.ReadFile(assetFiles, "assets/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appBody)
	index := string(indexBody)
	styles := string(styleBody)
	sessionPath := sourceSection(t, app, "  function buildSessionPath(cursor) {", "  function loadMoreSessions(")
	sessionMatch := sourceSection(t, app, "  function sessionMatchesLocally(session) {", "  function createSessionCard(")
	sessionSelection := sourceSection(t, app, "  function selectSession(sessionID) {", "  async function loadSessionDetail(")
	findingsLoad := sourceSection(t, app, "  async function loadFindings(sessionID) {", "  async function loadTimeline(")

	for _, required := range []string{
		`apiGet("/v1/stats")`,
		"`/v1/findings?${parameters.toString()}`",
		"`/v1/sessions/${encodeURIComponent(sessionID)}`",
		`parameters.set("cursor", cursor)`,
		`state.sessionOccurredAfter = dateLowerBound(state.filters.days);`,
		`parameters.set("occurred_after", state.sessionOccurredAfter);`,
		`session_id: sessionID`,
		`Partial session summary ·`,
		`"additional referenced resources were not included in this summary."`,
		`"No findings were reported for this session."`,
		`"Load more sessions before treating this outcome filter as complete."`,
		`details.append(createElement("summary", "", "Evidence"));`,
		`history === "mixed"`,
	} {
		if !strings.Contains(app, required) {
			t.Fatalf("browser launch UX is missing %q", required)
		}
	}

	for _, required := range []string{
		`id="overview-session-count"`,
		`id="filter-harness"`,
		`id="filter-outcome"`,
		`id="session-overview-title"`,
		`id="overview-state"`,
		`id="resource-disclosure"`,
		`id="all-events-toggle"`,
		`class="event-scroll"`,
		`maxlength="128"`,
	} {
		if !strings.Contains(index, required) {
			t.Fatalf("browser shell is missing %q", required)
		}
	}

	if strings.Contains(sessionPath, "dateLowerBound(") {
		t.Error("session request path recomputes the relative date lower bound")
	}
	if strings.Count(app, "dateLowerBound(state.filters.days)") != 1 {
		t.Error("relative date lower bound must be frozen exactly when the filter changes")
	}
	if strings.Contains(sessionMatch, "dateLowerBound(") ||
		strings.Contains(sessionMatch, "started_at") {
		t.Error("browser applies conflicting local date semantics")
	}
	if !strings.Contains(sessionMatch, `[harness, session.session_id]`) {
		t.Error("session search is not limited to the backend safe fields")
	}
	for _, required := range []string{
		`void loadTimeline(sessionID, false);`,
		`void loadSessionDetail(sessionID);`,
		`void loadFindings(sessionID);`,
	} {
		if !strings.Contains(sessionSelection, required) {
			t.Fatalf("progressive session selection is missing %q", required)
		}
	}
	if strings.Contains(sessionSelection, "Promise.allSettled") ||
		strings.Contains(sessionSelection, "await ") {
		t.Error("session selection still blocks on independent detail requests")
	}
	if !strings.Contains(findingsLoad,
		`readText(finding.session_id) !== sessionID`) {
		t.Error("findings response does not fail closed on cross-session rows")
	}
	for _, required := range []string{
		`session_id: sessionID`,
		`parameters.set("cursor", cursor)`,
		`renderSessionOverview();`,
		`renderEvents();`,
	} {
		if !strings.Contains(findingsLoad, required) {
			t.Errorf("session-scoped findings pagination is missing %q", required)
		}
	}
	if strings.Contains(findingsLoad, "pageLimits.findings.maximum") ||
		strings.Contains(findingsLoad, "state.stats") {
		t.Error("browser still scans or bounds findings using global findings state")
	}
	for _, required := range []string{
		`.session-overview {`,
		`overflow: auto;`,
		`min-height: 44px;`,
		`max-height: none;`,
		`overflow: visible;`,
		`.timeline-panel {`,
		`overflow-y: auto;`,
		`overscroll-behavior: contain;`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("responsive browser styles are missing %q", required)
		}
	}
}

func sourceSection(t *testing.T, source, start, end string) string {
	t.Helper()
	startIndex := strings.Index(source, start)
	if startIndex < 0 {
		t.Fatalf("browser source is missing section start %q", start)
	}
	endIndex := strings.Index(source[startIndex:], end)
	if endIndex < 0 {
		t.Fatalf("browser source is missing section end %q", end)
	}
	return source[startIndex : startIndex+endIndex]
}
