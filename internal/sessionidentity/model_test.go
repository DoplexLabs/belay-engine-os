package sessionidentity

import (
	"testing"
	"time"
)

func TestAuditObservationsUsesExactNativeIdentityOnly(t *testing.T) {
	values := []Observation{
		mustObservation(t, Observation{
			SourceKind:       SourceTranscript,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_transcript",
			NativeNamespace:  "codex_transcript",
			NativeSessionID:  "native-shared",
			Coverage:         CoverageComplete,
		}),
		mustObservation(t, Observation{
			SourceKind:       SourceNumbatArtifact,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_artifact",
			NativeNamespace:  "numbat_artifact",
			NativeSessionID:  "native-shared",
			Coverage:         CoverageObserved,
		}),
		mustObservation(t, Observation{
			SourceKind:       SourceTranscript,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_partial",
			NativeNamespace:  "codex_transcript",
			NativeSessionID:  "native-partial",
			Coverage:         CoveragePartial,
		}),
		mustObservation(t, Observation{
			SourceKind:       SourceNumbatHook,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_hook",
			NativeNamespace:  "numbat_hook",
			NativeSessionID:  "native-hook",
			Coverage:         CoverageObserved,
		}),
	}
	audit := AuditObservations(values)
	if audit.ExactNativeMatches != 1 ||
		audit.TranscriptOnly != 1 ||
		audit.EventOnly != 1 ||
		audit.Reasons[ReasonTranscriptPartial] != 1 ||
		audit.Reasons[ReasonHookNativeIDNotArtifactID] != 1 {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestObservationIdentityIsStableAcrossAliasCorrection(t *testing.T) {
	first := mustObservation(t, Observation{
		SourceKind:       SourceNumbatHook,
		SourceAgent:      "codex",
		SourceSessionKey: "ses_hook",
		NativeNamespace:  "numbat_hook",
		NativeSessionID:  "alias-before",
		Coverage:         CoverageObserved,
	})
	corrected := mustObservation(t, Observation{
		SourceKind:       SourceNumbatHook,
		SourceAgent:      "codex",
		SourceSessionKey: "ses_hook",
		NativeNamespace:  "numbat_hook",
		NativeSessionID:  "alias-after",
		Coverage:         CoverageObserved,
	})
	if first.ObservationID != corrected.ObservationID {
		t.Fatalf(
			"observation IDs changed across alias correction: %q != %q",
			first.ObservationID,
			corrected.ObservationID,
		)
	}
	if first.NativeIDHash == corrected.NativeIDHash {
		t.Fatal("native identity hash did not change across alias correction")
	}
}

func TestAuditObservationsClassifiesStrictMismatchTaxonomy(t *testing.T) {
	values := []Observation{
		mustObservation(t, Observation{
			SourceKind:       SourceTranscript,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_project_transcript",
			NativeNamespace:  "codex_transcript",
			NativeSessionID:  "native-project",
			ProjectIdentity:  "project-a",
			Coverage:         CoverageComplete,
		}),
		mustObservation(t, Observation{
			SourceKind:       SourceNumbatArtifact,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_project_artifact",
			NativeNamespace:  "numbat_artifact",
			NativeSessionID:  "native-project",
			ProjectIdentity:  "project-b",
			Coverage:         CoverageObserved,
		}),
		mustObservation(t, Observation{
			SourceKind:       SourceTranscript,
			SourceAgent:      "claude",
			SourceSessionKey: "ses_agent_transcript",
			NativeNamespace:  "claude_transcript",
			NativeSessionID:  "native-agent-mismatch",
			Coverage:         CoverageComplete,
		}),
		mustObservation(t, Observation{
			SourceKind:       SourceNumbatHook,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_agent_hook",
			NativeNamespace:  "numbat_hook",
			NativeSessionID:  "native-agent-mismatch",
			Coverage:         CoverageObserved,
		}),
		mustObservation(t, Observation{
			SourceKind:            SourceTranscript,
			SourceAgent:           "claude",
			SourceSessionKey:      "ses_parent",
			NativeNamespace:       "claude_transcript",
			NativeSessionID:       "native-parent",
			ParentNativeSessionID: "native-parent-root",
			Coverage:              CoverageComplete,
		}),
		mustObservation(t, Observation{
			SourceKind:       "numbat_unknown",
			SourceAgent:      "codex",
			SourceSessionKey: "ses_unknown_source",
			NativeNamespace:  "numbat_unknown",
			NativeSessionID:  "native-unknown-source",
			Coverage:         CoverageObserved,
		}),
	}
	audit := AuditObservations(values)
	if audit.ExactNativeMatches != 0 ||
		audit.TranscriptOnly != 3 ||
		audit.EventOnly != 3 ||
		audit.ClassifiedMismatches != 6 ||
		audit.Reasons[ReasonProjectIdentityMismatch] != 2 ||
		audit.Reasons[ReasonAgentMismatch] != 2 ||
		audit.Reasons[ReasonParentSubagentUnresolved] != 1 ||
		audit.Reasons[ReasonUnsupportedSourceLineage] != 1 {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestResolveToolCallLinksRequiresUniqueExactEvidence(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	values := []SessionEvidence{
		{
			SourceKind:  SourceTranscript,
			SourceAgent: "codex",
			SessionKey:  "ses_transcript",
			ToolCallIDs: []string{"call-one", "call-two", "transcript-only"},
		},
		{
			SourceKind:  SourceNumbatHook,
			SourceAgent: "codex",
			SessionKey:  "ses_hook",
			ToolCallIDs: []string{"call-one", "call-two", "hook-only"},
		},
		{
			SourceKind:  SourceTranscript,
			SourceAgent: "claude",
			SessionKey:  "ses_single_match",
			ToolCallIDs: []string{"only-one"},
		},
		{
			SourceKind:  SourceNumbatHook,
			SourceAgent: "claude",
			SessionKey:  "ses_single_source",
			ToolCallIDs: []string{"only-one"},
		},
	}
	links := ResolveToolCallLinks(values, now)
	if len(links) != 1 {
		t.Fatalf("links = %+v, want one exact link", links)
	}
	link := links[0]
	if link.LeftSessionKey != "ses_transcript" ||
		link.RightSessionKey != "ses_hook" ||
		link.Basis != BasisSharedToolCalls ||
		len(link.SourceRefs) != 2 {
		t.Fatalf("link = %+v", link)
	}

	ambiguous := append(values,
		SessionEvidence{
			SourceKind:  SourceNumbatArtifact,
			SourceAgent: "codex",
			SessionKey:  "ses_artifact",
			ToolCallIDs: []string{"call-one", "call-two"},
		},
	)
	if links := ResolveToolCallLinks(ambiguous, now); len(links) != 0 {
		t.Fatalf("ambiguous links = %+v, want none", links)
	}
}

func TestResolveExactNativeLinksRejectsProjectMismatchAndAmbiguity(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	values := []Observation{
		mustObservation(t, Observation{
			SourceKind:       SourceTranscript,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_transcript",
			NativeNamespace:  "codex_transcript",
			NativeSessionID:  "native-exact",
			ProjectIdentity:  "project-a",
			Coverage:         CoverageComplete,
		}),
		mustObservation(t, Observation{
			SourceKind:       SourceNumbatArtifact,
			SourceAgent:      "codex",
			SourceSessionKey: "ses_artifact",
			NativeNamespace:  "numbat_artifact",
			NativeSessionID:  "native-exact",
			ProjectIdentity:  "project-a",
			Coverage:         CoverageObserved,
		}),
	}
	links := ResolveExactNativeLinks(values, now)
	if len(links) != 1 ||
		links[0].LeftSessionKey != "ses_transcript" ||
		links[0].RightSessionKey != "ses_artifact" ||
		links[0].Basis != BasisExactNativeID ||
		len(links[0].SourceRefs) != 2 {
		t.Fatalf("exact links = %+v", links)
	}
	covered := ExactNativeObservationIDs(values)
	if len(covered) != 2 {
		t.Fatalf("exact native coverage = %+v, want both observations", covered)
	}

	sameKey := append([]Observation(nil), values...)
	sameKey[1].SourceSessionKey = sameKey[0].SourceSessionKey
	sameKey[1].ObservationID = StableObservationID(
		sameKey[1].SourceKind,
		sameKey[1].SourceAgent,
		sameKey[1].SourceSessionKey,
		sameKey[1].NativeNamespace,
	)
	if links := ResolveExactNativeLinks(sameKey, now); len(links) != 0 {
		t.Fatalf("same-key exact links = %+v, want no redundant alias", links)
	}
	if covered := ExactNativeObservationIDs(sameKey); len(covered) != 2 {
		t.Fatalf("same-key exact coverage = %+v, want both observations", covered)
	}

	projectMismatch := append(
		[]Observation(nil),
		values...,
	)
	projectMismatch[1].ProjectIdentity = "project-b"
	if links := ResolveExactNativeLinks(projectMismatch, now); len(links) != 0 {
		t.Fatalf("project-mismatched links = %+v", links)
	}

	ambiguous := append(values, mustObservation(t, Observation{
		SourceKind:       SourceNumbatHook,
		SourceAgent:      "codex",
		SourceSessionKey: "ses_hook_alias",
		NativeNamespace:  "numbat_hook",
		NativeSessionID:  "native-exact",
		Coverage:         CoverageObserved,
	}))
	if links := ResolveExactNativeLinks(ambiguous, now); len(links) != 0 {
		t.Fatalf("ambiguous exact links = %+v", links)
	}
}

func mustObservation(t *testing.T, value Observation) Observation {
	t.Helper()
	result, err := NewObservation("store_test", value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
