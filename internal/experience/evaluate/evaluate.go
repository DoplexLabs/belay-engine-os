package evaluate

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/experience"
)

var (
	ErrInvalidInput = errors.New("invalid deterministic evaluation input")
)

type resultBuilder struct {
	result Result
}

func Evaluate(input Input) (Result, error) {
	if err := validateInput(input); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	builder := resultBuilder{
		result: Result{
			DerivationVersion:  Version,
			OpportunityState:   experience.OpportunityUnknown,
			ApplicabilityState: experience.ApplicabilityUnknown,
			VerifierState:      experience.VerifierUnknown,
			TaskOutcomeState:   experience.TaskOutcomeUnknown,
		},
	}

	builder.result.OpportunityState = evaluateOpportunity(
		input.Facts.Opportunities,
		&builder,
	)
	builder.result.ApplicabilityState = evaluateApplicability(input, &builder)
	builder.result.TaskOutcomeState = evaluateTaskOutcome(input, &builder)

	missingCoverage := false
	for _, requirement := range normalizedCoverageRequirements(
		input.Experience.Verifier.CoverageRequirements,
	) {
		complete := input.Coverage.Complete(requirement)
		builder.result.Coverage = append(
			builder.result.Coverage,
			CoverageStatus{
				Requirement: requirement,
				Complete:    complete,
			},
		)
		if !complete {
			missingCoverage = true
			builder.addGap(Gap{
				Component: ComponentVerifier,
				Code:      GapCoverageMissing,
				Coverage:  requirement,
			})
		}
	}
	if !missingCoverage {
		builder.result.VerifierState = evaluateVerifier(input, &builder)
	}
	if builder.result.VerifierState == experience.VerifierUnknown {
		builder.addEvidence(
			ComponentVerifier,
			verifierCoverageEvidence(input)...,
		)
	}
	return builder.finish(), nil
}

func evaluateOpportunity(
	facts []OpportunityFact,
	builder *resultBuilder,
) experience.OpportunityState {
	if len(facts) == 0 {
		builder.addGap(Gap{
			Component: ComponentOpportunity,
			Code:      GapNoOpportunityObservation,
		})
		return experience.OpportunityUnknown
	}
	states := make(map[experience.OpportunityState]bool)
	ambiguous := false
	var evidence []experience.EvidenceRef
	for _, fact := range facts {
		if fact.Ambiguous {
			ambiguous = true
		}
		states[fact.State] = true
		evidence = append(evidence, fact.Evidence...)
	}
	if ambiguous || len(states) != 1 {
		builder.addGap(Gap{
			Component: ComponentOpportunity,
			Code:      GapAmbiguousMatch,
		})
		return experience.OpportunityUnknown
	}
	for state := range states {
		builder.addEvidence(ComponentOpportunity, evidence...)
		return state
	}
	return experience.OpportunityUnknown
}

func evaluateTaskOutcome(
	input Input,
	builder *resultBuilder,
) experience.TaskOutcomeState {
	facts := input.Facts.TaskOutcomes
	if len(facts) == 0 {
		builder.addGap(Gap{
			Component: ComponentTaskOutcome,
			Code:      GapNoTaskOutcomeObservation,
		})
		return experience.TaskOutcomeUnknown
	}
	states := make(map[experience.TaskOutcomeState]bool)
	ambiguous := false
	var evidence []experience.EvidenceRef
	for _, fact := range facts {
		if fact.Ambiguous {
			ambiguous = true
		}
		states[fact.State] = true
		evidence = append(evidence, fact.Evidence...)
	}
	if ambiguous || len(states) != 1 {
		builder.addGap(Gap{
			Component: ComponentTaskOutcome,
			Code:      GapAmbiguousMatch,
		})
		return experience.TaskOutcomeUnknown
	}
	for state := range states {
		builder.addEvidence(ComponentTaskOutcome, evidence...)
		return state
	}
	return experience.TaskOutcomeUnknown
}

func evaluateApplicability(
	input Input,
	builder *resultBuilder,
) experience.ApplicabilityState {
	value := input.Experience
	context := input.Context
	unknown := false
	contextEvidence := context.Evidence
	if len(contextEvidence) == 0 {
		contextEvidence = input.Application.SourceEvidence
	}

	if value.Applicability.ExpiresAt != nil &&
		!context.AsOf.Before(*value.Applicability.ExpiresAt) {
		return applicabilityDecision(
			builder,
			experience.ApplicabilityNotApplicable,
			contextEvidence,
		)
	}
	if len(value.Scope.Harnesses) > 0 {
		switch {
		case context.Harness == "":
			unknown = true
		case !contains(value.Scope.Harnesses, context.Harness):
			return applicabilityDecision(
				builder,
				experience.ApplicabilityNotApplicable,
				contextEvidence,
			)
		}
	}
	if len(value.Scope.Models) > 0 {
		switch {
		case context.Model == "":
			unknown = true
		case !contains(value.Scope.Models, context.Model):
			return applicabilityDecision(
				builder,
				experience.ApplicabilityNotApplicable,
				contextEvidence,
			)
		}
	}
	if len(value.Scope.TaskFamilies) > 0 {
		switch {
		case len(context.TaskFamilies) == 0:
			unknown = true
		case !overlap(value.Scope.TaskFamilies, context.TaskFamilies):
			return applicabilityDecision(
				builder,
				experience.ApplicabilityNotApplicable,
				contextEvidence,
			)
		}
	}
	if len(value.Scope.RepositoryPaths) > 0 {
		matched := matchAnyPath(
			value.Scope.RepositoryPaths,
			context.RepositoryPaths,
		)
		switch {
		case matched:
		case conditionKindComplete(
			context.CompleteConditionKinds,
			experience.ConditionPathPattern,
		):
			return applicabilityDecision(
				builder,
				experience.ApplicabilityNotApplicable,
				contextEvidence,
			)
		default:
			unknown = true
			builder.addGap(Gap{
				Component:     ComponentApplicability,
				Code:          GapFactsIncomplete,
				ConditionKind: experience.ConditionPathPattern,
			})
		}
	}

	for _, condition := range value.Applicability.DeterministicConditions {
		matched, ambiguous, evidence := matchCondition(
			condition,
			input.Facts.Conditions,
		)
		switch {
		case ambiguous:
			unknown = true
			builder.addGap(Gap{
				Component:     ComponentApplicability,
				Code:          GapAmbiguousMatch,
				ConditionKind: condition.Kind,
			})
		case matched:
			builder.addEvidence(ComponentApplicability, evidence...)
		case conditionKindComplete(
			context.CompleteConditionKinds,
			condition.Kind,
		):
			return applicabilityDecision(
				builder,
				experience.ApplicabilityNotApplicable,
				contextEvidence,
			)
		default:
			unknown = true
			builder.addGap(Gap{
				Component:     ComponentApplicability,
				Code:          GapFactsIncomplete,
				ConditionKind: condition.Kind,
			})
		}
	}
	if len(value.Applicability.Exclusions) > 0 {
		unknown = true
		builder.addGap(Gap{
			Component: ComponentApplicability,
			Code:      GapSemanticExclusion,
		})
	}
	if unknown {
		builder.addGap(Gap{
			Component: ComponentApplicability,
			Code:      GapNoApplicableObservation,
		})
		return experience.ApplicabilityUnknown
	}
	return applicabilityDecision(
		builder,
		experience.ApplicabilityApplicable,
		contextEvidence,
	)
}

func applicabilityDecision(
	builder *resultBuilder,
	state experience.ApplicabilityState,
	evidence []experience.EvidenceRef,
) experience.ApplicabilityState {
	if len(evidence) == 0 {
		builder.addGap(Gap{
			Component: ComponentApplicability,
			Code:      GapFactsIncomplete,
		})
		return experience.ApplicabilityUnknown
	}
	builder.addEvidence(ComponentApplicability, evidence...)
	return state
}

func matchCondition(
	condition experience.DeterministicCondition,
	facts []ConditionFact,
) (bool, bool, []experience.EvidenceRef) {
	var evidence []experience.EvidenceRef
	ambiguous := false
	for _, fact := range facts {
		if fact.Kind != condition.Kind ||
			!conditionValuesMatch(condition, fact.Values) {
			continue
		}
		if fact.Ambiguous {
			ambiguous = true
			continue
		}
		evidence = append(evidence, fact.Evidence...)
	}
	return len(evidence) > 0, ambiguous, evidence
}

func conditionValuesMatch(
	condition experience.DeterministicCondition,
	factValues []string,
) bool {
	switch condition.Kind {
	case experience.ConditionPathPattern:
		return matchAnyPath(condition.Values, factValues)
	case experience.ConditionPriorEventSequence:
		if len(condition.Values) != len(factValues) {
			return false
		}
		for index := range condition.Values {
			if condition.Values[index] != factValues[index] {
				return false
			}
		}
		return true
	default:
		return overlap(condition.Values, factValues)
	}
}

func evaluateVerifier(input Input, builder *resultBuilder) experience.VerifierState {
	switch input.Experience.Verifier.Kind {
	case experience.VerifierCommandObserved:
		return evaluateCommand(input, false, builder)
	case experience.VerifierCommandSucceeded:
		return evaluateCommand(input, true, builder)
	case experience.VerifierFileNotModified:
		return evaluateFile(input, false, builder)
	case experience.VerifierFileModified:
		return evaluateFile(input, true, builder)
	case experience.VerifierPathPatternNotModified:
		return evaluatePathPattern(input, builder)
	case experience.VerifierVerificationAfterLastEdit:
		return evaluateVerificationAfterLastEdit(input, builder)
	case experience.VerifierNoRepeatFailure:
		return evaluateNoRepeatFailure(input, builder)
	case experience.VerifierUserCorrectionAbsent:
		return evaluateUserCorrectionAbsent(input, builder)
	case experience.VerifierObservationOnly:
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapObservationOnly,
		})
		return experience.VerifierUnknown
	default:
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapUnsupportedMatch,
		})
		return experience.VerifierUnknown
	}
}

func evaluateCommand(
	input Input,
	requireSuccess bool,
	builder *resultBuilder,
) experience.VerifierState {
	spec := input.Experience.Verifier.Command
	normalized := input.NormalizedVerifier
	if normalized.CommandSignature == "" ||
		normalized.CommandScrubbingVersion != spec.ScrubbingVersion {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapUnsupportedMatch,
		})
		return experience.VerifierUnknown
	}
	var matches []CommandFact
	ambiguous := false
	for _, fact := range input.Facts.Commands {
		if fact.Signature != normalized.CommandSignature {
			continue
		}
		if spec.CommandClass != "" && fact.Class != spec.CommandClass {
			ambiguous = true
			continue
		}
		if fact.Ambiguous {
			ambiguous = true
			continue
		}
		matches = append(matches, fact)
	}
	if commandFactsConflict(matches) {
		ambiguous = true
	}
	if ambiguous {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapAmbiguousMatch,
		})
		return experience.VerifierUnknown
	}
	if len(matches) == 0 {
		if declaredCoverage(
			input.Experience.Verifier,
			experience.CoverageTranscriptComplete,
		) {
			evidence := coverageEvidence(
				input.Coverage,
				experience.CoverageTranscriptComplete,
			)
			if len(evidence) > 0 {
				builder.addEvidence(ComponentVerifier, evidence...)
				return experience.VerifierViolated
			}
			builder.addGap(Gap{
				Component: ComponentVerifier,
				Code:      GapFactsIncomplete,
			})
			return experience.VerifierUnknown
		}
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapCoverageNotDeclared,
			Coverage:  experience.CoverageTranscriptComplete,
		})
		return experience.VerifierUnknown
	}
	evidence := commandEvidence(matches)
	if !requireSuccess {
		builder.addEvidence(ComponentVerifier, evidence...)
		return experience.VerifierSatisfied
	}
	hasFailure := false
	hasUnknown := false
	for _, fact := range matches {
		switch fact.Result {
		case FactResultSucceeded:
			builder.addEvidence(ComponentVerifier, evidence...)
			return experience.VerifierSatisfied
		case FactResultFailed:
			hasFailure = true
		default:
			hasUnknown = true
		}
	}
	if hasUnknown {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapFactsIncomplete,
		})
		return experience.VerifierUnknown
	}
	if hasFailure {
		builder.addEvidence(ComponentVerifier, evidence...)
		return experience.VerifierViolated
	}
	return experience.VerifierUnknown
}

func evaluateFile(
	input Input,
	wantModified bool,
	builder *resultBuilder,
) experience.VerifierState {
	want := input.Experience.Verifier.File.Path
	var evidence []experience.EvidenceRef
	ambiguous := false
	for _, fact := range input.Facts.FileChanges {
		if fact.Path != want {
			continue
		}
		if fact.Ambiguous {
			ambiguous = true
			continue
		}
		evidence = append(evidence, fact.Evidence...)
	}
	if ambiguous {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapAmbiguousMatch,
		})
		return experience.VerifierUnknown
	}
	if len(evidence) > 0 {
		builder.addEvidence(ComponentVerifier, evidence...)
		if wantModified {
			return experience.VerifierSatisfied
		}
		return experience.VerifierViolated
	}
	if !fileAbsenceCoverageDeclared(input.Experience.Verifier) {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapCoverageNotDeclared,
		})
		return experience.VerifierUnknown
	}
	evidence = fileAbsenceEvidence(input)
	if len(evidence) == 0 {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapFactsIncomplete,
		})
		return experience.VerifierUnknown
	}
	builder.addEvidence(ComponentVerifier, evidence...)
	if wantModified {
		return experience.VerifierViolated
	}
	return experience.VerifierSatisfied
}

func evaluatePathPattern(
	input Input,
	builder *resultBuilder,
) experience.VerifierState {
	var evidence []experience.EvidenceRef
	ambiguous := false
	for _, fact := range input.Facts.FileChanges {
		if !matchAnyPath(
			input.Experience.Verifier.PathPattern.Patterns,
			[]string{fact.Path},
		) {
			continue
		}
		if fact.Ambiguous {
			ambiguous = true
			continue
		}
		evidence = append(evidence, fact.Evidence...)
	}
	if ambiguous {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapAmbiguousMatch,
		})
		return experience.VerifierUnknown
	}
	if len(evidence) > 0 {
		builder.addEvidence(ComponentVerifier, evidence...)
		return experience.VerifierViolated
	}
	if !fileAbsenceCoverageDeclared(input.Experience.Verifier) {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapCoverageNotDeclared,
		})
		return experience.VerifierUnknown
	}
	evidence = fileAbsenceEvidence(input)
	if len(evidence) == 0 {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapFactsIncomplete,
		})
		return experience.VerifierUnknown
	}
	builder.addEvidence(ComponentVerifier, evidence...)
	return experience.VerifierSatisfied
}

func evaluateVerificationAfterLastEdit(
	input Input,
	builder *resultBuilder,
) experience.VerifierState {
	var latest int64 = -1
	var latestEditEvidence []experience.EvidenceRef
	for _, fact := range input.Facts.FileChanges {
		if fact.Ambiguous {
			builder.addGap(Gap{
				Component: ComponentVerifier,
				Code:      GapAmbiguousMatch,
			})
			return experience.VerifierUnknown
		}
		if fact.Order > latest {
			latest = fact.Order
			latestEditEvidence = append(
				latestEditEvidence[:0],
				fact.Evidence...,
			)
		} else if fact.Order == latest {
			latestEditEvidence = append(
				latestEditEvidence,
				fact.Evidence...,
			)
		}
	}
	if latest < 0 {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapNoEditObserved,
		})
		return experience.VerifierUnknown
	}
	spec := input.Experience.Verifier.VerificationAfterLastEdit
	var matches []VerificationFact
	for _, fact := range input.Facts.Verifications {
		if fact.Order <= latest ||
			(len(spec.CommandClasses) > 0 &&
				!contains(spec.CommandClasses, fact.CommandClass)) {
			continue
		}
		if fact.Ambiguous {
			builder.addGap(Gap{
				Component: ComponentVerifier,
				Code:      GapAmbiguousMatch,
			})
			return experience.VerifierUnknown
		}
		matches = append(matches, fact)
	}
	if verificationFactsConflict(matches) {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapAmbiguousMatch,
		})
		return experience.VerifierUnknown
	}
	if len(matches) == 0 {
		if declaredCoverage(
			input.Experience.Verifier,
			experience.CoverageTranscriptComplete,
		) {
			evidence := coverageEvidence(
				input.Coverage,
				experience.CoverageTranscriptComplete,
			)
			if len(evidence) > 0 {
				builder.addEvidence(
					ComponentVerifier,
					append(latestEditEvidence, evidence...)...,
				)
				return experience.VerifierViolated
			}
			builder.addGap(Gap{
				Component: ComponentVerifier,
				Code:      GapFactsIncomplete,
			})
			return experience.VerifierUnknown
		}
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapCoverageNotDeclared,
			Coverage:  experience.CoverageTranscriptComplete,
		})
		return experience.VerifierUnknown
	}
	evidence := verificationEvidence(matches)
	if !spec.RequireSuccess {
		builder.addEvidence(
			ComponentVerifier,
			append(latestEditEvidence, evidence...)...,
		)
		return experience.VerifierSatisfied
	}
	hasFailure := false
	hasUnknown := false
	for _, fact := range matches {
		switch fact.Result {
		case FactResultSucceeded:
			builder.addEvidence(
				ComponentVerifier,
				append(latestEditEvidence, evidence...)...,
			)
			return experience.VerifierSatisfied
		case FactResultFailed:
			hasFailure = true
		default:
			hasUnknown = true
		}
	}
	if hasUnknown {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapFactsIncomplete,
		})
		return experience.VerifierUnknown
	}
	if hasFailure {
		builder.addEvidence(
			ComponentVerifier,
			append(latestEditEvidence, evidence...)...,
		)
		return experience.VerifierViolated
	}
	return experience.VerifierUnknown
}

func evaluateNoRepeatFailure(
	input Input,
	builder *resultBuilder,
) experience.VerifierState {
	spec := input.Experience.Verifier.NoRepeatFailure
	if input.Context.LastTurnIndex == nil {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapFactsIncomplete,
		})
		return experience.VerifierUnknown
	}
	var matches []FailureFact
	var evidence []experience.EvidenceRef
	ambiguous := false
	for _, fact := range input.Facts.Failures {
		if fact.CommandClass != spec.CommandClass ||
			fact.NormalizedPattern != spec.NormalizedPattern {
			continue
		}
		evidence = append(evidence, fact.Evidence...)
		if fact.Ambiguous {
			ambiguous = true
			continue
		}
		matches = append(matches, fact)
	}
	if ambiguous {
		builder.addEvidence(ComponentVerifier, evidence...)
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapAmbiguousMatch,
		})
		return experience.VerifierUnknown
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Order < matches[j].Order
	})
	for index := 1; index < len(matches); index++ {
		if matches[index].Order-matches[index-1].Order >
			int64(spec.WindowTurns) {
			continue
		}
		builder.addEvidence(ComponentVerifier, evidence...)
		return experience.VerifierViolated
	}
	builder.addEvidence(ComponentVerifier, evidence...)
	if !declaredCoverage(
		input.Experience.Verifier,
		experience.CoverageTranscriptComplete,
	) {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapCoverageNotDeclared,
			Coverage:  experience.CoverageTranscriptComplete,
		})
		return experience.VerifierUnknown
	}
	if !input.Coverage.Complete(experience.CoverageTranscriptComplete) {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapFactsIncomplete,
		})
		return experience.VerifierUnknown
	}
	coverage := coverageEvidence(
		input.Coverage,
		experience.CoverageTranscriptComplete,
	)
	if len(coverage) == 0 {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapFactsIncomplete,
		})
		return experience.VerifierUnknown
	}
	builder.addEvidence(ComponentVerifier, coverage...)
	return experience.VerifierSatisfied
}

func evaluateUserCorrectionAbsent(
	input Input,
	builder *resultBuilder,
) experience.VerifierState {
	spec := input.Experience.Verifier.UserCorrectionAbsent
	var evidence []experience.EvidenceRef
	ambiguous := false
	for _, fact := range input.Facts.Corrections {
		if len(spec.MarkerFamilies) > 0 &&
			!contains(spec.MarkerFamilies, fact.MarkerFamily) {
			continue
		}
		if fact.Ambiguous {
			ambiguous = true
			continue
		}
		evidence = append(evidence, fact.Evidence...)
	}
	if ambiguous {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapAmbiguousMatch,
		})
		return experience.VerifierUnknown
	}
	if len(evidence) > 0 {
		builder.addEvidence(ComponentVerifier, evidence...)
		return experience.VerifierViolated
	}
	if !declaredCoverage(
		input.Experience.Verifier,
		experience.CoverageTranscriptComplete,
	) {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapCoverageNotDeclared,
			Coverage:  experience.CoverageTranscriptComplete,
		})
		return experience.VerifierUnknown
	}
	evidence = coverageEvidence(
		input.Coverage,
		experience.CoverageTranscriptComplete,
	)
	if len(evidence) == 0 {
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapFactsIncomplete,
		})
		return experience.VerifierUnknown
	}
	builder.addEvidence(ComponentVerifier, evidence...)
	return experience.VerifierSatisfied
}

func (builder *resultBuilder) addGap(value Gap) {
	builder.result.Gaps = append(builder.result.Gaps, value)
}

func (builder *resultBuilder) addEvidence(
	component Component,
	refs ...experience.EvidenceRef,
) {
	for _, ref := range refs {
		builder.result.Evidence = append(
			builder.result.Evidence,
			CitedEvidence{Component: component, Ref: ref},
		)
	}
}

func (builder *resultBuilder) finish() Result {
	sort.Slice(builder.result.Gaps, func(i, j int) bool {
		left, _ := json.Marshal(builder.result.Gaps[i])
		right, _ := json.Marshal(builder.result.Gaps[j])
		return string(left) < string(right)
	})
	builder.result.Gaps = compactGaps(builder.result.Gaps)
	if len(builder.result.Gaps) > MaxResultGaps {
		builder.result.Gaps = builder.result.Gaps[:MaxResultGaps]
	}

	sort.Slice(builder.result.Evidence, func(i, j int) bool {
		left, _ := json.Marshal(builder.result.Evidence[i])
		right, _ := json.Marshal(builder.result.Evidence[j])
		return string(left) < string(right)
	})
	builder.result.Evidence = compactEvidence(builder.result.Evidence)
	if len(builder.result.Evidence) > MaxResultEvidence {
		builder.result.Evidence = boundedEvidence(
			builder.result,
			MaxResultEvidence,
		)
		builder.addGap(Gap{
			Component: ComponentVerifier,
			Code:      GapEvidenceTruncated,
		})
		sort.Slice(builder.result.Gaps, func(i, j int) bool {
			left, _ := json.Marshal(builder.result.Gaps[i])
			right, _ := json.Marshal(builder.result.Gaps[j])
			return string(left) < string(right)
		})
		builder.result.Gaps = compactGaps(builder.result.Gaps)
		if len(builder.result.Gaps) > MaxResultGaps {
			builder.result.Gaps = builder.result.Gaps[:MaxResultGaps]
		}
	}
	if len(builder.result.Coverage) == 0 {
		builder.result.Coverage = nil
	}
	if len(builder.result.Gaps) == 0 {
		builder.result.Gaps = nil
	}
	if len(builder.result.Evidence) == 0 {
		builder.result.Evidence = nil
	}
	return builder.result
}

func validateInput(input Input) error {
	if err := input.Application.Validate(); err != nil {
		return fmt.Errorf("application: %w", err)
	}
	if err := input.Experience.Validate(); err != nil {
		return fmt.Errorf("experience: %w", err)
	}
	if input.Application.DeliveryState != experience.DeliveryDelivered {
		return errors.New("application is not delivered")
	}
	if input.Application.Experience.ExperienceID !=
		input.Experience.ExperienceID ||
		input.Application.Experience.Version != input.Experience.Version {
		return errors.New("application experience version is not exact")
	}
	if input.Application.ProjectIdentity != input.Experience.Scope.ProjectIdentity ||
		input.Context.ProjectIdentity != input.Application.ProjectIdentity {
		return errors.New("project binding does not match")
	}
	if input.Context.SessionKey != input.Application.SessionKey {
		return errors.New("session binding does not match")
	}
	if input.Experience.Scope.Kind == experience.ScopeSession &&
		input.Experience.Scope.SessionKey != input.Application.SessionKey {
		return errors.New("session-scoped experience does not match application")
	}
	if input.Context.AsOf.IsZero() ||
		input.Context.AsOf.Before(*input.Application.DeliveredAt) {
		return errors.New("evaluation time is invalid")
	}
	if input.Context.Harness != "" && !input.Context.Harness.Valid() {
		return errors.New("context harness is invalid")
	}
	if err := validateNormalizedString(
		"context model",
		input.Context.Model,
		false,
	); err != nil {
		return err
	}
	for _, value := range input.Context.TaskFamilies {
		if err := validateNormalizedString(
			"context task family",
			value,
			true,
		); err != nil {
			return err
		}
	}
	for _, value := range input.Context.RepositoryPaths {
		if err := (experience.FileVerifierSpec{Path: value}).Validate(); err != nil {
			return fmt.Errorf("context repository path: %w", err)
		}
	}
	seenKinds := make(map[experience.DeterministicConditionKind]bool)
	for _, kind := range input.Context.CompleteConditionKinds {
		if !kind.Valid() || seenKinds[kind] {
			return errors.New("complete condition kinds are invalid or duplicated")
		}
		seenKinds[kind] = true
	}
	if input.Context.LastTurnIndex != nil && *input.Context.LastTurnIndex < 0 {
		return errors.New("last turn index is invalid")
	}
	if err := validateEvidence(
		input.Context,
		input.Context.Evidence,
		MaxEvidencePerFact,
		false,
	); err != nil {
		return fmt.Errorf("context evidence: %w", err)
	}
	if err := validateCoverageEvidence(
		input.Context,
		input.Coverage,
	); err != nil {
		return err
	}
	if err := validateNormalizedVerifier(
		input.Experience.Verifier,
		input.NormalizedVerifier,
	); err != nil {
		return err
	}
	return validateFacts(input.Context, input.Facts)
}

func validateNormalizedVerifier(
	verifier experience.Verifier,
	normalized NormalizedVerifier,
) error {
	if verifier.Kind != experience.VerifierCommandObserved &&
		verifier.Kind != experience.VerifierCommandSucceeded {
		if normalized.DeclaredCommand != "" ||
			normalized.CommandSignature != "" ||
			normalized.CommandScrubbingVersion != "" {
			return errors.New("non-command verifier has command matcher")
		}
		return nil
	}
	if normalized.DeclaredCommand == "" &&
		normalized.CommandSignature == "" &&
		normalized.CommandScrubbingVersion == "" {
		return nil
	}
	if normalized.DeclaredCommand != verifier.Command.Command {
		return errors.New("normalized command matcher is not bound to declared command")
	}
	if err := validateNormalizedString(
		"declared command",
		normalized.DeclaredCommand,
		true,
	); err != nil {
		return err
	}
	if err := validateNormalizedString(
		"normalized command signature",
		normalized.CommandSignature,
		true,
	); err != nil {
		return err
	}
	if strings.ToLower(normalized.CommandSignature) !=
		normalized.CommandSignature {
		return errors.New("normalized command signature must be lowercase")
	}
	return validateNormalizedString(
		"command scrubbing version",
		normalized.CommandScrubbingVersion,
		true,
	)
}

func validateFacts(context Context, facts Facts) error {
	total := len(facts.Opportunities) +
		len(facts.Conditions) +
		len(facts.Commands) +
		len(facts.FileChanges) +
		len(facts.Verifications) +
		len(facts.Failures) +
		len(facts.Corrections) +
		len(facts.TaskOutcomes)
	if total > MaxFacts {
		return errors.New("fact count exceeds bound")
	}
	for _, fact := range facts.Opportunities {
		if !fact.State.Valid() {
			return errors.New("opportunity fact state is invalid")
		}
		if err := validateFactMeta(context, fact.FactMeta); err != nil {
			return err
		}
	}
	for _, fact := range facts.Conditions {
		if !fact.Kind.Valid() || len(fact.Values) == 0 {
			return errors.New("condition fact is invalid")
		}
		if err := validateFactMeta(context, fact.FactMeta); err != nil {
			return err
		}
		for _, value := range fact.Values {
			if fact.Kind == experience.ConditionPathPattern {
				if err := (experience.FileVerifierSpec{Path: value}).Validate(); err != nil {
					return fmt.Errorf("condition path fact: %w", err)
				}
				continue
			}
			if err := validateNormalizedString(
				"condition fact value",
				value,
				true,
			); err != nil {
				return err
			}
		}
	}
	for _, fact := range facts.Commands {
		if err := validateFactMeta(context, fact.FactMeta); err != nil {
			return err
		}
		if err := validateNormalizedString(
			"command signature",
			fact.Signature,
			true,
		); err != nil {
			return err
		}
		if strings.ToLower(fact.Signature) != fact.Signature {
			return errors.New("command signature must be lowercase")
		}
		if err := validateNormalizedString(
			"command class",
			fact.Class,
			false,
		); err != nil {
			return err
		}
		if !fact.Result.Valid() {
			return errors.New("command fact result is invalid")
		}
	}
	for _, fact := range facts.FileChanges {
		if err := validateFactMeta(context, fact.FactMeta); err != nil {
			return err
		}
		if err := (experience.FileVerifierSpec{Path: fact.Path}).Validate(); err != nil {
			return fmt.Errorf("file change path: %w", err)
		}
	}
	for _, fact := range facts.Verifications {
		if err := validateFactMeta(context, fact.FactMeta); err != nil {
			return err
		}
		if err := validateNormalizedString(
			"verification command class",
			fact.CommandClass,
			true,
		); err != nil {
			return err
		}
		if !fact.Result.Valid() {
			return errors.New("verification fact result is invalid")
		}
	}
	for _, fact := range facts.Failures {
		if err := validateFactMeta(context, fact.FactMeta); err != nil {
			return err
		}
		if err := validateNormalizedString(
			"failure command class",
			fact.CommandClass,
			true,
		); err != nil {
			return err
		}
		if err := validateNormalizedString(
			"normalized failure pattern",
			fact.NormalizedPattern,
			true,
		); err != nil {
			return err
		}
	}
	for _, fact := range facts.Corrections {
		if err := validateFactMeta(context, fact.FactMeta); err != nil {
			return err
		}
		if err := validateNormalizedString(
			"correction marker family",
			fact.MarkerFamily,
			true,
		); err != nil {
			return err
		}
	}
	for _, fact := range facts.TaskOutcomes {
		if !fact.State.Valid() {
			return errors.New("task outcome fact state is invalid")
		}
		if err := validateFactMeta(context, fact.FactMeta); err != nil {
			return err
		}
	}
	return nil
}

func validateFactMeta(context Context, value FactMeta) error {
	if value.Order < 0 ||
		(context.LastTurnIndex != nil && value.Order > *context.LastTurnIndex) {
		return errors.New("fact order is outside the bounded session")
	}
	return validateEvidence(
		context,
		value.Evidence,
		MaxEvidencePerFact,
		true,
	)
}

func validateCoverageEvidence(context Context, value Coverage) error {
	seen := make(map[experience.CoverageRequirement]bool)
	for _, item := range value.Evidence {
		if !item.Requirement.Valid() || seen[item.Requirement] {
			return errors.New("coverage evidence requirement is invalid or duplicated")
		}
		seen[item.Requirement] = true
		if err := validateEvidence(
			context,
			item.Evidence,
			MaxEvidencePerFact,
			true,
		); err != nil {
			return fmt.Errorf("coverage evidence: %w", err)
		}
	}
	return nil
}

func validateEvidence(
	context Context,
	values []experience.EvidenceRef,
	limit int,
	required bool,
) error {
	if (required && len(values) == 0) || len(values) > limit {
		return errors.New("evidence is required and bounded")
	}
	for _, ref := range values {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("evidence reference: %w", err)
		}
		if ref.Kind == experience.EvidenceTranscriptTurn &&
			ref.SessionKey != context.SessionKey {
			return errors.New("transcript evidence is outside the bound session")
		}
	}
	return nil
}

func validateNormalizedString(name, value string, required bool) error {
	if required && value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value == "" {
		return nil
	}
	if value != strings.TrimSpace(value) ||
		len(value) > 16*1024 ||
		!utf8.ValidString(value) {
		return fmt.Errorf("%s is not normalized", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func matchAnyPath(patterns, paths []string) bool {
	for _, pattern := range patterns {
		if !safePattern(pattern) {
			return false
		}
		for _, value := range paths {
			if !safeRelativePath(value) {
				return false
			}
			if experience.RelativePathPatternMatches(pattern, value) {
				return true
			}
		}
	}
	return false
}

func safeRelativePath(value string) bool {
	return (experience.FileVerifierSpec{Path: value}).Validate() == nil
}

func safePattern(value string) bool {
	return (experience.PathPatternVerifierSpec{
		Patterns: []string{value},
	}).Validate() == nil &&
		path.Clean(value) == value
}

func fileAbsenceEvidence(input Input) []experience.EvidenceRef {
	var result []experience.EvidenceRef
	for _, requirement := range []experience.CoverageRequirement{
		experience.CoverageWorkspaceCaptured,
		experience.CoverageCanonicalComplete,
	} {
		if declaredCoverage(input.Experience.Verifier, requirement) {
			result = append(
				result,
				coverageEvidence(input.Coverage, requirement)...,
			)
		}
	}
	return result
}

func fileAbsenceCoverageDeclared(verifier experience.Verifier) bool {
	return declaredCoverage(
		verifier,
		experience.CoverageWorkspaceCaptured,
	) || declaredCoverage(
		verifier,
		experience.CoverageCanonicalComplete,
	)
}

func declaredCoverage(
	verifier experience.Verifier,
	requirement experience.CoverageRequirement,
) bool {
	return contains(verifier.CoverageRequirements, requirement)
}

func conditionKindComplete(
	values []experience.DeterministicConditionKind,
	want experience.DeterministicConditionKind,
) bool {
	return contains(values, want)
}

func coverageEvidence(
	coverage Coverage,
	requirement experience.CoverageRequirement,
) []experience.EvidenceRef {
	for _, item := range coverage.Evidence {
		if item.Requirement == requirement {
			return item.Evidence
		}
	}
	return nil
}

func verifierCoverageEvidence(input Input) []experience.EvidenceRef {
	var result []experience.EvidenceRef
	for _, requirement := range normalizedCoverageRequirements(
		input.Experience.Verifier.CoverageRequirements,
	) {
		result = append(
			result,
			coverageEvidence(input.Coverage, requirement)...,
		)
	}
	return result
}

func normalizedCoverageRequirements(
	values []experience.CoverageRequirement,
) []experience.CoverageRequirement {
	result := append([]experience.CoverageRequirement(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return compactComparable(result)
}

func commandFactsConflict(values []CommandFact) bool {
	for left := range values {
		for right := left + 1; right < len(values); right++ {
			if values[left].Order == values[right].Order &&
				values[left].Result != values[right].Result {
				return true
			}
		}
	}
	return false
}

func verificationFactsConflict(values []VerificationFact) bool {
	for left := range values {
		for right := left + 1; right < len(values); right++ {
			if values[left].Order == values[right].Order &&
				values[left].CommandClass == values[right].CommandClass &&
				values[left].Result != values[right].Result {
				return true
			}
		}
	}
	return false
}

func commandEvidence(values []CommandFact) []experience.EvidenceRef {
	var result []experience.EvidenceRef
	for _, value := range values {
		result = append(result, value.Evidence...)
	}
	return result
}

func verificationEvidence(
	values []VerificationFact,
) []experience.EvidenceRef {
	var result []experience.EvidenceRef
	for _, value := range values {
		result = append(result, value.Evidence...)
	}
	return result
}

func boundedEvidence(result Result, limit int) []CitedEvidence {
	required := map[Component]bool{
		ComponentOpportunity: result.OpportunityState !=
			experience.OpportunityUnknown,
		ComponentApplicability: result.ApplicabilityState !=
			experience.ApplicabilityUnknown,
		ComponentVerifier: result.VerifierState ==
			experience.VerifierSatisfied ||
			result.VerifierState == experience.VerifierViolated,
		ComponentTaskOutcome: result.TaskOutcomeState !=
			experience.TaskOutcomeUnknown,
	}
	keep := make([]bool, len(result.Evidence))
	anchored := make(map[Component]bool)
	kept := 0
	for index, cited := range result.Evidence {
		if !required[cited.Component] || anchored[cited.Component] {
			continue
		}
		keep[index] = true
		anchored[cited.Component] = true
		kept++
	}
	for index := range result.Evidence {
		if kept == limit {
			break
		}
		if keep[index] {
			continue
		}
		keep[index] = true
		kept++
	}
	bounded := make([]CitedEvidence, 0, limit)
	for index, cited := range result.Evidence {
		if keep[index] {
			bounded = append(bounded, cited)
		}
	}
	return bounded
}

func compactEvidence(values []CitedEvidence) []CitedEvidence {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	previous, _ := json.Marshal(values[0])
	for _, value := range values[1:] {
		current, _ := json.Marshal(value)
		if string(current) == string(previous) {
			continue
		}
		result = append(result, value)
		previous = current
	}
	return result
}

func compactGaps(values []Gap) []Gap {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	previous, _ := json.Marshal(values[0])
	for _, value := range values[1:] {
		current, _ := json.Marshal(value)
		if string(current) == string(previous) {
			continue
		}
		result = append(result, value)
		previous = current
	}
	return result
}

func compactComparable[T comparable](values []T) []T {
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

func contains[T comparable](values []T, want T) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func overlap[T comparable](left, right []T) bool {
	for _, value := range left {
		if contains(right, value) {
			return true
		}
	}
	return false
}
