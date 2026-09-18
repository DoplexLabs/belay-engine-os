package model

import "testing"

func TestSafeSourceSignalCode(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"tamper.guardrails_off",
		"a",
		"source-signal_v1",
	} {
		value := value
		t.Run("accept_"+value, func(t *testing.T) {
			t.Parallel()
			got := SafeSourceSignalCode(value)
			if got == nil || *got != value {
				t.Fatalf("SafeSourceSignalCode(%q) = %v", value, got)
			}
		})
	}

	for _, value := range []string{
		"",
		"Tamper.guardrails_off",
		".leading",
		"contains space",
		"tamper/guardrails",
		"tamper.guardrails_off\ninjected",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		value := value
		t.Run("reject", func(t *testing.T) {
			t.Parallel()
			if got := SafeSourceSignalCode(value); got != nil {
				t.Fatalf("SafeSourceSignalCode(%q) = %q, want nil", value, *got)
			}
		})
	}
}
