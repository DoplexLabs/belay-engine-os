package localhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/updatecheck"
)

type UpdateService interface {
	Status() updatecheck.Status
	Remind(string) error
	Dismiss(string) error
}

func WithUpdateService(service UpdateService) Option {
	return func(server *Server) {
		server.updates = service
	}
}

type updateActionRequest struct {
	Action  string `json:"action"`
	Version string `json:"version"`
}

func (s *Server) getUpdate(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeReadInvalidRequest(w, r)
		return
	}
	writeJSON(w, http.StatusOK, s.updates.Status())
}

func (s *Server) postUpdate(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeReadInvalidRequest(w, r)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4096))
	decoder.DisallowUnknownFields()
	var request updateActionRequest
	if err := decoder.Decode(&request); err != nil {
		writeProblem(
			w,
			r,
			http.StatusBadRequest,
			"Invalid request",
			"Update action must be valid JSON.",
		)
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeProblem(
			w,
			r,
			http.StatusBadRequest,
			"Invalid request",
			"Update action must contain one JSON object.",
		)
		return
	}
	version := strings.TrimSpace(request.Version)
	var err error
	switch strings.TrimSpace(request.Action) {
	case "remind":
		err = s.updates.Remind(version)
	case "dismiss":
		err = s.updates.Dismiss(version)
	default:
		writeProblem(
			w,
			r,
			http.StatusBadRequest,
			"Invalid request",
			"Update action must be remind or dismiss.",
		)
		return
	}
	if err != nil {
		writeProblem(
			w,
			r,
			http.StatusConflict,
			"Update changed",
			"The available release changed. Refresh before choosing again.",
		)
		return
	}
	writeJSON(w, http.StatusOK, s.updates.Status())
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else {
		return err
	}
}
