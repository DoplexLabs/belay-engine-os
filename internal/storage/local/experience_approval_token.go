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
	maxExperienceApprovalTokenBytes = 4096
	experienceApprovalTokenLifetime = 15 * time.Minute
)

var (
	ErrExperienceApprovalTokenInvalid = errors.New(
		"experience approval token is invalid",
	)
	ErrExperienceApprovalTokenExpired = errors.New(
		"experience approval token has expired",
	)
)

type experienceApprovalTokenPayload struct {
	Version             string `json:"v"`
	CandidateID         string `json:"c"`
	ProposalID          string `json:"p"`
	ProjectIdentity     string `json:"j"`
	SemanticInputHash   string `json:"i"`
	ProposedContentHash string `json:"h"`
	EvidenceGeneration  string `json:"g"`
	IssuedAt            string `json:"a"`
	ExpiresAt           string `json:"e"`
}

func (s *Store) IssueExperienceApprovalToken(
	claims experience.ApprovalTokenClaims,
) (string, error) {
	claims.IssuedAt = claims.IssuedAt.UTC()
	claims.ExpiresAt = claims.ExpiresAt.UTC()
	if !validExperienceApprovalClaims(claims) {
		return "", ErrExperienceApprovalTokenInvalid
	}
	body, err := json.Marshal(experienceApprovalTokenPayload{
		Version:             claims.Version,
		CandidateID:         claims.CandidateID,
		ProposalID:          claims.ProposalID,
		ProjectIdentity:     claims.ProjectIdentity,
		SemanticInputHash:   claims.SemanticInputHash,
		ProposedContentHash: claims.ProposedContentHash,
		EvidenceGeneration:  claims.EvidenceGeneration,
		IssuedAt:            formatProjectionTime(claims.IssuedAt),
		ExpiresAt:           formatProjectionTime(claims.ExpiresAt),
	})
	if err != nil {
		return "", errors.New("encode experience approval token")
	}
	key, err := s.derivedKey(experienceApprovalTokenKeyDomain)
	if err != nil {
		return "", err
	}
	defer zeroBytes(key)
	signature := opaqueDigest(key, body)
	return base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *Store) DecodeExperienceApprovalToken(
	token string,
) (experience.ApprovalTokenClaims, error) {
	if token == "" ||
		len(token) > maxExperienceApprovalTokenBytes ||
		strings.Count(token, ".") != 1 {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenInvalid
	}
	parts := strings.SplitN(token, ".", 2)
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil ||
		len(body) == 0 ||
		len(body) > maxExperienceApprovalTokenBytes {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenInvalid
	}
	key, err := s.derivedKey(experienceApprovalTokenKeyDomain)
	if err != nil {
		return experience.ApprovalTokenClaims{}, err
	}
	expected := opaqueDigest(key, body)
	zeroBytes(key)
	if !hmac.Equal(signature, expected) {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var payload experienceApprovalTokenPayload
	if err := decoder.Decode(&payload); err != nil {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenInvalid
	}
	issuedAt, err := time.Parse(projectionTimestampLayout, payload.IssuedAt)
	if err != nil {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenInvalid
	}
	expiresAt, err := time.Parse(projectionTimestampLayout, payload.ExpiresAt)
	if err != nil {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenInvalid
	}
	claims := experience.ApprovalTokenClaims{
		Version:             payload.Version,
		CandidateID:         payload.CandidateID,
		ProposalID:          payload.ProposalID,
		ProjectIdentity:     payload.ProjectIdentity,
		SemanticInputHash:   payload.SemanticInputHash,
		ProposedContentHash: payload.ProposedContentHash,
		EvidenceGeneration:  payload.EvidenceGeneration,
		IssuedAt:            issuedAt,
		ExpiresAt:           expiresAt,
	}
	if !validExperienceApprovalClaims(claims) {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenInvalid
	}
	if s.nowUTC().After(claims.ExpiresAt) {
		return experience.ApprovalTokenClaims{},
			ErrExperienceApprovalTokenExpired
	}
	return claims, nil
}

func validExperienceApprovalClaims(
	claims experience.ApprovalTokenClaims,
) bool {
	return claims.Validate() == nil &&
		claims.ExpiresAt.Sub(claims.IssuedAt) <=
			experienceApprovalTokenLifetime
}
