package localhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/missionpack"
)

const (
	missionPackHTTPDeadline   = 5 * time.Second
	maxMissionPackIssueIDSize = 512
)

type MissionPackService interface {
	Generate(
		context.Context,
		missionpack.Request,
	) (missionpack.Pack, error)
}

type missionPackErrorKind interface {
	MissionPackErrorKind() string
}

func WithMissionPackService(service MissionPackService) Option {
	return func(server *Server) {
		server.missionPacks = service
	}
}

func (s *Server) getMissionPack(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	issueID, intent, ok := missionPackHTTPQuery(r)
	if !ok {
		writeProblem(
			w,
			r,
			http.StatusBadRequest,
			"Invalid request",
			"Mission Pack request is invalid.",
		)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), missionPackHTTPDeadline)
	defer cancel()
	pack, err := s.missionPacks.Generate(ctx, missionpack.Request{
		IssueID: issueID,
		Intent:  intent,
	})
	if err != nil {
		writeMissionPackProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pack)
}

func missionPackHTTPQuery(
	r *http.Request,
) (string, missionpack.Intent, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", "", false
	}
	for name, entries := range values {
		if name != "issue_id" && name != "intent" {
			return "", "", false
		}
		if len(entries) != 1 {
			return "", "", false
		}
	}
	issueID := strings.TrimSpace(values.Get("issue_id"))
	if issueID == "" ||
		len(issueID) > maxMissionPackIssueIDSize ||
		!utf8.ValidString(issueID) {
		return "", "", false
	}
	intent := missionpack.Intent(strings.TrimSpace(values.Get("intent")))
	if intent == "" {
		intent = missionpack.IntentGeneral
	}
	if !validMissionPackHTTPIntent(intent) {
		return "", "", false
	}
	return issueID, intent, true
}

func validMissionPackHTTPIntent(intent missionpack.Intent) bool {
	switch intent {
	case missionpack.IntentGeneral,
		missionpack.IntentDebug,
		missionpack.IntentImplement,
		missionpack.IntentRefactor,
		missionpack.IntentReview,
		missionpack.IntentRelease:
		return true
	default:
		return false
	}
}

func writeMissionPackProblem(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	status := http.StatusInternalServerError
	title := "Internal server error"
	detail := "Belay could not prepare this Mission Pack."

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusServiceUnavailable
		title = "Service unavailable"
		detail = "Mission Pack preparation timed out."
	case errors.Is(err, missionpack.ErrProjectMismatch):
		status = http.StatusConflict
		title = "Conflict"
		detail = "The selected issue does not match the current project."
	case errors.Is(err, missionpack.ErrProjectNotFound):
		status = http.StatusNotFound
		title = "Not found"
		detail = "Belay has no retained evidence for this project."
	default:
		var classified missionPackErrorKind
		if errors.As(err, &classified) {
			switch classified.MissionPackErrorKind() {
			case "invalid_request", "invalid_selector":
				status = http.StatusBadRequest
				title = "Invalid request"
				detail = "Mission Pack request is invalid."
			case "project_mismatch":
				status = http.StatusConflict
				title = "Conflict"
				detail = "The selected issue does not match the current project."
			case "project_not_found":
				status = http.StatusNotFound
				title = "Not found"
				detail = "Belay has no retained evidence for this project."
			case "issue_not_found":
				status = http.StatusNotFound
				title = "Not found"
				detail = "The selected issue is unavailable."
			}
		}
	}
	writeProblem(w, r, status, title, detail)
}
