// Package derive deterministically derives causal trajectory edges and atomic
// outcomes from retained transcript turns and canonical events.
package derive

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	Version                        = "belay.trajectory-derive.v7"
	MaxDiagnostics                 = 512
	verificationMutationTurnWindow = 100
	repairCommandTurnWindow        = 20
)

type DiagnosticCode string

const (
	DiagnosticPartialTranscript           DiagnosticCode = "trajectory_partial_transcript"
	DiagnosticPartialCanonicalEvents      DiagnosticCode = "trajectory_partial_canonical_events"
	DiagnosticMissingToolCallID           DiagnosticCode = "trajectory_missing_tool_call_id"
	DiagnosticMissingToolCall             DiagnosticCode = "trajectory_missing_tool_call"
	DiagnosticAmbiguousToolCall           DiagnosticCode = "trajectory_ambiguous_tool_call"
	DiagnosticMissingInvocation           DiagnosticCode = "trajectory_missing_invocation_target"
	DiagnosticMissingParentToolCall       DiagnosticCode = "trajectory_missing_parent_tool_call"
	DiagnosticForeignCanonicalEvent       DiagnosticCode = "trajectory_foreign_canonical_event"
	DiagnosticMissingMutation             DiagnosticCode = "trajectory_missing_mutation_target"
	DiagnosticMissingVerificationResult   DiagnosticCode = "trajectory_missing_verification_result"
	DiagnosticAmbiguousVerificationResult DiagnosticCode = "trajectory_ambiguous_verification_result"
	DiagnosticUnknownVerificationResult   DiagnosticCode = "trajectory_unknown_verification_result"
	DiagnosticDiagnosticsTruncated        DiagnosticCode = "trajectory_diagnostics_truncated"
)

type Diagnostic struct {
	Code     DiagnosticCode      `json:"code"`
	Citation *trajectory.NodeRef `json:"citation,omitempty"`
}

type Coverage struct {
	Transcript               transcript.SessionCoverage `json:"transcript"`
	TranscriptTurnsComplete  bool                       `json:"transcript_turns_complete"`
	CanonicalEventsComplete  bool                       `json:"canonical_events_complete"`
	LinksComplete            bool                       `json:"links_complete"`
	MachineEnvelopesExcluded int                        `json:"machine_envelopes_excluded"`
	FullyDerived             bool                       `json:"fully_derived"`
}

type Input struct {
	Session                 transcript.Session
	Turns                   []transcript.Turn
	CanonicalEvents         []model.Event
	ProjectConfig           issueintel.ProjectConfig
	TranscriptTurnsComplete bool
	CanonicalEventsComplete bool
}

type Result struct {
	DerivationVersion string
	Edges             []trajectory.Edge
	Outcomes          []trajectory.Outcome
	Diagnostics       []Diagnostic
	Coverage          Coverage
}

type positionedTurn struct {
	position int
	turn     transcript.Turn
}

type matchedMutation struct {
	position int
	turn     transcript.Turn
	target   trajectory.NodeRef
}

type commandAttempt struct {
	position       int
	resultPosition int
	segment        int
	call           transcript.Turn
	result         transcript.Turn
	signature      string
	repairFamily   string
	exitCode       int
}

func Session(input Input) (Result, error) {
	if err := validateInput(input); err != nil {
		return Result{}, err
	}
	turns := append([]transcript.Turn(nil), input.Turns...)
	sort.Slice(turns, func(i, j int) bool {
		if turns[i].TurnIndex != turns[j].TurnIndex {
			return turns[i].TurnIndex < turns[j].TurnIndex
		}
		if !turns[i].OccurredAt.Equal(turns[j].OccurredAt) {
			return turns[i].OccurredAt.Before(turns[j].OccurredAt)
		}
		return turns[i].TurnID < turns[j].TurnID
	})
	events := append([]model.Event(nil), input.CanonicalEvents...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].Source.Sequence != events[j].Source.Sequence {
			return events[i].Source.Sequence < events[j].Source.Sequence
		}
		if !events[i].OccurredAt.Equal(events[j].OccurredAt) {
			return events[i].OccurredAt.Before(events[j].OccurredAt)
		}
		return events[i].EventID < events[j].EventID
	})

	result := Result{
		DerivationVersion: Version,
		Coverage: Coverage{
			Transcript:              input.Session.Coverage,
			TranscriptTurnsComplete: input.TranscriptTurnsComplete,
			CanonicalEventsComplete: input.CanonicalEventsComplete,
			LinksComplete:           true,
		},
	}
	if input.Session.Coverage != transcript.CoverageComplete ||
		!input.TranscriptTurnsComplete {
		result.addDiagnostic(DiagnosticPartialTranscript, nil)
	}
	if !input.CanonicalEventsComplete {
		result.addDiagnostic(DiagnosticPartialCanonicalEvents, nil)
	}

	calls := make(map[string][]transcript.Turn)
	for _, turn := range turns {
		if turn.Role != transcript.RoleToolCall {
			continue
		}
		callID := strings.TrimSpace(turn.Payload.ToolCallID)
		if callID == "" {
			result.addDiagnostic(
				DiagnosticMissingToolCallID,
				referencePointer(turnReference(turn)),
			)
			continue
		}
		calls[callID] = append(calls[callID], turn)
	}
	ambiguousCalls := make(map[string]bool)
	for callID, matches := range calls {
		if len(matches) < 2 {
			continue
		}
		ambiguousCalls[callID] = true
		for _, turn := range matches {
			result.addDiagnostic(
				DiagnosticAmbiguousToolCall,
				referencePointer(turnReference(turn)),
			)
		}
	}

	invocations := make(map[string][]model.Event)
	mutations := make(map[string][]model.Event)
	for _, event := range events {
		if event.Session.Key != input.Session.SessionKey {
			ref := eventReference(event)
			result.addDiagnostic(
				DiagnosticForeignCanonicalEvent,
				&ref,
			)
			continue
		}
		if !invocationEvent(event) ||
			event.Observation.Details == nil {
			continue
		}
		callID := strings.TrimSpace(event.Observation.Details.ToolCallID)
		if callID != "" {
			invocations[callID] = append(invocations[callID], event)
			if mutationEvent(event) {
				mutations[callID] = append(mutations[callID], event)
			}
		}
	}
	results := make(map[string][]positionedTurn)
	for position, turn := range turns {
		if turn.Role != transcript.RoleToolResult {
			continue
		}
		callID := strings.TrimSpace(turn.Payload.ToolCallID)
		if callID != "" {
			results[callID] = append(results[callID], positionedTurn{
				position: position,
				turn:     turn,
			})
		}
	}

	for index, turn := range turns {
		callID := strings.TrimSpace(turn.Payload.ToolCallID)
		switch turn.Role {
		case transcript.RoleToolCall:
			if callID != "" && !ambiguousCalls[callID] {
				targets := invocations[callID]
				if len(targets) == 0 {
					result.addDiagnostic(
						DiagnosticMissingInvocation,
						referencePointer(turnReference(turn)),
					)
				}
				for _, event := range targets {
					result.Edges = append(
						result.Edges,
						newEdge(
							input.Session,
							turnReference(turn),
							trajectory.RelationInvokes,
							eventReference(event),
							event.OccurredAt,
							trajectory.EvidenceObserved,
						),
					)
				}
			}
		case transcript.RoleToolResult:
			result.deriveToolResult(input.Session, turn, callID, calls, ambiguousCalls)
		}

		parentID := strings.TrimSpace(turn.Payload.ParentToolUseID)
		if parentID != "" {
			result.deriveParent(input.Session, turn, parentID, calls, ambiguousCalls)
		}
		if turn.Role == transcript.RoleUser {
			result.deriveCorrection(input.Session, turns, index)
		}
	}
	matchedMutations := result.deriveMutationVerification(
		input.Session,
		turns,
		mutations,
		results,
		ambiguousCalls,
		input.ProjectConfig,
	)
	result.deriveRepairCompletionCompaction(
		input.Session,
		turns,
		results,
		ambiguousCalls,
		matchedMutations,
	)

	sort.Slice(result.Edges, func(i, j int) bool {
		if !result.Edges[i].OccurredAt.Equal(result.Edges[j].OccurredAt) {
			return result.Edges[i].OccurredAt.Before(result.Edges[j].OccurredAt)
		}
		return result.Edges[i].EdgeID < result.Edges[j].EdgeID
	})
	sort.Slice(result.Outcomes, func(i, j int) bool {
		if !result.Outcomes[i].OccurredAt.Equal(result.Outcomes[j].OccurredAt) {
			return result.Outcomes[i].OccurredAt.Before(result.Outcomes[j].OccurredAt)
		}
		return result.Outcomes[i].OutcomeID < result.Outcomes[j].OutcomeID
	})
	sort.SliceStable(result.Diagnostics, func(i, j int) bool {
		left, right := diagnosticTurnIndex(result.Diagnostics[i]), diagnosticTurnIndex(result.Diagnostics[j])
		if left != right {
			return left < right
		}
		return result.Diagnostics[i].Code < result.Diagnostics[j].Code
	})
	result.Diagnostics = boundedDiagnostics(result.Diagnostics)
	for _, edge := range result.Edges {
		if err := edge.Validate(); err != nil {
			return Result{}, errors.New(
				"trajectory derivation produced an invalid edge",
			)
		}
	}
	for _, outcome := range result.Outcomes {
		if err := outcome.Validate(); err != nil {
			return Result{}, errors.New(
				"trajectory derivation produced an invalid outcome",
			)
		}
	}
	result.Coverage.FullyDerived =
		result.Coverage.Transcript == transcript.CoverageComplete &&
			result.Coverage.TranscriptTurnsComplete &&
			result.Coverage.CanonicalEventsComplete &&
			result.Coverage.LinksComplete
	return result, nil
}

func boundedDiagnostics(values []Diagnostic) []Diagnostic {
	if len(values) <= MaxDiagnostics {
		return values
	}
	result := append(
		[]Diagnostic(nil),
		values[:MaxDiagnostics-1]...,
	)
	result = append(result, Diagnostic{
		Code: DiagnosticDiagnosticsTruncated,
	})
	return result
}

func (result *Result) deriveToolResult(
	session transcript.Session,
	turn transcript.Turn,
	callID string,
	calls map[string][]transcript.Turn,
	ambiguous map[string]bool,
) {
	if callID == "" {
		result.addDiagnostic(
			DiagnosticMissingToolCallID,
			referencePointer(turnReference(turn)),
		)
		return
	}
	if ambiguous[callID] {
		result.addDiagnostic(
			DiagnosticAmbiguousToolCall,
			referencePointer(turnReference(turn)),
		)
		return
	}
	matches := calls[callID]
	if len(matches) != 1 {
		result.addDiagnostic(
			DiagnosticMissingToolCall,
			referencePointer(turnReference(turn)),
		)
		return
	}
	result.Edges = append(
		result.Edges,
		newEdge(
			session,
			turnReference(turn),
			trajectory.RelationReturnsFor,
			turnReference(matches[0]),
			turn.OccurredAt,
			trajectory.EvidenceObserved,
		),
	)
}

func (result *Result) deriveParent(
	session transcript.Session,
	turn transcript.Turn,
	parentID string,
	calls map[string][]transcript.Turn,
	ambiguous map[string]bool,
) {
	if ambiguous[parentID] {
		result.addDiagnostic(
			DiagnosticAmbiguousToolCall,
			referencePointer(turnReference(turn)),
		)
		return
	}
	matches := calls[parentID]
	if len(matches) != 1 {
		result.addDiagnostic(
			DiagnosticMissingParentToolCall,
			referencePointer(turnReference(turn)),
		)
		return
	}
	parent := turnReference(matches[0])
	child := turnReference(turn)
	if sameNode(parent, child) {
		return
	}
	result.Edges = append(
		result.Edges,
		newEdge(
			session,
			parent,
			trajectory.RelationParentOf,
			child,
			turn.OccurredAt,
			trajectory.EvidenceObserved,
		),
	)
}

func (result *Result) deriveCorrection(
	session transcript.Session,
	turns []transcript.Turn,
	index int,
) {
	turn := turns[index]
	text := strings.TrimSpace(turn.Payload.Text)
	envelope := transcriptissues.IsMachineGeneratedEnvelope(text)
	delegated := strings.TrimSpace(turn.Payload.ParentToolUseID) != ""
	if text == "" || envelope || delegated {
		if envelope || delegated {
			result.Coverage.MachineEnvelopesExcluded++
		}
		return
	}
	if transcriptissues.HighConfidenceCorrectionMarker(text) == "" ||
		index == 0 ||
		!behaviorRole(turns[index-1].Role) {
		return
	}
	userRef := turnReference(turn)
	behaviorRef := turnReference(turns[index-1])
	result.Edges = append(
		result.Edges,
		newEdge(
			session,
			userRef,
			trajectory.RelationRespondsTo,
			behaviorRef,
			turn.OccurredAt,
			trajectory.EvidenceDeterministicInference,
		),
		newEdge(
			session,
			userRef,
			trajectory.RelationCorrects,
			behaviorRef,
			turn.OccurredAt,
			trajectory.EvidenceDeterministicInference,
		),
	)
	outcome := trajectory.Outcome{
		SchemaVersion:     trajectory.OutcomeSchemaVersion,
		ProjectIdentity:   session.ProjectIdentity,
		SessionKey:        session.SessionKey,
		OccurredAt:        turn.OccurredAt,
		Kind:              trajectory.OutcomeCorrection,
		Result:            trajectory.ResultObserved,
		EvidenceClass:     trajectory.EvidenceDeterministicInference,
		Confidence:        trajectory.ConfidenceHigh,
		SourceRefs:        []trajectory.NodeRef{behaviorRef, userRef},
		DerivationVersion: Version,
	}
	outcome.OutcomeID = outcome.DeterministicID()
	result.Outcomes = append(result.Outcomes, outcome)
}

func (result *Result) deriveMutationVerification(
	session transcript.Session,
	turns []transcript.Turn,
	mutationEvents map[string][]model.Event,
	results map[string][]positionedTurn,
	ambiguousCalls map[string]bool,
	config issueintel.ProjectConfig,
) []matchedMutation {
	matchedMutations := make([]matchedMutation, 0)
	for position, turn := range turns {
		if turn.Role != transcript.RoleToolCall ||
			len(projectEditedFiles(session, turn)) == 0 {
			continue
		}
		callID := strings.TrimSpace(turn.Payload.ToolCallID)
		if callID == "" || ambiguousCalls[callID] {
			continue
		}
		targets := mutationEvents[callID]
		if len(targets) == 0 {
			matchedMutations = append(matchedMutations, matchedMutation{
				position: position,
				turn:     turn,
				target:   turnReference(turn),
			})
			continue
		}
		for _, event := range targets {
			callRef := turnReference(turn)
			eventRef := eventReference(event)
			result.Edges = append(
				result.Edges,
				newEdge(
					session,
					callRef,
					trajectory.RelationModifies,
					eventRef,
					event.OccurredAt,
					trajectory.EvidenceObserved,
				),
			)
			matchedMutations = append(matchedMutations, matchedMutation{
				position: position,
				turn:     turn,
				target:   eventRef,
			})
		}
	}

	priorVerification := -1
	for position, turn := range turns {
		if turn.Role != transcript.RoleToolCall {
			continue
		}
		_, _, recognized := transcriptissues.RetainedVerificationCommand(
			turn,
			config,
		)
		if !recognized {
			continue
		}
		windowStart := position - verificationMutationTurnWindow
		if windowStart < 0 {
			windowStart = 0
		}
		if priorVerification+1 > windowStart {
			windowStart = priorVerification + 1
		}
		verificationRef := turnReference(turn)
		for _, mutation := range matchedMutations {
			if mutation.position < windowStart ||
				mutation.position >= position {
				continue
			}
			mutationTurnRef := turnReference(mutation.turn)
			sourceRefs := []trajectory.NodeRef{
				verificationRef,
				mutationTurnRef,
			}
			if mutation.target.Kind != trajectory.NodeTranscriptTurn {
				sourceRefs = append(sourceRefs, mutation.target)
			}
			result.Edges = append(
				result.Edges,
				newEdgeWithSources(
					session,
					verificationRef,
					trajectory.RelationVerifies,
					mutation.target,
					turn.OccurredAt,
					trajectory.EvidenceDeterministicInference,
					sourceRefs,
				),
			)
		}
		result.deriveVerificationOutcome(
			session,
			turn,
			position,
			results,
			ambiguousCalls,
		)
		priorVerification = position
	}
	return matchedMutations
}

func (result *Result) deriveVerificationOutcome(
	session transcript.Session,
	call transcript.Turn,
	callPosition int,
	results map[string][]positionedTurn,
	ambiguousCalls map[string]bool,
) {
	callID := strings.TrimSpace(call.Payload.ToolCallID)
	if callID == "" {
		result.addDiagnostic(
			DiagnosticMissingVerificationResult,
			referencePointer(turnReference(call)),
		)
		return
	}
	if ambiguousCalls[callID] {
		return
	}
	matches := make([]transcript.Turn, 0, len(results[callID]))
	for _, candidate := range results[callID] {
		if candidate.position > callPosition {
			matches = append(matches, candidate.turn)
		}
	}
	switch len(matches) {
	case 0:
		result.addDiagnostic(
			DiagnosticMissingVerificationResult,
			referencePointer(turnReference(call)),
		)
		return
	case 1:
	default:
		result.addDiagnostic(
			DiagnosticAmbiguousVerificationResult,
			referencePointer(turnReference(call)),
		)
		return
	}
	verificationResult := matches[0]
	failed, known := transcriptissues.ExplicitToolResultFailed(
		verificationResult,
	)
	if !known {
		result.addDiagnostic(
			DiagnosticUnknownVerificationResult,
			referencePointer(turnReference(verificationResult)),
		)
		return
	}
	kind := trajectory.OutcomeVerificationPass
	outcomeResult := trajectory.ResultSucceeded
	if failed {
		kind = trajectory.OutcomeVerificationFail
		outcomeResult = trajectory.ResultFailed
	}
	callRef := turnReference(call)
	resultRef := turnReference(verificationResult)
	outcome := trajectory.Outcome{
		SchemaVersion:     trajectory.OutcomeSchemaVersion,
		ProjectIdentity:   session.ProjectIdentity,
		SessionKey:        session.SessionKey,
		OccurredAt:        verificationResult.OccurredAt,
		Kind:              kind,
		Result:            outcomeResult,
		EvidenceClass:     trajectory.EvidenceDeterministicInference,
		Confidence:        trajectory.ConfidenceHigh,
		SourceRefs:        []trajectory.NodeRef{callRef, resultRef},
		DerivationVersion: Version,
	}
	outcome.OutcomeID = outcome.DeterministicID()
	result.Outcomes = append(result.Outcomes, outcome)
}

func (result *Result) deriveRepairCompletionCompaction(
	session transcript.Session,
	turns []transcript.Turn,
	results map[string][]positionedTurn,
	ambiguousCalls map[string]bool,
	matchedMutations []matchedMutation,
) {
	result.deriveRepairs(
		session,
		turns,
		results,
		ambiguousCalls,
	)
	result.deriveCompletionClaims(session, turns, matchedMutations)
	result.deriveCompactions(session, turns)
}

func (result *Result) deriveRepairs(
	session transcript.Session,
	turns []transcript.Turn,
	results map[string][]positionedTurn,
	ambiguousCalls map[string]bool,
) {
	attempts := explicitCommandAttempts(turns, results, ambiguousCalls)
	for successIndex, success := range attempts {
		if success.exitCode != 0 {
			continue
		}
		for prior := successIndex - 1; prior >= 0; prior-- {
			failure := attempts[prior]
			if success.position-failure.position > repairCommandTurnWindow ||
				failure.segment != success.segment {
				break
			}
			if failure.resultPosition >= success.position {
				break
			}
			if failure.exitCode == 0 {
				break
			}
			if failure.repairFamily == "" ||
				failure.repairFamily != success.repairFamily ||
				failure.signature == success.signature {
				break
			}
			failureCallRef := turnReference(failure.call)
			failureResultRef := turnReference(failure.result)
			successCallRef := turnReference(success.call)
			successResultRef := turnReference(success.result)
			sourceRefs := []trajectory.NodeRef{
				failureCallRef,
				failureResultRef,
				successCallRef,
				successResultRef,
			}
			result.Edges = append(
				result.Edges,
				newEdgeWithSources(
					session,
					successCallRef,
					trajectory.RelationRepairs,
					failureCallRef,
					success.result.OccurredAt,
					trajectory.EvidenceDeterministicInference,
					sourceRefs,
				),
			)
			outcome := trajectory.Outcome{
				SchemaVersion:     trajectory.OutcomeSchemaVersion,
				ProjectIdentity:   session.ProjectIdentity,
				SessionKey:        session.SessionKey,
				OccurredAt:        success.result.OccurredAt,
				Kind:              trajectory.OutcomeRepair,
				Result:            trajectory.ResultSucceeded,
				EvidenceClass:     trajectory.EvidenceDeterministicInference,
				Confidence:        trajectory.ConfidenceHigh,
				SourceRefs:        sourceRefs,
				DerivationVersion: Version,
			}
			outcome.OutcomeID = outcome.DeterministicID()
			result.Outcomes = append(result.Outcomes, outcome)
			break
		}
	}
}

func (result *Result) deriveCompletionClaims(
	session transcript.Session,
	turns []transcript.Turn,
	matchedMutations []matchedMutation,
) {
	mutationsByPosition := make(map[int][]matchedMutation)
	for _, mutation := range matchedMutations {
		mutationsByPosition[mutation.position] = append(
			mutationsByPosition[mutation.position],
			mutation,
		)
	}
	for position, turn := range turns {
		if !transcriptissues.IsCompletionClaim(turn) {
			continue
		}
		latestMutationPosition := -1
		for prior := position - 1; prior >= 0; prior-- {
			if len(projectEditedFiles(session, turns[prior])) > 0 {
				latestMutationPosition = prior
				break
			}
		}
		if latestMutationPosition < 0 {
			continue
		}
		claimRef := turnReference(turn)
		for _, mutation := range mutationsByPosition[latestMutationPosition] {
			mutationTurnRef := turnReference(mutation.turn)
			sourceRefs := []trajectory.NodeRef{
				claimRef,
				mutationTurnRef,
			}
			if mutation.target.Kind != trajectory.NodeTranscriptTurn {
				sourceRefs = append(sourceRefs, mutation.target)
			}
			result.Edges = append(
				result.Edges,
				newEdgeWithSources(
					session,
					claimRef,
					trajectory.RelationClaimsCompletionAfter,
					mutation.target,
					turn.OccurredAt,
					trajectory.EvidenceDeterministicInference,
					sourceRefs,
				),
			)
		}
	}
}

func projectEditedFiles(
	session transcript.Session,
	turn transcript.Turn,
) []string {
	files := transcriptissues.ExtractEditedFiles(turn)
	root := strings.TrimSpace(session.ProjectPath)
	if len(files) == 0 || !filepath.IsAbs(root) {
		return files
	}
	root = filepath.Clean(root)
	base := strings.TrimSpace(turn.Payload.CWD)
	if !filepath.IsAbs(base) {
		base = root
	}
	result := make([]string, 0, len(files))
	for _, path := range files {
		candidate := path
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(base, candidate)
		}
		relative, err := filepath.Rel(root, filepath.Clean(candidate))
		if err != nil || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		result = append(result, path)
	}
	return result
}

func (result *Result) deriveCompactions(
	session transcript.Session,
	turns []transcript.Turn,
) {
	for position, turn := range turns {
		if turn.Role != transcript.RoleCompactionSummary || position == 0 {
			continue
		}
		result.Edges = append(
			result.Edges,
			newEdge(
				session,
				turnReference(turn),
				trajectory.RelationCompactsAfter,
				turnReference(turns[position-1]),
				turn.OccurredAt,
				trajectory.EvidenceDeterministicInference,
			),
		)
	}
}

func explicitCommandAttempts(
	turns []transcript.Turn,
	results map[string][]positionedTurn,
	ambiguousCalls map[string]bool,
) []commandAttempt {
	segments := turnSegments(turns)
	attempts := make([]commandAttempt, 0)
	for position, call := range turns {
		if call.Role != transcript.RoleToolCall {
			continue
		}
		signature, ok := transcriptissues.NormalizedCommandSignature(call)
		if !ok {
			continue
		}
		repairFamily, ok := transcriptissues.CommandRepairFamily(call)
		if !ok {
			continue
		}
		callID := strings.TrimSpace(call.Payload.ToolCallID)
		if callID == "" || ambiguousCalls[callID] {
			continue
		}
		matches := results[callID]
		if len(matches) != 1 ||
			matches[0].position <= position ||
			segments[matches[0].position] != segments[position] {
			continue
		}
		failed, known := transcriptissues.ExplicitToolResultFailed(
			matches[0].turn,
		)
		if !known {
			continue
		}
		exitCode := 0
		if failed {
			exitCode = 1
		}
		attempts = append(attempts, commandAttempt{
			position:       position,
			resultPosition: matches[0].position,
			segment:        segments[position],
			call:           call,
			result:         matches[0].turn,
			signature:      signature,
			repairFamily:   repairFamily,
			exitCode:       exitCode,
		})
	}
	return attempts
}

func turnSegments(turns []transcript.Turn) []int {
	result := make([]int, len(turns))
	segment := 0
	for position, turn := range turns {
		result[position] = segment
		if turn.Role == transcript.RoleUser ||
			turn.Role == transcript.RoleCompactionSummary {
			segment++
		}
	}
	return result
}

func newEdge(
	session transcript.Session,
	from trajectory.NodeRef,
	relation trajectory.EdgeRelation,
	to trajectory.NodeRef,
	occurredAt time.Time,
	evidence trajectory.EvidenceClass,
) trajectory.Edge {
	return newEdgeWithSources(
		session,
		from,
		relation,
		to,
		occurredAt,
		evidence,
		[]trajectory.NodeRef{from, to},
	)
}

func newEdgeWithSources(
	session transcript.Session,
	from trajectory.NodeRef,
	relation trajectory.EdgeRelation,
	to trajectory.NodeRef,
	occurredAt time.Time,
	evidence trajectory.EvidenceClass,
	sourceRefs []trajectory.NodeRef,
) trajectory.Edge {
	value := trajectory.Edge{
		SchemaVersion:     trajectory.EdgeSchemaVersion,
		ProjectIdentity:   session.ProjectIdentity,
		SessionKey:        session.SessionKey,
		From:              from,
		Relation:          relation,
		To:                to,
		EvidenceClass:     evidence,
		Confidence:        trajectory.ConfidenceHigh,
		DerivationVersion: Version,
		SourceRefs:        sourceRefs,
		OccurredAt:        occurredAt,
	}
	value.EdgeID = value.DeterministicID()
	return value
}

func validateInput(input Input) error {
	if strings.TrimSpace(input.Session.SessionKey) == "" ||
		input.Session.SessionKey != strings.TrimSpace(input.Session.SessionKey) ||
		strings.TrimSpace(input.Session.ProjectIdentity) == "" ||
		input.Session.ProjectIdentity != strings.TrimSpace(input.Session.ProjectIdentity) {
		return errors.New("trajectory derivation requires session and project identity")
	}
	switch input.Session.Coverage {
	case transcript.CoverageComplete, transcript.CoveragePartial, transcript.CoverageLive:
	default:
		return errors.New("trajectory derivation requires valid transcript coverage")
	}
	turnIndexes := make(map[int64]struct{}, len(input.Turns))
	for _, turn := range input.Turns {
		if turn.SessionKey != input.Session.SessionKey ||
			turn.TurnIndex < 0 ||
			turn.OccurredAt.IsZero() {
			return errors.New("trajectory derivation contains an invalid transcript turn")
		}
		if err := turnReference(turn).Validate(); err != nil {
			return errors.New(
				"trajectory derivation contains an invalid transcript citation",
			)
		}
		if _, exists := turnIndexes[turn.TurnIndex]; exists {
			return errors.New("trajectory derivation contains duplicate turn indexes")
		}
		turnIndexes[turn.TurnIndex] = struct{}{}
	}
	return nil
}

func invocationEvent(event model.Event) bool {
	switch event.Observation.Type {
	case "tool.call", "command.exec", "file.read", "file.write", "file.delete":
		return true
	default:
		return false
	}
}

func mutationEvent(event model.Event) bool {
	return event.Observation.Type == "file.write" ||
		event.Observation.Type == "file.delete"
}

func behaviorRole(role transcript.Role) bool {
	return role == transcript.RoleAssistant ||
		role == transcript.RoleToolCall ||
		role == transcript.RoleToolResult
}

func turnReference(turn transcript.Turn) trajectory.NodeRef {
	index := turn.TurnIndex
	return trajectory.NodeRef{
		Kind:       trajectory.NodeTranscriptTurn,
		SessionKey: turn.SessionKey,
		TurnIndex:  &index,
	}
}

func eventReference(event model.Event) trajectory.NodeRef {
	return trajectory.NodeRef{
		Kind:    trajectory.NodeCanonicalEvent,
		EventID: event.EventID,
	}
}

func sameNode(left, right trajectory.NodeRef) bool {
	return left.Kind == right.Kind &&
		left.SessionKey == right.SessionKey &&
		left.TurnIndex != nil &&
		right.TurnIndex != nil &&
		*left.TurnIndex == *right.TurnIndex
}

func referencePointer(value trajectory.NodeRef) *trajectory.NodeRef {
	return &value
}

func (result *Result) addDiagnostic(
	code DiagnosticCode,
	citation *trajectory.NodeRef,
) {
	result.Coverage.LinksComplete = false
	if citation == nil {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: code})
		return
	}
	value := *citation
	result.Diagnostics = append(result.Diagnostics, Diagnostic{
		Code:     code,
		Citation: &value,
	})
}

func diagnosticTurnIndex(value Diagnostic) int64 {
	if value.Citation == nil ||
		value.Citation.Kind != trajectory.NodeTranscriptTurn ||
		value.Citation.TurnIndex == nil {
		return -1
	}
	return *value.Citation.TurnIndex
}
