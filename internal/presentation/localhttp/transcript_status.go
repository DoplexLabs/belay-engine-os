package localhttp

import "net/http"

func (s *Server) getTranscriptStatus(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.URL.RawQuery != "" {
		writeReadInvalidRequest(writer, request)
		return
	}
	response, err := s.read.GetTranscriptStatus(request.Context())
	writeReadResult(writer, request, response, err)
}
