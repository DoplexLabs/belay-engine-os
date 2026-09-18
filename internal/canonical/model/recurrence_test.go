package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFixRecurrenceCatalogsAndSnapshot(t *testing.T) {
	for _, state := range []string{
		FixRecurrenceMatchingEvidence,
		FixRecurrenceMonitoringIncomplete,
		FixRecurrenceAwaitingEvidence,
		FixRecurrenceNoLaterMatch,
		FixRecurrenceComparisonUnavailable,
		FixRecurrenceRetracted,
	} {
		if !ValidFixRecurrenceState(state) {
			t.Fatalf("valid recurrence state rejected: %q", state)
		}
	}
	if ValidFixRecurrenceState("verified_fixed") {
		t.Fatal("unsupported remediation claim accepted")
	}
	for _, state := range []string{
		FixRecurrenceEvidenceAvailable,
		FixRecurrenceEvidencePartial,
		FixRecurrenceEvidencePruned,
		FixRecurrenceEvidenceUnknown,
	} {
		if !ValidFixEvidenceState(state) {
			t.Fatalf("valid evidence state rejected: %q", state)
		}
	}
	if !(FixMonitoringSnapshot{}).Empty() {
		t.Fatal("zero snapshot was not empty")
	}
	if (FixMonitoringSnapshot{IssuedAt: time.Now()}).Empty() {
		t.Fatal("issued snapshot was empty")
	}
}

func TestFixRecurrenceInternalScopeAndOrderNeverSerialize(t *testing.T) {
	order := int64(123456789)
	value := struct {
		Occurrence IssueOccurrence      `json:"occurrence"`
		Subject    FixMonitoringSubject `json:"subject"`
		Capability AnalysisCapability   `json:"capability"`
	}{
		Occurrence: IssueOccurrence{FingerprintScopeID: "psc_private"},
		Subject: FixMonitoringSubject{
			FingerprintScopeID: "psc_private",
			MonitorFromOrderNS: &order,
		},
		Capability: AnalysisCapability{
			FingerprintScopeID:     "psc_private",
			AnalysisThroughOrderNS: &order,
		},
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{
		"psc_private",
		"fingerprint_scope",
		"monitor_from_order",
		"analysis_through_order",
		"123456789",
	} {
		if strings.Contains(string(body), prohibited) {
			t.Fatalf("internal recurrence material serialized: %s", body)
		}
	}
}
