package localapp

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
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
	InsertEvidenceEpisode(
		context.Context,
		evidenceepisode.Episode,
	) (bool, error)
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
	EpisodesInserted             int
	EpisodesReplayed             int
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
		episodeRefs := make(map[string][]string)
		for _, recovery := range compiled.FailedApproachRecoveries {
			episode, err := evidenceepisode.NewFailureRepair(
				evidenceepisode.FailureRepairInput{
					ProjectIdentity: recovery.ProjectIdentity,
					Session:         session,
					SessionTurns:    turns,
					FailureCall:     recovery.FailureCall,
					FailureResult:   recovery.FailureResult,
					SuccessCall:     recovery.SuccessCall,
					SuccessResult:   recovery.SuccessResult,
					OutcomeRefs:     recovery.OutcomeRefs,
				},
			)
			if err != nil {
				return report, err
			}
			inserted, err := store.InsertEvidenceEpisode(ctx, episode)
			if err != nil {
				return report, err
			}
			if inserted {
				report.EpisodesInserted++
			} else {
				report.EpisodesReplayed++
			}
			episodeRefs[recovery.CandidateID] = append(
				episodeRefs[recovery.CandidateID],
				episode.EpisodeID,
			)
		}
		for _, procedure := range compiled.SuccessfulProcedureEpisodes {
			episode, err := evidenceepisode.NewMutationVerification(
				evidenceepisode.MutationVerificationInput{
					ProjectIdentity:       session.ProjectIdentity,
					Session:               session,
					SessionTurns:          turns,
					MutationRefs:          procedure.MutationRefs,
					SourceRefs:            procedure.EvidenceRefs,
					VerificationCallRef:   procedure.Anchor.VerifierCallRef,
					VerificationResultRef: procedure.Anchor.VerifierResultRef,
					OutcomeRefs:           procedure.OutcomeIDs,
					VerifierCommand:       procedure.Anchor.RawCommand,
					VerifierCommandClass:  procedure.Anchor.CommandClass,
				},
			)
			if err != nil {
				return report, err
			}
			inserted, err := store.InsertEvidenceEpisode(ctx, episode)
			if err != nil {
				return report, err
			}
			if inserted {
				report.EpisodesInserted++
			} else {
				report.EpisodesReplayed++
			}
			episodeRefs[procedure.CandidateID] = append(
				episodeRefs[procedure.CandidateID],
				episode.EpisodeID,
			)
		}
		for _, candidate := range compiled.Candidates {
			if refs := episodeRefs[candidate.CandidateID]; len(refs) > 0 {
				candidate.EpisodeRefs = append([]string(nil), refs...)
				sort.Strings(candidate.EpisodeRefs)
				candidate.CandidateID = candidate.DeterministicID()
				if err := candidate.Validate(); err != nil {
					return report, err
				}
			}
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
