package experience

import (
	"errors"
	"time"
)

const LifecycleActionTokenVersion = "belay.experience-lifecycle-token.v1"

type LifecycleAction string

const (
	LifecycleActionActivate  LifecycleAction = "activate"
	LifecycleActionPause     LifecycleAction = "pause"
	LifecycleActionSupersede LifecycleAction = "supersede"
	LifecycleActionExpire    LifecycleAction = "expire"
)

func (value LifecycleAction) Valid() bool {
	switch value {
	case LifecycleActionActivate,
		LifecycleActionPause,
		LifecycleActionSupersede,
		LifecycleActionExpire:
		return true
	default:
		return false
	}
}

func (value LifecycleAction) States() (LifecycleState, LifecycleState, bool) {
	switch value {
	case LifecycleActionActivate:
		return LifecycleApproved, LifecycleActive, true
	case LifecycleActionPause:
		return LifecycleActive, LifecyclePaused, true
	default:
		return "", "", false
	}
}

func (value LifecycleAction) TransitionFrom(
	current LifecycleState,
) (LifecycleState, bool) {
	switch value {
	case LifecycleActionActivate:
		return LifecycleActive, current == LifecycleApproved
	case LifecycleActionPause:
		return LifecyclePaused, current == LifecycleActive
	case LifecycleActionSupersede:
		return LifecycleSuperseded,
			ValidLifecycleTransition(current, LifecycleSuperseded)
	case LifecycleActionExpire:
		return LifecycleExpired,
			ValidLifecycleTransition(current, LifecycleExpired)
	default:
		return "", false
	}
}

type LifecycleActionTokenClaims struct {
	Version          string          `json:"version"`
	Action           LifecycleAction `json:"action"`
	Experience       ExperienceRef   `json:"experience"`
	ProjectIdentity  string          `json:"project_identity"`
	CurrentLifecycle LifecycleState  `json:"current_lifecycle"`
	ContentHash      string          `json:"content_hash"`
	IssuedAt         time.Time       `json:"issued_at"`
	ExpiresAt        time.Time       `json:"expires_at"`
}

func (value LifecycleActionTokenClaims) Validate() error {
	if value.Version != LifecycleActionTokenVersion {
		return errors.New("experience lifecycle token version is invalid")
	}
	if err := value.Experience.Validate(); err != nil {
		return err
	}
	if err := validateIdentifier(
		"experience lifecycle project identity",
		value.ProjectIdentity,
	); err != nil {
		return err
	}
	if !value.Action.Valid() {
		return errors.New("experience lifecycle action is invalid")
	}
	if _, valid := value.Action.TransitionFrom(value.CurrentLifecycle); !valid {
		return errors.New("experience lifecycle action does not match current state")
	}
	if !validSHA256(value.ContentHash) {
		return errors.New("experience lifecycle content hash must be sha256")
	}
	if value.IssuedAt.IsZero() ||
		value.ExpiresAt.IsZero() ||
		!value.ExpiresAt.After(value.IssuedAt) {
		return errors.New("experience lifecycle token time window is invalid")
	}
	return nil
}
