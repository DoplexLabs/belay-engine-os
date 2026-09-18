package localapp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

func TestExperienceLearningPauseRemovesGuidanceFromFreshMissionPack(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	runMissionPackTestCommand(t, root, "git", "init", "-b", "main")
	project := compilerTestProjectIdentity()
	runMissionPackTestCommand(
		t,
		root,
		"git",
		"remote",
		"add",
		"origin",
		project,
	)
	store, err := local.Open(
		filepath.Join(t.TempDir(), "belay.sqlite"),
		&memoryKeyProvider{keys: make(map[string][]byte)},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	const seed = "pause mission pack integration"
	candidate := experienceSemanticTestCandidate(seed)
	approved := compilerTestExperience(t, seed)
	approved.Governance.LifecycleState = experience.LifecycleApproved
	compilerTestRehash(t, &approved)
	if candidate.CandidateID != approved.OriginCandidateID {
		t.Fatalf(
			"candidate/experience identity mismatch = %s/%s",
			candidate.CandidateID,
			approved.OriginCandidateID,
		)
	}
	if _, err := store.InsertExperienceCandidate(ctx, candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertExperience(ctx, approved); err != nil {
		t.Fatal(err)
	}

	compiler, err := NewExperienceCompilerService(store)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewExperienceApprovalService(store)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := NewExperienceLifecycleService(store)
	if err != nil {
		t.Fatal(err)
	}
	repository := &missionPackTestRepository{
		cwdProject: missionpack.ResolvedProject{
			Identity:     project,
			IdentityKind: "remote",
			Path:         root,
		},
	}
	learning, err := newExperienceLearningService(
		&experienceLearningTestResolver{
			project: repository.cwdProject,
		},
		approval,
		lifecycle,
		compiler,
		&experienceLearningTestReviews{},
		store,
		store,
	)
	if err != nil {
		t.Fatal(err)
	}
	ref := compilerTestRef(approved)
	activationPreview, err := learning.PrepareLifecycle(
		ctx,
		ref,
		experience.LifecycleActionActivate,
	)
	if err != nil {
		t.Fatal(err)
	}
	activated, err := learning.ApplyLifecycle(
		ctx,
		ref,
		experience.LifecycleActionActivate,
		activationPreview.ActionToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	if activated.Transition.Transition.FromState !=
		experience.LifecycleApproved ||
		activated.Transition.Transition.ToState !=
			experience.LifecycleActive ||
		len(activated.Compilation.Generation.ExperienceRefs) != 1 ||
		activated.Compilation.Generation.ExperienceRefs[0] != ref {
		t.Fatalf(
			"activation result = %+v",
			activated,
		)
	}

	packs, err := NewMissionPackService(
		repository,
		WithMissionPackExperienceSelector(compiler),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := missionpack.Request{
		CWD:     root,
		Intent:  missionpack.IntentImplement,
		Harness: missionpack.HarnessCodex,
	}
	before, err := packs.Generate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Experiences) != 1 ||
		before.Experiences[0].ExperienceID != ref.ExperienceID ||
		before.Experiences[0].Version != ref.Version ||
		before.Experiences[0].Guidance != approved.Guidance.Instruction {
		t.Fatalf("pre-pause Mission Pack = %+v", before.Experiences)
	}

	preview, err := learning.PrepareLifecycle(
		ctx,
		ref,
		experience.LifecycleActionPause,
	)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := learning.ApplyLifecycle(
		ctx,
		ref,
		experience.LifecycleActionPause,
		preview.ActionToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Transition.Transition.ToState !=
		experience.LifecyclePaused ||
		paused.Compilation.Generation.Generation <=
			activated.Compilation.Generation.Generation ||
		len(paused.Compilation.Generation.ExperienceRefs) != 0 {
		t.Fatalf("pause result = %+v", paused)
	}

	after, err := packs.Generate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Experiences) != 0 ||
		after.ExperienceGeneration != 0 {
		t.Fatalf("post-pause Mission Pack = %+v", after)
	}
}
