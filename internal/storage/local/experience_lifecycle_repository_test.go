package local

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestExperienceLifecycleActionActivatesThenPauses(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 20, 30, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	candidate := storageExperienceCandidate("lifecycle activate pause")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleApproved)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}

	activateClaims := storageExperienceLifecycleClaims(
		now.Add(-time.Minute),
		experience.LifecycleActionActivate,
		experience.LifecycleApproved,
	)
	activateClaims.Experience = ref
	activateClaims.ProjectIdentity = value.Scope.ProjectIdentity
	activateClaims.ContentHash = value.ContentHash
	activated, err := store.ApplyExperienceLifecycleAction(
		ctx,
		ExperienceLifecycleActionInput{
			Claims:     activateClaims,
			ActorID:    "local_user",
			OccurredAt: now,
		},
	)
	if err != nil ||
		activated.Transition.FromState != experience.LifecycleApproved ||
		activated.Transition.ToState != experience.LifecycleActive ||
		activated.Transition.ReasonCode != "user_activated" {
		t.Fatalf("activate result = %+v/%v", activated, err)
	}
	stored, err := store.GetExperience(ctx, ref)
	if err != nil ||
		stored.CurrentLifecycle != experience.LifecycleActive ||
		stored.ActivatedAt == nil ||
		!stored.ActivatedAt.Equal(now) {
		t.Fatalf("active experience = %+v/%v", stored, err)
	}

	now = now.Add(time.Minute)
	pauseClaims := storageExperienceLifecycleClaims(
		now.Add(-time.Minute),
		experience.LifecycleActionPause,
		experience.LifecycleActive,
	)
	pauseClaims.Experience = ref
	pauseClaims.ProjectIdentity = value.Scope.ProjectIdentity
	pauseClaims.ContentHash = value.ContentHash
	paused, err := store.ApplyExperienceLifecycleAction(
		ctx,
		ExperienceLifecycleActionInput{
			Claims:     pauseClaims,
			ActorID:    "local_user",
			OccurredAt: now,
		},
	)
	if err != nil ||
		paused.Transition.FromState != experience.LifecycleActive ||
		paused.Transition.ToState != experience.LifecyclePaused ||
		paused.Transition.ReasonCode != "user_paused" {
		t.Fatalf("pause result = %+v/%v", paused, err)
	}
	stored, err = store.GetExperience(ctx, ref)
	if err != nil ||
		stored.CurrentLifecycle != experience.LifecyclePaused ||
		stored.ActivatedAt != nil {
		t.Fatalf("paused experience = %+v/%v", stored, err)
	}
	transitions, err := store.ListExperienceTransitions(ctx, ref, 10)
	if err != nil ||
		len(transitions) != 2 ||
		transitions[0].TransitionID !=
			activated.Transition.TransitionID ||
		transitions[1].TransitionID != paused.Transition.TransitionID {
		t.Fatalf("lifecycle transitions = %+v/%v", transitions, err)
	}
}

func TestExperienceLifecycleActionRejectsStaleBoundState(t *testing.T) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 21, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	candidate := storageExperienceCandidate("stale lifecycle")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleApproved)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	claims := storageExperienceLifecycleClaims(
		now.Add(-time.Minute),
		experience.LifecycleActionActivate,
		experience.LifecycleApproved,
	)
	claims.Experience = ref
	claims.ProjectIdentity = value.Scope.ProjectIdentity
	claims.ContentHash = value.ContentHash

	for name, mutate := range map[string]func(
		*experience.LifecycleActionTokenClaims,
	){
		"project": func(value *experience.LifecycleActionTokenClaims) {
			value.ProjectIdentity = "git@example.test:doplexlabs/other.git"
		},
		"content": func(value *experience.LifecycleActionTokenClaims) {
			value.ContentHash = storageSHA256("stale content")
		},
		"version": func(value *experience.LifecycleActionTokenClaims) {
			value.Experience.Version++
		},
	} {
		t.Run(name, func(t *testing.T) {
			stale := claims
			mutate(&stale)
			if _, err := store.ApplyExperienceLifecycleAction(
				ctx,
				ExperienceLifecycleActionInput{
					Claims:     stale,
					ActorID:    "local_user",
					OccurredAt: now,
				},
			); !errors.Is(err, ErrExperienceLifecycleActionStale) {
				t.Fatalf("stale %s error = %v", name, err)
			}
		})
	}

	if _, err := store.ApplyExperienceLifecycleAction(
		ctx,
		ExperienceLifecycleActionInput{
			Claims:     claims,
			ActorID:    "local_user",
			OccurredAt: now,
		},
	); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := store.ApplyExperienceLifecycleAction(
		ctx,
		ExperienceLifecycleActionInput{
			Claims:     claims,
			ActorID:    "local_user",
			OccurredAt: now,
		},
	); !errors.Is(err, ErrExperienceLifecycleActionStale) {
		t.Fatalf("reused stale lifecycle token error = %v", err)
	}
	transitions, err := store.ListExperienceTransitions(ctx, ref, 10)
	if err != nil || len(transitions) != 1 {
		t.Fatalf("transitions after stale actions = %+v/%v", transitions, err)
	}
}

func TestExperienceLifecycleActionSupersedesAndExpiresAllowedStates(
	t *testing.T,
) {
	tests := []struct {
		name       string
		action     experience.LifecycleAction
		current    experience.LifecycleState
		target     experience.LifecycleState
		reasonCode string
	}{
		{
			name:       "supersede approved",
			action:     experience.LifecycleActionSupersede,
			current:    experience.LifecycleApproved,
			target:     experience.LifecycleSuperseded,
			reasonCode: "user_superseded",
		},
		{
			name:       "supersede active",
			action:     experience.LifecycleActionSupersede,
			current:    experience.LifecycleActive,
			target:     experience.LifecycleSuperseded,
			reasonCode: "user_superseded",
		},
		{
			name:       "supersede paused",
			action:     experience.LifecycleActionSupersede,
			current:    experience.LifecyclePaused,
			target:     experience.LifecycleSuperseded,
			reasonCode: "user_superseded",
		},
		{
			name:       "supersede contradicted",
			action:     experience.LifecycleActionSupersede,
			current:    experience.LifecycleContradicted,
			target:     experience.LifecycleSuperseded,
			reasonCode: "user_superseded",
		},
		{
			name:       "expire approved",
			action:     experience.LifecycleActionExpire,
			current:    experience.LifecycleApproved,
			target:     experience.LifecycleExpired,
			reasonCode: "user_expired",
		},
		{
			name:       "expire active",
			action:     experience.LifecycleActionExpire,
			current:    experience.LifecycleActive,
			target:     experience.LifecycleExpired,
			reasonCode: "user_expired",
		},
		{
			name:       "expire paused",
			action:     experience.LifecycleActionExpire,
			current:    experience.LifecyclePaused,
			target:     experience.LifecycleExpired,
			reasonCode: "user_expired",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := openStorageTestStore(t)
			now := time.Date(
				2026,
				9,
				10,
				23,
				0,
				0,
				0,
				time.UTC,
			)
			store.clock = func() time.Time { return now }
			candidate := storageExperienceCandidate(test.name)
			if _, err := store.InsertExperienceCandidate(
				ctx,
				candidate,
			); err != nil {
				t.Fatal(err)
			}
			value := storageExperience(candidate, test.current)
			if _, err := store.InsertExperience(ctx, value); err != nil {
				t.Fatal(err)
			}
			ref := experience.ExperienceRef{
				ExperienceID: value.ExperienceID,
				Version:      value.Version,
			}
			claims := storageExperienceLifecycleClaims(
				now.Add(-time.Minute),
				test.action,
				test.current,
			)
			claims.Experience = ref
			claims.ProjectIdentity = value.Scope.ProjectIdentity
			claims.ContentHash = value.ContentHash
			result, err := store.ApplyExperienceLifecycleAction(
				ctx,
				ExperienceLifecycleActionInput{
					Claims:     claims,
					ActorID:    "local_user",
					OccurredAt: now,
				},
			)
			if err != nil ||
				result.Transition.FromState != test.current ||
				result.Transition.ToState != test.target ||
				result.Transition.ReasonCode != test.reasonCode ||
				result.Transition.ActorKind != experience.ActorUser ||
				result.Transition.ActorID != "local_user" {
				t.Fatalf("terminal action result = %+v/%v", result, err)
			}
			stored, err := store.GetExperience(ctx, ref)
			if err != nil ||
				stored.CurrentLifecycle != test.target ||
				stored.ActivatedAt != nil {
				t.Fatalf("terminal experience = %+v/%v", stored, err)
			}
		})
	}
}

func TestExperienceLifecycleActionRejectsInvalidAndReusedTerminalToken(
	t *testing.T,
) {
	ctx := context.Background()
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 23, 30, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	candidate := storageExperienceCandidate("terminal token rejection")
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	value := storageExperience(candidate, experience.LifecycleContradicted)
	if _, err := store.InsertExperience(ctx, value); err != nil {
		t.Fatal(err)
	}
	ref := experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
	expireClaims := storageExperienceLifecycleClaims(
		now.Add(-time.Minute),
		experience.LifecycleActionExpire,
		experience.LifecycleContradicted,
	)
	expireClaims.Experience = ref
	expireClaims.ProjectIdentity = value.Scope.ProjectIdentity
	expireClaims.ContentHash = value.ContentHash
	if _, err := store.ApplyExperienceLifecycleAction(
		ctx,
		ExperienceLifecycleActionInput{
			Claims:     expireClaims,
			ActorID:    "local_user",
			OccurredAt: now,
		},
	); !errors.Is(err, ErrExperienceLifecycleTokenInvalid) {
		t.Fatalf("contradicted expiration error = %v", err)
	}

	supersedeClaims := expireClaims
	supersedeClaims.Action = experience.LifecycleActionSupersede
	if _, err := store.ApplyExperienceLifecycleAction(
		ctx,
		ExperienceLifecycleActionInput{
			Claims:     supersedeClaims,
			ActorID:    "local_user",
			OccurredAt: now,
		},
	); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := store.ApplyExperienceLifecycleAction(
		ctx,
		ExperienceLifecycleActionInput{
			Claims:     supersedeClaims,
			ActorID:    "local_user",
			OccurredAt: now,
		},
	); !errors.Is(err, ErrExperienceLifecycleActionStale) {
		t.Fatalf("reused supersede token error = %v", err)
	}
}
