package localmcp

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/localapp"
	"github.com/DoplexLabs/belay-engine/internal/presentation/readmodel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testExperienceLearningService struct {
	listResult      localapp.ExperienceLearningListResult
	listErr         error
	activeResult    localapp.ExperienceLearningActiveListResult
	activeErr       error
	approval        ExperienceLearningApprovalResult
	approvalErr     error
	prepare         localapp.ExperienceLifecyclePreview
	prepareErr      error
	apply           localapp.ExperienceLearningLifecycleResult
	applyErr        error
	listCalls       int
	includeDeferred bool
	activeCalls     int
	activeCWD       string
	activeLimit     int
	approvalMode    experience.ApprovalMode
	approved        *experience.ExperienceProposal
	lifecycleCalls  []experience.LifecycleAction
	review          localapp.ExperienceLearningReviewResult
	reviewErr       error
	reviewCalls     []localapp.ExperienceLearningReviewDisposition
}

func (service *testExperienceLearningService) List(
	_ context.Context,
	_ string,
	_ experience.Harness,
	_ int,
	includeDeferred bool,
) (localapp.ExperienceLearningListResult, error) {
	service.listCalls++
	service.includeDeferred = includeDeferred
	return service.listResult, service.listErr
}

func (service *testExperienceLearningService) ListActive(
	_ context.Context,
	cwd string,
	limit int,
) (localapp.ExperienceLearningActiveListResult, error) {
	service.activeCalls++
	service.activeCWD = cwd
	service.activeLimit = limit
	return service.activeResult, service.activeErr
}

func (service *testExperienceLearningService) ApproveAsProposed(
	_ context.Context,
	_ string,
	_ string,
) (ExperienceLearningApprovalResult, error) {
	service.approvalMode = experience.ApprovalAsProposed
	return service.approval, service.approvalErr
}

func (service *testExperienceLearningService) ApproveWithContent(
	_ context.Context,
	request localapp.ExperienceLearningApprovalWithContentRequest,
) (ExperienceLearningApprovalResult, error) {
	service.approvalMode = request.Mode
	value := request.ApprovedContent
	service.approved = &value
	return service.approval, service.approvalErr
}

func (service *testExperienceLearningService) Defer(
	_ context.Context,
	_ string,
	_ string,
) (localapp.ExperienceLearningReviewResult, error) {
	service.reviewCalls = append(
		service.reviewCalls,
		localapp.ExperienceLearningReviewDefer,
	)
	return service.review, service.reviewErr
}

func (service *testExperienceLearningService) Reject(
	_ context.Context,
	_ string,
	_ string,
) (localapp.ExperienceLearningReviewResult, error) {
	service.reviewCalls = append(
		service.reviewCalls,
		localapp.ExperienceLearningReviewReject,
	)
	return service.review, service.reviewErr
}

func (service *testExperienceLearningService) PrepareLifecycle(
	_ context.Context,
	_ experience.ExperienceRef,
	action experience.LifecycleAction,
) (localapp.ExperienceLifecyclePreview, error) {
	service.lifecycleCalls = append(service.lifecycleCalls, action)
	return service.prepare, service.prepareErr
}

func (service *testExperienceLearningService) ApplyLifecycle(
	_ context.Context,
	_ experience.ExperienceRef,
	action experience.LifecycleAction,
	_ string,
) (localapp.ExperienceLearningLifecycleResult, error) {
	service.lifecycleCalls = append(service.lifecycleCalls, action)
	return service.apply, service.applyErr
}

func TestExperienceLearningToolsAreConditionalClosedWorldAndAnnotated(
	t *testing.T,
) {
	baseline := newTestClient(t, &testRepository{})
	baselineTools, err := baseline.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range baselineTools.Tools {
		if isExperienceLearningTool(tool.Name) {
			t.Fatalf("unconfigured server advertised %q", tool.Name)
		}
	}

	session := newExperienceLearningTestClient(
		t,
		&testExperienceLearningService{},
	)
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[string]*mcp.Tool)
	for _, tool := range result.Tools {
		if isExperienceLearningTool(tool.Name) {
			found[tool.Name] = tool
		}
	}
	if len(found) != 6 {
		t.Fatalf("experience learning tools = %v, want exactly six", found)
	}
	for _, name := range []string{
		"list_active_experiences",
		"list_experience_proposals",
		"approve_experience",
		"resolve_experience_proposal",
		"prepare_experience_lifecycle",
		"apply_experience_lifecycle",
	} {
		tool := found[name]
		if tool == nil {
			t.Fatalf("%q was not registered", name)
		}
		if tool.Annotations == nil ||
			tool.Annotations.DestructiveHint == nil ||
			*tool.Annotations.DestructiveHint ||
			tool.Annotations.OpenWorldHint == nil ||
			*tool.Annotations.OpenWorldHint {
			t.Fatalf("%q annotations = %#v", name, tool.Annotations)
		}
		wantReadOnly := name == "list_active_experiences" ||
			name == "list_experience_proposals" ||
			name == "prepare_experience_lifecycle"
		if tool.Annotations.ReadOnlyHint != wantReadOnly ||
			!tool.Annotations.IdempotentHint {
			t.Fatalf("%q annotations = %#v", name, tool.Annotations)
		}
		if !wantReadOnly &&
			!strings.Contains(
				strings.ToLower(tool.Description),
				"explicitly confirm",
			) {
			t.Fatalf("%q description lacks explicit confirmation: %q", name, tool.Description)
		}
	}
}

func TestResolveExperienceProposalDispatchesDeferRejectAndReplay(
	t *testing.T,
) {
	now := testExperienceLearningTime()
	availableAfter := now.Add(7 * 24 * time.Hour)
	service := &testExperienceLearningService{
		review: localapp.ExperienceLearningReviewResult{
			ProposalID:     "exs_proposal",
			Disposition:    localapp.ExperienceLearningReviewDefer,
			OccurredAt:     now,
			AvailableAfter: &availableAfter,
		},
	}
	session := newExperienceLearningTestClient(t, service)

	deferred := callTool(
		t,
		session,
		"resolve_experience_proposal",
		map[string]any{
			"proposal_id":  "exs_proposal",
			"action_token": "approval-token",
			"disposition":  "defer",
		},
	)
	if deferred.IsError {
		t.Fatalf("defer failed: %v", deferred.Content)
	}
	readModel := asObject(
		t,
		asObject(t, deferred.StructuredContent)["readmodel"],
	)
	if readModel["proposal_id"] != "exs_proposal" ||
		readModel["disposition"] != "defer" ||
		readModel["available_after"] == nil ||
		readModel["replayed"] != false {
		t.Fatalf("defer output = %#v", readModel)
	}

	service.review = localapp.ExperienceLearningReviewResult{
		ProposalID:  "exs_proposal",
		Disposition: localapp.ExperienceLearningReviewReject,
		OccurredAt:  now,
		Replayed:    true,
	}
	rejected := callTool(
		t,
		session,
		"resolve_experience_proposal",
		map[string]any{
			"proposal_id":  "exs_proposal",
			"action_token": "approval-token",
			"disposition":  "reject",
		},
	)
	if rejected.IsError {
		t.Fatalf("reject failed: %v", rejected.Content)
	}
	readModel = asObject(
		t,
		asObject(t, rejected.StructuredContent)["readmodel"],
	)
	if readModel["disposition"] != "reject" ||
		readModel["replayed"] != true {
		t.Fatalf("reject output = %#v", readModel)
	}
	if _, exists := readModel["available_after"]; exists {
		t.Fatalf("reject output included available_after: %#v", readModel)
	}
	if len(service.reviewCalls) != 2 ||
		service.reviewCalls[0] !=
			localapp.ExperienceLearningReviewDefer ||
		service.reviewCalls[1] !=
			localapp.ExperienceLearningReviewReject {
		t.Fatalf("review calls = %#v", service.reviewCalls)
	}
}

func TestResolveExperienceProposalStrictSchemaAndErrorMapping(
	t *testing.T,
) {
	service := &testExperienceLearningService{}
	session := newExperienceLearningTestClient(t, service)
	for _, input := range []map[string]any{
		{
			"proposal_id":  "exs_proposal",
			"action_token": "approval-token",
			"disposition":  "dismiss",
		},
		{
			"proposal_id":  "exs_proposal",
			"action_token": "approval-token",
			"disposition":  "defer",
			"unknown":      true,
		},
	} {
		assertStrictToolError(
			t,
			callTool(
				t,
				session,
				"resolve_experience_proposal",
				input,
			),
			strictInvalidInput,
		)
	}
	if len(service.reviewCalls) != 0 {
		t.Fatalf("invalid schema invoked service: %#v", service.reviewCalls)
	}

	now := testExperienceLearningTime()
	service.review = localapp.ExperienceLearningReviewResult{
		ProposalID:  "exs_proposal",
		Disposition: localapp.ExperienceLearningReviewReject,
		OccurredAt:  now,
	}
	for _, test := range []struct {
		err  error
		want strictToolErrorCode
	}{
		{
			localapp.ErrExperienceLearningReviewInvalid,
			strictInvalidInput,
		},
		{
			localapp.ErrExperienceLearningReviewStale,
			strictReadFailed,
		},
		{
			localapp.ErrExperienceLearningReviewExpired,
			strictReadFailed,
		},
		{
			localapp.ErrExperienceLearningReviewConflict,
			strictReadFailed,
		},
		{
			errors.New("private storage detail"),
			strictReadFailed,
		},
	} {
		service.reviewErr = test.err
		assertStrictToolError(
			t,
			callTool(
				t,
				session,
				"resolve_experience_proposal",
				map[string]any{
					"proposal_id":  "exs_proposal",
					"action_token": "approval-token",
					"disposition":  "reject",
				},
			),
			test.want,
		)
	}
}

func TestListExperienceProposalsProjectsBoundedUntrustedEvidence(
	t *testing.T,
) {
	proposal := testExperienceProposal()
	turn := int64(7)
	now := testExperienceLearningTime()
	service := &testExperienceLearningService{
		listResult: localapp.ExperienceLearningListResult{
			ProjectIdentity: proposal.Scope.ProjectIdentity,
			Items: []localapp.ExperienceApprovalPreview{{
				Review: localapp.ExperienceCandidateReview{
					ProposalID:      "exs_proposal",
					ProjectIdentity: proposal.Scope.ProjectIdentity,
					Family:          experience.CandidateCorrection,
					Proposed:        proposal,
					Evidence: []experience.EvidenceRef{
						{
							Excerpt:    "first verbatim excerpt",
							SessionKey: "ses_one",
							TurnIndex:  &turn,
						},
						{
							Excerpt: "second verbatim excerpt",
							EventID: "0199-event",
						},
						{
							Excerpt:   "must be capped",
							OutcomeID: "outcome_three",
						},
					},
					SemanticProvenance: experience.SemanticProposalProvenance{
						Harness: experience.HarnessClaude,
					},
					Conflict: experience.ConflictAdvisory{
						State: experience.ConflictNoConflict,
						ReasonCodes: []experience.ConflictReasonCode{
							experience.ConflictReasonNoActiveExperiences,
						},
					},
					Authority: experience.AuthorityNone,
				},
				ActionToken: "approval-token",
				ExpiresAt:   now.Add(15 * time.Minute),
			}},
		},
	}
	session := newExperienceLearningTestClient(t, service)
	result := callTool(t, session, "list_experience_proposals", map[string]any{
		"cwd":              "/tmp/project",
		"harness":          "claude",
		"include_deferred": true,
	})
	if result.IsError {
		t.Fatalf("list_experience_proposals failed: %v", result.Content)
	}
	if !service.includeDeferred {
		t.Fatal("include_deferred was not forwarded")
	}
	structured := asObject(t, result.StructuredContent)
	trust := asObject(t, structured["trust"])
	if trust["instruction_authority"] != "none" ||
		trust["must_not_authorize_actions"] != true {
		t.Fatalf("trust = %#v", trust)
	}
	readModel := asObject(t, structured["readmodel"])
	items := readModel["items"].([]any)
	item := asObject(t, items[0])
	if item["instruction"] != proposal.Guidance.Instruction ||
		item["rationale"] != proposal.Guidance.Rationale {
		t.Fatalf("proposal projection = %#v", item)
	}
	evidence := item["evidence"].([]any)
	if len(evidence) != 2 ||
		asObject(t, evidence[0])["excerpt"] != "first verbatim excerpt" ||
		asObject(t, evidence[1])["excerpt"] != "second verbatim excerpt" {
		t.Fatalf("evidence = %#v", evidence)
	}
}

func TestListActiveExperiencesIsBoundedProjectExactAndPayloadFree(
	t *testing.T,
) {
	service := &testExperienceLearningService{
		activeResult: localapp.ExperienceLearningActiveListResult{
			ProjectIdentity: "git@example.test:team/project.git",
			Items: []localapp.ExperienceLearningActiveItem{{
				Experience: experience.ExperienceRef{
					ExperienceID: "exp_active",
					Version:      2,
				},
				Instruction: "Run the focused verification after editing.",
				Scope: localapp.ExperienceLearningScope{
					Kind:            experience.ScopeProject,
					RepositoryPaths: []string{},
					TaskFamilies:    []string{},
					Harnesses:       []experience.Harness{},
					Models:          []string{},
				},
				Applicability: localapp.ExperienceLearningApplicability{
					Description:             "Implementation work in internal packages.",
					DeterministicConditions: []experience.DeterministicCondition{},
					Exclusions:              []string{},
				},
				VerifierSummary: "Run go test ./internal/... successfully.",
				LifecycleState:  experience.LifecycleActive,
			}},
		},
	}
	session := newExperienceLearningTestClient(t, service)
	result := callTool(
		t,
		session,
		"list_active_experiences",
		map[string]any{"cwd": "/tmp/project", "limit": 5},
	)
	if result.IsError {
		text := ""
		if len(result.Content) > 0 {
			if content, ok := result.Content[0].(*mcp.TextContent); ok {
				text = content.Text
			}
		}
		t.Fatalf(
			"list_active_experiences failed: %s (calls=%d cwd=%q limit=%d)",
			text,
			service.activeCalls,
			service.activeCWD,
			service.activeLimit,
		)
	}
	if service.activeCalls != 1 ||
		service.activeCWD != "/tmp/project" ||
		service.activeLimit != 5 {
		t.Fatalf("active request = %+v", service)
	}
	readModel := asObject(
		t,
		asObject(t, result.StructuredContent)["readmodel"],
	)
	if readModel["project"] != "git@example.test:team/project.git" {
		t.Fatalf("active project = %#v", readModel)
	}
	items := readModel["items"].([]any)
	item := asObject(t, items[0])
	if item["lifecycle_state"] != "active" ||
		item["instruction"] !=
			"Run the focused verification after editing." ||
		item["verifier_summary"] !=
			"Run go test ./internal/... successfully." {
		t.Fatalf("active item = %#v", item)
	}
	for _, forbidden := range []string{
		"payload",
		"provenance",
		"content_hash",
		"evidence",
		"action_token",
	} {
		if _, exists := item[forbidden]; exists {
			t.Fatalf("active item exposed %q: %#v", forbidden, item)
		}
	}
}

func TestListActiveExperiencesStrictSchemaAndFailClosedOutput(
	t *testing.T,
) {
	service := &testExperienceLearningService{}
	session := newExperienceLearningTestClient(t, service)
	for _, input := range []map[string]any{
		{"cwd": "relative"},
		{"cwd": "/tmp/project", "limit": 6},
		{"cwd": "/tmp/project", "harness": "codex"},
	} {
		assertStrictToolError(
			t,
			callTool(t, session, "list_active_experiences", input),
			strictInvalidInput,
		)
	}
	if service.activeCalls != 0 {
		t.Fatalf("invalid input invoked service %d times", service.activeCalls)
	}

	service.activeResult = localapp.ExperienceLearningActiveListResult{
		ProjectIdentity: "project",
		Items: []localapp.ExperienceLearningActiveItem{{
			Experience: experience.ExperienceRef{
				ExperienceID: "exp_active",
				Version:      1,
			},
			Instruction:     "Use the active guidance.",
			VerifierSummary: "Observe the result.",
			Scope: localapp.ExperienceLearningScope{
				Kind:            experience.ScopeProject,
				RepositoryPaths: []string{},
				TaskFamilies:    []string{},
				Harnesses:       []experience.Harness{},
				Models:          []string{},
			},
			Applicability: localapp.ExperienceLearningApplicability{
				Description:             "Project work.",
				DeterministicConditions: []experience.DeterministicCondition{},
				Exclusions:              []string{},
			},
			LifecycleState: experience.LifecyclePaused,
		}},
	}
	assertStrictToolError(
		t,
		callTool(
			t,
			session,
			"list_active_experiences",
			map[string]any{"cwd": "/tmp/project"},
		),
		strictReadFailed,
	)
}

func TestApproveExperienceRequiresModeSpecificContentAndDoesNotActivate(
	t *testing.T,
) {
	approved := testApprovedExperience(t)
	service := &testExperienceLearningService{
		approval: ExperienceLearningApprovalResult{
			Experience: approved,
		},
	}
	session := newExperienceLearningTestClient(t, service)

	result := callTool(t, session, "approve_experience", map[string]any{
		"proposal_id":   "exs_proposal",
		"action_token":  "approval-token",
		"approval_mode": "as_proposed",
	})
	if result.IsError {
		t.Fatalf("approve_experience failed: %v", result.Content)
	}
	readModel := asObject(t, asObject(t, result.StructuredContent)["readmodel"])
	if readModel["lifecycle_state"] != "approved" ||
		service.approvalMode != experience.ApprovalAsProposed {
		t.Fatalf("approval result = %#v, mode = %q", readModel, service.approvalMode)
	}

	assertStrictToolError(t, callTool(t, session, "approve_experience", map[string]any{
		"proposal_id":   "exs_proposal",
		"action_token":  "approval-token",
		"approval_mode": "narrowed",
	}), strictInvalidInput)
	assertStrictToolError(t, callTool(t, session, "approve_experience", map[string]any{
		"proposal_id":      "exs_proposal",
		"action_token":     "approval-token",
		"approval_mode":    "as_proposed",
		"approved_content": testExperienceProposal(),
	}), strictInvalidInput)

	edited := testExperienceProposal()
	edited.Guidance.Instruction = "Use the edited approved workflow."
	result = callTool(t, session, "approve_experience", map[string]any{
		"proposal_id":      "exs_proposal",
		"action_token":     "approval-token",
		"approval_mode":    "user_edited",
		"approved_content": edited,
	})
	if result.IsError ||
		service.approvalMode != experience.ApprovalUserEdited ||
		service.approved == nil ||
		service.approved.Guidance.Instruction !=
			"Use the edited approved workflow." {
		t.Fatalf(
			"edited approval result = %#v, mode = %q, content = %#v",
			result,
			service.approvalMode,
			service.approved,
		)
	}

	service.approvalErr = local.ErrExperienceApprovalStale
	assertStrictToolError(t, callTool(
		t,
		session,
		"approve_experience",
		map[string]any{
			"proposal_id":   "exs_proposal",
			"action_token":  "approval-token",
			"approval_mode": "as_proposed",
		},
	), strictReadFailed)
}

func TestExperienceLifecyclePrepareApplyAndPartialDelivery(
	t *testing.T,
) {
	now := testExperienceLearningTime()
	ref := experience.ExperienceRef{
		ExperienceID: "exp_test",
		Version:      1,
	}
	transition := experience.LifecycleTransition{
		TransitionID: "ext_transition",
		Experience:   ref,
		FromState:    experience.LifecycleApproved,
		ToState:      experience.LifecycleActive,
		OccurredAt:   now,
	}
	service := &testExperienceLearningService{
		prepare: localapp.ExperienceLifecyclePreview{
			Action:           experience.LifecycleActionActivate,
			Experience:       ref,
			ProjectIdentity:  "git@example.test:team/project.git",
			CurrentLifecycle: experience.LifecycleApproved,
			ContentHash:      "sha256:" + strings.Repeat("a", 64),
			ActionToken:      "lifecycle-token",
			IssuedAt:         now,
			ExpiresAt:        now.Add(15 * time.Minute),
		},
	}
	service.apply.Transition.Transition = transition
	service.apply.Compilation.Generation.ProjectIdentity =
		"git@example.test:team/project.git"
	service.apply.Compilation.Generation.Generation = 4
	service.apply.Compilation.Generation.CompiledHash =
		"sha256:" + strings.Repeat("b", 64)
	service.apply.Compilation.Generation.ExperienceRefs =
		[]experience.ExperienceRef{ref}
	service.apply.Compilation.Generation.State = "active"
	service.apply.Compilation.Generation.CompiledAt = now
	service.apply.Compilation.Generation.ActivatedAt = now
	session := newExperienceLearningTestClient(t, service)

	prepared := callTool(
		t,
		session,
		"prepare_experience_lifecycle",
		map[string]any{
			"experience": ref,
			"action":     "activate",
		},
	)
	if prepared.IsError {
		t.Fatalf("prepare failed: %v", prepared.Content)
	}
	preview := asObject(
		t,
		asObject(t, prepared.StructuredContent)["readmodel"],
	)
	if preview["action_token"] != "lifecycle-token" {
		t.Fatalf("preview = %#v", preview)
	}

	applied := callTool(t, session, "apply_experience_lifecycle", map[string]any{
		"experience":   ref,
		"action":       "activate",
		"action_token": "lifecycle-token",
	})
	if applied.IsError {
		t.Fatalf("apply failed: %v", applied.Content)
	}
	output := asObject(t, asObject(t, applied.StructuredContent)["readmodel"])
	if output["transition_committed"] != true ||
		output["delivery_ready"] != true ||
		output["delivery_status"] != "ready" {
		t.Fatalf("apply output = %#v", output)
	}

	service.apply.Transition.Transition = experience.LifecycleTransition{
		TransitionID: "ext_pause_transition",
		Experience:   ref,
		FromState:    experience.LifecycleActive,
		ToState:      experience.LifecyclePaused,
		OccurredAt:   now,
	}
	service.apply.Compilation.Generation.Generation = 5
	service.apply.Compilation.Generation.CompiledHash =
		"sha256:" + strings.Repeat("c", 64)
	service.apply.Compilation.Generation.ExperienceRefs =
		[]experience.ExperienceRef{}
	paused := callTool(t, session, "apply_experience_lifecycle", map[string]any{
		"experience":   ref,
		"action":       "pause",
		"action_token": "lifecycle-token",
	})
	if paused.IsError {
		t.Fatalf("pause failed: %v", paused.Content)
	}
	output = asObject(t, asObject(t, paused.StructuredContent)["readmodel"])
	generation := asObject(t, output["generation"])
	experienceRefs, ok := generation["experience_refs"].([]any)
	if output["transition_committed"] != true ||
		output["delivery_ready"] != true ||
		output["delivery_status"] != "ready" ||
		!ok ||
		len(experienceRefs) != 0 {
		t.Fatalf("pause output = %#v", output)
	}

	service.apply.Transition.Transition = transition
	service.applyErr = &localapp.ExperienceLearningPartialDeliveryError{
		Transition: service.apply.Transition,
		Err:        errors.New("private compilation detail"),
	}
	partial := callTool(t, session, "apply_experience_lifecycle", map[string]any{
		"experience":   ref,
		"action":       "activate",
		"action_token": "lifecycle-token",
	})
	if partial.IsError {
		t.Fatalf("partial delivery was reported as failure: %v", partial.Content)
	}
	output = asObject(t, asObject(t, partial.StructuredContent)["readmodel"])
	if output["transition_committed"] != true ||
		output["delivery_ready"] != false ||
		output["delivery_status"] != "generation_compile_failed" {
		t.Fatalf("partial output = %#v", output)
	}
	if _, exists := output["generation"]; exists {
		t.Fatalf("partial output claims a generation: %#v", output)
	}
}

func TestExperienceLearningStrictSchemasAndErrorsFailClosed(t *testing.T) {
	service := &testExperienceLearningService{}
	session := newExperienceLearningTestClient(t, service)
	assertStrictToolError(t, callTool(t, session, "list_experience_proposals", map[string]any{
		"cwd":     "relative",
		"harness": "claude",
	}), strictInvalidInput)
	assertStrictToolError(t, callTool(t, session, "list_experience_proposals", map[string]any{
		"cwd":     "/tmp/project",
		"harness": "windsurf",
	}), strictInvalidInput)
	assertStrictToolError(t, callTool(t, session, "list_experience_proposals", map[string]any{
		"cwd":     "/tmp/project",
		"harness": "codex",
		"unknown": true,
	}), strictInvalidInput)
	if service.listCalls != 0 {
		t.Fatalf("invalid schema invoked service %d times", service.listCalls)
	}

	for _, test := range []struct {
		err  error
		want strictToolErrorCode
	}{
		{localapp.ErrExperienceLearningInvalidRequest, strictInvalidInput},
		{localapp.ErrExperienceLearningProjectMismatch, strictInvalidInput},
		{localapp.ErrExperienceApprovalExpired, strictReadFailed},
		{localapp.ErrExperienceLifecycleStale, strictReadFailed},
		{errors.New("private database detail"), strictReadFailed},
	} {
		service.listErr = test.err
		assertStrictToolError(t, callTool(t, session, "list_experience_proposals", map[string]any{
			"cwd":     "/tmp/project",
			"harness": "codex",
		}), test.want)
	}
}

func newExperienceLearningTestClient(
	t *testing.T,
	service ExperienceLearningService,
) *mcp.ClientSession {
	t.Helper()
	repository := &testRepository{}
	read := readmodel.New(
		repository,
		readmodel.WithIssueRepository(repository),
		readmodel.WithIssueCursorCodec(testIssueCursorCodec{}),
		readmodel.WithClock(testTime),
		readmodel.WithCostIssueRepository(repository),
	)
	server, err := New(
		read,
		WithCostIssueFixService(&testCostIssueFixService{}),
		WithExperienceLearningService(service),
	)
	if err != nil {
		t.Fatal(err)
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.mcp.Connect(
		context.Background(),
		serverTransport,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "belay-learning-test", Version: "1"},
		nil,
	)
	clientSession, err := client.Connect(
		context.Background(),
		clientTransport,
		nil,
	)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func isExperienceLearningTool(name string) bool {
	names := []string{
		"apply_experience_lifecycle",
		"approve_experience",
		"list_active_experiences",
		"list_experience_proposals",
		"prepare_experience_lifecycle",
		"resolve_experience_proposal",
	}
	sort.Strings(names)
	index := sort.SearchStrings(names, name)
	return index < len(names) && names[index] == name
}

func testExperienceProposal() experience.ExperienceProposal {
	confidence := 0.9
	return experience.ExperienceProposal{
		Type: experience.ExperienceProcedure,
		Scope: experience.Scope{
			Kind:            experience.ScopeProject,
			ProjectIdentity: "git@example.test:team/project.git",
		},
		Applicability: experience.Applicability{
			SemanticDescription: "Apply to implementation work.",
		},
		Guidance: experience.Guidance{
			Instruction:          "Run the project verification after the final edit.",
			Rationale:            "This catches incomplete changes before completion.",
			InterventionStrength: experience.InterventionRequireVerification,
		},
		Verifier: experience.Verifier{
			Kind: experience.VerifierObservationOnly,
			ObservationOnly: &experience.ObservationOnlySpec{
				Explanation: "Observe whether verification ran.",
			},
		},
		Confidence: &confidence,
	}
}

func testApprovedExperience(t *testing.T) experience.Experience {
	t.Helper()
	proposal := testExperienceProposal()
	now := testExperienceLearningTime()
	turn := int64(7)
	evidenceSet := experience.EvidenceSet{
		Availability: experience.EvidenceAvailable,
		Refs: []experience.EvidenceRef{{
			Kind:       experience.EvidenceTranscriptTurn,
			SessionKey: "ses_test",
			TurnIndex:  &turn,
			Excerpt:    "Run verification.",
		}},
	}
	evidenceSet.EvidenceSetID = evidenceSet.DeterministicID()
	value := experience.Experience{
		SchemaVersion:     experience.ExperienceSchemaVersion,
		OriginCandidateID: "exc_test_candidate",
		Version:           1,
		Type:              proposal.Type,
		Scope:             proposal.Scope,
		Applicability:     proposal.Applicability,
		Guidance:          proposal.Guidance,
		Verifier:          proposal.Verifier,
		Evidence:          evidenceSet,
		Provenance: experience.Provenance{
			ExtractorVersion:  "approval.v1",
			InputHash:         "sha256:" + strings.Repeat("c", 64),
			SourceCandidateID: "exc_test_candidate",
			GeneratedAt:       now,
		},
		Governance: experience.Governance{
			LifecycleState: experience.LifecycleApproved,
			Authority:      experience.AuthorityUserApproved,
		},
		CreatedAt: now,
	}
	value.ExperienceID = experience.DeriveExperienceID(
		value.Scope.ProjectIdentity,
		value.OriginCandidateID,
	)
	value.ContentHash = value.CanonicalContentHash()
	value.Governance.Approval = &experience.ApprovalProvenance{
		ApprovedBy:          "user",
		ApprovedAt:          now,
		Mode:                experience.ApprovalAsProposed,
		CandidateID:         value.OriginCandidateID,
		ProposedContentHash: value.ContentHash,
		ApprovedContentHash: value.ContentHash,
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("approved experience fixture is invalid: %v", err)
	}
	return value
}

func testExperienceLearningTime() time.Time {
	return time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
}
