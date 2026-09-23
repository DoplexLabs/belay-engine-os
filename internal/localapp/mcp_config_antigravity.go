package localapp

import (
	"os"
	"path/filepath"
)

const (
	geminiDirectoryName            = ".gemini"
	antigravityDirectoryName       = "antigravity"
	antigravityConfigDirectoryName = "config"
	antigravityConfigFileName      = "mcp_config.json"
)

// antigravityJSONRegistry is the shared `mcpServers` JSON registry policy
// applied to ~/.gemini/config/mcp_config.json. Antigravity creates
// ~/.gemini/config lazily, so it may be absent on a machine that has
// Antigravity installed; the registry therefore creates that one directory
// (mode 0700) on install when ~/.gemini itself is a real directory.
var antigravityJSONRegistry = mcpJSONRegistry{label: "Antigravity", ensureDirectory: true}

// antigravityMCPTarget describes the Antigravity registry. Antigravity 2.0
// publishes no MCP configuration CLI, so the target is file backed: the global
// registry is the documented JSON object in ~/.gemini/config/mcp_config.json
// (%USERPROFILE%\.gemini\config\mcp_config.json on Windows). Belay never touches
// the workspace-level .agents/mcp_config.json or the legacy
// ~/.gemini/antigravity/mcp_config.json. Belay's own write is a single atomic
// rename of a fully rendered document, so repeating it can neither duplicate
// nor half-apply an entry; duplicateAddSafe is therefore true and the Codex
// `--allow-codex-mcp-add` opt-in does not apply to Antigravity.
func antigravityMCPTarget() mcpTargetAdapter {
	return mcpTargetAdapter{
		agent:            "antigravity",
		scope:            "user",
		duplicateAddSafe: true,
		file: &mcpFileAdapter{
			locate:  locateAntigravityConfig,
			inspect: inspectAntigravityConfig,
			add:     addAntigravityEntry,
			remove:  removeAntigravityEntry,
		},
	}
}

// locateAntigravityConfig reports the Antigravity registry path and whether
// Antigravity is detected. Antigravity 2.0 is detected only when
// ~/.gemini/antigravity, its application data directory, is a real directory;
// a symlink, a plain file, or an absent path is not a detected install. The
// registry path is reported even when ~/.gemini/config does not exist yet.
func locateAntigravityConfig(home string) (string, bool) {
	if home == "" || hasUnsafePathText(home) || !filepath.IsAbs(home) {
		return "", false
	}
	gemini := filepath.Join(filepath.Clean(home), geminiDirectoryName)
	info, err := os.Lstat(filepath.Join(gemini, antigravityDirectoryName))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	return filepath.Join(gemini, antigravityConfigDirectoryName, antigravityConfigFileName), true
}

// inspectAntigravityConfig classifies the `belay` entry in
// ~/.gemini/config/mcp_config.json under the shared registry policy. A missing
// ~/.gemini/config directory is simply an absent registry.
func inspectAntigravityConfig(path string) mcpInspection {
	return antigravityJSONRegistry.inspect(path)
}

// addAntigravityEntry installs the Belay entry, creating ~/.gemini/config when
// it is absent and ~/.gemini is a real directory.
func addAntigravityEntry(path string, identity MCPIdentity) mcpCommandResult {
	return antigravityJSONRegistry.add(path, identity)
}

// removeAntigravityEntry deletes the Belay entry from
// ~/.gemini/config/mcp_config.json when it is one of the allowed identities.
func removeAntigravityEntry(path string, allowed []MCPIdentity) mcpCommandResult {
	return antigravityJSONRegistry.remove(path, allowed)
}
