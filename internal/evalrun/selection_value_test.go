package evalrun

import (
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
)

func TestBuildSelectionValuePlanBindsDistinctTransferTargets(t *testing.T) {
	source, pool, targets, forbidden := validSelectionValueInputs(t)
	createdAt := time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)

	plan, err := BuildSelectionValuePlan(
		source,
		pool,
		targets,
		forbidden,
		createdAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanID == "" ||
		plan.SourceCapsuleID != source.CapsuleID ||
		len(plan.ExperiencePool) != 4 ||
		len(plan.Targets) != 2 ||
		plan.ScoringAuthority != "deterministic_observation" ||
		plan.LLMJudgeAuthority != "none" ||
		!plan.NegativeResultsPreserved {
		t.Fatalf("selection value plan = %+v", plan)
	}
	replay, err := BuildSelectionValuePlan(
		source,
		pool,
		targets,
		forbidden,
		createdAt.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if replay.PlanID != plan.PlanID {
		t.Fatalf("plan ID changed with creation time: %q != %q", replay.PlanID, plan.PlanID)
	}
	for first, last := 0, len(pool)-1; first < last; first, last = first+1, last-1 {
		pool[first], pool[last] = pool[last], pool[first]
	}
	targets[0], targets[1] = targets[1], targets[0]
	reordered, err := BuildSelectionValuePlan(
		source,
		pool,
		targets,
		[]string{forbidden[1], forbidden[0]},
		createdAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reordered.PlanID != plan.PlanID {
		t.Fatalf("plan ID changed with input order: %q != %q", reordered.PlanID, plan.PlanID)
	}
}

func TestBuildSelectionValuePlanRejectsWeakSelectionClaims(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PrivateEvalCapsule, *[]SelectionValueCandidate, *[]SelectionValueTarget, *[]string)
		want   string
	}{
		{
			name: "fewer than three distractors",
			mutate: func(_ *PrivateEvalCapsule, pool *[]SelectionValueCandidate, _ *[]SelectionValueTarget, _ *[]string) {
				*pool = (*pool)[:3]
			},
			want: "at least three",
		},
		{
			name: "target duplicates source replay",
			mutate: func(source *PrivateEvalCapsule, _ *[]SelectionValueCandidate, targets *[]SelectionValueTarget, _ *[]string) {
				(*targets)[0].Replay = *source.Replay
			},
			want: "distinct from the source",
		},
		{
			name: "prompt leaks source procedure",
			mutate: func(_ *PrivateEvalCapsule, _ *[]SelectionValueCandidate, targets *[]SelectionValueTarget, forbidden *[]string) {
				(*targets)[0].Replay.Task.Prompt += " " + (*forbidden)[0]
				(*targets)[0].SelectionRequest.TaskHint =
					(*targets)[0].Replay.Task.Prompt
			},
			want: "leaks the source procedure",
		},
		{
			name: "source not relevant",
			mutate: func(source *PrivateEvalCapsule, pool *[]SelectionValueCandidate, targets *[]SelectionValueTarget, _ *[]string) {
				(*targets)[0].RelevantCandidateIDs = []string{(*pool)[1].CandidateID}
				_ = source
			},
			want: "source procedure relevant",
		},
		{
			name: "missing actual selection paths",
			mutate: func(_ *PrivateEvalCapsule, _ *[]SelectionValueCandidate, targets *[]SelectionValueTarget, _ *[]string) {
				(*targets)[0].SelectionRequest.RepositoryPaths = nil
			},
			want: "selection request requires",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, pool, targets, forbidden := validSelectionValueInputs(t)
			test.mutate(&source, &pool, &targets, &forbidden)
			_, err := BuildSelectionValuePlan(
				source,
				pool,
				targets,
				forbidden,
				time.Now(),
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildSelectionValuePlan() error = %v, want %q", err, test.want)
			}
		})
	}
}

func validSelectionValueInputs(
	t *testing.T,
) (
	PrivateEvalCapsule,
	[]SelectionValueCandidate,
	[]SelectionValueTarget,
	[]string,
) {
	t.Helper()
	project := "https://example.com/project.git"
	sourceProposal := selectionValueProposal(
		project,
		"internal/loaders/**",
		"Before decoding a local manifest, reject unknown fields and trailing JSON values.",
		"Strict decoding prevents silently accepting misspelled policy fields.",
	)
	sourceReplay := selectionValueReplay(
		"0123456789abcdef0123456789abcdef01234567",
		"Add a local policy manifest loader.",
		"internal/loaders/policy.go",
		"go test ./internal/loaders -run TestPolicyManifest",
	)
	source := PrivateEvalCapsule{
		SchemaVersion:      PrivateEvalCapsuleSchemaVersion,
		CapsuleID:          "evc_selection_source",
		Readiness:          "executable",
		CandidateID:        "exc_selection_source",
		ProjectIdentity:    project,
		CandidateFamily:    experience.CandidateSuccessfulProcedure,
		TreatmentCandidate: sourceProposal,
		SourceSessions:     []string{"ses_selection_source"},
		Evidence: []experience.EvidenceRef{{
			Kind:       experience.EvidenceTranscriptTurn,
			SessionKey: "ses_selection_source",
			TurnIndex:  selectionInt64Pointer(4),
			Excerpt:    "The strict loader implementation passed verification.",
		}},
		Outcomes: []trajectory.Outcome{{
			ProjectIdentity: project,
			SessionKey:      "ses_selection_source",
			OccurredAt:      time.Date(2026, 9, 12, 14, 0, 0, 0, time.UTC),
			Kind:            trajectory.OutcomeVerificationPass,
			Result:          trajectory.ResultSucceeded,
			SourceRefs: []trajectory.NodeRef{{
				Kind:       trajectory.NodeTranscriptTurn,
				SessionKey: "ses_selection_source",
				TurnIndex:  selectionInt64Pointer(4),
			}},
		}},
		Provenance: CapsuleProvenance{
			InstructionAuthority: string(experience.AuthorityNone),
		},
		Evaluation: CapsuleEvaluationContract{
			TaskState: "reconstructed",
		},
		Replay: &sourceReplay,
	}
	pool := []SelectionValueCandidate{{
		CandidateID:     source.CandidateID,
		Family:          source.CandidateFamily,
		SourceCapsuleID: source.CapsuleID,
		Proposal:        sourceProposal,
	}}
	for index, item := range []struct {
		path        string
		instruction string
		rationale   string
	}{
		{
			path:        "internal/storage/**",
			instruction: "Wrap related SQLite writes in one transaction.",
			rationale:   "Atomic writes prevent partial local state.",
		},
		{
			path:        "internal/presentation/**",
			instruction: "Keep browser payloads bounded before rendering.",
			rationale:   "Bounded payloads protect report responsiveness.",
		},
		{
			path:        "internal/acquisition/**",
			instruction: "Resume append-only inputs from the persisted byte offset.",
			rationale:   "Offsets avoid replaying complete transcript files.",
		},
	} {
		pool = append(pool, SelectionValueCandidate{
			CandidateID: "exc_selection_distractor_" + string(rune('a'+index)),
			Family:      experience.CandidateSuccessfulProcedure,
			Proposal: selectionValueProposal(
				project,
				item.path,
				item.instruction,
				item.rationale,
			),
		})
	}
	targets := []SelectionValueTarget{
		selectionValueTarget(
			project,
			source.CandidateID,
			"target_hook_config",
			"1111111111111111111111111111111111111111",
			"Add support for loading hook configuration from a local file.",
			"internal/loaders/hooks.go",
			"go test ./internal/loaders -run TestHookConfig",
		),
		selectionValueTarget(
			project,
			source.CandidateID,
			"target_agent_profile",
			"2222222222222222222222222222222222222222",
			"Implement the agent profile file reader.",
			"internal/loaders/profile.go",
			"go test ./internal/loaders -run TestAgentProfile",
		),
	}
	return source, pool, targets, []string{
		"reject unknown fields",
		"trailing JSON values",
	}
}

func selectionInt64Pointer(value int64) *int64 {
	return &value
}

func selectionValueProposal(
	project string,
	repositoryPath string,
	instruction string,
	rationale string,
) experience.ExperienceProposal {
	return experience.ExperienceProposal{
		Type: experience.ExperienceProcedure,
		Scope: experience.Scope{
			Kind:            experience.ScopeProject,
			ProjectIdentity: project,
			RepositoryPaths: []string{repositoryPath},
			TaskFamilies:    []string{"implement"},
			Harnesses:       []experience.Harness{experience.HarnessCodex},
		},
		Applicability: experience.Applicability{
			SemanticDescription: "Implement a related local file loader.",
		},
		Guidance: experience.Guidance{
			Instruction:          instruction,
			Rationale:            rationale,
			InterventionStrength: experience.InterventionAdvise,
		},
		Verifier: experience.Verifier{
			Kind: experience.VerifierCommandSucceeded,
			Command: &experience.CommandVerifierSpec{
				Command:          "go test ./internal/loaders",
				CommandClass:     "go_test",
				ScrubbingVersion: "belay.redaction.v1",
			},
		},
	}
}

func selectionValueTarget(
	project string,
	sourceCandidateID string,
	targetID string,
	revision string,
	prompt string,
	repositoryPath string,
	outcomeCommand string,
) SelectionValueTarget {
	return SelectionValueTarget{
		TargetID: targetID,
		Replay: selectionValueReplay(
			revision,
			prompt,
			repositoryPath,
			outcomeCommand,
		),
		SelectionRequest: localapp.ExperienceSelectionRequest{
			ProjectIdentity: project,
			Harness:         experience.HarnessCodex,
			TaskFamily:      "implement",
			TaskHint:        prompt,
			RepositoryPaths: []string{repositoryPath},
			Model:           "openai.gpt-5.6-sol",
		},
		RelevantCandidateIDs: []string{sourceCandidateID},
	}
}

func selectionValueReplay(
	revision string,
	prompt string,
	repositoryPath string,
	outcomeCommand string,
) CapsuleReplayContract {
	return CapsuleReplayContract{
		SchemaVersion: CapsuleReplaySchemaVersion,
		Source: CapsuleReplaySource{
			Kind:       "git_revision",
			Repository: "https://example.com/project.git",
			Revision:   revision,
		},
		Task: CapsuleReplayTask{
			Prompt:               prompt,
			WorkingDirectory:     ".",
			AllowedMutationPaths: []string{repositoryPath},
		},
		Outcome: CapsuleOutcomeContract{
			Kind:             "command_succeeded",
			Command:          outcomeCommand,
			ExpectedExitCode: 0,
		},
		TreatmentVerifier: experience.Verifier{
			Kind: experience.VerifierCommandSucceeded,
			Command: &experience.CommandVerifierSpec{
				Command:          outcomeCommand,
				CommandClass:     "go_test",
				ScrubbingVersion: "belay.redaction.v1",
			},
		},
		Isolation: CapsuleReplayIsolation{
			Kind:             "disposable_git_worktree",
			ResetBetweenRuns: true,
		},
	}
}
