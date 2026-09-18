# Implementation Brief 04: Local Sessions and Read API

## How to Use This Brief

Before writing any code:
1. Read this brief, the shared read contract, and Brief 02 outputs.
2. Inspect Local repository and fixture conventions.
3. Understand source/coverage uncertainty before classifying outcomes.

When implementing:
4. Implement deterministic projectors before HTTP/UI adapters.
5. Write tests with each task.
6. Preserve event traceability and unknown outcomes.

When done:
7. Run projector, API, browser-contract, and no-network tests.
8. Confirm acceptance criteria.
9. Report implementation, tests, deviations, and issues.

## Service

Belay Local session projection and loopback read API

## Contextual agent

None required; implementation lives in `belay-engine`.

## Summary

Turn immutable Local events into deterministic session/timeline projections and
serve them through the shared read contract. This is the first visible Local
product slice and the data source for Local timeline and MCP.

## Reference materials

- Belay BRD v2.2, FR5, FR7, FR10, FR13–FR14
- Belay HLD v1.1, §§5.7–5.10
- `docs/contracts/read-api-v1.md`
- `docs/contracts/mcp-v1.md`
- Briefs 01 and 02 outputs

## Tasks

1. **Define projector metadata**
   - Projector name/version, source-event references, deterministic ordering,
     rebuild semantics, and outcome confidence.
   - Acceptance: deleting projections and rebuilding produces identical output.

2. **Implement session assembly**
   - Prefer harness session IDs; otherwise use versioned adapter-specific
     bounded rules.
   - Acceptance: late/historical/out-of-order fixtures produce stable sessions.

3. **Implement outcome and timeline projection**
   - Preserve completed, failed, stalled, interrupted, and unknown with source
     and confidence.
   - Acceptance: absence of evidence never becomes completed.

4. **Implement loopback read API**
   - Machine, session list/detail/events, local findings, and stats.
   - Acceptance: binds to loopback, uses scoped credentials, and passes shared
     schemas.

5. **Build minimal Local timeline**
   - Static UI consumes only the read API.
   - Acceptance: every rendered row exposes its source event ID and historical
     state.

6. **Implement Local MCP adapter**
   - Add only tools frozen in `mcp-v1`.
   - Acceptance: offline operation, response bounds, and injection fixtures
     pass; no write tool exists.

## Testing requirements

- Projection idempotency/rebuild tests
- Session boundary and confidence fixtures per harness
- Late/out-of-order/historical ordering tests
- Loopback-only and credential tests
- Shared read schema/browser contract tests
- MCP injection, escaping, pagination, and response-size tests
- No-network end-to-end test

## Contracts produced

- Local sessions, timelines, findings, and stats
- Local read adapter
- Local MCP adapter

## Contracts consumed

- Canonical Local events
- Shared read and MCP contracts

## Sequencing notes

Session work begins after Local storage is stable. UI and MCP begin after the
projector/read shapes pass fixtures.

## Risks and callouts

- Harness coverage differs; confidence is required product data.
- Raw intervention text is prohibited. Render event type/timing and bounded
  sanitized summaries only.
- A simple useful timeline is more important than visual polish in M2.
