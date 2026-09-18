package localhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/sourcecatalog"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

type attentionFamilyHTTPRepository struct {
	*issueHTTPRepository
	familyQuery model.AttentionFamilyQuery
	memberQuery model.AttentionFamilyMemberQuery
	family      model.AttentionFamilySummary
	member      model.AttentionFamilyMemberRecord
	memberErr   error
}

func (repository *attentionFamilyHTTPRepository) QueryAttentionFamilies(
	_ context.Context,
	query model.AttentionFamilyQuery,
) (model.AttentionFamilyPage, error) {
	repository.familyQuery = query
	epoch, snapshot, retention, issuedAt := attentionFamilyHTTPMetadata(query, repository.now)
	return model.AttentionFamilyPage{
		Data:                []model.AttentionFamilySummary{repository.family},
		Analysis:            model.IssueAnalysisCoverage{CurrentSessions: 2, Complete: true},
		CursorEpoch:         epoch,
		Snapshot:            snapshot,
		RetentionGeneration: retention,
		IssuedAt:            issuedAt,
		HasMore:             true,
	}, nil
}

func (repository *attentionFamilyHTTPRepository) QueryAttentionFamilyMembers(
	_ context.Context,
	query model.AttentionFamilyMemberQuery,
) (model.AttentionFamilyMemberPage, error) {
	repository.memberQuery = query
	if repository.memberErr != nil {
		return model.AttentionFamilyMemberPage{}, repository.memberErr
	}
	return model.AttentionFamilyMemberPage{
		Family:              repository.family,
		Data:                []model.AttentionFamilyMemberRecord{repository.member},
		Analysis:            model.IssueAnalysisCoverage{CurrentSessions: 2, Complete: true},
		CursorEpoch:         query.CursorEpoch,
		Snapshot:            query.Snapshot,
		RetentionGeneration: query.RetentionGeneration,
		IssuedAt:            query.IssuedAt,
		HasMore:             true,
		Found:               true,
	}, nil
}

func TestAttentionFamilyHTTPListDetailAndStrictShapes(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	familyID := httpTestAttentionFamilyID("a")
	issueID := httpTestIssueID("b")
	code := sourcecatalog.GuardrailsSourceSignalCode
	repository := &attentionFamilyHTTPRepository{
		issueHTTPRepository: &issueHTTPRepository{now: now},
		family: model.AttentionFamilySummary{
			FamilyID:              familyID,
			GroupKey:              "mapped:attention.agent_guardrails_configuration:1:1:1",
			Kind:                  model.AttentionFamilyKindMappedUpstream,
			MappingKey:            sourcecatalog.GuardrailsConfigurationMappingKey,
			MappingVersion:        "1",
			GroupingVersion:       "1",
			RepresentativeIssueID: issueID,
			AttentionKind:         model.AttentionKindIssue,
			Severity:              "low",
			Confidence:            "high",
			FirstObservedAt:       now.Add(-time.Hour),
			LastObservedAt:        now.Add(-time.Minute),
			SupportingIssueCount:  2,
			OccurrenceCount:       2,
			SessionCount:          2,
			Harnesses:             []string{"codex"},
			Scope:                 model.AttentionFamilyScopeCounts{Unscoped: 2},
			AnalysisStatus:        model.AnalysisCurrent,
			EvidenceComplete:      true,
		},
		member: model.AttentionFamilyMemberRecord{
			IssueSummary: model.IssueSummary{
				IssueID:             issueID,
				FingerprintID:       httpTestFingerprintID("c"),
				FingerprintVersion:  "1",
				Origin:              "numbat",
				DetectorID:          "numbat_finding",
				DetectorVersion:     sourcecatalog.OpaqueRuleVersion("1.1"),
				Category:            "numbat_finding",
				TitleCode:           "issue.numbat_finding",
				SourceSignalCode:    &code,
				Severity:            "high",
				Confidence:          "high",
				ScopeQuality:        model.ScopeUnscoped,
				FirstObservedAt:     now.Add(-time.Hour),
				LastObservedAt:      now.Add(-time.Minute),
				OccurrenceCount:     2,
				SessionCount:        2,
				Harnesses:           []string{"codex"},
				AnalysisStatus:      model.AnalysisCurrent,
				EvidenceComplete:    true,
				RetainedHistoryOnly: false,
			},
			SessionKey:          "session-latest",
			SessionSelection:    model.AttentionFamilyMemberSessionSelectionLatest,
			SessionStartedAt:    timePointerHTTP(now.Add(-2 * time.Hour)),
			SessionLastActiveAt: timePointerHTTP(now.Add(-30 * time.Second)),
			CitedEventCount:     2,
			EvidenceFirstAt:     timePointerHTTP(now.Add(-2 * time.Minute)),
			EvidenceLastAt:      timePointerHTTP(now.Add(-time.Minute)),
		},
	}
	server, err := New(readmodel.New(
		repository,
		readmodel.WithIssueRepository(repository),
		readmodel.WithIssueCursorCodec(issueHTTPCursorCodec{}),
		readmodel.WithClock(func() time.Time { return now }),
	), "launch-secret")
	if err != nil {
		t.Fatal(err)
	}

	listRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/attention-families?limit=1&severity=LOW&harness=Codex&origin=NUMBAT&analysis_status=CURRENT&observed_after=2026-09-08T03:00:00-07:00&attention_kind=ISSUE&experimental=STABLE",
		true,
	)
	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var list readmodel.AttentionFamilyList
	if err := json.NewDecoder(listResponse.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 ||
		list.Data[0].ViewCursor == "" ||
		list.NextCursor == nil ||
		list.ProjectionVersion != readmodel.AttentionFamilyProjectionVersion ||
		list.Data[0].Catalog.DisplayTitle != sourcecatalog.GuardrailsDisplayTitle ||
		list.Data[0].Catalog.ObservationStatement != sourcecatalog.GuardrailsObservationStatement ||
		list.Data[0].Catalog.Caveat != sourcecatalog.GuardrailsCaveat ||
		list.Data[0].Catalog.NextEvidenceAction != sourcecatalog.GuardrailsNextEvidenceAction ||
		repository.familyQuery.Filter.Severity != "low" ||
		repository.familyQuery.Filter.Harness != "Codex" ||
		repository.familyQuery.Filter.ObservedAfter == nil ||
		repository.familyQuery.Filter.ObservedAfter.Format(time.RFC3339Nano) != "2026-09-08T10:00:00Z" {
		t.Fatalf("list=%+v query=%+v", list, repository.familyQuery)
	}

	detailRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/attention-families/"+familyID+
			"?limit=1&view_cursor="+list.Data[0].ViewCursor,
		true,
	)
	detailResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
	var detail readmodel.AttentionFamilyDetail
	if err := json.NewDecoder(detailResponse.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Data.Family.FamilyID != familyID ||
		len(detail.Data.Members) != 1 ||
		detail.Data.Members[0].SessionID != "session-latest" ||
		detail.Data.Members[0].SessionSelection != model.AttentionFamilyMemberSessionSelectionLatest ||
		detail.Data.Members[0].CitedEventCount != 2 ||
		detail.Data.Members[0].EvidenceFirstAt == nil ||
		detail.Data.Members[0].Catalog.DisplayTitle != sourcecatalog.GuardrailsDisplayTitle ||
		detail.Data.Members[0].Catalog.ObservationStatement != sourcecatalog.GuardrailsObservationStatement ||
		detail.Data.Members[0].Catalog.Caveat != sourcecatalog.GuardrailsCaveat ||
		detail.Data.Members[0].ViewCursor == "" ||
		detail.NextCursor == nil ||
		repository.memberQuery.Snapshot != 31 {
		t.Fatalf("detail=%+v query=%+v", detail, repository.memberQuery)
	}

	for _, target := range []string{
		"/v1/attention-families/" + familyID,
		"/v1/attention-families/" + familyID + "?cursor=a&view_cursor=b",
		"/v1/attention-families/" + familyID + "?cursor=a&limit=1",
		"/v1/attention-families?cursor=a&severity=low",
		"/v1/attention-families?category=PRIVATE_CATEGORY_CANARY",
	} {
		request := issueHTTPRequest(http.MethodGet, "http://127.0.0.1"+target, true)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("target=%s status=%d body=%s", target, response.Code, response.Body.String())
		}
	}

	repository.memberErr = model.ErrIssueSnapshotExpired
	expiredRequest := issueHTTPRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/attention-families/"+familyID+
			"?view_cursor="+list.Data[0].ViewCursor,
		true,
	)
	expiredResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(expiredResponse, expiredRequest)
	if expiredResponse.Code != http.StatusGone {
		t.Fatalf("expired detail status=%d body=%s",
			expiredResponse.Code, expiredResponse.Body.String())
	}
}

func timePointerHTTP(value time.Time) *time.Time {
	return &value
}

type issueV2HTTPRepository struct {
	*issueHTTPRepository
}

func (repository *issueV2HTTPRepository) QueryIssueOccurrences(
	_ context.Context,
	query model.IssueOccurrenceQuery,
) (model.IssueOccurrencePage, error) {
	return model.IssueOccurrencePage{
		Data: []model.IssueOccurrence{{
			OccurrenceID:       "occurrence-1",
			IssueID:            query.IssueID,
			FingerprintID:      httpTestFingerprintID("d"),
			FingerprintVersion: "1",
			Origin:             "numbat",
			OriginRecordID:     "PRIVATE_ORIGIN_RECORD_CANARY",
			SessionID:          "session-1",
			Harness:            "codex",
			Provenance: model.DetectorProvenance{
				DetectorID:         "numbat_finding",
				DetectorVersion:    "rule-b05e244762b1",
				FingerprintVersion: "1",
				ProjectionVersion:  "1",
			},
			LastObservedAt:     time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
			AnalysisGeneration: 77,
			Evidence: model.IssueEvidence{
				CitedEventIDs: []string{httpTestEventID},
				Dimensions:    []string{"PRIVATE_DIMENSION_CANARY"},
			},
		}},
		CursorEpoch:         query.CursorEpoch,
		Snapshot:            query.Snapshot,
		RetentionGeneration: query.RetentionGeneration,
		IssuedAt:            query.IssuedAt,
	}, nil
}

func TestIssueHTTPDetailUsesPrivacyClosedV2Projection(t *testing.T) {
	repository := &issueV2HTTPRepository{
		issueHTTPRepository: &issueHTTPRepository{},
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
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{
		"origin_record_id",
		"analysis_generation",
		"dimensions",
		"PRIVATE_ORIGIN_RECORD_CANARY",
		"PRIVATE_DIMENSION_CANARY",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("HTTP v2 leaked %q: %s", forbidden, body)
		}
	}
	for _, required := range []string{
		`"projection_version":"belay.issue.v2"`,
		`"fingerprint_id":"` + httpTestFingerprintID("d") + `"`,
		`"detector_id":"numbat_finding"`,
		`"cited_event_ids":["` + httpTestEventID + `"]`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("HTTP v2 omitted %q: %s", required, body)
		}
	}
}

func attentionFamilyHTTPMetadata(
	query model.AttentionFamilyQuery,
	now time.Time,
) (string, int64, int64, time.Time) {
	if query.Snapshot != 0 {
		return query.CursorEpoch, query.Snapshot, query.RetentionGeneration, query.IssuedAt
	}
	return "http-epoch", 31, 4, now.UTC()
}

func httpTestAttentionFamilyID(fill string) string {
	return "atf_" + strings.Repeat(fill, 52)
}
