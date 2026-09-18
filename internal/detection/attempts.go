package detection

import (
	"context"
	"strings"
	"time"
)

const adjacentPairWindow = 5 * time.Second

type explicitOutcome uint8

const (
	outcomeUnknown explicitOutcome = iota
	outcomeSucceeded
	outcomeFailed
)

type commandAttemptTrust uint8

const (
	trustUnpairedResult commandAttemptTrust = iota
	trustExecObservation
	trustAdjacentPair
	trustToolCallPair
)

func (trust commandAttemptTrust) paired() bool {
	return trust == trustAdjacentPair || trust == trustToolCallPair
}

func (trust commandAttemptTrust) trustworthyPair() bool {
	return trust.paired()
}

func (trust commandAttemptTrust) failureConfidence() string {
	if trust == trustUnpairedResult {
		return ConfidenceLow
	}
	return ConfidenceHigh
}

type commandAttempt struct {
	signature          string
	class              string
	events             []preparedEvent
	first              time.Time
	last               time.Time
	firstPos           int
	lastPos            int
	outcome            explicitOutcome
	conflict           bool
	trust              commandAttemptTrust
	repetitionEligible bool
	classConflict      bool
}

func commandAttempts(ctx context.Context, session preparedSession) ([]commandAttempt, error) {
	var attempts []commandAttempt
	pending := make(map[string]int)
	multiplicity, err := commandToolCallMultiplicity(ctx, session)
	if err != nil {
		return nil, err
	}

	for position, item := range session.events {
		if err := contextCheck(ctx, position); err != nil {
			return nil, err
		}
		eventType := item.event.Observation.Type
		if eventType != "command.exec" && eventType != "command.result" {
			continue
		}
		signature := item.enrichment.CommandSignatureID
		toolCallID := eventToolCallID(item.event)

		if eventType == "command.result" {
			if toolCallID != "" {
				counts := multiplicity[toolCallID]
				if counts.execs == 1 && counts.results == 1 {
					if index, ok := pending[toolCallID]; ok &&
						resultMatchesAttemptIdentity(item, attempts[index]) {
						mergeAttempt(
							&attempts[index],
							item,
							position,
							trustToolCallPair,
						)
						delete(pending, toolCallID)
						continue
					}
				}
			} else if position > 0 && len(attempts) > 0 {
				previous := session.events[position-1]
				candidate := &attempts[len(attempts)-1]
				if previous.event.Observation.Type == "command.exec" &&
					eventToolCallID(previous.event) == "" &&
					candidate.lastPos == position-1 &&
					resultMatchesAttemptIdentity(item, *candidate) &&
					durationAbsolute(item.event.OccurredAt.Sub(previous.event.OccurredAt)) <= adjacentPairWindow {
					mergeAttempt(candidate, item, position, trustAdjacentPair)
					continue
				}
			}
		}

		if signature == "" {
			continue
		}
		attempts = append(attempts, newAttempt(item, position, eventType))
		index := len(attempts) - 1
		if eventType == "command.result" {
			if toolCallID != "" && multiplicity[toolCallID].execs > 0 {
				attempts[index].repetitionEligible = false
			} else if adjacentExecWithoutID(session.events, position) {
				attempts[index].repetitionEligible = false
			}
		}
		if eventType == "command.exec" && toolCallID != "" {
			counts := multiplicity[toolCallID]
			if counts.execs == 1 && counts.results == 1 {
				pending[toolCallID] = index
			}
		}
	}
	return attempts, nil
}

type toolCallMultiplicity struct {
	execs   int
	results int
}

func commandToolCallMultiplicity(
	ctx context.Context,
	session preparedSession,
) (map[string]toolCallMultiplicity, error) {
	result := make(map[string]toolCallMultiplicity)
	for index, item := range session.events {
		if err := contextCheck(ctx, index); err != nil {
			return nil, err
		}
		toolCallID := eventToolCallID(item.event)
		if toolCallID == "" {
			continue
		}
		counts := result[toolCallID]
		switch item.event.Observation.Type {
		case "command.exec":
			counts.execs++
		case "command.result":
			counts.results++
		default:
			continue
		}
		result[toolCallID] = counts
	}
	return result, nil
}

func resultMatchesAttemptIdentity(item preparedEvent, attempt commandAttempt) bool {
	resultSignature := item.enrichment.CommandSignatureID
	return attempt.signature != "" &&
		(resultSignature == "" || resultSignature == attempt.signature)
}

func adjacentExecWithoutID(events []preparedEvent, position int) bool {
	if position <= 0 {
		return false
	}
	previous := events[position-1]
	return previous.event.Observation.Type == "command.exec" &&
		eventToolCallID(previous.event) == "" &&
		durationAbsolute(events[position].event.OccurredAt.Sub(previous.event.OccurredAt)) <= adjacentPairWindow
}

func newAttempt(
	item preparedEvent,
	position int,
	eventType string,
) commandAttempt {
	outcome, conflict := explicitEventOutcomeDetails(
		item.event.Observation.ExitCode,
		item.event.Observation.Outcome,
	)
	trust := trustUnpairedResult
	if eventType == "command.exec" {
		trust = trustExecObservation
	}
	return commandAttempt{
		signature:          item.enrichment.CommandSignatureID,
		class:              normalizedCommandClass(item.enrichment.CommandClass),
		events:             []preparedEvent{item},
		first:              item.event.OccurredAt,
		last:               item.event.OccurredAt,
		firstPos:           position,
		lastPos:            position,
		outcome:            outcome,
		conflict:           conflict,
		trust:              trust,
		repetitionEligible: true,
	}
}

func mergeAttempt(
	attempt *commandAttempt,
	item preparedEvent,
	position int,
	trust commandAttemptTrust,
) {
	attempt.events = append(attempt.events, item)
	if item.event.OccurredAt.Before(attempt.first) {
		attempt.first = item.event.OccurredAt
	}
	if item.event.OccurredAt.After(attempt.last) {
		attempt.last = item.event.OccurredAt
	}
	attempt.lastPos = position
	attempt.trust = trust
	outcome, conflict := explicitEventOutcomeDetails(
		item.event.Observation.ExitCode,
		item.event.Observation.Outcome,
	)
	if conflict {
		attempt.outcome = outcomeUnknown
		attempt.conflict = true
	} else if outcome != outcomeUnknown {
		switch {
		case attempt.conflict:
			attempt.outcome = outcomeUnknown
		case attempt.outcome == outcomeUnknown:
			attempt.outcome = outcome
		case attempt.outcome != outcome:
			attempt.outcome = outcomeUnknown
			attempt.conflict = true
		}
	}
	if item.enrichment.CommandSignatureID == "" {
		return
	}
	incomingClass := normalizedCommandClass(item.enrichment.CommandClass)
	switch {
	case attempt.classConflict:
		attempt.class = CommandClassUnknown
	case attempt.class == CommandClassUnknown:
		attempt.class = incomingClass
	case incomingClass != CommandClassUnknown && incomingClass != attempt.class:
		attempt.class = CommandClassUnknown
		attempt.classConflict = true
	}
}

func explicitEventOutcome(exitCode *int, value string) explicitOutcome {
	outcome, _ := explicitEventOutcomeDetails(exitCode, value)
	return outcome
}

func explicitEventOutcomeDetails(
	exitCode *int,
	value string,
) (explicitOutcome, bool) {
	fromCode := outcomeUnknown
	if exitCode != nil {
		if *exitCode == 0 {
			fromCode = outcomeSucceeded
		} else {
			fromCode = outcomeFailed
		}
	}
	fromValue := outcomeUnknown
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "succeeded":
		fromValue = outcomeSucceeded
	case "failed":
		fromValue = outcomeFailed
	}
	if fromCode != outcomeUnknown &&
		fromValue != outcomeUnknown &&
		fromCode != fromValue {
		return outcomeUnknown, true
	}
	if fromCode != outcomeUnknown {
		return fromCode, false
	}
	return fromValue, false
}

func normalizedCommandClass(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case CommandClassTest:
		return CommandClassTest
	case CommandClassBuild:
		return CommandClassBuild
	case CommandClassTypecheck:
		return CommandClassTypecheck
	case CommandClassLint:
		return CommandClassLint
	case CommandClassFormatCheck:
		return CommandClassFormatCheck
	case CommandClassOther:
		return CommandClassOther
	default:
		return CommandClassUnknown
	}
}
