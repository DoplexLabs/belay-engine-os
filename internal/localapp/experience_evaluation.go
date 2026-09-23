package localapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/experience/evaluate"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	trajectoryderive "github.com/DoplexLabs/belay-engine/internal/trajectory/derive"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	maxExperienceEvaluationBatch    = 256
	maxExperienceEvaluationTurns    = 10000
	maxExperienceEvaluationOutcomes = 500
	maxEvaluationExcerptBytes       = 16 << 10
	commandScrubbingVersionV1       = "belay.redaction.v1"
)

var (
	ErrExperienceEvaluationNoEvidence = errors.New(
		"experience evaluation has no real bound-session evidence",
	)
	ErrExperienceEvaluationFactsExceeded = errors.New(
		"experience evaluation facts exceed the deterministic bound",
	)
	ErrExperienceEvaluationUnsupportedHarness = errors.New(
		"experience evaluation harness is unsupported",
	)
)

type ExperienceEvaluationRepository interface {
	TrajectoryCanonicalEventStore
	QueryExperienceApplicationsNeedingEvaluation(
		context.Context,
		int,
	) ([]experience.Application, error)
	GetExperience(
		context.Context,
		experience.ExperienceRef,
	) (local.StoredExperience, error)
	GetTranscriptSession(context.Context, string) (transcript.Session, error)
	QueryTranscriptTurns(context.Context, string, int) ([]transcript.Turn, error)
	QueryOutcomes(
		context.Context,
		local.OutcomeQuery,
	) ([]trajectory.Outcome, error)
	GetTrajectoryDerivationState(
		context.Context,
		string,
		string,
	) (local.TrajectoryDerivationState, error)
	IsTrajectoryDerivationStateCurrent(
		context.Context,
		local.TrajectoryDerivationState,
	) (bool, error)
	GetExperienceApplicationEvaluation(
		context.Context,
		string,
	) (*experience.Evaluation, error)
	ApplyExperienceEvaluation(
		context.Context,
		experience.Evaluation,
	) (experience.Application, bool, error)
}

type ExperienceEvaluationReport struct {
	Attempted int
	Applied   int
	Replayed  int
	Deferred  int
}

type ExperienceEvaluationApplicationError struct {
	ApplicationID string
	Cause         error
}

func (value *ExperienceEvaluationApplicationError) Error() string {
	return fmt.Sprintf(
		"evaluate experience application %q: %s",
		value.ApplicationID,
		experienceEvaluationDiagnostic(value.Cause),
	)
}

func (value *ExperienceEvaluationApplicationError) Unwrap() error {
	return value.Cause
}

type ExperienceEvaluationCoordinator struct {
	repository ExperienceEvaluationRepository
	now        func() time.Time
}

type ExperienceEvaluationOption func(*ExperienceEvaluationCoordinator)

func WithExperienceEvaluationClock(
	clock func() time.Time,
) ExperienceEvaluationOption {
	return func(coordinator *ExperienceEvaluationCoordinator) {
		if clock != nil {
			coordinator.now = clock
		}
	}
}

func NewExperienceEvaluationCoordinator(
	repository ExperienceEvaluationRepository,
	options ...ExperienceEvaluationOption,
) (*ExperienceEvaluationCoordinator, error) {
	if repository == nil {
		return nil, errors.New("experience evaluation requires a repository")
	}
	coordinator := &ExperienceEvaluationCoordinator{
		repository: repository,
		now:        time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(coordinator)
		}
	}
	return coordinator, nil
}

func (coordinator *ExperienceEvaluationCoordinator) Evaluate(
	ctx context.Context,
	limit int,
) (ExperienceEvaluationReport, error) {
	var report ExperienceEvaluationReport
	if ctx == nil {
		return report, errors.New("experience evaluation requires a context")
	}
	if coordinator == nil || coordinator.repository == nil {
		return report, errors.New("experience evaluation requires a repository")
	}
	if limit < 1 || limit > maxExperienceEvaluationBatch {
		return report, fmt.Errorf(
			"experience evaluation limit must be between 1 and %d",
			maxExperienceEvaluationBatch,
		)
	}
	observedAt := coordinator.now().UTC()
	if observedAt.IsZero() {
		return report, errors.New("experience evaluation observation time is required")
	}
	applications, err := coordinator.repository.
		QueryExperienceApplicationsNeedingEvaluation(ctx, limit)
	if err != nil {
		return report, fmt.Errorf(
			"query experience applications needing evaluation: %w",
			err,
		)
	}
	if len(applications) > limit {
		applications = applications[:limit]
	}
	runErrors := make([]error, 0)
	for _, application := range applications {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(runErrors, err)...)
		}
		report.Attempted++
		applied, err := coordinator.evaluateApplication(
			ctx,
			application,
			observedAt,
		)
		if err != nil {
			report.Deferred++
			runErrors = append(runErrors, &ExperienceEvaluationApplicationError{
				ApplicationID: application.ApplicationID,
				Cause:         err,
			})
			continue
		}
		if applied {
			report.Applied++
		} else {
			report.Replayed++
		}
	}
	return report, errors.Join(runErrors...)
}

func (coordinator *ExperienceEvaluationCoordinator) evaluateApplication(
	ctx context.Context,
	application experience.Application,
	observedAt time.Time,
) (bool, error) {
	input, anchors, err := coordinator.buildInput(
		ctx,
		application,
		observedAt,
	)
	if err != nil {
		return false, err
	}
	result, err := evaluate.Evaluate(input)
	if err != nil {
		return false, fmt.Errorf("derive deterministic evaluation: %w", err)
	}
	previous, err := coordinator.repository.GetExperienceApplicationEvaluation(
		ctx,
		application.ApplicationID,
	)
	if err != nil {
		return false, fmt.Errorf("load prior deterministic evaluation: %w", err)
	}
	evaluation, err := persistedExperienceEvaluation(
		application,
		result,
		anchors,
		observedAt,
		previous,
	)
	if err != nil {
		return false, err
	}
	_, applied, err := coordinator.repository.ApplyExperienceEvaluation(
		ctx,
		evaluation,
	)
	if err != nil {
		return false, fmt.Errorf("persist deterministic evaluation: %w", err)
	}
	return applied, nil
}

func (coordinator *ExperienceEvaluationCoordinator) buildInput(
	ctx context.Context,
	application experience.Application,
	observedAt time.Time,
) (evaluate.Input, []experience.EvidenceRef, error) {
	if err := application.Validate(); err != nil {
		return evaluate.Input{}, nil, fmt.Errorf("invalid queued application: %w", err)
	}
	if application.DeliveryState != experience.DeliveryDelivered ||
		application.SessionKey == "" {
		return evaluate.Input{}, nil, errors.New(
			"queued application is not exactly delivered and session-bound",
		)
	}
	stored, err := coordinator.repository.GetExperience(
		ctx,
		application.Experience,
	)
	if err != nil {
		return evaluate.Input{}, nil, fmt.Errorf("load exact experience: %w", err)
	}
	value := stored.Experience
	if value.ExperienceID != application.Experience.ExperienceID ||
		value.Version != application.Experience.Version ||
		value.Scope.ProjectIdentity != application.ProjectIdentity {
		return evaluate.Input{}, nil, errors.New(
			"experience version or project binding does not match application",
		)
	}
	session, err := coordinator.repository.GetTranscriptSession(
		ctx,
		application.SessionKey,
	)
	if err != nil {
		return evaluate.Input{}, nil, fmt.Errorf("load bound transcript session: %w", err)
	}
	if session.SessionKey != application.SessionKey ||
		session.ProjectIdentity != application.ProjectIdentity {
		return evaluate.Input{}, nil, errors.New(
			"transcript session does not match application binding",
		)
	}
	harness := evaluationHarness(session.Agent)
	if harness == "" {
		return evaluate.Input{}, nil,
			ErrExperienceEvaluationUnsupportedHarness
	}
	turns, err := coordinator.repository.QueryTranscriptTurns(
		ctx,
		application.SessionKey,
		maxExperienceEvaluationTurns,
	)
	if err != nil {
		return evaluate.Input{}, nil, fmt.Errorf("load bound transcript turns: %w", err)
	}
	if len(turns) > maxExperienceEvaluationTurns {
		return evaluate.Input{}, nil, errors.New(
			"transcript turn query exceeded the deterministic bound",
		)
	}
	sortTranscriptTurns(turns)
	for _, turn := range turns {
		if turn.SessionKey != application.SessionKey {
			return evaluate.Input{}, nil, errors.New(
				"transcript query returned a foreign session turn",
			)
		}
	}
	outcomes, err := coordinator.repository.QueryOutcomes(
		ctx,
		local.OutcomeQuery{
			ProjectIdentity: application.ProjectIdentity,
			SessionKey:      application.SessionKey,
			Limit:           maxExperienceEvaluationOutcomes,
		},
	)
	if err != nil {
		return evaluate.Input{}, nil, fmt.Errorf("load bound outcomes: %w", err)
	}
	if len(outcomes) > maxExperienceEvaluationOutcomes {
		return evaluate.Input{}, nil, errors.New(
			"outcome query exceeded the deterministic bound",
		)
	}
	sort.Slice(outcomes, func(left, right int) bool {
		if !outcomes[left].OccurredAt.Equal(outcomes[right].OccurredAt) {
			return outcomes[left].OccurredAt.Before(outcomes[right].OccurredAt)
		}
		return outcomes[left].OutcomeID < outcomes[right].OutcomeID
	})
	for _, outcome := range outcomes {
		if outcome.SessionKey != application.SessionKey ||
			outcome.ProjectIdentity != application.ProjectIdentity {
			return evaluate.Input{}, nil, errors.New(
				"outcome query returned foreign evidence",
			)
		}
	}
	canonicalEvents, canonicalLoadComplete, err :=
		loadTrajectoryCanonicalEvents(
			ctx,
			coordinator.repository,
			application.SessionKey,
		)
	if err != nil {
		return evaluate.Input{}, nil, fmt.Errorf(
			"load bound canonical events: %w",
			err,
		)
	}
	for _, event := range canonicalEvents {
		if event.Session.Key != application.SessionKey {
			return evaluate.Input{}, nil, errors.New(
				"canonical timeline returned foreign session evidence",
			)
		}
		if strings.TrimSpace(event.EventID) == "" {
			return evaluate.Input{}, nil, errors.New(
				"canonical timeline returned uncitable evidence",
			)
		}
	}

	trajectoryState, trajectoryCurrent, err := coordinator.trajectoryState(
		ctx,
		application.SessionKey,
	)
	if err != nil {
		return evaluate.Input{}, nil, err
	}
	coverage, anchors := evaluationCoverage(
		session,
		turns,
		canonicalEvents,
		canonicalLoadComplete,
		outcomes,
		trajectoryState,
		trajectoryCurrent,
	)
	facts, repositoryPaths, completeKinds, model, err := evaluationFacts(
		turns,
		outcomes,
		loadProjectConfig(session.ProjectPath),
	)
	if err != nil {
		return evaluate.Input{}, nil, err
	}
	if evaluationFactCount(facts) > evaluate.MaxFacts {
		return evaluate.Input{}, nil, ErrExperienceEvaluationFactsExceeded
	}
	completeKinds = coverageAwareConditionKinds(completeKinds, coverage)
	contextEvidence := boundedEvidenceRefs(anchors, evaluate.MaxEvidencePerFact)
	var lastTurnIndex *int64
	if len(turns) > 0 {
		value := turns[len(turns)-1].TurnIndex
		lastTurnIndex = &value
	}
	input := evaluate.Input{
		Application: application,
		Experience:  value,
		Context: evaluate.Context{
			ProjectIdentity:        application.ProjectIdentity,
			SessionKey:             application.SessionKey,
			Harness:                harness,
			Model:                  model,
			RepositoryPaths:        repositoryPaths,
			CompleteConditionKinds: completeKinds,
			LastTurnIndex:          lastTurnIndex,
			AsOf:                   observedAt,
			Evidence:               contextEvidence,
		},
		Coverage: coverage,
		NormalizedVerifier: normalizedEvaluationVerifier(
			value.Verifier,
		),
		Facts: facts,
	}
	return input, anchors, nil
}

func (coordinator *ExperienceEvaluationCoordinator) trajectoryState(
	ctx context.Context,
	sessionKey string,
) (local.TrajectoryDerivationState, bool, error) {
	state, err := coordinator.repository.GetTrajectoryDerivationState(
		ctx,
		sessionKey,
		trajectoryderive.Version,
	)
	if errors.Is(err, local.ErrTrajectoryDerivationNotFound) {
		return local.TrajectoryDerivationState{}, false, nil
	}
	if err != nil {
		return local.TrajectoryDerivationState{}, false,
			fmt.Errorf("load trajectory derivation state: %w", err)
	}
	if state.DerivationVersion != trajectoryderive.Version ||
		state.SessionKey != sessionKey ||
		state.Status != local.TrajectoryDerivationComplete {
		return state, false, nil
	}
	current, err := coordinator.repository.IsTrajectoryDerivationStateCurrent(
		ctx,
		state,
	)
	if err != nil {
		return local.TrajectoryDerivationState{}, false,
			fmt.Errorf("verify trajectory derivation freshness: %w", err)
	}
	return state, current, nil
}

func evaluationCoverage(
	session transcript.Session,
	turns []transcript.Turn,
	canonicalEvents []model.Event,
	canonicalLoadComplete bool,
	outcomes []trajectory.Outcome,
	state local.TrajectoryDerivationState,
	trajectoryCurrent bool,
) (evaluate.Coverage, []experience.EvidenceRef) {
	turnAnchors := transcriptCoverageAnchors(turns)
	canonicalAnchors := canonicalCoverageAnchors(canonicalEvents)
	outcomeAnchors := outcomeCoverageAnchors(outcomes)
	anchors := append(
		append([]experience.EvidenceRef(nil), turnAnchors...),
		canonicalAnchors...,
	)
	anchors = append(
		anchors,
		outcomeAnchors...,
	)
	anchors = boundedEvidenceRefs(anchors, evaluate.MaxEvidencePerFact)
	transcriptComplete := session.Coverage == transcript.CoverageComplete &&
		len(turns) == session.TurnCount &&
		session.TurnCount <= maxExperienceEvaluationTurns
	canonicalComplete := trajectoryCurrent &&
		state.Coverage.CanonicalEventsComplete &&
		canonicalLoadComplete
	outcomesComplete := trajectoryCurrent &&
		state.Coverage.FullyDerived &&
		state.Coverage.TranscriptTurnsComplete &&
		state.Coverage.CanonicalEventsComplete &&
		canonicalLoadComplete &&
		len(outcomes) < maxExperienceEvaluationOutcomes
	coverage := evaluate.Coverage{
		TranscriptComplete:          transcriptComplete,
		CanonicalEventsComplete:     canonicalComplete,
		WorkspaceStateCaptured:      false,
		OutcomeObservationsComplete: outcomesComplete,
	}
	addCoverageEvidence := func(
		requirement experience.CoverageRequirement,
		complete bool,
		refs []experience.EvidenceRef,
	) {
		if !complete || len(refs) == 0 {
			return
		}
		coverage.Evidence = append(
			coverage.Evidence,
			evaluate.CoverageEvidence{
				Requirement: requirement,
				Evidence: boundedEvidenceRefs(
					refs,
					evaluate.MaxEvidencePerFact,
				),
			},
		)
	}
	addCoverageEvidence(
		experience.CoverageTranscriptComplete,
		transcriptComplete,
		turnAnchors,
	)
	addCoverageEvidence(
		experience.CoverageCanonicalComplete,
		canonicalComplete,
		canonicalAnchors,
	)
	addCoverageEvidence(
		experience.CoverageOutcomesComplete,
		outcomesComplete,
		outcomeAnchors,
	)
	return coverage, anchors
}

func coverageAwareConditionKinds(
	values []experience.DeterministicConditionKind,
	coverage evaluate.Coverage,
) []experience.DeterministicConditionKind {
	result := make([]experience.DeterministicConditionKind, 0, len(values))
	for _, value := range values {
		switch value {
		case experience.ConditionToolName,
			experience.ConditionCommandClass:
			if coverage.TranscriptComplete {
				result = append(result, value)
			}
		case experience.ConditionPathPattern:
			if coverage.TranscriptComplete &&
				coverage.CanonicalEventsComplete {
				result = append(result, value)
			}
		}
	}
	return result
}

type pairedToolResult struct {
	resultIndex int
	hasResult   bool
	ambiguous   bool
}

func evaluationFacts(
	turns []transcript.Turn,
	outcomes []trajectory.Outcome,
	config issueintel.ProjectConfig,
) (evaluate.Facts, []string, []experience.DeterministicConditionKind, string, error) {
	var facts evaluate.Facts
	pairs := pairToolResults(turns)
	repositoryPathSet := make(map[string]bool)
	modelSet := make(map[string]bool)
	for index, turn := range turns {
		if model := strings.TrimSpace(turn.Model); model != "" {
			modelSet[model] = true
		}
		switch turn.Role {
		case transcript.RoleToolCall:
			callEvidence := []experience.EvidenceRef{turnEvidenceRef(turn)}
			pair := pairs[index]
			if pair.hasResult {
				callEvidence = append(
					callEvidence,
					turnEvidenceRef(turns[pair.resultIndex]),
				)
			}
			callEvidence = boundedEvidenceRefs(
				callEvidence,
				evaluate.MaxEvidencePerFact,
			)
			if toolName := strings.TrimSpace(turn.ToolName); toolName != "" {
				facts.Conditions = append(
					facts.Conditions,
					evaluate.ConditionFact{
						FactMeta: evaluate.FactMeta{
							Order:     turn.TurnIndex,
							Ambiguous: pair.ambiguous,
							Evidence:  callEvidence,
						},
						Kind:   experience.ConditionToolName,
						Values: []string{toolName},
					},
				)
			}
			class, signature, raw, command := transcriptissues.RetainedCommandInfo(
				turn,
				config,
			)
			if command {
				result := evaluate.FactResultUnknown
				if pair.hasResult && !pair.ambiguous {
					result = exactResultState(turns[pair.resultIndex])
				}
				facts.Commands = append(
					facts.Commands,
					evaluate.CommandFact{
						FactMeta: evaluate.FactMeta{
							Order:     turn.TurnIndex,
							Ambiguous: pair.ambiguous,
							Evidence:  callEvidence,
						},
						Signature: signature,
						Class:     class,
						Result:    result,
					},
				)
				if class != "" {
					facts.Conditions = append(
						facts.Conditions,
						evaluate.ConditionFact{
							FactMeta: evaluate.FactMeta{
								Order:     turn.TurnIndex,
								Ambiguous: pair.ambiguous,
								Evidence:  callEvidence,
							},
							Kind:   experience.ConditionCommandClass,
							Values: []string{class},
						},
					)
				}
				if verificationClass, recognized :=
					transcriptissues.ClassifyVerificationCommand(
						raw,
						config,
					); recognized {
					facts.Verifications = append(
						facts.Verifications,
						evaluate.VerificationFact{
							FactMeta: evaluate.FactMeta{
								Order:     turn.TurnIndex,
								Ambiguous: pair.ambiguous,
								Evidence:  callEvidence,
							},
							CommandClass: verificationClass,
							Result:       result,
						},
					)
				}
				if pair.hasResult && !pair.ambiguous {
					resultTurn := turns[pair.resultIndex]
					signature := transcriptissues.NormalizedErrorSignature(
						resultTurn,
					)
					if signature != "" &&
						transcriptissues.ToolResultFailed(resultTurn) &&
						class != "" {
						facts.Failures = append(
							facts.Failures,
							evaluate.FailureFact{
								FactMeta: evaluate.FactMeta{
									Order:    turn.TurnIndex,
									Evidence: callEvidence,
								},
								CommandClass:      class,
								NormalizedPattern: signature,
							},
						)
					}
				}
			}
			for _, path := range transcriptissues.ExtractEditedFiles(turn) {
				repositoryPathSet[path] = true
				facts.FileChanges = append(
					facts.FileChanges,
					evaluate.FileChangeFact{
						FactMeta: evaluate.FactMeta{
							Order: turn.TurnIndex,
							Evidence: []experience.EvidenceRef{
								turnEvidenceRef(turn),
							},
						},
						Path: path,
					},
				)
				facts.Conditions = append(
					facts.Conditions,
					evaluate.ConditionFact{
						FactMeta: evaluate.FactMeta{
							Order: turn.TurnIndex,
							Evidence: []experience.EvidenceRef{
								turnEvidenceRef(turn),
							},
						},
						Kind:   experience.ConditionPathPattern,
						Values: []string{path},
					},
				)
			}
		case transcript.RoleUser:
			marker := transcriptissues.HighConfidenceCorrectionMarker(
				turn.Payload.Text,
			)
			if marker != "" {
				facts.Corrections = append(
					facts.Corrections,
					evaluate.CorrectionFact{
						FactMeta: evaluate.FactMeta{
							Order: turn.TurnIndex,
							Evidence: []experience.EvidenceRef{
								turnEvidenceRef(turn),
							},
						},
						MarkerFamily: marker,
					},
				)
			}
		}
	}
	repositoryPaths := sortedSetValues(repositoryPathSet)
	models := sortedSetValues(modelSet)
	model := ""
	if len(models) == 1 {
		model = models[0]
	}
	completeKinds := []experience.DeterministicConditionKind{
		experience.ConditionToolName,
		experience.ConditionCommandClass,
		experience.ConditionPathPattern,
	}
	return facts, repositoryPaths, completeKinds, model, nil
}

func pairToolResults(turns []transcript.Turn) map[int]pairedToolResult {
	calls := make(map[string][]int)
	results := make(map[string][]int)
	for index, turn := range turns {
		callID := strings.TrimSpace(turn.Payload.ToolCallID)
		if callID == "" {
			continue
		}
		switch turn.Role {
		case transcript.RoleToolCall:
			calls[callID] = append(calls[callID], index)
		case transcript.RoleToolResult:
			if !turn.Payload.SupplementalEvidence {
				results[callID] = append(results[callID], index)
			}
		}
	}
	pairs := make(map[int]pairedToolResult)
	for index, call := range turns {
		if call.Role != transcript.RoleToolCall {
			continue
		}
		callID := strings.TrimSpace(call.Payload.ToolCallID)
		if callID == "" || len(calls[callID]) != 1 {
			pairs[index] = pairedToolResult{ambiguous: true}
			continue
		}
		candidates := make([]int, 0, len(results[callID]))
		for _, resultIndex := range results[callID] {
			if resultIndex <= index ||
				!sameToolParent(call, turns[resultIndex]) {
				continue
			}
			candidates = append(candidates, resultIndex)
		}
		switch len(candidates) {
		case 0:
			pairs[index] = pairedToolResult{}
		case 1:
			pairs[index] = pairedToolResult{
				resultIndex: candidates[0],
				hasResult:   true,
			}
		default:
			pairs[index] = pairedToolResult{ambiguous: true}
		}
	}
	return pairs
}

func sameToolParent(call, result transcript.Turn) bool {
	callParent := strings.TrimSpace(call.Payload.ParentToolUseID)
	resultParent := strings.TrimSpace(result.Payload.ParentToolUseID)
	return callParent == resultParent ||
		callParent == "" ||
		resultParent == ""
}

func exactResultState(turn transcript.Turn) evaluate.FactResult {
	if turn.Payload.ExitCode == nil {
		return evaluate.FactResultUnknown
	}
	if *turn.Payload.ExitCode == 0 {
		return evaluate.FactResultSucceeded
	}
	return evaluate.FactResultFailed
}

func sortedSetValues(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func normalizedEvaluationVerifier(
	verifier experience.Verifier,
) evaluate.NormalizedVerifier {
	if verifier.Kind != experience.VerifierCommandObserved &&
		verifier.Kind != experience.VerifierCommandSucceeded ||
		verifier.Command == nil ||
		verifier.Command.ScrubbingVersion != commandScrubbingVersionV1 {
		return evaluate.NormalizedVerifier{}
	}
	turn := transcript.Turn{
		Role: transcript.RoleToolCall,
		Payload: transcript.Payload{
			RawCommand: verifier.Command.Command,
		},
	}
	signature, ok := transcriptissues.NormalizedCommandSignature(turn)
	if !ok {
		return evaluate.NormalizedVerifier{}
	}
	return evaluate.NormalizedVerifier{
		DeclaredCommand:         verifier.Command.Command,
		CommandSignature:        signature,
		CommandScrubbingVersion: commandScrubbingVersionV1,
	}
}

func persistedExperienceEvaluation(
	application experience.Application,
	result evaluate.Result,
	anchors []experience.EvidenceRef,
	evaluatedAt time.Time,
	previous *experience.Evaluation,
) (experience.Evaluation, error) {
	coverage := make([]experience.CoverageRequirement, 0, len(result.Coverage))
	for _, item := range result.Coverage {
		if item.Complete {
			coverage = append(coverage, item.Requirement)
		}
	}
	evidence := make([]experience.EvidenceRef, 0, len(result.Evidence))
	for _, cited := range result.Evidence {
		evidence = append(evidence, cited.Ref)
	}
	evidence = boundedEvidenceRefs(evidence, evaluate.MaxResultEvidence)
	if len(evidence) == 0 {
		evidence = boundedEvidenceRefs(anchors, evaluate.MaxEvidencePerFact)
	}
	if len(evidence) == 0 {
		return experience.Evaluation{}, ErrExperienceEvaluationNoEvidence
	}
	value := experience.Evaluation{
		SchemaVersion:      experience.EvaluationSchemaVersion,
		ApplicationID:      application.ApplicationID,
		Experience:         application.Experience,
		EvaluatedAt:        evaluatedAt.UTC(),
		OpportunityState:   result.OpportunityState,
		ApplicabilityState: result.ApplicabilityState,
		VerifierState:      result.VerifierState,
		TaskOutcomeState:   result.TaskOutcomeState,
		Coverage:           coverage,
		SourceEvidence:     evidence,
		DerivationVersion:  result.DerivationVersion,
	}
	if previous != nil {
		if previous.ApplicationID != application.ApplicationID ||
			previous.Experience != application.Experience {
			return experience.Evaluation{}, errors.New(
				"prior evaluation does not match application",
			)
		}
		if sameEvaluationMaterial(*previous, value) {
			if err := previous.Validate(); err != nil {
				return experience.Evaluation{}, errors.New(
					"prior evaluation is invalid",
				)
			}
			return *previous, nil
		}
		if !value.EvaluatedAt.After(previous.EvaluatedAt) {
			value.EvaluatedAt = previous.EvaluatedAt.Add(time.Nanosecond)
		}
	}
	value.EvaluationID = value.DeterministicID()
	if err := value.Validate(); err != nil {
		return experience.Evaluation{}, fmt.Errorf(
			"build persisted deterministic evaluation: %w",
			err,
		)
	}
	return value, nil
}

func sameEvaluationMaterial(
	left, right experience.Evaluation,
) bool {
	left.EvaluationID = ""
	left.EvaluatedAt = time.Time{}
	right.EvaluationID = ""
	right.EvaluatedAt = time.Time{}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil &&
		rightErr == nil &&
		bytes.Equal(leftJSON, rightJSON)
}

func evaluationHarness(agent string) experience.Harness {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "claude", "claude-code":
		return experience.HarnessClaude
	case "codex":
		return experience.HarnessCodex
	case "cursor", "cursor-agent":
		return experience.HarnessCursor
	case "antigravity":
		return experience.HarnessAntigravity
	default:
		return ""
	}
}

func sortTranscriptTurns(turns []transcript.Turn) {
	sort.Slice(turns, func(left, right int) bool {
		if turns[left].TurnIndex != turns[right].TurnIndex {
			return turns[left].TurnIndex < turns[right].TurnIndex
		}
		if !turns[left].OccurredAt.Equal(turns[right].OccurredAt) {
			return turns[left].OccurredAt.Before(turns[right].OccurredAt)
		}
		return turns[left].TurnID < turns[right].TurnID
	})
}

func transcriptCoverageAnchors(
	turns []transcript.Turn,
) []experience.EvidenceRef {
	if len(turns) == 0 {
		return nil
	}
	result := []experience.EvidenceRef{turnEvidenceRef(turns[0])}
	if turns[len(turns)-1].TurnID != turns[0].TurnID {
		result = append(result, turnEvidenceRef(turns[len(turns)-1]))
	}
	return result
}

func outcomeCoverageAnchors(
	outcomes []trajectory.Outcome,
) []experience.EvidenceRef {
	if len(outcomes) == 0 {
		return nil
	}
	result := []experience.EvidenceRef{outcomeEvidenceRef(outcomes[0])}
	if outcomes[len(outcomes)-1].OutcomeID != outcomes[0].OutcomeID {
		result = append(
			result,
			outcomeEvidenceRef(outcomes[len(outcomes)-1]),
		)
	}
	return result
}

func canonicalCoverageAnchors(
	events []model.Event,
) []experience.EvidenceRef {
	if len(events) == 0 {
		return nil
	}
	result := []experience.EvidenceRef{canonicalEventEvidenceRef(events[0])}
	if events[len(events)-1].EventID != events[0].EventID {
		result = append(
			result,
			canonicalEventEvidenceRef(events[len(events)-1]),
		)
	}
	return result
}

func turnEvidenceRef(turn transcript.Turn) experience.EvidenceRef {
	index := turn.TurnIndex
	occurredAt := turn.OccurredAt.UTC()
	return experience.EvidenceRef{
		Kind:       experience.EvidenceTranscriptTurn,
		SessionKey: turn.SessionKey,
		TurnIndex:  &index,
		OccurredAt: &occurredAt,
		Excerpt:    evaluationTurnExcerpt(turn),
	}
}

func outcomeEvidenceRef(outcome trajectory.Outcome) experience.EvidenceRef {
	occurredAt := outcome.OccurredAt.UTC()
	return experience.EvidenceRef{
		Kind:       experience.EvidenceOutcomeObservation,
		OutcomeID:  outcome.OutcomeID,
		OccurredAt: &occurredAt,
	}
}

func canonicalEventEvidenceRef(event model.Event) experience.EvidenceRef {
	occurredAt := event.OccurredAt.UTC()
	return experience.EvidenceRef{
		Kind:       experience.EvidenceCanonicalEvent,
		EventID:    event.EventID,
		OccurredAt: &occurredAt,
	}
}

func evaluationTurnExcerpt(turn transcript.Turn) string {
	value := strings.TrimSpace(turn.Payload.Text)
	if value == "" {
		value = strings.TrimSpace(turn.Payload.RawCommand)
	}
	if value == "" {
		value = strings.TrimSpace(turn.Payload.ToolResult)
	}
	if value == "" && len(turn.Payload.ToolInput) > 0 {
		var compact bytes.Buffer
		if json.Compact(&compact, turn.Payload.ToolInput) == nil {
			value = compact.String()
		} else {
			value = string(turn.Payload.ToolInput)
		}
	}
	return truncateUTF8Bytes(strings.TrimSpace(value), maxEvaluationExcerptBytes)
}

func truncateUTF8Bytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func boundedEvidenceRefs(
	values []experience.EvidenceRef,
	limit int,
) []experience.EvidenceRef {
	if limit <= 0 {
		return nil
	}
	byKey := make(map[string]experience.EvidenceRef, len(values))
	keys := make([]string, 0, len(values))
	for _, value := range values {
		if err := value.Validate(); err != nil {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		key := string(encoded)
		if _, exists := byKey[key]; exists {
			continue
		}
		byKey[key] = value
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]experience.EvidenceRef, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}

func evaluationFactCount(facts evaluate.Facts) int {
	return len(facts.Opportunities) +
		len(facts.Conditions) +
		len(facts.Commands) +
		len(facts.FileChanges) +
		len(facts.Verifications) +
		len(facts.Failures) +
		len(facts.Corrections) +
		len(facts.TaskOutcomes)
}

func experienceEvaluationDiagnostic(err error) string {
	switch {
	case errors.Is(err, ErrExperienceEvaluationNoEvidence):
		return "no_real_evidence"
	case errors.Is(err, ErrExperienceEvaluationFactsExceeded):
		return "fact_bound_exceeded"
	case errors.Is(err, ErrExperienceEvaluationUnsupportedHarness):
		return "unsupported_harness"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "deterministic_evaluation_deferred"
	}
}
