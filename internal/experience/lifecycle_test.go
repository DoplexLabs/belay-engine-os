package experience

import (
	"testing"
	"time"
)

func TestLifecycleActionTransitionPolicy(t *testing.T) {
	tests := []struct {
		name    string
		action  LifecycleAction
		current LifecycleState
		target  LifecycleState
		valid   bool
	}{
		{"activate approved", LifecycleActionActivate, LifecycleApproved, LifecycleActive, true},
		{"activate paused", LifecycleActionActivate, LifecyclePaused, LifecycleActive, false},
		{"pause active", LifecycleActionPause, LifecycleActive, LifecyclePaused, true},
		{"pause approved", LifecycleActionPause, LifecycleApproved, LifecyclePaused, false},
		{"supersede approved", LifecycleActionSupersede, LifecycleApproved, LifecycleSuperseded, true},
		{"supersede active", LifecycleActionSupersede, LifecycleActive, LifecycleSuperseded, true},
		{"supersede paused", LifecycleActionSupersede, LifecyclePaused, LifecycleSuperseded, true},
		{"supersede contradicted", LifecycleActionSupersede, LifecycleContradicted, LifecycleSuperseded, true},
		{"expire approved", LifecycleActionExpire, LifecycleApproved, LifecycleExpired, true},
		{"expire active", LifecycleActionExpire, LifecycleActive, LifecycleExpired, true},
		{"expire paused", LifecycleActionExpire, LifecyclePaused, LifecycleExpired, true},
		{"expire contradicted", LifecycleActionExpire, LifecycleContradicted, LifecycleExpired, false},
		{"supersede terminal", LifecycleActionSupersede, LifecycleSuperseded, LifecycleSuperseded, false},
		{"expire terminal", LifecycleActionExpire, LifecycleExpired, LifecycleExpired, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, valid := test.action.TransitionFrom(test.current)
			if target != test.target || valid != test.valid {
				t.Fatalf(
					"TransitionFrom(%s) = %s, %t; want %s, %t",
					test.current,
					target,
					valid,
					test.target,
					test.valid,
				)
			}
		})
	}
}

func TestLifecycleActionTokenClaimsAcceptDynamicSourceStates(t *testing.T) {
	now := time.Date(2026, 9, 10, 23, 45, 0, 0, time.UTC)
	claims := LifecycleActionTokenClaims{
		Version: LifecycleActionTokenVersion,
		Action:  LifecycleActionSupersede,
		Experience: ExperienceRef{
			ExperienceID: "exp_lifecycle",
			Version:      1,
		},
		ProjectIdentity:  "git@example.test:doplexlabs/belay-engine.git",
		CurrentLifecycle: LifecycleContradicted,
		ContentHash:      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IssuedAt:         now,
		ExpiresAt:        now.Add(15 * time.Minute),
	}
	if err := claims.Validate(); err != nil {
		t.Fatalf("supersede contradicted claims error = %v", err)
	}
	claims.Action = LifecycleActionExpire
	if err := claims.Validate(); err == nil {
		t.Fatal("expire contradicted claims unexpectedly valid")
	}
}
