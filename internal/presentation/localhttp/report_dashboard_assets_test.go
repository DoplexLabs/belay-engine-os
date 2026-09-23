package localhttp

import (
	"strings"
	"testing"
)

func TestReportDashboardHidesPatternsAndShowsDebriefs(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`id="nav-attention"` + "\n" + `            type="button"` + "\n" + `            hidden`,
		`id="report-habits-list"`,
		`id="report-habits-open"`,
		`id="report-habit-summary" hidden`,
		`id="report-habits-empty" hidden`,
		`class="brief-summary report-stats"`,
		`aria-labelledby="brief-actions-heading" hidden`,
		`aria-labelledby="brief-recent-heading" hidden`,
		`aria-labelledby="brief-agents-heading" hidden`,
		`Habits and Sessions remain available.`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("Report dashboard shell is missing %q", required)
		}
	}
	habits := strings.Index(index, `id="nav-habits"`)
	sessions := strings.Index(index, `id="nav-sessions"`)
	attention := strings.Index(index, `id="nav-attention"`)
	if habits < 0 || sessions < 0 || attention < 0 || habits > sessions || sessions > attention {
		t.Fatal("navigation order must be Report, Habits, Sessions, then the hidden Patterns button")
	}
	usage := strings.Index(index, `id="report-usage-heading"`)
	debriefs := strings.Index(index, `id="report-habits-heading"`)
	patterns := strings.Index(index, `id="brief-actions-heading"`)
	if usage < 0 || debriefs < 0 || patterns < 0 || usage > debriefs || debriefs > patterns {
		t.Fatal("Report must show usage, then debriefs, before the hidden pattern sections")
	}

	for _, required := range []string{
		`await apiGet("/v1/user-insights?limit=3")`,
		`void loadReportHabits();`,
		`function renderReportHabits()`,
		`function renderReportHabitCard(session, harness, showPattern)`,
		`function renderReportHabitSummary(readySessions)`,
		`function createWaitingPattern(issue)`,
		`Repeated pattern`,
		`This uses that account and usually takes one to three minutes.`,
		`"Read the full debrief"`,
		`in the background.`,
		`"Your recent sessions at a glance"`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("Report dashboard browser contract is missing %q", required)
		}
	}
	if strings.Contains(app, `No harness installed to write this debrief.`) {
		t.Error("app.js still contains \"No harness installed to write this debrief.\"")
	}
	if strings.Contains(app, `"Your top recurring pattern"`) {
		t.Error("Report heading must no longer be pattern-driven")
	}
	for _, required := range []string{".report-stats dl > div {", ".report-habit-card {", ".report-habit-stat {", ".report-spark-wrap {"} {
		if !strings.Contains(styles, required) {
			t.Errorf("Report dashboard styles are missing %q", required)
		}
	}
}
