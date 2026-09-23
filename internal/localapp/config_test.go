package localapp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateConfigIsStableAndPrivate(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := LoadOrCreateConfig(paths)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateConfig(paths)
	if err != nil {
		t.Fatal(err)
	}
	if first.InstallationID != second.InstallationID {
		t.Fatalf("installation ID changed: %q != %q", first.InstallationID, second.InstallationID)
	}
	if !validInstallationID(first.InstallationID) {
		t.Fatalf("invalid generated installation ID %q", first.InstallationID)
	}
	info, err := os.Stat(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}
}

func TestResolveNumbatBinaryPrefersExplicitExecutable(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "numbat")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveNumbatBinary(paths, Config{}, binary)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(binary)
	if got != want {
		t.Fatalf("binary = %q, want %q", got, want)
	}
}

func TestResolveNumbatBinaryUsesProvidedExecutableSibling(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	belay := filepath.Join(bin, "belay")
	numbat := filepath.Join(bin, "numbat")
	if err := os.WriteFile(numbat, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveNumbatBinaryForExecutable(paths, Config{}, "", belay)
	if err != nil {
		t.Fatal(err)
	}
	if got != numbat {
		t.Fatalf("binary = %q, want sibling %q", got, numbat)
	}
}

func TestResolvePathsUsesBelayHome(t *testing.T) {
	root := filepath.Join(t.TempDir(), "custom")
	t.Setenv("BELAY_HOME", root)
	paths, err := ResolvePaths("")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != root {
		t.Fatalf("root = %q, want %q", paths.Root, root)
	}
	spools := map[string]string{
		"codex":       paths.CodexSpool,
		"claude":      paths.ClaudeSpool,
		"cursor":      paths.CursorSpool,
		"antigravity": paths.AntigravitySpool,
	}
	seen := make(map[string]string, len(spools))
	for agent, spool := range spools {
		if want := filepath.Join(root, "live", agent+".ndjson"); spool != want {
			t.Fatalf("%s spool = %q, want %q", agent, spool, want)
		}
		if other, duplicate := seen[spool]; duplicate {
			t.Fatalf("%s and %s share live spool %q", agent, other, spool)
		}
		seen[spool] = agent
	}
	if got, want := paths.TranscriptCursors,
		filepath.Join(root, "transcripts", "cursors"); got != want {
		t.Fatalf("transcript cursor root = %q, want %q", got, want)
	}
}

func TestEnsurePrivateDirectoriesCreatesEveryLiveSpoolDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	paths, err := ResolvePaths(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectories(paths); err != nil {
		t.Fatal(err)
	}
	for _, spool := range []string{
		paths.CodexSpool,
		paths.ClaudeSpool,
		paths.CursorSpool,
		paths.AntigravitySpool,
	} {
		info, err := os.Lstat(filepath.Dir(spool))
		if err != nil {
			t.Fatalf("live spool directory for %q missing: %v", spool, err)
		}
		if !info.IsDir() {
			t.Fatalf("live spool parent for %q is not a directory", spool)
		}
	}
}
