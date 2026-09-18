package localapp

import (
	"context"
	"errors"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const maxExperienceConflictQuery = 500

type ExperienceConflictPreflightStore interface {
	QueryActiveExperiences(
		context.Context,
		string,
		int,
	) ([]local.StoredExperience, error)
}

// PreflightExperienceConflict reads the bounded current experience projection
// and delegates to the pure advisory classifier. It performs no writes and
// does not alter proposal authority or lifecycle state.
func PreflightExperienceConflict(
	ctx context.Context,
	store ExperienceConflictPreflightStore,
	proposal experience.SemanticProposal,
) (experience.ConflictAdvisory, error) {
	if store == nil {
		return experience.ConflictAdvisory{}, errors.New(
			"experience conflict preflight requires a store",
		)
	}
	if err := proposal.Validate(); err != nil {
		return experience.ConflictAdvisory{}, err
	}
	stored, err := store.QueryActiveExperiences(
		ctx,
		proposal.ProjectIdentity,
		maxExperienceConflictQuery,
	)
	if err != nil {
		return experience.ConflictAdvisory{}, err
	}
	active := make([]experience.Experience, 0, len(stored))
	for _, value := range stored {
		if value.Experience.Scope.ProjectIdentity != proposal.ProjectIdentity {
			return experience.ConflictAdvisory{}, errors.New(
				"experience conflict query returned another project",
			)
		}
		if value.CurrentLifecycle != experience.LifecycleActive {
			return experience.ConflictAdvisory{}, errors.New(
				"active experience query returned an inactive experience",
			)
		}
		active = append(active, value.Experience)
	}
	result, err := experience.PreflightSemanticProposalConflict(
		proposal,
		active,
	)
	if err != nil {
		return experience.ConflictAdvisory{}, err
	}
	if len(stored) >= maxExperienceConflictQuery {
		return experience.MarkConflictActiveQueryCapReached(result)
	}
	return result, nil
}
