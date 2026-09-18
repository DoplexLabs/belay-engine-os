package localhttp

import (
	"errors"
	"net/http"

	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

func (s *Server) getDeveloperBrief(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeReadInvalidRequest(w, r)
		return
	}
	response, err := s.read.GetDeveloperBrief(r.Context())
	if errors.Is(err, readmodel.ErrDeveloperBriefUnavailable) {
		writeProblemType(
			w,
			r,
			http.StatusServiceUnavailable,
			"belay.local/developer-brief-unavailable",
			"Developer Brief unavailable",
			"Belay could not assemble the recent developer brief.",
		)
		return
	}
	writeReadResult(w, r, response, err)
}
