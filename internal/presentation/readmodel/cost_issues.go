package readmodel

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

const CostIssueProjectionVersion = "belay.cost-issues.v1"

type CostIssueRepository interface {
	QueryCostIssues(
		context.Context,
		issueintel.Query,
	) ([]issueintel.Issue, error)
	GetCostIssue(context.Context, string) (issueintel.Issue, error)
}

type InsightRepository interface {
	GetProjectInsight(
		context.Context,
		string,
	) (issueintel.InsightRecord, error)
}

type CostIssueListRequest struct {
	Limit           int
	ProjectIdentity string
	DetectorID      string
}

type CostIssueList struct {
	SchemaVersion string             `json:"schema_version"`
	Data          []issueintel.Issue `json:"data"`
	GeneratedAt   time.Time          `json:"generated_at"`
}

type CostIssueDetail struct {
	SchemaVersion string           `json:"schema_version"`
	Data          issueintel.Issue `json:"data"`
	GeneratedAt   time.Time        `json:"generated_at"`
}

func WithCostIssueRepository(repository CostIssueRepository) Option {
	return func(service *Service) {
		service.costIssueRepository = repository
		if insights, ok := repository.(InsightRepository); ok {
			service.insightRepository = insights
		}
		if reports, ok := repository.(UsageReportRepository); ok {
			service.reportRepository = reports
		}
	}
}

func (s *Service) ListCostIssues(
	ctx context.Context,
	request CostIssueListRequest,
) (CostIssueList, error) {
	if s == nil || s.costIssueRepository == nil {
		return CostIssueList{}, ErrCapabilityUnavailable
	}
	request.ProjectIdentity = strings.TrimSpace(request.ProjectIdentity)
	request.DetectorID = strings.TrimSpace(request.DetectorID)
	if request.Limit < 0 || request.Limit > 100 ||
		len(request.ProjectIdentity) > 4096 ||
		len(request.DetectorID) > 128 {
		return CostIssueList{}, ErrInvalidRequest
	}
	values, err := s.costIssueRepository.QueryCostIssues(ctx, issueintel.Query{
		Limit:           request.Limit,
		ProjectIdentity: request.ProjectIdentity,
		DetectorID:      request.DetectorID,
	})
	if err != nil {
		return CostIssueList{}, err
	}
	values = s.applyInsightFixes(ctx, values)
	return CostIssueList{
		SchemaVersion: CostIssueProjectionVersion,
		Data:          values,
		GeneratedAt:   s.now().UTC(),
	}, nil
}

func (s *Service) GetCostIssue(
	ctx context.Context,
	issueID string,
) (CostIssueDetail, error) {
	if s == nil || s.costIssueRepository == nil {
		return CostIssueDetail{}, ErrCapabilityUnavailable
	}
	issueID = strings.TrimSpace(issueID)
	if issueID == "" || len(issueID) > 512 {
		return CostIssueDetail{}, ErrInvalidRequest
	}
	value, err := s.costIssueRepository.GetCostIssue(ctx, issueID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CostIssueDetail{}, ErrNotFound
		}
		return CostIssueDetail{}, err
	}
	values := s.applyInsightFixes(ctx, []issueintel.Issue{value})
	if len(values) == 1 {
		value = values[0]
	}
	return CostIssueDetail{
		SchemaVersion: CostIssueProjectionVersion,
		Data:          value,
		GeneratedAt:   s.now().UTC(),
	}, nil
}

func (s *Service) applyInsightFixes(
	ctx context.Context,
	issues []issueintel.Issue,
) []issueintel.Issue {
	if s.insightRepository == nil || len(issues) == 0 {
		return issues
	}
	records := make(map[string]issueintel.InsightRecord)
	for index := range issues {
		project := issues[index].Project.Identity
		record, ok := records[project]
		if !ok {
			value, err := s.insightRepository.GetProjectInsight(ctx, project)
			if err != nil {
				records[project] = issueintel.InsightRecord{}
				continue
			}
			record = value
			records[project] = record
		}
		for _, fix := range record.Result.Fixes {
			if fix.IssueID != issues[index].IssueID {
				continue
			}
			issues[index].SuggestedFix.Rationale = fix.RuleText
			issues[index].SuggestedFix.TargetFile = fix.TargetFile
			break
		}
	}
	return issues
}
