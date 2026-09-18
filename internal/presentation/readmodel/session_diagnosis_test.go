package readmodel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestDiagnoseSessionUsesOneBoundedIssueReadAndNoAggregateCounts(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	repository := &briefIssueRepository{issuePage: model.IssuePage{
		Data: []model.IssueSummary{
			{
				IssueID:         testIssueID("a"),
				Category:        "command_failure",
				TitleCode:       "issue.explicit_command_failure",
				Severity:        "high",
				LastObservedAt:  now.Add(-time.Minute),
				OccurrenceCount: 99,
				SessionCount:    77,
				AnalysisStatus:  model.AnalysisCurrent,
			},
			{
				IssueID:        testIssueID("b"),
				Category:       model.AttentionKindEvidenceGap,
				TitleCode:      "issue.verification_not_observed",
				Severity:       "medium",
				LastObservedAt: now.Add(-2 * time.Minute),
				AnalysisStatus: model.AnalysisCurrent,
			},
		},
		Analysis: model.IssueAnalysisCoverage{
			CurrentSessions: 1,
			AnalysisThrough: now,
			Complete:        true,
		},
		CursorEpoch:         "epoch",
		Snapshot:            3,
		RetentionGeneration: 2,
		IssuedAt:            now,
	}}
	service := New(
		&briefCoreRepository{},
		WithIssueRepository(repository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
		WithClock(func() time.Time { return now }),
	)
	detail := SessionDetail{
		SchemaVersion: SchemaVersion,
		Data: model.SessionSummary{
			SessionID: "session-diagnosis",
			Harness:   "codex",
			EndedAt:   now,
			Outcome:   "failed",
			Overview: &model.SessionOverview{
				Counts: model.SessionInsightCounts{ExplicitFailedEvents: 4},
			},
		},
		DataThrough: now,
	}
	diagnosis := service.DiagnoseSession(context.Background(), detail)
	if diagnosis.Status != SessionDiagnosisReady ||
		diagnosis.Summary.State != SessionDiagnosisReviewedFindings ||
		len(diagnosis.ActionCards) != 3 {
		t.Fatalf("diagnosis = %+v", diagnosis)
	}
	if diagnosis.ActionCards[0].Evidence.SessionCount != nil ||
		diagnosis.ActionCards[0].Evidence.OccurrenceCount != nil {
		t.Fatalf("cross-session counts leaked: %+v", diagnosis.ActionCards[0].Evidence)
	}
	for _, card := range diagnosis.ActionCards[:2] {
		if card.NextStep.Kind != BriefTargetSession ||
			card.NextStep.Label != "Review session activity" ||
			card.NextStep.SessionID == nil ||
			*card.NextStep.SessionID != detail.Data.SessionID ||
			card.NextStep.FamilyID != nil ||
			card.NextStep.IssueID != nil ||
			card.NextStep.ViewCursor != nil {
			t.Fatalf("session issue target = %+v", card.NextStep)
		}
	}
	if diagnosis.ActionCards[2].Kind != BriefActionSessionOutcome {
		t.Fatalf("outcome fallback = %+v", diagnosis.ActionCards)
	}
	for _, card := range diagnosis.ActionCards {
		if card.Kind == BriefActionFailedActivity {
			t.Fatal("generic failure was not suppressed by explicit failure issue")
		}
	}
	if len(repository.issueQueries) != 1 {
		t.Fatalf("issue query count = %d", len(repository.issueQueries))
	}
	query := repository.issueQueries[0]
	if query.Limit != 20 ||
		query.Filter.SessionID != detail.Data.SessionID ||
		query.Filter.AttentionKind != model.AttentionKindAll ||
		query.Filter.Experimental != model.ExperimentalStable {
		t.Fatalf("diagnosis query = %+v", query)
	}
}

func TestDiagnoseSessionIssueCardsDoNotRequireIssueViewCursor(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	list := IssueList{
		Data: []model.IssueSummary{{
			IssueID:        testIssueID("c"),
			Category:       "command_failure",
			TitleCode:      "issue.explicit_command_failure",
			Severity:       "high",
			LastObservedAt: now,
			AnalysisStatus: model.AnalysisCurrent,
		}},
	}
	cards, _ := sessionIssueCards(list, "session-exact")
	if len(cards) != 1 ||
		cards[0].NextStep.Kind != BriefTargetSession ||
		cards[0].NextStep.SessionID == nil ||
		*cards[0].NextStep.SessionID != "session-exact" {
		t.Fatalf("cards = %+v", cards)
	}
}

func TestDiagnoseSessionFailureIsolationAndStateMatrix(t *testing.T) {
	now := time.Date(2026, 9, 9, 17, 0, 0, 0, time.UTC)
	repository := &briefIssueRepository{issueErr: errors.New("PRIVATE_ERROR")}
	service := New(
		&briefCoreRepository{},
		WithIssueRepository(repository),
		WithIssueCursorCodec(issueTestCursorCodec{}),
	)
	detail := SessionDetail{Data: model.SessionSummary{
		SessionID: "session-failed",
		Harness:   "codex",
		EndedAt:   now,
		Outcome:   "failed",
		Overview:  &model.SessionOverview{},
	}}
	limited := service.DiagnoseSession(context.Background(), detail)
	if limited.Status != SessionDiagnosisLimited ||
		limited.Summary.State != SessionDiagnosisReportedFailure ||
		len(limited.ActionCards) != 1 ||
		limited.Coverage.IssueSource != BriefSourceUnavailable {
		t.Fatalf("limited diagnosis = %+v", limited)
	}

	repository.issueErr = nil
	repository.issuePage = model.IssuePage{
		Analysis:            model.IssueAnalysisCoverage{Complete: true},
		CursorEpoch:         "epoch",
		Snapshot:            1,
		RetentionGeneration: 1,
		IssuedAt:            now,
	}
	detail.Data.Outcome = "succeeded"
	complete := service.DiagnoseSession(context.Background(), detail)
	if complete.Status != SessionDiagnosisReady ||
		complete.Summary.State != SessionDiagnosisNoReviewedAction {
		t.Fatalf("complete empty diagnosis = %+v", complete)
	}

	repository.issuePage.Analysis.Complete = false
	incomplete := service.DiagnoseSession(context.Background(), detail)
	if incomplete.Status != SessionDiagnosisInsufficientDetail ||
		incomplete.Summary.State != SessionDiagnosisInsufficient {
		t.Fatalf("incomplete diagnosis = %+v", incomplete)
	}
}
