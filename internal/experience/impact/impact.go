// Package impact derives local, observational experience-impact measurements.
//
// It is deliberately pure: no persistence, filesystem, process, network,
// HTTP, MCP, or model dependencies.
package impact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/experience"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	SchemaVersion     = "belay.experience-impact.v1"
	DerivationVersion = "belay.experience-impact-derive.v1"

	ComparisonMatched              = "matched"
	ComparisonInsufficientBaseline = "insufficient_baseline"

	MatchProject          = "project"
	MatchHarness          = "harness"
	MatchTaskFamily       = "task_family"
	MatchIssueFingerprint = "issue_fingerprint"

	VerificationObserved      = "observed"
	VerificationNotObserved   = "not_observed"
	VerificationNotApplicable = "not_applicable"

	minComparableSessions = 2
	maxComparableSessions = 5
)

type SessionInput struct {
	Session                 transcript.Session
	Turns                   []transcript.Turn
	WindowStartTurn         int64
	TaskFamily              string
	IssueFingerprint        string
	TaskOutcomeState        experience.TaskOutcomeState
	OutcomeCoverageComplete bool
}

type Input struct {
	Application   experience.Application
	Evaluation    *experience.Evaluation
	Current       SessionInput
	Prior         []SessionInput
	ProjectConfig issueintel.ProjectConfig
}

type EvidenceRange struct {
	SessionKey string `json:"session_key"`
	StartTurn  int64  `json:"start_turn"`
	EndTurn    int64  `json:"end_turn"`
}

type Metrics struct {
	Turns                         int                         `json:"turns"`
	ExplicitCorrections           int                         `json:"explicit_corrections"`
	FailedToolResults             int                         `json:"failed_tool_results"`
	Tokens                        int64                       `json:"tokens"`
	TokensLowerBound              bool                        `json:"tokens_lower_bound"`
	CostUSD                       *float64                    `json:"cost_usd,omitempty"`
	CostLowerBound                bool                        `json:"cost_lower_bound"`
	WallDurationMS                int64                       `json:"wall_duration_ms"`
	TimeToFirstActionMS           *int64                      `json:"time_to_first_action_ms,omitempty"`
	VerificationAfterFinalEdit    string                      `json:"verification_after_final_edit"`
	CompletionWithoutVerification bool                        `json:"completion_without_verification"`
	TaskOutcomeState              experience.TaskOutcomeState `json:"task_outcome_state"`
}

type BaselineMetrics struct {
	ExplicitCorrections *float64 `json:"explicit_corrections,omitempty"`
	FailedToolResults   *float64 `json:"failed_tool_results,omitempty"`
	Tokens              *float64 `json:"tokens,omitempty"`
	CostUSD             *float64 `json:"cost_usd,omitempty"`
	WallDurationMS      *float64 `json:"wall_duration_ms,omitempty"`
	TimeToFirstActionMS *float64 `json:"time_to_first_action_ms,omitempty"`
}

type MetricDeltas struct {
	ExplicitCorrections *float64 `json:"explicit_corrections,omitempty"`
	FailedToolResults   *float64 `json:"failed_tool_results,omitempty"`
	TokensPercent       *float64 `json:"tokens_percent,omitempty"`
	CostPercent         *float64 `json:"cost_percent,omitempty"`
	WallDurationPercent *float64 `json:"wall_duration_percent,omitempty"`
	FirstActionPercent  *float64 `json:"first_action_percent,omitempty"`
}

type Comparison struct {
	State              string          `json:"state"`
	ComparableSessions int             `json:"comparable_sessions"`
	SessionKeys        []string        `json:"session_keys"`
	MatchBasis         []string        `json:"match_basis"`
	BaselineMedian     BaselineMetrics `json:"baseline_median"`
	Delta              MetricDeltas    `json:"delta"`
}

type Coverage struct {
	CurrentTranscript transcript.SessionCoverage `json:"current_transcript"`
	PriorComplete     int                        `json:"prior_complete"`
	TokensLowerBound  bool                       `json:"tokens_lower_bound"`
	CostLowerBound    bool                       `json:"cost_lower_bound"`
	OutcomeComplete   bool                       `json:"outcome_complete"`
}

type Observation struct {
	SchemaVersion     string                   `json:"schema_version"`
	DerivationVersion string                   `json:"derivation_version"`
	ObservationID     string                   `json:"observation_id"`
	InputHash         string                   `json:"input_hash"`
	ApplicationID     string                   `json:"application_id"`
	EvaluationID      string                   `json:"evaluation_id,omitempty"`
	Experience        experience.ExperienceRef `json:"experience"`
	ProjectIdentity   string                   `json:"project_identity"`
	SessionKey        string                   `json:"session_key"`
	Evidence          EvidenceRange            `json:"evidence"`
	Current           Metrics                  `json:"current"`
	Comparison        Comparison               `json:"comparison"`
	Coverage          Coverage                 `json:"coverage"`
}

func Derive(input Input) (Observation, error) {
	if err := validateInput(input); err != nil {
		return Observation{}, err
	}
	currentTurns, evidence, err := boundedTurns(input.Current)
	if err != nil {
		return Observation{}, err
	}
	current := deriveMetrics(
		currentTurns,
		input.ProjectConfig,
		input.Current.TaskOutcomeState,
	)
	comparables := comparableSessions(input)
	comparison := compare(
		current,
		comparables,
		input.ProjectConfig,
		input.Current,
	)
	normalized := input
	normalized.Prior = comparables
	inputHash, err := stableInputHash(normalized)
	if err != nil {
		return Observation{}, err
	}
	observationID := deterministicObservationID(
		input.Application.ApplicationID,
		inputHash,
		DerivationVersion,
	)
	return Observation{
		SchemaVersion:     SchemaVersion,
		DerivationVersion: DerivationVersion,
		ObservationID:     observationID,
		InputHash:         inputHash,
		ApplicationID:     input.Application.ApplicationID,
		EvaluationID:      evaluationID(input.Evaluation),
		Experience:        input.Application.Experience,
		ProjectIdentity:   input.Application.ProjectIdentity,
		SessionKey:        input.Application.SessionKey,
		Evidence:          evidence,
		Current:           current,
		Comparison:        comparison,
		Coverage: Coverage{
			CurrentTranscript: input.Current.Session.Coverage,
			PriorComplete:     len(comparables),
			TokensLowerBound:  current.TokensLowerBound,
			CostLowerBound:    current.CostLowerBound,
			OutcomeComplete:   input.Current.OutcomeCoverageComplete,
		},
	}, nil
}

func deterministicObservationID(
	applicationID string,
	inputHash string,
	derivationVersion string,
) string {
	sum := sha256.Sum256([]byte(
		strings.TrimSpace(applicationID) + "\x00" +
			strings.TrimSpace(inputHash) + "\x00" +
			strings.TrimSpace(derivationVersion),
	))
	return "xim_" + hex.EncodeToString(sum[:])
}

func (value Observation) Validate() error {
	if value.SchemaVersion != SchemaVersion {
		return errors.New("impact observation schema version is invalid")
	}
	if value.DerivationVersion == "" ||
		value.ApplicationID == "" ||
		value.ProjectIdentity == "" ||
		value.SessionKey == "" {
		return errors.New("impact observation identity is incomplete")
	}
	if err := value.Experience.Validate(); err != nil {
		return errors.New("impact observation experience is invalid")
	}
	if !validSHA256(value.InputHash) {
		return errors.New("impact observation input hash is invalid")
	}
	if value.ObservationID != deterministicObservationID(
		value.ApplicationID,
		value.InputHash,
		value.DerivationVersion,
	) {
		return errors.New("impact observation ID is invalid")
	}
	if value.Evidence.SessionKey != value.SessionKey ||
		value.Evidence.StartTurn < 0 ||
		value.Evidence.EndTurn < value.Evidence.StartTurn {
		return errors.New("impact observation evidence range is invalid")
	}
	if value.Comparison.ComparableSessions != len(value.Comparison.SessionKeys) {
		return errors.New("impact comparison session count is invalid")
	}
	if !validMatchBasis(value.Comparison.MatchBasis) {
		return errors.New("impact comparison match basis is invalid")
	}
	switch value.Comparison.State {
	case ComparisonMatched:
		if value.Comparison.ComparableSessions < minComparableSessions {
			return errors.New("matched impact comparison lacks baseline")
		}
	case ComparisonInsufficientBaseline:
		if value.Comparison.ComparableSessions >= minComparableSessions {
			return errors.New("impact comparison incorrectly lacks baseline")
		}
	default:
		return errors.New("impact comparison state is invalid")
	}
	if value.Coverage.CurrentTranscript != transcript.CoverageComplete &&
		value.Coverage.CurrentTranscript != transcript.CoveragePartial {
		return errors.New("impact observation transcript coverage is invalid")
	}
	return nil
}

func validMatchBasis(values []string) bool {
	if len(values) < 2 ||
		values[0] != MatchProject ||
		values[1] != MatchHarness {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return false
		}
		seen[value] = true
		switch value {
		case MatchProject, MatchHarness, MatchTaskFamily,
			MatchIssueFingerprint:
		default:
			return false
		}
	}
	return true
}

func evaluationID(value *experience.Evaluation) string {
	if value == nil {
		return ""
	}
	return value.EvaluationID
}

func validSHA256(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil && strings.ToLower(value) == value
}

func validateInput(input Input) error {
	if err := input.Application.Validate(); err != nil {
		return errors.New("impact application is invalid")
	}
	if input.Application.DeliveryState != experience.DeliveryDelivered {
		return errors.New("impact application is not delivered")
	}
	if input.Application.SessionKey != input.Current.Session.SessionKey ||
		input.Application.ProjectIdentity != input.Current.Session.ProjectIdentity {
		return errors.New("impact application and current session do not match")
	}
	if input.Current.Session.Coverage == transcript.CoverageLive {
		return errors.New("impact current session is still live")
	}
	if !input.Current.TaskOutcomeState.Valid() {
		return errors.New("impact current task outcome is invalid")
	}
	if err := validateTurns(input.Current); err != nil {
		return err
	}
	if input.Evaluation != nil {
		if err := input.Evaluation.Validate(); err != nil {
			return errors.New("impact evaluation is invalid")
		}
		if input.Evaluation.ApplicationID !=
			input.Application.ApplicationID ||
			input.Evaluation.Experience != input.Application.Experience {
			return errors.New(
				"impact evaluation and application do not match",
			)
		}
	}
	for _, prior := range input.Prior {
		if !prior.TaskOutcomeState.Valid() {
			return errors.New("impact prior task outcome is invalid")
		}
		if err := validateTurns(prior); err != nil {
			return err
		}
	}
	return nil
}

func validateTurns(input SessionInput) error {
	previous := int64(-1)
	for _, turn := range input.Turns {
		if turn.SessionKey != input.Session.SessionKey {
			return errors.New("impact turn session does not match")
		}
		if turn.TurnIndex < previous {
			return errors.New("impact turns are not ordered")
		}
		previous = turn.TurnIndex
	}
	return nil
}

func boundedTurns(input SessionInput) ([]transcript.Turn, EvidenceRange, error) {
	start := sort.Search(len(input.Turns), func(index int) bool {
		return input.Turns[index].TurnIndex >= input.WindowStartTurn
	})
	if start >= len(input.Turns) {
		return nil, EvidenceRange{}, errors.New(
			"impact window has no retained turns",
		)
	}
	turns := input.Turns[start:]
	return turns, EvidenceRange{
		SessionKey: input.Session.SessionKey,
		StartTurn:  turns[0].TurnIndex,
		EndTurn:    turns[len(turns)-1].TurnIndex,
	}, nil
}

func comparableSessions(input Input) []SessionInput {
	result := make([]SessionInput, 0, len(input.Prior))
	for _, candidate := range input.Prior {
		if candidate.Session.SessionKey == input.Current.Session.SessionKey ||
			candidate.Session.ProjectIdentity != input.Current.Session.ProjectIdentity ||
			candidate.Session.Agent != input.Current.Session.Agent ||
			candidate.Session.Coverage != transcript.CoverageComplete ||
			candidate.Session.EndedAt.IsZero() ||
			!candidate.Session.EndedAt.Before(input.Current.Session.StartedAt) {
			continue
		}
		if NormalizeTaskFamily(input.Current.TaskFamily) != "" &&
			NormalizeTaskFamily(candidate.TaskFamily) !=
				NormalizeTaskFamily(input.Current.TaskFamily) {
			continue
		}
		if NormalizeIssueFingerprint(input.Current.IssueFingerprint) != "" &&
			NormalizeIssueFingerprint(candidate.IssueFingerprint) !=
				NormalizeIssueFingerprint(input.Current.IssueFingerprint) {
			continue
		}
		if _, _, err := boundedTurns(candidate); err != nil {
			continue
		}
		result = append(result, candidate)
	}
	sort.Slice(result, func(left, right int) bool {
		leftEnded := result[left].Session.EndedAt
		rightEnded := result[right].Session.EndedAt
		if !leftEnded.Equal(rightEnded) {
			return leftEnded.After(rightEnded)
		}
		return result[left].Session.SessionKey < result[right].Session.SessionKey
	})
	if len(result) > maxComparableSessions {
		result = result[:maxComparableSessions]
	}
	return result
}

func deriveMetrics(
	turns []transcript.Turn,
	config issueintel.ProjectConfig,
	outcome experience.TaskOutcomeState,
) Metrics {
	result := Metrics{
		VerificationAfterFinalEdit: VerificationNotApplicable,
		TaskOutcomeState:           outcome,
	}
	var cost float64
	costKnown := false
	firstAt := turns[0].OccurredAt
	lastAt := turns[len(turns)-1].OccurredAt
	firstActionIndex := -1
	lastEditIndex := -1
	verificationAfterEdit := false
	completionAfterEdit := false
	for index, turn := range turns {
		if turn.Role == transcript.RoleUser &&
			transcriptissues.HighConfidenceCorrectionMarker(
				turn.Payload.Text,
			) != "" {
			result.ExplicitCorrections++
		}
		if turn.Role == transcript.RoleToolResult &&
			transcriptissues.ToolResultFailed(turn) {
			result.FailedToolResults++
		}
		turnTokens := int64(0)
		if turn.InputTokens != nil {
			turnTokens += *turn.InputTokens
		}
		if turn.OutputTokens != nil {
			turnTokens += *turn.OutputTokens
		}
		result.Tokens += turnTokens
		if (turn.InputTokens == nil) != (turn.OutputTokens == nil) {
			result.TokensLowerBound = true
		}
		if turn.CostUSD != nil {
			cost += *turn.CostUSD
			costKnown = true
		} else if turnTokens > 0 {
			result.CostLowerBound = true
		}
		if firstActionIndex < 0 && isProductiveAction(turn, config) {
			firstActionIndex = index
		}
		if len(transcriptissues.ExtractEditedFiles(turn)) > 0 {
			lastEditIndex = index
			verificationAfterEdit = false
			completionAfterEdit = false
		}
		if lastEditIndex >= 0 && index > lastEditIndex {
			if transcriptissues.IsCompletionClaim(turn) {
				completionAfterEdit = true
			}
			if isVerificationAction(turn, config) {
				verificationAfterEdit = true
			}
		}
	}
	result.Turns = len(turns)
	if costKnown {
		result.CostUSD = &cost
	}
	if !firstAt.IsZero() && lastAt.After(firstAt) {
		result.WallDurationMS = lastAt.Sub(firstAt).Milliseconds()
	}
	if firstActionIndex >= 0 &&
		!firstAt.IsZero() &&
		!turns[firstActionIndex].OccurredAt.Before(firstAt) {
		value := turns[firstActionIndex].OccurredAt.Sub(firstAt).Milliseconds()
		result.TimeToFirstActionMS = &value
	}
	if lastEditIndex >= 0 {
		result.VerificationAfterFinalEdit = VerificationNotObserved
		if verificationAfterEdit {
			result.VerificationAfterFinalEdit = VerificationObserved
		}
		result.CompletionWithoutVerification =
			completionAfterEdit && !verificationAfterEdit
	}
	return result
}

func isProductiveAction(
	turn transcript.Turn,
	config issueintel.ProjectConfig,
) bool {
	if len(transcriptissues.ExtractEditedFiles(turn)) > 0 {
		return true
	}
	_, _, _, ok := transcriptissues.RetainedCommandInfo(
		turn,
		config,
	)
	return ok
}

func isVerificationAction(
	turn transcript.Turn,
	config issueintel.ProjectConfig,
) bool {
	_, _, raw, ok := transcriptissues.RetainedCommandInfo(turn, config)
	if !ok {
		return false
	}
	_, ok = transcriptissues.ClassifyVerificationCommand(raw, config)
	return ok
}

func compare(
	current Metrics,
	inputs []SessionInput,
	config issueintel.ProjectConfig,
	currentInput SessionInput,
) Comparison {
	result := Comparison{
		State:              ComparisonInsufficientBaseline,
		ComparableSessions: len(inputs),
		SessionKeys:        make([]string, 0, len(inputs)),
		MatchBasis:         comparisonMatchBasis(currentInput),
	}
	if len(inputs) < minComparableSessions {
		for _, input := range inputs {
			result.SessionKeys = append(
				result.SessionKeys,
				input.Session.SessionKey,
			)
		}
		return result
	}
	metrics := make([]Metrics, 0, len(inputs))
	for _, input := range inputs {
		turns, _, _ := boundedTurns(input)
		metrics = append(
			metrics,
			deriveMetrics(turns, config, input.TaskOutcomeState),
		)
		result.SessionKeys = append(result.SessionKeys, input.Session.SessionKey)
	}
	result.State = ComparisonMatched
	result.BaselineMedian = baselineMedian(metrics)
	result.Delta = deltas(current, result.BaselineMedian)
	return result
}

func comparisonMatchBasis(input SessionInput) []string {
	result := []string{MatchProject, MatchHarness}
	if NormalizeTaskFamily(input.TaskFamily) != "" {
		result = append(result, MatchTaskFamily)
	}
	if NormalizeIssueFingerprint(input.IssueFingerprint) != "" {
		result = append(result, MatchIssueFingerprint)
	}
	return result
}

func baselineMedian(values []Metrics) BaselineMetrics {
	corrections := make([]float64, 0, len(values))
	failures := make([]float64, 0, len(values))
	tokens := make([]float64, 0, len(values))
	costs := make([]float64, 0, len(values))
	wall := make([]float64, 0, len(values))
	firstAction := make([]float64, 0, len(values))
	for _, value := range values {
		corrections = append(corrections, float64(value.ExplicitCorrections))
		failures = append(failures, float64(value.FailedToolResults))
		if !value.TokensLowerBound {
			tokens = append(tokens, float64(value.Tokens))
		}
		wall = append(wall, float64(value.WallDurationMS))
		if value.CostUSD != nil && !value.CostLowerBound {
			costs = append(costs, *value.CostUSD)
		}
		if value.TimeToFirstActionMS != nil {
			firstAction = append(
				firstAction,
				float64(*value.TimeToFirstActionMS),
			)
		}
	}
	return BaselineMetrics{
		ExplicitCorrections: median(corrections),
		FailedToolResults:   median(failures),
		Tokens:              median(tokens),
		CostUSD:             median(costs),
		WallDurationMS:      median(wall),
		TimeToFirstActionMS: median(firstAction),
	}
}

func deltas(current Metrics, baseline BaselineMetrics) MetricDeltas {
	result := MetricDeltas{}
	if baseline.ExplicitCorrections != nil {
		result.ExplicitCorrections = floatPointer(
			float64(current.ExplicitCorrections) -
				*baseline.ExplicitCorrections,
		)
	}
	if baseline.FailedToolResults != nil {
		result.FailedToolResults = floatPointer(
			float64(current.FailedToolResults) -
				*baseline.FailedToolResults,
		)
	}
	if !current.TokensLowerBound {
		result.TokensPercent = percentDelta(
			float64(current.Tokens),
			baseline.Tokens,
		)
	}
	if current.CostUSD != nil && !current.CostLowerBound {
		result.CostPercent = percentDelta(*current.CostUSD, baseline.CostUSD)
	}
	result.WallDurationPercent = percentDelta(
		float64(current.WallDurationMS),
		baseline.WallDurationMS,
	)
	if current.TimeToFirstActionMS != nil {
		result.FirstActionPercent = percentDelta(
			float64(*current.TimeToFirstActionMS),
			baseline.TimeToFirstActionMS,
		)
	}
	return result
}

func median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	values = append([]float64(nil), values...)
	sort.Float64s(values)
	middle := len(values) / 2
	value := values[middle]
	if len(values)%2 == 0 {
		value = (values[middle-1] + values[middle]) / 2
	}
	return &value
}

func percentDelta(current float64, baseline *float64) *float64 {
	if baseline == nil || *baseline == 0 {
		return nil
	}
	return floatPointer((current - *baseline) / *baseline * 100)
}

func floatPointer(value float64) *float64 {
	return &value
}

func stableInputHash(input Input) (string, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return "", errors.New("encode impact input")
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func NormalizeTaskFamily(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func NormalizeIssueFingerprint(value string) string {
	return strings.TrimSpace(value)
}
