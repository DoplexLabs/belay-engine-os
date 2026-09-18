package localhttp

import (
	"strings"
	"testing"
)

func TestRecurrenceBrowserShellPlacementAndIndependentFilters(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")

	for _, required := range []string{
		`id="fix-monitoring-section"`,
		`id="fix-monitoring-heading">After attempts`,
		`id="fix-monitoring-filters"`,
		`id="fix-monitoring-filter-state"`,
		`id="fix-monitoring-filter-change-kind"`,
		`id="fix-monitoring-filter-severity"`,
		`id="fix-monitoring-filter-harness"`,
		`id="fix-monitoring-filter-issue-id"`,
		`id="fix-monitoring-filter-recorded-after"`,
		`id="fix-monitoring-filter-retracted"`,
		`Include retracted`,
		`id="fix-monitoring-list"`,
		`id="fix-monitoring-pagination"`,
		`id="fix-monitoring-load-more"`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("recurrence browser shell is missing %q", required)
		}
	}
	if strings.Index(index, `id="fix-monitoring-section"`) >
		strings.Index(index, `id="stable-issues-heading"`) {
		t.Error("After attempts must precede and not replace the Issues section")
	}
	for _, forbidden := range []string{
		`id="issue-filter-recurrence"`,
		`<span>Session spread</span>`,
		`<option value="repeated">Exact matches</option>`,
		`<option value="single">One session</option>`,
	} {
		if strings.Contains(index, forbidden) {
			t.Errorf("default Attention retains removed P0-07 control %q", forbidden)
		}
	}
}

func TestRecurrenceFrozenListDetailAndCursorContracts(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		`fixMonitoring: { page: 20 }`,
		`fixMonitoringDetail: { page: 20 }`,
		`fixRecurrences: { page: 20 }`,
		`parameters.set("state", filters.state);`,
		`parameters.set("change_kind", filters.changeKind);`,
		`parameters.set("severity", filters.severity);`,
		`parameters.set("harness", filters.harness);`,
		`parameters.set("issue_id", filters.issueID);`,
		`parameters.set("recorded_after", filters.recordedAfter);`,
		`parameters.set("include_retracted", "true");`,
		"`/v1/fix-monitoring?${parameters.toString()}`",
		"`/v1/issues/${encodeURIComponent(issueID)}/fix-monitoring?${parameters.toString()}`",
		"`/v1/issues/${encodeURIComponent(issueID)}/fixes/${encodeURIComponent(annotationID)}/recurrences?${parameters.toString()}`",
		`response.schema_version !== "belay.fix-monitoring.v1"`,
		`response.monitoring_view_cursor`,
		`annotation.observation_view_cursor`,
		`parameters.set("observation_view_cursor", viewCursor);`,
		`parameters.delete("limit");`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("frozen recurrence HTTP contract is missing %q", required)
		}
	}
	if strings.Contains(app,
		"`/v1/issues/${encodeURIComponent(issueID)}/fixes?${parameters.toString()}`") {
		t.Error("browser must not render attempt history from the P0-03 GET /fixes route")
	}

	listPath := browserSourceBlock(
		t,
		app,
		"  function buildFixMonitoringPath(cursor) {",
		"  async function loadFixMonitoring(",
	)
	if !strings.Contains(listPath, `if (cursor) {`) ||
		!strings.Contains(listPath, `parameters.set("cursor", cursor);`) {
		t.Error("monitoring continuation must use its endpoint-specific cursor")
	}
	detail := browserSourceBlock(
		t,
		app,
		"  async function loadFixMonitoringDetail(",
		"  function loadMoreFixMonitoringDetail()",
	)
	for _, required := range []string{
		`parameters.set("cursor", cursor);`,
		`parameters.set("view_cursor", viewCursor);`,
		`response.current_issue_available === true`,
		`(!currentIssueAvailable && currentIssue !== null)`,
		`generation !== state.fixHistoryRequestGeneration`,
	} {
		if !strings.Contains(detail, required) {
			t.Errorf("monitoring detail contract is missing %q", required)
		}
	}
}

func TestRecurrenceTruthfulStatesHistoryOnlyAndScopedErrors(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		"Same finding observed later",
		"This does not establish causality or whether the attempted change worked.",
		"Monitoring is incomplete.",
		"Some later Local activity is pending, failed, truncated, or only partially analyzed.",
		"Waiting for later sessions",
		"No comparable completed Local activity is available yet.",
		"No later matching evidence was observed in completed Local analysis.",
		"This does not verify resolution.",
		"Later sessions cannot be compared",
		"Belay cannot compare this attempt with later activity under the current rules.",
		"Attempt record retracted — no longer monitored.",
		"Monitoring status unavailable.",
		"This may be continuation within the original session.",
		"Matching evidence was recorded before this attempt record was retracted.",
		"This finding is no longer in the current results.",
		"New attempt recording and supporting sessions are unavailable because this finding is no longer current.",
		"Attempt dates and matching-event counts may remain even if cited events are later removed.",
		"belay.local/monitoring-catchup-in-progress",
		"belay.local/monitoring-catchup-failed",
		"The saved After attempts view is out of date.",
	} {
		if !strings.Contains(app, required) {
			t.Errorf("truthful recurrence contract is missing %q", required)
		}
	}
	for _, prohibited := range []string{
		"Fix succeeded",
		"Fix worked",
		"Issue resolved",
		"Recurrence prevented",
		"Verified resolution",
	} {
		if strings.Contains(app, prohibited) {
			t.Errorf("recurrence UI makes prohibited claim %q", prohibited)
		}
	}
	if !strings.Contains(app,
		`monitoringState !== "unknown"`) {
		t.Error("unknown monitoring state must fail closed for retraction")
	}
}

func TestRecurrenceLazyObservationEvidenceAndBounds(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		`const canonicalUUIDv7Pattern =`,
		`.filter((value) => canonicalUUIDv7Pattern.test(value))`,
		`.slice(0, 50);`,
		`"Show observations"`,
		`"Load more observations"`,
		`"Inspect retained evidence"`,
		`eventIDs.slice(0, 50).forEach((eventID) => {`,
		`parameters.append("event_id", eventID);`,
		"`/v1/sessions/${encodeURIComponent(sessionID)}/events/lookup?${parameters.toString()}`",
		`status === "unknown"`,
		`"Evidence status unknown; retained and missing counts are unavailable."`,
		`observation.evidence_truncated === true`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("lazy recurrence evidence contract is missing %q", required)
		}
	}
	row := browserSourceBlock(
		t,
		app,
		"  function createFixObservationRow(",
		"  function appendObservationEvidenceMetadata(",
	)
	if strings.Contains(row, "loadRecurrenceEvidence(") &&
		!strings.Contains(row, `inspect.addEventListener("click"`) {
		t.Error("observation evidence must load only after an explicit user action")
	}
}

func TestRecurrenceMutationRefreshFocusAccessibilityAndMobile(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`await reloadFixMonitoringAndFocus(issueID, annotationID);`,
		`state.fixHistoryNextCursor = "";`,
		`state.fixHistoryViewCursor = "";`,
		`fixObservationPages.clear();`,
		`focusRegistry.monitoringCards.get(reference.key)`,
		`focusRegistry.fixHistoryRows.get(reference.annotationID)`,
		`!element.isConnected`,
		`if (current.inert === true) return false;`,
		`restoreLogicalFocus(`,
		`state.fixHistoryStale = true;`,
		`const detailReady = await refreshSelectedMonitoringIssue(summary);`,
		`state.fixMonitoring.viewCursor,`,
		`true,`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("recurrence focus/refresh contract is missing %q", required)
		}
	}
	for _, required := range []string{
		`.monitoring-filters {`,
		`.monitoring-checkbox {`,
		`min-height: 44px;`,
		`.fix-observation-section {`,
		`.fix-observation-card:focus-visible {`,
		`@media (max-width: 680px)`,
		`@media (max-height: 700px)`,
		`overscroll-behavior: contain;`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("recurrence accessibility/mobile styles are missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"innerHTML",
		"outerHTML",
		"insertAdjacentHTML",
		"document.write",
		"eval(",
	} {
		if strings.Contains(app, forbidden) {
			t.Errorf("recurrence browser contains unsafe rendering primitive %q", forbidden)
		}
	}
}

func TestRecurrenceMutationInvalidatesSnapshotsBeforeFreshDetailReload(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	reload := browserSourceBlock(
		t,
		app,
		"  async function reloadFixMonitoringAndFocus(",
		"  function invalidateFixMonitoringSnapshotsAfterMutation()",
	)
	invalidate := browserSourceBlock(
		t,
		app,
		"  function invalidateFixMonitoringSnapshotsAfterMutation()",
		"  function fixMutationErrorMessage(",
	)

	invalidateIndex := strings.Index(
		reload,
		"    invalidateFixMonitoringSnapshotsAfterMutation();",
	)
	detailIndex := strings.Index(reload, "    const loaded = await loadFixMonitoringDetail(")
	if invalidateIndex < 0 || detailIndex < 0 || invalidateIndex > detailIndex {
		t.Fatal("mutation refresh must invalidate monitoring snapshots before its fresh detail load")
	}
	if strings.Count(reload, "loadFixMonitoringDetail(") != 1 {
		t.Error("mutation refresh must start exactly one fresh monitoring-detail load")
	}
	for _, required := range []string{
		`state.fixMonitoring.requestGeneration += 1;`,
		`state.fixMonitoring.data = [];`,
		`state.fixMonitoring.nextCursor = "";`,
		`state.fixMonitoring.hasMore = false;`,
		`state.fixMonitoring.viewCursor = "";`,
		`focusRegistry.monitoringCards.clear();`,
		`state.fixHistoryRequestGeneration += 1;`,
		`state.fixHistoryNextCursor = "";`,
		`state.fixHistoryHasMore = false;`,
		`state.fixHistoryViewCursor = "";`,
		`observation_view_cursor: "",`,
		`page.requestGeneration += 1;`,
		`fixObservationPages.clear();`,
		`focusRegistry.fixObservationRows.clear();`,
	} {
		if !strings.Contains(invalidate, required) {
			t.Errorf("mutation invalidation is missing %q", required)
		}
	}
	for _, required := range []string{
		`void loadFixMonitoring(false);`,
		`issueID,`,
		`annotationID,`,
		`focusRegistry.fixHistoryRows.get(annotationID)`,
	} {
		if !strings.Contains(reload, required) {
			t.Errorf("fresh mutation reload/focus flow is missing %q", required)
		}
	}
}

func TestRecurrenceMonitoringUsesExactIssueSnapshotForEligibility(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	eligibility := browserSourceBlock(
		t,
		app,
		"  async function loadMonitoringIssueEligibility(issueID)",
		"  async function loadFixMonitoringDetail(",
	)
	detail := browserSourceBlock(
		t,
		app,
		"  async function loadFixMonitoringDetail(",
		"  function loadMoreFixMonitoringDetail()",
	)

	for _, required := range []string{
		`state.issues.data.some(`,
		`state.evidenceGaps.data.some(`,
		`return loadFixEligibility(issueID, loadedViewCursor);`,
		`limit: "1"`,
		"`/v1/issues/${encodeURIComponent(issueID)}/occurrences?${parameters.toString()}`",
		`response.schema_version !== "belay.read.v1"`,
		`!isRecord(response.data.issue)`,
		`const issue = response.data.issue;`,
		`readText(issue.issue_id) !== issueID`,
		`const viewCursor = readCursor(response.view_cursor);`,
		`state.selectedIssueSource !== "monitoring"`,
		`return loadFixEligibility(issueID, viewCursor);`,
	} {
		if !strings.Contains(eligibility, required) {
			t.Errorf("exact monitoring eligibility flow is missing %q", required)
		}
	}
	if strings.Contains(eligibility, "`/v1/issues?${parameters.toString()}`") ||
		strings.Contains(eligibility, `issue_id: issueID`) {
		t.Error("monitoring eligibility must not use the unsupported issue-list issue_id fallback")
	}
	if !strings.Contains(detail, `void loadMonitoringIssueEligibility(issueID);`) {
		t.Error("current monitoring detail must use exact issue lookup when loaded Attention arrays cannot supply a snapshot")
	}
	if strings.Contains(detail, `state.fixEligibilityStatus = "error";`) {
		t.Error("monitoring detail must not disable eligibility merely because the issue is outside loaded Attention arrays")
	}
}

func TestRecurrenceExpiredListPaginationClosesConnectedMonitoringDetail(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	list := browserSourceBlock(
		t,
		app,
		"  async function loadFixMonitoring(append)",
		"  function fixMonitoringErrorMessage(",
	)
	expiryIndex := strings.Index(list, `if (isCursorExpired(error) && append) {`)
	closeIndex := strings.Index(list, `closeIssueDetail(false);`)
	resetIndex := strings.Index(list, `resetFixMonitoringBucket();`)
	refreshIndex := strings.Index(list, `await loadFixMonitoring(false);`)
	if expiryIndex < 0 || closeIndex < expiryIndex ||
		resetIndex < closeIndex || refreshIndex < resetIndex {
		t.Fatal("expired monitoring pagination must close connected detail before refreshing the list")
	}
	for _, required := range []string{
		`state.selectedIssueSource === "monitoring"`,
		`state.attentionRefreshGeneration += 1;`,
		`closeIssueDetail(false);`,
		`resetFixMonitoringBucket();`,
		`await loadFixMonitoring(false);`,
		`the open detail was closed`,
	} {
		if !strings.Contains(list, required) {
			t.Errorf("expired monitoring pagination recovery is missing %q", required)
		}
	}
}

func TestRecurrenceExpiredDetailClearsDependentStateBeforeListRefresh(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	detail := browserSourceBlock(
		t,
		app,
		"  async function loadFixMonitoringDetail(",
		"  async function recoverExpiredFixMonitoringDetail(",
	)
	recovery := browserSourceBlock(
		t,
		app,
		"  async function recoverExpiredFixMonitoringDetail(",
		"  function loadMoreFixMonitoringDetail()",
	)
	reset := browserSourceBlock(
		t,
		app,
		"  function resetFixIssueState()",
		"  async function loadFixEligibility(",
	)
	closeDetail := browserSourceBlock(
		t,
		app,
		"  function closeIssueDetail(",
		"  async function refreshAttentionAfterExpiry()",
	)

	if !strings.Contains(detail, `if (isCursorExpired(error)) {`) ||
		!strings.Contains(
			detail,
			`return recoverExpiredFixMonitoringDetail(issueID, append);`,
		) {
		t.Fatal("both initial and paginated monitoring-detail 410s must use full-chain recovery")
	}
	if strings.Contains(detail, `isCursorExpired(error) && append`) {
		t.Error("initial monitoring-detail 410 must not bypass full-chain recovery")
	}
	for _, required := range []string{
		`? "Attempt pagination"`,
		`: "Attempt detail";`,
		`resetFixMonitoringBucket(false);`,
		`closeIssueDetail(false);`,
		`await loadFixMonitoring(false);`,
		`The open attempt details were cleared`,
		`reopen the finding to inspect current evidence`,
	} {
		if !strings.Contains(recovery, required) {
			t.Errorf("expired monitoring-detail recovery is missing %q", required)
		}
	}
	resetListIndex := strings.Index(
		recovery,
		`resetFixMonitoringBucket(false);`,
	)
	closeIndex := strings.Index(recovery, `closeIssueDetail(false);`)
	refreshIndex := strings.Index(recovery, `await loadFixMonitoring(false);`)
	if resetListIndex < 0 || closeIndex < resetListIndex ||
		refreshIndex < closeIndex {
		t.Fatal("expired monitoring detail must clear list and dependent detail before refreshing")
	}
	for _, required := range []string{
		`state.fixHistory = [];`,
		`state.fixHistoryNextCursor = "";`,
		`state.fixHistoryHasMore = false;`,
		`state.fixHistoryViewCursor = "";`,
		`state.fixHistoryCurrentIssueAvailable = null;`,
		`state.fixEvidenceEvaluatedAt = "";`,
		`page.requestGeneration += 1;`,
		`fixObservationPages.clear();`,
		`focusRegistry.fixObservationRows.clear();`,
	} {
		if !strings.Contains(reset, required) {
			t.Errorf("dependent monitoring-detail invalidation is missing %q", required)
		}
	}
	clearIndex := strings.Index(closeDetail, `resetFixIssueState();`)
	renderIndex := strings.Index(closeDetail, `renderFixMonitoring();`)
	if clearIndex < 0 || renderIndex < clearIndex {
		t.Fatal("dependent detail and observation state must clear before close renders")
	}
}

func TestRecurrenceMonitoringViewCursorFailsClosed(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	list := browserSourceBlock(
		t,
		app,
		"  async function loadFixMonitoring(append)",
		"  function fixMonitoringErrorMessage(",
	)
	selectIssue := browserSourceBlock(
		t,
		app,
		"  function selectMonitoringIssue(summary, moveFocus)",
		"  function refreshSelectedMonitoringIssue(summary)",
	)
	refresh := browserSourceBlock(
		t,
		app,
		"  function refreshSelectedMonitoringIssue(summary)",
		"  function monitoringSummaryIssue(summary)",
	)

	checkIndex := strings.Index(list, `if (!viewCursor) {`)
	assignIndex := strings.Index(list, `bucket.data = data;`)
	if checkIndex < 0 || assignIndex < 0 || checkIndex > assignIndex {
		t.Fatal("monitoring list must reject a missing view cursor before replacing cached cards")
	}
	if !strings.Contains(list,
		`Local API returned monitoring results without a view cursor.`) {
		t.Error("monitoring list must identify its scoped missing-snapshot failure")
	}
	detail := browserSourceBlock(
		t,
		app,
		"  async function loadFixMonitoringDetail(",
		"  function loadMoreFixMonitoringDetail()",
	)
	detailCheckIndex := strings.Index(detail, `if (!monitoringViewCursor) {`)
	detailAssignIndex := strings.Index(detail, `state.fixHistory = history;`)
	if detailCheckIndex < 0 ||
		detailAssignIndex < 0 ||
		detailCheckIndex > detailAssignIndex {
		t.Error("monitoring detail must reject a missing view cursor before replacing connected attempt content")
	}
	if !strings.Contains(detail,
		`Local API returned attempt monitoring without a view cursor.`) {
		t.Error("monitoring detail must identify its scoped missing-snapshot failure")
	}
	for name, block := range map[string]string{
		"open":    selectIssue,
		"refresh": refresh,
	} {
		for _, required := range []string{
			`const monitoringViewCursor = readCursor(`,
			`if (!monitoringViewCursor) {`,
			`return Promise.resolve(false);`,
		} {
			if !strings.Contains(block, required) {
				t.Errorf("%s monitoring flow is missing fail-closed check %q", name, required)
			}
		}
		if strings.Contains(block, "loadFixMonitoringDetail(\n      issueID,\n      false,\n      state.fixMonitoring.viewCursor,") {
			t.Errorf("%s monitoring flow must not pass a missing top-level view cursor through as a fresh detail read", name)
		}
	}
	for _, required := range []string{
		`state.fixHistoryStale = true;`,
		`The open attempt was preserved`,
		`renderFixAttempts();`,
	} {
		if !strings.Contains(refresh, required) {
			t.Errorf("scoped monitoring refresh failure is missing %q", required)
		}
	}
}
