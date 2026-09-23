package numbat

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/limits"
)

func TestParseFindingCitedEventIDLimit(t *testing.T) {
	record := validFindingRecordForLimitTest()
	record["cited_event_ids"] = findingCitationIDs(limits.MaxFindingCitedEventIDs)
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseLine(body); err != nil {
		t.Fatalf("ParseLine(at limit) error = %v", err)
	}

	record["cited_event_ids"] = findingCitationIDs(limits.MaxFindingCitedEventIDs + 1)
	body, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ParseLine(body)
	var parseErr *ParseError
	if err == nil || !errors.As(err, &parseErr) {
		t.Fatalf("ParseLine(over limit) error = %v, want ParseError", err)
	}
	if parseErr.Category != IssueInvalidRecord ||
		parseErr.Reason != "finding cited_event_ids exceeds limit" {
		t.Fatalf("ParseLine(over limit) = %+v", parseErr)
	}
}

func TestParseSessionLinkV040(t *testing.T) {
	body := []byte(`{
		"schema_version":"0.4.0",
		"record_type":"session_link",
		"run_id":"run-lineage",
		"endpoint":{"os":"darwin","arch":"arm64"},
		"link_id":"sl-e12b40b1ba4c8c398f955732be9cd2f9",
		"source_agent":"claude-code",
		"left":{"namespace":"hook","session_id":"hook-session"},
		"right":{"namespace":"artifact","session_id":"artifact-session"},
		"relationship":"hook_artifact_alias",
		"confidence":"high",
		"source_refs":["hook_payload:artifact_path"]
	}`)
	record, err := ParseLine(body)
	if err != nil {
		t.Fatal(err)
	}
	link, ok := record.(SessionLinkRecord)
	if !ok || link.Right.SessionID != "artifact-session" {
		t.Fatalf("record = %#v", record)
	}
}

func validFindingRecordForLimitTest() map[string]any {
	return map[string]any{
		"schema_version":  SchemaVersion,
		"record_type":     "finding",
		"run_id":          "run-limit",
		"endpoint":        map[string]any{"os": "darwin", "arch": "arm64"},
		"finding_id":      "finding-limit",
		"detected_at":     "2026-09-08T18:00:00Z",
		"rule_id":         "rule.limit",
		"rule_version":    "1",
		"severity":        "low",
		"source_agent":    "codex",
		"source_type":     "artifact",
		"title":           "Bounded finding",
		"evidence_refs":   []any{map[string]any{"artifact_type": "codex_rollout"}},
		"cited_event_ids": []string{"event-000"},
		"redacted":        true,
		"confidence":      "high",
	}
}

func findingCitationIDs(count int) []string {
	result := make([]string, count)
	for index := range result {
		result[index] = fmt.Sprintf("event-%03d", index)
	}
	return result
}
