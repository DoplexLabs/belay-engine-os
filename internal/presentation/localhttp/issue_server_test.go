package localhttp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

const httpTestEventID = "01890f2e-6d4b-7c8a-9b0c-123456789abc"

type issueHTTPRepository struct {
	testRepository
	issueQuery      model.IssueQuery
	occurrenceQuery model.IssueOccurrenceQuery
	eventQuery      model.EventLookupQuery
	issueErr        error
	issueAbsent     bool
	issueSummary    *model.IssueSummary
	now             time.Time
}

type issueHTTPCursorCodec struct{}

func (issueHTTPCursorCodec) SealIssueCursor(payload []byte) (string, error) {
	sum := sha256.Sum256(append([]byte("localhttp-test:"), payload...))
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func (issueHTTPCursorCodec) OpenIssueCursor(value string) ([]byte, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return nil, model.ErrIssueCursorInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, model.ErrIssueCursorInvalid
	}
	expected, _ := issueHTTPCursorCodec{}.SealIssueCursor(payload)
	if expected != value {
		return nil, model.ErrIssueCursorInvalid
	}
	return payload, nil
}

func (repository *issueHTTPRepository) QueryIssues(
	_ context.Context,
	query model.IssueQuery,
) (model.IssuePage, error) {
	repository.issueQuery = query
	if repository.issueErr != nil {
		return model.IssuePage{}, repository.issueErr
	}
	epoch, snapshot, retentionGeneration, issuedAt := issueHTTPPageMetadata(
		query,
		repository.now,
	)
	if repository.issueAbsent && query.Filter.IssueID != "" {
		return model.IssuePage{
			CursorEpoch:         epoch,
			Snapshot:            snapshot,
			RetentionGeneration: retentionGeneration,
			IssuedAt:            issuedAt,
		}, nil
	}
	issueID := httpTestIssueID("a")
	if query.Filter.IssueID != "" {
		issueID = query.Filter.IssueID
	}
	summary := model.IssueSummary{
		IssueID:        issueID,
		FingerprintID:  httpTestFingerprintID("b"),
		Severity:       "high",
		TitleCode:      "issue.explicit_command_failure",
		SessionCount:   2,
		LastObservedAt: time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
	}
	if repository.issueSummary != nil {
		summary = *repository.issueSummary
		summary.IssueID = issueID
	}
	return model.IssuePage{
		Data: []model.IssueSummary{summary},
		Analysis: model.IssueAnalysisCoverage{
			CurrentSessions: 1,
			Complete:        true,
		},
		CursorEpoch:         epoch,
		Snapshot:            snapshot,
		RetentionGeneration: retentionGeneration,
		IssuedAt:            issuedAt,
		HasMore:             query.Filter.IssueID == "",
	}, nil
}

func TestIssueHTTPReturnsFixedSourceSignalCatalog(t *testing.T) {
	sourceSignalCode := "tamper.guardrails_off"
	repository := &issueHTTPRepository{
		issueSummary: &model.IssueSummary{
			FingerprintID:    httpTestFingerprintID("b"),
			Origin:           "numbat",
			TitleCode:        "issue.numbat_finding",
			SourceSignalCode: &sourceSignalCode,
			Severity:         "high",
			SessionCount:     1,
			LastObservedAt:   time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
		},
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
		"http://127.0.0.1/v1/issues/"+httpTestIssueID("a")+"/occurrences?limit=1",
		true,
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var detail readmodel.IssueDetail
	if err := json.NewDecoder(response.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Catalog.CatalogVersion != readmodel.IssueCatalogVersion ||
		detail.Catalog.DisplayTitle != "Fewer approval prompts enabled" ||
		detail.Catalog.ObservationStatement != "Belay recorded a setting that lets actions already permitted by the agent run without asking for approval each time." ||
		detail.Catalog.Caveat != "This setting may be intentional. The record does not show whether an action bypassed a prompt or caused harm." ||
		detail.Catalog.NextEvidenceAction != "review_agent_permissions" ||
		detail.Catalog.SourceSignalCode == nil ||
		*detail.Catalog.SourceSignalCode != sourceSignalCode ||
		detail.Catalog.SourceSignalCatalogVersion != readmodel.SourceSignalCatalogVersion ||
		detail.Catalog.SourceSignalCatalogStatus != "known" {
		t.Fatalf("catalog = %+v", detail.Catalog)
	}

	unknownSourceSignalCode := "custom.imported_signal"
	repository.issueSummary.SourceSignalCode = &unknownSourceSignalCode
	unknownResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unknownResponse, request)
	if unknownResponse.Code != http.StatusOK {
		t.Fatalf("unknown status = %d body=%s", unknownResponse.Code, unknownResponse.Body.String())
	}
	var unknownDetail readmodel.IssueDetail
	if err := json.NewDecoder(unknownResponse.Body).Decode(&unknownDetail); err != nil {
		t.Fatal(err)
	}
	if unknownDetail.Catalog.DisplayTitle != "Imported finding—not yet explained by Belay" ||
		unknownDetail.Catalog.ObservationStatement != "Belay retained this imported finding but does not yet have a reviewed explanation." ||
		unknownDetail.Catalog.Caveat != "Review the cited evidence; Belay does not infer its impact or recommend a change." ||
		unknownDetail.Catalog.SourceSignalCatalogStatus != "unknown" ||
		unknownDetail.Catalog.SourceSignalCode == nil ||
		*unknownDetail.Catalog.SourceSignalCode != unknownSourceSignalCode ||
		strings.Contains(unknownDetail.Catalog.ObservationStatement, "supported signal") ||
		unknownDetail.Catalog.NextEvidenceAction == "review_agent_permissions" {
		t.Fatalf("unknown catalog = %+v", unknownDetail.Catalog)
	}
}

func TestIssueHTTPReturnsExplicitToolFailureCatalog(t *testing.T) {
	repository := &issueHTTPRepository{
		issueSummary: &model.IssueSummary{
			FingerprintID:  httpTestFingerprintID("b"),
			Origin:         "belay",
			DetectorID:     "explicit_tool_failure",
			TitleCode:      "issue.explicit_tool_failure",
			Category:       "tool_failure",
			Severity:       "low",
			SessionCount:   1,
			LastObservedAt: time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
		},
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
		"http://127.0.0.1/v1/issues/"+httpTestIssueID("a")+"/occurrences?limit=1",
		true,
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var detail readmodel.IssueDetail
	if err := json.NewDecoder(response.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Catalog.DisplayTitle != "Tool call failed" ||
		detail.Catalog.ObservationStatement != "The agent reported that a tool call failed." ||
		detail.Catalog.Caveat != "Belay does not know why it failed or whether a later attempt succeeded." ||
		detail.Catalog.NextEvidenceAction != "inspect_cited_events" {
		t.Fatalf("catalog = %+v", detail.Catalog)
	}
}

func TestIssueHTTPReturnsRetainedVerificationGapCatalog(t *testing.T) {
	repository := &issueHTTPRepository{
		issueSummary: &model.IssueSummary{
			FingerprintID:  httpTestFingerprintID("b"),
			Origin:         "belay",
			DetectorID:     "retained_verification_gap_after_changes",
			TitleCode:      "issue.retained_verification_gap_after_changes",
			Category:       "evidence_gap",
			Severity:       "info",
			SessionCount:   1,
			LastObservedAt: time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
		},
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
		"http://127.0.0.1/v1/issues/"+httpTestIssueID("a")+"/occurrences?limit=1",
		true,
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var detail readmodel.IssueDetail
	if err := json.NewDecoder(response.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Catalog.DisplayTitle != "No recognized verification retained after changes" ||
		detail.Catalog.ObservationStatement != "Belay's retained evidence contains no recognized verification command after the final recorded file change and before the session ended." ||
		detail.Catalog.Caveat != "This does not show that verification did not occur; Belay only checks supported commands in retained activity." ||
		detail.Catalog.NextEvidenceAction != "inspect_cited_events" {
		t.Fatalf("catalog = %+v", detail.Catalog)
	}
}

func issueHTTPPageMetadata(
	query model.IssueQuery,
	now time.Time,
) (string, int64, int64, time.Time) {
	if query.Snapshot != 0 {
		return query.CursorEpoch, query.Snapshot, query.RetentionGeneration, query.IssuedAt
	}
	if now.IsZero() {
		now = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	}
	return "http-epoch", 31, 4, now.UTC()
}

func (repository *issueHTTPRepository) QueryIssueOccurrences(
	_ context.Context,
	query model.IssueOccurrenceQuery,
) (model.IssueOccurrencePage, error) {
	repository.occurrenceQuery = query
	return model.IssueOccurrencePage{
		Data: []model.IssueOccurrence{{
			OccurrenceID:   "occurrence-1",
			IssueID:        query.IssueID,
			SessionID:      "session-1",
			LastObservedAt: time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
		}},
		CursorEpoch:         query.CursorEpoch,
		Snapshot:            query.Snapshot,
		RetentionGeneration: query.RetentionGeneration,
		IssuedAt:            query.IssuedAt,
	}, nil
}

func (repository *issueHTTPRepository) LookupSessionEvents(
	_ context.Context,
	query model.EventLookupQuery,
) (model.EventLookupResult, error) {
	repository.eventQuery = query
	return model.EventLookupResult{
		Data: []model.Event{{
			EventID:    httpTestEventID,
			OccurredAt: time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
		}},
		RequestedCount:  len(query.EventIDs),
		FoundCount:      1,
		MissingEventIDs: []string{},
		DataThrough:     time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC),
	}, nil
}

func (repository *issueHTTPRepository) VisitSessionEvents(
	_ context.Context,
	query model.EventLookupQuery,
	visit func(model.Event) error,
) (model.EventLookupSummary, error) {
	result, err := repository.LookupSessionEvents(context.Background(), query)
	if err != nil {
		return model.EventLookupSummary{}, err
	}
	for _, event := range result.Data {
		if err := visit(event); err != nil {
			return model.EventLookupSummary{}, err
		}
	}
	return model.EventLookupSummary{
		RequestedCount:  result.RequestedCount,
		FoundCount:      result.FoundCount,
		MissingCount:    result.MissingCount,
		MissingEventIDs: result.MissingEventIDs,
		DataThrough:     result.DataThrough,
	}, nil
}

func TestIssueAndExactEventRoutesAreAuthorizedAndUseSharedContracts(t *testing.T) {
	repository := &issueHTTPRepository{}
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	readService := readmodel.New(
		repository,
		readmodel.WithIssueRepository(repository),
		readmodel.WithIssueCursorCodec(issueHTTPCursorCodec{}),
		readmodel.WithClock(func() time.Time { return now }),
	)
	fixService := &fixHTTPService{}
	server, err := New(
		readService,
		"launch-secret",
		WithFixService(fixService),
	)
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues",
		false,
	)
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized issue status = %d", unauthorizedResponse.Code)
	}

	nonLoopback := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues",
		true,
	)
	nonLoopback.RemoteAddr = "203.0.113.9:1234"
	nonLoopbackResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(nonLoopbackResponse, nonLoopback)
	if nonLoopbackResponse.Code != http.StatusForbidden {
		t.Fatalf("non-loopback issue status = %d", nonLoopbackResponse.Code)
	}

	listRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues?limit=1&severity=HIGH&category=COMMAND_FAILURE&harness=Codex&origin=BELAY&analysis_status=CURRENT&observed_after=2026-09-08T10:00:00-02:00&recurrence=REPEATED&session_id=session-1&fingerprint_id="+strings.ToUpper(httpTestFingerprintID("b"))+"&attention_kind=ALL&experimental=INCLUDE",
		true,
	)
	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("issue list status = %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var list readmodel.IssueList
	if err := json.NewDecoder(listResponse.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if list.ViewCursor == "" ||
		list.NextCursor == nil ||
		list.ProjectionVersion != readmodel.IssueProjectionVersion ||
		list.Selection.AttentionKind != model.AttentionKindAll ||
		!list.Selection.IncludesEvidenceGaps ||
		!list.Selection.IncludesExperimental ||
		!list.HasMore ||
		list.ReturnedCount != 1 {
		t.Fatalf("issue list response = %+v", list)
	}
	query := repository.issueQuery
	if query.Filter.Severity != "high" ||
		query.Filter.Category != "command_failure" ||
		query.Filter.Harness != "Codex" ||
		query.Filter.Origin != "belay" ||
		query.Filter.AnalysisStatus != model.AnalysisCurrent ||
		query.Filter.ObservedAfter == nil ||
		query.Filter.ObservedAfter.Format(time.RFC3339Nano) != "2026-09-08T12:00:00Z" ||
		query.Filter.Recurrence != "repeated" ||
		query.Filter.SessionID != "session-1" ||
		query.Filter.FingerprintID != httpTestFingerprintID("b") ||
		query.Filter.AttentionKind != model.AttentionKindAll ||
		query.Filter.Experimental != model.ExperimentalInclude {
		t.Fatalf("HTTP issue query = %+v", query)
	}

	detailRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues/"+httpTestIssueID("a")+"/occurrences?limit=1&view_cursor="+list.ViewCursor,
		true,
	)
	detailResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("issue detail status = %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail readmodel.IssueDetail
	if err := json.NewDecoder(detailResponse.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Data.Issue.IssueID != httpTestIssueID("a") ||
		detail.ViewCursor == "" ||
		len(detail.Data.Occurrences) != 1 ||
		repository.issueQuery.Snapshot != 31 ||
		repository.occurrenceQuery.Snapshot != 31 ||
		detail.Catalog.TitleCode != "issue.explicit_command_failure" ||
		detail.Catalog.DisplayTitle != "Command failed" ||
		detail.Catalog.ObservationStatement != "The agent reported that a command failed." ||
		detail.Catalog.Caveat != "This does not identify why the command failed or whether a later attempt succeeded." ||
		detail.Catalog.SourceSignalCode != nil ||
		detail.Catalog.SourceSignalCatalogStatus != "not_applicable" ||
		detail.GlobalAnalysisCoverage.CurrentSessions != 1 ||
		!repository.issueQuery.IssuedAt.Equal(now) ||
		!repository.occurrenceQuery.IssuedAt.Equal(now) {
		t.Fatalf("snapshot-stable detail=%+v summary=%+v occurrence=%+v",
			detail, repository.issueQuery, repository.occurrenceQuery)
	}

	targetIssueID := httpTestIssueID("c")
	exactRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues/"+targetIssueID+
			"/occurrences?limit=1",
		true,
	)
	exactResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(exactResponse, exactRequest)
	if exactResponse.Code != http.StatusOK {
		t.Fatalf("fresh exact issue status = %d body=%s",
			exactResponse.Code, exactResponse.Body.String())
	}
	var exact readmodel.IssueDetail
	if err := json.NewDecoder(exactResponse.Body).Decode(&exact); err != nil {
		t.Fatal(err)
	}
	if exact.Data.Issue.IssueID != targetIssueID ||
		exact.ViewCursor == "" ||
		repository.issueQuery.Filter.IssueID != targetIssueID {
		t.Fatalf("fresh exact issue = %+v query=%+v",
			exact, repository.issueQuery)
	}
	eligibilityRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues/"+targetIssueID+
			"/fix-eligibility?view_cursor="+exact.ViewCursor,
		true,
	)
	eligibilityResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(eligibilityResponse, eligibilityRequest)
	if eligibilityResponse.Code != http.StatusOK {
		t.Fatalf("exact issue eligibility status = %d body=%s",
			eligibilityResponse.Code, eligibilityResponse.Body.String())
	}
	if fixService.prepareIssueID != targetIssueID ||
		fixService.prepareClaims.CursorEpoch != "http-epoch" ||
		fixService.prepareClaims.Snapshot != 31 ||
		fixService.prepareClaims.RetentionGeneration != 4 ||
		!fixService.prepareClaims.IssuedAt.Equal(now) {
		t.Fatalf("exact eligibility handoff issue=%q claims=%+v",
			fixService.prepareIssueID, fixService.prepareClaims)
	}

	lookupRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/sessions/session-1/events/lookup?event_id="+httpTestEventID+"&event_id="+httpTestEventID,
		true,
	)
	lookupResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(lookupResponse, lookupRequest)
	if lookupResponse.Code != http.StatusOK {
		t.Fatalf("event lookup status = %d body=%s", lookupResponse.Code, lookupResponse.Body.String())
	}
	var lookup readmodel.EventLookup
	if err := json.NewDecoder(lookupResponse.Body).Decode(&lookup); err != nil {
		t.Fatal(err)
	}
	if len(repository.eventQuery.EventIDs) != 1 ||
		lookup.RequestedCount != 1 ||
		lookup.FoundCount != 1 ||
		lookup.MissingEventIDs == nil {
		t.Fatalf("event lookup query=%+v response=%+v", repository.eventQuery, lookup)
	}
}

func TestIssueHTTPErrorMatrixIsFixedAndNonReflective(t *testing.T) {
	repository := &issueHTTPRepository{}
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	server, err := New(readmodel.New(
		repository,
		readmodel.WithIssueRepository(repository),
		readmodel.WithIssueCursorCodec(issueHTTPCursorCodec{}),
		readmodel.WithClock(func() time.Time { return now }),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}

	assertProblem := func(path string, status int, forbidden string) problem {
		t.Helper()
		request := issueHTTPRequest(http.MethodGet, "http://127.0.0.1"+path, true)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("%s status = %d body=%s", path, response.Code, response.Body.String())
		}
		if forbidden != "" && strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("%s reflected private input %q", path, forbidden)
		}
		var value problem
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	assertProblem(
		"/v1/issues/PRIVATE_ISSUE_CANARY/occurrences?view_cursor=PRIVATE_CURSOR_CANARY",
		http.StatusBadRequest,
		"PRIVATE_ISSUE_CANARY",
	)
	assertProblem(
		"/v1/sessions/session-1/events/lookup?event_id=PRIVATE_EVENT_CANARY",
		http.StatusBadRequest,
		"PRIVATE_EVENT_CANARY",
	)

	repository.issueAbsent = true
	assertProblem(
		"/v1/issues/"+httpTestIssueID("a")+"/occurrences",
		http.StatusNotFound,
		httpTestIssueID("a"),
	)
	repository.issueAbsent = false

	listRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues?limit=1",
		true,
	)
	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("cursor source status = %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var list readmodel.IssueList
	if err := json.NewDecoder(listResponse.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	assertProblem(
		"/v1/issues/"+httpTestIssueID("a")+"/occurrences?cursor="+*list.NextCursor,
		http.StatusBadRequest,
		*list.NextCursor,
	)
	assertProblem(
		"/v1/issues/"+httpTestIssueID("a")+"/occurrences?cursor=PRIVATE_PAGE_CURSOR&view_cursor=PRIVATE_VIEW_CURSOR",
		http.StatusBadRequest,
		"PRIVATE_PAGE_CURSOR",
	)
	for _, query := range []string{
		"unknown=PRIVATE_QUERY_CANARY",
		"limit=",
		"limit=0",
		"limit=-1",
		"limit=not-a-number",
		"limit=101",
		"limit=1&limit=2",
		"cursor=",
		"view_cursor=",
		"cursor=PRIVATE_PAGE_CURSOR&limit=1",
	} {
		assertProblem(
			"/v1/issues/"+httpTestIssueID("a")+"/occurrences?"+query,
			http.StatusBadRequest,
			"PRIVATE_",
		)
	}

	repository.issueErr = model.ErrIssueSnapshotExpired
	expired := assertProblem(
		"/v1/issues?cursor="+*list.NextCursor,
		http.StatusGone,
		*list.NextCursor,
	)
	if expired.Type != "belay.local/cursor-expired" ||
		expired.Title != "Cursor expired" ||
		expired.Detail != "The issue view changed. Restart pagination without a cursor." {
		t.Fatalf("expired problem = %+v", expired)
	}

	repository.issueErr = model.ErrIssueSnapshotInvalid
	assertProblem(
		"/v1/issues?cursor="+*list.NextCursor,
		http.StatusBadRequest,
		*list.NextCursor,
	)

	repository.issueErr = errors.New("PRIVATE_INTERNAL_CANARY")
	assertProblem(
		"/v1/issues",
		http.StatusInternalServerError,
		"PRIVATE_INTERNAL_CANARY",
	)
}

func TestExactEventLookupRejectsEveryParameterExceptRepeatedEventID(t *testing.T) {
	repository := &issueHTTPRepository{}
	server, err := New(readmodel.New(
		repository,
		readmodel.WithIssueRepository(repository),
		readmodel.WithIssueCursorCodec(issueHTTPCursorCodec{}),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{
		"event_id=" + httpTestEventID + "&cursor=PRIVATE_CURSOR_CANARY",
		"event_id=" + httpTestEventID + "&query=PRIVATE_QUERY_CANARY",
		"event_id=" + httpTestEventID + "&unknown=PRIVATE_UNKNOWN_CANARY",
		"event_id=" + httpTestEventID + "&bad=%zz",
	} {
		request := issueHTTPRequest(
			http.MethodGet,
			"http://127.0.0.1/v1/sessions/session-1/events/lookup?"+query,
			true,
		)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query %q status = %d body=%s", query, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "PRIVATE_") {
			t.Fatalf("query %q reflected input: %s", query, response.Body.String())
		}
	}
	if repository.eventQuery.SessionID != "" {
		t.Fatalf("rejected query reached repository: %+v", repository.eventQuery)
	}

	request := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/sessions/session-1/events/lookup?event_id="+httpTestEventID+"&event_id="+httpTestEventID,
		true,
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("repeated event_id status = %d body=%s", response.Code, response.Body.String())
	}
	if len(repository.eventQuery.EventIDs) != 1 {
		t.Fatalf("deduplicated event query = %+v", repository.eventQuery)
	}
}

func issueHTTPRequest(method, target string, authorized bool) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.RemoteAddr = "127.0.0.1:1234"
	if authorized {
		request.Header.Set("Authorization", "Bearer launch-secret")
	}
	return request
}

func httpTestIssueID(character string) string {
	return "iss_" + strings.Repeat(character, 52)
}

func httpTestFingerprintID(character string) string {
	return "ifp_" + strings.Repeat(character, 52)
}
