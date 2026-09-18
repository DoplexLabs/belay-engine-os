package localhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/userinsights"
)

type habitDebriefHTTPService struct {
	records   map[string]userinsights.DebriefRecord
	err       error
	harness   string
	available bool
	calls     []string
}

func (s *habitDebriefHTTPService) Generate(_ context.Context, key string, refresh bool) (userinsights.DebriefRecord, error) {
	s.calls = append(s.calls, key)
	if s.err != nil {
		return userinsights.DebriefRecord{}, s.err
	}
	record := userinsights.DebriefRecord{
		SchemaVersion: userinsights.DebriefSchemaVersion, SessionKey: key, ProjectIdentity: "/Users/private/project",
		Harness: s.harness, Model: "claude-test", PromptVersion: userinsights.DebriefPromptVersion, InputHash: "h",
		GeneratedAt: time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC),
		Debrief: userinsights.Debrief{Headline: "Checks came late.", Insights: []userinsights.Insight{{
			Kind: "verification", Title: "Ask for checks first", SayThisInstead: "Run go test after each change.", EvidenceTurns: []int64{2},
		}}},
	}
	if refresh {
		record.InputHash = "h2"
	}
	s.records[key] = record
	return record, nil
}

func (s *habitDebriefHTTPService) Harness() (string, bool) { return s.harness, s.available }

type habitDebriefHTTPRepository struct {
	userInsightsHTTPRepository
	service *habitDebriefHTTPService
}

func (r habitDebriefHTTPRepository) QueryHabitDebriefs(_ context.Context, keys []string) (map[string]userinsights.DebriefRecord, error) {
	result := map[string]userinsights.DebriefRecord{}
	for _, key := range keys {
		if record, ok := r.service.records[key]; ok {
			result[key] = record
		}
	}
	return result, nil
}

func newHabitDebriefTestServer(t *testing.T, service *habitDebriefHTTPService) *Server {
	t.Helper()
	repository := habitDebriefHTTPRepository{service: service}
	repository.userInsightsHTTPRepository = userInsightsHTTPFixture()
	read := readmodel.New(
		testRepository{},
		readmodel.WithTranscriptRepository(repository),
		readmodel.WithHabitDebriefRepository(repository),
		readmodel.WithUserInsightHarness(service.Harness),
	)
	server, err := New(read, "launch-secret", WithHabitDebriefService(service))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestUserInsightsListReportsHarnessAndDebriefStatus(t *testing.T) {
	service := &habitDebriefHTTPService{records: map[string]userinsights.DebriefRecord{}, harness: "claude", available: true}
	server := newHabitDebriefTestServer(t, service)
	fetch := func() map[string]any {
		request := issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/user-insights", true)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	payload := fetch()
	harness := payload["harness"].(map[string]any)
	first := payload["sessions"].([]any)[0].(map[string]any)
	if harness["available"] != true || harness["name"] != "claude" || first["debrief_status"] != "missing" || first["debrief"] != nil {
		t.Fatalf("unexpected list before generation: %v", payload)
	}

	request := issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/user-insights/ses_http/debrief?generate=1", true)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Checks came late.") {
		t.Fatalf("generate failed: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "/Users/private") {
		t.Fatal("debrief response leaked the project path")
	}
	first = fetch()["sessions"].([]any)[0].(map[string]any)
	if first["debrief_status"] != "ready" || first["debrief"] == nil {
		t.Fatalf("debrief should be attached after generation: %v", first)
	}

	stored := issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/user-insights/ses_http/debrief", true)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, stored)
	if response.Code != http.StatusOK || len(service.calls) != 1 {
		t.Fatalf("stored read must not regenerate: %d calls=%v", response.Code, service.calls)
	}
	missing := issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/user-insights/ses_none/debrief", true)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, missing)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing stored debrief should be 404, got %d", response.Code)
	}
	bad := issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/user-insights/ses_http/debrief?limit=3", true)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, bad)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown parameter should be 400, got %d", response.Code)
	}
}

func TestUserInsightDebriefMapsServiceErrors(t *testing.T) {
	cases := []struct {
		err  error
		code int
		typ  string
	}{
		{localapp.ErrHabitHarnessUnavailable, http.StatusServiceUnavailable, "belay.local/habits-harness-unavailable"},
		{localapp.ErrHabitSessionNotFound, http.StatusNotFound, "about:blank"},
		{localapp.ErrHabitSessionNotReady, http.StatusConflict, "belay.local/habits-session-not-ready"},
		{errors.Join(localapp.ErrHabitGenerationFailed, errors.New("boom")), http.StatusBadGateway, "belay.local/habits-generation-failed"},
		{context.DeadlineExceeded, http.StatusGatewayTimeout, "belay.local/habits-generation-timeout"},
	}
	for _, tc := range cases {
		service := &habitDebriefHTTPService{records: map[string]userinsights.DebriefRecord{}, harness: "codex", available: true, err: tc.err}
		server := newHabitDebriefTestServer(t, service)
		request := issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/user-insights/ses_http/debrief?generate=1", true)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != tc.code || !strings.Contains(response.Body.String(), tc.typ) {
			t.Fatalf("%v: got %d %s", tc.err, response.Code, response.Body.String())
		}
	}
}
