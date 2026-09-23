package localapp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type experienceCompilerTestStore struct {
	generation      local.ExperienceGeneration
	values          map[experience.ExperienceRef]local.StoredExperience
	compileResult   local.CompileExperienceGenerationResult
	compileCalls    int
	compileProject  string
	compileTime     time.Time
	generationReads int
	experienceReads []experience.ExperienceRef
}

func (store *experienceCompilerTestStore) CompileActiveExperienceGeneration(
	_ context.Context,
	projectIdentity string,
	compiledAt time.Time,
) (local.CompileExperienceGenerationResult, error) {
	store.compileCalls++
	store.compileProject = projectIdentity
	store.compileTime = compiledAt
	return store.compileResult, nil
}

func (store *experienceCompilerTestStore) GetActiveExperienceGeneration(
	_ context.Context,
	_ string,
) (local.ExperienceGeneration, error) {
	store.generationReads++
	return store.generation, nil
}

func (store *experienceCompilerTestStore) GetExperience(
	_ context.Context,
	ref experience.ExperienceRef,
) (local.StoredExperience, error) {
	store.experienceReads = append(store.experienceReads, ref)
	value, found := store.values[ref]
	if !found {
		return local.StoredExperience{}, errors.New("experience not found")
	}
	return value, nil
}

func TestExperienceCompilerCompileUsesClockAndReportsReplay(t *testing.T) {
	now := time.Date(2026, 9, 10, 23, 0, 0, 123, time.FixedZone("test", 3600))
	value := compilerTestExperience(t, "compile")
	generation := compilerTestGeneration(
		now.UTC().Round(0),
		compilerTestRef(value),
	)
	store := &experienceCompilerTestStore{
		compileResult: local.CompileExperienceGenerationResult{
			Generation: generation,
			Replayed:   true,
		},
	}
	service, err := NewExperienceCompilerService(
		store,
		WithExperienceCompilerClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Compile(
		context.Background(),
		"  "+value.Scope.ProjectIdentity+"  ",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed ||
		result.Generation.Generation != generation.Generation ||
		store.compileCalls != 1 ||
		store.compileProject != value.Scope.ProjectIdentity ||
		!store.compileTime.Equal(now.UTC().Round(0)) {
		t.Fatalf("compile result/store = %+v/%+v", result, store)
	}
}

func TestExperienceCompilerSelectionIsStableAndUnscopedIsEligible(t *testing.T) {
	now := time.Date(2026, 9, 10, 23, 10, 0, 0, time.UTC)
	base := compilerTestExperience(t, "stable base")
	harness := compilerTestExperience(t, "stable harness")
	harness.Scope.Harnesses = []experience.Harness{experience.HarnessClaude}
	compilerTestRehash(t, &harness)
	task := compilerTestExperience(t, "stable task")
	task.Scope.TaskFamilies = []string{"implement"}
	compilerTestRehash(t, &task)
	values := compilerTestStoredValues(base, harness, task)
	firstStore := &experienceCompilerTestStore{
		generation: compilerTestGeneration(
			now,
			compilerTestRef(base),
			compilerTestRef(task),
			compilerTestRef(harness),
		),
		values: values,
	}
	secondStore := &experienceCompilerTestStore{
		generation: compilerTestGeneration(
			now,
			compilerTestRef(harness),
			compilerTestRef(base),
			compilerTestRef(task),
		),
		values: values,
	}
	request := ExperienceSelectionRequest{
		ProjectIdentity: base.Scope.ProjectIdentity,
		Harness:         experience.HarnessClaude,
		TaskFamily:      "implement",
		RepositoryPaths: []string{},
	}

	first := compilerTestSelect(t, firstStore, now, request)
	second := compilerTestSelect(t, secondStore, now, request)
	if !reflect.DeepEqual(first.ExperienceRefs, second.ExperienceRefs) ||
		!reflect.DeepEqual(
			first.Generation.ExperienceRefs,
			second.Generation.ExperienceRefs,
		) {
		t.Fatalf("shuffled selection differs: %+v / %+v", first, second)
	}
	want := []experience.ExperienceRef{
		compilerTestRef(task),
		compilerTestRef(harness),
		compilerTestRef(base),
	}
	if !reflect.DeepEqual(first.ExperienceRefs, want) ||
		first.Selected[2].Score != 10 ||
		firstStore.compileCalls != 0 {
		t.Fatalf("stable selection = %+v, want refs %+v", first, want)
	}
}

func TestExperienceCompilerSelectionRequiresExactScopedMatches(t *testing.T) {
	now := time.Date(2026, 9, 10, 23, 20, 0, 0, time.UTC)
	value := compilerTestExperience(t, "exact scopes")
	value.Scope.Kind = experience.ScopeSession
	value.Scope.SessionKey = "ses_exact"
	value.Scope.Harnesses = []experience.Harness{experience.HarnessClaude}
	value.Scope.TaskFamilies = []string{"implement"}
	value.Scope.RepositoryPaths = []string{"internal/**"}
	value.Scope.Models = []string{"model-v1"}
	compilerTestRehash(t, &value)
	store := compilerTestStoreFor(now, value)
	exact := ExperienceSelectionRequest{
		ProjectIdentity: value.Scope.ProjectIdentity,
		SessionKey:      "ses_exact",
		Harness:         experience.HarnessClaude,
		TaskFamily:      "implement",
		RepositoryPaths: []string{"internal/localapp/service.go"},
		Model:           "model-v1",
	}
	result := compilerTestSelect(t, store, now, exact)
	if len(result.Selected) != 1 || result.Selected[0].Score != 160 {
		t.Fatalf("exact scoped selection = %+v", result)
	}

	tests := []struct {
		name   string
		mutate func(*ExperienceSelectionRequest)
	}{
		{name: "session", mutate: func(value *ExperienceSelectionRequest) {
			value.SessionKey = "ses_other"
		}},
		{name: "harness", mutate: func(value *ExperienceSelectionRequest) {
			value.Harness = experience.HarnessCodex
		}},
		{name: "task", mutate: func(value *ExperienceSelectionRequest) {
			value.TaskFamily = "review"
		}},
		{name: "path", mutate: func(value *ExperienceSelectionRequest) {
			value.RepositoryPaths = []string{"docs/readme.md"}
		}},
		{name: "model", mutate: func(value *ExperienceSelectionRequest) {
			value.Model = "model-v2"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := exact
			test.mutate(&request)
			result := compilerTestSelect(t, store, now, request)
			if len(result.Selected) != 0 ||
				result.Selected == nil ||
				result.ExperienceRefs == nil {
				t.Fatalf("mismatched scope selected = %+v", result)
			}
		})
	}
}

func TestExperienceCompilerSelectionUsesTaskHintWhenPreTaskPathsAreUnknown(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 23, 25, 0, 0, time.UTC)
	value := compilerTestExperience(t, "sqlite migration")
	value.Scope.TaskFamilies = []string{
		"database-migration",
		"schema-change",
	}
	value.Scope.RepositoryPaths = []string{"task.py"}
	value.Applicability.DeterministicConditions =
		[]experience.DeterministicCondition{{
			Kind:   experience.ConditionPathPattern,
			Values: []string{"task.py"},
		}}
	value.Applicability.SemanticDescription =
		"Writing SQLite migrations that must be safely re-runnable."
	value.Guidance.Instruction =
		"Make SQLite schema migrations idempotent and atomic."
	compilerTestRehash(t, &value)
	store := compilerTestStoreFor(now, value)

	relevant := compilerTestSelect(
		t,
		store,
		now,
		ExperienceSelectionRequest{
			ProjectIdentity: value.Scope.ProjectIdentity,
			Harness:         experience.HarnessClaude,
			TaskFamily:      "implement",
			TaskHint:        "SQLite migration invariants",
			RepositoryPaths: []string{},
		},
	)
	if len(relevant.Selected) != 1 || relevant.Selected[0].Score <= 10 {
		t.Fatalf("relevant pre-task selection = %+v", relevant)
	}

	unrelated := compilerTestSelect(
		t,
		store,
		now,
		ExperienceSelectionRequest{
			ProjectIdentity: value.Scope.ProjectIdentity,
			Harness:         experience.HarnessClaude,
			TaskFamily:      "implement",
			TaskHint:        "Update the CSS color palette",
			RepositoryPaths: []string{},
		},
	)
	if len(unrelated.Selected) != 0 {
		t.Fatalf("unrelated pre-task selection = %+v", unrelated)
	}
}

func TestExperienceCompilerSelectionNormalizesAsyncVocabulary(t *testing.T) {
	now := time.Date(2026, 9, 10, 23, 20, 0, 0, time.UTC)
	value := compilerTestExperience(t, "async vocabulary")
	value.Scope.TaskFamilies = []string{"Python asynchronous concurrency"}
	value.Applicability.SemanticDescription =
		"Cancel sibling tasks and await cleanup before returning."
	compilerTestRehash(t, &value)
	store := &experienceCompilerTestStore{
		generation: compilerTestGeneration(now, compilerTestRef(value)),
		values:     compilerTestStoredValues(value),
	}
	result := compilerTestSelect(
		t,
		store,
		now,
		ExperienceSelectionRequest{
			ProjectIdentity: value.Scope.ProjectIdentity,
			Harness:         experience.HarnessCodex,
			TaskFamily:      "implement",
			TaskHint:        "Async cancellation and cleanup",
			RepositoryPaths: make([]string, 0),
		},
	)
	if len(result.Selected) != 1 {
		t.Fatalf("async vocabulary selection = %+v", result)
	}
}

func TestExperienceCompilerSelectionSupportsOnlyPathConditions(t *testing.T) {
	now := time.Date(2026, 9, 10, 23, 30, 0, 0, time.UTC)
	supported := compilerTestExperience(t, "path condition")
	supported.Scope.RepositoryPaths = []string{"src/**"}
	supported.Applicability.DeterministicConditions =
		[]experience.DeterministicCondition{{
			Kind:   experience.ConditionPathPattern,
			Values: []string{"src/**/handler?.go"},
		}}
	compilerTestRehash(t, &supported)
	unsupported := compilerTestExperience(t, "unsupported condition")
	unsupported.Applicability.DeterministicConditions =
		[]experience.DeterministicCondition{{
			Kind:   experience.ConditionEventType,
			Values: []string{"tool_result"},
		}}
	compilerTestRehash(t, &unsupported)
	store := compilerTestStoreFor(now, supported, unsupported)

	result := compilerTestSelect(
		t,
		store,
		now,
		ExperienceSelectionRequest{
			ProjectIdentity: supported.Scope.ProjectIdentity,
			RepositoryPaths: []string{"src/api/v2/handler1.go"},
		},
	)
	if !reflect.DeepEqual(
		result.ExperienceRefs,
		[]experience.ExperienceRef{compilerTestRef(supported)},
	) || result.Selected[0].Score != 40 {
		t.Fatalf("path-condition selection = %+v", result)
	}
}

func TestExperienceCompilerSelectionOmitsPausedAndExpiredAfterCompile(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 23, 40, 0, 0, time.UTC)
	paused := compilerTestExperience(t, "paused after compile")
	expired := compilerTestExperience(t, "expired after compile")
	expiry := now.Add(-time.Second)
	expired.Applicability.ExpiresAt = &expiry
	compilerTestRehash(t, &expired)
	store := compilerTestStoreFor(now, paused, expired)
	pausedRef := compilerTestRef(paused)
	pausedStored := store.values[pausedRef]
	pausedStored.CurrentLifecycle = experience.LifecyclePaused
	store.values[pausedRef] = pausedStored
	expiredRef := compilerTestRef(expired)
	expiredStored := store.values[expiredRef]
	expiredStored.ExpiresAt = &expiry
	store.values[expiredRef] = expiredStored

	result := compilerTestSelect(
		t,
		store,
		now,
		ExperienceSelectionRequest{
			ProjectIdentity: paused.Scope.ProjectIdentity,
			RepositoryPaths: []string{},
		},
	)
	if len(result.Selected) != 0 ||
		result.Selected == nil ||
		result.ExperienceRefs == nil {
		t.Fatalf("paused/expired selection = %+v", result)
	}
}

func TestExperienceCompilerSelectionFailsClosedOnGenerationReferences(
	t *testing.T,
) {
	now := time.Date(2026, 9, 10, 23, 50, 0, 0, time.UTC)
	value := compilerTestExperience(t, "reference failures")
	request := ExperienceSelectionRequest{
		ProjectIdentity: value.Scope.ProjectIdentity,
		RepositoryPaths: []string{},
	}
	missingRef := experience.ExperienceRef{
		ExperienceID: "exp_missing",
		Version:      1,
	}
	missingStore := &experienceCompilerTestStore{
		generation: compilerTestGeneration(now, missingRef),
		values:     map[experience.ExperienceRef]local.StoredExperience{},
	}
	service, err := NewExperienceCompilerService(
		missingStore,
		WithExperienceCompilerClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Select(
		context.Background(),
		request,
	); !errors.Is(err, ErrExperienceSelectionCorrupt) {
		t.Fatalf("missing ref error = %v", err)
	}

	crossProject := compilerTestExperience(t, "cross project ref")
	crossProject.Scope.ProjectIdentity =
		"git@example.test:doplexlabs/other.git"
	crossProject.ExperienceID = experience.DeriveExperienceID(
		crossProject.Scope.ProjectIdentity,
		crossProject.OriginCandidateID,
	)
	compilerTestRehash(t, &crossProject)
	crossStore := &experienceCompilerTestStore{
		generation: compilerTestGeneration(
			now,
			compilerTestRef(crossProject),
		),
		values: compilerTestStoredValues(crossProject),
	}
	service, err = NewExperienceCompilerService(
		crossStore,
		WithExperienceCompilerClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Select(
		context.Background(),
		request,
	); !errors.Is(err, ErrExperienceSelectionCorrupt) {
		t.Fatalf("cross-project ref error = %v", err)
	}
}

func TestExperienceCompilerSelectionEnforcesItemAndTokenCaps(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	t.Run("three items", func(t *testing.T) {
		values := []experience.Experience{
			compilerTestExperience(t, "cap one"),
			compilerTestExperience(t, "cap two"),
			compilerTestExperience(t, "cap three"),
			compilerTestExperience(t, "cap four"),
		}
		store := compilerTestStoreFor(now, values...)
		result := compilerTestSelect(
			t,
			store,
			now,
			ExperienceSelectionRequest{
				ProjectIdentity: values[0].Scope.ProjectIdentity,
				RepositoryPaths: []string{},
			},
		)
		if len(result.Selected) != 3 ||
			len(result.ExperienceRefs) != 3 ||
			result.EstimatedTokens > maxSelectedExperienceTokens {
			t.Fatalf("three-item cap result = %+v", result)
		}
	})

	t.Run("oversized first is skipped and budget continues", func(t *testing.T) {
		oversized := compilerTestExperience(t, "oversized")
		oversized.Scope.Kind = experience.ScopeSession
		oversized.Scope.SessionKey = "ses_budget"
		oversized.Scope.Harnesses =
			[]experience.Harness{experience.HarnessClaude}
		oversized.Scope.TaskFamilies = []string{"implement"}
		oversized.Scope.RepositoryPaths = []string{"internal/**"}
		oversized.Scope.Models = []string{"model-v1"}
		oversized.Guidance.Instruction = strings.Repeat("x", 2040)
		oversized.Guidance.Rationale = strings.Repeat("y", 1000)
		compilerTestRehash(t, &oversized)

		mediumFirst := compilerTestExperience(t, "medium first")
		mediumFirst.Scope.Kind = experience.ScopeSession
		mediumFirst.Scope.SessionKey = "ses_budget"
		mediumFirst.Scope.Harnesses =
			[]experience.Harness{experience.HarnessClaude}
		mediumFirst.Guidance.Rationale = strings.Repeat("m", 1500)
		compilerTestRehash(t, &mediumFirst)

		mediumSecond := compilerTestExperience(t, "medium second")
		mediumSecond.Scope.Kind = experience.ScopeSession
		mediumSecond.Scope.SessionKey = "ses_budget"
		mediumSecond.Guidance.Rationale = strings.Repeat("n", 1500)
		compilerTestRehash(t, &mediumSecond)

		tiny := compilerTestExperience(t, "tiny after skipped items")
		store := compilerTestStoreFor(
			now,
			oversized,
			mediumFirst,
			mediumSecond,
			tiny,
		)
		result := compilerTestSelect(
			t,
			store,
			now,
			ExperienceSelectionRequest{
				ProjectIdentity: oversized.Scope.ProjectIdentity,
				SessionKey:      "ses_budget",
				Harness:         experience.HarnessClaude,
				TaskFamily:      "implement",
				RepositoryPaths: []string{"internal/localapp/service.go"},
				Model:           "model-v1",
			},
		)
		if selectionHasRef(result, compilerTestRef(oversized)) ||
			!selectionHasRef(result, compilerTestRef(mediumFirst)) ||
			selectionHasRef(result, compilerTestRef(mediumSecond)) ||
			!selectionHasRef(result, compilerTestRef(tiny)) ||
			result.EstimatedTokens > maxSelectedExperienceTokens {
			t.Fatalf("token-cap selection = %+v", result)
		}
	})
}

func TestExperienceCompilerSelectionNormalizesEquivalentRequests(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 10, 0, 0, time.UTC)
	value := compilerTestExperience(t, "normalized request")
	value.Scope.Harnesses = []experience.Harness{experience.HarnessClaude}
	value.Scope.TaskFamilies = []string{"implement"}
	value.Scope.RepositoryPaths = []string{"internal/**"}
	value.Scope.Models = []string{"model-v1"}
	compilerTestRehash(t, &value)
	store := compilerTestStoreFor(now, value)
	first := compilerTestSelect(
		t,
		store,
		now,
		ExperienceSelectionRequest{
			ProjectIdentity: "  " + value.Scope.ProjectIdentity + " ",
			Harness:         experience.Harness(" CLAUDE "),
			TaskFamily:      " implement ",
			TaskHint:        "edit   the generated\nclient",
			RepositoryPaths: []string{
				" internal/client.go ",
				"internal/client.go",
			},
			Model: " model-v1 ",
		},
	)
	second := compilerTestSelect(
		t,
		store,
		now,
		ExperienceSelectionRequest{
			ProjectIdentity: value.Scope.ProjectIdentity,
			Harness:         experience.HarnessClaude,
			TaskFamily:      "implement",
			TaskHint:        "edit the generated client",
			RepositoryPaths: []string{"internal/client.go"},
			Model:           "model-v1",
		},
	)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("normalized selections differ: %+v / %+v", first, second)
	}
}

func TestExperienceCompilerSelectionRejectsInvalidRequest(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 20, 0, 0, time.UTC)
	value := compilerTestExperience(t, "invalid request")
	store := compilerTestStoreFor(now, value)
	service, err := NewExperienceCompilerService(
		store,
		WithExperienceCompilerClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []ExperienceSelectionRequest{
		{
			ProjectIdentity: value.Scope.ProjectIdentity,
			Harness:         experience.Harness("windsurf"),
			RepositoryPaths: []string{},
		},
		{
			ProjectIdentity: value.Scope.ProjectIdentity,
			RepositoryPaths: []string{"/absolute/path.go"},
		},
		{
			ProjectIdentity: value.Scope.ProjectIdentity,
			RepositoryPaths: []string{"internal/**"},
		},
	}
	for _, request := range tests {
		if _, err := service.Select(
			context.Background(),
			request,
		); !errors.Is(err, ErrExperienceCompilerInvalidRequest) {
			t.Fatalf("invalid request %+v error = %v", request, err)
		}
	}
}

func compilerTestSelect(
	t *testing.T,
	store *experienceCompilerTestStore,
	now time.Time,
	request ExperienceSelectionRequest,
) ExperienceSelectionResult {
	t.Helper()
	service, err := NewExperienceCompilerService(
		store,
		WithExperienceCompilerClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Select(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func compilerTestStoreFor(
	now time.Time,
	values ...experience.Experience,
) *experienceCompilerTestStore {
	refs := make([]experience.ExperienceRef, len(values))
	for index, value := range values {
		refs[index] = compilerTestRef(value)
	}
	return &experienceCompilerTestStore{
		generation: compilerTestGeneration(now, refs...),
		values:     compilerTestStoredValues(values...),
	}
}

func compilerTestStoredValues(
	values ...experience.Experience,
) map[experience.ExperienceRef]local.StoredExperience {
	result := make(
		map[experience.ExperienceRef]local.StoredExperience,
		len(values),
	)
	for _, value := range values {
		result[compilerTestRef(value)] = local.StoredExperience{
			Experience:       value,
			CurrentLifecycle: experience.LifecycleActive,
			ExpiresAt:        value.Applicability.ExpiresAt,
		}
	}
	return result
}

func compilerTestGeneration(
	now time.Time,
	refs ...experience.ExperienceRef,
) local.ExperienceGeneration {
	return local.ExperienceGeneration{
		ProjectIdentity: compilerTestProjectIdentity(),
		Generation:      7,
		CompiledHash:    "sha256:" + strings.Repeat("a", 64),
		ExperienceRefs:  append([]experience.ExperienceRef(nil), refs...),
		State:           local.GenerationActive,
		CompiledAt:      now.Add(-time.Minute),
		ActivatedAt:     now.Add(-time.Minute),
	}
}

func compilerTestExperience(
	t *testing.T,
	seed string,
) experience.Experience {
	t.Helper()
	value := localappConflictExperience(t, seed)
	value.Scope = experience.Scope{
		Kind:            experience.ScopeProject,
		ProjectIdentity: value.Scope.ProjectIdentity,
	}
	value.Applicability = experience.Applicability{
		SemanticDescription: "Use the approved project workflow.",
	}
	value.Guidance.Instruction = "Follow the approved project workflow."
	value.Guidance.Rationale = "This avoids repeating the cited failure."
	value.Guidance.Exceptions = nil
	value.Governance.LifecycleState = experience.LifecycleActive
	compilerTestRehash(t, &value)
	return value
}

func compilerTestRehash(t *testing.T, value *experience.Experience) {
	t.Helper()
	value.ContentHash = value.CanonicalContentHash()
	approval := *value.Governance.Approval
	approval.ProposedContentHash = value.ContentHash
	approval.ApprovedContentHash = value.ContentHash
	value.Governance.Approval = &approval
	if err := value.Validate(); err != nil {
		t.Fatalf("compiler test experience is invalid: %v", err)
	}
}

func compilerTestRef(
	value experience.Experience,
) experience.ExperienceRef {
	return experience.ExperienceRef{
		ExperienceID: value.ExperienceID,
		Version:      value.Version,
	}
}

func compilerTestProjectIdentity() string {
	return "git@example.test:doplexlabs/belay-engine.git"
}

func selectionHasRef(
	result ExperienceSelectionResult,
	ref experience.ExperienceRef,
) bool {
	for _, selected := range result.ExperienceRefs {
		if selected == ref {
			return true
		}
	}
	return false
}
