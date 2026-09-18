package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/sourcecatalog"
)

type attentionFamilyTestRepository struct {
	*issueTestRepository
	familyQueries []model.AttentionFamilyQuery
	memberQueries []model.AttentionFamilyMemberQuery
	familyPage    model.AttentionFamilyPage
	memberPage    model.AttentionFamilyMemberPage
	familyErr     error
	memberErr     error
}

func (repository *attentionFamilyTestRepository) QueryAttentionFamilies(
	_ context.Context,
	query model.AttentionFamilyQuery,
) (model.AttentionFamilyPage, error) {
	repository.familyQueries = append(repository.familyQueries, query)
	if repository.familyErr != nil {
		return model.AttentionFamilyPage{}, repository.familyErr
	}
	page := repository.familyPage
	if query.Snapshot != 0 {
		page.CursorEpoch = query.CursorEpoch
		page.Snapshot = query.Snapshot
		page.RetentionGeneration = query.RetentionGeneration
		page.IssuedAt = query.IssuedAt
	}
	return page, nil
}

func (repository *attentionFamilyTestRepository) QueryAttentionFamilyMembers(
	_ context.Context,
	query model.AttentionFamilyMemberQuery,
) (model.AttentionFamilyMemberPage, error) {
	repository.memberQueries = append(repository.memberQueries, query)
	if repository.memberErr != nil {
		return model.AttentionFamilyMemberPage{}, repository.memberErr
	}
	page := repository.memberPage
	page.CursorEpoch = query.CursorEpoch
	page.Snapshot = query.Snapshot
	page.RetentionGeneration = query.RetentionGeneration
	page.IssuedAt = query.IssuedAt
	return page, nil
}

func TestAttentionFamilyListAndDetailPreserveSnapshotAndExactChildCursor(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	familyID := testAttentionFamilyID("a")
	issueID := testIssueID("b")
	groupKey := "mapped:attention.agent_guardrails_configuration:1:1:1"
	code := sourcecatalog.GuardrailsSourceSignalCode
	family := model.AttentionFamilySummary{
		FamilyID:              familyID,
		GroupKey:              groupKey,
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
		OccurrenceCount:       3,
		SessionCount:          2,
		Harnesses:             []string{"claude-code", "codex"},
		Scope:                 model.AttentionFamilyScopeCounts{Unscoped: 2},
		AnalysisStatus:        model.AnalysisCurrent,
		EvidenceComplete:      true,
	}
	member := model.IssueSummary{
		IssueID:             issueID,
		FingerprintID:       testFingerprintID("c"),
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
		SessionCount:        1,
		Harnesses:           []string{"codex"},
		AnalysisStatus:      model.AnalysisCurrent,
		EvidenceComplete:    true,
		RetainedHistoryOnly: false,
	}
	repository := &attentionFamilyTestRepository{
		issueTestRepository: &issueTestRepository{},
		familyPage: model.AttentionFamilyPage{
			Data:                []model.AttentionFamilySummary{family},
			Analysis:            model.IssueAnalysisCoverage{CurrentSessions: 3, Complete: true},
			CursorEpoch:         "family-epoch",
			Snapshot:            42,
			RetentionGeneration: 7,
			IssuedAt:            now,
			HasMore:             true,
		},
		memberPage: model.AttentionFamilyMemberPage{
			Family: family,
			Data: []model.AttentionFamilyMemberRecord{{
				IssueSummary:        member,
				SessionKey:          "session-latest",
				SessionSelection:    model.AttentionFamilyMemberSessionSelectionLatest,
				SessionStartedAt:    attentionFamilyTimePointer(now.Add(-90 * time.Minute)),
				SessionLastActiveAt: attentionFamilyTimePointer(now.Add(-30 * time.Second)),
				CitedEventCount:     2,
				EvidenceFirstAt:     attentionFamilyTimePointer(now.Add(-2 * time.Minute)),
				EvidenceLastAt:      attentionFamilyTimePointer(now.Add(-time.Minute)),
			}},
			Analysis: model.IssueAnalysisCoverage{CurrentSessions: 3, Complete: true},
			HasMore:  true,
			Found:    true,
		},
	}
	service := New(
		issueTestCoreRepository{},
		WithIssueRepository(repository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)
	after := now.Add(-2 * time.Hour).In(time.FixedZone("offset", -7*60*60))
	list, err := service.ListAttentionFamilies(
		context.Background(),
		AttentionFamilyListRequest{
			Limit:          1,
			Severity:       " LOW ",
			Harness:        " Codex ",
			Origin:         " NUMBAT ",
			AnalysisStatus: " CURRENT ",
			ObservedAfter:  &after,
			AttentionKind:  " ISSUE ",
			Experimental:   " STABLE ",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if list.ProjectionVersion != AttentionFamilyProjectionVersion ||
		len(list.Data) != 1 ||
		list.NextCursor == nil ||
		list.Data[0].ViewCursor == "" ||
		list.Data[0].Catalog.DisplayTitle != "Fewer approval prompts enabled" ||
		list.Data[0].Catalog.ObservationStatement != "Belay recorded a setting that lets actions already permitted by the agent run without asking for approval each time." ||
		list.Data[0].Catalog.Caveat != "This setting may be intentional. The record does not show whether an action bypassed a prompt or caused harm." ||
		list.Data[0].Catalog.CatalogVersion != sourcecatalog.CatalogVersion ||
		list.GlobalAnalysisCoverage.CurrentSessions != 3 {
		t.Fatalf("list = %+v", list)
	}
	encodedList, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encodedList), `"global_analysis_coverage"`) ||
		strings.Contains(string(encodedList), `"analysis":`) {
		t.Fatalf("family list coverage contract = %s", encodedList)
	}
	firstQuery := repository.familyQueries[0]
	if firstQuery.Filter.Severity != "low" ||
		firstQuery.Filter.Harness != "Codex" ||
		firstQuery.Filter.Origin != "numbat" ||
		firstQuery.Filter.AnalysisStatus != model.AnalysisCurrent ||
		firstQuery.Filter.ObservedAfter == nil ||
		!firstQuery.Filter.ObservedAfter.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("normalized query = %+v", firstQuery)
	}

	continued, err := service.ListAttentionFamilies(
		context.Background(),
		AttentionFamilyListRequest{Cursor: *list.NextCursor},
	)
	if err != nil {
		t.Fatal(err)
	}
	if continued.ReturnedCount != 1 ||
		len(repository.familyQueries) != 2 ||
		repository.familyQueries[1].Snapshot != 42 ||
		repository.familyQueries[1].Cursor == nil ||
		repository.familyQueries[1].Cursor.GroupKey != groupKey {
		t.Fatalf("continuation=%+v query=%+v", continued, repository.familyQueries[1])
	}

	detail, err := service.GetAttentionFamily(
		context.Background(),
		AttentionFamilyDetailRequest{
			FamilyID:   familyID,
			Limit:      1,
			ViewCursor: list.Data[0].ViewCursor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if detail.ProjectionVersion != AttentionFamilyProjectionVersion ||
		len(detail.Data.Members) != 1 ||
		detail.NextCursor == nil ||
		detail.Data.Members[0].Issue.IssueID != issueID ||
		detail.Data.Members[0].SessionID != "session-latest" ||
		detail.Data.Members[0].SessionSelection != model.AttentionFamilyMemberSessionSelectionLatest ||
		detail.Data.Members[0].SessionStartedAt == nil ||
		!detail.Data.Members[0].SessionStartedAt.Equal(now.Add(-90*time.Minute)) ||
		detail.Data.Members[0].CitedEventCount != 2 ||
		detail.Data.Members[0].EvidenceFirstAt == nil ||
		!detail.Data.Members[0].EvidenceFirstAt.Equal(now.Add(-2*time.Minute)) ||
		detail.Data.Members[0].ViewCursor == "" ||
		detail.GlobalAnalysisCoverage.CurrentSessions != 3 {
		t.Fatalf("detail = %+v", detail)
	}
	claims, err := service.DecodeIssueViewCursor(detail.Data.Members[0].ViewCursor)
	if err != nil {
		t.Fatal(err)
	}
	if claims.CursorEpoch != "family-epoch" ||
		claims.Snapshot != 42 ||
		claims.RetentionGeneration != 7 ||
		!claims.IssuedAt.Equal(now) {
		t.Fatalf("exact child claims = %+v", claims)
	}
	if len(repository.memberQueries) != 1 ||
		repository.memberQueries[0].GroupKey != groupKey ||
		repository.memberQueries[0].Snapshot != 42 {
		t.Fatalf("member query = %+v", repository.memberQueries)
	}
}

func attentionFamilyTimePointer(value time.Time) *time.Time {
	return &value
}

func TestAttentionFamilyDetailValidatesCursorBeforeExistence(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	familyID := testAttentionFamilyID("d")
	repository := &attentionFamilyTestRepository{
		issueTestRepository: &issueTestRepository{},
		memberErr:           model.ErrIssueSnapshotExpired,
	}
	service := New(
		issueTestCoreRepository{},
		WithIssueRepository(repository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)
	filters := attentionFamilyCursorFilters{
		AttentionKind: model.AttentionKindIssue,
		Experimental:  model.ExperimentalStable,
	}
	viewCursor, err := service.sealAttentionFamilyCursor(attentionFamilyCursorEnvelope{
		Version:             attentionFamilyCursorVersion,
		Kind:                "attention_family_view",
		CatalogVersion:      sourcecatalog.CatalogVersion,
		CursorEpoch:         "expired-epoch",
		Snapshot:            10,
		RetentionGeneration: 2,
		IssuedAt:            now.Format(time.RFC3339Nano),
		Filters:             &filters,
		FamilyID:            familyID,
		GroupKey:            "mapped:attention.agent_guardrails_configuration:1:1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.GetAttentionFamily(context.Background(), AttentionFamilyDetailRequest{
		FamilyID:   familyID,
		ViewCursor: viewCursor,
	})
	if !errors.Is(err, ErrCursorExpired) || len(repository.memberQueries) != 1 {
		t.Fatalf("expired detail error=%v queries=%d", err, len(repository.memberQueries))
	}

	staleCatalog, err := service.sealAttentionFamilyCursor(attentionFamilyCursorEnvelope{
		Version:             attentionFamilyCursorVersion,
		Kind:                "attention_family_view",
		CatalogVersion:      "belay.attention-families.old",
		CursorEpoch:         "family-epoch",
		Snapshot:            10,
		RetentionGeneration: 2,
		IssuedAt:            now.Format(time.RFC3339Nano),
		Filters:             &filters,
		FamilyID:            familyID,
		GroupKey:            "mapped:attention.agent_guardrails_configuration:1:1:1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.GetAttentionFamily(context.Background(), AttentionFamilyDetailRequest{
		FamilyID:   familyID,
		ViewCursor: staleCatalog,
	})
	if !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("catalog mismatch error = %v", err)
	}
}

func TestAttentionFamilyDetailRejectsUnqualifiedSessionContext(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	familyID := testAttentionFamilyID("e")
	issueID := testIssueID("f")
	family := model.AttentionFamilySummary{
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
		LastObservedAt:        now,
		SupportingIssueCount:  1,
		OccurrenceCount:       1,
		SessionCount:          1,
		Harnesses:             []string{"codex"},
		Scope:                 model.AttentionFamilyScopeCounts{Unscoped: 1},
		AnalysisStatus:        model.AnalysisCurrent,
		EvidenceComplete:      true,
	}
	code := sourcecatalog.GuardrailsSourceSignalCode
	member := model.IssueSummary{
		IssueID:            issueID,
		FingerprintID:      testFingerprintID("g"),
		FingerprintVersion: "1",
		Origin:             "numbat",
		DetectorID:         "numbat_finding",
		DetectorVersion:    sourcecatalog.OpaqueRuleVersion("1.1"),
		Category:           "numbat_finding",
		TitleCode:          "issue.numbat_finding",
		SourceSignalCode:   &code,
		Severity:           "low",
		Confidence:         "high",
		ScopeQuality:       model.ScopeUnscoped,
		FirstObservedAt:    now.Add(-time.Hour),
		LastObservedAt:     now,
		OccurrenceCount:    1,
		SessionCount:       1,
		Harnesses:          []string{"codex"},
		AnalysisStatus:     model.AnalysisCurrent,
		EvidenceComplete:   true,
	}
	repository := &attentionFamilyTestRepository{
		issueTestRepository: &issueTestRepository{},
		memberPage: model.AttentionFamilyMemberPage{
			Family: family,
			Data: []model.AttentionFamilyMemberRecord{{
				IssueSummary: member,
				SessionKey:   "session-1",
			}},
			Analysis:            model.IssueAnalysisCoverage{CurrentSessions: 1, Complete: true},
			CursorEpoch:         "family-epoch",
			Snapshot:            42,
			RetentionGeneration: 7,
			IssuedAt:            now,
			Found:               true,
		},
	}
	service := New(
		issueTestCoreRepository{},
		WithIssueRepository(repository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)
	viewCursor, err := service.sealAttentionFamilyCursor(attentionFamilyCursorEnvelope{
		Version:             attentionFamilyCursorVersion,
		Kind:                "attention_family_view",
		CatalogVersion:      sourcecatalog.CatalogVersion,
		CursorEpoch:         "family-epoch",
		Snapshot:            42,
		RetentionGeneration: 7,
		IssuedAt:            now.Format(time.RFC3339Nano),
		Filters: &attentionFamilyCursorFilters{
			AttentionKind: model.AttentionKindIssue,
			Experimental:  model.ExperimentalStable,
		},
		FamilyID: familyID,
		GroupKey: family.GroupKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetAttentionFamily(context.Background(), AttentionFamilyDetailRequest{
		FamilyID:   familyID,
		ViewCursor: viewCursor,
	}); err == nil || !strings.Contains(err.Error(), "invalid member context") {
		t.Fatalf("unqualified context error = %v", err)
	}
}

func TestAttentionFamilyRequestShapesFailClosed(t *testing.T) {
	service := New(
		issueTestCoreRepository{},
		WithAttentionFamilyRepository(&attentionFamilyTestRepository{
			issueTestRepository: &issueTestRepository{},
		}),
		WithIssueCursorCodec(issueTestCursorCodec{}),
	)
	for _, request := range []AttentionFamilyDetailRequest{
		{FamilyID: testAttentionFamilyID("e")},
		{
			FamilyID:   testAttentionFamilyID("e"),
			Cursor:     "cursor",
			ViewCursor: "view",
		},
		{FamilyID: "PRIVATE_FAMILY_CANARY", ViewCursor: "cursor"},
	} {
		if _, err := service.GetAttentionFamily(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("request=%+v error=%v", request, err)
		}
	}
}

func testAttentionFamilyID(fill string) string {
	return "atf_" + strings.Repeat(fill, 52)
}
