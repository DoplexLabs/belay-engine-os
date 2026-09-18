package localapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/experience/candidatecompiler"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	maxCandidateProjectSessions = 200
	maxCandidateSessionRecords  = 500
)

type ExperienceCandidateCompilationStore interface {
	QueryTranscriptSessions(
		context.Context,
		transcript.SessionQuery,
	) ([]transcript.Session, error)
	QueryTranscriptTurns(context.Context, string, int) ([]transcript.Turn, error)
	QueryTrajectoryEdges(
		context.Context,
		local.TrajectoryEdgeQuery,
	) ([]trajectory.Edge, error)
	QueryOutcomes(
		context.Context,
		local.OutcomeQuery,
	) ([]trajectory.Outcome, error)
	InsertExperienceCandidate(context.Context, experience.Candidate) (bool, error)
}

type ExperienceCandidateCompilationReport struct {
	ProjectIdentity              string
	SessionsConsidered           int
	SessionsCompiled             int
	PartialSessionsCompiled      int
	LiveSessionsCompiled         int
	SessionsSkippedIncomplete    int
	TranscriptIncompleteSessions int
	EdgeCapSessions              int
	OutcomeCapSessions           int
	ProjectSessionCapReached     bool
	CandidatesInserted           int
	CandidatesReplayed           int
}

// CompileProjectExperienceCandidatesOnce reads existing retained trajectory
// evidence and stores inert deterministic candidates. It does not invoke a
// harness, approve an experience, or activate guidance.
func CompileProjectExperienceCandidatesOnce(
	ctx context.Context,
	store ExperienceCandidateCompilationStore,
	projectIdentity string,
) (ExperienceCandidateCompilationReport, error) {
	if store == nil || projectIdentity == "" {
		return ExperienceCandidateCompilationReport{}, errors.New(
			"experience candidate compilation requires a store and project",
		)
	}
	report := ExperienceCandidateCompilationReport{
		ProjectIdentity: projectIdentity,
	}
	sessions, err := store.QueryTranscriptSessions(ctx, transcript.SessionQuery{
		ProjectIdentity: projectIdentity,
		Limit:           maxCandidateProjectSessions,
	})
	if err != nil {
		return report, err
	}
	report.ProjectSessionCapReached =
		len(sessions) >= maxCandidateProjectSessions
	for _, session := range sessions {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.SessionsConsidered++
		if session.ProjectIdentity != projectIdentity {
			return report, errors.New(
				"candidate transcript session project does not match query",
			)
		}
		turns, err := store.QueryTranscriptTurns(
			ctx,
			session.SessionKey,
			maxTrajectoryTranscriptTurns,
		)
		if err != nil {
			return report, err
		}
		switch session.Coverage {
		case transcript.CoverageComplete,
			transcript.CoveragePartial,
			transcript.CoverageLive:
		default:
			report.SessionsSkippedIncomplete++
			report.TranscriptIncompleteSessions++
			continue
		}
		if len(turns) != session.TurnCount {
			report.SessionsSkippedIncomplete++
			report.TranscriptIncompleteSessions++
			continue
		}
		edges, err := store.QueryTrajectoryEdges(ctx, local.TrajectoryEdgeQuery{
			ProjectIdentity: projectIdentity,
			SessionKey:      session.SessionKey,
			Limit:           maxCandidateSessionRecords,
		})
		if err != nil {
			return report, err
		}
		outcomes, err := store.QueryOutcomes(ctx, local.OutcomeQuery{
			ProjectIdentity: projectIdentity,
			SessionKey:      session.SessionKey,
			Limit:           maxCandidateSessionRecords,
		})
		if err != nil {
			return report, err
		}
		incomplete := false
		if len(edges) >= maxCandidateSessionRecords {
			report.EdgeCapSessions++
			incomplete = true
		}
		if len(outcomes) >= maxCandidateSessionRecords {
			report.OutcomeCapSessions++
			incomplete = true
		}
		if incomplete {
			report.SessionsSkippedIncomplete++
			continue
		}
		compiled, err := candidatecompiler.Compile(candidatecompiler.Input{
			ProjectIdentity: projectIdentity,
			Turns:           turns,
			Edges:           edges,
			Outcomes:        outcomes,
			ProjectConfigs: map[string]issueintel.ProjectConfig{
				session.SessionKey: loadProjectConfig(session.ProjectPath),
			},
		})
		if err != nil {
			return report, fmt.Errorf(
				"compile experience candidates for session %q: %w",
				session.SessionKey,
				err,
			)
		}
		report.SessionsCompiled++
		switch session.Coverage {
		case transcript.CoveragePartial:
			report.PartialSessionsCompiled++
		case transcript.CoverageLive:
			report.LiveSessionsCompiled++
		}
		for _, candidate := range compiled.Candidates {
			inserted, err := store.InsertExperienceCandidate(ctx, candidate)
			if err != nil {
				return report, err
			}
			if inserted {
				report.CandidatesInserted++
			} else {
				report.CandidatesReplayed++
			}
		}
	}
	return report, nil
}
