package local

import (
	"bytes"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const (
	maxExperienceLifecycleTokenBytes = 4096
	experienceLifecycleTokenLifetime = 15 * time.Minute
)

var (
	ErrExperienceLifecycleTokenInvalid = errors.New(
		"experience lifecycle token is invalid",
	)
	ErrExperienceLifecycleTokenExpired = errors.New(
		"experience lifecycle token has expired",
	)
)

type experienceLifecycleTokenPayload struct {
	Version           string                     `json:"v"`
	Action            experience.LifecycleAction `json:"a"`
	ExperienceID      string                     `json:"x"`
	ExperienceVersion int                        `json:"n"`
	ProjectIdentity   string                     `json:"j"`
	CurrentLifecycle  experience.LifecycleState  `json:"s"`
	ContentHash       string                     `json:"h"`
	IssuedAt          string                     `json:"i"`
	ExpiresAt         string                     `json:"e"`
}

func (s *Store) IssueExperienceLifecycleToken(
	claims experience.LifecycleActionTokenClaims,
) (string, error) {
	claims.IssuedAt = claims.IssuedAt.UTC()
	claims.ExpiresAt = claims.ExpiresAt.UTC()
	if !validExperienceLifecycleClaims(claims) {
		return "", ErrExperienceLifecycleTokenInvalid
	}
	body, err := json.Marshal(experienceLifecycleTokenPayload{
		Version:           claims.Version,
		Action:            claims.Action,
		ExperienceID:      claims.Experience.ExperienceID,
		ExperienceVersion: claims.Experience.Version,
		ProjectIdentity:   claims.ProjectIdentity,
		CurrentLifecycle:  claims.CurrentLifecycle,
		ContentHash:       claims.ContentHash,
		IssuedAt:          formatProjectionTime(claims.IssuedAt),
		ExpiresAt:         formatProjectionTime(claims.ExpiresAt),
	})
	if err != nil {
		return "", errors.New("encode experience lifecycle token")
	}
	key, err := s.derivedKey(experienceLifecycleTokenKeyDomain)
	if err != nil {
		return "", err
	}
	defer zeroBytes(key)
	signature := opaqueDigest(key, body)
	return base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *Store) DecodeExperienceLifecycleToken(
	token string,
) (experience.LifecycleActionTokenClaims, error) {
	if token == "" ||
		len(token) > maxExperienceLifecycleTokenBytes ||
		strings.Count(token, ".") != 1 {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenInvalid
	}
	parts := strings.SplitN(token, ".", 2)
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil ||
		len(body) == 0 ||
		len(body) > maxExperienceLifecycleTokenBytes {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenInvalid
	}
	key, err := s.derivedKey(experienceLifecycleTokenKeyDomain)
	if err != nil {
		return experience.LifecycleActionTokenClaims{}, err
	}
	expected := opaqueDigest(key, body)
	zeroBytes(key)
	if !hmac.Equal(signature, expected) {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var payload experienceLifecycleTokenPayload
	if err := decoder.Decode(&payload); err != nil {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenInvalid
	}
	issuedAt, err := time.Parse(projectionTimestampLayout, payload.IssuedAt)
	if err != nil {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenInvalid
	}
	expiresAt, err := time.Parse(projectionTimestampLayout, payload.ExpiresAt)
	if err != nil {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenInvalid
	}
	claims := experience.LifecycleActionTokenClaims{
		Version: payload.Version,
		Action:  payload.Action,
		Experience: experience.ExperienceRef{
			ExperienceID: payload.ExperienceID,
			Version:      payload.ExperienceVersion,
		},
		ProjectIdentity:  payload.ProjectIdentity,
		CurrentLifecycle: payload.CurrentLifecycle,
		ContentHash:      payload.ContentHash,
		IssuedAt:         issuedAt,
		ExpiresAt:        expiresAt,
	}
	if !validExperienceLifecycleClaims(claims) {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenInvalid
	}
	if s.nowUTC().After(claims.ExpiresAt) {
		return experience.LifecycleActionTokenClaims{},
			ErrExperienceLifecycleTokenExpired
	}
	return claims, nil
}

func validExperienceLifecycleClaims(
	claims experience.LifecycleActionTokenClaims,
) bool {
	return claims.Validate() == nil &&
		claims.ExpiresAt.Sub(claims.IssuedAt) <=
			experienceLifecycleTokenLifetime
}
