package localhttp

import (
	"strings"
	"testing"
)

func TestHabitsBrowserShellIsSeparateAndHiddenByDefault(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")

	for _, required := range []string{
		`id="nav-habits"`,
		`<span id="nav-habits-label">Habits</span>`,
		`id="habits-view"`,
		`id="habits-view"` + "\n" + `          aria-labelledby="habits-heading"` + "\n" + `          hidden`,
		`id="habits-heading" tabindex="-1">Habits`,
		`id="habits-rail"`,
		`id="habits-detail"`,
		`id="habits-older"`,
		`id="habits-status"`,
		`id="habits-loading"`,
		`id="habits-error"`,
		`id="habits-retry"`,
		`id="habits-content" hidden`,
		`id="habits-empty" hidden`,
		`id="habits-list"`,
		`id="habits-about" hidden`,
		`id="habits-limitations"`,
		`id="habits-debrief-all" type="button" hidden`,
		`Nothing here is compared with other people, and nothing here is sent to your agent.`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("Habits shell is missing %q", required)
		}
	}

	// The existing views keep their default states: Report stays the landing
	// view and the other workspaces keep their own markup untouched.
	for _, preserved := range []string{
		`id="nav-brief"` + "\n" + `            type="button"` + "\n" + `            aria-current="page"`,
		`id="attention-view"` + "\n" + `          hidden`,
		`id="sessions-view" hidden`,
	} {
		if !strings.Contains(index, preserved) {
			t.Errorf("Habits must not change existing view defaults: missing %q", preserved)
		}
	}
	if strings.Index(index, `id="habits-view"`) < strings.Index(index, `id="sessions-view"`) {
		t.Error("Habits view should render after the existing workspaces")
	}
}

func TestHabitsBrowserContractReadsOnlyItsOwnRoute(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`navHabits: "Habits"`,
		`habitsStatus: "idle"`,
		`["brief", "attention", "sessions", "habits"].includes(view)`,
		`setViewVisibility(elements.habitsView, habitsActive);`,
		`setCurrentNavigation(elements.navHabits, habitsActive);`,
		`elements.navHabitsLabel.textContent = copy.navHabits;`,
		`await apiGet("/v1/user-insights")`,
		`"belay.user-insights.v1"`,
		`.slice(0, 25)`,
		`renderDeterministicHabits(session, key, true)`,
		`renderDeterministicHabits(session, key, false)`,
		`"Available immediately from local evidence"`,
		`Add an AI-written debrief with ${habitsHarnessLabel(harness.name)}`,
		`"Open session evidence"`,
		`debrief.insights.filter(isRecord).slice(0, 5)`,
		`if (state.activeView === "habits") {`,
		`/debrief?generate=1`,
		`encodeURIComponent(key)`,
		`"Say this instead"`,
		`"What an expert would have done"`,
		`"Copy opening"`,
		`"Copy message"`,
		`"Rewrite debrief"`,
		`state.habitsSelectedKey`,
		`{ id: "change", label: "What to change" }`,
		`{ id: "message", label: "Your first message" }`,
		`{ id: "well", label: "What went well" }`,
		`{ id: "next", label: "Next time" }`,
		`function renderHabitsTimeline(debrief, session)`,
		`function renderHabitsStats(debrief, session)`,
		`insight.at_minute`,
		`"need attention"`,
		`"How the time span is calculated"`,
		`not active work time or guaranteed savings.`,
		`session.debrief_status === "stale"`,
		`"Outcome not reported"`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("Habits browser contract is missing %q", required)
		}
	}
	if strings.Count(app, `navHabits: "Habits"`) != 2 {
		t.Error("Habits label must be defined for both runtime experiences")
	}
	// Habits never writes through the Local API and never reuses the
	// Report, Patterns, or Sessions loaders.
	habitsStart := strings.Index(app, "async function loadUserInsights()")
	habitsEnd := strings.Index(app, "function createElement(tagName, className, text)")
	if habitsStart < 0 || habitsEnd < habitsStart {
		t.Fatal("Habits loader and renderer must be defined together")
	}
	habits := app[habitsStart:habitsEnd]
	for _, forbidden := range []string{
		"apiMutation(",
		"loadDeveloperBrief(",
		"loadCostIssues(",
		"refreshAttention(",
		"refreshSessions(",
		"innerHTML",
	} {
		if strings.Contains(habits, forbidden) {
			t.Errorf("Habits code must stay separate from other views: found %q", forbidden)
		}
	}
	for _, required := range []string{
		".habits-rail-item {",
		".habits-insight {",
		".habits-deterministic-card {",
		".habits-quote {",
		".habits-phase-bar {",
		".habits-marker {",
		".habits-tab[aria-selected=\"true\"] {",
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("Habits styles are missing %q", required)
		}
	}
}
