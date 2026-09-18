package localhttp

import (
	"strings"
	"testing"
)

func TestRuntimeExperienceLoadsBeforePrimaryView(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		`const runtimeReady = loadRuntimeExperience();`,
		`const response = await apiGet("/v1/runtime");`,
		`await runtimeReady;`,
		`await requestInitializationStatus();`,
		`await refreshActiveViewForInitialization(false);`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("runtime experience startup contract is missing %q", required)
		}
	}

	startup := browserSourceBlock(
		t,
		app,
		"  async function startProgressiveInitialization() {",
		"  async function loadRuntimeExperience() {",
	)
	runtime := strings.Index(startup, "await runtimeReady;")
	initialization := strings.Index(startup, "await requestInitializationStatus();")
	primaryView := strings.Index(
		startup,
		"await refreshActiveViewForInitialization(false);",
	)
	if runtime < 0 || initialization < runtime || primaryView < initialization {
		t.Fatal("runtime experience must resolve before initialization and the primary view load")
	}
}

func TestRuntimeExperienceValidationAndClosedFallback(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	parser := browserSourceBlock(
		t,
		app,
		"  function requireRuntimeExperience(response) {",
		"  function applyExperience(experience) {",
	)
	loader := browserSourceBlock(
		t,
		app,
		"  async function loadRuntimeExperience() {",
		"  function requireRuntimeExperience(response) {",
	)

	for _, required := range []string{
		`const runtimeSchemaVersion = "belay.local-runtime.v1";`,
		`readText(response.schema_version) !== runtimeSchemaVersion`,
		`!["current", "value-first"].includes(experience)`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("runtime experience validation is missing %q", required)
		}
	}
	if !strings.Contains(parser, `throw new Error("Local API returned an invalid runtime experience.");`) {
		t.Fatal("invalid runtime response must be rejected")
	}
	for _, required := range []string{
		`let experience = "current";`,
		`} catch {`,
		`experience = "current";`,
		`applyExperience(experience);`,
	} {
		if !strings.Contains(loader, required) {
			t.Errorf("runtime experience fail-closed fallback is missing %q", required)
		}
	}
}

func TestRuntimeExperienceCopyAndDataAttributes(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	index := readBrowserAsset(t, "assets/index.html")

	for _, required := range []string{
		`<body data-experience="current">`,
		`id="app-shell" data-experience="current"`,
		`<span id="nav-brief-label">Report</span>`,
		`<span id="nav-attention-label">Patterns</span>`,
		`<span id="nav-sessions-label">Sessions</span>`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("current-mode browser shell is missing %q", required)
		}
	}

	for _, required := range []string{
		`navBrief: "Report"`,
		`navAttention: "Patterns"`,
		`navSessions: "Sessions"`,
		`briefLoading: "Preparing your report…"`,
		`navBrief: "Report"`,
		`navAttention: "Patterns"`,
		`navSessions: "Sessions"`,
		`briefLoading: "Preparing your report…"`,
		`briefOpenAttention: "View all patterns"`,
		`briefOpenSessions: "View sessions"`,
		`attentionHeading: "Patterns"`,
		`sessionsHeading: "Sessions"`,
		`document.body.dataset.experience = selected;`,
		`elements.appShell.dataset.experience = selected;`,
		`elements.navBriefLabel.textContent = copy.navBrief;`,
		`elements.navAttentionLabel.textContent = copy.navAttention;`,
		`elements.navSessionsLabel.textContent = copy.navSessions;`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("runtime experience copy contract is missing %q", required)
		}
	}

	apply := browserSourceBlock(
		t,
		app,
		"  function applyExperience(experience) {",
		"  function experiencePageText(value) {",
	)
	for _, forbidden := range []string{
		"innerHTML",
		"outerHTML",
		"insertAdjacentHTML",
	} {
		if strings.Contains(apply, forbidden) {
			t.Errorf("runtime experience copy uses unsafe rendering primitive %q", forbidden)
		}
	}
}
