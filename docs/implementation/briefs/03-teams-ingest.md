# Implementation Brief 03: Teams Enrollment and Ingest

## How to Use This Brief

Before writing any code:
1. Read this brief and all reference materials.
2. Read the frozen event, transmission, state, and ingest contracts.
3. Establish private `belay-cloud` conventions before implementation.

When implementing:
4. Complete tasks in order with tests.
5. Derive tenant identity from credentials, never request bodies.
6. Cover every replay, auth, size-limit, and transaction error.

When done:
7. Run unit, database, contract, and isolation tests.
8. Confirm acceptance criteria.
9. Report implementation, tests, deviations, and issues.

## Service

Private Belay Teams enrollment and ingest

## Contextual agent

None currently; implement in the future private `belay-cloud` repository.

## Summary

Create the minimum hosted foundation required for founder dogfood: development
workspaces/machines, per-machine signing credentials, atomic idempotent event
ingest, and durable projection jobs.

## Reference materials

- Belay BRD v2.2, FR3–FR4 and NFR1–NFR4
- Belay HLD v1.1, §§5.3, 5.5–5.6, 5.11–5.13
- `docs/contracts/event-envelope-v1.md`
- `docs/contracts/transmitted-fields-v1.md`
- `docs/contracts/local-teams-state-v1.md`
- `docs/contracts/teams-ingest-v1.md`

## Design context

Teams is opt-in. Machine credentials resolve exactly one workspace/machine.
Valid batches atomically persist immutable events and enqueue projection work.
The cloud never stores recoverable machine private keys.

## Tasks

1. **Scaffold private cloud repository**
   - Establish API/worker processes, migrations, config, local Postgres, tests,
     and generated-contract consumption.
   - Acceptance: CI builds both processes and checks schema-generation parity.

2. **Create M0 schema**
   - Workspaces, machines, machine credentials, batches, events, jobs, and audit
     records.
   - Acceptance: tenant-owned keys/indexes lead with `workspace_id`; migrations
     apply and roll forward from empty.

3. **Implement development enrollment**
   - Issue short-lived single-use code, accept Ed25519 public key, return
     machine/credential IDs.
   - Acceptance: code replay, expiry, and cross-workspace use fail closed.

4. **Implement signed ingest**
   - Verify credential, canonical signature, timestamp, nonce, digest, limits,
     schemas, and transmission policy.
   - Acceptance: workspace/machine cannot be overridden by body fields.

5. **Implement atomic persistence and idempotency**
   - Insert batch, new events, and projection job in one transaction.
   - Acceptance: duplicate replay inserts no events; same batch ID/different
     digest returns conflict; database failure yields no acknowledgement.

6. **Implement audit-safe observability**
   - Request IDs, counts, latency, duplicate/reject metrics, and payload-free
     logs.
   - Acceptance: seeded event content never appears in logs/traces/errors.

## Testing requirements

- Signature canonicalization fixtures shared with uploader
- Enrollment expiry/replay/revocation tests
- Batch/event duplicate and conflict tests
- Compression-bomb and all size-limit tests
- Transaction rollback and ambiguous-timeout replay tests
- Cross-workspace isolation tests from first migration
- Payload absence in logs/traces/errors
- Concurrent ingest tests for the same event IDs

## Contracts produced

- Enrollment response and machine credential
- Teams ingest responses/acknowledgement watermark
- Immutable hosted events and durable projection jobs

## Contracts consumed

- Canonical event and transmission contracts
- Local/Teams state transitions
- Signed ingest request

## Sequencing notes

Schema and validation begin after Brief 01. Uploader integration waits for
signature and acknowledgement fixtures.

## Risks and callouts

- Production identity/RBAC hardening remains M4; M0 still requires secure
  development credentials.
- Transaction-scoped tenant context must be safe under connection pooling.
- Request bodies must be excluded from every observability path.
