// Package userinsights turns one retained transcript session into a
// plain-language debrief for the human operator. It is deliberately separate
// from issue detection, Mission Packs, and experience learning: nothing here
// feeds the agent, and nothing here is derived from other users. Every
// finding is computed deterministically from the session's own turns, and the
// only comparison is against the same developer's other sessions in the same
// project.
package userinsights

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/detection/transcriptissues"
	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	// AnalysisVersion changes whenever a rule, threshold, or piece of copy
	// changes so consumers can tell retros apart.
	AnalysisVersion = "belay.user-insights-analysis.v1"

	FindingLateVerification    = "late_verification"
	FindingNoVerification      = "no_verification"
	FindingLongPlanning        = "long_planning"
	FindingRepeatedCorrections = "repeated_corrections"
	FindingContextCompaction   = "context_compaction"
	FindingChecksNamedUpFront  = "checks_named_up_front"
	ToneImprove                = "improve"
	ToneKeep                   = "keep"
	EvidenceMeasurement        = "measurement"
	EvidenceJudgment           = "judgment"
	OutcomeVerifiedPass        = "verified_pass"
	OutcomeVerifiedFail        = "verified_fail"
	OutcomeUnverifiedChanges   = "unverified_changes"
	OutcomeNoChanges           = "no_changes"
	ComparisonLonger           = "longer"
	ComparisonShorter          = "shorter"
	ComparisonTypical          = "typical"
	ComparisonInsufficientData = "insufficient_data"
	minBaselineSessions        = 3
	minBaselineUserTurns       = 3
	longerRatio                = 1.6
	shorterRatio               = 0.6
	longPlanningShare          = 0.35
	longPlanningMinUserTurns   = 5
	longPlanningMinDuration    = 10 * time.Minute
	noVerificationMinChanges   = 3
	repeatedCorrectionMinimum  = 3
	compactionMinimum          = 2
	maxFindingsPerSession      = 3
)

var checksNamedPattern = regexp.MustCompile(
	`(?i)\b(tests?|testing|verify|verification|validate|validation|lint|linter|typecheck|type-check|typechecks|build passes|make verify|go test|npm test|pytest|cargo test)\b`,
)

// Measurements are the deterministic facts one retro is built from. They are
// exposed so the browser can show "how Belay knows" without recomputing.
type Measurements struct {
	UserTurns                          int        `json:"user_turns"`
	AssistantTurns                     int        `json:"assistant_turns"`
	Corrections                        int        `json:"corrections"`
	FirstPromptChars                   int        `json:"first_prompt_chars"`
	FirstPromptNamesChecks             bool       `json:"first_prompt_names_checks"`
	FileChangeTurns                    int        `json:"file_change_turns"`
	FirstChangeAt                      *time.Time `json:"first_change_at"`
	UserTurnsBeforeFirstChange         int        `json:"user_turns_before_first_change"`
	PlanningDurationMS                 int64      `json:"planning_duration_ms"`
	PlanningCostUSD                    *float64   `json:"planning_cost_usd"`
	PlanningShare                      *float64   `json:"planning_share"`
	VerificationRuns                   int        `json:"verification_runs"`
	VerificationPasses                 int        `json:"verification_passes"`
	VerificationFailures               int        `json:"verification_failures"`
	FirstVerificationAt                *time.Time `json:"first_verification_at"`
	CorrectionsBeforeFirstVerification int        `json:"corrections_before_first_verification"`
	ChangesBeforeFirstVerificationMS   int64      `json:"changes_before_first_verification_ms"`
	LastVerificationPassed             *bool      `json:"last_verification_passed"`
	Compactions                        int        `json:"compactions"`
	DurationMS                         int64      `json:"duration_ms"`
	CostUSD                            *float64   `json:"cost_usd"`
}

// Finding is one plain-language observation about how the human drove the
// session. Titles and summaries never contain raw commands, paths, or prose
// from the transcript.
type Finding struct {
	Kind          string   `json:"kind"`
	Tone          string   `json:"tone"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary"`
	NextTime      string   `json:"next_time"`
	EvidenceClass string   `json:"evidence_class"`
	Evidence      []string `json:"evidence"`
	TimeCostMS    *int64   `json:"time_cost_ms"`
	DollarCost    *float64 `json:"dollar_cost"`
}

// Baseline compares one session with the same developer's other complete
// sessions in the same project. It is nil until enough sessions exist.
type Baseline struct {
	SessionCount     int      `json:"session_count"`
	MedianDurationMS int64    `json:"median_duration_ms"`
	MedianCostUSD    *float64 `json:"median_cost_usd"`
	DurationRatio    *float64 `json:"duration_ratio"`
	Comparison       string   `json:"comparison"`
}

// Retro is the complete debrief for one session.
type Retro struct {
	AnalysisVersion string       `json:"analysis_version"`
	SessionKey      string       `json:"session_key"`
	Agent           string       `json:"agent"`
	StartedAt       time.Time    `json:"started_at"`
	EndedAt         time.Time    `json:"ended_at"`
	DurationMS      int64        `json:"duration_ms"`
	CostUSD         *float64     `json:"cost_usd"`
	Outcome         string       `json:"outcome"`
	Verdict         string       `json:"verdict"`
	Findings        []Finding    `json:"findings"`
	Opener          string       `json:"opener"`
	Measurements    Measurements `json:"measurements"`
	Baseline        *Baseline    `json:"baseline"`
}

// Analyze computes the deterministic measurements for one session. Turns
// must be in transcript order. The project configuration only widens the set
// of recognized verification commands; an empty value is valid.
func Analyze(
	session transcript.Session,
	turns []transcript.Turn,
	config issueintel.ProjectConfig,
) Measurements {
	return AnalyzeSession(session, turns, nil, config)
}

// AnalyzeSession is Analyze with the session's canonical Numbat events, which
// carry file writes that some harnesses do not expose as edit tools (Codex
// apply_patch, shell redirections). Events may be nil.
func AnalyzeSession(
	session transcript.Session,
	turns []transcript.Turn,
	events []model.Event,
	config issueintel.ProjectConfig,
) Measurements {
	result := Measurements{
		Compactions: session.CompactionSummaryCount,
		CostUSD:     copyFloat(session.TotalCostUSD),
	}
	if len(turns) == 0 {
		result.DurationMS = sessionDuration(session, nil)
		return result
	}
	results := make(map[string][]transcript.Turn)
	for _, turn := range turns {
		if turn.Role != transcript.RoleToolResult {
			continue
		}
		id := strings.TrimSpace(turn.Payload.ToolCallID)
		if id != "" {
			results[id] = append(results[id], turn)
		}
	}
	if session.CompactionSummaryCount == 0 {
		for _, turn := range turns {
			if turn.Role == transcript.RoleCompactionSummary {
				result.Compactions++
			}
		}
	}

	mutationCalls := make(map[string]bool)
	unmatchedMutations := 0
	var firstMutationEvent *time.Time
	for _, event := range events {
		if event.Observation.Type != "file.write" && event.Observation.Type != "file.delete" {
			continue
		}
		if !event.OccurredAt.IsZero() && (firstMutationEvent == nil || event.OccurredAt.Before(*firstMutationEvent)) {
			at := event.OccurredAt
			firstMutationEvent = &at
		}
		if event.Observation.Details != nil && strings.TrimSpace(event.Observation.Details.ToolCallID) != "" {
			mutationCalls[strings.TrimSpace(event.Observation.Details.ToolCallID)] = true
		} else {
			unmatchedMutations++
		}
	}
	var firstChange *time.Time
	var firstVerification *time.Time
	var planningCost float64
	planningCostKnown := false
	firstPromptSeen := false
	previousWasBehavior := false
	for _, turn := range turns {
		switch turn.Role {
		case transcript.RoleUser:
			text := strings.TrimSpace(turn.Payload.Text)
			if text == "" ||
				transcriptissues.IsMachineGeneratedEnvelope(text) ||
				strings.TrimSpace(turn.Payload.ParentToolUseID) != "" {
				continue
			}
			result.UserTurns++
			if !firstPromptSeen {
				firstPromptSeen = true
				result.FirstPromptChars = len([]rune(text))
				result.FirstPromptNamesChecks = checksNamedPattern.MatchString(text)
			}
			if firstChange == nil {
				result.UserTurnsBeforeFirstChange++
			}
			if previousWasBehavior &&
				transcriptissues.HighConfidenceCorrectionMarker(text) != "" {
				result.Corrections++
				if firstVerification == nil {
					result.CorrectionsBeforeFirstVerification++
				}
			}
			previousWasBehavior = false
		case transcript.RoleAssistant:
			result.AssistantTurns++
			previousWasBehavior = true
		case transcript.RoleToolResult:
			previousWasBehavior = true
		case transcript.RoleToolCall:
			previousWasBehavior = true
			if len(editedFilesForTurn(turn)) > 0 || mutationCalls[strings.TrimSpace(turn.Payload.ToolCallID)] {
				result.FileChangeTurns++
				if firstChange == nil {
					at := turn.OccurredAt
					firstChange = &at
				}
			}
			if _, _, ok := transcriptissues.RetainedVerificationCommand(
				turn,
				config,
			); ok {
				result.VerificationRuns++
				if firstVerification == nil {
					at := turn.OccurredAt
					firstVerification = &at
				}
				id := strings.TrimSpace(turn.Payload.ToolCallID)
				if id != "" && len(results[id]) == 1 {
					failed, known := transcriptissues.ExplicitToolResultFailed(
						results[id][0],
					)
					if known {
						passed := !failed
						result.LastVerificationPassed = &passed
						if failed {
							result.VerificationFailures++
						} else {
							result.VerificationPasses++
						}
					}
				}
			}
		}
		if firstChange == nil && turn.CostUSD != nil {
			planningCost += *turn.CostUSD
			planningCostKnown = true
		}
	}

	if unmatchedMutations > 0 && result.FileChangeTurns == 0 {
		result.FileChangeTurns = unmatchedMutations
		if firstChange == nil && firstMutationEvent != nil {
			firstChange = firstMutationEvent
		}
	}
	result.DurationMS = sessionDuration(session, turns)
	result.FirstChangeAt = firstChange
	result.FirstVerificationAt = firstVerification
	if firstChange != nil {
		start := turns[0].OccurredAt
		if !start.IsZero() && !firstChange.Before(start) {
			result.PlanningDurationMS = firstChange.Sub(start).Milliseconds()
		}
		if planningCostKnown {
			result.PlanningCostUSD = &planningCost
		}
		if result.DurationMS > 0 {
			share := float64(result.PlanningDurationMS) / float64(result.DurationMS)
			if share > 1 {
				share = 1
			}
			result.PlanningShare = &share
		}
		if firstVerification != nil && firstVerification.After(*firstChange) {
			result.ChangesBeforeFirstVerificationMS = firstVerification.Sub(*firstChange).Milliseconds()
		}
	}
	return result
}

// ComputeBaseline builds the within-developer comparison from the other
// complete sessions in the same project. It never looks outside the project
// and returns nil below the minimum sample size.
func ComputeBaseline(
	session transcript.Session,
	measurements Measurements,
	others []transcript.Session,
) *Baseline {
	durations := make([]int64, 0, len(others))
	costs := make([]float64, 0, len(others))
	for _, other := range others {
		if other.SessionKey == session.SessionKey ||
			other.ProjectIdentity != session.ProjectIdentity ||
			strings.TrimSpace(other.ProjectIdentity) == "" ||
			other.Coverage != transcript.CoverageComplete ||
			other.UserTurnCount < minBaselineUserTurns {
			continue
		}
		duration := sessionDuration(other, nil)
		if duration <= 0 {
			continue
		}
		durations = append(durations, duration)
		if other.TotalCostUSD != nil {
			costs = append(costs, *other.TotalCostUSD)
		}
	}
	if len(durations) < minBaselineSessions {
		return nil
	}
	baseline := &Baseline{
		SessionCount:     len(durations),
		MedianDurationMS: medianInt64(durations),
		Comparison:       ComparisonInsufficientData,
	}
	if len(costs) >= minBaselineSessions {
		median := medianFloat64(costs)
		baseline.MedianCostUSD = &median
	}
	if baseline.MedianDurationMS > 0 && measurements.DurationMS > 0 {
		ratio := float64(measurements.DurationMS) / float64(baseline.MedianDurationMS)
		baseline.DurationRatio = &ratio
		switch {
		case ratio >= longerRatio:
			baseline.Comparison = ComparisonLonger
		case ratio <= shorterRatio:
			baseline.Comparison = ComparisonShorter
		default:
			baseline.Comparison = ComparisonTypical
		}
	}
	return baseline
}

// BuildRetro assembles the debrief. Findings are ordered by how much time
// they explain and capped so the page stays readable.
func BuildRetro(
	session transcript.Session,
	measurements Measurements,
	baseline *Baseline,
) Retro {
	retro := Retro{
		AnalysisVersion: AnalysisVersion,
		SessionKey:      strings.TrimSpace(session.SessionKey),
		Agent:           strings.TrimSpace(session.Agent),
		StartedAt:       session.StartedAt.UTC(),
		EndedAt:         session.EndedAt.UTC(),
		DurationMS:      measurements.DurationMS,
		CostUSD:         copyFloat(measurements.CostUSD),
		Outcome:         outcome(measurements),
		Measurements:    measurements,
		Baseline:        baseline,
	}
	retro.Findings = findings(measurements)
	retro.Verdict = verdict(retro.Outcome, retro.Findings, baseline)
	retro.Opener = opener(retro.Findings)
	return retro
}

func findings(m Measurements) []Finding {
	var result []Finding
	if m.FileChangeTurns >= noVerificationMinChanges && m.VerificationRuns == 0 {
		result = append(result, Finding{
			Kind:  FindingNoVerification,
			Tone:  ToneImprove,
			Title: "No checks were run",
			Summary: "The agent changed files, but no test, build, lint, or " +
				"typecheck ran before the session ended. Whatever it got wrong " +
				"is still waiting for you.",
			NextTime: "Say in your first message which checks have to pass, " +
				"and ask for them to be re-run after each change.",
			EvidenceClass: EvidenceMeasurement,
			Evidence: []string{
				plural(m.FileChangeTurns, "file-editing step", "file-editing steps") +
					" ran and no recognized verification command followed.",
			},
		})
	} else if m.VerificationRuns > 0 &&
		m.FileChangeTurns > 0 &&
		m.CorrectionsBeforeFirstVerification > 0 {
		gap := m.ChangesBeforeFirstVerificationMS
		finding := Finding{
			Kind:  FindingLateVerification,
			Tone:  ToneImprove,
			Title: "Testing came up as an afterthought",
			Summary: "You let the agent build first and had to step in before " +
				"any check ran. It then went back over work it had already " +
				"called finished.",
			NextTime: "Say what has to be proven before any code changes, and " +
				"ask for it to be re-run after each phase.",
			EvidenceClass: EvidenceMeasurement,
			Evidence: []string{
				"The first check ran only after " +
					plural(m.CorrectionsBeforeFirstVerification, "correction", "corrections") +
					" from you.",
			},
		}
		if gap > 0 {
			finding.TimeCostMS = &gap
			finding.Evidence = append(
				finding.Evidence,
				humanDuration(gap)+" of file changes happened before the first check.",
			)
		}
		result = append(result, finding)
	}

	if m.FirstChangeAt != nil &&
		m.PlanningShare != nil &&
		*m.PlanningShare >= longPlanningShare &&
		m.UserTurnsBeforeFirstChange >= longPlanningMinUserTurns &&
		m.PlanningDurationMS >= longPlanningMinDuration.Milliseconds() {
		planning := m.PlanningDurationMS
		finding := Finding{
			Kind:  FindingLongPlanning,
			Tone:  ToneImprove,
			Title: "Long back-and-forth before the first change",
			Summary: "About " + percent(*m.PlanningShare) + " of the session " +
				"went to discussion before anything changed. That can be the " +
				"right call, but it is the biggest single block of time here.",
			NextTime: "State the design you want as the plan, and ask the agent " +
				"to challenge only the parts it thinks are wrong.",
			EvidenceClass: EvidenceJudgment,
			Evidence: []string{
				plural(m.UserTurnsBeforeFirstChange, "message", "messages") +
					" from you before the first file change.",
				"Whether that discussion paid off is a judgment call Belay " +
					"cannot make from the transcript alone.",
			},
			TimeCostMS: &planning,
			DollarCost: copyFloat(m.PlanningCostUSD),
		}
		result = append(result, finding)
	}

	if m.Corrections >= repeatedCorrectionMinimum {
		result = append(result, Finding{
			Kind:  FindingRepeatedCorrections,
			Tone:  ToneImprove,
			Title: "You had to correct the agent repeatedly",
			Summary: "You stepped in " + plural(m.Corrections, "time", "times") +
				" to redirect the agent. Each one usually means a rule it " +
				"did not have when it started.",
			NextTime: "Put the standing rules in your first message or in the " +
				"project's instructions file so you do not have to repeat them.",
			EvidenceClass: EvidenceMeasurement,
			Evidence: []string{
				plural(m.Corrections, "message", "messages") +
					" opened with an explicit correction.",
			},
		})
	}

	if m.Compactions >= compactionMinimum {
		result = append(result, Finding{
			Kind:  FindingContextCompaction,
			Tone:  ToneImprove,
			Title: "The session outgrew the agent's memory",
			Summary: "The agent had to compress its own context " +
				plural(m.Compactions, "time", "times") +
				". After each one it works from a summary, not the real history.",
			NextTime: "Split work this size into phases, and start a fresh " +
				"session for each phase with a short written plan.",
			EvidenceClass: EvidenceMeasurement,
			Evidence: []string{
				plural(m.Compactions, "context compaction", "context compactions") +
					" were recorded in the transcript.",
			},
		})
	}

	if m.FirstPromptNamesChecks &&
		m.VerificationRuns > 0 &&
		m.Corrections <= 1 {
		result = append(result, Finding{
			Kind:  FindingChecksNamedUpFront,
			Tone:  ToneKeep,
			Title: "You named the checks up front",
			Summary: "Your first message said what had to be proven, and the " +
				"agent ran checks " + plural(m.VerificationRuns, "time", "times") +
				" without needing much correction.",
			NextTime:      "Keep doing this. It is the habit that saves the most time.",
			EvidenceClass: EvidenceMeasurement,
			Evidence: []string{
				"The first message mentioned tests, verification, or a build check.",
				plural(m.VerificationRuns, "verification command", "verification commands") +
					" ran during the session.",
			},
		})
	}

	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Tone != result[right].Tone {
			return result[left].Tone == ToneImprove
		}
		return timeCost(result[left]) > timeCost(result[right])
	})
	if len(result) > maxFindingsPerSession {
		result = result[:maxFindingsPerSession]
	}
	return result
}

func outcome(m Measurements) string {
	switch {
	case m.FileChangeTurns == 0:
		return OutcomeNoChanges
	case m.LastVerificationPassed != nil && *m.LastVerificationPassed:
		return OutcomeVerifiedPass
	case m.LastVerificationPassed != nil:
		return OutcomeVerifiedFail
	default:
		return OutcomeUnverifiedChanges
	}
}

func verdict(outcome string, findings []Finding, baseline *Baseline) string {
	var opening string
	switch outcome {
	case OutcomeVerifiedPass:
		opening = "Finished with passing checks"
	case OutcomeVerifiedFail:
		opening = "Ended with a failing check"
	case OutcomeUnverifiedChanges:
		opening = "Finished without running checks"
	default:
		opening = "No files were changed"
	}
	comparison := ""
	if baseline != nil && baseline.DurationRatio != nil {
		switch baseline.Comparison {
		case ComparisonLonger:
			comparison = ", and it took about " + ratioLabel(*baseline.DurationRatio) +
				" as long as your usual session in this project"
		case ComparisonShorter:
			comparison = ", and it was shorter than your usual session in this project"
		case ComparisonTypical:
			comparison = ", in about your usual time for this project"
		}
	}
	improve := 0
	for _, finding := range findings {
		if finding.Tone == ToneImprove {
			improve++
		}
	}
	closing := ""
	switch {
	case improve == 1:
		closing = " One habit explains most of the time."
	case improve > 1:
		closing = " " + capitalize(numberWord(improve)) + " habits explain most of the time."
	case len(findings) > 0:
		closing = " Nothing to change here."
	}
	if baseline == nil {
		closing += " Belay needs a few more sessions in this project before it can compare."
	}
	return opening + comparison + "." + closing
}

func opener(findings []Finding) string {
	kinds := make(map[string]bool, len(findings))
	for _, finding := range findings {
		if finding.Tone == ToneImprove {
			kinds[finding.Kind] = true
		}
	}
	if len(kinds) == 0 {
		return ""
	}
	lines := []string{
		"Here is the task: <one sentence>. Here is the design I want: <one sentence>.",
	}
	if kinds[FindingRepeatedCorrections] {
		lines = append(lines, "Standing rules for this project: <the things you keep having to repeat>.")
	}
	if kinds[FindingNoVerification] || kinds[FindingLateVerification] {
		lines = append(lines, "Before changing any code, list the checks that must pass and run them after each phase.")
	}
	if kinds[FindingLongPlanning] {
		lines = append(lines, "Push back only on the parts of the design you think are wrong, then start.")
	}
	if kinds[FindingContextCompaction] {
		lines = append(lines, "Work in phases and stop after each one so I can start a fresh session.")
	}
	return strings.Join(lines, " ")
}

func behaviorRole(role transcript.Role) bool {
	return role == transcript.RoleAssistant ||
		role == transcript.RoleToolCall ||
		role == transcript.RoleToolResult
}

func sessionDuration(session transcript.Session, turns []transcript.Turn) int64 {
	if session.WallDurationMS > 0 {
		return session.WallDurationMS
	}
	if !session.StartedAt.IsZero() && session.EndedAt.After(session.StartedAt) {
		return session.EndedAt.Sub(session.StartedAt).Milliseconds()
	}
	if len(turns) >= 2 {
		first := turns[0].OccurredAt
		last := turns[len(turns)-1].OccurredAt
		if !first.IsZero() && last.After(first) {
			return last.Sub(first).Milliseconds()
		}
	}
	return 0
}

func timeCost(finding Finding) int64 {
	if finding.TimeCostMS == nil {
		return 0
	}
	return *finding.TimeCostMS
}

func medianInt64(values []int64) int64 {
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left] < sorted[right] })
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func medianFloat64(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func copyFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func plural(count int, singular, pluralForm string) string {
	if count == 1 {
		return "1 " + singular
	}
	return itoa(count) + " " + pluralForm
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

func percent(share float64) string {
	rounded := int(share*20+0.5) * 5
	if rounded > 100 {
		rounded = 100
	}
	return itoa(rounded) + "%"
}

func ratioLabel(ratio float64) string {
	switch {
	case ratio >= 2.75:
		return "three times"
	case ratio >= 2.25:
		return "two and a half times"
	case ratio >= 1.75:
		return "twice"
	default:
		return "one and a half times"
	}
}

func humanDuration(ms int64) string {
	minutes := ms / 60000
	if minutes < 1 {
		return "Under a minute"
	}
	hours := minutes / 60
	if hours > 0 {
		return "About " + itoa(int(hours)) + "h " + itoa(int(minutes%60)) + "m"
	}
	return "About " + itoa(int(minutes)) + " minutes"
}

func numberWord(value int) string {
	words := []string{"zero", "one", "two", "three", "four", "five"}
	if value >= 0 && value < len(words) {
		return words[value]
	}
	return itoa(value)
}

func capitalize(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
