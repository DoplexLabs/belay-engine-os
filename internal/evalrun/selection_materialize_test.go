package evalrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

func TestMaterializeSelectionValuePlanUsesProductionSelector(t *testing.T) {
	source, pool, targets, forbidden := validSelectionValueInputs(t)
	plan, err := BuildSelectionValuePlan(
		source,
		pool,
		targets,
		forbidden,
		time.Date(2026, 9, 12, 16, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	result, err := MaterializeSelectionValuePlan(
		context.Background(),
		root,
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != SelectionMaterializationSchemaVersion ||
		result.StoreKind != "disposable_encrypted_sqlite" ||
		len(result.Generation.ExperienceRefs) != 4 ||
		len(result.Targets) != 2 ||
		result.Summary.TargetsWithRelevantSelection != 2 ||
		result.Summary.TargetsAbstained != 0 ||
		result.Summary.MicroPrecision != 1 ||
		result.Summary.MicroRecall != 1 ||
		result.Summary.GateDecision != "ready_for_harness_baselines" {
		t.Fatalf("selection materialization = %+v", result)
	}
	for _, target := range result.Targets {
		if len(target.Selected) != 1 ||
			target.Selected[0].CandidateID != source.CandidateID ||
			target.Selected[0].Score < 90 ||
			target.EstimatedTokens < 1 {
			t.Fatalf("target selection = %+v", target)
		}
	}
	assertSelectionStoreEncrypted(
		t,
		root,
		source.TreatmentCandidate.Guidance.Instruction,
	)
}

func TestMaterializeSelectionValuePlanPreservesSelectionAbstention(t *testing.T) {
	source, pool, targets, forbidden := validSelectionValueInputs(t)
	targets[0].SelectionRequest.Harness = experience.HarnessClaude
	plan, err := BuildSelectionValuePlan(
		source,
		pool,
		targets,
		forbidden,
		time.Date(2026, 9, 12, 16, 30, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := MaterializeSelectionValuePlan(
		context.Background(),
		t.TempDir(),
		plan,
	)
	if err != nil {
		t.Fatal(err)
	}
	var abstained bool
	for _, target := range result.Targets {
		if target.TargetID == targets[0].TargetID {
			abstained = target.Abstained &&
				len(target.Selected) == 0 &&
				target.Recall == 0
		}
	}
	if !abstained ||
		result.Summary.TargetsAbstained != 1 ||
		result.Summary.GateDecision != "selection_not_proven" {
		t.Fatalf("abstention result = %+v", result)
	}
}

func TestMaterializeSelectionValuePlanRefusesExistingStore(t *testing.T) {
	source, pool, targets, forbidden := validSelectionValueInputs(t)
	plan, err := BuildSelectionValuePlan(
		source,
		pool,
		targets,
		forbidden,
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "selection-value.sqlite"),
		[]byte("do not overwrite"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, err = MaterializeSelectionValuePlan(
		context.Background(),
		root,
		plan,
	)
	if err == nil || !strings.Contains(err.Error(), "refuses an existing") {
		t.Fatalf("MaterializeSelectionValuePlan() error = %v", err)
	}
}

func assertSelectionStoreEncrypted(
	t *testing.T,
	root string,
	canary string,
) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	inspected := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		inspected++
		if strings.Contains(string(body), canary) {
			t.Fatalf("%s contains plaintext guidance", entry.Name())
		}
	}
	if inspected == 0 {
		t.Fatal("selection materialization created no encrypted store files")
	}
}
