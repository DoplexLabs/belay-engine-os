package localhttp

import (
	"strings"
	"testing"
)

func TestFixBrowserShellAndDisclosureContract(t *testing.T) {
	index := readBrowserAsset(t, "assets/index.html")

	for _, required := range []string{
		`id="fix-attempts-heading">Fix attempts`,
		`id="record-fix-attempt"`,
		`id="fix-eligibility-status"`,
		`id="fix-history-list"`,
		`id="fix-history-pagination"`,
		`id="fix-history-load-more"`,
		`id="fix-attempt-dialog"`,
		`role="dialog"`,
		`aria-modal="true"`,
		`aria-labelledby="fix-attempt-dialog-title"`,
		`aria-describedby="fix-attempt-dialog-description"`,
		`id="fix-retraction-dialog"`,
		`aria-labelledby="fix-retraction-dialog-title"`,
		`aria-describedby="fix-retraction-dialog-description"`,
		`Records that you attempted an external change.`,
		`Belay cannot verify`,
		`the change or its effect.`,
		`No note, command, path, diff, prompt, or output is`,
		`No free-text explanation is stored.`,
	} {
		if !strings.Contains(index, required) {
			t.Errorf("fix browser shell is missing %q", required)
		}
	}

	for _, disclosure := range []string{
		"Belay compares later local analysis for the same finding.",
		"Belay may check later local activity for the same finding.",
	} {
		if !strings.Contains(index, disclosure) {
			t.Errorf("P0-04 disclosure is missing %q", disclosure)
		}
	}
	if strings.Index(index, `id="matching-sessions-heading"`) >
		strings.Index(index, `id="fix-attempts-heading"`) {
		t.Error("value-first issue detail must show matching evidence before fix-attempt history")
	}
	if strings.Contains(index, " checked") {
		t.Error("fix/retraction dialog must not preselect a radio choice")
	}
	if strings.Contains(index, "<textarea") {
		t.Error("fix/retraction dialogs must not accept free text")
	}
}

func TestFixBrowserFrozenHTTPAndCatalogContract(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")

	for _, category := range []string{
		"code_change",
		"configuration_change",
		"dependency_change",
		"permission_change",
		"environment_change",
		"agent_instruction",
		"project_rule",
		"monitor_hook",
		"other",
	} {
		if !strings.Contains(app, `value: "`+category+`"`) {
			t.Errorf("fix-change.v1 catalog is missing %q", category)
		}
	}
	for _, definition := range []string{
		"Source or test code changed.",
		"Project/application configuration changed.",
		"Dependency version or lock state changed.",
		"Access or permission configuration changed.",
		"Local runtime, toolchain, or environment changed.",
		"User-level agent instruction changed.",
		"Repository/project agent rule changed.",
		"Agent monitoring hook/configuration changed.",
		"A deliberate category outside the listed choices.",
	} {
		if !strings.Contains(app, definition) {
			t.Errorf("fix-change.v1 catalog is missing definition %q", definition)
		}
	}
	for _, reason := range []string{
		"recorded_by_mistake",
		"superseded",
		"other",
	} {
		if !strings.Contains(app, `value: "`+reason+`"`) {
			t.Errorf("retraction catalog is missing %q", reason)
		}
	}
	for _, required := range []string{
		`fixMonitoringDetail: { page: 20 }`,
		"`/v1/issues/${encodeURIComponent(issueID)}/fix-eligibility?${parameters.toString()}`",
		"`/v1/issues/${encodeURIComponent(issueID)}/fix-monitoring?${parameters.toString()}`",
		"`/v1/issues/${encodeURIComponent(issueID)}/fixes`",
		"`/v1/issues/${encodeURIComponent(issueID)}/fixes/${encodeURIComponent(annotationID)}/retractions`",
		`headers["Content-Type"] = "application/json";`,
		`headers["Idempotency-Key"] = idempotencyKey;`,
		`headers["X-Belay-Intent"] = intent;`,
		`"record-fix-attempt.v1"`,
		`"retract-fix-attempt.v1"`,
		`credentials: "omit"`,
		`cache: "no-store"`,
		`JSON.stringify(body)`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("fix HTTP browser contract is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`headers["Origin"]`,
		`headers.Origin`,
		`method: "PUT"`,
		`method: "DELETE"`,
		`no_visible_occurrence`,
		`latest_occurrence_not_current`,
	} {
		if strings.Contains(app, forbidden) {
			t.Errorf("fix browser must leave forbidden write behavior absent: %q", forbidden)
		}
	}
}

func TestFixBrowserMutationDeadlineAndRetryStateContract(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	mutation := browserSourceBlock(
		t,
		app,
		"  async function apiMutation(",
		"  async function apiRequest(",
	)
	request := browserSourceBlock(
		t,
		app,
		"  async function apiRequest(",
		"  function throwIfMutationAborted(",
	)
	record := browserSourceBlock(
		t,
		app,
		"  async function submitFixAttempt() {",
		"  function openFixRetractionDialog(",
	)
	retract := browserSourceBlock(
		t,
		app,
		"  async function submitFixRetraction() {",
		"  async function reloadFixMonitoringAndFocus(",
	)

	if !strings.Contains(app, `const mutationRequestDeadlineMilliseconds = 15_000;`) {
		t.Error("mutation deadline must be fixed and bounded")
	}
	for _, required := range []string{
		`const controller = new AbortController();`,
		`deadlineReached = true;`,
		`controller.abort();`,
		`controller.signal`,
		`throw new LocalMutationTimeoutError();`,
		`globalThis.clearTimeout(deadline);`,
	} {
		if !strings.Contains(mutation, required) {
			t.Errorf("mutation deadline contract is missing %q", required)
		}
	}
	if !strings.Contains(app,
		`Belay did not confirm the ${verb} in time. Retry the same submission; do not start another one.`) {
		t.Error("timeout feedback must explicitly preserve same-submission retry semantics")
	}
	if strings.Count(request, `throwIfMutationAborted(signal);`) != 2 {
		t.Error("POST transport must reject aborted responses both before and after body decoding")
	}
	if !strings.Contains(request, `request.signal = signal;`) {
		t.Error("POST fetch must receive the bounded AbortController signal")
	}
	for name, submit := range map[string]string{
		"record":  record,
		"retract": retract,
	} {
		catchIndex := strings.Index(submit, "    } catch (error) {")
		if catchIndex < 0 {
			t.Fatalf("%s submit is missing its error-state transition", name)
		}
		catch := submit[catchIndex:]
		for _, required := range []string{
			`draft.pending = false;`,
			`state.modalSubmitting = false;`,
			`syncFix`,
		} {
			if !strings.Contains(catch, required) {
				t.Errorf("%s timeout recovery is missing %q", name, required)
			}
		}
	}
	if strings.Count(record[strings.Index(record, "    } catch (error) {"):],
		`draft.idempotencyKey = "";`) != 1 {
		t.Error("record timeout/general failure must preserve the retry key; only 410 may clear it")
	}
	if strings.Contains(retract[strings.Index(retract, "    } catch (error) {"):],
		`draft.idempotencyKey = "";`) {
		t.Error("retraction timeout/general failure must preserve the retry key")
	}
}

func TestFixBrowserDraftIdempotencyAndExpiryContract(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	submit := browserSourceBlock(
		t,
		app,
		"  async function submitFixAttempt() {",
		"  function openFixRetractionDialog(",
	)

	for _, required := range []string{
		`const fixDrafts = new Map();`,
		`globalThis.crypto.randomUUID()`,
		`isCanonicalUUIDv4(value)`,
		`draft.idempotencyKey = createUUIDv4();`,
		`draft.actionToken = eligibility.actionToken;`,
		`action_token: draft.actionToken`,
		`draft.attempted = true;`,
		`draft.unresolved = true;`,
		`draft.pending = true;`,
		`fixDrafts.delete(issueID);`,
		`Previously recorded attempt restored; no duplicate created.`,
		`Attempt recorded · Not verified by Belay.`,
		`function abandonFixDraft()`,
		`Retry same attempt`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("fix draft/idempotency contract is missing %q", required)
		}
	}

	expiry := browserSourceBlock(
		t,
		submit,
		"      if (isCursorExpired(error)) {",
		"      syncFixDraftDialog();",
	)
	for _, required := range []string{
		`draft.idempotencyKey = "";`,
		`draft.actionToken = "";`,
		`draft.attempted = false;`,
		`draft.unresolved = false;`,
		`nothing was resubmitted`,
		`await refreshAttentionAfterExpiry();`,
	} {
		if !strings.Contains(expiry, required) {
			t.Errorf("410 handling is missing %q", required)
		}
	}
	if strings.Contains(expiry, "submitFixAttempt(") ||
		strings.Contains(expiry, "apiMutation(") {
		t.Error("410 handling must never auto-resubmit")
	}

	catchRemainder := submit[strings.Index(submit, "    } catch (error) {"):]
	if strings.Count(catchRemainder, `draft.idempotencyKey = "";`) != 1 {
		t.Error("only the explicit 410 path may discard the retained retry key")
	}
}

func TestFixBrowserHistoryRetractionAndTruthfulWording(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	history := browserSourceBlock(
		t,
		app,
		"  function renderFixHistory() {",
		"  function renderFixDialogChoices() {",
	)

	for _, required := range []string{
		`available: "Evidence retained"`,
		`partial: "Evidence partly retained"`,
		`pruned: "Evidence pruned"`,
		`unknown: "Evidence status unknown"`,
		`All events cited when this attempt was recorded are still stored.`,
		`Some events cited when this attempt was recorded are still stored.`,
		`The events cited when this attempt was recorded are no longer stored.`,
		`Belay could not determine whether the originally cited events are still stored.`,
		`Retracted attempt`,
		`Active attempt`,
		`Attempt status unavailable`,
		`Attempt status is unavailable. Retraction is disabled until Belay confirms this attempt is active.`,
		`Preserved in history and excluded from active monitoring.`,
		`openFixRetractionDialog(annotationID);`,
	} {
		if !strings.Contains(history, required) {
			t.Errorf("fix history contract is missing %q", required)
		}
	}
	for _, required := range []string{
		`Previously recorded retraction restored; no duplicate created.`,
		`Attempt record retracted. The original attempt remains in history.`,
		`"belay.local/already-retracted"`,
		`"belay.local/idempotency-conflict"`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("retraction lifecycle contract is missing %q", required)
		}
	}

	for _, prohibited := range []string{
		"Issue fixed",
		"Issue resolved",
		"Fix successful",
		"Recurrence prevented",
		"Change verified",
	} {
		if strings.Contains(app, prohibited) {
			t.Errorf("fix browser makes prohibited outcome claim %q", prohibited)
		}
	}
}

func TestFixBrowserUnknownAnnotationStateIsNeutralAndNotRetractable(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")
	historyRow := browserSourceBlock(
		t,
		app,
		"  function createFixHistoryRow(",
		"  function appendFixMetadata(",
	)
	normalizer := browserSourceBlock(
		t,
		app,
		"  function normalizeFixAnnotationState(",
		"  function normalizeFixEvidenceStatus(",
	)
	openRetraction := browserSourceBlock(
		t,
		app,
		"  function openFixRetractionDialog(",
		"  function closeFixRetractionDialog(",
	)

	for _, required := range []string{
		`return ["active", "retracted"].includes(stateValue)`,
		`: "unknown";`,
	} {
		if !strings.Contains(normalizer, required) {
			t.Errorf("annotation-state normalization is missing %q", required)
		}
	}
	for _, required := range []string{
		`annotationState === "active"`,
		`"Attempt status unavailable"`,
		`stateBadge.dataset.tone = annotationState;`,
		`Retraction is disabled until Belay confirms this attempt is active.`,
	} {
		if !strings.Contains(historyRow, required) {
			t.Errorf("unknown annotation rendering is missing %q", required)
		}
	}
	if !strings.Contains(openRetraction,
		`normalizeFixAnnotationState(annotation && annotation.state) !== "active"`) {
		t.Error("retraction dialog must fail closed unless history reports exact active state")
	}
	if !strings.Contains(styles, `.fix-state-badge[data-tone="unknown"] {`) ||
		!strings.Contains(styles, `background: transparent;`) {
		t.Error("unknown annotation state must use explicit neutral styling")
	}
}

func TestFixBrowserModalAccessibilityAndSafeRendering(t *testing.T) {
	app := readBrowserAsset(t, "assets/app.js")
	styles := readBrowserAsset(t, "assets/styles.css")

	for _, required := range []string{
		`elements.appShell.inert = true;`,
		`elements.appShell.setAttribute("aria-hidden", "true");`,
		`elements.appShell.inert = false;`,
		`elements.appShell.removeAttribute("aria-hidden");`,
		`if (event.key === "Escape") {`,
		`if (state.modalSubmitting) return;`,
		`if (event.key !== "Tab") return;`,
		`dialog.querySelectorAll(`,
		`restoreLogicalFocus(returnFocus, elements.issueDetailHeading);`,
		`focusRegistry.fixTriggers.get(reference.issueID)`,
		`focusRegistry.fixHistoryRows.get(reference.annotationID)`,
		`focusRegistry.fixRetractionTriggers.get(reference.annotationID)`,
		`!element.isConnected`,
	} {
		if !strings.Contains(app, required) {
			t.Errorf("fix modal accessibility contract is missing %q", required)
		}
	}
	for _, required := range []string{
		`.modal-layer {`,
		`.modal-dialog {`,
		`max-height: min(760px, calc(100dvh - 48px));`,
		`.modal-scroll {`,
		`overflow-y: auto;`,
		`.modal-close {`,
		`min-width: 44px;`,
		`.modal-actions .primary-button,`,
		`min-height: 44px;`,
		`@media (max-height: 700px)`,
	} {
		if !strings.Contains(styles, required) {
			t.Errorf("fix modal styles are missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"innerHTML",
		"outerHTML",
		"insertAdjacentHTML",
		"document.write",
		"eval(",
		"localStorage",
		"sessionStorage",
		"console.",
	} {
		if strings.Contains(app, forbidden) {
			t.Errorf("fix browser contains unsafe primitive %q", forbidden)
		}
	}
}
