package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/sourcecatalog"
)

type briefCoreRepository struct {
	mu      sync.Mutex
	page    model.SessionPage
	err     error
	queries []model.SessionQuery
	session model.SessionSummary
	through time.Time
}

func (repository *briefCoreRepository) QuerySessions(
	_ context.Context,
	query model.SessionQuery,
) (model.SessionPage, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.queries = append(repository.queries, query)
	return repository.page, repository.err
}

func (repository *briefCoreRepository) GetSession(
	_ context.Context,
	_ string,
) (model.SessionSummary, time.Time, error) {
	return repository.session, repository.through, repository.err
}

func (*briefCoreRepository) QuerySessionTimeline(
	context.Context,
	model.TimelineQuery,
) (model.EventPage, error) {
	return model.EventPage{}, nil
}

func (*briefCoreRepository) QueryActivityPage(
	context.Context,
	model.ActivityQuery,
) (model.EventPage, error) {
	return model.EventPage{}, nil
}

func (*briefCoreRepository) QueryFindings(
	context.Context,
	model.FindingQuery,
) (model.FindingPage, error) {
	return model.FindingPage{}, nil
}

func (*briefCoreRepository) GetStats(
	context.Context,
) (model.LocalStats, time.Time, error) {
	return model.LocalStats{}, time.Time{}, nil
}

type briefIssueRepository struct {
	mu            sync.Mutex
	issuePage     model.IssuePage
	familyPage    model.AttentionFamilyPage
	issueErr      error
	familyErr     error
	issueQueries  []model.IssueQuery
	familyQueries []model.AttentionFamilyQuery
}

func (repository *briefIssueRepository) QueryIssues(
	_ context.Context,
	query model.IssueQuery,
) (model.IssuePage, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.issueQueries = append(repository.issueQueries, query)
	return repository.issuePage, repository.issueErr
}

func (*briefIssueRepository) QueryIssueOccurrences(
	context.Context,
	model.IssueOccurrenceQuery,
) (model.IssueOccurrencePage, error) {
	return model.IssueOccurrencePage{}, nil
}

func (*briefIssueRepository) LookupSessionEvents(
	context.Context,
	model.EventLookupQuery,
) (model.EventLookupResult, error) {
	return model.EventLookupResult{}, nil
}

func (*briefIssueRepository) VisitSessionEvents(
	context.Context,
	model.EventLookupQuery,
	func(model.Event) error,
) (model.EventLookupSummary, error) {
	return model.EventLookupSummary{}, nil
}

func (repository *briefIssueRepository) QueryAttentionFamilies(
	_ context.Context,
	query model.AttentionFamilyQuery,
) (model.AttentionFamilyPage, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.familyQueries = append(repository.familyQueries, query)
	return repository.familyPage, repository.familyErr
}

func (*briefIssueRepository) QueryAttentionFamilyMembers(
	context.Context,
	model.AttentionFamilyMemberQuery,
) (model.AttentionFamilyMemberPage, error) {
	return model.AttentionFamilyMemberPage{}, nil
}

func TestDeveloperBriefComposesBoundedRankedSources(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	coverage := model.IssueAnalysisCoverage{
		CurrentSessions: 3,
		AnalysisThrough: now.Add(-time.Minute),
		Complete:        true,
	}
	core := &briefCoreRepository{page: model.SessionPage{
		Data: []model.SessionSummary{
			briefSession("failed", "codex", "failed", now.Add(-time.Hour)),
			briefSession("interrupted", "claude-code", "interrupted", now.Add(-2*time.Hour)),
			briefSession("succeeded", "codex", "succeeded", now.Add(-3*time.Hour)),
			briefSession("future", "codex", "failed", now.Add(time.Minute)),
		},
		Snapshot:    3,
		DataThrough: now.Add(-30 * time.Second),
	}}
	code := sourcecatalog.GuardrailsSourceSignalCode
	issueRepository := &briefIssueRepository{
		familyPage: model.AttentionFamilyPage{
			Data: []model.AttentionFamilySummary{{
				FamilyID:              testAttentionFamilyID("a"),
				GroupKey:              "mapped:attention.agent_guardrails_configuration:1:1:1",
				Kind:                  model.AttentionFamilyKindMappedUpstream,
				MappingKey:            sourcecatalog.GuardrailsConfigurationMappingKey,
				MappingVersion:        "1",
				GroupingVersion:       "1",
				RepresentativeIssueID: testIssueID("a"),
				Representative: model.IssueSummary{
					IssueID:          testIssueID("a"),
					TitleCode:        "issue.numbat_finding",
					SourceSignalCode: &code,
				},
				AttentionKind:        model.AttentionKindIssue,
				Severity:             "high",
				Confidence:           "high",
				FirstObservedAt:      now.Add(-2 * time.Hour),
				LastObservedAt:       now.Add(-20 * time.Minute),
				SupportingIssueCount: 2,
				OccurrenceCount:      3,
				SessionCount:         2,
				Harnesses:            []string{"codex", "claude-code"},
				AnalysisStatus:       model.AnalysisCurrent,
				EvidenceComplete:     true,
			}},
			Analysis:            coverage,
			CursorEpoch:         "epoch",
			Snapshot:            7,
			RetentionGeneration: 2,
			IssuedAt:            now,
		},
		issuePage: model.IssuePage{
			Data: []model.IssueSummary{{
				IssueID:         testIssueID("b"),
				FingerprintID:   testFingerprintID("b"),
				Category:        model.AttentionKindEvidenceGap,
				TitleCode:       "issue.verification_not_observed",
				Severity:        "medium",
				Confidence:      "high",
				LastObservedAt:  now.Add(-10 * time.Minute),
				OccurrenceCount: 1,
				SessionCount:    1,
				Harnesses:       []string{"codex"},
				AnalysisStatus:  model.AnalysisCurrent,
			}},
			Analysis:            coverage,
			CursorEpoch:         "epoch",
			Snapshot:            7,
			RetentionGeneration: 2,
			IssuedAt:            now,
		},
	}
	service := New(
		core,
		WithIssueRepository(issueRepository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)

	brief, err := service.GetDeveloperBrief(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if brief.Status != BriefStatusReady ||
		!brief.Window.StartedAt.Equal(now.Add(-24*time.Hour)) ||
		!brief.Window.EndedAt.Equal(now) ||
		brief.RecentSummary.EvaluatedSessionCount != 3 ||
		len(brief.RecentWork) != 3 ||
		len(brief.ActionCards) != 4 {
		t.Fatalf("brief = %+v", brief)
	}
	gotKinds := []string{
		brief.ActionCards[0].Kind,
		brief.ActionCards[1].Kind,
		brief.ActionCards[2].Kind,
		brief.ActionCards[3].Kind,
	}
	wantKinds := []string{
		BriefActionReviewedFinding,
		BriefActionSessionOutcome,
		BriefActionSessionOutcome,
		BriefActionEvidenceGap,
	}
	if strings.Join(gotKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("action order = %v, want %v", gotKinds, wantKinds)
	}
	if brief.ActionCards[0].NextStep.Kind != BriefTargetAttentionFamily ||
		brief.ActionCards[3].NextStep.Kind != BriefTargetIssue ||
		brief.RecentWork[0].OutcomeExplanation !=
			"The agent reported that this session ended with a failure." {
		t.Fatalf("targets/copy = %+v %+v", brief.ActionCards, brief.RecentWork)
	}
	if len(core.queries) != 1 ||
		core.queries[0].Limit != 101 ||
		core.queries[0].OccurredAfter == nil ||
		!core.queries[0].OccurredAfter.Equal(now.Add(-24*time.Hour)) ||
		core.queries[0].OccurredBefore == nil ||
		!core.queries[0].OccurredBefore.Equal(now) {
		t.Fatalf("session query = %+v", core.queries)
	}
	if len(issueRepository.familyQueries) != 1 ||
		issueRepository.familyQueries[0].Limit != 20 ||
		issueRepository.familyQueries[0].Filter.AttentionKind != model.AttentionKindIssue ||
		len(issueRepository.issueQueries) != 1 ||
		issueRepository.issueQueries[0].Limit != 20 ||
		issueRepository.issueQueries[0].Filter.AttentionKind != model.AttentionKindEvidenceGap {
		t.Fatalf(
			"family/issue queries = %+v / %+v",
			issueRepository.familyQueries,
			issueRepository.issueQueries,
		)
	}
	encoded, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= 64*1024 {
		t.Fatalf("encoded brief = %d bytes", len(encoded))
	}
	for _, prohibited := range []string{
		"source_signal_code", "fingerprint_id", "detector_id",
		"PRIVATE_PROMPT_CANARY", "numbat",
	} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(prohibited)) {
			t.Fatalf("brief leaked %q: %s", prohibited, encoded)
		}
	}
}

func TestDeveloperBriefPartialAndUnavailableSources(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	core := &briefCoreRepository{
		page: model.SessionPage{
			Data:        []model.SessionSummary{briefSession("ok", "codex", "succeeded", now)},
			DataThrough: now,
		},
	}
	issues := &briefIssueRepository{
		familyErr: errors.New("PRIVATE_REPOSITORY_CANARY"),
		issueErr:  errors.New("PRIVATE_REPOSITORY_CANARY"),
	}
	service := New(
		core,
		WithIssueRepository(issues),
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)
	brief, err := service.GetDeveloperBrief(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if brief.Status != BriefStatusLimited ||
		brief.Coverage.Complete ||
		len(brief.Coverage.Limitations) != 2 {
		t.Fatalf("limited brief = %+v", brief)
	}
	encoded, _ := json.Marshal(brief)
	if strings.Contains(string(encoded), "PRIVATE_REPOSITORY_CANARY") {
		t.Fatalf("repository error leaked: %s", encoded)
	}

	core.err = errors.New("sessions unavailable")
	if _, err := service.GetDeveloperBrief(context.Background()); !errors.Is(
		err,
		ErrDeveloperBriefUnavailable,
	) {
		t.Fatalf("all-source error = %v", err)
	}
}

func TestRankBriefCandidatesCapsOutcomesAndUsesStableTies(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	values := []briefCandidate{
		testBriefCandidate("session-c", BriefActionSessionOutcome, 600, 2, now),
		testBriefCandidate("finding-b", BriefActionReviewedFinding, 600, 0, now),
		testBriefCandidate("session-a", BriefActionSessionOutcome, 600, 2, now),
		testBriefCandidate("session-b", BriefActionSessionOutcome, 600, 2, now),
		testBriefCandidate("finding-a", BriefActionReviewedFinding, 600, 0, now),
		testBriefCandidate("gap", BriefActionEvidenceGap, 300, 1, now),
	}
	first := rankBriefCandidates(append([]briefCandidate(nil), values...))
	second := rankBriefCandidates([]briefCandidate{
		values[5], values[3], values[1], values[4], values[0], values[2],
	})
	firstIDs, secondIDs := make([]string, len(first)), make([]string, len(second))
	outcomes := 0
	for index := range first {
		firstIDs[index] = first[index].CardID
		secondIDs[index] = second[index].CardID
		if first[index].Kind == BriefActionSessionOutcome {
			outcomes++
		}
	}
	if strings.Join(firstIDs, ",") != strings.Join(secondIDs, ",") ||
		outcomes != 2 || len(first) != 5 {
		t.Fatalf("ranked first=%v second=%v outcomes=%d", firstIDs, secondIDs, outcomes)
	}
}

func TestDeveloperBriefConsolidatesSemanticallyIdenticalExactFamilies(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	catalog := AttentionFamilyCatalogMetadata{
		CatalogVersion:       IssueCatalogVersion,
		GroupingVersion:      "1",
		DisplayTitle:         "No recognized verification retained after changes",
		ObservationStatement: "Belay's retained evidence contains no recognized verification command after the final recorded file change and before the session ended.",
		Caveat:               "This does not show that verification did not occur; Belay only checks supported commands in retained activity.",
		NextEvidenceAction:   "inspect_cited_events",
	}
	families := []AttentionFamilySummary{
		briefExactFamily(
			"a",
			now.Add(-30*time.Minute),
			1,
			2,
			[]string{"codex"},
			"cursor-a",
			catalog,
		),
		briefExactFamily(
			"b",
			now.Add(-5*time.Minute),
			2,
			3,
			[]string{"claude-code", "codex"},
			"cursor-b",
			catalog,
		),
		briefExactFamily(
			"c",
			now.Add(-10*time.Minute),
			1,
			4,
			[]string{"claude-code"},
			"cursor-c",
			catalog,
		),
	}

	first := rankBriefCandidates(briefFamilyCandidates(
		families,
		now.Add(-time.Hour),
		now,
	))
	second := rankBriefCandidates(briefFamilyCandidates(
		[]AttentionFamilySummary{families[2], families[0], families[1]},
		now.Add(-time.Hour),
		now,
	))
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("consolidated cards = %+v / %+v", first, second)
	}
	card := first[0]
	if !strings.HasPrefix(card.CardID, "signal:") ||
		card.CardID != second[0].CardID ||
		card.Evidence.SessionCount == nil ||
		*card.Evidence.SessionCount != 4 ||
		card.Evidence.OccurrenceCount == nil ||
		*card.Evidence.OccurrenceCount != 9 ||
		strings.Join(card.Evidence.Harnesses, ",") != "claude-code,codex" ||
		!card.Evidence.LastObservedAt.Equal(now.Add(-5*time.Minute)) ||
		card.NextStep.IssueID == nil ||
		*card.NextStep.IssueID != testIssueID("b") ||
		card.NextStep.ViewCursor == nil ||
		*card.NextStep.ViewCursor != "cursor-b" {
		t.Fatalf("consolidated card = %+v", card)
	}
	if second[0].NextStep.IssueID == nil ||
		*second[0].NextStep.IssueID != testIssueID("b") {
		t.Fatalf("reordered representative = %+v", second[0].NextStep)
	}
}

func TestDeveloperBriefDoesNotMergeDistinctSignalCopy(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	firstCatalog := AttentionFamilyCatalogMetadata{
		CatalogVersion:       IssueCatalogVersion,
		GroupingVersion:      "1",
		DisplayTitle:         "Signal title",
		ObservationStatement: "First reviewed observation.",
		Caveat:               "First reviewed limitation.",
		NextEvidenceAction:   "inspect_cited_events",
	}
	secondCatalog := firstCatalog
	secondCatalog.ObservationStatement = "Different reviewed observation."
	cards := rankBriefCandidates(briefFamilyCandidates(
		[]AttentionFamilySummary{
			briefExactFamily("a", now, 1, 1, []string{"codex"}, "cursor-a", firstCatalog),
			briefExactFamily("b", now, 1, 1, []string{"codex"}, "cursor-b", secondCatalog),
		},
		now.Add(-time.Hour),
		now,
	))
	if len(cards) != 2 || cards[0].CardID == cards[1].CardID {
		t.Fatalf("distinct signals merged: %+v", cards)
	}
}

func TestDeveloperBriefDoesNotMergeDifferentNavigationKinds(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	catalog := AttentionFamilyCatalogMetadata{
		CatalogVersion:       IssueCatalogVersion,
		GroupingVersion:      "1",
		DisplayTitle:         "Same reviewed signal",
		ObservationStatement: "Same reviewed observation.",
		Caveat:               "Same reviewed limitation.",
		NextEvidenceAction:   "inspect_cited_events",
	}
	exact := briefExactFamily(
		"a",
		now,
		1,
		1,
		[]string{"codex"},
		"cursor-a",
		catalog,
	)
	mapped := briefExactFamily(
		"b",
		now,
		1,
		1,
		[]string{"codex"},
		"cursor-b",
		catalog,
	)
	mapped.Kind = model.AttentionFamilyKindMappedUpstream

	cards := rankBriefCandidates(briefFamilyCandidates(
		[]AttentionFamilySummary{exact, mapped},
		now.Add(-time.Hour),
		now,
	))
	if len(cards) != 2 {
		t.Fatalf("different navigation kinds merged: %+v", cards)
	}
	gotKinds := []string{cards[0].NextStep.Kind, cards[1].NextStep.Kind}
	sort.Strings(gotKinds)
	wantKinds := []string{BriefTargetAttentionFamily, BriefTargetIssue}
	sort.Strings(wantKinds)
	if strings.Join(gotKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("navigation kinds = %v, want %v", gotKinds, wantKinds)
	}
}

func TestDeveloperBriefCoverageIsConservativeAndReportsBounds(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	coverage := buildDeveloperBriefCoverage(
		briefSourceResult[SessionList]{value: SessionList{
			Data:        make([]model.SessionSummary, 100),
			HasMore:     true,
			DataThrough: now,
		}},
		briefSourceResult[AttentionFamilyList]{value: AttentionFamilyList{
			Data:    make([]AttentionFamilySummary, 20),
			HasMore: true,
			GlobalAnalysisCoverage: model.IssueAnalysisCoverage{
				CurrentSessions: 10,
				AnalysisThrough: now,
				Complete:        true,
			},
		}},
		briefSourceResult[IssueList]{value: IssueList{
			Data:    make([]model.IssueSummary, 20),
			HasMore: true,
			Analysis: model.IssueAnalysisCoverage{
				CurrentSessions: 8,
				PendingSessions: 2,
				AnalysisThrough: now.Add(-time.Minute),
				Complete:        false,
			},
		}},
	)
	if coverage.Complete || coverage.IssueAnalysis == nil ||
		coverage.IssueAnalysis.Complete ||
		coverage.IssueAnalysis.CurrentSessions != 8 ||
		coverage.IssueAnalysis.PendingSessions != 2 ||
		!coverage.IssueAnalysis.AnalysisThrough.Equal(now.Add(-time.Minute)) ||
		len(coverage.Limitations) != 4 {
		t.Fatalf("coverage = %+v", coverage)
	}
}

func TestDeveloperBriefSuppressesUnknownStaleAndActionlessCatalogs(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	base := AttentionFamilySummary{
		FamilyID:              testAttentionFamilyID("a"),
		Kind:                  model.AttentionFamilyKindExactIssue,
		RepresentativeIssueID: testIssueID("a"),
		Severity:              "high",
		LastObservedAt:        now,
		SessionCount:          1,
		OccurrenceCount:       1,
		AnalysisStatus:        model.AnalysisCurrent,
		ViewCursor:            "cursor",
		Catalog: AttentionFamilyCatalogMetadata{
			DisplayTitle:         "Known title",
			ObservationStatement: "Known observation.",
			Caveat:               "Known limitation.",
			NextEvidenceAction:   "inspect_cited_events",
		},
	}
	unknown := base
	unknown.CatalogStatus = "unknown"
	stale := base
	stale.FamilyID = testAttentionFamilyID("b")
	stale.CatalogStatus = "known"
	stale.AnalysisStatus = model.AnalysisPending
	actionless := base
	actionless.FamilyID = testAttentionFamilyID("c")
	actionless.CatalogStatus = "known"
	actionless.Catalog.NextEvidenceAction = "unsupported_action"
	if got := briefFamilyCandidates(
		[]AttentionFamilySummary{unknown, stale, actionless},
		now.Add(-time.Hour),
		now,
	); len(got) != 0 {
		t.Fatalf("suppressed families produced cards: %+v", got)
	}
}

func briefSession(id, harness, outcome string, endedAt time.Time) model.SessionSummary {
	return model.SessionSummary{
		SessionID:  "session-" + id,
		Harness:    harness,
		StartedAt:  endedAt.Add(-10 * time.Minute),
		EndedAt:    endedAt,
		EventCount: 3,
		Outcome:    outcome,
		History:    "live",
	}
}

func briefExactFamily(
	id string,
	lastObservedAt time.Time,
	sessionCount int,
	occurrenceCount int,
	harnesses []string,
	viewCursor string,
	catalog AttentionFamilyCatalogMetadata,
) AttentionFamilySummary {
	return AttentionFamilySummary{
		FamilyID:              testAttentionFamilyID(id),
		Kind:                  model.AttentionFamilyKindExactIssue,
		RepresentativeIssueID: testIssueID(id),
		Severity:              "medium",
		LastObservedAt:        lastObservedAt,
		SessionCount:          sessionCount,
		OccurrenceCount:       occurrenceCount,
		Harnesses:             harnesses,
		AnalysisStatus:        model.AnalysisCurrent,
		ViewCursor:            viewCursor,
		Catalog:               catalog,
		CatalogStatus:         "known",
	}
}

func testBriefCandidate(
	id, kind string,
	rank, kindOrder int,
	at time.Time,
) briefCandidate {
	return briefCandidate{
		card: DeveloperBriefActionCard{
			CardID: id,
			Kind:   kind,
		},
		rank:            rank,
		sessionCount:    1,
		occurrenceCount: 1,
		lastObserved:    at,
		kindOrder:       kindOrder,
		stableID:        id,
	}
}
