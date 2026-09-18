package trajectory

import (
	"strings"
	"testing"
	"time"
)

func TestNodeRefRequiresExactlyOneSupportedIdentity(t *testing.T) {
	turn := int64(4)
	valid := []NodeRef{
		{Kind: NodeCanonicalEvent, EventID: "evt_1"},
		{Kind: NodeTranscriptTurn, SessionKey: "ses_1", TurnIndex: &turn},
		{Kind: NodeOutcome, OutcomeID: "out_1"},
		{Kind: NodeExperience, ExperienceID: "exp_1", ExperienceVersion: 1},
		{Kind: NodeApplication, ApplicationID: "exa_1"},
		{Kind: NodeGitObject, GitObject: strings.Repeat("a", 40)},
	}
	for _, ref := range valid {
		if err := ref.Validate(); err != nil {
			t.Errorf("%s node rejected: %v", ref.Kind, err)
		}
	}

	invalid := []NodeRef{
		{Kind: NodeCanonicalEvent, EventID: "evt_1", OutcomeID: "out_1"},
		{Kind: NodeTranscriptTurn, SessionKey: "ses_1"},
		{Kind: NodeExperience, ExperienceID: "exp_1"},
		{Kind: NodeGitObject, GitObject: "not-a-hash"},
	}
	for _, ref := range invalid {
		if err := ref.Validate(); err == nil {
			t.Errorf("invalid %s node was accepted: %+v", ref.Kind, ref)
		}
	}
}

func TestEdgeDeterministicIDAndEvidenceClassRemainDistinct(t *testing.T) {
	first := validEdge()
	second := first
	second.SourceRefs = []NodeRef{first.SourceRefs[1], first.SourceRefs[0]}
	second.EdgeID = second.DeterministicID()

	if first.EdgeID != second.EdgeID {
		t.Fatalf("source-ref ordering changed deterministic ID:\n%s\n%s", first.EdgeID, second.EdgeID)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("valid edge rejected: %v", err)
	}

	inferred := first
	inferred.EvidenceClass = EvidenceDeterministicInference
	inferred.EdgeID = inferred.DeterministicID()
	if inferred.EdgeID == first.EdgeID {
		t.Fatal("observed and inferred edges produced the same ID")
	}
}

func TestSemanticHypothesisCannotFeedDeterministicVerifier(t *testing.T) {
	edge := validEdge()
	edge.EvidenceClass = EvidenceSemanticHypothesis
	edge.EdgeID = edge.DeterministicID()
	if err := edge.Validate(); err != nil {
		t.Fatalf("semantic edge should remain valid trajectory evidence: %v", err)
	}

	err := ValidateDeterministicVerifierInputs([]Edge{edge}, nil)
	if err == nil || !strings.Contains(err.Error(), "semantic hypothesis") {
		t.Fatalf("deterministic verifier input error = %v, want semantic-hypothesis rejection", err)
	}

	edge.EvidenceClass = EvidenceObserved
	edge.EdgeID = edge.DeterministicID()
	if err := ValidateDeterministicVerifierInputs([]Edge{edge}, nil); err != nil {
		t.Fatalf("observed edge rejected as verifier input: %v", err)
	}
}

func TestOutcomeMustBeDeterministicAndTraceable(t *testing.T) {
	outcome := validOutcome()
	if err := outcome.Validate(); err != nil {
		t.Fatalf("valid outcome rejected: %v", err)
	}

	semantic := outcome
	semantic.EvidenceClass = EvidenceSemanticHypothesis
	semantic.OutcomeID = semantic.DeterministicID()
	if err := semantic.Validate(); err == nil || !strings.Contains(err.Error(), "observed or deterministic") {
		t.Fatalf("semantic outcome error = %v, want deterministic evidence rejection", err)
	}

	missingSources := outcome
	missingSources.SourceRefs = nil
	missingSources.OutcomeID = missingSources.DeterministicID()
	if err := missingSources.Validate(); err == nil || !strings.Contains(err.Error(), "source references") {
		t.Fatalf("missing source error = %v, want traceability rejection", err)
	}
}

func validEdge() Edge {
	turnOne := int64(1)
	turnTwo := int64(2)
	value := Edge{
		SchemaVersion:     EdgeSchemaVersion,
		ProjectIdentity:   "git@example.test:doplexlabs/belay-engine.git",
		SessionKey:        "ses_test",
		From:              NodeRef{Kind: NodeTranscriptTurn, SessionKey: "ses_test", TurnIndex: &turnOne},
		Relation:          RelationRespondsTo,
		To:                NodeRef{Kind: NodeTranscriptTurn, SessionKey: "ses_test", TurnIndex: &turnTwo},
		EvidenceClass:     EvidenceObserved,
		Confidence:        ConfidenceHigh,
		DerivationVersion: "trajectory.det.v1",
		SourceRefs: []NodeRef{
			{Kind: NodeTranscriptTurn, SessionKey: "ses_test", TurnIndex: &turnOne},
			{Kind: NodeTranscriptTurn, SessionKey: "ses_test", TurnIndex: &turnTwo},
		},
		OccurredAt: time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC),
	}
	value.EdgeID = value.DeterministicID()
	return value
}

func validOutcome() Outcome {
	turn := int64(8)
	value := Outcome{
		SchemaVersion:     OutcomeSchemaVersion,
		ProjectIdentity:   "git@example.test:doplexlabs/belay-engine.git",
		SessionKey:        "ses_test",
		OccurredAt:        time.Date(2026, 9, 10, 11, 5, 0, 0, time.UTC),
		Kind:              OutcomeVerificationPass,
		Result:            ResultSucceeded,
		EvidenceClass:     EvidenceObserved,
		Confidence:        ConfidenceHigh,
		SourceRefs:        []NodeRef{{Kind: NodeTranscriptTurn, SessionKey: "ses_test", TurnIndex: &turn}},
		DerivationVersion: "outcome.det.v1",
	}
	value.OutcomeID = value.DeterministicID()
	return value
}
