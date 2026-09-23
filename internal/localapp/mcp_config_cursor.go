package localapp

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	cursorDirectoryName  = ".cursor"
	cursorConfigFileName = "mcp.json"
)

// cursorJSONRegistry is the shared `mcpServers` JSON registry policy applied to
// ~/.cursor/mcp.json. Cursor is detected by ~/.cursor itself, so the registry
// directory always exists when a write is attempted and is never created.
var cursorJSONRegistry = mcpJSONRegistry{label: "Cursor"}

// cursorMCPTarget describes the Cursor registry. Cursor publishes no MCP
// configuration CLI, so the target is file backed: the global registry is the
// documented JSON object in ~/.cursor/mcp.json. Belay's own write is a single
// atomic rename of a fully rendered document, so repeating it can neither
// duplicate nor half-apply an entry; duplicateAddSafe is therefore true and the
// Codex `--allow-codex-mcp-add` opt-in does not apply to Cursor.
func cursorMCPTarget() mcpTargetAdapter {
	return mcpTargetAdapter{
		agent:            "cursor",
		scope:            "user",
		duplicateAddSafe: true,
		file: &mcpFileAdapter{
			locate:  locateCursorConfig,
			inspect: inspectCursorConfig,
			add:     addCursorEntry,
			remove:  removeCursorEntry,
		},
	}
}

// locateCursorConfig reports the Cursor registry path and whether Cursor is
// detected. Cursor is detected only when ~/.cursor is a real directory; a
// symlink, a plain file, or an absent path is not a detected Cursor install.
func locateCursorConfig(home string) (string, bool) {
	if home == "" || hasUnsafePathText(home) || !filepath.IsAbs(home) {
		return "", false
	}
	directory := filepath.Join(filepath.Clean(home), cursorDirectoryName)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	return filepath.Join(directory, cursorConfigFileName), true
}

// inspectCursorConfig classifies the `belay` entry in ~/.cursor/mcp.json under
// the shared registry policy.
func inspectCursorConfig(path string) mcpInspection {
	return cursorJSONRegistry.inspect(path)
}

// parseCursorEntry decodes a stdio launch entry Belay could have written.
func parseCursorEntry(raw json.RawMessage) (MCPIdentity, bool) {
	return parseMCPJSONEntry(raw)
}

// addCursorEntry installs the Belay entry in ~/.cursor/mcp.json.
func addCursorEntry(path string, identity MCPIdentity) mcpCommandResult {
	return cursorJSONRegistry.add(path, identity)
}

// removeCursorEntry deletes the Belay entry from ~/.cursor/mcp.json when it is
// one of the allowed identities.
func removeCursorEntry(path string, allowed []MCPIdentity) mcpCommandResult {
	return cursorJSONRegistry.remove(path, allowed)
}
