package localhttp

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

func (s *Server) listAttentionFamilies(w http.ResponseWriter, r *http.Request) {
	parameters, err := exactQuery(
		r,
		"limit",
		"cursor",
		"severity",
		"harness",
		"origin",
		"analysis_status",
		"observed_after",
		"attention_kind",
		"experimental",
	)
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	cursor, cursorPresent, err := optionalExactParameter(parameters, "cursor")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	if cursorPresent {
		if len(parameters) != 1 {
			writeReadInvalidRequest(w, r)
			return
		}
		response, err := s.read.ListAttentionFamilies(
			r.Context(),
			readmodel.AttentionFamilyListRequest{Cursor: cursor},
		)
		writeReadResult(w, r, response, err)
		return
	}
	limit, err := optionalAttentionFamilyLimit(parameters)
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	values := make(map[string]string, len(parameters))
	for _, name := range []string{
		"severity",
		"harness",
		"origin",
		"analysis_status",
		"attention_kind",
		"experimental",
	} {
		value, present, err := optionalExactParameter(parameters, name)
		if err != nil {
			writeReadInvalidRequest(w, r)
			return
		}
		if present {
			values[name] = value
		}
	}
	var observedAfter *time.Time
	value, present, err := optionalExactParameter(parameters, "observed_after")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	if present {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			writeReadInvalidRequest(w, r)
			return
		}
		parsed = parsed.UTC()
		observedAfter = &parsed
	}
	response, err := s.read.ListAttentionFamilies(
		r.Context(),
		readmodel.AttentionFamilyListRequest{
			Limit:          limit,
			Severity:       values["severity"],
			Harness:        values["harness"],
			Origin:         values["origin"],
			AnalysisStatus: values["analysis_status"],
			ObservedAfter:  observedAfter,
			AttentionKind:  values["attention_kind"],
			Experimental:   values["experimental"],
		},
	)
	writeReadResult(w, r, response, err)
}

func (s *Server) getAttentionFamily(w http.ResponseWriter, r *http.Request) {
	parameters, err := exactQuery(r, "limit", "cursor", "view_cursor")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	cursor, cursorPresent, err := optionalExactParameter(parameters, "cursor")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	viewCursor, viewPresent, err := optionalExactParameter(parameters, "view_cursor")
	if err != nil || cursorPresent == viewPresent {
		writeReadInvalidRequest(w, r)
		return
	}
	limit := 0
	if cursorPresent {
		if len(parameters) != 1 {
			writeReadInvalidRequest(w, r)
			return
		}
	} else {
		if len(parameters) > 2 {
			writeReadInvalidRequest(w, r)
			return
		}
		value, present, err := optionalExactParameter(parameters, "limit")
		if err != nil {
			writeReadInvalidRequest(w, r)
			return
		}
		if present {
			limit, err = strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 100 {
				writeReadInvalidRequest(w, r)
				return
			}
		}
	}
	response, err := s.read.GetAttentionFamily(
		r.Context(),
		readmodel.AttentionFamilyDetailRequest{
			FamilyID:   r.PathValue("family_id"),
			Limit:      limit,
			Cursor:     cursor,
			ViewCursor: viewCursor,
		},
	)
	writeReadResult(w, r, response, err)
}

func optionalAttentionFamilyLimit(parameters url.Values) (int, error) {
	value, present, err := optionalExactParameter(parameters, "limit")
	if err != nil || !present {
		return 0, err
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 100 {
		return 0, errors.New("invalid attention family limit")
	}
	return limit, nil
}
