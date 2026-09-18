package localhttp

import (
	"strings"
	"testing"
)

func TestSessionsSummaryLayoutContract(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`class="local-overview" aria-label="Local activity overview" hidden`,
		`class="filter-grid session-filter-row"`,
		`<details class="filter-more" id="filter-more">`,
		`id="selected-meta"`,
		`id="session-tab-summary"`,
		`id="session-tab-timeline"`,
		`id="session-summary-tab"`,
		`id="session-timeline-tab" hidden`,
		`id="session-visual"`,
		`>Worth a look</h4>`,
		`>Key moments</h4>`,
		`>Files and resources</h4>`,
		`Up to five notable events from the loaded timeline.`,
		`id="filter-harness"`,
		`id="filter-outcome"`,
		`id="filter-capture"`,
		`id="filter-date"`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("Sessions shell is missing %q", required)
		}
	}
	diagnosis := strings.Index(index, `id="session-diagnosis"`)
	tabs := strings.Index(index, `id="session-tab-summary"`)
	visual := strings.Index(index, `id="session-visual"`)
	overview := strings.Index(index, `id="session-overview"`)
	timeline := strings.Index(index, `id="session-timeline-tab"`)
	if !(diagnosis < tabs && tabs < visual && visual < overview && overview < timeline) {
		t.Fatal("Sessions detail must order diagnosis, tabs, visual summary, details, then the timeline tab")
	}

	for _, required := range []string{
		`sessionTab: "summary"`,
		`function setSessionTab(tab)`,
		`function renderSessionVisual(overview)`,
		`function sessionDurationLabel(session)`,
		"project || `${harness} session`",
		`.concat(readText(session.project))`,
		`[harness, session.session_id]`,
		`elements.selectedMeta.textContent`,
		`sessionTestCommandPattern.test(commandText)`,
		"`Drawn from the ${formatNumber(events.length)} loaded ${events.length === 1 ? \"event\" : \"events\"}.`",
		`setSessionTab("summary");`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("Sessions browser contract is missing %q", required)
		}
	}
	visualCode := sourceSection(t, app, "  function renderSessionVisual(overview) {", "  function renderFindingList() {")
	if strings.Contains(visualCode, "innerHTML") || strings.Contains(visualCode, "apiGet(") {
		t.Error("the visual summary must be derived from already loaded events without new requests")
	}
	for _, required := range []string{
		`.session-tab[aria-selected="true"] {`,
		`.session-strip {`,
		`.session-mark[data-tone="failed"]`,
		`.session-mix-line {`,
		`.session-dot[data-tone="succeeded"] {`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("Sessions styles are missing %q", required)
		}
	}
}
