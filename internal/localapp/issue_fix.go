package localapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
)

const maxFixTargetBytes = 1 << 20

type CostIssueFixStore interface {
	GetCostIssue(context.Context, string) (issueintel.Issue, error)
	GetProjectInsight(
		context.Context,
		string,
	) (issueintel.InsightRecord, error)
	SaveCostIssueFix(context.Context, issueintel.FixRecord) error
	GetCostIssueFix(context.Context, string) (issueintel.FixRecord, error)
	RecordCostIssueFixApplied(
		context.Context,
		string,
		string,
		string,
		string,
		time.Time,
	) (issueintel.FixRecord, error)
}

type CostIssueFixService struct {
	store CostIssueFixStore
	now   func() time.Time
}

func NewCostIssueFixService(store CostIssueFixStore) (*CostIssueFixService, error) {
	if store == nil {
		return nil, errors.New("cost issue fix service requires a store")
	}
	return &CostIssueFixService{store: store, now: time.Now}, nil
}

func (s *CostIssueFixService) ProposeFix(
	ctx context.Context,
	issueID, kind, targetFile string,
) (issueintel.FixRecord, error) {
	issue, err := s.store.GetCostIssue(ctx, strings.TrimSpace(issueID))
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	targetFile, err = normalizeFixTarget(targetFile)
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	if kind = strings.TrimSpace(kind); kind == "" || len(kind) > 128 {
		return issueintel.FixRecord{}, errors.New("invalid fix kind")
	}
	if kind != issue.SuggestedFix.Kind {
		return issueintel.FixRecord{}, errors.New(
			"fix kind does not match the detected issue",
		)
	}
	rule := issue.SuggestedFix.Rationale
	if insight, insightErr := s.store.GetProjectInsight(
		ctx,
		issue.Project.Identity,
	); insightErr == nil {
		for _, fix := range insight.Result.Fixes {
			if fix.IssueID == issue.IssueID {
				rule = fix.RuleText
				if targetFile == "" {
					targetFile = fix.TargetFile
				}
				break
			}
		}
	}
	if targetFile == "" {
		targetFile, err = normalizeFixTarget(issue.SuggestedFix.TargetFile)
		if err != nil {
			return issueintel.FixRecord{}, err
		}
	}
	_, oldBody, err := loadFixTarget(issue.Project.Path, targetFile)
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	newBody, err := applyRuleToConfig(targetFile, oldBody, rule, issue)
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	diff := unifiedConfigDiff(targetFile, oldBody, newBody)
	fixID := "fix_" + fixDigest(
		issue.IssueID,
		kind,
		targetFile,
		rule,
		string(oldBody),
	)
	if existing, getErr := s.store.GetCostIssueFix(ctx, fixID); getErr == nil {
		return existing, nil
	}
	record := issueintel.FixRecord{
		FixID:       fixID,
		IssueID:     issue.IssueID,
		Project:     issue.Project,
		Kind:        kind,
		TargetFile:  targetFile,
		RuleText:    strings.TrimSpace(rule),
		UnifiedDiff: diff,
		State:       "proposed",
		ProposedAt:  s.now().UTC(),
	}
	if err := s.store.SaveCostIssueFix(ctx, record); err != nil {
		return issueintel.FixRecord{}, err
	}
	return record, nil
}

func (s *CostIssueFixService) RecordApplied(
	ctx context.Context,
	fixID, appliedPath, contentSHA256, gitCommit string,
) (issueintel.FixRecord, error) {
	record, err := s.store.GetCostIssueFix(ctx, fixID)
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	expected, currentBody, err := loadFixTarget(
		record.Project.Path,
		record.TargetFile,
	)
	if err != nil {
		return issueintel.FixRecord{}, err
	}
	cleanApplied := filepath.Clean(strings.TrimSpace(appliedPath))
	if cleanApplied != expected &&
		filepath.ToSlash(cleanApplied) != filepath.ToSlash(record.TargetFile) {
		return issueintel.FixRecord{}, errors.New(
			"applied path does not match proposed target",
		)
	}
	contentSHA256 = strings.ToLower(strings.TrimSpace(contentSHA256))
	if len(contentSHA256) != 64 {
		return issueintel.FixRecord{}, errors.New("invalid content sha256")
	}
	if _, err := hex.DecodeString(contentSHA256); err != nil {
		return issueintel.FixRecord{}, errors.New("invalid content sha256")
	}
	info, err := os.Lstat(expected)
	if err != nil {
		return issueintel.FixRecord{}, errors.New(
			"applied fix target does not exist",
		)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return issueintel.FixRecord{}, errors.New(
			"applied fix target is not a regular file",
		)
	}
	actualHash := sha256.Sum256(currentBody)
	if hex.EncodeToString(actualHash[:]) != contentSHA256 {
		return issueintel.FixRecord{}, errors.New(
			"content sha256 does not match the applied file",
		)
	}
	return s.store.RecordCostIssueFixApplied(
		ctx,
		fixID,
		cleanApplied,
		contentSHA256,
		strings.TrimSpace(gitCommit),
		s.now().UTC(),
	)
}

func (s *CostIssueFixService) Status(
	ctx context.Context,
	fixID string,
) (issueintel.FixStatus, error) {
	record, err := s.store.GetCostIssueFix(ctx, fixID)
	if err != nil {
		return issueintel.FixStatus{}, err
	}
	return issueintel.FixStatus{
		Fix:               record,
		Applied:           record.State == "applied",
		VerificationState: "deferred",
	}, nil
}

func normalizeFixTarget(value string) (string, error) {
	value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	if value == "." || value == "" || filepath.IsAbs(value) ||
		strings.HasPrefix(value, "../") || strings.Contains(value, "/../") {
		return "", errors.New("invalid fix target file")
	}
	switch value {
	case "CLAUDE.md", "AGENTS.md", ".claude/settings.json",
		".codex/config.toml", ".codex/rules/default.rules", ".cursorrules",
		cursorProjectRuleFile, antigravityProjectRuleFile:
		return value, nil
	}
	if strings.HasPrefix(value, ".claude/hooks/") &&
		value != ".claude/hooks/" {
		return value, nil
	}
	return "", errors.New("fix target is outside harness configuration")
}

func loadFixTarget(
	projectPath, targetFile string,
) (string, []byte, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if !filepath.IsAbs(projectPath) {
		return "", nil, errors.New("project path is unavailable")
	}
	targetPath := filepath.Join(projectPath, filepath.FromSlash(targetFile))
	relative, err := filepath.Rel(projectPath, targetPath)
	if err != nil || strings.HasPrefix(relative, "..") {
		return "", nil, errors.New("fix target escapes project")
	}
	info, err := os.Lstat(targetPath)
	if errors.Is(err, os.ErrNotExist) {
		return targetPath, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() > maxFixTargetBytes {
		return "", nil, errors.New("fix target is not a bounded regular file")
	}
	body, err := os.ReadFile(targetPath)
	if err != nil {
		return "", nil, err
	}
	return targetPath, body, nil
}

func applyRuleToConfig(
	target string,
	oldBody []byte,
	rule string,
	issue issueintel.Issue,
) ([]byte, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" || strings.ContainsAny(rule, "\r\n") {
		return nil, errors.New("fix rule must be one line")
	}
	switch target {
	case "CLAUDE.md", "AGENTS.md", ".cursorrules":
		if issue.SuggestedFix.Kind == "permission_allowlist" {
			return nil, errors.New(
				"permission fixes require a harness permission file",
			)
		}
		return appendRuleLine(oldBody, "- "+rule), nil
	case cursorProjectRuleFile:
		// Cursor project rules are Markdown with required front matter;
		// Cursor has no permission allowlist file.
		if issue.SuggestedFix.Kind == "permission_allowlist" {
			return nil, errors.New(
				"permission fixes require a harness permission file",
			)
		}
		return appendRuleLine(
			cursorRuleFileBody(oldBody),
			"- "+rule,
		), nil
	case antigravityProjectRuleFile:
		// Antigravity project rules are Markdown with a trigger front
		// matter; Antigravity keeps permissions in IDE settings and has no
		// project-level permission allowlist file.
		if issue.SuggestedFix.Kind == "permission_allowlist" {
			return nil, errors.New(
				"permission fixes require a harness permission file",
			)
		}
		return appendRuleLine(
			antigravityRuleFileBody(oldBody),
			"- "+rule,
		), nil
	case ".codex/config.toml":
		return nil, errors.New("unsupported Codex config fix kind")
	case ".codex/rules/default.rules":
		if issue.SuggestedFix.Kind != "permission_allowlist" {
			return nil, errors.New(
				"Codex rules are only supported for permission fixes",
			)
		}
		tool, pattern, _ := strings.Cut(issue.Fingerprint, "\x00")
		tool = strings.ToLower(strings.TrimSpace(tool))
		if !strings.Contains(tool, "bash") &&
			!strings.Contains(tool, "exec") &&
			!strings.Contains(tool, "command") {
			return nil, errors.New(
				"Codex permission rules require a command tool",
			)
		}
		fields := strings.Fields(pattern)
		quoted := make([]string, 0, len(fields))
		for _, field := range fields {
			quoted = append(quoted, strconv.Quote(field))
		}
		if len(quoted) == 0 {
			return nil, errors.New("permission rule pattern is unavailable")
		}
		line := "prefix_rule(pattern=[" + strings.Join(quoted, ", ") +
			`], decision="allow")`
		return appendRuleLine(oldBody, line), nil
	case ".claude/settings.json":
		if issue.SuggestedFix.Kind != "permission_allowlist" {
			return nil, errors.New(
				"Claude settings are only supported for permission fixes",
			)
		}
		var root map[string]any
		if len(strings.TrimSpace(string(oldBody))) == 0 {
			root = make(map[string]any)
		} else if json.Unmarshal(oldBody, &root) != nil {
			return nil, errors.New("Claude settings JSON is invalid")
		}
		tool, pattern, _ := strings.Cut(issue.Fingerprint, "\x00")
		entry, err := claudePermissionEntry(tool, pattern)
		if err != nil {
			return nil, err
		}
		permissions, _ := root["permissions"].(map[string]any)
		if permissions == nil {
			permissions = make(map[string]any)
			root["permissions"] = permissions
		}
		values, _ := permissions["allow"].([]any)
		for _, value := range values {
			if value == entry {
				body, _ := json.MarshalIndent(root, "", "  ")
				return append(body, '\n'), nil
			}
		}
		permissions["allow"] = append(values, entry)
		body, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(body, '\n'), nil
	default:
		if strings.HasPrefix(target, ".claude/hooks/") {
			return nil, errors.New("unsupported Claude hook fix kind")
		}
	}
	return nil, errors.New("unsupported fix target")
}

func claudePermissionEntry(tool, pattern string) (string, error) {
	tool = strings.ToLower(strings.TrimSpace(tool))
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", errors.New("permission rule pattern is unavailable")
	}
	var name string
	switch {
	case strings.Contains(tool, "bash"),
		strings.Contains(tool, "exec"),
		strings.Contains(tool, "command"):
		name = "Bash"
	case tool == "read":
		name = "Read"
	case tool == "edit":
		name = "Edit"
	case tool == "write":
		name = "Write"
	case tool == "webfetch":
		name = "WebFetch"
	default:
		return "", errors.New("unsupported Claude permission tool")
	}
	return name + "(" + pattern + ")", nil
}

// cursorProjectRuleFile is the single Cursor project rule file Belay may
// write. Cursor loads every .cursor/rules/*.mdc file; Belay owns only its
// own, never another rule the user or another tool wrote.
const cursorProjectRuleFile = ".cursor/rules/belay.mdc"

const cursorRuleFrontMatter = "---\n" +
	"description: Belay Local project rules\n" +
	"alwaysApply: true\n---\n"

// cursorRuleFileBody seeds a new Cursor rule file with the front matter
// Cursor requires and leaves an existing file untouched.
func cursorRuleFileBody(body []byte) []byte {
	if len(strings.TrimSpace(string(body))) != 0 {
		return body
	}
	return []byte(cursorRuleFrontMatter)
}

// antigravityProjectRuleFile is the single Antigravity project rule file
// Belay may write. Antigravity loads every .agents/rules/*.md file at the
// workspace root (the legacy .agent/rules/ directory is read only for
// backward compatibility and Belay never writes it); Belay owns only its own
// file, never another rule the user or another tool wrote. Global rules in
// ~/.gemini/GEMINI.md are never a Belay target.
const antigravityProjectRuleFile = ".agents/rules/belay.md"

// antigravityRuleFrontMatter is the always-on trigger Antigravity expects at
// the top of a rule file.
const antigravityRuleFrontMatter = "---\ntrigger: always_on\n---\n"

// antigravityRuleFileBody seeds a new Antigravity rule file with the front
// matter Antigravity requires and leaves an existing file untouched.
func antigravityRuleFileBody(body []byte) []byte {
	if len(strings.TrimSpace(string(body))) != 0 {
		return body
	}
	return []byte(antigravityRuleFrontMatter)
}

func appendRuleLine(body []byte, line string) []byte {
	result := append([]byte(nil), body...)
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	if len(result) > 0 {
		result = append(result, '\n')
	}
	return append(result, []byte(line+"\n")...)
}

func unifiedConfigDiff(target string, oldBody, newBody []byte) string {
	oldLines := strings.Split(strings.TrimSuffix(string(oldBody), "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(string(newBody), "\n"), "\n")
	if len(oldBody) == 0 {
		oldLines = nil
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "--- a/%s\n+++ b/%s\n", target, target)
	fmt.Fprintf(
		&builder,
		"@@ -1,%d +1,%d @@\n",
		len(oldLines),
		len(newLines),
	)
	for _, line := range oldLines {
		builder.WriteString("-" + line + "\n")
	}
	for _, line := range newLines {
		builder.WriteString("+" + line + "\n")
	}
	return builder.String()
}

func fixDigest(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:16])
}
