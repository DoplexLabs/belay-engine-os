# Implementation Brief 02: Belay Edge and Local Persistence

## How to Use This Brief

Before writing any code:
1. Read this entire brief and all reference materials.
2. Read the frozen event/mapping contracts.
3. Inspect current repository conventions and tests.

When implementing:
4. Complete tasks in order and write tests with each task.
5. Keep all Numbat interaction behind the adapter.
6. Cover every error and fail-open path.

When done:
7. Run all tests and Local no-network checks.
8. Confirm acceptance criteria.
9. Report implementation, tests, deviations, and issues.

## Service

Public Belay edge and Local storage

## Contextual agent

None required; implementation lives in `belay-engine`.

## Summary

Build the accountless endpoint foundation: supervise Numbat, normalize/redact
events, persist immutable Local history, and expose safe query primitives. The
agent harness must remain unaffected under every Belay failure.

## Reference materials

- Belay BRD v2.2, FR1–FR5 and FR10
- Belay HLD v1.1, §§5.4, 5.6, 5.10, 5.13–5.14
- `docs/contracts/event-envelope-v1.md`
- `docs/contracts/transmitted-fields-v1.md`
- `docs/contracts/local-teams-state-v1.md`
- Brief 01 outputs

## Design context

Local is the primary trust/acquisition product, not a demo mode. It works
offline, sends no telemetry, stores only minimized/redacted events, and remains
usable regardless of Teams state.

## Tasks

1. **Scaffold the Go edge**
   - Establish command, internal module, build, lint, unit-test, and fixture
     conventions.
   - Acceptance: one command builds and tests on macOS.

2. **Implement Numbat process adapter**
   - Start/supervise the pinned executable and consume only approved outputs.
   - Acceptance: Numbat crash/restart does not affect the harness or duplicate
     accepted events.

3. **Implement normalization and disclosure policy**
   - Convert known records, minimize fields, apply bounded redaction, and reject
     unsafe/unknown payloads.
   - Acceptance: prohibited fixtures never reach storage; payload-free counters
     record drops.

4. **Implement Local key and storage lifecycle**
   - Create installation identity, OS-keystore-backed data key, SQLite
     migrations, append-only events, and indexes.
   - Acceptance: restart, migration, pruning, corrupt record, and missing-key
     behaviors are explicit and tested.

5. **Implement historical scan**
   - Scan supported artifacts newest-first without delaying live capture.
   - Acceptance: historical records are labeled, deduplicated, and ordered by
     occurrence rather than import time.

6. **Implement Local query repository**
   - Provide bounded machine, session-event, finding, and summary primitives for
     later read adapters.
   - Acceptance: no API layer reads SQLite directly outside the repository.

7. **Add endpoint diagnostics**
   - Expose local-store size, drop counts, versions, redaction errors, and Teams
     state without event payloads.
   - Acceptance: diagnostics contain identifiers/counts only.

## Testing requirements

- Unit coverage for normalization, redaction, identity, storage, and pruning
- Fuzz redaction and malformed upstream records
- SQLite migration, corruption, recovery, and concurrent-read tests
- No-network integration test
- Process crash/restart and full-disk simulation
- Historical/live deduplication and ordering tests
- Test proving no Doplex endpoint is contacted in `local_only`

## Contracts produced

- Local persisted event representation
- Local repository/query interfaces
- Endpoint diagnostics

## Contracts consumed

- Numbat mapping and canonical event contract
- Local/Teams state contract
- Transmitted-field policy

## Sequencing notes

Begin after Brief 01 freezes required fields. Local session projections and read
API consume this repository in Brief 04.

## Risks and callouts

- Application-level encryption must preserve indexable envelope fields without
  exposing payloads.
- Redaction failure drops/quarantines data but never blocks the agent.
- A Local retention decision must not be coupled to Teams acknowledgement.
