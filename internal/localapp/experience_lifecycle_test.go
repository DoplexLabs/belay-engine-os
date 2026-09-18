package localapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type experienceLifecycleServiceTestStore struct {
	stored      local.StoredExperience
	issued      experience.LifecycleActionTokenClaims
	decoded     experience.LifecycleActionTokenClaims
	applied     local.ExperienceLifecycleActionInput
	decodeError error
}

func (store *experienceLifecycleServiceTestStore) GetExperience(
	_ context.Context,
	_ experience.ExperienceRef,
) (local.StoredExperience, error) {
	return store.stored, nil
}

func (store *experienceLifecycleServiceTestStore) IssueExperienceLifecycleToken(
	claims experience.LifecycleActionTokenClaims,
) (string, error) {
	store.issued = claims
	return "signed-lifecycle-action", nil
}

func (store *experienceLifecycleServiceTestStore) DecodeExperienceLifecycleToken(
	_ string,
) (experience.LifecycleActionTokenClaims, error) {
	return store.decoded, store.decodeError
}

func (store *experienceLifecycleServiceTestStore) ApplyExperienceLifecycleAction(
	_ context.Context,
	input local.ExperienceLifecycleActionInput,
) (local.ExperienceLifecycleActionResult, error) {
	store.applied = input
	toState, _ := input.Claims.Action.TransitionFrom(
		input.Claims.CurrentLifecycle,
	)
	return local.ExperienceLifecycleActionResult{
		Transition: experience.LifecycleTransition{
			Experience: input.Claims.Experience,
			FromState:  input.Claims.CurrentLifecycle,
			ToState:    toState,
		},
	}, nil
}

func TestExperienceLifecycleServiceSupersedeAndExpire(t *testing.T) {
	now := time.Date(2026, 9, 10, 22, 30, 0, 0, time.UTC)
	value := localappConflictExperience(t, "lifecycle terminal actions")
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	tests := []struct {
		name    string
		action  experience.LifecycleAction
		current experience.LifecycleState
		apply   func(
			*ExperienceLifecycleService,
			context.Context,
			experience.ExperienceRef,
			string,
			string,
		) (local.ExperienceLifecycleActionResult, error)
		target experience.LifecycleState
	}{
		{
			name:    "supersede approved",
			action:  experience.LifecycleActionSupersede,
			current: experience.LifecycleApproved,
			apply:   (*ExperienceLifecycleService).Supersede,
			target:  experience.LifecycleSuperseded,
		},
		{
			name:    "supersede active",
			action:  experience.LifecycleActionSupersede,
			current: experience.LifecycleActive,
			apply:   (*ExperienceLifecycleService).Supersede,
			target:  experience.LifecycleSuperseded,
		},
		{
			name:    "supersede paused",
			action:  experience.LifecycleActionSupersede,
			current: experience.LifecyclePaused,
			apply:   (*ExperienceLifecycleService).Supersede,
			target:  experience.LifecycleSuperseded,
		},
		{
			name:    "supersede contradicted",
			action:  experience.LifecycleActionSupersede,
			current: experience.LifecycleContradicted,
			apply:   (*ExperienceLifecycleService).Supersede,
			target:  experience.LifecycleSuperseded,
		},
		{
			name:    "expire approved",
			action:  experience.LifecycleActionExpire,
			current: experience.LifecycleApproved,
			apply:   (*ExperienceLifecycleService).Expire,
			target:  experience.LifecycleExpired,
		},
		{
			name:    "expire active",
			action:  experience.LifecycleActionExpire,
			current: experience.LifecycleActive,
			apply:   (*ExperienceLifecycleService).Expire,
			target:  experience.LifecycleExpired,
		},
		{
			name:    "expire paused",
			action:  experience.LifecycleActionExpire,
			current: experience.LifecyclePaused,
			apply:   (*ExperienceLifecycleService).Expire,
			target:  experience.LifecycleExpired,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &experienceLifecycleServiceTestStore{
				stored: local.StoredExperience{
					Experience:       value,
					CurrentLifecycle: test.current,
				},
			}
			service, err := NewExperienceLifecycleService(
				store,
				WithExperienceLifecycleClock(func() time.Time {
					return now
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := service.Prepare(
				context.Background(),
				ref,
				test.action,
			)
			if err != nil ||
				preview.CurrentLifecycle != test.current ||
				store.issued.CurrentLifecycle != test.current {
				t.Fatalf(
					"terminal preview/claims = %+v/%+v/%v",
					preview,
					store.issued,
					err,
				)
			}
			store.decoded = store.issued
			result, err := test.apply(
				service,
				context.Background(),
				ref,
				preview.ActionToken,
				"local_user",
			)
			if err != nil ||
				result.Transition.FromState != test.current ||
				result.Transition.ToState != test.target ||
				store.applied.ActorID != "local_user" {
				t.Fatalf(
					"terminal apply/input = %+v/%+v/%v",
					result,
					store.applied,
					err,
				)
			}
		})
	}
}

func TestExperienceLifecycleServiceRejectsInvalidTerminalActions(t *testing.T) {
	value := localappConflictExperience(t, "invalid lifecycle terminal actions")
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	store := &experienceLifecycleServiceTestStore{
		stored: local.StoredExperience{
			Experience:       value,
			CurrentLifecycle: experience.LifecycleContradicted,
		},
	}
	service, err := NewExperienceLifecycleService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Prepare(
		context.Background(),
		ref,
		experience.LifecycleActionExpire,
	); !errors.Is(err, ErrExperienceLifecycleStale) {
		t.Fatalf("contradicted expiration preview error = %v", err)
	}

	for _, terminal := range []experience.LifecycleState{
		experience.LifecycleSuperseded,
		experience.LifecycleExpired,
	} {
		store.stored.CurrentLifecycle = terminal
		for _, action := range []experience.LifecycleAction{
			experience.LifecycleActionSupersede,
			experience.LifecycleActionExpire,
		} {
			if _, err := service.Prepare(
				context.Background(),
				ref,
				action,
			); !errors.Is(err, ErrExperienceLifecycleStale) {
				t.Fatalf(
					"%s from %s preview error = %v",
					action,
					terminal,
					err,
				)
			}
		}
	}
}

func TestExperienceLifecycleServiceRejectsWrongActionAndMissingActor(
	t *testing.T,
) {
	value := localappConflictExperience(t, "lifecycle action binding")
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	claims := experience.LifecycleActionTokenClaims{
		Version:          experience.LifecycleActionTokenVersion,
		Action:           experience.LifecycleActionSupersede,
		Experience:       ref,
		ProjectIdentity:  value.Scope.ProjectIdentity,
		CurrentLifecycle: experience.LifecycleActive,
		ContentHash:      value.ContentHash,
		IssuedAt:         time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC),
		ExpiresAt:        time.Date(2026, 9, 10, 22, 15, 0, 0, time.UTC),
	}
	store := &experienceLifecycleServiceTestStore{decoded: claims}
	service, err := NewExperienceLifecycleService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Expire(
		context.Background(),
		ref,
		"signed-lifecycle-action",
		"local_user",
	); !errors.Is(err, ErrExperienceLifecycleInvalidRequest) {
		t.Fatalf("wrong-action token error = %v", err)
	}
	if _, err := service.Supersede(
		context.Background(),
		ref,
		"signed-lifecycle-action",
		" ",
	); !errors.Is(err, ErrExperienceLifecycleInvalidRequest) {
		t.Fatalf("missing actor error = %v", err)
	}
}

func TestExperienceLifecycleServiceBindsPreviewAndRoutesAction(t *testing.T) {
	now := time.Date(2026, 9, 10, 22, 0, 0, 0, time.UTC)
	value := localappConflictExperience(t, "lifecycle service")
	value.Governance.LifecycleState = experience.LifecycleApproved
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	store := &experienceLifecycleServiceTestStore{
		stored: local.StoredExperience{
			Experience:       value,
			CurrentLifecycle: experience.LifecycleApproved,
		},
	}
	service, err := NewExperienceLifecycleService(
		store,
		WithExperienceLifecycleClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.Prepare(
		context.Background(),
		ref,
		experience.LifecycleActionActivate,
	)
	if err != nil ||
		preview.ActionToken != "signed-lifecycle-action" ||
		store.issued.Experience != ref ||
		store.issued.ProjectIdentity != value.Scope.ProjectIdentity ||
		store.issued.CurrentLifecycle != experience.LifecycleApproved ||
		store.issued.ContentHash != value.ContentHash ||
		!store.issued.IssuedAt.Equal(now) ||
		!store.issued.ExpiresAt.Equal(
			now.Add(experienceLifecyclePreviewLifetime),
		) {
		t.Fatalf("lifecycle preview/claims = %+v/%+v/%v", preview, store.issued, err)
	}

	store.decoded = store.issued
	result, err := service.Activate(
		context.Background(),
		ref,
		preview.ActionToken,
		"local_user",
	)
	if err != nil ||
		result.Transition.ToState != experience.LifecycleActive ||
		store.applied.Claims != store.issued ||
		store.applied.ActorID != "local_user" ||
		!store.applied.OccurredAt.Equal(now) {
		t.Fatalf("lifecycle apply/input = %+v/%+v/%v", result, store.applied, err)
	}

	if _, err := service.Pause(
		context.Background(),
		ref,
		preview.ActionToken,
		"local_user",
	); !errors.Is(err, ErrExperienceLifecycleInvalidRequest) {
		t.Fatalf("cross-action token error = %v", err)
	}
}

func TestExperienceLifecycleServiceRejectsExpiredTokenAndWrongState(
	t *testing.T,
) {
	value := localappConflictExperience(t, "lifecycle errors")
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	store := &experienceLifecycleServiceTestStore{
		stored: local.StoredExperience{
			Experience:       value,
			CurrentLifecycle: experience.LifecycleActive,
		},
		decodeError: local.ErrExperienceLifecycleTokenExpired,
	}
	service, err := NewExperienceLifecycleService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Activate(
		context.Background(),
		ref,
		"expired",
		"local_user",
	); !errors.Is(err, ErrExperienceLifecycleExpired) {
		t.Fatalf("expired lifecycle error = %v", err)
	}
	if _, err := service.Prepare(
		context.Background(),
		ref,
		experience.LifecycleActionActivate,
	); !errors.Is(err, ErrExperienceLifecycleStale) {
		t.Fatalf("wrong-state activation preview error = %v", err)
	}
}
