package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/sourcecatalog"
)

const testEventID = "01890f2e-6d4b-7c8a-9b0c-123456789abc"

type issueTestCoreRepository struct{}

func (issueTestCoreRepository) QuerySessions(
	context.Context,
	model.SessionQuery,
) (model.SessionPage, error) {
	return model.SessionPage{}, nil
}

func (issueTestCoreRepository) GetSession(
	context.Context,
	string,
) (model.SessionSummary, time.Time, error) {
	return model.SessionSummary{}, time.Time{}, nil
}

func (issueTestCoreRepository) QuerySessionTimeline(
	context.Context,
	model.TimelineQuery,
) (model.EventPage, error) {
	return model.EventPage{}, nil
}

func (issueTestCoreRepository) QueryActivityPage(
	context.Context,
	model.ActivityQuery,
) (model.EventPage, error) {
	return model.EventPage{}, nil
}

func (issueTestCoreRepository) QueryFindings(
	context.Context,
	model.FindingQuery,
) (model.FindingPage, error) {
	return model.FindingPage{}, nil
}

func (issueTestCoreRepository) GetStats(
	context.Context,
) (model.LocalStats, time.Time, error) {
	return model.LocalStats{}, time.Time{}, nil
}

type issueTestRepository struct {
	queryIssues      func(model.IssueQuery) (model.IssuePage, error)
	queryOccurrences func(model.IssueOccurrenceQuery) (model.IssueOccurrencePage, error)
	lookupEvents     func(model.EventLookupQuery) (model.EventLookupResult, error)
	visitEvents      func(model.EventLookupQuery, func(model.Event) error) (model.EventLookupSummary, error)
}

type issueCapableCoreRepository struct {
	issueTestCoreRepository
	*issueTestRepository
}

func (repository *issueTestRepository) QueryIssues(
	_ context.Context,
	query model.IssueQuery,
) (model.IssuePage, error) {
	if repository.queryIssues == nil {
		return model.IssuePage{}, nil
	}
	return repository.queryIssues(query)
}

func (repository *issueTestRepository) QueryIssueOccurrences(
	_ context.Context,
	query model.IssueOccurrenceQuery,
) (model.IssueOccurrencePage, error) {
	if repository.queryOccurrences == nil {
		return model.IssueOccurrencePage{}, nil
	}
	return repository.queryOccurrences(query)
}

func (repository *issueTestRepository) LookupSessionEvents(
	_ context.Context,
	query model.EventLookupQuery,
) (model.EventLookupResult, error) {
	if repository.lookupEvents == nil {
		return model.EventLookupResult{}, nil
	}
	return repository.lookupEvents(query)
}

func (repository *issueTestRepository) VisitSessionEvents(
	_ context.Context,
	query model.EventLookupQuery,
	visit func(model.Event) error,
) (model.EventLookupSummary, error) {
	if repository.visitEvents != nil {
		return repository.visitEvents(query, visit)
	}
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

func TestIssueListNormalizesAndPreservesCursorSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	issuedAt := now
	issueID := testIssueID("a")
	fingerprintID := testFingerprintID("b")
	observedAfter := now.Add(-time.Hour).In(time.FixedZone("offset", 2*60*60))
	var queries []model.IssueQuery
	repository := &issueTestRepository{}
	repository.queryIssues = func(query model.IssueQuery) (model.IssuePage, error) {
		queries = append(queries, query)
		if len(queries) == 1 {
			return model.IssuePage{
				Data: []model.IssueSummary{{
					IssueID:          issueID,
					Severity:         "high",
					SessionCount:     2,
					LastObservedAt:   now.Add(-time.Minute),
					FingerprintID:    fingerprintID,
					AnalysisStatus:   model.AnalysisCurrent,
					EvidenceComplete: true,
				}},
				Analysis:            model.IssueAnalysisCoverage{CurrentSessions: 3, Complete: true},
				CursorEpoch:         "epoch-1",
				Snapshot:            17,
				RetentionGeneration: 3,
				IssuedAt:            issuedAt,
				HasMore:             true,
			}, nil
		}
		return model.IssuePage{
			Data: []model.IssueSummary{{
				IssueID:        testIssueID("c"),
				Severity:       "medium",
				SessionCount:   1,
				LastObservedAt: now.Add(-2 * time.Minute),
			}},
			CursorEpoch:         query.CursorEpoch,
			Snapshot:            query.Snapshot,
			RetentionGeneration: query.RetentionGeneration,
			IssuedAt:            query.IssuedAt,
		}, nil
	}
	service := New(
		issueTestCoreRepository{},
		WithIssueRepository(repository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)
	request := IssueListRequest{
		Limit:          1,
		Severity:       " HIGH ",
		Category:       " COMMAND_FAILURE ",
		Harness:        " Codex ",
		Origin:         " BELAY ",
		AnalysisStatus: " CURRENT ",
		ObservedAfter:  &observedAfter,
		Recurrence:     " REPEATED ",
		SessionID:      " session-1 ",
		FingerprintID:  " " + strings.ToUpper(fingerprintID) + " ",
		AttentionKind:  " ISSUE ",
		Experimental:   " INCLUDE ",
	}
	first, err := service.ListIssues(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProjectionVersion != IssueProjectionVersion ||
		first.NextCursor == nil ||
		first.ViewCursor == "" ||
		!first.HasMore ||
		first.ReturnedCount != 1 ||
		first.Limit != 1 {
		t.Fatalf("first issue page metadata = %+v", first)
	}
	if first.Data[0].Harnesses == nil {
		t.Fatal("issue summary harnesses must be a non-nil slice")
	}
	query := queries[0]
	if query.Limit != 1 ||
		query.Snapshot != 0 ||
		query.Filter.Severity != "high" ||
		query.Filter.Category != "command_failure" ||
		query.Filter.Harness != "Codex" ||
		query.Filter.Origin != "belay" ||
		query.Filter.AnalysisStatus != model.AnalysisCurrent ||
		query.Filter.ObservedAfter == nil ||
		!query.Filter.ObservedAfter.Equal(observedAfter.UTC()) ||
		query.Filter.Recurrence != "repeated" ||
		query.Filter.SessionID != "session-1" ||
		query.Filter.FingerprintID != fingerprintID ||
		query.Filter.AttentionKind != model.AttentionKindIssue ||
		query.Filter.Experimental != model.ExperimentalInclude {
		t.Fatalf("normalized issue query = %+v", query)
	}

	now = now.Add(5 * time.Minute)
	second, err := service.ListIssues(context.Background(), IssueListRequest{
		Cursor: *first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || second.NextCursor != nil {
		t.Fatalf("second issue page metadata = %+v", second)
	}
	query = queries[1]
	if query.Snapshot != 17 ||
		!query.IssuedAt.Equal(time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)) ||
		query.Cursor == nil ||
		query.Cursor.IssueID != issueID ||
		query.Cursor.SeverityRank != 4 ||
		!query.Cursor.Repeated {
		t.Fatalf("continued issue query = %+v", query)
	}
	view, err := service.openIssueCursor(second.ViewCursor, "issue_view")
	if err != nil {
		t.Fatal(err)
	}
	if view.Snapshot != 17 ||
		view.IssuedAt != "2026-09-08T12:00:00Z" {
		t.Fatalf("continued view cursor = %+v", view)
	}

	if _, err := service.ListIssues(context.Background(), IssueListRequest{
		Cursor:   *first.NextCursor,
		Category: "different",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cursor-plus-filter error = %v, want ErrInvalidRequest", err)
	}
	if len(queries) != 2 {
		t.Fatalf("repository called after cursor/filter mismatch: %d calls", len(queries))
	}
}

func TestIssueDetailUsesViewSnapshotAndIssueBoundOccurrenceCursor(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	issuedAt := now
	issueID := testIssueID("d")
	otherIssueID := testIssueID("e")
	var summaryQueries []model.IssueQuery
	var occurrenceQueries []model.IssueOccurrenceQuery
	repository := &issueTestRepository{}
	repository.queryIssues = func(query model.IssueQuery) (model.IssuePage, error) {
		summaryQueries = append(summaryQueries, query)
		return model.IssuePage{
			Data: []model.IssueSummary{{
				IssueID:        issueID,
				Severity:       "medium",
				SessionCount:   2,
				LastObservedAt: now.Add(-time.Minute),
			}},
			Analysis:            model.IssueAnalysisCoverage{CurrentSessions: 2, Complete: true},
			CursorEpoch:         "epoch-2",
			Snapshot:            23,
			RetentionGeneration: 5,
			IssuedAt:            issuedAt,
		}, nil
	}
	repository.queryOccurrences = func(query model.IssueOccurrenceQuery) (model.IssueOccurrencePage, error) {
		occurrenceQueries = append(occurrenceQueries, query)
		return model.IssueOccurrencePage{
			Data: []model.IssueOccurrence{{
				OccurrenceID:   "occurrence-1",
				IssueID:        issueID,
				LastObservedAt: now.Add(-time.Minute),
			}},
			CursorEpoch:         query.CursorEpoch,
			Snapshot:            query.Snapshot,
			RetentionGeneration: query.RetentionGeneration,
			IssuedAt:            query.IssuedAt,
			HasMore:             len(occurrenceQueries) == 1,
		}, nil
	}
	service := New(
		issueTestCoreRepository{},
		WithIssueRepository(repository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)
	viewCursor, err := service.sealIssueCursor(issueCursorEnvelope{
		Version:             issueCursorVersion,
		Kind:                "issue_view",
		CursorEpoch:         "epoch-2",
		Snapshot:            23,
		RetentionGeneration: 5,
		IssuedAt:            now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID:    strings.ToUpper(issueID),
		Limit:      1,
		ViewCursor: viewCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == nil ||
		!first.HasMore ||
		first.ViewCursor == "" ||
		first.Data.Occurrences == nil ||
		first.Data.Occurrences[0].Evidence.CitedEventIDs == nil {
		t.Fatalf("first issue detail = %+v", first)
	}
	detailView, err := service.openIssueCursor(first.ViewCursor, "issue_view")
	if err != nil {
		t.Fatal(err)
	}
	if detailView.Snapshot != 23 ||
		detailView.IssuedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("detail view cursor = %+v", detailView)
	}
	if summaryQueries[0].Snapshot != 23 ||
		summaryQueries[0].Filter.AttentionKind != model.AttentionKindAll ||
		summaryQueries[0].Filter.Experimental != model.ExperimentalInclude ||
		occurrenceQueries[0].Snapshot != 23 ||
		!occurrenceQueries[0].IssuedAt.Equal(now) {
		t.Fatalf("view-transferred queries = summary %+v occurrence %+v",
			summaryQueries[0], occurrenceQueries[0])
	}

	now = now.Add(2 * time.Minute)
	second, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID: issueID,
		Cursor:  *first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || second.NextCursor != nil {
		t.Fatalf("second issue detail = %+v", second)
	}
	continuedView, err := service.openIssueCursor(second.ViewCursor, "issue_view")
	if err != nil {
		t.Fatal(err)
	}
	if continuedView.Snapshot != 23 ||
		continuedView.IssuedAt !=
			time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC).
				Format(time.RFC3339Nano) {
		t.Fatalf("continued detail view cursor = %+v", continuedView)
	}
	if occurrenceQueries[1].Snapshot != 23 ||
		!occurrenceQueries[1].IssuedAt.Equal(time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)) ||
		occurrenceQueries[1].Cursor == nil ||
		occurrenceQueries[1].Cursor.OccurrenceID != "occurrence-1" {
		t.Fatalf("continued occurrence query = %+v", occurrenceQueries[1])
	}
	if _, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID: otherIssueID,
		Cursor:  *first.NextCursor,
	}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-issue occurrence cursor error = %v", err)
	}
	if _, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID:    issueID,
		Cursor:     *first.NextCursor,
		ViewCursor: viewCursor,
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cursor mutual exclusion error = %v", err)
	}
}

func TestDecodeIssueViewCursorReturnsNeutralClaims(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	service := New(
		issueTestCoreRepository{},
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)
	cursor, err := service.sealIssueCursor(issueCursorEnvelope{
		Version:             issueCursorVersion,
		Kind:                "issue_view",
		CursorEpoch:         "epoch-3",
		Snapshot:            23,
		RetentionGeneration: 7,
		IssuedAt:            now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := service.DecodeIssueViewCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if claims.CursorEpoch != "epoch-3" ||
		claims.Snapshot != 23 ||
		claims.RetentionGeneration != 7 ||
		!claims.IssuedAt.Equal(now) {
		t.Fatalf("claims = %+v", claims)
	}

	empty, err := service.sealIssueCursor(issueCursorEnvelope{
		Version:             issueCursorVersion,
		Kind:                "issue_view",
		CursorEpoch:         "epoch-3",
		Snapshot:            0,
		RetentionGeneration: 7,
		IssuedAt:            now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecodeIssueViewCursor(empty); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("empty snapshot error = %v, want ErrInvalidCursor", err)
	}
	if _, err := service.DecodeIssueViewCursor("PRIVATE_CURSOR"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("malformed cursor error = %v, want ErrInvalidCursor", err)
	}
	now = now.Add(issueCursorLifetime + time.Nanosecond)
	if _, err := service.DecodeIssueViewCursor(cursor); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expired cursor error = %v, want ErrCursorExpired", err)
	}
}

func TestIssueErrorPrecedenceAndLegacyCursorIsolation(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	issueID := testIssueID("f")
	repository := &issueTestRepository{}
	service := New(
		issueTestCoreRepository{},
		WithIssueRepository(repository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)
	view := func(issuedAt time.Time) string {
		value, err := service.sealIssueCursor(issueCursorEnvelope{
			Version:             issueCursorVersion,
			Kind:                "issue_view",
			CursorEpoch:         "epoch-4",
			Snapshot:            7,
			RetentionGeneration: 2,
			IssuedAt:            issuedAt.Format(time.RFC3339Nano),
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if _, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID:    "PRIVATE_ISSUE_CANARY",
		ViewCursor: "PRIVATE_CURSOR_CANARY",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("malformed identifier precedence error = %v", err)
	}
	if _, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID:    issueID,
		ViewCursor: view(now.Add(time.Second)),
	}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("future cursor error = %v", err)
	}
	if _, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID:    issueID,
		ViewCursor: view(now.Add(-issueCursorLifetime - time.Nanosecond)),
	}); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expired cursor error = %v", err)
	}

	repository.queryIssues = func(model.IssueQuery) (model.IssuePage, error) {
		return model.IssuePage{}, model.ErrIssueSnapshotExpired
	}
	if _, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID:    issueID,
		ViewCursor: view(now),
	}); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("compacted cursor error = %v", err)
	}
	repository.queryIssues = func(model.IssueQuery) (model.IssuePage, error) {
		return model.IssuePage{}, model.ErrIssueSnapshotInvalid
	}
	if _, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID:    issueID,
		ViewCursor: view(now),
	}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("newer snapshot error = %v", err)
	}
	repository.queryIssues = func(model.IssueQuery) (model.IssuePage, error) {
		return model.IssuePage{
			CursorEpoch:         "epoch-4",
			Snapshot:            7,
			RetentionGeneration: 2,
			IssuedAt:            now,
		}, nil
	}
	if _, err := service.GetIssue(context.Background(), IssueDetailRequest{
		IssueID:    issueID,
		ViewCursor: view(now),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent issue error = %v", err)
	}

}

func TestNewRequiresExplicitIssueRepositoryCapability(t *testing.T) {
	repository := &issueCapableCoreRepository{
		issueTestRepository: &issueTestRepository{},
	}
	service := New(repository)

	if _, err := service.ListIssues(context.Background(), IssueListRequest{}); err == nil {
		t.Fatal("New(repository) implicitly enabled issue reads")
	}
	if _, err := service.LookupSessionEvents(context.Background(), EventLookupRequest{
		SessionID: "session-1",
		EventIDs:  []string{testEventID},
	}); err == nil {
		t.Fatal("New(repository) implicitly enabled exact event lookup")
	}
}

func TestLegacyCursorsRemainCompatibleAndRejectIssueOnlyFields(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	kinds := []struct {
		name     string
		kind     string
		sequence int64
	}{
		{name: "sessions", kind: "sessions"},
		{name: "session timeline", kind: "session_events", sequence: 7},
		{name: "activity", kind: "activity", sequence: 7},
		{name: "findings", kind: "findings"},
	}
	issueFields := []struct {
		name   string
		mutate func(*cursorEnvelope)
	}{
		{
			name: "issued_at",
			mutate: func(cursor *cursorEnvelope) {
				cursor.IssuedAt = now.Format(time.RFC3339Nano)
			},
		},
		{
			name: "severity_rank",
			mutate: func(cursor *cursorEnvelope) {
				cursor.SeverityRank = 1
			},
		},
		{
			name: "repeated",
			mutate: func(cursor *cursorEnvelope) {
				cursor.Repeated = true
			},
		},
	}

	for _, kind := range kinds {
		t.Run(kind.name, func(t *testing.T) {
			legacy := cursorEnvelope{
				Version:     cursorVersion,
				Kind:        kind.kind,
				Snapshot:    1,
				Fingerprint: "fingerprint",
				Time:        now.Format(time.RFC3339Nano),
				Sequence:    kind.sequence,
				ID:          "position-1",
			}
			encoded, err := encodeCursor(legacy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeCursor(encoded, kind.kind, "fingerprint"); err != nil {
				t.Fatalf("legacy cursor rejected: %v", err)
			}

			for _, field := range issueFields {
				t.Run(field.name, func(t *testing.T) {
					withIssueField := legacy
					field.mutate(&withIssueField)
					encoded, err := encodeCursor(withIssueField)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := decodeCursor(
						encoded,
						kind.kind,
						"fingerprint",
					); !errors.Is(err, ErrInvalidCursor) {
						t.Fatalf("issue-only field accepted: %v", err)
					}
				})
			}
		})
	}
}

func TestExactEventLookupValidatesDeduplicatesAndReturnsNonNilSlices(t *testing.T) {
	var query model.EventLookupQuery
	repository := &issueTestRepository{
		lookupEvents: func(value model.EventLookupQuery) (model.EventLookupResult, error) {
			query = value
			return model.EventLookupResult{
				Data:           nil,
				RequestedCount: len(value.EventIDs),
				MissingCount:   len(value.EventIDs),
			}, nil
		},
	}
	service := New(
		issueTestCoreRepository{},
		WithIssueRepository(repository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
	)
	response, err := service.LookupSessionEvents(context.Background(), EventLookupRequest{
		SessionID: " session-1 ",
		EventIDs:  []string{testEventID, testEventID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if query.SessionID != "session-1" ||
		len(query.EventIDs) != 1 ||
		response.RequestedCount != 1 ||
		response.Data == nil ||
		response.MissingEventIDs == nil {
		t.Fatalf("event lookup query=%+v response=%+v", query, response)
	}
	if _, err := service.LookupSessionEvents(context.Background(), EventLookupRequest{
		SessionID: "session-1",
		EventIDs:  []string{"PRIVATE_EVENT_CANARY"},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("malformed event ID error = %v", err)
	}
}

func TestIssueCatalogUsesFixedSourceSignalPresentation(t *testing.T) {
	tamper := numbatGuardrailsOffSourceSignal
	unknown := "custom.rule"
	tests := []struct {
		name                    string
		issue                   model.IssueSummary
		wantTitle               string
		wantObservation         string
		wantCaveat              string
		wantAction              string
		wantCatalogStatus       string
		wantSourceCatalogStatus string
		wantSourceCode          *string
	}{
		{
			name: "known Numbat source signal",
			issue: model.IssueSummary{
				TitleCode:        "issue.numbat_finding",
				Origin:           "numbat",
				SourceSignalCode: &tamper,
			},
			wantTitle:               "Fewer approval prompts enabled",
			wantObservation:         "Belay recorded a setting that lets actions already permitted by the agent run without asking for approval each time.",
			wantCaveat:              "This setting may be intentional. The record does not show whether an action bypassed a prompt or caused harm.",
			wantAction:              "review_agent_permissions",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "known",
			wantSourceCode:          &tamper,
		},
		{
			name: "unknown safe Numbat source signal",
			issue: model.IssueSummary{
				TitleCode:        "issue.numbat_finding",
				Origin:           "numbat",
				SourceSignalCode: &unknown,
			},
			wantTitle:               "Imported finding—not yet explained by Belay",
			wantObservation:         "Belay retained this imported finding but does not yet have a reviewed explanation.",
			wantCaveat:              "Review the cited evidence; Belay does not infer its impact or recommend a change.",
			wantAction:              "inspect_cited_events",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "unknown",
			wantSourceCode:          &unknown,
		},
		{
			name: "missing source signal",
			issue: model.IssueSummary{
				TitleCode: "issue.numbat_finding",
				Origin:    "numbat",
			},
			wantTitle:               "Imported finding—not yet explained by Belay",
			wantObservation:         "Belay retained this imported finding but does not yet have a reviewed explanation.",
			wantCaveat:              "Review the cited evidence; Belay does not infer its impact or recommend a change.",
			wantAction:              "inspect_cited_events",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "unknown",
		},
		{
			name: "non Numbat catalog",
			issue: model.IssueSummary{
				TitleCode: "issue.explicit_command_failure",
				Origin:    "belay",
			},
			wantTitle:               "Command failed",
			wantObservation:         "The agent reported that a command failed.",
			wantCaveat:              "This does not identify why the command failed or whether a later attempt succeeded.",
			wantAction:              "inspect_cited_events",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "not_applicable",
		},
		{
			name: "explicit tool failure",
			issue: model.IssueSummary{
				TitleCode: "issue.explicit_tool_failure",
				Origin:    "belay",
			},
			wantTitle:               "Tool call failed",
			wantObservation:         "The agent reported that a tool call failed.",
			wantCaveat:              "Belay does not know why it failed or whether a later attempt succeeded.",
			wantAction:              "inspect_cited_events",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "not_applicable",
		},
		{
			name: "repeated command attempts",
			issue: model.IssueSummary{
				TitleCode: "issue.repeated_command_attempts",
				Origin:    "belay",
			},
			wantTitle:               "Command repeatedly attempted",
			wantObservation:         "Belay recorded the same minimized command pattern several times close together.",
			wantCaveat:              "This can be intentional and does not prove that the agent was stuck.",
			wantAction:              "inspect_matching_sessions",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "not_applicable",
		},
		{
			name: "permission denial",
			issue: model.IssueSummary{
				TitleCode: "issue.explicit_permission_denial",
				Origin:    "belay",
			},
			wantTitle:               "Permission denied",
			wantObservation:         "The agent reported that a permission request was denied.",
			wantCaveat:              "This may be expected. Inspect the cited evidence if the denial blocked the work.",
			wantAction:              "inspect_cited_events",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "not_applicable",
		},
		{
			name: "verification gap",
			issue: model.IssueSummary{
				TitleCode: "issue.verification_not_observed",
				Origin:    "belay",
			},
			wantTitle:               "No recognized verification command observed",
			wantObservation:         "After a recorded file change, Belay did not see a test or verification command it recognizes before the session ended.",
			wantCaveat:              "Verification may have happened outside the activity Belay recorded.",
			wantAction:              "inspect_verification_events",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "not_applicable",
		},
		{
			name: "retained verification gap",
			issue: model.IssueSummary{
				TitleCode: "issue.retained_verification_gap_after_changes",
				Origin:    "belay",
			},
			wantTitle:               "No recognized verification retained after changes",
			wantObservation:         "Belay's retained evidence contains no recognized verification command after the final recorded file change and before the session ended.",
			wantCaveat:              "This does not show that verification did not occur; Belay only checks supported commands in retained activity.",
			wantAction:              "inspect_cited_events",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "not_applicable",
		},
		{
			name: "unresolved verification",
			issue: model.IssueSummary{
				TitleCode: "issue.unresolved_verification_failure_at_completion",
				Origin:    "belay",
			},
			wantTitle:               "Verification still failed at session end",
			wantObservation:         "A test or verification command Belay recognizes failed, and Belay did not see a later successful run before the session ended.",
			wantCaveat:              "This describes only the recorded session.",
			wantAction:              "inspect_verification_events",
			wantCatalogStatus:       "known",
			wantSourceCatalogStatus: "not_applicable",
		},
		{
			name: "unsupported issue title",
			issue: model.IssueSummary{
				TitleCode: "issue.future_unknown",
				Origin:    "belay",
			},
			wantTitle:               "Finding not yet explained",
			wantObservation:         "Belay retained this finding but does not yet have a reviewed explanation.",
			wantCaveat:              "Review the cited evidence; Belay does not infer its impact or recommend a change.",
			wantAction:              "inspect_cited_events",
			wantCatalogStatus:       "unknown",
			wantSourceCatalogStatus: "not_applicable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := issueCatalog(test.issue)
			if catalog.CatalogVersion != IssueCatalogVersion ||
				catalog.SourceSignalCatalogVersion != SourceSignalCatalogVersion ||
				catalog.DisplayTitle != test.wantTitle ||
				catalog.ObservationStatement != test.wantObservation ||
				catalog.Caveat != test.wantCaveat ||
				catalog.NextEvidenceAction != test.wantAction ||
				catalog.CatalogStatus != test.wantCatalogStatus ||
				catalog.SourceSignalCatalogStatus != test.wantSourceCatalogStatus {
				t.Fatalf("catalog = %+v", catalog)
			}
			if test.wantSourceCode == nil {
				if catalog.SourceSignalCode != nil {
					t.Fatalf("source signal = %q, want null", *catalog.SourceSignalCode)
				}
			} else if catalog.SourceSignalCode == nil ||
				*catalog.SourceSignalCode != *test.wantSourceCode {
				t.Fatalf("source signal = %v, want %q", catalog.SourceSignalCode, *test.wantSourceCode)
			}
			if test.name == "known Numbat source signal" &&
				(catalog.ObservationStatement != sourcecatalog.GuardrailsObservationStatement ||
					catalog.Caveat != sourcecatalog.GuardrailsCaveat) {
				t.Fatalf("known source copy = %+v", catalog)
			}
			if strings.Contains(catalog.ObservationStatement, "may have") {
				t.Fatalf("catalog observation speculates: %+v", catalog)
			}
		})
	}
}

func testIssueID(character string) string {
	return "iss_" + strings.Repeat(character, 52)
}

func testFingerprintID(character string) string {
	return "ifp_" + strings.Repeat(character, 52)
}
