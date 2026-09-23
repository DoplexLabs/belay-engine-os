package readmodel

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

type costIssueReadRepository struct {
	issues []issueintel.Issue
	query  issueintel.Query
}

func (r *costIssueReadRepository) QueryCostIssues(
	_ context.Context,
	query issueintel.Query,
) ([]issueintel.Issue, error) {
	r.query = query
	return append([]issueintel.Issue(nil), r.issues...), nil
}

func (r *costIssueReadRepository) GetCostIssue(
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

func TestCostIssueReadModelListsAndGetsRankedIssues(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	repository := &costIssueReadRepository{
		issues: []issueintel.Issue{{
			IssueID:    "csi_test",
			DetectorID: issueintel.DetectorRetryLoop,
			Headline:   "Tests failed repeatedly",
			Excerpts: []issueintel.Excerpt{{
				Text: "FAIL package/example",
			}},
		}},
	}
	service := New(
		issueTestCoreRepository{},
		WithClock(func() time.Time { return now }),
		WithCostIssueRepository(repository),
	)
	list, err := service.ListCostIssues(
		context.Background(),
		CostIssueListRequest{
			Limit:           5,
			ProjectIdentity: "project-a",
			DetectorID:      issueintel.DetectorRetryLoop,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if list.SchemaVersion != CostIssueProjectionVersion ||
		!list.GeneratedAt.Equal(now) ||
		len(list.Data) != 1 ||
		repository.query != (issueintel.Query{
			Limit:           5,
			ProjectIdentity: "project-a",
			DetectorID:      issueintel.DetectorRetryLoop,
			RankingPolicy:   issueintel.RankingPolicyDeterministic,
		}) ||
		list.Data[0].EvidenceBasis.Kind !=
			issueintel.EvidenceBasisTranscriptExcerpt {
		t.Fatalf("cost issue list/query = %+v/%+v", list, repository.query)
	}
	detail, err := service.GetCostIssue(context.Background(), "csi_test")
	if err != nil || detail.Data.Headline != "Tests failed repeatedly" {
		t.Fatalf("cost issue detail/error = %+v/%v", detail, err)
	}
	if _, err := service.GetCostIssue(
		context.Background(),
		"csi_missing",
	); err != ErrNotFound {
		t.Fatalf("missing cost issue error = %v", err)
	}
}

func TestCostIssueReadModelCanRestoreLegacyRankingPolicy(t *testing.T) {
	repository := &costIssueReadRepository{}
	service := New(
		issueTestCoreRepository{},
		WithCostIssueRepository(repository),
		WithCostIssueRankingPolicy(issueintel.RankingPolicyLegacy),
	)
	if _, err := service.ListCostIssues(
		context.Background(),
		CostIssueListRequest{Limit: 5},
	); err != nil {
		t.Fatal(err)
	}
	if repository.query.RankingPolicy != issueintel.RankingPolicyLegacy {
		t.Fatalf("ranking policy = %q", repository.query.RankingPolicy)
	}
}

func TestCostIssueEvidenceBasisPrefersEpisodesAndExplainsMissingExcerpts(
	t *testing.T,
) {
	episode := costIssueEvidenceBasis(issueintel.Issue{
		EpisodeRefs: []string{"eep_test"},
		Excerpts:    []issueintel.Excerpt{{Text: "FAIL"}},
	})
	if episode.Kind != issueintel.EvidenceBasisActionSequence {
		t.Fatalf("episode evidence basis = %+v", episode)
	}
	activity := costIssueEvidenceBasis(issueintel.Issue{})
	if activity.Kind != issueintel.EvidenceBasisRetainedActivity ||
		activity.Summary == "" {
		t.Fatalf("activity evidence basis = %+v", activity)
	}
}

func TestCostIssueReadModelRequiresCapabilityAndValidRequest(t *testing.T) {
	service := New(issueTestCoreRepository{})
	if _, err := service.ListCostIssues(
		context.Background(),
		CostIssueListRequest{},
	); err != ErrCapabilityUnavailable {
		t.Fatalf("missing capability error = %v", err)
	}
	service = New(
		issueTestCoreRepository{},
		WithCostIssueRepository(&costIssueReadRepository{}),
	)
	if _, err := service.ListCostIssues(
		context.Background(),
		CostIssueListRequest{Limit: 101},
	); err != ErrInvalidRequest {
		t.Fatalf("invalid list error = %v", err)
	}
}
