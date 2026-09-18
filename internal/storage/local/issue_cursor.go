package local

import (
	"crypto/hmac"
	"encoding/base64"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

const maxIssueCursorBytes = 16 * 1024

func (s *Store) SealIssueCursor(payload []byte) (string, error) {
	if len(payload) == 0 || len(payload) > maxIssueCursorBytes {
		return "", model.ErrIssueCursorInvalid
	}
	key, err := s.derivedKey(issueCursorKeyDomain)
	if err != nil {
		return "", err
	}
	defer zeroBytes(key)
	signature := opaqueDigest(key, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *Store) OpenIssueCursor(value string) ([]byte, error) {
	if value == "" ||
		len(value) > maxIssueCursorBytes*2 ||
		strings.Count(value, ".") != 1 {
		return nil, model.ErrIssueCursorInvalid
	}
	parts := strings.SplitN(value, ".", 2)
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) == 0 || len(payload) > maxIssueCursorBytes {
		return nil, model.ErrIssueCursorInvalid
	}
	if base64.RawURLEncoding.EncodeToString(payload) != parts[0] {
		return nil, model.ErrIssueCursorInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, model.ErrIssueCursorInvalid
	}
	if base64.RawURLEncoding.EncodeToString(signature) != parts[1] {
		return nil, model.ErrIssueCursorInvalid
	}
	key, err := s.derivedKey(issueCursorKeyDomain)
	if err != nil {
		return nil, err
	}
	expected := opaqueDigest(key, payload)
	zeroBytes(key)
	if !hmac.Equal(signature, expected) {
		return nil, model.ErrIssueCursorInvalid
	}
	return append([]byte(nil), payload...), nil
}
