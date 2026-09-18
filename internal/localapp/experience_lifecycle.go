package localapp

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const experienceLifecyclePreviewLifetime = 15 * time.Minute

var (
	ErrExperienceLifecycleInvalidRequest = errors.New(
		"experience lifecycle request is invalid",
	)
	ErrExperienceLifecycleExpired = errors.New(
		"experience lifecycle preview has expired",
	)
	ErrExperienceLifecycleStale = errors.New(
		"experience lifecycle preview is stale",
	)
)

type ExperienceLifecycleServiceStore interface {
	GetExperience(
		context.Context,
		experience.ExperienceRef,
	) (local.StoredExperience, error)
	IssueExperienceLifecycleToken(
		experience.LifecycleActionTokenClaims,
	) (string, error)
	DecodeExperienceLifecycleToken(
		string,
	) (experience.LifecycleActionTokenClaims, error)
	ApplyExperienceLifecycleAction(
		context.Context,
		local.ExperienceLifecycleActionInput,
	) (local.ExperienceLifecycleActionResult, error)
}

type ExperienceLifecycleService struct {
	store ExperienceLifecycleServiceStore
	now   func() time.Time
}

type ExperienceLifecycleServiceOption func(*ExperienceLifecycleService)

func WithExperienceLifecycleClock(
	clock func() time.Time,
) ExperienceLifecycleServiceOption {
	return func(service *ExperienceLifecycleService) {
		if clock != nil {
			service.now = clock
		}
	}
}

type ExperienceLifecyclePreview struct {
	Action           experience.LifecycleAction `json:"action"`
	Experience       experience.ExperienceRef   `json:"experience"`
	ProjectIdentity  string                     `json:"project_identity"`
	CurrentLifecycle experience.LifecycleState  `json:"current_lifecycle"`
	ContentHash      string                     `json:"content_hash"`
	ActionToken      string                     `json:"action_token"`
	IssuedAt         time.Time                  `json:"issued_at"`
	ExpiresAt        time.Time                  `json:"expires_at"`
}

func NewExperienceLifecycleService(
	store ExperienceLifecycleServiceStore,
	options ...ExperienceLifecycleServiceOption,
) (*ExperienceLifecycleService, error) {
	if store == nil {
		return nil, errors.New(
			"experience lifecycle service requires a store",
		)
	}
	service := &ExperienceLifecycleService{
		store: store,
		now:   time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func (service *ExperienceLifecycleService) Prepare(
	ctx context.Context,
	ref experience.ExperienceRef,
	action experience.LifecycleAction,
) (ExperienceLifecyclePreview, error) {
	if !action.Valid() {
		return ExperienceLifecyclePreview{},
			ErrExperienceLifecycleInvalidRequest
	}
	stored, err := service.store.GetExperience(ctx, ref)
	if err != nil {
		return ExperienceLifecyclePreview{},
			mapExperienceLifecycleError(err)
	}
	if stored.Experience.ExperienceID != ref.ExperienceID ||
		stored.Experience.Version != ref.Version ||
		!lifecycleActionAllowed(action, stored.CurrentLifecycle) {
		return ExperienceLifecyclePreview{}, ErrExperienceLifecycleStale
	}
	issuedAt := service.now().UTC().Round(0)
	expiresAt := issuedAt.Add(experienceLifecyclePreviewLifetime)
	claims := experience.LifecycleActionTokenClaims{
		Version:          experience.LifecycleActionTokenVersion,
		Action:           action,
		Experience:       ref,
		ProjectIdentity:  stored.Experience.Scope.ProjectIdentity,
		CurrentLifecycle: stored.CurrentLifecycle,
		ContentHash:      stored.Experience.ContentHash,
		IssuedAt:         issuedAt,
		ExpiresAt:        expiresAt,
	}
	token, err := service.store.IssueExperienceLifecycleToken(claims)
	if err != nil {
		return ExperienceLifecyclePreview{},
			mapExperienceLifecycleError(err)
	}
	return ExperienceLifecyclePreview{
		Action:           claims.Action,
		Experience:       claims.Experience,
		ProjectIdentity:  claims.ProjectIdentity,
		CurrentLifecycle: claims.CurrentLifecycle,
		ContentHash:      claims.ContentHash,
		ActionToken:      token,
		IssuedAt:         issuedAt,
		ExpiresAt:        expiresAt,
	}, nil
}

func (service *ExperienceLifecycleService) Activate(
	ctx context.Context,
	ref experience.ExperienceRef,
	actionToken string,
	actorID string,
) (local.ExperienceLifecycleActionResult, error) {
	return service.apply(
		ctx,
		ref,
		experience.LifecycleActionActivate,
		actionToken,
		actorID,
	)
}

func (service *ExperienceLifecycleService) Pause(
	ctx context.Context,
	ref experience.ExperienceRef,
	actionToken string,
	actorID string,
) (local.ExperienceLifecycleActionResult, error) {
	return service.apply(
		ctx,
		ref,
		experience.LifecycleActionPause,
		actionToken,
		actorID,
	)
}

func (service *ExperienceLifecycleService) Supersede(
	ctx context.Context,
	ref experience.ExperienceRef,
	actionToken string,
	actorID string,
) (local.ExperienceLifecycleActionResult, error) {
	return service.apply(
		ctx,
		ref,
		experience.LifecycleActionSupersede,
		actionToken,
		actorID,
	)
}

func (service *ExperienceLifecycleService) Expire(
	ctx context.Context,
	ref experience.ExperienceRef,
	actionToken string,
	actorID string,
) (local.ExperienceLifecycleActionResult, error) {
	return service.apply(
		ctx,
		ref,
		experience.LifecycleActionExpire,
		actionToken,
		actorID,
	)
}

func (service *ExperienceLifecycleService) apply(
	ctx context.Context,
	ref experience.ExperienceRef,
	action experience.LifecycleAction,
	actionToken string,
	actorID string,
) (local.ExperienceLifecycleActionResult, error) {
	actionToken = strings.TrimSpace(actionToken)
	actorID = strings.TrimSpace(actorID)
	if err := ref.Validate(); err != nil ||
		actionToken == "" ||
		actorID == "" {
		return local.ExperienceLifecycleActionResult{},
			ErrExperienceLifecycleInvalidRequest
	}
	claims, err := service.store.DecodeExperienceLifecycleToken(actionToken)
	if err != nil {
		return local.ExperienceLifecycleActionResult{},
			mapExperienceLifecycleError(err)
	}
	if claims.Experience != ref || claims.Action != action {
		return local.ExperienceLifecycleActionResult{},
			ErrExperienceLifecycleInvalidRequest
	}
	result, err := service.store.ApplyExperienceLifecycleAction(
		ctx,
		local.ExperienceLifecycleActionInput{
			Claims:     claims,
			ActorID:    actorID,
			OccurredAt: service.now().UTC().Round(0),
		},
	)
	if err != nil {
		return local.ExperienceLifecycleActionResult{},
			mapExperienceLifecycleError(err)
	}
	return result, nil
}

func lifecycleActionAllowed(
	action experience.LifecycleAction,
	current experience.LifecycleState,
) bool {
	_, valid := action.TransitionFrom(current)
	return valid
}

func mapExperienceLifecycleError(err error) error {
	switch {
	case errors.Is(err, local.ErrExperienceLifecycleTokenInvalid):
		return ErrExperienceLifecycleInvalidRequest
	case errors.Is(err, local.ErrExperienceLifecycleTokenExpired):
		return ErrExperienceLifecycleExpired
	case errors.Is(err, local.ErrExperienceLifecycleActionStale),
		errors.Is(err, local.ErrExperienceLifecycleConflict):
		return ErrExperienceLifecycleStale
	default:
		return err
	}
}

var _ ExperienceLifecycleServiceStore = (*local.Store)(nil)
