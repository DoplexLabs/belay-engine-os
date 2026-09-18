package localaction

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	historyCursorVersion = 1
	maxHistoryCursorSize = 2048
)

type historyCursorEnvelope struct {
	Version            int    `json:"v"`
	Kind               string `json:"k"`
	IssueID            string `json:"i"`
	AnnotationSnapshot int64  `json:"a"`
	RetractionSnapshot int64  `json:"r"`
	RecordedAt         string `json:"t"`
	AnnotationID       string `json:"n"`
}

type historyCursor struct {
	IssueID            string
	AnnotationSnapshot int64
	RetractionSnapshot int64
	RecordedAt         time.Time
	AnnotationID       string
}

func encodeHistoryCursor(cursor historyCursor) (string, error) {
	if !issueIDPattern.MatchString(cursor.IssueID) ||
		cursor.AnnotationSnapshot <= 0 ||
		cursor.RetractionSnapshot < 0 ||
		cursor.RecordedAt.IsZero() ||
		!annotationIDPattern.MatchString(cursor.AnnotationID) {
		return "", ErrInvalidRequest
	}
	body, err := json.Marshal(historyCursorEnvelope{
		Version:            historyCursorVersion,
		Kind:               "fix_annotations",
		IssueID:            cursor.IssueID,
		AnnotationSnapshot: cursor.AnnotationSnapshot,
		RetractionSnapshot: cursor.RetractionSnapshot,
		RecordedAt:         cursor.RecordedAt.UTC().Format(time.RFC3339Nano),
		AnnotationID:       cursor.AnnotationID,
	})
	if err != nil {
		return "", errors.New("encode fix history cursor")
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeHistoryCursor(value, issueID string) (historyCursor, error) {
	if value == "" || len(value) > maxHistoryCursorSize {
		return historyCursor{}, ErrInvalidRequest
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(body) == 0 || len(body) > maxHistoryCursorSize {
		return historyCursor{}, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var envelope historyCursorEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return historyCursor{}, ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return historyCursor{}, ErrInvalidRequest
	}
	if envelope.Version != historyCursorVersion ||
		envelope.Kind != "fix_annotations" ||
		envelope.IssueID != issueID ||
		envelope.AnnotationSnapshot <= 0 ||
		envelope.RetractionSnapshot < 0 ||
		!annotationIDPattern.MatchString(envelope.AnnotationID) {
		return historyCursor{}, ErrInvalidRequest
	}
	recordedAt, err := time.Parse(time.RFC3339Nano, envelope.RecordedAt)
	if err != nil || recordedAt.Location() != time.UTC {
		return historyCursor{}, ErrInvalidRequest
	}
	return historyCursor{
		IssueID:            envelope.IssueID,
		AnnotationSnapshot: envelope.AnnotationSnapshot,
		RetractionSnapshot: envelope.RetractionSnapshot,
		RecordedAt:         recordedAt,
		AnnotationID:       envelope.AnnotationID,
	}, nil
}
