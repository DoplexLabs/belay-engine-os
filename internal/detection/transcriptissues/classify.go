package transcriptissues

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/DoplexLabs/belay-engine/internal/issueintel"
	"github.com/DoplexLabs/belay-engine/internal/transcript"
)

const (
	commandClassTest           = "test"
	commandClassBuild          = "build"
	commandClassTypecheck      = "typecheck"
	commandClassLint           = "lint"
	commandClassFormat         = "format_check"
	commandClassOther          = "other"
	correctionMarkerWords      = 8
	maxRepairCommandBytes      = 16 * 1024
	maxRepairCommandTokens     = 64
	maxRepairCommandTokenBytes = 1024
	maxErrorLineRunes          = 2048
	maxErrorSignatureRunes     = 512
)

var (
	numberPattern           = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	hashPattern             = regexp.MustCompile(`\b[0-9a-f]{7,}\b`)
	portPattern             = regexp.MustCompile(`(?i)(?::|--port[=\s]+)\d{2,5}\b`)
	tempPathPattern         = regexp.MustCompile(`(?i)(?:/private)?/tmp/[^\s"'` + "`" + `]+|/var/folders/[^\s"'` + "`" + `]+`)
	absolutePathPattern     = regexp.MustCompile(`(?:[A-Za-z]:\\|/)[^\s"'` + "`" + `:]+`)
	spacePattern            = regexp.MustCompile(`\s+`)
	errorMarkerPattern      = regexp.MustCompile(`(?i)\b(error|failed|failure|fatal|panic|exception|denied|not found|timed out)\b`)
	genericExitPattern      = regexp.MustCompile(`(?i)^(?:process |command )?(?:exited|failed)(?: with)?(?: exit)? code \d+\b|^exit code \d+\b`)
	completionPattern       = regexp.MustCompile(`(?i)\b(done|complete|completed|implemented|finished|tests? pass(?:ed)?)\b`)
	correctionPattern       = regexp.MustCompile(`(?i)\b(no|don't|do not|stop|wrong|not that|i said|again|undo|revert)\b|(?i)\buse\b.+\bnot\b`)
	strongCorrectionPattern = regexp.MustCompile(
		`(?i)\b(don't|do not|stop|wrong|not that|i said|again|undo|revert)\b|` +
			`(?i)\buse\b.+\bnot\b`,
	)
	leadingNoCorrectionPattern = regexp.MustCompile(`(?i)^\s*no(?:\b|[!,:;.-])`)
	approvalPattern            = regexp.MustCompile(`(?i)^(yes|y|allow|allowed|approve|approved|continue|go ahead|ok|okay)$`)
	denialPattern              = regexp.MustCompile(`(?i)^(no|n|deny|denied|reject|rejected|cancel|stop)$`)
	patchFilePattern           = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$`)
)

func commandInfo(
	turn transcript.Turn,
	config issueintel.ProjectConfig,
) (class, normalized, raw string, ok bool) {
	raw = strings.TrimSpace(turn.Payload.RawCommand)
	if raw == "" && turn.Role == transcript.RoleToolCall {
		raw = commandFromToolInput(turn.Payload.ToolInput)
	}
	if raw == "" {
		return "", "", "", false
	}
	class = classifyCommand(raw, config)
	normalized = normalizeCommand(raw)
	if normalized == "" {
		return "", "", "", false
	}
	return class, normalized, raw, true
}

func commandFromToolInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return findStringField(value, map[string]bool{
		"command": true,
		"cmd":     true,
		"script":  true,
	})
}

func classifyCommand(raw string, config issueintel.ProjectConfig) string {
	for _, candidate := range verificationCommandCandidates(raw) {
		class := classifyCommandCandidate(candidate, config)
		if class != commandClassOther {
			return class
		}
	}
	return commandClassOther
}

func classifyCommandCandidate(
	raw string,
	config issueintel.ProjectConfig,
) string {
	normalized := strings.ToLower(spacePattern.ReplaceAllString(strings.TrimSpace(raw), " "))
	for _, configured := range config.VerificationCommands {
		configured = strings.ToLower(spacePattern.ReplaceAllString(strings.TrimSpace(configured), " "))
		if configured != "" && strings.HasPrefix(normalized, configured) {
			class := verificationClassFromWords(configured)
			if class == commandClassOther {
				return commandClassBuild
			}
			return class
		}
	}
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return commandClassOther
	}
	executable := filepath.Base(fields[0])
	joined := " " + normalized + " "
	switch executable {
	case "go":
		if len(fields) > 1 && fields[1] == "test" {
			return commandClassTest
		}
		if len(fields) > 1 && fields[1] == "build" {
			return commandClassBuild
		}
	case "cargo":
		if len(fields) > 1 && fields[1] == "test" {
			return commandClassTest
		}
		if len(fields) > 1 && (fields[1] == "build" || fields[1] == "check") {
			return commandClassBuild
		}
	case "python", "python3":
		if len(fields) > 2 && fields[1] == "-m" &&
			(fields[2] == "pytest" || fields[2] == "unittest") {
			return commandClassTest
		}
	case "pytest", "jest", "vitest", "mocha":
		return commandClassTest
	case "tsc", "pyright", "mypy":
		return commandClassTypecheck
	case "eslint", "ruff", "golangci-lint", "shellcheck", "hadolint":
		return commandClassLint
	case "gofmt", "prettier", "black":
		if strings.Contains(joined, " --check ") ||
			strings.Contains(joined, " -d ") {
			return commandClassFormat
		}
	case "make":
		if len(fields) > 1 {
			return verificationClassFromWords(fields[1])
		}
	case "npm", "pnpm", "yarn", "bun":
		script := packageScript(fields)
		if script != "" {
			return verificationClassFromWords(script)
		}
	}
	return verificationClassFromExecutable(executable)
}

func verificationCommandCandidates(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	result := []string{raw}
	current := raw
	for range 4 {
		left, right, ok := splitTopLevelAndAnd(current)
		if !ok || !workingDirectoryWrapper(left) {
			break
		}
		result = append(result, right)
		current = right
	}
	return result
}

func splitTopLevelAndAnd(raw string) (string, string, bool) {
	var quote byte
	escaped := false
	for index := 0; index+1 < len(raw); index++ {
		character := raw[index]
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			if character == '\\' && quote == '"' {
				escaped = true
				continue
			}
			if character == quote {
				quote = 0
			}
			continue
		}
		switch character {
		case '\\':
			escaped = true
		case '\'', '"':
			quote = character
		case '&':
			if raw[index+1] != '&' {
				continue
			}
			left := strings.TrimSpace(raw[:index])
			right := strings.TrimSpace(raw[index+2:])
			return left, right, left != "" && right != ""
		}
	}
	return "", "", false
}

func workingDirectoryWrapper(raw string) bool {
	tokens, ok := firstLogicalCommandTokens(raw)
	if !ok || len(tokens) != 2 {
		return false
	}
	return strings.EqualFold(filepath.Base(tokens[0]), "cd")
}

func packageScript(fields []string) string {
	if len(fields) < 2 {
		return ""
	}
	index := 1
	if fields[index] == "run" {
		index++
	}
	if index >= len(fields) {
		return ""
	}
	return fields[index]
}

func verificationClassFromExecutable(value string) string {
	words := strings.FieldsFunc(
		strings.ToLower(filepath.Base(value)),
		func(character rune) bool {
			return !unicode.IsLetter(character) && !unicode.IsDigit(character)
		},
	)
	hasWord := func(want string) bool {
		for _, word := range words {
			if word == want {
				return true
			}
		}
		return false
	}
	switch {
	case hasWord("typecheck") ||
		(hasWord("type") && hasWord("check")):
		return commandClassTypecheck
	case hasWord("lint"):
		return commandClassLint
	case hasWord("format") && hasWord("check"):
		return commandClassFormat
	case hasWord("test") || hasWord("tests"):
		return commandClassTest
	case hasWord("build") || hasWord("check"):
		return commandClassBuild
	default:
		return commandClassOther
	}
}

func verificationClassFromWords(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "typecheck"),
		strings.Contains(value, "type-check"),
		strings.Contains(value, "check-types"):
		return commandClassTypecheck
	case strings.Contains(value, "lint"):
		return commandClassLint
	case strings.Contains(value, "format") && strings.Contains(value, "check"):
		return commandClassFormat
	case strings.Contains(value, "test"):
		return commandClassTest
	case strings.Contains(value, "build"),
		strings.Contains(value, "compile"),
		strings.TrimSpace(value) == "check":
		return commandClassBuild
	default:
		return commandClassOther
	}
}

func isVerificationTurn(turn transcript.Turn, config issueintel.ProjectConfig) bool {
	class, _, _, ok := commandInfo(turn, config)
	if !ok {
		return false
	}
	switch class {
	case commandClassTest,
		commandClassBuild,
		commandClassTypecheck,
		commandClassLint,
		commandClassFormat:
		return true
	default:
		return false
	}
}

// ClassifyVerificationCommand returns the stable verification class for a
// command and reports whether the command is recognized as verification.
func ClassifyVerificationCommand(
	raw string,
	config issueintel.ProjectConfig,
) (string, bool) {
	class := classifyCommand(raw, config)
	switch class {
	case commandClassTest,
		commandClassBuild,
		commandClassTypecheck,
		commandClassLint,
		commandClassFormat:
		return class, true
	default:
		return "", false
	}
}

// RetainedVerificationCommand returns a retained tool call's recognized
// verification class and command. It falls back to structured tool input when
// a native transcript does not provide a separate raw_command field.
func RetainedVerificationCommand(
	turn transcript.Turn,
	config issueintel.ProjectConfig,
) (class, raw string, ok bool) {
	raw = strings.TrimSpace(turn.Payload.RawCommand)
	if raw == "" && turn.Role == transcript.RoleToolCall {
		raw = commandFromToolInput(turn.Payload.ToolInput)
	}
	if raw == "" {
		return "", "", false
	}
	for _, candidate := range verificationCommandCandidates(raw) {
		class = classifyCommandCandidate(candidate, config)
		switch class {
		case commandClassTest,
			commandClassBuild,
			commandClassTypecheck,
			commandClassLint,
			commandClassFormat:
			return class, candidate, true
		}
	}
	return "", "", false
}

// ExtractEditedFiles returns normalized file paths explicitly present in a
// retained file-edit tool call. It does not infer paths from surrounding turns.
func ExtractEditedFiles(turn transcript.Turn) []string {
	return editedFiles(turn)
}

// NormalizedCommandSignature returns the detector's stable command signature
// for a retained command turn.
func NormalizedCommandSignature(turn transcript.Turn) (string, bool) {
	_, normalized, _, ok := commandInfo(turn, issueintel.ProjectConfig{})
	return normalized, ok
}

// RetainedCommandInfo returns the shared deterministic command
// classification, normalized signature, and retained raw command for one turn.
func RetainedCommandInfo(
	turn transcript.Turn,
	config issueintel.ProjectConfig,
) (class, normalized, raw string, ok bool) {
	return commandInfo(turn, config)
}

// ToolResultFailed reports whether the retained tool result is classified as
// a failure by the transcript issue detector's deterministic rules.
func ToolResultFailed(turn transcript.Turn) bool {
	return turnFailed(turn)
}

// ExplicitToolResultFailed returns a harness-reported success or failure
// status without interpreting result text. Numeric exit codes take precedence;
// Claude's explicit is_error field is used when no exit code is available.
func ExplicitToolResultFailed(turn transcript.Turn) (failed, known bool) {
	if turn.Role != transcript.RoleToolResult {
		return false, false
	}
	if turn.Payload.ExitCode != nil {
		return *turn.Payload.ExitCode != 0, true
	}
	if turn.Payload.ToolIsError != nil {
		return *turn.Payload.ToolIsError, true
	}
	return false, false
}

// NormalizedErrorSignature returns the detector's stable normalized error
// signature for a retained tool result.
func NormalizedErrorSignature(turn transcript.Turn) string {
	return normalizedErrorSignature(turn)
}

// NormalizedFirstFailureLine returns the retry-loop fingerprint form of the
// first retained non-empty tool-result line. It does not decide whether the
// result failed; callers must use ToolResultFailed first.
func NormalizedFirstFailureLine(turn transcript.Turn) string {
	line := strings.ToLower(firstMeaningfulLine(turn.Payload.ToolResult))
	line = tempPathPattern.ReplaceAllString(line, "<tmp>")
	line = absolutePathPattern.ReplaceAllString(line, "<path>")
	line = hashPattern.ReplaceAllString(line, "<hash>")
	line = numberPattern.ReplaceAllString(line, "<n>")
	line = spacePattern.ReplaceAllString(line, " ")
	return truncateRunes(strings.TrimSpace(line), maxErrorSignatureRunes)
}

// CommandRepairFamily returns the first logical command's stable executable
// family for conservative repair comparison. It only tokenizes shell syntax;
// it never expands variables, substitutions, or executes any input.
func CommandRepairFamily(turn transcript.Turn) (string, bool) {
	_, _, raw, ok := commandInfo(turn, issueintel.ProjectConfig{})
	if !ok {
		return "", false
	}
	tokens, ok := firstLogicalCommandTokens(raw)
	if !ok {
		return "", false
	}
	index := 0
	for index < len(tokens) && shellAssignment(tokens[index]) {
		index++
	}
	if index < len(tokens) &&
		strings.EqualFold(filepath.Base(tokens[index]), "env") {
		index++
		for index < len(tokens) {
			token := tokens[index]
			switch {
			case shellAssignment(token):
				index++
			case token == "--":
				index++
				goto executable
			case token == "-i" || token == "--ignore-environment":
				index++
			case token == "-u" || token == "--unset" ||
				token == "-C" || token == "--chdir":
				if index+1 >= len(tokens) {
					return "", false
				}
				index += 2
			case strings.HasPrefix(token, "--unset=") ||
				strings.HasPrefix(token, "--chdir="):
				index++
			case strings.HasPrefix(token, "-"):
				return "", false
			default:
				goto executable
			}
		}
	}

executable:
	for index < len(tokens) && shellAssignment(tokens[index]) {
		index++
	}
	if index >= len(tokens) {
		return "", false
	}
	executable := strings.ToLower(filepath.Base(tokens[index]))
	if !safeCommandFamilyPart(executable) {
		return "", false
	}
	family := executable
	if subcommand := meaningfulRepairSubcommand(executable, tokens[index+1:]); subcommand != "" {
		family += " " + subcommand
	}
	if len(family) > 128 {
		return "", false
	}
	return family, true
}

func firstLogicalCommandTokens(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxRepairCommandBytes {
		return nil, false
	}
	tokens := make([]string, 0, 8)
	var current strings.Builder
	var quote byte
	escaped := false
	flush := func() bool {
		if current.Len() == 0 {
			return true
		}
		if current.Len() > maxRepairCommandTokenBytes ||
			len(tokens) >= maxRepairCommandTokens {
			return false
		}
		tokens = append(tokens, current.String())
		current.Reset()
		return true
	}
	for index := 0; index < len(raw); index++ {
		character := raw[index]
		if escaped {
			current.WriteByte(character)
			escaped = false
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
				continue
			}
			if character == '\\' && quote == '"' {
				escaped = true
				continue
			}
			current.WriteByte(character)
			continue
		}
		switch character {
		case '\\':
			escaped = true
		case '\'', '"':
			quote = character
		case '\n', '\r', ';', '&', '|':
			if !flush() {
				return nil, false
			}
			return tokens, len(tokens) > 0
		case ' ', '\t':
			if !flush() {
				return nil, false
			}
		default:
			current.WriteByte(character)
			if current.Len() > maxRepairCommandTokenBytes {
				return nil, false
			}
		}
	}
	if escaped || quote != 0 || !flush() {
		return nil, false
	}
	return tokens, len(tokens) > 0
}

func shellAssignment(value string) bool {
	separator := strings.IndexByte(value, '=')
	if separator <= 0 {
		return false
	}
	for index, character := range value[:separator] {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			character == '_' ||
			(index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func safeCommandFamilyPart(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) ||
			unicode.IsDigit(character) ||
			strings.ContainsRune("._+-", character) {
			continue
		}
		return false
	}
	return true
}

func meaningfulRepairSubcommand(executable string, arguments []string) string {
	switch executable {
	case "go":
		return allowlistedLeadingArgument(arguments, map[string]bool{
			"build": true, "clean": true, "env": true, "fmt": true,
			"generate": true, "get": true, "install": true, "list": true,
			"mod": true, "run": true, "test": true, "tool": true,
			"version": true, "vet": true, "work": true,
		})
	case "cargo":
		return allowlistedLeadingArgument(arguments, map[string]bool{
			"build": true, "check": true, "clean": true, "clippy": true,
			"fmt": true, "run": true, "test": true,
		})
	case "git":
		return allowlistedLeadingArgument(arguments, map[string]bool{
			"add": true, "apply": true, "branch": true, "checkout": true,
			"clean": true, "commit": true, "diff": true, "fetch": true,
			"log": true, "merge": true, "pull": true, "push": true,
			"rebase": true, "restore": true, "show": true, "status": true,
			"switch": true,
		})
	case "python", "python3":
		if len(arguments) >= 2 && arguments[0] == "-m" &&
			safeCommandFamilyPart(arguments[1]) {
			return "-m " + strings.ToLower(arguments[1])
		}
	case "make":
		for _, argument := range arguments {
			if strings.HasPrefix(argument, "-") || shellAssignment(argument) {
				continue
			}
			if safeCommandFamilyPart(argument) {
				return strings.ToLower(argument)
			}
			return ""
		}
	case "npm", "pnpm", "yarn", "bun":
		index := 0
		if index < len(arguments) && arguments[index] == "run" {
			index++
		}
		if index < len(arguments) && safeCommandFamilyPart(arguments[index]) {
			return strings.ToLower(arguments[index])
		}
	}
	return ""
}

func allowlistedLeadingArgument(
	arguments []string,
	allowed map[string]bool,
) string {
	if len(arguments) == 0 {
		return ""
	}
	argument := strings.ToLower(arguments[0])
	if allowed[argument] {
		return argument
	}
	return ""
}

// IsCompletionClaim reports whether an assistant turn contains the detector's
// deterministic completion marker.
func IsCompletionClaim(turn transcript.Turn) bool {
	return isCompletionClaim(turn)
}

// HighConfidenceCorrectionMarker returns the explicit correction marker used
// by deterministic correction detection. Short-turn length alone is not a
// high-confidence correction signal.
func HighConfidenceCorrectionMarker(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" ||
		strings.HasPrefix(normalized, "no problem") ||
		strings.HasPrefix(normalized, "no worries") ||
		strings.HasPrefix(normalized, "no thank") ||
		IsMachineGeneratedEnvelope(value) {
		return ""
	}
	if match := leadingNoCorrectionPattern.FindString(value); match != "" {
		return strings.ToLower(strings.TrimSpace(match))
	}
	words := strings.Fields(value)
	if len(words) > correctionMarkerWords {
		words = words[:correctionMarkerWords]
	}
	match := strongCorrectionPattern.FindString(strings.Join(words, " "))
	return strings.ToLower(strings.TrimSpace(match))
}

// IsMachineGeneratedEnvelope reports whether text is a retained task,
// system, or subagent notification rather than a real user instruction.
func IsMachineGeneratedEnvelope(value string) bool {
	trimmed := strings.TrimSpace(value)
	normalized := strings.ToLower(trimmed)
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
	if modeRawQueryEnvelope(trimmed) {
		return true
	}
	if !strings.HasPrefix(normalized, "{") {
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &object) != nil {
		return false
	}
	var envelopeType string
	if raw, ok := object["type"]; ok {
		_ = json.Unmarshal(raw, &envelopeType)
	}
	switch strings.ToLower(strings.TrimSpace(envelopeType)) {
	case "task_notification",
		"task-notification",
		"system_reminder",
		"system-reminder",
		"subagent_notification",
		"subagent-notification":
		return true
	}
	return nonEmptyJSONString(object["description"]) &&
		nonEmptyJSONString(object["prompt"])
}

func modeRawQueryEnvelope(value string) bool {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) < 2 {
		return false
	}
	first := strings.TrimSpace(lines[0])
	second := strings.TrimSpace(lines[1])
	if len(first) < len("MODE:") ||
		!strings.EqualFold(first[:len("MODE:")], "MODE:") ||
		strings.TrimSpace(first[len("MODE:"):]) == "" {
		return false
	}
	return len(second) >= len("RAW QUERY:") &&
		strings.EqualFold(second[:len("RAW QUERY:")], "RAW QUERY:")
}

func nonEmptyJSONString(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil &&
		strings.TrimSpace(value) != ""
}

func normalizeCommand(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = tempPathPattern.ReplaceAllString(value, "<tmp>")
	value = portPattern.ReplaceAllString(value, "<port>")
	value = hashPattern.ReplaceAllString(value, "<hash>")
	value = numberPattern.ReplaceAllString(value, "<n>")
	value = spacePattern.ReplaceAllString(value, " ")
	return strings.TrimSpace(value)
}

func commandSubject(normalized string) string {
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return "The same command"
	}
	return "`" + filepath.Base(fields[0]) + "`"
}

func turnFailed(turn transcript.Turn) bool {
	if turn.Payload.ToolIsError != nil && *turn.Payload.ToolIsError {
		return true
	}
	if turn.Payload.ExitCode != nil && *turn.Payload.ExitCode != 0 {
		return true
	}
	if failed, structured, _ := structuredResultFailure(
		turn.Payload.ToolResult,
	); structured {
		return failed
	}
	return errorMarkerPattern.MatchString(firstMeaningfulLine(turn.Payload.ToolResult))
}

func normalizedErrorSignature(turn transcript.Turn) string {
	line := firstSpecificErrorLine(turn.Payload.ToolResult)
	failed, _, structuredText := structuredResultFailure(
		turn.Payload.ToolResult,
	)
	if failed && structuredText != "" {
		line = structuredText
	}
	if line == "" || genericExitPattern.MatchString(line) ||
		!errorMarkerPattern.MatchString(line) {
		return ""
	}
	line = strings.ToLower(line)
	line = tempPathPattern.ReplaceAllString(line, "<tmp>")
	line = absolutePathPattern.ReplaceAllString(line, "<path>")
	line = hashPattern.ReplaceAllString(line, "<hash>")
	line = numberPattern.ReplaceAllString(line, "<n>")
	line = spacePattern.ReplaceAllString(line, " ")
	return truncateRunes(strings.TrimSpace(line), maxErrorSignatureRunes)
}

func firstSpecificErrorLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" ||
			genericExitPattern.MatchString(line) ||
			strings.EqualFold(line, "final output:") {
			continue
		}
		if errorMarkerPattern.MatchString(line) {
			return truncateRunes(line, maxErrorLineRunes)
		}
	}
	return ""
}

func firstMeaningfulLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return truncateRunes(line, maxErrorLineRunes)
		}
	}
	return ""
}

func structuredResultFailure(value string) (bool, bool, string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" ||
		(!strings.HasPrefix(trimmed, "{") &&
			!strings.HasPrefix(trimmed, "[")) {
		return false, false, ""
	}
	var decoded any
	if json.Unmarshal([]byte(trimmed), &decoded) != nil {
		return false, false, ""
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return false, true, ""
	}
	if raw, exists := object["error"]; exists && raw != nil {
		text := strings.TrimSpace(fmt.Sprint(raw))
		if text != "" && text != "<nil>" {
			return true, true, text
		}
	}
	if raw, ok := object["is_error"].(bool); ok {
		return raw, true, ""
	}
	if raw, ok := object["success"].(bool); ok {
		return !raw, true, ""
	}
	if raw, ok := object["exit_code"].(float64); ok {
		return raw != 0, true, ""
	}
	if raw, ok := object["status"].(string); ok {
		status := strings.ToLower(strings.TrimSpace(raw))
		switch status {
		case "error", "failed", "failure", "fatal":
			return true, true, raw
		case "ok", "success", "succeeded", "complete", "completed":
			return false, true, ""
		}
	}
	return false, true, ""
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
}

func isCompletionClaim(turn transcript.Turn) bool {
	return turn.Role == transcript.RoleAssistant &&
		completionPattern.MatchString(turn.Payload.Text)
}

func correctionMarker(value string) string {
	value = strings.TrimSpace(value)
	match := correctionPattern.FindString(value)
	return strings.ToLower(strings.TrimSpace(match))
}

func wordCount(value string) int {
	return len(strings.FieldsFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsPunct(character)
	}))
}

func isApprovalText(value string) bool {
	return approvalPattern.MatchString(strings.TrimSpace(value))
}

func isDenialText(value string) bool {
	return denialPattern.MatchString(strings.TrimSpace(value))
}

func editedFiles(turn transcript.Turn) []string {
	if turn.Role != transcript.RoleToolCall {
		return nil
	}
	name := strings.ToLower(strings.TrimSpace(turn.ToolName))
	if !strings.Contains(name, "edit") &&
		!strings.Contains(name, "write") &&
		!strings.Contains(name, "patch") {
		return nil
	}
	var files []string
	if len(turn.Payload.ToolInput) > 0 {
		var value any
		if json.Unmarshal(turn.Payload.ToolInput, &value) == nil {
			collectPathFields(value, &files)
			collectPatchPaths(value, &files)
		}
		for _, match := range patchFilePattern.FindAllStringSubmatch(
			string(turn.Payload.ToolInput),
			-1,
		) {
			if len(match) == 2 {
				files = append(files, match[1])
			}
		}
	}
	files = normalizeFiles(files)
	return files
}

func editContentHash(turn transcript.Turn) string {
	body := turn.Payload.ToolInput
	if len(body) == 0 {
		body = []byte(turn.Payload.RawCommand)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:16])
}

func findStringField(value any, names map[string]bool) string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			if names[strings.ToLower(key)] {
				if text, ok := child.(string); ok {
					return text
				}
			}
			if found := findStringField(child, names); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findStringField(child, names); found != "" {
				return found
			}
		}
	}
	return ""
}

func collectPathFields(value any, result *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch strings.ToLower(key) {
			case "path", "file", "file_path", "filename", "target_file":
				if text, ok := child.(string); ok {
					*result = append(*result, text)
				}
			}
			collectPathFields(child, result)
		}
	case []any:
		for _, child := range typed {
			collectPathFields(child, result)
		}
	}
}

func collectPatchPaths(value any, result *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			collectPatchPaths(child, result)
		}
	case []any:
		for _, child := range typed {
			collectPatchPaths(child, result)
		}
	case string:
		for _, match := range patchFilePattern.FindAllStringSubmatch(typed, -1) {
			if len(match) == 2 {
				*result = append(*result, match[1])
			}
		}
	}
}

func normalizeFiles(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
		if value == "" || value == "." || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func nearestPriorToolCall(turns []transcript.Turn, index int) (transcript.Turn, bool) {
	for prior := index - 1; prior >= 0 && index-prior <= 3; prior-- {
		if turns[prior].Role == transcript.RoleToolCall {
			return turns[prior], true
		}
		if turns[prior].Role == transcript.RoleUser {
			break
		}
	}
	return transcript.Turn{}, false
}

func candidateID(project string, turn transcript.Turn) string {
	sum := sha256.Sum256([]byte(
		project + "\x00" + turn.SessionKey + "\x00" + strconv.FormatInt(turn.TurnIndex, 10),
	))
	return "crc_" + hex.EncodeToString(sum[:16])
}
