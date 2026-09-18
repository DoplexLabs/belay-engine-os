# Implementation Brief: P0 Read-Only MCP Issue Evidence

- **Status:** Approved for implementation
- **Date:** 2026-09-08

## How to Use This Brief

Before writing any code:

1. Read this brief end to end.
2. Read the approved design and every reference named for your service.
3. Read the existing code and tests before editing.

When implementing:

4. Work through tasks in order and add tests with each change.
5. Preserve the original six MCP tools, offline behavior, local privacy,
   fixed-error boundary, exact fingerprint semantics, and P0-03/P0-04
   capability isolation.
6. Every applicable row in the design's normative acceptance matrix requires
   an explicit automated test.

When done:

7. Run focused tests for your slice and report changed paths, tests,
   deviations, and blockers.
8. Do not merge, deploy, publish, or incur costs.
9. The integration owner runs the full test, race, vet, verification, and
   independent-review gates after all slices are combined.

## Overview

Implement the approved P0-05 issue-evidence loop:

```text
list_issues
  -> get_issue with the frozen view cursor
  -> lookup_session_events for exact cited IDs
  -> calling agent reasons over bounded untrusted evidence
```

Belay does not call a model, generate diagnosis, recommend remediation,
execute fixes, or expose P0-03/P0-04 mutation/monitoring capabilities through
MCP.

## Reference Materials

- `docs/design/p0-05-mcp-issue-evidence.md`
- `docs/design/p0-04-recurrence-monitoring.md`
- `docs/design/p0-03-fix-recording.md`
- `docs/design/p0-02-attention-inbox-and-matching-sessions.md`
- `docs/design/p0-01-issue-fingerprints-and-detectors.md`
- `docs/contracts/mcp-v1.md`
- `docs/contracts/read-api-v1.md`
- `docs/launch/local-v0-requirements.md`
- `docs/launch/clean-machine-alpha-qa.md`
- `internal/canonical/model/issue.go`
- `internal/canonical/model/fix.go`
- `internal/canonical/model/event.go`
- `internal/storage/local/store.go`
- `internal/storage/local/issue_repository.go`
- `internal/storage/local/event_lookup.go`
- `internal/storage/local/fix_token.go`
- `internal/storage/local/fix_repository.go`
- `internal/presentation/readmodel/service.go`
- `internal/presentation/localhttp/server.go`
- `internal/presentation/localhttp/assets/app.js`
- `internal/presentation/localmcp/server.go`
- `cmd/belay/local.go`

## Frozen Cross-Service Contracts

### Issue cursor codec

```go
type IssueCursorCodec interface {
    SealIssueCursor(payload []byte) (string, error)
    OpenIssueCursor(value string) ([]byte, error)
}
```

- Format:
  `base64url(canonical-json) + "." + base64url(HMAC-SHA256)`.
- Key domain: `belay.local.issue-cursor.v2`.
- Store derives the HMAC key from the persistent Local data key.
- Bad encoding, unknown payload fields, wrong kind, or bad MAC:
  `ErrInvalidCursor`.
- Valid MAC with stale epoch, expired time, retention mismatch, or pruned
  snapshot: `ErrCursorExpired`.
- Issue cursors only use V2. Existing session/activity/finding and P0-04
  monitoring cursors remain unchanged.

### Canonical issue snapshot fields

`model.IssueQuery`, `model.IssueOccurrenceQuery`, and their result pages carry:

- opaque `CursorEpoch`;
- issue projection `Snapshot`;
- `RetentionGeneration`;
- `IssuedAt`;
- existing filter and position fields.

Fresh repository reads return epoch, snapshot, retention generation, summaries,
and global coverage from one read transaction. Continuations validate all
authenticated snapshot claims in that same transaction.

### Fix security

- `IssueViewClaims`, `FixEligibilityQuery`, and `FixActionClaims` carry epoch.
- `FixActionTokenVersion` is `belay.fix-action.v2`.
- Epoch is checked transactionally before idempotent replay, snapshot
  validation, or insertion.
- A correctly signed V1 token maps to cursor-expired/HTTP 410.
- A malformed, bad-signature, or cross-Store token remains invalid input.

### Readmodel additions

`IssueList` adds normalized selection:

```json
{
  "selection": {
    "attention_kind": "issue",
    "experimental": "stable",
    "includes_evidence_gaps": false,
    "includes_experimental": false
  }
}
```

`IssueDetail` adds:

- fixed `catalog`;
- snapshot-matched `global_analysis_coverage`;
- existing summary, occurrences, `view_cursor`, and page metadata.

Catalog field names:

- `catalog_version`;
- `catalog_status`;
- `title_code`;
- `observation_statement`;
- `caveat`;
- `next_evidence_action`.

The exact catalog rows are normative in design section 6.3.

### Bounded event evidence

Storage provides a visitor-style exact lookup that decrypts/decodes one event
at a time and returns an `EventLookupSummary`. It never accumulates
`[]model.Event`.

The MCP readmodel maps each row immediately to recursively closed MCP DTOs and
encodes through the 2 MiB result budget. Every nested schema has
`additionalProperties=false`.

### MCP tools

The server advertises exactly nine tools:

1. `list_sessions`
2. `get_session`
3. `get_session_timeline`
4. `query_activity`
5. `list_findings`
6. `get_stats`
7. `list_issues`
8. `get_issue`
9. `lookup_session_events`

The original six retain their current names, schemas, limits, success shapes,
cursor behavior, and error text for messages within the 256 KiB frame bound.

Every new success result is:

```json
{
  "untrusted_observations": true,
  "trust": {
    "classification": "untrusted_observations",
    "instruction_authority": "none",
    "must_not_authorize_actions": true
  },
  "readmodel": {}
}
```

New-tool fixed codes are exactly:

- `belay_mcp/invalid_input`
- `belay_mcp/invalid_cursor`
- `belay_mcp/cursor_expired`
- `belay_mcp/issue_not_found`
- `belay_mcp/read_busy`
- `belay_mcp/read_timeout`
- `belay_mcp/result_too_large`
- `belay_mcp/cancelled`
- `belay_mcp/read_failed`

## Implementation Sequence

1. Service A freezes canonical contracts, migration 012, materialized storage,
   cursor/action security, and bounded event visitor.
2. Service B implements readmodel and HTTP against Service A.
3. Service B browser work and Service C MCP work proceed in parallel after
   readmodel contracts compile.
4. Service B owns runtime wiring and public documentation after the MCP
   constructor signature is stable.
5. Integrate all slices and run the complete verification matrix.
6. Delegate independent storage/security, MCP/protocol, and product/UX review;
   resolve findings and repeat verification.

## Service A: Canonical Model, Storage, Cursor Security, and Bounded Evidence

- **Contextual Agent:** Leibniz
- **Owns:**
  - `internal/canonical/model/**`;
  - `internal/storage/local/**`;
  - `internal/localaction/**` only where epoch/error propagation requires it.
- **Must not edit:** readmodel, localhttp, localmcp, browser assets, `cmd`,
  public docs.

### Design context

This slice creates the trustworthy snapshot and bounded-read foundation
consumed by both HTTP and MCP. Migration 012 must materialize only current
provable issue state, establish a persistent opaque epoch, and fail closed
before serving if materialized state is not ready.

### Tasks

1. Add canonical cursor epoch, retention generation, issue query/page, neutral
   claims, fix-action V2, and event visitor summary contracts.
2. Add only migration `012_*.sql`; do not modify migrations 001–011.
3. Add revisioned issue-summary, harness/session relation, global coverage,
   generation timestamp, readiness, and cursor-epoch storage.
4. Reuse `local_migration_progress` for deterministic bounded current-state
   materialization. Generate the epoch through `OpenOptions.Random`.
5. Resume migration 012 before `Store` is returned. Detect generation drift
   from older-binary writes and rebuild current state or fail closed.
6. Update projection, session-status, dirty-state, and retention transactions
   so affected issue summaries and global coverage advance atomically after
   all occurrence/event mutations.
7. Coalesce unchanged summary/coverage intervals. Compact only revisions older
   than the 15-minute cursor lifetime, cascade relation cleanup, and advance
   oldest retained generation atomically.
8. Replace issue-list occurrence aggregation with indexed revisioned-summary
   traversal and indexed relation probes. Select `limit+1` before decoding.
9. Implement the Store-backed HMAC issue cursor codec. Verify MAC before
   parsing. Never expose key material.
10. Implement fix-action V2 epoch claims. Recognize correctly signed V1 tokens
    as expired. Check epoch before idempotent replay or insertion and include
    epoch/version in request fingerprints.
11. Add the visitor-style exact event read. Reuse single-row `decodeEvent`, not
    `decodeEventRows`; close rows before the watermark lookup.
12. Extend mutation guards and all migration/table/trigger allowlist tests.

### Acceptance criteria

- Fresh snapshot, epoch, retention generation, and coverage are transactionally
  consistent.
- Session `current|pending|failed|truncated` transitions recompute every issue
  referenced by that session even when occurrence rows do not change.
- Migration interruption after DDL, between batches, and before readiness
  resumes correctly.
- Older-binary generation drift rebuilds or fails closed.
- Bad-MAC/cross-Store cursor is invalid; stale epoch is expired; restart keeps
  a cursor valid until ordinary expiry.
- Pre-012 fix tokens cannot return idempotent success or write anything.
- Query work is page-bounded at 5,000 groups/100,000 occurrences.
- High churn keeps revision growth bounded without breaking valid cursors.
- Event visitor preserves requested/found/missing semantics and canonical
  source order while holding only one decoded event at a time.

### Testing requirements

- Model contract and validator tests.
- Fresh/upgrade/interruption/readiness/drift migration tests.
- Summary/coverage aggregation, session-status, retention, coalescing,
  compaction, guard, scale, query-plan, cancellation, and high-churn tests.
- Cursor malformed, bad-MAC, wrong-kind, cross-Store, stale-epoch,
  retention-expired, close/reopen, and key-zeroing tests.
- Fix token V1/V2, epoch-before-replay, cross-Store, transaction-race, and
  no-write tests.
- Visitor validation, deduplication, order, callback cancellation/error,
  privacy canary, and no-slice accumulation tests.

### Contracts produced

- Canonical cursor/query/page/claim types.
- `IssueCursorCodec`.
- Materialized `QueryIssues` semantics and typed errors.
- Visitor-style exact event lookup.
- Fix action-token V2 and expired-token distinction.

### Risks and callouts

- SQLite is configured with one connection; visitor callbacks must not re-enter
  Store reads.
- Retention currently marks sessions dirty before deletions. Materialization
  must happen after final deletion state, not from the stale pre-delete view.
- Keep original cursor codecs and P0-04 monitoring snapshots untouched.
- Update every hard-coded migration count from 11 to 12.

## Service B: Readmodel, HTTP, Browser, Runtime, and Documentation

- **Contextual Agent:** Averroes
- **Owns:**
  - `internal/presentation/readmodel/**`;
  - `internal/presentation/localhttp/**`;
  - `cmd/belay/local.go`, `cmd/belay/local_test.go`;
  - `README.md`, `llms.txt`, and the public contract/launch docs listed above.
- **Must not edit:** canonical/storage/localaction or localmcp.

### Design context

This slice turns storage contracts into one shared issue readmodel for HTTP and
MCP, then makes the browser issue-first and cursor-v2-correct. Production HTTP
and MCP explicitly receive the Store-backed issue cursor codec; MCP receives
no monitoring or action capabilities.

### Tasks

1. Add `WithIssueCursorCodec` and
   `RequireIssueEvidenceCapabilities`. Keep capability discovery explicit.
2. Implement issue-only cursor-v2 without changing the legacy shared cursor
   version used by the original six reads.
3. Bind normalized filters, effective limits, epoch, snapshot, retention
   generation, route kind, issue ID, position, and issued-at in cursors.
4. Enforce cursor-only continuation. Cursor plus filter/limit is invalid input.
5. Add issue-list selection metadata.
6. Add exact fixed catalog metadata and snapshot-matched global analysis
   coverage to issue detail.
7. Add recursively closed MCP event-evidence DTOs and a bounded result-builder
   readmodel method over Service A's visitor.
8. Update exact HTTP query parsing and preserve fixed 400/404/410/500 mapping.
9. Inject the Store-backed codec into production HTTP and deterministic codecs
   into unit tests.
10. Change browser issue and occurrence pagination to cursor-only requests.
11. Consume server selection/catalog/global coverage. Keep local titles only
    as old-server display fallback.
12. On HTTP 410, clear issue lists, detail, occurrence, catalog, coverage,
    eligibility, action-token, and fix state; refresh both Attention lists and
    require explicit reselection/retry.
13. Extract `newLocalMCPServer(store)` once Service C's constructor is stable:
    core repository + issue repository + cursor codec only.
14. Update MCP/read API contracts, Local requirements, clean-machine QA,
    `README.md`, and `llms.txt` for nine tools, offline/client privacy, and the
    exact value loop.

### Acceptance criteria

- Existing HTTP issue clients remain additive-compatible except intentional
  V1 issue-cursor expiry.
- Every successful list/detail page exposes a valid V2 view cursor.
- List-to-detail coverage and issue data share one snapshot.
- Browser never sends limit/filters with a continuation cursor.
- Browser cannot record a fix with stale eligibility or action token.
- Runtime MCP construction fails before advertisement without issue
  repository or codec.
- MCP runtime receives no fix, monitoring, shell, filesystem, hook, browser
  write, or action capability.
- Public docs do not claim generated diagnosis or prompt-injection immunity.

### Testing requirements

- Readmodel defaults, selection, catalog, coverage, pagination, cursor binding,
  invalid-vs-expired, deterministic codec, restart, and capability tests.
- HTTP exact-query, 400/404/410, cursor-only, list-to-detail, fix eligibility,
  and real-Store integration tests.
- Browser static/behavioral tests for cursor-only requests, mandatory cursors,
  normalized selection, server catalog, coverage isolation, full 410 reset,
  explicit reselection, and no auto-resubmission.
- Runtime construction/isolation, recovery ordering, and stdout-purity tests.
- Documentation surface validation and manual QA steps for Codex and Claude.

### Contracts produced

- Shared issue list/detail/evidence readmodel shapes.
- HTTP issue behavior and browser state machine.
- Production HTTP/MCP construction.
- Public Local/MCP documentation.

### Contracts consumed

- Service A's codec, epoch-bearing snapshots, materialized repository, visitor,
  and fix token errors.
- Service C's final `localmcp.New`/Run signature for runtime wiring.

### Risks and callouts

- Current HTTP list parsing accepts unknown/repeated parameters and silently
  clamps limits; replace it with the exact-query pattern.
- Current browser resends filters/limits on issue and occurrence continuation.
- Many P0-03/P0-04 tests build issue-capable readmodels and will need explicit
  test codecs.
- Do not expose P0-04 monitoring through MCP while adding HTTP codec wiring.

## Service C: Local MCP Transport, Strict Adapter, and Issue Tools

- **Contextual Agent:** Noether
- **Owns:**
  - `internal/presentation/localmcp/**`;
  - `go.mod`, `go.sum` only if schema validation requires dependency promotion.
- **Must not edit:** canonical/storage/readmodel/localhttp/browser/cmd/docs.

### Design context

This slice exposes the shared readmodel safely over stdio. The SDK's typed tool
path remains for the original six tools. The three new tools use a low-level
adapter because Belay must own fixed, non-reflective validation and error
semantics.

### Tasks

1. Add a 256 KiB newline-delimited frame-limiting reader and delegate to
   `mcp.IOTransport`. Oversized frames close the session without response.
2. Add the strict new-tool adapter:
   - 16 KiB raw arguments;
   - four-slot nonblocking admission;
   - 15-second deadline starting after admission;
   - duplicate-key detection;
   - explicit JSON Schema validation;
   - strict typed decode and EOF check;
   - one-slot database-active queue;
   - fixed tool-error results;
   - 2 MiB capped complete-wrapper encoding;
   - output-schema validation over bounded bytes.
3. Add recursively closed explicit input/output schemas with
   `additionalProperties=false`.
4. Add `list_issues`, `get_issue`, and `lookup_session_events`.
5. Preserve fixed narrative content and put all stored strings only in
   structured content.
6. Return the exact trust wrapper on new-tool success.
7. Map expected failures to `CallToolResult{IsError:true}` and fixed text with
   a nil Go error; reserve Go errors for connection/server failure.
8. Require issue repository and codec capability before server creation.
9. Advertise exactly nine read-only/idempotent/non-destructive/closed-world
   tools and bump implementation version to `1.1.0`.
10. Replace `mcp.StdioTransport` with the bounded transport.

### Acceptance criteria

- Original six tool schemas/results/errors remain byte-compatible within the
  frame bound.
- Duplicate keys and malicious values never reach SDK-generated reflective
  errors.
- Fifth admitted call returns `belay_mcp/read_busy`.
- At most one new-tool read is database-active; queue time counts toward the
  same deadline.
- Cancellation/shutdown release every token and goroutine.
- Output overflow returns no partial data.
- Injection canaries appear only in structured evidence.
- No prompts, resources, logging, completions, mutation, fix, recurrence,
  shell, filesystem, hook, or execution capability is advertised.

### Testing requirements

- Exact nine-tool discovery and original-six frozen contract tests.
- Raw NDJSON malformed JSON, duplicate key, unknown/wrong field, 16 KiB
  arguments, 256 KiB frame, and stdout-purity tests.
- Explicit recursive schema and output-schema-failure tests.
- Fixed error mapping with no reflected payload values.
- Admission/database semaphore, queue timeout, cancellation, shutdown, and
  cleanup tests.
- Result overflow/worst-valid-output tests.
- Trust wrapper, injection canary, exact issue workflow, missing evidence, and
  no-capability construction tests.

### Contracts produced

- Nine-tool MCP server implementation version `1.1.0`.
- Bounded stdio transport and strict new-tool adapter.
- Fixed new-tool schemas, trust wrapper, and error codes.

### Contracts consumed

- Service B's issue list/detail/evidence readmodel and capability check.

### Risks and callouts

- `mcp.Client` test calls cannot represent duplicate keys; use raw NDJSON.
- Set successful `StructuredContent` from already bounded bytes to avoid a
  second uncontrolled serialization.
- Promote `github.com/google/jsonschema-go` only if direct schema validation
  is needed; do not add unnecessary dependencies.

## Integration Checkpoints

1. **Foundation compiles:** canonical/storage/localaction focused tests pass.
2. **Readmodel compiles:** HTTP real-Store list/detail/fix tests pass with V2
   cursors.
3. **MCP compiles:** exact nine tools and original-six compatibility pass.
4. **Runtime integration:** source-built `belay mcp` initializes, lists issues,
   gets detail, and hydrates cited events without monitoring/action exposure.
5. **Browser integration:** new source-built Local opens Attention, handles
   pagination and stale epoch, and preserves P0-03/P0-04 behavior.
6. **Full verification:**
   - focused package tests;
   - `go test -count=1 ./...`;
   - `go test -race -count=1 ./...`;
   - `go vet ./...`;
   - `make verify`;
   - `node --check internal/presentation/localhttp/assets/app.js`;
   - `git diff --check`.
7. **Independent review:** storage/security, MCP/protocol, and product/UX all
   approve the integrated implementation.

## Open Items

No blocking product or architecture questions remain.
