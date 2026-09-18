package localhttp

import (
	"strings"
	"testing"
)

func TestUpdateNoticeIsGentleAndUserControlled(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		`id="update-notice"`,
		`id="update-modal-layer"`,
		`id="update-copy-command"`,
		`id="update-remind"`,
		`id="update-skip"`,
		`curl -fsSL https://getbelay.vercel.app/install | bash`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("update UI is missing %q", required)
		}
	}
	for _, required := range []string{
		`void loadUpdateStatus();`,
		`await apiGet("/v1/update")`,
		`await apiPost("/v1/update", { action, version })`,
		`elements.updateNotice.hidden = !available;`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("update behavior is missing %q", required)
		}
	}
}
