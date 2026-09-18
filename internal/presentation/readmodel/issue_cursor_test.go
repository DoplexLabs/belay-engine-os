package readmodel

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

type issueTestCursorCodec struct{}

func (issueTestCursorCodec) SealIssueCursor(payload []byte) (string, error) {
	sum := sha256.Sum256(append([]byte("readmodel-test:"), payload...))
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func (issueTestCursorCodec) OpenIssueCursor(value string) ([]byte, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return nil, model.ErrIssueCursorInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, model.ErrIssueCursorInvalid
	}
	expected, _ := issueTestCursorCodec{}.SealIssueCursor(payload)
	if expected != value {
		return nil, model.ErrIssueCursorInvalid
	}
	return payload, nil
}
