package localhttp

import (
	"strings"
	"testing"
)

func TestMissionPackBrowserContract(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`id="mission-pack-dialog"`,
		`id="mission-pack-loading"`,
		`id="mission-pack-content"`,
		`id="mission-pack-copy-claude"`,
		`id="mission-pack-copy-codex"`,
		`id="mission-pack-show-evidence"`,
		`id="report-evidence-copy-claude"`,
		`id="report-evidence-copy-codex"`,
		`Copy for Claude Code`,
		`Copy for Codex`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("Mission Pack browser shell is missing %q", required)
		}
	}

	for _, required := range []string{
		`"Use in next session"`,
		`/v1/mission-pack?issue_id=${encodeURIComponent(issueID)}&intent=general`,
		`readText(response.schema_version) !== "belay.mission-pack.v1"`,
		`missionPackCacheByID.set(pack.pack_id, pack);`,
		`missionPackCacheByID.clear();`,
		`state.missionPackRequestController.abort();`,
		"`/belay start --issue ${selector}`",
		"`$belay start --issue ${selector}`",
		`agentIssueCommand("claude", issueID)`,
		`agentIssueCommand("codex", issueID)`,
		`openReportEvidenceDrawer(issue);`,
		`state.activeModal === "mission-pack"`,
		`document.createTextNode`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("Mission Pack browser behavior is missing %q", required)
		}
	}
	if strings.Contains(app, `apiGet("/v1/mission-pack`) {
		t.Fatal("Mission Pack must be fetched with an issue selector after user action")
	}

	for _, required := range []string{
		`.mission-pack-drawer`,
		`.mission-pack-clamp`,
		`.mission-pack-expandable`,
		`.mission-pack-actions`,
		`grid-template-columns: 1fr;`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("Mission Pack responsive styling is missing %q", required)
		}
	}
}

func TestMissionPackDrawerShowsOnlyActionableUserMetadata(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	start := strings.Index(app, "function renderMissionPack(pack)")
	end := strings.Index(app, "function closeMissionPackDrawer(restoreFocus)")
	if start < 0 || end <= start {
		t.Fatal("Mission Pack drawer renderer is unavailable")
	}
	renderer := app[start:end]

	for _, required := range []string{
		`const isEmpty = readText(pack.status).toLowerCase() === "empty";`,
		`elements.missionPackCopyClaude.disabled = isEmpty;`,
		`elements.missionPackCopyCodex.disabled = isEmpty;`,
		`"Belay found no useful guidance for this session."`,
		`if (pack.known_traps.length)`,
		`if (pack.operating_rules.length)`,
		`if (pack.verification.length)`,
		`if (pack.completion_checklist.length)`,
		`Target: ${compactDisplayPath(value.target_file)}`,
	} {
		if !strings.Contains(renderer, required) {
			t.Errorf("Mission Pack drawer polish is missing %q", required)
		}
	}

	for _, internalDetail := range []string{
		`transcript generations analyzed`,
		`estimated tokens`,
		`Within the Mission Pack size limit`,
		`missionPackProvenanceLabels`,
		`Math.round(value.confidence * 100)`,
		`source && source.kind`,
		`"Data notes"`,
		`createMissionPackWarning`,
		`"Freshness"`,
		`analysis_status`,
		`provenance.join(" · ")`,
		`Lower-ranked guidance was trimmed.`,
		`Project config and prior sessions`,
	} {
		if strings.Contains(renderer, internalDetail) {
			t.Errorf("Mission Pack drawer still exposes internal detail %q", internalDetail)
		}
	}
}

func TestMissionPackDrawerRendersSpecificTrapHeadlineAndCostOnly(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	start := strings.Index(app, "function createMissionPackTrap(value)")
	end := strings.Index(app, "function missionPackCost(value)")
	if start < 0 || end <= start {
		t.Fatal("Mission Pack trap renderer is unavailable")
	}
	renderer := app[start:end]
	for _, required := range []string{
		`createElement("h4", "mission-pack-clamp", readText(value.title))`,
		`const cost = missionPackCost(value);`,
		`if (cost)`,
		`createElement("p", "mission-pack-item-meta", cost)`,
	} {
		if !strings.Contains(renderer, required) {
			t.Errorf("Mission Pack trap renderer is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`value.session_count`,
		`"session"`,
		`"sessions"`,
		`innerHTML`,
	} {
		if strings.Contains(renderer, forbidden) {
			t.Errorf(
				"Mission Pack trap renderer exposes redundant or unsafe detail %q",
				forbidden,
			)
		}
	}
}
