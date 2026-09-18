package localhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

type costIssueHTTPRepository struct {
	issues []issueintel.Issue
	query  issueintel.Query
}

func (r *costIssueHTTPRepository) QueryCostIssues(
	_ context.Context,
	query issueintel.Query,
) ([]issueintel.Issue, error) {
	r.query = query
	return append([]issueintel.Issue(nil), r.issues...), nil
}

func (r *costIssueHTTPRepository) GetCostIssue(
	_ context.Context,
	issueID string,
) (issueintel.Issue, error) {
	for _, issue := range r.issues {
		if issue.IssueID == issueID {
			return issue, nil
		}
	}
	return issueintel.Issue{}, sql.ErrNoRows
}

func TestCostIssueRoutesRequireAuthValidateAndReturnEvidence(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	usd := 4.20
	repository := &costIssueHTTPRepository{
		issues: []issueintel.Issue{{
			IssueID:      "csi_http",
			DetectorID:   issueintel.DetectorRetryLoop,
			Headline:     "Tests failed repeatedly",
			SessionCount: 2,
			Cost: issueintel.Cost{
				WastedMinutes: 8,
				WastedTokens:  2200,
				WastedUSD:     &usd,
			},
			Excerpts: []issueintel.Excerpt{{
				Text: "FAIL package/example",
			}},
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
		"http://127.0.0.1/v1/cost-issues",
		nil,
	)
	unauthorized.RemoteAddr = "127.0.0.1:1234"
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.Code)
	}

	listRequest := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/cost-issues?limit=5&project=project-a&detector=retry_loop",
		nil,
	)
	listRequest.RemoteAddr = "127.0.0.1:1234"
	listRequest.Header.Set("Authorization", "Bearer launch-secret")
	listResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status/body = %d/%s", listResponse.Code, listResponse.Body)
	}
	var list readmodel.CostIssueList
	if err := json.NewDecoder(listResponse.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 ||
		list.Data[0].Excerpts[0].Text != "FAIL package/example" ||
		repository.query.Limit != 5 {
		t.Fatalf("cost issue list/query = %+v/%+v", list, repository.query)
	}

	detailRequest := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/cost-issues/csi_http",
		nil,
	)
	detailRequest.RemoteAddr = "127.0.0.1:1234"
	detailRequest.Header.Set("Authorization", "Bearer launch-secret")
	detailResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf(
			"detail status/body = %d/%s",
			detailResponse.Code,
			detailResponse.Body,
		)
	}

	invalidRequest := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1/v1/cost-issues?unknown=true",
		nil,
	)
	invalidRequest.RemoteAddr = "127.0.0.1:1234"
	invalidRequest.Header.Set("Authorization", "Bearer launch-secret")
	invalidResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d", invalidResponse.Code)
	}
}
