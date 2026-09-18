package evalrun

import (
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

func TestBuildPrivateEvalCapsuleBindsEvidenceAndRemainsNonAuthoritative(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	turn := int64(4)
	confidence := 0.8
	review := localapp.ExperienceCandidateReview{
		SchemaVersion:    localapp.ExperienceCandidateReviewSchemaVersion,
		CandidateID:      "exc_private_eval",
		ProposalID:       "exs_private_eval",
		ProjectIdentity:  "https://example.com/project.git",
		Family:           experience.CandidateFailedApproach,
		ObservedBehavior: "A failed command was followed by a successful repair.",
		Proposed: experience.ExperienceProposal{
			Type: experience.ExperienceWarning,
			Scope: experience.Scope{
				Kind:            experience.ScopeProject,
				ProjectIdentity: "https://example.com/project.git",
			},
			Applicability: experience.Applicability{
				SemanticDescription: "When running the affected verification task.",
			},
			Guidance: experience.Guidance{
				Instruction:          "Use the repaired command.",
				Rationale:            "The original command failed.",
				InterventionStrength: experience.InterventionAdvise,
			},
			Verifier: experience.Verifier{
				Kind: experience.VerifierObservationOnly,
				ObservationOnly: &experience.ObservationOnlySpec{
					Explanation: "Observe the command and result.",
				},
			},
			Confidence: &confidence,
		},
		Evidence: []experience.EvidenceRef{{
			Kind:       experience.EvidenceTranscriptTurn,
			SessionKey: "ses_private_eval",
			TurnIndex:  &turn,
			Excerpt:    "command failed, then repair passed",
			OccurredAt: &now,
		}},
		Outcomes: []trajectory.Outcome{{
			SchemaVersion:     trajectory.OutcomeSchemaVersion,
			OutcomeID:         "out_private_eval",
			ProjectIdentity:   "https://example.com/project.git",
			SessionKey:        "ses_private_eval",
			Kind:              trajectory.OutcomeRepair,
			Result:            trajectory.ResultSucceeded,
			EvidenceClass:     trajectory.EvidenceDeterministicInference,
			Confidence:        trajectory.ConfidenceHigh,
			OccurredAt:        now,
			SourceRefs:        []trajectory.NodeRef{{Kind: trajectory.NodeTranscriptTurn, SessionKey: "ses_private_eval", TurnIndex: &turn}},
			DerivationVersion: "belay.trajectory-derive.v4",
		}},
		SemanticProvenance: experience.SemanticProposalProvenance{
			Harness:       experience.HarnessCodex,
			Model:         "test-model",
			PromptVersion: localapp.ExperiencePromptVersion,
			InputHash:     "semantic-input-hash",
			OutputHash:    "semantic-output-hash",
			GeneratedAt:   now,
		},
		ProposedContentHash: "proposed-content-hash",
		SemanticInputHash:   "semantic-input-hash",
		EvidenceGeneration:  "evidence-generation-hash",
		Authority:           experience.AuthorityNone,
	}

	capsule, err := BuildPrivateEvalCapsule(review, now)
	if err != nil {
		t.Fatal(err)
	}
	if capsule.CapsuleID == "" ||
		capsule.Readiness != "draft" ||
		len(capsule.ReadinessGaps) != 4 ||
		capsule.Provenance.InstructionAuthority != string(experience.AuthorityNone) ||
		capsule.Evaluation.LLMJudgeAuthority != "none" ||
		!capsule.Evaluation.NegativeResultsPreserved ||
		len(capsule.SourceSessions) != 1 ||
		capsule.SourceSessions[0] != "ses_private_eval" {
		t.Fatalf("capsule = %+v", capsule)
	}
	replay, err := BuildPrivateEvalCapsule(review, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if replay.CapsuleID != capsule.CapsuleID {
		t.Fatalf("capsule ID changed with creation time: %q != %q", replay.CapsuleID, capsule.CapsuleID)
	}
}

func TestBuildPrivateEvalCapsuleRejectsGrantedAuthority(t *testing.T) {
	review := localapp.ExperienceCandidateReview{
		SchemaVersion:   localapp.ExperienceCandidateReviewSchemaVersion,
		CandidateID:     "exc_private_eval",
		ProposalID:      "exs_private_eval",
		ProjectIdentity: "https://example.com/project.git",
		Family:          experience.CandidateCorrection,
		Authority:       experience.AuthorityUserApproved,
	}
	if _, err := BuildPrivateEvalCapsule(review, time.Now()); err == nil {
		t.Fatal("BuildPrivateEvalCapsule() accepted an authoritative candidate")
	}
}
