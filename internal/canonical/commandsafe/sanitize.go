// Package commandsafe owns Belay's fail-closed command display policy.
// It emits only allowlisted executable basenames and option names.
package commandsafe

import (
	"regexp"
	"strings"
)

const maxSummaryBytes = 512

type Display struct {
	Executable string
	Summary    string
}

var (
	executablePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	optionPattern     = regexp.MustCompile(`^-{1,2}[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

	executables = stringSet(
		"git", "go", "node", "npm", "npx", "pnpm", "yarn", "bun",
		"cargo", "rustc", "rustfmt", "python", "python3", "pip", "pip3",
		"pytest", "tsc", "eslint", "prettier", "ruff", "black", "gofmt",
		"golangci-lint", "shellcheck", "hadolint", "rg", "grep", "sed",
		"find", "pwd", "ls", "cat", "head", "tail", "wc", "jq", "gh",
		"make", "just", "task", "mvn", "mvnw", "gradle", "gradlew",
	)
	wrappers = stringSet(
		"env", "sh", "bash", "zsh", "fish", "sudo", "xargs",
	)
	options = stringSet(
		"-a", "--all", "-c", "--check", "--changed", "--color", "--config",
		"--count", "--cover", "--diff", "--dry-run", "--exclude", "-f",
		"--files", "--filter", "--fix", "--force", "-g", "--glob", "-h",
		"--help", "--hidden", "-i", "--ignore-case", "--include", "--json",
		"-l", "--line-number", "--locked", "-n", "--name-only", "--no-cache",
		"--no-pager", "--offline", "-o", "--output", "-p", "--package",
		"--porcelain", "--project", "-q", "--quiet", "-r", "-race", "--race",
		"--recursive", "--release", "--run", "--short", "--stat", "-t",
		"--target", "--timeout", "--type", "-v", "--verbose", "--version",
		"--watch", "--workspace",
	)
)

// Normalize returns a safe display projection for a raw command or a legacy
// command summary. The zero value is the neutral representation.
func Normalize(command string) Display {
	executable, fields, ok := safeFields(command)
	if !ok {
		return Display{}
	}
	result := []string{executable}
	for _, field := range fields {
		if !strings.HasPrefix(field, "-") {
			continue
		}
		name, _, _ := strings.Cut(field, "=")
		if optionPattern.MatchString(name) && options[name] {
			result = append(result, name)
		}
		if len(result) == 8 {
			break
		}
	}
	summary := strings.Join(result, " ")
	if len(summary) > maxSummaryBytes {
		summary = summary[:maxSummaryBytes]
	}
	return Display{Executable: executable, Summary: summary}
}

func safeFields(command string) (string, []string, bool) {
	if command == "" || containsUnsafeSyntax(command) {
		return "", nil, false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", nil, false
	}
	executable := fields[0]
	if strings.Contains(executable, "=") ||
		strings.ContainsAny(executable, `/\`) ||
		!executablePattern.MatchString(executable) ||
		wrappers[executable] ||
		!executables[executable] {
		return "", nil, false
	}
	return executable, fields[1:], true
}

func containsUnsafeSyntax(command string) bool {
	for _, character := range command {
		if character < 0x20 || character == 0x7f ||
			strings.ContainsRune(`;&|<>()$`+"`"+`'"{}\[]*?!~#`, character) {
			return true
		}
	}
	return false
}

func stringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}
