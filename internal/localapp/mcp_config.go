package localapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MCPConfigSchemaVersion = "belay.mcp-config.v1"

	MCPConfigInstall   MCPConfigAction = "install"
	MCPConfigStatus    MCPConfigAction = "status"
	MCPConfigUninstall MCPConfigAction = "uninstall"

	mcpCommandTimeout     = 10 * time.Second
	mcpTransactionTimeout = 30 * time.Second
	mcpOverallTimeout     = 35 * time.Second
	mcpStdoutLimit        = 64 << 10
	mcpStderrLimit        = 16 << 10
)

type MCPConfigAction string

type MCPConfigResult struct {
	SchemaVersion string                  `json:"schema_version"`
	Action        MCPConfigAction         `json:"action"`
	OverallStatus string                  `json:"overall_status"`
	Targets       []MCPConfigTargetResult `json:"targets"`
}

type MCPConfigTargetResult struct {
	Agent     string  `json:"agent"`
	Detected  bool    `json:"detected"`
	Scope     string  `json:"scope"`
	Status    string  `json:"status"`
	Ownership string  `json:"ownership"`
	Changed   bool    `json:"changed"`
	ErrorCode *string `json:"error_code"`
}

type MCPConfigRequest struct {
	Paths            Paths
	InstallationID   string
	Executable       string
	Action           MCPConfigAction
	AllowCodexMCPAdd bool
}

type MCPIdentity struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type mcpEntry struct {
	identity MCPIdentity
	scope    string
}

type mcpInspectionKind string

const (
	mcpInspectionAbsent       mcpInspectionKind = "absent"
	mcpInspectionPresent      mcpInspectionKind = "present"
	mcpInspectionUnverifiable mcpInspectionKind = "unverifiable"
	mcpInspectionUnavailable  mcpInspectionKind = "unavailable"
)

type mcpInspection struct {
	kind      mcpInspectionKind
	entry     mcpEntry
	errorCode string
}

type mcpCommandResult struct {
	exitCode       int
	stdout         string
	stderr         string
	stdoutOverflow bool
	stderrOverflow bool
}

type mcpCommandRunner func(context.Context, string, []string) mcpCommandResult

type mcpConfigDependencies struct {
	lookPath func(string) (string, error)
	run      mcpCommandRunner
	now      func() time.Time
	lock     func(context.Context, string) (func(), error)
}

type mcpTargetAdapter struct {
	agent            string
	executable       string
	scope            string
	duplicateAddSafe bool
	statusArgs       []string
	addPrefix        []string
	removeArgs       []string
	parseStatus      func(mcpCommandResult) mcpInspection
}

var defaultMCPConfigDependencies = mcpConfigDependencies{
	lookPath: execLookPath,
	run:      runMCPCommand,
	now:      func() time.Time { return time.Now().UTC() },
	lock:     acquireMCPConfigLock,
}

func ManageMCPConfig(ctx context.Context, request MCPConfigRequest) (MCPConfigResult, error) {
	return manageMCPConfig(ctx, request, defaultMCPConfigDependencies)
}

func (result MCPConfigResult) Complete() bool {
	return result.OverallStatus == "complete"
}

func ResolveBelayMCPIdentity(executable, home string) (MCPIdentity, error) {
	if hasUnsafePathText(executable) {
		return MCPIdentity{}, errors.New("invalid Belay executable")
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return MCPIdentity{}, errors.New("invalid Belay executable")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return MCPIdentity{}, errors.New("invalid Belay executable")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return MCPIdentity{}, errors.New("invalid Belay executable")
	}
	if hasUnsafePathText(resolved) || !filepath.IsAbs(resolved) {
		return MCPIdentity{}, errors.New("invalid Belay executable")
	}
	if hasUnsafePathText(home) || !filepath.IsAbs(home) {
		return MCPIdentity{}, errors.New("invalid Belay home")
	}
	cleanHome := filepath.Clean(home)
	args := []string{"mcp"}
	defaultRoot, defaultErr := defaultBelayRoot()
	if defaultErr != nil || cleanHome != defaultRoot {
		args = append(args, "--home", cleanHome)
	}
	return MCPIdentity{Command: resolved, Args: args}, nil
}

func defaultBelayRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(filepath.Join(home, DefaultDirName))
	if err != nil {
		return "", err
	}
	return filepath.Clean(root), nil
}

func hasUnsafePathText(value string) bool {
	if strings.TrimSpace(value) == "" || strings.IndexByte(value, 0) >= 0 {
		return true
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func manageMCPConfig(
	ctx context.Context,
	request MCPConfigRequest,
	dependencies mcpConfigDependencies,
) (MCPConfigResult, error) {
	result := MCPConfigResult{
		SchemaVersion: MCPConfigSchemaVersion,
		Action:        request.Action,
		OverallStatus: "partial",
		Targets:       make([]MCPConfigTargetResult, 0, 2),
	}
	if request.Action != MCPConfigInstall &&
		request.Action != MCPConfigStatus &&
		request.Action != MCPConfigUninstall {
		return result, errors.New("unsupported MCP configuration action")
	}
	identity, identityErr := ResolveBelayMCPIdentity(request.Executable, request.Paths.Root)
	targets := defaultMCPTargets()
	available := make([]mcpTargetAdapter, len(targets))
	copy(available, targets)
	for index := range available {
		path, err := dependencies.lookPath(available[index].executable)
		if err != nil || !usableExecutable(path) {
			available[index].executable = ""
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !usableExecutable(resolved) {
			available[index].executable = ""
			continue
		}
		available[index].executable = path
	}

	if identityErr != nil {
		code := "invalid_executable"
		if strings.Contains(identityErr.Error(), "home") {
			code = "invalid_home"
		}
		for _, target := range available {
			result.Targets = append(result.Targets, MCPConfigTargetResult{
				Agent:     target.agent,
				Detected:  target.executable != "",
				Scope:     "user",
				Status:    failureStatus(request.Action, target.executable == ""),
				Ownership: "unknown",
				ErrorCode: stringPointer(code),
			})
		}
		return result, errors.New("MCP configuration incomplete")
	}

	overallCtx, cancelOverall := context.WithTimeout(ctx, mcpOverallTimeout)
	defer cancelOverall()

	manifest, manifestState := loadMCPManifest(request.Paths.MCPManifest, request.InstallationID)
	if manifestState == manifestMissing && request.Action == MCPConfigInstall {
		installationID := request.InstallationID
		if !validInstallationID(installationID) {
			installationID = existingInstallationID(request.Paths.Config)
		}
		if !validInstallationID(installationID) {
			var err error
			installationID, err = newInstallationID()
			if err != nil {
				manifestState = manifestInvalid
			}
		}
		manifest = mcpOwnershipManifest{
			Version:        mcpManifestVersion,
			InstallationID: installationID,
			Targets:        make(map[string]mcpManifestTarget),
		}
	}
	if manifest.Targets == nil {
		manifest.Targets = make(map[string]mcpManifestTarget)
	}

	var unlock func()
	if request.Action != MCPConfigStatus {
		if request.Action == MCPConfigInstall {
			if err := ensurePrivateDirectory(request.Paths.Root); err != nil {
				for _, target := range available {
					result.Targets = append(result.Targets, targetLockFailure(request.Action, target))
				}
				return result, errors.New("MCP configuration incomplete")
			}
		}
		if request.Paths.MCPConfigLock == "" {
			for _, target := range available {
				result.Targets = append(result.Targets, targetLockFailure(request.Action, target))
			}
			return result, errors.New("MCP configuration incomplete")
		}
		var err error
		unlock, err = dependencies.lock(overallCtx, request.Paths.MCPConfigLock)
		if err != nil {
			for _, target := range available {
				result.Targets = append(result.Targets, targetLockFailure(request.Action, target))
			}
			return result, errors.New("MCP configuration incomplete")
		}
		defer unlock()
		// The lock can have been contended; refresh ownership state after it.
		lockedManifest, lockedState := loadMCPManifest(
			request.Paths.MCPManifest,
			request.InstallationID,
		)
		if lockedState != manifestMissing || manifestState != manifestMissing {
			manifest, manifestState = lockedManifest, lockedState
			if manifest.Targets == nil {
				manifest.Targets = make(map[string]mcpManifestTarget)
			}
		}
	}

	manifestChanged := false
	for _, target := range available {
		targetResult := MCPConfigTargetResult{
			Agent:     target.agent,
			Detected:  target.executable != "",
			Scope:     "user",
			Ownership: "none",
		}
		if target.executable == "" {
			if request.Action == MCPConfigInstall {
				targetResult.Status = "skipped_not_detected"
			} else {
				targetResult.Status = "unavailable"
				targetResult.Ownership = "unknown"
				targetResult.ErrorCode = stringPointer("cli_not_found")
			}
			result.Targets = append(result.Targets, targetResult)
			continue
		}
		transactionCtx, cancel := context.WithTimeout(overallCtx, mcpTransactionTimeout)
		switch request.Action {
		case MCPConfigStatus:
			targetResult = statusMCPConfigTarget(
				transactionCtx,
				dependencies,
				target,
				identity,
				manifest,
				manifestState,
			)
		case MCPConfigInstall:
			targetResult, manifestChanged = installMCPConfigTarget(
				transactionCtx,
				dependencies,
				target,
				identity,
				manifest,
				manifestState,
				manifestChanged,
				request.AllowCodexMCPAdd,
			)
		case MCPConfigUninstall:
			targetResult, manifestChanged = uninstallMCPConfigTarget(
				transactionCtx,
				dependencies,
				target,
				identity,
				manifest,
				manifestState,
				manifestChanged,
			)
		}
		cancel()
		result.Targets = append(result.Targets, targetResult)
	}

	if manifestChanged &&
		(request.Action == MCPConfigInstall || manifestState == manifestValid) {
		if err := writeMCPManifest(request.Paths.MCPManifest, manifest); err != nil {
			for index := range result.Targets {
				if !result.Targets[index].Changed {
					continue
				}
				result.Targets[index].ErrorCode = stringPointer("ownership_manifest_invalid")
			}
		}
	}
	if mcpConfigOperationComplete(result) {
		result.OverallStatus = "complete"
		return result, nil
	}
	return result, errors.New("MCP configuration incomplete")
}

func defaultMCPTargets() []mcpTargetAdapter {
	return []mcpTargetAdapter{
		{
			agent:            "codex",
			executable:       "codex",
			scope:            "user",
			duplicateAddSafe: false,
			statusArgs:       []string{"mcp", "get", "belay", "--json"},
			addPrefix:        []string{"mcp", "add", "belay", "--"},
			removeArgs:       []string{"mcp", "remove", "belay"},
			parseStatus:      parseCodexMCPStatus,
		},
		{
			agent:            "claude",
			executable:       "claude",
			scope:            "user",
			duplicateAddSafe: true,
			statusArgs:       []string{"mcp", "get", "belay"},
			addPrefix:        []string{"mcp", "add", "--scope", "user", "belay", "--"},
			removeArgs:       []string{"mcp", "remove", "--scope", "user", "belay"},
			parseStatus:      parseClaudeMCPStatus,
		},
	}
}

func statusMCPConfigTarget(
	ctx context.Context,
	dependencies mcpConfigDependencies,
	target mcpTargetAdapter,
	current MCPIdentity,
	manifest mcpOwnershipManifest,
	manifestState manifestLoadState,
) MCPConfigTargetResult {
	inspection := inspectMCPConfigTarget(ctx, dependencies, target)
	ownership := classifyMCPOwnership(inspection, current, manifest, manifestState, target.agent)
	result := MCPConfigTargetResult{
		Agent:     target.agent,
		Detected:  true,
		Scope:     "user",
		Ownership: ownership,
	}
	switch inspection.kind {
	case mcpInspectionAbsent:
		result.Status = "absent"
	case mcpInspectionUnverifiable:
		result.Status = "unverifiable"
		result.ErrorCode = stringPointer(inspection.errorCode)
	case mcpInspectionUnavailable:
		result.Status = "unavailable"
		result.ErrorCode = stringPointer(inspection.errorCode)
	default:
		switch ownership {
		case "current":
			result.Status = "owned_current"
		case "recognized":
			result.Status = "owned_recognized"
		default:
			result.Status = "foreign"
		}
	}
	if manifestState == manifestInvalid && result.ErrorCode == nil {
		result.ErrorCode = stringPointer("ownership_manifest_invalid")
	}
	return result
}

func installMCPConfigTarget(
	ctx context.Context,
	dependencies mcpConfigDependencies,
	target mcpTargetAdapter,
	current MCPIdentity,
	manifest mcpOwnershipManifest,
	manifestState manifestLoadState,
	manifestChanged bool,
	allowCodexMCPAdd bool,
) (MCPConfigTargetResult, bool) {
	status := statusMCPConfigTarget(ctx, dependencies, target, current, manifest, manifestState)
	switch status.Status {
	case "owned_current":
		status.Status = "already_installed"
		return status, manifestChanged
	case "foreign":
		status.Status = "foreign_preserved"
		status.ErrorCode = stringPointer("conflicting_entry")
		return status, manifestChanged
	case "unverifiable", "unavailable":
		return status, manifestChanged
	case "absent":
		if !target.duplicateAddSafe &&
			!(target.agent == "codex" && allowCodexMCPAdd) {
			status.Status = "unavailable"
			status.Ownership = "unknown"
			status.ErrorCode = stringPointer("install_failed")
			return status, manifestChanged
		}
		return installAbsentMCPConfigTarget(
			ctx,
			dependencies,
			target,
			current,
			manifest,
			status,
			manifestChanged,
		)
	case "owned_recognized":
		if !target.duplicateAddSafe {
			status.Status = "unavailable"
			status.ErrorCode = stringPointer("install_failed")
			return status, manifestChanged
		}
		return updateRecognizedMCPConfigTarget(
			ctx,
			dependencies,
			target,
			current,
			manifest,
			status,
			manifestChanged,
		)
	default:
		status.Status = "failed"
		status.ErrorCode = stringPointer("status_failed")
		return status, manifestChanged
	}
}

func installAbsentMCPConfigTarget(
	ctx context.Context,
	dependencies mcpConfigDependencies,
	target mcpTargetAdapter,
	current MCPIdentity,
	manifest mcpOwnershipManifest,
	result MCPConfigTargetResult,
	manifestChanged bool,
) (MCPConfigTargetResult, bool) {
	addResult := runTargetCommand(
		ctx,
		dependencies,
		target,
		append(append([]string(nil), target.addPrefix...), append([]string{current.Command}, current.Args...)...),
	)
	inspection := inspectMCPConfigTarget(ctx, dependencies, target)
	if inspection.kind == mcpInspectionPresent &&
		classifyMCPOwnership(inspection, current, manifest, manifestMissing, target.agent) == "current" {
		result.Status = "installed"
		result.Ownership = "current"
		result.Changed = true
		result.ErrorCode = nil
		recordManifestIdentity(&manifest, target.agent, current, dependencies.now())
		return result, true
	}
	result.Status = "failed"
	result.Ownership = ownershipForInspection(inspection)
	result.ErrorCode = stringPointer(commandFailureCode(addResult, "install_failed", "verification_failed"))
	return result, manifestChanged
}

func updateRecognizedMCPConfigTarget(
	ctx context.Context,
	dependencies mcpConfigDependencies,
	target mcpTargetAdapter,
	current MCPIdentity,
	manifest mcpOwnershipManifest,
	result MCPConfigTargetResult,
	manifestChanged bool,
) (MCPConfigTargetResult, bool) {
	oldTarget, ok := manifest.Targets[target.agent]
	if !ok {
		result.Status = "foreign_preserved"
		result.Ownership = "foreign"
		result.ErrorCode = stringPointer("conflicting_entry")
		return result, manifestChanged
	}
	old := MCPIdentity{Command: oldTarget.Command, Args: append([]string(nil), oldTarget.Args...)}
	recheck := inspectMCPConfigTarget(ctx, dependencies, target)
	if classifyMCPOwnership(recheck, current, manifest, manifestValid, target.agent) != "recognized" {
		result.Status = "failed"
		result.Ownership = ownershipForInspection(recheck)
		result.ErrorCode = stringPointer("verification_failed")
		return result, manifestChanged
	}
	removeResult := runTargetCommand(ctx, dependencies, target, target.removeArgs)
	removedState := inspectMCPConfigTarget(ctx, dependencies, target)
	if removedState.kind != mcpInspectionAbsent {
		result.Status = "failed"
		if removeResult.exitCode != 0 {
			result.ErrorCode = stringPointer("remove_failed")
		} else {
			result.ErrorCode = stringPointer("verification_failed")
		}
		return result, manifestChanged
	}
	addResult := runTargetCommand(
		ctx,
		dependencies,
		target,
		append(append([]string(nil), target.addPrefix...), append([]string{current.Command}, current.Args...)...),
	)
	post := inspectMCPConfigTarget(ctx, dependencies, target)
	if post.kind == mcpInspectionPresent &&
		classifyMCPOwnership(post, current, manifest, manifestMissing, target.agent) == "current" {
		result.Status = "updated"
		result.Ownership = "current"
		result.Changed = true
		result.ErrorCode = nil
		recordManifestIdentity(&manifest, target.agent, current, dependencies.now())
		return result, true
	}
	if post.kind != mcpInspectionAbsent {
		result.Status = "rollback_failed"
		result.Ownership = ownershipForInspection(post)
		result.ErrorCode = stringPointer("rollback_failed")
		return result, manifestChanged
	}
	rollbackResult := runTargetCommand(
		ctx,
		dependencies,
		target,
		append(append([]string(nil), target.addPrefix...), append([]string{old.Command}, old.Args...)...),
	)
	rollbackInspection := inspectMCPConfigTarget(ctx, dependencies, target)
	if rollbackResult.exitCode == 0 &&
		classifyMCPOwnership(rollbackInspection, current, manifest, manifestValid, target.agent) == "recognized" {
		result.Status = "rollback_restored"
		result.Ownership = "recognized"
		result.Changed = false
		result.ErrorCode = stringPointer(commandFailureCode(addResult, "install_failed", "verification_failed"))
		return result, manifestChanged
	}
	result.Status = "rollback_failed"
	result.Ownership = ownershipForInspection(rollbackInspection)
	result.ErrorCode = stringPointer("rollback_failed")
	return result, manifestChanged
}

func uninstallMCPConfigTarget(
	ctx context.Context,
	dependencies mcpConfigDependencies,
	target mcpTargetAdapter,
	current MCPIdentity,
	manifest mcpOwnershipManifest,
	manifestState manifestLoadState,
	manifestChanged bool,
) (MCPConfigTargetResult, bool) {
	status := statusMCPConfigTarget(ctx, dependencies, target, current, manifest, manifestState)
	switch status.Status {
	case "absent":
		return status, manifestChanged
	case "foreign":
		status.Status = "foreign_preserved"
		status.ErrorCode = stringPointer("conflicting_entry")
		return status, manifestChanged
	case "unverifiable", "unavailable":
		return status, manifestChanged
	case "owned_current", "owned_recognized":
	default:
		status.Status = "failed"
		status.ErrorCode = stringPointer("status_failed")
		return status, manifestChanged
	}
	recheck := inspectMCPConfigTarget(ctx, dependencies, target)
	recheckedOwnership := classifyMCPOwnership(
		recheck,
		current,
		manifest,
		manifestState,
		target.agent,
	)
	if recheckedOwnership != "current" && recheckedOwnership != "recognized" {
		status.Status = "failed"
		status.Ownership = ownershipForInspection(recheck)
		status.ErrorCode = stringPointer("verification_failed")
		return status, manifestChanged
	}
	remove := runTargetCommand(ctx, dependencies, target, target.removeArgs)
	post := inspectMCPConfigTarget(ctx, dependencies, target)
	if post.kind != mcpInspectionAbsent {
		status.Status = "failed"
		status.Ownership = ownershipForInspection(post)
		if remove.exitCode != 0 {
			status.ErrorCode = stringPointer("remove_failed")
		} else {
			status.ErrorCode = stringPointer("verification_failed")
		}
		return status, manifestChanged
	}
	status.Status = "removed"
	status.Ownership = "none"
	status.Changed = true
	status.ErrorCode = nil
	delete(manifest.Targets, target.agent)
	return status, true
}

func inspectMCPConfigTarget(
	ctx context.Context,
	dependencies mcpConfigDependencies,
	target mcpTargetAdapter,
) mcpInspection {
	result := runTargetCommand(ctx, dependencies, target, target.statusArgs)
	if result.stdoutOverflow || result.stderrOverflow {
		return mcpInspection{
			kind:      mcpInspectionUnverifiable,
			errorCode: "status_output_too_large",
		}
	}
	if ctx.Err() != nil || result.exitCode == -1 {
		return mcpInspection{kind: mcpInspectionUnavailable, errorCode: "status_timeout"}
	}
	return target.parseStatus(result)
}

func runTargetCommand(
	ctx context.Context,
	dependencies mcpConfigDependencies,
	target mcpTargetAdapter,
	args []string,
) mcpCommandResult {
	commandCtx, cancel := context.WithTimeout(ctx, mcpCommandTimeout)
	defer cancel()
	return dependencies.run(commandCtx, target.executable, args)
}

func runMCPCommand(ctx context.Context, executable string, args []string) mcpCommandResult {
	var stdout boundedMCPWriter
	stdout.limit = mcpStdoutLimit
	var stderr boundedMCPWriter
	stderr.limit = mcpStderrLimit
	command := exec.CommandContext(ctx, executable, args...)
	command.Stdin = bytes.NewReader(nil)
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.Env = allowedMCPEnvironment()
	err := command.Run()
	exitCode := -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	} else if err == nil {
		exitCode = 0
	}
	return mcpCommandResult{
		exitCode:       exitCode,
		stdout:         safeMCPOutput(stdout.String()),
		stderr:         safeMCPOutput(stderr.String()),
		stdoutOverflow: stdout.overflow,
		stderrOverflow: stderr.overflow,
	}
}

func allowedMCPEnvironment() []string {
	names := []string{
		"HOME",
		"USER",
		"TMPDIR",
		"PATH",
		"LANG",
		"LC_ALL",
		"CODEX_HOME",
		"CLAUDE_CONFIG_DIR",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
	}
	result := make([]string, 0, len(names)+3)
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	result = append(result, "NO_COLOR=1", "TERM=dumb", "CI=1")
	return result
}

type boundedMCPWriter struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (writer *boundedMCPWriter) Write(data []byte) (int, error) {
	original := len(data)
	remaining := writer.limit - writer.buffer.Len()
	if remaining <= 0 {
		writer.overflow = true
		return original, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		writer.overflow = true
	}
	_, _ = writer.buffer.Write(data)
	return original, nil
}

func (writer *boundedMCPWriter) String() string {
	return writer.buffer.String()
}

func safeMCPOutput(value string) string {
	if !utf8.ValidString(value) {
		return ""
	}
	var output strings.Builder
	output.Grow(len(value))
	for _, character := range value {
		if character == '\n' || character == '\r' || character == '\t' ||
			!unicode.IsControl(character) {
			output.WriteRune(character)
		}
	}
	return output.String()
}

type codexStatus struct {
	Name              string          `json:"name"`
	Enabled           *bool           `json:"enabled"`
	DisabledReason    json.RawMessage `json:"disabled_reason"`
	Transport         json.RawMessage `json:"transport"`
	EnabledTools      json.RawMessage `json:"enabled_tools"`
	DisabledTools     json.RawMessage `json:"disabled_tools"`
	StartupTimeoutSec json.RawMessage `json:"startup_timeout_sec"`
	ToolTimeoutSec    json.RawMessage `json:"tool_timeout_sec"`
}

type codexTransport struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	EnvVars []string          `json:"env_vars"`
	CWD     *string           `json:"cwd"`
	URL     *string           `json:"url"`
	Headers map[string]string `json:"headers"`
}

func parseCodexMCPStatus(result mcpCommandResult) mcpInspection {
	if result.exitCode != 0 {
		if codexAbsentOutput(result.stdout, result.stderr) {
			return mcpInspection{kind: mcpInspectionAbsent}
		}
		return mcpInspection{kind: mcpInspectionUnavailable, errorCode: "status_failed"}
	}
	decoder := json.NewDecoder(strings.NewReader(result.stdout))
	decoder.DisallowUnknownFields()
	var status codexStatus
	if err := decoder.Decode(&status); err != nil {
		return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF ||
		status.Name != "belay" ||
		status.Enabled == nil ||
		!*status.Enabled ||
		!jsonNull(status.DisabledReason) ||
		!jsonNull(status.EnabledTools) ||
		!jsonNull(status.DisabledTools) ||
		!jsonNull(status.StartupTimeoutSec) ||
		!jsonNull(status.ToolTimeoutSec) {
		return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
	}
	var transport codexTransport
	transportDecoder := json.NewDecoder(bytes.NewReader(status.Transport))
	transportDecoder.DisallowUnknownFields()
	if err := transportDecoder.Decode(&transport); err != nil {
		return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
	}
	var transportExtra any
	if transportDecoder.Decode(&transportExtra) != io.EOF {
		return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
	}
	if transport.Type != "stdio" ||
		transport.Command == "" ||
		len(transport.Env) != 0 ||
		len(transport.EnvVars) != 0 ||
		(transport.CWD != nil && *transport.CWD != "") ||
		(transport.URL != nil && *transport.URL != "") ||
		len(transport.Headers) != 0 {
		return mcpInspection{kind: mcpInspectionPresent, entry: mcpEntry{
			scope: "user",
		}}
	}
	return mcpInspection{
		kind: mcpInspectionPresent,
		entry: mcpEntry{
			scope: "user",
			identity: MCPIdentity{
				Command: transport.Command,
				Args:    append([]string(nil), transport.Args...),
			},
		},
	}
}

func codexAbsentOutput(stdout, stderr string) bool {
	const expected = "Error: No MCP server named 'belay' found."
	return singleExactOutputLine(stdout, expected) && strings.TrimSpace(stderr) == "" ||
		singleExactOutputLine(stderr, expected) && strings.TrimSpace(stdout) == ""
}

func parseClaudeMCPStatus(result mcpCommandResult) mcpInspection {
	if result.exitCode != 0 {
		if claudeAbsentOutput(result.stdout, result.stderr) {
			return mcpInspection{kind: mcpInspectionAbsent}
		}
		return mcpInspection{kind: mcpInspectionUnavailable, errorCode: "status_failed"}
	}
	lines := strings.Split(strings.ReplaceAll(result.stdout, "\r\n", "\n"), "\n")
	fields := make(map[string]string)
	headerSeen := false
	removeSeen := false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if line == "belay:" {
			if headerSeen {
				return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
			}
			headerSeen = true
			continue
		}
		if strings.HasPrefix(line, "To remove this server, run:") {
			removeSeen = true
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
		}
		key = strings.TrimSpace(key)
		if _, exists := fields[key]; exists {
			return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
		}
		switch key {
		case "Scope", "Status", "Issue", "Type", "Command", "Args", "Environment":
			fields[key] = strings.TrimSpace(value)
		default:
			return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
		}
	}
	if !headerSeen || !removeSeen ||
		fields["Scope"] != "User config (available in all your projects)" ||
		fields["Type"] != "stdio" ||
		fields["Command"] == "" ||
		fields["Environment"] != "" {
		return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
	}
	args, ok := parseClaudeArgs(fields["Args"])
	if !ok {
		return mcpInspection{kind: mcpInspectionUnverifiable, errorCode: "status_unparseable"}
	}
	return mcpInspection{
		kind: mcpInspectionPresent,
		entry: mcpEntry{
			scope: "user",
			identity: MCPIdentity{
				Command: fields["Command"],
				Args:    args,
			},
		},
	}
}

func parseClaudeArgs(value string) ([]string, bool) {
	if value == "mcp" {
		return []string{"mcp"}, true
	}
	const prefix = "mcp --home "
	if !strings.HasPrefix(value, prefix) {
		return nil, false
	}
	home := strings.TrimPrefix(value, prefix)
	if home == "" || strings.ContainsAny(home, "\t\r\n") {
		return nil, false
	}
	// Claude's human output does not quote argument boundaries. A home with
	// whitespace cannot be proven to be one argv element and is fail-closed.
	if strings.IndexFunc(home, unicode.IsSpace) >= 0 {
		return nil, false
	}
	return []string{"mcp", "--home", home}, true
}

func claudeAbsentOutput(stdout, stderr string) bool {
	for _, value := range []string{stdout, stderr} {
		for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == `No MCP server named "belay".` ||
				strings.HasPrefix(line, `No MCP server named "belay". Configured servers:`) {
				return true
			}
		}
	}
	return false
}

func singleExactOutputLine(value, expected string) bool {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line != expected {
			return false
		}
		count++
	}
	return count == 1
}

func jsonNull(value json.RawMessage) bool {
	return len(value) == 0 || strings.TrimSpace(string(value)) == "null"
}

func classifyMCPOwnership(
	inspection mcpInspection,
	current MCPIdentity,
	manifest mcpOwnershipManifest,
	manifestState manifestLoadState,
	agent string,
) string {
	if inspection.kind == mcpInspectionAbsent {
		return "none"
	}
	if inspection.kind != mcpInspectionPresent {
		return "unknown"
	}
	if inspection.entry.scope != "user" {
		return "foreign"
	}
	if equalMCPIdentity(inspection.entry.identity, current) {
		return "current"
	}
	if manifestState == manifestValid {
		if target, ok := manifest.Targets[agent]; ok {
			recognized := MCPIdentity{
				Command: target.Command,
				Args:    append([]string(nil), target.Args...),
			}
			if target.Scope == "user" && equalMCPIdentity(inspection.entry.identity, recognized) {
				return "recognized"
			}
		}
	}
	return "foreign"
}

func equalMCPIdentity(left, right MCPIdentity) bool {
	return left.Command == right.Command && slices.Equal(left.Args, right.Args)
}

func ownershipForInspection(inspection mcpInspection) string {
	switch inspection.kind {
	case mcpInspectionAbsent:
		return "none"
	case mcpInspectionPresent:
		return "foreign"
	default:
		return "unknown"
	}
}

func commandFailureCode(result mcpCommandResult, commandCode, verificationCode string) string {
	if result.stdoutOverflow || result.stderrOverflow {
		return "status_output_too_large"
	}
	if result.exitCode != 0 {
		return commandCode
	}
	return verificationCode
}

func recordManifestIdentity(
	manifest *mcpOwnershipManifest,
	agent string,
	identity MCPIdentity,
	now time.Time,
) {
	if manifest.Targets == nil {
		manifest.Targets = make(map[string]mcpManifestTarget)
	}
	manifest.Targets[agent] = mcpManifestTarget{
		Scope:      "user",
		Command:    identity.Command,
		Args:       append([]string(nil), identity.Args...),
		VerifiedAt: now.UTC().Format(time.RFC3339),
	}
}

func mcpConfigOperationComplete(result MCPConfigResult) bool {
	for _, target := range result.Targets {
		if target.ErrorCode != nil {
			return false
		}
		switch result.Action {
		case MCPConfigInstall:
			if target.Status != "installed" &&
				target.Status != "already_installed" &&
				target.Status != "updated" &&
				target.Status != "skipped_not_detected" {
				return false
			}
		case MCPConfigStatus:
			if target.Status != "owned_current" &&
				target.Status != "owned_recognized" &&
				target.Status != "absent" &&
				target.Status != "foreign" {
				return false
			}
		case MCPConfigUninstall:
			if target.Status != "removed" && target.Status != "absent" {
				return false
			}
		}
	}
	return true
}

func failureStatus(action MCPConfigAction, undetected bool) string {
	if undetected && action == MCPConfigInstall {
		return "skipped_not_detected"
	}
	return "unavailable"
}

func targetLockFailure(action MCPConfigAction, target mcpTargetAdapter) MCPConfigTargetResult {
	status := "unavailable"
	if action == MCPConfigInstall && target.executable == "" {
		status = "skipped_not_detected"
	}
	return MCPConfigTargetResult{
		Agent:     target.agent,
		Detected:  target.executable != "",
		Scope:     "user",
		Status:    status,
		Ownership: "unknown",
		ErrorCode: stringPointer("lock_timeout"),
	}
}

func stringPointer(value string) *string {
	return &value
}

func existingInstallationID(path string) string {
	file, _, size, err := openRegularNoFollow(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	if size <= 0 || size > 64<<10 {
		return ""
	}
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil || config.Version != ConfigVersion {
		return ""
	}
	if !validInstallationID(config.InstallationID) {
		return ""
	}
	return config.InstallationID
}
