package localapp

import (
	"context"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxExperienceAnalysisProjects         = 100
	experienceTrajectoryBatchLimit        = 25
	maxExperienceTrajectoryAnalysisPasses = 20
	maxExperienceAnalysisFailureDetails   = 8
	maxExperienceAnalysisFailureBytes     = 512
)

var ErrExperienceAnalysisIncomplete = errors.New(
	"experience analysis incomplete",
)

type ExperienceProjectDiscoveryStore interface {
	ListTranscriptProjectIdentities(context.Context, int) ([]string, error)
}

type ExperienceAnalysisStore interface {
	ExperienceProjectDiscoveryStore
	IncrementalTrajectoryDerivationStore
	ExperienceCandidateCompilationStore
	ExperienceSemanticProposalStore
}

type ExperienceProjectAnalysisReport struct {
	ProjectsConsidered                 int      `json:"projects_considered"`
	ProjectsCompiled                   int      `json:"projects_compiled"`
	ProjectsAnalyzed                   int      `json:"projects_analyzed"`
	ProjectCapReached                  bool     `json:"project_cap_reached"`
	TrajectoryPasses                   int      `json:"trajectory_passes"`
	TrajectoryPassLimitReached         bool     `json:"trajectory_pass_limit_reached"`
	TrajectoryClaims                   int      `json:"trajectory_claims"`
	TrajectoryComplete                 int      `json:"trajectory_complete"`
	TrajectoryPartial                  int      `json:"trajectory_partial"`
	TrajectoryFailed                   int      `json:"trajectory_failed"`
	TrajectoryStale                    int      `json:"trajectory_stale"`
	CompilationSessionsConsidered      int      `json:"compilation_sessions_considered"`
	CompilationSessionsCompiled        int      `json:"compilation_sessions_compiled"`
	CompilationPartialSessionsCompiled int      `json:"compilation_partial_sessions_compiled"`
	CompilationLiveSessionsCompiled    int      `json:"compilation_live_sessions_compiled"`
	CompilationSessionsSkipped         int      `json:"compilation_sessions_skipped"`
	TranscriptIncompleteSessions       int      `json:"transcript_incomplete_sessions"`
	EdgeCapSessions                    int      `json:"edge_cap_sessions"`
	OutcomeCapSessions                 int      `json:"outcome_cap_sessions"`
	ProjectSessionCaps                 int      `json:"project_session_caps"`
	CandidatesInserted                 int      `json:"candidates_inserted"`
	CandidatesReplayed                 int      `json:"candidates_replayed"`
	EpisodesInserted                   int      `json:"episodes_inserted"`
	EpisodesReplayed                   int      `json:"episodes_replayed"`
	CandidatesConsidered               int      `json:"candidates_considered"`
	CandidatesSkippedExisting          int      `json:"candidates_skipped_existing"`
	CandidatesSkippedInsufficient      int      `json:"candidates_skipped_insufficient"`
	PendingCandidatesDeferred          int      `json:"pending_candidates_deferred"`
	CandidateCapReached                bool     `json:"candidate_cap_reached"`
	Proposals                          int      `json:"proposals"`
	Rejections                         int      `json:"rejections"`
	Defers                             int      `json:"defers"`
	ResultsInserted                    int      `json:"results_inserted"`
	ResultsReplayed                    int      `json:"results_replayed"`
	DiscoveryFailures                  int      `json:"discovery_failures"`
	TrajectoryPassFailures             int      `json:"trajectory_pass_failures"`
	ProjectCompilationFailures         int      `json:"project_compilation_failures"`
	PendingCandidateCheckFailures      int      `json:"pending_candidate_check_failures"`
	ProjectAnalysisFailures            int      `json:"project_analysis_failures"`
	ProjectFailures                    int      `json:"project_failures"`
	FailureDetails                     []string `json:"failure_details,omitempty"`
}

type experiencePendingCandidateReport struct {
	CandidatesConsidered      int
	CandidatesSkippedExisting int
	PendingCandidatesDeferred int
	CandidateCapReached       bool
	Pending                   bool
}

type experienceAnalysisOperations struct {
	analyzeTrajectories func(context.Context) (TrajectoryBatchReport, error)
	compileProject      func(
		context.Context,
		string,
	) (ExperienceCandidateCompilationReport, error)
	pendingProject func(
		context.Context,
		string,
	) (experiencePendingCandidateReport, error)
	analyzeProject func(
		context.Context,
		string,
	) (ExperienceSemanticAnalysisReport, error)
}

// AnalyzeExperienceProjectsOnce advances bounded retained transcript evidence
// through deterministic trajectory and candidate derivation, then asks the
// selected installed harness for inert semantic proposal decisions.
func AnalyzeExperienceProjectsOnce(
	ctx context.Context,
	store ExperienceAnalysisStore,
	harness SemanticHarness,
	runner ExperienceSemanticHarnessRunner,
) (ExperienceProjectAnalysisReport, error) {
	if store == nil || runner == nil {
		return ExperienceProjectAnalysisReport{}, errors.New(
			"experience project analysis requires a store and runner",
		)
	}
	if !harness.Valid() {
		return ExperienceProjectAnalysisReport{}, errors.New(
			"experience project analysis requires a supported harness",
		)
	}
	return analyzeExperienceProjectsOnce(
		ctx,
		store,
		experienceAnalysisOperations{
			analyzeTrajectories: func(
				ctx context.Context,
			) (TrajectoryBatchReport, error) {
				return AnalyzeTrajectorySessionsOnce(
					ctx,
					store,
					experienceTrajectoryBatchLimit,
				)
			},
			compileProject: func(
				ctx context.Context,
				projectIdentity string,
			) (ExperienceCandidateCompilationReport, error) {
				return CompileProjectExperienceCandidatesOnce(
					ctx,
					store,
					projectIdentity,
				)
			},
			pendingProject: func(
				ctx context.Context,
				projectIdentity string,
			) (experiencePendingCandidateReport, error) {
				pending, semanticReport, err := queryPendingExperienceCandidates(
					ctx,
					store,
					projectIdentity,
					harness,
				)
				return experiencePendingCandidateReport{
					CandidatesConsidered: semanticReport.CandidatesConsidered,
					CandidatesSkippedExisting: semanticReport.
						CandidatesSkippedExisting,
					PendingCandidatesDeferred: semanticReport.
						PendingCandidatesDeferred,
					CandidateCapReached: semanticReport.
						CandidateQueryCapReached,
					Pending: len(pending) > 0,
				}, err
			},
			analyzeProject: func(
				ctx context.Context,
				projectIdentity string,
			) (ExperienceSemanticAnalysisReport, error) {
				return AnalyzeExperienceCandidates(
					ctx,
					store,
					projectIdentity,
					harness,
					runner,
				)
			},
		},
	)
}

func analyzeExperienceProjectsOnce(
	ctx context.Context,
	store ExperienceProjectDiscoveryStore,
	operations experienceAnalysisOperations,
) (ExperienceProjectAnalysisReport, error) {
	if store == nil ||
		operations.analyzeTrajectories == nil ||
		operations.compileProject == nil ||
		operations.pendingProject == nil ||
		operations.analyzeProject == nil {
		return ExperienceProjectAnalysisReport{}, errors.New(
			"experience project analysis operations are required",
		)
	}
	discoveredProjects, err := store.ListTranscriptProjectIdentities(
		ctx,
		maxExperienceAnalysisProjects+1,
	)
	report := ExperienceProjectAnalysisReport{}
	if err != nil {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		report.DiscoveryFailures = 1
		return report, ErrExperienceAnalysisIncomplete
	}
	projects := normalizedExperienceProjectIdentities(discoveredProjects)
	if len(projects) > maxExperienceAnalysisProjects {
		report.ProjectCapReached = true
		projects = projects[:maxExperienceAnalysisProjects]
	}
	report.ProjectsConsidered = len(projects)

	incomplete := report.ProjectCapReached
	for pass := 0; pass < maxExperienceTrajectoryAnalysisPasses; pass++ {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		trajectoryReport, trajectoryErr := operations.analyzeTrajectories(ctx)
		report.TrajectoryPasses++
		report.TrajectoryClaims += trajectoryReport.Dirty
		report.TrajectoryComplete += trajectoryReport.Complete
		report.TrajectoryPartial += trajectoryReport.Partial
		report.TrajectoryFailed += trajectoryReport.Failed
		report.TrajectoryStale += trajectoryReport.Stale
		if trajectoryReport.Failed > 0 {
			incomplete = true
		}
		if trajectoryErr != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			report.TrajectoryPassFailures++
			incomplete = true
		}
		if trajectoryReport.Dirty == 0 {
			break
		}
		if pass == maxExperienceTrajectoryAnalysisPasses-1 {
			report.TrajectoryPassLimitReached = true
			incomplete = true
		}
	}

	for _, projectIdentity := range projects {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		compilation, err := operations.compileProject(ctx, projectIdentity)
		report.CompilationSessionsConsidered += compilation.SessionsConsidered
		report.CompilationSessionsCompiled += compilation.SessionsCompiled
		report.CompilationPartialSessionsCompiled +=
			compilation.PartialSessionsCompiled
		report.CompilationLiveSessionsCompiled +=
			compilation.LiveSessionsCompiled
		report.CompilationSessionsSkipped +=
			compilation.SessionsSkippedIncomplete
		report.TranscriptIncompleteSessions +=
			compilation.TranscriptIncompleteSessions
		report.EdgeCapSessions += compilation.EdgeCapSessions
		report.OutcomeCapSessions += compilation.OutcomeCapSessions
		if compilation.ProjectSessionCapReached {
			report.ProjectSessionCaps++
			incomplete = true
		}
		report.CandidatesInserted += compilation.CandidatesInserted
		report.CandidatesReplayed += compilation.CandidatesReplayed
		report.EpisodesInserted += compilation.EpisodesInserted
		report.EpisodesReplayed += compilation.EpisodesReplayed
		if err != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			report.ProjectCompilationFailures++
			report.ProjectFailures++
			report.addFailureDetail(projectIdentity, err)
			incomplete = true
			continue
		}
		report.ProjectsCompiled++

		pending, err := operations.pendingProject(ctx, projectIdentity)
		report.CandidatesConsidered += pending.CandidatesConsidered
		report.CandidatesSkippedExisting +=
			pending.CandidatesSkippedExisting
		report.PendingCandidatesDeferred +=
			pending.PendingCandidatesDeferred
		report.CandidateCapReached =
			report.CandidateCapReached || pending.CandidateCapReached
		if pending.CandidateCapReached ||
			pending.PendingCandidatesDeferred > 0 {
			incomplete = true
		}
		if err != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			report.PendingCandidateCheckFailures++
			report.ProjectFailures++
			report.addFailureDetail(projectIdentity, err)
			incomplete = true
			continue
		}
		if !pending.Pending {
			continue
		}

		report.ProjectsAnalyzed++
		semantic, err := operations.analyzeProject(ctx, projectIdentity)
		report.CandidatesSkippedInsufficient +=
			semantic.CandidatesSkippedInsufficient
		report.Proposals += semantic.Proposed
		report.Rejections += semantic.Rejected
		report.Defers += semantic.Deferred
		report.ResultsInserted += semantic.ResultsInserted
		report.ResultsReplayed += semantic.ResultsReplayed
		report.CandidateCapReached =
			report.CandidateCapReached || semantic.CandidateQueryCapReached
		if semantic.CandidateQueryCapReached ||
			semantic.PendingCandidatesDeferred > 0 {
			incomplete = true
		}
		if err != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			report.ProjectAnalysisFailures++
			report.ProjectFailures++
			report.addFailureDetail(projectIdentity, err)
			incomplete = true
		}
	}
	if incomplete {
		return report, ErrExperienceAnalysisIncomplete
	}
	return report, nil
}

func (report *ExperienceProjectAnalysisReport) addFailureDetail(
	projectIdentity string,
	err error,
) {
	if report == nil || err == nil ||
		len(report.FailureDetails) >= maxExperienceAnalysisFailureDetails {
		return
	}
	value := strings.TrimSpace(projectIdentity + ": " + err.Error())
	if len(value) > maxExperienceAnalysisFailureBytes {
		value = value[:maxExperienceAnalysisFailureBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	report.FailureDetails = append(report.FailureDetails, value)
}

func normalizedExperienceProjectIdentities(projects []string) []string {
	result := make([]string, 0, len(projects))
	seen := make(map[string]bool, len(projects))
	for _, projectIdentity := range projects {
		projectIdentity = strings.TrimSpace(projectIdentity)
		if projectIdentity == "" || seen[projectIdentity] {
			continue
		}
		seen[projectIdentity] = true
		result = append(result, projectIdentity)
	}
	sort.Strings(result)
	return result
}
