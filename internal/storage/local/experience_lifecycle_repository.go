package local

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

var ErrExperienceLifecycleActionStale = errors.New(
	"experience lifecycle preview is stale",
)

type ExperienceLifecycleActionInput struct {
	Claims     experience.LifecycleActionTokenClaims
	ActorID    string
	OccurredAt time.Time
}

type ExperienceLifecycleActionResult struct {
	Transition experience.LifecycleTransition
}

// ApplyExperienceLifecycleAction rechecks every token-bound value and appends
// the encrypted lifecycle transition in one SQLite transaction. It does not
// compile or activate a Mission Pack generation.
func (s *Store) ApplyExperienceLifecycleAction(
	ctx context.Context,
	input ExperienceLifecycleActionInput,
) (ExperienceLifecycleActionResult, error) {
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.Claims.IssuedAt = input.Claims.IssuedAt.UTC()
	input.Claims.ExpiresAt = input.Claims.ExpiresAt.UTC()
	input.OccurredAt = input.OccurredAt.UTC()
	if !validExperienceLifecycleClaims(input.Claims) ||
		input.ActorID == "" ||
		len(input.ActorID) > 256 ||
		input.OccurredAt.IsZero() ||
		input.OccurredAt.Before(input.Claims.IssuedAt) ||
		input.OccurredAt.After(input.Claims.ExpiresAt) {
		return ExperienceLifecycleActionResult{},
			ErrExperienceLifecycleTokenInvalid
	}
	now := s.nowUTC()
	if now.After(input.Claims.ExpiresAt) {
		return ExperienceLifecycleActionResult{},
			ErrExperienceLifecycleTokenExpired
	}
	if input.OccurredAt.After(now) {
		return ExperienceLifecycleActionResult{},
			ErrExperienceLifecycleTokenInvalid
	}
	toState, valid := input.Claims.Action.TransitionFrom(
		input.Claims.CurrentLifecycle,
	)
	if !valid {
		return ExperienceLifecycleActionResult{},
			ErrExperienceLifecycleTokenInvalid
	}
	fromState := input.Claims.CurrentLifecycle

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExperienceLifecycleActionResult{},
			errors.New("begin experience lifecycle action")
	}
	defer tx.Rollback()
	var result ExperienceLifecycleActionResult
	err = withMutationTx(
		ctx,
		tx,
		mutationExperienceRegistry,
		func() error {
			transactionNow := s.nowUTC()
			if transactionNow.After(input.Claims.ExpiresAt) {
				return ErrExperienceLifecycleTokenExpired
			}
			if input.OccurredAt.After(transactionNow) {
				return ErrExperienceLifecycleTokenInvalid
			}
			stored, err := s.scanStoredExperience(tx.QueryRowContext(
				ctx,
				experienceSelectSQL+
					` WHERE value.experience_id = ? AND value.version = ?`,
				input.Claims.Experience.ExperienceID,
				input.Claims.Experience.Version,
			))
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrExperienceLifecycleActionStale
				}
				return err
			}
			if stored.Experience.ExperienceID !=
				input.Claims.Experience.ExperienceID ||
				stored.Experience.Version !=
					input.Claims.Experience.Version ||
				stored.Experience.Scope.ProjectIdentity !=
					input.Claims.ProjectIdentity ||
				stored.Experience.ContentHash != input.Claims.ContentHash ||
				stored.CurrentLifecycle !=
					input.Claims.CurrentLifecycle {
				return ErrExperienceLifecycleActionStale
			}

			reasonCode, valid := experienceLifecycleReasonCode(
				input.Claims.Action,
			)
			if !valid {
				return ErrExperienceLifecycleTokenInvalid
			}
			transition := experience.LifecycleTransition{
				Experience: input.Claims.Experience,
				FromState:  fromState,
				ToState:    toState,
				ReasonCode: reasonCode,
				ActorKind:  experience.ActorUser,
				ActorID:    input.ActorID,
				OccurredAt: input.OccurredAt,
			}
			transition.TransitionID = transition.DeterministicID()
			plaintext, payload, err := s.prepareExperienceTransition(
				transition,
			)
			if err != nil {
				return err
			}
			inserted, err := s.appendExperienceTransitionTx(
				ctx,
				tx,
				transition,
				plaintext,
				payload,
			)
			if err != nil {
				return err
			}
			if !inserted {
				return ErrExperienceLifecycleActionStale
			}
			result.Transition = transition
			return nil
		},
	)
	if err != nil {
		return ExperienceLifecycleActionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ExperienceLifecycleActionResult{},
			errors.New("commit experience lifecycle action")
	}
	return result, nil
}

func experienceLifecycleReasonCode(
	action experience.LifecycleAction,
) (string, bool) {
	switch action {
	case experience.LifecycleActionActivate:
		return "user_activated", true
	case experience.LifecycleActionPause:
		return "user_paused", true
	case experience.LifecycleActionSupersede:
		return "user_superseded", true
	case experience.LifecycleActionExpire:
		return "user_expired", true
	default:
		return "", false
	}
}
