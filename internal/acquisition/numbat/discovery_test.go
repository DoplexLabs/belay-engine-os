package numbat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverUsesExactArgsAndPreservesUnknownRows(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("NUMBAT_TEST_LOG", logPath)
	client := newFakeClient(t, `
printf '%s\n' "$@" > "$NUMBAT_TEST_LOG"
printf '%s\n' '[
  {"agent":"codex","present":true,"detected":true,"at_rest":"found","hook":"hooks","wired":"yes","setup_hint":"ready","at_rest_scan_incomplete":false},
  {"agent":"Claude Code","present":true,"detected":true,"at_rest":"found","hook":"hooks","wired":"no","setup_hint":"install","at_rest_scan_incomplete":false},
  {"agent":"future-agent","present":true,"detected":false,"at_rest":"unknown","hook":"extension","wired":"no","setup_hint":"future","at_rest_scan_incomplete":true,"future_field":{"enabled":true}}
]'
`)
	inventory, result, err := client.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v; stderr = %q", err, result.Stderr)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(args), "agents\n--format\njson\n"; got != want {
		t.Fatalf("Discover() args = %q, want %q", got, want)
	}
	if len(inventory.Rows) != 3 || len(inventory.UnknownRows) != 1 {
		t.Fatalf("Discover() row counts = %d/%d, want 3/1", len(inventory.Rows), len(inventory.UnknownRows))
	}
	codex, ok := inventory.LaunchTargets[AgentCodex]
	if !ok || !codex.Present || !codex.Detected {
		t.Fatalf("Codex inventory = %+v, present = %v", codex, ok)
	}
	if claude, ok := inventory.LaunchTargets[AgentClaude]; !ok || !claude.Present {
		t.Fatalf("Claude inventory = %+v, present = %v", claude, ok)
	}
	unknown := inventory.UnknownRows[0]
	var future map[string]bool
	if err := json.Unmarshal(unknown.Raw["future_field"], &future); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(future, map[string]bool{"enabled": true}) {
		t.Fatalf("preserved future field = %#v", future)
	}
}

func TestDiscoverRejectsMalformedInventory(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: `printf '%s' '[{"agent":'`},
		{name: "not array", body: `printf '%s' '{"agent":"codex"}'`},
		{name: "missing agent", body: `printf '%s' '[{"present":true}]'`},
		{name: "duplicate launch target", body: `printf '%s' '[{"agent":"codex"},{"agent":"codex"}]'`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newFakeClient(t, test.body+"\n")
			if _, _, err := client.Discover(context.Background()); err == nil {
				t.Fatal("Discover() error = nil, want malformed inventory error")
			}
		})
	}
}
