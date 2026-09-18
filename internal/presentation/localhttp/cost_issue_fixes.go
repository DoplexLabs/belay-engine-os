package localhttp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

const costIssueFixSchemaVersion = "belay.cost-issue-fix.v1"

type costIssueFixResponse struct {
	SchemaVersion string               `json:"schema_version"`
	Data          issueintel.FixRecord `json:"data"`
}

type costIssueFixStatusResponse struct {
	SchemaVersion string               `json:"schema_version"`
	Data          issueintel.FixStatus `json:"data"`
}

func (s *Server) registerCostIssueFixRoutes(
	mux *http.ServeMux,
	trustedListener string,
) {
	mux.Handle(
		"POST /v1/cost-issues/{id}/fixes",
		s.authorizeWrite(s.protectWrite(
			trustedListener,
			"propose-cost-issue-fix.v1",
			http.HandlerFunc(s.proposeCostIssueFix),
		)),
	)
	mux.Handle(
		"GET /v1/cost-fixes/{fix_id}",
		s.authorize(http.HandlerFunc(s.getCostIssueFixStatus)),
	)
	mux.Handle(
		"POST /v1/cost-fixes/{fix_id}/applied",
		s.authorizeWrite(s.protectWrite(
			trustedListener,
			"record-cost-issue-fix.v1",
			http.HandlerFunc(s.recordCostIssueFixApplied),
		)),
	)
}

func (s *Server) proposeCostIssueFix(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeInvalidRequest(w, r)
		return
	}
	issueID := strings.TrimSpace(r.PathValue("id"))
	if issueID == "" || len(issueID) > 512 {
		writeInvalidRequest(w, r)
		return
	}
	object, err := decodeStrictObject(r, "kind", "target_file")
	if err != nil {
		writeInvalidRequest(w, r)
		return
	}
	kind, kindOK := requiredString(object, "kind")
	targetFile, targetOK := requiredString(object, "target_file")
	if !kindOK || !targetOK || len(kind) > 128 || len(targetFile) > 4096 {
		writeInvalidRequest(w, r)
		return
	}
	record, err := s.costFix.ProposeFix(
		r.Context(),
		issueID,
		kind,
		targetFile,
	)
	if err != nil {
		writeCostIssueFixError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, costIssueFixResponse{
		SchemaVersion: costIssueFixSchemaVersion,
		Data:          record,
	})
}

func (s *Server) getCostIssueFixStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeInvalidRequest(w, r)
		return
	}
	fixID := strings.TrimSpace(r.PathValue("fix_id"))
	if fixID == "" || len(fixID) > 512 {
		writeInvalidRequest(w, r)
		return
	}
	status, err := s.costFix.Status(r.Context(), fixID)
	if err != nil {
		writeCostIssueFixError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, costIssueFixStatusResponse{
		SchemaVersion: costIssueFixSchemaVersion,
		Data:          status,
	})
}

func (s *Server) recordCostIssueFixApplied(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.URL.RawQuery != "" {
		writeInvalidRequest(w, r)
		return
	}
	fixID := strings.TrimSpace(r.PathValue("fix_id"))
	if fixID == "" || len(fixID) > 512 {
		writeInvalidRequest(w, r)
		return
	}
	object, err := decodeStrictObject(
		r,
		"file_path",
		"content_sha256",
		"git_commit",
	)
	if err != nil {
		writeInvalidRequest(w, r)
		return
	}
	filePath, pathOK := requiredString(object, "file_path")
	contentSHA256, hashOK := requiredString(object, "content_sha256")
	var gitCommit string
	if raw, ok := object["git_commit"]; !ok ||
		json.Unmarshal(raw, &gitCommit) != nil {
		writeInvalidRequest(w, r)
		return
	}
	gitCommit = strings.TrimSpace(gitCommit)
	if !pathOK || !hashOK ||
		len(filePath) > 4096 ||
		len(contentSHA256) != 64 ||
		len(gitCommit) > 256 {
		writeInvalidRequest(w, r)
		return
	}
	record, err := s.costFix.RecordApplied(
		r.Context(),
		fixID,
		filePath,
		contentSHA256,
		gitCommit,
	)
	if err != nil {
		writeCostIssueFixError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, costIssueFixResponse{
		SchemaVersion: costIssueFixSchemaVersion,
		Data:          record,
	})
}

func writeCostIssueFixError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	if errors.Is(err, sql.ErrNoRows) {
		writeProblemType(
			w,
			r,
			http.StatusNotFound,
			"belay.local/cost-fix-not-found",
			"Fix not found",
			"The requested local issue or fix was not found.",
		)
		return
	}
	writeProblemType(
		w,
		r,
		http.StatusUnprocessableEntity,
		"belay.local/cost-fix-rejected",
		"Fix rejected",
		"Belay Local could not create or record that bounded configuration fix.",
	)
}
