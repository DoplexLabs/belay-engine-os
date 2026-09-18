package localapp

import (
	"context"
	"errors"
	"fmt"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/experience/impact"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	maxExperienceImpactBatch         = 128
	maxExperienceImpactTurns         = 10000
	maxExperienceImpactPriorSessions = 25
)

type ExperienceImpactRepository interface {
	QueryExperienceApplicationsNeedingImpact(
		context.Context,
		string,
		int,
	) ([]experience.Application, error)
	GetExperience(
		context.Context,
		experience.ExperienceRef,
	) (local.StoredExperience, error)
	GetExperienceApplicationEvaluation(
		context.Context,
		string,
	) (*experience.Evaluation, error)
	GetTranscriptSession(context.Context, string) (transcript.Session, error)
	QueryTranscriptTurns(context.Context, string, int) ([]transcript.Turn, error)
	QueryTranscriptSessions(
		context.Context,
		transcript.SessionQuery,
	) ([]transcript.Session, error)
	InsertExperienceImpactObservation(
		context.Context,
		impact.Observation,
	) (bool, error)
}

type ExperienceImpactReport struct {
	Attempted            int
	Inserted             int
	Replayed             int
	Deferred             int
	InsufficientBaseline int
}

type ExperienceImpactApplicationError struct {
	ApplicationID string
	Cause         error
}

func (value *ExperienceImpactApplicationError) Error() string {
	return fmt.Sprintf(
		"observe experience application %q: %s",
		value.ApplicationID,
		value.Cause,
	)
}

func (value *ExperienceImpactApplicationError) Unwrap() error {
	return value.Cause
}

type ExperienceImpactCoordinator struct {
	repository ExperienceImpactRepository
}

func NewExperienceImpactCoordinator(
	repository ExperienceImpactRepository,
) (*ExperienceImpactCoordinator, error) {
	if repository == nil {
		return nil, errors.New("experience impact requires a repository")
	}
	return &ExperienceImpactCoordinator{repository: repository}, nil
}

func (coordinator *ExperienceImpactCoordinator) Observe(
	ctx context.Context,
	limit int,
) (ExperienceImpactReport, error) {
	var report ExperienceImpactReport
	if ctx == nil {
		return report, errors.New("experience impact requires a context")
	}
	if coordinator == nil || coordinator.repository == nil {
		return report, errors.New("experience impact requires a repository")
	}
	if limit < 1 || limit > maxExperienceImpactBatch {
		return report, fmt.Errorf(
			"experience impact limit must be between 1 and %d",
			maxExperienceImpactBatch,
		)
	}
	applications, err := coordinator.repository.
		QueryExperienceApplicationsNeedingImpact(
			ctx,
			impact.DerivationVersion,
			limit,
		)
	if err != nil {
		return report, fmt.Errorf(
			"query experience applications needing impact: %w",
			err,
		)
	}
	if len(applications) > limit {
		applications = applications[:limit]
	}
	runErrors := make([]error, 0)
	for _, application := range applications {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(runErrors, err)...)
		}
		report.Attempted++
		observation, inserted, err := coordinator.observeApplication(
			ctx,
			application,
		)
		if err != nil {
			report.Deferred++
			runErrors = append(runErrors, &ExperienceImpactApplicationError{
				ApplicationID: application.ApplicationID,
				Cause:         err,
			})
			continue
		}
		if observation.Comparison.State ==
			impact.ComparisonInsufficientBaseline {
			report.InsufficientBaseline++
		}
		if inserted {
			report.Inserted++
		} else {
			report.Replayed++
		}
	}
	return report, errors.Join(runErrors...)
}

func (coordinator *ExperienceImpactCoordinator) observeApplication(
	ctx context.Context,
	application experience.Application,
) (impact.Observation, bool, error) {
	input, err := coordinator.buildImpactInput(ctx, application)
	if err != nil {
		return impact.Observation{}, false, err
	}
	observation, err := impact.Derive(input)
	if err != nil {
		return impact.Observation{}, false,
			fmt.Errorf("derive experience impact: %w", err)
	}
	inserted, err := coordinator.repository.InsertExperienceImpactObservation(
		ctx,
		observation,
	)
	if err != nil {
		return impact.Observation{}, false,
			fmt.Errorf("persist experience impact: %w", err)
	}
	return observation, inserted, nil
}

func (coordinator *ExperienceImpactCoordinator) buildImpactInput(
	ctx context.Context,
	application experience.Application,
) (impact.Input, error) {
	if err := application.Validate(); err != nil {
		return impact.Input{}, fmt.Errorf("invalid queued application: %w", err)
	}
	if application.DeliveryState != experience.DeliveryDelivered ||
		application.SessionKey == "" {
		return impact.Input{}, errors.New(
			"queued impact application is not delivered and session-bound",
		)
	}
	stored, err := coordinator.repository.GetExperience(
		ctx,
		application.Experience,
	)
	if err != nil {
		return impact.Input{}, fmt.Errorf("load exact experience: %w", err)
	}
	value := stored.Experience
	if value.ExperienceID != application.Experience.ExperienceID ||
		value.Version != application.Experience.Version ||
		value.Scope.ProjectIdentity != application.ProjectIdentity {
		return impact.Input{}, errors.New(
			"impact experience version does not match application",
		)
	}
	evaluation, err := coordinator.repository.GetExperienceApplicationEvaluation(
		ctx,
		application.ApplicationID,
	)
	if err != nil {
		return impact.Input{}, fmt.Errorf(
			"load experience application evaluation: %w",
			err,
		)
	}
	if evaluation == nil {
		return impact.Input{}, errors.New(
			"impact application has no deterministic evaluation",
		)
	}
	session, err := coordinator.repository.GetTranscriptSession(
		ctx,
		application.SessionKey,
	)
	if err != nil {
		return impact.Input{}, fmt.Errorf("load bound transcript session: %w", err)
	}
	if session.SessionKey != application.SessionKey ||
		session.ProjectIdentity != application.ProjectIdentity ||
		session.Coverage == transcript.CoverageLive ||
		session.EndedAt.IsZero() {
		return impact.Input{}, errors.New(
			"impact transcript session does not match a completed binding",
		)
	}
	turns, err := coordinator.repository.QueryTranscriptTurns(
		ctx,
		session.SessionKey,
		maxExperienceImpactTurns,
	)
	if err != nil {
		return impact.Input{}, fmt.Errorf("load bound transcript turns: %w", err)
	}
	if len(turns) == 0 {
		return impact.Input{}, errors.New(
			"impact transcript session has no retained turns",
		)
	}
	current := impact.SessionInput{
		Session:                 session,
		Turns:                   turns,
		WindowStartTurn:         impactWindowStart(turns, *evaluation),
		TaskOutcomeState:        application.TaskOutcomeState,
		OutcomeCoverageComplete: knownTaskOutcome(application.TaskOutcomeState),
	}
	priorSessions, err := coordinator.repository.QueryTranscriptSessions(
		ctx,
		transcript.SessionQuery{
			Agent:           session.Agent,
			ProjectIdentity: session.ProjectIdentity,
			Coverage:        transcript.CoverageComplete,
			Limit:           maxExperienceImpactPriorSessions,
		},
	)
	if err != nil {
		return impact.Input{}, fmt.Errorf(
			"query matched prior transcript sessions: %w",
			err,
		)
	}
	prior := make([]impact.SessionInput, 0, len(priorSessions))
	for _, candidate := range priorSessions {
		if candidate.SessionKey == session.SessionKey ||
			candidate.EndedAt.IsZero() ||
			!candidate.EndedAt.Before(session.StartedAt) {
			continue
		}
		candidateTurns, err := coordinator.repository.QueryTranscriptTurns(
			ctx,
			candidate.SessionKey,
			maxExperienceImpactTurns,
		)
		if err != nil || len(candidateTurns) == 0 {
			continue
		}
		prior = append(prior, impact.SessionInput{
			Session:          candidate,
			Turns:            candidateTurns,
			WindowStartTurn:  candidateTurns[0].TurnIndex,
			TaskOutcomeState: experience.TaskOutcomeUnknown,
		})
	}
	return impact.Input{
		Application:   application,
		Evaluation:    evaluation,
		Current:       current,
		Prior:         prior,
		ProjectConfig: loadProjectConfig(session.ProjectPath),
	}, nil
}

func impactWindowStart(
	turns []transcript.Turn,
	evaluation experience.Evaluation,
) int64 {
	result := turns[0].TurnIndex
	last := turns[len(turns)-1].TurnIndex
	sessionKey := turns[0].SessionKey
	found := false
	for _, ref := range evaluation.SourceEvidence {
		if ref.Kind != experience.EvidenceTranscriptTurn ||
			ref.SessionKey != sessionKey ||
			ref.TurnIndex == nil ||
			*ref.TurnIndex < result ||
			*ref.TurnIndex > last {
			continue
		}
		if !found || *ref.TurnIndex < result {
			result = *ref.TurnIndex
			found = true
		}
	}
	return result
}

func knownTaskOutcome(value experience.TaskOutcomeState) bool {
	return value == experience.TaskOutcomeSucceeded ||
		value == experience.TaskOutcomeFailed
}
