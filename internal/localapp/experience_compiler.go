package localapp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const (
	maxExperienceSelectionPaths     = 128
	maxExperienceSelectionTaskBytes = 1120
	maxExperienceSelectionTaskRunes = 280
	maxExperienceGenerationRefs     = 500
	maxSelectedExperiences          = 3
	maxSelectedExperienceTokens     = 600
)

var (
	ErrExperienceCompilerInvalidRequest = errors.New(
		"experience compiler request is invalid",
	)
	ErrExperienceSelectionCorrupt = errors.New(
		"experience selection source is corrupt",
	)
)

type ExperienceCompilerStore interface {
	CompileActiveExperienceGeneration(
		context.Context,
		string,
		time.Time,
	) (local.CompileExperienceGenerationResult, error)
	GetActiveExperienceGeneration(
		context.Context,
		string,
	) (local.ExperienceGeneration, error)
	GetExperience(
		context.Context,
		experience.ExperienceRef,
	) (local.StoredExperience, error)
}

type ExperienceCompilerService struct {
	store ExperienceCompilerStore
	now   func() time.Time
}

type ExperienceCompilerServiceOption func(*ExperienceCompilerService)

func WithExperienceCompilerClock(
	clock func() time.Time,
) ExperienceCompilerServiceOption {
	return func(service *ExperienceCompilerService) {
		if clock != nil {
			service.now = clock
		}
	}
}

type ExperienceSelectionRequest struct {
	ProjectIdentity string             `json:"project_identity"`
	SessionKey      string             `json:"session_key,omitempty"`
	Harness         experience.Harness `json:"harness,omitempty"`
	TaskFamily      string             `json:"task_family,omitempty"`
	TaskHint        string             `json:"task_hint,omitempty"`
	RepositoryPaths []string           `json:"repository_paths"`
	Model           string             `json:"model,omitempty"`
}

type SelectedExperience struct {
	Ref             experience.ExperienceRef `json:"ref"`
	Experience      experience.Experience    `json:"experience"`
	Score           int                      `json:"score"`
	EstimatedTokens int                      `json:"estimated_tokens"`
}

type ExperienceSelectionResult struct {
	Generation      local.ExperienceGeneration `json:"generation"`
	Selected        []SelectedExperience       `json:"selected"`
	ExperienceRefs  []experience.ExperienceRef `json:"experience_refs"`
	EstimatedTokens int                        `json:"estimated_tokens"`
}

func NewExperienceCompilerService(
	store ExperienceCompilerStore,
	options ...ExperienceCompilerServiceOption,
) (*ExperienceCompilerService, error) {
	if store == nil {
		return nil, errors.New("experience compiler service requires a store")
	}
	service := &ExperienceCompilerService{
		store: store,
		now:   time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// Compile explicitly snapshots the active experience set. Selection never
// calls this method or otherwise mutates generation state.
func (service *ExperienceCompilerService) Compile(
	ctx context.Context,
	projectIdentity string,
) (local.CompileExperienceGenerationResult, error) {
	if ctx == nil {
		return local.CompileExperienceGenerationResult{},
			ErrExperienceCompilerInvalidRequest
	}
	projectIdentity, err := normalizeExperienceSelectionProject(
		projectIdentity,
	)
	if err != nil {
		return local.CompileExperienceGenerationResult{}, err
	}
	compiledAt := service.now().UTC().Round(0)
	if compiledAt.IsZero() {
		return local.CompileExperienceGenerationResult{},
			ErrExperienceCompilerInvalidRequest
	}
	result, err := service.store.CompileActiveExperienceGeneration(
		ctx,
		projectIdentity,
		compiledAt,
	)
	if err != nil {
		return local.CompileExperienceGenerationResult{}, err
	}
	if err := validateSelectionGeneration(
		result.Generation,
		projectIdentity,
	); err != nil {
		return local.CompileExperienceGenerationResult{}, err
	}
	return result, nil
}

// Select is a read-only projection over the exact active generation refs.
func (service *ExperienceCompilerService) Select(
	ctx context.Context,
	request ExperienceSelectionRequest,
) (ExperienceSelectionResult, error) {
	if ctx == nil {
		return ExperienceSelectionResult{},
			ErrExperienceCompilerInvalidRequest
	}
	request, err := normalizeExperienceSelectionRequest(request)
	if err != nil {
		return ExperienceSelectionResult{}, err
	}
	generation, err := service.store.GetActiveExperienceGeneration(
		ctx,
		request.ProjectIdentity,
	)
	if err != nil {
		return ExperienceSelectionResult{}, err
	}
	if err := validateSelectionGeneration(
		generation,
		request.ProjectIdentity,
	); err != nil {
		return ExperienceSelectionResult{}, err
	}

	refs := append(
		[]experience.ExperienceRef(nil),
		generation.ExperienceRefs...,
	)
	seen := make(map[experience.ExperienceRef]struct{}, len(refs))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return ExperienceSelectionResult{}, fmt.Errorf(
				"%w: invalid generation reference",
				ErrExperienceSelectionCorrupt,
			)
		}
		if _, duplicate := seen[ref]; duplicate {
			return ExperienceSelectionResult{}, fmt.Errorf(
				"%w: duplicate generation reference",
				ErrExperienceSelectionCorrupt,
			)
		}
		seen[ref] = struct{}{}
	}
	sortExperienceRefs(refs)
	generation.ExperienceRefs = append(
		make([]experience.ExperienceRef, 0, len(refs)),
		refs...,
	)

	now := service.now().UTC().Round(0)
	if now.IsZero() {
		return ExperienceSelectionResult{},
			ErrExperienceCompilerInvalidRequest
	}
	eligible := make([]SelectedExperience, 0, len(refs))
	for _, ref := range refs {
		stored, err := service.store.GetExperience(ctx, ref)
		if err != nil {
			return ExperienceSelectionResult{}, fmt.Errorf(
				"%w: load generation experience %s version %d: %v",
				ErrExperienceSelectionCorrupt,
				ref.ExperienceID,
				ref.Version,
				err,
			)
		}
		value := stored.Experience
		if err := value.Validate(); err != nil {
			return ExperienceSelectionResult{}, fmt.Errorf(
				"%w: invalid generation experience",
				ErrExperienceSelectionCorrupt,
			)
		}
		if value.ExperienceID != ref.ExperienceID ||
			value.Version != ref.Version ||
			value.Scope.ProjectIdentity != generation.ProjectIdentity ||
			value.Scope.ProjectIdentity != request.ProjectIdentity {
			return ExperienceSelectionResult{}, fmt.Errorf(
				"%w: generation experience identity mismatch",
				ErrExperienceSelectionCorrupt,
			)
		}
		if !stored.CurrentLifecycle.Valid() {
			return ExperienceSelectionResult{}, fmt.Errorf(
				"%w: invalid rebuilt lifecycle",
				ErrExperienceSelectionCorrupt,
			)
		}
		if !selectionTimesEqual(
			stored.ExpiresAt,
			value.Applicability.ExpiresAt,
		) {
			return ExperienceSelectionResult{}, fmt.Errorf(
				"%w: experience expiration mismatch",
				ErrExperienceSelectionCorrupt,
			)
		}
		if stored.CurrentLifecycle != experience.LifecycleActive {
			continue
		}
		if stored.ExpiresAt != nil && !stored.ExpiresAt.After(now) {
			continue
		}
		score, eligibleForRequest := scoreExperienceSelection(
			value,
			request,
		)
		if !eligibleForRequest {
			continue
		}
		estimatedTokens, err := estimateExperienceSelectionTokens(value)
		if err != nil {
			return ExperienceSelectionResult{}, fmt.Errorf(
				"%w: estimate experience tokens",
				ErrExperienceSelectionCorrupt,
			)
		}
		eligible = append(eligible, SelectedExperience{
			Ref:             ref,
			Experience:      value,
			Score:           score,
			EstimatedTokens: estimatedTokens,
		})
	}
	sort.Slice(eligible, func(first, second int) bool {
		if eligible[first].Score != eligible[second].Score {
			return eligible[first].Score > eligible[second].Score
		}
		if eligible[first].Ref.ExperienceID !=
			eligible[second].Ref.ExperienceID {
			return eligible[first].Ref.ExperienceID <
				eligible[second].Ref.ExperienceID
		}
		return eligible[first].Ref.Version <
			eligible[second].Ref.Version
	})

	result := ExperienceSelectionResult{
		Generation:     generation,
		Selected:       make([]SelectedExperience, 0, maxSelectedExperiences),
		ExperienceRefs: make([]experience.ExperienceRef, 0, maxSelectedExperiences),
	}
	for _, item := range eligible {
		if len(result.Selected) == maxSelectedExperiences {
			break
		}
		if item.EstimatedTokens > maxSelectedExperienceTokens ||
			result.EstimatedTokens+item.EstimatedTokens >
				maxSelectedExperienceTokens {
			continue
		}
		result.Selected = append(result.Selected, item)
		result.ExperienceRefs = append(result.ExperienceRefs, item.Ref)
		result.EstimatedTokens += item.EstimatedTokens
	}
	return result, nil
}

func normalizeExperienceSelectionRequest(
	request ExperienceSelectionRequest,
) (ExperienceSelectionRequest, error) {
	projectIdentity, err := normalizeExperienceSelectionProject(
		request.ProjectIdentity,
	)
	if err != nil {
		return ExperienceSelectionRequest{}, err
	}
	request.ProjectIdentity = projectIdentity
	request.SessionKey = strings.TrimSpace(request.SessionKey)
	request.TaskFamily = strings.TrimSpace(request.TaskFamily)
	request.Model = strings.TrimSpace(request.Model)
	request.Harness = experience.Harness(strings.ToLower(
		strings.TrimSpace(string(request.Harness)),
	))
	rawTaskHint := strings.TrimSpace(request.TaskHint)
	if !validExperienceSelectionTaskHint(rawTaskHint) {
		return ExperienceSelectionRequest{},
			ErrExperienceCompilerInvalidRequest
	}
	request.TaskHint = strings.Join(strings.Fields(rawTaskHint), " ")

	scope := experience.Scope{
		Kind:            experience.ScopeProject,
		ProjectIdentity: request.ProjectIdentity,
	}
	if request.SessionKey != "" {
		scope.Kind = experience.ScopeSession
		scope.SessionKey = request.SessionKey
	}
	if request.TaskFamily != "" {
		scope.TaskFamilies = []string{request.TaskFamily}
	}
	if request.Model != "" {
		scope.Models = []string{request.Model}
	}
	if err := scope.Validate(); err != nil {
		return ExperienceSelectionRequest{},
			fmt.Errorf("%w: %v", ErrExperienceCompilerInvalidRequest, err)
	}
	if request.Harness != "" && !request.Harness.Valid() {
		return ExperienceSelectionRequest{},
			ErrExperienceCompilerInvalidRequest
	}
	if !validExperienceSelectionTaskHint(request.TaskHint) {
		return ExperienceSelectionRequest{},
			ErrExperienceCompilerInvalidRequest
	}
	if len(request.RepositoryPaths) > maxExperienceSelectionPaths {
		return ExperienceSelectionRequest{},
			ErrExperienceCompilerInvalidRequest
	}
	paths := make([]string, 0, len(request.RepositoryPaths))
	for _, value := range request.RepositoryPaths {
		value = strings.TrimSpace(value)
		if err := (experience.FileVerifierSpec{Path: value}).Validate(); err != nil {
			return ExperienceSelectionRequest{},
				fmt.Errorf(
					"%w: repository path: %v",
					ErrExperienceCompilerInvalidRequest,
					err,
				)
		}
		paths = append(paths, value)
	}
	sort.Strings(paths)
	request.RepositoryPaths = compactSelectionStrings(paths)
	if request.RepositoryPaths == nil {
		request.RepositoryPaths = make([]string, 0)
	}
	return request, nil
}

func normalizeExperienceSelectionProject(value string) (string, error) {
	value = strings.TrimSpace(value)
	if err := (experience.Scope{
		Kind:            experience.ScopeProject,
		ProjectIdentity: value,
	}).Validate(); err != nil {
		return "", fmt.Errorf(
			"%w: %v",
			ErrExperienceCompilerInvalidRequest,
			err,
		)
	}
	return value, nil
}

func validateSelectionGeneration(
	value local.ExperienceGeneration,
	projectIdentity string,
) error {
	if value.ProjectIdentity != projectIdentity ||
		value.Generation < 1 ||
		value.State != local.GenerationActive ||
		len(value.ExperienceRefs) > maxExperienceGenerationRefs ||
		value.CompiledAt.IsZero() ||
		value.ActivatedAt.IsZero() ||
		!validSelectionSHA256(value.CompiledHash) {
		return fmt.Errorf(
			"%w: invalid active generation",
			ErrExperienceSelectionCorrupt,
		)
	}
	if value.PreviousGeneration != nil && *value.PreviousGeneration < 1 {
		return fmt.Errorf(
			"%w: invalid previous generation",
			ErrExperienceSelectionCorrupt,
		)
	}
	return nil
}

func validExperienceSelectionTaskHint(value string) bool {
	if !utf8.ValidString(value) ||
		len(value) > maxExperienceSelectionTaskBytes ||
		utf8.RuneCountInString(value) >
			maxExperienceSelectionTaskRunes {
		return false
	}
	for _, character := range value {
		if (character < 0x20 &&
			character != '\n' &&
			character != '\r' &&
			character != '\t') ||
			character == 0x7f {
			return false
		}
	}
	return true
}

func validSelectionSHA256(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) ||
		len(value) != len(prefix)+64 ||
		value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil
}

func scoreExperienceSelection(
	value experience.Experience,
	request ExperienceSelectionRequest,
) (int, bool) {
	if value.Scope.ProjectIdentity != request.ProjectIdentity {
		return 0, false
	}
	score := 10
	if value.Scope.Kind == experience.ScopeSession {
		if request.SessionKey == "" ||
			value.Scope.SessionKey != request.SessionKey {
			return 0, false
		}
		score += 50
	}
	if len(value.Scope.Harnesses) > 0 {
		if request.Harness == "" ||
			!selectionContains(value.Scope.Harnesses, request.Harness) {
			return 0, false
		}
		score += 20
	}
	lexicalOverlap := selectionLexicalOverlap(
		request.TaskHint,
		value.Applicability.SemanticDescription+" "+
			value.Guidance.Instruction+" "+
			value.Guidance.Rationale+" "+
			strings.Join(value.Scope.TaskFamilies, " "),
	)
	unresolvedSemanticScope := false
	if len(value.Scope.TaskFamilies) > 0 {
		if request.TaskFamily != "" &&
			selectionContains(
				value.Scope.TaskFamilies,
				request.TaskFamily,
			) {
			score += 30
		} else if lexicalOverlap == 0 {
			return 0, false
		} else {
			unresolvedSemanticScope = true
		}
	}
	if len(value.Scope.RepositoryPaths) > 0 {
		if len(request.RepositoryPaths) == 0 {
			unresolvedSemanticScope = true
		} else if !selectionPathsMatch(
			value.Scope.RepositoryPaths,
			request.RepositoryPaths,
		) {
			return 0, false
		} else {
			score += 30
		}
	}
	if len(value.Scope.Models) > 0 {
		if request.Model == "" ||
			!selectionContains(value.Scope.Models, request.Model) {
			return 0, false
		}
		score += 20
	}
	for _, condition := range value.Applicability.DeterministicConditions {
		if condition.Kind != experience.ConditionPathPattern {
			return 0, false
		}
		if len(request.RepositoryPaths) == 0 {
			unresolvedSemanticScope = true
			continue
		}
		if !selectionPathsMatch(
			condition.Values,
			request.RepositoryPaths,
		) {
			return 0, false
		}
	}
	if unresolvedSemanticScope && lexicalOverlap == 0 {
		return 0, false
	}
	score += lexicalOverlap
	return score, true
}

func selectionPathsMatch(patterns, paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, pattern := range patterns {
		for _, value := range paths {
			if experience.RelativePathPatternMatches(pattern, value) {
				return true
			}
		}
	}
	return false
}

func selectionLexicalOverlap(taskHint, experienceText string) int {
	hintTerms := selectionLexicalTerms(taskHint)
	if len(hintTerms) == 0 {
		return 0
	}
	textTerms := selectionLexicalTerms(experienceText)
	overlap := 0
	for term := range hintTerms {
		if _, found := textTerms[term]; found {
			overlap++
			if overlap == 10 {
				return overlap
			}
		}
	}
	return overlap
}

func selectionLexicalTerms(value string) map[string]struct{} {
	stopwords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {},
		"be": {}, "by": {}, "for": {}, "from": {}, "in": {}, "is": {},
		"it": {}, "of": {}, "on": {}, "or": {}, "that": {}, "the": {},
		"this": {}, "to": {}, "with": {},
	}
	parts := strings.FieldsFunc(
		strings.ToLower(value),
		func(character rune) bool {
			return !unicode.IsLetter(character) &&
				!unicode.IsNumber(character)
		},
	)
	result := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = normalizeSelectionLexicalTerm(part)
		if part == "" {
			continue
		}
		if _, ignored := stopwords[part]; ignored {
			continue
		}
		result[part] = struct{}{}
	}
	return result
}

func normalizeSelectionLexicalTerm(value string) string {
	switch value {
	case "asynchronous":
		return "async"
	case "cancellation", "cancelled", "canceled",
		"cancelling", "canceling", "cancels":
		return "cancel"
	default:
		return value
	}
}

func estimateExperienceSelectionTokens(
	value experience.Experience,
) (int, error) {
	verifier, err := canonicalSelectionVerifierJSON(value.Verifier)
	if err != nil {
		return 0, err
	}
	bytes := len([]byte(value.Guidance.Instruction)) +
		len([]byte(value.Guidance.Rationale)) +
		len(verifier)
	for _, exception := range value.Guidance.Exceptions {
		bytes += len([]byte(exception))
	}
	return (bytes + 3) / 4, nil
}

func canonicalSelectionVerifierJSON(
	value experience.Verifier,
) ([]byte, error) {
	value.CoverageRequirements = append(
		[]experience.CoverageRequirement(nil),
		value.CoverageRequirements...,
	)
	sort.Slice(value.CoverageRequirements, func(first, second int) bool {
		return value.CoverageRequirements[first] <
			value.CoverageRequirements[second]
	})
	value.CoverageRequirements = compactSelectionComparable(
		value.CoverageRequirements,
	)
	if value.PathPattern != nil {
		copyValue := *value.PathPattern
		copyValue.Patterns = normalizedSelectionStrings(copyValue.Patterns)
		value.PathPattern = &copyValue
	}
	if value.VerificationAfterLastEdit != nil {
		copyValue := *value.VerificationAfterLastEdit
		copyValue.CommandClasses = normalizedSelectionStrings(
			copyValue.CommandClasses,
		)
		value.VerificationAfterLastEdit = &copyValue
	}
	if value.UserCorrectionAbsent != nil {
		copyValue := *value.UserCorrectionAbsent
		copyValue.MarkerFamilies = normalizedSelectionStrings(
			copyValue.MarkerFamilies,
		)
		value.UserCorrectionAbsent = &copyValue
	}
	return json.Marshal(value)
}

func normalizedSelectionStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return compactSelectionStrings(result)
}

func compactSelectionStrings(values []string) []string {
	return compactSelectionComparable(values)
}

func compactSelectionComparable[T comparable](values []T) []T {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func selectionContains[T comparable](values []T, target T) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sortExperienceRefs(values []experience.ExperienceRef) {
	sort.Slice(values, func(first, second int) bool {
		if values[first].ExperienceID != values[second].ExperienceID {
			return values[first].ExperienceID <
				values[second].ExperienceID
		}
		return values[first].Version < values[second].Version
	})
}

func selectionTimesEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Equal(*second)
}

var _ ExperienceCompilerStore = (*local.Store)(nil)
