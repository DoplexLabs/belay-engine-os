package evalrun

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

const CapsuleReplaySchemaVersion = "belay.private-eval-replay.v1"

type CapsuleReplayContract struct {
	SchemaVersion     string                 `json:"schema_version"`
	Source            CapsuleReplaySource    `json:"source"`
	Task              CapsuleReplayTask      `json:"task"`
	Outcome           CapsuleOutcomeContract `json:"outcome"`
	TreatmentVerifier experience.Verifier    `json:"treatment_verifier"`
	Isolation         CapsuleReplayIsolation `json:"isolation"`
}

type CapsuleReplaySource struct {
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
}

type CapsuleReplayTask struct {
	Prompt               string   `json:"prompt"`
	WorkingDirectory     string   `json:"working_directory"`
	SetupCommands        []string `json:"setup_commands,omitempty"`
	AllowedMutationPaths []string `json:"allowed_mutation_paths,omitempty"`
}

type CapsuleOutcomeContract struct {
	Kind             string `json:"kind"`
	Command          string `json:"command"`
	ExpectedExitCode int    `json:"expected_exit_code"`
}

type CapsuleReplayIsolation struct {
	Kind             string `json:"kind"`
	ResetBetweenRuns bool   `json:"reset_between_runs"`
}

var fullGitRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (value CapsuleReplayContract) Validate() error {
	if value.SchemaVersion != CapsuleReplaySchemaVersion {
		return errors.New("private eval replay schema version is invalid")
	}
	if value.Source.Kind != "git_revision" ||
		strings.TrimSpace(value.Source.Repository) == "" ||
		!fullGitRevisionPattern.MatchString(value.Source.Revision) {
		return errors.New(
			"private eval replay source requires a repository and full Git revision",
		)
	}
	if err := value.Task.validate(); err != nil {
		return err
	}
	if value.Outcome.Kind != "command_succeeded" ||
		strings.TrimSpace(value.Outcome.Command) == "" ||
		len(value.Outcome.Command) > 4096 ||
		value.Outcome.ExpectedExitCode != 0 {
		return errors.New(
			"private eval outcome requires an exact successful command",
		)
	}
	if err := value.TreatmentVerifier.Validate(); err != nil {
		return fmt.Errorf("validate private eval treatment verifier: %w", err)
	}
	if value.TreatmentVerifier.Kind == experience.VerifierObservationOnly {
		return errors.New(
			"private eval treatment verifier must be deterministic",
		)
	}
	if value.Isolation.Kind != "disposable_git_worktree" ||
		!value.Isolation.ResetBetweenRuns {
		return errors.New(
			"private eval replay requires resettable disposable Git worktrees",
		)
	}
	return nil
}

func (value CapsuleReplayTask) validate() error {
	prompt := strings.TrimSpace(value.Prompt)
	if prompt == "" || len(prompt) > 16*1024 {
		return errors.New("private eval task prompt is required and bounded")
	}
	if !validCapsuleRelativePath(value.WorkingDirectory, true) {
		return errors.New(
			"private eval task working directory must be relative",
		)
	}
	if len(value.SetupCommands) > 8 ||
		len(value.AllowedMutationPaths) > 32 {
		return errors.New("private eval task setup exceeds item limits")
	}
	for _, command := range value.SetupCommands {
		if strings.TrimSpace(command) == "" || len(command) > 4096 {
			return errors.New("private eval setup command is invalid")
		}
	}
	for _, item := range value.AllowedMutationPaths {
		if !validCapsuleRelativePath(item, false) {
			return errors.New(
				"private eval allowed mutation path must be relative",
			)
		}
	}
	return nil
}

func validCapsuleRelativePath(value string, allowDot bool) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, `\`) ||
		strings.HasPrefix(value, "/") {
		return false
	}
	cleaned := path.Clean(value)
	if cleaned == "." {
		return allowDot
	}
	return cleaned == value &&
		cleaned != ".." &&
		!strings.HasPrefix(cleaned, "../")
}

func CompletePrivateEvalCapsule(
	draft PrivateEvalCapsule,
	replay CapsuleReplayContract,
) (PrivateEvalCapsule, error) {
	if draft.SchemaVersion != PrivateEvalCapsuleSchemaVersion ||
		draft.Readiness != "draft" ||
		draft.Replay != nil ||
		strings.TrimSpace(draft.CapsuleID) == "" {
		return PrivateEvalCapsule{}, errors.New(
			"private eval completion requires an uncompleted draft capsule",
		)
	}
	if err := replay.Validate(); err != nil {
		return PrivateEvalCapsule{}, err
	}
	result := draft
	result.Readiness = "executable"
	result.ReadinessGaps = nil
	result.Evaluation.TaskState = "reconstructed"
	result.Replay = &replay
	result.CapsuleID = privateEvalCapsuleID(result)
	if result.CapsuleID == draft.CapsuleID {
		return PrivateEvalCapsule{}, errors.New(
			"private eval completion did not change capsule identity",
		)
	}
	return result, nil
}
