package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/DoplexLabs/belay-engine/internal/localapp"
)

var manageMCPConfiguration = localapp.ManageMCPConfig

func runMCPConfig(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
) error {
	if len(args) == 0 {
		printMCPConfigUsage(stderr)
		return errors.New("mcp-config requires install, status, or uninstall")
	}
	if isHelpArgument(args[0]) {
		printMCPConfigUsage(stdout)
		return nil
	}
	action := localapp.MCPConfigAction(args[0])
	if action != localapp.MCPConfigInstall &&
		action != localapp.MCPConfigStatus &&
		action != localapp.MCPConfigUninstall {
		printMCPConfigUsage(stderr)
		return fmt.Errorf("unknown mcp-config action %q", args[0])
	}
	flags := flag.NewFlagSet("mcp-config "+string(action), flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: belay mcp-config %s [flags]\n", action)
		fmt.Fprintln(
			stderr,
			"Targets: codex (CLI), claude (CLI), cursor (~/.cursor/mcp.json), "+
				"antigravity (~/.gemini/config/mcp_config.json).",
		)
		flags.PrintDefaults()
	}
	home := flags.String(
		"home",
		"",
		"Belay Local state directory (default BELAY_HOME or ~/.belay)",
	)
	allowCodexMCPAdd := false
	if action == localapp.MCPConfigInstall {
		flags.BoolVar(
			&allowCodexMCPAdd,
			"allow-codex-mcp-add",
			false,
			"accept Codex CLI non-atomic duplicate-name behavior after strict absence verification",
		)
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("mcp-config accepts no positional arguments after the action")
	}
	paths, err := localapp.ResolvePaths(*home)
	if err != nil {
		return err
	}
	executable, executableErr := currentExecutablePath()
	if executableErr != nil {
		executable = ""
	}
	installationID := ""
	if action == localapp.MCPConfigInstall {
		config, configErr := localapp.LoadOrCreateConfig(paths)
		if configErr != nil {
			return configErr
		}
		installationID = config.InstallationID
	}
	if allowCodexMCPAdd {
		fmt.Fprintln(
			stderr,
			"belay mcp-config: Codex MCP add opt-in accepts non-atomic duplicate-name behavior",
		)
	}
	result, manageErr := manageMCPConfiguration(ctx, localapp.MCPConfigRequest{
		Paths:            paths,
		InstallationID:   installationID,
		Executable:       executable,
		Action:           action,
		AllowCodexMCPAdd: allowCodexMCPAdd,
	})
	if err := writeJSON(stdout, result); err != nil {
		return err
	}
	return manageErr
}

func onboardMCPConfiguration(
	ctx context.Context,
	runtime preparedRuntime,
	commandName string,
	allowCodexMCPAdd bool,
	stderr io.Writer,
) bool {
	result, err := manageMCPConfiguration(ctx, localapp.MCPConfigRequest{
		Paths:            runtime.paths,
		InstallationID:   runtime.config.InstallationID,
		Executable:       runtime.belayExecutable,
		Action:           localapp.MCPConfigInstall,
		AllowCodexMCPAdd: allowCodexMCPAdd,
	})
	statuses := map[string]string{
		"codex":       "unavailable",
		"claude":      "unavailable",
		"cursor":      "unavailable",
		"antigravity": "unavailable",
	}
	for _, target := range result.Targets {
		statuses[target.Agent] = target.Status
	}
	fmt.Fprintf(
		stderr,
		"belay %s: mcp codex=%s claude=%s cursor=%s antigravity=%s\n",
		commandName,
		statuses["codex"],
		statuses["claude"],
		statuses["cursor"],
		statuses["antigravity"],
	)
	return err == nil && result.Complete()
}

func printMCPConfigUsage(writer io.Writer) {
	fmt.Fprintln(writer, `usage: belay mcp-config ACTION [flags]

Actions:
  status      show whether the Belay Local MCP server is registered with each agent
  install     register the Belay Local MCP server with detected agents
  uninstall   remove the Belay Local MCP registration

Examples:
  belay mcp-config status
  belay mcp-config install

Run belay mcp-config ACTION -h for the flags of one action.`)
}
