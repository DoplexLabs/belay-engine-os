package numbat

import (
	"slices"
	"testing"
)

func TestHostEnvironmentNamesStayBounded(t *testing.T) {
	unix := hostEnvironmentNames("darwin")
	if !slices.Equal(unix, hostEnvironmentNames("linux")) {
		t.Fatal("darwin and linux allowlists differ")
	}
	for _, forbidden := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "AWS_SECRET_ACCESS_KEY", "SSH_AUTH_SOCK"} {
		if slices.Contains(unix, forbidden) || slices.Contains(hostEnvironmentNames("windows"), forbidden) {
			t.Fatalf("allowlist forwards %s", forbidden)
		}
	}
	windows := hostEnvironmentNames("windows")
	for _, required := range []string{"USERPROFILE", "APPDATA", "LOCALAPPDATA", "PATHEXT", "SYSTEMROOT", "TEMP", "PATH", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if !slices.Contains(windows, required) {
			t.Fatalf("windows allowlist lacks %s", required)
		}
	}
	if len(windows) <= len(unix) {
		t.Fatal("windows allowlist should extend the unix allowlist")
	}
}
