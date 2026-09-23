package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLocalDatabaseDefaultsToHomeDatabase(t *testing.T) {
	home := filepath.Join(t.TempDir(), "belay home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(home, "belay.sqlite")

	_, err := resolveLocalDatabase("", home)
	if err == nil || !strings.Contains(err.Error(), database) ||
		!strings.Contains(err.Error(), "belay quickstart") {
		t.Fatalf("missing-database error = %v, want guidance naming %s", err, database)
	}
	if _, statErr := os.Stat(database); !os.IsNotExist(statErr) {
		t.Fatalf("resolveLocalDatabase created %s", database)
	}

	if err := os.WriteFile(database, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveLocalDatabase("", home)
	if err != nil || resolved != database {
		t.Fatalf("resolveLocalDatabase() = %q, %v; want %q", resolved, err, database)
	}

	explicit := filepath.Join(t.TempDir(), "other.sqlite")
	resolved, err = resolveLocalDatabase(explicit, home)
	if err != nil || resolved != explicit {
		t.Fatalf("resolveLocalDatabase(explicit) = %q, %v; want %q", resolved, err, explicit)
	}
}

func TestLocalReadCommandsNoLongerRequireDB(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"sessions", "--home", home},
		{"timeline", "--home", home, "--session", "session-1"},
		{"prune", "--home", home},
	} {
		var stdout, stderr bytes.Buffer
		err := run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		if err == nil || strings.Contains(err.Error(), "--db is required") ||
			!strings.Contains(err.Error(), "no Belay Local database at") {
			t.Fatalf("run(%q) error = %v, want default-database guidance", args, err)
		}
	}
}

func TestSubcommandGroupsPrintUsage(t *testing.T) {
	for _, command := range []string{"hooks", "mcp-config"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), []string{command}, strings.NewReader(""), &stdout, &stderr)
			if err == nil {
				t.Fatalf("belay %s without an action succeeded", command)
			}
			if !strings.Contains(stderr.String(), "belay "+command+" status") {
				t.Fatalf("stderr = %q, want an example action", stderr.String())
			}

			stdout.Reset()
			stderr.Reset()
			err = run(context.Background(), []string{command, "--help"}, strings.NewReader(""), &stdout, &stderr)
			if err != nil {
				t.Fatalf("belay %s --help error = %v", command, err)
			}
			if !strings.Contains(stdout.String(), "Actions:") {
				t.Fatalf("stdout = %q, want usage", stdout.String())
			}
		})
	}
}
