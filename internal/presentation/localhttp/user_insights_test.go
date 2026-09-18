package localhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type userInsightsHTTPRepository struct {
	sessions []transcript.Session
	turns    map[string][]transcript.Turn
}

func (repository userInsightsHTTPRepository) TranscriptCoverage(
	context.Context,
) (transcript.CoverageCounts, error) {
	return transcript.CoverageCounts{}, nil
}

func (repository userInsightsHTTPRepository) QueryTranscriptSessions(
	_ context.Context,
	query transcript.SessionQuery,
) ([]transcript.Session, error) {
	if query.Limit < len(repository.sessions) {
		return repository.sessions[:query.Limit], nil
	}
	return repository.sessions, nil
}

func (repository userInsightsHTTPRepository) QueryTranscriptTurns(
	_ context.Context,
	sessionKey string,
	_ int,
) ([]transcript.Turn, error) {
	return repository.turns[sessionKey], nil
}

func newUserInsightsTestServer(t *testing.T) *Server {
	t.Helper()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	service := readmodel.New(
		testRepository{},
		readmodel.WithClock(func() time.Time { return now }),
		readmodel.WithTranscriptRepository(userInsightsHTTPFixture()),
	)
	server, err := New(service, "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func userInsightsHTTPFixture() userInsightsHTTPRepository {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	zero := 0
	turn := func(index int, role transcript.Role, minute int) transcript.Turn {
		value := transcript.Turn{
			TurnID:     "turn-" + string(rune('a'+index)),
			SessionKey: "ses_http",
			TurnIndex:  int64(index),
			OccurredAt: start.Add(time.Duration(minute) * time.Minute),
			Role:       role,
		}
		return value
	}
	prompt := turn(0, transcript.RoleUser, 0)
	prompt.Payload.Text = "Refactor the private billing module."
	edit := turn(1, transcript.RoleToolCall, 1)
	edit.ToolName = "Edit"
	edit.Payload.ToolInput = json.RawMessage(`{"file_path":"/Users/private/project/billing.go"}`)
	edit2 := turn(2, transcript.RoleToolCall, 2)
	edit2.ToolName = "Edit"
	edit2.Payload.ToolInput = json.RawMessage(`{"file_path":"/Users/private/project/tax.go"}`)
	edit3 := turn(3, transcript.RoleToolCall, 3)
	edit3.ToolName = "Edit"
	edit3.Payload.ToolInput = json.RawMessage(`{"file_path":"/Users/private/project/fees.go"}`)
	done := turn(4, transcript.RoleAssistant, 4)
	done.Payload.Text = "Done."
	correction := turn(5, transcript.RoleUser, 5)
	correction.Payload.Text = "No, run the tests."
	check := turn(6, transcript.RoleToolCall, 20)
	check.ToolName = "Bash"
	check.Payload.RawCommand = "go test ./..."
	check.Payload.ToolCallID = "call-1"
	result := turn(7, transcript.RoleToolResult, 21)
	result.Payload.ToolCallID = "call-1"
	result.Payload.ExitCode = &zero

	return userInsightsHTTPRepository{
		sessions: []transcript.Session{{
			SessionKey:      "ses_http",
			Agent:           "claude-code",
			ProjectPath:     "/Users/private/project",
			ProjectIdentity: "/Users/private/project",
			StartedAt:       start,
			EndedAt:         start.Add(30 * time.Minute),
			WallDurationMS:  (30 * time.Minute).Milliseconds(),
			Coverage:        transcript.CoverageComplete,
			UserTurnCount:   2,
		}},
		turns: map[string][]transcript.Turn{
			"ses_http": {prompt, edit, edit2, edit3, done, correction, check, result},
		},
	}
}

func TestUserInsightsRouteReturnsSafeDebriefs(t *testing.T) {
	server := newUserInsightsTestServer(t)
	request := issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/user-insights?limit=5", true)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		SchemaVersion     string `json:"schema_version"`
		ProjectionVersion string `json:"projection_version"`
		Sessions          []struct {
			SessionKey string `json:"session_key"`
			Project    string `json:"project"`
			Verdict    string `json:"verdict"`
			Findings   []struct {
				Kind string `json:"kind"`
			} `json:"findings"`
			Opener string `json:"opener"`
		} `json:"sessions"`
		Coverage struct {
			EvaluatedSessions int `json:"evaluated_sessions"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != readmodel.SchemaVersion ||
		payload.ProjectionVersion != readmodel.UserInsightsProjectionVersion {
		t.Fatalf("unexpected envelope: %+v", payload)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].Project != "project" ||
		payload.Coverage.EvaluatedSessions != 1 {
		t.Fatalf("unexpected sessions: %+v", payload)
	}
	if len(payload.Sessions[0].Findings) == 0 ||
		payload.Sessions[0].Findings[0].Kind != "late_verification" ||
		payload.Sessions[0].Opener == "" {
		t.Fatalf("expected a late verification debrief with an opener: %+v", payload.Sessions[0])
	}
	body := response.Body.String()
	for _, forbidden := range []string{"/Users/private", "go test", "run the tests", "billing.go"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("user insights response leaked %q", forbidden)
		}
	}
}

func TestUserInsightsRouteRejectsUnknownParametersAndRequiresAuth(t *testing.T) {
	server := newUserInsightsTestServer(t)
	for _, target := range []string{
		"http://127.0.0.1/v1/user-insights?session=ses_http",
		"http://127.0.0.1/v1/user-insights?limit=0",
		"http://127.0.0.1/v1/user-insights?limit=abc",
	} {
		request := issueHTTPRequest(http.MethodGet, target, true)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", target, response.Code)
		}
	}
	request := issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/user-insights", false)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without the launch token, got %d", response.Code)
	}
}

func TestUserInsightsRouteReportsUnavailableWithoutTranscripts(t *testing.T) {
	server, err := New(readmodel.New(testRepository{}), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/user-insights", true)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(response.Body.String(), "belay.local/user-insights-unavailable") {
		t.Fatalf("expected the fixed unavailable problem, got %d %s", response.Code, response.Body.String())
	}
}
