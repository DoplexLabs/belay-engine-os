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
