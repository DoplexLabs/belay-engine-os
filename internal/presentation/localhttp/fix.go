package localhttp

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/localaction"
)

const maxFixRequestBody = 1024

var (
	fixIssueIDPattern      = regexp.MustCompile(`^iss_[a-z2-7]{52}$`)
	fixAnnotationIDPattern = regexp.MustCompile(`^fxa_[a-z2-7]{52}$`)
)

type FixService interface {
	PrepareFixAttempt(
		context.Context,
		string,
		model.IssueViewClaims,
	) (localaction.FixEligibility, error)
	RecordFixAttempt(
		context.Context,
		string,
		string,
		model.FixChangeKind,
		string,
	) (localaction.FixAttemptResult, error)
	RetractFixAttempt(
		context.Context,
		string,
		string,
		model.FixRetractionReason,
		string,
	) (localaction.FixRetractionResult, error)
	ListFixAttempts(
		context.Context,
		string,
		int,
		string,
	) (localaction.FixAttemptPage, error)
}

type fixEligibilityResponse struct {
	SchemaVersion string             `json:"schema_version"`
	Data          fixEligibilityData `json:"data"`
}

type fixEligibilityData struct {
	Eligible             bool       `json:"eligible"`
	Reason               string     `json:"reason"`
	ActionToken          *string    `json:"action_token"`
	ExpiresAt            *time.Time `json:"expires_at"`
	ChangeCatalogVersion string     `json:"change_catalog_version"`
}

type fixAnnotationResponse struct {
	SchemaVersion string           `json:"schema_version"`
	Data          fixAnnotationDTO `json:"data"`
	Replayed      bool             `json:"replayed"`
}

type fixRetractionResponse struct {
	SchemaVersion string              `json:"schema_version"`
	Data          model.FixRetraction `json:"data"`
	Replayed      bool                `json:"replayed"`
}

type fixHistoryResponse struct {
	SchemaVersion       string             `json:"schema_version"`
	Data                []fixAnnotationDTO `json:"data"`
	NextCursor          *string            `json:"next_cursor"`
	HasMore             bool               `json:"has_more"`
	ReturnedCount       int                `json:"returned_count"`
	Limit               int                `json:"limit"`
	EvidenceEvaluatedAt time.Time          `json:"evidence_evaluated_at"`
}

type fixAnnotationDTO struct {
	AnnotationID              string              `json:"annotation_id"`
	IssueID                   string              `json:"issue_id"`
	AnchorRevisionID          string              `json:"anchor_revision_id"`
	AnchorOccurrenceID        string              `json:"anchor_occurrence_id"`
	AnchorSessionID           string              `json:"anchor_session_id"`
	FingerprintID             string              `json:"fingerprint_id"`
	FingerprintVersion        string              `json:"fingerprint_version"`
	Origin                    string              `json:"origin"`
	DetectorID                string              `json:"detector_id"`
	DetectorVersion           string              `json:"detector_version"`
	ScopeQuality              model.ScopeQuality  `json:"scope_quality"`
	IssueSnapshotGeneration   int64               `json:"issue_snapshot_generation"`
	AnchorAnalysisGeneration  int64               `json:"anchor_analysis_generation"`
	AnchorFirstObservedAt     time.Time           `json:"anchor_first_observed_at"`
	AnchorLastObservedAt      time.Time           `json:"anchor_last_observed_at"`
	ChangeKind                model.FixChangeKind `json:"change_kind"`
	ChangeCatalogVersion      string              `json:"change_catalog_version"`
	RecordedVia               string              `json:"recorded_via"`
	RecordedAt                time.Time           `json:"recorded_at"`
	MonitorFrom               time.Time           `json:"monitor_from"`
	EvidenceCurrentlyRetained string              `json:"evidence_currently_retained"`
	State                     string              `json:"state"`
	RetractionReason          *string             `json:"retraction_reason"`
	RetractedAt               *time.Time          `json:"retracted_at"`
}

func (s *Server) registerFixRoutes(mux *http.ServeMux, trustedListener string) {
	mux.Handle(
		"GET /v1/issues/{id}/fix-eligibility",
		s.authorize(http.HandlerFunc(s.getFixEligibility)),
	)
	mux.Handle(
		"GET /v1/issues/{id}/fixes",
		s.authorize(http.HandlerFunc(s.listFixAttempts)),
	)
	mux.Handle(
		"POST /v1/issues/{id}/fixes",
		s.authorizeWrite(s.protectWrite(
			trustedListener,
			"record-fix-attempt.v1",
			http.HandlerFunc(s.recordFixAttempt),
		)),
	)
	mux.Handle(
		"POST /v1/issues/{id}/fixes/{annotation_id}/retractions",
		s.authorizeWrite(s.protectWrite(
			trustedListener,
			"retract-fix-attempt.v1",
			http.HandlerFunc(s.retractFixAttempt),
		)),
	)
}

func (s *Server) getFixEligibility(w http.ResponseWriter, r *http.Request) {
	parameters, err := exactQuery(r, "view_cursor")
	if err != nil || len(parameters["view_cursor"]) != 1 ||
		strings.TrimSpace(parameters["view_cursor"][0]) == "" {
		writeInvalidRequest(w, r)
		return
	}
	issueID := normalizeFixIssueID(r.PathValue("id"))
	if !fixIssueIDPattern.MatchString(issueID) {
		writeInvalidRequest(w, r)
		return
	}
	claims, err := s.read.DecodeIssueViewCursor(parameters["view_cursor"][0])
	if err != nil {
		writeReadResult(w, r, nil, err)
		return
	}
	result, err := s.fix.PrepareFixAttempt(r.Context(), issueID, claims)
	if err != nil {
		writeFixError(w, r, err, false)
		return
	}
	writeJSON(w, http.StatusOK, fixEligibilityResponse{
		SchemaVersion: model.FixSchemaVersion,
		Data: fixEligibilityData{
			Eligible:             result.Eligible,
			Reason:               result.Reason,
			ActionToken:          result.ActionToken,
			ExpiresAt:            result.ExpiresAt,
			ChangeCatalogVersion: result.ChangeCatalogVersion,
		},
	})
}

func (s *Server) listFixAttempts(w http.ResponseWriter, r *http.Request) {
	parameters, err := exactQuery(r, "limit", "cursor")
	if err != nil || len(parameters["limit"]) > 1 || len(parameters["cursor"]) > 1 {
		writeInvalidRequest(w, r)
		return
	}
	limit := 20
	if len(parameters["limit"]) == 1 {
		limit, err = strconv.Atoi(strings.TrimSpace(parameters["limit"][0]))
		if err != nil || limit <= 0 {
			writeInvalidRequest(w, r)
			return
		}
	}
	cursor := ""
	if len(parameters["cursor"]) == 1 {
		cursor = strings.TrimSpace(parameters["cursor"][0])
		if cursor == "" {
			writeInvalidRequest(w, r)
			return
		}
	}
	issueID := normalizeFixIssueID(r.PathValue("id"))
	if !fixIssueIDPattern.MatchString(issueID) {
		writeInvalidRequest(w, r)
		return
	}
	result, err := s.fix.ListFixAttempts(r.Context(), issueID, limit, cursor)
	if err != nil {
		writeFixError(w, r, err, false)
		return
	}
	data := make([]fixAnnotationDTO, len(result.Data))
	for index := range result.Data {
		data[index] = annotationDTO(result.Data[index])
	}
	writeJSON(w, http.StatusOK, fixHistoryResponse{
		SchemaVersion:       model.FixSchemaVersion,
		Data:                data,
		NextCursor:          result.NextCursor,
		HasMore:             result.HasMore,
		ReturnedCount:       result.ReturnedCount,
		Limit:               result.Limit,
		EvidenceEvaluatedAt: result.EvidenceEvaluatedAt,
	})
}

func (s *Server) recordFixAttempt(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeInvalidRequest(w, r)
		return
	}
	issueID := normalizeFixIssueID(r.PathValue("id"))
	idempotencyKey, ok := singleHeader(r, "Idempotency-Key")
	if !fixIssueIDPattern.MatchString(issueID) ||
		!ok ||
		!model.IsCanonicalUUIDv4(idempotencyKey) {
		writeInvalidRequest(w, r)
		return
	}
	object, err := decodeStrictObject(r, "action_token", "change_kind")
	if err != nil {
		writeInvalidRequest(w, r)
		return
	}
	actionToken, ok := requiredString(object, "action_token")
	if !ok {
		writeInvalidRequest(w, r)
		return
	}
	changeKindValue, ok := requiredString(object, "change_kind")
	changeKind := model.FixChangeKind(changeKindValue)
	if !ok || !changeKind.Valid() {
		writeInvalidRequest(w, r)
		return
	}
	result, err := s.fix.RecordFixAttempt(
		r.Context(),
		issueID,
		actionToken,
		changeKind,
		idempotencyKey,
	)
	if err != nil {
		writeFixError(w, r, err, true)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, fixAnnotationResponse{
		SchemaVersion: model.FixSchemaVersion,
		Data:          annotationDTO(result.Annotation),
		Replayed:      result.Replayed,
	})
}

func (s *Server) retractFixAttempt(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeInvalidRequest(w, r)
		return
	}
	issueID := normalizeFixIssueID(r.PathValue("id"))
	annotationID := strings.ToLower(strings.TrimSpace(r.PathValue("annotation_id")))
	idempotencyKey, ok := singleHeader(r, "Idempotency-Key")
	if !fixIssueIDPattern.MatchString(issueID) ||
		!fixAnnotationIDPattern.MatchString(annotationID) ||
		!ok ||
		!model.IsCanonicalUUIDv4(idempotencyKey) {
		writeInvalidRequest(w, r)
		return
	}
	object, err := decodeStrictObject(r, "reason")
	if err != nil {
		writeInvalidRequest(w, r)
		return
	}
	reasonValue, ok := requiredString(object, "reason")
	reason := model.FixRetractionReason(reasonValue)
	if !ok || !reason.Valid() {
		writeInvalidRequest(w, r)
		return
	}
	result, err := s.fix.RetractFixAttempt(
		r.Context(),
		issueID,
		annotationID,
		reason,
		idempotencyKey,
	)
	if err != nil {
		writeFixError(w, r, err, true)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, fixRetractionResponse{
		SchemaVersion: model.FixSchemaVersion,
		Data:          result.Retraction,
		Replayed:      result.Replayed,
	})
}

func (s *Server) authorizeWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header, ok := singleHeader(r, "Authorization")
		const prefix = "Bearer "
		if !ok ||
			!strings.HasPrefix(header, prefix) ||
			subtle.ConstantTimeCompare(
				[]byte(strings.TrimPrefix(header, prefix)),
				[]byte(s.token),
			) != 1 {
			writeProblem(
				w,
				r,
				http.StatusUnauthorized,
				"Unauthorized",
				"A valid per-launch Local token is required.",
			)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) protectWrite(
	trustedListener string,
	intent string,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, trusted := trustedOrigin(trustedListener)
		requestOrigin, originOK := singleHeader(r, "Origin")
		requestIntent, intentOK := singleHeader(r, "X-Belay-Intent")
		fetchSites := r.Header.Values("Sec-Fetch-Site")
		fetchOK := len(fetchSites) == 0 ||
			(len(fetchSites) == 1 && fetchSites[0] == "same-origin")
		if !trusted ||
			r.Host != trustedListener ||
			!originOK ||
			requestOrigin != origin ||
			!intentOK ||
			requestIntent != intent ||
			!fetchOK {
			writeProblemType(
				w,
				r,
				http.StatusForbidden,
				"belay.local/write-forbidden",
				"Write forbidden",
				"Belay Local rejected the browser write.",
			)
			return
		}

		contentType, ok := singleHeader(r, "Content-Type")
		if !ok || !validJSONContentType(contentType) {
			writeProblemType(
				w,
				r,
				http.StatusUnsupportedMediaType,
				"belay.local/unsupported-media-type",
				"Unsupported media type",
				"Belay Local writes require JSON.",
			)
			return
		}
		encodings := r.Header.Values("Content-Encoding")
		if len(encodings) > 1 ||
			(len(encodings) == 1 &&
				!strings.EqualFold(strings.TrimSpace(encodings[0]), "identity")) {
			writeProblemType(
				w,
				r,
				http.StatusUnsupportedMediaType,
				"belay.local/unsupported-media-type",
				"Unsupported media type",
				"Belay Local writes require an identity-encoded body.",
			)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxFixRequestBody+1))
		if err != nil {
			writeInvalidRequest(w, r)
			return
		}
		_ = r.Body.Close()
		if len(body) > maxFixRequestBody {
			writeProblemType(
				w,
				r,
				http.StatusRequestEntityTooLarge,
				"belay.local/request-too-large",
				"Request too large",
				"Belay Local write bodies must not exceed 1 KiB.",
			)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func trustedOrigin(listener string) (string, bool) {
	host, port, err := net.SplitHostPort(listener)
	if err != nil || port == "" {
		return "", false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", false
	}
	value, err := strconv.Atoi(port)
	if err != nil || value <= 0 || value > 65535 {
		return "", false
	}
	return "http://" + listener, true
}

func exactQuery(r *http.Request, allowed ...string) (url.Values, error) {
	parameters, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	allowedNames := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedNames[name] = struct{}{}
	}
	for name := range parameters {
		if _, ok := allowedNames[name]; !ok {
			return nil, errors.New("unknown query parameter")
		}
	}
	return parameters, nil
}

func singleHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	return value, value != ""
}

func validJSONContentType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || mediaType != "application/json" {
		return false
	}
	for name, value := range parameters {
		if !strings.EqualFold(name, "charset") ||
			!strings.EqualFold(strings.TrimSpace(value), "utf-8") {
			return false
		}
	}
	return true
}

func decodeStrictObject(r *http.Request, allowed ...string) (map[string]json.RawMessage, error) {
	allowedNames := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedNames[name] = struct{}{}
	}
	decoder := json.NewDecoder(r.Body)
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, errors.New("JSON body must be an object")
	}
	result := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("JSON object key is invalid")
		}
		if _, allowed := allowedNames[name]; !allowed {
			return nil, errors.New("unknown JSON field")
		}
		if _, duplicate := result[name]; duplicate {
			return nil, errors.New("duplicate JSON field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		result[name] = value
	}
	token, err = decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok = token.(json.Delim)
	if !ok || delim != '}' {
		return nil, errors.New("JSON object is incomplete")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing JSON data")
	}
	if len(result) != len(allowedNames) {
		return nil, errors.New("required JSON field is missing")
	}
	return result, nil
}

func requiredString(object map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := object[name]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func normalizeFixIssueID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func annotationDTO(annotation model.FixAnnotation) fixAnnotationDTO {
	var retractionReason *string
	if annotation.RetractionReason != "" {
		value := annotation.RetractionReason
		retractionReason = &value
	}
	return fixAnnotationDTO{
		AnnotationID:              annotation.AnnotationID,
		IssueID:                   annotation.IssueID,
		AnchorRevisionID:          annotation.AnchorRevisionID,
		AnchorOccurrenceID:        annotation.AnchorOccurrenceID,
		AnchorSessionID:           annotation.AnchorSessionID,
		FingerprintID:             annotation.FingerprintID,
		FingerprintVersion:        annotation.FingerprintVersion,
		Origin:                    annotation.Origin,
		DetectorID:                annotation.DetectorID,
		DetectorVersion:           annotation.DetectorVersion,
		ScopeQuality:              annotation.ScopeQuality,
		IssueSnapshotGeneration:   annotation.IssueSnapshotGeneration,
		AnchorAnalysisGeneration:  annotation.AnchorAnalysisGeneration,
		AnchorFirstObservedAt:     annotation.AnchorFirstObservedAt,
		AnchorLastObservedAt:      annotation.AnchorLastObservedAt,
		ChangeKind:                annotation.ChangeKind,
		ChangeCatalogVersion:      annotation.ChangeCatalogVersion,
		RecordedVia:               annotation.RecordedVia,
		RecordedAt:                annotation.RecordedAt,
		MonitorFrom:               annotation.MonitorFrom,
		EvidenceCurrentlyRetained: annotation.EvidenceCurrentlyRetained,
		State:                     annotation.State,
		RetractionReason:          retractionReason,
		RetractedAt:               annotation.RetractedAt,
	}
}

func writeInvalidRequest(w http.ResponseWriter, r *http.Request) {
	writeProblem(
		w,
		r,
		http.StatusBadRequest,
		"Invalid request",
		"The supplied fix request is invalid.",
	)
}

func writeFixError(w http.ResponseWriter, r *http.Request, err error, write bool) {
	switch {
	case errors.Is(err, localaction.ErrInvalidRequest):
		writeInvalidRequest(w, r)
	case errors.Is(err, localaction.ErrCursorExpired):
		writeProblemType(
			w,
			r,
			http.StatusGone,
			"belay.local/cursor-expired",
			"Cursor expired",
			"The issue view changed. Refresh before trying again.",
		)
	case errors.Is(err, localaction.ErrNotFound):
		writeProblem(
			w,
			r,
			http.StatusNotFound,
			"Not found",
			"The requested fix resource is not available.",
		)
	case errors.Is(err, localaction.ErrIneligibleIssue):
		writeProblemType(
			w,
			r,
			http.StatusConflict,
			"belay.local/ineligible-fix-annotation",
			"Fix attempt unavailable",
			"This issue cannot accept a fix-attempt declaration.",
		)
	case errors.Is(err, localaction.ErrIdempotencyConflict):
		writeProblemType(
			w,
			r,
			http.StatusConflict,
			"belay.local/idempotency-conflict",
			"Idempotency conflict",
			"The retry key was already used for different content.",
		)
	case errors.Is(err, localaction.ErrAlreadyRetracted):
		writeProblemType(
			w,
			r,
			http.StatusConflict,
			"belay.local/already-retracted",
			"Already retracted",
			"This fix-attempt declaration was already retracted.",
		)
	default:
		title := "Local read failed"
		detail := "Belay could not complete the local read."
		if write {
			title = "Local write failed"
			detail = "Belay could not complete the local write."
		}
		writeProblem(w, r, http.StatusInternalServerError, title, detail)
	}
}
