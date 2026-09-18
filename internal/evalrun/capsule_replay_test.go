package evalrun

import (
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestCompletePrivateEvalCapsuleRequiresReproducibleDeterministicReplay(t *testing.T) {
	draft := PrivateEvalCapsule{
		SchemaVersion: PrivateEvalCapsuleSchemaVersion,
		CapsuleID:     "evc_draft",
		Readiness:     "draft",
		ReadinessGaps: []string{"task_setup_missing"},
		Evaluation: CapsuleEvaluationContract{
			TaskState: "reconstruction_required",
		},
	}
	replay := CapsuleReplayContract{
		SchemaVersion: CapsuleReplaySchemaVersion,
		Source: CapsuleReplaySource{
			Kind:       "git_revision",
			Repository: "https://example.com/project.git",
			Revision:   "0123456789abcdef0123456789abcdef01234567",
		},
		Task: CapsuleReplayTask{
			Prompt:           "Make the bounded change and verify it.",
			WorkingDirectory: ".",
			SetupCommands:    []string{"go mod download"},
			AllowedMutationPaths: []string{
				"internal/example",
			},
		},
		Outcome: CapsuleOutcomeContract{
			Kind:             "command_succeeded",
			Command:          "go test ./internal/example",
			ExpectedExitCode: 0,
		},
		TreatmentVerifier: experience.Verifier{
			Kind: experience.VerifierCommandSucceeded,
			Command: &experience.CommandVerifierSpec{
				Command:          "go test ./internal/example",
				CommandClass:     "go_test",
				ScrubbingVersion: "belay.redaction.v1",
			},
		},
		Isolation: CapsuleReplayIsolation{
			Kind:             "disposable_git_worktree",
			ResetBetweenRuns: true,
		},
	}

	completed, err := CompletePrivateEvalCapsule(draft, replay)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Readiness != "executable" ||
		len(completed.ReadinessGaps) != 0 ||
		completed.Evaluation.TaskState != "reconstructed" ||
		completed.Replay == nil ||
		completed.CapsuleID == draft.CapsuleID {
		t.Fatalf("completed capsule = %+v", completed)
	}

	replay.Source.Revision = "short"
	if _, err := CompletePrivateEvalCapsule(draft, replay); err == nil {
		t.Fatal("CompletePrivateEvalCapsule() accepted an inexact revision")
	}
}

func TestCompletePrivateEvalCapsuleRejectsObservationOnlyTreatment(t *testing.T) {
	draft := PrivateEvalCapsule{
		SchemaVersion: PrivateEvalCapsuleSchemaVersion,
		CapsuleID:     "evc_draft",
		Readiness:     "draft",
	}
	replay := CapsuleReplayContract{
		SchemaVersion: CapsuleReplaySchemaVersion,
		Source: CapsuleReplaySource{
			Kind:       "git_revision",
			Repository: "https://example.com/project.git",
			Revision:   "0123456789abcdef0123456789abcdef01234567",
		},
		Task: CapsuleReplayTask{
			Prompt:           "Explain the result.",
			WorkingDirectory: ".",
		},
		Outcome: CapsuleOutcomeContract{
			Kind:             "command_succeeded",
			Command:          "go test ./...",
			ExpectedExitCode: 0,
		},
		TreatmentVerifier: experience.Verifier{
			Kind: experience.VerifierObservationOnly,
			ObservationOnly: &experience.ObservationOnlySpec{
				Explanation: "Judge whether the response seems better.",
			},
		},
		Isolation: CapsuleReplayIsolation{
			Kind:             "disposable_git_worktree",
			ResetBetweenRuns: true,
		},
	}
	if _, err := CompletePrivateEvalCapsule(draft, replay); err == nil {
		t.Fatal("CompletePrivateEvalCapsule() accepted observation-only treatment")
	}
}
