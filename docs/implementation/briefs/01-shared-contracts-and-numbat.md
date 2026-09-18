# Implementation Brief 01: Shared Contracts and Numbat Adapter

## How to Use This Brief

Before writing any code:
1. Read this entire brief end to end.
2. Read the reference materials below and understand the full design.
3. Read the upstream Numbat files identified by the adapter spike.

When implementing:
4. Work through tasks in order; implement each task and its tests together.
5. Follow upstream public contracts without importing or modifying Numbat
   internals.
6. Every new path and error case requires explicit test coverage.

When done:
7. Run the full test suite.
8. Confirm every acceptance criterion.
9. Report implementation, tests, deviations, and open issues.

## Service

Shared contracts and Numbat adapter

## Contextual agent

None required; implementation can be handled in `belay-engine`.

## Summary

Establish the only supported boundary between pinned Numbat releases and Belay.
Convert upstream normalized observations into the canonical event envelope
without leaking prohibited content. This work unblocks every other service.

## Reference materials

- `research/belay-brd-v2.2.md`
- `research/belay-high-level-tech-design-v1.1.md`
- `docs/contracts/`
- `docs/decisions/0001-stack-and-repository-boundaries.md`
- Pinned upstream Numbat release and its public schemas/docs

## Design context

Numbat remains unmodified and outside the Belay package boundary. Belay accepts
its versioned NDJSON record stream from a private file and maps known records
into one Local/Teams event contract. Unknown versions are quarantined locally
and never affect the agent.

## Tasks

1. **Record the Numbat research pin and release gap**
   - Record exact commit, schema, checksums, license, supported platforms, and
     build provenance.
   - Current discovery: commit `f0778c09dc48281aa93a3887d05096c0a1f3f9f7`
     emits schema 0.3.0, while the available release tag is v0.2.0.
   - Acceptance: the research exception is explicit and launch cannot silently
     depend on an untagged commit.

2. **Spike public integration interfaces**
   - Exercise `scan --emit all`, private-file hook output, and historical
     reconstruction with Codex and one second harness. Defer OTLP from the
     smallest spike.
   - Acceptance: sanitized fixture records and a written capability matrix
     identify fields, session IDs, ordering, and coverage depth.

3. **Publish the mapping contract**
   - Map each supported upstream record/version to `belay.event.v1`.
   - Acceptance: every mapped field has source, transformation, limits, and
     prohibited-content treatment documented.

4. **Create authoritative schemas**
   - Add machine-readable JSON Schema for event, ingest, read, and MCP payloads.
   - Acceptance: documentation examples validate; invalid/prohibited fixtures
     fail.

5. **Add code generation and compatibility checks**
   - Generate Go types and later TypeScript types from schemas.
   - Acceptance: CI fails on stale generated output or breaking contract change.

6. **Build conformance fixtures**
   - Start with two harnesses; grow to five before launch.
   - Acceptance: a Numbat tag update cannot merge until all fixtures round-trip.

## Testing requirements

- Golden fixtures for every supported record family
- Unknown version/type and missing-field tests
- Fuzz malformed JSON and bounded strings/maps
- Secret, prompt, transcript, file-body, stdout, environment, path, and URL
  leakage fixtures
- Deterministic event ID and ordering tests
- Compatibility test across the pinned and candidate Numbat release

## Contracts produced

- Numbat mapping contract
- Canonical event schema
- Generated contract artifacts
- Sanitized conformance fixtures

## Contracts consumed

- Upstream Numbat public schemas and output interfaces
- Teams transmitted-field policy

## Sequencing notes

This is the first implementation slice. Edge storage and Teams ingest may begin
only after required event fields and transmission policy are frozen.

## Risks and callouts

- Do not import upstream `internal/` packages.
- Consume `scan --emit all`/hook NDJSON, not `timeline --format json`.
- Route `event`, `finding`, `scan_summary`, and `diagnostic` separately; ignore
  `indicator` for M0 and reject `enforcement`.
- Use `scan_summary` completion state rather than process exit alone.
- Do not pass through unknown fields.
- Structured tool arguments may leak content even after generic secret
  redaction; allowlist individual fields.
- Lack of an upstream stable ID requires deterministic Belay identity rules.
