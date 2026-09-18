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

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/sourcecatalog"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

type developerBriefHTTPRepository struct {
	testRepository
	now        time.Time
	sessionErr error
	issueErr   error
	familyErr  error
}

func (repository *developerBriefHTTPRepository) QuerySessions(
	_ context.Context,
	_ model.SessionQuery,
) (model.SessionPage, error) {
	if repository.sessionErr != nil {
		return model.SessionPage{}, repository.sessionErr
	}
	return model.SessionPage{
		Data:        []model.SessionSummary{repository.session()},
		Snapshot:    1,
		DataThrough: repository.now,
	}, nil
}

func (repository *developerBriefHTTPRepository) GetSession(
	_ context.Context,
	_ string,
) (model.SessionSummary, time.Time, error) {
	if repository.sessionErr != nil {
		return model.SessionSummary{}, time.Time{}, repository.sessionErr
	}
	return repository.session(), repository.now, nil
}

func (repository *developerBriefHTTPRepository) QueryIssues(
	_ context.Context,
	query model.IssueQuery,
) (model.IssuePage, error) {
	if repository.issueErr != nil {
		return model.IssuePage{}, repository.issueErr
	}
	epoch, snapshot, retention, issuedAt := issueHTTPPageMetadata(query, repository.now)
	return model.IssuePage{
		Data: []model.IssueSummary{{
			IssueID:         httpTestIssueID("b"),
			FingerprintID:   httpTestFingerprintID("b"),
			Category:        model.AttentionKindEvidenceGap,
			TitleCode:       "issue.verification_not_observed",
			Severity:        "medium",
			Confidence:      "high",
			LastObservedAt:  repository.now.Add(-time.Minute),
			OccurrenceCount: 1,
			SessionCount:    1,
			Harnesses:       []string{"codex"},
			AnalysisStatus:  model.AnalysisCurrent,
		}},
		Analysis: model.IssueAnalysisCoverage{
			CurrentSessions: 1,
			AnalysisThrough: repository.now,
			Complete:        true,
		},
		CursorEpoch:         epoch,
		Snapshot:            snapshot,
		RetentionGeneration: retention,
		IssuedAt:            issuedAt,
	}, nil
}

func (*developerBriefHTTPRepository) QueryIssueOccurrences(
	context.Context,
	model.IssueOccurrenceQuery,
) (model.IssueOccurrencePage, error) {
	return model.IssueOccurrencePage{}, nil
}

func (*developerBriefHTTPRepository) LookupSessionEvents(
	context.Context,
	model.EventLookupQuery,
) (model.EventLookupResult, error) {
	return model.EventLookupResult{}, nil
}

func (*developerBriefHTTPRepository) VisitSessionEvents(
	context.Context,
	model.EventLookupQuery,
	func(model.Event) error,
) (model.EventLookupSummary, error) {
	return model.EventLookupSummary{}, nil
}

func (repository *developerBriefHTTPRepository) QueryAttentionFamilies(
	_ context.Context,
	query model.AttentionFamilyQuery,
) (model.AttentionFamilyPage, error) {
	if repository.familyErr != nil {
		return model.AttentionFamilyPage{}, repository.familyErr
	}
	epoch, snapshot, retention, issuedAt := attentionFamilyHTTPMetadata(query, repository.now)
	code := sourcecatalog.GuardrailsSourceSignalCode
	return model.AttentionFamilyPage{
		Data: []model.AttentionFamilySummary{{
			FamilyID:              httpTestAttentionFamilyID("a"),
			GroupKey:              "mapped:attention.agent_guardrails_configuration:1:1:1",
			Kind:                  model.AttentionFamilyKindMappedUpstream,
			MappingKey:            sourcecatalog.GuardrailsConfigurationMappingKey,
			MappingVersion:        "1",
			GroupingVersion:       "1",
			RepresentativeIssueID: httpTestIssueID("a"),
			Representative: model.IssueSummary{
				IssueID:          httpTestIssueID("a"),
				TitleCode:        "issue.numbat_finding",
				SourceSignalCode: &code,
			},
			AttentionKind:        model.AttentionKindIssue,
			Severity:             "high",
			Confidence:           "high",
			FirstObservedAt:      repository.now.Add(-time.Hour),
			LastObservedAt:       repository.now.Add(-time.Minute),
			SupportingIssueCount: 1,
			OccurrenceCount:      1,
			SessionCount:         1,
			Harnesses:            []string{"codex"},
			AnalysisStatus:       model.AnalysisCurrent,
			EvidenceComplete:     true,
		}},
		Analysis: model.IssueAnalysisCoverage{
			CurrentSessions: 1,
			AnalysisThrough: repository.now,
			Complete:        true,
		},
		CursorEpoch:         epoch,
		Snapshot:            snapshot,
		RetentionGeneration: retention,
		IssuedAt:            issuedAt,
	}, nil
}

func (*developerBriefHTTPRepository) QueryAttentionFamilyMembers(
	context.Context,
	model.AttentionFamilyMemberQuery,
) (model.AttentionFamilyMemberPage, error) {
	return model.AttentionFamilyMemberPage{}, nil
}

func (repository *developerBriefHTTPRepository) session() model.SessionSummary {
	return model.SessionSummary{
		SessionID:  "session-http",
		Harness:    "codex",
		StartedAt:  repository.now.Add(-10 * time.Minute),
		EndedAt:    repository.now.Add(-time.Minute),
		EventCount: 3,
		Outcome:    "failed",
		History:    "live",
		Overview: &model.SessionOverview{
			Counts: model.SessionInsightCounts{ExplicitFailedEvents: 1},
		},
	}
}

func TestDeveloperBriefHTTPReadyStrictAndProtected(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	repository := &developerBriefHTTPRepository{now: now}
	server, err := New(readmodel.New(
		repository,
		readmodel.WithIssueRepository(repository),
		readmodel.WithIssueCursorCodec(issueHTTPCursorCodec{}),
		readmodel.WithClock(func() time.Time { return now }),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/developer-brief",
		false,
	)
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.Code)
	}
	nonLoopback := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/developer-brief",
		true,
	)
	nonLoopback.RemoteAddr = "203.0.113.10:1234"
	nonLoopbackResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(nonLoopbackResponse, nonLoopback)
	if nonLoopbackResponse.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d", nonLoopbackResponse.Code)
	}

	request := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/developer-brief",
		true,
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("security headers = %v", response.Header())
	}
	var brief readmodel.DeveloperBrief
	if err := json.NewDecoder(response.Body).Decode(&brief); err != nil {
		t.Fatal(err)
	}
	if brief.Status != readmodel.BriefStatusReady ||
		brief.ProjectionVersion != readmodel.DeveloperBriefProjectionVersion ||
		len(brief.ActionCards) != 3 {
		t.Fatalf("brief = %+v", brief)
	}

	for _, query := range []string{"?limit=1", "?x=", "?x=1&x=2", "?=value"} {
		badRequest := issueHTTPRequest(
			http.MethodGet,
			"http://127.0.0.1/v1/developer-brief"+query,
			true,
		)
		badResponse := httptest.NewRecorder()
		server.Handler().ServeHTTP(badResponse, badRequest)
		if badResponse.Code != http.StatusBadRequest {
			t.Fatalf("query %q status = %d body=%s", query, badResponse.Code, badResponse.Body.String())
		}
	}
}

func TestDeveloperBriefHTTPAllSourcesUnavailableUsesFixedProblem(t *testing.T) {
	privateError := errors.New("PRIVATE_REPOSITORY_ERROR")
	repository := &developerBriefHTTPRepository{
		now:        time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC),
		sessionErr: privateError,
		issueErr:   privateError,
		familyErr:  privateError,
	}
	server, err := New(readmodel.New(
		repository,
		readmodel.WithIssueRepository(repository),
		readmodel.WithIssueCursorCodec(issueHTTPCursorCodec{}),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/developer-brief",
		true,
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(response.Body.String(), "belay.local/developer-brief-unavailable") ||
		strings.Contains(response.Body.String(), privateError.Error()) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestSessionHTTPAddsDiagnosisWithoutChangingCoreFields(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	repository := &developerBriefHTTPRepository{now: now}
	server, err := New(readmodel.New(
		repository,
		readmodel.WithIssueRepository(repository),
		readmodel.WithIssueCursorCodec(issueHTTPCursorCodec{}),
		readmodel.WithClock(func() time.Time { return now }),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/sessions/session-http",
		true,
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var detail readmodel.SessionDetailWithDiagnosis
	if err := json.NewDecoder(response.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.SchemaVersion != readmodel.SchemaVersion ||
		detail.Data.SessionID != "session-http" ||
		detail.DataThrough != now ||
		detail.Diagnosis.ProjectionVersion != readmodel.SessionDiagnosisProjectionVersion ||
		detail.Diagnosis.Summary.State != readmodel.SessionDiagnosisReviewedFindings ||
		len(detail.Diagnosis.ActionCards) == 0 ||
		detail.Diagnosis.ActionCards[0].NextStep.Kind != readmodel.BriefTargetSession ||
		detail.Diagnosis.ActionCards[0].NextStep.Label != "Review session activity" ||
		detail.Diagnosis.ActionCards[0].NextStep.SessionID == nil ||
		*detail.Diagnosis.ActionCards[0].NextStep.SessionID != "session-http" ||
		detail.Diagnosis.ActionCards[0].NextStep.IssueID != nil ||
		detail.Diagnosis.ActionCards[0].NextStep.ViewCursor != nil {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestSessionHTTPCoreErrorPrecedesDiagnosis(t *testing.T) {
	repository := &developerBriefHTTPRepository{
		now:        time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC),
		sessionErr: readmodel.ErrNotFound,
		issueErr:   errors.New("diagnosis must not run"),
	}
	server, err := New(readmodel.New(
		repository,
		readmodel.WithIssueRepository(repository),
		readmodel.WithIssueCursorCodec(issueHTTPCursorCodec{}),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	request := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/sessions/missing",
		true,
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}
