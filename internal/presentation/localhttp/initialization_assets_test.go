package localhttp

import (
	"strings"
	"testing"
)

func TestProgressiveInitializationBrowserShell(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`id="initialization-banner"`,
		`role="status"`,
		`aria-live="polite"`,
		`aria-atomic="true"`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("initialization status shell is missing %q", required)
		}
	}
	for _, required := range []string{
		`.initialization-banner {`,
		`.initialization-banner[data-state="degraded"]`,
		`grid-template-rows: 64px auto minmax(0, 1fr);`,
		`grid-template-rows: 56px auto minmax(0, 1fr);`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("initialization status styling is missing %q", required)
		}
	}
}

func TestProgressiveInitializationPollingAndCopy(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		`const initializationPollMilliseconds = 2_000;`,
		`await apiGet("/v1/initialization")`,
		`const value = isRecord(response) ? response.initialization : null;`,
		`readText(response.schema_version) !== "belay.initialization.v1"`,
		`["initializing", "ready", "degraded"].includes(initializationState)`,
		`Belay is importing local agent history. Results are partial and will update automatically.`,
		`Belay imported available data, but part of the initial scan could not complete. Results may be partial.`,
		`if (state.initializationRequestInFlight) return;`,
		`const generation = ++state.initializationPollGeneration;`,
		`if (generation !== state.initializationPollGeneration)`,
		`state.initializationPollTimer = globalThis.setTimeout(() => {`,
		`globalThis.clearTimeout(state.initializationPollTimer);`,
		`elements.initializationBanner.hidden = !message;`,
		`elements.initializationBanner.textContent = message;`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("progressive initialization contract is missing %q", required)
		}
	}
	if strings.Contains(app, "setInterval(") {
		t.Fatal("initialization polling must not use an overlapping interval")
	}
	for _, forbidden := range []string{
		`response.data`,
		`readText(value.schema_version)`,
	} {
		parser := browserSourceBlock(
			t,
			app,
			"  function requireInitializationStatus(response) {",
			"  function renderInitializationStatus()",
		)
		if strings.Contains(parser, forbidden) {
			t.Errorf("initialization parser still accepts obsolete shape %q", forbidden)
		}
	}
}

func TestProgressiveInitializationRefreshPreservesActiveNavigation(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		`void startProgressiveInitialization();`,
		`await requestInitializationStatus();`,
		`await refreshActiveViewForInitialization(false);`,
		`await refreshActiveViewForInitialization(true);`,
		`await loadDeveloperBrief(preserveSelection);`,
		`if (state.selectedFamily || state.selectedIssueID) return false;`,
		`await refreshAttention(false, true);`,
		`if (state.selectedSessionID) return false;`,
		`await refreshSessions(false);`,
		`if (preserveCurrent && priorBrief) {`,
		`Importing earlier sessions…`,
		`No recent activity has been imported yet`,
		`Initial import is still in progress; this result is partial and will update automatically.`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("progressive initialization refresh contract is missing %q", required)
		}
	}

	start := strings.Index(app, "async function startProgressiveInitialization()")
	if start < 0 {
		t.Fatal("progressive initialization startup function is missing")
	}
	startup := app[start:]
	status := strings.Index(startup, "await requestInitializationStatus();")
	refresh := strings.Index(
		startup,
		"await refreshActiveViewForInitialization(false);",
	)
	if status < 0 || refresh < 0 || status > refresh {
		t.Fatal("initialization status must be known before the initial view data is rendered")
	}

	safeRefresh := browserSourceBlock(
		t,
		app,
		"  async function refreshActiveViewForInitialization(preserveSelection) {",
		"  async function loadDeveloperBrief(",
	)
	issueGuard := strings.Index(
		safeRefresh,
		"if (state.selectedFamily || state.selectedIssueID) return false;",
	)
	attentionRefresh := strings.Index(
		safeRefresh,
		"await refreshAttention(false, true);",
	)
	sessionGuard := strings.Index(
		safeRefresh,
		"if (state.selectedSessionID) return false;",
	)
	sessionRefresh := strings.Index(safeRefresh, "await refreshSessions(false);")
	if issueGuard < 0 || attentionRefresh < issueGuard {
		t.Fatal("open Attention detail is not guarded before polling refresh")
	}
	if sessionGuard < 0 || sessionRefresh < sessionGuard {
		t.Fatal("open Session detail is not guarded before polling refresh")
	}
}

func TestInitializationWrappedResponseAndPartialBriefFixture(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	wrappedResponse := `{
		"schema_version": "belay.initialization.v1",
		"initialization": {
			"state": "initializing",
			"started_at": "2026-09-09T00:00:00Z",
			"completed_at": null,
			"error_code": null
		}
	}`

	for _, required := range []string{
		`"schema_version": "belay.initialization.v1"`,
		`"initialization": {`,
		`"state": "initializing"`,
	} {
		if !strings.Contains(wrappedResponse, required) {
			t.Fatalf("wrapped initialization fixture is missing %q", required)
		}
	}
	for _, required := range []string{
		`response.initialization`,
		`elements.initializationBanner.hidden = !message;`,
		`elements.initializationBanner.textContent = message;`,
		`initializing && sessionCount === 0`,
		`Importing earlier sessions…`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("wrapped initializing response behavior is missing %q", required)
		}
	}
}

func TestSessionsListRejectsOutOfOrderInitializationRefresh(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	loadSessions := browserSourceBlock(
		t,
		app,
		"  async function loadSessions(append) {",
		"  function buildSessionPath(cursor)",
	)

	for _, required := range []string{
		`const generation = ++state.sessionsRequestGeneration;`,
		`const cursor = append ? state.sessionNextCursor : "";`,
		`const priorSessions = append && cursor ? state.sessions.slice() : [];`,
		`if (generation !== state.sessionsRequestGeneration) return false;`,
		`deduplicateByID(priorSessions.concat(page), "session_id")`,
		`if (generation === state.sessionsRequestGeneration) {`,
	} {
		if !strings.Contains(loadSessions, required) {
			t.Errorf("sessions request ordering contract is missing %q", required)
		}
	}

	successGuard := strings.Index(
		loadSessions,
		`if (generation !== state.sessionsRequestGeneration) return false;`,
	)
	sessionMutation := strings.Index(loadSessions, "state.sessions =")
	catchIndex := strings.Index(loadSessions, "} catch (error) {")
	if catchIndex < 0 {
		t.Fatal("sessions loader catch block is missing")
	}
	catchBlock := loadSessions[catchIndex:]
	staleErrorGuard := strings.Index(
		catchBlock,
		`if (generation !== state.sessionsRequestGeneration) return false;`,
	)
	errorMutation := strings.Index(catchBlock, "state.sessions = []")
	if successGuard < 0 || sessionMutation < successGuard {
		t.Fatal("an older sessions response can mutate the replacement list")
	}
	if staleErrorGuard < 0 || errorMutation < staleErrorGuard {
		t.Fatal("an older sessions error can clear a newer filtered list")
	}

	latestGeneration := 0
	sessions := ""
	startRequest := func() int {
		latestGeneration++
		return latestGeneration
	}
	commit := func(generation int, value string) {
		if generation == latestGeneration {
			sessions = value
		}
	}
	initializationPoll := startRequest()
	filterRequest := startRequest()
	commit(filterRequest, "newer filtered sessions")
	commit(initializationPoll, "older initialization sessions")
	if sessions != "newer filtered sessions" {
		t.Fatalf("older initialization response overwrote filter result: %q", sessions)
	}
}

func TestSessionsRefreshFollowUpRequiresCommittedList(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	refresh := browserSourceBlock(
		t,
		app,
		"  async function refreshSessions(preserveSelection) {",
		"  function setActiveView(view, moveFocus)",
	)

	for _, required := range []string{
		`const sessionsReady =`,
		`sessionsResult.status === "fulfilled" && sessionsResult.value === true;`,
		`if (!sessionsReady) return false;`,
	} {
		if !strings.Contains(refresh, required) {
			t.Errorf("sessions refresh ownership contract is missing %q", required)
		}
	}
}
