package localhttp

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestAttentionBrowserShellContract(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")

	for _, required := range []string{
		`<nav class="primary-nav" aria-label="Belay Local views">`,
		`id="nav-attention"`,
		`aria-current="page"`,
		`id="nav-sessions"`,
		`id="attention-view"`,
		`id="sessions-view" hidden`,
		`id="issue-list"`,
		`id="issues-pagination"`,
		`id="evidence-gap-list"`,
		`id="evidence-gaps-pagination"`,
		`id="issue-detail-heading" tabindex="-1"`,
		`id="occurrence-list"`,
		`id="occurrences-pagination"`,
		`id="findings-pagination"`,
		`id="findings-load-more"`,
		`id="attention-refresh-notice"`,
		`aria-live="polite"`,
		`Include experimental findings`,
		`Precision is still being validated.`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("Attention browser shell is missing %q", required)
		}
	}

	idPattern := regexp.MustCompile(`\sid="([^"]+)"`)
	seen := make(map[string]struct{})
	for _, match := range idPattern.FindAllStringSubmatch(index, -1) {
		if _, duplicate := seen[match[1]]; duplicate {
			t.Errorf("browser shell contains duplicate id %q", match[1])
		}
		seen[match[1]] = struct{}{}
	}
}

func TestAttentionBrowserFrozenContract(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")

	catalog := map[string][]string{
		"issue.explicit_command_failure": {
			"Command failed",
			"The agent reported that a command failed.",
		},
		"issue.explicit_tool_failure": {
			"Tool call failed",
			"The agent reported that a tool call failed.",
			"Belay does not know why it failed or whether a later attempt succeeded.",
		},
		"issue.repeated_command_attempts": {
			"Command repeatedly attempted",
			"Belay recorded the same minimized command pattern several times close together.",
		},
		"issue.explicit_permission_denial": {
			"Permission denied",
			"The agent reported that a permission request was denied.",
		},
		"issue.verification_not_observed": {
			"No recognized verification command observed",
			"After a recorded file change, Belay did not see a test or verification command it recognizes before the session ended.",
		},
		"issue.retained_verification_gap_after_changes": {
			"No recognized verification retained after changes",
			"Belay's retained evidence contains no recognized verification command after the final recorded file change and before the session ended.",
			"This does not show that verification did not occur; Belay only checks supported commands in retained activity.",
		},
		"issue.unresolved_verification_failure_at_completion": {
			"Verification still failed at session end",
			"A verification command explicitly failed and no later successful verification was observed before session end.",
		},
		"issue.numbat_finding": {
			"Imported finding without an explanation",
			"Belay stored an imported finding, but no reviewed Belay explanation is available for it.",
		},
	}
	for code, values := range catalog {
		if !strings.Contains(app, `"`+code+`"`) {
			t.Errorf("fixed issue catalog is missing %q", code)
		}
		for _, value := range values {
			if !strings.Contains(app, value) {
				t.Errorf("fixed issue catalog entry %q is missing %q", code, value)
			}
		}
	}

	for _, required := range []string{
		`issues: createAttentionFamilyBucket()`,
		`evidenceGaps: createIssueBucket("evidence_gap")`,
		"`/v1/attention-families?${new URLSearchParams({ cursor }).toString()}`",
		"`/v1/attention-families/${encodeURIComponent(",
		`view_cursor: state.selectedFamilyViewCursor`,
		`readText(family.kind) === "exact_issue"`,
		`kind === "mapped_upstream"`,
		`attention_kind: kind`,
		`experimental: state.issueFilters.experimental ? "include" : "stable"`,
		`bucket.viewCursor = viewCursor;`,
		`parameters.set("view_cursor", viewCursor)`,
		"`/v1/issues/${encodeURIComponent(issueID)}/occurrences?${parameters.toString()}`",
		`eventIDs.forEach((eventID) => parameters.append("event_id", eventID));`,
		"`/v1/sessions/${encodeURIComponent(sessionID)}/events/lookup?${parameters.toString()}`",
		`occurrenceEventIDs(occurrence).slice(0, 50)`,
		`error.status === 410`,
		`error.problemType === "belay.local/cursor-expired"`,
		`title: "Detected issue"`,
		`return /^[a-z0-9_]{1,64}$/.test(code)`,
		`return "Evidence completeness is unavailable.";`,
		`: "unknown";`,
		`"This result may be out of date while Belay analyzes the session again."`,
		`failed: "Belay could not refresh this result; it may be out of date."`,
		`"Only part of the session was analyzed; other findings may be missing."`,
		`"Belay cannot confirm whether this result is current or complete."`,
		`return "Analysis status unavailable";`,
		`return "Session count unavailable";`,
		`"No findings match these filters"`,
		`"No findings were reported in completed local analysis"`,
		`"No findings are available from completed analysis"`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("Attention browser data contract is missing %q", required)
		}
	}
}

func TestAttentionBrowserAccessibilityAndSafeRendering(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`element.inert = !visible;`,
		`element.setAttribute("aria-hidden", "true");`,
		`const focusRegistry = {`,
		`focusRegistry.issueCards.get(reference.key)`,
		`focusRegistry.occurrenceActions.get(reference.key)`,
		`focusRegistry.sessionCards.get(reference.sessionID)`,
		`clearIssueFocusRegistry(bucket.kind);`,
		`focusRegistry.occurrenceActions.clear();`,
		`focusRegistry.sessionCards.clear();`,
		`state.issueReturnFocus = {`,
		`key: issueFocusKey(state.selectedIssueKind, issueID),`,
		`state.sessionReturnFocus = { type: "session", sessionID };`,
		`state.sessionReturnFocus = { type: "occurrence", key: focusKey };`,
		`!element.isConnected`,
		`element.closest("[hidden]")`,
		`element.closest('[aria-hidden="true"]')`,
		`if (current.inert === true) return false;`,
		`focusCurrentElement(resolveFocusReference(reference))`,
		`returnToBrief`,
		`elements.briefHeading`,
		`elements.navAttention`,
		`inspect.setAttribute("aria-expanded", "false");`,
		`severity.dataset.tone = severityTone(issue.severity);`,
		`["critical", "high", "medium", "low", "info"].includes(severity)`,
		`["current", "pending", "failed", "truncated"].includes(status)`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("Attention accessibility/safety contract is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"issueOriginButton",
		"sessionOriginButton",
		"An immutable upstream Numbat rule emitted a finding.",
	} {
		if strings.Contains(app, forbidden) {
			t.Errorf("browser asset retains stale contract %q", forbidden)
		}
	}
	if got := strings.Count(app, ".focus();"); got != 1 ||
		!strings.Contains(app, "element.focus();") {
		t.Errorf("focus must be centralized behind current-DOM checks; source has %d direct focus calls", got)
	}
	for _, forbidden := range []string{
		"innerHTML",
		"outerHTML",
		"insertAdjacentHTML",
		"document.write",
		"eval(",
	} {
		if strings.Contains(app, forbidden) {
			t.Errorf("browser asset contains unsafe rendering primitive %q", forbidden)
		}
	}
	for _, required := range []string{
		`.primary-nav-button[aria-current="page"]`,
		`body.is-attention-detail-open .attention-detail-panel`,
		`@media (prefers-reduced-motion: reduce)`,
		`@media (max-height: 700px)`,
		`height: 100dvh;`,
		`overscroll-behavior: contain;`,
		`min-height: 44px;`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("Attention styles are missing %q", required)
		}
	}
	for _, block := range []struct {
		start string
		end   string
	}{
		{".primary-nav-button {", ".primary-nav-button:hover {"},
		{".issue-card-main {", `.issue-card-main[aria-pressed="true"] {`},
		{".fingerprint-panel button,", ".primary-button {"},
		{".pagination-bar button {", ".pagination-bar button:hover {"},
	} {
		section := browserSourceBlock(t, styles, block.start, block.end)
		if !strings.Contains(section, "min-height: 44px;") {
			t.Errorf("primary touch target %q is smaller than 44px", block.start)
		}
	}
}

func TestAttentionRefreshClosesStaleDetailAndConfirmsCurrentChain(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	refresh := browserSourceBlock(
		t,
		app,
		"  async function refreshAttention(preserveSelection, suppressFailureNotice = false) {",
		"  async function loadIssuePagesForSelection(",
	)

	for _, required := range []string{
		`closeIssueDetail(false, true);`,
		`clearAttentionFamilyDetailState();`,
		`const [issuesReady, gapsReady, monitoringReady] = await Promise.all([`,
		`const refreshGeneration = ++state.attentionRefreshGeneration;`,
		`if (refreshGeneration !== state.attentionRefreshGeneration) return false;`,
		`if (!issuesReady || !gapsReady) {`,
		`const chainReady = await loadFamilyPagesForSelection(`,
		`const memberChainReady = await loadFamilyMemberPagesForSelection(`,
		`const summary = state.issues.data.find(`,
		`if (!summary) {`,
		`detailReady = await selectAttentionFamily(summary, false);`,
		`detailReady = await selectIssue(`,
		`the selected item was not found in the sessions currently loaded`,
		`the selected finding is no longer visible in this group`,
		`if (!detailReady) {`,
		`selected detail is hidden until current data confirms it.`,
	} {
		if !strings.Contains(refresh, required) {
			t.Errorf("current-chain refresh contract is missing %q", required)
		}
	}
	if strings.Contains(refresh, "Promise.allSettled") {
		t.Error("Attention refresh cannot treat failed required reads as success")
	}
	closeIndex := strings.Index(refresh, "closeIssueDetail(false, true);")
	clearFamilyIndex := strings.Index(refresh, "clearAttentionFamilyDetailState();")
	readIndex := strings.Index(refresh, "await Promise.all([")
	selectIndex := strings.Index(refresh, "await selectAttentionFamily(")
	missingIndex := strings.Index(refresh, "if (!summary) {")
	if closeIndex < 0 || readIndex < 0 || closeIndex > readIndex {
		t.Error("selected detail is not closed before fresh list reads")
	}
	if clearFamilyIndex < 0 || readIndex < 0 || clearFamilyIndex > readIndex {
		t.Error("selected family detail is not cleared before fresh list reads")
	}
	if missingIndex < 0 || selectIndex < 0 || missingIndex > selectIndex {
		t.Error("selected detail can be retained without confirming list visibility")
	}
}

func TestAttentionFamilyCoverageFiltersAndBoundedChildRestoration(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	loadFamilies := browserSourceBlock(
		t,
		app,
		"  async function loadAttentionFamilies(bucket, append) {",
		"  function buildAttentionFamilyPath(cursor) {",
	)
	filters := browserSourceBlock(
		t,
		app,
		"  function renderAttentionFilters() {",
		"  function attentionFiltersActive() {",
	)
	restore := browserSourceBlock(
		t,
		app,
		"  async function loadFamilyMemberPagesForSelection(issueID, pageBudget) {",
		"  function resetAndLoadAttention() {",
	)

	for _, required := range []string{
		`response.global_analysis_coverage`,
		`readGlobalAnalysisCoverage(response.analysis, bucket.analysis)`,
	} {
		if !strings.Contains(loadFamilies, required) {
			t.Errorf("family global coverage fallback is missing %q", required)
		}
	}
	for _, required := range []string{
		`Displayed finding and evidence-gap counts match the active filters.`,
		`The analysis summary still covers all stored sessions in this saved view.`,
	} {
		if !strings.Contains(filters, required) {
			t.Errorf("filtered family count disclosure is missing %q", required)
		}
	}
	if strings.Contains(filters, "all exact occurrences visible") {
		t.Error("filtered family copy still claims unfiltered occurrence totals")
	}
	for _, required := range []string{
		`for (let page = 1; page < pageBudget; page += 1)`,
		`state.familyMembers.some(`,
		`!state.familyMemberHasMore`,
		`await loadAttentionFamilyDetail(true)`,
	} {
		if !strings.Contains(restore, required) {
			t.Errorf("bounded child restoration is missing %q", required)
		}
	}
}

func TestCursorRefreshNoticeWaitsForRequiredReads(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	refresh := browserSourceBlock(
		t,
		app,
		"  async function refreshAttentionAfterExpiry() {",
		"  function showAttentionNotice(",
	)

	for _, required := range []string{
		`"The finding view changed. Refreshing Attention with current data…"`,
		`const refreshed = await refreshAttention(false, true);`,
		`if (refreshed) {`,
		`"Attention refreshed with current data."`,
		`"Attention refresh failed. Retry before relying on the issue lists."`,
	} {
		if !strings.Contains(refresh, required) {
			t.Errorf("cursor refresh status contract is missing %q", required)
		}
	}
	awaitIndex := strings.Index(refresh, "await refreshAttention(false, true)")
	successIndex := strings.Index(refresh, `"Attention refreshed with current data."`)
	if awaitIndex < 0 || successIndex < 0 || successIndex < awaitIndex {
		t.Error("cursor refresh reports success before required reads complete")
	}
}

func TestAttentionBrowserUsesCursorOnlyContinuationAndAdditiveMetadata(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	listPath := browserSourceBlock(
		t,
		app,
		"  function buildIssuePath(kind, cursor) {",
		"  function renderIssueBucket(bucket) {",
	)
	detail := browserSourceBlock(
		t,
		app,
		"  async function loadIssueDetail(append, viewCursor) {",
		"  function loadMoreOccurrences() {",
	)

	for _, required := range []string{
		`if (cursor) {`,
		"return `/v1/issues?${new URLSearchParams({ cursor }).toString()}`;",
		`const parameters = new URLSearchParams({`,
		`attention_kind: kind,`,
		`experimental: state.issueFilters.experimental ? "include" : "stable",`,
	} {
		if !strings.Contains(listPath, required) {
			t.Errorf("issue-list cursor-v2 request contract is missing %q", required)
		}
	}
	cursorBranch := listPath[:strings.Index(listPath, "    const parameters =")]
	for _, forbidden := range []string{"limit:", "attention_kind:", "experimental:"} {
		if strings.Contains(cursorBranch, forbidden) {
			t.Errorf("issue-list continuation still sends %q", forbidden)
		}
	}

	for _, required := range []string{
		`const parameters = cursor`,
		`? new URLSearchParams({ cursor })`,
		`: new URLSearchParams({`,
		`limit: String(pageLimits.occurrences.page),`,
		`const pagination = requireCursorPage(`,
		`const responseViewCursor = readCursor(response.view_cursor);`,
		`response.catalog`,
		`response.global_analysis_coverage`,
	} {
		if !strings.Contains(detail, required) {
			t.Errorf("issue-detail cursor-v2/additive contract is missing %q", required)
		}
	}
	if strings.Contains(detail, `parameters.set("cursor"`) {
		t.Error("issue occurrence continuation mutates a fresh-request parameter set")
	}

	for _, required := range []string{
		`bucket.selection = selection;`,
		`function readIssueSelection(value, kind)`,
		`function readIssueCatalog(value, issue, previous)`,
		`function readGlobalAnalysisCoverage(value, fallback)`,
		`function globalCoverageQualifier(coverage)`,
		`"belay.issue-explanations.v1"`,
		`"belay.source-signals.v1"`,
		`"review_agent_permissions"`,
		`"inspect_cited_events"`,
		`"inspect_matching_sessions"`,
		`"inspect_verification_events"`,
		`Local API returned issue results without a view cursor.`,
		`Local API returned issue detail without a view cursor.`,
		`hasMore !== Boolean(nextCursor)`,
		`currentCursor && nextCursor === currentCursor`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("browser metadata/cursor consistency contract is missing %q", required)
		}
	}
}

func TestAttentionFamilyBrowserListDetailAndExactChildContract(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, forbidden := range []string{
		`id="issue-filter-recurrence"`,
		`id="issue-filter-category"`,
		`Session spread`,
		`state.issueFilters.recurrence`,
		`state.issueFilters.category`,
		`parameters.set("recurrence"`,
		`parameters.set("category"`,
	} {
		if strings.Contains(index, forbidden) || strings.Contains(app, forbidden) {
			t.Errorf("default Attention retains removed filter contract %q", forbidden)
		}
	}

	for _, required := range []string{
		`function buildAttentionFamilyPath(cursor)`,
		"return `/v1/attention-families?${new URLSearchParams({ cursor }).toString()}`;",
		`view_cursor: state.selectedFamilyViewCursor`,
		`? new URLSearchParams({ cursor })`,
		`async function selectAttentionFamily(`,
		`revealDetail = true`,
		`readText(family.kind) === "exact_issue"`,
		`kind === "mapped_upstream"`,
		`toFiniteNumber(family.supporting_issue_count) === 1`,
		`selectSingleMemberAttentionFamily(family, true)`,
		`selectAttentionFamily(family, false, false)`,
		`state.familyMembers.length !== 1`,
		`state.familyMemberHasMore`,
		`source: "family"`,
		`returnFocus: { type: "family-member", key }`,
		`const catalog = isRecord(family.catalog) ? family.catalog : {};`,
		`with this finding`,
		`cited_event_count`,
		`session_selection`,
		`latest_matching_session`,
		`session_started_at`,
		`session_last_active_at`,
		`evidence_first_at`,
		`evidence_last_at`,
		`Belay grouped these records because the same permission-mode finding appeared. The sessions may be unrelated.`,
		`Sessions with this finding could not be loaded. Refresh Attention and try again.`,
		`clearAttentionFamilyDetailState();`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("Attention family browser contract is missing %q", required)
		}
	}
	if strings.Contains(app, "function attentionFamilyCatalog(") {
		t.Error("browser overrides the authoritative backend family catalog")
	}
	familyLoad := browserSourceBlock(
		t,
		app,
		"  async function loadAttentionFamilyDetail(append) {",
		"  function requireAttentionFamilyMember(member) {",
	)
	if strings.Contains(familyLoad, "error.message") {
		t.Error("family detail exposes raw API/cursor/snapshot errors")
	}
	for _, required := range []string{
		`id="family-detail" hidden`,
		`id="family-detail-heading" tabindex="-1"`,
		`id="family-member-list"`,
		`id="family-members-load-more"`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("Attention family shell is missing %q", required)
		}
	}
	for _, required := range []string{
		`.family-detail`,
		`.family-member-card`,
		`min-height: 44px;`,
		`overflow-wrap: anywhere;`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("Attention family styles are missing %q", required)
		}
	}
}

func TestAttentionConfigAgentCopyIsScopedToMappedCitedEvidence(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	descriptor := browserSourceBlock(
		t,
		app,
		"  function eventDescriptor(observation, evidenceContext = \"\") {",
		"  function isSignalEvent(event, cited) {",
	)

	for _, required := range []string{
		`"config.agent": "Agent configuration observed"`,
		`evidenceContext === "mapped-guardrail"`,
		`? "Fewer approval prompts enabled"`,
		`Belay recorded a setting that lets actions already permitted by the agent run without asking for approval each time.`,
		`Agent configuration metadata was reported.`,
		`createLookupEvent(event, state.selectedIssueEvidenceContext)`,
		`attention.agent_guardrails_configuration`,
		`sourceSignalCode === "tamper.guardrails_off"`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("contextual config.agent presentation is missing %q", required)
		}
	}
	if !strings.Contains(descriptor, `["config.agent", "session.start"].includes(type) &&`) ||
		!strings.Contains(descriptor, `evidenceContext === "mapped-guardrail"`) {
		t.Error("reduced-approval copy is not gated by cited event type and finding context")
	}
	genericTimeline := browserSourceBlock(
		t,
		app,
		"  function createEventRow(event, findings) {",
		"  function createEvidenceDetails(event) {",
	)
	if !strings.Contains(genericTimeline, `eventDescriptor(observation);`) {
		t.Error("generic session timeline no longer uses the neutral event descriptor")
	}
	if strings.Contains(genericTimeline, "mapped-guardrail") {
		t.Error("generic session timeline opts into reduced-approval evidence copy")
	}
}

func TestReducedApprovalFindingUsesPlainCustomerCopy(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		`title: "Fewer approval prompts enabled"`,
		`Belay recorded a setting that lets actions already permitted by the agent run without asking for approval each time.`,
		`This setting may be intentional. The record does not show whether an action bypassed a prompt or caused harm.`,
		`Review the current agent permission mode. If this was intentional, no change may be needed.`,
		`"Fewer approval prompts enabled"`,
		`elements.issueTechnicalDetails.hidden =`,
		`reducedApprovalMode || historyOnly || currentProjectionPending`,
		`elements.fixAttemptsSection.hidden = true;`,
		`elements.recordFixAttempt.hidden = true;`,
		`function consolidateSessionFindings(findings)`,
		`new Set(`,
		`group.cited_event_ids.concat(findingEventIDs(finding))`,
		`evidence_first_at`,
		`evidence_last_at`,
		`additional imported`,
		`hidden because Belay does not yet have a clear explanation`,
		`Outcome not reported`,
		`Imported history`,
	} {
		if !strings.Contains(app+index, required) {
			t.Errorf("plain-language finding contract is missing %q", required)
		}
	}

	member := browserSourceBlock(
		t,
		app,
		"  function createAttentionFamilyMember(member) {",
		"  function attentionEvidenceWindow(member) {",
	)
	for _, required := range []string{
		`session_started_at`,
		`session_last_active_at`,
		`session_selection`,
		`latest_matching_session`,
		`Latest of ${formatNumber(issueSessionCount)} matching sessions`,
		`Session start unavailable`,
		`cited_event_count`,
		`cited ${`,
	} {
		if !strings.Contains(member, required) {
			t.Errorf("family member presentation is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`compactID(sessionID)`,
		`Session identifier unavailable`,
		`issue.first_observed_at`,
		`operator choice`,
	} {
		if strings.Contains(member, forbidden) {
			t.Errorf("family member exposes customer-irrelevant identifier copy %q", forbidden)
		}
	}

	metadata := browserSourceBlock(
		t,
		app,
		"  function renderIssueMetadata(issue) {",
		"  function issueSourceLabel(value) {",
	)
	if !strings.Contains(metadata, `isReducedApprovalIssueContext(issue, state.selectedIssueCatalog)`) ||
		!strings.Contains(metadata, `elements.issueMetadata.replaceChildren();`) {
		t.Error("reduced-approval detail does not clear technical metadata")
	}
	for _, forbidden := range []string{`Source rule ID`, `source_signal_code`} {
		if strings.Contains(metadata, forbidden) {
			t.Errorf("reduced-approval detail metadata retains %q", forbidden)
		}
	}

	for _, forbidden := range []string{
		"Safety confirmations may be turned off",
		"Agent guardrail configuration observed",
		"Configured Numbat rule finding",
		"Source rule ID ·",
		"Deterministic signals",
		"Outcome unavailable",
		"Reconstructed",
		"Opaque exact-match identifier",
	} {
		if strings.Contains(app, forbidden) || strings.Contains(index, forbidden) {
			t.Errorf("browser retains prohibited customer copy %q", forbidden)
		}
	}
}

func TestBrowserClarityDefaultSurfaceAndSafeErrors(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	app := readBrowserAsset(t, "assets/app.js")

	for _, required := range []string{
		`<span>Agent name</span>`,
		`placeholder="Exact agent name"`,
		`placeholder="Search session ID or agent name"`,
		`<span>Agent</span>`,
		`<option value="">All agents</option>`,
		`<span>Collection</span>`,
		`<option value="">Live + imported history</option>`,
		`<option value="">Any outcome</option>`,
		`<option value="reported">Outcome reported</option>`,
		`<option value="unavailable">Outcome not reported</option>`,
		`Recorded activity`,
		`Worth a look`,
		`Key moments`,
		`Up to five notable events from the loaded timeline.`,
		`Files and resources`,
		`All loaded events`,
		`Private change record`,
		`Changes you recorded`,
		`Correct attempt history`,
		`Retract attempt record`,
		`Include experimental findings`,
		`Showing reviewed findings.`,
		`Follow-up evidence`,
		`Same finding observed later`,
		`Waiting for later sessions`,
		`Later sessions cannot be compared`,
		`Agent monitoring setup`,
		`Sessions where Belay did not see expected verification are`,
		`listed separately from other findings.`,
		`Findings and evidence gaps`,
		`Exact-match ID`,
		`Loading timeline…`,
		`Discard pending submission`,
		`Discard pending retraction`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("plain default browser surface is missing %q", required)
		}
	}

	for _, required := range []string{
		`"Minimized summary"`,
		`"Detected secrets removed"`,
		`"Collection detail"`,
		`function collectionDetailLabel(value)`,
		`"Imported activity metadata"`,
		`"Live agent activity"`,
		`"Tool-call activity"`,
		`"Tracing activity"`,
		`"Activity metadata"`,
		`"Complete session summary"`,
		`Partial session summary ·`,
		`"Loading the complete session summary; counts below use the events loaded so far."`,
		`"More findings are available. Use Load more to view them."`,
		`"Belay recorded a finding that does not yet have a plain-language explanation."`,
		`Last retained data received by Belay`,
		`User input recorded — content not retained`,
		`Assistant response recorded — content not retained`,
		`Reasoning started — content not retained`,
		`Reasoning ended — content not retained`,
		`notable loaded events`,
		`lower-priority lifecycle events hidden`,
		`This page was not opened from a valid Belay Local link. Reopen it using the URL printed by belay local.`,
		`Imported finding without an explanation`,
		`no reviewed Belay explanation is available for it`,
		`"Fewer approval prompts enabled"`,
		`"Project identified"`,
		`"Project not identified"`,
		`"Project information conflicts"`,
		`return "Unavailable";`,
		`? "Imported local check"`,
		`"Load more sessions before treating this outcome filter as complete."`,
		`"Filters were applied across all stored sessions."`,
		`"Showing recent sessions. Load more to see older sessions."`,
		`"History source", "Imported history"`,
		`"No cited events were provided for this finding."`,
		`"The cited events are no longer stored."`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("plain browser behavior is missing %q", required)
		}
	}

	for _, forbidden := range []string{
		">Harness<",
		">Capture<",
		"All harnesses",
		"Deterministic overview",
		"What Belay observed",
		"Needs attention",
		"Work observed",
		"Key sequence",
		"Resources touched",
		"Belay reconstructs",
		">All events<",
		"Developer declarations",
		"Private Local declaration",
		"Record declaration",
		"Append-only correction",
		"Retract declaration",
	} {
		if strings.Contains(index, forbidden) {
			t.Errorf("default browser surface retains jargon %q", forbidden)
		}
	}
	for _, forbidden := range []string{
		`"Safe summary"`,
		`"Secrets removed"`,
		`"Data current `,
		`signal events`,
		`repetitive lifecycle events`,
		`configured checks`,
		`API projection`,
		`salient resources`,
		`exact active state`,
		`payload-free monitoring metadata`,
		`A local check reported activity that Belay can explain.`,
		`"Capture depth"`,
		`"Full-session metadata"`,
		`Partial overview ·`,
		`"Loading full-session overview`,
		`More findings are available through explicit pagination.`,
		`A configured Belay check reported local evidence.`,
		`No evidence gaps reported by configured detectors`,
		`private command signature`,
		`The source explicitly reported a failed command result.`,
		`The source explicitly reported a denied permission event.`,
		`This explanation comes from Belay's reviewed finding descriptions`,
		`Reduced-approval permission mode used`,
		`Reduced-approval permission mode observed`,
		`Verification evidence not observed`,
		`Prior retained result`,
		`Partial analysis; additional signals`,
		`Experimental signal`,
		`Stable signals`,
		`selected signal`,
		`selected affected record`,
		`Post-attempt evidence`,
		`retained cited event ID`,
		`No requested cited event remains in this retained session`,
		`Outcome · Not reported by source`,
	} {
		if strings.Contains(app, forbidden) {
			t.Errorf("browser-rendered copy retains jargon %q", forbidden)
		}
	}

	issueCard := browserSourceBlock(
		t,
		app,
		"  function createIssueCard(issue, kind) {",
		"  function selectIssue(issue, kind, moveFocus, options = {}) {",
	)
	if strings.Contains(issueCard, `safeCatalogCode(issue.category)`) {
		t.Error("default issue card exposes a raw category code")
	}

	detectorVersion := browserSourceBlock(
		t,
		app,
		"  function detectorVersionLabel(issue) {",
		"  function projectRelationshipLabel(value) {",
	)
	if strings.Contains(detectorVersion, "detector_id") ||
		strings.Contains(detectorVersion, "Configured detector") {
		t.Error("check version display exposes a detector identifier")
	}

	evidenceDetails := browserSourceBlock(
		t,
		app,
		"  function createEvidenceDetails(event) {",
		"  function appendEvidence(list, label, value) {",
	)
	if strings.Contains(evidenceDetails, "reconstruction_source") ||
		strings.Contains(evidenceDetails, "Reconstruction") {
		t.Error("history evidence exposes reconstruction protocol metadata")
	}

	errorPresentation := browserSourceBlock(
		t,
		app,
		"  function showError(title, error) {",
		"  function hideError() {",
	)
	for _, required := range []string{
		`customerErrorMessage(`,
		`(api|schema|cursor|snapshot|projection|uuid|idempotency|mutation|listener)`,
		`return fallback;`,
	} {
		if !strings.Contains(errorPresentation, required) {
			t.Errorf("central customer-safe error mapping is missing %q", required)
		}
	}
	if strings.Contains(errorPresentation, `elements.errorDetail.textContent = error.message`) {
		t.Error("central error banner exposes raw error text")
	}
}

func TestValueFirstAttentionAndSessionContracts(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`id="stable-issues-section"`,
		`id="evidence-gaps-section"`,
		`id="fix-monitoring-section"`,
		`id="issue-evidence-preview"`,
		`id="issue-evidence-preview-all"`,
		`id="issue-technical-details"`,
		`id="fix-attempts-section"`,
		`<summary>Technical details</summary>`,
		`id="needs-attention-summary"`,
		`id="observed-work-summary"`,
		`id="session-highlights"`,
		`Up to five notable events from the loaded timeline.`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("value-first browser shell is missing %q", required)
		}
	}

	layout := browserSourceBlock(
		t,
		app,
		"  function prepareValueFirstAttentionLayout() {",
		"  function bindEvents() {",
	)
	for _, required := range []string{
		`elements.stableIssuesSection,`,
		`elements.evidenceGapsSection,`,
		`elements.fixMonitoringSection,`,
		`analysisDisclosure,`,
		`filterDisclosure,`,
	} {
		if !strings.Contains(layout, required) {
			t.Errorf("first-viewport Attention order is missing %q", required)
		}
	}
	if issues, gaps, monitoring := strings.Index(layout, "elements.stableIssuesSection"), strings.Index(layout, "elements.evidenceGapsSection"), strings.Index(layout, "elements.fixMonitoringSection"); issues < 0 || gaps < issues || monitoring < gaps {
		t.Error("Attention sections are not ordered Issues, Evidence gaps, After attempts")
	}

	preview := browserSourceBlock(
		t,
		app,
		"  async function loadIssueEvidencePreview(",
		"  function renderIssueEvidencePreview() {",
	)
	for _, required := range []string{
		`expanded ? pageLimits.events.maximum / 10 : 3`,
		`const generation = ++state.issueEvidencePreviewRequestGeneration;`,
		`generation !== state.issueEvidencePreviewRequestGeneration`,
		`issueID !== state.selectedIssueID`,
		`occurrenceID !== state.issueEvidencePreview.occurrenceID`,
		`eventIDs.forEach((eventID) => parameters.append("event_id", eventID));`,
	} {
		if !strings.Contains(preview, required) {
			t.Errorf("bounded cancellable evidence preview is missing %q", required)
		}
	}
	for _, required := range []string{
		`state.issueEvidencePreviewRequestGeneration += 1;`,
		`state.issueEvidencePreview = createIssueEvidencePreview();`,
		`resetIssueEvidencePreview();`,
		`No cited events were provided for this finding.`,
		`The cited events are no longer stored.`,
		`Some cited events are no longer stored.`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("evidence preview reset/disclosure is missing %q", required)
		}
	}

	for _, required := range []string{
		`elements.recordFixAttempt.hidden =`,
		`serverReportedIneligible`,
		`const hasDurableHistory =`,
		`const showFixSection = canResume || canRecord || hasDurableHistory;`,
		`elements.fixAttemptsSection.hidden = !showFixSection;`,
		`!canRecord ||`,
		`renderFixHistory();`,
		`selectSessionHighlights(state.events)`,
		`.slice(0, 5)`,
		`left.priority - right.priority || left.index - right.index`,
		`.sort((left, right) => left.index - right.index)`,
		`type.startsWith("command.")`,
		`? commandDisplayDetail(observation)`,
		`: resourceName || summary`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("value-first behavior is missing %q", required)
		}
	}
	if strings.Contains(
		browserSourceBlock(
			t,
			app,
			"  function createEventRow(",
			"  function createEvidenceDetails(",
		),
		`"event-type"`,
	) {
		t.Error("raw event type remains in the primary timeline heading")
	}

	for _, required := range []string{
		`"command.result": "Command result"`,
		`function commandDisplayDetail(observation)`,
		`if (type === "command.result" && commandDisplayDetail(observation)) return 3;`,
		`if (type === "command.result") {`,
		`return Boolean(commandDisplayDetail(observation));`,
		`"command completed"`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("command timeline cleanup is missing %q", required)
		}
	}
	if strings.Contains(app, `"command.result": "Command completed"`) {
		t.Error("generic Command completed remains a primary timeline label")
	}

	familyDetail := browserSourceBlock(
		t,
		index,
		`<div class="family-detail" id="family-detail" hidden>`,
		`<div class="issue-detail" id="issue-detail" hidden>`,
	)
	if strings.Contains(familyDetail, `fix-attempt`) {
		t.Error("mapped family detail exposes an exact fix workflow")
	}

	for _, required := range []string{
		`"config.agent": "Agent configuration observed"`,
		`? "Fewer approval prompts enabled"`,
		`Belay recorded a setting that lets actions already permitted by the agent run without asking for approval each time.`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("fixed config.agent presentation is missing %q", required)
		}
	}

	for _, required := range []string{
		`.attention-disclosure`,
		`.issue-evidence-preview`,
		`.technical-details`,
		`.session-highlights`,
		`max-height: none;`,
		`.event-scroll,`,
		`overflow: visible;`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("value-first accessibility/mobile style is missing %q", required)
		}
	}
}

func TestAttentionEvidencePreview410FullyResetsState(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	reset := browserSourceBlock(
		t,
		app,
		"  function resetIssueEvidencePreview() {",
		"  async function loadIssueEvidencePreview(",
	)
	preview := browserSourceBlock(
		t,
		app,
		"  async function loadIssueEvidencePreview(",
		"  function renderIssueEvidencePreview() {",
	)

	for _, required := range []string{
		`issueEvidencePreviewController: null,`,
		`state.issueEvidencePreviewController.abort();`,
		`state.issueEvidencePreviewController = null;`,
		`state.issueEvidencePreview = createIssueEvidencePreview();`,
		`renderIssueEvidencePreview();`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("evidence-preview reset contract is missing %q", required)
		}
	}
	for _, required := range []string{
		`state.issueEvidencePreviewRequestGeneration += 1;`,
		`state.issueEvidencePreviewController.abort();`,
		`state.issueEvidencePreviewController = null;`,
		`state.issueEvidencePreview = createIssueEvidencePreview();`,
		`renderIssueEvidencePreview();`,
	} {
		if !strings.Contains(reset, required) {
			t.Errorf("evidence-preview reset does not clear %q", required)
		}
	}
	for _, required := range []string{
		`const controller = new AbortController();`,
		`state.issueEvidencePreviewController = controller;`,
		`controller.signal,`,
		`controller !== state.issueEvidencePreviewController`,
		`controller.signal.aborted`,
		`if (isCursorExpired(error)) {`,
		`resetIssueEvidencePreview();`,
		`await refreshAttentionAfterExpiry();`,
	} {
		if !strings.Contains(preview, required) {
			t.Errorf("evidence-preview 410 recovery is missing %q", required)
		}
	}
	expiry := strings.Index(preview, `if (isCursorExpired(error)) {`)
	staleError := strings.Index(
		preview,
		`state.issueEvidencePreview.status = "error";`,
	)
	if expiry < 0 || staleError < 0 || expiry > staleError {
		t.Error("evidence-preview 410 must reset and return before stale error state is rendered")
	}
}

func TestAttentionBrowserRejectsContinuationViewCursorMismatchBeforeMutation(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	list := browserSourceBlock(
		t,
		app,
		"  async function loadIssueBucket(bucket, append) {",
		"  function buildIssuePath(kind, cursor) {",
	)
	detail := browserSourceBlock(
		t,
		app,
		"  async function loadIssueDetail(append, viewCursor) {",
		"  function loadMoreOccurrences() {",
	)

	listCheck := `if (cursor && viewCursor !== bucket.viewCursor) {`
	listError := `Local API changed the issue-list view cursor during continuation.`
	listMutation := `bucket.data =`
	for _, required := range []string{listCheck, listError} {
		if !strings.Contains(list, required) {
			t.Errorf("issue-list continuation mismatch handling is missing %q", required)
		}
	}
	if check, mutation := strings.Index(list, listCheck), strings.Index(list, listMutation); check < 0 || mutation < 0 || check > mutation {
		t.Error("issue-list view cursor mismatch must fail before cached rows are mutated")
	}

	detailCheck := `responseViewCursor !== state.selectedIssueViewCursor`
	detailError := `Local API changed the issue-detail view cursor during continuation.`
	detailMutation := `state.selectedIssue = issue;`
	for _, required := range []string{detailCheck, detailError} {
		if !strings.Contains(detail, required) {
			t.Errorf("issue-detail continuation mismatch handling is missing %q", required)
		}
	}
	if check, mutation := strings.Index(detail, detailCheck), strings.Index(detail, detailMutation); check < 0 || mutation < 0 || check > mutation {
		t.Error("issue-detail view cursor mismatch must fail before detail state is mutated")
	}
}

func TestAttentionBrowserCursorExpiryClearsEveryDependentState(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	refresh := browserSourceBlock(
		t,
		app,
		"  async function refreshAttentionAfterExpiry() {",
		"  function showAttentionNotice(",
	)
	clearState := browserSourceBlock(
		t,
		app,
		"  function clearExpiredIssueSnapshotState() {",
		"  function clearExpiredFixDraftState() {",
	)
	clearDrafts := browserSourceBlock(
		t,
		app,
		"  function clearExpiredFixDraftState() {",
		"  function showAttentionNotice(",
	)
	closeDetail := browserSourceBlock(
		t,
		app,
		"  function closeIssueDetail(",
		"  async function refreshAttentionAfterExpiry()",
	)

	if !strings.Contains(refresh, "clearExpiredIssueSnapshotState();") {
		t.Fatal("410 recovery does not clear dependent state before refresh")
	}
	for _, required := range []string{
		`resetIssueBucket(state.issues);`,
		`resetIssueBucket(state.evidenceGaps);`,
		`resetFixMonitoringBucket(false);`,
		`clearExpiredFixDraftState();`,
		`closeIssueDetail(false, true);`,
		`clearAttentionFamilyDetailState();`,
	} {
		if !strings.Contains(clearState, required) {
			t.Errorf("410 state clearing is missing %q", required)
		}
	}
	for _, required := range []string{
		`draft.idempotencyKey = "";`,
		`draft.actionToken = "";`,
		`draft.attempted = false;`,
		`draft.unresolved = false;`,
		`draft.pending = false;`,
		`state.modalSubmitting = false;`,
	} {
		if !strings.Contains(clearDrafts, required) {
			t.Errorf("410 fix/action-token clearing is missing %q", required)
		}
	}
	for _, required := range []string{
		`state.selectedIssueCatalog = null;`,
		`state.selectedGlobalAnalysisCoverage = null;`,
		`state.selectedIssueViewCursor = "";`,
		`state.occurrenceNextCursor = "";`,
		`resetFixIssueState();`,
	} {
		if !strings.Contains(closeDetail, required) {
			t.Errorf("410 detail clearing is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"submitFixAttempt(",
		"apiMutation(",
		"selectIssue(",
	} {
		if strings.Contains(refresh, forbidden) ||
			strings.Contains(clearState, forbidden) ||
			strings.Contains(clearDrafts, forbidden) {
			t.Errorf("410 recovery must not automatically invoke %q", forbidden)
		}
	}
}

func TestAttentionUnknownCountsAndAnalysisStayUncertain(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	recurrence := browserSourceBlock(
		t,
		app,
		"  function issueRecurrenceLabel(issue) {",
		"  function issueHarnessLabel(",
	)
	analysis := browserSourceBlock(
		t,
		app,
		"  function normalizeAnalysisStatus(value) {",
		"  function safeCatalogCode(",
	)

	for _, required := range []string{
		`const sessions = Number(issue && issue.session_count);`,
		`if (!Number.isFinite(sessions) || sessions < 1) {`,
		`return "Session count unavailable";`,
	} {
		if !strings.Contains(recurrence, required) {
			t.Errorf("uncertain session-count contract is missing %q", required)
		}
	}
	if strings.Index(recurrence, `return "Session count unavailable";`) >
		strings.Index(recurrence, `"Observed in one session"`) {
		t.Error("missing or zero session count can be presented as one session")
	}
	for _, required := range []string{
		`: "unknown";`,
		`if (status === "unknown") return "Analysis status unavailable";`,
	} {
		if !strings.Contains(analysis, required) {
			t.Errorf("unknown analysis status contract is missing %q", required)
		}
	}
	for _, required := range []string{
		`elements.issueAnalysisQualifier.textContent =`,
		`analysisQualifiers[status] || "";`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("issue detail uncertainty qualifier is missing %q", required)
		}
	}
}

func TestAttentionDirectSessionOpeningFetchesBeforeRendering(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	openSession := browserSourceBlock(
		t,
		app,
		"  function openSession(sessionID, optionalKnownSummary) {",
		"  function beginSessionSelection(",
	)
	directLoad := browserSourceBlock(
		t,
		app,
		"  async function loadDirectSession(sessionID, generation) {",
		"  async function loadFindings(",
	)

	for _, required := range []string{
		`if (!known) {`,
		`void loadDirectSession(sessionID, generation);`,
		`beginSessionSelection(sessionID, known);`,
	} {
		if !strings.Contains(openSession, required) {
			t.Errorf("direct session opening is missing %q", required)
		}
	}
	fetchIndex := strings.Index(
		directLoad,
		"`/v1/sessions/${encodeURIComponent(sessionID)}`",
	)
	renderIndex := strings.Index(
		directLoad,
		"beginSessionSelection(sessionID, detail);",
	)
	if fetchIndex < 0 || renderIndex < 0 || fetchIndex > renderIndex {
		t.Error("an unloaded occurrence session is rendered before its detail fetch")
	}
	for _, required := range []string{
		`const returnFocus = state.sessionReturnFocus;`,
		`setActiveView("attention", false);`,
		`restoreLogicalFocus(`,
		`showError("Unable to open selected session", error);`,
	} {
		if !strings.Contains(directLoad, required) {
			t.Errorf("direct session failure handling is missing %q", required)
		}
	}
	activateIndex := strings.Index(directLoad, `setActiveView("attention", false);`)
	restoreIndex := strings.Index(directLoad, "restoreLogicalFocus(")
	if activateIndex < 0 || restoreIndex < 0 || activateIndex > restoreIndex {
		t.Error("direct session failure restores focus before the mobile pane is active")
	}
}

func TestSessionFindingsRemainBoundedAndExplicitlyPaginated(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	findings := browserSourceBlock(
		t,
		app,
		"  async function loadFindings(sessionID) {",
		"  function loadMoreFindings() {",
	)

	if got := strings.Count(findings, "await apiGet("); got != 1 {
		t.Fatalf("session findings page performs %d API requests; want exactly one", got)
	}
	for _, forbidden := range []string{"while (", "do {", "pageLimits.findings.maximum"} {
		if strings.Contains(findings, forbidden) {
			t.Errorf("session findings loader contains unbounded behavior %q", forbidden)
		}
	}
	for _, required := range []string{
		`findings: { page: 20 }`,
		`session_id: sessionID`,
		`parameters.set("cursor", cursor)`,
		`state.findingNextCursor`,
		`state.findingHasMore`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("bounded findings contract is missing %q", required)
		}
	}
}

func readBrowserAsset(t *testing.T, name string) string {
	t.Helper()
	body, err := fs.ReadFile(assetFiles, name)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func browserSourceBlock(t *testing.T, source, start, end string) string {
	t.Helper()
	startIndex := strings.Index(source, start)
	if startIndex < 0 {
		t.Fatalf("browser source is missing section start %q", start)
	}
	endOffset := strings.Index(source[startIndex:], end)
	if endOffset < 0 {
		t.Fatalf("browser source is missing section end %q", end)
	}
	return source[startIndex : startIndex+endOffset]
}
