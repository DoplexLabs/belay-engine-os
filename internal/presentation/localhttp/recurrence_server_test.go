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
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

type recurrenceHTTPRepository struct {
	testRepository
	listQuery        model.FixMonitoringQuery
	detailQuery      model.FixMonitoringDetailQuery
	observationQuery model.FixRecurrenceObservationQuery
	err              error
	now              time.Time
	issueID          string
	annotationID     string
}

func (repository *recurrenceHTTPRepository) QueryFixMonitoring(
	_ context.Context,
	query model.FixMonitoringQuery,
) (model.FixMonitoringPage, error) {
	repository.listQuery = query
	if repository.err != nil {
		return model.FixMonitoringPage{}, repository.err
	}
	title := "explicit_command_failure"
	severity := "high"
	return model.FixMonitoringPage{
		Data: []model.FixMonitoringSummary{{
			FixAttemptMonitoring: model.FixAttemptMonitoring{
				AnnotationID:       repository.annotationID,
				IssueID:            repository.issueID,
				Subject:            model.FixMonitoringSubject{TitleCode: &title, Severity: &severity},
				ChangeKind:         model.FixChangeCode,
				RecordedAt:         repository.now.Add(-time.Minute),
				MonitorFrom:        repository.now.Add(-time.Minute),
				FixRecurrenceState: model.FixRecurrenceAwaitingEvidence,
			},
			ActiveAttemptCount: 1,
		}},
		Snapshot:            recurrenceHTTPSnapshot(repository.now),
		EvidenceEvaluatedAt: repository.now,
	}, nil
}

func (repository *recurrenceHTTPRepository) QueryIssueFixMonitoring(
	_ context.Context,
	query model.FixMonitoringDetailQuery,
) (model.FixMonitoringDetailPage, error) {
	repository.detailQuery = query
	if repository.err != nil {
		return model.FixMonitoringDetailPage{}, repository.err
	}
	title := "explicit_command_failure"
	return model.FixMonitoringDetailPage{
		IssueID: repository.issueID,
		Data: []model.FixAttemptMonitoring{{
			AnnotationID: repository.annotationID,
			IssueID:      repository.issueID,
			Subject: model.FixMonitoringSubject{
				TitleCode:          &title,
				Origin:             "belay",
				DetectorID:         "explicit_command_failure",
				DetectorVersion:    "1",
				FingerprintVersion: "1",
			},
			ChangeKind:                      model.FixChangeCode,
			RecordedAt:                      repository.now.Add(-time.Minute),
			MonitorFrom:                     repository.now.Add(-time.Minute),
			State:                           model.FixStateActive,
			FixRecurrenceState:              model.FixRecurrenceMatchingEvidence,
			FixRecurrenceCount:              1,
			AnchorEvidenceCurrentlyRetained: model.FixRecurrenceEvidenceAvailable,
			FutureComparisonAvailable:       true,
			HistoricalMatchingEvidenceCount: 1,
			OtherSessionObservationCount:    1,
		}},
		Snapshot:            recurrenceHTTPSnapshot(repository.now),
		EvidenceEvaluatedAt: repository.now,
	}, nil
}

func (repository *recurrenceHTTPRepository) QueryFixRecurrenceObservations(
	_ context.Context,
	query model.FixRecurrenceObservationQuery,
) (model.FixRecurrenceObservationPage, error) {
	repository.observationQuery = query
	if repository.err != nil {
		return model.FixRecurrenceObservationPage{}, repository.err
	}
	retained := 1
	missing := 0
	truncated := false
	return model.FixRecurrenceObservationPage{
		IssueID:      repository.issueID,
		AnnotationID: repository.annotationID,
		Data: []model.FixRecurrenceObservation{{
			RecurrenceID:              "fxo_" + strings.Repeat("c", 52),
			OccurrenceID:              "occurrence-1",
			SessionID:                 "session-1",
			FingerprintVersion:        "1",
			Origin:                    "belay",
			DetectorID:                "explicit_command_failure",
			DetectorVersion:           "1",
			FirstQualifyingEventAt:    repository.now.Add(time.Minute),
			LastQualifyingEventAt:     repository.now.Add(time.Minute),
			QualifyingCitationCount:   1,
			RetainedEventIDs:          []string{httpTestEventID},
			RetainedEventCount:        &retained,
			MissingEventCount:         &missing,
			EvidenceComplete:          true,
			EvidenceTruncated:         &truncated,
			EvidenceCurrentlyRetained: model.FixRecurrenceEvidenceAvailable,
			ObservedAt:                repository.now.Add(2 * time.Minute),
		}},
		Snapshot:            recurrenceHTTPSnapshot(repository.now),
		EvidenceEvaluatedAt: repository.now,
	}, nil
}

func TestFixMonitoringRoutesUseExactDTOsAndSnapshotHandoffs(t *testing.T) {
	now := time.Date(2026, 9, 8, 14, 0, 0, 0, time.UTC)
	repository := &recurrenceHTTPRepository{
		now:          now,
		issueID:      httpTestIssueID("a"),
		annotationID: "fxa_" + strings.Repeat("b", 52),
	}
	server, err := New(readmodel.New(
		repository,
		readmodel.WithFixMonitoringRepository(repository),
		readmodel.WithClock(func() time.Time { return now }),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}

	listRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/fix-monitoring?limit=1&state=AWAITING_LATER_EVIDENCE&change_kind=CODE_CHANGE&severity=HIGH&harness=Codex&recorded_after=2026-09-08T10:00:00-04:00&issue_id="+strings.ToUpper(repository.issueID)+"&include_retracted=true",
		true,
	)
	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var list readmodel.FixMonitoringList
	if err := json.NewDecoder(listResponse.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if list.SchemaVersion != model.FixMonitoringSchemaVersion ||
		list.MonitoringViewCursor == "" ||
		len(list.Data) != 1 ||
		list.Data[0].AnnotationID != repository.annotationID {
		t.Fatalf("list response = %+v", list)
	}
	if repository.listQuery.Filter.State != model.FixRecurrenceAwaitingEvidence ||
		repository.listQuery.Filter.ChangeKind != model.FixChangeCode ||
		repository.listQuery.Filter.Severity != "high" ||
		repository.listQuery.Filter.Harness != "Codex" ||
		repository.listQuery.Filter.RecordedAfter == nil ||
		!repository.listQuery.Filter.RecordedAfter.Equal(now) ||
		repository.listQuery.Filter.IssueID != repository.issueID ||
		!repository.listQuery.Filter.IncludeRetracted {
		t.Fatalf("list query = %+v", repository.listQuery)
	}

	detailRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues/"+repository.issueID+
			"/fix-monitoring?limit=1&view_cursor="+list.MonitoringViewCursor,
		true,
	)
	detailResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail readmodel.FixMonitoringDetail
	if err := json.NewDecoder(detailResponse.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.CurrentIssueAvailable || detail.CurrentIssue != nil ||
		len(detail.Data) != 1 ||
		detail.Data[0].ObservationViewCursor == "" ||
		repository.detailQuery.Snapshot != recurrenceHTTPSnapshot(now) {
		t.Fatalf("detail=%+v query=%+v", detail, repository.detailQuery)
	}

	observationRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/issues/"+repository.issueID+
			"/fixes/"+repository.annotationID+
			"/recurrences?limit=1&observation_view_cursor="+
			detail.Data[0].ObservationViewCursor,
		true,
	)
	observationResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(observationResponse, observationRequest)
	if observationResponse.Code != http.StatusOK {
		t.Fatalf("observation status = %d body=%s",
			observationResponse.Code, observationResponse.Body.String())
	}
	var observations readmodel.FixRecurrenceObservationList
	if err := json.NewDecoder(observationResponse.Body).Decode(&observations); err != nil {
		t.Fatal(err)
	}
	if len(observations.Data) != 1 ||
		observations.Data[0].RetainedEventIDs[0] != httpTestEventID ||
		repository.observationQuery.Snapshot != recurrenceHTTPSnapshot(now) {
		t.Fatalf("observations=%+v query=%+v",
			observations, repository.observationQuery)
	}

	mismatchedPaths := []string{
		"/v1/issues/" + httpTestIssueID("z") +
			"/fix-monitoring?view_cursor=" + detail.MonitoringViewCursor,
		"/v1/issues/" + repository.issueID +
			"/fixes/fxa_" + strings.Repeat("z", 52) +
			"/recurrences?observation_view_cursor=" +
			detail.Data[0].ObservationViewCursor,
	}
	for _, path := range mismatchedPaths {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(
			response,
			issueHTTPRequest(http.MethodGet, "http://127.0.0.1"+path, true),
		)
		if response.Code != http.StatusNotFound {
			t.Fatalf("route-bound cursor mismatch status=%d body=%s",
				response.Code, response.Body.String())
		}
	}

	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{
			name:   "catching up",
			err:    model.ErrFixMonitoringCatchingUp,
			status: http.StatusServiceUnavailable,
		},
		{
			name:   "catch-up failed",
			err:    model.ErrFixMonitoringCatchupFailed,
			status: http.StatusServiceUnavailable,
		},
		{
			name:   "expired",
			err:    model.ErrFixMonitoringSnapshotExpired,
			status: http.StatusGone,
		},
	} {
		t.Run("mismatched cursor precedence/"+test.name, func(t *testing.T) {
			repository.err = test.err
			defer func() { repository.err = nil }()
			for _, path := range mismatchedPaths {
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(
					response,
					issueHTTPRequest(
						http.MethodGet,
						"http://127.0.0.1"+path,
						true,
					),
				)
				if response.Code != test.status {
					t.Fatalf("%s status=%d body=%s",
						path, response.Code, response.Body.String())
				}
			}
		})
	}

	invalidLimits := []string{
		"limit=0",
		"limit=-1",
		"limit=not-a-number",
		"limit=101",
		"limit=1&limit=2",
	}
	for _, query := range invalidLimits {
		for _, path := range []string{
			"/v1/fix-monitoring?" + query,
			"/v1/issues/" + repository.issueID +
				"/fix-monitoring?view_cursor=" +
				detail.MonitoringViewCursor + "&" + query,
			"/v1/issues/" + repository.issueID +
				"/fixes/" + repository.annotationID +
				"/recurrences?observation_view_cursor=" +
				detail.Data[0].ObservationViewCursor + "&" + query,
		} {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(
				response,
				issueHTTPRequest(
					http.MethodGet,
					"http://127.0.0.1"+path,
					true,
				),
			)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s status=%d body=%s",
					path, response.Code, response.Body.String())
			}
		}
	}
}

func TestFixMonitoringRoutesRejectQueriesAndMapFixedErrors(t *testing.T) {
	now := time.Date(2026, 9, 8, 15, 0, 0, 0, time.UTC)
	repository := &recurrenceHTTPRepository{
		now:          now,
		issueID:      httpTestIssueID("d"),
		annotationID: "fxa_" + strings.Repeat("e", 52),
	}
	server, err := New(readmodel.New(
		repository,
		readmodel.WithFixMonitoringRepository(repository),
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
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		if forbidden != "" && strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("%s reflected %q: %s", path, forbidden, response.Body.String())
		}
		var value problem
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	for _, path := range []string{
		"/v1/fix-monitoring?unknown=PRIVATE_QUERY_CANARY",
		"/v1/fix-monitoring?limit=1&limit=2",
		"/v1/fix-monitoring?cursor=PRIVATE_CURSOR&state=awaiting_later_evidence",
		"/v1/fix-monitoring?include_retracted=yes",
		"/v1/issues/" + repository.issueID + "/fix-monitoring?cursor=a&view_cursor=b",
		"/v1/issues/" + repository.issueID + "/fix-monitoring?cursor=a&limit=1",
		"/v1/issues/" + repository.issueID + "/fixes/" + repository.annotationID + "/recurrences",
		"/v1/issues/" + repository.issueID + "/fixes/" + repository.annotationID + "/recurrences?cursor=a&observation_view_cursor=b",
	} {
		assertProblem(path, http.StatusBadRequest, "PRIVATE_")
	}

	tests := []struct {
		err         error
		status      int
		problemType string
	}{
		{
			err:         model.ErrFixMonitoringCatchingUp,
			status:      http.StatusServiceUnavailable,
			problemType: "belay.local/monitoring-catchup-in-progress",
		},
		{
			err:         model.ErrFixMonitoringCatchupFailed,
			status:      http.StatusServiceUnavailable,
			problemType: "belay.local/monitoring-catchup-failed",
		},
		{
			err:         model.ErrFixMonitoringSnapshotExpired,
			status:      http.StatusGone,
			problemType: "belay.local/cursor-expired",
		},
		{
			err:         model.ErrFixMonitoringNotFound,
			status:      http.StatusNotFound,
			problemType: "about:blank",
		},
		{
			err:         errors.New("PRIVATE_INTERNAL_CANARY"),
			status:      http.StatusInternalServerError,
			problemType: "about:blank",
		},
	}
	for _, test := range tests {
		repository.err = test.err
		value := assertProblem(
			"/v1/fix-monitoring",
			test.status,
			"PRIVATE_INTERNAL_CANARY",
		)
		if value.Type != test.problemType {
			t.Fatalf("error %v problem=%+v", test.err, value)
		}
	}
}

func TestFixMonitoringRoutesKeepExistingRoutesAvailableDuringCatchup(t *testing.T) {
	now := time.Date(2026, 9, 8, 16, 0, 0, 0, time.UTC)
	repository := &recurrenceHTTPRepository{
		now:          now,
		issueID:      httpTestIssueID("f"),
		annotationID: "fxa_" + strings.Repeat("g", 52),
		err:          model.ErrFixMonitoringCatchingUp,
	}
	server, err := New(readmodel.New(
		repository,
		readmodel.WithFixMonitoringRepository(repository),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}
	monitoring := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		monitoring,
		issueHTTPRequest(http.MethodGet, "http://127.0.0.1/v1/fix-monitoring", true),
	)
	if monitoring.Code != http.StatusServiceUnavailable {
		t.Fatalf("monitoring catch-up status=%d", monitoring.Code)
	}
	health := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		health,
		issueHTTPRequest(http.MethodGet, "http://127.0.0.1/healthz", false),
	)
	if health.Code != http.StatusOK {
		t.Fatalf("health status during catch-up=%d body=%s",
			health.Code, health.Body.String())
	}
}

func recurrenceHTTPSnapshot(now time.Time) model.FixMonitoringSnapshot {
	return model.FixMonitoringSnapshot{
		ProjectionGeneration: 3,
		EventGeneration:      4,
		RetentionGeneration:  1,
		AnnotationSequence:   5,
		RetractionSequence:   6,
		JobSequence:          7,
		JobEventSequence:     8,
		ObservationSequence:  9,
		IssuedAt:             now.UTC(),
	}
}
