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

type transcriptHTTPRepository struct {
	coverage transcript.CoverageCounts
	sessions []transcript.Session
}

func (repository transcriptHTTPRepository) TranscriptCoverage(
	context.Context,
) (transcript.CoverageCounts, error) {
	return repository.coverage, nil
}

func (repository transcriptHTTPRepository) QueryTranscriptSessions(
	_ context.Context,
	query transcript.SessionQuery,
) ([]transcript.Session, error) {
	if len(repository.sessions) > query.Limit {
		return repository.sessions[:query.Limit], nil
	}
	return repository.sessions, nil
}

func TestTranscriptStatusRouteRequiresAuthAndRejectsQueries(t *testing.T) {
	server := newTranscriptStatusTestServer(t)

	unauthorized := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/transcript-status",
		nil,
	)
	unauthorized.RemoteAddr = "127.0.0.1:1234"
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.Code)
	}

	withQuery := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/transcript-status?limit=1",
		nil,
	)
	withQuery.RemoteAddr = "127.0.0.1:1234"
	withQuery.Header.Set("Authorization", "Bearer launch-secret")
	queryResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(queryResponse, withQuery)
	if queryResponse.Code != http.StatusBadRequest {
		t.Fatalf(
			"query status = %d body=%s",
			queryResponse.Code,
			queryResponse.Body.String(),
		)
	}
}

func TestTranscriptStatusRouteReturnsBoundedPayloadFreeSchema(t *testing.T) {
	server := newTranscriptStatusTestServer(t)
	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/transcript-status",
		nil,
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	var status readmodel.TranscriptStatus
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.SchemaVersion != readmodel.TranscriptStatusSchemaVersion ||
		status.Coverage.WithTranscript != 9 ||
		status.Coverage.Partial != 2 ||
		status.Coverage.Without != 3 ||
		status.Coverage.TranscriptOnly != 1 ||
		len(status.Sessions) != 10 ||
		!status.Sessions[0].Active ||
		status.Sessions[0].Project != "repo" {
		t.Fatalf("transcript status = %+v", status)
	}
	for _, prohibited := range []string{
		"/Users/private/project",
		"git@example.invalid:private/repo.git",
		"project_identity",
		"project_path",
		"git_remote",
		"payload",
		"excerpt",
		"raw_command",
	} {
		if strings.Contains(body, prohibited) {
			t.Fatalf("transcript status leaked %q: %s", prohibited, body)
		}
	}
}

func newTranscriptStatusTestServer(t *testing.T) *Server {
	t.Helper()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	sessions := make([]transcript.Session, 12)
	for index := range sessions {
		sessions[index] = transcript.Session{
			SessionKey:      "ses_test_" + string(rune('a'+index)),
			Agent:           "codex",
			NativeSessionID: "native-private",
			ProjectPath:     "/Users/private/project",
			GitRemoteURL:    "git@example.invalid:private/repo.git",
			ProjectIdentity: "git@example.invalid:private/repo.git",
			StartedAt:       now.Add(-time.Duration(index+1) * time.Minute),
			EndedAt:         now.Add(-time.Duration(index) * time.Minute),
			Coverage:        transcript.CoverageLive,
			TurnCount:       index + 1,
		}
	}
	service := readmodel.New(
		testRepository{},
		readmodel.WithClock(func() time.Time { return now }),
		readmodel.WithTranscriptRepository(transcriptHTTPRepository{
			coverage: transcript.CoverageCounts{
				CanonicalCompleteOrLiveWithTranscript: 9,
				CanonicalPartial:                      2,
				CanonicalWithoutTranscript:            3,
				TranscriptOnly:                        1,
			},
			sessions: sessions,
		}),
	)
	server, err := New(service, "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	return server
}
