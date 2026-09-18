# Implementation Brief: P0 Explicit Local Fix Recording

- **Status:** Approved for implementation
- **Date:** 2026-09-08

## How to Use This Brief

Before writing any code:

1. Read this entire brief end to end.
2. Read the approved design and reference materials.
3. Read the existing files and tests called out below.

When implementing:

4. Work through tasks in order and write tests with each change.
5. Preserve append-only evidence, explicit capability injection, fixed claims,
   listener-bound write security, and no-free-text privacy.
6. Every edge/error path requires an explicit test.

When done:

7. Run focused tests, `go test ./...`, `go test -race ./...`, `go vet ./...`,
   `make verify`, and `git diff --check`.
8. Confirm every acceptance criterion.
9. Report implementation, tests, deviations, and blockers.

## Overview

Implement browser-only Local fix-attempt declarations over stable issue
fingerprints. The feature records no command, note, diff, or remediation. It
uses signed issue-bound action tokens, durable idempotency, exact anchor
revision/citation baselines, append-only retractions, and durable history.

P0-04 recurrence consumes only active attempts. This feature must prominently
state that recurrence monitoring is not yet available.

## References

- `docs/design/p0-03-fix-recording.md`
- `docs/design/p0-02-attention-inbox-and-matching-sessions.md`
- `docs/contracts/read-api-v1.md`
- `docs/contracts/mcp-v1.md`
- `internal/storage/local/identity.go`
- `internal/storage/local/issue_repository.go`
- `internal/storage/local/mutation.go`
- `internal/storage/local/retention.go`
- `internal/presentation/readmodel/service.go`
- `internal/presentation/localhttp/server.go`
- `internal/presentation/localhttp/assets/`

## Frozen Contracts

### Eligibility

- stable, non-experimental, non-evidence-gap;
- aggregate analysis current;
- latest visible occurrence current;
- scope quality resolved or lexical;
- signed token bound to issue ID, snapshot, issued-at, and expiry.

### Create

`POST /v1/issues/{id}/fixes`

- exact listener Origin/Host;
- bearer, JSON, intent header, UUIDv4 idempotency key;
- signed action token plus required `fix-change.v1` category;
- 201 create, 200 identical replay, 409 conflict;
- replay may bypass expiry only after valid token MAC/structure/issue binding.

### Retract

`POST /v1/issues/{id}/fixes/{annotation_id}/retractions`

- append-only fixed reason;
- separate idempotency domain;
- 201 create, 200 replay, 409 conflict/already retracted.

### History

`GET /v1/issues/{id}/fixes`

- durable even if issue projection disappears;
- dual annotation/retraction high-water cursor;
- annotation membership/state snapshot-stable;
- evidence status evaluated at read time:
  `available|partial|pruned|unknown`.

### Capability boundary

- Local HTTP receives fix services explicitly.
- MCP remains exactly six read-only tools.
- No CLI command.

## Sequence

1. Model/storage/token/history/retraction foundation.
2. Browser assets may proceed in parallel against frozen HTTP contracts.
3. Local action service, readmodel claims handoff, HTTP and CLI wiring.
4. Integration, documentation, independent reviews.

## Service A: Canonical Model and Local Storage

- **Contextual Agent:** Noether
- **Owns:**
  - new canonical fix model/tests;
  - storage identity/token codecs;
  - migration 010;
  - fix repository/write/history/retraction code and tests;
  - conditional mutation guards/lifecycle/retention tests.
- **Must not edit:** presentation/readmodel, HTTP, browser assets, MCP, docs.

### Tasks

1. Add fixed change/retraction/state/evidence enums, action claims, annotation,
   retraction and dual-snapshot query/page types.
2. Add distinct HKDF/HMAC domains for IDs, request fingerprints and tokens.
3. Implement migration 010 exactly as approved:
   annotations, citation sidecars, retractions, indexes, fixed constraints.
4. Install conditional immutable guards safely on fresh/upgraded connections.
5. Implement signed token codec; decoding authenticates MAC/structure without
   applying expiry.
6. Implement eligibility query matching issue-summary aggregation semantics.
7. Implement record transaction:
   replay/conflict before expiry, aggregate eligibility, latest anchor,
   exact revision/generation/timestamps/citations, `ON CONFLICT` recovery.
8. Implement retraction replay/conflict/already-retracted transaction.
9. Implement dual-high-water history and read-time evidence status.
10. Keep annotations outside ordinary retention and projection state.

### Acceptance Criteria

- No free-text/payload field exists.
- Replay after expiry works only with a valid signed token and same request
  fingerprint.
- Invalid MAC or issue mismatch never replays.
- Concurrent identical writes produce one row.
- Conflicting writes never mutate existing rows.
- P0-04 can identify active/retracted attempts and exact monitoring baseline.
- Event pruning cascades citation sidecars without deleting annotation.
- No write changes dirty sessions or projection generations.

### Tests

- fresh and migration-009 upgrades;
- schema/check/index/timestamp constraints;
- token tampering/issue mismatch/expiry boundary;
- all reachable eligibility states, missing-issue behavior, and latest-anchor
  ordering;
- identical/conflicting/concurrent creation;
- identical/conflicting/concurrent retraction;
- evidence available/partial/pruned/unknown;
- dual history snapshot stability;
- mutation guards across stores/connections;
- privacy canaries and reflection tests;
- retention/reset behavior.

## Service B: Local Action, Readmodel Claims, HTTP and Wiring

- **Contextual Agent:** Averroes
- **Prerequisite:** Service A contracts
- **Owns:**
  - `internal/localaction/**`;
  - narrow readmodel view-claims API and tests;
  - Local HTTP routes/security/strict parsing/tests;
  - `cmd/belay/local.go` explicit wiring/tests.
- **Must not edit:** storage implementation, browser assets, MCP tools, docs.

### Tasks

1. Add neutral view claims handoff without localaction importing readmodel.
2. Implement eligibility, record, retract and history orchestration.
3. Implement eligibility/read/write routes and exact response contracts.
4. Bind writes to actual numeric loopback listener origin/host/port.
5. Enforce total security/parsing/error precedence.
6. Preserve unresolved replay behavior: valid token auth before idempotency;
   expiry only after replay lookup.
7. Explicitly inject action capability into HTTP only.

### Acceptance Criteria

- Handler without trusted listener origin fails write routes closed.
- DNS-rebinding-style Host/Origin pairs are rejected.
- Forwarded headers are ignored.
- Strict media type/encoding/body/query/header/JSON rules hold.
- Error bodies reflect no canaries.
- GET history works after issue disappearance.
- MCP remains six tools and receives no mutation capability.

### Tests

- auth/origin/host/fetch-site/intent matrix;
- 415/413/400 precedence;
- duplicate headers/fields/trailing JSON/unknown queries;
- token MAC/issue/expiry and replay order;
- create/replay/conflict/status codes;
- retraction flows;
- real-store HTTP integration;
- CLI HTTP-versus-MCP capability wiring.

## Service C: Embedded Browser

- **Contextual Agent:** Leibniz
- **Owns:** Local HTTP HTML/JS/CSS assets and disjoint asset contract tests.
- **Must not edit:** Go server/readmodel/storage/MCP/docs.

### Tasks

1. Add independent eligibility/history loading to issue detail.
2. Display the explicit P0-04-not-available disclosure.
3. Implement the accessible record dialog and deliberate required category.
4. Retain unresolved draft/idempotency key across close/reopen/retry.
5. Implement distinct creation/replay/expiry/error announcements.
6. Implement history pagination, evidence/state wording and retraction dialog.
7. Preserve logical-ID focus restoration, inert background, focus containment,
   Escape behavior, mobile scrolling and 44px controls.
8. Render every dynamic value as text with allowlisted classes/attributes.

### Acceptance Criteria

- No recording occurs without explicit confirmation.
- No category is preselected.
- 410 discards stale key/token and never auto-resubmits.
- Network/5xx retains the unresolved key.
- Replay is not presented as a new record.
- Retraction is available and P0-04 can ignore it.
- No wording says fixed/resolved/successful/prevented/safe.
- Existing Attention, matching sessions and Sessions timeline remain usable.

### Tests

- fixed category/reason catalogs;
- dialog semantics/focus/static invariants;
- draft/key state transitions;
- create/replay/410/403/409/5xx messages;
- history/evidence/retraction rendering;
- unsafe-string fixtures;
- bounded requests/pagination;
- existing Attention/Session source contracts.

## Integration Checkpoints

1. Verify Service A contracts before Service B starts.
2. Merge shared worktree slices and run full tests/race/vet/release checks.
3. Independently review:
   - storage/idempotency/retraction/retention;
   - listener security/error precedence/capability isolation;
   - browser truthfulness/accessibility/draft recovery.
4. Update public Local contracts only after integration is clean.

## Open Items

None. The approved design resolves implementation choices.
