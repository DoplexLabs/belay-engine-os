package localhttp

import (
	"strings"
	"testing"
)

func TestCostIssueAttentionAssetsExposeRankedIssuesAndSeparateSafety(t *testing.T) {
	indexBody, err := assetFiles.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	appBody, err := assetFiles.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	styleBody, err := assetFiles.ReadFile("assets/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexBody)
	app := string(appBody)
	styles := string(styleBody)
	for _, required := range []string{
		`id="attention-mode-issues"`,
		`id="attention-mode-safety"`,
		`id="cost-issues-section"`,
		`id="cost-issue-list"`,
		`What keeps going wrong`,
		`Safety`,
		`Show evidence`,
		`Prepare fix`,
	} {
		if !strings.Contains(index+app, required) {
			t.Fatalf("cost issue assets missing %q", required)
		}
	}
	for _, required := range []string{
		`await apiGet("/v1/cost-issues?limit=5")`,
		`readText(response.schema_version) !== "belay.cost-issues.v1"`,
		`formatIssueDollarCost(issue.cost)`,
		`readText(excerpt && excerpt.text)`,
		`setAttentionMode("safety")`,
		`"propose-cost-issue-fix.v1"`,
		`"belay.cost-issue-fix.v1"`,
		`apply it with /belay in Claude Code or $belay in Codex`,
	} {
		if !strings.Contains(app, required) {
			t.Fatalf("cost issue application missing %q", required)
		}
	}
	for _, required := range []string{
		`.attention-workspace.cost-mode`,
		`.cost-issue-card`,
		`.cost-issue-excerpt blockquote`,
		`.cost-issue-fix-diff`,
	} {
		if !strings.Contains(styles, required) {
			t.Fatalf("cost issue styles missing %q", required)
		}
	}
}
