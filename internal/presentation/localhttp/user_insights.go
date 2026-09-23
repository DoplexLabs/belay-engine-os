package localhttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/userinsights"
)

// HabitDebriefService generates one session's debrief through the user's own
// installed harness.
type HabitDebriefService interface {
	Generate(context.Context, string, bool) (userinsights.DebriefRecord, error)
	Harness() (string, bool)
}

func WithHabitDebriefService(service HabitDebriefService) Option {
	return func(server *Server) {
		server.habits = service
	}
}

type habitDebriefResponse struct {
	SchemaVersion     string                     `json:"schema_version"`
	ProjectionVersion string                     `json:"projection_version"`
	Data              userinsights.DebriefRecord `json:"data"`
}

// getUserInsightDebrief returns one session's stored debrief. With
// generate=1 it runs the user's harness first, which can take minutes; the
// browser calls it only from an explicit click.
func (s *Server) getUserInsightDebrief(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query := r.URL.Query()
	for name := range query {
		if name != "generate" && name != "refresh" {
			writeReadInvalidRequest(w, r)
			return
		}
	}
	generate := queryValue(r, "generate") == "1"
	refresh := queryValue(r, "refresh") == "1"
	sessionKey := r.PathValue("session_key")
	if !generate {
		record, err := s.read.GetUserInsightDebrief(r.Context(), sessionKey)
		if errors.Is(err, readmodel.ErrUserInsightsUnavailable) {
			writeHabitsUnavailable(w, r)
			return
		}
		writeReadResult(w, r, habitDebriefResponse{
			SchemaVersion:     readmodel.SchemaVersion,
			ProjectionVersion: readmodel.UserInsightsProjectionVersion,
			Data:              record,
		}, err)
		return
	}
	if s.habits == nil {
		writeHabitsUnavailable(w, r)
		return
	}
	record, err := s.habits.Generate(r.Context(), sessionKey, refresh)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, habitDebriefResponse{
			SchemaVersion:     readmodel.SchemaVersion,
			ProjectionVersion: readmodel.UserInsightsProjectionVersion,
			Data:              readmodel.PublicDebriefRecord(record),
		})
	case errors.Is(err, localapp.ErrHabitHarnessUnavailable):
		writeProblemType(w, r, http.StatusServiceUnavailable,
			"belay.local/habits-harness-unavailable", "No harness available",
			"Belay needs Claude Code, Codex, the Cursor CLI, or the Antigravity CLI installed on this machine to write a debrief.")
	case errors.Is(err, localapp.ErrHabitSessionNotFound):
		writeProblem(w, r, http.StatusNotFound, "Not found", "That session is not in the transcript store.")
	case errors.Is(err, localapp.ErrHabitSessionNotReady):
		writeProblemType(w, r, http.StatusConflict,
			"belay.local/habits-session-not-ready", "Session still in progress",
			"Belay debriefs a session once its transcript is complete and it has at least two of your messages.")
	case errors.Is(err, localapp.ErrHabitGenerationFailed):
		writeProblemType(w, r, http.StatusBadGateway,
			"belay.local/habits-generation-failed", "Debrief failed",
			"Your harness did not return a usable debrief. Try again in a moment.")
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		writeProblemType(w, r, http.StatusGatewayTimeout,
			"belay.local/habits-generation-timeout", "Debrief timed out",
			"Your harness took too long to answer. Try again.")
	default:
		writeProblem(w, r, http.StatusInternalServerError, "Local read failed", "Belay could not complete the debrief.")
	}
}

func writeHabitsUnavailable(w http.ResponseWriter, r *http.Request) {
	writeProblemType(w, r, http.StatusServiceUnavailable,
		"belay.local/user-insights-unavailable", "Habits unavailable",
		"Belay could not read retained transcript sessions for a debrief.")
}

const maxUserInsightsLimit = 25

// getUserInsights serves the Habits view. It accepts only an optional
// bounded limit and shares no state with the issue, report, or Mission Pack
// handlers.
func (s *Server) getUserInsights(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for name := range query {
		if name != "limit" {
			writeReadInvalidRequest(w, r)
			return
		}
	}
	limit := 0
	if raw := queryValue(r, "limit"); raw != "" {
		parsed := boundedInt(r, "limit", -1, maxUserInsightsLimit)
		if parsed <= 0 {
			writeReadInvalidRequest(w, r)
			return
		}
		limit = parsed
	}
	response, err := s.read.GetUserInsights(
		r.Context(),
		readmodel.UserInsightsRequest{Limit: limit},
	)
	if errors.Is(err, readmodel.ErrUserInsightsUnavailable) {
		writeProblemType(
			w,
			r,
			http.StatusServiceUnavailable,
			"belay.local/user-insights-unavailable",
			"Habits unavailable",
			"Belay could not read retained transcript sessions for a debrief.",
		)
		return
	}
	writeReadResult(w, r, response, err)
}
