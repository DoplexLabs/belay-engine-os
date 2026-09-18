package transcriptissues

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const coldStartSharePercent = 15

type conversationDoneObservation struct {
	observation  observation
	sessionIndex int
}

type conversationColdStart struct {
	session       preparedSession
	prefixEnd     int
	boundaryIndex int
	cost          issueintel.Cost
}

func detectRepeatedCorrections(
	ctx context.Context,
	project preparedProject,
) ([]observation, []issueintel.CorrectionCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	var observations []observation
	var candidates []issueintel.CorrectionCandidate
	for sessionIndex, session := range project.sessions {
		if sessionIndex&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		for turnIndex, turn := range session.turns {
			if turnIndex&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
			}
			if turn.Role != transcript.RoleUser {
				continue
			}
			text := strings.TrimSpace(turn.Payload.Text)
			if text == "" {
				continue
			}
			marker := correctionMarker(text)
			shortTurn := wordCount(text) < 40
			if !shortTurn && marker == "" {
				continue
			}
			candidates = append(candidates, issueintel.CorrectionCandidate{
				CandidateID: candidateID(project.project.Identity, turn),
				Project:     project.project,
				Citation:    excerptFromTurn(turn).Citation,
				Text:        text,
				Marker:      marker,
				ShortTurn:   shortTurn,
				OccurredAt:  turn.OccurredAt,
			})
			if marker == "" ||
				turnIndex == 0 ||
				session.turns[turnIndex-1].Role != transcript.RoleAssistant {
				continue
			}

			start := turnIndex - 1
			first, last := conversationWindowBounds(
				project,
				session,
				start,
				turnIndex,
			)
			observations = append(observations, observation{
				detectorID:  issueintel.DetectorRepeatedCorrection,
				fingerprint: conversationCorrectionFingerprint(text),
				subject:     conversationCorrectionSubject(text),
				cost:        windowCost(session.turns, start, turnIndex),
				costSpans: []costSpan{
					windowSpan(session, start, turnIndex),
				},
				session:    sessionRef(session),
				firstSeen:  first,
				lastSeen:   last,
				occurredAt: conversationOccurredAt(project, session, turnIndex),
				excerpts: conversationExcerpts(
					session.turns,
					turnIndex-1,
					turnIndex,
					turnIndex+1,
				),
				fix: issueintel.SuggestedFix{
					Kind:       "instruction",
					TargetFile: instructionTarget(project),
					Rationale:  "Record the recurring correction as a durable project instruction.",
				},
			})
		}
	}
	return observations, candidates, nil
}

func detectDoneWithoutVerification(
	ctx context.Context,
	project preparedProject,
) ([]observation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var detected []conversationDoneObservation
	for sessionIndex, session := range project.sessions {
		if sessionIndex&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		for _, claimIndex := range conversationCompletionClaims(session.turns) {
			phaseStart := conversationTaskPhaseStart(
				session.turns,
				claimIndex,
			)
			lastEdit := conversationLastEditBetween(
				session.turns,
				phaseStart,
				claimIndex,
			)
			if lastEdit < 0 ||
				conversationHasVerificationBetween(
					session.turns,
					lastEdit+1,
					claimIndex,
					project.config,
				) {
				continue
			}
			first, last := conversationWindowBounds(
				project,
				session,
				lastEdit,
				claimIndex,
			)
			indexes := []int{lastEdit, claimIndex}
			if claimIndex+1 < len(session.turns) &&
				session.turns[claimIndex+1].Role ==
					transcript.RoleCompactionSummary {
				indexes = append(indexes, claimIndex+1)
				_, last = conversationWindowBounds(
					project,
					session,
					lastEdit,
					claimIndex+1,
				)
			}
			detected = append(detected, conversationDoneObservation{
				sessionIndex: sessionIndex,
				observation: observation{
					detectorID:  issueintel.DetectorDoneWithoutVerification,
					fingerprint: "completion-claim-without-verification",
					subject:     "completion",
					cost:        windowCost(session.turns, lastEdit, claimIndex),
					costSpans: []costSpan{
						windowSpan(session, lastEdit, claimIndex),
					},
					session:    sessionRef(session),
					firstSeen:  first,
					lastSeen:   last,
					occurredAt: conversationOccurredAt(project, session, claimIndex),
					excerpts:   conversationExcerpts(session.turns, indexes...),
					fix: issueintel.SuggestedFix{
						Kind:       "workflow",
						TargetFile: instructionTarget(project),
						Rationale:  "Require a recognized verification command after the final edit before claiming completion.",
					},
				},
			})
		}
	}

	for sessionIndex, session := range project.sessions {
		if sessionIndex&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		complaintIndex, phaseEnd, ok := conversationOpeningComplaint(session)
		if !ok {
			continue
		}
		target := -1
		for index := range detected {
			if detected[index].sessionIndex >= sessionIndex {
				continue
			}
			if target < 0 ||
				detected[index].observation.occurredAt.After(
					detected[target].observation.occurredAt,
				) {
				target = index
			}
		}
		if target < 0 {
			continue
		}
		detected[target].observation.excerpts = conversationMergeExcerpts(
			detected[target].observation.excerpts,
			conversationExcerpts(
				session.turns,
				complaintIndex,
				conversationFirstAssistant(
					session.turns,
					complaintIndex+1,
					phaseEnd,
				),
			),
		)
		if occurred := conversationOccurredAt(
			project,
			session,
			phaseEnd,
		); occurred.After(detected[target].observation.lastSeen) {
			detected[target].observation.lastSeen = occurred
		}
	}

	result := make([]observation, 0, len(detected))
	for _, value := range detected {
		result = append(result, value.observation)
	}
	return result, nil
}

func detectColdStartCost(
	ctx context.Context,
	project preparedProject,
) ([]observation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var starts []conversationColdStart
	var totalTurns, coldTurns int64
	var totalTokens, coldTokens int64
	var totalUSD, coldUSD float64
	for sessionIndex, session := range project.sessions {
		if sessionIndex&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		totalTurns += int64(len(session.turns))
		for _, turn := range session.turns {
			totalTokens += turnTokens(turn)
			if turn.CostUSD != nil {
				totalUSD += *turn.CostUSD
			}
		}

		boundary := conversationFirstCommandOrEdit(session.turns)
		prefixEnd := boundary - 1
		if boundary < 0 {
			prefixEnd = len(session.turns) - 1
		}
		if prefixEnd < 0 {
			continue
		}
		cost := windowCost(session.turns, 0, prefixEnd)
		starts = append(starts, conversationColdStart{
			session:       session,
			prefixEnd:     prefixEnd,
			boundaryIndex: boundary,
			cost:          cost,
		})
		coldTurns += int64(prefixEnd + 1)
		coldTokens += cost.WastedTokens
		if cost.WastedUSD != nil {
			coldUSD += *cost.WastedUSD
		}
	}

	if len(starts) < 5 ||
		!conversationOverShare(
			coldTurns,
			totalTurns,
			coldTokens,
			totalTokens,
			coldUSD,
			totalUSD,
		) {
		return nil, nil
	}

	result := make([]observation, 0, len(starts))
	for index, start := range starts {
		if index&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		first, last := conversationWindowBounds(
			project,
			start.session,
			0,
			start.prefixEnd,
		)
		indexes := []int{0, start.prefixEnd}
		if start.boundaryIndex >= 0 {
			indexes = append(indexes, start.boundaryIndex)
		}
		occurredIndex := start.prefixEnd
		if start.boundaryIndex >= 0 {
			occurredIndex = start.boundaryIndex
		}
		result = append(result, observation{
			detectorID:  issueintel.DetectorColdStartCost,
			fingerprint: "project-cold-start",
			subject:     "project startup",
			cost:        start.cost,
			costSpans: []costSpan{
				windowSpan(start.session, 0, start.prefixEnd),
			},
			session:   sessionRef(start.session),
			firstSeen: first,
			lastSeen:  last,
			occurredAt: conversationOccurredAt(
				project,
				start.session,
				occurredIndex,
			),
			excerpts: conversationExcerpts(start.session.turns, indexes...),
			fix: issueintel.SuggestedFix{
				Kind:       "instruction",
				TargetFile: instructionTarget(project),
				Rationale:  "Persist the project commands and orientation needed to begin useful work.",
			},
		})
	}
	return result, nil
}

func detectCompactionBeforeCompletion(
	ctx context.Context,
	project preparedProject,
) ([]observation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var result []observation
	for sessionIndex, session := range project.sessions {
		if sessionIndex&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		var qualifying []int
		for turnIndex, turn := range session.turns {
			if turnIndex&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if turn.Role != transcript.RoleCompactionSummary ||
				conversationHasSuccessfulVerification(
					session.turns,
					turnIndex+1,
					project.config,
				) {
				continue
			}
			qualifying = append(qualifying, turnIndex)
		}
		for qualifyingIndex, turnIndex := range qualifying {
			end := len(session.turns) - 1
			first, last := conversationWindowBounds(
				project,
				session,
				turnIndex,
				end,
			)
			indexes := []int{turnIndex - 1, turnIndex}
			if verification := conversationFirstVerification(
				session.turns,
				turnIndex+1,
				project.config,
			); verification >= 0 {
				indexes = append(indexes, verification)
			}
			indexes = append(indexes, end)
			cost := issueintel.Cost{}
			var spans []costSpan
			if qualifyingIndex == len(qualifying)-1 {
				cost = windowCost(session.turns, turnIndex, end)
				spans = []costSpan{
					windowSpan(session, turnIndex, end),
				}
			}
			result = append(result, observation{
				detectorID:  issueintel.DetectorCompactionBeforeCompletion,
				fingerprint: "compaction-without-successful-verification",
				subject:     "context compaction",
				cost:        cost,
				costSpans:   spans,
				session:     sessionRef(session),
				firstSeen:   first,
				lastSeen:    last,
				occurredAt:  conversationOccurredAt(project, session, turnIndex),
				excerpts:    conversationExcerpts(session.turns, indexes...),
				fix: issueintel.SuggestedFix{
					Kind:       "workflow",
					TargetFile: instructionTarget(project),
					Rationale:  "Preserve a concrete verification checkpoint across compaction and run it before ending the session.",
				},
			})
		}
	}
	return result, nil
}

func conversationCorrectionFingerprint(text string) string {
	normalized := strings.ToLower(spacePattern.ReplaceAllString(
		strings.TrimSpace(text),
		" ",
	))
	normalized = tempPathPattern.ReplaceAllString(normalized, "<tmp>")
	normalized = absolutePathPattern.ReplaceAllString(normalized, "<path>")
	normalized = hashPattern.ReplaceAllString(normalized, "<hash>")
	normalized = numberPattern.ReplaceAllString(normalized, "<n>")
	sum := sha256.Sum256([]byte(normalized))
	return "correction-" + hex.EncodeToString(sum[:16])
}

func conversationCorrectionSubject(text string) string {
	text = spacePattern.ReplaceAllString(strings.TrimSpace(text), " ")
	const limit = 96
	if len(text) > limit {
		text = strings.TrimSpace(text[:limit]) + "..."
	}
	return `"` + text + `"`
}

func conversationCompletionClaims(turns []transcript.Turn) []int {
	lastAssistant := -1
	for index := range turns {
		if turns[index].Role == transcript.RoleAssistant {
			lastAssistant = index
		}
	}
	seen := make(map[int]bool)
	var result []int
	if lastAssistant >= 0 && isCompletionClaim(turns[lastAssistant]) {
		seen[lastAssistant] = true
		result = append(result, lastAssistant)
	}
	for index := 0; index+1 < len(turns); index++ {
		if turns[index+1].Role == transcript.RoleCompactionSummary &&
			isCompletionClaim(turns[index]) &&
			!seen[index] {
			result = append(result, index)
		}
	}
	return result
}

func conversationTaskPhaseStart(
	turns []transcript.Turn,
	end int,
) int {
	if end > len(turns) {
		end = len(turns)
	}
	for index := end - 1; index >= 0; index-- {
		if turns[index].Role != transcript.RoleUser {
			continue
		}
		if isApprovalText(turns[index].Payload.Text) {
			continue
		}
		return index + 1
	}
	return 0
}

func conversationLastEditBetween(
	turns []transcript.Turn,
	start, end int,
) int {
	if start < 0 {
		start = 0
	}
	if end > len(turns) {
		end = len(turns)
	}
	for index := end - 1; index >= start; index-- {
		if len(editedFiles(turns[index])) > 0 {
			return index
		}
	}
	return -1
}

func conversationHasVerificationBetween(
	turns []transcript.Turn,
	start, end int,
	config issueintel.ProjectConfig,
) bool {
	if start < 0 {
		start = 0
	}
	if end > len(turns) {
		end = len(turns)
	}
	for index := start; index < end; index++ {
		if isVerificationTurn(turns[index], config) {
			return true
		}
	}
	return false
}

func conversationFirstVerification(
	turns []transcript.Turn,
	start int,
	config issueintel.ProjectConfig,
) int {
	if start < 0 {
		start = 0
	}
	for index := start; index < len(turns); index++ {
		if isVerificationTurn(turns[index], config) {
			return index
		}
	}
	return -1
}

func conversationHasSuccessfulVerification(
	turns []transcript.Turn,
	start int,
	config issueintel.ProjectConfig,
) bool {
	if start < 0 {
		start = 0
	}
	for index := start; index < len(turns); index++ {
		if !isVerificationTurn(turns[index], config) {
			continue
		}
		if conversationVerificationSucceeded(turns, index) {
			return true
		}
	}
	return false
}

func conversationVerificationSucceeded(
	turns []transcript.Turn,
	index int,
) bool {
	turn := turns[index]
	if succeeded, known := conversationTurnSucceeded(turn); known {
		return succeeded
	}
	if turn.Role != transcript.RoleToolCall {
		return false
	}
	callID := strings.TrimSpace(turn.Payload.ToolCallID)
	for next := index + 1; next < len(turns); next++ {
		candidate := turns[next]
		if candidate.Role == transcript.RoleUser {
			break
		}
		if candidate.Role != transcript.RoleToolResult {
			continue
		}
		candidateID := strings.TrimSpace(candidate.Payload.ToolCallID)
		if callID != "" && candidateID != callID {
			continue
		}
		if callID == "" &&
			candidate.ToolName != "" &&
			turn.ToolName != "" &&
			candidate.ToolName != turn.ToolName {
			continue
		}
		succeeded, known := conversationTurnSucceeded(candidate)
		return known && succeeded
	}
	return false
}

func conversationTurnSucceeded(turn transcript.Turn) (bool, bool) {
	known := false
	if turn.Payload.ToolIsError != nil {
		known = true
		if *turn.Payload.ToolIsError {
			return false, true
		}
	}
	if turn.Payload.ExitCode != nil {
		known = true
		if *turn.Payload.ExitCode != 0 {
			return false, true
		}
	}
	if known {
		return true, true
	}
	if strings.TrimSpace(turn.Payload.ToolResult) != "" {
		return !turnFailed(turn), true
	}
	return false, false
}

func conversationOpeningComplaint(
	session preparedSession,
) (int, int, bool) {
	firstUser := -1
	boundary := len(session.turns)
	for index, turn := range session.turns {
		if _, _, _, ok := commandInfo(turn, issueintel.ProjectConfig{}); ok ||
			len(editedFiles(turn)) > 0 {
			boundary = index
			break
		}
		if firstUser < 0 && turn.Role == transcript.RoleUser {
			firstUser = index
		}
	}
	if firstUser < 0 ||
		correctionMarker(session.turns[firstUser].Payload.Text) == "" {
		return 0, 0, false
	}
	phaseEnd := boundary - 1
	if phaseEnd < firstUser {
		phaseEnd = firstUser
	}
	return firstUser, phaseEnd, true
}

func conversationFirstAssistant(
	turns []transcript.Turn,
	start, end int,
) int {
	if start < 0 {
		start = 0
	}
	if end >= len(turns) {
		end = len(turns) - 1
	}
	for index := start; index <= end; index++ {
		if turns[index].Role == transcript.RoleAssistant {
			return index
		}
	}
	return -1
}

func conversationFirstCommandOrEdit(turns []transcript.Turn) int {
	for index, turn := range turns {
		if _, _, _, ok := commandInfo(turn, issueintel.ProjectConfig{}); ok ||
			len(editedFiles(turn)) > 0 {
			return index
		}
	}
	return -1
}

func conversationOverShare(
	coldTurns, totalTurns int64,
	coldTokens, totalTokens int64,
	coldUSD, totalUSD float64,
) bool {
	if totalUSD > 0 &&
		coldUSD*100 > totalUSD*coldStartSharePercent {
		return true
	}
	if totalUSD > 0 {
		return false
	}
	if totalTokens > 0 {
		return coldTokens*100 > totalTokens*coldStartSharePercent
	}
	return totalTurns > 0 &&
		coldTurns*100 > totalTurns*coldStartSharePercent
}

func conversationExcerpts(
	turns []transcript.Turn,
	indexes ...int,
) []issueintel.Excerpt {
	seen := make(map[int]bool)
	result := make([]issueintel.Excerpt, 0, maxExcerpts)
	for _, index := range indexes {
		if index < 0 || index >= len(turns) ||
			seen[index] || len(result) == maxExcerpts {
			continue
		}
		seen[index] = true
		excerpt := excerptFromTurn(turns[index])
		if strings.TrimSpace(excerpt.Text) == "" {
			continue
		}
		result = append(result, excerpt)
	}
	return result
}

func conversationMergeExcerpts(
	existing []issueintel.Excerpt,
	values []issueintel.Excerpt,
) []issueintel.Excerpt {
	seen := make(map[string]bool, len(existing)+len(values))
	result := make([]issueintel.Excerpt, 0, maxExcerpts)
	appendValue := func(value issueintel.Excerpt) {
		key := value.Citation.SessionKey + "\x00" +
			value.Citation.OccurredAt.UTC().Format(time.RFC3339Nano) +
			"\x00" + value.Text
		if strings.TrimSpace(value.Text) == "" ||
			seen[key] || len(result) == maxExcerpts {
			return
		}
		seen[key] = true
		result = append(result, value)
	}
	for _, value := range existing {
		appendValue(value)
	}
	for _, value := range values {
		appendValue(value)
	}
	return result
}

func conversationWindowBounds(
	project preparedProject,
	session preparedSession,
	start, end int,
) (time.Time, time.Time) {
	if start < 0 {
		start = 0
	}
	if end >= len(session.turns) {
		end = len(session.turns) - 1
	}
	var first, last time.Time
	if start <= end && start < len(session.turns) {
		first, last = observedBounds(session.turns[start : end+1]...)
	}
	if first.IsZero() {
		first = session.metadata.StartedAt
	}
	if first.IsZero() {
		first = project.now
	}
	if last.IsZero() {
		last = session.metadata.EndedAt
	}
	if last.IsZero() || last.Before(first) {
		last = first
	}
	return first, last
}

func conversationOccurredAt(
	project preparedProject,
	session preparedSession,
	index int,
) time.Time {
	if index >= 0 && index < len(session.turns) &&
		!session.turns[index].OccurredAt.IsZero() {
		return session.turns[index].OccurredAt
	}
	if !session.metadata.EndedAt.IsZero() {
		return session.metadata.EndedAt
	}
	if !session.metadata.StartedAt.IsZero() {
		return session.metadata.StartedAt
	}
	return project.now
}
