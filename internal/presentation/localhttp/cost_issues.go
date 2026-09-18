package localhttp

import (
	"net/http"
	"strconv"

	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

func (s *Server) listCostIssues(w http.ResponseWriter, r *http.Request) {
	parameters, err := exactQuery(r, "limit", "project", "detector")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	limit := 0
	if value, present, parameterErr := optionalExactParameter(
		parameters,
		"limit",
	); parameterErr != nil {
		writeReadInvalidRequest(w, r)
		return
	} else if present {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			writeReadInvalidRequest(w, r)
			return
		}
	}
	project, _, err := optionalExactParameter(parameters, "project")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	detector, _, err := optionalExactParameter(parameters, "detector")
	if err != nil {
		writeReadInvalidRequest(w, r)
		return
	}
	response, err := s.read.ListCostIssues(
		r.Context(),
		readmodel.CostIssueListRequest{
			Limit:           limit,
			ProjectIdentity: project,
			DetectorID:      detector,
		},
	)
	writeReadResult(w, r, response, err)
}

func (s *Server) getCostIssue(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeReadInvalidRequest(w, r)
		return
	}
	response, err := s.read.GetCostIssue(
		r.Context(),
		r.PathValue("id"),
	)
	writeReadResult(w, r, response, err)
}
