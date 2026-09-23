package localapp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

type experienceLearningTestResolver struct {
	project   missionpack.ResolvedProject
	selectors []missionpack.ProjectSelector
}

func (resolver *experienceLearningTestResolver) ResolveMissionPackProject(
	_ context.Context,
	selector missionpack.ProjectSelector,
) (missionpack.ResolvedProject, error) {
	resolver.selectors = append(resolver.selectors, selector)
	return resolver.project, nil
}

type experienceLearningTestApproval struct {
	actor              string
	asProposedCalls    int
	withContentCalls   int
	withContentRequest ExperienceApprovalWithContentRequest
}

func (approval *experienceLearningTestApproval) ApproveAsProposed(
	_ context.Context,
	_ string,
	_ string,
	actor string,
) (local.ExperienceApprovalResult, error) {
	approval.asProposedCalls++
	approval.actor = actor
	return local.ExperienceApprovalResult{
		Experience: experience.Experience{ExperienceID: "exp_approved"},
	}, nil
}

func (approval *experienceLearningTestApproval) ApproveWithContent(
	_ context.Context,
	request ExperienceApprovalWithContentRequest,
) (local.ExperienceApprovalResult, error) {
	approval.withContentCalls++
	approval.actor = request.Actor
	approval.withContentRequest = request
	return local.ExperienceApprovalResult{
		Experience: experience.Experience{ExperienceID: "exp_edited"},
	}, nil
}

type experienceLearningTestLifecycle struct {
	prepared       []experience.LifecycleAction
	activated      int
	paused         int
	actor          string
	activateResult local.ExperienceLifecycleActionResult
	pauseResult    local.ExperienceLifecycleActionResult
}

func (lifecycle *experienceLearningTestLifecycle) Prepare(
	_ context.Context,
	ref experience.ExperienceRef,
	action experience.LifecycleAction,
) (ExperienceLifecyclePreview, error) {
	lifecycle.prepared = append(lifecycle.prepared, action)
	return ExperienceLifecyclePreview{
		Action:     action,
		Experience: ref,
	}, nil
}

func (lifecycle *experienceLearningTestLifecycle) Activate(
	_ context.Context,
	_ experience.ExperienceRef,
	_ string,
	actor string,
) (local.ExperienceLifecycleActionResult, error) {
	lifecycle.activated++
	lifecycle.actor = actor
	return lifecycle.activateResult, nil
}

func (lifecycle *experienceLearningTestLifecycle) Pause(
	_ context.Context,
	_ experience.ExperienceRef,
	_ string,
	actor string,
) (local.ExperienceLifecycleActionResult, error) {
	lifecycle.paused++
	lifecycle.actor = actor
	return lifecycle.pauseResult, nil
}

type experienceLearningTestCompiler struct {
	projects []string
	result   local.CompileExperienceGenerationResult
	err      error
}

func (compiler *experienceLearningTestCompiler) Compile(
	_ context.Context,
	projectIdentity string,
) (local.CompileExperienceGenerationResult, error) {
	compiler.projects = append(compiler.projects, projectIdentity)
	return compiler.result, compiler.err
}

type experienceLearningTestReviews struct {
	project         string
	harness         experience.Harness
	limit           int
	includeDeferred bool
	items           []ExperienceApprovalPreview
}

func (reviews *experienceLearningTestReviews) List(
	_ context.Context,
	projectIdentity string,
	harness experience.Harness,
	limit int,
	includeDeferred bool,
) ([]ExperienceApprovalPreview, error) {
	reviews.project = projectIdentity
	reviews.harness = harness
	reviews.limit = limit
	reviews.includeDeferred = includeDeferred
	return reviews.items, nil
}

type experienceLearningTestExperienceReader struct {
	stored        local.StoredExperience
	active        []local.StoredExperience
	activeProject string
	activeLimit   int
}

func (reader *experienceLearningTestExperienceReader) GetExperience(
	_ context.Context,
	_ experience.ExperienceRef,
) (local.StoredExperience, error) {
	return reader.stored, nil
}

func (reader *experienceLearningTestExperienceReader) QueryActiveExperiences(
	_ context.Context,
	projectIdentity string,
	limit int,
) ([]local.StoredExperience, error) {
	reader.activeProject = projectIdentity
	reader.activeLimit = limit
	return reader.active, nil
}

type experienceLearningTestReviewActions struct {
	claims       experience.ApprovalTokenClaims
	decodeErr    error
	recordErr    error
	replayed     bool
	decodedToken string
	inputs       []local.ExperienceReviewActionInput
}

func (actions *experienceLearningTestReviewActions) DecodeExperienceApprovalToken(
	token string,
) (experience.ApprovalTokenClaims, error) {
	actions.decodedToken = token
	return actions.claims, actions.decodeErr
}

func (actions *experienceLearningTestReviewActions) RecordExperienceReviewAction(
	_ context.Context,
	input local.ExperienceReviewActionInput,
) (local.ExperienceReviewActionResult, error) {
	actions.inputs = append(actions.inputs, input)
	if actions.recordErr != nil {
		return local.ExperienceReviewActionResult{}, actions.recordErr
	}
	return local.ExperienceReviewActionResult{
		Action: local.ExperienceReviewAction{
			ActionID:       "era_test",
			ProposalID:     input.Claims.ProposalID,
			Disposition:    input.Disposition,
			OccurredAt:     input.OccurredAt,
			AvailableAfter: input.AvailableAfter,
		},
		Replayed: actions.replayed,
	}, nil
}

func TestExperienceLearningListResolvesExactProjectAndBindsHarness(t *testing.T) {
	root := t.TempDir()
	runExperienceLearningGit(t, root, "init", "-b", "main")
	runExperienceLearningGit(
		t,
		root,
		"remote",
		"add",
		"origin",
		"https://example.test/team/project.git",
	)
	project := "https://example.test/team/project.git"
	resolver := &experienceLearningTestResolver{
		project: missionpack.ResolvedProject{
			Identity:     project,
			IdentityKind: "remote",
			Path:         root,
		},
	}
	items := make([]ExperienceApprovalPreview, 5)
	for index := range items {
		items[index].Review.ProjectIdentity = project
		items[index].Review.SemanticProvenance.Harness =
			experience.HarnessCodex
		items[index].Review.Authority = experience.AuthorityNone
	}
	reviews := &experienceLearningTestReviews{items: items}
	service := newExperienceLearningTestService(
		t,
		resolver,
		&experienceLearningTestApproval{},
		&experienceLearningTestLifecycle{},
		&experienceLearningTestCompiler{},
		reviews,
		project,
	)

	result, err := service.List(
		context.Background(),
		root,
		experience.HarnessCodex,
		5,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectIdentity != project ||
		len(result.Items) != 5 ||
		reviews.project != project ||
		reviews.harness != experience.HarnessCodex ||
		reviews.limit != 5 ||
		!reviews.includeDeferred {
		t.Fatalf("list result/review request = %+v/%+v", result, reviews)
	}
	if len(resolver.selectors) != 1 ||
		resolver.selectors[0].RemoteIdentity != project ||
		resolver.selectors[0].ProjectRoot != resolvedTestPath(t, root) ||
		resolver.selectors[0].ProjectPath != resolvedTestPath(t, root) {
		t.Fatalf("project selector = %+v", resolver.selectors)
	}
	if _, err := service.List(
		context.Background(),
		root,
		experience.HarnessClaude,
		5,
		false,
	); !errors.Is(err, ErrExperienceLearningProjectMismatch) {
		t.Fatalf("harness mismatch error = %v", err)
	}
	if _, err := service.List(
		context.Background(),
		root,
		experience.HarnessCodex,
		6,
		false,
	); !errors.Is(err, ErrExperienceLearningInvalidRequest) {
		t.Fatalf("limit error = %v", err)
	}
}

func TestExperienceLearningListActiveProjectsBoundedApprovedGuidance(
	t *testing.T,
) {
	root := t.TempDir()
	runExperienceLearningGit(t, root, "init", "-b", "main")
	project := compilerTestProjectIdentity()
	runExperienceLearningGit(
		t,
		root,
		"remote",
		"add",
		"origin",
		project,
	)
	first := compilerTestExperience(t, "active list first")
	first.Scope.RepositoryPaths = []string{"internal/**"}
	first.Scope.TaskFamilies = []string{"implement"}
	first.Scope.Harnesses = []experience.Harness{experience.HarnessCodex}
	first.Applicability = experience.Applicability{
		SemanticDescription: "Implementation work in internal packages.",
		DeterministicConditions: []experience.DeterministicCondition{{
			Kind:   experience.ConditionPathPattern,
			Values: []string{"internal/**"},
		}},
		Exclusions: []string{"Documentation-only changes."},
	}
	compilerTestRehash(t, &first)
	second := compilerTestExperience(t, "active list second")
	reader := &experienceLearningTestExperienceReader{
		active: []local.StoredExperience{
			{
				Experience:       second,
				CurrentLifecycle: experience.LifecycleActive,
			},
			{
				Experience:       first,
				CurrentLifecycle: experience.LifecycleActive,
			},
		},
	}
	lifecycle := &experienceLearningTestLifecycle{}
	compiler := &experienceLearningTestCompiler{}
	service, err := newExperienceLearningService(
		&experienceLearningTestResolver{
			project: missionpack.ResolvedProject{
				Identity:     project,
				IdentityKind: "remote",
				Path:         root,
			},
		},
		&experienceLearningTestApproval{},
		lifecycle,
		compiler,
		&experienceLearningTestReviews{},
		reader,
		&experienceLearningTestReviewActions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.ListActive(context.Background(), root, 5)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectIdentity != project ||
		len(result.Items) != 2 ||
		reader.activeProject != project ||
		reader.activeLimit != 5 {
		t.Fatalf("active result/reader = %+v/%+v", result, reader)
	}
	if result.Items[0].Experience.ExperienceID >
		result.Items[1].Experience.ExperienceID {
		t.Fatalf("active items are not stable: %+v", result.Items)
	}
	var projected ExperienceLearningActiveItem
	for _, item := range result.Items {
		if item.Experience.ExperienceID == first.ExperienceID {
			projected = item
		}
	}
	if projected.Instruction != first.Guidance.Instruction ||
		projected.Scope.Kind != experience.ScopeProject ||
		!reflect.DeepEqual(
			projected.Scope.RepositoryPaths,
			[]string{"internal/**"},
		) ||
		projected.Applicability.Description !=
			"Implementation work in internal packages." ||
		projected.VerifierSummary == "" ||
		projected.LifecycleState != experience.LifecycleActive {
		t.Fatalf("active projection = %+v", projected)
	}
	if lifecycle.activated != 0 ||
		lifecycle.paused != 0 ||
		len(compiler.projects) != 0 {
		t.Fatalf(
			"listing mutated lifecycle/compiler = %+v/%+v",
			lifecycle,
			compiler,
		)
	}
}

func TestExperienceLearningListActiveFailsClosedOnMismatchAndOverflow(
	t *testing.T,
) {
	root := t.TempDir()
	runExperienceLearningGit(t, root, "init", "-b", "main")
	project := compilerTestProjectIdentity()
	runExperienceLearningGit(
		t,
		root,
		"remote",
		"add",
		"origin",
		project,
	)
	value := compilerTestExperience(t, "active list mismatch")
	resolver := &experienceLearningTestResolver{
		project: missionpack.ResolvedProject{
			Identity:     project,
			IdentityKind: "remote",
			Path:         root,
		},
	}
	newService := func(values []local.StoredExperience) *ExperienceLearningService {
		service, err := newExperienceLearningService(
			resolver,
			&experienceLearningTestApproval{},
			&experienceLearningTestLifecycle{},
			&experienceLearningTestCompiler{},
			&experienceLearningTestReviews{},
			&experienceLearningTestExperienceReader{active: values},
			&experienceLearningTestReviewActions{},
		)
		if err != nil {
			t.Fatal(err)
		}
		return service
	}

	for _, stored := range []local.StoredExperience{
		{
			Experience:       value,
			CurrentLifecycle: experience.LifecyclePaused,
		},
		func() local.StoredExperience {
			other := value
			other.Scope.ProjectIdentity = "git@example.test:other/project.git"
			return local.StoredExperience{
				Experience:       other,
				CurrentLifecycle: experience.LifecycleActive,
			}
		}(),
		func() local.StoredExperience {
			unapproved := value
			unapproved.Governance.Authority = experience.AuthorityNone
			return local.StoredExperience{
				Experience:       unapproved,
				CurrentLifecycle: experience.LifecycleActive,
			}
		}(),
	} {
		if _, err := newService(
			[]local.StoredExperience{stored},
		).ListActive(
			context.Background(),
			root,
			5,
		); !errors.Is(err, ErrExperienceLearningProjectMismatch) {
			t.Fatalf("active mismatch error = %v", err)
		}
	}

	overflow := make([]local.StoredExperience, 6)
	for index := range overflow {
		overflow[index] = local.StoredExperience{
			Experience: compilerTestExperience(
				t,
				fmt.Sprintf("active overflow %d", index),
			),
			CurrentLifecycle: experience.LifecycleActive,
		}
	}
	if _, err := newService(overflow).ListActive(
		context.Background(),
		root,
		5,
	); !errors.Is(err, ErrExperienceLearningResultOverflow) {
		t.Fatalf("active overflow error = %v", err)
	}
}

func TestExperienceLearningApprovalUsesUserAndDoesNotActivate(t *testing.T) {
	approval := &experienceLearningTestApproval{}
	lifecycle := &experienceLearningTestLifecycle{}
	compiler := &experienceLearningTestCompiler{}
	service := newExperienceLearningTestService(
		t,
		&experienceLearningTestResolver{},
		approval,
		lifecycle,
		compiler,
		&experienceLearningTestReviews{},
		"project",
	)

	if _, err := service.ApproveAsProposed(
		context.Background(),
		"proposal",
		"token",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveWithContent(
		context.Background(),
		ExperienceLearningApprovalWithContentRequest{
			ProposalID:  "proposal",
			ActionToken: "token",
			Mode:        experience.ApprovalUserEdited,
		},
	); err != nil {
		t.Fatal(err)
	}
	if approval.actor != "user" ||
		approval.asProposedCalls != 1 ||
		approval.withContentCalls != 1 ||
		approval.withContentRequest.Actor != "user" ||
		lifecycle.activated != 0 ||
		lifecycle.paused != 0 ||
		len(compiler.projects) != 0 {
		t.Fatalf(
			"approval/lifecycle/compiler = %+v/%+v/%+v",
			approval,
			lifecycle,
			compiler,
		)
	}
}

func TestExperienceLearningDeferAndRejectUseFixedReviewSemantics(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 30, 0, 0, time.UTC)
	claims := experience.ApprovalTokenClaims{
		ProposalID: "proposal",
	}
	actions := &experienceLearningTestReviewActions{claims: claims}
	approval := &experienceLearningTestApproval{}
	lifecycle := &experienceLearningTestLifecycle{}
	compiler := &experienceLearningTestCompiler{}
	service := newExperienceLearningTestServiceWithReviewActions(
		t,
		&experienceLearningTestResolver{},
		approval,
		lifecycle,
		compiler,
		&experienceLearningTestReviews{},
		"project",
		actions,
		func() time.Time { return now },
	)

	deferred, err := service.Defer(
		context.Background(),
		" proposal ",
		" token ",
	)
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := service.Reject(
		context.Background(),
		"proposal",
		"token",
	)
	if err != nil {
		t.Fatal(err)
	}
	if actions.decodedToken != "token" || len(actions.inputs) != 2 {
		t.Fatalf(
			"decoded token/inputs = %q/%+v",
			actions.decodedToken,
			actions.inputs,
		)
	}
	deferInput := actions.inputs[0]
	wantAvailableAfter := now.Add(7 * 24 * time.Hour)
	if deferInput.Actor != "user" ||
		deferInput.Disposition != local.ExperienceReviewDefer ||
		!deferInput.OccurredAt.Equal(now) ||
		deferInput.AvailableAfter == nil ||
		!deferInput.AvailableAfter.Equal(wantAvailableAfter) {
		t.Fatalf("defer input = %+v", deferInput)
	}
	rejectInput := actions.inputs[1]
	if rejectInput.Actor != "user" ||
		rejectInput.Disposition != local.ExperienceReviewReject ||
		!rejectInput.OccurredAt.Equal(now) ||
		rejectInput.AvailableAfter != nil {
		t.Fatalf("reject input = %+v", rejectInput)
	}
	if deferred.Disposition != ExperienceLearningReviewDefer ||
		deferred.AvailableAfter == nil ||
		!deferred.AvailableAfter.Equal(wantAvailableAfter) ||
		rejected.Disposition != ExperienceLearningReviewReject ||
		rejected.AvailableAfter != nil ||
		approval.asProposedCalls != 0 ||
		approval.withContentCalls != 0 ||
		lifecycle.activated != 0 ||
		lifecycle.paused != 0 ||
		len(compiler.projects) != 0 {
		t.Fatalf(
			"results/approval/lifecycle/compiler = %+v/%+v/%+v/%+v/%+v",
			deferred,
			rejected,
			approval,
			lifecycle,
			compiler,
		)
	}
}

func TestExperienceLearningReviewRejectsProposalTokenMismatch(t *testing.T) {
	actions := &experienceLearningTestReviewActions{
		claims: experience.ApprovalTokenClaims{
			ProposalID: "other-proposal",
		},
	}
	service := newExperienceLearningTestServiceWithReviewActions(
		t,
		&experienceLearningTestResolver{},
		&experienceLearningTestApproval{},
		&experienceLearningTestLifecycle{},
		&experienceLearningTestCompiler{},
		&experienceLearningTestReviews{},
		"project",
		actions,
		time.Now,
	)

	if _, err := service.Defer(
		context.Background(),
		"proposal",
		"token",
	); !errors.Is(err, ErrExperienceLearningReviewInvalid) {
		t.Fatalf("mismatch error = %v", err)
	}
	if len(actions.inputs) != 0 {
		t.Fatalf("review writes = %+v", actions.inputs)
	}
	for _, input := range []struct {
		proposal string
		token    string
	}{
		{proposal: "", token: "token"},
		{proposal: "proposal", token: ""},
	} {
		if _, err := service.Reject(
			context.Background(),
			input.proposal,
			input.token,
		); !errors.Is(err, ErrExperienceLearningReviewInvalid) {
			t.Fatalf("empty input error = %v", err)
		}
	}
}

func TestExperienceLearningReviewMapsErrorsAndPropagatesReplay(t *testing.T) {
	tests := []struct {
		name      string
		decodeErr error
		recordErr error
		want      error
	}{
		{
			name:      "invalid token",
			decodeErr: local.ErrExperienceApprovalTokenInvalid,
			want:      ErrExperienceLearningReviewInvalid,
		},
		{
			name:      "expired token",
			decodeErr: local.ErrExperienceApprovalTokenExpired,
			want:      ErrExperienceLearningReviewExpired,
		},
		{
			name:      "invalid action",
			recordErr: local.ErrExperienceReviewActionInvalid,
			want:      ErrExperienceLearningReviewInvalid,
		},
		{
			name:      "stale action",
			recordErr: local.ErrExperienceReviewActionStale,
			want:      ErrExperienceLearningReviewStale,
		},
		{
			name:      "conflicting action",
			recordErr: local.ErrExperienceReviewActionConflict,
			want:      ErrExperienceLearningReviewConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actions := &experienceLearningTestReviewActions{
				claims: experience.ApprovalTokenClaims{
					ProposalID: "proposal",
				},
				decodeErr: test.decodeErr,
				recordErr: test.recordErr,
			}
			service := newExperienceLearningTestServiceWithReviewActions(
				t,
				&experienceLearningTestResolver{},
				&experienceLearningTestApproval{},
				&experienceLearningTestLifecycle{},
				&experienceLearningTestCompiler{},
				&experienceLearningTestReviews{},
				"project",
				actions,
				time.Now,
			)
			if _, err := service.Reject(
				context.Background(),
				"proposal",
				"token",
			); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}

	actions := &experienceLearningTestReviewActions{
		claims: experience.ApprovalTokenClaims{
			ProposalID: "proposal",
		},
		replayed: true,
	}
	service := newExperienceLearningTestServiceWithReviewActions(
		t,
		&experienceLearningTestResolver{},
		&experienceLearningTestApproval{},
		&experienceLearningTestLifecycle{},
		&experienceLearningTestCompiler{},
		&experienceLearningTestReviews{},
		"project",
		actions,
		time.Now,
	)
	result, err := service.Reject(
		context.Background(),
		"proposal",
		"token",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed {
		t.Fatalf("replayed result = %+v", result)
	}
}

func TestExperienceLearningSeparatesLifecycleActionsAndCompilesBoth(t *testing.T) {
	project := "project"
	ref := experience.ExperienceRef{
		ExperienceID: "exp_learning",
		Version:      1,
	}
	lifecycle := &experienceLearningTestLifecycle{
		activateResult: local.ExperienceLifecycleActionResult{
			Transition: experience.LifecycleTransition{
				Experience: ref,
				ToState:    experience.LifecycleActive,
			},
		},
		pauseResult: local.ExperienceLifecycleActionResult{
			Transition: experience.LifecycleTransition{
				Experience: ref,
				ToState:    experience.LifecyclePaused,
			},
		},
	}
	compiler := &experienceLearningTestCompiler{
		result: local.CompileExperienceGenerationResult{
			Generation: local.ExperienceGeneration{
				ProjectIdentity: project,
				Generation:      7,
			},
			Replayed: true,
		},
	}
	service := newExperienceLearningTestService(
		t,
		&experienceLearningTestResolver{},
		&experienceLearningTestApproval{},
		lifecycle,
		compiler,
		&experienceLearningTestReviews{},
		project,
	)

	for _, action := range []experience.LifecycleAction{
		experience.LifecycleActionActivate,
		experience.LifecycleActionPause,
	} {
		preview, err := service.PrepareLifecycle(
			context.Background(),
			ref,
			action,
		)
		if err != nil || preview.Action != action {
			t.Fatalf("prepare %s = %+v, %v", action, preview, err)
		}
		result, err := service.ApplyLifecycle(
			context.Background(),
			ref,
			action,
			"token",
		)
		if err != nil {
			t.Fatalf("apply %s: %v", action, err)
		}
		if result.Compilation.Generation.Generation != 7 ||
			!result.Compilation.Replayed {
			t.Fatalf("apply %s result = %+v", action, result)
		}
	}
	if lifecycle.activated != 1 ||
		lifecycle.paused != 1 ||
		lifecycle.actor != "user" ||
		len(compiler.projects) != 2 ||
		compiler.projects[0] != project ||
		compiler.projects[1] != project {
		t.Fatalf("lifecycle/compiler = %+v/%+v", lifecycle, compiler)
	}
	if _, err := service.PrepareLifecycle(
		context.Background(),
		ref,
		experience.LifecycleActionExpire,
	); !errors.Is(err, ErrExperienceLearningUnsupportedLifecycle) {
		t.Fatalf("unsupported prepare error = %v", err)
	}
}

func TestExperienceLearningClassifiesCompileAfterTransitionFailure(t *testing.T) {
	project := "project"
	ref := experience.ExperienceRef{
		ExperienceID: "exp_partial",
		Version:      1,
	}
	transition := local.ExperienceLifecycleActionResult{
		Transition: experience.LifecycleTransition{
			Experience: ref,
			ToState:    experience.LifecycleActive,
		},
	}
	compileErr := errors.New("compile unavailable")
	lifecycle := &experienceLearningTestLifecycle{
		activateResult: transition,
	}
	compiler := &experienceLearningTestCompiler{err: compileErr}
	service := newExperienceLearningTestService(
		t,
		&experienceLearningTestResolver{},
		&experienceLearningTestApproval{},
		lifecycle,
		compiler,
		&experienceLearningTestReviews{},
		project,
	)

	result, err := service.ApplyLifecycle(
		context.Background(),
		ref,
		experience.LifecycleActionActivate,
		"token",
	)
	var partial *ExperienceLearningPartialDeliveryError
	if !errors.As(err, &partial) ||
		!errors.Is(err, compileErr) ||
		!reflect.DeepEqual(partial.Transition, transition) ||
		!reflect.DeepEqual(result.Transition, transition) ||
		lifecycle.activated != 1 ||
		len(compiler.projects) != 1 {
		t.Fatalf(
			"partial result/error/lifecycle/compiler = %+v/%v/%+v/%+v",
			result,
			err,
			lifecycle,
			compiler,
		)
	}
}

func newExperienceLearningTestService(
	t *testing.T,
	resolver experienceLearningProjectResolver,
	approval experienceLearningApproval,
	lifecycle experienceLearningLifecycle,
	compiler experienceLearningCompiler,
	reviews experienceLearningReviewProvider,
	projectIdentity string,
) *ExperienceLearningService {
	t.Helper()
	service, err := newExperienceLearningService(
		resolver,
		approval,
		lifecycle,
		compiler,
		reviews,
		&experienceLearningTestExperienceReader{
			stored: local.StoredExperience{
				Experience: experience.Experience{
					Scope: experience.Scope{
						ProjectIdentity: projectIdentity,
					},
				},
			},
		},
		&experienceLearningTestReviewActions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func newExperienceLearningTestServiceWithReviewActions(
	t *testing.T,
	resolver experienceLearningProjectResolver,
	approval experienceLearningApproval,
	lifecycle experienceLearningLifecycle,
	compiler experienceLearningCompiler,
	reviews experienceLearningReviewProvider,
	projectIdentity string,
	actions experienceLearningReviewActions,
	clock func() time.Time,
) *ExperienceLearningService {
	t.Helper()
	service, err := newExperienceLearningService(
		resolver,
		approval,
		lifecycle,
		compiler,
		reviews,
		&experienceLearningTestExperienceReader{
			stored: local.StoredExperience{
				Experience: experience.Experience{
					Scope: experience.Scope{
						ProjectIdentity: projectIdentity,
					},
				},
			},
		},
		actions,
	)
	if err != nil {
		t.Fatal(err)
	}
	WithExperienceLearningClock(clock)(service)
	return service
}

func runExperienceLearningGit(
	t *testing.T,
	cwd string,
	args ...string,
) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = cwd
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func resolvedTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(resolved)
}

func TestExperienceLearningRejectsNonAbsoluteCWD(t *testing.T) {
	service := newExperienceLearningTestService(
		t,
		&experienceLearningTestResolver{},
		&experienceLearningTestApproval{},
		&experienceLearningTestLifecycle{},
		&experienceLearningTestCompiler{},
		&experienceLearningTestReviews{},
		filepath.Clean("/project"),
	)
	if _, err := service.List(
		context.Background(),
		"relative",
		experience.HarnessCodex,
		1,
		false,
	); !errors.Is(err, ErrExperienceLearningInvalidRequest) {
		t.Fatalf("relative cwd error = %v", err)
	}
}

func TestExperienceLearningListBindsCursorHarness(t *testing.T) {
	root := t.TempDir()
	runExperienceLearningGit(t, root, "init", "-b", "main")
	runExperienceLearningGit(
		t,
		root,
		"remote",
		"add",
		"origin",
		"https://example.test/team/project.git",
	)
	project := "https://example.test/team/project.git"
	resolver := &experienceLearningTestResolver{
		project: missionpack.ResolvedProject{
			Identity:     project,
			IdentityKind: "remote",
			Path:         root,
		},
	}
	items := make([]ExperienceApprovalPreview, 2)
	for index := range items {
		items[index].Review.ProjectIdentity = project
		items[index].Review.SemanticProvenance.Harness =
			experience.HarnessCursor
		items[index].Review.Authority = experience.AuthorityNone
	}
	reviews := &experienceLearningTestReviews{items: items}
	service := newExperienceLearningTestService(
		t,
		resolver,
		&experienceLearningTestApproval{},
		&experienceLearningTestLifecycle{},
		&experienceLearningTestCompiler{},
		reviews,
		project,
	)

	result, err := service.List(
		context.Background(),
		root,
		experience.HarnessCursor,
		5,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 ||
		reviews.harness != experience.HarnessCursor {
		t.Fatalf("cursor list result/request = %+v/%+v", result, reviews)
	}
	if _, err := service.List(
		context.Background(),
		root,
		experience.Harness("windsurf"),
		5,
		false,
	); !errors.Is(err, ErrExperienceLearningInvalidRequest) {
		t.Fatalf("unsupported harness error = %v", err)
	}
}

func TestEvaluationHarnessRecognizesEverySupportedAgent(t *testing.T) {
	for agent, want := range map[string]experience.Harness{
		"claude":       experience.HarnessClaude,
		"claude-code":  experience.HarnessClaude,
		"codex":        experience.HarnessCodex,
		"cursor":       experience.HarnessCursor,
		"cursor-agent": experience.HarnessCursor,
		"Cursor":       experience.HarnessCursor,
		"antigravity":  experience.HarnessAntigravity,
		"Antigravity":  experience.HarnessAntigravity,
		"agy":          experience.Harness(""),
		"windsurf":     experience.Harness(""),
	} {
		if got := evaluationHarness(agent); got != want {
			t.Fatalf("evaluationHarness(%q) = %q, want %q", agent, got, want)
		}
	}
}
