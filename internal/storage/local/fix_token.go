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

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const maxFixActionTokenBytes = 2048

var (
	ErrFixActionTokenInvalid = errors.New("fix action token is invalid")
	ErrFixActionTokenExpired = errors.New("fix action token has expired")
)

type fixActionTokenPayload struct {
	Version             string `json:"v"`
	CursorEpoch         string `json:"x"`
	IssueID             string `json:"i"`
	Snapshot            int64  `json:"s"`
	RetentionGeneration int64  `json:"r"`
	IssuedAt            string `json:"a"`
	ExpiresAt           string `json:"e"`
}

type fixActionTokenPayloadV1 struct {
	Version   string `json:"v"`
	IssueID   string `json:"i"`
	Snapshot  int64  `json:"s"`
	IssuedAt  string `json:"a"`
	ExpiresAt string `json:"e"`
}

func (s *Store) IssueFixActionToken(claims model.FixActionClaims) (string, error) {
	claims.IssuedAt = claims.IssuedAt.UTC()
	claims.ExpiresAt = claims.ExpiresAt.UTC()
	if !validFixActionClaims(claims) {
		return "", ErrFixActionTokenInvalid
	}
	epoch, err := s.currentIssueCursorEpoch()
	if err != nil {
		return "", err
	}
	if claims.CursorEpoch != epoch {
		return "", ErrFixActionTokenExpired
	}
	body, err := json.Marshal(fixActionTokenPayload{
		Version:             claims.Version,
		CursorEpoch:         claims.CursorEpoch,
		IssueID:             claims.IssueID,
		Snapshot:            claims.Snapshot,
		RetentionGeneration: claims.RetentionGeneration,
		IssuedAt:            formatProjectionTime(claims.IssuedAt),
		ExpiresAt:           formatProjectionTime(claims.ExpiresAt),
	})
	if err != nil {
		return "", errors.New("encode fix action token")
	}
	key, err := s.derivedKey(fixActionTokenKeyDomain)
	if err != nil {
		return "", err
	}
	defer zeroBytes(key)
	signature := opaqueDigest(key, body)
	return base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *Store) DecodeFixActionToken(token string) (model.FixActionClaims, error) {
	if token == "" || len(token) > maxFixActionTokenBytes || strings.Count(token, ".") != 1 {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}
	parts := strings.SplitN(token, ".", 2)
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(body) == 0 || len(body) > maxFixActionTokenBytes {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}
	key, err := s.derivedKey(fixActionTokenKeyDomain)
	if err != nil {
		return model.FixActionClaims{}, err
	}
	expected := opaqueDigest(key, body)
	zeroBytes(key)
	if !hmac.Equal(signature, expected) {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}

	var version struct {
		Version string `json:"v"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}
	if version.Version == model.FixActionTokenVersionV1 {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		var legacy fixActionTokenPayloadV1
		if err := decoder.Decode(&legacy); err != nil {
			return model.FixActionClaims{}, ErrFixActionTokenInvalid
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return model.FixActionClaims{}, ErrFixActionTokenInvalid
		}
		issuedAt, err := time.Parse(projectionTimestampLayout, legacy.IssuedAt)
		if err != nil {
			return model.FixActionClaims{}, ErrFixActionTokenInvalid
		}
		expiresAt, err := time.Parse(projectionTimestampLayout, legacy.ExpiresAt)
		if err != nil ||
			!validIssueID(legacy.IssueID) ||
			legacy.Snapshot < 1 ||
			!expiresAt.After(issuedAt) ||
			expiresAt.Sub(issuedAt) > issueCursorLifetime {
			return model.FixActionClaims{}, ErrFixActionTokenInvalid
		}
		return model.FixActionClaims{}, ErrFixActionTokenExpired
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var payload fixActionTokenPayload
	if err := decoder.Decode(&payload); err != nil {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}
	issuedAt, err := time.Parse(projectionTimestampLayout, payload.IssuedAt)
	if err != nil {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}
	expiresAt, err := time.Parse(projectionTimestampLayout, payload.ExpiresAt)
	if err != nil {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}
	claims := model.FixActionClaims{
		Version:             payload.Version,
		CursorEpoch:         payload.CursorEpoch,
		IssueID:             payload.IssueID,
		Snapshot:            payload.Snapshot,
		RetentionGeneration: payload.RetentionGeneration,
		IssuedAt:            issuedAt,
		ExpiresAt:           expiresAt,
	}
	if !validFixActionClaims(claims) {
		return model.FixActionClaims{}, ErrFixActionTokenInvalid
	}
	epoch, err := s.currentIssueCursorEpoch()
	if err != nil {
		return model.FixActionClaims{}, err
	}
	if claims.CursorEpoch != epoch {
		return model.FixActionClaims{}, ErrFixActionTokenExpired
	}
	return claims, nil
}

func validFixActionClaims(claims model.FixActionClaims) bool {
	return claims.Version == model.FixActionTokenVersion &&
		claims.CursorEpoch != "" &&
		validIssueID(claims.IssueID) &&
		claims.Snapshot > 0 &&
		claims.RetentionGeneration > 0 &&
		!claims.IssuedAt.IsZero() &&
		!claims.ExpiresAt.IsZero() &&
		claims.ExpiresAt.After(claims.IssuedAt) &&
		claims.ExpiresAt.Sub(claims.IssuedAt) <= issueCursorLifetime
}
