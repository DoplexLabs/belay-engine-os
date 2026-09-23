package localapp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const antigravityTargetIndex = 3

func TestAntigravityInstallCreatesConfigDirectoryAndRegistry(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	home := t.TempDir()
	path := antigravityFixture(t, home, "")
	if _, statErr := os.Lstat(filepath.Dir(path)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("fixture pre-created ~/.gemini/config: %v", statErr)
	}
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
		antigravityTestDependencies(t, runner, home),
	)
	if err != nil {
		t.Fatal(err)
	}
	antigravity := result.Targets[antigravityTargetIndex]
	if result.OverallStatus != "complete" ||
		antigravity.Agent != "antigravity" ||
		!antigravity.Detected ||
		antigravity.Scope != "user" ||
		antigravity.Status != "installed" ||
		antigravity.Ownership != "current" ||
		!antigravity.Changed ||
		antigravity.ErrorCode != nil {
		t.Fatalf("install result = %+v", result)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("file target ran host CLI commands: %+v", runner.calls)
	}
	if path != filepath.Join(home, ".gemini", "config", "mcp_config.json") {
		t.Fatalf("registry path = %q", path)
	}
	body := readFile(t, path)
	want := "{\n  \"mcpServers\": {\n    \"belay\": {\n      \"command\": " +
		mustJSON(t, identity.Command) + ",\n      \"args\": [\n        \"mcp\",\n" +
		"        \"--home\",\n        " + mustJSON(t, paths.Root) + "\n      ]\n" +
		"    }\n  }\n}\n"
	if body != want {
		t.Fatalf("registry =\n%s\nwant\n%s", body, want)
	}
	directory, err := os.Lstat(filepath.Dir(path))
	if err != nil || !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("config directory = %v err=%v", directory, err)
	}
	if runtime.GOOS != "windows" {
		if directory.Mode().Perm() != 0o700 {
			t.Fatalf("config directory mode = %o", directory.Mode().Perm())
		}
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
	recorded := manifest.Targets["antigravity"]
	if recorded.Scope != "user" ||
		recorded.Command != identity.Command ||
		!slices.Equal(recorded.Args, identity.Args) {
		t.Fatalf("manifest target = %+v", recorded)
	}
}

func TestAntigravityInstallPreservesForeignServersAndUnknownKeys(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	home := t.TempDir()
	original := `{
  "mcpServers": {
    "other": {"command": "/usr/bin/other", "args": ["--flag"], "env": {"A": "<b>"}, "disabled": false},
    "remote": {"serverUrl": "https://example.test/mcp", "headers": {"X": "y"}, "disabledTools": ["a"]}
  },
  "unknownTopLevel": {"z": 1, "a": [true, null, 2.5]},
  "version": 3
}
`
	path := antigravityFixture(t, home, original)
	result, err := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigInstall,
		},
		antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
	)
	if err != nil || result.Targets[antigravityTargetIndex].Status != "installed" {
		t.Fatalf("install result = %+v err=%v", result, err)
	}
	after := readFile(t, path)
	before := decodeJSON(t, original)
	afterDocument := decodeJSON(t, after)
	beforeServers := before["mcpServers"].(map[string]any)
	afterServers, ok := afterDocument["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers = %#v", afterDocument["mcpServers"])
	}
	for name := range beforeServers {
		if !reflect.DeepEqual(beforeServers[name], afterServers[name]) {
			t.Fatalf("server %q changed: %#v -> %#v", name, beforeServers[name], afterServers[name])
		}
	}
	for _, key := range []string{"unknownTopLevel", "version"} {
		if !reflect.DeepEqual(before[key], afterDocument[key]) {
			t.Fatalf("top-level %q changed: %#v -> %#v", key, before[key], afterDocument[key])
		}
	}
	if _, present := afterServers["belay"]; !present {
		t.Fatalf("belay entry missing: %#v", afterServers)
	}
	// Preserved values are re-emitted as written (only whitespace is
	// normalized): the HTML-escapable "<b>" and the number literals survive.
	for _, fragment := range []string{`"A": "<b>"`, `2.5`, `"serverUrl": "https://example.test/mcp"`} {
		if !strings.Contains(after, fragment) {
			t.Fatalf("registry lost verbatim value %s:\n%s", fragment, after)
		}
	}
}

func TestAntigravityInstallOfExactCurrentEntryLeavesBytesUnchanged(t *testing.T) {
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
	path := antigravityFixture(t, home, cursorRegistry(t, map[string]MCPIdentity{"belay": identity}))
	before := readFile(t, path)
	for round := 0; round < 2; round++ {
		result, err := manageMCPConfig(
			context.Background(),
			MCPConfigRequest{
				Paths:          paths,
				InstallationID: "inst_abcdefgh",
				Executable:     belay,
				Action:         MCPConfigInstall,
			},
			antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
		)
		target := result.Targets[antigravityTargetIndex]
		if err != nil ||
			target.Status != "already_installed" ||
			target.Ownership != "current" ||
			target.Changed {
			t.Fatalf("round %d install result = %+v err=%v", round, result, err)
		}
		if got := readFile(t, path); got != before {
			t.Fatalf("round %d rewrote the registry:\n%s", round, got)
		}
	}
}

func TestAntigravityStatusReportsAbsentRegistryShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		// createConfigDirectory controls whether ~/.gemini/config exists at all.
		createConfigDirectory bool
	}{
		{name: "missing_config_directory"},
		{name: "missing_file", createConfigDirectory: true},
		{name: "empty_object", body: "{}\n", createConfigDirectory: true},
		{name: "no_servers_key", body: `{"other": true}` + "\n", createConfigDirectory: true},
		{name: "empty_servers", body: `{"mcpServers": {}}` + "\n", createConfigDirectory: true},
		{
			name:                  "foreign_servers_only",
			body:                  `{"mcpServers": {"other": {"command": "/usr/bin/other", "args": []}}}` + "\n",
			createConfigDirectory: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths, err := ResolvePaths(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			belay := writeMCPExecutable(t, "belay")
			home := t.TempDir()
			path := antigravityFixture(t, home, "")
			if test.createConfigDirectory {
				if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if test.body != "" {
				if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, action := range []MCPConfigAction{MCPConfigStatus, MCPConfigUninstall} {
				result, _ := manageMCPConfig(
					context.Background(),
					MCPConfigRequest{
						Paths:          paths,
						InstallationID: "inst_abcdefgh",
						Executable:     belay,
						Action:         action,
					},
					antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
				)
				target := result.Targets[antigravityTargetIndex]
				if !target.Detected ||
					target.Status != "absent" ||
					target.Ownership != "none" ||
					target.Changed ||
					target.ErrorCode != nil {
					t.Fatalf("%s result = %+v", action, target)
				}
			}
			if test.body != "" {
				if got := readFile(t, path); got != test.body {
					t.Fatalf("status rewrote the registry:\n%s", got)
				}
			} else if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("status or uninstall created the registry: %v", statErr)
			}
			if !test.createConfigDirectory {
				if _, statErr := os.Lstat(filepath.Dir(path)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("status or uninstall created ~/.gemini/config: %v", statErr)
				}
			}
		})
	}
}

func TestAntigravityRefusesForeignUnverifiableAndUnreadableRegistries(t *testing.T) {
	foreign := cursorRegistry(t, map[string]MCPIdentity{
		"belay": {Command: "/foreign/belay", Args: []string{"mcp"}},
	})
	oversized := `{"mcpServers": {"belay": {"command": "/bin/belay", "args": ["mcp"]}}, "pad": "` +
		strings.Repeat("x", mcpJSONConfigMaxBytes) + `"}` + "\n"
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
			name:      "remote_server_url_entry",
			body:      `{"mcpServers":{"belay":{"serverUrl":"https://example.test/mcp"}}}` + "\n",
			status:    "unverifiable",
			install:   "unverifiable",
			uninstall: "unverifiable",
			errorCode: "status_unparseable",
		},
		{
			name: "env_member",
			body: `{"mcpServers":{"belay":{"command":"/bin/belay","args":["mcp"],` +
				`"env":{"TOKEN":"secret"}}}}` + "\n",
			status:    "unverifiable",
			install:   "unverifiable",
			uninstall: "unverifiable",
			errorCode: "status_unparseable",
		},
		{
			name: "unknown_member",
			body: `{"mcpServers":{"belay":{"command":"/bin/belay","args":["mcp"],` +
				`"disabled":true}}}` + "\n",
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
			name:      "trailing_data",
			body:      `{"mcpServers":{"belay":{"command":"/bin/belay","args":["mcp"]}}} {}` + "\n",
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
			name:      "oversized",
			body:      oversized,
			status:    "unverifiable",
			install:   "unverifiable",
			uninstall: "unverifiable",
			errorCode: "status_output_too_large",
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
					path := antigravityFixture(t, home, "")
					if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
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
						antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
					)
					if err == nil {
						t.Fatalf("%s succeeded: %+v", action, result)
					}
					antigravity := result.Targets[antigravityTargetIndex]
					if antigravity.Status != want || antigravity.Changed {
						t.Fatalf("%s result = %+v", action, antigravity)
					}
					if action != MCPConfigStatus &&
						(antigravity.ErrorCode == nil || *antigravity.ErrorCode != test.errorCode) {
						t.Fatalf("%s error code = %+v, want %q", action, antigravity, test.errorCode)
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

func TestAntigravityUninstallRemovesOwnedEntryAndKeepsEverythingElse(t *testing.T) {
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
	otherValue := `{"command": "/usr/bin/other", "args": ["serve"], "cwd": "/srv", "env": {"K": "<v>"}}`
	remoteValue := `{"serverUrl": "https://example.test/mcp", "headers": {"Authorization": "Bearer x"}}`
	unknownValue := `{"nested": [1, "two", {"three": null}]}`
	original := `{
  "unknownTopLevel": ` + unknownValue + `,
  "mcpServers": {
    "zeta": ` + otherValue + `,
    "belay": {"command": ` + mustJSON(t, identity.Command) + `, "args": ` +
		mustJSONValue(t, identity.Args) + `},
    "alpha": ` + remoteValue + `
  }
}
`
	path := antigravityFixture(t, home, original)
	if err := writeMCPManifest(paths.MCPManifest, mcpOwnershipManifest{
		Version:        mcpManifestVersion,
		InstallationID: "inst_abcdefgh",
		Targets: map[string]mcpManifestTarget{
			"antigravity": {
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
		antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
	)
	if err == nil {
		t.Fatal("uninstall completed with undetected CLI targets")
	}
	antigravity := result.Targets[antigravityTargetIndex]
	if antigravity.Status != "removed" ||
		antigravity.Ownership != "none" ||
		!antigravity.Changed ||
		antigravity.ErrorCode != nil {
		t.Fatalf("uninstall result = %+v", antigravity)
	}
	after := readFile(t, path)
	before := decodeJSON(t, original)
	afterDocument := decodeJSON(t, after)
	delete(before["mcpServers"].(map[string]any), "belay")
	if !reflect.DeepEqual(before, afterDocument) {
		t.Fatalf("registry =\n%s\nwant the original without belay:\n%s", after, original)
	}
	// Whitespace is normalized on the way out, but values are re-emitted as
	// written: no HTML escaping, sorted member order for the rewritten objects.
	for _, fragment := range []string{
		`"K": "<v>"`,
		`"Authorization": "Bearer x"`,
		"\"mcpServers\": {\n    \"alpha\": {",
		"\n    \"zeta\": {",
		"\n  \"unknownTopLevel\": {",
	} {
		if !strings.Contains(after, fragment) {
			t.Fatalf("registry missing %q:\n%s", fragment, after)
		}
	}
	if strings.Contains(after, "belay") {
		t.Fatalf("registry still mentions belay:\n%s", after)
	}
	manifest, state := loadMCPManifest(paths.MCPManifest, "inst_abcdefgh")
	if state != manifestValid {
		t.Fatalf("manifest state = %v", state)
	}
	if _, present := manifest.Targets["antigravity"]; present {
		t.Fatalf("manifest still records antigravity: %+v", manifest)
	}
}

func TestAntigravityUninstallKeepsEmptyServersObject(t *testing.T) {
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
	path := antigravityFixture(t, home, cursorRegistry(t, map[string]MCPIdentity{"belay": identity}))
	result, _ := manageMCPConfig(
		context.Background(),
		MCPConfigRequest{
			Paths:          paths,
			InstallationID: "inst_abcdefgh",
			Executable:     belay,
			Action:         MCPConfigUninstall,
		},
		antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
	)
	if result.Targets[antigravityTargetIndex].Status != "removed" {
		t.Fatalf("uninstall result = %+v", result.Targets[antigravityTargetIndex])
	}
	if got := readFile(t, path); got != "{\n  \"mcpServers\": {}\n}\n" {
		t.Fatalf("registry = %q", got)
	}
}

func TestAntigravityInstallUpdatesRecognizedPriorIdentityViaManifest(t *testing.T) {
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
	path := antigravityFixture(t, home, cursorRegistry(t, map[string]MCPIdentity{
		"belay": prior,
		"other": {Command: "/usr/bin/other", Args: []string{"serve"}},
	}))
	if err := writeMCPManifest(paths.MCPManifest, mcpOwnershipManifest{
		Version:        mcpManifestVersion,
		InstallationID: "inst_abcdefgh",
		Targets: map[string]mcpManifestTarget{
			"antigravity": {
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
		antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
	)
	antigravity := result.Targets[antigravityTargetIndex]
	if err != nil ||
		antigravity.Status != "updated" ||
		antigravity.Ownership != "current" ||
		!antigravity.Changed {
		t.Fatalf("install result = %+v err=%v", antigravity, err)
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
	if state != manifestValid || manifest.Targets["antigravity"].Command != current.Command {
		t.Fatalf("manifest = %+v state=%v", manifest, state)
	}
}

func TestAntigravityDetectionRequiresRealAppDataDirectory(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, home string)
	}{
		{
			name:    "absent_gemini_directory",
			prepare: func(*testing.T, string) {},
		},
		{
			name: "gemini_without_antigravity",
			prepare: func(t *testing.T, home string) {
				if err := os.MkdirAll(filepath.Join(home, ".gemini", "config"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "antigravity_regular_file",
			prepare: func(t *testing.T, home string) {
				if err := os.Mkdir(filepath.Join(home, ".gemini"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, ".gemini", "antigravity"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "antigravity_symlinked_directory",
			prepare: func(t *testing.T, home string) {
				if err := os.Mkdir(filepath.Join(home, ".gemini"), 0o700); err != nil {
					t.Fatal(err)
				}
				target := t.TempDir()
				if err := os.Symlink(target, filepath.Join(home, ".gemini", "antigravity")); err != nil {
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
			if path, detected := locateAntigravityConfig(home); detected || path != "" {
				t.Fatalf("locate = %q, %v", path, detected)
			}
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
			target := install.Targets[antigravityTargetIndex]
			if target.Agent != "antigravity" ||
				target.Detected ||
				target.Status != "skipped_not_detected" ||
				target.ErrorCode != nil {
				t.Fatalf("install target = %+v", target)
			}
			status, err := manageMCPConfig(
				context.Background(),
				MCPConfigRequest{Paths: paths, Executable: belay, Action: MCPConfigStatus},
				dependencies,
			)
			if err == nil {
				t.Fatalf("status succeeded without Antigravity: %+v", status)
			}
			target = status.Targets[antigravityTargetIndex]
			if target.Detected ||
				target.Status != "unavailable" ||
				target.Ownership != "unknown" ||
				target.ErrorCode == nil ||
				*target.ErrorCode != "cli_not_found" {
				t.Fatalf("status target = %+v", target)
			}
			registry := filepath.Join(home, ".gemini", "config", "mcp_config.json")
			if _, statErr := os.Lstat(registry); statErr == nil {
				t.Fatal("undetected Antigravity target created a registry")
			}
		})
	}
	t.Run("real_directory", func(t *testing.T) {
		home := fakeHomeDirectory(t)
		if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity"), 0o700); err != nil {
			t.Fatal(err)
		}
		path, detected := locateAntigravityConfig(home)
		if !detected || path != filepath.Join(home, ".gemini", "config", "mcp_config.json") {
			t.Fatalf("locate = %q, %v", path, detected)
		}
	})
}

func TestAntigravityRefusesSymlinkedGeminiOrConfigDirectory(t *testing.T) {
	t.Run("symlinked_config_directory", func(t *testing.T) {
		paths, err := ResolvePaths(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		belay := writeMCPExecutable(t, "belay")
		home := t.TempDir()
		path := antigravityFixture(t, home, "")
		elsewhere := t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Dir(path)); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		for _, action := range []MCPConfigAction{MCPConfigInstall, MCPConfigStatus, MCPConfigUninstall} {
			result, err := manageMCPConfig(
				context.Background(),
				MCPConfigRequest{
					Paths:          paths,
					InstallationID: "inst_abcdefgh",
					Executable:     belay,
					Action:         action,
				},
				antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
			)
			target := result.Targets[antigravityTargetIndex]
			if err == nil ||
				!target.Detected ||
				target.Status != "unavailable" ||
				target.Changed ||
				target.ErrorCode == nil ||
				*target.ErrorCode != "status_failed" {
				t.Fatalf("%s result = %+v err=%v", action, target, err)
			}
		}
		entries, err := os.ReadDir(elsewhere)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("symlink target received files: %v", entries)
		}
	})
	t.Run("symlinked_gemini_directory", func(t *testing.T) {
		paths, err := ResolvePaths(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		belay := writeMCPExecutable(t, "belay")
		home := t.TempDir()
		elsewhere := t.TempDir()
		if err := os.Mkdir(filepath.Join(elsewhere, "antigravity"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(home, ".gemini")); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		result, err := manageMCPConfig(
			context.Background(),
			MCPConfigRequest{
				Paths:          paths,
				InstallationID: "inst_abcdefgh",
				Executable:     belay,
				Action:         MCPConfigInstall,
			},
			antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
		)
		target := result.Targets[antigravityTargetIndex]
		if err == nil ||
			target.Status != "failed" ||
			target.Changed ||
			target.ErrorCode == nil ||
			*target.ErrorCode != "install_failed" {
			t.Fatalf("install result = %+v err=%v", target, err)
		}
		if _, statErr := os.Lstat(filepath.Join(elsewhere, "config")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("install created config through the symlinked ~/.gemini: %v", statErr)
		}
	})
	t.Run("config_regular_file", func(t *testing.T) {
		paths, err := ResolvePaths(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		belay := writeMCPExecutable(t, "belay")
		home := t.TempDir()
		path := antigravityFixture(t, home, "")
		if err := os.WriteFile(filepath.Dir(path), []byte("not a directory"), 0o600); err != nil {
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
			antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
		)
		target := result.Targets[antigravityTargetIndex]
		if err == nil || target.Status != "unavailable" || target.Changed {
			t.Fatalf("install result = %+v err=%v", target, err)
		}
		if got := readFile(t, filepath.Dir(path)); got != "not a directory" {
			t.Fatalf("config file rewritten: %q", got)
		}
	})
}

func TestAntigravityNeverTouchesLegacyOrWorkspaceRegistries(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	belay := writeMCPExecutable(t, "belay")
	home := t.TempDir()
	path := antigravityFixture(t, home, "")
	legacy := filepath.Join(home, ".gemini", "antigravity", "mcp_config.json")
	workspace := filepath.Join(home, ".agents", "mcp_config.json")
	legacyBody := `{"mcpServers": {"belay": {"command": "/legacy/belay", "args": ["mcp"]}}}` + "\n"
	workspaceBody := `{"mcpServers": {"belay": {"command": "/workspace/belay", "args": ["mcp"]}}}` + "\n"
	if err := os.WriteFile(legacy, []byte(legacyBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace, []byte(workspaceBody), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := ResolveBelayMCPIdentity(belay, paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	expectations := map[MCPConfigAction]string{
		MCPConfigStatus:    "absent",
		MCPConfigInstall:   "installed",
		MCPConfigUninstall: "removed",
	}
	for _, action := range []MCPConfigAction{MCPConfigStatus, MCPConfigInstall, MCPConfigUninstall} {
		result, _ := manageMCPConfig(
			context.Background(),
			MCPConfigRequest{
				Paths:          paths,
				InstallationID: "inst_abcdefgh",
				Executable:     belay,
				Action:         action,
			},
			antigravityTestDependencies(t, &scriptedMCPRunner{t: t}, home),
		)
		target := result.Targets[antigravityTargetIndex]
		if target.Status != expectations[action] {
			t.Fatalf("%s result = %+v", action, target)
		}
		// The legacy and workspace files hold a foreign `belay` entry; had Belay
		// read either one, status would have been foreign instead of absent.
		if got := readFile(t, legacy); got != legacyBody {
			t.Fatalf("%s rewrote the legacy registry:\n%s", action, got)
		}
		if got := readFile(t, workspace); got != workspaceBody {
			t.Fatalf("%s rewrote the workspace registry:\n%s", action, got)
		}
		if action == MCPConfigInstall {
			document := decodeJSON(t, readFile(t, path))
			entry := document["mcpServers"].(map[string]any)["belay"].(map[string]any)
			if entry["command"] != identity.Command {
				t.Fatalf("global registry entry = %#v", entry)
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(home, ".gemini", "antigravity"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "mcp_config.json" {
		t.Fatalf("app-data directory changed: %v", entries)
	}
}

func TestAntigravityWriterRefusesUnsafeIdentityAndUnallowedRemoval(t *testing.T) {
	home := t.TempDir()
	safe := MCPIdentity{Command: filepath.Join(home, "bin", "belay"), Args: []string{"mcp"}}
	path := antigravityFixture(t, home, cursorRegistry(t, map[string]MCPIdentity{"belay": safe}))
	unchanged := readFile(t, path)
	for _, identity := range []MCPIdentity{
		{Command: "belay", Args: []string{"mcp"}},
		{Command: "/bin/be\nlay", Args: []string{"mcp"}},
		{Command: "/bin/belay", Args: []string{"mcp\u0000"}},
		{Command: "/bin/belay"},
	} {
		if result := addAntigravityEntry(path, identity); result.exitCode == 0 {
			t.Fatalf("unsafe identity %+v was written", identity)
		}
	}
	if result := addAntigravityEntry(
		path,
		MCPIdentity{Command: "/other/belay", Args: []string{"mcp"}},
	); result.exitCode == 0 {
		t.Fatal("existing entry was overwritten")
	}
	if result := removeAntigravityEntry(
		path,
		[]MCPIdentity{{Command: "/other/belay", Args: []string{"mcp"}}},
	); result.exitCode == 0 {
		t.Fatal("unallowed entry was removed")
	}
	if got := readFile(t, path); got != unchanged {
		t.Fatalf("registry changed:\n%s", got)
	}
	if result := removeAntigravityEntry(path, []MCPIdentity{safe}); result.exitCode != 0 {
		t.Fatalf("owned removal failed: %+v", result)
	}
	if got := readFile(t, path); got != "{\n  \"mcpServers\": {}\n}\n" {
		t.Fatalf("registry = %q", got)
	}
	if inspection := inspectAntigravityConfig(path); inspection.kind != mcpInspectionAbsent {
		t.Fatalf("inspection = %+v", inspection)
	}
}

func antigravityTestDependencies(
	t *testing.T,
	runner *scriptedMCPRunner,
	home string,
) mcpConfigDependencies {
	t.Helper()
	dependencies := mcpTestDependencies(t, runner, "", "")
	dependencies.home = func() (string, error) { return home, nil }
	return dependencies
}

// antigravityFixture creates the ~/.gemini/antigravity app-data directory that
// marks an Antigravity 2.0 install and, when body is not empty, the global
// registry under ~/.gemini/config. With an empty body, ~/.gemini/config is left
// absent so tests can prove Belay creates it only on install.
func antigravityFixture(t *testing.T, home, body string) string {
	t.Helper()
	gemini := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(filepath.Join(gemini, "antigravity"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(gemini, "config", "mcp_config.json")
	if body != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func mustJSONValue(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
