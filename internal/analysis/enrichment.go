// Package analysis connects validated acquisition records to deterministic
// detector inputs and rebuildable issue projections.
package analysis

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/DoplexLabs/belay-engine/internal/acquisition/numbat"
	"github.com/DoplexLabs/belay-engine/internal/canonical/model"
	"github.com/DoplexLabs/belay-engine/internal/detection"
	"github.com/DoplexLabs/belay-engine/internal/storage/local"
)

const EnrichmentVersion = "belay.enrichment.v1"

const (
	diagnosticProjectScopeDerivation = "project_scope_derivation_failed"
	diagnosticProjectScopeUpsert     = "project_scope_upsert_failed"
	diagnosticCommandDerivation      = "command_signature_derivation_failed"
	diagnosticEnrichmentConflict     = "event_enrichment_conflict"
	diagnosticEnrichmentUpsert       = "event_enrichment_upsert_failed"
)

type enrichmentError struct {
	code string
	err  error
}

func (e *enrichmentError) Error() string {
	return "event enrichment failed"
}

func (e *enrichmentError) Unwrap() error {
	return e.err
}

// EnrichmentDiagnosticCode converts an enrichment failure into a fixed,
// payload-free diagnostic code suitable for durable local reporting.
func EnrichmentDiagnosticCode(err error) string {
	var typed *enrichmentError
	if errors.As(err, &typed) && typed.code != "" {
		return typed.code
	}
	if errors.Is(err, local.ErrEventEnrichmentConflict) {
		return diagnosticEnrichmentConflict
	}
	return diagnosticEnrichmentUpsert
}

// EnrichEventRecord derives private sidecars from a validated raw record after
// its canonical event ID has been resolved. It also runs for duplicate replay,
// allowing previously stored events to gain enrichment without mutation.
func EnrichEventRecord(
	ctx context.Context,
	store *local.Store,
	eventID string,
	sessionID string,
	record numbat.EventRecord,
) error {
	if filepath.IsAbs(record.ProjectPath) {
		scope, err := store.DeriveProjectScope(record.ProjectPath)
		if err != nil {
			return &enrichmentError{
				code: diagnosticProjectScopeDerivation,
				err:  err,
			}
		}
		if _, err := store.UpsertSessionScope(ctx, sessionID, scope); err != nil {
			return &enrichmentError{
				code: diagnosticProjectScopeUpsert,
				err:  err,
			}
		}
	}

	enrichment := model.EventEnrichment{
		CommandClass:    classifyCommand(record.Command),
		PermissionClass: classifyPermission(record),
		Version:         EnrichmentVersion,
	}
	if strings.TrimSpace(record.Command) != "" {
		signature, err := store.DeriveCommandSignature(record.Command)
		if err != nil {
			return &enrichmentError{
				code: diagnosticCommandDerivation,
				err:  err,
			}
		}
		enrichment.CommandSignatureID = signature
	}
	_, err := store.UpsertEventEnrichment(ctx, eventID, enrichment)
	if err == nil {
		return nil
	}
	code := diagnosticEnrichmentUpsert
	if errors.Is(err, local.ErrEventEnrichmentConflict) {
		code = diagnosticEnrichmentConflict
	}
	return &enrichmentError{code: code, err: err}
}

func classifyCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return detection.CommandClassUnknown
	}
	executable := strings.ToLower(filepath.Base(fields[0]))
	arguments := lowerFields(fields[1:])
	hasArgument := func(values ...string) bool {
		for _, argument := range arguments {
			for _, value := range values {
				if argument == value {
					return true
				}
			}
		}
		return false
	}
	hasPair := func(first, second string) bool {
		for index := 0; index+1 < len(arguments); index++ {
			if arguments[index] == first && arguments[index+1] == second {
				return true
			}
		}
		return false
	}

	switch executable {
	case "pytest", "jest", "vitest", "mocha", "rspec":
		return detection.CommandClassTest
	case "tsc", "pyright", "mypy":
		return detection.CommandClassTypecheck
	case "eslint", "golangci-lint", "shellcheck", "hadolint":
		return detection.CommandClassLint
	case "go":
		switch goSubcommand(arguments) {
		case "test":
			return detection.CommandClassTest
		case "build", "install":
			return detection.CommandClassBuild
		case "vet":
			return detection.CommandClassLint
		}
	case "cargo":
		subcommand, remaining := cargoSubcommand(arguments)
		switch subcommand {
		case "test":
			return detection.CommandClassTest
		case "nextest":
			if firstPositional(remaining) == "run" {
				return detection.CommandClassTest
			}
		case "build", "check":
			return detection.CommandClassBuild
		case "clippy":
			return detection.CommandClassLint
		case "fmt":
			if hasArgument("--check") {
				return detection.CommandClassFormatCheck
			}
		}
	case "gofmt":
		if hasArgument("-d", "-l") {
			return detection.CommandClassFormatCheck
		}
	case "prettier", "black":
		if hasArgument("--check") {
			return detection.CommandClassFormatCheck
		}
	case "ruff":
		switch {
		case hasPair("format", "--check") || hasPair("--check", "format"):
			return detection.CommandClassFormatCheck
		case firstPositional(arguments) == "check":
			return detection.CommandClassLint
		}
	case "make", "just", "task":
		switch firstPositional(arguments) {
		case "test", "tests", "check":
			return detection.CommandClassTest
		case "build":
			return detection.CommandClassBuild
		case "lint":
			return detection.CommandClassLint
		case "typecheck", "type-check":
			return detection.CommandClassTypecheck
		}
	case "npm", "pnpm", "yarn", "bun":
		script := packageScript(arguments)
		switch script {
		case "test", "tests":
			return detection.CommandClassTest
		case "build":
			return detection.CommandClassBuild
		case "typecheck", "type-check", "check-types":
			return detection.CommandClassTypecheck
		case "lint":
			return detection.CommandClassLint
		case "format:check", "format-check":
			return detection.CommandClassFormatCheck
		}
	case "mvn", "mvnw", "gradle", "gradlew":
		switch {
		case hasArgument("test", "check"):
			return detection.CommandClassTest
		case hasArgument("build", "assemble", "package"):
			return detection.CommandClassBuild
		}
	}
	return detection.CommandClassOther
}

func goSubcommand(arguments []string) string {
	for index := 0; index < len(arguments); index++ {
		value := arguments[index]
		if value == "-c" || value == "-modfile" || value == "-overlay" {
			index++
			continue
		}
		if strings.HasPrefix(value, "-") {
			continue
		}
		return value
	}
	return ""
}

func cargoSubcommand(arguments []string) (string, []string) {
	for index := 0; index < len(arguments); index++ {
		value := arguments[index]
		if strings.HasPrefix(value, "+") {
			continue
		}
		if value == "--config" || value == "--manifest-path" ||
			value == "--target-dir" || value == "--color" {
			index++
			continue
		}
		if strings.HasPrefix(value, "-") {
			continue
		}
		return value, arguments[index+1:]
	}
	return "", nil
}

func firstPositional(arguments []string) string {
	for _, value := range arguments {
		if value == "--" {
			continue
		}
		if !strings.HasPrefix(value, "-") {
			return value
		}
	}
	return ""
}

func packageScript(arguments []string) string {
	for index, value := range arguments {
		if value == "run" && index+1 < len(arguments) {
			return arguments[index+1]
		}
		if index == 0 && value != "exec" && value != "x" {
			return value
		}
	}
	return ""
}

func classifyPermission(record numbat.EventRecord) string {
	tool := strings.ToLower(strings.TrimSpace(record.ToolName))
	switch {
	case record.URL != "" || containsAny(tool, "network", "http", "web", "fetch", "curl"):
		return "permission.network"
	case record.MCPServer != "" || record.MCPTool != "" || containsAny(tool, "mcp"):
		return "permission.mcp"
	case record.FilePath != "" || containsAny(tool, "file", "read", "write", "edit", "patch"):
		return "permission.file"
	case record.Command != "" || containsAny(tool, "shell", "bash", "terminal", "exec", "command"):
		return "permission.shell"
	case tool != "":
		return "permission.tool"
	default:
		return "permission.unknown"
	}
}

func lowerFields(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = strings.ToLower(value)
	}
	return result
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
