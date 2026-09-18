# Numbat Adapter Spike

- **Status:** Validated research checkpoint
- **Inspected upstream commit:** `f0778c09dc48281aa93a3887d05096c0a1f3f9f7`
- **Observed upstream schema:** `0.3.0`
- **Observed release-tag gap:** only `v0.2.0` is currently tagged
- **Validated:** 2026-09-08

## Live conformance checkpoint

The adapter was exercised against local Codex and Claude Code artifacts using
stock Numbat built from the inspected commit:

- Binary version: `numbat dev+f0778c09dc48 (schema 0.3.0)`
- Binary SHA-256:
  `2443f1b001323a23aa4896ae903d641fb77e0f2867f09c93a5a558bef0897493`
- Codex NDJSON SHA-256:
  `e6435202d1ffaecaf0dea6de24bff0dd9c4d317811878e3b9cb6fd2e41138a71`
- Claude NDJSON SHA-256:
  `bb79ef346af4fae8bd8916eaef6f426f79052a338c25bde1b609472751e95903`

The Codex run produced 148 records: 144 events, three indicators, and one
complete scan summary. Belay accepted all 144 events, ignored the three
indicators, accepted the summary, and produced no quarantine or malformed
records. Replaying the same output produced 144 event duplicates and no new
canonical events.

The Claude Code run produced 14 records: 11 events, two indicators, and one
complete scan summary. Belay accepted all 11 events, ignored the two
indicators, accepted the summary, and produced no quarantine or malformed
records. Replaying the same output produced 11 event duplicates and no new
canonical events.

The live event-type coverage was:

| Harness | Event types observed |
|---|---|
| Codex | `session.start`, `prompt.user`, `message.assistant`, `tool.call`, `tool.result`, `command.exec`, `command.result`, `session.end` |
| Claude Code | `session.start`, `prompt.user`, `message.assistant`, `command.exec`, `command.result`, `config.mcp`, `session.end` |

A byte scan of the resulting Local database found none of the local username,
home-directory prefix, or endpoint hostname used by the live runs.

Raw artifacts and complete Numbat outputs are local-only research inputs and
remain uncommitted. The test fixtures under
`testdata/numbat/v0.3.0/live/` are manually re-authored from observed
field-presence and event-type shapes. Every identifier, timestamp, endpoint
field, path, project, command, URL, content value, evidence reference, and
other user-derived value is synthetic. No raw record or opaque source
identifier is copied into the fixtures.

## Decision to freeze

Belay integrates with stock Numbat exclusively through its versioned NDJSON
process boundary.

Belay does not import Numbat `internal/` packages, modify Numbat source, consume
its presentation-oriented timeline JSON, or treat Numbat state databases as the
Belay event store.

## Recommended process boundary

```text
Agent artifacts/hooks
       ↓
stock pinned Numbat
       ↓ private NDJSON file, --emit all
Belay importer
       ↓ strict record routing and field validation
minimize/redact
       ↓
Belay canonical Local store
       ↓
Local read API/MCP and optional Teams queue
```

Use file-first delivery so Belay storage or cloud failures cannot delay a
harness hook. HTTP is not placed on the hook execution path.

## Upstream facts that shape the adapter

- Numbat is a Go executable, not a reusable public SDK.
- Operational code is under Go `internal/`.
- The record stream contains `event`, `finding`, `enforcement`, `indicator`,
  `scan_summary`, and `diagnostic`.
- Schema 0.3.0 has 18 closed event types.
- Source kinds are `artifact`, `hook`, and `otel`.
- Extraction confidence is `high`, `medium`, or `low`.
- Every record includes endpoint data such as hostname and username that Belay
  must not persist unchanged.
- Numbat output may include bounded preview/full-content fields; Belay drops
  prohibited content before canonical persistence.
- `scan_summary` reports `complete`, `partial`, or `error`.
- Hook/OTLP record IDs are not globally stable across process runs.
- Numbat supports enforcement records; Belay V1 rejects them.

## Record routing

| Numbat record | Belay M0 behavior |
|---|---|
| `event` | Validate, minimize/redact, map to canonical event |
| `finding` | Store as Local finding with source/event references |
| `scan_summary` | Update historical import-run completeness |
| `diagnostic` | Bounded payload-free operational diagnostic |
| `indicator` | Ignore for M0 |
| `enforcement` | Reject; never store or present |

## Validated vertical slice

1. Built the exact inspected Numbat commit for research.
2. Ran both Codex and Claude Code artifacts through `numbat scan --emit all`.
3. Recorded checksums and created privacy-safe, shape-derived fixtures.
4. Strictly validated record type, upstream schema, event type, and
   event/field co-occurrence.
5. Assigned Belay UUIDv7 identities while preserving upstream identity
   separately.
6. Minimized/redacted into the canonical event.
7. Persisted into an encrypted temporary Local store.
8. Rendered fixture responses through `list_sessions` and
   `get_session_timeline`.

This checkpoint does not cover real hook installation, OTLP, findings,
diagnostics, enforcement emitted by a live run, cloud ingest, signing, browser
UI, long-running or corrupt artifact recovery, non-macOS artifacts, or every
event type in each harness. The synthetic all-event-types fixture remains the
source of full 18-event adapter coverage.

## Acceptance tests

1. Real Numbat invocations for Codex and Claude Code produce NDJSON accepted by
   the adapter.
2. All 18 event types pass mapping fixtures.
3. Unknown schema versions, record types, fields, and invalid field/event
   combinations quarantine without stopping later valid records.
4. Replaying the same artifact scan creates zero duplicate canonical events.
5. Belay UUIDv7 is always primary identity; Numbat IDs remain source metadata.
6. Seeded hostname, username, bearer token, raw prompt, home path, URL query
   secret, transcript, file content, and stdout do not appear in stored JSON or
   database bytes.
7. `enforcement` cannot reach storage, APIs, or MCP.
8. Malformed/oversized lines increment payload-free diagnostics and import
   continues.
9. `scan_summary=partial|error` does not mark an import complete.
10. Session ordering is source order, occurrence time, then Belay ID.
11. Adapter startup verifies expected Numbat version and accepted schema.
12. Binary/source checksum matches the recorded research checkpoint.

## Release decision still required

Belay's launch rule requires a released Numbat tag, while the inspected 0.3.0
wire behavior is not currently represented by a release tag. M0 research may
proceed against the exact commit as an explicit research exception. This
checkpoint does not approve a production pin. Before production packaging,
choose one:

1. Wait for an upstream release containing schema 0.3.0.
2. Pin v0.2.0 and reduce the adapter to its released schema.
3. Approve a time-bounded commit-pin exception with checksum and provenance.

The implementation must not silently make this decision.

## Packaging corrections

- Package upstream `LICENSE` and `THIRD_PARTY_LICENSES.txt`.
- Package `NOTICE` only if the selected release contains one.
- Derive rule/category claims from the selected pin. The inspected commit has
  51 rules in 12 categories, not the earlier 52/11 claim.
- Belay, not Numbat, owns signed rule manifests, compatibility checks, staging,
  atomic activation, and rollback.
