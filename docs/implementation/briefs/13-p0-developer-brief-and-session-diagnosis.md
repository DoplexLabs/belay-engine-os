# Implementation Brief: P0-08 Developer Brief and Session Diagnosis

## How to Use This Brief

Before coding:

1. Read this brief and
   `docs/design/p0-08-developer-brief-and-session-diagnosis.md` completely.
2. Read the current P0-06/P0-07 designs and the implemented read API contract.
3. Inspect current readmodel, Attention-family, issue-catalog, session-detail,
   Local HTTP, and browser navigation code.
4. Treat the design's DTOs, bounds, admission rules, ranking, fixed copy, and
   failure states as normative.

During implementation:

5. Keep the feature read-only and Local-only.
6. Do not add storage schema, canonical fields, a projector, background work,
   telemetry, network dependencies, or MCP tools.
7. Preserve all existing issue/family/session/fix/monitoring behavior.
8. Keep all narrative fixed and all dynamic values safely rendered.

When done:

9. Report changed files, focused verification, deviations, and manual-QA
   status.
10. Do not merge, publish, deploy, or incur costs without approval.

## 1. Goal

Ship a default 24-hour Developer Brief that gives an individual developer using
multiple coding agents:

- recent activity orientation;
- at most five useful things worth reviewing;
- a truthful reason and limitation for each;
- a direct next step into existing Attention or Sessions;
- visible coverage limitations.

Also add deterministic Session Diagnosis to existing session detail using the
stored metadata overview and bounded session-scoped issue reads.

## 2. Scope

### In scope

- additive readmodel DTOs and composition;
- one authenticated GET endpoint;
- additive session-detail diagnosis;
- default Brief browser destination;
- deterministic ranking and fixed copy;
- partial/failure states;
- read API and manual-QA documentation;
- focused privacy, performance, HTTP, and browser tests.

### Out of scope

- migrations or new storage indexes;
- canonical/model changes outside presentation DTOs;
- event or occurrence hydration in the Brief;
- persisted Brief history;
- custom time windows;
- semantic grouping or task names;
- generated diagnosis;
- dismiss/read state;
- capture health or current process state;
- MCP changes; exactly nine read-only tools remain.

## 3. Frozen Contracts

### 3.1 Endpoint

```text
GET /v1/developer-brief
```

- no query parameters;
- loopback and bearer authentication inherited from Local HTTP;
- `200` with `status=ready|limited` when at least one source is usable;
- `503 belay.local/developer-brief-unavailable` only when all sources fail.

### 3.2 Window and bounds

- rolling window: exactly 24 hours from injected UTC clock;
- session candidates: 100;
- Attention-family candidates: 20;
- evidence-gap candidates: 20;
- action cards: 5;
- recent-work cards: 8;
- source deadline: 2 seconds;
- no continuation draining;
- response target: below 64 KiB.

### 3.3 Projection versions

```text
schema_version: belay.read.v1
Developer Brief projection_version: belay.developer-brief.v1
Session Diagnosis projection_version: belay.session-diagnosis.v1
```

### 3.4 Action-card sources

Admit only:

- current, stable, reviewed Attention families;
- current, stable, reviewed evidence gaps;
- explicit failed/interrupted session outcomes.

Suppress:

- unknown catalog and unsupported imported findings;
- experimental findings;
- pending/failed/truncated issue analysis;
- actionless candidates;
- generic finding counts;
- succeeded/incomplete/unknown outcomes as action cards;
- candidates requiring event hydration.

Card semantics are fixed: title = what deserves review, observation = why the
card is shown, evidence = bounded support, limitation = claim boundary, and
next step = one existing destination.

### 3.5 Ranking

Use the exact rank buckets:

1. critical finding: 700;
2. high finding: 650;
3. failed session: 600;
4. medium finding: 500;
5. interrupted session: 450;
6. low finding: 400;
7. evidence gap: 300;
8. info finding: 200.

Merge all admitted candidates and apply the rank table exactly. Return at most
two session-outcome cards; after that cap, select the next ranked finding or
evidence gap.

Tie-break:

1. session count descending;
2. occurrence count descending;
3. last observed descending;
4. kind order finding, evidence gap, session outcome;
5. stable target ID ascending.

Do not expose the numeric score.

### 3.6 Session detail

Local HTTP keeps existing top-level fields and adds:

```go
type SessionDetailWithDiagnosis struct {
    SchemaVersion string               `json:"schema_version"`
    Data          model.SessionSummary `json:"data"`
    Diagnosis     SessionDiagnosis     `json:"diagnosis"`
    DataThrough   time.Time            `json:"data_through"`
}
```

Do not add diagnosis to shared `readmodel.SessionDetail`: the existing MCP
`get_session` tool serializes that type. Keep shared `GetSession` and its output
unchanged.

Core session read errors retain current behavior. After a successful core read,
Local HTTP calls `DiagnoseSession(ctx, detail)`. Issue-enrichment failure
returns successful HTTP core detail with a limited or insufficient diagnosis.

Diagnosis may perform one stable session-scoped issue query with limit 20. It
must not load occurrences, cited events, family members, timeline pages,
findings, fix history, or monitoring.

## 4. Service A: Readmodel Contracts and Composition

- **Owns:** `internal/presentation/readmodel/**`
- **Must not edit:** storage, canonical event/issue models, MCP, command runtime

### Expected files

New:

- `internal/presentation/readmodel/developer_brief.go`
- `internal/presentation/readmodel/developer_brief_test.go`
- `internal/presentation/readmodel/session_diagnosis.go`
- `internal/presentation/readmodel/session_diagnosis_test.go`

Minimal existing:

- `internal/presentation/readmodel/service.go` only if a shared helper is
  unavoidable; do not change `SessionDetail`
- existing test fixtures only where interface/response construction requires
  additive updates.

### Tasks

1. Define exact Brief, action-card, recent-work, source-coverage, limitation,
   diagnosis, and diagnosis-coverage DTOs.
2. Define closed string enums/constants for every status, kind, state, target,
   source, and limitation code.
3. Add `GetDeveloperBrief(ctx)` using the injected clock.
4. Launch exactly the three bounded source reads specified by the design.
5. Use a two-second child context and cleanup-safe concurrency.
6. Normalize all arrays to non-null.
7. Admit and suppress candidates exactly as frozen.
8. Implement pure deterministic ranking and exact deduplication.
9. Compute evaluated-session summary and lower-bound state.
10. Build recent-work cards from session summaries only.
11. Add `DiagnoseSession(ctx, SessionDetail)` without changing `GetSession` or
    `SessionDetail`.
12. Reuse authoritative `issueCatalog` and family catalog text; do not create a
    second narrative catalog.
13. Carry an internal non-JSON reviewed-catalog status through family
    presentation while the raw representative issue is available. Do not add
    this implementation field to the existing family HTTP response.
14. Add a fixed outcome-explanation helper shared by Brief and diagnosis.
15. Return a repository-neutral sentinel for all-source Brief failure so HTTP
    can map it to fixed 503.

### Required implementation properties

- No `GetSession` call inside Brief card construction.
- No event/timeline/finding/occurrence/member lookup.
- No raw repository error in DTO text.
- No mutable global cache.
- No semantic deduplication.
- No non-current issue in action cards.
- No `no_reviewed_action_available` diagnosis unless issue query is complete,
  untruncated, and analysis coverage is complete.
- Cross-session issue summary counts are not copied into session diagnosis.

### Focused tests

- exact 24-hour window boundaries;
- clock determinism;
- source request filters/limits;
- ranking matrix and ties;
- two-session-outcome-card cap;
- reordered input produces byte-equivalent semantic order;
- max cards;
- exact deduplication;
- unknown/experimental/stale/actionless suppression;
- valid family/issue/session target shape;
- `has_more` limitation and lower-bound count;
- one/two/all source failure matrix;
- context timeout and goroutine cleanup;
- fixed outcome language;
- Session Diagnosis state matrix and generic-failure suppression;
- no cross-session aggregate leakage into diagnosis;
- prohibited-content canaries;
- encoded Brief below 64 KiB.

### Acceptance

- Readmodel tests prove the complete design ranking and failure matrix.
- Existing readmodel and MCP callers compile with unchanged shared session
  output.
- No storage or MCP change is required.

## 5. Service B: Local HTTP

- **Owns:** `internal/presentation/localhttp/*.go` and focused Go tests
- **Must not edit:** storage, analysis, MCP, command runtime

### Expected files

New:

- `internal/presentation/localhttp/developer_brief.go`
- `internal/presentation/localhttp/developer_brief_test.go`

Minimal existing:

- `internal/presentation/localhttp/server.go`
- shared server fixtures/tests as needed.

### Tasks

1. Register authenticated `GET /v1/developer-brief`.
2. Reject all query parameters with the standard fixed 400 problem.
3. Map all-source failure to:

```text
status: 503
type: belay.local/developer-brief-unavailable
title: Developer Brief unavailable
detail: Belay could not assemble the recent developer brief.
```

4. Return partial source failures as normal 200 DTOs.
5. Preserve existing loopback/auth/header middleware.
6. Verify additive `GET /v1/sessions/{id}` diagnosis JSON.
7. Implement it with the HTTP-only `SessionDetailWithDiagnosis` wrapper after
   core `GetSession` succeeds.
8. Do not add a write route, action token, or browser mutation.

### Focused tests

- unauthenticated and non-loopback rejection;
- clean no-query request;
- unknown, repeated, empty-name, and non-empty query rejection;
- ready and limited response;
- fixed 503 and no repository text;
- HTTP session-detail backward-compatible fields plus diagnosis;
- core session 404/error precedence over diagnosis;
- unchanged shared/MCP session-detail JSON;
- content type and no-store/security-header inheritance.

### Acceptance

- Route is strict, bounded through readmodel, and payload-free on errors.
- Existing route behavior remains unchanged.

## 6. Service C: Browser

- **Owns:** `internal/presentation/localhttp/assets/**` and asset-contract tests
- **Must not edit:** readmodel contracts after freeze, storage, MCP

### Tasks

1. Add Brief before Attention and Sessions in navigation and make it default.
2. Add Brief state with independent loading/ready/limited/empty/error states.
3. Render:
   - `Your last 24 hours`;
   - compact recent summary;
   - `Needs review`;
   - `Recent work`;
   - collapsed coverage/limitations;
   - explicit full Attention/Sessions links.
4. Render action cards in server order without client reranking.
5. Implement target navigation:
   - Attention family;
   - exact issue;
   - session.
6. On target 410, clear stale target state before refreshing the Brief and
   require reselection.
7. Add `What deserves review` diagnosis before the existing session overview.
8. Preserve existing Attention, fix, monitoring, session, timeline, and back
   navigation state.
9. Use response catalog text as authoritative. Do not add another issue-copy
   map.
10. Render every dynamic value through `textContent`.
11. Keep one vertical mobile scroll and restore focus to the originating card.

### Required copy

Complete empty:

- `No recorded agent activity in the last 24 hours`, or
- `No reviewed action was identified in the evaluated activity`.

The second state must include:

> This is not a claim that all activity was successful or problem-free.

Limited:

- heading: `Brief is limited`;
- render fixed response limitations;
- preserve usable cards.

All-source failure:

- scoped Brief error only;
- Attention and Sessions remain usable.

### Focused tests

- Brief is initial view;
- DOM hierarchy and action order;
- no browser reranking;
- all three target types;
- 410 clear-before-refresh;
- no stale Brief card retained after refresh;
- ready/limited/empty/error rendering;
- diagnosis state/copy rendering;
- no raw technical identifiers in primary cards;
- `textContent`/safe rendering canaries;
- keyboard/focus/live-region behavior;
- narrow-screen single-scroll structure.

### Acceptance

- A developer can open Belay, identify a useful item, understand why it is
  shown, and reach evidence without first opening the raw timeline.
- Existing views remain fully usable when Brief is limited or unavailable.

## 7. Service D: Contracts and QA

- **Owns:** read API and launch/manual-QA documentation assigned by integration
  owner
- **Must not edit:** MCP contract unless a correction is needed to state that
  P0-08 adds no tools

### Tasks

1. Add `GET /v1/developer-brief` exact shape and semantics to
   `docs/contracts/read-api-v1.md`.
2. Document additive session `diagnosis`.
3. State fixed 24-hour window, source bounds, non-atomic composition,
   limitations, and failure behavior.
4. Add manual QA for mixed Codex/Claude recent sessions.
5. Reassert exactly nine MCP tools and no P0-08 MCP surface.

### Manual QA gate

1. Brief is the first screen.
2. Mixed Codex and Claude Code sessions appear in Recent work.
3. At least one reviewed finding opens the correct Attention detail.
4. A failed or interrupted session appears only when action slots remain.
5. Unknown imported and experimental findings do not appear.
6. A partial analysis fixture visibly limits the Brief.
7. Opening a session shows deterministic diagnosis before raw timeline.
8. No card contains prompt/completion/file content/output/raw command/private
   path or Numbat/internal vocabulary.
9. Attention, fix monitoring, Sessions, and exactly nine MCP tools still work.
10. MCP `get_session` output has no P0-08 diagnosis field and remains schema
    compatible.

## 8. Implementation Sequence

1. Service A freezes DTOs/enums and pure helper tests.
2. Service A implements Brief composition.
3. Service A implements failure-isolated Session Diagnosis.
4. Service B adds the strict route and HTTP tests.
5. Service C implements the browser against frozen DTOs.
6. Service D updates contracts and QA.
7. Run focused package and asset checks.
8. Integrate and perform an independent truthfulness/privacy/API review.
9. Run founder manual QA before beginning P0-09.

## 9. Verification

Minimum focused checks:

```text
gofmt on changed Go files
go test ./internal/presentation/readmodel
go test ./internal/presentation/localhttp
go test -race ./internal/presentation/readmodel ./internal/presentation/localhttp
go vet ./internal/presentation/readmodel ./internal/presentation/localhttp
node --check internal/presentation/localhttp/assets/app.js
git diff --check
```

Run broader repository verification only after focused checks pass and the
integrated branch is stable.

## 10. Risks and Guardrails

1. **False completeness:** Never call bounded evaluated counts total counts.
2. **Stale diagnosis:** Non-current issue analysis is excluded from action
   cards and exposed as a limitation.
3. **Latency amplification:** Never load overview/timeline per recent session.
4. **N+1 reads:** Session Diagnosis performs one issue query only.
5. **Copy drift:** Reuse authoritative readmodel catalogs; browser does not
   reconstruct explanations.
6. **Privacy expansion:** No event hydration or new data collection.
7. **Cross-source inconsistency:** Do not claim one atomic Brief snapshot.
8. **Failure coupling:** Brief/diagnosis failures never block ingestion,
   startup, Attention, Sessions, or core session detail.
9. **MCP scope creep:** Exactly nine read-only tools remain.

## 11. Deviation Policy

Stop and request design review before:

- adding storage or a migration;
- changing issue/family identity or cursor formats;
- adding event/occurrence hydration to the Brief;
- increasing candidate or response bounds;
- adding generated prose;
- adding a custom window;
- adding or changing an MCP tool;
- weakening failure isolation or privacy exclusions.
