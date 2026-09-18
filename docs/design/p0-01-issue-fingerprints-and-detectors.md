# P0 Feature Design 01: Issue Fingerprints and Deterministic Detectors

- **Status:** Implemented and independently verified
- **Date:** 2026-09-08
- **Owner:** Doplex Labs
- **Repository:** `DoplexLabs/belay-engine`
- **Product references:**
  - `research/belay-brd-v2.2.md`
  - `research/belay-high-level-tech-design-v1.1.md`
  - `docs/launch/local-v0-requirements.md`
  - `docs/contracts/event-envelope-v1.md`
  - `docs/contracts/read-api-v1.md`
  - `docs/contracts/mcp-v1.md`

Implementation verification completed on 2026-09-08 with:

- full unit and integration tests;
- the Go race detector across all packages;
- `go vet ./...`;
- `make verify`;
- independent storage/concurrency review;
- independent privacy/detector review.

## 1. Overview

Belay Local currently stores minimized canonical events and upstream Numbat
findings, then exposes them as sessions, timelines, findings, and global
counts. That evidence is trustworthy enough to inspect, but it does not yet
answer the user's primary question: "Which observable problem keeps happening?"

This feature adds a Local-only, deterministic issue projection over immutable
canonical events. It groups repeated detector occurrences using opaque,
versioned fingerprints and provides the foundation for the P0 attention inbox,
similar-session evidence, fix records, and recurrence measurement.

The 2026-09-08 P0 roadmap decision brings Local issue grouping, fix annotation,
and recurrence measurement forward from their deferred status in BRD v2.2.
This does not authorize Belay to execute fixes, block agents, infer code
correctness, or guarantee prevention. MCP remains read-only. Later fix
recording will be an explicit local annotation or CLI action.

## 2. Glossary

- **Canonical event:** An immutable, minimized Belay event produced from a
  validated Numbat record.
- **Detector:** A versioned, deterministic function over one session's
  canonical events.
- **Occurrence:** One detector match in one session.
- **Issue fingerprint:** A store-keyed opaque identifier grouping exact
  deterministic detector signatures within a compatible project scope. It
  does not prove a shared root cause.
- **Issue:** A read projection aggregating retained occurrences with the same
  fingerprint.
- **Project scope:** A store-keyed opaque identifier derived from Numbat's raw
  project path before the path is discarded.
- **Unscoped session:** A session for which no project scope can be derived.
- **Evidence complete:** Whether all matching evidence fitted within the
  detector's configured citation bound.
- **Verification command:** A command classified into a fixed, privacy-safe
  category such as test, build, typecheck, lint, or format-check.

## 3. Requirements

### 3.1 Functional requirements

1. Evaluate bundled deterministic detectors against existing historical and
   newly imported live sessions.
2. Generate stable, opaque issue fingerprints without storing raw project
   paths, command arguments, prompts, completions, file contents, or output.
3. Group occurrences only when their detector semantics and project scope are
   compatible.
4. Preserve immutable cited event IDs and detector/catalog versions for every
   occurrence.
5. Durably schedule and reconcile a touched session idempotently after replay,
   restart, late event arrival, scope enrichment, retention, or
   historical/live overlap.
6. Provide five initial detectors:
   - explicit command failure, grouped across matching sessions;
   - repeated command attempts within one session;
   - explicit permission denial, grouped across matching sessions;
   - terminal verification evidence gap after file mutation;
   - terminal session observed after an unresolved explicit verification
     failure.
7. Keep upstream Numbat findings unchanged and distinguish their origin from
   Belay-derived occurrences.
8. Project upstream Numbat findings into the same issue stream without changing
   their immutable source rows.
9. Allow issue projections to be rebuilt from retained canonical evidence.
10. Surface detector failures as payload-free local diagnostics without
   blocking import, advancing agent hooks, or preventing Local from opening.
11. Freeze the issue read contract needed by the attention inbox, exact
    matching-session evidence, and read-only MCP before storage implementation.

### 3.2 Non-functional requirements

- No Belay network request or product telemetry.
- No model inference.
- No write-capable MCP tool.
- Deterministic output for the same canonical evidence and projection version.
- Bounded session analysis, citations, memory, and database work.
- Store-keyed HMAC identifiers must be unlinkable between independent Local
  stores.
- Unknown outcomes remain unknown.
- Findings describe observations, never root cause, safety, correctness, or
  intent.
- Acquisition remains fail-open; disclosure remains fail-closed.

## 4. Current State / Background

Canonical events already preserve the inputs needed for conservative
detectors: event type, actor, action, explicit outcome, optional exit code,
duration, minimized command summary/resource, coverage, confidence, and
historical source.

Current findings are immutable Numbat rule matches keyed by upstream
`finding_id`. They are occurrence records, not issue families. They contain no
stable fingerprint, issue aggregation, project scope, detector origin, or
rebuild checkpoint.

The importer maps and writes records one at a time. Historical scan and live
tailing both call the same importer. Returning a detector error from import
would incorrectly prevent live cursor advancement, so analysis must run as a
separate fail-open reconciliation step.

The canonical event contract intentionally excludes raw project paths and
positional command arguments. Session keys omit project path whenever Numbat
supplies a session ID. Grouping common names such as `src/main.go`, `npm`, or
`git` across repositories or commands would therefore create false recurrence.
Private project-scope and exact-command signatures must be derived before raw
values are discarded and stored only as opaque sidecar identifiers.

## 5. High Level Design

```text
Numbat validated record
        |
        +--> domain-separated project/command sidecar derivation
        |
        v
canonical mapper --> immutable event + dirty-session generation
                                           |
                                           v
                                  serialized reconciler
                                           |
                                           v
                         coalesced canonical evidence + sidecars
                                           |
                                           v
                                  pure detector catalog
                                           |
                                           v
                               rebuildable issue occurrences
                                           |
                                           v
                                  grouped issue projection
```

### 5.1 Design decisions

#### Events remain the source of truth

`belay.event.v1` remains the immutable evidence contract. Issue occurrences are
derived state and may be transactionally deleted and rebuilt for one session.

#### Project scope is opaque and local

The storage layer derives independent scope and fingerprint keys using
HKDF-SHA256 from the Local data key and fixed domain labels. The AES-GCM key is
never exposed to the importer or detector package and is not directly reused as
an HMAC key.

When a validated Numbat event contains a non-empty project path, the importer
asks the Local store to normalize and derive:

```text
psc_<lower-base32-no-padding(HMAC-SHA256(project_scope_key, normalized_path))>
```

Normalization is byte-exact and versioned:

1. Reject empty or NUL-containing input.
2. Require an absolute path. Relative paths remain unscoped because resolving
   them against process working directory would make identity nondeterministic.
3. Apply `filepath.Clean`.
4. Resolve symlinks with `filepath.EvalSymlinks` when the path exists.
5. On Darwin, normalize the known `/private/var`→`/var` and
   `/private/tmp`→`/tmp` aliases.
6. Convert separators with `filepath.ToSlash`.
7. Remove a trailing slash except for the filesystem root.
8. Preserve case and Unicode bytes; no locale-sensitive folding is performed.

The result records `scope_quality=resolved` when symlink resolution succeeds
and `scope_quality=lexical` otherwise. Lexical scopes may under-group aliases
but cannot merge distinct normalized paths. Golden fixtures cover all defined
behavior.

The raw path is not persisted or logged. The mapping from session key to scope
is stored separately. Reprocessing a duplicate event may populate or improve a
previously missing scope. Every scope transition marks the session dirty even
when no event is newly inserted.

If no scope is available, occurrence fingerprints include the session key and
receive `scope_quality=unscoped`. They may support session-local attention but
must not be presented as cross-session recurrence.

Conflicting non-empty scopes for one session set `scope_quality=conflict`, mark
the session dirty, emit a bounded diagnostic, and disable cross-session
grouping for that session.

#### Fingerprints use semantic versions, not implementation versions

Fingerprint material is a canonical length-prefixed encoding:

```text
fingerprint_version
detector_id
project_scope_id OR session_key
allowlisted detector dimensions
```

The identifier is:

```text
ifp_<lower-base32-no-padding(HMAC-SHA256(issue_fingerprint_key, material))>
```

The public issue ID is a deterministic one-to-one presentation of the same
digest:

```text
issue_id = iss_<fingerprint digest>
```

It remains stable across projection rebuilds. `fingerprint_id` exists for
explicit contract/version semantics; `issue_id` is the stable address used by
detail routes and MCP.

`detector_version` records implementation provenance. It is excluded from
identity while semantics are compatible. `fingerprint_version` changes when
grouping semantics or dimensions change.

Event IDs, finding IDs, run IDs, source type, artifact path, import order, and
timestamps are excluded from fingerprint identity.

#### Private event sidecars preserve exact matching without exposing arguments

Import stores an encrypted/opaque sidecar keyed by the persisted canonical
event ID:

- exact command signature ID, derived from a normalized full command before
  minimization;
- fixed command class;
- fixed permission class;
- enrichment version.

Command normalization applies `strings.TrimSpace` and otherwise preserves the
exact UTF-8 bytes. It performs no shell parsing, quote interpretation, escape
handling, interior whitespace collapsing, or dialect-specific rewriting. The
normalized command is never stored.
Its signature is:

```text
cmd_<lower-base32-no-padding(HMAC-SHA256(command_signature_key, normalized_command))>
```

This favors precision over recall: commands with different targets do not
group. A future version may add a separately versioned command-family
signature.

The append result resolves the canonical event ID even on source-dedup replay,
allowing old events to receive sidecars during re-scan without mutating the
canonical event. Result events inherit command identity only through an exact
same-session tool-call pairing; missing or reused tool-call IDs reduce
confidence and never produce a high-confidence resolution claim.

#### Issue occurrences are rebuildable projections with a durable work queue

Appending a new event and upserting its session in `dirty_sessions` occur in
the same SQLite transaction. Scope/enrichment transitions and retention also
upsert the session. A crash after event commit therefore cannot lose analysis
work.

One bounded reconciler is serialized per Local store. For each dirty session it:

1. Claims one dirty-session generation without blocking event insertion.
2. Loads canonical events in the public timeline order:
   source-sequence, occurrence time, event ID.
3. Coalesces conservative historical/live duplicates.
4. Evaluates each detector independently with panic/error isolation.
5. Builds at most one occurrence per `(session, fingerprint)`.
6. In one authorized projection transaction, replaces that session's prior
   Belay-derived occurrences and citations only if
   `claimed_generation == dirty_sessions.target_generation`. If a new event or
   metadata transition advances the target during analysis, the commit is
   rejected and the session remains dirty.
7. Marks analysis `current` only after projection commit.

Existing Numbat findings remain append-only and are never deleted by detector
reconciliation. A separate projector creates issue occurrences with
`origin=numbat` using rule ID/version, locally re-keyed upstream project hash
when available, and observed event type. Original finding rows remain the
evidence source.

One permanently failing session cannot prevent later sessions from being
processed. Failed work receives bounded backoff and remains `failed` or
`pending`; it is never reported as current.

#### Historical/live detector-input coalescing

Equivalent artifact and hook events may coexist because source deduplication
intentionally preserves source identity. Detector input coalesces only when a
non-empty, event-type-specific equivalence identity can be constructed and:

- session, event type, actor, explicit outcome, and sidecar signature match;
- source classes differ between historical and live;
- occurrence timestamps are within one second; and
- tool-call IDs match when both are present.

Command events require a non-empty exact command signature; permission events
require a non-empty permission class; file events require a non-empty canonical
resource name. Two missing sidecar values are never treated as equal. The
higher-coverage observation is retained and all source event IDs remain
available as evidence. Ambiguous observations remain separate.

#### Detector output uses fixed catalog language

Titles, explanations, severity, suggested action type, and confidence rules
come from a bundled catalog. Event-derived strings are evidence values marked
untrusted by presentation layers. A detector never generates narrative text
from raw content.

### 5.2 Initial detector catalog

#### D1: Explicit command failure

- **ID:** `explicit_command_failure`
- **Condition:** At least one command attempt with an exact private command
  signature explicitly reports non-zero exit status or `outcome=failed`.
- **Occurrence:** One per matching session and command signature. The issue
  projection determines whether it repeated across sessions.
- **Pairing:** Matching exec/result observations sharing a tool-call ID count
  once. Without a tool-call ID, adjacent equivalent exec/result observations
  within a bounded interval count once.
- **Severity:** low for one failed attempt in a session, medium for three or
  more.
- **Confidence:** high only when the cited attempt has an explicit outcome;
  otherwise the detector does not fire.
- **Fingerprint dimensions:** project scope and command signature.
- **Wording:** "An explicit command failure was observed." Aggregated issues
  may say "Observed in N sessions."

#### D2: Repeated command attempts

- **ID:** `repeated_command_attempts`
- **Condition:** At least four distinct attempts with the same minimized
  command signature occur in one session, regardless of unknown outcome.
- **Severity:** info and hidden from the default inbox until validation shows
  acceptable precision.
- **Confidence:** medium with tool-call pairing, otherwise low.
- **Fingerprint dimensions:** project scope and command signature.
- **Wording:** "The same command was attempted repeatedly."
- This is an attention signal, not a failure or stall claim.

#### D3: Explicit permission denial

- **ID:** `explicit_permission_denial`
- **Condition:** At least one explicit `permission.denied` observation.
- **Occurrence:** One per matching session and fixed permission class. The
  issue projection determines whether it repeated across sessions.
- **Severity:** low; repeated denials remain attention signals because denial
  may represent correct user intent.
- **Confidence:** high.
- **Fingerprint dimensions:** project scope and a fixed permission class
  derived from allowlisted tool name/type. If no class is available, the
  fingerprint uses `permission.unknown` and confidence is medium.
- **Wording:** "An explicit permission denial was observed."

#### D4: Terminal verification evidence gap after mutation

- **ID:** `verification_not_observed`
- **Condition:** One or more file writes/deletes occur and no recognized
  verification command is observed after the final mutation, a terminal
  `session.end` is present, and observed coverage satisfies the detector's
  published compatibility matrix.
- **Category:** evidence gap, shown separately from issues.
- **Severity:** info.
- **Confidence:** medium. The detector is disabled for incomplete sessions,
  insufficient coverage, and unsupported historical sources.
- **Fingerprint dimensions:** project scope and fixed verification-gap class.
- **Wording:** "No test, build, typecheck, or lint command was observed after
  the final file change."
- The detector does not claim that verification did not occur outside observed
  coverage.

D4 compatibility matrix:

| Harness | History | Required observed coverage | Enabled |
|---|---|---|---|
| Codex | live only | hook `file.write|file.delete`, hook `command.exec|command.result`, and hook `session.end`; both `tool_call` and `lifecycle` depths; no pending/truncated analysis | yes |
| Claude Code | live only | hook `file.write|file.delete`, hook `command.exec|command.result`, and hook `session.end`; both `tool_call` and `lifecycle` depths; no pending/truncated analysis | yes |
| Any | historical or mixed | artifact evidence may omit verification performed elsewhere | no |
| Other harness | any | no launch-validated completeness contract | no |

The matrix is versioned with the detector catalog. Absence of any required
event class disables D4 for that session; it does not produce a lower-confidence
finding.

#### D5: Terminal session after unresolved explicit verification failure

- **ID:** `unresolved_verification_failure_at_completion`
- **Condition:** A recognized verification command explicitly fails; no later
  command with the same exact private command signature explicitly succeeds;
  and a later `session.end` is observed.
- **Severity:** high.
- **Confidence:** high for the unresolved explicit command evidence, but the
  detector does not claim the terminal session succeeded because Numbat 0.3.0
  does not report an outcome on `session.end`.
- **Fingerprint dimensions:** project scope, exact command signature, and
  verification category.
- **Wording:** "The session ended after an observed verification failure
  without a later observed passing run of the same command."
- This is a terminal unresolved-verification signal. It does not prove false
  completion or successful task completion. Landing-page copy must use the
  stronger example only when the source provides explicit terminal success.

### 5.3 Privacy-safe command classification

Current command summaries preserve only the executable and option names, so
they cannot distinguish `pnpm test` from `pnpm typecheck`. The importer-side
enrichment derives a fixed enum before arguments are discarded:

```text
test | build | typecheck | lint | format_check | other | unknown
```

Classification uses an allowlisted executable/subcommand catalog. It stores
only the fixed enum and opaque exact signature, never the matched positional
argument. Existing events are enriched on duplicate re-scan; events that cannot
be replayed remain `unknown`.

## 6. Low Level Design

### 6.1 Canonical model and consumer contract

Keep `model.Event` and `belay.event.v1` unchanged. Sidecar enrichment prevents
an event-contract migration and supports immutable existing events.

Add issue projection types in `internal/canonical/model/issue.go`:

- `IssueOccurrence`
- `IssueSummary`
- `IssueEvidence`
- `DetectorProvenance`
- `ScopeQuality`
- `AnalysisStatus`

Freeze these read shapes:

```go
type IssueSummary struct {
    IssueID             string
    FingerprintID       string
    FingerprintVersion  string
    Origin              string
    DetectorID          string
    DetectorVersion     string
    Category            string
    TitleCode           string
    Severity            string
    Confidence          string
    ScopeQuality        string
    FirstObservedAt     time.Time
    LastObservedAt      time.Time
    OccurrenceCount     int
    SessionCount        int
    Harnesses           []string
    AnalysisStatus      string
    EvidenceComplete    bool
    RetainedHistoryOnly bool
}
```

Issue occurrence rows include session ID, harness, observed interval, cited
event IDs, origin record ID, analysis generation, and evidence completeness.

Aggregation semantics at the cursor snapshot:

- `occurrence_count` counts visible occurrence revisions.
- `session_count` counts distinct visible session IDs.
- `first_observed_at` and `last_observed_at` cover retained Local history only.
- `severity` is the highest fixed severity rank among visible occurrences.
- `confidence` is the lowest confidence among visible occurrences, so a mixed
  issue is not overstated.
- `analysis_status` is the least-current visible occurrence status using
  `failed`, `pending`, `truncated`, then `current`.
- `evidence_complete` is true only when every visible occurrence is complete.
- `harnesses` is a sorted distinct set.
- `recurrence=repeated` requires at least two distinct sessions. Unscoped
  fingerprints include session ID and therefore cannot satisfy it.
- Filters select issue groups; summary counts remain the complete visible group
  at the snapshot rather than being recomputed from only matching occurrences.

The issue list response includes list-level analysis coverage:

```json
{
  "analysis": {
    "current_sessions": 120,
    "pending_sessions": 2,
    "failed_sessions": 1,
    "truncated_sessions": 0,
    "unscoped_sessions": 8,
    "analysis_through": "2026-09-08T18:05:01Z",
    "complete": false
  }
}
```

An empty issue list may be described as "No issues reported by configured
detectors" only when `analysis.complete=true`. Otherwise consumers say that no
issues are available from the completed portion and identify pending/failed
coverage.

The next presentation feature implements:

- `GET /v1/issues`
- `GET /v1/issues/{id}/occurrences`
- `list_issues` MCP tool
- `get_issue` MCP tool

Issue list filters: severity, category, harness, origin, analysis status,
observed-after, recurrence (`single|repeated`), session ID, and exact
fingerprint ID. Ordering is severity rank, repeated before single,
`last_observed_at DESC`, then issue ID. Cursor snapshots bind every normalized
filter.

`list_issues` accepts the same filters, `limit` default 20/maximum 100, and an
opaque cursor. It returns structured issue summaries, list-level analysis
coverage, `has_more`, and `next_cursor`.

`get_issue` accepts `issue_id`, occurrence `limit` default 20/maximum 100, and
an opaque occurrence cursor. It returns one issue summary at the cursor
snapshot plus occurrences ordered by `last_observed_at DESC, occurrence_id`,
cited event IDs, coverage, and completeness.

Both MCP tools:

- are read-only and expose no fix/remediation action;
- mark all event-derived evidence as untrusted observations;
- use fixed catalog titles rather than narrative diagnosis;
- enforce response-size limits and truncation metadata;
- reject malformed, cross-tool, issue-mismatched, or filter-mismatched cursors
  without reflecting cursor contents.

Issue cursors include an issued-at timestamp and expire after 15 minutes.
Expired cursors return `410 application/problem+json` with fixed type
`belay.local/cursor-expired`; MCP returns a fixed input error instructing the
client to restart pagination. This bounded lifetime permits projection revision
compaction without silently weakening cursor stability.

### 6.2 Detector package

Create `internal/detection` with no storage, presentation, Numbat, filesystem,
or network dependency.

Core interfaces:

```go
type Detector interface {
    ID() string
    Version() string
    FingerprintVersion() string
    Evaluate(SessionInput) ([]Match, error)
}

type SessionInput struct {
    SessionID     string
    ProjectScope  string
    ScopeQuality  string
    Events        []model.Event
    Enrichments   map[string]EventEnrichment
}
```

The package provides pairing, command-signature, verification-category, and
bounded-citation helpers shared by detectors.

Limits:

- maximum 10,000 events analyzed per session;
- maximum 100 derived matches per session;
- maximum 50 cited events per occurrence;
- deterministic truncation with `evidence_complete=false`.

Each detector is called behind panic recovery and a deadline. Sessions
exceeding limits produce no detector results and expose
`analysis_status=truncated`. Detector failures preserve the previous projection
but expose `analysis_status=failed`, so stale results are never represented as
current.

### 6.3 Local storage

Add migration `005_issue_projection.sql`:

- `session_scopes`
  - `session_key TEXT PRIMARY KEY`;
  - `project_scope_id TEXT`;
  - `normalization_version TEXT NOT NULL`;
  - `scope_quality TEXT NOT NULL CHECK (...)`;
  - created/updated timestamps.
- `event_enrichments`
  - `event_id TEXT PRIMARY KEY REFERENCES events(event_id) ON DELETE CASCADE`;
  - command signature ID/class, permission class, enrichment version;
  - encrypted enrichment payload and encoding;
  - created timestamp.
- `dirty_sessions`
  - `session_key TEXT PRIMARY KEY`;
  - target event generation, dirty reason, state, attempt count, retry time;
  - claimed and updated timestamps.
- `session_analysis_revisions`
  - revision ID and session key;
  - status `current|pending|failed|truncated`;
  - target event generation and successfully analyzed event generation;
  - fixed error code when applicable;
  - `visible_from_generation` and nullable `visible_until_generation`;
  - timestamps.
- `issue_occurrences`
  - `revision_id TEXT PRIMARY KEY`;
  - stable logical occurrence ID;
  - fingerprint ID/version, origin;
  - session key referencing retained event sessions logically;
  - detector ID/version, projection version/generation;
  - category, severity, confidence;
  - first/last observed timestamps;
  - evidence-complete and analysis-status fields;
  - encrypted evidence payload and encoding;
  - created/updated timestamps;
  - `visible_from_generation INTEGER NOT NULL`;
  - nullable `visible_until_generation`;
  - unique active `(session_key, fingerprint_id, origin)`.
- `issue_occurrence_events`
  - occurrence revision ID and canonical event ID foreign keys;
  - primary key on both IDs.
- `issue_projection_metadata`
  - singleton current generation, oldest retained generation, and last
    successful analysis timestamp.
- `analysis_diagnostics`
  - bounded/deduplicated fixed error code, session ID, detector ID, count,
    first/last observed timestamps;
  - no arbitrary error text.

Only opaque identifiers, fixed enums, versions, bounded counters, and
timestamps remain plaintext. Evidence dimensions and citations use the
existing authenticated encryption envelope.

Projection replacement increments the global issue generation, closes prior
active occurrence revisions by setting `visible_until_generation`, inserts new
revisions with `visible_from_generation`, and acknowledges the dirty-session
generation in one transaction. Issue list/detail cursors carry the projection
generation. Queries include rows where:

```text
visible_from_generation <= snapshot
AND (visible_until_generation IS NULL OR visible_until_generation > snapshot)
```

This preserves cursor stability while reconciliation changes current issues.
Every successful, failed, pending, or truncated analysis transition also
increments the global generation and publishes a
`session_analysis_revisions` row rather than mutating active occurrence rows.
On failure, prior occurrence evidence remains queryable but joins to the failed
session-analysis revision, so it is never represented as current.

Revision history participates in Local retention and disk accounting. P0 keeps
closed revisions for at least one hour, four times the maximum issue-cursor
lifetime. Compaction advances `oldest_retained_generation` only after the
one-hour floor. A cursor whose snapshot is older than that generation is
expired even if its timestamp was forged or malformed.

Add a `projection_rebuild` mutation authorization mode. It may mutate only
derived issue, enrichment-work-state, and diagnostic tables. Events and
upstream findings remain immutable.

Retention marks every affected session dirty before deleting evidence, deletes
dependent occurrences, orphaned scope/enrichment rows, orphaned analysis
diagnostics, and completed dirty-session state in the same authorized prune
transaction. Byte accounting includes encrypted issue and event-enrichment
payloads. The session is rebuilt before its issue data can return to `current`.
Empty issue groups disappear naturally because summaries aggregate retained
occurrences.

### 6.4 Reconciliation orchestration

Historical scan and direct import signal the reconciler after import commits.
Live tailing signals only after the spool cursor is durably advanced. Local
startup always drains durable dirty sessions. Reconciliation errors:

- do not change importer success;
- do not prevent live cursor advancement;
- record only detector ID, projection version, session opaque ID, and error
  code;
- leave the previous successful issue projection available;
- leave the durable session generation pending for retry.

Catalog upgrades transactionally mark affected sessions dirty. A catalog change
with compatible grouping retains fingerprint version; a semantic grouping
change requires a new fingerprint version and does not silently merge history.

### 6.5 Read boundary

This feature implements the storage repository methods and contract fixtures
for issue summaries and occurrences. The next feature wires the already-frozen
contract to HTTP, browser, and MCP.

`/v1/findings` and `list_findings` remain backward compatible. Numbat-backed
issue occurrences reference their immutable source finding IDs.

## 7. Monitoring

Belay Local sends no telemetry. Payload-free local diagnostics and `doctor`
output may report:

- detector catalog/projection version;
- last successful reconciliation;
- sessions pending reconciliation;
- occurrences produced;
- sessions skipped by limits;
- detector failures by fixed error code;
- unscoped session count;
- conflicting-scope count.
- current, pending, failed, and truncated session-analysis counts.

No diagnostic includes raw commands, paths, evidence summaries, prompts,
outputs, or HMAC material.

## 8. Open Questions

The following decisions are intentionally conservative for P0:

1. Rule/detector semantic changes require a fingerprint-version bump.
2. Unscoped or conflicting sessions do not contribute to cross-session
   recurrence.
3. Exact fingerprint matches are called "matching sessions," never semantic
   similarity or shared root cause.
4. Cross-agent file-collision detection is deferred until scope coverage and
   overlap semantics are validated.
5. Fix execution remains outside Belay. Later fix recording stores explicit
   annotations and monitoring criteria only.
6. Key rotation is unsupported in P0. A future rotation design must
   transactionally re-key scopes, signatures, fingerprints, and projections.
7. Plaintext opaque relationships expose timing/frequency graph structure to a
   same-UID database reader; this remains within the documented Local threat
   model.

## 9. Task Breakdown

1. Add domain-separated opaque-ID derivation, path/command normalization, and
   privacy golden tests.
2. Add event sidecar enrichment and session-scope persistence, including
   duplicate re-scan enrichment.
3. Add the issue projection model, frozen repository contract, and SQLite
   migration.
4. Implement the pure detector framework and five catalog rules.
5. Implement conservative historical/live coalescing and upstream-finding
   projection.
6. Implement durable dirty-session scheduling and generation-checked,
   serialized per-session projection replacement.
7. Integrate fail-open reconciliation with historical import, post-cursor live
   tailing, direct import, Local startup, and catalog upgrades.
8. Integrate retention, encryption lifecycle, analysis status, and doctor
   diagnostics.
9. Add unit, migration, replay, privacy, concurrency, and end-to-end fixtures.
10. Update contracts and launch requirements to record the approved P0 scope
   change without claiming remediation or prevention.

## 10. Appendix: Acceptance Gates

1. Privacy canaries are absent from fingerprints, issue tables, logs, API
   fixtures, and MCP fixtures.
2. The same semantic fixture produces the same fingerprint across random event
   IDs, run IDs, artifact relocation, import order, and restart.
3. Separate project scopes never group, even with identical filenames and
   command signatures.
4. Unscoped sessions never produce cross-session recurrence.
5. Replay, crash/restart, and concurrent reconciliation produce identical
   occurrence counts.
6. A crash after event commit but before reconciliation converges on restart.
7. One permanently failing session does not block later sessions.
8. A stale reconciler cannot overwrite a newer committed generation.
9. Late events rebuild a session projection without orphan rows or count
   inflation.
10. Equivalent historical/live evidence in one session cannot inflate detector
    thresholds.
11. Unknown outcomes never satisfy explicit-failure or explicit-success
   conditions.
12. Negative fixtures cover expected failing tests, `grep`/search exit codes,
   intentional retries, deliberate permission denials, incomplete history, and
   TDD loops without producing a high-confidence false-completion issue.
13. Detector failure or panic never blocks event persistence, live cursor
    advancement,
    Local startup, browser reads, or MCP reads.
14. Failed, pending, and truncated analysis is never reported as current.
15. Retention/rebuild matches a fresh database containing only retained events.
16. Fresh migration and upgrade from migration 004 both pass.
17. Scope and fingerprint domains use distinct derived keys; separate stores
    produce unlinkable identifiers.
18. Exact command signatures distinguish positional subcommands and targets
    without exposing them in database bytes.
19. Existing events receive enrichment through duplicate re-scan without
    canonical mutation.
20. The full repository test suite, vet, architecture checks, and packaged
    smoke tests pass.
