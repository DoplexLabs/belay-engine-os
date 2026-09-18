package localhttp

import "net/http"

func (s *Server) getReport(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeReadInvalidRequest(w, r)
		return
	}
	response, err := s.read.GetReport(r.Context())
	writeReadResult(w, r, response, err)
}
