package readmodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/canonical/sourcecatalog"
)

const attentionFamilyCursorVersion = 1

type attentionFamilyCursorFilters struct {
	Severity       string `json:"severity,omitempty"`
	Harness        string `json:"harness,omitempty"`
	Origin         string `json:"origin,omitempty"`
	AnalysisStatus string `json:"analysis_status,omitempty"`
	ObservedAfter  string `json:"observed_after,omitempty"`
	AttentionKind  string `json:"attention_kind"`
	Experimental   string `json:"experimental"`
}

type attentionFamilyCursorEnvelope struct {
	Version              int                           `json:"v"`
	Kind                 string                        `json:"k"`
	CatalogVersion       string                        `json:"c"`
	CursorEpoch          string                        `json:"e"`
	Snapshot             int64                         `json:"s"`
	RetentionGeneration  int64                         `json:"g"`
	IssuedAt             string                        `json:"a"`
	Limit                int                           `json:"l,omitempty"`
	Filters              *attentionFamilyCursorFilters `json:"f,omitempty"`
	FamilyID             string                        `json:"i,omitempty"`
	GroupKey             string                        `json:"y,omitempty"`
	Time                 string                        `json:"t,omitempty"`
	PositionID           string                        `json:"p,omitempty"`
	SeverityRank         int                           `json:"r,omitempty"`
	SupportingIssueCount int                           `json:"n,omitempty"`
	AnalysisStatusRank   int                           `json:"u,omitempty"`
}

func (s *Service) sealAttentionFamilyCursor(
	cursor attentionFamilyCursorEnvelope,
) (string, error) {
	if s == nil || s.issueCursorCodec == nil {
		return "", capabilityUnavailable()
	}
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", errors.New("encode attention family cursor")
	}
	value, err := s.issueCursorCodec.SealIssueCursor(body)
	if err != nil {
		if errors.Is(err, model.ErrIssueCursorInvalid) {
			return "", invalidCursor()
		}
		return "", err
	}
	return value, nil
}

func (s *Service) openAttentionFamilyCursor(
	value string,
) (attentionFamilyCursorEnvelope, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return attentionFamilyCursorEnvelope{}, invalidCursor()
	}
	if s == nil || s.issueCursorCodec == nil {
		return attentionFamilyCursorEnvelope{}, capabilityUnavailable()
	}
	body, err := s.issueCursorCodec.OpenIssueCursor(value)
	if err != nil {
		if errors.Is(err, model.ErrIssueCursorInvalid) {
			return attentionFamilyCursorEnvelope{}, invalidCursor()
		}
		return attentionFamilyCursorEnvelope{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var cursor attentionFamilyCursorEnvelope
	if err := decoder.Decode(&cursor); err != nil {
		return attentionFamilyCursorEnvelope{}, invalidCursor()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return attentionFamilyCursorEnvelope{}, invalidCursor()
	}
	if cursor.Version != attentionFamilyCursorVersion {
		return attentionFamilyCursorEnvelope{}, invalidCursor()
	}
	if cursor.CatalogVersion != sourcecatalog.CatalogVersion {
		return attentionFamilyCursorEnvelope{}, cursorExpired()
	}
	return cursor, nil
}

func attentionFamilyCursorFiltersFromRequest(
	request AttentionFamilyListRequest,
) attentionFamilyCursorFilters {
	return attentionFamilyCursorFilters{
		Severity:       request.Severity,
		Harness:        request.Harness,
		Origin:         request.Origin,
		AnalysisStatus: request.AnalysisStatus,
		ObservedAfter:  timeString(request.ObservedAfter),
		AttentionKind:  request.AttentionKind,
		Experimental:   request.Experimental,
	}
}

func attentionFamilyListRequestFromCursor(
	cursor attentionFamilyCursorEnvelope,
) (AttentionFamilyListRequest, error) {
	if cursor.Filters == nil {
		return AttentionFamilyListRequest{}, invalidCursor()
	}
	request := AttentionFamilyListRequest{
		Limit:          cursor.Limit,
		Severity:       cursor.Filters.Severity,
		Harness:        cursor.Filters.Harness,
		Origin:         cursor.Filters.Origin,
		AnalysisStatus: cursor.Filters.AnalysisStatus,
		AttentionKind:  cursor.Filters.AttentionKind,
		Experimental:   cursor.Filters.Experimental,
	}
	if cursor.Filters.ObservedAfter != "" {
		parsed, err := parseUTCTime(cursor.Filters.ObservedAfter)
		if err != nil {
			return AttentionFamilyListRequest{}, invalidCursor()
		}
		request.ObservedAfter = &parsed
	}
	if request.Limit < 1 || request.Limit > maxIssueLimit {
		return AttentionFamilyListRequest{}, invalidCursor()
	}
	if err := validateAttentionFamilyListRequest(request); err != nil {
		return AttentionFamilyListRequest{}, invalidCursor()
	}
	return request, nil
}

func validateAttentionFamilyCursorSnapshot(
	cursor attentionFamilyCursorEnvelope,
	now time.Time,
) error {
	if cursor.CursorEpoch == "" ||
		cursor.Snapshot <= 0 ||
		cursor.RetentionGeneration < 1 {
		return invalidCursor()
	}
	return validateCursorIssuedAt(cursor.IssuedAt, now)
}
