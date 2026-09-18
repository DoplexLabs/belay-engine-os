# Implementation Brief: P0 Issue Fingerprints and Deterministic Detectors

- **Status:** Complete and independently verified
- **Completed:** 2026-09-08

## How to Use This Brief

Before writing any code:

1. Read this entire brief end to end.
2. Read the reference materials below and the approved feature design.
3. Read the existing code in the files and areas called out in the tasks.

When implementing:

4. Work through tasks in order. For each task, implement the change and write
   tests before moving to the next task.
5. Follow existing package boundaries, append-only event guarantees, encrypted
   payload conventions, stable cursor semantics, and fail-open acquisition.
6. Every new code path, edge case, and error path requires an explicit test.

When done:

7. Run the relevant package tests, then `go test ./...`, `go vet ./...`, and
   `make verify`.
8. Confirm every acceptance criterion.
9. Report what was implemented, tested, deviated, and remains open.

## Service

Belay Engine / Belay Local

## Contextual Agents

- Storage and projection foundation: Noether
- Pure detector catalog: Leibniz
- Import/reconciliation integration: Averroes after foundation contracts land

## Summary

Implement the approved Local-only issue projection over immutable canonical
events. The feature adds private project/command identities, durable
dirty-session reconciliation, five conservative detectors, upstream-finding
projection, snapshot-stable issue repository queries, analysis completeness,
and retention/lifecycle integration.

This feature does not add browser routes or MCP tools yet; it freezes and
implements the repository contracts consumed by the next feature.

## Reference Materials

- Approved design:
  `docs/design/p0-01-issue-fingerprints-and-detectors.md`
- Product requirements:
  `research/belay-brd-v2.2.md`
- HLD:
  `research/belay-high-level-tech-design-v1.1.md`
- Existing contracts:
  - `docs/contracts/event-envelope-v1.md`
  - `docs/contracts/read-api-v1.md`
  - `docs/contracts/mcp-v1.md`
- Existing implementation:
  - `internal/canonical/model/event.go`
  - `internal/canonical/numbatmap/map.go`
  - `internal/pipeline/importer.go`
  - `internal/storage/local/store.go`
  - `internal/storage/local/crypto.go`
  - `internal/storage/local/lifecycle.go`
  - `internal/storage/local/retention.go`
  - `internal/storage/local/mutation.go`
  - `internal/localapp/runtime.go`
  - `internal/localapp/tailer.go`

## Cross-Package Contracts

### Opaque identity derivation

- Domain-separated HKDF-SHA256 keys for:
  - project scopes;
  - exact command signatures;
  - issue fingerprints.
- IDs use lowercase, no-padding Base32.
- No raw path or command is persisted or logged.

### Event persistence result

Appending or replaying an event must resolve:

```go
type AppendEventResult struct {
    Inserted       bool
    EventID        string
    ReadGeneration int64
}
```

The event insert and dirty-session target-generation upsert are one
transaction. Duplicate replay returns the existing canonical event ID so
sidecar enrichment may be populated without mutating the event.

### Detector input/output

```go
type EventEnrichment struct {
    CommandSignatureID string
    CommandClass       string
    PermissionClass    string
    Version            string
}

type SessionInput struct {
    SessionID    string
    ProjectScope string
    ScopeQuality string
    Events       []model.Event
    Enrichments  map[string]EventEnrichment
}

type Detector interface {
    ID() string
    Version() string
    FingerprintVersion() string
    Evaluate(context.Context, SessionInput) ([]Match, error)
}
```

One match is allowed per `(session, fingerprint)`. Detector output contains
fixed catalog codes, evidence interval, bounded cited event IDs, confidence,
severity, evidence completeness, and fingerprint dimensions. It contains no
free-form event-derived narrative.

### Projection generation

- One serialized reconciler per store.
- Commit succeeds only when the claimed dirty target equals the current target.
- Every success/failure/pending/truncated transition increments the global
  projection generation and publishes a session-analysis revision.
- Occurrence and analysis revisions are queryable at a cursor generation.

## Implementation Sequence

### Phase A: storage and identity foundation

1. Add domain-separated key derivation and opaque-ID helpers.
2. Add exact path/command normalization with golden tests.
3. Add migration 005 tables, constraints, indexes, and lifecycle encoding
   support.
4. Change event append to return the canonical ID/generation and transactionally
   mark the session dirty.
5. Add scope/enrichment upsert APIs, conflict semantics, and dirty marking.
6. Add revisioned issue/analysis replacement and snapshot repository queries.
7. Integrate retention, byte accounting, mutation authorization, and diagnostic
   deduplication.

### Phase B: pure detector catalog

1. Implement detector interfaces and bounded evaluation helpers.
2. Implement conservative historical/live coalescing.
3. Implement:
   - explicit command failure;
   - repeated command attempts, info/experimental;
   - explicit permission denial;
   - terminal verification evidence gap with exact compatibility matrix;
   - terminal unresolved explicit verification failure.
4. Implement upstream-finding match normalization separately from raw Numbat
   parsing.
5. Add threshold, pairing, ordering, unknown-outcome, negative-corpus, limit,
   panic/error, and deterministic-fingerprint tests.

### Phase C: acquisition and reconciliation integration

1. Derive scope/enrichment from validated records before minimization.
2. Populate enrichment on inserted and duplicate events.
3. Signal reconciliation after historical/direct import.
4. Signal live reconciliation only after durable spool-cursor advancement.
5. Drain durable dirty sessions on Local startup and catalog upgrades.
6. Isolate per-session/per-detector failures and preserve acquisition progress.
7. Project immutable Numbat findings into issue occurrences.
8. Expose detector state in `doctor`.

### Phase D: verification and documentation

1. Add end-to-end Codex and Claude fixtures.
2. Add crash/restart, reverse-generation commit, concurrent import, retention,
   migration 004→005, key loss, raw SQLite/WAL privacy, and catalog-upgrade
   tests.
3. Add repository contract fixtures for issue list/detail consumers.
4. Update read/MCP candidate contracts and Local requirements to document the
   approved scope without claiming remediation or prevention.

## Testing Requirements

- Unit coverage for every helper, detector, repository method, and failure path.
- Golden identity fixtures across restarts, stores, path aliases, whitespace,
  positional command targets, and secret-pattern inputs.
- Exact 10,000/10,001-event, 50-citation, and 100-match boundaries.
- Replay and duplicate enrichment tests.
- Historical/live threshold de-duplication tests.
- Generation-CAS and stale-writer rejection tests.
- Projection cursor stability and expiration tests.
- Mixed analysis-status/list-completeness tests.
- Retention result equals a fresh database containing retained events only.
- Existing event/finding encryption and append-only guarantees remain intact.
- No raw path, command, prompt, output, or diagnostic text in SQLite/WAL bytes.
- Full tests, vet, architecture rules, and release-surface verification.

## Acceptance Criteria

The approved design's Appendix acceptance gates are normative. Implementation
is incomplete until all gates have direct automated evidence or an explicitly
documented manual launch gate.

## Risks and Callouts

- Do not change `belay.event.v1`; use sidecars for old-event enrichment.
- Do not run reconciliation in importer/event transactions.
- Do not advance a live cursor after reconciliation; cursor advancement comes
  first.
- Do not present exact fingerprint matching as semantic root-cause matching.
- Do not report empty issues as complete while any session analysis is pending,
  failed, or truncated.
- Do not add write-capable MCP or fix execution in this feature.
