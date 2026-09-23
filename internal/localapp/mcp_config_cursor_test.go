package localapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCursorInstallCreatesRegistryWithOnlyTheBelayEntry(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	home := t.TempDir()
	path := cursorFixture(t, home, "")
	identity, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedMCPRunner{t: t}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigInstall,
		},
		cursorTestDependencies(t, runner, home),
	)
	if err != nil {
		t.Fatal(err)
	}
	cursor := result.Targets[2]
	if result.OverallStatus != "complete" ||
		cursor.Agent != "cursor" ||
		!cursor.Detected ||
		cursor.Scope != "user" ||
		cursor.Status != "installed" ||
		cursor.Ownership != "current" ||
		!cursor.Changed ||
		cursor.ErrorCode != nil {
		t.Fatalf("install result = %+v", result)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("file target ran host CLI commands: %+v", runner.calls)
	}
	body := readFile(t, path)
	want := "{\n  \"mcpServers\": {\n    \"belay\": {\n      \"command\": " +
		mustJSON(t, identity.Command) + ",\n      \"args\": [\n        \"mcp\",\n" +
		"        \"--home\",\n        " + mustJSON(t, paths.Root) + "\n      ]\n" +
		"    }\n  }\n}\n"
	if body != want {
		t.Fatalf("registry =\n%s\nwant\n%s", body, want)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("registry mode = %o", info.Mode().Perm())
		}
	}
	manifest, state := loadMCPManifest(paths.MCPManifest, "inst_abcdefgh")
	if state != manifestValid {
		t.Fatalf("manifest state = %v", state)
	}
	recorded := manifest.Targets["cursor"]
	if recorded.Scope != "user" ||
		recorded.Command != identity.Command ||
		!slices.Equal(recorded.Args, identity.Args) {
		t.Fatalf("manifest target = %+v", recorded)
	}
}

func TestCursorInstallPreservesForeignServersAndUnknownKeys(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	home := t.TempDir()
	original := `{
  "mcpServers": {
    "other": {"command": "/usr/bin/other", "args": ["--flag"], "env": {"A": "<b>"}},
    "remote": {"url": "https://example.test/mcp", "headers": {"X": "y"}}
  },
  "unknownTopLevel": {"z": 1, "a": [true, null, 2.5]},
  "version": 3
}
`
	path := cursorFixture(t, home, original)
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigInstall,
		},
		cursorTestDependencies(t, &scriptedMCPRunner{t: t}, home),
	)
	if err != nil || result.Targets[2].Status != "installed" {
		t.Fatalf("install result = %+v err=%v", result, err)
	}
	before := decodeJSON(t, original)
	after := decodeJSON(t, readFile(t, path))
	beforeServers := before["mcpServers"].(map[string]any)
	afterServers, ok := after["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers = %#v", after["mcpServers"])
	}
	for _, name := range []string{"other", "remote"} {
		if !reflect.DeepEqual(beforeServers[name], afterServers[name]) {
			t.Fatalf("server %q changed: %#v -> %#v", name, beforeServers[name], afterServers[name])
		}
	}
	if _, present := afterServers["belay"]; !present || len(afterServers) != 3 {
		t.Fatalf("servers after install = %#v", afterServers)
	}
	for _, key := range []string{"unknownTopLevel", "version"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Fatalf("top-level %q changed: %#v -> %#v", key, before[key], after[key])
		}
	}
	body := readFile(t, path)
	escaped := "\\u003c"
	if !strings.Contains(body, `"<b>"`) || strings.Contains(body, escaped) {
		t.Fatalf("preserved value was re-escaped:\n%s", body)
	}
	if strings.Index(body, `"z"`) > strings.Index(body, `"a"`) {
		t.Fatalf("preserved member order changed:\n%s", body)
	}
}

func TestCursorInstallOfExactCurrentEntryLeavesBytesUnchanged(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	home := t.TempDir()
	identity, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	path := cursorFixture(t, home, cursorRegistry(t, map[string]MCPIdentity{"belay": identity}))
	before := readFile(t, path)
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigInstall,
		},
		cursorTestDependencies(t, &scriptedMCPRunner{t: t}, home),
	)
	if err != nil ||
		result.Targets[2].Status != "already_installed" ||
		result.Targets[2].Ownership != "current" ||
		result.Targets[2].Changed {
		t.Fatalf("install result = %+v err=%v", result, err)
	}
	if got := readFile(t, path); got != before {
		t.Fatalf("registry rewritten:\n%s", got)
	}
}

func TestCursorRefusesForeignUnverifiableAndUnreadableRegistries(t *testing.T) {
	foreign := cursorRegistry(t, map[string]MCPIdentity{
		"belay": {Command: "/foreign/belay", Args: []string{"mcp"}},
	})
	tests := []struct {
		name      string
		body      string
		symlink   bool
		status    string
		install   string
		uninstall string
		errorCode string
	}{
		{
			name:      "foreign",
			body:      foreign,
			status:    "foreign",
			install:   "foreign_preserved",
			uninstall: "foreign_preserved",
			errorCode: "conflicting_entry",
		},
		{
			name:      "remote_url_entry",
			body:      `{"mcpServers":{"belay":{"url":"https://example.test/mcp"}}}` + "\n",
			status:    "unverifiable",
			install:   "unverifiable",
			uninstall: "unverifiable",
			errorCode: "status_unparseable",
		},
		{
			name: "unknown_entry_member",
			body: `{"mcpServers":{"belay":{"command":"/bin/belay","args":["mcp"],` +
				`"env":{"TOKEN":"secret"}}}}` + "\n",
			status:    "unverifiable",
			install:   "unverifiable",
			uninstall: "unverifiable",
			errorCode: "status_unparseable",
		},
		{
			name:      "malformed_json",
			body:      "{\"mcpServers\": {\"belay\": }\n",
			status:    "unverifiable",
			install:   "unverifiable",
			uninstall: "unverifiable",
			errorCode: "status_unparseable",
		},
		{
			name:      "non_object_servers",
			body:      `{"mcpServers":["belay"]}` + "\n",
			status:    "unverifiable",
			install:   "unverifiable",
			uninstall: "unverifiable",
			errorCode: "status_unparseable",
		},
		{
			name:      "symlinked_registry",
			body:      foreign,
			symlink:   true,
			status:    "unavailable",
			install:   "unavailable",
			uninstall: "unavailable",
			errorCode: "status_failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actions := map[MCPConfigAction]string{
				MCPConfigStatus:    test.status,
				MCPConfigInstall:   test.install,
				MCPConfigUninstall: test.uninstall,
			}
			for action, want := range actions {
				t.Run(string(action), func(t *testing.T) {
					paths, err := ResolvePaths(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					belay := writeMCPExecutable(t, "belay")
					home := t.TempDir()
					path := cursorFixture(t, home, "")
					if test.symlink {
						target := filepath.Join(t.TempDir(), "elsewhere.json")
						if err := os.WriteFile(target, []byte(test.body), 0o600); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(target, path); err != nil {
							t.Skipf("symlink unsupported: %v", err)
						}
						defer func() {
							if got := readFile(t, target); got != test.body {
								t.Fatalf("symlink target rewritten:\n%s", got)
							}
						}()
					} else {
						if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					result, err := manageMCPConfig(
						context.Background(),
						MCPConfigRequest{
							Paths:          paths,
							InstallationID: "inst_abcdefgh",
							Executable:     belay,
							Action:         action,
						},
						cursorTestDependencies(t, &scriptedMCPRunner{t: t}, home),
					)
					if err == nil {
						t.Fatalf("%s succeeded: %+v", action, result)
					}
					cursor := result.Targets[2]
					if cursor.Status != want || cursor.Changed {
						t.Fatalf("%s result = %+v", action, cursor)
					}
					if action != MCPConfigStatus &&
						(cursor.ErrorCode == nil || *cursor.ErrorCode != test.errorCode) {
						t.Fatalf("%s error code = %+v, want %q", action, cursor, test.errorCode)
					}
					if !test.symlink {
						if got := readFile(t, path); got != test.body {
							t.Fatalf("%s rewrote the registry:\n%s", action, got)
						}
					}
				})
			}
		})
	}
}

func TestCursorUninstallRemovesOwnedEntryAndKeepsOtherServers(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	home := t.TempDir()
	identity, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	path := cursorFixture(t, home, cursorRegistry(t, map[string]MCPIdentity{
		"belay": identity,
		"other": {Command: "/usr/bin/other", Args: []string{"serve"}},
	}))
	if err := writeMCPManifest(paths.MCPManifest, mcpOwnershipManifest{
		Version:        mcpManifestVersion,
		InstallationID: "inst_abcdefgh",
		Targets: map[string]mcpManifestTarget{
			"cursor": {
				Scope:      "user",
				Command:    identity.Command,
				Args:       identity.Args,
				VerifiedAt: "2026-09-09T20:00:00Z",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigUninstall,
		},
		cursorTestDependencies(t, &scriptedMCPRunner{t: t}, home),
	)
	if err == nil {
		t.Fatal("uninstall completed with undetected CLI targets")
	}
	if result.Targets[2].Status != "removed" ||
		result.Targets[2].Ownership != "none" ||
		!result.Targets[2].Changed {
		t.Fatalf("uninstall result = %+v", result.Targets[2])
	}
	document := decodeJSON(t, readFile(t, path))
	servers := document["mcpServers"].(map[string]any)
	if _, present := servers["belay"]; present {
		t.Fatalf("belay entry remains: %#v", servers)
	}
	other, ok := servers["other"].(map[string]any)
	if !ok || other["command"] != "/usr/bin/other" {
		t.Fatalf("foreign server lost: %#v", servers)
	}
	manifest, state := loadMCPManifest(paths.MCPManifest, "inst_abcdefgh")
	if state != manifestValid {
		t.Fatalf("manifest state = %v", state)
	}
	if _, present := manifest.Targets["cursor"]; present {
		t.Fatalf("manifest still records cursor: %+v", manifest)
	}
}

func TestCursorUninstallKeepsEmptyServersObject(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	home := t.TempDir()
	identity, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	path := cursorFixture(t, home, cursorRegistry(t, map[string]MCPIdentity{"belay": identity}))
	result, _ := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigUninstall,
		},
		cursorTestDependencies(t, &scriptedMCPRunner{t: t}, home),
	)
	if result.Targets[2].Status != "removed" {
		t.Fatalf("uninstall result = %+v", result.Targets[2])
	}
	if got := readFile(t, path); got != "{\n  \"mcpServers\": {}\n}\n" {
		t.Fatalf("registry = %q", got)
	}
}

func TestCursorInstallUpdatesRecognizedPriorIdentityAfterArchiveMove(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	home := t.TempDir()
	current, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	prior := MCPIdentity{
		Command: "/old/archive/bin/belay",
		Args:    append([]string(nil), current.Args...),
	}
	path := cursorFixture(t, home, cursorRegistry(t, map[string]MCPIdentity{
		"belay": prior,
		"other": {Command: "/usr/bin/other", Args: []string{"serve"}},
	}))
	if err := writeMCPManifest(paths.MCPManifest, mcpOwnershipManifest{
		Version:        mcpManifestVersion,
		InstallationID: "inst_abcdefgh",
		Targets: map[string]mcpManifestTarget{
			"cursor": {
				Scope:      "user",
				Command:    prior.Command,
				Args:       prior.Args,
				VerifiedAt: "2026-09-09T20:00:00Z",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigInstall,
		},
		cursorTestDependencies(t, &scriptedMCPRunner{t: t}, home),
	)
	if err != nil ||
		result.Targets[2].Status != "updated" ||
		result.Targets[2].Ownership != "current" ||
		!result.Targets[2].Changed {
		t.Fatalf("install result = %+v err=%v", result.Targets[2], err)
	}
	document := decodeJSON(t, readFile(t, path))
	servers := document["mcpServers"].(map[string]any)
	entry := servers["belay"].(map[string]any)
	if entry["command"] != current.Command {
		t.Fatalf("entry = %#v", entry)
	}
	if _, present := servers["other"]; !present {
		t.Fatalf("foreign server lost: %#v", servers)
	}
	manifest, state := loadMCPManifest(paths.MCPManifest, "inst_abcdefgh")
	if state != manifestValid || manifest.Targets["cursor"].Command != current.Command {
		t.Fatalf("manifest = %+v state=%v", manifest, state)
	}
}

func TestCursorDetectionUsesRealHomeDirectoryOnly(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, home string)
	}{
		{
			name:    "absent_directory",
			prepare: func(*testing.T, string) {},
		},
		{
			name: "regular_file",
			prepare: func(t *testing.T, home string) {
				if err := os.WriteFile(filepath.Join(home, ".cursor"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlinked_directory",
			prepare: func(t *testing.T, home string) {
				target := t.TempDir()
				if err := os.Symlink(target, filepath.Join(home, ".cursor")); err != nil {
					t.Skipf("symlink unsupported: %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths, err := ResolvePaths(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			belay := writeMCPExecutable(t, "belay")
			home := fakeHomeDirectory(t)
			test.prepare(t, home)
			dependencies := mcpTestDependencies(t, &scriptedMCPRunner{t: t}, "", "")
			// Exercise the documented resolution: os.UserHomeDir reads HOME, and
			// USERPROFILE on Windows.
			dependencies.home = os.UserHomeDir
			install, err := manageMCPConfig(
				context.Background(),
				MCPConfigRequest{
					Paths:          paths,
					InstallationID: "inst_abcdefgh",
					Executable:     belay,
					Action:         MCPConfigInstall,
				},
				dependencies,
			)
			if err != nil {
				t.Fatalf("install err = %v result=%+v", err, install)
			}
			if install.Targets[2].Detected ||
				install.Targets[2].Status != "skipped_not_detected" ||
				install.Targets[2].ErrorCode != nil {
				t.Fatalf("install target = %+v", install.Targets[2])
			}
			status, err := manageMCPConfig(
				context.Background(),
				MCPConfigRequest{Paths: paths, Executable: belay, Action: MCPConfigStatus},
				dependencies,
			)
			if err == nil {
				t.Fatalf("status succeeded without Cursor: %+v", status)
			}
			cursor := status.Targets[2]
			if cursor.Detected ||
				cursor.Status != "unavailable" ||
				cursor.Ownership != "unknown" ||
				cursor.ErrorCode == nil ||
				*cursor.ErrorCode != "cli_not_found" {
				t.Fatalf("status target = %+v", cursor)
			}
			if _, statErr := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); statErr == nil {
				t.Fatal("undetected Cursor target created a registry")
			}
		})
	}
}

func TestCursorEntryParserRejectsUnsafeAndUnknownLaunchData(t *testing.T) {
	for _, raw := range []string{
		`"belay"`,
		`[]`,
		`{}`,
		`{"command":""}`,
		`{"command":"/bin/belay","args":[1]}`,
		`{"command":"/bin/belay\u0000","args":["mcp"]}`,
		`{"command":"/bin/belay","args":["mcp\n--home"]}`,
		`{"command":"/bin/belay","args":["mcp"],"type":"stdio"}`,
		`{"command":"/bin/belay","args":["mcp"],"cwd":"/tmp"}`,
	} {
		if _, ok := parseCursorEntry(json.RawMessage(raw)); ok {
			t.Fatalf("entry %s was accepted", raw)
		}
	}
	identity, ok := parseCursorEntry(json.RawMessage(
		`{"command":"/bin/belay","args":["mcp","--home","/private/home"]}`,
	))
	if !ok || identity.Command != "/bin/belay" ||
		!slices.Equal(identity.Args, []string{"mcp", "--home", "/private/home"}) {
		t.Fatalf("identity = %+v ok=%v", identity, ok)
	}
}

func cursorTestDependencies(
	t *testing.T,
	runner *scriptedMCPRunner,
	home string,
) mcpConfigDependencies {
	t.Helper()
	dependencies := mcpTestDependencies(t, runner, "", "")
	dependencies.home = func() (string, error) { return home, nil }
	return dependencies
}

func fakeHomeDirectory(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// cursorFixture creates ~/.cursor and, when body is not empty, the registry.
func cursorFixture(t *testing.T, home, body string) string {
	t.Helper()
	directory := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "mcp.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func cursorRegistry(t *testing.T, servers map[string]MCPIdentity) string {
	t.Helper()
	body, err := json.MarshalIndent(
		map[string]any{"mcpServers": servers},
		"",
		"  ",
	)
	if err != nil {
		t.Fatal(err)
	}
	return string(body) + "\n"
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func decodeJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return document
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestCursorWriterRefusesUnsafeIdentityAndUnallowedRemoval(t *testing.T) {
	home := t.TempDir()
	safe := MCPIdentity{Command: filepath.Join(home, "bin", "belay"), Args: []string{"mcp"}}
	path := cursorFixture(t, home, cursorRegistry(t, map[string]MCPIdentity{"belay": safe}))
	unchanged := readFile(t, path)
	for _, identity := range []MCPIdentity{
		{Command: "belay", Args: []string{"mcp"}},
		{Command: "/bin/be\nlay", Args: []string{"mcp"}},
		{Command: "/bin/belay", Args: []string{"mcp\u0000"}},
		{Command: "/bin/belay"},
	} {
		if result := addCursorEntry(path, identity); result.exitCode == 0 {
			t.Fatalf("unsafe identity %+v was written", identity)
		}
	}
	// A different Belay-shaped identity must never overwrite the stored entry.
	if result := addCursorEntry(
		path,
		MCPIdentity{Command: "/other/belay", Args: []string{"mcp"}},
	); result.exitCode == 0 {
		t.Fatal("existing entry was overwritten")
	}
	if result := removeCursorEntry(
		path,
		[]MCPIdentity{{Command: "/other/belay", Args: []string{"mcp"}}},
	); result.exitCode == 0 {
		t.Fatal("unallowed entry was removed")
	}
	if got := readFile(t, path); got != unchanged {
		t.Fatalf("registry changed:\n%s", got)
	}
	if result := removeCursorEntry(path, []MCPIdentity{safe}); result.exitCode != 0 {
		t.Fatalf("owned removal failed: %+v", result)
	}
	if got := readFile(t, path); got != "{\n  \"mcpServers\": {}\n}\n" {
		t.Fatalf("registry = %q", got)
	}
}
