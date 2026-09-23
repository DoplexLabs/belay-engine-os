// Package evidenceepisode defines replay-stable, model-independent evidence
// episodes. Episodes reference retained evidence; they do not copy transcript
// content into their contract.
package evidenceepisode

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	SchemaVersion     = "belay.evidence-episode.v1"
	DerivationVersion = "belay.evidence-episode.det.v1"

	KindFailureRepair        = "failure_repair"
	KindMutationVerification = "mutation_verification"

	CoverageComplete    = "complete"
	CoveragePartial     = "partial"
	CoverageUnavailable = "unavailable"
	CoverageUnknown     = "unknown"

	maxActiveGap = 30 * time.Minute
)

var episodeIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type Coverage struct {
	Transcript      string   `json:"transcript"`
	CanonicalEvents string   `json:"canonical_events"`
	ToolCallLinks   string   `json:"tool_call_links"`
	SessionIdentity string   `json:"session_identity"`
	Cost            string   `json:"cost"`
	Bounded         bool     `json:"bounded"`
	Diagnostics     []string `json:"diagnostics,omitempty"`
}

type Episode struct {
	SchemaVersion              string                   `json:"schema_version"`
	EpisodeID                  string                   `json:"episode_id"`
	ProjectIdentity            string                   `json:"project_identity"`
	SessionKey                 string                   `json:"session_key"`
	Kind                       string                   `json:"kind"`
	FirstTurn                  int64                    `json:"first_turn"`
	LastTurn                   int64                    `json:"last_turn"`
	StartedAt                  time.Time                `json:"started_at"`
	EndedAt                    time.Time                `json:"ended_at"`
	SourceRefs                 []trajectory.NodeRef     `json:"source_refs"`
	OutcomeRefs                []string                 `json:"outcome_refs"`
	MutationRefs               []trajectory.NodeRef     `json:"mutation_refs,omitempty"`
	VerificationRefs           []trajectory.NodeRef     `json:"verification_refs,omitempty"`
	VerifierCommand            string                   `json:"verifier_command,omitempty"`
	VerifierCommandClass       string                   `json:"verifier_command_class,omitempty"`
	FailureSignature           string                   `json:"failure_signature,omitempty"`
	RepairFamily               string                   `json:"repair_family,omitempty"`
	FailedCommandSignature     string                   `json:"failed_command_signature,omitempty"`
	SuccessfulCommandSignature string                   `json:"successful_command_signature,omitempty"`
	Cost                       issueintel.Cost          `json:"cost"`
	Coverage                   Coverage                 `json:"coverage"`
	EvidenceClass              trajectory.EvidenceClass `json:"evidence_class"`
	Confidence                 trajectory.Confidence    `json:"confidence"`
	DerivationVersion          string                   `json:"derivation_version"`
	InputHash                  string                   `json:"input_hash"`
}

type Audit struct {
	Total      int            `json:"total"`
	Complete   int            `json:"complete"`
	Incomplete int            `json:"incomplete"`
	ByKind     map[string]int `json:"by_kind"`
}

type FailureRepairInput struct {
	ProjectIdentity string
	Session         transcript.Session
	SessionTurns    []transcript.Turn
	FailureCall     transcript.Turn
	FailureResult   transcript.Turn
	SuccessCall     transcript.Turn
	SuccessResult   transcript.Turn
	OutcomeRefs     []string
}

type MutationVerificationInput struct {
	ProjectIdentity       string
	Session               transcript.Session
	SessionTurns          []transcript.Turn
	MutationRefs          []trajectory.NodeRef
	SourceRefs            []trajectory.NodeRef
	VerificationCallRef   trajectory.NodeRef
	VerificationResultRef trajectory.NodeRef
	OutcomeRefs           []string
	VerifierCommand       string
	VerifierCommandClass  string
}

func NewMutationVerification(
	input MutationVerificationInput,
) (Episode, error) {
	projectIdentity := strings.TrimSpace(input.ProjectIdentity)
	sessionKey := strings.TrimSpace(input.Session.SessionKey)
	verifierCommand := strings.TrimSpace(input.VerifierCommand)
	verifierClass := strings.TrimSpace(input.VerifierCommandClass)
	if projectIdentity == "" || sessionKey == "" ||
		input.Session.ProjectIdentity != projectIdentity ||
		verifierCommand == "" || verifierClass == "" {
		return Episode{}, errors.New(
			"mutation-verification episode requires one project session and verifier",
		)
	}
	mutationRefs := normalizedNodeRefs(input.MutationRefs)
	sourceRefs := normalizedNodeRefs(append(
		append([]trajectory.NodeRef(nil), input.SourceRefs...),
		input.VerificationCallRef,
		input.VerificationResultRef,
	))
	verificationRefs := normalizedNodeRefs([]trajectory.NodeRef{
		input.VerificationCallRef,
		input.VerificationResultRef,
	})
	if len(mutationRefs) == 0 || len(sourceRefs) == 0 ||
		len(verificationRefs) != 2 {
		return Episode{}, errors.New(
			"mutation-verification episode requires mutation and verification evidence",
		)
	}
	for _, ref := range append(
		append([]trajectory.NodeRef(nil), mutationRefs...),
		verificationRefs...,
	) {
		if ref.Kind != trajectory.NodeTranscriptTurn ||
			ref.SessionKey != sessionKey ||
			ref.TurnIndex == nil ||
			*ref.TurnIndex < 0 {
			return Episode{}, errors.New(
				"mutation-verification evidence crosses sessions",
			)
		}
	}
	turns, err := turnsForRefs(
		input.SessionTurns,
		transcriptNodeRefs(sourceRefs),
	)
	if err != nil || len(turns) == 0 {
		return Episode{}, errors.New(
			"mutation-verification evidence is missing",
		)
	}
	call, callOK := turnForRef(turns, input.VerificationCallRef)
	result, resultOK := turnForRef(turns, input.VerificationResultRef)
	resultFailed, resultKnown := transcriptissues.ExplicitToolResultFailed(result)
	if !callOK || !resultOK ||
		call.Role != transcript.RoleToolCall ||
		result.Role != transcript.RoleToolResult ||
		strings.TrimSpace(call.Payload.ToolCallID) == "" ||
		call.Payload.ToolCallID != result.Payload.ToolCallID ||
		!resultKnown || resultFailed ||
		call.TurnIndex > result.TurnIndex {
		return Episode{}, errors.New(
			"mutation-verification result is not an explicit success",
		)
	}
	for _, ref := range mutationRefs {
		turn, ok := turnForRef(turns, ref)
		if !ok || turn.Role != transcript.RoleToolCall ||
			len(transcriptissues.ExtractEditedFiles(turn)) == 0 ||
			turn.TurnIndex >= call.TurnIndex {
			return Episode{}, errors.New(
				"mutation-verification mutation evidence is invalid",
			)
		}
	}
	transcriptCoverage := string(input.Session.Coverage)
	if transcriptCoverage == "" {
		transcriptCoverage = CoverageUnknown
	}
	value := Episode{
		SchemaVersion:        SchemaVersion,
		ProjectIdentity:      projectIdentity,
		SessionKey:           sessionKey,
		Kind:                 KindMutationVerification,
		FirstTurn:            turns[0].TurnIndex,
		LastTurn:             turns[len(turns)-1].TurnIndex,
		StartedAt:            turns[0].OccurredAt.UTC(),
		EndedAt:              turns[len(turns)-1].OccurredAt.UTC(),
		SourceRefs:           sourceRefs,
		OutcomeRefs:          normalizedStrings(input.OutcomeRefs),
		MutationRefs:         mutationRefs,
		VerificationRefs:     verificationRefs,
		VerifierCommand:      verifierCommand,
		VerifierCommandClass: verifierClass,
		Coverage: Coverage{
			Transcript:      transcriptCoverage,
			CanonicalEvents: CoverageUnknown,
			ToolCallLinks:   CoverageComplete,
			SessionIdentity: CoverageUnknown,
			Cost:            CoverageUnavailable,
			Bounded:         true,
		},
		EvidenceClass:     trajectory.EvidenceDeterministicInference,
		Confidence:        trajectory.ConfidenceHigh,
		DerivationVersion: DerivationVersion,
	}
	value.InputHash = value.inputHash()
	value.EpisodeID = value.deterministicID()
	return value, value.Validate()
}

func NewFailureRepair(input FailureRepairInput) (Episode, error) {
	projectIdentity := strings.TrimSpace(input.ProjectIdentity)
	sessionKey := strings.TrimSpace(input.Session.SessionKey)
	if projectIdentity == "" || sessionKey == "" ||
		input.Session.ProjectIdentity != projectIdentity {
		return Episode{}, errors.New("failure-repair episode requires one project session")
	}
	turns := []transcript.Turn{
		input.FailureCall,
		input.FailureResult,
		input.SuccessCall,
		input.SuccessResult,
	}
	for _, turn := range turns {
		if turn.SessionKey != sessionKey {
			return Episode{}, errors.New("failure-repair evidence crosses sessions")
		}
	}
	if input.FailureCall.Role != transcript.RoleToolCall ||
		input.FailureResult.Role != transcript.RoleToolResult ||
		input.SuccessCall.Role != transcript.RoleToolCall ||
		input.SuccessResult.Role != transcript.RoleToolResult ||
		strings.TrimSpace(input.FailureCall.Payload.ToolCallID) == "" ||
		input.FailureCall.Payload.ToolCallID != input.FailureResult.Payload.ToolCallID ||
		strings.TrimSpace(input.SuccessCall.Payload.ToolCallID) == "" ||
		input.SuccessCall.Payload.ToolCallID != input.SuccessResult.Payload.ToolCallID ||
		input.FailureCall.Payload.ToolCallID == input.SuccessCall.Payload.ToolCallID ||
		input.FailureCall.TurnIndex > input.FailureResult.TurnIndex ||
		input.FailureResult.TurnIndex >= input.SuccessCall.TurnIndex ||
		input.SuccessCall.TurnIndex > input.SuccessResult.TurnIndex {
		return Episode{}, errors.New("failure-repair evidence order is invalid")
	}
	failure, failureKnown := transcriptissues.ExplicitToolResultFailed(
		input.FailureResult,
	)
	successFailed, successKnown := transcriptissues.ExplicitToolResultFailed(
		input.SuccessResult,
	)
	failedSignature, failedCommand := transcriptissues.NormalizedCommandSignature(
		input.FailureCall,
	)
	successSignature, successCommand := transcriptissues.NormalizedCommandSignature(
		input.SuccessCall,
	)
	failedFamily, failedFamilyKnown := transcriptissues.CommandRepairFamily(
		input.FailureCall,
	)
	successFamily, successFamilyKnown := transcriptissues.CommandRepairFamily(
		input.SuccessCall,
	)
	if !failureKnown || !failure || !successKnown || successFailed ||
		!failedCommand || !successCommand ||
		!failedFamilyKnown || !successFamilyKnown ||
		failedFamily != successFamily ||
		failedSignature == successSignature {
		return Episode{}, errors.New("failure-repair evidence is not qualified")
	}
	failureSignature := transcriptissues.NormalizedFirstFailureLine(
		input.FailureResult,
	)
	if failureSignature == "" {
		failureSignature = "structured command failure"
	}
	sourceRefs := make([]trajectory.NodeRef, 0, len(turns))
	for _, turn := range turns {
		index := turn.TurnIndex
		sourceRefs = append(sourceRefs, trajectory.NodeRef{
			Kind:       trajectory.NodeTranscriptTurn,
			SessionKey: sessionKey,
			TurnIndex:  &index,
		})
	}
	outcomeRefs := normalizedStrings(input.OutcomeRefs)
	spanTurns := turnsWithin(
		input.SessionTurns,
		sessionKey,
		input.FailureCall.TurnIndex,
		input.SuccessResult.TurnIndex,
	)
	if len(spanTurns) == 0 {
		return Episode{}, errors.New("failure-repair cost span is empty")
	}
	cost := costForTurns(spanTurns)
	if input.Session.Coverage != transcript.CoverageComplete {
		cost.LowerBound = true
	}
	costCoverage := CoverageComplete
	if cost.LowerBound {
		costCoverage = CoveragePartial
	}
	transcriptCoverage := string(input.Session.Coverage)
	if transcriptCoverage == "" {
		transcriptCoverage = CoverageUnknown
	}
	value := Episode{
		SchemaVersion:              SchemaVersion,
		ProjectIdentity:            projectIdentity,
		SessionKey:                 sessionKey,
		Kind:                       KindFailureRepair,
		FirstTurn:                  input.FailureCall.TurnIndex,
		LastTurn:                   input.SuccessResult.TurnIndex,
		StartedAt:                  spanTurns[0].OccurredAt.UTC(),
		EndedAt:                    spanTurns[len(spanTurns)-1].OccurredAt.UTC(),
		SourceRefs:                 sourceRefs,
		OutcomeRefs:                outcomeRefs,
		FailureSignature:           failureSignature,
		RepairFamily:               failedFamily,
		FailedCommandSignature:     failedSignature,
		SuccessfulCommandSignature: successSignature,
		Cost:                       cost,
		Coverage: Coverage{
			Transcript:      transcriptCoverage,
			CanonicalEvents: CoverageUnknown,
			ToolCallLinks:   CoverageComplete,
			SessionIdentity: CoverageUnknown,
			Cost:            costCoverage,
			Bounded:         true,
		},
		EvidenceClass:     trajectory.EvidenceDeterministicInference,
		Confidence:        trajectory.ConfidenceHigh,
		DerivationVersion: DerivationVersion,
	}
	value.InputHash = value.inputHash()
	value.EpisodeID = value.deterministicID()
	return value, value.Validate()
}

func (value Episode) Validate() error {
	if value.SchemaVersion != SchemaVersion ||
		strings.TrimSpace(value.ProjectIdentity) == "" ||
		strings.TrimSpace(value.SessionKey) == "" ||
		value.FirstTurn < 0 || value.LastTurn < value.FirstTurn ||
		value.StartedAt.IsZero() || value.EndedAt.Before(value.StartedAt) ||
		len(value.SourceRefs) == 0 ||
		!value.Coverage.Bounded ||
		value.EvidenceClass != trajectory.EvidenceDeterministicInference ||
		value.Confidence != trajectory.ConfidenceHigh ||
		value.DerivationVersion != DerivationVersion {
		return errors.New("evidence episode is invalid")
	}
	for _, ref := range value.SourceRefs {
		if err := ref.Validate(); err != nil {
			return errors.New("evidence episode has invalid evidence")
		}
	}
	switch value.Kind {
	case KindFailureRepair:
		if len(value.SourceRefs) != 4 ||
			value.FailureSignature == "" ||
			value.RepairFamily == "" ||
			value.FailedCommandSignature == "" ||
			value.SuccessfulCommandSignature == "" ||
			value.FailedCommandSignature == value.SuccessfulCommandSignature {
			return errors.New("failure-repair episode is invalid")
		}
		for _, ref := range value.SourceRefs {
			if ref.Kind != trajectory.NodeTranscriptTurn ||
				ref.SessionKey != value.SessionKey {
				return errors.New(
					"failure-repair episode has invalid evidence",
				)
			}
		}
	case KindMutationVerification:
		if len(value.MutationRefs) == 0 ||
			len(value.VerificationRefs) != 2 ||
			strings.TrimSpace(value.VerifierCommand) == "" ||
			strings.TrimSpace(value.VerifierCommandClass) == "" {
			return errors.New("mutation-verification episode is invalid")
		}
		for _, ref := range append(
			append([]trajectory.NodeRef(nil), value.MutationRefs...),
			value.VerificationRefs...,
		) {
			if err := ref.Validate(); err != nil ||
				ref.Kind != trajectory.NodeTranscriptTurn ||
				ref.SessionKey != value.SessionKey {
				return errors.New(
					"mutation-verification episode has invalid evidence",
				)
			}
		}
	default:
		return errors.New("evidence episode kind is invalid")
	}
	if value.InputHash == "" || value.InputHash != value.inputHash() ||
		value.EpisodeID == "" || value.EpisodeID != value.deterministicID() {
		return errors.New("evidence episode identity is invalid")
	}
	return nil
}

func (value Episode) deterministicID() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		SchemaVersion,
		value.ProjectIdentity,
		value.SessionKey,
		value.Kind,
		value.InputHash,
		DerivationVersion,
	}, "\x00")))
	return "epi_" + strings.ToLower(episodeIDEncoding.EncodeToString(sum[:]))
}

func (value Episode) inputHash() string {
	body, err := json.Marshal(struct {
		SourceRefs                 []trajectory.NodeRef
		OutcomeRefs                []string
		MutationRefs               []trajectory.NodeRef
		VerificationRefs           []trajectory.NodeRef
		VerifierCommand            string
		VerifierCommandClass       string
		FailureSignature           string
		RepairFamily               string
		FailedCommandSignature     string
		SuccessfulCommandSignature string
	}{
		SourceRefs:                 value.SourceRefs,
		OutcomeRefs:                normalizedStrings(value.OutcomeRefs),
		MutationRefs:               normalizedNodeRefs(value.MutationRefs),
		VerificationRefs:           normalizedNodeRefs(value.VerificationRefs),
		VerifierCommand:            strings.TrimSpace(value.VerifierCommand),
		VerifierCommandClass:       strings.TrimSpace(value.VerifierCommandClass),
		FailureSignature:           value.FailureSignature,
		RepairFamily:               value.RepairFamily,
		FailedCommandSignature:     value.FailedCommandSignature,
		SuccessfulCommandSignature: value.SuccessfulCommandSignature,
	})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func turnsWithin(
	values []transcript.Turn,
	sessionKey string,
	first int64,
	last int64,
) []transcript.Turn {
	var result []transcript.Turn
	for _, turn := range values {
		if turn.SessionKey == sessionKey &&
			turn.TurnIndex >= first &&
			turn.TurnIndex <= last {
			result = append(result, turn)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TurnIndex != result[j].TurnIndex {
			return result[i].TurnIndex < result[j].TurnIndex
		}
		return result[i].TurnID < result[j].TurnID
	})
	return result
}

func costForTurns(turns []transcript.Turn) issueintel.Cost {
	var result issueintel.Cost
	var usd float64
	knownUSD := false
	for index, turn := range turns {
		tokens := turnTokens(turn)
		result.WastedTokens += tokens
		if turn.CostUSD != nil {
			usd += *turn.CostUSD
			knownUSD = true
		} else if tokens > 0 {
			result.LowerBound = true
		}
		if index == 0 {
			continue
		}
		gap := turn.OccurredAt.Sub(turns[index-1].OccurredAt)
		if gap > 0 {
			if gap > maxActiveGap {
				gap = maxActiveGap
				result.LowerBound = true
			}
			result.WastedMinutes += gap.Minutes()
		}
	}
	if knownUSD {
		result.WastedUSD = &usd
	}
	return result
}

func turnTokens(turn transcript.Turn) int64 {
	var result int64
	for _, value := range []*int64{
		turn.InputTokens,
		turn.OutputTokens,
		turn.CacheReadTokens,
		turn.CacheWriteTokens,
	} {
		if value != nil && *value > 0 {
			result += *value
		}
	}
	return result
}

func normalizedNodeRefs(values []trajectory.NodeRef) []trajectory.NodeRef {
	seen := make(map[string]bool)
	var result []trajectory.NodeRef
	for _, value := range values {
		if err := value.Validate(); err != nil {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		key := string(encoded)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := json.Marshal(result[i])
		right, _ := json.Marshal(result[j])
		return string(left) < string(right)
	})
	return result
}

func transcriptNodeRefs(values []trajectory.NodeRef) []trajectory.NodeRef {
	var result []trajectory.NodeRef
	for _, value := range values {
		if value.Kind == trajectory.NodeTranscriptTurn {
			result = append(result, value)
		}
	}
	return result
}

func turnsForRefs(
	values []transcript.Turn,
	refs []trajectory.NodeRef,
) ([]transcript.Turn, error) {
	byKey := make(map[string]transcript.Turn)
	ambiguous := make(map[string]bool)
	for _, turn := range values {
		key := turn.SessionKey + "\x00" + fmt.Sprint(turn.TurnIndex)
		if _, exists := byKey[key]; exists {
			delete(byKey, key)
			ambiguous[key] = true
			continue
		}
		if !ambiguous[key] {
			byKey[key] = turn
		}
	}
	result := make([]transcript.Turn, 0, len(refs))
	for _, ref := range refs {
		if ref.TurnIndex == nil {
			return nil, errors.New("episode evidence has no turn index")
		}
		key := ref.SessionKey + "\x00" + fmt.Sprint(*ref.TurnIndex)
		turn, ok := byKey[key]
		if !ok || ambiguous[key] {
			return nil, errors.New("episode evidence turn is missing or ambiguous")
		}
		result = append(result, turn)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TurnIndex != result[j].TurnIndex {
			return result[i].TurnIndex < result[j].TurnIndex
		}
		return result[i].TurnID < result[j].TurnID
	})
	return result, nil
}

func turnForRef(
	values []transcript.Turn,
	ref trajectory.NodeRef,
) (transcript.Turn, bool) {
	if ref.TurnIndex == nil {
		return transcript.Turn{}, false
	}
	for _, turn := range values {
		if turn.SessionKey == ref.SessionKey &&
			turn.TurnIndex == *ref.TurnIndex {
			return turn, true
		}
	}
	return transcript.Turn{}, false
}

func normalizedStrings(values []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
