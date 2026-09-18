# Belay Local Developer Alpha Requirements

Status: implementation contract for the Apple Silicon individual-developer
alpha slice.

## Product promise

Belay Local gives one developer a private, offline view of activity across the
AI agent harnesses present on their machine. It reconstructs supported history,
continues collecting supported live activity after explicit hook installation,
and exposes the same read-only evidence through a local browser and MCP. The
browser additionally supports one explicit, fixed-schema mutation: recording or
retracting a declaration that the developer attempted an external fix.

Belay Local has no account, hosted dependency, product telemetry, Belay model
inference, write-capable MCP tool, or Teams requirement. Its read-only MCP
server exposes bounded evidence to the developer's configured calling agent;
that agent, not Belay, may interpret the evidence.

The implemented P0 scope includes a browser Attention Inbox over the internal
deterministic issue repository. Exact, opaque fingerprints group retained
occurrences across matching sessions, with analysis completeness,
snapshot-stable reads, and bounded cited-event lookup. Matching is exact, not
semantic. Experimental signals are hidden by default, and verification
evidence gaps remain separate from the default issue count.

Eligible stable issues can retain append-only fix-attempt declarations. These
records contain a fixed change category and exact monitoring baseline only.
They do not execute remediation, accept free text, mark an issue resolved, or
claim that a change worked. P0-04 monitors later exact compatible fingerprint
observations and preserves durable positive history. A later match is attention
evidence, not proof that the attempted fix failed; no later match is not proof
that it worked.

## Launch scope

### Required

1. Resolve and verify a pinned, unmodified Numbat executable.
2. Inventory every harness Numbat reports on the machine.
3. Scan the launch-validated Codex and Claude Code artifact stores and import
   Numbat `--emit all` NDJSON through the existing strict adapter.
4. Preserve historical/live deduplication and deterministic timeline ordering.
5. Serve a loopback-only Local API and embedded browser timeline.
6. Serve a read-only stdio MCP adapter over the Local read model.
7. Offer explicit monitor-only live-hook install, status, and uninstall.
8. Keep Local usable with the network disabled.
9. Provide an installable macOS development package and reproducible build
   instructions.
10. Publish exact coverage and known limitations.
11. Make Attention the initial browser view, with deterministic issue detail,
    exact matching sessions, and truthful incomplete-analysis states.
12. Let the authenticated Local browser record and retract durable,
    append-only fix-attempt declarations for eligible stable issues.
13. Monitor active attempts for later exact compatible fingerprint observations
    with explicit comparison coverage, bounded evidence, and durable history.

### Launch-validated harnesses

- Codex
- Claude Code

Numbat may discover and parse additional supported harnesses. They are reported
as upstream-supported until Belay adds a sanitized end-to-end launch fixture.

### Launch-validated platform

- Apple Silicon macOS (`darwin/arm64`)

Intel macOS remains an engineering build target, not an alpha support claim,
until the clean-machine checklist passes on Intel hardware.

### Deferred

- Belay Teams enrollment, upload, API, UI, billing, or deployment
- Write-capable MCP, remediation, or automatic fix execution
- Belay-hosted model inference
- Enforcement or blocking mode
- Windows packaging
- Linux packaging beyond reproducible source builds

Automatic remediation, resolution claims, semantic similarity, and claims that
a fix prevented recurrence remain deferred. P0-04 reports only deterministic
exact compatible fingerprint evidence observed after an active declaration's
server-recorded baseline.

## User commands

The launch CLI must converge on these stable workflows:

```text
belay quickstart
belay local
belay local --install-hooks
belay scan
belay agents
belay hooks status|install|uninstall
belay mcp
belay doctor
```

`belay quickstart` is the packaged one-command onboarding path. Invoking it is
explicit consent to initialize private Local state, verify the packaged sibling
Numbat using the checksum and version marker embedded at build time, install
reversible monitor-only hooks for detected Codex and Claude Code installations,
safely inspect supported MCP configuration, scan supported history, start
Local, print the loopback URL, and attempt to open the dashboard. Claude MCP
registration may proceed automatically only when its status is safely
understood. Normal quickstart does not add an absent Codex MCP entry. The
explicit one-command Codex path is
`belay quickstart --allow-codex-mcp-add`; it permits `codex mcp add` only after
strict absence verification and accepts the CLI's non-atomic duplicate-name
behavior. It never updates, migrates, replaces, or removes an existing Codex
entry. A recognized prior Codex registration must be verified with
`mcp-config status`, explicitly removed with `mcp-config uninstall`, confirmed
absent, and then reinstalled with the opt-in command. Browser-open failure is
non-fatal because the URL remains printed.

`belay local` initializes Local state if necessary, performs a historical scan,
imports any new live records, starts the loopback API, and prints the local URL.
It is the lower-side-effect path: it does not open a browser and never edits an
agent configuration unless `--install-hooks` was explicitly provided.

## Local filesystem contract

Default root: `${BELAY_HOME:-~/.belay}`

```text
config.json             non-secret installation/runtime configuration
belay.sqlite            encrypted Local event store
live/codex.ndjson       append-only Codex monitor-mode record stream
live/claude.ndjson      append-only Claude monitor-mode record stream
live/*.cursor.json      importer file identity and byte offset
logs/                   payload-free operational logs
bin/numbat-<checksum>   private checksum-addressed Numbat executable
```

The data-encryption key remains in the OS keychain. Browser and MCP credentials
must not be written to logs.

Belay Local protects against accidental disclosure and untrusted event content;
it is not a tamper-proof boundary against the logged-in OS user. A same-UID
process may replace binaries, hook files, or local state and may suppress or
forge endpoint observations. Release signing and checksum pinning protect the
distribution and normal activation path, not a machine already controlled by
its owner or an attacker running as that owner.

## Acquisition behavior

- Belay invokes Numbat only through its executable interface.
- Historical scans use `numbat scan --agent codex --emit all --output stdout`
  and the equivalent `--agent claude` command.
- Inventory command: `numbat agents --format json`.
- Live installation uses separate monitor-only hook commands and spool files
  for Codex and Claude Code.
- Enforcement is never enabled by Belay Local.
- Hook changes require explicit user intent and preserve upstream backups.
- Scan failure or one malformed artifact does not prevent Local from opening.
- Live import resumes from a durable file cursor and tolerates truncation or
  rotation without duplicating accepted events.

## Local API

The server binds only to an ephemeral port on `127.0.0.1`. A random per-launch
bearer token protects JSON routes.

Required routes:

- `GET /healthz`
- `GET /v1/sessions`
- `GET /v1/sessions/{id}`
- `GET /v1/sessions/{id}/events`
- `GET /v1/sessions/{id}/events/lookup`
- `GET /v1/activity`
- `GET /v1/findings`
- `GET /v1/issues`
- `GET /v1/issues/{id}/occurrences`
- `GET /v1/issues/{id}/fix-eligibility`
- `GET /v1/issues/{id}/fixes`
- `POST /v1/issues/{id}/fixes`
- `POST /v1/issues/{id}/fixes/{annotation_id}/retractions`
- `GET /v1/fix-monitoring`
- `GET /v1/issues/{id}/fix-monitoring`
- `GET /v1/issues/{id}/fixes/{annotation_id}/recurrences`
- `GET /v1/stats`

The issue routes are bounded, snapshot-stable reads over the revisioned issue
projection. The exact event lookup accepts only one to 50 repeated canonical
`event_id` parameters, rejects unrelated query modes, constrains results to the
selected session, and returns found/missing counts without broad timeline
scanning.

The embedded browser is a client of these routes and never reads SQLite
directly. Event-derived strings render as text, never HTML.

Event rows show prominent outcome badges only when the source explicitly
reports `succeeded`, `failed`, or `interrupted`. When the source does not report
an event outcome, the browser preserves the canonical `unknown` value as
subdued `Outcome · Not reported by source` metadata instead of presenting every
event as a prominent unknown status. Session outcome badges remain unchanged.

Session, session-event, activity, and finding responses include `has_more`,
`next_cursor`, `returned_count`, and the effective bounded `limit`. A non-empty
opaque `next_cursor` is returned exactly when another matching row exists in the
stable ingestion snapshot. Cursors are endpoint-specific and bound to normalized
filters; malformed, cross-endpoint, or filter-mismatched cursors fail closed.

Session filters include harness, raw projection outcome, historical/live/mixed
capture, RFC3339 overlap windows, and bounded search over session ID and harness.
Activity resource-kind filters scan the complete cursor snapshot. Finding
filters include time, severity, and optional exact session ID.

Issue list, view, and occurrence cursors use authenticated cursor-v2 with a
persistent Store-specific epoch. A fresh issue-list request carries filters and
an optional limit; continuation contains only `cursor`. A fresh issue-detail
request may carry `limit` plus `view_cursor`; occurrence continuation contains
only `cursor`. Every successful issue page includes a rowless `view_cursor`,
and `has_more=true` is valid only with a non-empty `next_cursor`.

Issue lists expose normalized `selection` alongside global analysis coverage.
Issue detail exposes fixed `belay.issue-explanations.v1` catalog metadata and
`global_analysis_coverage` from the same frozen snapshot as its issue summary
and occurrences. Catalog statements and evidence-navigation actions are fixed
presentation content, not generated diagnosis or remediation advice.

On issue HTTP 410, the browser clears stale list, detail, occurrence, catalog,
coverage, eligibility, action-token, and dependent fix state before rendering;
it refreshes both Attention lists and requires explicit reselection and action
retry. It never automatically resubmits a browser mutation.

Session projections without observed `session.end` terminal evidence report
`incomplete`, never success. This is a projection state and does not change the
canonical event outcome enum.

## P0 browser-only fix-attempt declarations

Fix actions are injected into Local HTTP only. MCP remains read-only and there
is no fix-recording CLI command.

An issue is eligible only when its supplied issue-view snapshot:

- contains the issue and at least one occurrence;
- has aggregate and occurrence analysis status `current`;
- is stable rather than experimental;
- is not an evidence gap; and
- has `resolved` or `lexical` scope quality.

No visible rows at the snapshot return not-found. Because aggregate analysis
status covers every visible occurrence, any non-current visible occurrence is
reported as `analysis_not_current`.

The fixed `fix-change.v1` categories are:

- `code_change`;
- `configuration_change`;
- `dependency_change`;
- `permission_change`;
- `environment_change`;
- `agent_instruction`;
- `project_rule`;
- `monitor_hook`;
- `other`.

Retractions use only `recorded_by_mistake`, `superseded`, or `other`.

Writes require the launch bearer token, JSON, a canonical UUIDv4
`Idempotency-Key`, the exact route-specific `X-Belay-Intent`, and browser
same-origin proof bound to the actual numeric loopback listener address and
port. `Host` and `Origin` must exactly match that listener;
`Sec-Fetch-Site`, when present, must be `same-origin`. Forwarded host/protocol
headers are ignored, request bodies are limited to 1 KiB, unknown or duplicate
JSON fields are rejected, and errors do not reflect request values.

An eligible browser first exchanges the issue `view_cursor` through
`GET /v1/issues/{id}/fix-eligibility` for a signed, issue-bound action token.
`POST /v1/issues/{id}/fixes` accepts that token and one fixed change category.
First creation returns `201`; identical replay returns the original row with
`200` and `replayed=true`, including after token expiry. Reuse of the same
idempotency key with different canonical intent returns `409`.

`GET /v1/issues/{id}/fixes` returns bounded, snapshot-stable history with
`next_cursor`, `has_more`, `returned_count`, `limit`, and
`evidence_evaluated_at`. History remains readable after the issue projection
disappears. Each row reports `active` or `retracted` and current evidence
retention as `available`, `partial`, `pruned`, or `unknown`.

`POST /v1/issues/{id}/fixes/{annotation_id}/retractions` appends one fixed-reason
retraction. It never updates or deletes the original declaration. First append
returns `201`, identical replay returns `200`, and another request against an
already retracted annotation returns `409`.

Annotations store exact opaque issue/fingerprint provenance, anchor revision and
session identifiers, fixed category, server recording time, and monitoring
baseline. They never store a note, command, path, diff, prompt, output, rule
body, hook body, environment value, or URL. Ordinary pruning may remove citation
sidecars and change only the computed evidence-retention status; it does not
delete annotations or retractions. Records survive Local restart and remain
until the entire Local database is reset.

## P0 exact recurrence monitoring

Fix monitoring is an optional Local HTTP read capability injected separately
from the core read model. It is not exposed to MCP or through a new CLI
command.

`GET /v1/fix-monitoring` returns one bounded, grouped row per issue with the
most actionable driving attempt state and aggregate attempt counts. It accepts
exact filters for `state`, `change_kind`, `severity`, `harness`,
`recorded_after`, `issue_id`, and `include_retracted`, plus `limit` or a
continuation `cursor`.

`GET /v1/issues/{id}/fix-monitoring` returns bounded attempt history for one
issue. It supports a fresh read, a rowless `monitoring_view_cursor` transferred
from the top-level list, or its own continuation cursor. Current issue
presentation is explicitly nullable so durable attempt history remains
available after the current issue projection disappears.

`GET /v1/issues/{id}/fixes/{annotation_id}/recurrences` requires either the
attempt's rowless `observation_view_cursor` or an observation continuation
cursor. Each row is one durable exact post-baseline occurrence and includes
same-anchor-session status, bounded retained canonical event IDs, original
citation count, retained/missing counts, truncation, and evidence-retention
state. Unknown evidence returns an empty event-ID array with nullable counts and
truncation.

All three routes use `schema_version=belay.fix-monitoring.v1`, default limit 20,
maximum 100, deterministic endpoint-specific ordering, non-nil arrays, and
opaque dedicated cursors. Their immutable 15-minute snapshot binds issue
projection, event, retention, annotation, retraction, recurrence-job,
job-event, and observation high-water marks. Continuations preserve the
original filters and page size. A retention-generation change or expired
snapshot returns 410 rather than silently changing membership or evidence.

The fixed attempt states are:

- `matching_evidence_observed`;
- `monitoring_incomplete`;
- `awaiting_later_evidence`;
- `no_later_match_observed`;
- `comparison_unavailable`;
- `retracted`.

Coverage separately reports comparable current, pending, failed, and truncated
analysis plus `analysis_through` and `complete`. Comparison-unavailable reasons
are fixed, payload-free codes. Numbat positive-only evidence can establish a
later exact match but never establish no-match. Historical matching count
aggregates qualifying observations across active and retracted attempts and is
reported separately from the driving attempt's own recurrence count.

Synchronous schema migration finishes before the Store is returned. The HTTP
server starts before background historical catch-up. While readiness is
`catching_up` or `failed`, only these three monitoring routes fail closed with
fixed non-reflective 503 problems; existing Local reads and writes remain
available. Cancellation preserves durable progress without recording a false
failure, and a due retry or Local restart resumes convergence.

## MCP

Required read-only tools:

- `list_sessions`
- `get_session`
- `get_session_timeline`
- `query_activity`
- `list_findings`
- `get_stats`
- `list_issues`
- `get_issue`
- `lookup_session_events`

Responses are bounded, structured, schema-versioned, and label event-derived
strings as untrusted observations. No tool can execute a command, write a file,
modify an agent, record a fix, or read/register recurrence monitoring.

The MCP list tools use the same server-side filters, deterministic order,
stable-snapshot cursor semantics, limits, and completeness rules as the Local
read API. They do not fetch broad pages and filter them inside MCP.

The launch value loop is:

1. `list_issues`;
2. verify normalized selection and analysis completeness;
3. transfer the returned `view_cursor` into `get_issue`;
4. inspect fixed catalog meaning, exact matching occurrences, and cited IDs;
5. hydrate only needed citations with `lookup_session_events`;
6. let the configured calling agent reason over that bounded evidence.

Belay does not generate that diagnosis, recommend a change, or execute
remediation. Exact matching does not establish semantic similarity or common
root cause.

Belay Local and all nine tools make no Belay product-network request and send
no product telemetry. A configured MCP client or remotely hosted model may
process or transmit results according to that product's privacy policy and the
user's configuration. Allowed minimized evidence may include executable/tool
names, bounded option names, project-relative paths or basenames,
model/provider labels, and network scheme/host values. Evidence is untrusted
data and must not, by itself, authorize a command or another tool call.

## P0 Attention Inbox and issue intelligence

The implemented feature uses an internal, rebuildable projection over retained
canonical events and immutable Numbat findings. It:

- derives private, store-keyed project scopes and exact command signatures
  before raw values are discarded;
- emits conservative deterministic occurrences from a fixed detector catalog;
- groups only exact compatible fingerprints and calls their sessions
  "matching sessions," never semantically similar sessions;
- preserves bounded cited event IDs and detector/catalog provenance;
- maintains revisioned issue and session-analysis state for stable read
  snapshots;
- reports current, pending, failed, truncated, and unscoped analysis coverage.
- presents stable issues in the browser Attention Inbox;
- presents verification evidence gaps in a separately paginated section;
- excludes experimental signals from default issue reads;
- exposes exact matching sessions and bounded cited-event evidence.

The foundation does not infer root cause, task intent, correctness, safety,
stalls, or successful completion. Unknown outcomes remain unknown. Unscoped or
conflicting sessions may support session-local attention but never
cross-session recurrence.

The implemented Attention Inbox and issue HTTP routes:

- show list-level analysis completeness and avoid a complete "no issues" claim
  while analysis is pending, failed, or truncated;
- preserve exact matching semantics and expose cited evidence;
- use fixed catalog language and label event-derived values as untrusted;
- paginate over an immutable issue-projection generation;
- expire issue cursors after 15 minutes and require a fresh read rather than
  silently weakening snapshot stability;
- keep issue evidence reads deterministic and provide no remediation or
  fix-execution action.

The MCP issue-evidence tools expose the same deterministic issue semantics
without exposing browser-only fix recording or P0-04 recurrence monitoring.

## Launch acceptance

1. On a macOS account with Codex and Claude Code history, one packaged
   `belay quickstart` invocation needs no manual Numbat path or pin flags,
   discovers both harnesses, installs monitor-only hooks, and renders sessions
   from both without an account. Claude MCP may be configured automatically
   when safely understood; Codex MCP requires the explicit
   `--allow-codex-mcp-add` opt-in.
2. Re-running the scan inserts no duplicate canonical events.
3. With networking disabled, browser and MCP session reads still work.
4. Explicit hook installation records a new supported agent action without
   blocking or changing the agent action.
5. Killing Belay or Numbat does not block an agent action.
6. Prompt bodies, transcripts, secrets, endpoint identity, raw paths, and
   prohibited evidence do not appear in API, MCP, logs, or database bytes.
7. Every timeline row contains its immutable Belay event ID and source metadata.
8. A malformed/oversized record is quarantined or diagnosed while later valid
   records continue.
9. A clean checkout passes tests, vet, static builds, and a no-network smoke
   test.
10. The release contains the selected Belay license plus Numbat license and
    third-party attribution.
11. Every distributable archive is built from a clean checkout and records
    `belay_dirty=false`; dirty validation artifacts are never distributed.
12. Attention defaults to stable issues, keeps Evidence gaps separate, exposes
    exact matching sessions, and qualifies empty states when analysis coverage
    is incomplete.
13. An eligible issue can record and retract a fixed-schema fix-attempt
    declaration only after explicit confirmation, with truthful non-resolution
    wording and no free-text field.
14. Fix history and identical idempotent replay survive a full Local stop and
    restart, while MCP remains exactly nine read-only tools with no fix or
    monitoring capability.
15. Monitoring list/detail/observation cursor chains remain snapshot-stable;
    history survives issue disappearance and restart; retention expiry returns
    410; and unknown/incomplete comparison never becomes a success claim.
16. Local HTTP and existing routes are available while historical monitoring
    catch-up runs, with only monitoring reads returning fixed 503 readiness
    problems until convergence.
17. An MCP client can list issues, transfer a view cursor, inspect exact
    matching occurrences/catalog/coverage, and hydrate selected cited events;
    missing evidence remains explicit and no Belay-generated diagnosis appears.
18. Issue-list and occurrence browser continuations are cursor-only. A stale
    epoch returns 410, clears dependent state, refreshes both Attention lists,
    and never automatically retries a fix mutation.

## Alpha and production gates

- Belay is licensed under MIT; license selection is complete. Numbat remains under its own Apache-2.0 license, vendored in `licenses/numbat`.
- The checksum-verified Numbat research commit is an explicit Developer Alpha
  exception. A released upstream tag with schema 0.3.0, or a renewed exception,
  remains a production gate.
- Apple Developer ID signing and notarization remain production gates, not
  requirements for the explicitly unsigned Developer Alpha.
- Repository visibility, artifact publication, naming clearance, and external
  tester authorization remain human-owned launch gates.

The objective clean-machine evidence is recorded in
`docs/launch/clean-machine-alpha-qa.md`.
