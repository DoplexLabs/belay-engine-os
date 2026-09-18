package localmcp

import (
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

func TestCostIssueToolsReturnRankedIssuesAndVerbatimExcerpts(t *testing.T) {
	session := newTestClient(t, &testRepository{})
	top := callTool(t, session, "get_top_issues", map[string]any{
		"limit": 5,
	})
	if top.IsError {
		t.Fatalf("get_top_issues failed: %v", top.Content)
	}
	topReadmodel := asObject(
		t,
		asObject(t, top.StructuredContent)["readmodel"],
	)
	data, ok := topReadmodel["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("top issue data = %#v", topReadmodel["data"])
	}
	issue := asObject(t, data[0])
	if issue["issue_id"] != "csi_test" {
		t.Fatalf("top issue = %#v", issue)
	}

	excerpts := callTool(t, session, "get_issue_excerpts", map[string]any{
		"issue_id": "csi_test",
	})
	if excerpts.IsError {
		t.Fatalf("get_issue_excerpts failed: %v", excerpts.Content)
	}
	excerptReadmodel := asObject(
		t,
		asObject(t, excerpts.StructuredContent)["readmodel"],
	)
	values, ok := excerptReadmodel["excerpts"].([]any)
	if !ok || len(values) != 2 ||
		asObject(t, values[1])["text"] != "FAIL package/example" {
		t.Fatalf("issue excerpts = %#v", excerptReadmodel)
	}
}

func TestCostIssueFixToolsProposeRecordAndReportDeferredStatus(t *testing.T) {
	fixService := &testCostIssueFixService{
		proposed: issueintel.FixRecord{
			FixID:       "fix_proposed",
			IssueID:     "csi_test",
			Kind:        "harness_rule",
			TargetFile:  "AGENTS.md",
			UnifiedDiff: "--- a/AGENTS.md\n+++ b/AGENTS.md\n",
			State:       "proposed",
		},
		applied: issueintel.FixRecord{
			FixID:         "fix_proposed",
			State:         "applied",
			AppliedPath:   "AGENTS.md",
			ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		status: issueintel.FixStatus{
			Fix: issueintel.FixRecord{
				FixID: "fix_proposed",
				State: "applied",
			},
			Applied:           true,
			VerificationState: "deferred",
		},
	}
	session := newTestClientWithFixService(
		t,
		&testRepository{},
		fixService,
	)
	proposed := callTool(t, session, "propose_fix", map[string]any{
		"issue_id":    "csi_test",
		"kind":        "harness_rule",
		"target_file": "AGENTS.md",
	})
	if proposed.IsError {
		t.Fatalf("propose_fix failed: %v", proposed.Content)
	}
	proposedRecord := asObject(
		t,
		asObject(t, proposed.StructuredContent)["readmodel"],
	)
	if proposedRecord["fix_id"] != "fix_proposed" ||
		proposedRecord["unified_diff"] == "" {
		t.Fatalf("proposed fix = %#v", proposedRecord)
	}

	applied := callTool(t, session, "record_fix_applied", map[string]any{
		"fix_id":         "fix_proposed",
		"file_path":      "AGENTS.md",
		"content_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	if applied.IsError {
		t.Fatalf("record_fix_applied failed: %v", applied.Content)
	}

	status := callTool(t, session, "get_fix_status", map[string]any{
		"fix_id": "fix_proposed",
	})
	if status.IsError {
		t.Fatalf("get_fix_status failed: %v", status.Content)
	}
	statusRecord := asObject(
		t,
		asObject(t, status.StructuredContent)["readmodel"],
	)
	if statusRecord["verification_state"] != "deferred" ||
		statusRecord["applied"] != true {
		t.Fatalf("fix status = %#v", statusRecord)
	}
}

func TestCostIssueFixToolsRejectUnboundedInputs(t *testing.T) {
	session := newTestClient(t, &testRepository{})
	result := callTool(t, session, "propose_fix", map[string]any{
		"issue_id":    "csi_test",
		"kind":        "harness_rule",
		"target_file": string(make([]byte, 4097)),
	})
	if !result.IsError {
		t.Fatal("propose_fix accepted an oversized target")
	}
	result = callTool(t, session, "record_fix_applied", map[string]any{
		"fix_id":         "fix_proposed",
		"file_path":      "AGENTS.md",
		"content_sha256": "short",
	})
	if !result.IsError {
		t.Fatal("record_fix_applied accepted a short content hash")
	}
}
