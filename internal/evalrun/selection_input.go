package evalrun

import (
	"context"
	"errors"
	"time"
)

const SelectionValueInputSchemaVersion = "belay.selection-value-input.v1"
const SelectionValuePreparationSchemaVersion = "belay.selection-value-preparation.v1"

type SelectionValueInput struct {
	SchemaVersion          string                    `json:"schema_version"`
	Source                 PrivateEvalCapsule        `json:"source"`
	ExperiencePool         []SelectionValueCandidate `json:"experience_pool"`
	Targets                []SelectionValueTarget    `json:"targets"`
	ForbiddenPromptPhrases []string                  `json:"forbidden_prompt_phrases"`
}

type SelectionValuePreparationResult struct {
	SchemaVersion string                         `json:"schema_version"`
	Plan          SelectionValuePlan             `json:"plan"`
	Selection     SelectionMaterializationResult `json:"selection"`
}

func PrepareSelectionValueEvaluation(
	ctx context.Context,
	root string,
	input SelectionValueInput,
) (SelectionValuePreparationResult, error) {
	if ctx == nil {
		return SelectionValuePreparationResult{}, errors.New(
			"selection value preparation requires context",
		)
	}
	if input.SchemaVersion != SelectionValueInputSchemaVersion {
		return SelectionValuePreparationResult{}, errors.New(
			"selection value input schema version is invalid",
		)
	}
	plan, err := BuildSelectionValuePlan(
		input.Source,
		input.ExperiencePool,
		input.Targets,
		input.ForbiddenPromptPhrases,
		time.Now().UTC().Round(0),
	)
	if err != nil {
		return SelectionValuePreparationResult{}, err
	}
	selection, err := MaterializeSelectionValuePlan(ctx, root, plan)
	if err != nil {
		return SelectionValuePreparationResult{}, err
	}
	return SelectionValuePreparationResult{
		SchemaVersion: SelectionValuePreparationSchemaVersion,
		Plan:          plan,
		Selection:     selection,
	}, nil
}
