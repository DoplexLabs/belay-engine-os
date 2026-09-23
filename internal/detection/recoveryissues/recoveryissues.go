// Package recoveryissues projects strict failed-approach recoveries into
// cost-ranked issues. It consumes only persisted-contract episodes already
// accepted by the deterministic experience candidate compiler.
package recoveryissues

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/evidenceepisode"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/trajectory"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

var issueIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type occurrence struct {
	fingerprint string
	subject     string
	episode     evidenceepisode.Episode
	session     issueintel.SessionRef
	cost        issueintel.Cost
	excerpts    []issueintel.Excerpt
}

// Analyze creates one issue per stable failure/repair fingerprint. It does
// not discover recoveries itself; candidatecompiler remains the single strict
// qualification boundary.
func Analyze(
	project issueintel.Project,
	sessions []issueintel.Session,
	episodes []evidenceepisode.Episode,
	now time.Time,
) []issueintel.Issue {
	sessionByKey := make(map[string]issueintel.Session, len(sessions))
	for _, session := range sessions {
		sessionByKey[session.Metadata.SessionKey] = session
	}

	grouped := make(map[string][]occurrence)
	for _, episode := range episodes {
		if episode.Validate() != nil ||
			episode.Kind != evidenceepisode.KindFailureRepair {
			continue
		}
		session, ok := sessionByKey[episode.SessionKey]
		if !ok || episode.ProjectIdentity != project.Identity {
			continue
		}
		fingerprint := strings.Join(
			[]string{
				project.Identity,
				episode.FailureSignature,
				episode.RepairFamily,
				episode.SuccessfulCommandSignature,
			},
			"\x00",
		)
		excerpts := episodeExcerpts(session.Turns, episode)
		if len(excerpts) == 0 {
			continue
		}
		grouped[fingerprint] = append(grouped[fingerprint], occurrence{
			fingerprint: fingerprint,
			subject:     episode.FailureSignature,
			episode:     episode,
			session: issueintel.SessionRef{
				SessionKey: session.Metadata.SessionKey,
				Agent:      session.Metadata.Agent,
				StartedAt:  session.Metadata.StartedAt,
				EndedAt:    session.Metadata.EndedAt,
			},
			cost:     episode.Cost,
			excerpts: excerpts,
		})
	}

	result := make([]issueintel.Issue, 0, len(grouped))
	for fingerprint, values := range grouped {
		sort.Slice(values, func(i, j int) bool {
			return values[i].episode.EndedAt.Before(
				values[j].episode.EndedAt,
			)
		})
		first, last := values[0], values[len(values)-1]
		sessionsByKey := make(map[string]issueintel.SessionRef)
		var cost issueintel.Cost
		var excerpts []issueintel.Excerpt
		var occurrences []time.Time
		var episodeRefs []string
		for _, value := range values {
			sessionsByKey[value.session.SessionKey] = value.session
			cost = mergeCost(cost, value.cost)
			excerpts = mergeExcerpts(excerpts, value.excerpts)
			occurrences = append(
				occurrences,
				value.episode.EndedAt,
			)
			episodeRefs = append(episodeRefs, value.episode.EpisodeID)
		}
		sessionRefs := make([]issueintel.SessionRef, 0, len(sessionsByKey))
		for _, session := range sessionsByKey {
			sessionRefs = append(sessionRefs, session)
		}
		sort.Slice(sessionRefs, func(i, j int) bool {
			if !sessionRefs[i].StartedAt.Equal(sessionRefs[j].StartedAt) {
				return sessionRefs[i].StartedAt.Before(sessionRefs[j].StartedAt)
			}
			return sessionRefs[i].SessionKey < sessionRefs[j].SessionKey
		})
		result = append(result, issueintel.Issue{
			IssueID:     issueID(fingerprint),
			DetectorID:  issueintel.DetectorFailureRepaired,
			Fingerprint: fingerprint,
			Headline: fmt.Sprintf(
				"`%s` was resolved by a different command%s.",
				first.subject,
				sessionSuffix(len(sessionRefs)),
			),
			Cost:         cost,
			SessionCount: len(sessionRefs),
			Sessions:     sessionRefs,
			FirstSeen:    first.episode.StartedAt,
			LastSeen:     last.episode.EndedAt,
			Trend:        buildTrend(now, occurrences),
			Excerpts:     excerpts,
			EpisodeRefs:  normalizedEpisodeRefs(episodeRefs),
			Project:      project,
			SuggestedFix: issueintel.SuggestedFix{
				Kind:       "harness_rule",
				TargetFile: instructionTarget(sessionRefs),
				Rationale:  "Preserve the successful recovery procedure so the next agent can apply it before repeating the failed approach.",
			},
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i].Cost.WastedUSD, result[j].Cost.WastedUSD
		if (left != nil) != (right != nil) {
			return left != nil
		}
		if left != nil && *left != *right {
			return *left > *right
		}
		if result[i].SessionCount != result[j].SessionCount {
			return result[i].SessionCount > result[j].SessionCount
		}
		return result[i].IssueID < result[j].IssueID
	})
	return result
}

func episodeExcerpts(
	turns []transcript.Turn,
	episode evidenceepisode.Episode,
) []issueintel.Excerpt {
	byIndex := make(map[int64]transcript.Turn)
	ambiguous := make(map[int64]bool)
	for _, turn := range turns {
		if turn.SessionKey != episode.SessionKey {
			continue
		}
		if _, exists := byIndex[turn.TurnIndex]; exists {
			delete(byIndex, turn.TurnIndex)
			ambiguous[turn.TurnIndex] = true
			continue
		}
		if !ambiguous[turn.TurnIndex] {
			byIndex[turn.TurnIndex] = turn
		}
	}
	var result []issueintel.Excerpt
	for _, ref := range episode.SourceRefs {
		if ref.Kind != trajectory.NodeTranscriptTurn ||
			ref.TurnIndex == nil ||
			ref.SessionKey != episode.SessionKey ||
			ambiguous[*ref.TurnIndex] {
			continue
		}
		turn, ok := byIndex[*ref.TurnIndex]
		if ok {
			result = append(result, excerpt(turn))
		}
	}
	return result
}

func excerpt(turn transcript.Turn) issueintel.Excerpt {
	text := strings.TrimSpace(turn.Payload.Text)
	if turn.Role == transcript.RoleToolCall {
		text = strings.TrimSpace(turn.Payload.RawCommand)
		if text == "" {
			text = strings.TrimSpace(string(turn.Payload.ToolInput))
		}
	}
	if turn.Role == transcript.RoleToolResult {
		text = strings.TrimSpace(turn.Payload.ToolResult)
	}
	return issueintel.Excerpt{
		Citation: issueintel.Citation{
			SessionKey:      turn.SessionKey,
			TurnIndex:       turn.TurnIndex,
			OccurredAt:      turn.OccurredAt,
			SourceFileID:    turn.Payload.SourceFileID,
			JSONLByteOffset: turn.Payload.JSONLByteOffset,
		},
		Role:     turn.Role,
		ToolName: turn.ToolName,
		Text:     text,
	}
}

func mergeCost(left, right issueintel.Cost) issueintel.Cost {
	left.WastedMinutes += right.WastedMinutes
	left.WastedTokens += right.WastedTokens
	left.LowerBound = left.LowerBound || right.LowerBound
	if right.WastedUSD == nil {
		if right.WastedTokens > 0 {
			left.LowerBound = true
		}
		return left
	}
	if left.WastedUSD == nil {
		zero := 0.0
		left.WastedUSD = &zero
	}
	*left.WastedUSD += *right.WastedUSD
	return left
}

func mergeExcerpts(
	left []issueintel.Excerpt,
	right []issueintel.Excerpt,
) []issueintel.Excerpt {
	seen := make(map[string]bool)
	result := make([]issueintel.Excerpt, 0, 5)
	for _, values := range [][]issueintel.Excerpt{left, right} {
		for _, value := range values {
			key := fmt.Sprintf(
				"%s\x00%d\x00%s",
				value.Citation.SessionKey,
				value.Citation.TurnIndex,
				value.Text,
			)
			if value.Text == "" || seen[key] || len(result) == 5 {
				continue
			}
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func normalizedEpisodeRefs(values []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func issueID(fingerprint string) string {
	sum := sha256.Sum256([]byte(fingerprint))
	return "csi_" + strings.ToLower(issueIDEncoding.EncodeToString(sum[:]))
}

func sessionSuffix(count int) string {
	if count <= 1 {
		return ""
	}
	return fmt.Sprintf(" across %d sessions", count)
}

func instructionTarget(sessions []issueintel.SessionRef) string {
	claude := 0
	for _, session := range sessions {
		if session.Agent == "claude-code" {
			claude++
		}
	}
	if claude*2 >= len(sessions) {
		return "CLAUDE.md"
	}
	return "AGENTS.md"
}

func buildTrend(
	now time.Time,
	occurrences []time.Time,
) []issueintel.TrendWeek {
	current := weekStart(now)
	result := make([]issueintel.TrendWeek, 8)
	indexByWeek := make(map[time.Time]int, 8)
	for index := range result {
		start := current.AddDate(0, 0, -7*(7-index))
		result[index].WeekStart = start
		indexByWeek[start] = index
	}
	for _, value := range occurrences {
		if index, ok := indexByWeek[weekStart(value)]; ok {
			result[index].Count++
		}
	}
	return result
}

func weekStart(value time.Time) time.Time {
	value = value.UTC()
	daysSinceMonday := (int(value.Weekday()) + 6) % 7
	start := value.AddDate(0, 0, -daysSinceMonday)
	return time.Date(
		start.Year(),
		start.Month(),
		start.Day(),
		0,
		0,
		0,
		0,
		time.UTC,
	)
}
