package numbatmap

import (
	"strings"
	"testing"
)

func TestScrubSecretsUsesCanonicalPatternsWithoutMinimization(t *testing.T) {
	input := strings.Join([]string{
		"prefix",
		"token=private-token-value",
		"AKIAABCDEFGHIJKLMNOP",
		"suffix",
	}, " ")
	got, removed := ScrubSecrets(input)
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if strings.Contains(got, "private-token-value") ||
		strings.Contains(got, "AKIAABCDEFGHIJKLMNOP") {
		t.Fatalf("secret remained after scrub: %q", got)
	}
	if !strings.Contains(got, "prefix") || !strings.Contains(got, "suffix") {
		t.Fatalf("non-secret text was minimized: %q", got)
	}
}
