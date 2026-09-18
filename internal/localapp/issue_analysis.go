package localapp

import (
	"context"
	"errors"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const defaultTranscriptIssueProjectLimit = 25

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
