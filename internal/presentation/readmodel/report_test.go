package readmodel

import (
	"context"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type reportTestRepository struct {
	issues []issueintel.Issue
	usage  issueintel.UsageSnapshot
	fixes  []issueintel.FixRecord
}

func (r *reportTestRepository) QueryCostIssues(
	_ context.Context,
	query issueintel.Query,
) ([]issueintel.Issue, error) {
	values := append([]issueintel.Issue(nil), r.issues...)
	if query.Limit > 0 && len(values) > query.Limit {
		values = values[:query.Limit]
	}
	return values, nil
}

func (r *reportTestRepository) GetCostIssue(
	_ context.Context,
	issueID string,
) (issueintel.Issue, error) {
	for _, issue := range r.issues {
		if issue.IssueID == issueID {
			return issue, nil
		}
	}
	return issueintel.Issue{}, ErrNotFound
}

func (r *reportTestRepository) ReadUsageSnapshot(
	context.Context,
) (issueintel.UsageSnapshot, error) {
	return r.usage, nil
}

func (r *reportTestRepository) ReadCostIssueTotals(
	context.Context,
) (issueintel.IssueCostTotals, error) {
	var result issueintel.IssueCostTotals
	for _, issue := range r.issues {
		result.IssueCount++
		if issue.Cost.WastedUSD == nil {
			result.LowerBound = true
			continue
		}
		result.AttributedUSD += *issue.Cost.WastedUSD
		result.LowerBound = result.LowerBound || issue.Cost.LowerBound
	}
	return result, nil
}

func (r *reportTestRepository) ListCostIssueFixes(
	context.Context,
	int,
) ([]issueintel.FixRecord, error) {
	return append([]issueintel.FixRecord(nil), r.fixes...), nil
}

func TestReportCombinesUsageIssuesFixesAndWaste(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	firstCost, secondCost := 4.0, 3.0
	repository := &reportTestRepository{
		usage: issueintel.UsageSnapshot{
			Totals: issueintel.UsageTotals{
				SessionCount:     12,
				WallDurationMS:   7_200_000,
				TotalTokens:      120_000,
				TotalCostUSD:     10,
				CostLowerBound:   false,
				TokensLowerBound: true,
				Harnesses:        []string{"claude-code", "codex"},
			},
			Weeks: []issueintel.UsageWeek{{
				WeekStart:    now.AddDate(0, 0, -7),
				SessionCount: 4,
				TotalCostUSD: 2,
			}},
			Coverage: transcript.CoverageCounts{
				WithTranscript:    10,
				WithoutTranscript: 2,
			},
		},
		issues: []issueintel.Issue{
			{
				IssueID: "csi_one",
				Cost:    issueintel.Cost{WastedUSD: &firstCost},
			},
			{
				IssueID: "csi_two",
				Cost: issueintel.Cost{
					WastedUSD:  &secondCost,
					LowerBound: true,
				},
			},
		},
		fixes: []issueintel.FixRecord{{
			FixID: "fix_one",
			State: "applied",
		}},
	}
	service := New(
		issueTestCoreRepository{},
		WithClock(func() time.Time { return now }),
		WithCostIssueRepository(repository),
	)
	report, err := service.GetReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ProjectionVersion != ReportProjectionVersion ||
		report.Totals.Sessions != 12 ||
		report.Totals.Hours != 2 ||
		report.Totals.Tokens != 120_000 ||
		report.Totals.Dollars != 10 ||
		len(report.TopIssues) != 2 ||
		len(report.Fixes) != 1 ||
		!report.Fixes[0].Applied ||
		report.Fixes[0].VerificationState != "deferred" ||
		report.Waste.SharePercent == nil ||
		*report.Waste.SharePercent != 70 ||
		!report.Waste.LowerBound {
		t.Fatalf("report = %+v", report)
	}
}

func TestReportKeepsFreshInstallArraysAndOmitsUncertainWasteShare(
	t *testing.T,
) {
	repository := &reportTestRepository{
		usage: issueintel.UsageSnapshot{
			Totals: issueintel.UsageTotals{
				TotalCostUSD:   2,
				CostLowerBound: true,
			},
		},
	}
	service := New(
		issueTestCoreRepository{},
		WithCostIssueRepository(repository),
	)
	report, err := service.GetReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Weeks == nil || report.TopIssues == nil ||
		report.Fixes == nil || report.Totals.Harnesses == nil ||
		report.Waste.SharePercent != nil ||
		!report.Waste.TotalIncomplete {
		t.Fatalf("empty report = %+v", report)
	}
}
