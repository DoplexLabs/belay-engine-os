# Implementation Brief: P0 Attention Inbox and Exact Matching Sessions

- **Status:** Implemented and independently verified
- **Date:** 2026-09-08

## How to Use This Brief

Before writing any code:

1. Read this entire brief end to end.
2. Read the reference materials below and the approved design.
3. Read the existing code in the files and areas called out in the tasks.

When implementing:

4. Work through tasks in order. Implement each change with tests before moving
   to the next task.
5. Follow existing package boundaries, cursor rules, Local privacy guarantees,
   text-only rendering, and bounded-read conventions.
6. Every new code path, edge case, and error path requires an explicit test.

When done:

7. Run package tests, `go test ./...`, `go test -race ./...`, `go vet ./...`,
   and `make verify`.
8. Confirm every acceptance criterion.
9. Report what was implemented, tested, deviated, and remains open.

## Overview

The approved Attention Inbox and exact matching-session experience is
implemented over the P0-01 deterministic issue projection. Attention is the
initial browser destination; Sessions/Timeline remains available as evidence
drill-down.

The completed implementation covers canonical issue presentation fields, issue
projection/query changes, exact cited-event lookup, shared readmodel cursors,
authenticated Local HTTP routes, and the embedded browser. MCP issue tools,
fix recording, recurrence state, hosted Teams, generated diagnosis, and
automatic remediation remain out of scope.

Independent closeout reviews found no blocker/high API, storage, privacy, or
integration defects. Verification covers real-store HTTP behavior, migration
compatibility, cursor stability and expiry, strict exact-event lookup,
experimental and Evidence-gap defaults, legacy cursor compatibility, safe
browser rendering, and the core-only six-tool MCP boundary.

## Reference Materials

- Approved design:
  `docs/design/p0-02-attention-inbox-and-matching-sessions.md`
- Foundation design:
  `docs/design/p0-01-issue-fingerprints-and-detectors.md`
- HTTP contract:
  `docs/contracts/read-api-v1.md`
- Local requirements:
  `docs/launch/local-v0-requirements.md`
- Current implementation:
  - `internal/canonical/model/issue.go`
  - `internal/canonical/model/uuidv7.go`
  - `internal/analysis/reconciler.go`
  - `internal/storage/local/issue_repository.go`
  - `internal/storage/local/projection.go`
  - `internal/presentation/readmodel/service.go`
  - `internal/presentation/localhttp/server.go`
  - `internal/presentation/localhttp/assets/`

## Cross-Layer Contracts

### Issue presentation fields

- `IssueOccurrence.Experimental bool`
- `IssueSummary.Experimental bool`
- Projection migration backfills `experimental=1` only for detector
  `repeated_command_attempts`.
- Attention kind is derived, not stored:
  - `category=evidence_gap` → `evidence_gap`
  - otherwise → `issue`

### Issue list

`GET /v1/issues`

- default/maximum limit: 20/100;
- defaults:
  - `attention_kind=issue`
  - `experimental=stable`;
- filters and normalization exactly match the approved design;
- response includes:
  - `schema_version`;
  - `projection_version=belay.issue.v1`;
  - `data`;
  - `analysis`;
  - `view_cursor`;
  - `next_cursor`;
  - `has_more`;
  - `returned_count`;
  - `limit`.

### Issue detail

`GET /v1/issues/{id}/occurrences`

- accepts `limit`, occurrence `cursor`, or initial `view_cursor`;
- `cursor` and `view_cursor` are mutually exclusive;
- response contains one issue summary and bounded occurrences;
- syntactically valid absent issue → 404;
- malformed/cross-route/cross-issue/impossible cursor → 400;
- expired/compacted cursor → 410
  `type=belay.local/cursor-expired`.

### View and pagination cursors

- Issue list cursor: kind `issues`, snapshot, issued-at, normalized filter
  fingerprint, severity rank, repeated, last observed, issue ID.
- View cursor: kind `issue_view`, snapshot, issued-at, fixed view fingerprint,
  no row position.
- Occurrence cursor: kind `issue_occurrences`, snapshot, issued-at, exact issue
  binding, last observed, occurrence ID.
- Issue-only fields must be absent on existing cursor kinds.
- Existing session/activity/finding cursors remain valid.

### Exact cited-event lookup

`GET /v1/sessions/{id}/events/lookup`

- repeated `event_id`, one to 50;
- canonical lowercase UUIDv7 validation;
- exact session-constrained lookup;
- duplicate IDs removed;
- canonical timeline ordering;
- response includes data, requested/found/missing counts, missing IDs, and
  `data_through`;
- no cursor and no broad timeline scan.

### Browser semantics

- Primary navigation: ordinary buttons inside
  `<nav aria-label="Belay Local views">`, active button
  `aria-current="page"`.
- Initial view: Attention.
- Stable Issues and Evidence gaps have independent list/view cursor state.
- Experimental signals excluded by default and explicitly opt-in.
- Analysis status qualifies stale/partial results.
- Exact matches never use semantic/root-cause wording.
- Dynamic values use text nodes and allowlisted classes/attributes.
- Session findings use one bounded page plus explicit load-more.

## Implementation Sequence

1. Canonical model, analysis projection, migration, repository filters, and
   exact event lookup.
2. In parallel with step 1, browser assets may implement the frozen contract
   without editing Go presentation files.
3. Shared readmodel and Local HTTP routes consume step 1.
4. Browser/HTTP integration and documentation.
5. Independent integration and privacy reviews.

## Service Brief A: Canonical Model, Analysis, and Local Storage

- **Contextual Agent:** Noether
- **Scope owner files:**
  - `internal/canonical/model/issue.go`
  - canonical UUID validation file/tests
  - `internal/analysis/reconciler.go` and tests
  - `internal/storage/local/issue_repository.go`
  - `internal/storage/local/projection.go`
  - next storage migration and storage tests
- **Must not edit:** `internal/presentation/**`, browser assets, public docs

### Tasks

1. Add `Experimental` to occurrence and summary models and propagate detector
   catalog values through reconciliation and persistence.
2. Add migration/backfill and prove upgraded repeated-command revisions are
   excluded from stable defaults immediately.
3. Add `AttentionKind` and `Experimental` query modes with design-specified
   defaults and aggregation.
4. Split snapshot errors into repository-neutral expired versus invalid
   sentinels, with an injectable storage clock.
5. Add canonical UUIDv7 validation.
6. Add bounded exact session event lookup by IDs, including requested/found/
   missing metadata and deterministic ordering.
7. Preserve snapshot, retention, encryption, and append-only invariants.

### Acceptance Criteria

- Default issue queries exclude experimental and evidence-gap rows.
- `all/include` exact detail queries can retrieve them explicitly.
- Existing database rows are backfilled safely without reanalysis.
- Future/newer snapshots are invalid; old/compacted snapshots are expired.
- Event lookup cannot cross sessions and cannot exceed 50 IDs.
- No raw project/command material or plaintext payload is added.

### Testing

- fresh and upgraded database migration;
- experimental aggregation and filters;
- attention-kind filters;
- snapshot clock boundary and error identity;
- UUIDv7 valid/invalid forms;
- event lookup duplicate, missing, cross-session, ordering, limit, and
  encryption cases;
- full storage/analysis package race tests.

## Service Brief B: Shared Readmodel and Local HTTP

- **Contextual Agent:** Averroes
- **Prerequisite:** Service Brief A contracts available
- **Scope owner files:**
  - `internal/presentation/readmodel/service.go` and tests
  - `internal/presentation/localhttp/server.go` and HTTP tests
- **Must not edit:** browser assets, storage implementation, MCP server

### Tasks

1. Introduce a separate optional `IssueRepository`; preserve the existing core
   repository and six-tool MCP construction.
2. Add issue request/response structs, projection version, normalization,
   validation, and fixed title-independent transport.
3. Implement route-scoped issue/view/occurrence cursors with injectable clock.
4. Implement list/detail algorithms, same-snapshot view transfer, and exact
   error precedence.
5. Add both authenticated issue routes.
6. Add authenticated exact event lookup route.
7. Map invalid/not-found/expired/internal errors to fixed, non-reflective
   problem responses.

### Acceptance Criteria

- Existing cursor encodings remain accepted and issue-only fields are rejected
  on legacy kinds.
- All issue filters are bound into pagination cursors.
- List/detail view snapshot cannot drift.
- HTTP routes require loopback bearer authorization.
- MCP code and tests do not need fake issue methods.
- No error reflects cursor, issue ID, event ID, or filter canaries.

### Testing

- normalization/error matrix from the design;
- cross-route, cross-filter, cross-issue, future, expired, and compacted
  cursors;
- view/occurrence cursor mutual exclusion;
- 404 precedence;
- authorization and non-loopback rejection;
- exact event lookup inputs and output metadata;
- stable pagination while reconciliation advances.

## Service Brief C: Embedded Browser

- **Contextual Agent:** Leibniz
- **Scope owner files:**
  - `internal/presentation/localhttp/assets/index.html`
  - `internal/presentation/localhttp/assets/app.js`
  - `internal/presentation/localhttp/assets/styles.css`
  - a new browser-source contract test file if needed
- **Must not edit:** `server.go`, `service.go`, storage, MCP, public docs

### Tasks

1. Add accessible Attention/Sessions primary navigation; Attention is initial.
2. Add analysis coverage and truthful complete/incomplete empty states.
3. Add independently paginated stable Issues and Evidence gaps.
4. Add default stable filters and explicit experimental opt-in.
5. Add issue cards with fixed title/explanation catalog and exhaustive catalog
   test coverage.
6. Add issue detail with analysis-state qualifiers, exact-match language,
   evidence quality, and copyable opaque fingerprint.
7. Add matching-session occurrence rows and lazy exact cited-event lookup.
8. Refactor session opening so any occurrence session can be opened by ID.
9. Replace findings auto-drain with explicit pagination.
10. Add focus transfer/restoration, inert hidden panes, and safe DOM rendering.

### Acceptance Criteria

- Screenshot's current session timeline remains available under Sessions.
- No prominent `UNKNOWN` issue/outcome claim is introduced.
- Experimental and evidence-gap signals never inflate default stable issues.
- No semantic similarity, root-cause, correctness, safety, or fix claim.
- No unbounded page draining.
- Event-derived strings never become markup, selectors, CSS classes, or URLs.
- Mobile and keyboard navigation preserve context and focus.

### Testing

- fixed title/explanation catalog exhaustiveness;
- complete/incomplete/filtered empty states;
- pending/failed/truncated qualifiers;
- exact-match wording;
- independent cursor state;
- cursor-expired full Attention refresh;
- malicious markup/control/URL/selector fixture rendering;
- bounded node and request counts;
- keyboard/focus behavior;
- existing Sessions/Timeline regression behavior.

## Completed Integration Checkpoints

1. Storage/analysis tests and generated model/query contracts were inspected
   after Service A.
2. All new routes were exercised through `httptest` before browser integration.
3. Source-contract, real-store HTTP integration, and browser tests were run
   together after integration.
4. Unit, race, vet, and release-surface verification were completed.
5. Independent reviews covered:
   - storage/snapshot/cursor correctness;
   - privacy, claims, unsafe rendering, and bounded behavior.

## Risks and Callouts

- Migration and backfill must make default filtering correct immediately.
- View cursors are rowless and require separate validation from page cursors.
- Stable issues and Evidence gaps may be at different snapshots; each carries
  its own view cursor.
- The browser must not silently scan timelines or findings.

## Open Items

No Feature 2 implementation blockers remain. MCP issue tools, fix recording,
and recurrence measurement remain separate future features.
