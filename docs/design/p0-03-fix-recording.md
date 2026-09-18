# P0-03: Explicit Local Fix Recording

- **Status:** Approved after independent storage, security, and UX/privacy reviews
- **Date:** 2026-09-08
- **Scope:** Canonical fix-annotation model, Local storage, Local action/read
  services, authenticated Local HTTP, and embedded browser
- **Depends on:** P0-01 issue identities and P0-02 Attention Inbox
- **Explicitly excludes:** fix execution, generated remediation, write-capable
  MCP, suppression, resolution claims, recurrence classification, Teams, and
  free-text notes

## 1. Overview

Belay can now identify deterministic attention signals and show exact matching
sessions, but it has no explicit record of when a developer declared an
external change attempt in response. Without that anchor, Belay cannot later
measure whether the exact fingerprint appeared again afterward.

This feature adds a private, append-only **fix-attempt annotation**. A developer
declares that they attempted an external change against a current, stable issue.
Belay snapshots the issue fingerprint and provenance at that moment and stores
a monitoring start time. The record does not execute anything, describe the
change in free text, mark the issue resolved, suppress future detections, or
claim the change worked.

The browser wording is **Record fix attempt** and **Fix attempt declaration
recorded · Not verified by Belay**. Future P0-04 recurrence measurement consumes
only active, non-retracted attempts with an exact fingerprint and
`monitor_from` timestamp.

## 2. Glossary

- **Fix attempt:** A developer declaration that they attempted an external
  change intended to address an issue. Belay does not verify intent,
  correctness, causality, or outcome.
- **Fix annotation:** The immutable Local record of one fix attempt.
- **Anchor occurrence:** The latest visible occurrence in an aggregate-eligible
  issue snapshot when the annotation is recorded.
- **Monitoring baseline:** The annotation's exact fingerprint and
  `monitor_from` timestamp.
- **Idempotency key:** A browser-generated random UUID reused for retries of one
  confirmation.
- **Replay:** A repeated request with the same idempotency key and canonical
  content that returns the original annotation.
- **Conflict:** Reuse of an idempotency key with different canonical content.
- **Issue view cursor:** The P0-02 rowless cursor carrying the issue projection
  snapshot requested by the browser. It is not a signed proof.
- **Fix action token:** A signed, issue-bound token created after server-side
  eligibility validation.
- **Retraction:** An append-only declaration that a prior fix attempt should not
  participate in recurrence measurement.

## 3. Requirements

### 3.1 Functional requirements

1. Let a developer record one external fix attempt from an eligible issue
   detail view.
2. Require an explicit confirmation step; never record on a single card click.
3. Require the issue view cursor from the inspected Attention snapshot.
4. Exchange that cursor for a signed issue-bound fix action token after
   server-side eligibility validation.
5. Resolve the latest visible occurrence for that issue inside the same
   transaction as the annotation insert.
6. Store immutable issue/fingerprint/provenance fields needed for future exact
   recurrence measurement.
7. Require a deliberate fixed primary change category:
   - `code_change`;
   - `configuration_change`;
   - `dependency_change`;
   - `permission_change`;
   - `environment_change`;
   - `agent_instruction`;
   - `project_rule`;
   - `monitor_hook`;
   - `other`.
8. Persist `change_catalog_version=fix-change.v1`.
9. Store `recorded_via=local_ui`.
10. Set `monitor_from` to the server recording time.
11. Make creation transactionally idempotent, including replay after action
    token expiry.
12. Return the original record for an identical replay.
13. Reject idempotency-key reuse with changed canonical client intent.
14. Support an append-only retraction with a fixed reason:
    - `recorded_by_mistake`;
    - `superseded`;
    - `other`.
15. Show bounded, snapshot-stable fix-attempt history on issue detail.
16. Keep history available independently of occurrence/timeline reads.
17. Preserve annotations and retractions when events, findings, or issue
    revisions are pruned.
18. Report retained anchor evidence as
    `available|partial|pruned|unknown` without preventing
    retention.
19. Keep MCP exactly six read-only tools.

Primary change categories use catalog `fix-change.v1`:

| Category | Fixed meaning/example |
|---|---|
| `code_change` | Source or test code changed |
| `configuration_change` | Project/application configuration changed |
| `dependency_change` | Dependency version or lock state changed |
| `permission_change` | Access or permission configuration changed |
| `environment_change` | Local runtime, toolchain, or environment changed |
| `agent_instruction` | User-level agent instruction changed |
| `project_rule` | Repository/project agent rule changed |
| `monitor_hook` | Agent monitoring hook/configuration changed |
| `other` | A deliberate category outside the fixed catalog |

### 3.2 Eligibility

A fix attempt may be recorded only when, at the supplied issue snapshot:

- the issue ID is syntactically valid and visible;
- at least one occurrence is visible;
- the aggregate issue has `analysis_status=current`;
- the latest visible anchor occurrence also has `analysis_status=current`;
- the issue is not `experimental`;
- the issue category is not `evidence_gap`.

Belay and Numbat origins are eligible. Only `resolved` and `lexical` scope
qualities are eligible. Unscoped and conflicting issues cannot support the
feature's recurrence purpose and remain history-only attention signals.

### 3.3 Non-functional requirements

1. Local-only and offline; no telemetry or hosted dependency.
2. No free-text note, command, path, diff, prompt, output, rule body, hook body,
   environment value, or URL is accepted or stored.
3. No update or delete operation for fix annotations or retractions.
4. The route requires loopback, bearer authentication, same-origin browser
   intent, JSON, and a fixed intent header.
5. Request bodies are bounded to 1 KiB and reject unknown fields/trailing JSON.
6. History page size defaults to 20 and is capped at 100.
7. Writes never dirty sessions, change issue projection generation, alter
   detector state, or suppress future occurrences.
8. Ordinary retention does not select annotations/retractions, and annotations
   do not retain source evidence.
9. Errors never reflect the idempotency key, cursor, issue ID, or request body.
10. All browser dynamic values render as text with allowlisted classes.

## 4. Current State / Background and Context

### 4.1 Issue projection

P0-01/P0-02 provide stable issue/fingerprint IDs, revisioned occurrence
snapshots, exact matching sessions, and a 15-minute rowless issue view cursor.
Issue occurrences are derived and retention-deletable. They are not suitable
as foreign-key owners for durable annotations.

### 4.2 Presentation boundaries

The shared readmodel has explicit optional capability interfaces. Local HTTP
receives issue capabilities explicitly; MCP receives only the core repository.
Fix capabilities must follow the same explicit injection pattern and must not
be added to MCP.

### 4.3 Security boundary

Local HTTP is loopback-only, bearer-authenticated, no-store, frame-denied, and
served with a restrictive CSP. There are no cookies. The write route adds
strict same-origin and explicit-intent validation because loopback alone is not
a sufficient browser-write boundary.

### 4.4 Retention

Ordinary pruning currently evaluates canonical events, immutable findings, and
issue occurrence revisions. Fix annotations are intentionally outside those
bounds. They remain until the Local database is reset. Mistakes are corrected
through append-only retractions.

### 4.5 Approved P0 scope override

BRD v2.2 deferred fix recording. The later 2026-09-08 Local P0 roadmap decision
documented in P0-01 explicitly advances Local fix annotation and recurrence
measurement. This feature implements only that Local override and does not
change hosted, Teams, or public product claims until their source documents are
separately revised.

## 5. High Level Design

### 5.1 Component flow

```text
Issue detail + view cursor
        -> eligibility endpoint
        -> aggregate issue validation
        -> signed issue-bound action token

Confirmation dialog + action token + idempotency key
        -> listener-bound Local HTTP write route
        -> localaction.Service
        -> one storage transaction:
           replay/conflict lookup
           token freshness/snapshot validation
           aggregate issue eligibility
           exact anchor revision/citation capture
           insert/replay/conflict
        -> append-only fix_annotations

Undo/retract confirmation
        -> same write security boundary
        -> append-only fix_annotation_retractions
```

History follows a separate read path:

```text
Issue detail
    -> Local HTTP GET
    -> localaction/read service
    -> snapshot-stable fix annotation repository
```

### 5.2 Capability separation

Introduce:

```go
type FixAnnotationReader interface {
    QueryFixAnnotations(context.Context, model.FixAnnotationQuery) (
        model.FixAnnotationPage,
        error,
    )
}

type FixAnnotationWriter interface {
    RecordFixAnnotation(context.Context, local.FixAnnotationInput) (
        local.FixAnnotationResult,
        error,
    )
}
```

Add an action-token capability:

```go
type FixActionTokenCodec interface {
    IssueFixActionToken(model.FixActionClaims) (string, error)
    DecodeFixActionToken(string) (model.FixActionClaims, error)
}
```

`internal/localaction.Service` receives the reader, writer, retraction writer,
and token codec. It owns fixed request validation and error classification but
does not import the presentation/readmodel package.

Local HTTP receives the action service explicitly. MCP receives neither
interface and remains unchanged.

### 5.3 Annotation semantics

The stored record is a fact about user input:

> At `recorded_at`, through the Local UI, the developer declared an external
> change attempt of `change_kind` while inspecting this exact issue
> fingerprint.

It is not a fact that:

- the change was applied correctly;
- the issue was fixed or resolved;
- later absence was caused by the change;
- future matching occurrences should be hidden;
- the annotation is advice or an instruction.

### 5.4 Eligibility and signed action token

The unsigned P0-02 view cursor is a transport mechanism, not proof. When issue
detail loads, the browser requests:

```text
GET /v1/issues/{issue_id}/fix-eligibility?view_cursor=...
```

HTTP asks readmodel to structurally decode and freshness-check the view cursor,
then passes neutral `IssueViewClaims` to `localaction.Service`. The action
service performs a read transaction that derives the same aggregate issue
summary as `QueryIssues`, validates all eligibility rules, and verifies the
latest visible occurrence.

If eligible, it returns a signed token with:

```go
type FixActionClaims struct {
    Version    string
    IssueID    string
    Snapshot   int64
    IssuedAt   time.Time
    ExpiresAt  time.Time
}
```

The token is authenticated with a store-derived HMAC key under:

```text
belay.local.fix-action-token.v1
```

The browser cannot alter the issue, snapshot, or expiry. The write path decodes
and authenticates the token, validates its structure, and binds its issue ID to
the route before durable idempotency replay is checked. Only token
expiry/snapshot freshness is deferred until after replay lookup.

### 5.5 Anchor selection

The browser does not supply an occurrence ID. Inside the write transaction,
storage first derives and validates the complete aggregate issue at the signed
snapshot. It then selects the latest visible occurrence without pre-filtering:

```text
last_observed_at DESC, occurrence_id ASC
```

That exact latest occurrence must itself be current. Storage captures:

- occurrence revision ID, occurrence ID, and session ID;
- fingerprint ID/version;
- origin;
- detector ID/version;
- scope quality;
- issue snapshot generation;
- anchor analysis generation;
- first/last observed timestamps;
- the exact cited event IDs and baseline citation count.

This minimizes user friction and prevents a stale/mismatched client occurrence
or an older current occurrence from becoming the baseline while newer analysis
is incomplete.

### 5.6 Idempotency

The browser creates one UUIDv4 idempotency key when the confirmation dialog
is first confirmed. It retains that unresolved draft and key across dialog
closure/reopening, timeout, network failure, and 5xx. The user must explicitly
abandon an unresolved draft before a new key is generated.

Storage derives:

```text
annotation_id =
    fxa_<base32(HMAC-SHA256(fix-annotation-key, idempotency-key))>
```

The key uses a new domain-separated HKDF purpose:

```text
belay.local.fix-annotation.v1
```

The raw idempotency key is never persisted or logged. Storage also persists a
domain-separated request fingerprint over canonical client intent:

```text
contract_version |
issue_id |
signed_snapshot |
signed_issued_at |
change_catalog_version |
change_kind |
recorded_via
```

On annotation-ID conflict:

- the same request fingerprint returns the original row as a replay even if the
  action token has expired;
- a different request fingerprint is an idempotency conflict;
- server-selected anchor fields are not recomputed for replay.

Creation uses `INSERT ... ON CONFLICT(annotation_id) DO NOTHING`, followed by a
reread/compare so correctness does not depend on connection serialization.

Retractions use a separate idempotency key/domain and the same replay/conflict
semantics.

### 5.7 History snapshots

Fix annotations and retractions each use a monotonic internal sequence. A fresh
history request captures both high-water marks in one read transaction. Cursor
pages carry:

- kind `fix_annotations`;
- exact issue ID binding;
- annotation snapshot sequence;
- retraction snapshot sequence;
- last `recorded_at`;
- last annotation ID.

Ordering is `recorded_at DESC, annotation_id DESC`. New annotations after page
one and new retractions after page one are excluded from that cursor chain.
Fix-history cursors do not expire because records are durable and ordinary
retention does not compact them.

History remains readable independently of current issue projection visibility.
A syntactically valid issue ID with no annotation rows returns `200` with an
empty list.

### 5.8 Evidence retention status

Each annotation stores the exact anchor revision ID, baseline citation count,
and a transactional copy of its cited event IDs in
`fix_annotation_events`. Those rows reference events with `ON DELETE CASCADE`
so they do not retain evidence.

History computes:

```text
baseline = 0                         -> unknown
remaining citations = baseline      -> available
remaining citations = 0             -> pruned
0 < remaining citations < baseline  -> partial
```

Annotation membership/order is cursor-stable. Evidence status is explicitly
named `evidence_currently_retained`, evaluated in the same read transaction as
each page, and accompanied by `evidence_evaluated_at`.

### 5.9 Append-only retraction

A mistaken fix attempt is corrected by appending one retraction record, never
by changing or deleting the annotation. Retraction reasons are fixed enums.
History retains both facts and exposes annotation state:

- `active`;
- `retracted`.

P0-04 recurrence ignores retracted attempts while preserving their audit
history.

## 6. Low Level Design

### 6.1 Canonical model

Add `internal/canonical/model/fix.go`:

```go
type FixChangeKind string

const (
    FixChangeCode              FixChangeKind = "code_change"
    FixChangeConfiguration     FixChangeKind = "configuration_change"
    FixChangeDependency        FixChangeKind = "dependency_change"
    FixChangePermission        FixChangeKind = "permission_change"
    FixChangeEnvironment       FixChangeKind = "environment_change"
    FixChangeAgentInstruction  FixChangeKind = "agent_instruction"
    FixChangeProjectRule       FixChangeKind = "project_rule"
    FixChangeMonitorHook       FixChangeKind = "monitor_hook"
    FixChangeOther             FixChangeKind = "other"
)

type FixAnnotation struct {
    AnnotationID         string        `json:"annotation_id"`
    IssueID              string        `json:"issue_id"`
    AnchorRevisionID     string        `json:"anchor_revision_id"`
    AnchorOccurrenceID   string        `json:"anchor_occurrence_id"`
    AnchorSessionID      string        `json:"anchor_session_id"`
    FingerprintID        string        `json:"fingerprint_id"`
    FingerprintVersion   string        `json:"fingerprint_version"`
    Origin               string        `json:"origin"`
    DetectorID           string        `json:"detector_id"`
    DetectorVersion      string        `json:"detector_version"`
    ScopeQuality         ScopeQuality  `json:"scope_quality"`
    IssueSnapshotGeneration int64      `json:"issue_snapshot_generation"`
    AnchorAnalysisGeneration int64     `json:"anchor_analysis_generation"`
    AnchorFirstObservedAt time.Time     `json:"anchor_first_observed_at"`
    AnchorLastObservedAt time.Time      `json:"anchor_last_observed_at"`
    ChangeKind           FixChangeKind `json:"change_kind"`
    ChangeCatalogVersion string        `json:"change_catalog_version"`
    RecordedVia          string        `json:"recorded_via"`
    RecordedAt           time.Time     `json:"recorded_at"`
    MonitorFrom          time.Time     `json:"monitor_from"`
    EvidenceCurrentlyRetained string   `json:"evidence_currently_retained"`
    State                 string        `json:"state"`
    RetractionReason      string        `json:"retraction_reason,omitempty"`
    RetractedAt           *time.Time    `json:"retracted_at,omitempty"`
}
```

Add `FixRetraction`, signed action claims, query/page/position types, and fixed
catalog constants. No field is free text.

### 6.2 Storage schema

Add `010_fix_annotations.sql`:

```sql
CREATE TABLE IF NOT EXISTS fix_annotations (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    annotation_id TEXT NOT NULL UNIQUE,
    request_fingerprint TEXT NOT NULL,
    issue_id TEXT NOT NULL,
    anchor_revision_id TEXT NOT NULL,
    anchor_occurrence_id TEXT NOT NULL,
    anchor_session_id TEXT NOT NULL,
    fingerprint_id TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    scope_quality TEXT NOT NULL CHECK (
        scope_quality IN ('resolved', 'lexical', 'unscoped', 'conflict')
    ),
    issue_snapshot_generation INTEGER NOT NULL CHECK (
        issue_snapshot_generation > 0
    ),
    anchor_analysis_generation INTEGER NOT NULL CHECK (
        anchor_analysis_generation > 0
    ),
    anchor_first_observed_at TEXT NOT NULL,
    anchor_last_observed_at TEXT NOT NULL,
    baseline_citation_count INTEGER NOT NULL CHECK (
        baseline_citation_count >= 0
    ),
    change_kind TEXT NOT NULL CHECK (
        change_kind IN (
            'code_change',
            'configuration_change',
            'dependency_change',
            'permission_change',
            'environment_change',
            'agent_instruction',
            'project_rule',
            'monitor_hook',
            'other'
        )
    ),
    change_catalog_version TEXT NOT NULL CHECK (
        change_catalog_version = 'fix-change.v1'
    ),
    recorded_via TEXT NOT NULL CHECK (recorded_via = 'local_ui'),
    recorded_at TEXT NOT NULL,
    monitor_from TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS fix_annotation_events (
    annotation_id TEXT NOT NULL
        REFERENCES fix_annotations(annotation_id) ON DELETE CASCADE,
    event_id TEXT NOT NULL
        REFERENCES events(event_id) ON DELETE CASCADE,
    PRIMARY KEY (annotation_id, event_id)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS fix_annotation_retractions (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    retraction_id TEXT NOT NULL UNIQUE,
    request_fingerprint TEXT NOT NULL,
    annotation_id TEXT NOT NULL UNIQUE
        REFERENCES fix_annotations(annotation_id),
    reason TEXT NOT NULL CHECK (
        reason IN ('recorded_by_mistake', 'superseded', 'other')
    ),
    recorded_via TEXT NOT NULL CHECK (recorded_via = 'local_ui'),
    retracted_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS fix_annotations_issue_order_idx
    ON fix_annotations(issue_id, recorded_at DESC, annotation_id DESC);

CREATE INDEX IF NOT EXISTS fix_annotations_fingerprint_monitor_idx
    ON fix_annotations(fingerprint_id, monitor_from, annotation_id);
```

Do not add a foreign key from annotations to issue occurrences, revisions, or
sessions. The citation side table may reference events with cascading deletion
because it must observe, not prevent, evidence pruning.

All stored timestamps use fixed-width `formatProjectionTime`.

Use distinct HKDF/HMAC domains for annotation IDs, annotation request
fingerprints, action-token authentication, retraction IDs, and retraction
request fingerprints.

### 6.3 Mutation guards

Install connection-local `BEFORE UPDATE` and `BEFORE DELETE` triggers on
`fix_annotations` and `fix_annotation_retractions` that always abort. No
mutation purpose authorizes changing or deleting either record.

Connection initialization must conditionally install these triggers only after
the table exists:

1. initialize existing core guards;
2. inspect for both Feature 3 tables;
3. install annotation/retraction guards when present;
4. post-migration guard installation covers the initial upgraded connection.

### 6.4 Signed token and neutral claims

Readmodel exposes a narrow method that converts a valid P0-02 view cursor into
neutral `model.IssueViewClaims`. It does not expose private cursor internals.
`localaction` never imports readmodel.

The action service validates issue eligibility and asks the token codec to sign
`FixActionClaims`. Token decoding verifies structure and MAC but does not reject
expiry. It also requires the signed issue ID to equal the route issue before
calling storage. New-write freshness is enforced transactionally after replay
lookup.

### 6.5 Write input and storage transaction

Storage input:

```go
type FixAnnotationInput struct {
    Claims          model.FixActionClaims
    ChangeKind      model.FixChangeKind
    RecordedVia     string
    IdempotencyKey string
}
```

Transaction steps:

1. receive already MAC-authenticated, structurally valid, route-bound claims;
2. validate fixed enums, claims values, IDs, and canonical UUIDv4 key;
3. derive annotation ID and canonical request fingerprint;
4. begin one write transaction;
5. read an existing annotation ID:
   - same request fingerprint → replay original row;
   - different fingerprint → typed conflict;
6. for a new ID, validate signed snapshot freshness/retention;
7. derive the same aggregate issue summary as `QueryIssues`;
8. reject non-current, experimental, evidence-gap, unscoped, or conflicting
   aggregate issues;
9. select the latest visible occurrence without eligibility pre-filtering and
   require that exact occurrence to be current;
10. capture exact revision/generations/timestamps/citations;
11. set `recorded_at=monitor_from=Store.now().UTC()` using fixed-width storage;
12. `INSERT ... ON CONFLICT(annotation_id) DO NOTHING`;
13. if not inserted, reread and compare request fingerprint;
14. insert citation sidecar rows;
15. commit without touching issue/session projection state.

This ordering allows a confirmed replay after token expiry while preventing a
new write from using stale claims.

### 6.6 Retraction transaction

Retraction input contains annotation ID, issue ID, fixed reason,
`recorded_via=local_ui`, and a separate UUIDv4 idempotency key.

The transaction:

1. derives retraction ID/request fingerprint;
2. handles replay/conflict;
3. verifies the annotation belongs to the path issue;
4. returns `already_retracted` if another active retraction exists;
5. appends one retraction using `ON CONFLICT` recovery.

### 6.7 History repository

`QueryFixAnnotations`:

- validates exact issue ID;
- bounds limit to 20/100;
- captures annotation and retraction `MAX(sequence)` values for a fresh
  snapshot;
- filters both tables to their cursor high-water marks;
- orders by recorded time and annotation ID;
- binds cursor to issue ID;
- left-joins the one retraction;
- computes evidence status from baseline citation count and remaining sidecar
  rows;
- returns `evidence_evaluated_at`;
- returns limit+one behavior for stable pagination.

Membership/order are snapshot-stable. Evidence status is intentionally
evaluated at page-read time and may change between pages after retention.

### 6.8 Local action service

Add `internal/localaction` with no dependency on HTTP, browser, or MCP.

Primary methods:

```go
PrepareFixAttempt(ctx, issueID, model.IssueViewClaims) (FixEligibility, error)
RecordFixAttempt(ctx, issueID, actionToken, changeKind, idempotencyKey)
RetractFixAttempt(ctx, issueID, annotationID, reason, idempotencyKey)
ListFixAttempts(ctx, issueID, limit, cursor)
```

Service errors:

- `ErrInvalidRequest`;
- `ErrCursorExpired`;
- `ErrNotFound`;
- `ErrIneligibleIssue`;
- `ErrIdempotencyConflict`;
- `ErrAlreadyRetracted`.

History uses a dedicated fix cursor codec. Token/key/request values are never
logged.

### 6.9 HTTP contract

#### `GET /v1/issues/{id}/fix-eligibility`

Accepts exactly one `view_cursor` query parameter. Returns:

```json
{
  "schema_version": "belay.fix.v1",
  "data": {
    "eligible": true,
    "reason": "eligible",
    "action_token": "<signed opaque token>",
    "expires_at": "2026-09-08T18:15:00Z",
    "change_catalog_version": "fix-change.v1"
  }
}
```

Ineligible current issues return `200` with `eligible=false`, a fixed reason,
and `action_token=null`. Missing/expired/invalid cursor behavior follows issue
detail.

Fixed ineligibility reasons are:

- `analysis_not_current`;
- `experimental_signal`;
- `evidence_gap`;
- `scope_unavailable`.

The aggregate and anchor are evaluated in one transaction over the same visible
occurrence set. No visible occurrence therefore means the issue is absent at
that snapshot and returns `404`, not an ineligibility reason. Because aggregate
analysis status is the least-current status across every visible occurrence,
any non-current anchor is already represented by `analysis_not_current`.

#### `POST /v1/issues/{id}/fixes`

Required headers:

```text
Authorization: Bearer <launch token>
Content-Type: application/json
Idempotency-Key: <canonical UUIDv4>
X-Belay-Intent: record-fix-attempt.v1
Origin: http://<exact numeric listener address and port>
```

If `Sec-Fetch-Site` is present it must equal `same-origin`.

Body:

```json
{
  "action_token": "<signed opaque token>",
  "change_kind": "code_change"
}
```

Rules:

- maximum body: 1 KiB;
- no query parameters;
- `Content-Type` must be `application/json` with optional UTF-8 charset;
- non-identity `Content-Encoding` is rejected;
- security headers must each have exactly one value;
- exactly one JSON object;
- duplicate JSON fields are rejected;
- unknown fields and trailing data rejected;
- no client timestamp, annotation ID, fingerprint, status, note, or
  occurrence ID.

First creation returns `201`:

```json
{
  "schema_version": "belay.fix.v1",
  "data": {
    "annotation_id": "fxa_...",
    "issue_id": "iss_...",
    "anchor_revision_id": "ior_...",
    "anchor_occurrence_id": "occ_...",
    "anchor_session_id": "ses_...",
    "fingerprint_id": "ifp_...",
    "fingerprint_version": "1",
    "origin": "belay",
    "detector_id": "explicit_command_failure",
    "detector_version": "1",
    "scope_quality": "resolved",
    "issue_snapshot_generation": 42,
    "anchor_analysis_generation": 41,
    "anchor_first_observed_at": "2026-09-08T17:00:00Z",
    "anchor_last_observed_at": "2026-09-08T17:02:00Z",
    "change_kind": "code_change",
    "change_catalog_version": "fix-change.v1",
    "recorded_via": "local_ui",
    "recorded_at": "2026-09-08T18:00:00Z",
    "monitor_from": "2026-09-08T18:00:00Z",
    "evidence_currently_retained": "available",
    "state": "active",
    "retraction_reason": null,
    "retracted_at": null
  },
  "replayed": false
}
```

Identical replay returns `200` with the original record and `replayed=true`.

#### `GET /v1/issues/{id}/fixes`

Accepts:

- `limit` default 20, maximum 100;
- fix-history `cursor`.

Returns standard list metadata plus `schema_version=belay.fix.v1`.

```json
{
  "schema_version": "belay.fix.v1",
  "data": [],
  "next_cursor": null,
  "has_more": false,
  "returned_count": 0,
  "limit": 20,
  "evidence_evaluated_at": "2026-09-08T18:05:00Z"
}
```

A valid issue ID with no annotations returns this empty `200` response even if
the current issue projection no longer contains that issue.

#### `POST /v1/issues/{id}/fixes/{annotation_id}/retractions`

Uses the same listener-bound write protections, with:

```text
X-Belay-Intent: retract-fix-attempt.v1
```

Body:

```json
{"reason":"recorded_by_mistake"}
```

First append returns `201`, identical replay returns `200`, and an annotation
already retracted through another request returns fixed `409`.

Response:

```json
{
  "schema_version": "belay.fix.v1",
  "data": {
    "retraction_id": "fxr_...",
    "annotation_id": "fxa_...",
    "issue_id": "iss_...",
    "reason": "recorded_by_mistake",
    "recorded_via": "local_ui",
    "retracted_at": "2026-09-08T18:10:00Z"
  },
  "replayed": false
}
```

### 6.10 Listener-bound write security

Write validation is bound to the actual listener created by `Start`:

- exact `Origin == http://<listener.Addr()>`;
- exact request `Host == <listener.Addr()>`;
- numeric loopback host only;
- exact bound port;
- forwarded-host/proto headers are ignored;
- absent/`null` Origin is rejected;
- `Sec-Fetch-Site`, when present, must be `same-origin`;
- CORS remains disabled.

Direct test handlers must receive an explicit trusted numeric loopback origin;
without one, write routes fail closed.

### 6.11 HTTP error precedence

Evaluation order is total:

1. loopback and route match;
2. bearer authentication;
3. canonical listener Origin/Host, fetch-site, and intent;
4. content type, then content encoding, then body limit;
5. path/header/body/query syntax, then action-token structure/MAC and signed
   issue-to-route binding;
6. derive annotation/retraction ID and check replay/conflict;
7. action-token expiry and snapshot freshness for new fix attempts;
8. issue/annotation existence and eligibility;
9. insert and commit.

| Condition | Status/type |
|---|---|
| Missing/invalid bearer | 401 existing Local problem |
| Cross-origin, missing Origin, `null` Origin, bad fetch-site or intent | 403 fixed `belay.local/write-forbidden` |
| Unsupported media type | 415 fixed `belay.local/unsupported-media-type` |
| Body exceeds 1 KiB | 413 fixed `belay.local/request-too-large` |
| Malformed JSON, unknown field, invalid enum/key/ID/cursor | 400 existing invalid-request shape |
| Invalid token MAC, token issue mismatch, or impossible token claims | 400 existing invalid-request shape |
| Expired/compacted signed action token snapshot for a new write | 410 `belay.local/cursor-expired` |
| Issue absent at snapshot | 404 fixed not-found |
| Experimental/evidence-gap/non-current issue | 409 `belay.local/ineligible-fix-annotation` |
| Idempotency key reused with different canonical content | 409 `belay.local/idempotency-conflict` |
| Annotation already retracted by another request | 409 `belay.local/already-retracted` |
| Storage failure | 500 fixed Local write failure |

No problem detail reflects request values.

### 6.12 Browser flow

Issue detail gains a **Fix attempts** section above Matching sessions.

Eligible current stable issue:

1. Issue detail loads eligibility independently; it prominently says:
   **This version records attempts only; recurrence monitoring is not yet
   available.**
2. **Record fix attempt** opens a modal dialog with `role=dialog`,
   `aria-modal=true`, an accessible name/description, inert background, focus
   containment, Escape-to-close when not submitting, and short-height scrolling.
3. Dialog explains:
   **Records your declaration that you attempted an external change. Belay
   cannot verify the change or its effect.**
4. Developer deliberately selects one **Primary change category**. Definitions
   and examples are fixed by `fix-change.v1`; there is no default selection.
5. Browser creates a UUIDv4 idempotency key only on first confirmation.
6. Confirm submits the current signed action token.
7. Pending state disables duplicate submit.
8. Creation announces:
   **Fix attempt declaration recorded · Not verified by Belay.**
9. Replay announces:
   **Previously recorded attempt restored; no duplicate created.**
10. Creation focuses the connected new history row; replay focuses the
    connected existing row or live status.
11. Cancel/close restores focus by logical ID to a currently connected trigger.

Draft/key lifecycle:

- timeout/network/5xx keeps the dialog/draft/key retryable;
- closing/reopening preserves an unresolved draft and key;
- changing category after a submission attempt requires explicit abandonment
  and a new key;
- confirmed 410 discards the stale key/token, preserves category, refreshes
  Attention, and requires a new confirmation/key;
- never auto-resubmit.

403/409/validation failures render an inline alert and move focus to that alert
or the invalid control. Background panes remain inert while the dialog is open.
All primary controls are at least 44px on mobile.

History:

- loads independently from occurrences;
- uses explicit Load more;
- shows primary change category, catalog version, recorded time, state,
  retraction, and evidence status;
- offers **Retract** with a separate accessible confirmation and fixed reason;
- never says fixed, resolved, successful, prevented, or safe.

### 6.13 CLI and MCP

No CLI command is added in Feature 3. `recorded_via=cli` is not accepted yet.

MCP remains exactly six tools. There is no `record_fix`, `list_fixes`, or other
annotation tool in P0 MCP. P0-05 adds read-only issue diagnosis tools only.

### 6.14 Retention and reset

- Ordinary `belay prune` excludes fix annotations and retractions.
- Annotation rows do not count toward event/finding payload limits.
- Source pruning may change only computed evidence status and citation sidecars.
- Full database deletion/reset removes annotations with all Local state.
- No individual deletion, update, or export in Feature 3.

## 7. Monitoring

Belay sends no telemetry. Payload-free local diagnostics may report:

- total fix annotation count;
- active and retracted annotation counts;
- most recent annotation timestamp;
- idempotency replay count;
- idempotency conflict count;
- rejected ineligible annotation count;
- retraction replay/conflict count.

Diagnostics must not include annotation IDs, issue IDs, fingerprint IDs,
idempotency keys, or request values.

## 8. Resolved Decisions

1. P0 accepts no free-text note.
2. Records are called **fix-attempt declarations**, not resolved fixes.
3. The unsigned view cursor is exchanged for a signed issue-bound action token.
4. The server validates the aggregate issue before choosing the latest visible
   anchor occurrence.
5. Exact anchor revision/generation/timestamps/citations are persisted.
6. Annotations and retractions survive ordinary retention and do not retain
   evidence.
7. Mistakes are corrected by append-only retractions.
8. Only resolved/lexical, current, stable, non-evidence-gap issues are eligible.
9. Recording is browser-only; MCP and CLI remain unchanged.
10. Same-origin listener binding, intent, JSON, bearer, signed token, and
    idempotency protections are all required for writes.
11. Feature 3 prominently says recurrence monitoring is not yet available.

## 9. Task Breakdown

1. Add canonical fix/retraction models, catalog validation, identity/request
   fingerprint domains, and signed action token claims.
2. Add migration 010, citation sidecars, conditional immutable guards, and
   lifecycle tests.
3. Implement eligibility derivation and signed action-token issuance.
4. Implement transactional record/replay/conflict logic with exact anchor
   capture.
5. Implement append-only retraction/replay/conflict logic.
6. Implement durable fix-history snapshots and read-time evidence status.
7. Add local action service and neutral view-claims handoff.
8. Add explicit HTTP eligibility/action/read capabilities and listener-bound
   security/error handling.
9. Add browser dialog, unresolved-draft recovery, history, retraction, focus
   behavior, and bounded pagination.
10. Update contracts/requirements while keeping MCP read-only and labeling
   recurrence as unavailable until P0-04.
11. Run full unit, race, vet, release, migration, privacy, and browser static
   verification.
12. Independently review storage/idempotency and HTTP/browser claims/security.

## 10. Appendix

Primary references:

- `docs/design/p0-01-issue-fingerprints-and-detectors.md`
- `docs/design/p0-02-attention-inbox-and-matching-sessions.md`
- `docs/contracts/read-api-v1.md`
- `docs/contracts/mcp-v1.md`
- `internal/storage/local/identity.go`
- `internal/storage/local/issue_repository.go`
- `internal/storage/local/mutation.go`
- `internal/storage/local/retention.go`
- `internal/presentation/readmodel/service.go`
- `internal/presentation/localhttp/server.go`
- `internal/presentation/localhttp/assets/`
