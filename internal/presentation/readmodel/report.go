package readmodel

import (
	"context"
	"math"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const ReportProjectionVersion = "belay.report.v1"

type UsageReportRepository interface {
	ReadUsageSnapshot(context.Context) (issueintel.UsageSnapshot, error)
	ReadCostIssueTotals(context.Context) (issueintel.IssueCostTotals, error)
	ListCostIssueFixes(context.Context, int) ([]issueintel.FixRecord, error)
}

type ReportTotals struct {
	Sessions          int      `json:"sessions"`
	Hours             float64  `json:"hours"`
	HoursLowerBound   bool     `json:"hours_lower_bound"`
	Tokens            int64    `json:"tokens"`
	TokensLowerBound  bool     `json:"tokens_lower_bound"`
	Dollars           float64  `json:"dollars"`
	DollarsLowerBound bool     `json:"dollars_lower_bound"`
	Harnesses         []string `json:"harnesses"`
}

type ReportWaste struct {
	AttributedUSD   float64  `json:"attributed_usd"`
	SharePercent    *float64 `json:"share_percent"`
	LowerBound      bool     `json:"lower_bound"`
	OverlapCapped   bool     `json:"overlap_capped"`
	TotalIncomplete bool     `json:"total_incomplete"`
}

type ReportAbout struct {
	TranscriptCoverage transcript.CoverageCounts `json:"transcript_coverage"`
	CostLowerBound     bool                      `json:"cost_lower_bound"`
	TokenLowerBound    bool                      `json:"token_lower_bound"`
	Notes              []string                  `json:"notes"`
}

type Report struct {
	SchemaVersion     string                 `json:"schema_version"`
	ProjectionVersion string                 `json:"projection_version"`
	GeneratedAt       time.Time              `json:"generated_at"`
	Totals            ReportTotals           `json:"totals"`
	Weeks             []issueintel.UsageWeek `json:"weeks"`
	TopIssues         []issueintel.Issue     `json:"top_issues"`
	Fixes             []issueintel.FixStatus `json:"fixes"`
	Waste             ReportWaste            `json:"waste"`
	About             ReportAbout            `json:"about"`
}

func WithUsageReportRepository(repository UsageReportRepository) Option {
	return func(service *Service) {
		service.reportRepository = repository
	}
}

func (s *Service) GetReport(ctx context.Context) (Report, error) {
	if s == nil || s.reportRepository == nil ||
		s.costIssueRepository == nil {
		return Report{}, ErrCapabilityUnavailable
	}
	usage, err := s.reportRepository.ReadUsageSnapshot(ctx)
	if err != nil {
		return Report{}, err
	}
	top, err := s.costIssueRepository.QueryCostIssues(
		ctx,
		issueintel.Query{Limit: 5},
	)
	if err != nil {
		return Report{}, err
	}
	top = s.applyInsightFixes(ctx, top)
	issueCosts, err := s.reportRepository.ReadCostIssueTotals(ctx)
	if err != nil {
		return Report{}, err
	}
	fixRecords, err := s.reportRepository.ListCostIssueFixes(ctx, 50)
	if err != nil {
		return Report{}, err
	}
	fixes := make([]issueintel.FixStatus, 0, len(fixRecords))
	for _, record := range fixRecords {
		fixes = append(fixes, issueintel.FixStatus{
			Fix:               record,
			Applied:           record.State == "applied",
			VerificationState: "deferred",
		})
	}
	return Report{
		SchemaVersion:     SchemaVersion,
		ProjectionVersion: ReportProjectionVersion,
		GeneratedAt:       s.nowUTC(),
		Totals: ReportTotals{
			Sessions: usage.Totals.SessionCount,
			Hours: roundReportValue(
				float64(usage.Totals.WallDurationMS)/3_600_000,
				2,
			),
			HoursLowerBound:  usage.Totals.WallDurationLowerBound,
			Tokens:           usage.Totals.TotalTokens,
			TokensLowerBound: usage.Totals.TokensLowerBound,
			Dollars: roundReportValue(
				usage.Totals.TotalCostUSD,
				4,
			),
			DollarsLowerBound: usage.Totals.CostLowerBound,
			Harnesses:         nonNilStrings(usage.Totals.Harnesses),
		},
		Weeks:     nonNilUsageWeeks(usage.Weeks),
		TopIssues: nonNilIssues(top),
		Fixes:     fixes,
		Waste: reportWaste(
			usage.Totals.TotalCostUSD,
			usage.Totals.CostLowerBound,
			issueCosts,
		),
		About: ReportAbout{
			TranscriptCoverage: usage.Coverage,
			CostLowerBound:     usage.Totals.CostLowerBound,
			TokenLowerBound:    usage.Totals.TokensLowerBound,
			Notes: []string{
				"Token operations include input, output, cache reads, and cache writes from retained usage data.",
				"Dollars are direct-API list-price equivalents, not a billing statement; unknown model prices are never estimated.",
				"Each turn is counted once per issue, and once across detectors in the total attributed-spend percentage.",
				"Issue minutes estimate active elapsed time and exclude pauses longer than 30 minutes; session span retains the harness session's full first-to-last timestamp range.",
				"Fix recurrence and before/after cost verification are deferred.",
			},
		},
	}, nil
}

func reportWaste(
	totalCost float64,
	totalCostLowerBound bool,
	issueCosts issueintel.IssueCostTotals,
) ReportWaste {
	result := ReportWaste{
		AttributedUSD:   roundReportValue(issueCosts.AttributedUSD, 4),
		LowerBound:      issueCosts.LowerBound,
		TotalIncomplete: totalCostLowerBound,
	}
	if totalCost <= 0 || totalCostLowerBound {
		return result
	}
	share := issueCosts.AttributedUSD / totalCost * 100
	if share > 100 {
		result.OverlapCapped = true
		return result
	}
	share = roundReportValue(share, 1)
	result.SharePercent = &share
	return result
}

func nonNilUsageWeeks(values []issueintel.UsageWeek) []issueintel.UsageWeek {
	if values == nil {
		return make([]issueintel.UsageWeek, 0)
	}
	return values
}

func nonNilIssues(values []issueintel.Issue) []issueintel.Issue {
	if values == nil {
		return make([]issueintel.Issue, 0)
	}
	return values
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return make([]string, 0)
	}
	return append([]string(nil), values...)
}

func roundReportValue(value float64, places int) float64 {
	scale := math.Pow10(places)
	return math.Round(value*scale) / scale
}
