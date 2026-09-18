package model

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestIsCanonicalUUIDv7(t *testing.T) {
	valid := []string{
		"00000000-0000-7000-8000-000000000000",
		"018f23ab-cdef-7abc-bdef-0123456789ab",
	}
	for _, value := range valid {
		if !IsCanonicalUUIDv7(value) {
			t.Errorf("IsCanonicalUUIDv7(%q) = false, want true", value)
		}
	}

	invalid := []string{
		"",
		"00000000-0000-7000-8000-00000000000",
		"000000000000-7000-8000-000000000000",
		"00000000-0000-6000-8000-000000000000",
		"00000000-0000-7000-7000-000000000000",
		"00000000-0000-7000-c000-000000000000",
		"00000000-0000-7000-8000-00000000000g",
		"018F23AB-CDEF-7ABC-BDEF-0123456789AB",
		strings.Repeat("a", 36),
	}
	for _, value := range invalid {
		if IsCanonicalUUIDv7(value) {
			t.Errorf("IsCanonicalUUIDv7(%q) = true, want false", value)
		}
	}
}

func TestNewUUIDv7ProducesCanonicalValue(t *testing.T) {
	value, err := NewUUIDv7(
		time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC),
		bytes.NewReader(make([]byte, 10)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !IsCanonicalUUIDv7(value) {
		t.Fatalf("NewUUIDv7() = %q, want canonical UUIDv7", value)
	}
}
