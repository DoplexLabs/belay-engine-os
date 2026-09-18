package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestBrowserCommandUsesDirectPlatformCommand(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"darwin", "/usr/bin/open", []string{"http://127.0.0.1:1234/#token=test"}},
		{"linux", "xdg-open", []string{"http://127.0.0.1:1234/#token=test"}},
		{"windows", "rundll32", []string{
			"url.dll,FileProtocolHandler",
			"http://127.0.0.1:1234/#token=test",
		}},
	}
	for _, test := range tests {
		t.Run(test.goos, func(t *testing.T) {
			name, args, err := browserCommand(
				test.goos,
				"http://127.0.0.1:1234/#token=test",
			)
			if err != nil {
				t.Fatal(err)
			}
			if name != test.wantName || !slices.Equal(args, test.wantArgs) {
				t.Fatalf("browser command = %q %q, want %q %q",
					name, args, test.wantName, test.wantArgs)
			}
			for _, shell := range []string{"sh", "bash", "zsh", "cmd", "powershell"} {
				if name == shell {
					t.Fatalf("browser command unexpectedly uses shell %q", name)
				}
			}
		})
	}
}

func TestBrowserOpenFailureIsPayloadFreeAndNonFatal(t *testing.T) {
	original := openBrowser
	t.Cleanup(func() { openBrowser = original })
	openBrowser = func(context.Context, string) error {
		return errors.New("token=private-browser-error")
	}
	var stderr bytes.Buffer

	attemptBrowserOpen(
		context.Background(),
		"http://127.0.0.1:1234/#token=private",
		"quickstart",
		&stderr,
	)

	output := stderr.String()
	if !strings.Contains(output, "browser could not be opened; use the printed Local URL") {
		t.Fatalf("browser warning = %q", output)
	}
	for _, prohibited := range []string{"private-browser-error", "token=private"} {
		if strings.Contains(output, prohibited) {
			t.Fatalf("browser warning leaked %q: %s", prohibited, output)
		}
	}
}
