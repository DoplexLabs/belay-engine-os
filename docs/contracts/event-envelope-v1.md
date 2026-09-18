# Candidate Contract: Belay Event Envelope V1

- **Status:** Candidate; freeze after Numbat adapter spike
- **Contract ID:** `belay.event.v1`
- **Owners:** shared contracts

## Purpose

One immutable event representation serves Belay Local and Belay Teams. Local
stores this envelope without tenant identity. Teams adds authenticated
workspace, machine, and receipt fields at ingest.

## Required envelope

```json
{
  "schema_version": "belay.event.v1",
  "event_id": "0199d7a5-6bc0-7b9e-8c7c-55c16b5d10f2",
  "installation_id": "inst_01K4BELAY",
  "occurred_at": "2026-09-08T18:12:43.123456Z",
  "observed_at": "2026-09-08T18:12:43.201004Z",
  "source": {
    "engine": "numbat",
    "engine_version": "research-commit",
    "schema_version": "0.3.0",
    "record_type": "event",
    "run_id": "optional-upstream-run-id",
    "record_id": "upstream-event-42",
    "kind": "artifact",
    "agent": "codex",
    "adapter_version": "numbat-0.3.0/v1",
    "deduplication_key": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "sequence": 42
  },
  "session": {
    "key": "ses_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  },
  "observation": {
    "type": "command.result",
    "actor": "tool",
    "action": "command",
    "outcome": "failed",
    "exit_code": 1,
    "summary": "npm --",
    "resource": {
      "kind": "command",
      "name": "npm"
    },
    "details": {
      "tool_call_id": "call-42",
      "tags": ["test.contract"]
    }
  },
  "coverage": {
    "depth": "tool_call",
    "confidence": "high"
  },
  "redaction": {
    "policy_version": "belay.redaction.v1",
    "fields_removed": 0,
    "secrets_removed": 0
  },
  "historical": {
    "is_historical": false
  }
}
```

## Field rules

### Identity

- `event_id` is always a Belay-generated UUIDv7.
- `event_id` matches
  `^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`.
- Numbat `record_id` is required source metadata. Hook and OTLP identifiers are
  not globally stable across process runs.
- `installation_id` is random and local. It is not a user, workspace, or device
  hardware identifier.
- `installation_id` matches `^inst_[A-Za-z0-9_-]{8,128}$`.
- `session` contains only `key`; `key` matches `^ses_[a-f0-9]{32}$`.
- Local uniqueness is `(installation_id, event_id)`.
- Teams uniqueness is `(workspace_id, machine_id, event_id)`.

### Time and ordering

- Timestamps are UTC RFC 3339 with microsecond precision when available.
- Timeline order is source sequence, then occurrence time, then UUIDv7.
- `received_at` is Teams-only and must not become primary timeline order.

### Source

Required Numbat source fields are `engine`, `engine_version`,
`schema_version`, `record_type`, `record_id`, `kind`, `agent`,
`adapter_version`, `deduplication_key`, and `sequence`. `run_id` is optional.

`deduplication_key` matches `^sha256:[a-f0-9]{64}$`. `sequence` is an integer
greater than or equal to one.

Numbat `source.kind` values:

- `hook`
- `artifact`
- `otel`

Belay-generated records use a separate source engine and must not masquerade as
Numbat records. Unknown Numbat source kinds are quarantined.

### Observation

Numbat 0.3.0 event types preserved by the candidate adapter:

- `session.start`
- `session.end`
- `prompt.user`
- `message.assistant`
- `message.reasoning`
- `tool.call`
- `tool.result`
- `command.exec`
- `command.result`
- `file.read`
- `file.write`
- `file.delete`
- `permission.requested`
- `permission.approved`
- `permission.denied`
- `config.agent`
- `config.mcp`
- `network.indicator`

`type`, `actor`, `action`, and `outcome` are required. `outcome` is one of
`succeeded`, `failed`, `interrupted`, or `unknown`. Absence of evidence is
represented as `unknown` and must never be normalized to `succeeded`.

`summary` is bounded, locally redacted display text. It is not a prompt,
completion, stdout/stderr stream, file body, or diff.

Optional structured metadata belongs in the closed `details` object. Its only
allowed fields are `tool_call_id`, `decision`, `approval_required`,
`approval_decision`, `mcp_server`, `mcp_tool`, `model`, `model_provider`,
`cli_version`, `sub_agent`, `diff_sha256`, `diff_bytes`, and `tags`. The
canonical event has no `attributes` field and permits no arbitrary metadata
map.

For `prompt.user`, `message.assistant`, and `message.reasoning`, Belay may retain
the event's existence, time, actor, and session relationship but must discard
content/preview fields.

### Coverage and confidence

Candidate `coverage.depth` values:

- `artifact`
- `lifecycle`
- `tool_call`
- `synchronous_hook`
- `otlp`

Numbat extraction confidence is preserved as `high`, `medium`, or `low`.
Belay-derived numeric metric confidence belongs to projections, not the source
event.

### Historical records

- Historical events preserve original occurrence time when known.
- `historical.is_historical` is required.
- `historical.reconstruction_source` is optional when `is_historical=false`;
  when present in that state, it may be `null` or a string of at most 128
  characters.
- When `is_historical=true`, `reconstruction_source` is required and must be a
  non-empty string of at most 128 characters. The source-level
  `adapter_version` remains required for every event.
- Historical delivery follows the same identity and deduplication rules.

## Local-only and Teams-only fields

The canonical event must not contain `workspace_id`, cloud user identity, cloud
credential IDs, or billing information.

Teams adds this ingest envelope without mutating the canonical event:

```json
{
  "workspace_id": "ws_...",
  "machine_id": "mach_...",
  "received_at": "2026-09-08T18:12:45.000000Z",
  "credential_id": "cred_..."
}
```

## Prohibited content

The schema and adapter must reject:

- Raw user prompts
- Model completions or reasoning
- File contents and diffs
- Command stdout/stderr streams
- Environment snapshots
- Credentials, tokens, cookies, or authorization headers
- Unbounded arbitrary maps

## Freeze criteria

1. Map five representative upstream Numbat records without prohibited content.
2. Map all 18 upstream event types and every record type.
3. Round-trip fixtures through JSON Schema and field/event co-occurrence
   validation.
4. Prove deterministic ordering and duplicate handling.
5. Prove unknown event types do not become successful outcomes.
6. Validate the same fixtures in Local and Teams contract tests.
