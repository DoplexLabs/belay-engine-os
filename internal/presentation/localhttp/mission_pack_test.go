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

	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

type missionPackHTTPService struct {
	pack     missionpack.Pack
	err      error
	request  missionpack.Request
	deadline time.Time
	calls    int
}

func (s *missionPackHTTPService) Generate(
	ctx context.Context,
	request missionpack.Request,
) (missionpack.Pack, error) {
	s.calls++
	s.request = request
	s.deadline, _ = ctx.Deadline()
	return s.pack, s.err
}

type classifiedMissionPackHTTPError string

func (err classifiedMissionPackHTTPError) Error() string {
	return "private Mission Pack failure"
}

func (err classifiedMissionPackHTTPError) MissionPackErrorKind() string {
	return string(err)
}

func TestMissionPackHTTPGeneratesAuthenticatedBoundedProjection(t *testing.T) {
	service := &missionPackHTTPService{
		pack: missionpack.Pack{
			SchemaVersion:    missionpack.SchemaVersion,
			GeneratorVersion: missionpack.GeneratorVersion,
			PackID:           "mpk_test",
			Project: missionpack.Project{
				Label:        "belay-engine",
				IdentityKind: "remote",
				Branch:       "main",
			},
			Intent: missionpack.IntentImplement,
			Trust: missionpack.Trust{
				InstructionAuthority: "none",
				ActivationRequired:   true,
			},
		},
	}
	server := newMissionPackHTTPTestServer(t, service)

	unauthorized := missionPackHTTPRequest(
		"/v1/mission-pack?issue_id=issue-1&intent=implement",
		false,
	)
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf(
			"unauthorized status = %d, want %d",
			unauthorizedResponse.Code,
			http.StatusUnauthorized,
		)
	}
	if service.calls != 0 {
		t.Fatalf("unauthorized service calls = %d, want 0", service.calls)
	}

	started := time.Now()
	request := missionPackHTTPRequest(
		"/v1/mission-pack?issue_id=issue-1&intent=implement",
		true,
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"status = %d, want %d; body=%s",
			response.Code,
			http.StatusOK,
			response.Body.String(),
		)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"Cache-Control = %q, want no-store",
			response.Header().Get("Cache-Control"),
		)
	}
	if service.calls != 1 ||
		service.request.IssueID != "issue-1" ||
		service.request.Intent != missionpack.IntentImplement ||
		service.request.CWD != "" ||
		service.request.TaskHint != "" {
		t.Fatalf(
			"service call = calls:%d request:%+v",
			service.calls,
			service.request,
		)
	}
	remaining := service.deadline.Sub(started)
	if remaining < 4*time.Second || remaining > missionPackHTTPDeadline+time.Second {
		t.Fatalf("handler deadline = %s, want fixed five-second bound", remaining)
	}
	body := response.Body.String()
	for _, forbidden := range []string{
		`"cwd"`,
		`"path"`,
		`"git_remote_url"`,
		`"remote_url"`,
		`"task_hint"`,
		`"excerpts"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response contains forbidden field %s: %s", forbidden, body)
		}
	}
	var pack missionpack.Pack
	if err := json.Unmarshal([]byte(body), &pack); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if pack.PackID != service.pack.PackID {
		t.Fatalf("pack ID = %q, want %q", pack.PackID, service.pack.PackID)
	}
}

func TestMissionPackHTTPRejectsInvalidSelectors(t *testing.T) {
	service := &missionPackHTTPService{}
	server := newMissionPackHTTPTestServer(t, service)
	for _, target := range []string{
		"/v1/mission-pack",
		"/v1/mission-pack?issue_id=",
		"/v1/mission-pack?issue_id=issue-1&cwd=%2Fprivate",
		"/v1/mission-pack?issue_id=issue-1&issue_id=issue-2",
		"/v1/mission-pack?issue_id=issue-1&intent=debug&intent=review",
		"/v1/mission-pack?issue_id=issue-1&intent=private",
		"/v1/mission-pack?issue_id=issue-1;intent=debug",
	} {
		t.Run(target, func(t *testing.T) {
			request := missionPackHTTPRequest(target, true)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					http.StatusBadRequest,
					response.Body.String(),
				)
			}
			var value problem
			if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if value.Detail != "Mission Pack request is invalid." {
				t.Fatalf("problem = %+v", value)
			}
		})
	}
	if service.calls != 0 {
		t.Fatalf("invalid selector service calls = %d, want 0", service.calls)
	}
}

func TestMissionPackHTTPMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		detail string
	}{
		{
			name:   "invalid selector",
			err:    classifiedMissionPackHTTPError("invalid_selector"),
			status: http.StatusBadRequest,
			detail: "Mission Pack request is invalid.",
		},
		{
			name:   "project mismatch",
			err:    missionpack.ErrProjectMismatch,
			status: http.StatusConflict,
			detail: "The selected issue does not match the current project.",
		},
		{
			name:   "project not found",
			err:    missionpack.ErrProjectNotFound,
			status: http.StatusNotFound,
			detail: "Belay has no retained evidence for this project.",
		},
		{
			name:   "issue not found",
			err:    classifiedMissionPackHTTPError("issue_not_found"),
			status: http.StatusNotFound,
			detail: "The selected issue is unavailable.",
		},
		{
			name:   "deadline",
			err:    context.DeadlineExceeded,
			status: http.StatusServiceUnavailable,
			detail: "Mission Pack preparation timed out.",
		},
		{
			name:   "local data failure",
			err:    errors.New("private store failure"),
			status: http.StatusInternalServerError,
			detail: "Belay could not prepare this Mission Pack.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &missionPackHTTPService{err: test.err}
			server := newMissionPackHTTPTestServer(t, service)
			request := missionPackHTTPRequest(
				"/v1/mission-pack?issue_id=issue-1",
				true,
			)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					response.Code,
					test.status,
					response.Body.String(),
				)
			}
			var value problem
			if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if value.Detail != test.detail ||
				strings.Contains(response.Body.String(), "private") {
				t.Fatalf("problem = %+v", value)
			}
		})
	}
}

func newMissionPackHTTPTestServer(
	t *testing.T,
	service MissionPackService,
) *Server {
	t.Helper()
	server, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithMissionPackService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func missionPackHTTPRequest(target string, authorized bool) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+target, nil)
	request.RemoteAddr = "127.0.0.1:1234"
	if authorized {
		request.Header.Set("Authorization", "Bearer launch-secret")
	}
	return request
}
