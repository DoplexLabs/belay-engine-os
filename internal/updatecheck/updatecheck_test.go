package updatecheck

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestCheckCachesCompatibleReleaseForEighteenHours(t *testing.T) {
	var requests atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`[
			{"tag_name":"v0.0.1-alpha.11","draft":false,"published_at":"2026-09-16T10:00:00Z","assets":[{"name":"belay-linux-amd64.tar.gz"}]},
			{"tag_name":"v0.0.1-alpha.10","name":"Alpha 10","draft":false,"html_url":"https://example.invalid/release","published_at":"2026-09-16T09:00:00Z","assets":[{"name":"belay-v0.0.1-alpha.10-darwin-arm64.tar.gz"}]},
			{"tag_name":"v0.0.1-alpha.6","draft":true,"published_at":"2026-09-16T08:00:00Z","assets":[{"name":"belay-v0.0.1-alpha.6-darwin-arm64.tar.gz"}]}
		]`)),
		}, nil
	})}

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	client := New(
		t.TempDir(),
		"0.0.1-alpha.5",
		WithClock(func() time.Time { return now }),
		WithHTTPClient(httpClient),
		WithEnvironment(func(key string) string {
			if key == EnvEndpoint {
				return "https://example.invalid/releases"
			}
			return ""
		}),
	)
	first, err := client.Check(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.UpdateAvailable || first.LatestVersion != "0.0.1-alpha.10" {
		t.Fatalf("first status = %#v", first)
	}
	if first.NextCheckAt != now.Add(CheckInterval).Format(time.RFC3339) {
		t.Fatalf("next check = %q", first.NextCheckAt)
	}

	now = now.Add(CheckInterval - time.Second)
	if _, err := client.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestReminderAndDismissalHideAvailableRelease(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	client := New(
		t.TempDir(),
		"0.0.1-alpha.5",
		WithClock(func() time.Time { return now }),
		WithEnvironment(func(key string) string {
			if key == EnvEndpoint {
				return "https://example.invalid/releases"
			}
			return ""
		}),
	)
	client.mu.Lock()
	err := client.saveStateLocked(State{
		Version:       stateVersion,
		LatestVersion: "0.0.1-alpha.10",
	})
	client.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	if err := client.Remind("0.0.1-alpha.10"); err != nil {
		t.Fatal(err)
	}
	if client.Status().UpdateAvailable {
		t.Fatal("reminded release must be hidden")
	}
	now = now.Add(CheckInterval)
	if !client.Status().UpdateAvailable {
		t.Fatal("release must return after the 18-hour reminder window")
	}
	if err := client.Dismiss("0.0.1-alpha.10"); err != nil {
		t.Fatal(err)
	}
	if client.Status().UpdateAvailable {
		t.Fatal("dismissed release must stay hidden")
	}
}
