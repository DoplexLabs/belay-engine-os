package missionpack

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

const (
	maxCandidateIssues = 10
	maxRuleRunes       = 300
	maxCommandRunes    = 256
	maxDisplayRunes    = 300
	highConfidence     = 0.8

	maxExperiences                  = 3
	maxExperienceTokens             = 600
	maxExperienceIDRunes            = 256
	maxExperienceTypeRunes          = 64
	maxExperienceGuidanceBytes      = 2 * 1024
	maxExperienceApplicabilityBytes = 2 * 1024
	maxExperienceExceptions         = 8
	maxExperienceRationaleBytes     = 8 * 1024
	maxExperienceVerifierRunes      = 300
	experienceAuthorityUserApproved = "user_approved"
)

var packIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func Build(input BuildInput) (Pack, error) {
	if !validIntent(input.Request.Intent) {
		return Pack{}, errors.New("invalid Mission Pack intent")
	}
	if !validHarness(input.Request.Harness) {
		return Pack{}, errors.New("invalid Mission Pack harness")
	}
	if input.Request.GeneratedAt.IsZero() {
		return Pack{}, errors.New("Mission Pack generated_at is required")
	}
	input.Project.Identity = strings.TrimSpace(input.Project.Identity)
	input.Project.Label = oneLine(input.Project.Label, 120)
	input.Project.IdentityKind = oneLine(input.Project.IdentityKind, 32)
	if input.Project.Identity == "" || input.Project.Label == "" {
		return Pack{}, errors.New("Mission Pack project is required")
	}
	experiences, err := normalizeExperienceItems(
		input.ExperienceGeneration,
		input.Experiences,
	)
	if err != nil {
		return Pack{}, err
	}

	issues, err := selectIssues(input)
	if err != nil {
		return Pack{}, err
	}
	pack := Pack{
		SchemaVersion:    SchemaVersion,
		GeneratorVersion: GeneratorVersion,
		GeneratedAt:      input.Request.GeneratedAt.UTC(),
		Project: Project{
			Label:        input.Project.Label,
			IdentityKind: input.Project.IdentityKind,
			Branch:       oneLine(input.Workspace.Branch, 160),
			Worktree:     oneLine(input.Workspace.Worktree, 160),
		},
		Intent:  input.Request.Intent,
		Harness: input.Request.Harness,
		Trust: Trust{
			InstructionAuthority: "none",
			GuidanceState:        "proposal",
			EvidenceState:        "untrusted",
			ActivationRequired:   true,
		},
		SourceState:          input.SourceState,
		ExperienceGeneration: input.ExperienceGeneration,
		Experiences:          experiences,
		Context: Context{
			Harnesses: sortedDistinctLines(input.Workspace.Harnesses, 64),
			Facts:     selectFacts(input.Facts),
		},
		KnownTraps:     buildKnownTraps(issues),
		OperatingRules: selectRules(input, issues),
		Verification: selectVerification(
			input.Request.Intent,
			input.Commands,
			input.ProjectFiles,
		),
		Completion: make([]ChecklistItem, 0),
		Warnings:   make([]Warning, 0),
	}
	pack.SourceState.AnalysisStatus = oneLine(
		pack.SourceState.AnalysisStatus,
		32,
	)
	if !pack.SourceState.DataThrough.IsZero() {
		pack.SourceState.DataThrough = pack.SourceState.DataThrough.UTC()
	}
	pack.Warnings = buildWarnings(input, pack)
	pack.Completion = buildChecklist(pack)
	setPackStatusAndTrust(&pack)
	if len(pack.Experiences) > 0 {
		pack.PackID = derivePackID(input, pack)
		pack.RenderedMarkdown = renderMarkdown(pack)
		if !withinBudget(pack.RenderedMarkdown) {
			return Pack{}, errors.New(
				"Mission Pack experience guidance exceeds pack budget",
			)
		}
		pack.EstimatedTokens = estimateTokens(pack.RenderedMarkdown)
		ensureNonNil(&pack)
		return pack, nil
	}
	truncateToBudget(&pack, input.Request.IssueID)
	pack.PackID = derivePackID(input, pack)
	pack.RenderedMarkdown = renderMarkdown(pack)
	if !withinBudget(pack.RenderedMarkdown) {
		pack.RenderedMarkdown = truncateMarkdown(pack.RenderedMarkdown)
		pack.Truncated = true
		pack.Warnings = appendWarning(pack.Warnings, "pack_truncated")
		setPackStatusAndTrust(&pack)
	}
	pack.EstimatedTokens = estimateTokens(pack.RenderedMarkdown)
	ensureNonNil(&pack)
	return pack, nil
}

func normalizeExperienceItems(
	generation int64,
	values []ExperienceItem,
) ([]ExperienceItem, error) {
	if len(values) == 0 {
		if generation != 0 {
			return nil, errors.New(
				"Mission Pack experience generation requires experience items",
			)
		}
		return nil, nil
	}
	if generation <= 0 {
		return nil, errors.New(
			"Mission Pack experiences require a positive generation",
		)
	}
	if len(values) > maxExperiences {
		return nil, errors.New("Mission Pack has too many experiences")
	}

	result := make([]ExperienceItem, 0, len(values))
	totalBytes := 0
	for _, value := range values {
		item, itemBytes, err := normalizeExperienceItem(value)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
		totalBytes += itemBytes
	}
	if (totalBytes+3)/4 > maxExperienceTokens {
		return nil, errors.New(
			"Mission Pack experiences exceed the experience token budget",
		)
	}
	return result, nil
}

func normalizeExperienceItem(
	value ExperienceItem,
) (ExperienceItem, int, error) {
	var err error
	if value.ExperienceID, err = validExperienceOneLine(
		"experience ID",
		value.ExperienceID,
		maxExperienceIDRunes,
	); err != nil {
		return ExperienceItem{}, 0, err
	}
	if value.Version <= 0 {
		return ExperienceItem{}, 0, errors.New(
			"Mission Pack experience version must be positive",
		)
	}
	if value.Type, err = validExperienceOneLine(
		"experience type",
		value.Type,
		maxExperienceTypeRunes,
	); err != nil {
		return ExperienceItem{}, 0, err
	}
	value.Guidance = strings.TrimSpace(value.Guidance)
	if value.Guidance == "" ||
		strings.ContainsAny(value.Guidance, "\r\n") ||
		len([]byte(value.Guidance)) > maxExperienceGuidanceBytes {
		return ExperienceItem{}, 0, errors.New(
			"Mission Pack experience guidance is invalid",
		)
	}
	value.Applicability = strings.TrimSpace(value.Applicability)
	if value.Applicability == "" ||
		strings.ContainsAny(value.Applicability, "\r\n") ||
		len([]byte(value.Applicability)) > maxExperienceApplicabilityBytes {
		return ExperienceItem{}, 0, errors.New(
			"Mission Pack experience applicability is invalid",
		)
	}
	if len(value.Exceptions) > maxExperienceExceptions {
		return ExperienceItem{}, 0, errors.New(
			"Mission Pack experience has too many exceptions",
		)
	}
	seenExceptions := make(map[string]bool, len(value.Exceptions))
	exceptions := make([]string, 0, len(value.Exceptions))
	for _, exception := range value.Exceptions {
		exception = strings.TrimSpace(exception)
		if exception == "" ||
			strings.ContainsAny(exception, "\r\n") ||
			len([]byte(exception)) > maxExperienceGuidanceBytes {
			return ExperienceItem{}, 0, errors.New(
				"Mission Pack experience exception is invalid",
			)
		}
		normalized := strings.ToLower(exception)
		if seenExceptions[normalized] {
			return ExperienceItem{}, 0, errors.New(
				"Mission Pack experience exceptions are duplicated",
			)
		}
		seenExceptions[normalized] = true
		exceptions = append(exceptions, exception)
	}
	value.Exceptions = exceptions
	value.Rationale = strings.TrimSpace(value.Rationale)
	if value.Rationale == "" ||
		len([]byte(value.Rationale)) > maxExperienceRationaleBytes {
		return ExperienceItem{}, 0, errors.New(
			"Mission Pack experience rationale is invalid",
		)
	}
	if value.Verifier.Kind, err = validExperienceOneLine(
		"experience verifier kind",
		value.Verifier.Kind,
		maxExperienceTypeRunes,
	); err != nil {
		return ExperienceItem{}, 0, err
	}
	if value.Verifier.Summary, err = validExperienceOneLine(
		"experience verifier summary",
		value.Verifier.Summary,
		maxExperienceVerifierRunes,
	); err != nil {
		return ExperienceItem{}, 0, err
	}
	if value.Authority != experienceAuthorityUserApproved {
		return ExperienceItem{}, 0, errors.New(
			"Mission Pack experience authority must be user_approved",
		)
	}
	if len(value.Sources) > MaxSourcesPerItem {
		return ExperienceItem{}, 0, errors.New(
			"Mission Pack experience has too many sources",
		)
	}
	for _, source := range value.Sources {
		if err := validateExperienceSource(source); err != nil {
			return ExperienceItem{}, 0, err
		}
	}
	value.Sources = normalizeSources(value.Sources, MaxSourcesPerItem)

	itemBytes := len([]byte(value.Guidance)) +
		len([]byte(value.Applicability)) +
		len([]byte(value.Rationale)) +
		len([]byte(value.Verifier.Kind)) +
		len([]byte(value.Verifier.Summary))
	for _, exception := range value.Exceptions {
		itemBytes += len([]byte(exception))
	}
	return value, itemBytes, nil
}

func validExperienceOneLine(
	field string,
	value string,
	maxRunes int,
) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" ||
		strings.ContainsAny(value, "\r\n") ||
		utf8.RuneCountInString(value) > maxRunes {
		return "", fmt.Errorf("Mission Pack %s is invalid", field)
	}
	return value, nil
}

func validateExperienceSource(value SourceRef) error {
	if _, err := validExperienceOneLine(
		"experience source kind",
		value.Kind,
		64,
	); err != nil {
		return err
	}
	for _, identifier := range []string{
		value.IssueID,
		value.InsightID,
		value.CandidateID,
		value.EpisodeID,
		value.SessionKey,
		value.EventID,
		value.ProjectFile,
		value.SourceSHA256,
		value.SourceFileID,
	} {
		if identifier == "" {
			continue
		}
		if strings.ContainsAny(identifier, "\r\n") ||
			utf8.RuneCountInString(strings.TrimSpace(identifier)) >
				maxExperienceIDRunes {
			return errors.New(
				"Mission Pack experience source identifier is invalid",
			)
		}
	}
	return nil
}

func validIntent(value Intent) bool {
	switch value {
	case IntentGeneral, IntentDebug, IntentImplement, IntentRefactor,
		IntentReview, IntentRelease:
		return true
	default:
		return false
	}
}

func validHarness(value Harness) bool {
	switch value {
	case "", HarnessClaude, HarnessCodex, HarnessCursor, HarnessAntigravity:
		return true
	default:
		return false
	}
}

func selectIssues(input BuildInput) ([]issueintel.Issue, error) {
	unique := make(map[string]issueintel.Issue, len(input.Issues))
	for _, issue := range input.Issues {
		issue.IssueID = strings.TrimSpace(issue.IssueID)
		if issue.IssueID == "" {
			continue
		}
		if issue.Project.Identity != "" &&
			issue.Project.Identity != input.Project.Identity {
			continue
		}
		if current, ok := unique[issue.IssueID]; !ok ||
			baseIssueLess(issue, current) {
			unique[issue.IssueID] = issue
		}
	}
	all := make([]issueintel.Issue, 0, len(unique))
	for _, issue := range unique {
		all = append(all, issue)
	}
	sort.Slice(all, func(i, j int) bool {
		return baseIssueLess(all[i], all[j])
	})

	anchorID := strings.TrimSpace(input.Request.IssueID)
	if anchorID != "" {
		for _, issue := range all {
			if issue.IssueID == anchorID {
				return []issueintel.Issue{issue}, nil
			}
		}
		return nil, errors.New("Mission Pack anchor issue is unavailable")
	}

	remaining := make([]issueintel.Issue, 0, min(len(all), maxCandidateIssues))
	for _, issue := range all {
		if issueSessionCount(issue) < 2 {
			continue
		}
		remaining = append(remaining, issue)
		if len(remaining) == maxCandidateIssues {
			break
		}
	}

	result := make([]issueintel.Issue, 0, MaxKnownTraps)
	seenFamilies := make(map[string]bool, MaxKnownTraps)
	for _, issue := range remaining {
		if len(result) == MaxKnownTraps {
			break
		}
		family := detectorFamily(issue.DetectorID)
		if seenFamilies[family] {
			continue
		}
		result = append(result, issue)
		seenFamilies[family] = true
	}
	return result, nil
}

func containsIssue(issues []issueintel.Issue, issueID string) bool {
	for _, issue := range issues {
		if issue.IssueID == issueID {
			return true
		}
	}
	return false
}

func baseIssueLess(left, right issueintel.Issue) bool {
	leftUSD, rightUSD := left.Cost.WastedUSD, right.Cost.WastedUSD
	if (leftUSD != nil) != (rightUSD != nil) {
		return leftUSD != nil
	}
	if leftUSD != nil && *leftUSD != *rightUSD {
		return *leftUSD > *rightUSD
	}
	if left.SessionCount != right.SessionCount {
		return left.SessionCount > right.SessionCount
	}
	if !left.LastSeen.Equal(right.LastSeen) {
		return left.LastSeen.After(right.LastSeen)
	}
	return left.IssueID < right.IssueID
}

func issueSessionCount(issue issueintel.Issue) int {
	return max(issue.SessionCount, len(issue.Sessions))
}

func detectorFamily(detectorID string) string {
	switch strings.TrimSpace(detectorID) {
	case issueintel.DetectorRetryLoop,
		issueintel.DetectorRecurringError,
		issueintel.DetectorRepeatedCorrection,
		issueintel.DetectorDoneWithoutVerification,
		issueintel.DetectorPermissionChurn,
		issueintel.DetectorColdStartCost,
		issueintel.DetectorFileThrash,
		issueintel.DetectorFileReversal,
		issueintel.DetectorFailureRepaired,
		issueintel.DetectorCompactionBeforeCompletion:
		return strings.TrimSpace(detectorID)
	default:
		return "unknown"
	}
}

func buildKnownTraps(issues []issueintel.Issue) []GuidanceItem {
	result := make([]GuidanceItem, 0, len(issues))
	for _, issue := range issues {
		result = append(result, GuidanceItem{
			ID:               "trap_" + shortDigest(issue.IssueID),
			Kind:             "known_trap",
			Title:            knownTrapDisplayTitle(issue),
			RequiresApproval: true,
			SessionCount:     issueSessionCount(issue),
			WastedMinutes:    issue.Cost.WastedMinutes,
			WastedTokens:     issue.Cost.WastedTokens,
			WastedUSD:        cloneFloat(issue.Cost.WastedUSD),
			CostLowerBound:   issue.Cost.LowerBound,
			Sources:          issueSources(issue),
		})
	}
	return result
}

func issueSources(issue issueintel.Issue) []SourceRef {
	result := make([]SourceRef, 0, MaxSourcesPerItem)
	episodeRefs := append([]string(nil), issue.EpisodeRefs...)
	sort.Strings(episodeRefs)
	for _, episodeID := range episodeRefs {
		episodeID = boundedIdentifier(episodeID)
		if episodeID == "" {
			continue
		}
		result = append(result, SourceRef{
			Kind:      "evidence_episode",
			IssueID:   boundedIdentifier(issue.IssueID),
			EpisodeID: episodeID,
		})
		break
	}
	for _, excerpt := range issue.Excerpts {
		citation := excerpt.Citation
		turnIndex := citation.TurnIndex
		byteOffset := citation.JSONLByteOffset
		result = append(result, SourceRef{
			Kind:            "cost_issue",
			IssueID:         boundedIdentifier(issue.IssueID),
			SessionKey:      boundedIdentifier(citation.SessionKey),
			TurnIndex:       &turnIndex,
			SourceFileID:    boundedIdentifier(citation.SourceFileID),
			JSONLByteOffset: &byteOffset,
			ObservedAt:      timePointer(citation.OccurredAt),
		})
		if len(result) == MaxSourcesPerItem {
			break
		}
	}
	if len(result) == 0 {
		result = append(result, SourceRef{
			Kind:    "cost_issue",
			IssueID: boundedIdentifier(issue.IssueID),
		})
	}
	return normalizeSources(result, MaxSourcesPerItem)
}

func knownTrapDisplayTitle(issue issueintel.Issue) string {
	if headline := oneLine(issue.Headline, maxDisplayRunes); headline != "" {
		return headline
	}
	title := knownTrapTitle(issue.DetectorID)
	sessionCount := issueSessionCount(issue)
	if sessionCount <= 0 {
		return title
	}
	return oneLine(
		fmt.Sprintf(
			"%s across %d session%s",
			title,
			sessionCount,
			plural(sessionCount),
		),
		maxDisplayRunes,
	)
}

func knownTrapTitle(detectorID string) string {
	switch detectorID {
	case issueintel.DetectorRetryLoop:
		return "Repeated command failure loop"
	case issueintel.DetectorRecurringError:
		return "Recurring error across sessions"
	case issueintel.DetectorRepeatedCorrection:
		return "Repeated agent correction"
	case issueintel.DetectorDoneWithoutVerification:
		return "Completion claimed without verification"
	case issueintel.DetectorPermissionChurn:
		return "Repeated permission approval"
	case issueintel.DetectorColdStartCost:
		return "High project cold-start cost"
	case issueintel.DetectorFileThrash:
		return "Repeated edits to the same file"
	case issueintel.DetectorFileReversal:
		return "File returned to an earlier edit state"
	case issueintel.DetectorFailureRepaired:
		return "A failed approach had a successful recovery"
	case issueintel.DetectorCompactionBeforeCompletion:
		return "Context compaction before verified completion"
	default:
		return "Recurring agent workflow issue"
	}
}

func selectRules(
	input BuildInput,
	issues []issueintel.Issue,
) []GuidanceItem {
	result := make([]GuidanceItem, 0, MaxOperatingRules)
	if input.Request.Harness == "" {
		return result
	}
	seen := make(map[string]bool)
	anchorID := strings.TrimSpace(input.Request.IssueID)
	issuesByID := make(map[string]issueintel.Issue, len(issues))
	for _, issue := range issues {
		issuesByID[issue.IssueID] = issue
	}

	if input.Insight != nil &&
		!input.InsightStale &&
		strings.TrimSpace(input.SourceState.AnalysisStatus) !=
			AnalysisStatusStale {
		taskHint := strings.TrimSpace(input.Request.TaskHint)
		relevanceTokens := make(map[string]struct{})
		if anchorID == "" {
			if taskHint == "" {
				return result
			}
			relevanceTokens = lexicalTokens(
				oneLine(taskHint, maxDisplayRunes),
			)
			if len(relevanceTokens) == 0 {
				return result
			}
		}

		fixes := append(
			[]issueintel.InsightFix(nil),
			input.Insight.Result.Fixes...,
		)
		sort.Slice(fixes, func(i, j int) bool {
			if fixes[i].IssueID != fixes[j].IssueID {
				return fixes[i].IssueID < fixes[j].IssueID
			}
			if fixes[i].Confidence != fixes[j].Confidence {
				return fixes[i].Confidence > fixes[j].Confidence
			}
			if fixes[i].RuleText != fixes[j].RuleText {
				return fixes[i].RuleText < fixes[j].RuleText
			}
			return fixes[i].TargetFile < fixes[j].TargetFile
		})
		bestFix := make(map[string]issueintel.InsightFix)
		for _, fix := range fixes {
			if fix.Confidence < highConfidence {
				continue
			}
			targetFile, ok := semanticTargetForHarness(
				input.Request.Harness,
				fix.TargetFile,
			)
			if !ok {
				continue
			}
			fix.TargetFile = targetFile
			if _, selected := issuesByID[fix.IssueID]; !selected {
				continue
			}
			if anchorID != "" && fix.IssueID != anchorID {
				continue
			}
			issue := issuesByID[fix.IssueID]
			if anchorID == "" &&
				!issueFixRelevant(issue, fix, relevanceTokens) {
				continue
			}
			if _, exists := bestFix[fix.IssueID]; !exists {
				bestFix[fix.IssueID] = fix
			}
		}
		for _, issue := range issues {
			if anchorID != "" && issue.IssueID != anchorID {
				continue
			}
			fix, ok := bestFix[issue.IssueID]
			if !ok {
				continue
			}
			rule := insightFixRule(*input.Insight, issue, fix)
			appendRule(&result, seen, rule)
			if len(result) == MaxOperatingRules {
				return result
			}
		}

		if anchorID != "" {
			return result
		}

		clusters := make(
			[]rankedInsightCluster,
			0,
			len(input.Insight.Result.Clusters),
		)
		for _, cluster := range input.Insight.Result.Clusters {
			targetFile, ok := semanticTargetForHarness(
				input.Request.Harness,
				cluster.TargetFile,
			)
			if !ok {
				continue
			}
			cluster.TargetFile = targetFile
			candidateIDs, sessionSupport := clusterCandidateSupport(
				cluster,
				input.Candidates,
			)
			if len(candidateIDs) < 2 || sessionSupport < 2 {
				continue
			}
			clusters = append(clusters, rankedInsightCluster{
				cluster:        cluster,
				candidateIDs:   candidateIDs,
				sessionSupport: sessionSupport,
			})
		}
		sort.Slice(clusters, func(i, j int) bool {
			if clusters[i].sessionSupport != clusters[j].sessionSupport {
				return clusters[i].sessionSupport >
					clusters[j].sessionSupport
			}
			if len(clusters[i].candidateIDs) !=
				len(clusters[j].candidateIDs) {
				return len(clusters[i].candidateIDs) >
					len(clusters[j].candidateIDs)
			}
			left, right := clusters[i].cluster, clusters[j].cluster
			if left.Confidence != right.Confidence {
				return left.Confidence > right.Confidence
			}
			if left.Topic != right.Topic {
				return left.Topic < right.Topic
			}
			if left.RuleText != right.RuleText {
				return left.RuleText < right.RuleText
			}
			if left.TargetFile != right.TargetFile {
				return left.TargetFile < right.TargetFile
			}
			return strings.Join(clusters[i].candidateIDs, "\x00") <
				strings.Join(clusters[j].candidateIDs, "\x00")
		})
		for _, ranked := range clusters {
			cluster := ranked.cluster
			if cluster.Confidence < highConfidence ||
				!clusterRelevant(
					cluster,
					ranked.candidateIDs,
					input.Candidates,
					relevanceTokens,
				) {
				continue
			}
			rule := insightClusterRule(
				*input.Insight,
				cluster,
				ranked.candidateIDs,
				input.Candidates,
			)
			appendRule(&result, seen, rule)
			if len(result) == MaxOperatingRules {
				return result
			}
		}
	}
	return result
}

func semanticTargetForHarness(
	harness Harness,
	targetFile string,
) (string, bool) {
	switch harness {
	case HarnessClaude:
		// An analysis run by the Antigravity CLI proposes its own rule
		// file, .agents/rules/belay.md; Claude reads CLAUDE.md instead.
		switch strings.TrimSpace(targetFile) {
		case "CLAUDE.md", ".claude/settings.json":
			return strings.TrimSpace(targetFile), true
		case "AGENTS.md", ".codex/rules/default.rules",
			".agents/rules/belay.md":
			return "CLAUDE.md", true
		}
	case HarnessCodex:
		switch strings.TrimSpace(targetFile) {
		case "AGENTS.md", ".codex/rules/default.rules":
			return strings.TrimSpace(targetFile), true
		case "CLAUDE.md", ".agents/rules/belay.md":
			return "AGENTS.md", true
		case ".claude/settings.json":
			return "", false
		}
	case HarnessCursor:
		// Cursor reads AGENTS.md at the project root and has no CLAUDE.md,
		// Codex rules file, or Antigravity rules directory, so every
		// portable instruction target folds into AGENTS.md. Claude-only
		// settings have no Cursor equivalent.
		switch strings.TrimSpace(targetFile) {
		case "AGENTS.md":
			return "AGENTS.md", true
		case "CLAUDE.md", ".codex/rules/default.rules",
			".agents/rules/belay.md":
			return "AGENTS.md", true
		case ".claude/settings.json":
			return "", false
		}
	case HarnessAntigravity:
		// Antigravity reads project rules only from Markdown files under
		// .agents/rules/ at the workspace root; it does not read AGENTS.md,
		// CLAUDE.md, or Codex rules. Every portable instruction target folds
		// into Belay's single owned rule file. Antigravity keeps permissions
		// in IDE settings, so Claude-only settings have no equivalent.
		// Belay owns exactly one Antigravity rule file, .agents/rules/belay.md.
		switch strings.TrimSpace(targetFile) {
		case ".agents/rules/belay.md":
			return ".agents/rules/belay.md", true
		case "AGENTS.md", "CLAUDE.md", ".codex/rules/default.rules":
			return ".agents/rules/belay.md", true
		case ".claude/settings.json":
			return "", false
		}
	}
	return "", false
}

type rankedInsightCluster struct {
	cluster        issueintel.InsightCluster
	candidateIDs   []string
	sessionSupport int
}

func clusterCandidateSupport(
	cluster issueintel.InsightCluster,
	candidates map[string]issueintel.CorrectionCandidate,
) ([]string, int) {
	candidateIDs := make([]string, 0, len(cluster.CandidateIDs))
	seenCandidates := make(map[string]bool, len(cluster.CandidateIDs))
	sessions := make(map[string]bool)
	for _, candidateID := range cluster.CandidateIDs {
		candidate, ok := candidates[candidateID]
		if !ok || seenCandidates[candidateID] {
			continue
		}
		seenCandidates[candidateID] = true
		candidateIDs = append(candidateIDs, candidateID)
		sessionKey := strings.TrimSpace(candidate.Citation.SessionKey)
		if sessionKey != "" {
			sessions[sessionKey] = true
		}
	}
	sort.Strings(candidateIDs)
	return candidateIDs, len(sessions)
}

func insightFixRule(
	insight issueintel.InsightRecord,
	issue issueintel.Issue,
	fix issueintel.InsightFix,
) GuidanceItem {
	confidence := fix.Confidence
	guidance := oneLine(fix.RuleText, maxRuleRunes)
	return GuidanceItem{
		ID: "rule_" + shortDigest(
			"fix\x00"+issue.IssueID+"\x00"+guidance,
		),
		Kind:             "insight_fix",
		Title:            oneLine("Rule for "+knownTrapTitle(issue.DetectorID), maxDisplayRunes),
		Guidance:         guidance,
		TargetFile:       oneLine(fix.TargetFile, 160),
		Confidence:       &confidence,
		RequiresApproval: true,
		Sources: normalizeSources([]SourceRef{{
			Kind:       "insight",
			IssueID:    boundedIdentifier(issue.IssueID),
			InsightID:  boundedIdentifier(insight.InsightID),
			ObservedAt: timePointer(insight.GeneratedAt),
		}}, MaxSourcesPerItem),
	}
}

func issueFixRelevant(
	issue issueintel.Issue,
	fix issueintel.InsightFix,
	relevanceTokens map[string]struct{},
) bool {
	return semanticTextRelevant(
		issue.Headline+" "+fix.RuleText,
		relevanceTokens,
	)
}

func clusterRelevant(
	cluster issueintel.InsightCluster,
	candidateIDs []string,
	candidates map[string]issueintel.CorrectionCandidate,
	relevanceTokens map[string]struct{},
) bool {
	evidenceTokens := lexicalTokens(
		oneLine(cluster.Topic+" "+cluster.RuleText, maxRuleRunes),
	)
	for _, candidateID := range candidateIDs {
		candidate, ok := candidates[candidateID]
		if !ok {
			continue
		}
		for token := range lexicalTokens(
			oneLine(candidate.Text, maxDisplayRunes),
		) {
			evidenceTokens[token] = struct{}{}
		}
	}
	return tokenSetsRelevant(evidenceTokens, relevanceTokens)
}

func semanticTextRelevant(
	value string,
	relevanceTokens map[string]struct{},
) bool {
	return tokenSetsRelevant(
		lexicalTokens(oneLine(value, maxDisplayRunes+maxRuleRunes)),
		relevanceTokens,
	)
}

func tokenSetsRelevant(
	evidenceTokens map[string]struct{},
	relevanceTokens map[string]struct{},
) bool {
	requiredOverlap := 1
	if len(relevanceTokens) >= 2 {
		requiredOverlap = 2
	}
	overlap := 0
	for token := range relevanceTokens {
		if _, ok := evidenceTokens[token]; !ok {
			continue
		}
		overlap++
		if overlap >= requiredOverlap {
			return true
		}
	}
	return false
}

func insightClusterRule(
	insight issueintel.InsightRecord,
	cluster issueintel.InsightCluster,
	candidateIDs []string,
	candidates map[string]issueintel.CorrectionCandidate,
) GuidanceItem {
	confidence := cluster.Confidence
	sources := make([]SourceRef, 0, len(candidateIDs))
	boundedCandidateIDs := make([]string, 0, len(candidateIDs))
	for _, candidateID := range candidateIDs {
		candidate, ok := candidates[candidateID]
		if !ok {
			continue
		}
		boundedCandidateID := boundedIdentifier(candidateID)
		boundedCandidateIDs = append(
			boundedCandidateIDs,
			boundedCandidateID,
		)
		turnIndex := candidate.Citation.TurnIndex
		byteOffset := candidate.Citation.JSONLByteOffset
		sources = append(sources, SourceRef{
			Kind:            "insight_cluster",
			InsightID:       boundedIdentifier(insight.InsightID),
			CandidateID:     boundedCandidateID,
			SessionKey:      boundedIdentifier(candidate.Citation.SessionKey),
			TurnIndex:       &turnIndex,
			SourceFileID:    boundedIdentifier(candidate.Citation.SourceFileID),
			JSONLByteOffset: &byteOffset,
			ObservedAt:      timePointer(candidate.Citation.OccurredAt),
		})
	}
	if len(sources) == 0 {
		sources = append(sources, SourceRef{
			Kind:       "insight_cluster",
			InsightID:  boundedIdentifier(insight.InsightID),
			ObservedAt: timePointer(insight.GeneratedAt),
		})
	}
	guidance := oneLine(cluster.RuleText, maxRuleRunes)
	return GuidanceItem{
		ID: "rule_" + shortDigest(
			"cluster\x00"+strings.Join(boundedCandidateIDs, "\x00")+
				"\x00"+guidance,
		),
		Kind:             "correction_cluster",
		Title:            oneLine(cluster.Topic, maxDisplayRunes),
		Guidance:         guidance,
		TargetFile:       oneLine(cluster.TargetFile, 160),
		Confidence:       &confidence,
		RequiresApproval: true,
		Sources:          normalizeSources(sources, MaxSourcesPerItem),
	}
}

func appendRule(
	result *[]GuidanceItem,
	seen map[string]bool,
	rule GuidanceItem,
) {
	rule.Title = oneLine(rule.Title, maxDisplayRunes)
	rule.Guidance = oneLine(rule.Guidance, maxRuleRunes)
	if rule.Guidance == "" {
		return
	}
	normalized := strings.ToLower(rule.Guidance)
	if seen[normalized] {
		return
	}
	seen[normalized] = true
	rule.Sources = normalizeSources(rule.Sources, MaxSourcesPerItem)
	*result = append(*result, rule)
}

type mergedCommand struct {
	command      string
	class        string
	configured   bool
	observed     bool
	successCount int
	lastSuccess  time.Time
	sources      []SourceRef
}

func selectVerification(
	intent Intent,
	observed []ObservedCommand,
	discovered []DiscoveredCommand,
) []CommandItem {
	merged := make(map[string]*mergedCommand)
	for _, value := range discovered {
		command, ok := validCommand(value.Command)
		if !ok {
			continue
		}
		current := merged[command]
		if current == nil {
			current = &mergedCommand{command: command}
			merged[command] = current
		}
		current.configured = true
		if current.class == "" {
			current.class = oneLine(value.Class, 80)
		}
		current.sources = append(current.sources, SourceRef{
			Kind:         "project_config",
			ProjectFile:  boundedIdentifier(value.SourceFile),
			SourceSHA256: boundedIdentifier(value.SourceSHA256),
		})
	}
	for _, value := range observed {
		command, ok := validCommand(value.Command)
		if !ok || value.SuccessCount <= 0 {
			continue
		}
		current := merged[command]
		if current == nil {
			current = &mergedCommand{command: command}
			merged[command] = current
		}
		current.observed = true
		current.successCount += value.SuccessCount
		if value.LastSuccess.After(current.lastSuccess) {
			current.lastSuccess = value.LastSuccess.UTC()
		}
		if current.class == "" {
			current.class = oneLine(value.Class, 80)
		}
		current.sources = append(current.sources, value.Sources...)
	}

	values := make([]mergedCommand, 0, len(merged))
	for _, value := range merged {
		value.sources = normalizeSources(value.sources, MaxSourcesPerItem)
		values = append(values, *value)
	}
	sort.Slice(values, func(i, j int) bool {
		leftBoth := values[i].configured && values[i].observed
		rightBoth := values[j].configured && values[j].observed
		if leftBoth != rightBoth {
			return leftBoth
		}
		if values[i].observed != values[j].observed {
			return values[i].observed
		}
		leftRelease := releaseOrientedCommand(values[i].command)
		rightRelease := releaseOrientedCommand(values[j].command)
		if leftRelease != rightRelease {
			if intent == IntentRelease {
				return leftRelease
			}
			return !leftRelease
		}
		if values[i].successCount != values[j].successCount {
			return values[i].successCount > values[j].successCount
		}
		if !values[i].lastSuccess.Equal(values[j].lastSuccess) {
			return values[i].lastSuccess.After(values[j].lastSuccess)
		}
		return values[i].command < values[j].command
	})

	result := make([]CommandItem, 0, min(len(values), MaxVerificationCommands))
	seenClasses := make(map[string]bool, MaxVerificationCommands)
	for _, value := range values {
		if len(result) == MaxVerificationCommands {
			break
		}
		classKey := strings.ToLower(strings.TrimSpace(value.class))
		if classKey != "" && seenClasses[classKey] {
			continue
		}
		result = append(result, CommandItem{
			ID:               "cmd_" + shortDigest(value.command),
			Command:          value.command,
			Class:            value.class,
			Configured:       value.configured,
			Observed:         value.observed,
			SuccessCount:     value.successCount,
			LastSuccess:      timePointer(value.lastSuccess),
			RequiresApproval: true,
			Sources:          value.sources,
		})
		if classKey != "" {
			seenClasses[classKey] = true
		}
	}
	return result
}

func releaseOrientedCommand(command string) bool {
	for _, token := range strings.FieldsFunc(
		strings.ToLower(command),
		func(character rune) bool {
			return !unicode.IsLetter(character) &&
				!unicode.IsNumber(character)
		},
	) {
		switch token {
		case "release", "prerelease", "publish", "deploy",
			"distribution", "distribute", "dist", "packaging":
			return true
		}
	}
	return false
}

func validCommand(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != "" &&
		utf8.RuneCountInString(value) <= maxCommandRunes &&
		!strings.ContainsAny(value, "\r\n")
}

func selectFacts(values []CanonicalFact) []ContextFact {
	candidates := make([]ContextFact, 0, len(values))
	for _, value := range values {
		summary := canonicalFactSummary(value.Kind, value.Value)
		if summary == "" {
			continue
		}
		id := strings.TrimSpace(value.FactID)
		if id == "" {
			id = shortDigest(value.Kind + "\x00" + value.Value)
		}
		candidates = append(candidates, ContextFact{
			ID:           "fact_" + shortDigest(id),
			Kind:         oneLine(value.Kind, 64),
			Summary:      summary,
			SessionCount: value.SessionCount,
			ObservedAt:   timePointer(value.ObservedAt),
			Sources: normalizeSources(
				value.Sources,
				MaxSourcesPerItem,
			),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].SessionCount != candidates[j].SessionCount {
			return candidates[i].SessionCount >
				candidates[j].SessionCount
		}
		leftObserved := timeValue(candidates[i].ObservedAt)
		rightObserved := timeValue(candidates[j].ObservedAt)
		if !leftObserved.Equal(rightObserved) {
			return leftObserved.After(
				rightObserved,
			)
		}
		if candidates[i].Kind != candidates[j].Kind {
			return candidates[i].Kind < candidates[j].Kind
		}
		if candidates[i].Summary != candidates[j].Summary {
			return candidates[i].Summary < candidates[j].Summary
		}
		return candidates[i].ID < candidates[j].ID
	})
	if len(candidates) > MaxContextFacts {
		candidates = candidates[:MaxContextFacts]
	}
	return candidates
}

func canonicalFactSummary(kind, value string) string {
	value = oneLine(value, 240)
	if value == "" {
		return ""
	}
	switch strings.TrimSpace(kind) {
	case CanonicalFactFileWritten:
		return oneLine("Frequently edited file: "+value, maxDisplayRunes)
	case CanonicalFactCommandExecutable:
		return oneLine("Common executable: "+value, maxDisplayRunes)
	case CanonicalFactMCPTool:
		return oneLine("Common MCP tool: "+value, maxDisplayRunes)
	case CanonicalFactTool:
		return oneLine("Common tool: "+value, maxDisplayRunes)
	default:
		return ""
	}
}

func buildWarnings(input BuildInput, pack Pack) []Warning {
	result := make([]Warning, 0, 8)
	analysisCurrent := pack.SourceState.AnalysisStatus ==
		AnalysisStatusCurrent &&
		pack.SourceState.AnalyzedGeneration >=
			pack.SourceState.TranscriptGeneration
	if !analysisCurrent {
		result = appendWarning(result, "analysis_pending")
	}
	switch input.Coverage.TranscriptStatus {
	case TranscriptCoverageComplete:
	default:
		result = appendWarning(result, "transcript_partial")
	}
	if len(pack.KnownTraps) == 0 {
		result = appendWarning(result, "no_recurring_traps")
	} else if len(pack.OperatingRules) == 0 &&
		input.Request.Harness != "" {
		result = appendWarning(result, "insight_unavailable")
	}
	if input.Request.Harness == "" {
		result = appendWarning(result, "harness_unspecified")
	}
	if input.Insight != nil &&
		(input.InsightStale ||
			pack.SourceState.AnalysisStatus == AnalysisStatusStale) {
		result = appendWarning(result, "insight_stale")
	}
	if len(pack.Verification) == 0 {
		result = appendWarning(result, "verification_unavailable")
	}
	if pack.Project.Branch == "" {
		result = appendWarning(result, "branch_unavailable")
	}
	if !input.Coverage.CanonicalContextAvailable {
		result = appendWarning(
			result,
			"canonical_context_unavailable",
		)
	}
	return result
}

func appendWarning(values []Warning, code string) []Warning {
	for _, value := range values {
		if value.Code == code {
			return values
		}
	}
	message := map[string]string{
		"analysis_pending":              "Transcript analysis is not current; this pack uses the latest completed snapshot.",
		"transcript_partial":            "Transcript coverage is partial; some prior-session guidance may be missing.",
		"no_recurring_traps":            "No issue has repeated across enough sessions to qualify as a known trap.",
		"insight_unavailable":           "No useful semantic insight is available; no topic-specific operating rule is included.",
		"insight_stale":                 "Semantic insight predates the latest analyzed evidence.",
		"harness_unspecified":           "Current harness was not supplied; harness-specific operating rules were omitted.",
		"verification_unavailable":      "No established verification command is available.",
		"branch_unavailable":            "The current Git branch is unavailable.",
		"canonical_context_unavailable": "Structured project context is unavailable.",
		"pack_truncated":                "Lower-ranked content was removed to keep this Mission Pack within its size budget.",
	}[code]
	return append(values, Warning{Code: code, Message: message})
}

func buildChecklist(pack Pack) []ChecklistItem {
	if len(pack.KnownTraps) == 0 &&
		len(pack.OperatingRules) == 0 &&
		len(pack.Verification) == 0 {
		return make([]ChecklistItem, 0)
	}
	texts := make([]string, 0, MaxChecklistItems)
	if len(pack.KnownTraps) > 0 {
		texts = append(
			texts,
			"Review the selected known traps before making changes.",
		)
	}
	for _, rule := range pack.OperatingRules {
		texts = append(texts, oneLine("Follow: "+rule.Guidance, maxDisplayRunes))
		if len(texts) == MaxChecklistItems-2 {
			break
		}
	}
	if len(pack.Verification) > 0 {
		text := "Run at least one listed verification command after the final edit."
		if pack.Intent == IntentReview || pack.Intent == IntentGeneral {
			text = "If files changed, run at least one listed verification command after the final edit."
		}
		texts = append(
			texts,
			text,
		)
	} else {
		text := "Inspect project scripts and choose an appropriate verification command."
		if pack.Intent == IntentReview || pack.Intent == IntentGeneral {
			text = "If files changed, inspect project scripts and choose an appropriate verification command."
		}
		texts = append(
			texts,
			text,
		)
	}
	texts = append(
		texts,
		"Report the verification result before claiming completion.",
	)
	if len(texts) > MaxChecklistItems {
		texts = texts[:MaxChecklistItems]
	}
	result := make([]ChecklistItem, 0, len(texts))
	for index, text := range texts {
		result = append(result, ChecklistItem{
			ID:   fmt.Sprintf("check_%d_%s", index+1, shortDigest(text)),
			Text: text,
		})
	}
	return result
}

func packStatus(pack Pack) string {
	if len(pack.Experiences) > 0 {
		return "ready"
	}
	if len(pack.KnownTraps) == 0 &&
		len(pack.OperatingRules) == 0 &&
		len(pack.Verification) == 0 {
		return "empty"
	}
	if len(pack.KnownTraps)+len(pack.OperatingRules) > 0 &&
		len(pack.Verification) > 0 &&
		len(pack.Warnings) == 0 {
		return "ready"
	}
	return "partial"
}

func setPackStatusAndTrust(pack *Pack) {
	pack.Status = packStatus(*pack)
	pack.Trust.InstructionAuthority = "none"
	pack.Trust.EvidenceState = "untrusted"
	if pack.Status == "empty" {
		pack.Trust.GuidanceState = "unavailable"
		pack.Trust.ActivationRequired = false
		return
	}
	pack.Trust.GuidanceState = "proposal"
	pack.Trust.ActivationRequired = true
}

func truncateToBudget(pack *Pack, anchorIssueID string) {
	for {
		pack.Completion = buildChecklist(*pack)
		setPackStatusAndTrust(pack)
		rendered := renderMarkdown(*pack)
		if withinBudget(rendered) {
			pack.RenderedMarkdown = rendered
			pack.EstimatedTokens = estimateTokens(rendered)
			return
		}
		if !pack.Truncated {
			pack.Truncated = true
			pack.Warnings = appendWarning(
				pack.Warnings,
				"pack_truncated",
			)
		}
		switch {
		case len(pack.Context.Facts) > 0:
			pack.Context.Facts = pack.Context.Facts[:len(pack.Context.Facts)-1]
		case len(pack.Verification) > 1:
			pack.Verification = pack.Verification[:len(pack.Verification)-1]
		case len(pack.OperatingRules) > 0:
			pack.OperatingRules = pack.OperatingRules[:len(pack.OperatingRules)-1]
		case len(pack.KnownTraps) > 2:
			pack.KnownTraps = pack.KnownTraps[:2]
		case stripSecondarySources(pack):
		case len(pack.KnownTraps) > 1:
			pack.KnownTraps = removeLowestNonAnchor(
				pack.KnownTraps,
				anchorIssueID,
			)
		default:
			pack.RenderedMarkdown = truncateMarkdown(rendered)
			pack.EstimatedTokens = estimateTokens(
				pack.RenderedMarkdown,
			)
			return
		}
	}
}

func stripSecondarySources(pack *Pack) bool {
	changed := false
	strip := func(values []SourceRef) []SourceRef {
		if len(values) <= 1 {
			return values
		}
		changed = true
		return values[:1]
	}
	for index := range pack.KnownTraps {
		pack.KnownTraps[index].Sources = strip(
			pack.KnownTraps[index].Sources,
		)
	}
	for index := range pack.OperatingRules {
		pack.OperatingRules[index].Sources = strip(
			pack.OperatingRules[index].Sources,
		)
	}
	for index := range pack.Verification {
		pack.Verification[index].Sources = strip(
			pack.Verification[index].Sources,
		)
	}
	for index := range pack.Context.Facts {
		pack.Context.Facts[index].Sources = strip(
			pack.Context.Facts[index].Sources,
		)
	}
	return changed
}

func removeLowestNonAnchor(
	values []GuidanceItem,
	anchorIssueID string,
) []GuidanceItem {
	if len(values) <= 1 {
		return values
	}
	anchorIssueID = boundedIdentifier(anchorIssueID)
	for index := len(values) - 1; index >= 0; index-- {
		if anchorIssueID != "" && guidanceHasIssue(
			values[index],
			anchorIssueID,
		) {
			continue
		}
		return append(values[:index:index], values[index+1:]...)
	}
	return values
}

func guidanceHasIssue(value GuidanceItem, issueID string) bool {
	for _, source := range value.Sources {
		if source.IssueID == issueID {
			return true
		}
	}
	return false
}

func renderMarkdown(pack Pack) string {
	if len(pack.Experiences) > 0 {
		return renderExperienceMarkdown(pack)
	}
	if pack.Status == "empty" {
		return "Belay found no useful guidance for this session.\n"
	}

	var builder strings.Builder
	builder.WriteString("# Mission Pack: ")
	builder.WriteString(markdownText(pack.Project.Label))
	builder.WriteString("\n\n")
	builder.WriteString(markdownText(strings.Title(string(pack.Intent))))
	if pack.Project.Branch != "" {
		builder.WriteString(" · ")
		builder.WriteString(markdownText(pack.Project.Branch))
	}
	builder.WriteByte('\n')

	if len(pack.KnownTraps) > 0 {
		builder.WriteString("\n## Known traps\n\n")
		for _, item := range pack.KnownTraps {
			builder.WriteString("- ")
			builder.WriteString(markdownText(item.Title))
			if item.WastedUSD != nil {
				fmt.Fprintf(&builder, " — $%.2f attributed", *item.WastedUSD)
			}
			builder.WriteByte('\n')
		}
	}
	if len(pack.OperatingRules) > 0 {
		builder.WriteString("\n## Proposed operating rules\n\n")
		for _, item := range pack.OperatingRules {
			builder.WriteString("- ")
			builder.WriteString(markdownText(item.Guidance))
			if item.TargetFile != "" {
				builder.WriteString(" (target: ")
				builder.WriteString(markdownText(item.TargetFile))
				builder.WriteByte(')')
			}
			builder.WriteByte('\n')
		}
	}
	if len(pack.Verification) > 0 {
		builder.WriteString("\n## Verification commands to consider\n\n")
		for _, item := range pack.Verification {
			builder.WriteString("- ")
			builder.WriteString(markdownText(item.Command))
			builder.WriteByte('\n')
		}
	}
	if len(pack.Completion) > 0 {
		builder.WriteString("\n## Completion checklist\n\n")
		for _, item := range pack.Completion {
			builder.WriteString("- [ ] ")
			builder.WriteString(markdownText(item.Text))
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func renderExperienceMarkdown(pack Pack) string {
	var builder strings.Builder
	builder.WriteString("# Mission Pack: ")
	builder.WriteString(markdownText(pack.Project.Label))
	builder.WriteString("\n\n## Project guidance\n\n")
	for _, item := range pack.Experiences {
		builder.WriteString("- Rule: ")
		builder.WriteString(markdownText(item.Guidance))
		builder.WriteString("\n  Apply when: ")
		builder.WriteString(markdownText(item.Applicability))
		for _, exception := range item.Exceptions {
			builder.WriteString("\n  Exception: ")
			builder.WriteString(markdownText(exception))
		}
		builder.WriteString(
			"\n  Plan: Before editing, map the rule and each exception to an explicit code predicate and required behavior.",
		)
		builder.WriteString(
			"\n  Proof: Verify the main rule and every exception path separately; an exception is required behavior, not permission to skip the rule.",
		)
		builder.WriteString(
			"\n  Boundary: Preserve existing behavior outside this rule and make the narrowest relevant change.",
		)
		builder.WriteString("\n  Verify: ")
		builder.WriteString(markdownText(item.Verifier.Summary))
		builder.WriteByte('\n')
	}

	builder.WriteString("\n## Completion\n\n")
	seen := make(map[string]bool, len(pack.Experiences))
	for _, item := range pack.Experiences {
		summary := item.Verifier.Summary
		if seen[summary] {
			continue
		}
		seen[summary] = true
		builder.WriteString("- ")
		builder.WriteString(markdownText(summary))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func plural(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
}

func withinBudget(markdown string) bool {
	return len(markdown) <= MaxRenderedMarkdown &&
		estimateTokens(markdown) <= MaxEstimatedTokens
}

func estimateTokens(value string) int {
	return (len([]byte(value)) + 3) / 4
}

func truncateMarkdown(value string) string {
	limit := min(MaxRenderedMarkdown, MaxEstimatedTokens*4)
	if len(value) <= limit {
		return value
	}
	suffix := "\n\n_[Mission Pack output truncated.]_\n"
	cut := limit - len(suffix)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut]) + suffix
}

func derivePackID(input BuildInput, pack Pack) string {
	taskSum := sha256.Sum256([]byte(input.Request.TaskHint))
	idGeneratorVersion := GeneratorVersion
	if len(pack.Experiences) == 0 {
		idGeneratorVersion = legacyGeneratorVersion
	}
	values := []string{
		SchemaVersion,
		idGeneratorVersion,
		input.Project.Identity,
		strings.TrimSpace(input.Request.IssueID),
		string(input.Request.Intent),
		string(input.Request.Harness),
		hex.EncodeToString(taskSum[:]),
		fmt.Sprintf("%d", pack.SourceState.TranscriptGeneration),
		fmt.Sprintf("%d", pack.SourceState.AnalyzedGeneration),
		pack.Project.Branch,
	}
	if input.Insight != nil {
		values = append(values, input.Insight.InputHash)
	}
	for _, item := range pack.KnownTraps {
		values = append(values, item.ID)
	}
	for _, item := range pack.OperatingRules {
		values = append(values, item.ID)
	}
	for _, item := range pack.Verification {
		values = append(values, item.ID)
	}
	for _, item := range pack.Context.Facts {
		values = append(values, item.ID)
	}
	if len(pack.Experiences) > 0 {
		values = append(
			values,
			"experience_generation",
			fmt.Sprintf("%d", pack.ExperienceGeneration),
		)
		for _, item := range pack.Experiences {
			values = append(
				values,
				"experience",
				item.ExperienceID,
				fmt.Sprintf("%d", item.Version),
			)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return "mpk_" + strings.ToLower(packIDEncoding.EncodeToString(sum[:]))
}

func normalizeSources(values []SourceRef, limit int) []SourceRef {
	seen := make(map[string]bool, len(values))
	result := make([]SourceRef, 0, min(len(values), limit))
	for _, value := range values {
		value.Kind = oneLine(value.Kind, 64)
		value.IssueID = boundedIdentifier(value.IssueID)
		value.InsightID = boundedIdentifier(value.InsightID)
		value.CandidateID = boundedIdentifier(value.CandidateID)
		value.EpisodeID = boundedIdentifier(value.EpisodeID)
		value.SessionKey = boundedIdentifier(value.SessionKey)
		value.EventID = boundedIdentifier(value.EventID)
		value.ProjectFile = boundedIdentifier(value.ProjectFile)
		value.SourceSHA256 = boundedIdentifier(value.SourceSHA256)
		value.SourceFileID = boundedIdentifier(value.SourceFileID)
		if value.ObservedAt != nil {
			value.ObservedAt = timePointer(*value.ObservedAt)
		}
		key := sourceKey(value)
		if value.Kind == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return sourceKey(result[i]) < sourceKey(result[j])
	})
	if len(result) > limit {
		result = result[:limit]
	}
	if result == nil {
		return make([]SourceRef, 0)
	}
	return result
}

func sourceKey(value SourceRef) string {
	turn := int64(-1)
	if value.TurnIndex != nil {
		turn = *value.TurnIndex
	}
	offset := int64(-1)
	if value.JSONLByteOffset != nil {
		offset = *value.JSONLByteOffset
	}
	observedAt := ""
	if value.ObservedAt != nil {
		observedAt = value.ObservedAt.UTC().Format(timeLayout)
	}
	return strings.Join([]string{
		value.Kind,
		value.IssueID,
		value.InsightID,
		value.CandidateID,
		value.EpisodeID,
		value.SessionKey,
		fmt.Sprintf("%d", turn),
		value.EventID,
		value.ProjectFile,
		value.SourceSHA256,
		value.SourceFileID,
		fmt.Sprintf("%d", offset),
		observedAt,
	}, "\x00")
}

const timeLayout = "2006-01-02T15:04:05.999999999Z07:00"

func lexicalTokens(value string) map[string]struct{} {
	result := make(map[string]struct{})
	var token strings.Builder
	flush := func() {
		current := token.String()
		token.Reset()
		if len(current) < 2 || lexicalStopWords[current] {
			return
		}
		result[current] = struct{}{}
	}
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			token.WriteRune(character)
			continue
		}
		flush()
	}
	flush()
	return result
}

var lexicalStopWords = map[string]bool{
	"about": true, "after": true, "again": true, "agent": true,
	"all": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "before": true, "by": true, "change": true,
	"code": true, "complete": true, "done": true, "each": true,
	"file": true, "fix": true, "for": true, "from": true,
	"guidance": true, "has": true, "have": true, "if": true,
	"implement": true, "in": true, "into": true, "is": true,
	"it": true, "make": true, "not": true, "of": true, "on": true,
	"only": true, "or": true, "project": true, "review": true,
	"rule": true, "run": true, "session": true, "should": true,
	"task": true, "that": true, "the": true, "then": true,
	"this": true, "to": true, "update": true, "use": true,
	"was": true, "were": true, "when": true, "with": true,
	"work": true, "your": true,
}

func oneLine(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes == 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func sortedDistinctLines(values []string, maxRunes int) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = oneLine(value, maxRunes)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func markdownText(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"*", "\\*",
		"_", "\\_",
		"[", "\\[",
		"]", "\\]",
		"<", "\\<",
		">", "\\>",
		"#", "\\#",
	)
	return replacer.Replace(value)
}

func boundedIdentifier(value string) string {
	return oneLine(value, 256)
}

func shortDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	result := value.UTC()
	return &result
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}

func ensureNonNil(pack *Pack) {
	if pack.Context.Harnesses == nil {
		pack.Context.Harnesses = make([]string, 0)
	}
	if pack.Context.Facts == nil {
		pack.Context.Facts = make([]ContextFact, 0)
	}
	if pack.KnownTraps == nil {
		pack.KnownTraps = make([]GuidanceItem, 0)
	}
	if pack.OperatingRules == nil {
		pack.OperatingRules = make([]GuidanceItem, 0)
	}
	if pack.Verification == nil {
		pack.Verification = make([]CommandItem, 0)
	}
	if pack.Completion == nil {
		pack.Completion = make([]ChecklistItem, 0)
	}
	if pack.Warnings == nil {
		pack.Warnings = make([]Warning, 0)
	}
	for index := range pack.Experiences {
		if pack.Experiences[index].Exceptions == nil {
			pack.Experiences[index].Exceptions = make([]string, 0)
		}
		if pack.Experiences[index].Sources == nil {
			pack.Experiences[index].Sources = make([]SourceRef, 0)
		}
	}
	for index := range pack.KnownTraps {
		if pack.KnownTraps[index].Sources == nil {
			pack.KnownTraps[index].Sources = make([]SourceRef, 0)
		}
	}
	for index := range pack.OperatingRules {
		if pack.OperatingRules[index].Sources == nil {
			pack.OperatingRules[index].Sources = make([]SourceRef, 0)
		}
	}
	for index := range pack.Verification {
		if pack.Verification[index].Sources == nil {
			pack.Verification[index].Sources = make([]SourceRef, 0)
		}
	}
	for index := range pack.Context.Facts {
		if pack.Context.Facts[index].Sources == nil {
			pack.Context.Facts[index].Sources = make([]SourceRef, 0)
		}
	}
}
