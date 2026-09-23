// Package transcriptissues derives deterministic, cost-ranked issues from
// encrypted local transcript turns supplied by the Local orchestration layer.
package transcriptissues

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

var issueIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func AnalyzeProject(
	ctx context.Context,
	input issueintel.ProjectInput,
) (issueintel.Analysis, error) {
	project, err := prepareProject(ctx, input)
	if err != nil {
		return issueintel.Analysis{}, err
	}
	var issues []issueintel.Issue
	var candidates []issueintel.CorrectionCandidate
	var allObservations []observation
	for _, scoped := range scopedProjects(project) {
		observations, scopedCandidates, err := analyzePreparedProject(
			ctx,
			scoped,
		)
		if err != nil {
			return issueintel.Analysis{}, err
		}
		allObservations = append(allObservations, observations...)
		candidates = append(candidates, scopedCandidates...)
		issues = append(issues, aggregateIssues(scoped, observations)...)
	}
	sortIssues(issues)
	return issueintel.Analysis{
		Issues:               issues,
		CorrectionCandidates: candidates,
		AttributedCost: attributedCost(
			project,
			observationCostSpans(allObservations),
			observationTimeSpans(allObservations),
		),
	}, nil
}

func analyzePreparedProject(
	ctx context.Context,
	project preparedProject,
) ([]observation, []issueintel.CorrectionCandidate, error) {
	detectors := []func(context.Context, preparedProject) ([]observation, error){
		detectRetryLoops,
		detectRecurringErrors,
		detectDoneWithoutVerification,
		detectPermissionChurn,
		detectColdStartCost,
		detectFileThrash,
		detectCompactionBeforeCompletion,
	}
	var observations []observation
	for _, detector := range detectors {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		values, err := detector(ctx, project)
		if err != nil {
			return nil, nil, err
		}
		observations = append(observations, values...)
	}
	corrections, candidates, err := detectRepeatedCorrections(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	observations = append(observations, corrections...)
	return observations, candidates, nil
}

func prepareProject(
	ctx context.Context,
	input issueintel.ProjectInput,
) (preparedProject, error) {
	input.Project.Identity = strings.TrimSpace(input.Project.Identity)
	input.Project.Path = strings.TrimSpace(input.Project.Path)
	if input.Project.Identity == "" || input.Now.IsZero() {
		return preparedProject{}, errors.New("invalid transcript issue project")
	}
	result := preparedProject{
		project:       input.Project,
		scopeIdentity: input.Project.Identity,
		config:        input.Config,
		now:           input.Now.UTC(),
	}
	totalTurns := 0
	for index, session := range input.Sessions {
		if index&31 == 0 {
			if err := ctx.Err(); err != nil {
				return preparedProject{}, err
			}
		}
		if session.Metadata.SessionKey == "" ||
			session.Metadata.ProjectIdentity != input.Project.Identity {
			return preparedProject{}, errors.New("invalid transcript issue session")
		}
		turns := append([]transcript.Turn(nil), session.Turns...)
		sort.SliceStable(turns, func(i, j int) bool {
			if turns[i].TurnIndex != turns[j].TurnIndex {
				return turns[i].TurnIndex < turns[j].TurnIndex
			}
			if !turns[i].OccurredAt.Equal(turns[j].OccurredAt) {
				return turns[i].OccurredAt.Before(turns[j].OccurredAt)
			}
			return turns[i].TurnID < turns[j].TurnID
		})
		totalTurns += len(turns)
		if totalTurns > maxProjectTurns {
			return preparedProject{}, errors.New("transcript issue project turn limit exceeded")
		}
		result.sessions = append(result.sessions, preparedSession{
			metadata: session.Metadata,
			turns:    turns,
		})
	}
	sort.SliceStable(result.sessions, func(i, j int) bool {
		left, right := result.sessions[i].metadata, result.sessions[j].metadata
		if !left.StartedAt.Equal(right.StartedAt) {
			return left.StartedAt.Before(right.StartedAt)
		}
		return left.SessionKey < right.SessionKey
	})
	return result, nil
}

func aggregateIssues(
	project preparedProject,
	observations []observation,
) []issueintel.Issue {
	groups := make(map[string]*observationGroup)
	for _, value := range observations {
		if value.detectorID == "" || value.fingerprint == "" ||
			value.session.SessionKey == "" || value.firstSeen.IsZero() {
			continue
		}
		key := value.detectorID + "\x00" + value.fingerprint
		group := groups[key]
		if group == nil {
			group = &observationGroup{
				detectorID:  value.detectorID,
				fingerprint: value.fingerprint,
				subject:     value.subject,
				sessions:    make(map[string]issueintel.SessionRef),
				firstSeen:   value.firstSeen,
				lastSeen:    value.lastSeen,
				fix:         value.fix,
			}
			groups[key] = group
		}
		group.cost.WastedMinutes += value.cost.WastedMinutes
		group.cost.WastedTokens += value.cost.WastedTokens
		group.cost.LowerBound = group.cost.LowerBound || value.cost.LowerBound
		if value.cost.WastedUSD != nil {
			if group.cost.WastedUSD == nil {
				zero := 0.0
				group.cost.WastedUSD = &zero
			}
			*group.cost.WastedUSD += *value.cost.WastedUSD
			group.usdKnown = true
		} else {
			group.cost.LowerBound = true
		}
		group.costSpans = append(group.costSpans, value.costSpans...)
		group.timeSpans = append(group.timeSpans, value.timeSpans...)
		group.sessions[value.session.SessionKey] = value.session
		for _, span := range value.costSpans {
			group.sessions[span.session.SessionKey] = span.session
		}
		if value.firstSeen.Before(group.firstSeen) {
			group.firstSeen = value.firstSeen
		}
		if value.lastSeen.After(group.lastSeen) {
			group.lastSeen = value.lastSeen
		}
		occurredAt := value.occurredAt
		if occurredAt.IsZero() {
			occurredAt = value.firstSeen
		}
		group.occurrences = append(group.occurrences, occurredAt.UTC())
		group.excerpts = mergeExcerpts(group.excerpts, value.excerpts)
	}

	result := make([]issueintel.Issue, 0, len(groups))
	for _, group := range groups {
		if len(group.excerpts) < 2 {
			continue
		}
		if len(group.costSpans) > 0 {
			group.cost = attributedCost(
				project,
				group.costSpans,
				group.timeSpans,
			)
			group.usdKnown = group.cost.WastedUSD != nil
		}
		if !group.usdKnown {
			group.cost.WastedUSD = nil
		}
		sessions := make([]issueintel.SessionRef, 0, len(group.sessions))
		for _, session := range group.sessions {
			sessions = append(sessions, session)
		}
		sort.Slice(sessions, func(i, j int) bool {
			if !sessions[i].StartedAt.Equal(sessions[j].StartedAt) {
				return sessions[i].StartedAt.Before(sessions[j].StartedAt)
			}
			return sessions[i].SessionKey < sessions[j].SessionKey
		})
		result = append(result, issueintel.Issue{
			IssueID: issueID(
				effectiveScopeIdentity(project),
				group.detectorID,
				group.fingerprint,
			),
			DetectorID:  group.detectorID,
			Fingerprint: scopedFingerprint(project, group.fingerprint),
			Headline: headline(
				group.detectorID,
				group.subject,
				len(group.occurrences),
				len(sessions),
			),
			Cost:         group.cost,
			SessionCount: len(sessions),
			Sessions:     sessions,
			FirstSeen:    group.firstSeen,
			LastSeen:     group.lastSeen,
			Trend:        buildTrend(project.now, group.occurrences),
			Excerpts:     group.excerpts,
			Project:      project.project,
			SuggestedFix: group.fix,
		})
	}
	sortIssues(result)
	return result
}

func sortIssues(result []issueintel.Issue) {
	sort.Slice(result, func(i, j int) bool {
		leftUSD, rightUSD := result[i].Cost.WastedUSD, result[j].Cost.WastedUSD
		if (leftUSD != nil) != (rightUSD != nil) {
			return leftUSD != nil
		}
		if leftUSD != nil && *leftUSD != *rightUSD {
			return *leftUSD > *rightUSD
		}
		if result[i].SessionCount != result[j].SessionCount {
			return result[i].SessionCount > result[j].SessionCount
		}
		if !result[i].LastSeen.Equal(result[j].LastSeen) {
			return result[i].LastSeen.After(result[j].LastSeen)
		}
		return result[i].IssueID < result[j].IssueID
	})
}

func observationCostSpans(observations []observation) []costSpan {
	var spans []costSpan
	for _, value := range observations {
		spans = append(spans, value.costSpans...)
	}
	return spans
}

func observationTimeSpans(observations []observation) []costSpan {
	var spans []costSpan
	for _, value := range observations {
		spans = append(spans, value.timeSpans...)
	}
	return spans
}

func scopedFingerprint(project preparedProject, fingerprint string) string {
	scope := effectiveScopeIdentity(project)
	if scope == project.project.Identity {
		return fingerprint
	}
	return scope + "\x00" + fingerprint
}

func effectiveScopeIdentity(project preparedProject) string {
	if project.scopeIdentity != "" {
		return project.scopeIdentity
	}
	return project.project.Identity
}

func issueID(project, detector, fingerprint string) string {
	sum := sha256.Sum256([]byte(project + "\x00" + detector + "\x00" + fingerprint))
	return "csi_" + strings.ToLower(issueIDEncoding.EncodeToString(sum[:]))
}

func headline(detector, subject string, occurrences, sessions int) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		subject = "the same pattern"
	}
	switch detector {
	case issueintel.DetectorRetryLoop:
		return fmt.Sprintf("%s failed %d times across %d sessions.", subject, occurrences, sessions)
	case issueintel.DetectorRecurringError:
		return fmt.Sprintf("%s recurred %d times across %d sessions.", subject, occurrences, sessions)
	case issueintel.DetectorRepeatedCorrection:
		return fmt.Sprintf(
			"You've corrected the agent about %s %d times across %d sessions. Nothing in CLAUDE.md or AGENTS.md says this.",
			subject,
			occurrences,
			sessions,
		)
	case issueintel.DetectorDoneWithoutVerification:
		return fmt.Sprintf("The agent claimed completion without verification %d times across %d sessions.", occurrences, sessions)
	case issueintel.DetectorPermissionChurn:
		return fmt.Sprintf("You approved %s %d times across %d sessions.", subject, occurrences, sessions)
	case issueintel.DetectorColdStartCost:
		return fmt.Sprintf("Cold starts consumed more than 15%% of spend across %d sessions.", sessions)
	case issueintel.DetectorFileThrash:
		return fmt.Sprintf("%s was repeatedly edited %d times across %d sessions.", subject, occurrences, sessions)
	case issueintel.DetectorFileReversal:
		return fmt.Sprintf("%s returned to an earlier edit state across %d sessions.", subject, sessions)
	case issueintel.DetectorCompactionBeforeCompletion:
		return fmt.Sprintf("Context compacted before verified completion in %d sessions.", sessions)
	default:
		return fmt.Sprintf("%s occurred %d times across %d sessions.", subject, occurrences, sessions)
	}
}

func buildTrend(now time.Time, occurrences []time.Time) []issueintel.TrendWeek {
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
	return time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
}

func mergeExcerpts(
	existing []issueintel.Excerpt,
	values []issueintel.Excerpt,
) []issueintel.Excerpt {
	seen := make(map[string]bool, len(existing)+len(values))
	result := make([]issueintel.Excerpt, 0, maxExcerpts)
	appendValue := func(value issueintel.Excerpt) {
		key := fmt.Sprintf(
			"%s\x00%d\x00%s",
			value.Citation.SessionKey,
			value.Citation.TurnIndex,
			value.Text,
		)
		if value.Text == "" || seen[key] || len(result) == maxExcerpts {
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
