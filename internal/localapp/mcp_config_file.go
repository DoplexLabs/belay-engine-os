package localapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
)

const (
	mcpJSONServersKey     = "mcpServers"
	mcpJSONEntryName      = "belay"
	mcpJSONConfigMaxBytes = 1 << 20
)

// mcpJSONRegistry describes one harness whose MCP registry is a JSON document
// with a top-level `mcpServers` object (Cursor's ~/.cursor/mcp.json and
// Antigravity's ~/.gemini/config/mcp_config.json share this shape). The
// registry performs the file-backed status/add/remove actions with one policy:
//
//   - only an entry carrying exactly `command` and `args` is verifiable; every
//     other shape (remote `serverUrl`/`url`, `env`, unknown members, malformed
//     JSON) is unverifiable and is never rewritten;
//   - every other server and every unknown top-level key is round-tripped as
//     an opaque json.RawMessage, so it survives a Belay write byte-for-byte;
//   - the document is re-rendered with sorted keys and replaced by one atomic
//     rename of a private (0600) temporary file, never through a symlink.
type mcpJSONRegistry struct {
	// label names the harness in error text ("<label> MCP configuration ...").
	label string
	// ensureDirectory lets add create the registry's immediate parent directory
	// (mode 0700) when it is absent and its own parent is a real directory.
	// Harnesses whose registry directory is the detection marker leave it false.
	ensureDirectory bool
}

// mcpJSONDocument is the round-tripped registry. Every value except the Belay
// entry stays an opaque json.RawMessage so foreign servers, remote entries and
// unknown top-level keys survive a Belay write unread and unmodified.
type mcpJSONDocument struct {
	root           map[string]json.RawMessage
	servers        map[string]json.RawMessage
	serversPresent bool
}

// fileAdapter wires the registry into the target adapter vocabulary next to a
// harness-specific locate function.
func (registry mcpJSONRegistry) fileAdapter(locate func(home string) (string, bool)) *mcpFileAdapter {
	return &mcpFileAdapter{
		locate:  locate,
		inspect: registry.inspect,
		add:     registry.add,
		remove:  registry.remove,
	}
}

// load reads the registry with the no-follow regular-file helper under a fixed
// size cap. A missing file (or a missing parent directory) is an empty document;
// anything Belay cannot prove it understands is returned as a failing
// inspection instead.
func (registry mcpJSONRegistry) load(path string) (mcpJSONDocument, *mcpInspection) {
	unavailable := &mcpInspection{
		kind:      mcpInspectionUnavailable,
		errorCode: "status_failed",
	}
	if directory, err := os.Lstat(filepath.Dir(path)); err == nil {
		if !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 {
			return mcpJSONDocument{}, unavailable
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return mcpJSONDocument{}, unavailable
	}
	file, _, size, err := openRegularNoFollow(path)
	if errors.Is(err, os.ErrNotExist) {
		return mcpJSONDocument{root: map[string]json.RawMessage{}}, nil
	}
	if err != nil {
		return mcpJSONDocument{}, unavailable
	}
	defer file.Close()
	tooLarge := &mcpInspection{
		kind:      mcpInspectionUnverifiable,
		errorCode: "status_output_too_large",
	}
	if size > mcpJSONConfigMaxBytes {
		return mcpJSONDocument{}, tooLarge
	}
	body, readErr := io.ReadAll(io.LimitReader(file, mcpJSONConfigMaxBytes+1))
	if readErr != nil {
		return mcpJSONDocument{}, unavailable
	}
	if len(body) > mcpJSONConfigMaxBytes {
		return mcpJSONDocument{}, tooLarge
	}
	unparseable := &mcpInspection{
		kind:      mcpInspectionUnverifiable,
		errorCode: "status_unparseable",
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var root map[string]json.RawMessage
	if err := decoder.Decode(&root); err != nil || root == nil {
		return mcpJSONDocument{}, unparseable
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return mcpJSONDocument{}, unparseable
	}
	document := mcpJSONDocument{root: root}
	raw, present := root[mcpJSONServersKey]
	if !present {
		return document, nil
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &servers); err != nil || servers == nil {
		return mcpJSONDocument{}, unparseable
	}
	document.servers = servers
	document.serversPresent = true
	return document, nil
}

// inspect classifies the `belay` entry without guessing. Absent file or absent
// entry is absent; an entry carrying exactly `command` and `args` is compared
// by exact identity; every other shape is unverifiable and is never rewritten.
func (registry mcpJSONRegistry) inspect(path string) mcpInspection {
	document, failure := registry.load(path)
	if failure != nil {
		return *failure
	}
	raw, present := document.servers[mcpJSONEntryName]
	if !present {
		return mcpInspection{kind: mcpInspectionAbsent}
	}
	identity, ok := parseMCPJSONEntry(raw)
	if !ok {
		return mcpInspection{
			kind:      mcpInspectionUnverifiable,
			errorCode: "status_unparseable",
		}
	}
	return mcpInspection{
		kind: mcpInspectionPresent,
		entry: mcpEntry{
			scope:    "user",
			identity: identity,
		},
	}
}

// parseMCPJSONEntry decodes a stdio launch entry Belay could have written. Any
// unknown member, any non-object shape, and any remote or environment member
// fails closed: the entry cannot be proven to be Belay's.
func parseMCPJSONEntry(raw json.RawMessage) (MCPIdentity, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var entry struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := decoder.Decode(&entry); err != nil {
		return MCPIdentity{}, false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return MCPIdentity{}, false
	}
	if entry.Command == "" || hasUnsafePathText(entry.Command) {
		return MCPIdentity{}, false
	}
	for _, argument := range entry.Args {
		if hasUnsafePathText(argument) {
			return MCPIdentity{}, false
		}
	}
	return MCPIdentity{
		Command: entry.Command,
		Args:    append([]string(nil), entry.Args...),
	}, true
}

// add installs the Belay entry. It refuses to overwrite any existing `belay`
// member that is not already the exact identity being written, so a foreign,
// recognized prior, or unverifiable entry is preserved untouched.
func (registry mcpJSONRegistry) add(path string, identity MCPIdentity) mcpCommandResult {
	if !safeMCPJSONIdentity(identity) {
		return mcpCommandResult{exitCode: 1}
	}
	document, failure := registry.load(path)
	if failure != nil {
		return mcpCommandResult{exitCode: 1}
	}
	if raw, present := document.servers[mcpJSONEntryName]; present {
		existing, ok := parseMCPJSONEntry(raw)
		if !ok || !equalMCPIdentity(existing, identity) {
			return mcpCommandResult{exitCode: 1}
		}
		return mcpCommandResult{exitCode: 0}
	}
	entry, err := marshalMCPJSONValue(identity)
	if err != nil {
		return mcpCommandResult{exitCode: 1}
	}
	if document.servers == nil {
		document.servers = make(map[string]json.RawMessage)
	}
	document.servers[mcpJSONEntryName] = entry
	if registry.ensureDirectory {
		if err := registry.ensureParentDirectory(path); err != nil {
			return mcpCommandResult{exitCode: 1}
		}
	}
	if err := registry.write(path, document); err != nil {
		return mcpCommandResult{exitCode: 1}
	}
	return mcpCommandResult{exitCode: 0}
}

// remove deletes the Belay entry only when the on-disk entry is one of the
// allowed identities: the exact current identity or a prior identity the
// ownership manifest recorded. The `mcpServers` key is kept as an empty object
// when the last server is removed.
func (registry mcpJSONRegistry) remove(path string, allowed []MCPIdentity) mcpCommandResult {
	document, failure := registry.load(path)
	if failure != nil {
		return mcpCommandResult{exitCode: 1}
	}
	raw, present := document.servers[mcpJSONEntryName]
	if !present {
		return mcpCommandResult{exitCode: 0}
	}
	identity, ok := parseMCPJSONEntry(raw)
	if !ok {
		return mcpCommandResult{exitCode: 1}
	}
	if !slices.ContainsFunc(allowed, func(candidate MCPIdentity) bool {
		return safeMCPJSONIdentity(candidate) && equalMCPIdentity(candidate, identity)
	}) {
		return mcpCommandResult{exitCode: 1}
	}
	delete(document.servers, mcpJSONEntryName)
	if err := registry.write(path, document); err != nil {
		return mcpCommandResult{exitCode: 1}
	}
	return mcpCommandResult{exitCode: 0}
}

// safeMCPJSONIdentity keeps injection-shaped text out of the registry. The
// command and every argument are written as JSON strings, and only an absolute
// command path free of control characters and NUL is ever written.
func safeMCPJSONIdentity(identity MCPIdentity) bool {
	if len(identity.Args) == 0 ||
		hasUnsafePathText(identity.Command) ||
		!filepath.IsAbs(identity.Command) {
		return false
	}
	for _, argument := range identity.Args {
		if hasUnsafePathText(argument) {
			return false
		}
	}
	return true
}

// write renders and atomically replaces the registry.
//
// Formatting policy: the document is re-rendered as UTF-8 JSON indented with two
// spaces and terminated by one newline. Members of the top-level object and of
// `mcpServers` are emitted in sorted key order; every preserved value keeps its
// own member order, because it is round-tripped as an opaque json.RawMessage.
// HTML escaping is disabled, so no character is re-encoded on the way out.
// Preservation is therefore semantic and byte-exact per value, while whitespace
// and the order of the two rewritten objects are normalized.
func (registry mcpJSONRegistry) write(path string, document mcpJSONDocument) error {
	if document.root == nil {
		document.root = make(map[string]json.RawMessage)
	}
	if document.servers == nil {
		document.servers = make(map[string]json.RawMessage)
	}
	servers, err := marshalMCPJSONValue(document.servers)
	if err != nil {
		return err
	}
	document.root[mcpJSONServersKey] = servers
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document.root); err != nil {
		return errors.New("encode " + registry.label + " MCP configuration")
	}
	return registry.writeFileAtomic(path, buffer.Bytes())
}

func marshalMCPJSONValue(value any) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, errors.New("encode MCP configuration")
	}
	return json.RawMessage(bytes.TrimRight(buffer.Bytes(), "\n")), nil
}

// ensureParentDirectory creates the registry's immediate parent directory with
// mode 0700 when it is absent. The directory above it must already be a real
// directory, and an existing parent must be a real directory as well; a symlink
// or a non-directory at either position is refused rather than followed.
func (registry mcpJSONRegistry) ensureParentDirectory(path string) error {
	directory := filepath.Dir(path)
	if !realDirectory(filepath.Dir(directory)) {
		return errors.New(registry.label + " MCP configuration root must be a real directory")
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return errors.New("create " + registry.label + " MCP configuration directory")
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return errors.New("restrict " + registry.label + " MCP configuration directory")
		}
		info, err = os.Lstat(directory)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New(registry.label + " MCP configuration directory must be a real directory")
	}
	return nil
}

// writeFileAtomic writes body through a temporary file in the registry
// directory and renames it into place. The destination must be a real regular
// file inside a real directory; a symlink at either position is refused.
func (registry mcpJSONRegistry) writeFileAtomic(path string, body []byte) error {
	label := registry.label
	directory := filepath.Dir(path)
	if !realDirectory(directory) {
		return errors.New(label + " MCP configuration directory must be a real directory")
	}
	if existing, err := os.Lstat(path); err == nil {
		if !existing.Mode().IsRegular() || existing.Mode()&os.ModeSymlink != 0 {
			return errors.New(label + " MCP configuration must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect " + label + " MCP configuration")
	}
	temp, err := os.CreateTemp(directory, ".belay-mcp-*.json")
	if err != nil {
		return errors.New("create " + label + " MCP configuration")
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return errors.New("restrict " + label + " MCP configuration")
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return errors.New("write " + label + " MCP configuration")
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return errors.New("sync " + label + " MCP configuration")
	}
	if err := temp.Close(); err != nil {
		return errors.New("close " + label + " MCP configuration")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return errors.New("activate " + label + " MCP configuration")
	}
	return nil
}

// realDirectory reports whether path is a directory itself, not a symlink to
// one and not a regular file.
func realDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
