package localapp

import (
	"context"
	"errors"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/detection/recoveryissues"
	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
	"github.com/DoplexLabs/belay-engine/internal/experience/candidatecompiler"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	defaultTranscriptIssueProjectLimit = 25
	maxRecoverySessionRecords          = 500
)

type TranscriptIssueAnalysisStore interface {
	ListDirtyTranscriptProjects(
		context.Context,
		...int,
	) ([]local.DirtyTranscriptProject, error)
	LoadTranscriptProjectData(
		context.Context,
		string,
	) (issueintel.ProjectInput, error)
	ReplaceProjectIssueAnalysis(
		context.Context,
		issueintel.Project,
		int64,
		issueintel.Analysis,
	) error
	QueryTrajectoryEdges(
		context.Context,
		local.TrajectoryEdgeQuery,
	) ([]trajectory.Edge, error)
	QueryOutcomes(
		context.Context,
		local.OutcomeQuery,
	) ([]trajectory.Outcome, error)
	InsertEvidenceEpisode(
		context.Context,
		evidenceepisode.Episode,
	) (bool, error)
}

type TranscriptIssueAnalysisReport struct {
	Projects   int
	Issues     int
	Candidates int
	Stale      int
}

func AnalyzeTranscriptIssuesOnce(
	ctx context.Context,
	store TranscriptIssueAnalysisStore,
	limit int,
) (TranscriptIssueAnalysisReport, error) {
	if store == nil {
		return TranscriptIssueAnalysisReport{}, errors.New(
			"transcript issue analysis requires a store",
		)
	}
	if limit <= 0 {
		limit = defaultTranscriptIssueProjectLimit
	}
	dirty, err := store.ListDirtyTranscriptProjects(ctx, limit)
	if err != nil {
		return TranscriptIssueAnalysisReport{}, err
	}
	var report TranscriptIssueAnalysisReport
	var analysisErrors []error
	for _, project := range dirty {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(analysisErrors, err)...)
		}
		input, err := store.LoadTranscriptProjectData(
			ctx,
			project.Project.Identity,
		)
		if err != nil {
			analysisErrors = append(analysisErrors, err)
			continue
		}
		input.Config = loadProjectConfig(input.Project.Path)
		analysis, err := transcriptissues.AnalyzeProject(ctx, input)
		if err != nil {
			analysisErrors = append(analysisErrors, err)
			continue
		}
		analysis, err = addRecoveryIssues(ctx, store, input, analysis)
		if err != nil {
			analysisErrors = append(analysisErrors, err)
			continue
		}
		err = store.ReplaceProjectIssueAnalysis(
			ctx,
			input.Project,
			project.TranscriptGeneration,
			analysis,
		)
		if errors.Is(err, local.ErrTranscriptProjectGenerationChanged) {
			report.Stale++
			continue
		}
		if err != nil {
			analysisErrors = append(analysisErrors, err)
			continue
		}
		report.Projects++
		report.Issues += len(analysis.Issues)
		report.Candidates += len(analysis.CorrectionCandidates)
	}
	return report, errors.Join(analysisErrors...)
}

func addRecoveryIssues(
	ctx context.Context,
	store TranscriptIssueAnalysisStore,
	input issueintel.ProjectInput,
	analysis issueintel.Analysis,
) (issueintel.Analysis, error) {
	var edges []trajectory.Edge
	var outcomes []trajectory.Outcome
	var turns []transcript.Turn
	configs := make(map[string]issueintel.ProjectConfig, len(input.Sessions))
	for _, session := range input.Sessions {
		sessionEdges, err := store.QueryTrajectoryEdges(
			ctx,
			local.TrajectoryEdgeQuery{
				ProjectIdentity: input.Project.Identity,
				SessionKey:      session.Metadata.SessionKey,
				Limit:           maxRecoverySessionRecords,
			},
		)
		if err != nil {
			return analysis, err
		}
		sessionOutcomes, err := store.QueryOutcomes(
			ctx,
			local.OutcomeQuery{
				ProjectIdentity: input.Project.Identity,
				SessionKey:      session.Metadata.SessionKey,
				Limit:           maxRecoverySessionRecords,
			},
		)
		if err != nil {
			return analysis, err
		}
		if len(sessionEdges) >= maxRecoverySessionRecords ||
			len(sessionOutcomes) >= maxRecoverySessionRecords {
			continue
		}
		edges = append(edges, sessionEdges...)
		outcomes = append(outcomes, sessionOutcomes...)
		turns = append(turns, session.Turns...)
		configs[session.Metadata.SessionKey] = input.Config
	}
	if len(edges) == 0 || len(outcomes) == 0 {
		return analysis, nil
	}
	compiled, err := candidatecompiler.Compile(candidatecompiler.Input{
		ProjectIdentity: input.Project.Identity,
		Turns:           turns,
		Edges:           edges,
		Outcomes:        outcomes,
		ProjectConfigs:  configs,
	})
	if err != nil {
		return analysis, err
	}
	sessionsByKey := make(
		map[string]issueintel.Session,
		len(input.Sessions),
	)
	for _, session := range input.Sessions {
		sessionsByKey[session.Metadata.SessionKey] = session
	}
	episodes := make(
		[]evidenceepisode.Episode,
		0,
		len(compiled.FailedApproachRecoveries),
	)
	for _, recovery := range compiled.FailedApproachRecoveries {
		session, ok := sessionsByKey[recovery.SessionKey]
		if !ok {
			continue
		}
		episode, err := evidenceepisode.NewFailureRepair(
			evidenceepisode.FailureRepairInput{
				ProjectIdentity: recovery.ProjectIdentity,
				Session:         session.Metadata,
				SessionTurns:    session.Turns,
				FailureCall:     recovery.FailureCall,
				FailureResult:   recovery.FailureResult,
				SuccessCall:     recovery.SuccessCall,
				SuccessResult:   recovery.SuccessResult,
				OutcomeRefs:     recovery.OutcomeRefs,
			},
		)
		if err != nil {
			return analysis, err
		}
		if _, err := store.InsertEvidenceEpisode(ctx, episode); err != nil {
			return analysis, err
		}
		episodes = append(episodes, episode)
	}
	analysis.Issues = append(
		analysis.Issues,
		recoveryissues.Analyze(
			input.Project,
			input.Sessions,
			episodes,
			input.Now,
		)...,
	)
	return analysis, nil
}

func PollTranscriptIssueAnalysis(
	ctx context.Context,
	store TranscriptIssueAnalysisStore,
	interval time.Duration,
	onError func(error),
) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	run := func() {
		_, err := AnalyzeTranscriptIssuesOnce(
			ctx,
			store,
			defaultTranscriptIssueProjectLimit,
		)
		if err != nil &&
			!errors.Is(err, context.Canceled) &&
			ctx.Err() == nil &&
			onError != nil {
			onError(err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
