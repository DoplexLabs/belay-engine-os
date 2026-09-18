package numbatmap

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|passwd|authorization|cookie)\s*[:=]\s*[^\s,;]+`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

var safeTag = regexp.MustCompile(`^[a-z0-9_.:-]{1,64}$`)

func sanitizeLabel(value string, limit int, secrets *int) string {
	value = strings.TrimSpace(value)
	value, removed := ScrubSecrets(value)
	*secrets += removed
	value = strings.ReplaceAll(value, "\x1b", "")
	return bounded(value, limit)
}

// ScrubSecrets applies Belay's existing local secret patterns without
// otherwise minimizing or truncating the supplied text.
func ScrubSecrets(value string) (string, int) {
	removed := 0
	for _, pattern := range secretPatterns {
		matches := pattern.FindAllStringIndex(value, -1)
		if len(matches) == 0 {
			continue
		}
		removed += len(matches)
		value = pattern.ReplaceAllString(value, "[redacted]")
	}
	return value, removed
}

func secretSignals(value string) int {
	count := 0
	for _, pattern := range secretPatterns {
		count += len(pattern.FindAllStringIndex(value, -1))
	}
	return count
}

func safePath(value, projectPath string) string {
	if value == "" {
		return ""
	}
	clean := filepath.Clean(value)
	if projectPath != "" {
		if relative, err := filepath.Rel(filepath.Clean(projectPath), clean); err == nil &&
			relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return bounded(filepath.ToSlash(relative), 256)
		}
	}
	if filepath.IsAbs(clean) {
		return bounded(filepath.Base(clean), 256)
	}
	return bounded(filepath.ToSlash(clean), 256)
}

func safeURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if port := parsed.Port(); port != "" {
		host += ":" + port
	}
	return bounded(parsed.Scheme+"://"+host, 256)
}

func safeTags(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !safeTag.MatchString(value) {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == 32 {
			break
		}
	}
	return result
}

func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for len(value) > limit {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}

func isEmptyDetails(details *model.Details) bool {
	return details.ToolCallID == "" &&
		details.Decision == "" &&
		details.ApprovalRequired == nil &&
		details.ApprovalDecision == "" &&
		details.MCPServer == "" &&
		details.MCPTool == "" &&
		details.Model == "" &&
		details.ModelProvider == "" &&
		details.CLIVersion == "" &&
		details.SubAgent == "" &&
		details.DiffSHA256 == "" &&
		details.DiffBytes == 0 &&
		len(details.Tags) == 0
}
