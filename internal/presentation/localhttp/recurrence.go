package localhttp

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

func (s *Server) registerFixMonitoringRoutes(mux *http.ServeMux) {
	mux.Handle(
		"GET /v1/fix-monitoring",
		s.authorize(http.HandlerFunc(s.listFixMonitoring)),
	)
	mux.Handle(
		"GET /v1/issues/{id}/fix-monitoring",
		s.authorize(http.HandlerFunc(s.getIssueFixMonitoring)),
	)
	mux.Handle(
		"GET /v1/issues/{id}/fixes/{annotation_id}/recurrences",
		s.authorize(http.HandlerFunc(s.listFixRecurrences)),
	)
}

func (s *Server) listFixMonitoring(w http.ResponseWriter, r *http.Request) {
	parameters, err := exactQuery(
		r,
		"state",
		"change_kind",
		"severity",
		"harness",
		"recorded_after",
		"issue_id",
		"include_retracted",
		"limit",
		"cursor",
	)
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	cursor, present, err := optionalExactParameter(parameters, "cursor")
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	if present {
		if len(parameters) != 1 {
			writeMonitoringInvalidRequest(w, r)
			return
		}
		response, err := s.read.ListFixMonitoring(
			r.Context(),
			readmodel.FixMonitoringListRequest{Cursor: cursor},
		)
		writeFixMonitoringResult(w, r, response, err)
		return
	}
	limit, err := monitoringLimit(parameters)
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	state, _, err := optionalExactParameter(parameters, "state")
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	changeKind, _, err := optionalExactParameter(parameters, "change_kind")
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	severity, _, err := optionalExactParameter(parameters, "severity")
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	harness, _, err := optionalExactParameter(parameters, "harness")
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	issueID, _, err := optionalExactParameter(parameters, "issue_id")
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	recordedAfterValue, recordedAfterPresent, err := optionalExactParameter(
		parameters,
		"recorded_after",
	)
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	var recordedAfter *time.Time
	if recordedAfterPresent {
		value, parseErr := time.Parse(time.RFC3339Nano, recordedAfterValue)
		if parseErr != nil {
			writeMonitoringInvalidRequest(w, r)
			return
		}
		value = value.UTC()
		recordedAfter = &value
	}
	includeRetracted := false
	includeValue, includePresent, err := optionalExactParameter(
		parameters,
		"include_retracted",
	)
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	if includePresent {
		switch includeValue {
		case "true":
			includeRetracted = true
		case "false":
		default:
			writeMonitoringInvalidRequest(w, r)
			return
		}
	}
	response, err := s.read.ListFixMonitoring(
		r.Context(),
		readmodel.FixMonitoringListRequest{
			Limit:            limit,
			State:            state,
			ChangeKind:       changeKind,
			Severity:         severity,
			Harness:          harness,
			RecordedAfter:    recordedAfter,
			IssueID:          issueID,
			IncludeRetracted: includeRetracted,
		},
	)
	writeFixMonitoringResult(w, r, response, err)
}

func (s *Server) getIssueFixMonitoring(w http.ResponseWriter, r *http.Request) {
	issueID := normalizeFixIssueID(r.PathValue("id"))
	if !fixIssueIDPattern.MatchString(issueID) {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	parameters, err := exactQuery(r, "limit", "cursor", "view_cursor")
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	cursor, cursorPresent, err := optionalExactParameter(parameters, "cursor")
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	viewCursor, viewPresent, err := optionalExactParameter(parameters, "view_cursor")
	if err != nil || (cursorPresent && viewPresent) {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	if cursorPresent {
		if len(parameters) != 1 {
			writeMonitoringInvalidRequest(w, r)
			return
		}
		response, err := s.read.GetIssueFixMonitoring(
			r.Context(),
			readmodel.FixMonitoringDetailRequest{
				IssueID: issueID,
				Cursor:  cursor,
			},
		)
		writeFixMonitoringResult(w, r, response, err)
		return
	}
	limit, err := monitoringLimit(parameters)
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	response, err := s.read.GetIssueFixMonitoring(
		r.Context(),
		readmodel.FixMonitoringDetailRequest{
			IssueID:    issueID,
			Limit:      limit,
			ViewCursor: viewCursor,
		},
	)
	writeFixMonitoringResult(w, r, response, err)
}

func (s *Server) listFixRecurrences(w http.ResponseWriter, r *http.Request) {
	issueID := normalizeFixIssueID(r.PathValue("id"))
	annotationID := strings.ToLower(strings.TrimSpace(r.PathValue("annotation_id")))
	if !fixIssueIDPattern.MatchString(issueID) ||
		!fixAnnotationIDPattern.MatchString(annotationID) {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	parameters, err := exactQuery(
		r,
		"limit",
		"cursor",
		"observation_view_cursor",
	)
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	cursor, cursorPresent, err := optionalExactParameter(parameters, "cursor")
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	viewCursor, viewPresent, err := optionalExactParameter(
		parameters,
		"observation_view_cursor",
	)
	if err != nil || cursorPresent == viewPresent {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	if cursorPresent {
		if len(parameters) != 1 {
			writeMonitoringInvalidRequest(w, r)
			return
		}
		response, err := s.read.ListFixRecurrenceObservations(
			r.Context(),
			readmodel.FixRecurrenceObservationListRequest{
				IssueID:      issueID,
				AnnotationID: annotationID,
				Cursor:       cursor,
			},
		)
		writeFixMonitoringResult(w, r, response, err)
		return
	}
	limit, err := monitoringLimit(parameters)
	if err != nil {
		writeMonitoringInvalidRequest(w, r)
		return
	}
	response, err := s.read.ListFixRecurrenceObservations(
		r.Context(),
		readmodel.FixRecurrenceObservationListRequest{
			IssueID:               issueID,
			AnnotationID:          annotationID,
			Limit:                 limit,
			ObservationViewCursor: viewCursor,
		},
	)
	writeFixMonitoringResult(w, r, response, err)
}

func optionalExactParameter(
	parameters url.Values,
	name string,
) (string, bool, error) {
	values, present := parameters[name]
	if !present {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, errors.New("repeated query parameter")
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return "", false, errors.New("empty query parameter")
	}
	return value, true, nil
}

func monitoringLimit(parameters url.Values) (int, error) {
	value, present, err := optionalExactParameter(parameters, "limit")
	if err != nil || !present {
		return 0, err
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 100 {
		return 0, errors.New("invalid monitoring limit")
	}
	return limit, nil
}

func writeMonitoringInvalidRequest(w http.ResponseWriter, r *http.Request) {
	writeProblem(
		w,
		r,
		http.StatusBadRequest,
		"Invalid request",
		"The supplied monitoring cursor or filters are invalid.",
	)
}

func writeFixMonitoringResult(
	w http.ResponseWriter,
	r *http.Request,
	response any,
	err error,
) {
	switch {
	case errors.Is(err, readmodel.ErrInvalidCursor),
		errors.Is(err, readmodel.ErrInvalidRequest):
		writeMonitoringInvalidRequest(w, r)
	case errors.Is(err, readmodel.ErrMonitoringCatchingUp):
		writeProblemType(
			w,
			r,
			http.StatusServiceUnavailable,
			"belay.local/monitoring-catchup-in-progress",
			"Monitoring catch-up in progress",
			"Belay is preparing historical monitoring. Retry this read shortly.",
		)
	case errors.Is(err, readmodel.ErrMonitoringCatchupFailed):
		writeProblemType(
			w,
			r,
			http.StatusServiceUnavailable,
			"belay.local/monitoring-catchup-failed",
			"Monitoring catch-up failed",
			"Belay could not complete historical monitoring catch-up. Retry after Local recovery.",
		)
	case errors.Is(err, readmodel.ErrCursorExpired):
		writeProblemType(
			w,
			r,
			http.StatusGone,
			"belay.local/cursor-expired",
			"Cursor expired",
			"This monitoring view expired. Refresh monitoring to continue.",
		)
	case errors.Is(err, readmodel.ErrNotFound):
		writeProblem(
			w,
			r,
			http.StatusNotFound,
			"Not found",
			"The requested monitoring resource is not available.",
		)
	case err != nil:
		writeProblem(
			w,
			r,
			http.StatusInternalServerError,
			"Local read failed",
			"Belay could not complete the local read.",
		)
	default:
		writeJSON(w, http.StatusOK, response)
	}
}
