# Candidate Contract: Teams Ingest API V1

- **Status:** Candidate
- **Contract ID:** `belay.ingest.v1`
- **Endpoint:** `POST /v1/events`
- **Authentication:** machine signature over TLS

## Authoritative schemas

The machine-readable Draft 2020-12 contracts are:

| Message | Schema ID | Repository path |
|---|---|---|
| Request | `https://schemas.doplex.ai/belay/ingest/v1/request.schema.json` | `contracts/ingest/v1/request.schema.json` |
| Acknowledgement | `https://schemas.doplex.ai/belay/ingest/v1/acknowledgement.schema.json` | `contracts/ingest/v1/acknowledgement.schema.json` |
| Problem details | `https://schemas.doplex.ai/belay/ingest/v1/problem.schema.json` | `contracts/ingest/v1/problem.schema.json` |

The request schema references the canonical event schema by its stable ID,
`https://schemas.doplex.ai/belay/event/v1/event.schema.json`. Contract-owned
objects are closed: fields not named by these schemas are rejected, and there
are no arbitrary extension maps.

## Request

Headers:

```text
Content-Type: application/json
Content-Encoding: gzip (optional)
Belay-Credential-ID: cred_...
Belay-Timestamp: 2026-09-08T18:12:45Z
Belay-Nonce: base64url-random
Belay-Content-SHA256: lowercase-hex
Belay-Signature: base64url-ed25519-signature
```

Signature input is a canonical byte sequence containing method, path,
credential ID, timestamp, nonce, and content digest. The exact canonicalization
must be frozen with cross-language fixtures before implementation.

Valid body:

```json
{
  "schema_version": "belay.ingest.v1",
  "batch_id": "0199d7a5-6dc1-7a2b-8c3d-55c16b5d10f2",
  "client_watermark": 812,
  "events": [
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
  ]
}
```

`batch_id` is a lowercase UUIDv7. `client_watermark` is a nonnegative integer.
Each request contains between 1 and 500 canonical events.

The body must not contain authoritative workspace or machine identity. The
server resolves both from the credential.

## Candidate limits

- Compressed request: 1 MiB
- Decompressed request: 8 MiB
- Events per batch: 500
- Event size: 16 KiB
- Timestamp skew: 5 minutes for live requests
- Nonce replay window: 10 minutes
- Authenticated admission rate: 60 attempts per credential per fixed 60-second
  Postgres window

These are safe starting values, not launch promises. The M0 admission rate is
configurable: the maximum attempt count must be between 1 and 1,000, and the
fixed window must be between 1 and 3,600 seconds. Fixed windows can permit a
boundary burst of up to twice the configured count.

## Validation order

1. Reject an unsupported media type or a body larger than the compressed-size
   limit.
2. Resolve the credential and its workspace and machine scope, enforce active
   and entitled state, validate timestamp skew and nonce syntax, verify the
   digest of the compressed request bytes, and verify the signature.
3. In a durable Postgres admission transaction, obtain one
   Postgres-authoritative timestamp and enforce the per-credential rate limit
   before nonce replay protection. An over-limit request returns `429` without
   reserving its nonce. An active nonce replay is rejected. A successful
   admission commits its rate accounting and nonce reservation.
4. After successful admission, accept only identity or gzip content encoding,
   decompress if necessary, and enforce the decompressed-size limit.
5. Parse JSON, validate the closed batch and event schemas, enforce event-count
   and serialized event-size limits, and enforce the transmitted-field policy.
6. In a separate transaction, check batch idempotency and atomically insert
   the batch, new events, and projection job.
7. Return an acknowledgement only after the persistence transaction commits.

Admission is intentionally durable before payload decoding and validation. A
successfully admitted request therefore consumes its rate attempt and nonce
even when decompression, JSON parsing, schema validation, or the later
persistence transaction fails.

## Responses

### `202 Accepted`

New events were durably committed.

```json
{
  "request_id": "req-01K4BELAY8R7Y6T5S4Q3P2N1M0",
  "batch_id": "0199d7a5-6dc1-7a2b-8c3d-55c16b5d10f2",
  "accepted": 1,
  "duplicates": 0,
  "acknowledged_watermark": 812
}
```

### `200 OK`

The batch was a valid duplicate replay and no new event was inserted. It uses
the same acknowledgement schema; `accepted` is zero and `duplicates` is the
number of already-present events. For every acknowledgement, `accepted` plus
`duplicates` equals the request event count and is therefore at most 500.

### Errors

- `400` malformed JSON or invalid request framing
- `401` unknown, invalid, expired, or revoked credential/signature
- `403` credential is valid but not entitled to ingest
- `409` batch ID reused with a different content digest
- `413` compressed/decompressed/event limits exceeded
- `415` unsupported content encoding/type
- `422` bounded batch, event, or transmitted-field validation failure
- `429` authenticated credential rate limit; includes `Retry-After`
- `500` internal server error
- `503` temporary ingest dependency failure; the client may retry the same body
  and IDs with fresh authentication attempt headers

Errors use `application/problem+json`, include a request ID, and never echo
event payloads. The bounded response follows the current API's RFC 9457-style
shape:

```json
{
  "type": "urn:belay:problem:batch-digest-conflict",
  "title": "Batch conflict",
  "status": 409,
  "detail": "The batch ID was previously used with different content.",
  "request_id": "req-01K4BELAY8R7Y6T5S4Q3P2N1M0"
}
```

The `429` body has the same payload-free shape and uses problem type
`urn:belay:problem:ingest-rate-limit`. Its `Retry-After` response header is the
whole number of seconds, with a minimum of one, until the
Postgres-authoritative fixed window resets. Over-limit attempts increment a
bounded aggregate rejection counter; they do not reserve a nonce or create a
per-attempt audit row.

## Idempotency

- `batch_id` is unique per credential with a stored content digest.
- Event uniqueness is `(workspace_id, machine_id, event_id)`.
- Reusing a batch ID with different content is a conflict and security audit
  event.
- Admission is scoped by workspace and credential. Rate-limit state is one
  fixed-window row per credential, and nonce state is keyed by credential and
  nonce.
- Rate enforcement occurs before nonce enforcement. Authenticated attempts
  within the limit count even when they later fail nonce, payload, or
  persistence checks.
- A nonce is active while its stored `seen_at` is later than the
  Postgres admission time minus 10 minutes. At the exact 10-minute boundary it
  may be atomically reclaimed. Concurrent use of one nonce admits at most one
  request.
- Active nonce replays and over-limit attempts update saturating aggregate
  counters rather than writing an unbounded audit record per attempt.
- Each nonce-processed admission performs opportunistic cleanup of at most 100
  expired nonce rows.
- Admission and payload persistence are separate transactions. Successful
  admission remains committed if payload validation or persistence fails.
  For a new valid batch, the batch row, event rows, and projection job commit
  atomically in the persistence transaction.

## Retry semantics

An ambiguous or retryable attempt preserves the exact request body and content
digest. In particular, it preserves `batch_id`, every `event_id`, event
ordering, and `client_watermark`.

Each attempt generates a fresh `Belay-Timestamp`, a never-before-used
`Belay-Nonce`, and a fresh `Belay-Signature` over the new timestamp and nonce
plus the unchanged content digest. A client must never reuse a prior timestamp,
nonce, or signature. This rule also applies after validation failure,
persistence failure, `429`, and `503`: the body, IDs, ordering, watermark, and
digest stay fixed while all three authentication attempt values are replaced.

## Acceptance tests

1. Unsupported media type and compressed-size violations fail before
   authentication or admission.
2. Cross-workspace identity supplied in a body cannot override credential
   scope.
3. Invalid signature, digest, timestamp, and revoked credential fail before
   admission or payload persistence.
4. The candidate rate defaults and configuration bounds are enforced.
   Per-credential fixed windows use Postgres time, reset at the exact boundary,
   and remain isolated across credentials.
5. The first attempt over the configured limit returns a payload-free `429`
   with `Retry-After`, reserves no nonce, writes no event, batch, job, or
   per-attempt audit row, and increments only a bounded aggregate rejection
   counter.
6. Active nonce replay is rejected, concurrent use admits at most one request,
   the exact 10-minute boundary permits atomic reuse, application clock skew
   cannot advance expiry, and replay accounting is bounded and payload-free.
7. Opportunistic nonce cleanup removes no more than 100 expired rows in one
   nonce-processed admission.
8. A signed invalid gzip or schema-invalid body commits admission before
   failing payload validation; replaying its nonce is rejected before decoding
   again.
9. A persistence transaction failure commits no batch, event, or job but
   preserves admission. Retrying the same body, IDs, and digest with fresh
   timestamp, nonce, and signature can succeed.
10. Duplicate replay with fresh authentication inserts zero duplicate rows,
    while batch ID reuse with a different content digest returns `409`.
11. Batch, new events, and projection job are atomic, and no acknowledgement is
    returned before their durable commit.
12. Logs, traces, problem responses, rate-limit state, nonce state, and
    admission counters contain no event body.
13. Request, acknowledgement, and problem examples validate against their
    published schemas with the canonical event schema registered by absolute
    ID.
