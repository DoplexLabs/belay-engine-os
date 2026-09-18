# ADR 0001: Stack and Repository Boundaries

- **Status:** Accepted
- **Date:** 2026-09-08
- **Decision owner:** Maintainer
- **References:** Belay BRD v2.2; Belay HLD v1.1

## Context

Belay has an intentionally open edge and closed hosted control plane. The edge
must integrate closely with Numbat, run reliably on developer machines, embed
the Local product surface, and remain straightforward to audit. The hosted
system must remain operable by a two-person team and preserve shared contracts
with Local.

## Proposed decision

### Repository boundary

- `DoplexLabs/belay-engine` is public before Local launch.
- It contains the edge wrapper, Local store, Local read API, Local MCP,
  Local web assets, Teams uploader, shared schemas, conformance fixtures, and
  release tooling.
- A separate private `DoplexLabs/belay-cloud` repository contains Teams
  identity/workspaces, enrollment, ingest, projections, hosted read API,
  billing, exports/deletion, notifications, worker processes, and Teams web UI.
- The cloud repository consumes released contract artifacts from
  `belay-engine`; it must not copy and privately fork those contracts.

### Implementation languages

- Use Go for the edge and Local services.
- Keep Numbat behind a narrow executable/NDJSON-file adapter. Do not import or
  modify Numbat internals; its operational Go packages are under `internal/`
  and are not a supported external API.
- Use TypeScript for the Teams API, workers, and browser UI. Local UI builds to
  static assets embedded in the Go endpoint package; Teams API, workers, and UI
  remain in the private cloud repository.

### Storage and contracts

- Use SQLite for Local indexes and control state. Immutable minimized events
  remain logically append-only.
- Encrypt Local event/finding payload blobs with application-layer AES-256-GCM
  and an OS-keystore-backed data key while leaving only minimized index fields
  plaintext. Keep the pure-Go SQLite driver; do not introduce SQLCipher or a
  CGO dependency.
- Use Postgres for Teams events, control state, jobs, and projections.
- Define canonical contracts in JSON Schema and OpenAPI. Generate Go and
  TypeScript types where practical; generated code is never the source of truth.

### Hosted architecture

- Start with one TypeScript monorepo containing Fastify API, worker, and Next.js
  web applications.
- Use explicit SQL/Kysely and Postgres-backed durable jobs before introducing a
  broker.
- Defer cloud-provider selection until the hosted deployment slice; M0 runs
  locally against containerized Postgres.

## Consequences

- The public/private boundary is enforceable at the repository level.
- The cloud product can share TypeScript types and validation with its UI while
  the edge keeps a small distributable Go runtime.
- Browser UI development remains productive without forcing a Node runtime onto
  the endpoint.
- Shared schemas become a release dependency and require compatibility tests.
- Local encryption needs an early packaging spike and may change implementation
  without changing the event contract.
- Belay owns durable Local event storage; Numbat's state databases are not event
  stores.

## Alternatives rejected for M0

- One repository containing closed cloud code: conflicts with the open-edge
  decision and complicates release hygiene.
- TypeScript/Node for the endpoint: adds a runtime and weakens packaging.
- Go for the hosted API/worker: viable, but would split the hosted product
  between Go services and a TypeScript UI without a demonstrated M0 advantage.
- Rust for the endpoint: viable, but increases integration cost relative to the
  Go-based Numbat dependency without a demonstrated V1 benefit.
- Microservices or Kafka: unnecessary operational cost before measured load.
- Fingerprint-only Teams ingestion: cannot reconstruct hosted timelines or
  support auditable metrics.

## Approval record

Approved by Maintainer on 2026-09-08:

1. Public `belay-engine` / private `belay-cloud` repository split.
2. Go for the edge; TypeScript for the cloud and browser UIs.
3. SQLite Local and Postgres Teams.
4. Application-layer payload encryption spike.
