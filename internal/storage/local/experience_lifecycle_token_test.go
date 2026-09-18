package local

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestExperienceLifecycleTokenRoundTripTamperAndExpiry(t *testing.T) {
	store := openStorageTestStore(t)
	now := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	claims := storageExperienceLifecycleClaims(
		now,
		experience.LifecycleActionActivate,
		experience.LifecycleApproved,
	)
	token, err := store.IssueExperienceLifecycleToken(claims)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.DecodeExperienceLifecycleToken(token)
	if err != nil || got != claims {
		t.Fatalf("lifecycle token round trip = %+v/%v", got, err)
	}

	parts := strings.SplitN(token, ".", 2)
	tampered := "A" + parts[0][1:] + "." + parts[1]
	if _, err := store.DecodeExperienceLifecycleToken(tampered); !errors.Is(
		err,
		ErrExperienceLifecycleTokenInvalid,
	) {
		t.Fatalf("tampered lifecycle token error = %v", err)
	}

	store.clock = func() time.Time {
		return claims.ExpiresAt.Add(time.Nanosecond)
	}
	if _, err := store.DecodeExperienceLifecycleToken(token); !errors.Is(
		err,
		ErrExperienceLifecycleTokenExpired,
	) {
		t.Fatalf("expired lifecycle token error = %v", err)
	}
}

func storageExperienceLifecycleClaims(
	now time.Time,
	action experience.LifecycleAction,
	current experience.LifecycleState,
) experience.LifecycleActionTokenClaims {
	return experience.LifecycleActionTokenClaims{
		Version: experience.LifecycleActionTokenVersion,
		Action:  action,
		Experience: experience.ExperienceRef{
			ExperienceID: "exp_lifecycle",
			Version:      1,
		},
		ProjectIdentity:  storageProjectIdentity,
		CurrentLifecycle: current,
		ContentHash:      storageSHA256("lifecycle content"),
		IssuedAt:         now,
		ExpiresAt:        now.Add(experienceLifecycleTokenLifetime),
	}
}
