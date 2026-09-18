package localhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/localaction"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

const (
	httpFixIssueID      = "iss_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	httpFixAnnotationID = "fxa_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	httpFixKey          = "12345678-1234-4234-9234-123456789abc"
)

type fixHTTPService struct {
	eligibility localaction.FixEligibility
	prepareErr  error
	record      localaction.FixAttemptResult
	recordErr   error
	retraction  localaction.FixRetractionResult
	retractErr  error
	page        localaction.FixAttemptPage
	pageErr     error

	recordCalls    int
	prepareIssueID string
	prepareClaims  model.IssueViewClaims
}

func (s *fixHTTPService) PrepareFixAttempt(
	_ context.Context,
	issueID string,
	claims model.IssueViewClaims,
) (localaction.FixEligibility, error) {
	s.prepareIssueID = issueID
	s.prepareClaims = claims
	return s.eligibility, s.prepareErr
}

func (s *fixHTTPService) RecordFixAttempt(
	context.Context,
	string,
	string,
	model.FixChangeKind,
	string,
) (localaction.FixAttemptResult, error) {
	s.recordCalls++
	return s.record, s.recordErr
}

func (s *fixHTTPService) RetractFixAttempt(
	context.Context,
	string,
	string,
	model.FixRetractionReason,
	string,
) (localaction.FixRetractionResult, error) {
	return s.retraction, s.retractErr
}

func (s *fixHTTPService) ListFixAttempts(
	context.Context,
	string,
	int,
	string,
) (localaction.FixAttemptPage, error) {
	return s.page, s.pageErr
}

func TestFixRoutesRequireExplicitCapabilityAndTrustedListener(t *testing.T) {
	withoutFix, err := New(readmodel.New(testRepository{}), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := fixHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues/"+httpFixIssueID+"/fixes",
		nil,
	)
	response := httptest.NewRecorder()
	withoutFix.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("core-only fix route status = %d", response.Code)
	}

	service := &fixHTTPService{record: localaction.FixAttemptResult{
		Annotation: testFixAnnotation(),
	}}
	withFix, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithFixService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	request = validFixWriteRequest(
		http.MethodPost,
		"http://127.0.0.1:43123/v1/issues/"+httpFixIssueID+"/fixes",
		`{"action_token":"signed","change_kind":"code_change"}`,
		"record-fix-attempt.v1",
	)
	response = httptest.NewRecorder()
	withFix.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("untrusted direct handler status = %d body=%s", response.Code, response.Body.String())
	}
	if service.recordCalls != 0 {
		t.Fatal("fail-closed direct write reached action service")
	}
}

func TestFixWriteSecurityParsingAndPrecedence(t *testing.T) {
	service := &fixHTTPService{record: localaction.FixAttemptResult{
		Annotation: testFixAnnotation(),
	}}
	server, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithFixService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	const listener = "127.0.0.1:43123"
	handler := server.handler(listener)
	target := "http://" + listener + "/v1/issues/" + httpFixIssueID + "/fixes"
	validBody := `{"action_token":"signed","change_kind":"code_change"}`

	tests := []struct {
		name   string
		mutate func(*http.Request)
		body   string
		status int
	}{
		{
			name: "authentication before origin",
			mutate: func(request *http.Request) {
				request.Header.Del("Authorization")
				request.Header.Set("Origin", "http://evil.example")
			},
			status: http.StatusUnauthorized,
		},
		{
			name: "origin before content type",
			mutate: func(request *http.Request) {
				request.Header.Set("Origin", "http://evil.example")
				request.Header.Set("Content-Type", "text/plain")
			},
			status: http.StatusForbidden,
		},
		{
			name: "host mismatch",
			mutate: func(request *http.Request) {
				request.Host = "127.0.0.1:43124"
			},
			status: http.StatusForbidden,
		},
		{
			name: "missing origin",
			mutate: func(request *http.Request) {
				request.Header.Del("Origin")
			},
			status: http.StatusForbidden,
		},
		{
			name: "null origin",
			mutate: func(request *http.Request) {
				request.Header.Set("Origin", "null")
			},
			status: http.StatusForbidden,
		},
		{
			name: "cross site",
			mutate: func(request *http.Request) {
				request.Header.Set("Sec-Fetch-Site", "cross-site")
			},
			status: http.StatusForbidden,
		},
		{
			name: "wrong intent",
			mutate: func(request *http.Request) {
				request.Header.Set("X-Belay-Intent", "other")
			},
			status: http.StatusForbidden,
		},
		{
			name: "duplicate origin",
			mutate: func(request *http.Request) {
				request.Header.Add("Origin", "http://"+listener)
			},
			status: http.StatusForbidden,
		},
		{
			name: "bad content type",
			mutate: func(request *http.Request) {
				request.Header.Set("Content-Type", "text/plain")
			},
			status: http.StatusUnsupportedMediaType,
		},
		{
			name: "bad charset",
			mutate: func(request *http.Request) {
				request.Header.Set("Content-Type", "application/json; charset=iso-8859-1")
			},
			status: http.StatusUnsupportedMediaType,
		},
		{
			name: "encoded",
			mutate: func(request *http.Request) {
				request.Header.Set("Content-Encoding", "gzip")
			},
			status: http.StatusUnsupportedMediaType,
		},
		{
			name:   "body limit",
			body:   strings.Repeat("x", maxFixRequestBody+1),
			status: http.StatusRequestEntityTooLarge,
		},
		{
			name:   "malformed JSON",
			body:   `{`,
			status: http.StatusBadRequest,
		},
		{
			name:   "duplicate JSON field",
			body:   `{"action_token":"a","action_token":"b","change_kind":"code_change"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "unknown JSON field",
			body:   `{"action_token":"a","change_kind":"code_change","note":"PRIVATE_BODY_CANARY"}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "trailing JSON",
			body:   validBody + `{}`,
			status: http.StatusBadRequest,
		},
		{
			name: "unknown query",
			mutate: func(request *http.Request) {
				request.URL.RawQuery = "private=PRIVATE_QUERY_CANARY"
			},
			status: http.StatusBadRequest,
		},
		{
			name: "duplicate idempotency header",
			mutate: func(request *http.Request) {
				request.Header.Add("Idempotency-Key", httpFixKey)
			},
			status: http.StatusBadRequest,
		},
		{
			name: "duplicate authorization",
			mutate: func(request *http.Request) {
				request.Header.Add("Authorization", "Bearer launch-secret")
			},
			status: http.StatusUnauthorized,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			if body == "" {
				body = validBody
			}
			request := validFixWriteRequest(
				http.MethodPost,
				target,
				body,
				"record-fix-attempt.v1",
			)
			request.Header.Set("X-Request-ID", "PRIVATE_REQUEST_CANARY")
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.status, response.Body.String())
			}
			for _, canary := range []string{
				"PRIVATE_BODY_CANARY",
				"PRIVATE_QUERY_CANARY",
				"PRIVATE_REQUEST_CANARY",
			} {
				if strings.Contains(response.Body.String(), canary) {
					t.Fatalf("response reflected %q: %s", canary, response.Body.String())
				}
			}
		})
	}
	if service.recordCalls != 0 {
		t.Fatalf("rejected writes reached action service %d times", service.recordCalls)
	}

	request := validFixWriteRequest(
		http.MethodPost,
		target,
		validBody,
		"record-fix-attempt.v1",
	)
	request.Header.Set("Forwarded", "host=evil.example;proto=https")
	request.Header.Set("X-Forwarded-Host", "evil.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("forwarded-header request status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestFixResponsesUseExactContractsAndNullRetractionFields(t *testing.T) {
	now := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	annotation := testFixAnnotation()
	service := &fixHTTPService{
		record: localaction.FixAttemptResult{Annotation: annotation},
		page: localaction.FixAttemptPage{
			Data:                []model.FixAnnotation{annotation},
			ReturnedCount:       1,
			Limit:               20,
			EvidenceEvaluatedAt: now,
		},
	}
	server, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithFixService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	const listener = "127.0.0.1:43123"
	handler := server.handler(listener)
	request := validFixWriteRequest(
		http.MethodPost,
		"http://"+listener+"/v1/issues/"+httpFixIssueID+"/fixes",
		`{"action_token":"signed","change_kind":"code_change"}`,
		"record-fix-attempt.v1",
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"retraction_reason":null`) ||
		!strings.Contains(body, `"retracted_at":null`) {
		t.Fatalf("active annotation omitted explicit nulls: %s", body)
	}

	request = fixHTTPRequest(
		http.MethodGet,
		"http://"+listener+"/v1/issues/"+httpFixIssueID+"/fixes?limit=20",
		nil,
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("history status = %d body=%s", response.Code, response.Body.String())
	}
	var history fixHistoryResponse
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if history.SchemaVersion != model.FixSchemaVersion ||
		len(history.Data) != 1 ||
		history.Data[0].RetractionReason != nil ||
		history.Data[0].RetractedAt != nil ||
		!history.EvidenceEvaluatedAt.Equal(now) {
		t.Fatalf("history = %+v", history)
	}
}

func TestFixEligibilityRequiresExactViewCursorAndUsesNullActionFields(t *testing.T) {
	now := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	repository := &issueHTTPRepository{now: now}
	service := &fixHTTPService{eligibility: localaction.FixEligibility{
		Reason:               model.FixEligibilityEvidenceGap,
		ChangeCatalogVersion: model.FixChangeCatalogVersion,
	}}
	server, err := New(
		readmodel.New(
			repository,
			readmodel.WithIssueRepository(repository),
			readmodel.WithIssueCursorCodec(issueHTTPCursorCodec{}),
			readmodel.WithClock(func() time.Time { return now }),
		),
		"launch-secret",
		WithFixService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.handler("127.0.0.1:43123")
	listRequest := fixHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1:43123/v1/issues?limit=1",
		nil,
	)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("issue list status = %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var list readmodel.IssueList
	if err := json.NewDecoder(listResponse.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}

	request := fixHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1:43123/v1/issues/"+httpTestIssueID("a")+
			"/fix-eligibility?view_cursor="+list.ViewCursor,
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("eligibility status = %d body=%s", response.Code, response.Body.String())
	}
	var result fixEligibilityResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Data.Eligible ||
		result.Data.Reason != model.FixEligibilityEvidenceGap ||
		result.Data.ActionToken != nil ||
		result.Data.ExpiresAt != nil ||
		result.Data.ChangeCatalogVersion != model.FixChangeCatalogVersion {
		t.Fatalf("eligibility = %+v", result)
	}

	for _, query := range []string{
		"",
		"view_cursor=",
		"view_cursor=" + list.ViewCursor + "&view_cursor=" + list.ViewCursor,
		"view_cursor=" + list.ViewCursor + "&unknown=PRIVATE_QUERY",
		"bad=%zz",
	} {
		target := "http://127.0.0.1:43123/v1/issues/" +
			httpTestIssueID("a") + "/fix-eligibility"
		if query != "" {
			target += "?" + query
		}
		request := fixHTTPRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query %q status=%d body=%s", query, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "PRIVATE_QUERY") {
			t.Fatalf("query reflected: %s", response.Body.String())
		}
	}
}

func TestFixHTTPErrorMapping(t *testing.T) {
	service := &fixHTTPService{}
	server, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithFixService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	const listener = "127.0.0.1:43123"
	handler := server.handler(listener)
	for _, test := range []struct {
		err         error
		status      int
		problemType string
	}{
		{localaction.ErrInvalidRequest, http.StatusBadRequest, "about:blank"},
		{localaction.ErrCursorExpired, http.StatusGone, "belay.local/cursor-expired"},
		{localaction.ErrNotFound, http.StatusNotFound, "about:blank"},
		{localaction.ErrIneligibleIssue, http.StatusConflict, "belay.local/ineligible-fix-annotation"},
		{localaction.ErrIdempotencyConflict, http.StatusConflict, "belay.local/idempotency-conflict"},
		{errors.New("PRIVATE_INTERNAL_CANARY"), http.StatusInternalServerError, "about:blank"},
	} {
		service.recordErr = test.err
		request := validFixWriteRequest(
			http.MethodPost,
			"http://"+listener+"/v1/issues/"+httpFixIssueID+"/fixes",
			`{"action_token":"signed","change_kind":"code_change"}`,
			"record-fix-attempt.v1",
		)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("error %v status=%d body=%s", test.err, response.Code, response.Body.String())
		}
		var value problem
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		if value.Type != test.problemType ||
			strings.Contains(response.Body.String(), "PRIVATE_INTERNAL_CANARY") {
			t.Fatalf("error %v problem=%+v", test.err, value)
		}
	}
}

func TestStartedServerUsesActualListenerForWriteOriginAndHost(t *testing.T) {
	service := &fixHTTPService{record: localaction.FixAttemptResult{
		Annotation: testFixAnnotation(),
	}}
	server, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithFixService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	running, err := server.Start(ctx, "127.0.0.1:0")
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skip("sandbox does not permit loopback listeners")
		}
		t.Fatal(err)
	}
	defer running.Close(context.Background())
	request, err := http.NewRequest(
		http.MethodPost,
		running.URL+"/v1/issues/"+httpFixIssueID+"/fixes",
		strings.NewReader(`{"action_token":"signed","change_kind":"code_change"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+running.Token)
	request.Header.Set("Origin", running.URL)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.Header.Set("Content-Encoding", "identity")
	request.Header.Set("Idempotency-Key", httpFixKey)
	request.Header.Set("X-Belay-Intent", "record-fix-attempt.v1")
	request.Header.Set("X-Forwarded-Host", "evil.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.StatusCode, body)
	}
}

func fixHTTPRequest(
	method string,
	target string,
	body io.Reader,
) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	return request
}

func validFixWriteRequest(
	method string,
	target string,
	body string,
	intent string,
) *http.Request {
	request := fixHTTPRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Origin", "http://"+request.Host)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", httpFixKey)
	request.Header.Set("X-Belay-Intent", intent)
	return request
}

func testFixAnnotation() model.FixAnnotation {
	at := time.Date(2026, 9, 8, 18, 0, 0, 0, time.UTC)
	return model.FixAnnotation{
		AnnotationID:              httpFixAnnotationID,
		IssueID:                   httpFixIssueID,
		AnchorRevisionID:          "revision-1",
		AnchorOccurrenceID:        "occurrence-1",
		AnchorSessionID:           "session-1",
		FingerprintID:             "ifp_cccccccccccccccccccccccccccccccccccccccccccccccccccc",
		FingerprintVersion:        "1",
		Origin:                    "belay",
		DetectorID:                "explicit_command_failure",
		DetectorVersion:           "1",
		ScopeQuality:              model.ScopeResolved,
		IssueSnapshotGeneration:   7,
		AnchorAnalysisGeneration:  6,
		AnchorFirstObservedAt:     at.Add(-time.Minute),
		AnchorLastObservedAt:      at,
		ChangeKind:                model.FixChangeCode,
		ChangeCatalogVersion:      model.FixChangeCatalogVersion,
		RecordedVia:               model.FixRecordedViaLocalUI,
		RecordedAt:                at,
		MonitorFrom:               at,
		EvidenceCurrentlyRetained: model.FixEvidenceAvailable,
		State:                     model.FixStateActive,
	}
}
