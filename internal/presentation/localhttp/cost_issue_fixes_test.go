package localhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
)

type costIssueFixHTTPService struct {
	proposeIssueID string
	proposeKind    string
	proposeTarget  string
	recordFixID    string
}

func (s *costIssueFixHTTPService) ProposeFix(
	_ context.Context,
	issueID, kind, targetFile string,
) (issueintel.FixRecord, error) {
	s.proposeIssueID = issueID
	s.proposeKind = kind
	s.proposeTarget = targetFile
	return issueintel.FixRecord{
		FixID:       "fix_http",
		IssueID:     issueID,
		Kind:        kind,
		TargetFile:  targetFile,
		UnifiedDiff: "--- a/AGENTS.md\n+++ b/AGENTS.md\n",
		State:       "proposed",
	}, nil
}

func (s *costIssueFixHTTPService) RecordApplied(
	_ context.Context,
	fixID, filePath, contentSHA256, gitCommit string,
) (issueintel.FixRecord, error) {
	s.recordFixID = fixID
	return issueintel.FixRecord{
		FixID:         fixID,
		AppliedPath:   filePath,
		ContentSHA256: contentSHA256,
		GitCommit:     gitCommit,
		State:         "applied",
	}, nil
}

func (s *costIssueFixHTTPService) Status(
	_ context.Context,
	fixID string,
) (issueintel.FixStatus, error) {
	return issueintel.FixStatus{
		Fix: issueintel.FixRecord{
			FixID: fixID,
			State: "applied",
		},
		Applied:           true,
		VerificationState: "deferred",
	}, nil
}

func TestCostIssueFixHTTPWrapsSharedServiceAndProtectsWrites(t *testing.T) {
	service := &costIssueFixHTTPService{}
	server, err := New(
		readmodel.New(testRepository{}),
		"launch-secret",
		WithCostIssueFixService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.handler("127.0.0.1:43123")
	proposalRequest := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1:43123/v1/cost-issues/csi_http/fixes",
		bytes.NewBufferString(
			`{"kind":"harness_rule","target_file":"AGENTS.md"}`,
		),
	)
	authorizeCostFixRequest(proposalRequest, "propose-cost-issue-fix.v1")
	proposalResponse := httptest.NewRecorder()
	handler.ServeHTTP(proposalResponse, proposalRequest)
	if proposalResponse.Code != http.StatusOK {
		t.Fatalf(
			"proposal status/body = %d/%s",
			proposalResponse.Code,
			proposalResponse.Body,
		)
	}
	var proposal costIssueFixResponse
	if err := json.NewDecoder(proposalResponse.Body).Decode(&proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.SchemaVersion != costIssueFixSchemaVersion ||
		proposal.Data.FixID != "fix_http" ||
		service.proposeIssueID != "csi_http" ||
		service.proposeKind != "harness_rule" ||
		service.proposeTarget != "AGENTS.md" {
		t.Fatalf("proposal/service = %+v/%+v", proposal, service)
	}

	statusRequest := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1:43123/v1/cost-fixes/fix_http",
		nil,
	)
	statusRequest.Header.Set("Authorization", "Bearer launch-secret")
	statusRequest.RemoteAddr = "127.0.0.1:1234"
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf(
			"status response = %d/%s",
			statusResponse.Code,
			statusResponse.Body,
		)
	}

	forbidden := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1:43123/v1/cost-issues/csi_http/fixes",
		bytes.NewBufferString(
			`{"kind":"harness_rule","target_file":"AGENTS.md"}`,
		),
	)
	forbidden.Header.Set("Authorization", "Bearer launch-secret")
	forbidden.Header.Set("Content-Type", "application/json")
	forbidden.RemoteAddr = "127.0.0.1:1234"
	forbiddenResponse := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf("unprotected write status = %d", forbiddenResponse.Code)
	}
}

func authorizeCostFixRequest(request *http.Request, intent string) {
	request.Header.Set("Authorization", "Bearer launch-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:43123")
	request.Header.Set("X-Belay-Intent", intent)
	request.Header.Set(
		"Idempotency-Key",
		"12345678-1234-4234-9234-123456789abc",
	)
	request.RemoteAddr = "127.0.0.1:1234"
}
