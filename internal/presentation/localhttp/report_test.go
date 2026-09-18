package localhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

type reportHTTPRepository struct {
	issues []issueintel.Issue
	usage  issueintel.UsageSnapshot
	fixes  []issueintel.FixRecord
}

func (repository *reportHTTPRepository) QueryCostIssues(
	_ context.Context,
	query issueintel.Query,
) ([]issueintel.Issue, error) {
	issues := append([]issueintel.Issue(nil), repository.issues...)
	if query.Limit > 0 && len(issues) > query.Limit {
		issues = issues[:query.Limit]
	}
	return issues, nil
}

func (repository *reportHTTPRepository) GetCostIssue(
	_ context.Context,
	issueID string,
) (issueintel.Issue, error) {
	for _, issue := range repository.issues {
		if issue.IssueID == issueID {
			return issue, nil
		}
	}
	return issueintel.Issue{}, readmodel.ErrNotFound
}

func (repository *reportHTTPRepository) ReadUsageSnapshot(
	context.Context,
) (issueintel.UsageSnapshot, error) {
	return repository.usage, nil
}

func (repository *reportHTTPRepository) ReadCostIssueTotals(
	context.Context,
) (issueintel.IssueCostTotals, error) {
	var result issueintel.IssueCostTotals
	for _, issue := range repository.issues {
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

func (repository *reportHTTPRepository) ListCostIssueFixes(
	context.Context,
	int,
) ([]issueintel.FixRecord, error) {
	return append([]issueintel.FixRecord(nil), repository.fixes...), nil
}

func TestReportRouteRequiresAuthRejectsQueriesAndReturnsReport(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	wastedUSD := 3.5
	repository := &reportHTTPRepository{
		issues: []issueintel.Issue{{
			IssueID:      "csi_report",
			Headline:     "The same test failure kept recurring",
			SessionCount: 3,
			Cost: issueintel.Cost{
				WastedUSD: &wastedUSD,
			},
		}},
		usage: issueintel.UsageSnapshot{
			Totals: issueintel.UsageTotals{
				SessionCount:   8,
				WallDurationMS: 3_600_000,
				TotalTokens:    80_000,
				TotalCostUSD:   10,
				Harnesses:      []string{"codex"},
			},
			Coverage: transcript.CoverageCounts{
				CanonicalCompleteOrLiveWithTranscript: 8,
			},
		},
		fixes: []issueintel.FixRecord{{
			FixID: "fix_report",
			State: "applied",
		}},
	}
	server, err := New(
		readmodel.New(
			testRepository{},
			readmodel.WithClock(func() time.Time { return now }),
			readmodel.WithCostIssueRepository(repository),
		),
		"launch-secret",
	)
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/report",
		nil,
	)
	unauthorized.RemoteAddr = "127.0.0.1:1234"
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.Code)
	}

	withQuery := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/report?limit=1",
		nil,
	)
	withQuery.RemoteAddr = "127.0.0.1:1234"
	withQuery.Header.Set("Authorization", "Bearer launch-secret")
	queryResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(queryResponse, withQuery)
	if queryResponse.Code != http.StatusBadRequest {
		t.Fatalf("query status/body = %d/%s", queryResponse.Code, queryResponse.Body)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/report",
		nil,
	)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Authorization", "Bearer launch-secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body)
	}
	var report readmodel.Report
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.ProjectionVersion != readmodel.ReportProjectionVersion ||
		report.Totals.Sessions != 8 ||
		report.Totals.Hours != 1 ||
		len(report.TopIssues) != 1 ||
		len(report.Fixes) != 1 ||
		report.Waste.SharePercent == nil ||
		*report.Waste.SharePercent != 35 {
		t.Fatalf("report = %+v", report)
	}
}
