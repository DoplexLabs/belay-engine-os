package localhttp

import (
	"strings"
	"testing"
)

func TestTranscriptStatusBriefShellAndPrivacyCopy(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	for _, required := range []string{
		`id="transcript-overview-heading"`,
		`id="transcript-with-count"`,
		`id="transcript-partial-count"`,
		`id="transcript-without-count"`,
		`id="transcript-only-count"`,
		`id="transcript-session-list"`,
		`aria-live="polite"`,
		`Full local transcripts are retained encrypted on this device.`,
		`Nothing is uploaded.`,
		`The timeline shows minimized canonical events.`,
		`transcripts stay on this device and are used only for Belay`,
		`Local intelligence over loopback.`,
		`<span class="privacy-label">Minimized canonical events</span>`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("transcript status shell is missing %q", required)
		}
	}
	for _, inaccurate := range []string{
		`without showing prompt text, response text, or file contents`,
		`<span class="privacy-label">Minimized metadata</span>`,
	} {
		if strings.Contains(index, inaccurate) {
			t.Errorf("browser copy retains inaccurate claim %q", inaccurate)
		}
	}
}

func TestTranscriptStatusPollingAndSafeRendering(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	for _, required := range []string{
		`const transcriptPollMilliseconds = 2_000;`,
		`await apiGet("/v1/transcript-status")`,
		`readText(response.schema_version) !== "belay.transcript-status.v1"`,
		`coverage.transcript_only`,
		`project: readText(session && session.project)`,
		`sessions.length > 10`,
		`state.transcriptPollTimer = globalThis.setTimeout(() => {`,
		`elements.transcriptSessionList.replaceChildren();`,
		`project.textContent =`,
		`detail.textContent =`,
		`row.append(project, detail);`,
		`Updates every 2 seconds`,
		`Transcript status unavailable · retrying every 2 seconds`,
		`Last transcript status is stale · retrying every 2 seconds`,
		`state.transcriptStatusStale ? "stale" : "current"`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("transcript polling contract is missing %q", required)
		}
	}

	request := browserSourceBlock(
		t,
		app,
		"  async function requestTranscriptStatus() {",
		"  function requireTranscriptStatus(response) {",
	)
	success := strings.Index(request, "state.transcriptStatusStale = false;")
	renderSuccess := strings.Index(request, "renderTranscriptStatus();")
	catchIndex := strings.Index(request, "} catch {")
	stale := strings.Index(request, "state.transcriptStatusStale = true;")
	renderFailure := strings.LastIndex(request, "renderTranscriptStatus();")
	if success < 0 || renderSuccess < success {
		t.Fatal("successful transcript polling does not clear stale state before rendering")
	}
	if catchIndex < 0 || stale < catchIndex || renderFailure < stale {
		t.Fatal("failed transcript polling does not mark retained data stale before rendering")
	}
	renderer := browserSourceBlock(
		t,
		app,
		"  function renderTranscriptStatus() {",
		"  function scheduleTranscriptPoll()",
	)
	for _, forbidden := range []string{
		"innerHTML",
		"insertAdjacentHTML",
		"outerHTML",
		"payload",
		"excerpt",
		"project_path",
		"git_remote",
		"project_identity",
	} {
		if strings.Contains(renderer, forbidden) {
			t.Errorf("transcript renderer contains unsafe/private field %q", forbidden)
		}
	}
}
