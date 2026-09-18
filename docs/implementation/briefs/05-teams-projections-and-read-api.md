# Implementation Brief 05: Teams Projections and Read API

## How to Use This Brief

Before writing any code:
1. Read this brief, shared contracts, and Brief 03 outputs.
2. Inspect hosted database/worker conventions.
3. Compare Local projector fixtures before implementing.

When implementing:
4. Reuse shared semantics without copying private variants.
5. Write tests with every projector and endpoint.
6. Enforce workspace scope in repository APIs.

When done:
7. Run projector, database, isolation, and read-contract tests.
8. Confirm acceptance criteria.
9. Report implementation, tests, deviations, and issues.

## Service

Belay Teams projection worker and hosted read API

## Contextual agent

None currently; implement in the future private `belay-cloud` repository.

## Summary

Project hosted immutable events into session timelines and expose the same read
shapes used by Local. This establishes the first cloud value without adding
fleet analytics or billing complexity prematurely.

## Reference materials

- Belay BRD v2.2, FR5–FR7 and FR14
- Belay HLD v1.1, §§5.5–5.9
- `docs/contracts/read-api-v1.md`
- Briefs 01, 03, and 04 outputs

## Tasks

1. **Implement durable projector jobs**
   - Postgres leases, retries, idempotency keys, versions, and poison-event
     quarantine.
   - Acceptance: worker crash/retry creates no duplicate projections.

2. **Implement hosted session projector**
   - Match Local event ordering, session boundaries, outcomes, coverage, and
     confidence.
   - Acceptance: shared fixtures are semantically identical to Local.

3. **Implement workspace-scoped repositories**
   - Sessions and events require explicit authenticated workspace context.
   - Acceptance: cross-workspace queries fail closed under pooled connections.

4. **Implement hosted read endpoints**
   - `/sessions`, `/sessions/{id}`, and `/sessions/{id}/events` first.
   - Acceptance: cursor/freshness/error shapes pass the shared contract suite.

5. **Add `data_through` and projection metrics**
   - Expose queue lag and projector version without leaking payloads.
   - Acceptance: delayed projection is distinguishable from missing data.

6. **Build minimal Teams timeline**
   - UI consumes only hosted read endpoints.
   - Acceptance: no private query side door and every row traces to an event ID.

## Testing requirements

- Job lease/retry/dead-letter tests
- Projector idempotency/rebuild tests
- Local/Teams shared fixture parity
- Cross-workspace isolation including pooled-connection reuse
- Cursor stability with late events
- Projection lag/freshness tests
- Browser contract and payload-escaping tests

## Contracts produced

- Hosted session projections
- Teams read API session resources
- Projection freshness/version metadata

## Contracts consumed

- Canonical hosted events
- Shared projector semantics
- Shared read contract

## Sequencing notes

Begins after atomic ingest and Local projector semantics exist. The M2 release
gate is shared Local/Teams timeline parity.

## Risks and callouts

- Do not let hosted convenience produce a richer private session shape than
  Local; extend the shared contract instead.
- Row-level security is defense in depth, not a substitute for explicit
  workspace repository scope.
- Preserve normalized events when a projector fails so a corrected version can
  rebuild.
