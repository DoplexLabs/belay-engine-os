package readmodel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

func TestPresentIssueDetailV2IsPrivacyClosed(t *testing.T) {
	code := "tamper.guardrails_off"
	detail := IssueDetail{
		SchemaVersion:     SchemaVersion,
		ProjectionVersion: IssueProjectionVersion,
		Data: IssueDetailData{
			Issue: model.IssueSummary{IssueID: testIssueID("a")},
			Occurrences: []model.IssueOccurrence{{
				OccurrenceID:       "occurrence-1",
				IssueID:            testIssueID("a"),
				FingerprintID:      testFingerprintID("b"),
				FingerprintVersion: "1",
				Origin:             "numbat",
				OriginRecordID:     "PRIVATE_ORIGIN_RECORD_CANARY",
				SessionID:          "session-1",
				Harness:            "codex",
				Provenance: model.DetectorProvenance{
					DetectorID:         "numbat_finding",
					DetectorVersion:    "rule-b05e244762b1",
					FingerprintVersion: "1",
					ProjectionVersion:  "1",
				},
				Category:           "numbat_finding",
				TitleCode:          "issue.numbat_finding",
				SourceSignalCode:   &code,
				Severity:           "low",
				Confidence:         "high",
				ScopeQuality:       model.ScopeUnscoped,
				FirstObservedAt:    time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
				LastObservedAt:     time.Date(2026, 9, 8, 11, 1, 0, 0, time.UTC),
				AnalysisGeneration: 99,
				Evidence: model.IssueEvidence{
					CitedEventIDs: []string{testEventID},
					Dimensions:    []string{"PRIVATE_DIMENSION_CANARY"},
				},
			}},
		},
	}
	presented := PresentIssueDetailV2(detail)
	body, err := json.Marshal(presented)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{
		"origin_record_id",
		"analysis_generation",
		"dimensions",
		"PRIVATE_ORIGIN_RECORD_CANARY",
		"PRIVATE_DIMENSION_CANARY",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("v2 leaked %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{
		`"projection_version":"belay.issue.v2"`,
		`"fingerprint_id":"` + testFingerprintID("b") + `"`,
		`"detector_id":"numbat_finding"`,
		`"cited_event_ids":["` + testEventID + `"]`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("v2 omitted %q: %s", required, text)
		}
	}
}
