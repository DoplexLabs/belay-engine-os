package localapp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/missionpack"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const (
	maxExperienceLearningItems           = 5
	maxExperienceLearningProjectionItems = 16
	// experienceLearningDeferWindow is the fixed period before a deferred
	// proposal becomes eligible for review again.
	experienceLearningDeferWindow = 7 * 24 * time.Hour
	experienceLearningReviewActor = "user"
)

var (
	ErrExperienceLearningInvalidRequest = errors.New(
		"experience learning request is invalid",
	)
	ErrExperienceLearningProjectMismatch = errors.New(
		"experience learning project does not match the observed workspace",
	)
	ErrExperienceLearningResultOverflow = errors.New(
		"experience learning result exceeds the bounded projection",
	)
	ErrExperienceLearningUnsupportedLifecycle = errors.New(
		"experience learning lifecycle action is unsupported",
	)
	ErrExperienceLearningReviewInvalid = errors.New(
		"experience learning review action is invalid",
	)
	ErrExperienceLearningReviewStale = errors.New(
		"experience learning review action is stale",
	)
	ErrExperienceLearningReviewExpired = errors.New(
		"experience learning review action has expired",
	)
	ErrExperienceLearningReviewConflict = errors.New(
		"experience learning review action conflicts with an existing action",
	)
)

type ExperienceLearningStore interface {
	ExperienceApprovalServiceStore
	ExperienceLifecycleServiceStore
	ExperienceCompilerStore
	ExperienceCandidateReviewListStore
	RecordExperienceReviewAction(
		context.Context,
		local.ExperienceReviewActionInput,
	) (local.ExperienceReviewActionResult, error)
	ResolveMissionPackProject(
		context.Context,
		missionpack.ProjectSelector,
	) (missionpack.ResolvedProject, error)
}

type ExperienceLearningListResult struct {
	ProjectIdentity string                      `json:"project_identity"`
	Items           []ExperienceApprovalPreview `json:"items"`
}

type ExperienceLearningActiveListResult struct {
	ProjectIdentity string                         `json:"project_identity"`
	Items           []ExperienceLearningActiveItem `json:"items"`
}

type ExperienceLearningActiveItem struct {
	Experience      experience.ExperienceRef        `json:"experience"`
	Instruction     string                          `json:"instruction"`
	Scope           ExperienceLearningScope         `json:"scope"`
	Applicability   ExperienceLearningApplicability `json:"applicability"`
	VerifierSummary string                          `json:"verifier_summary"`
	LifecycleState  experience.LifecycleState       `json:"lifecycle_state"`
}

type ExperienceLearningScope struct {
	Kind            experience.ScopeKind `json:"kind"`
	SessionKey      string               `json:"session_key,omitempty"`
	RepositoryPaths []string             `json:"repository_paths"`
	TaskFamilies    []string             `json:"task_families"`
	Harnesses       []experience.Harness `json:"harnesses"`
	Models          []string             `json:"models"`
}

type ExperienceLearningApplicability struct {
	Description             string                              `json:"description"`
	DeterministicConditions []experience.DeterministicCondition `json:"deterministic_conditions"`
	Exclusions              []string                            `json:"exclusions"`
	ExpiresAt               *time.Time                          `json:"expires_at,omitempty"`
}

type ExperienceLearningApprovalWithContentRequest struct {
	ProposalID      string                        `json:"proposal_id"`
	ActionToken     string                        `json:"action_token"`
	Mode            experience.ApprovalMode       `json:"approval_mode"`
	ApprovedContent experience.ExperienceProposal `json:"approved_content"`
}

type ExperienceLearningLifecycleResult struct {
	Transition  local.ExperienceLifecycleActionResult   `json:"transition"`
	Compilation local.CompileExperienceGenerationResult `json:"compilation"`
}

type ExperienceLearningReviewDisposition string

const (
	ExperienceLearningReviewDefer  ExperienceLearningReviewDisposition = "defer"
	ExperienceLearningReviewReject ExperienceLearningReviewDisposition = "reject"
)

type ExperienceLearningReviewResult struct {
	ActionID       string                              `json:"action_id"`
	ProposalID     string                              `json:"proposal_id"`
	Disposition    ExperienceLearningReviewDisposition `json:"disposition"`
	OccurredAt     time.Time                           `json:"occurred_at"`
	AvailableAfter *time.Time                          `json:"available_after,omitempty"`
	Replayed       bool                                `json:"replayed"`
}

// ExperienceLearningPartialDeliveryError means the lifecycle transition was
// committed, but its active generation was not compiled. Callers must not
// report the lifecycle write as failed or retry the token-bound transition.
type ExperienceLearningPartialDeliveryError struct {
	Transition local.ExperienceLifecycleActionResult
	Err        error
}

func (err *ExperienceLearningPartialDeliveryError) Error() string {
	return fmt.Sprintf(
		"experience lifecycle transition succeeded but generation compilation failed: %v",
		err.Err,
	)
}

func (err *ExperienceLearningPartialDeliveryError) Unwrap() error {
	return err.Err
}

type experienceLearningProjectResolver interface {
	ResolveMissionPackProject(
		context.Context,
		missionpack.ProjectSelector,
	) (missionpack.ResolvedProject, error)
}

type experienceLearningApproval interface {
	ApproveAsProposed(
		context.Context,
		string,
		string,
		string,
	) (local.ExperienceApprovalResult, error)
	ApproveWithContent(
		context.Context,
		ExperienceApprovalWithContentRequest,
	) (local.ExperienceApprovalResult, error)
}

type experienceLearningLifecycle interface {
	Prepare(
		context.Context,
		experience.ExperienceRef,
		experience.LifecycleAction,
	) (ExperienceLifecyclePreview, error)
	Activate(
		context.Context,
		experience.ExperienceRef,
		string,
		string,
	) (local.ExperienceLifecycleActionResult, error)
	Pause(
		context.Context,
		experience.ExperienceRef,
		string,
		string,
	) (local.ExperienceLifecycleActionResult, error)
}

type experienceLearningCompiler interface {
	Compile(
		context.Context,
		string,
	) (local.CompileExperienceGenerationResult, error)
}

type experienceLearningReviewProvider interface {
	List(
		context.Context,
		string,
		experience.Harness,
		int,
		bool,
	) ([]ExperienceApprovalPreview, error)
}

type experienceLearningExperienceReader interface {
	GetExperience(
		context.Context,
		experience.ExperienceRef,
	) (local.StoredExperience, error)
	QueryActiveExperiences(
		context.Context,
		string,
		int,
	) ([]local.StoredExperience, error)
}

type experienceLearningReviewActions interface {
	DecodeExperienceApprovalToken(
		string,
	) (experience.ApprovalTokenClaims, error)
	RecordExperienceReviewAction(
		context.Context,
		local.ExperienceReviewActionInput,
	) (local.ExperienceReviewActionResult, error)
}

type ExperienceLearningService struct {
	resolver   experienceLearningProjectResolver
	approval   experienceLearningApproval
	lifecycle  experienceLearningLifecycle
	compiler   experienceLearningCompiler
	reviews    experienceLearningReviewProvider
	experience experienceLearningExperienceReader
	actions    experienceLearningReviewActions
	now        func() time.Time
}

type ExperienceLearningServiceOption func(*ExperienceLearningService)

func WithExperienceLearningClock(
	clock func() time.Time,
) ExperienceLearningServiceOption {
	return func(service *ExperienceLearningService) {
		if clock != nil {
			service.now = clock
		}
	}
}

func NewExperienceLearningService(
	store ExperienceLearningStore,
	options ...ExperienceLearningServiceOption,
) (*ExperienceLearningService, error) {
	if store == nil {
		return nil, errors.New(
			"experience learning service requires a store",
		)
	}
	approval, err := NewExperienceApprovalService(store)
	if err != nil {
		return nil, err
	}
	lifecycle, err := NewExperienceLifecycleService(store)
	if err != nil {
		return nil, err
	}
	compiler, err := NewExperienceCompilerService(store)
	if err != nil {
		return nil, err
	}
	service, err := newExperienceLearningService(
		store,
		approval,
		lifecycle,
		compiler,
		experienceLearningStoreReviews{
			store:    store,
			approval: approval,
		},
		store,
		store,
	)
	if err != nil {
		return nil, err
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func newExperienceLearningService(
	resolver experienceLearningProjectResolver,
	approval experienceLearningApproval,
	lifecycle experienceLearningLifecycle,
	compiler experienceLearningCompiler,
	reviews experienceLearningReviewProvider,
	experienceReader experienceLearningExperienceReader,
	actions experienceLearningReviewActions,
) (*ExperienceLearningService, error) {
	if resolver == nil ||
		approval == nil ||
		lifecycle == nil ||
		compiler == nil ||
		reviews == nil ||
		experienceReader == nil ||
		actions == nil {
		return nil, errors.New(
			"experience learning service dependencies are required",
		)
	}
	return &ExperienceLearningService{
		resolver:   resolver,
		approval:   approval,
		lifecycle:  lifecycle,
		compiler:   compiler,
		reviews:    reviews,
		experience: experienceReader,
		actions:    actions,
		now:        time.Now,
	}, nil
}

func (service *ExperienceLearningService) List(
	ctx context.Context,
	cwd string,
	harness experience.Harness,
	limit int,
	includeDeferred bool,
) (ExperienceLearningListResult, error) {
	if ctx == nil ||
		!harness.Valid() ||
		limit < 1 ||
		limit > maxExperienceLearningItems {
		return ExperienceLearningListResult{},
			ErrExperienceLearningInvalidRequest
	}
	project, err := service.resolveProject(ctx, cwd)
	if err != nil {
		return ExperienceLearningListResult{}, err
	}
	items, err := service.reviews.List(
		ctx,
		project.Identity,
		harness,
		limit,
		includeDeferred,
	)
	if err != nil {
		return ExperienceLearningListResult{}, err
	}
	if len(items) > limit || len(items) > maxExperienceLearningItems {
		return ExperienceLearningListResult{},
			ErrExperienceLearningProjectMismatch
	}
	for _, item := range items {
		if item.Review.ProjectIdentity != project.Identity ||
			item.Review.SemanticProvenance.Harness != harness ||
			item.Review.Authority != experience.AuthorityNone {
			return ExperienceLearningListResult{},
				ErrExperienceLearningProjectMismatch
		}
	}
	return ExperienceLearningListResult{
		ProjectIdentity: project.Identity,
		Items:           items,
	}, nil
}

func (service *ExperienceLearningService) ListActive(
	ctx context.Context,
	cwd string,
	limit int,
) (ExperienceLearningActiveListResult, error) {
	if ctx == nil || limit < 1 || limit > maxExperienceLearningItems {
		return ExperienceLearningActiveListResult{},
			ErrExperienceLearningInvalidRequest
	}
	project, err := service.resolveActiveProject(ctx, cwd)
	if err != nil {
		return ExperienceLearningActiveListResult{}, err
	}
	stored, err := service.experience.QueryActiveExperiences(
		ctx,
		project.Identity,
		limit,
	)
	if err != nil {
		return ExperienceLearningActiveListResult{}, err
	}
	if len(stored) > limit || len(stored) > maxExperienceLearningItems {
		return ExperienceLearningActiveListResult{},
			ErrExperienceLearningResultOverflow
	}

	items := make(
		[]ExperienceLearningActiveItem,
		0,
		len(stored),
	)
	for _, value := range stored {
		item, err := projectActiveExperience(project.Identity, value)
		if err != nil {
			return ExperienceLearningActiveListResult{}, err
		}
		items = append(items, item)
	}
	sort.Slice(items, func(first, second int) bool {
		left := items[first].Experience
		right := items[second].Experience
		if left.ExperienceID != right.ExperienceID {
			return left.ExperienceID < right.ExperienceID
		}
		return left.Version < right.Version
	})
	return ExperienceLearningActiveListResult{
		ProjectIdentity: project.Identity,
		Items:           items,
	}, nil
}

func (service *ExperienceLearningService) ApproveAsProposed(
	ctx context.Context,
	proposalID string,
	actionToken string,
) (local.ExperienceApprovalResult, error) {
	return service.approval.ApproveAsProposed(
		ctx,
		proposalID,
		actionToken,
		"user",
	)
}

func (service *ExperienceLearningService) ApproveWithContent(
	ctx context.Context,
	request ExperienceLearningApprovalWithContentRequest,
) (local.ExperienceApprovalResult, error) {
	return service.approval.ApproveWithContent(
		ctx,
		ExperienceApprovalWithContentRequest{
			ProposalID:      request.ProposalID,
			ActionToken:     request.ActionToken,
			Actor:           "user",
			Mode:            request.Mode,
			ApprovedContent: request.ApprovedContent,
		},
	)
}

func (service *ExperienceLearningService) Defer(
	ctx context.Context,
	proposalID string,
	actionToken string,
) (ExperienceLearningReviewResult, error) {
	return service.recordReviewAction(
		ctx,
		proposalID,
		actionToken,
		ExperienceLearningReviewDefer,
	)
}

func (service *ExperienceLearningService) Reject(
	ctx context.Context,
	proposalID string,
	actionToken string,
) (ExperienceLearningReviewResult, error) {
	return service.recordReviewAction(
		ctx,
		proposalID,
		actionToken,
		ExperienceLearningReviewReject,
	)
}

func (service *ExperienceLearningService) recordReviewAction(
	ctx context.Context,
	proposalID string,
	actionToken string,
	disposition ExperienceLearningReviewDisposition,
) (ExperienceLearningReviewResult, error) {
	proposalID = strings.TrimSpace(proposalID)
	actionToken = strings.TrimSpace(actionToken)
	if ctx == nil || proposalID == "" || actionToken == "" {
		return ExperienceLearningReviewResult{},
			ErrExperienceLearningReviewInvalid
	}
	claims, err := service.actions.DecodeExperienceApprovalToken(actionToken)
	if err != nil {
		return ExperienceLearningReviewResult{},
			mapExperienceLearningReviewError(err)
	}
	if claims.ProposalID != proposalID {
		return ExperienceLearningReviewResult{},
			ErrExperienceLearningReviewInvalid
	}
	occurredAt := service.now().UTC().Round(0)
	input := local.ExperienceReviewActionInput{
		Claims:      claims,
		Actor:       experienceLearningReviewActor,
		Disposition: local.ExperienceReviewDisposition(disposition),
		OccurredAt:  occurredAt,
	}
	if disposition == ExperienceLearningReviewDefer {
		availableAfter := occurredAt.Add(experienceLearningDeferWindow)
		input.AvailableAfter = &availableAfter
	}
	result, err := service.actions.RecordExperienceReviewAction(ctx, input)
	if err != nil {
		return ExperienceLearningReviewResult{},
			mapExperienceLearningReviewError(err)
	}
	return ExperienceLearningReviewResult{
		ActionID:       result.Action.ActionID,
		ProposalID:     result.Action.ProposalID,
		Disposition:    ExperienceLearningReviewDisposition(result.Action.Disposition),
		OccurredAt:     result.Action.OccurredAt,
		AvailableAfter: result.Action.AvailableAfter,
		Replayed:       result.Replayed,
	}, nil
}

func mapExperienceLearningReviewError(err error) error {
	switch {
	case errors.Is(err, local.ErrExperienceApprovalTokenInvalid),
		errors.Is(err, local.ErrExperienceReviewActionInvalid):
		return ErrExperienceLearningReviewInvalid
	case errors.Is(err, local.ErrExperienceApprovalTokenExpired):
		return ErrExperienceLearningReviewExpired
	case errors.Is(err, local.ErrExperienceReviewActionStale):
		return ErrExperienceLearningReviewStale
	case errors.Is(err, local.ErrExperienceReviewActionConflict):
		return ErrExperienceLearningReviewConflict
	default:
		return err
	}
}

func (service *ExperienceLearningService) PrepareLifecycle(
	ctx context.Context,
	ref experience.ExperienceRef,
	action experience.LifecycleAction,
) (ExperienceLifecyclePreview, error) {
	if !supportedExperienceLearningLifecycle(action) {
		return ExperienceLifecyclePreview{},
			ErrExperienceLearningUnsupportedLifecycle
	}
	return service.lifecycle.Prepare(ctx, ref, action)
}

func (service *ExperienceLearningService) ApplyLifecycle(
	ctx context.Context,
	ref experience.ExperienceRef,
	action experience.LifecycleAction,
	actionToken string,
) (ExperienceLearningLifecycleResult, error) {
	if !supportedExperienceLearningLifecycle(action) {
		return ExperienceLearningLifecycleResult{},
			ErrExperienceLearningUnsupportedLifecycle
	}
	stored, err := service.experience.GetExperience(ctx, ref)
	if err != nil {
		return ExperienceLearningLifecycleResult{}, err
	}
	var transition local.ExperienceLifecycleActionResult
	switch action {
	case experience.LifecycleActionActivate:
		transition, err = service.lifecycle.Activate(
			ctx,
			ref,
			actionToken,
			"user",
		)
	case experience.LifecycleActionPause:
		transition, err = service.lifecycle.Pause(
			ctx,
			ref,
			actionToken,
			"user",
		)
	}
	if err != nil {
		return ExperienceLearningLifecycleResult{}, err
	}
	result := ExperienceLearningLifecycleResult{
		Transition: transition,
	}
	result.Compilation, err = service.compiler.Compile(
		ctx,
		stored.Experience.Scope.ProjectIdentity,
	)
	if err != nil {
		return result, &ExperienceLearningPartialDeliveryError{
			Transition: transition,
			Err:        err,
		}
	}
	return result, nil
}

func (service *ExperienceLearningService) resolveProject(
	ctx context.Context,
	cwd string,
) (missionpack.ResolvedProject, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" ||
		len(cwd) > maxMissionPackCWDBytes ||
		!filepath.IsAbs(cwd) {
		return missionpack.ResolvedProject{},
			ErrExperienceLearningInvalidRequest
	}
	workspace := observeMissionPackWorkspace(ctx, filepath.Clean(cwd))
	resolved, err := service.resolver.ResolveMissionPackProject(
		ctx,
		missionpack.ProjectSelector{
			RemoteIdentity: workspace.remote,
			ProjectRoot:    workspace.root,
			ProjectPath:    workspace.path,
		},
	)
	if err != nil {
		return missionpack.ResolvedProject{}, err
	}
	if strings.TrimSpace(resolved.Identity) == "" ||
		!filepath.IsAbs(resolved.Path) {
		return missionpack.ResolvedProject{},
			ErrExperienceLearningProjectMismatch
	}
	if workspace.remote != "" &&
		(resolved.IdentityKind != "remote" ||
			resolved.Identity != workspace.remote) {
		return missionpack.ResolvedProject{},
			ErrExperienceLearningProjectMismatch
	}
	return resolved, nil
}

func (service *ExperienceLearningService) resolveActiveProject(
	ctx context.Context,
	cwd string,
) (missionpack.ResolvedProject, error) {
	project, err := service.resolveProject(ctx, cwd)
	if err != nil {
		return missionpack.ResolvedProject{}, err
	}
	workspace := observeMissionPackWorkspace(ctx, filepath.Clean(cwd))
	if workspace.remote != "" {
		if project.IdentityKind != "remote" ||
			project.Identity != workspace.remote {
			return missionpack.ResolvedProject{},
				ErrExperienceLearningProjectMismatch
		}
		return project, nil
	}
	projectPath := filepath.Clean(project.Path)
	if resolved, resolveErr := filepath.EvalSymlinks(projectPath); resolveErr == nil {
		projectPath = filepath.Clean(resolved)
	}
	if project.IdentityKind != "path" ||
		project.Identity != projectPath ||
		!experienceLearningPathContains(projectPath, workspace.path) {
		return missionpack.ResolvedProject{},
			ErrExperienceLearningProjectMismatch
	}
	return project, nil
}

func experienceLearningPathContains(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if !filepath.IsAbs(parent) || !filepath.IsAbs(child) {
		return false
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil &&
		relative != ".." &&
		!strings.HasPrefix(
			relative,
			".."+string(filepath.Separator),
		)
}

func projectActiveExperience(
	projectIdentity string,
	stored local.StoredExperience,
) (ExperienceLearningActiveItem, error) {
	value := stored.Experience
	if value.Validate() != nil ||
		value.Scope.ProjectIdentity != projectIdentity ||
		stored.CurrentLifecycle != experience.LifecycleActive ||
		value.Governance.Authority != experience.AuthorityUserApproved ||
		len(value.Scope.RepositoryPaths) >
			maxExperienceLearningProjectionItems ||
		len(value.Scope.TaskFamilies) >
			maxExperienceLearningProjectionItems ||
		len(value.Scope.Harnesses) >
			maxExperienceLearningProjectionItems ||
		len(value.Scope.Models) >
			maxExperienceLearningProjectionItems ||
		len(value.Applicability.DeterministicConditions) >
			maxExperienceLearningProjectionItems ||
		len(value.Applicability.Exclusions) >
			maxExperienceLearningProjectionItems {
		return ExperienceLearningActiveItem{},
			ErrExperienceLearningProjectMismatch
	}
	for _, condition := range value.Applicability.DeterministicConditions {
		if len(condition.Values) > maxExperienceLearningProjectionItems {
			return ExperienceLearningActiveItem{},
				ErrExperienceLearningResultOverflow
		}
	}
	verifier, err := missionPackVerifierSummary(value.Verifier)
	if err != nil {
		return ExperienceLearningActiveItem{},
			ErrExperienceLearningProjectMismatch
	}
	return ExperienceLearningActiveItem{
		Experience: experience.ExperienceRef{
			ExperienceID: value.ExperienceID,
			Version:      value.Version,
		},
		Instruction: value.Guidance.Instruction,
		Scope: ExperienceLearningScope{
			Kind:            value.Scope.Kind,
			SessionKey:      value.Scope.SessionKey,
			RepositoryPaths: append([]string(nil), value.Scope.RepositoryPaths...),
			TaskFamilies:    append([]string(nil), value.Scope.TaskFamilies...),
			Harnesses: append(
				[]experience.Harness(nil),
				value.Scope.Harnesses...,
			),
			Models: append([]string(nil), value.Scope.Models...),
		},
		Applicability: ExperienceLearningApplicability{
			Description: boundedMissionPackSummaryText(
				value.Applicability.SemanticDescription,
				300,
			),
			DeterministicConditions: cloneExperienceLearningConditions(
				value.Applicability.DeterministicConditions,
			),
			Exclusions: cloneExperienceLearningExclusions(
				value.Applicability.Exclusions,
			),
			ExpiresAt: cloneExperienceLearningTime(
				value.Applicability.ExpiresAt,
			),
		},
		VerifierSummary: verifier.Summary,
		LifecycleState:  experience.LifecycleActive,
	}, nil
}

func cloneExperienceLearningConditions(
	values []experience.DeterministicCondition,
) []experience.DeterministicCondition {
	result := make(
		[]experience.DeterministicCondition,
		len(values),
	)
	for index, value := range values {
		result[index] = experience.DeterministicCondition{
			Kind:   value.Kind,
			Values: append([]string(nil), value.Values...),
		}
	}
	return result
}

func cloneExperienceLearningExclusions(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = boundedMissionPackSummaryText(value, 300)
	}
	return result
}

func cloneExperienceLearningTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func supportedExperienceLearningLifecycle(
	action experience.LifecycleAction,
) bool {
	return action == experience.LifecycleActionActivate ||
		action == experience.LifecycleActionPause
}

type experienceLearningStoreReviews struct {
	store    ExperienceCandidateReviewListStore
	approval *ExperienceApprovalService
}

func (provider experienceLearningStoreReviews) List(
	ctx context.Context,
	projectIdentity string,
	harness experience.Harness,
	limit int,
	includeDeferred bool,
) ([]ExperienceApprovalPreview, error) {
	reviews, err := ListExperienceCandidateReviews(
		ctx,
		provider.store,
		projectIdentity,
		harness,
		limit,
		includeDeferred,
	)
	if err != nil {
		return nil, err
	}
	items := make([]ExperienceApprovalPreview, 0, len(reviews))
	for _, review := range reviews {
		preview, err := provider.approval.Prepare(
			ctx,
			review.ProposalID,
		)
		if err != nil {
			return nil, err
		}
		if preview.Review.ProposalID != review.ProposalID ||
			preview.Review.ProjectIdentity != projectIdentity ||
			preview.Review.SemanticProvenance.Harness != harness ||
			preview.Review.Authority != experience.AuthorityNone {
			return nil, ErrExperienceLearningProjectMismatch
		}
		items = append(items, preview)
	}
	return items, nil
}

var _ ExperienceLearningStore = (*local.Store)(nil)
