package evalrun

import (
	"context"
	"testing"
)

func TestPrepareSelectionValueEvaluationBuildsAndSelects(t *testing.T) {
	source, pool, targets, forbidden := validSelectionValueInputs(t)
	result, err := PrepareSelectionValueEvaluation(
		context.Background(),
		t.TempDir(),
		SelectionValueInput{
			SchemaVersion:          SelectionValueInputSchemaVersion,
			Source:                 source,
			ExperiencePool:         pool,
			Targets:                targets,
			ForbiddenPromptPhrases: forbidden,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != SelectionValuePreparationSchemaVersion ||
		result.Plan.PlanID == "" ||
		result.Selection.PlanID != result.Plan.PlanID ||
		result.Selection.Summary.GateDecision !=
			"ready_for_harness_baselines" {
		t.Fatalf("selection preparation = %+v", result)
	}
}

func TestPrepareSelectionValueEvaluationRejectsUnversionedInput(t *testing.T) {
	_, err := PrepareSelectionValueEvaluation(
		context.Background(),
		t.TempDir(),
		SelectionValueInput{},
	)
	if err == nil {
		t.Fatal("PrepareSelectionValueEvaluation() accepted unversioned input")
	}
}
