package localhttp

import (
	"strings"
	"testing"
)

func TestDeveloperBriefBrowserShellAndDefaultView(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")

	for _, required := range []string{
		`id="nav-brief"`,
		`id="nav-brief"` + "\n" + `            type="button"` + "\n" + `            aria-current="page"`,
		`id="brief-view"`,
		`id="brief-heading" tabindex="-1">Your recent sessions at a glance`,
		`id="brief-action-list"`,
		`id="brief-recent-list"`,
		`id="brief-agent-summary"`,
		`id="brief-coverage"`,
		`id="report-sparkline"`,
		`>What keeps going wrong</h2>`,
		`>Fixes</h2>`,
		`>Waste</h2>`,
		`<summary>About this data</summary>`,
		`id="attention-view"` + "\n" + `          hidden`,
		`id="sessions-view" hidden`,
		`id="session-diagnosis"`,
		`id="session-diagnosis-heading" tabindex="-1"`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("Developer Brief shell is missing %q", required)
		}
	}

	diagnosis := strings.Index(index, `id="session-diagnosis"`)
	overview := strings.Index(index, `id="session-overview"`)
	if diagnosis < 0 || overview < 0 || diagnosis > overview {
		t.Fatal("session diagnosis must render before the existing overview and timeline")
	}
}

func TestDeveloperBriefBrowserContractAndBounds(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	index := readBrowserAsset(t, "assets/index.html")
	browser := app + index

	for _, required := range []string{
		`activeView: "brief"`,
		`setActiveView("brief", false);`,
		`await apiGet("/v1/report")`,
		`"belay.report.v1"`,
		`brief.top_issues.slice(0, 5)`,
		`elements.briefActionsSection.hidden = false;`,
		`fixes.slice(0, 20)`,
		`No recurring issues detected yet`,
		`No fixes recorded yet`,
		`"Use in next session"`,
		`"Show evidence"`,
		`formatIssueMinutes(issue.cost)`,
		`humanizeReportHeadline(issue.headline)`,
		"`/belay start --issue ${selector}`",
		"`$belay start --issue ${selector}`",
		`agentIssueCommand("claude", issueID)`,
		`agentIssueCommand("codex", issueID)`,
		`status.verification_state === "deferred"`,
		`kind === "open_attention_family"`,
		`kind === "open_issue"`,
		`kind === "open_session"`,
		`state.sessionReturnView =`,
		`? "brief"`,
		`: "sessions";`,
		`state.briefSelectionID = "";`,
		`await loadDeveloperBrief();`,
	} {
		if !strings.Contains(browser, required) {
			t.Errorf("Developer Brief browser contract is missing %q", required)
		}
	}
	if !strings.Contains(index, `>What keeps going wrong</h2>`) {
		t.Error("Report is missing the primary issue section")
	}

	if strings.Contains(app, "brief.top_issues.sort(") {
		t.Fatal("browser must preserve the server-ranked report order")
	}
}

func TestSessionDiagnosisBrowserContract(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		`state.selectedDiagnosis = isRecord(response.diagnosis)`,
		`"belay.session-diagnosis.v1"`,
		`diagnosis.action_cards.slice(0, 5)`,
		`elements.sessionDiagnosisTitle.textContent =`,
		`readText(diagnosis.summary.title)`,
		`elements.sessionDiagnosisDetail.textContent =`,
		`readText(diagnosis.summary.detail)`,
		`focusRegistry.diagnosisActions.set(cardID, button);`,
		`returnFocus.type === "diagnosis-action"`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("session diagnosis browser contract is missing %q", required)
		}
	}
}

func TestDeveloperBriefAccessibilityAndSafeRendering(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`setViewVisibility(elements.briefView, briefActive);`,
		`setCurrentNavigation(elements.navBrief, briefActive);`,
		`focusRegistry.briefActions.get(reference.key)`,
		`focusRegistry.briefSessions.get(reference.key)`,
		`focusRegistry.diagnosisActions.get(reference.key)`,
		`elements.briefStatus.textContent =`,
		`report-issue-card`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("Developer Brief accessibility contract is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"innerHTML",
		"outerHTML",
		"insertAdjacentHTML",
		"document.write",
	} {
		if strings.Contains(app, forbidden) {
			t.Errorf("browser asset contains unsafe rendering primitive %q", forbidden)
		}
	}
	for _, required := range []string{
		`.brief-workspace {`,
		`overflow-y: auto;`,
		`.brief-session-button {`,
		`min-height: 44px;`,
		`.report-sparkline {`,
		`.report-card-actions {`,
		`.session-diagnosis {`,
		`@media (max-width: 680px)`,
		`.session-diagnosis-actions {`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("Developer Brief responsive styles are missing %q", required)
		}
	}
}
