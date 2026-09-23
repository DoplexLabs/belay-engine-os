package localapp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

const (
	InsightPromptVersion               = "belay.insight-prompt.v5"
	maxSemanticProjects                = 100
	maxSemanticCommandOutput           = 4 << 20
	maxClaudeInlineSchema              = 64 << 10
	maxSemanticPromptAndSchema         = 512 << 10
	maxSemanticIssues                  = 32
	maxSemanticCandidates              = 64
	maxSemanticExcerptsPerIssue        = 3
	maxSemanticEvidenceTextBytes       = 1536
	maxSemanticHeadlineBytes           = 512
	maxSemanticSuggestedRationaleBytes = 768
	maxSemanticIdentifierBytes         = 256
	maxSemanticErrorDetail             = 512
	semanticHarnessWaitDelay           = 2 * time.Second
	defaultSemanticRunTimeout          = 5 * time.Minute
)

const semanticClipMarker = "\n[... Belay clipped content ...]\n"

// claudeSemanticSettings turns off every hook for Belay's own `claude -p`
// run. The run inherits the user's settings, where Belay's monitor hooks
// would otherwise record each background analysis call as a new live
// Claude Code session. `--bare` also skips hooks but stops reading OAuth
// credentials, which breaks subscription sign-in.
const claudeSemanticSettings = `{"disableAllHooks":true}`

type SemanticHarness string

const (
	SemanticHarnessClaude SemanticHarness = "claude"
	SemanticHarnessCodex  SemanticHarness = "codex"
	SemanticHarnessCursor SemanticHarness = "cursor"
	// SemanticHarnessAntigravity runs the semantic pass through the
	// Antigravity CLI (`agy`), whose headless mode binds its reply to a JSON
	// schema. The Antigravity IDE itself has no headless mode.
	SemanticHarnessAntigravity SemanticHarness = "antigravity"
)

// semanticHarnessLabel names a harness the way its vendor does, so prompts
// describe the running agent accurately.
func semanticHarnessLabel(harness SemanticHarness) string {
	switch harness {
	case SemanticHarnessClaude:
		return "Claude Code"
	case SemanticHarnessCodex:
		return "Codex"
	case SemanticHarnessCursor:
		return "Cursor Agent"
	case SemanticHarnessAntigravity:
		return "Antigravity"
	default:
		return string(harness)
	}
}

type SemanticInsightStore interface {
	ListInsightProjects(context.Context, int) ([]issueintel.Project, error)
	LoadSemanticInput(
		context.Context,
		issueintel.Project,
	) (issueintel.SemanticInput, error)
	ReplaceProjectInsight(context.Context, issueintel.InsightRecord) error
	GetProjectInsight(
		context.Context,
		string,
	) (issueintel.InsightRecord, error)
}

type SemanticHarnessResult struct {
	Result issueintel.InsightResult
	Model  string
}

type ExperienceSemanticHarnessResult struct {
	Output []byte
	Model  string
}

type SemanticHarnessRunner func(
	context.Context,
	SemanticHarness,
	[]byte,
	[]byte,
) (SemanticHarnessResult, error)

type ExperienceSemanticHarnessRunner func(
	context.Context,
	SemanticHarness,
	[]byte,
	[]byte,
) (ExperienceSemanticHarnessResult, error)

type semanticRawHarnessResult struct {
	body  []byte
	model string
}

type SemanticAnalysisReport struct {
	Projects                     int                  `json:"projects"`
	Skipped                      int                  `json:"skipped"`
	Clusters                     int                  `json:"clusters"`
	Fixes                        int                  `json:"fixes"`
	SanitizedProjects            int                  `json:"sanitized_projects"`
	DroppedClusters              int                  `json:"dropped_clusters"`
	DroppedFixes                 int                  `json:"dropped_fixes"`
	ExperienceProjectsConsidered int                  `json:"experience_projects_considered"`
	ExperienceProjectsCompiled   int                  `json:"experience_projects_compiled"`
	ExperienceProjectsAnalyzed   int                  `json:"experience_projects_analyzed"`
	ExperienceProjectFailures    int                  `json:"experience_project_failures"`
	ExperienceFailureDetails     []string             `json:"experience_failure_details,omitempty"`
	ExperienceCandidatesInserted int                  `json:"experience_candidates_inserted"`
	ExperienceCandidatesReplayed int                  `json:"experience_candidates_replayed"`
	ExperienceProposals          int                  `json:"experience_proposals"`
	ExperienceRejections         int                  `json:"experience_rejections"`
	ExperienceDefers             int                  `json:"experience_defers"`
	Habits                       *HabitAnalysisReport `json:"habits,omitempty"`
}

func (report *SemanticAnalysisReport) AddExperience(
	experienceReport ExperienceProjectAnalysisReport,
) {
	if report == nil {
		return
	}
	report.ExperienceProjectsConsidered +=
		experienceReport.ProjectsConsidered
	report.ExperienceProjectsCompiled += experienceReport.ProjectsCompiled
	report.ExperienceProjectsAnalyzed += experienceReport.ProjectsAnalyzed
	report.ExperienceProjectFailures += experienceReport.ProjectFailures
	report.ExperienceFailureDetails = append(
		report.ExperienceFailureDetails,
		experienceReport.FailureDetails...,
	)
	report.ExperienceCandidatesInserted +=
		experienceReport.CandidatesInserted
	report.ExperienceCandidatesReplayed +=
		experienceReport.CandidatesReplayed
	report.ExperienceProposals += experienceReport.Proposals
	report.ExperienceRejections += experienceReport.Rejections
	report.ExperienceDefers += experienceReport.Defers
}

func AnalyzeSemanticProjects(
	ctx context.Context,
	store SemanticInsightStore,
	harness SemanticHarness,
	runner SemanticHarnessRunner,
) (SemanticAnalysisReport, error) {
	if store == nil || runner == nil {
		return SemanticAnalysisReport{}, errors.New(
			"semantic analysis requires a store and harness runner",
		)
	}
	if !harness.Valid() {
		return SemanticAnalysisReport{}, errors.New("invalid semantic harness")
	}
	projects, err := store.ListInsightProjects(ctx, maxSemanticProjects)
	if err != nil {
		return SemanticAnalysisReport{}, err
	}
	var report SemanticAnalysisReport
	var analysisErrors []error
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(append(analysisErrors, err)...)
		}
		input, err := store.LoadSemanticInput(ctx, project)
		if err != nil {
			analysisErrors = append(analysisErrors, err)
			continue
		}
		boundedInput, prompt, schema, inputHash, err := prepareSemanticPrompt(
			harness,
			input,
		)
		if err != nil {
			analysisErrors = append(analysisErrors, err)
			continue
		}
		existing, existingErr := store.GetProjectInsight(
			ctx,
			project.Identity,
		)
		if existingErr == nil &&
			existing.Harness == string(harness) &&
			existing.PromptVersion == InsightPromptVersion &&
			existing.InputHash == inputHash {
			report.Skipped++
			continue
		}
		result, err := runner(ctx, harness, prompt, schema)
		if err != nil {
			analysisErrors = append(analysisErrors, err)
			continue
		}
		sanitized, diagnostics := sanitizeSemanticResult(
			boundedInput,
			result.Result,
		)
		if diagnostics.DroppedClusters > 0 ||
			diagnostics.DroppedFixes > 0 {
			report.SanitizedProjects++
			report.DroppedClusters += diagnostics.DroppedClusters
			report.DroppedFixes += diagnostics.DroppedFixes
		}
		generatedAt := time.Now().UTC()
		record := issueintel.InsightRecord{
			InsightID: "ins_" + semanticDigest(
				project.Identity,
				string(harness),
				InsightPromptVersion,
				inputHash,
			),
			Project:       project,
			Harness:       string(harness),
			Model:         semanticModelName(result.Model),
			PromptVersion: InsightPromptVersion,
			InputHash:     inputHash,
			GeneratedAt:   generatedAt,
			Result:        sanitized,
			Sanitization:  diagnostics,
		}
		if err := store.ReplaceProjectInsight(ctx, record); err != nil {
			analysisErrors = append(analysisErrors, err)
			continue
		}
		report.Projects++
		report.Clusters += len(sanitized.Clusters)
		report.Fixes += len(sanitized.Fixes)
	}
	return report, errors.Join(analysisErrors...)
}

func semanticModelName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func (h SemanticHarness) Valid() bool {
	return h == SemanticHarnessClaude ||
		h == SemanticHarnessCodex ||
		h == SemanticHarnessCursor ||
		h == SemanticHarnessAntigravity
}

func InstalledSemanticHarness(preferred string) (SemanticHarness, bool) {
	switch strings.ToLower(strings.TrimSpace(preferred)) {
	case "claude", "claude-code":
		_, err := exec.LookPath("claude")
		return SemanticHarnessClaude, err == nil
	case "codex":
		_, err := exec.LookPath("codex")
		return SemanticHarnessCodex, err == nil
	case "cursor", "cursor-agent":
		// The Cursor CLI runs in read-only ask mode and returns text, which
		// Belay validates against its own output schema.
		_, ok := cursorAgentPath()
		return SemanticHarnessCursor, ok
	case "antigravity", "agy":
		// The Antigravity CLI, not the IDE, runs the pass; its headless mode
		// binds the reply to Belay's output schema.
		_, ok := antigravityCLIPath()
		return SemanticHarnessAntigravity, ok
	case "", "auto":
		// Schema-enforcing harnesses first, then the CLIs whose replies Belay
		// validates itself.
		if _, err := exec.LookPath("claude"); err == nil {
			return SemanticHarnessClaude, true
		}
		if _, err := exec.LookPath("codex"); err == nil {
			return SemanticHarnessCodex, true
		}
		if _, ok := cursorAgentPath(); ok {
			return SemanticHarnessCursor, true
		}
		if _, ok := antigravityCLIPath(); ok {
			return SemanticHarnessAntigravity, true
		}
	}
	return "", false
}

func RunInstalledSemanticHarness(
	ctx context.Context,
	harness SemanticHarness,
	prompt []byte,
	schema []byte,
) (SemanticHarnessResult, error) {
	raw, err := runInstalledSemanticHarnessRaw(ctx, harness, prompt, schema)
	if err != nil {
		return SemanticHarnessResult{}, err
	}
	return decodeSemanticResult(raw.body, raw.model)
}

func RunInstalledExperienceSemanticHarness(
	ctx context.Context,
	harness SemanticHarness,
	prompt []byte,
	schema []byte,
) (ExperienceSemanticHarnessResult, error) {
	raw, err := runInstalledSemanticHarnessRaw(ctx, harness, prompt, schema)
	if err != nil {
		return ExperienceSemanticHarnessResult{}, err
	}
	return ExperienceSemanticHarnessResult{
		Output: append([]byte(nil), raw.body...),
		Model:  raw.model,
	}, nil
}

func runInstalledSemanticHarnessRaw(
	ctx context.Context,
	harness SemanticHarness,
	prompt []byte,
	schema []byte,
) (semanticRawHarnessResult, error) {
	if !harness.Valid() {
		return semanticRawHarnessResult{}, errors.New(
			"invalid semantic harness",
		)
	}
	runCtx, cancel := context.WithTimeout(ctx, defaultSemanticRunTimeout)
	defer cancel()
	tempDir, err := os.MkdirTemp("", "belay-analyze-*")
	if err != nil {
		return semanticRawHarnessResult{}, errors.New(
			"create semantic analysis workspace",
		)
	}
	defer os.RemoveAll(tempDir)
	schemaPath := filepath.Join(tempDir, "schema.json")
	if err := os.WriteFile(schemaPath, schema, 0o600); err != nil {
		return semanticRawHarnessResult{}, errors.New(
			"write semantic output schema",
		)
	}
	var name string
	var executable string
	var args []string
	var outputPath string
	stdin := prompt
	decode := decodeClaudeSemanticPayload
	switch harness {
	case SemanticHarnessClaude:
		if len(schema) > maxClaudeInlineSchema {
			return semanticRawHarnessResult{}, errors.New(
				"Claude semantic output schema exceeds inline safety limit",
			)
		}
		name = "claude"
		args = []string{
			"-p",
			"--input-format",
			"text",
			"--output-format",
			"json",
			"--json-schema",
			string(schema),
			"--tools",
			"",
			"--no-session-persistence",
			"--settings",
			claudeSemanticSettings,
		}
	case SemanticHarnessCodex:
		name = "codex"
		outputPath = filepath.Join(tempDir, "result.json")
		args = []string{
			"exec",
			"--json",
			"--ephemeral",
			"--sandbox",
			"read-only",
			"--skip-git-repo-check",
			"--output-schema",
			schemaPath,
			"--output-last-message",
			outputPath,
			"-",
		}
	case SemanticHarnessCursor:
		name = "Cursor CLI"
		path, ok := cursorAgentPath()
		if !ok {
			return semanticRawHarnessResult{}, errors.New(
				"Cursor CLI harness is not installed",
			)
		}
		executable = path
		args = cursorSemanticArgs(tempDir)
		decode = func(body []byte) (semanticRawHarnessResult, error) {
			return decodeCursorSemanticPayload(body, schema)
		}
	case SemanticHarnessAntigravity:
		name = "Antigravity CLI"
		path, ok := antigravityCLIPath()
		if !ok {
			return semanticRawHarnessResult{}, errors.New(
				"Antigravity CLI harness is not installed",
			)
		}
		executable = path
		args = antigravitySemanticArgs(schemaPath)
		framed, err := antigravityStreamJSONPrompt(prompt)
		if err != nil {
			return semanticRawHarnessResult{}, err
		}
		stdin = framed
		decode = func(body []byte) (semanticRawHarnessResult, error) {
			return decodeAntigravitySemanticPayload(body, schema)
		}
	}
	if executable == "" {
		path, err := exec.LookPath(name)
		if err != nil {
			return semanticRawHarnessResult{}, fmt.Errorf(
				"%s harness is not installed",
				name,
			)
		}
		executable = path
	}
	command := exec.CommandContext(runCtx, executable, args...)
	configureSemanticHarnessCommand(command)
	command.Dir = tempDir
	command.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &boundedWriter{
		writer: &stdout,
		limit:  maxSemanticCommandOutput,
	}
	command.Stderr = &boundedWriter{
		writer: &stderr,
		limit:  maxSemanticCommandOutput,
	}
	if err := command.Run(); err != nil {
		return semanticRawHarnessResult{}, semanticHarnessFailure(
			name,
			err,
			stderr.Bytes(),
			prompt,
		)
	}
	if outputPath != "" {
		body, err := os.ReadFile(outputPath)
		if err != nil {
			return semanticRawHarnessResult{}, errors.New(
				"read Codex semantic output",
			)
		}
		return semanticRawHarnessResult{body: body}, nil
	}
	return decode(stdout.Bytes())
}

func semanticHarnessFailure(
	name string,
	runErr error,
	stderr []byte,
	prompt []byte,
) error {
	detail := sanitizeSemanticHarnessStderr(stderr, prompt)
	if detail == "" {
		return fmt.Errorf("%s semantic analysis failed: %w", name, runErr)
	}
	return fmt.Errorf(
		"%s semantic analysis failed: %w; harness stderr: %s",
		name,
		runErr,
		detail,
	)
}

func sanitizeSemanticHarnessStderr(stderr []byte, prompt []byte) string {
	value := strings.ToValidUTF8(string(stderr), "\uFFFD")
	promptText := string(prompt)
	if promptText != "" {
		value = strings.ReplaceAll(value, promptText, "[prompt redacted]")
		prefixLength := min(len(promptText), 128)
		if prefixLength >= 32 &&
			strings.Contains(value, promptText[:prefixLength]) {
			return "[prompt content redacted]"
		}
	}
	value = scrubTranscriptMetadata(value)
	value = safeMCPOutput(value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > maxSemanticErrorDetail {
		value = string(runes[:maxSemanticErrorDetail]) + "…"
	}
	return value
}

type semanticPromptPayload struct {
	Issues     []semanticPromptIssue     `json:"issues"`
	Candidates []semanticPromptCandidate `json:"correction_candidates"`
}

type semanticPromptIssue struct {
	IssueID      string                  `json:"issue_id"`
	DetectorID   string                  `json:"detector_id"`
	Headline     string                  `json:"headline"`
	SuggestedFix issueintel.SuggestedFix `json:"suggested_fix"`
	Excerpts     []issueintel.Excerpt    `json:"excerpts"`
}

type semanticPromptCandidate struct {
	CandidateID string              `json:"candidate_id"`
	Text        string              `json:"text"`
	Citation    issueintel.Citation `json:"citation"`
}

func semanticPrompt(
	harness SemanticHarness,
	input issueintel.SemanticInput,
) ([]byte, []byte, string, error) {
	_, prompt, schema, inputHash, err := prepareSemanticPrompt(harness, input)
	return prompt, schema, inputHash, err
}

func prepareSemanticPrompt(
	harness SemanticHarness,
	input issueintel.SemanticInput,
) (issueintel.SemanticInput, []byte, []byte, string, error) {
	if !harness.Valid() {
		return issueintel.SemanticInput{}, nil, nil, "", errors.New(
			"invalid semantic harness",
		)
	}
	bounded := boundedSemanticInput(input)
	for {
		prompt, schema, inputHash, err := semanticPromptFromBounded(
			harness,
			bounded,
		)
		if err != nil {
			return issueintel.SemanticInput{}, nil, nil, "", err
		}
		if len(schema) <= maxClaudeInlineSchema &&
			len(prompt)+len(schema) <= maxSemanticPromptAndSchema {
			return bounded, prompt, schema, inputHash, nil
		}
		switch {
		case len(bounded.CorrectionCandidates) > 0:
			bounded.CorrectionCandidates =
				bounded.CorrectionCandidates[:len(bounded.CorrectionCandidates)-1]
		case len(bounded.Issues) > 0:
			bounded.Issues = bounded.Issues[:len(bounded.Issues)-1]
		default:
			return issueintel.SemanticInput{}, nil, nil, "", errors.New(
				"semantic prompt exceeds safety limit",
			)
		}
	}
}

func boundedSemanticInput(
	input issueintel.SemanticInput,
) issueintel.SemanticInput {
	result := issueintel.SemanticInput{
		Project: issueintel.Project{
			Identity: input.Project.Identity,
		},
		Issues: make(
			[]issueintel.Issue,
			0,
			min(len(input.Issues), maxSemanticIssues),
		),
	}
	seenIssues := make(map[string]bool, maxSemanticIssues)
	for _, issue := range input.Issues {
		if len(result.Issues) == maxSemanticIssues {
			break
		}
		if !validSemanticIdentifier(issue.IssueID) ||
			seenIssues[issue.IssueID] {
			continue
		}
		seenIssues[issue.IssueID] = true
		result.Issues = append(result.Issues, boundedSemanticIssue(issue))
	}

	linkedCitations := make(map[string]bool)
	for _, issue := range result.Issues {
		for _, excerpt := range issue.Excerpts {
			if key := semanticCitationKey(excerpt.Citation); key != "" {
				linkedCitations[key] = true
			}
		}
	}
	candidates := append(
		[]issueintel.CorrectionCandidate(nil),
		input.CorrectionCandidates...,
	)
	sort.Slice(candidates, func(i, j int) bool {
		leftLinked := linkedCitations[semanticCitationKey(candidates[i].Citation)]
		rightLinked := linkedCitations[semanticCitationKey(candidates[j].Citation)]
		if leftLinked != rightLinked {
			return leftLinked
		}
		leftTime := semanticCandidateTime(candidates[i])
		rightTime := semanticCandidateTime(candidates[j])
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		if candidates[i].CandidateID != candidates[j].CandidateID {
			return candidates[i].CandidateID < candidates[j].CandidateID
		}
		leftCitation := semanticCitationSortKey(candidates[i].Citation)
		rightCitation := semanticCitationSortKey(candidates[j].Citation)
		if leftCitation != rightCitation {
			return leftCitation < rightCitation
		}
		return candidates[i].Text < candidates[j].Text
	})
	result.CorrectionCandidates = make(
		[]issueintel.CorrectionCandidate,
		0,
		min(len(candidates), maxSemanticCandidates),
	)
	seenCandidates := make(map[string]bool, maxSemanticCandidates)
	for _, candidate := range candidates {
		if len(result.CorrectionCandidates) == maxSemanticCandidates {
			break
		}
		if !semanticCandidateForHarness(candidate) {
			continue
		}
		if !validSemanticIdentifier(candidate.CandidateID) ||
			seenCandidates[candidate.CandidateID] {
			continue
		}
		seenCandidates[candidate.CandidateID] = true
		candidate.Project = issueintel.Project{}
		candidate.Text = clipSemanticText(
			candidate.Text,
			maxSemanticEvidenceTextBytes,
		)
		candidate.Marker = ""
		result.CorrectionCandidates = append(
			result.CorrectionCandidates,
			candidate,
		)
	}
	return result
}

func boundedSemanticIssue(issue issueintel.Issue) issueintel.Issue {
	issue.Fingerprint = ""
	issue.Project = issueintel.Project{}
	issue.Headline = clipSemanticText(
		issue.Headline,
		maxSemanticHeadlineBytes,
	)
	issue.SuggestedFix.Kind = clipSemanticText(issue.SuggestedFix.Kind, 64)
	issue.SuggestedFix.TargetFile = clipSemanticText(
		issue.SuggestedFix.TargetFile,
		160,
	)
	issue.SuggestedFix.Rationale = clipSemanticText(
		issue.SuggestedFix.Rationale,
		maxSemanticSuggestedRationaleBytes,
	)
	excerptCount := min(len(issue.Excerpts), maxSemanticExcerptsPerIssue)
	excerpts := make([]issueintel.Excerpt, 0, excerptCount)
	for _, excerpt := range issue.Excerpts[:excerptCount] {
		excerpt.Text = clipSemanticText(
			excerpt.Text,
			maxSemanticEvidenceTextBytes,
		)
		excerpt.ToolName = clipSemanticText(excerpt.ToolName, 128)
		excerpts = append(excerpts, excerpt)
	}
	issue.Excerpts = excerpts
	return issue
}

func validSemanticIdentifier(value string) bool {
	return value != "" &&
		len(value) <= maxSemanticIdentifierBytes &&
		utf8.ValidString(value) &&
		!strings.ContainsAny(value, "\r\n")
}

func semanticCitationKey(citation issueintel.Citation) string {
	if citation.SessionKey == "" {
		return ""
	}
	return citation.SessionKey + "\x00" +
		fmt.Sprintf("%d", citation.TurnIndex)
}

func semanticCitationSortKey(citation issueintel.Citation) string {
	return semanticCitationKey(citation) + "\x00" +
		citation.SourceFileID + "\x00" +
		fmt.Sprintf("%d", citation.JSONLByteOffset) + "\x00" +
		citation.OccurredAt.UTC().Format(time.RFC3339Nano)
}

func semanticCandidateTime(
	candidate issueintel.CorrectionCandidate,
) time.Time {
	if !candidate.OccurredAt.IsZero() {
		return candidate.OccurredAt
	}
	return candidate.Citation.OccurredAt
}

func semanticCandidateForHarness(
	candidate issueintel.CorrectionCandidate,
) bool {
	if semanticMachineEnvelope(candidate.Text) {
		return false
	}
	if strings.TrimSpace(candidate.Marker) != "" {
		return true
	}
	return !semanticAcknowledgementOnly(candidate.Text)
}

func semanticAcknowledgementOnly(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			return character
		}
		return ' '
	}, normalized)
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "thanks",
		"thank you",
		"thanks for the update",
		"thank you for the update",
		"ok",
		"okay",
		"ok thanks",
		"okay thanks",
		"got it",
		"got it thanks",
		"sounds good",
		"sounds good to me",
		"great",
		"great thanks",
		"perfect":
		return true
	default:
		return false
	}
}

func semanticMachineEnvelope(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{
		"<task-notification",
		"<task_notification",
		"<system-reminder",
		"<system_reminder",
		"<subagent-notification",
		"<subagent_notification",
		"[task-notification",
		"[task_notification",
		"[system-reminder",
		"[system_reminder",
		"[subagent-notification",
		"[subagent_notification",
	} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	if !strings.HasPrefix(normalized, "{") {
		return false
	}
	compact := strings.NewReplacer(
		" ", "",
		"\t", "",
		"\r", "",
		"\n", "",
	).Replace(normalized)
	return strings.Contains(compact, `"type":"task_notification"`) ||
		strings.Contains(compact, `"type":"task-notification"`) ||
		strings.Contains(compact, `"type":"system_reminder"`) ||
		strings.Contains(compact, `"type":"system-reminder"`) ||
		strings.Contains(compact, `"type":"subagent_notification"`) ||
		strings.Contains(compact, `"type":"subagent-notification"`)
}

func clipSemanticText(value string, maximumBytes int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if maximumBytes <= 0 {
		return ""
	}
	if len(value) <= maximumBytes {
		return value
	}
	if maximumBytes <= len(semanticClipMarker) {
		return semanticClipMarker[:maximumBytes]
	}
	contentBytes := maximumBytes - len(semanticClipMarker)
	headBytes := (contentBytes + 1) / 2
	tailBytes := contentBytes - headBytes
	for headBytes > 0 && !utf8.RuneStart(value[headBytes]) {
		headBytes--
	}
	tailStart := len(value) - tailBytes
	for tailStart < len(value) && !utf8.RuneStart(value[tailStart]) {
		tailStart++
	}
	return value[:headBytes] + semanticClipMarker + value[tailStart:]
}

func semanticPromptFromBounded(
	harness SemanticHarness,
	input issueintel.SemanticInput,
) ([]byte, []byte, string, error) {
	payload := semanticPromptPayload{
		Issues: make([]semanticPromptIssue, 0, len(input.Issues)),
		Candidates: make(
			[]semanticPromptCandidate,
			0,
			len(input.CorrectionCandidates),
		),
	}
	for _, issue := range input.Issues {
		payload.Issues = append(payload.Issues, semanticPromptIssue{
			IssueID:      issue.IssueID,
			DetectorID:   issue.DetectorID,
			Headline:     issue.Headline,
			SuggestedFix: issue.SuggestedFix,
			Excerpts:     issue.Excerpts,
		})
	}
	for _, candidate := range input.CorrectionCandidates {
		payload.Candidates = append(
			payload.Candidates,
			semanticPromptCandidate{
				CandidateID: candidate.CandidateID,
				Text:        candidate.Text,
				Citation:    candidate.Citation,
			},
		)
	}
	inputBody, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, "", errors.New("encode semantic candidates")
	}
	schema, err := semanticOutputSchema(payload)
	if err != nil {
		return nil, nil, "", err
	}
	preferredTarget := semanticPreferredTarget(harness)
	instructions := []byte(
		"You are refining deterministic Belay Local findings. " +
			"Do not use tools, inspect files, or add facts. " +
			"Use only the JSON candidates and bounded verbatim excerpts below. " +
			"Cluster correction candidates by durable rule topic. " +
			"Return a fix only when the supplied evidence supports a specific, " +
			"durable rule. You may omit issues whose evidence does not support " +
			"such a rule. Never invent a rule merely to cover every issue. " +
			"Each returned fix rule must be concise and single-line. " +
			"For durable project rules in this " +
			semanticHarnessLabel(harness) +
			" analysis, prefer " + preferredTarget + ". " +
			"Use another approved target only when the evidence clearly requires it. " +
			"Approved targets are CLAUDE.md, AGENTS.md, .claude/settings.json, " +
			".codex/rules/default.rules, and .agents/rules/belay.md. Use each " +
			"issue ID and correction " +
			"candidate ID at most once across the entire response. Return only " +
			"schema-valid JSON.\n\n",
	)
	prompt := append(instructions, inputBody...)
	sum := sha256.Sum256(append(
		[]byte(
			InsightPromptVersion+"\x00"+string(harness)+"\x00"+
				preferredTarget+"\x00",
		),
		inputBody...,
	))
	return prompt, schema, hex.EncodeToString(sum[:]), nil
}

func semanticPreferredTarget(harness SemanticHarness) string {
	// Codex and Cursor both read AGENTS.md at the project root; Cursor has
	// no CLAUDE.md. Antigravity reads project rules only from
	// .agents/rules/*.md, so an Antigravity CLI analysis prefers Belay's own
	// rule file there; Mission Packs adapt it for the other harnesses.
	switch harness {
	case SemanticHarnessCodex, SemanticHarnessCursor:
		return "AGENTS.md"
	case SemanticHarnessAntigravity:
		return antigravityProjectRuleFile
	default:
		return "CLAUDE.md"
	}
}

func semanticOutputSchema(
	payload semanticPromptPayload,
) ([]byte, error) {
	issueIDs := make([]string, 0, len(payload.Issues))
	for _, issue := range payload.Issues {
		issueIDs = append(issueIDs, issue.IssueID)
	}
	candidateIDs := make([]string, 0, len(payload.Candidates))
	for _, candidate := range payload.Candidates {
		candidateIDs = append(candidateIDs, candidate.CandidateID)
	}
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"clusters", "fixes"},
		"properties": map[string]any{
			"clusters": map[string]any{
				"type":     "array",
				"maxItems": len(payload.Candidates),
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"candidate_ids",
						"topic",
						"rule_text",
						"target_file",
						"confidence",
					},
					"properties": map[string]any{
						"candidate_ids": map[string]any{
							"type":        "array",
							"minItems":    1,
							"maxItems":    len(payload.Candidates),
							"uniqueItems": true,
							"items": semanticIdentifierSchema(
								candidateIDs,
							),
						},
						"topic":       insightLineSchema(120),
						"rule_text":   insightLineSchema(500),
						"target_file": insightTargetSchema(),
						"confidence": map[string]any{
							"type":    "number",
							"minimum": 0,
							"maximum": 1,
						},
					},
				},
			},
			"fixes": map[string]any{
				"type": "array",
				"description": "Include only issues whose supplied evidence " +
					"supports a specific durable rule; omit unsupported issues.",
				"minItems": 0,
				"maxItems": len(payload.Issues),
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"issue_id",
						"rule_text",
						"target_file",
						"confidence",
					},
					"properties": map[string]any{
						"issue_id": semanticIdentifierSchema(
							issueIDs,
						),
						"rule_text":   insightLineSchema(500),
						"target_file": insightTargetSchema(),
						"confidence": map[string]any{
							"type":    "number",
							"minimum": 0,
							"maximum": 1,
						},
					},
				},
			},
		},
	}
	body, err := json.Marshal(schema)
	if err != nil {
		return nil, errors.New("encode semantic output schema")
	}
	return body, nil
}

func semanticIdentifierSchema(values []string) map[string]any {
	result := insightLineSchema(maxSemanticIdentifierBytes)
	if len(values) > 0 {
		result["enum"] = values
	}
	return result
}

func insightLineSchema(maximum int) map[string]any {
	return map[string]any{
		"type":      "string",
		"minLength": 1,
		"maxLength": maximum,
		"pattern":   `^[^\r\n]+$`,
	}
}

func insightTargetSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{
			"CLAUDE.md",
			"AGENTS.md",
			".claude/settings.json",
			".codex/rules/default.rules",
			antigravityProjectRuleFile,
		},
	}
}

func decodeClaudeSemanticResult(body []byte) (SemanticHarnessResult, error) {
	raw, err := decodeClaudeSemanticPayload(body)
	if err != nil {
		return SemanticHarnessResult{}, err
	}
	return decodeSemanticResult(raw.body, raw.model)
}

func decodeClaudeSemanticPayload(
	body []byte,
) (semanticRawHarnessResult, error) {
	var envelope struct {
		Model            string          `json:"model"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Result           json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return semanticRawHarnessResult{}, errors.New(
			"decode Claude semantic response",
		)
	}
	if len(envelope.StructuredOutput) > 0 &&
		string(envelope.StructuredOutput) != "null" {
		return semanticRawHarnessResult{
			body:  append([]byte(nil), envelope.StructuredOutput...),
			model: strings.TrimSpace(envelope.Model),
		}, nil
	}
	var text string
	if json.Unmarshal(envelope.Result, &text) == nil {
		return semanticRawHarnessResult{
			body:  []byte(text),
			model: strings.TrimSpace(envelope.Model),
		}, nil
	}
	if len(envelope.Result) == 0 {
		return semanticRawHarnessResult{}, errors.New(
			"decode Claude semantic response",
		)
	}
	return semanticRawHarnessResult{
		body:  append([]byte(nil), envelope.Result...),
		model: strings.TrimSpace(envelope.Model),
	}, nil
}

func decodeSemanticResult(
	body []byte,
	model string,
) (SemanticHarnessResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var result issueintel.InsightResult
	if err := decoder.Decode(&result); err != nil {
		return SemanticHarnessResult{}, errors.New(
			"decode semantic result",
		)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return SemanticHarnessResult{}, errors.New(
			"semantic result contains trailing JSON",
		)
	}
	return SemanticHarnessResult{
		Result: result,
		Model:  strings.TrimSpace(model),
	}, nil
}

func validateSemanticResult(
	input issueintel.SemanticInput,
	result issueintel.InsightResult,
) error {
	_, diagnostics := sanitizeSemanticResult(input, result)
	switch {
	case diagnostics.InvalidFixFields > 0:
		return errors.New("semantic result contains an invalid fix")
	case diagnostics.UnknownFixIssues > 0 ||
		diagnostics.AmbiguousFixIssues > 0:
		return errors.New("semantic result contains an invalid fix")
	case diagnostics.InvalidClusterFields > 0:
		return errors.New("semantic result contains an invalid cluster")
	case diagnostics.UnknownClusterCandidates > 0 ||
		diagnostics.AmbiguousClusterCandidates > 0:
		return errors.New(
			"semantic result contains an invalid cluster candidate",
		)
	default:
		return nil
	}
}

func sanitizeSemanticResult(
	input issueintel.SemanticInput,
	result issueintel.InsightResult,
) (issueintel.InsightResult, issueintel.InsightSanitization) {
	issueIDs := make(map[string]bool, len(input.Issues))
	for _, issue := range input.Issues {
		issueIDs[issue.IssueID] = true
	}
	candidateIDs := make(map[string]bool, len(input.CorrectionCandidates))
	for _, candidate := range input.CorrectionCandidates {
		candidateIDs[candidate.CandidateID] = true
	}

	fixCounts := make(map[string]int, len(result.Fixes))
	for _, fix := range result.Fixes {
		fixCounts[fix.IssueID]++
	}
	var sanitized issueintel.InsightResult
	var diagnostics issueintel.InsightSanitization
	for _, fix := range result.Fixes {
		switch {
		case !issueIDs[fix.IssueID]:
			diagnostics.DroppedFixes++
			diagnostics.UnknownFixIssues++
		case fixCounts[fix.IssueID] > 1:
			diagnostics.DroppedFixes++
			diagnostics.AmbiguousFixIssues++
		case !validInsightLine(fix.RuleText, 500) ||
			!validInsightTarget(fix.TargetFile) ||
			fix.Confidence < 0 ||
			fix.Confidence > 1:
			diagnostics.DroppedFixes++
			diagnostics.InvalidFixFields++
		default:
			sanitized.Fixes = append(sanitized.Fixes, fix)
		}
	}

	type clusterCandidate struct {
		value issueintel.InsightCluster
	}
	baseValid := make([]clusterCandidate, 0, len(result.Clusters))
	for _, cluster := range result.Clusters {
		if len(cluster.CandidateIDs) == 0 ||
			!validInsightLine(cluster.Topic, 120) ||
			!validInsightLine(cluster.RuleText, 500) ||
			!validInsightTarget(cluster.TargetFile) ||
			cluster.Confidence < 0 ||
			cluster.Confidence > 1 {
			diagnostics.DroppedClusters++
			diagnostics.InvalidClusterFields++
			continue
		}
		unknown := false
		duplicate := false
		withinCluster := make(map[string]bool, len(cluster.CandidateIDs))
		for _, candidateID := range cluster.CandidateIDs {
			if !candidateIDs[candidateID] {
				unknown = true
				break
			}
			if withinCluster[candidateID] {
				duplicate = true
				break
			}
			withinCluster[candidateID] = true
		}
		if unknown {
			diagnostics.DroppedClusters++
			diagnostics.UnknownClusterCandidates++
			continue
		}
		if duplicate {
			diagnostics.DroppedClusters++
			diagnostics.AmbiguousClusterCandidates++
			continue
		}
		cluster.CandidateIDs = append([]string(nil), cluster.CandidateIDs...)
		sort.Strings(cluster.CandidateIDs)
		baseValid = append(baseValid, clusterCandidate{value: cluster})
	}

	candidateCounts := make(map[string]int, len(candidateIDs))
	for _, cluster := range baseValid {
		for _, candidateID := range cluster.value.CandidateIDs {
			candidateCounts[candidateID]++
		}
	}
	for _, cluster := range baseValid {
		ambiguous := false
		for _, candidateID := range cluster.value.CandidateIDs {
			if candidateCounts[candidateID] > 1 {
				ambiguous = true
				break
			}
		}
		if ambiguous {
			diagnostics.DroppedClusters++
			diagnostics.AmbiguousClusterCandidates++
			continue
		}
		sanitized.Clusters = append(sanitized.Clusters, cluster.value)
	}
	sort.Slice(sanitized.Fixes, func(i, j int) bool {
		return sanitized.Fixes[i].IssueID < sanitized.Fixes[j].IssueID
	})
	sort.Slice(sanitized.Clusters, func(i, j int) bool {
		left := strings.Join(sanitized.Clusters[i].CandidateIDs, "\x00")
		right := strings.Join(sanitized.Clusters[j].CandidateIDs, "\x00")
		return left < right
	})
	return sanitized, diagnostics
}

func validInsightLine(value string, maximum int) bool {
	value = strings.TrimSpace(value)
	return value != "" &&
		len(value) <= maximum &&
		!strings.ContainsAny(value, "\r\n")
}

func validInsightTarget(value string) bool {
	switch strings.TrimSpace(value) {
	case "CLAUDE.md", "AGENTS.md", ".claude/settings.json",
		".codex/rules/default.rules", antigravityProjectRuleFile:
		return true
	default:
		return false
	}
}

func semanticDigest(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:16])
}

type boundedWriter struct {
	writer *bytes.Buffer
	limit  int
}

func (w *boundedWriter) Write(body []byte) (int, error) {
	if w.writer.Len()+len(body) > w.limit {
		return 0, errors.New("semantic harness output exceeds safety limit")
	}
	return w.writer.Write(body)
}
