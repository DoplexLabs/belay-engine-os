# P0-02: Attention Inbox and Exact Matching Sessions

- **Status:** Implemented and independently verified
- **Scope:** Canonical issue model, analysis projection, Local storage/read
  repository, shared read model, Local HTTP, and embedded browser
- **Depends on:** P0-01 issue fingerprints and deterministic detectors
- **Explicitly excludes:** MCP issue tools, fix recording, recurrence state, hosted
  Teams behavior, model-generated diagnosis, and automatic remediation

## 1. Overview

Before Feature 2, Belay Local made retained agent activity inspectable through
a session list and event timeline. That surface answered
"what happened?" only after a developer already knows which session to inspect.
P0-01 adds a private deterministic issue projection that can identify specific
evidence patterns and group exact compatible fingerprints across sessions.

This feature turns that projection into the first useful triage workflow:

1. an **Attention Inbox** ordered by severity, recurrence, and recency;
2. an issue detail view explaining the deterministic signal and its evidence
   quality;
3. **matching sessions** that share the exact opaque fingerprint;
4. direct navigation from a matching occurrence to its retained session and
   cited timeline evidence.

The feature remains evidence-first and offline. It does not infer root cause,
intent, correctness, safety, or whether a fix worked. "Matching" means exact
fingerprint equality within a compatible private project scope, never semantic
similarity.

Implementation verification covered storage migration/backfill, default
experimental and Evidence-gap separation, immutable issue/view/occurrence
cursors, exact event lookup, HTTP authorization and error precedence,
real-store HTTP integration, safe browser rendering, accessibility behavior,
legacy cursor compatibility, and the six-tool MCP boundary.

## 2. Glossary

- **Attention Inbox:** The ordered list of issue summaries that may warrant
  developer inspection.
- **Issue:** A stable group of visible occurrences with one exact
  `issue_id`/fingerprint identity.
- **Occurrence:** One issue fingerprint observed in one retained session.
- **Matching session:** A session represented by an occurrence with the exact
  same fingerprint. It is not a claim of common root cause.
- **Analysis coverage:** Counts and status describing how much retained session
  history has completed deterministic analysis.
- **Projection snapshot:** An immutable issue-generation view used throughout a
  cursor chain.
- **Fresh request:** A request without a cursor, evaluated at the current
  projection generation.
- **Untrusted observation:** Event-derived data that must render as text and
  must never be treated as executable markup or instructions.

## 3. Requirements

### 3.1 Functional requirements

1. Implement authenticated Local routes:
   - `GET /v1/issues`
   - `GET /v1/issues/{id}/occurrences`
   - `GET /v1/sessions/{id}/events/lookup`
2. Expose issue reads through `internal/presentation/readmodel`; HTTP must not
   query SQLite or storage-specific types directly.
3. Support the frozen issue-list filters:
   - severity;
   - category;
   - harness;
   - origin;
   - analysis status;
   - observed-after;
   - recurrence (`single|repeated`);
   - session ID;
   - exact fingerprint ID.
4. Preserve deterministic issue ordering:
   severity descending, repeated before single, last observed descending, then
   issue ID ascending.
5. Bind every normalized filter and route identity into opaque cursors.
6. Keep list and occurrence pagination stable across reconciliation.
7. Expire issue cursors after 15 minutes and return the fixed HTTP `410`
   cursor-expired problem without reflecting cursor contents.
8. Return `404` for an issue absent at a fresh snapshot. A malformed, expired,
   cross-route, or issue-mismatched cursor takes precedence over `404`.
9. Make Attention a first-class browser destination and the initial view.
10. Show truthful analysis coverage before making an empty-state claim.
11. Exclude experimental detectors from the default inbox and expose them only
    through an explicitly labeled opt-in.
12. Present verification evidence gaps in a separate Evidence gaps section;
    they must not inflate the default issue count.
13. Show exact matching sessions and occurrence evidence, with explicit
    wording that matches are deterministic rather than semantic.
14. Let a user open any matching session by ID, including sessions not loaded
    in the current session-list page.
15. Let a user retrieve cited events by exact bounded IDs without scanning or
    auto-loading an unbounded timeline.
16. Preserve the existing Sessions and Timeline workflow.
17. Stop the existing session detail from silently draining every findings
    page; findings pagination must be explicit.
18. Keep all user-visible detector titles and explanations fixed, local catalog
    strings. Do not generate narrative diagnosis.

### 3.2 Non-functional requirements

1. Local-only and offline; no new network dependency or telemetry.
2. New routes remain loopback-only and require the per-launch bearer token.
3. Responses are bounded: default 20, maximum 100 for issues and occurrences.
4. Browser pagination is explicit. It must not silently drain all pages.
5. Event-derived values render with `textContent`, never `innerHTML`.
6. Existing CSP, cache-control, referrer, and frame protections remain intact.
7. The UI remains usable with incomplete, pending, failed, truncated, unscoped,
   or conflict-quality analysis.
8. Existing session/activity/finding API and MCP behavior remains backward
   compatible.
9. No raw prompt, completion, reasoning, command output, file content, diff,
   environment value, URL query, raw project path, or key material is exposed.
10. Tests cover cursor privacy, snapshot stability, route authorization,
    incomplete analysis wording, exact-match wording, and safe DOM rendering.
11. Mobile/list-detail navigation uses semantic navigation controls with
    `aria-current`, `inert` or `hidden` for obscured panes, explicit focus
    transfer, and focus restoration.

## 4. Current State / Background and Context

### 4.1 Presentation architecture

`internal/presentation/readmodel.Service` is the shared boundary for Local HTTP
and MCP. It validates requests, normalizes filters, applies bounded limits, and
owns opaque cursor encoding. Its `Repository` interface currently covers
sessions, timeline, activity, findings, and stats.

`internal/presentation/localhttp.Server` explicitly registers authenticated
JSON routes, then serves embedded static assets. The server enforces loopback
access, a constant-time bearer-token check, no-store responses, and a restrictive
CSP.

The browser is a dependency-free embedded HTML/CSS/JavaScript application. It
now presents Attention as the initial destination and preserves Sessions and
Timeline as evidence drill-down. It uses the HTTP routes exclusively and
builds event-derived content with DOM text nodes.

### 4.2 Issue projection foundation

P0-01 provides:

- `Store.QueryIssues`;
- `Store.QueryIssueOccurrences`;
- stable issue and fingerprint IDs;
- deterministic severity/recurrence/recency ordering;
- exact occurrence evidence and cited event IDs;
- list-level analysis coverage;
- immutable issue-generation snapshots;
- a 15-minute cursor lifetime and at least one hour of revision protection.

Issue filters select eligible groups, while summary counts continue to describe
the full visible group at the snapshot. For example, filtering by one harness
may return an issue whose complete summary contains other harnesses.

### 4.3 Product gap

The existing dashboard emphasizes many low-level events and sessions. Even with
better outcome labels, users must manually inspect activity to discover useful
patterns. The Attention Inbox changes the default question from "which session
should I open?" to "what evidence deserves inspection, and where else did the
exact pattern occur?"

## 5. High Level Design

### 5.1 Component flow

```text
Embedded browser
    |
    | Bearer-authenticated loopback JSON
    v
Local HTTP routes
    |
    | validated request / presentation response
    v
Readmodel service
    |
    | IssueQuery / IssueOccurrenceQuery
    v
Local issue repository
    |
    v
Revisioned issue projection + canonical cited events
```

The browser never reads SQLite. The HTTP adapter never constructs SQL queries.
The readmodel never derives new issue semantics; it only validates, paginates,
and presents repository results.

### 5.2 Browser information architecture

The desktop shell gains two primary destinations:

- **Attention** — default;
- **Sessions** — existing activity browser.

Attention uses the existing two-pane layout:

- left pane: analysis coverage, stable issue filters, issue cards, a separate
  Evidence gaps section, and pagination;
- right pane: selected issue summary, deterministic explanation, matching
  sessions, evidence quality, and occurrence pagination.

On narrow screens, the existing back-navigation pattern switches between list
and detail. The browser keeps navigation in memory; it does not place the bearer
token or issue IDs into a server-visible URL. The token continues to be
recovered from the launch fragment and retained only in browser history state.

If no issue is selected, the right pane explains how deterministic analysis
works. It must not claim that Belay understands root cause or agent intent.

### 5.3 Attention card semantics

Each issue card shows only bounded, decision-useful fields:

- fixed title derived from `title_code`;
- severity;
- repeated or single-session state;
- session count and occurrence count;
- harness set;
- last observed relative time;
- analysis status when not current;
- evidence or scope caveat when incomplete, unscoped, or conflicting.

Recommended fixed title catalog:

| Title code | Display title |
|---|---|
| `issue.explicit_command_failure` | Command failed |
| `issue.repeated_command_attempts` | Command repeatedly attempted |
| `issue.explicit_permission_denial` | Permission denied |
| `issue.verification_not_observed` | Verification evidence not observed |
| `issue.unresolved_verification_failure_at_completion` | Verification still failed at session end |
| `issue.numbat_finding` | Numbat finding |

Unknown future title codes display the fixed fallback **Detected issue** plus
the safe category code. Raw title codes are not treated as prose.

The repeated label is **Exact match across N sessions**. The single label is
**Observed in one session**. The UI never says "similar sessions," "same root
cause," or "this will fix."

The built-in catalog is tested exhaustively: every non-experimental title code
must have a fixed title and explanation before release. Unknown future codes
retain the safe fallback.

`repeated_command_attempts` remains experimental and is omitted by default.
The advanced filter labels its opt-in **Include experimental signals** and
explains that precision is still being validated.

`verification_not_observed` is an evidence gap, not an issue claim. It appears
only in the separate **Evidence gaps** section with its own count and empty
state.

### 5.4 Issue detail semantics

The detail header repeats the fixed title and summary attributes. A
deterministic "Why this needs attention" block is selected by `title_code`:

- command failure: source explicitly reported a failed command result;
- repeated attempts: the same private command signature was observed multiple
  times in one bounded interval;
- permission denial: source explicitly reported a denied permission event;
- verification evidence gap: a supported live session ended without the
  required verification evidence;
- unresolved verification failure: a verification command explicitly failed
  and no later successful verification was observed before session end;
- Numbat finding: an immutable upstream Numbat rule emitted a finding.

These are detector definitions, not generated explanations. The detail view
also displays:

- retained-history interval;
- confidence;
- scope quality;
- origin and detector version;
- evidence completeness;
- analysis status;
- exact fingerprint ID behind a copy action, labeled as an opaque exact-match
  identifier.

Analysis-state wording qualifies retained results:

- current: the fixed explanation may be shown without an analysis qualifier;
- pending: **Prior retained result while reanalysis is pending.**
- failed: **Prior retained result; the latest analysis failed.**
- truncated: **Partial analysis; additional signals may be absent.**

For Numbat, the fixed explanation is **A retained upstream Numbat finding was
reported.** It does not describe the upstream rule itself as immutable.

### 5.5 Matching-session rows

Each occurrence row shows:

- harness;
- opaque session ID;
- first and last observed time;
- analysis status;
- evidence completeness;
- count of cited events;
- origin label;
- **Inspect session evidence** action.

Expanding an occurrence calls the exact bounded event lookup route for its
cited IDs. Returned events render in canonical timeline order with explicit
found/missing counts. An **Open full session** action calls
`GET /v1/sessions/{id}` directly, then loads only the first timeline page. The
session does not need to be present in the loaded Sessions list.

The browser never walks timeline cursors looking for cited IDs and never claims
that a missing retained event never existed.

### 5.6 Analysis coverage and truthful empty states

The Attention list always renders analysis coverage:

- current;
- pending;
- failed;
- truncated;
- unscoped;
- analysis-through time.

Empty-state rules:

- `analysis.complete=true`: **No issues reported by configured detectors.**
- incomplete coverage: **No issues are available from completed analysis.**
  Follow with fixed counts for pending, failed, and truncated sessions.
- request filters active: **No issues match these filters.** Coverage remains
  visible and no global safety claim is made.

Unscoped sessions do not make analysis incomplete, but the UI explains that
they cannot establish cross-session recurrence.

The timestamp label is **Latest completed analysis**, not "analysis through,"
because the current value is not a contiguous coverage watermark.

When occurrence-level filters are active, cards include the fixed disclosure:
**Filter selected this issue through a matching occurrence; totals include all
exact occurrences visible in this snapshot.**

### 5.7 Failure behavior

- `400`: invalid filters or malformed/cross-route cursor; generic fixed detail.
- `401`: launch token missing or invalid.
- `404`: issue absent at a fresh snapshot.
- `410`: issue cursor expired; browser invalidates both Attention panes,
  restarts the list at a fresh snapshot, clears stale detail, and shows a
  non-alarming **Issue data refreshed** notice.
- `500`: fixed Local read failure; existing data remains visible where safe,
  with a retry action.

The UI must distinguish an analysis failure reported in valid data from an HTTP
request failure.

## 6. Low Level Design

### 6.1 Canonical/shared errors

Add repository-neutral sentinels in the canonical model boundary:

- `ErrIssueSnapshotExpired`: issued-at older than 15 minutes or generation
  older than retained history;
- `ErrIssueSnapshotInvalid`: future issued-at or generation newer than current.

Alias the existing storage-local expiration value to the canonical sentinel so
callers have one identity. The readmodel defines:

- `ErrCursorExpired`;
- `ErrInvalidCursor`;
- `ErrInvalidRequest`;
- `ErrNotFound`.

The readmodel maps expired snapshots to `ErrCursorExpired` and impossible
snapshots to `ErrInvalidCursor`. The HTTP adapter maps expiration to:

```json
{
  "type": "belay.local/cursor-expired",
  "title": "Cursor expired",
  "status": 410,
  "detail": "The issue view changed. Restart pagination without a cursor."
}
```

No generation, cursor, issue ID, or filter value is reflected.

Mapping order is:

1. malformed request or identifier → 400;
2. malformed, cross-route, cross-issue, future, or newer-generation cursor →
   400;
3. expired or compacted cursor → 410;
4. syntactically valid issue absent at a fresh or valid view snapshot → 404;
5. all other repository errors → 500.

### 6.2 Readmodel repository interface

Keep the existing `readmodel.Repository` unchanged so CLI/MCP and their fakes do
not acquire issue-only methods. Add a separate interface:

```go
type IssueRepository interface {
QueryIssues(context.Context, model.IssueQuery) (model.IssuePage, error)
QueryIssueOccurrences(
    context.Context,
    model.IssueOccurrenceQuery,
) (model.IssueOccurrencePage, error)
LookupSessionEvents(
    context.Context,
    model.EventLookupQuery,
) (model.EventLookupResult, error)
}
```

`readmodel.Service` accepts the core repository plus an optional issue
repository. HTTP construction supplies both from `Store`; current MCP
construction supplies only the core repository until P0-05.

Add request types:

```go
type IssueListRequest struct {
    Limit          int
    Cursor         string
    Severity       string
    Category       string
    Harness        string
    Origin         string
    AnalysisStatus string
    ObservedAfter  *time.Time
    Recurrence     string
    SessionID      string
    FingerprintID  string
    AttentionKind  string
    Experimental   string
}

type IssueDetailRequest struct {
    IssueID   string
    Limit     int
    Cursor    string
    ViewCursor string
}
```

Add response types matching `docs/contracts/read-api-v1.md`:

- `IssueList`;
- `IssueDetail`;
- nested detail data containing `issue` and `occurrences`.

`IssueList` explicitly includes `view_cursor`. All returned slices are non-nil.

`model.IssueOccurrence` and `model.IssueSummary` gain `Experimental bool`. The
projection schema persists the flag and summary aggregation uses logical OR.
`model.IssueFilter` gains:

- `AttentionKind`: `issue|evidence_gap|all`;
- `Experimental`: `stable|include|only`.

HTTP defaults are `attention_kind=issue` and `experimental=stable`. Exact
issue-detail lookup uses `all/include` because selecting an opaque issue ID is
explicit intent.

The migration adds only `experimental INTEGER NOT NULL DEFAULT 0` to issue
occurrence revisions. It deterministically backfills active and retained
revisions with `experimental=1` where
`detector_id='repeated_command_attempts'`; all other existing detector rows
remain stable. New reconciliation writes the catalog value.

`attention_kind` is not persisted. It is derived during repository filtering:

- category `evidence_gap` → `evidence_gap`;
- every other category → `issue`.

Existing D4 rows already carry `category='evidence_gap'`, so they are separated
immediately after upgrade without waiting for reanalysis. Migration and query
tests prove that an upgraded database cannot expose repeated-command or
evidence-gap rows in the default stable issue inbox.

### 6.3 Request normalization

| Field | Normalization and validation |
|---|---|
| `limit` | Existing Local behavior: malformed/non-positive defaults to 20; values above 100 clamp to 100 |
| `severity` | lowercase; empty or `info|low|medium|high|critical`; max 16 bytes |
| `category` | lowercase fixed code `[a-z0-9_]{1,64}` |
| `harness` | trim; case-insensitive exact match; max 128 bytes |
| `origin` | lowercase; empty or `belay|numbat` |
| `analysis_status` | lowercase; empty or `current|pending|failed|truncated` |
| `observed_after` | RFC3339Nano normalized to UTC |
| `recurrence` | lowercase; empty or `single|repeated` |
| `session_id` | trim; max 256 bytes |
| `fingerprint_id` | lowercase; empty or `^ifp_[a-z2-7]{52}$` |
| path `issue_id` | lowercase; required `^iss_[a-z2-7]{52}$`; malformed is 400, valid absent is 404 |
| `attention_kind` | lowercase; default `issue`; `issue|evidence_gap|all` |
| `experimental` | lowercase; default `stable`; `stable|include|only` |

Every normalized value participates in the filter fingerprint.

### 6.4 Cursor envelope

Extend the existing versioned envelope with optional issue fields:

```go
IssuedAt     string `json:"a,omitempty"`
SeverityRank int    `json:"r,omitempty"`
Repeated     bool   `json:"p,omitempty"`
```

Existing session/activity/finding cursors remain valid because the new fields
are optional and omitted for those routes. Per-kind validation requires issue
fields to be absent/zero for every pre-existing cursor kind.

Issue-list cursor:

- kind: `issues`;
- projection snapshot;
- normalized filter fingerprint;
- issued-at UTC;
- severity rank;
- repeated flag;
- last-observed UTC;
- issue ID.

The list response also includes an opaque `view_cursor` carrying kind
`issue_view`, projection snapshot, issued-at, and a fixed
`stableFingerprint({kind:"issue_view"})` fingerprint without a row position.
It intentionally transfers snapshot identity rather than list-filter identity.
Selecting a card passes this cursor through the detail route's `view_cursor`
query parameter so both panes read the same snapshot.

The `issue_view` kind is explicitly rowless:

- `Time`, `ID`, `SeverityRank`, and `Repeated` must be absent/zero;
- snapshot and issued-at are required;
- its fixed fingerprint is recomputed during decode;
- it is accepted only as `view_cursor`;
- occurrence `cursor` and `view_cursor` are mutually exclusive and supplying
  both returns 400.

Occurrence cursor:

- kind: `issue_occurrences`;
- projection snapshot;
- fingerprint bound to exact issue ID;
- issued-at UTC;
- last-observed UTC;
- occurrence ID.

The cursor decoder remains strict about unknown fields, maximum encoded and
decoded size, UTC timestamps, ID length, route kind, and filter fingerprint.
Issue cursor validation additionally requires a valid severity rank and
issued-at. Severity rank is restricted to `1..5`. Issue-only fields on legacy
cursor kinds are rejected.

### 6.5 Readmodel list algorithm

For a fresh issue-list request:

1. normalize and validate all filters;
2. capture `issuedAt` from an injectable UTC clock;
3. call `QueryIssues` with snapshot zero and limit plus one behavior delegated
   to the repository contract;
4. construct the next cursor from the last returned summary when `has_more`;
5. return analysis coverage, `view_cursor`, fixed
   `model.IssueProjectionVersion`, and bounded metadata.

For a cursor request:

1. decode and validate route/filter binding;
2. pass snapshot, issued-at, and deterministic position to the repository;
3. map projection expiration to `ErrCursorExpired`;
4. preserve the same issued-at in the next cursor.

Both readmodel and Local storage receive injectable clocks. Production defaults
to `time.Now`; deterministic tests cover both service-side encoding and
repository-side expiration.

### 6.6 Readmodel detail algorithm

For a fresh detail request:

1. validate the issue ID and capture issued-at, or decode an optional
   `view_cursor`;
2. query `QueryIssues` with exact issue ID and limit one to obtain the summary
   at the selected view snapshot;
3. return `ErrNotFound` if absent;
4. query occurrences using that snapshot and issued-at;
5. construct an occurrence cursor if required.

For a cursor request:

1. decode the issue-bound occurrence cursor first;
2. query the exact issue summary at its snapshot;
3. query occurrences at the same snapshot;
4. expiration or mismatch errors take precedence over not-found behavior.

An occurrence cursor fully carries forward the selected snapshot, so subsequent
pages omit `view_cursor`. A direct detail request with neither cursor uses the
current projection generation.

Revision retention guarantees the two read transactions observe the same
logical projection snapshot. No write lock spans browser reads.

### 6.7 Exact event lookup

`GET /v1/sessions/{id}/events/lookup` accepts repeated `event_id` parameters:

- at least one and at most 50;
- each must be a lowercase canonical UUIDv7 in the existing
  `8-4-4-4-12` hexadecimal form; validation uses a shared canonical UUID
  validator rather than an invented prefix;
- duplicate IDs are removed without changing first-request order;
- no cursor and no free-form query.

The repository performs one bounded exact-ID query constrained to the session.
The response returns events in canonical timeline order plus:

- `requested_count`;
- `found_count`;
- `missing_count`;
- `missing_event_ids`;
- `data_through`.

Missing IDs are ordinary data, not a 404. Errors never reflect IDs. This route
is used only when the user expands occurrence evidence.

### 6.8 HTTP adapter

Register both routes through `authorize`:

```go
mux.Handle("GET /v1/issues", ...)
mux.Handle("GET /v1/issues/{id}/occurrences", ...)
mux.Handle("GET /v1/sessions/{id}/events/lookup", ...)
```

HTTP parsing uses existing bounded integer, trimmed query value, and RFC3339
helpers. `writeReadResult` gains explicit mappings for:

- invalid request/cursor → 400;
- not found → 404;
- expired cursor → 410;
- all other errors → fixed 500.

The route remains GET-only and same-origin.

### 6.9 Browser state

Add independent state buckets:

```text
activeView
issues / issueNextCursor / issueHasMore / issueFilters / issueAnalysis
issueViewCursor
selectedIssueID / selectedIssue
occurrences / occurrenceNextCursor / occurrenceHasMore
issueRequestGeneration / occurrenceRequestGeneration
evidenceGaps / evidenceGapNextCursor / evidenceGapHasMore
evidenceGapViewCursor
evidenceGapAnalysis / evidenceGapStatus / evidenceGapError
evidenceGapRequestGeneration
```

The stable-issue and Evidence-gap requests maintain independent list cursors
and independent view cursors. Opening an Evidence-gap card passes
`evidenceGapViewCursor`; opening a stable issue passes `issueViewCursor`.

Request-generation counters prevent a slower prior filter request from
overwriting a newer result. Refresh resets issue cursor chains and preserves a
selected issue only if it still exists at the fresh snapshot.

### 6.10 Browser rendering

Add:

- primary Attention/Sessions navigation;
- coverage panel;
- severity, recurrence, and harness filters in the default visible filter row;
- advanced category/origin/status filters in a collapsible section;
- issue cards;
- a separately paginated Evidence gaps section;
- issue detail;
- matching-session rows;
- issue and occurrence pagination states;
- fixed expired-data notice.

All dynamic nodes use `createElement` and `textContent`. Classes are selected
from fixed allowlists. No event-derived value becomes a CSS class, URL, HTML
fragment, or selector.

Runtime DOM tests inject markup, control characters, long opaque IDs, URL-like
values, and selector strings. Tests assert text-only rendering, allowlisted
classes/attributes, bounded node counts, keyboard operation, focus transfer,
and focus restoration.

Primary destinations use a `<nav aria-label="Belay Local views">` containing
ordinary buttons. The active button has `aria-current="page"`; activation uses
normal Tab/Shift-Tab and Enter/Space behavior, not the ARIA tab pattern or
arrow-key roving focus. Hidden mobile panes are `inert` and `aria-hidden`.
Opening detail moves focus to its heading; Back/Escape restores focus to the
originating issue/session card.

### 6.11 Direct session opening

Refactor session selection into:

```text
openSession(sessionID, optionalKnownSummary)
```

If the summary is not loaded, fetch `/v1/sessions/{id}` before rendering the
timeline. Existing session-list selection passes its known summary and avoids
an extra request where possible.

The browser retains the originating issue ID so Back returns to issue detail
rather than losing the triage context.

Session detail findings load one bounded page. A visible **Load more findings**
action advances its cursor; no loop auto-drains findings.

### 6.12 Documentation

Closeout documentation:

- marks both HTTP issue routes and exact event lookup implemented;
- documents `view_cursor`, `attention_kind`, and experimental filtering;
- records Attention Inbox as a shipped Local browser capability;
- keeps MCP issue tools explicitly unimplemented until Feature 5;
- uses exact-match and truthful incomplete-coverage wording;
- keeps fix recording and recurrence monitoring as future capabilities.

## 7. Monitoring

Belay sends no telemetry. Existing payload-free diagnostics are sufficient for
projection health. The browser locally displays:

- analysis coverage counts;
- analysis-through time;
- request failure state;
- cursor refresh events.

No persistent browser analytics, issue-open counters, or product telemetry are
added.

## 8. Resolved Decisions

The following implemented defaults remain authoritative:

1. Attention is the initial browser view; Sessions remains one click away.
2. The browser initially exposes severity, recurrence, and harness filters;
   lower-frequency filters remain under Advanced.
3. Fixed detector explanations are presentation catalog content, not stored
   issue fields.
4. Cited-event navigation is bounded and manual when evidence is outside the
   loaded timeline page.
5. MCP issue tools remain a separate P0-05 feature.

## 9. Completed Task Breakdown

All implementation and verification items below are complete:

1. Add shared cursor-expiration semantics and readmodel issue request/response
   types.
2. Persist/backfill experimental state and derive attention kind from the
   fixed category.
3. Implement issue-list validation, normalized filter fingerprinting,
   pagination/view cursors, and tests.
4. Implement issue-detail snapshot/cursor behavior and tests.
5. Implement bounded exact cited-event lookup.
6. Add HTTP routes, error mappings, authorization tests, and contract fixtures.
7. Refactor direct session opening and bounded findings pagination.
8. Add Attention navigation, coverage, stable issues, Evidence gaps, and
   filters.
9. Add issue detail, exact matching sessions, and evidence navigation.
10. Add browser runtime unsafe-string, incomplete-coverage, and
   accessibility tests.
11. Add an explicit API error/cursor matrix covering cross-route, cross-issue,
    changed filters, future/impossible cursors, compacted snapshots, malformed
    versus absent IDs, issued-at preservation, and non-reflection canaries.
12. Update public Local documentation without exposing future MCP/fix features.
13. Run unit, race, vet, release-surface, and browser static checks.

## 10. Appendix

Primary references:

- `docs/design/p0-01-issue-fingerprints-and-detectors.md`
- `docs/contracts/read-api-v1.md`
- `docs/launch/local-v0-requirements.md`
- `internal/canonical/model/issue.go`
- `internal/storage/local/issue_repository.go`
- `internal/presentation/readmodel/service.go`
- `internal/presentation/localhttp/server.go`
- `internal/presentation/localhttp/assets/`
