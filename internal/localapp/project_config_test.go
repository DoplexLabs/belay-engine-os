package localapp

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadProjectConfigDiscoversVerificationCommands(t *testing.T) {
	root := t.TempDir()
	writeProjectConfigTestFile(t, filepath.Join(root, "CLAUDE.md"), "# instructions\n")
	writeProjectConfigTestFile(t, filepath.Join(root, "AGENTS.md"), "# instructions\n")
	writeProjectConfigTestFile(t, filepath.Join(root, "package.json"), `{
		"packageManager": "pnpm@9.12.0",
		"scripts": {
			"check": "tsc --noEmit && eslint .",
			"start": "node server.js",
			"test": "vitest run"
		}
	}`)
	writeProjectConfigTestFile(t, filepath.Join(root, "Makefile"), `
verify:
	go test ./...
serve:
	go run .
`)
	writeProjectConfigTestFile(t, filepath.Join(root, "pyproject.toml"), `
[tool.pytest.ini_options]
addopts = "-q"
[tool.ruff]
line-length = 100
[tool.poe.tasks]
typecheck = "mypy ."
serve = "python app.py"
`)
	config := loadProjectConfig(root)
	if !config.HasClaudeInstructions || !config.HasCodexInstructions {
		t.Fatalf("instruction files = %+v", config)
	}
	for _, command := range []string{
		"pnpm run check",
		"pnpm run test",
		"make verify",
		"pytest",
		"ruff check",
		"poe typecheck",
	} {
		if !slices.Contains(config.VerificationCommands, command) {
			t.Fatalf("missing %q in %v", command, config.VerificationCommands)
		}
	}
	for _, command := range []string{
		"npm run check",
		"yarn check",
		"bun run check",
		"pnpm run start",
		"make serve",
		"poe serve",
	} {
		if slices.Contains(config.VerificationCommands, command) {
			t.Fatalf("non-verification command retained: %q", command)
		}
	}
}

func TestLoadProjectConfigRejectsSymlinksAndOversizedFiles(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "CLAUDE.md")
	writeProjectConfigTestFile(t, outside, "private\n")
	if err := os.Symlink(outside, filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	oversized := make([]byte, maxProjectConfigBytes+1)
	if err := os.WriteFile(filepath.Join(root, "package.json"), oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	config := loadProjectConfig(root)
	if config.HasClaudeInstructions || len(config.VerificationCommands) != 0 {
		t.Fatalf("unsafe project config was loaded: %+v", config)
	}
}

func writeProjectConfigTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
