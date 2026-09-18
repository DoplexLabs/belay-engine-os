# Implementation Brief: P0 Exact Post-Attempt Recurrence Monitoring

- **Status:** Implemented and independently verified
- **Date:** 2026-09-08

## How to Use This Brief

Before writing any code:

1. Read this entire brief end to end.
2. Read the approved design and all references for your service.
3. Read the existing code and tests named in your service section.

When implementing:

4. Work through tasks in order and add tests with each change.
5. Preserve exact fingerprint scope, normalized instant ordering, durable
   positive observations, qualified negative coverage, cursor stability,
   privacy, and MCP isolation.
6. Every edge/error path in the design's normative acceptance matrix requires
   an explicit automated test.

When done:

7. Run focused tests, `go test -count=1 ./...`, `go test -race -count=1 ./...`,
   `go vet ./...`, `make verify`, `node --check` when assets change, and
   `git diff --check`.
8. Confirm every service acceptance criterion.
9. Report changed paths, tests, deviations, and blockers.

## Overview

Implement automatic, exact post-attempt monitoring for active P0-03
fix-attempt declarations. Belay records a durable positive observation only
when a compatible current occurrence cites at least one canonical event whose
normalized instant is strictly after the attempt baseline.

The feature never verifies a fix or claims causality. Negative no-match status
requires explicit Belay detector absence capability, complete compatible
coverage, and completed recurrence jobs. Current Numbat integration remains
positive-only.

## References

- `docs/design/p0-04-recurrence-monitoring.md`
- `docs/design/p0-03-fix-recording.md`
- `docs/design/p0-02-attention-inbox-and-matching-sessions.md`
- `docs/design/p0-01-issue-fingerprints-and-detectors.md`
- `docs/contracts/read-api-v1.md`
- `docs/contracts/mcp-v1.md`
- `docs/launch/local-v0-requirements.md`
- `internal/canonical/model/issue.go`
- `internal/canonical/model/fix.go`
- `internal/detection/types.go`
- `internal/detection/catalog.go`
- `internal/analysis/reconciler.go`
- `internal/storage/local/projection.go`
- `internal/storage/local/fix_repository.go`
- `internal/storage/local/retention.go`
- `internal/presentation/readmodel/service.go`
- `internal/presentation/localhttp/fix.go`
- `cmd/belay/local.go`

## Frozen Cross-Service Contracts

### Canonical snapshot

```go
type FixMonitoringSnapshot struct {
    ProjectionGeneration int64
    EventGeneration      int64
    RetentionGeneration  int64
    AnnotationSequence   int64
    RetractionSequence   int64
    JobSequence          int64
    JobEventSequence     int64
    ObservationSequence  int64
    IssuedAt              time.Time
}
```

Every monitoring page carries this snapshot. Presentation cursor envelopes
bind endpoint kind, snapshot, normalized filters, route IDs, page size, row
position, and issued-at.

### Storage repository

```go
type FixMonitoringRepository interface {
    QueryFixMonitoring(
        context.Context,
        model.FixMonitoringQuery,
    ) (model.FixMonitoringPage, error)

    QueryIssueFixMonitoring(
        context.Context,
        model.FixMonitoringDetailQuery,
    ) (model.FixMonitoringDetailPage, error)

    QueryFixRecurrenceObservations(
        context.Context,
        model.FixRecurrenceObservationQuery,
    ) (model.FixRecurrenceObservationPage, error)
}
```

Repository results must include:

- exact snapshot and final row position;
- `HasMore`;
- nullable `CurrentIssue` from the same transaction;
- non-nil slices;
- explicit nullable subject/evidence values;
- `EvidenceEvaluatedAt`;
- durable history after current issue disappearance.

Typed repository errors must distinguish:

- invalid/future snapshot;
- expired/compacted/retention-changed snapshot;
- issue or annotation route mismatch/not found;
- catch-up in progress;
- catch-up failed.

### Worker

```go
func NewRecurrenceWorker(store *local.Store) *RecurrenceWorker
func (w *RecurrenceWorker) Startup(context.Context) (RecurrenceReport, error)
func (w *RecurrenceWorker) Drain(context.Context) (RecurrenceReport, error)
```

Storage claim methods are equivalent to:

```go
ClaimRecurrenceJob(ctx, now, lease) (model.FixRecurrenceJobClaim, error)
ProcessRecurrenceBatch(ctx, claim, limit) (model.FixRecurrenceBatchResult, error)
FailRecurrenceJob(ctx, claim, code, retryAt) error
```

Batch processing rechecks retraction, inserts observations, advances the
annotation sequence cursor, and completes the job under claim-generation/token
CAS.

### HTTP

```text
GET /v1/fix-monitoring
GET /v1/issues/{issue_id}/fix-monitoring
GET /v1/issues/{issue_id}/fixes/{annotation_id}/recurrences
```

Exact contract, DTOs, query names, null semantics, ordering, and precedence are
frozen in design sections 6.6–6.10.

Fixed error mapping:

- malformed query/cursor → 400;
- missing/binding mismatch → 404;
- expired/retention-invalid cursor → 410;
- catch-up in progress → fixed 503;
- failed catch-up awaiting retry → distinct fixed 503;
- unexpected read failure → non-reflective 500.

### Browser

- P0-04 detail is the authoritative browser attempt/history source.
- P0-03 `GET /fixes` remains public API compatibility only.
- P0-03 eligibility and mutation flows remain intact.
- Existing issue-list recurrence is relabeled **Session spread**.
- Unknown/no-match are neutral; matching evidence is attention, never success.
- Evidence inspection uses at most 50 canonical UUIDv7 event IDs with the
  existing session-constrained lookup route.

### Capability boundary

- Local HTTP receives the optional fix-monitoring repository.
- The Local runtime starts the recurrence worker.
- MCP remains exactly six read-only tools.
- MCP readmodel construction does not receive monitoring capability.
- No new CLI command or HTTP mutation is added.

## Implementation Sequence

1. Service A canonical/detection/migration/projection/worker/repository
   foundation.
2. Service C browser assets may proceed in parallel against frozen HTTP DTOs.
3. Service B starts after Service A repository/runtime signatures compile.
4. Integrate all slices; update docs; run full verification.
5. Independent storage, API/runtime, and UX reviews.
6. Resolve every finding and repeat verification before commit.

## Service A: Canonical Model, Detection, Storage, Analysis, and Worker

- **Contextual Agent:** Noether
- **Owns:**
  - `internal/canonical/model/**` recurrence/occurrence changes;
  - `internal/detection/**`;
  - `internal/analysis/**` recurrence and reconciler changes;
  - `internal/storage/local/**`, migration 011, repositories and tests.
- **Must not edit:** presentation/readmodel, localhttp, browser assets, cmd,
  MCP, public docs.

### Existing patterns

- Fixed canonical enums/query/page types in `internal/canonical/model`.
- Store-derived opaque identities in `internal/storage/local/identity.go`.
- CAS projection replacement in `projection.go`.
- Dirty-session startup/drain shape in `analysis/reconciler.go`.
- Snapshot repositories in `issue_repository.go`.
- Transactional retention dependency handling in `retention.go`.

### Tasks

1. Add canonical recurrence states, evidence/unavailability catalogs, subject,
   capability, job/claim, observation, coverage, snapshot, query/page/position
   models and validators.
2. Add distinct `fxo_` observation and `fxj_` job identity domains/validators.
3. Extend `IssueOccurrence` with internal exact `FingerprintScopeID`.
4. Change detector evaluation to return matches plus explicit
   `supported|not_applicable|incomplete` absence capability.
5. Make every built-in detector prove prerequisites before supporting absence.
6. Preserve catalog fail-open behavior while retaining per-detector
   applicability.
7. Implement migration 011:
   - normalized event instant;
   - exact occurrence fingerprint scope;
   - analysis watermark/generation fields;
   - monitoring metadata/subjects;
   - capabilities;
   - jobs/job events;
   - observations/citation sidecars;
   - retention generation;
   - indexes and guard hooks.
8. Implement resumable synchronous Go backfill before mutation-guard
   installation and Store return.
9. Backfill only provable Belay/Numbat scopes; never substitute session scope
   for an unprovable Numbat hint.
10. Populate future monitoring subjects transactionally with fix annotation
    creation.
11. Enqueue catch-up jobs for every retained compatible completed revision,
    including closed revisions.
12. Schedule one-time Belay session reanalysis for absence capabilities.
13. Extend projection replacement to persist exact scopes, normalized
    watermark, capabilities and deterministic queued job atomically.
14. Implement annotation-snapshot/sequence batched jobs, claim generation,
    random claim token, lease recovery, stale-worker CAS and bounded backoff.
15. Implement exact recurrence qualification and first-observation persistence.
16. Treat repeat projections for the same annotation/occurrence as already
    observed; reject only deterministic-ID identity collisions.
17. Recheck retraction in the observation transaction.
18. Implement readiness transitions:
    `catching_up → ready`, retryable `failed`, retry/restart recovery, and
    cancellation persistence.
19. Integrate job/capability dependencies into retention; pending work pins
    revisions/citations; completed job deletion cascades events and advances
    retention generation.
20. Implement the three snapshot repositories and typed errors.
21. Add payload-free diagnostics only.

### Acceptance Criteria

- Strict normalized instant comparison; equal instant never qualifies.
- Exact occurrence fingerprint scope is persisted and used.
- Numbat absence never creates no-match.
- Unsupported detector prerequisites never create negative capability.
- One annotation/occurrence produces at most one durable observation.
- Repeat projections never fail or duplicate.
- Multiple attempts independently observe one later occurrence.
- Retraction-before-insert prevents the observation.
- >100 candidates resume deterministically over batches.
- Crash/restart/stale leases converge without loss or duplicates.
- Positive observations survive ordinary retention.
- Pending jobs pin dependencies.
- Cursor snapshot inputs and retention generation are complete.
- Legacy closed revisions recover historical positives.
- No raw path, command, prompt, output, or payload enters recurrence tables.

### Testing Requirements

Implement every applicable row in the design's normative matrix, especially:

- pre/equal/post instant and timezone/fraction variants;
- same-session and other-session observations;
- late pre-attempt import;
- repeat projection;
- Numbat multiple hints/positive-only;
- detector not-applicable/incomplete/failure;
- migration interruption/resume and fresh/upgrade fixtures;
- pre-011 closed-revision catch-up;
- 201-candidate batching;
- crash before/after commit, stale claim and concurrent stores;
- retraction race;
- retention pins/cascades/generation;
- unauthorized update/delete/replace on every guarded table;
- history after issue projection disappearance;
- privacy reflection/canary tests.

Run focused model/detection/analysis/storage tests plus full test/race/vet.

### Contracts Produced

- canonical recurrence models and fixed catalogs;
- detector applicability result;
- exact occurrence fingerprint scope;
- recurrence worker;
- repository interface implementation and typed errors;
- migration/readiness state.

### Risks and Callouts

- Existing migration-count/trigger-count/table-allowlist tests require updates.
- Backfill runs before key initialization; it must not decrypt payloads or
  derive keyed IDs.
- Current reconciler discards timeline event generation; preserve it.
- Do not loosen existing event/annotation append-only protections.

## Service B: Readmodel, HTTP, Runtime Wiring, Integration, and Contracts

- **Contextual Agent:** Averroes
- **Prerequisite:** Service A canonical/repository/runtime contracts compile.
- **Owns:**
  - `internal/presentation/readmodel/**`;
  - Local HTTP Go server/routes/DTO/tests, excluding assets;
  - `cmd/belay/local.go` and tests;
  - `internal/localapp/**` only if coordination requires it;
  - README, `llms.txt`, read/MCP contracts, launch requirements and QA.
- **Must not edit:** canonical/detection/analysis/storage implementation,
  browser assets, localmcp tools.

### Tasks

1. Add optional `FixMonitoringRepository` capability to readmodel.
2. Add dedicated monitoring cursor envelopes for:
   - top-level list;
   - issue attempt detail;
   - observation pages.
3. Bind exact snapshot, normalized filters, route IDs, page size, endpoint and
   row position; preserve 15-minute expiry.
4. Implement list/detail/observation request normalization and strict
   validation.
5. Preserve exact ordering tie-breakers and rowless view cursors.
6. Map repository models into exact DTOs, including:
   - per-attempt subject/coverage;
   - current issue object/null;
   - explicit retraction nulls;
   - exact observation evidence IDs/count/null semantics;
   - non-nil arrays.
7. Implement the three authenticated read routes with exact query parsing and
   total error precedence.
8. Add fixed catch-up-in-progress and catch-up-failed 503 problems.
9. Add real-store integration for:
   - fresh/list-view/detail/observation cursor chains;
   - history-only/all-retracted;
   - catch-up readiness/failure/recovery;
   - retention cursor 410;
   - close/reopen persistence.
10. Refactor startup coordination:
    - open/migrate Store;
    - construct HTTP with monitoring capability;
    - start HTTP;
    - run existing analysis recovery;
    - start catch-up/recurrence worker;
    - preserve cancellation/restart.
11. Keep MCP construction core-only and exactly six tools.
12. Update all public Local/read contracts and clean-machine QA.

### Acceptance Criteria

- Normal Attention can open fresh monitoring detail without a list cursor.
- After-attempt navigation reuses the exact view snapshot.
- Continuation preserves filters and original page size.
- Equal timestamps paginate without loss/duplicates.
- History-only returns `current_issue_available=false` and
  `current_issue=null`.
- Nullable subject/evidence fields encode exactly as designed.
- Unknown/repeated queries fail 400.
- Route-binding mismatch fails 404.
- Retention/expiry fails 410.
- Catch-up states fail closed only for P0-04 routes.
- Errors reflect no request or stored values.
- Existing fix write security and Attention routes remain unchanged.
- MCP gains no repository option or tool.

### Testing Requirements

- unit cursor/normalization/DTO tests;
- complete HTTP auth/query/error matrix;
- equal-time pagination;
- current issue object/null invariants;
- UUIDv7 evidence validation and unknown evidence nulls;
- migration/catch-up 503 states;
- runtime startup order, cancellation and restart;
- real-store retention/cursor/history integration;
- MCP six-tool and capability-isolation tests;
- public release-surface validation.

### Contracts Consumed

- Service A canonical models;
- Service A typed repository errors/pages;
- Service A recurrence worker and readiness lifecycle.

### Contracts Produced

- exact three-route HTTP contract;
- monitoring-specific opaque cursors;
- browser DTO/error surface;
- runtime orchestration.

### Risks and Callouts

- Do not reuse/widen legacy cursor envelopes.
- `startAnalysisRecovery` currently has no completion sequencing; coordinate it
  explicitly before legacy catch-up convergence.
- P0-03 writes and P0-02 issue views must remain independently usable during
  P0-04 catch-up.
- Do not modify `internal/presentation/localmcp/**`.

## Service C: Embedded Browser

- **Contextual Agent:** Leibniz
- **Owns:** Local HTTP assets and asset-only/static contract tests.
- **Must not edit:** Go server/readmodel/cmd/storage/analysis/detection/MCP/docs.

### Files

- `internal/presentation/localhttp/assets/index.html`
- `internal/presentation/localhttp/assets/app.js`
- `internal/presentation/localhttp/assets/styles.css`
- `internal/presentation/localhttp/attention_assets_test.go`
- `internal/presentation/localhttp/fix_assets_test.go`
- new `internal/presentation/localhttp/recurrence_assets_test.go`

`bootstrap.js` must remain unchanged.

### Tasks

1. Add independently paginated **After attempts** before Issues.
2. Add fixed monitoring filters and visible **Include retracted** control.
3. Relabel existing issue **Recurrence** filter to **Session spread** without
   changing its API values.
4. Add monitoring list state, request generations, filters, cursor pagination,
   catch-up/failed/empty/error states.
5. Replace browser-rendered P0-03 history with authoritative P0-04 monitoring
   detail while preserving eligibility/mutation/draft code.
6. Support:
   - After-attempt view-cursor navigation;
   - fresh normal-Attention detail;
   - bounded attempt pagination;
   - history-only current-issue null state.
7. Add per-annotation lazy observation pages and bounded retained event lookup.
8. Render exact evidence status/count/truncation/unknown semantics.
9. Render matching, incomplete, awaiting, no-match, unavailable, retracted and
   unknown wording exactly.
10. Show same-session counts/caveat without requiring expansion.
11. Preserve historical matching counts after retraction and all-retracted
    opt-in discovery.
12. After record/retract:
    - clear monitoring cursor;
    - fresh reload;
    - focus by annotation ID.
13. Preserve connected stale content with scoped error on same-issue refresh
    failure; switching issues clears stale detail.
14. Keep Issues, Evidence gaps, Sessions, matching sessions, eligibility and
    fix actions independently usable on monitoring failure.
15. Preserve safe text rendering, allowlisted classes, focus restoration,
    connected fallbacks, live regions, mobile scrolling and 44px controls.

### Acceptance Criteria

- Monitoring list precedes but never hides the existing issue list.
- Catch-up/failed 503 affects only monitoring UI.
- P0-03 `GET /fixes` is no longer the browser attempt source.
- Fix POSTs retain existing security/idempotency/deadline behavior.
- Unknown states are neutral and never expose retraction.
- No-match is never success/green/check-mark.
- Same-session continuation caveat appears in summary/detail.
- Retraction preserves historical positive wording.
- Evidence is loaded only on explicit request and never broad-scans timeline.
- Every dynamic value renders as text.
- Partial failures are scoped and recoverable.

### Testing Requirements

- After attempts placement/filter/pagination;
- Session spread label with unchanged query semantics;
- fresh/view/detail/observation cursor paths;
- exact DTO/null/unknown handling;
- catch-up/failed/410 states;
- history-only/all-retracted;
- actionable older-attempt focus;
- same-session/retracted wording;
- lazy UUIDv7 evidence lookup;
- POST refresh/idempotency preservation;
- request-generation stale-response protection;
- keyboard/focus/mobile/static safe-rendering contracts;
- existing Attention/Fix asset regression tests;
- `node --check`.

### Contracts Consumed

- Service B exact monitoring HTTP routes/DTOs/errors.
- Existing P0-02 issue cursor remains the source for fix eligibility.

### Risks and Callouts

- Never mix issue, monitoring and observation cursor families.
- Do not duplicate attempt rendering from P0-03 history.
- Do not auto-load observation evidence.
- Do not infer timing, state, causality or success in JavaScript.

## Integration Checkpoints

1. Service A canonical/storage contracts compile and pass focused tests.
2. Service B consumes exact Service A pages/errors without storage imports
   leaking into presentation.
3. Service C frozen route/DTO assumptions match Service B.
4. Real-store flow:
   attempt → later exact event → projection → job → observation → browser.
5. Numbat positive-only and Belay no-match capability behavior are verified.
6. Retention changes expire cursor chains and preserve positive observations.
7. Close/reopen resumes catch-up/jobs and preserves monitoring history.
8. MCP remains six read-only tools.
9. Full unit/race/vet/release/browser verification is green.
10. Independent final reviews approve all three domains.

## Open Items

None. Any implementation pressure to weaken exact scope, detector capability,
job crash safety, retention generation, or truthfulness must return to design
review rather than silently changing the contract.
