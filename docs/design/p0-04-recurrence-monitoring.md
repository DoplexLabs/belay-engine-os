# P0-04: Exact Post-Attempt Recurrence Monitoring

- **Status:** Implemented and verified after independent storage/projection,
  API/integration, and product-truthfulness reviews
- **Date:** 2026-09-08
- **Scope:** Canonical recurrence model, Local storage and projection
  integration, readmodel/HTTP, and embedded browser
- **Depends on:** P0-01 issue identities, P0-02 Attention Inbox, and P0-03
  explicit fix-attempt declarations
- **Explicitly excludes:** causal attribution, fix verification, automatic
  remediation, suppression, write-capable MCP, Teams, hosted processing,
  semantic similarity, and generated diagnosis

## 1. Overview

P0-03 lets a developer record that they attempted an external change against
an exact Belay issue fingerprint. P0-04 turns that immutable declaration into
useful follow-up: Belay compares later completed Local analysis with the
attempt's exact baseline and reports whether exact matching evidence was
observed after the attempt.

This feature measures observation, not causality. “Matching evidence observed
after the attempt” means a stable detector produced the same exact fingerprint
and cited at least one canonical event strictly after the attempt. “No later
exact match observed” means only that compatible, completed, retained Local
analysis has not reported that exact match. Neither statement proves that the
attempted change caused, fixed, or prevented anything.

Positive recurrence observations are persisted append-only so ordinary
retention or later reanalysis cannot erase the historical fact that Belay
reported matching evidence after the attempt. P0 does not automatically
invalidate historical positive observations. Negative status remains
coverage-qualified and may become incomplete when relevant analysis or
retained coverage is insufficient.

## 2. Glossary

- **Attempt:** One active or retracted P0-03 fix-attempt declaration.
- **Baseline:** The immutable P0-03 issue snapshot, anchor occurrence,
  analysis generation, citations, and `monitor_from` timestamp.
- **Fingerprint scope:** The exact store-keyed opaque project scope used when
  deriving one issue occurrence identity. It may differ from session scope.
- **Monitoring subject:** Fixed, payload-free issue display, fingerprint scope,
  and detector semantics captured for an attempt.
- **Comparable activity:** Canonical activity in the same opaque project scope
  with at least one event strictly after `monitor_from`.
- **Qualifying recurrence:** A current, stable, non-evidence-gap occurrence
  with the same issue/fingerprint semantics and at least one cited canonical
  event strictly after `monitor_from`.
- **Observation:** An immutable durable record that Belay found one qualifying
  recurrence for one attempt and one occurrence/session.
- **Detector capability:** Payload-free proof that one analysis revision
  evaluated a particular origin, detector, fingerprint version, and exact
  fingerprint scope with semantics that permit a negative comparison.
- **Positive-only capability:** A source can support exact positive matches but
  cannot prove absence. Current Numbat integration is positive-only.
- **Coverage:** The relevant current/pending/failed/truncated Local analysis
  used to qualify a no-match statement.
- **Anchor evidence:** P0-03 citations supporting the original issue at
  declaration time.
- **Recurrence evidence:** Cited post-attempt events supporting a recurrence
  observation.

## 3. Requirements

### 3.1 Functional requirements

1. Automatically monitor every active eligible P0-03 attempt; no separate
   registration action is required.
2. Detect only exact post-attempt recurrence:
   - same issue ID, fingerprint ID, fingerprint version, and origin;
   - stable, non-experimental issue signal;
   - not an evidence gap;
   - current analysis;
   - resolved or lexical project scope;
   - at least one cited canonical event whose normalized instant is strictly
     greater than the normalized `monitor_from` instant.
3. Never count:
   - a projection generation change by itself;
   - a late import whose event occurred before or exactly at `monitor_from`;
   - an unsupported or incompatible fingerprint version;
   - pending, failed, or truncated analysis;
   - a retracted attempt.
4. Permit a later event in the anchor session to count. The browser must not
   assume session boundaries are fix boundaries.
5. Record at most one recurrence observation per attempt and stable occurrence
   ID. Additional later cited events in that occurrence do not inflate the
   recurrence count.
6. Let one later occurrence count independently for multiple active attempts
   when it is strictly after each attempt's baseline.
7. Preserve recurrence observations across ordinary event, session, and issue
   projection retention.
8. Preserve evidence availability separately from recurrence state.
9. Exclude retracted attempts from active monitoring while keeping their
   recurrence history auditable.
10. Support multiple active attempts. The most actionable active attempt drives
    the issue-level monitoring summary; counts expose whether other active
    attempts also have matching observations.
11. If the driving attempt is retracted, recompute the driver from remaining
    active attempts.
12. Provide a durable, independently paginated “After attempts” browser
    section that remains useful if the current issue projection disappears.
13. Show attempt-level state and recurrence evidence in issue detail.
14. Preserve P0-02 Attention and exact matching-session behavior.
15. Keep all-retracted durable history discoverable through an explicit
    `include_retracted` filter and history-only issue detail.
16. Keep MCP exactly read-only. P0-05 may expose recurrence as diagnosis
    evidence but does not gain a mutation capability.

### 3.2 Truthfulness requirements

1. Use only these user-visible state meanings:
   - `matching_evidence_observed`;
   - `monitoring_incomplete`;
   - `awaiting_later_evidence`;
   - `no_later_match_observed`;
   - `comparison_unavailable`;
   - `retracted`.
2. Never describe an attempt as fixed, resolved, successful, working,
   prevented, verified, or safe.
3. Never infer that the attempt caused a later state.
4. `no_later_match_observed` requires:
   - at least one comparable completed analysis after the baseline;
   - no qualifying recurrence observation;
   - no relevant pending, failed, or truncated coverage at the snapshot.
5. If any recurrence observation exists, the state remains
   `matching_evidence_observed`
   even when other coverage is incomplete. The response separately reports
   `analysis_complete=false`; the count is a lower bound.
6. Evidence pruning or later reanalysis never changes
   `matching_evidence_observed` into a no-match state.
7. Unknown enum values render as “Monitoring status unavailable” and never
   gain positive/success styling.

### 3.3 Privacy and security requirements

1. Store no prompt, completion, command, output, file contents, raw path,
   change description, note, or generated diagnosis.
2. Project scope remains a store-keyed opaque identifier.
3. Recurrence IDs use a distinct store-derived HMAC domain.
4. Monitoring subjects and observations are append-only. Capability rows are
   immutable projection revisions. Job state mutates only through the
   recurrence-worker authorization. Citation sidecars may be deleted only by
   existing event-retention cascades.
5. Reads use existing bearer authentication and loopback restrictions.
6. P0-04 introduces no new HTTP mutation route, browser write intent, CLI
   mutation, hook behavior, or MCP mutation.
7. Local diagnostics are payload-free and contain no issue, annotation,
   fingerprint, session, event, or recurrence IDs.

### 3.4 Reliability and performance requirements

1. A durable recurrence job is atomically enqueued with the successful session
   projection commit. Observation evaluation is restart-safe and bounded
   outside the projection transaction.
2. Projection retry, job retry, process restart, rebuild, and concurrent stores
   cannot duplicate an observation.
3. Projection CAS failure persists neither the projection nor its recurrence
   job.
4. Monitoring work is indexed by exact fingerprint scope, issue/fingerprint
   semantics, and baseline time.
5. Every list is bounded to default 20, maximum 100, and uses opaque
   endpoint-specific cursors.
6. Fresh reads and all cursor pages are snapshot-consistent for membership,
   state, and ordering. A retention-generation change expires an existing
   cursor chain rather than silently changing state.
7. Evidence availability is evaluated against the cursor's retention
   generation. A retention change expires the cursor; a fresh read may return
   degraded evidence.

## 4. Current State / Background and Context

P0-01 provides deterministic issue/fingerprint identities and revisioned issue
occurrences. P0-02 exposes the Attention Inbox and exact matching sessions.
P0-03 adds:

- immutable fix annotations and retractions;
- exact issue/fingerprint/origin identity;
- anchor revision, occurrence, session, analysis generation, and timestamps;
- exact baseline citation sidecars;
- `monitor_from`;
- durable annotation/retraction high-water pagination.

Issue occurrences are revisioned but retention-deletable. Deriving recurrence
only from the current projection would therefore allow a previously observed
recurrence to disappear after pruning. P0-04 needs a durable positive ledger.

P0-03 does not persist the exact opaque fingerprint scope ID or durable issue
display metadata on the annotation. Session scope is not always sufficient:
Numbat may derive one occurrence from a finding-specific project-scope hint,
and one session may therefore contain multiple fingerprint scopes. Migration
011 persists `fingerprint_scope_id` on occurrence revisions, adds a
monitoring-subject sidecar, and performs only provable backfills. Future
annotations copy scope from the anchor occurrence transactionally.

Current Belay detectors can emit a durable capability manifest proving that a
particular detector/fingerprint version ran for one exact scope. Current
Numbat integration does not provide a negative-completion manifest. Numbat
therefore supports positive recurrence observations only; absence of a Numbat
finding never produces a no-match claim.

Existing issue-list `recurrence=single|repeated` describes whether an issue is
present in one or multiple retained sessions. It is not post-attempt
recurrence. P0-04 uses the distinct names `fix_recurrence_state` and
`fix_recurrence_count`.

## 5. High-Level Design

### 5.1 Data flow

```text
P0-03 fix attempt
      |
      v
monitoring subject + immutable baseline
      |
      v
later session analysis commits a projection + bounded recurrence job
      |
      v
restart-safe recurrence worker
      |
      +--> evaluate active attempts in the same exact fingerprint scope
               |
               +--> exact fingerprint + post-baseline cited event?
                           |
                           v
                    append recurrence observation

read transaction
      |
      +--> annotation/retraction high-waters
      +--> observation high-water
      +--> issue projection generation
      +--> event/read generation
      +--> retention generation
      +--> coverage at that generation
      |
      v
After attempts summary + attempt detail + browser
```

### 5.2 Monitoring state machine

State is computed per attempt in this precedence order:

1. `retracted` when a retraction is visible at the snapshot.
2. `matching_evidence_observed` when at least one observation is visible.
   Missing future-comparison capability is returned as a separate qualifier
   and never conceals historical positive evidence.
3. `comparison_unavailable` when no observation exists and the subject lacks a
   usable exact fingerprint scope or compatible negative-comparison
   capability.
4. `monitoring_incomplete` when no observation exists and any
   relevant post-baseline analysis is pending, failed, or truncated.
5. `awaiting_later_evidence` when there is no comparable completed
   post-baseline activity.
6. `no_later_match_observed` when comparable completed activity exists, no
   observation exists, and relevant compatible coverage is complete.

`matching_evidence_observed` includes:

- `fix_recurrence_count`: distinct occurrence/session observations;
- `analysis_complete`: whether other relevant coverage is complete;
- `count_is_lower_bound`: inverse of `analysis_complete`;
- `future_comparison_available` and a fixed unavailability reason.

No state is terminal. Later Local activity can move:

- awaiting → incomplete;
- awaiting/incomplete/no-match → matching evidence observed;
- incomplete → no-match after successful analysis;
- any active state → retracted.

Historical positive observations are monotonic in P0. Later analysis may add
coverage context but does not automatically invalidate or erase them.
Retraction changes participation, not history. A retracted attempt with prior
observations exposes `historical_matching_evidence_count > 0` and fixed wording
that matching evidence was recorded before retraction.

### 5.3 Exact qualification

For one annotation and one newly committed occurrence, a recurrence qualifies
only when all conditions hold:

```text
annotation is active
monitoring subject has resolved/lexical opaque scope
occurrence fingerprint_scope_id == subject fingerprint_scope_id
projection status == current
occurrence.issue_id == annotation.issue_id
occurrence.fingerprint_id == annotation.fingerprint_id
occurrence.fingerprint_version == annotation.fingerprint_version
occurrence.origin == annotation.origin
occurrence.experimental == false
occurrence.category != evidence_gap
occurrence.scope_quality in {resolved, lexical}
projection_generation > annotation.issue_snapshot_generation
at least one cited canonical event:
  event.session_key == occurrence.session
  event.occurred_at_order_ns > annotation.monitor_from_order_ns
```

Detector-version changes are allowed only when fingerprint version remains
identical. The observation records both baseline and observed detector
provenance.

The anchor session is not categorically excluded. A continued session can
contain matching evidence after an attempt. The user-facing wording does not
call that a new session-level recurrence; it says **Exact matching evidence was
observed after this attempt** and adds **This may be continuation within the
original session** when applicable. Strict normalized cited-event time and
projection-generation boundaries prevent original anchor evidence from being
recounted.

### 5.4 Positive ledger versus negative coverage

Positive recurrence is durable. Once an observation is appended, retention or
later reanalysis cannot erase it in P0.

Negative status is deliberately coverage-qualified and derived at the cursor
snapshot. It may degrade from `no_later_match_observed` to
`awaiting_later_evidence` or `monitoring_incomplete` when retained comparable
coverage is no longer sufficient. The browser wording always says “in
retained, completed Local analysis.”

This asymmetry prevents false confidence:

- a known observed recurrence is not forgotten;
- absence is never claimed beyond currently provable coverage.

### 5.5 Multiple attempts

Attempts are independent:

- a new attempt does not retract or supersede an older attempt;
- the same later occurrence may qualify for each attempt whose baseline it
  follows;
- the most actionable active attempt drives the issue-level “After attempts”
  card, with active and observed-attempt counts;
- issue detail shows all attempts and their independent states;
- retracting the driving attempt recomputes the driver;
- retracted attempts retain observation history but do not receive new
  observations.
- all-retracted issues are discoverable through `include_retracted=true`.

## 6. Low-Level Design

### 6.1 Canonical model

Add `internal/canonical/model/recurrence.go`.

Fixed schema/constants:

```go
const (
    FixMonitoringSchemaVersion = "belay.fix-monitoring.v1"

    FixRecurrenceMatchingEvidence    = "matching_evidence_observed"
    FixRecurrenceMonitoringIncomplete = "monitoring_incomplete"
    FixRecurrenceAwaitingEvidence    = "awaiting_later_evidence"
    FixRecurrenceNoLaterMatch        = "no_later_match_observed"
    FixRecurrenceComparisonUnavailable = "comparison_unavailable"
    FixRecurrenceRetracted           = "retracted"

    FixRecurrenceEvidenceAvailable = "available"
    FixRecurrenceEvidencePartial   = "partial"
    FixRecurrenceEvidencePruned    = "pruned"
    FixRecurrenceEvidenceUnknown   = "unknown"

    FixComparisonScopeUnavailable       = "scope_unavailable"
    FixComparisonSourcePositiveOnly     = "source_positive_only"
    FixComparisonCapabilityUnavailable  = "capability_unavailable"
    FixComparisonBaselineTimeUnavailable = "baseline_time_unavailable"
    FixComparisonFingerprintUnsupported = "fingerprint_version_unsupported"
)
```

Primary model types:

- `FixMonitoringSubject`;
- `AnalysisCapability`;
- `FixRecurrenceJob`;
- `FixRecurrenceObservation`;
- `FixMonitoringCoverage`;
- `FixAttemptMonitoring`;
- `FixMonitoringSummary`;
- `FixMonitoringQuery/Page/Position`;
- `FixMonitoringDetailQuery/Page/Position`;
- `FixRecurrenceObservationQuery/Page/Position`.

`FixMonitoringCoverage` contains only bounded counters/timestamps:

```go
type FixMonitoringCoverage struct {
    ComparableCurrent   int
    ComparablePending   int
    ComparableFailed    int
    ComparableTruncated int
    AnalysisThrough     *time.Time
    Complete            bool
}
```

`FixAttemptMonitoring` includes:

- annotation ID and immutable attempt category/timestamps;
- fixed recurrence state;
- observation count;
- same-anchor-session and other-session observation counts;
- historical observation count even when retracted;
- `analysis_complete` and `count_is_lower_bound`;
- `future_comparison_available` and fixed reason;
- anchor and recurrence evidence status;
- nullable newest qualifying observation timestamp;
- monitoring coverage;
- retraction metadata;
- no free text.

Unknown or future unavailability reasons fail closed to
`capability_unavailable` in presentation; the raw unknown value is not echoed.

### 6.2 Migration 011

Add `011_fix_recurrence.sql`.

#### Exact fingerprint scope and normalized time

Extend occurrence persistence with:

```sql
ALTER TABLE issue_occurrences
    ADD COLUMN fingerprint_scope_id TEXT;
```

For every future resolved/lexical occurrence, the exact opaque scope used by
`DeriveIssueIdentity` is required and copied into the revision. Unscoped
occurrences may remain null but cannot become monitoring subjects.

Backfill rules:

- Belay occurrence: copy retained `session_scopes.project_scope_id` only when
  scope quality and issue identity are consistent.
- Numbat occurrence: derive/copy the opaque scope from the retained finding's
  exact `ProjectScopeHint` and origin record only when provable.
- Otherwise leave null. Never substitute session scope for an unprovable
  Numbat hint.

Add an integer normalized event-time column:

```sql
ALTER TABLE events ADD COLUMN occurred_at_order_ns INTEGER;
```

A Go-assisted migration scans existing rows, parses RFC3339Nano to an instant,
and writes UTC Unix nanoseconds in bounded transactions. New ingestion writes
the normalized value with the original timestamp. A row that cannot be parsed
is quarantined from recurrence comparison rather than ordered lexically.

The normalized baseline is stored in the new monitoring-subject sidecar rather
than updating append-only `fix_annotations`. The Go-assisted migration parses
each existing annotation's `monitor_from` while creating its subject. Future
annotation writes persist the annotation and subject atomically. An annotation
whose baseline cannot be normalized receives an unavailable subject and can
never produce a comparison claim.

The backfill is resumable and crash-safe:

1. SQL migration 011 adds nullable columns/tables plus a
   `local_migration_progress` marker.
2. `Store.migrate` resumes event/annotation batches by stable row sequence
   before `installMutationGuards` and before the Store is returned to any
   caller.
3. New writes always populate normalized columns during backfill.
4. Existing append-only annotation guards remain enabled because backfill
   inserts sidecars and never updates annotations.
5. The Store is not returned and no Local server starts until both normalized
   scans complete.
6. The completion marker and capability enablement commit atomically.

Legacy catch-up is mandatory before monitoring is called ready:

1. enqueue deterministic recurrence jobs for every retained compatible
   completed analysis revision whose activity watermark may be after at least
   one attempt, including closed/non-current revisions;
2. those jobs can recover positive Belay and Numbat observations from retained
   occurrences/citations;
3. mark retained Belay sessions dirty once so the normal reconciler emits
   detector-specific absence capabilities and exact fingerprint scopes;
4. do not synthesize negative capability from legacy “current” status alone;
5. keep monitoring readiness `catching_up` until catch-up jobs and required
   Belay reanalysis have converged.

Add singleton `fix_monitoring_metadata`:

```sql
CREATE TABLE fix_monitoring_metadata (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    readiness TEXT NOT NULL CHECK (
        readiness IN ('catching_up', 'ready', 'failed')
    ),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    retry_at TEXT,
    failure_code TEXT,
    catchup_started_at TEXT,
    catchup_completed_at TEXT,
    updated_at TEXT NOT NULL
);
```

Schema and normalized-time backfill are synchronous inside `Store.migrate`; an
error prevents Store opening and therefore no server exists. Historical
projection catch-up and one-time Belay reanalysis are background runtime work
after the server starts. Only P0-04 monitoring routes return a temporary fixed
503 while readiness is `catching_up`; all pre-existing Local functionality
remains available.

Readiness transitions are fixed:

- first P0-04 open or incomplete restart: `catching_up`;
- successful legacy jobs plus required Belay reanalysis: `ready`;
- a retryable catch-up coordinator/storage failure: `failed` with a fixed
  payload-free failure code, incremented attempt count, and bounded `retry_at`;
- due retry or next Local restart: `failed → catching_up`;
- successful retry: `catching_up → ready`;
- context cancellation leaves durable progress as `catching_up` and is not
  recorded as failure.

Both `catching_up` and `failed` fail P0-04 reads closed with fixed 503 problems.
No partial legacy monitoring result is presented as complete. Once readiness is
`ready`, an individual later recurrence-job failure does not globally disable
monitoring; it contributes incomplete coverage for affected subjects and
continues its own retry cycle.

Extend each immutable session analysis revision with:

- `analysis_through_order_ns` nullable;
- `analyzed_event_generation`;
- a stable revision identifier if the current schema does not already expose
  one.

These fields freeze which activity the revision actually analyzed. Coverage
never combines an old analysis revision with later live events.

#### `fix_monitoring_subjects`

```sql
CREATE TABLE fix_monitoring_subjects (
    annotation_id TEXT PRIMARY KEY
        REFERENCES fix_annotations(annotation_id),
    fingerprint_scope_id TEXT,
    scope_capture_status TEXT NOT NULL CHECK (
        scope_capture_status IN ('captured', 'unavailable')
    ),
    category TEXT,
    title_code TEXT,
    severity TEXT,
    confidence TEXT,
    anchor_harness TEXT,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    negative_comparison_mode TEXT NOT NULL CHECK (
        negative_comparison_mode IN ('supported', 'positive_only')
    ),
    monitor_from_order_ns INTEGER,
    captured_at TEXT NOT NULL
) WITHOUT ROWID;
```

Constraints require `fingerprint_scope_id` when status is `captured` and null
when unavailable. Nullable display fields mean unavailable; no fabricated
unknown catalog code participates in filtering or severity ordering.

Migration backfill:

1. join the exact retained anchor occurrence;
2. use its backfilled `fingerprint_scope_id`;
3. copy fixed display/provenance fields when retained;
4. mark Belay subjects `supported`;
5. mark current Numbat subjects `positive_only`;
6. use `scope_capture_status=unavailable` and nullable display fields when
   retained state is insufficient;
7. never infer or reconstruct a raw path.

Future `RecordFixAnnotation` inserts this subject in the same transaction as
the annotation. Failure rolls back both.

#### `session_analysis_capabilities`

```sql
CREATE TABLE session_analysis_capabilities (
    revision_id TEXT NOT NULL
        REFERENCES session_analysis_revisions(revision_id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    fingerprint_scope_id TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    negative_comparison_mode TEXT NOT NULL CHECK (
        negative_comparison_mode IN ('supported', 'positive_only')
    ),
    analysis_through_order_ns INTEGER,
    PRIMARY KEY (
        revision_id, fingerprint_scope_id, origin,
        detector_id, fingerprint_version
    )
) WITHOUT ROWID;
```

Belay analysis emits one row only when a stable detector explicitly reports
sufficient input coverage for an absence claim, including when it emitted no
occurrence. Current Numbat ingestion emits `positive_only`; it never proves
absence. Capability rows are revisioned with analysis and survive ordinary
event retention long enough to support cursor snapshots.

The detector contract is extended from “matches only” to a fixed result:

```go
type AbsenceCapability string

const (
    AbsenceSupported     AbsenceCapability = "supported"
    AbsenceNotApplicable AbsenceCapability = "not_applicable"
    AbsenceIncomplete    AbsenceCapability = "incomplete"
)

type DetectorResult struct {
    Matches           []Match
    AbsenceCapability AbsenceCapability
    UnavailableReason string
}
```

Each built-in detector must explicitly prove its prerequisites before
returning `supported`. “No match” alone is never converted into negative
capability. `not_applicable`, missing prerequisite coverage, detector failure,
or truncation produces no supported capability and therefore cannot support a
no-match claim.

#### `fix_recurrence_jobs`

```sql
CREATE TABLE fix_recurrence_jobs (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL UNIQUE,
    session_id TEXT NOT NULL,
    revision_id TEXT NOT NULL
        REFERENCES session_analysis_revisions(revision_id),
    projection_generation INTEGER NOT NULL,
    annotation_snapshot INTEGER NOT NULL CHECK (annotation_snapshot >= 0),
    state TEXT NOT NULL CHECK (
        state IN ('pending', 'claimed', 'complete', 'failed')
    ),
    attempt_after_sequence INTEGER NOT NULL DEFAULT 0 CHECK (
        attempt_after_sequence >= 0
    ),
    claim_generation INTEGER NOT NULL DEFAULT 0 CHECK (claim_generation >= 0),
    claim_token TEXT,
    lease_expires_at TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    retry_at TEXT,
    claimed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

One job is enqueued atomically per committed projection revision. At enqueue it
captures `MAX(fix_annotations.sequence)`. Batches scan
`sequence > attempt_after_sequence AND sequence <= annotation_snapshot`
ordered by sequence, so membership is frozen and restart deterministic.
Complete jobs remain linked to their revision while that revision can support
monitoring coverage. Retention may delete obsolete jobs only under
`retention_prune`, before deleting the completed revision, and doing so
increments retention generation. The restrictive revision foreign key
automatically pins queued/failed/claimed jobs.

Logical job state for snapshot reads comes from an append-only event ledger:

```sql
CREATE TABLE fix_recurrence_job_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL REFERENCES fix_recurrence_jobs(job_id)
        ON DELETE CASCADE,
    event_kind TEXT NOT NULL CHECK (
        event_kind IN ('queued', 'failed', 'complete')
    ),
    attempt_number INTEGER NOT NULL CHECK (attempt_number >= 0),
    recorded_at TEXT NOT NULL,
    UNIQUE(job_id, event_kind, attempt_number)
);
```

`queued` is appended in the projection transaction. Observation inserts and
the terminal `complete` event commit atomically in the worker transaction.
Failure appends `failed`; a later retry may append `complete`. Claim/lease
changes are operational and do not alter snapshot-visible logical state.
Job events are append-only during the job lifetime; retention-authorized parent
deletion cascades them atomically and advances retention generation.

#### `fix_recurrence_observations`

```sql
CREATE TABLE fix_recurrence_observations (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    recurrence_id TEXT NOT NULL UNIQUE,
    annotation_id TEXT NOT NULL
        REFERENCES fix_annotations(annotation_id),
    issue_id TEXT NOT NULL,
    revision_id TEXT NOT NULL,
    occurrence_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    fingerprint_id TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('belay', 'numbat')),
    detector_id TEXT NOT NULL,
    detector_version TEXT NOT NULL,
    analysis_generation INTEGER NOT NULL CHECK (analysis_generation > 0),
    projection_generation INTEGER NOT NULL CHECK (projection_generation > 0),
    first_qualifying_event_order_ns INTEGER NOT NULL,
    last_qualifying_event_order_ns INTEGER NOT NULL,
    qualifying_citation_count INTEGER NOT NULL CHECK (
        qualifying_citation_count > 0
    ),
    evidence_complete INTEGER NOT NULL CHECK (evidence_complete IN (0, 1)),
    same_session_as_anchor INTEGER NOT NULL CHECK (
        same_session_as_anchor IN (0, 1)
    ),
    observed_at TEXT NOT NULL,
    UNIQUE(annotation_id, occurrence_id)
);
```

#### `fix_recurrence_observation_events`

```sql
CREATE TABLE fix_recurrence_observation_events (
    recurrence_id TEXT NOT NULL
        REFERENCES fix_recurrence_observations(recurrence_id)
        ON DELETE CASCADE,
    event_id TEXT NOT NULL
        REFERENCES events(event_id)
        ON DELETE CASCADE,
    PRIMARY KEY (recurrence_id, event_id)
) WITHOUT ROWID;
```

The parent observation survives event retention. Sidecar rows may cascade.

#### Retention generation

```sql
ALTER TABLE issue_projection_metadata
    ADD COLUMN retention_generation INTEGER NOT NULL DEFAULT 1;
```

Any prune that deletes events, sessions, scopes, findings, occurrence
revisions, analysis revisions, or recurrence citation sidecars increments this
generation in the same transaction. A monitoring cursor whose retention
generation no longer matches returns 410. Fresh reads may then truthfully
degrade negative coverage or evidence status.

Indexes:

- subject fingerprint scope + issue/fingerprint semantics + annotation;
- capability scope + detector/fingerprint semantics + revision;
- pending job state + retry/order;
- job event sequence + job;
- annotation + recurrence sequence;
- issue + observation time;
- session + fingerprint + observation;
- annotation monitor time.

Monitoring subjects and observations receive connection-local update/delete
guards. Capability revisions follow existing projection-rebuild/retention
authorization. Job state changes are allowed only under a dedicated recurrence
worker authorization. Job events are append-only. Citation sidecar updates are
forbidden; deletes are allowed only under retention pruning. Recursive triggers
remain enabled so replacement statements cannot bypass guards.

### 6.3 Identity

Add distinct store-derived domains:

- `belay.local.fix-recurrence-id.v1`;
- `belay.local.fix-recurrence-job-id.v1`.

Opaque prefixes are:

- recurrence observation: `fxo_`;
- recurrence job: `fxj_`.

They have dedicated validators and cannot be accepted as annotation (`fxa_`)
or retraction (`fxr_`) IDs.

Observation identity is deterministic over:

```text
annotation_id
occurrence_id
```

Job identity is deterministic over:

```text
session_id
projection_generation
```

This makes projection/job retries and concurrent stores converge on one row.

### 6.4 Projection integration

Extend `replaceSessionProjection` inside the existing mutation-authorized
transaction:

1. validate projection CAS target;
2. close prior analysis/occurrence revisions;
3. insert the new analysis revision, exact fingerprint scopes, normalized
   analysis watermark, detector capabilities, and occurrences;
4. enqueue one deterministic recurrence job;
5. acknowledge dirty-session state;
6. commit.

This keeps projection latency bounded. A committed projection always has a
durable job; a failed CAS has neither.

The Local recurrence worker runs after analysis drain, on Local startup, and
periodically while the Local process is active. HTTP GET requests never perform
hidden writes; they report pending work as incomplete. Each claim processes at
most 100 attempts, persists its cursor, and retries with bounded backoff. A
failed job makes affected coverage incomplete and never produces a no-match
claim.

Claim protocol:

1. claim one pending/failed job whose retry time is due, or one claimed job
   whose lease expired;
2. increment `claim_generation`, generate an in-memory random claim token, set
   `lease_expires_at`, and commit with compare-and-swap;
3. read the next annotation-sequence batch inside the frozen
   `annotation_snapshot`;
4. in one transaction, recheck each annotation's retraction, insert eligible
   observations, append any failure event, and advance
   `attempt_after_sequence`;
5. every progress/finalization write includes job ID, claim generation, and
   claim token; a stale worker updates zero rows and discards its result;
6. when the cursor reaches `annotation_snapshot`, append `complete` and mark
   the job complete in the same transaction.

Claim tokens are never persisted to diagnostics or API responses. Retraction is
checked in the same transaction as observation insertion, so a declaration
retracted before that transaction cannot receive a new observation.

Retention must pin the queued revision, its occurrence citations, and required
capability rows while a job's latest logical event is `queued` or `failed`.
There is no best-effort drop path; full Local reset is the only way to discard
unprocessed pinned work.

Candidate selection uses:

- exact `fingerprint_scope_id`;
- issue ID, fingerprint ID/version, and origin;
- `monitor_from_order_ns`;
- non-retracted annotations at the worker snapshot;
- only occurrences/capabilities from the queued immutable revision.

For each exact matching occurrence:

1. resolve cited event IDs through `issue_occurrence_events`;
2. constrain events to the occurrence session;
3. select only normalized event instants strictly greater than
   `monitor_from_order_ns`;
4. sort/deduplicate IDs;
5. derive deterministic recurrence ID;
6. insert observation;
7. insert citation sidecars.

On recurrence-ID/uniqueness conflict, the worker rereads the row:

- the same annotation ID and occurrence ID means the occurrence was already
  observed; projection-dependent provenance/timestamps do not need to match
  and the first observation remains authoritative;
- a deterministic recurrence ID mapped to a different annotation/occurrence
  identity is a collision, fails the job, and records a payload-free
  diagnostic.

Observation writes and the job's `complete` event commit in one transaction.
No observation is created for pending/failed/truncated projections. P0 adds no
automatic supersession or deletion of historical observations.

### 6.5 Coverage derivation

Coverage is computed in the same read transaction and at the same projection
generation as monitoring state.

Comparable session criteria:

- a capability row with the same exact fingerprint scope, origin, detector,
  and fingerprint version as the monitoring subject;
- `negative_comparison_mode=supported`;
- capability `analysis_through_order_ns > monitor_from_order_ns`;
- active analysis revision visible at the cursor projection generation;
- session is not excluded merely because it is the anchor session.

Belay capability rows support negative comparison. Current Numbat capability
rows are positive-only and therefore cannot increment comparable-current or
produce `no_later_match_observed`.

Counters come from the immutable revision status/capability and analyzed event
watermark. A `current` compatible revision is comparable completed analysis.
Pending, failed, truncated, missing-revision, failed-job, and unprocessed-job
states make coverage incomplete.

At the captured event/read generation, any post-baseline dirty session whose
latest event generation is newer than its analyzed generation is pending
coverage. Before its exact fingerprint scope is materialized, it is
conservatively pending for compatible Belay subjects rather than ignored. This
may delay a no-match claim but cannot create a false one.

A current compatible capability counts toward `comparable_current` only when
the corresponding recurrence job has a `complete` event visible at the
snapshot. `queued` or latest-`failed` jobs count as incomplete.

`analysis_through` is the greatest immutable capability watermark considered,
not wall-clock time. Empty coverage encodes as `null`.

### 6.6 Repository reads and snapshot model

Add storage methods:

```go
QueryFixMonitoring(ctx, model.FixMonitoringQuery) (model.FixMonitoringPage, error)
QueryIssueFixMonitoring(ctx, model.FixMonitoringDetailQuery) (model.FixMonitoringDetailPage, error)
QueryFixRecurrenceObservations(ctx, model.FixRecurrenceObservationQuery) (model.FixRecurrenceObservationPage, error)
```

A fresh snapshot captures in one read transaction:

- current issue projection generation;
- current event/read generation;
- current retention generation;
- annotation max sequence;
- retraction max sequence;
- recurrence job max sequence;
- recurrence job-event max sequence;
- recurrence observation max sequence;
- issued-at time.

Cursor claims bind:

- endpoint/schema version;
- every high-water value;
- issue ID for detail;
- annotation ID for observation detail;
- normalized filters;
- effective page size;
- row position;
- issued-at.

Cursor lifetime remains 15 minutes. Cursor pages:

- use existing opaque, strictly validated read cursors; they are not described
  as cryptographically signed;
- keep attempt membership/retraction/observation stable;
- compute coverage from revisioned state at the captured projection generation;
- keep state and ordering stable;
- require the captured retention generation to remain current, otherwise 410;
- evaluate evidence sidecar retention at the retained cursor generation and
  return `evidence_evaluated_at`.

The top-level list returns a rowless `monitoring_view_cursor`. Detail may start
in either of two ways:

- from an After-attempts row: pass exactly `view_cursor` for the same snapshot;
- from normal P0-02 issue navigation or a direct route: omit both cursors and
  create a fresh monitoring snapshot.

The first detail response returns its own `monitoring_view_cursor`. Subsequent
attempt pages accept exactly `cursor`. Each attempt row returns an
annotation/issue-bound `observation_view_cursor`; observation pages are loaded
independently.

All initial list/detail/observation reads accept `limit` default 20, maximum
100. Continuation requests contain only `cursor`; the effective limit is
carried inside the cursor and cannot change mid-chain.

### 6.7 Ordering and grouping

Top-level “After attempts” returns one row per issue with active attempts at the
snapshot. `include_retracted=true` also returns all-retracted issues. Grouping
occurs before filters.

The driving active attempt is selected by:

```text
state_rank ASC,
last_matching_evidence_at DESC NULLS LAST,
recorded_at DESC,
annotation_id DESC
```

This prevents a newer awaiting attempt from hiding an older active attempt with
matching evidence. Rows include active-attempt and observed-attempt counts.

Order rows by:

1. recurrence state rank:
   - matching evidence observed;
   - incomplete;
   - comparison unavailable;
   - awaiting later evidence;
   - no later match observed;
   - retracted;
2. captured severity rank;
3. activity time =
   `COALESCE(last_matching_evidence_at, recorded_at)` descending;
4. issue ID.

Null severity sorts after known severity. The exact ordering tuple is embedded
in the cursor.

Count invariants:

- `active_attempt_count`: active attempts only;
- `observed_attempt_count`: active attempts with at least one observation;
- `historical_matching_evidence_count`: observations across active and
  retracted attempts;
- all-retracted rows have `active_attempt_count=0`,
  `observed_attempt_count=0`, state `retracted`, and preserve the historical
  count;
- retracted driving rows return zero/empty active coverage and null
  `analysis_through`; they do not imply current monitoring.

Filters:

- recurrence state;
- fixed change category;
- severity;
- harness;
- recorded-after;
- exact issue ID;
- include-retracted, exact `true|false`.

Filters apply to the already selected driving row, not to arbitrary hidden
attempts. `include_retracted=false` is the default. For an all-retracted issue
included explicitly, an attempt with historical matching observations outranks
an unobserved retracted attempt, then newest recorded time wins. Its state is
still `retracted`; historical observation counts remain visible.

Issue detail orders attempts by:

```text
recorded_at DESC, annotation_id DESC
```

Observation pages order by:

```text
first_qualifying_event_order_ns DESC, recurrence_id DESC
```

Equal-timestamp rows therefore paginate deterministically.

### 6.8 Readmodel

Add an optional recurrence repository capability to `readmodel.Service`.
Core-only/MCP construction without this capability continues to work.

New methods:

```go
ListFixMonitoring(ctx, FixMonitoringListRequest)
GetIssueFixMonitoring(ctx, FixMonitoringDetailRequest)
ListFixRecurrenceObservations(ctx, FixRecurrenceObservationListRequest)
```

The readmodel:

- normalizes fixed filters;
- validates/bounds limit;
- strictly validates endpoint-specific opaque cursors;
- maps fixed catalog codes to existing fixed display labels only;
- never produces narrative diagnosis;
- returns explicit unknown states rather than guessing.

### 6.9 HTTP API

Add read-only routes:

```text
GET /v1/fix-monitoring
GET /v1/issues/{id}/fix-monitoring
GET /v1/issues/{id}/fixes/{annotation_id}/recurrences
```

All use existing bearer authentication, loopback binding, strict query
parsing, response size limits, and fixed problem responses.

Top-level response:

```json
{
  "schema_version": "belay.fix-monitoring.v1",
  "data": [
    {
      "issue_id": "iss_...",
      "annotation_id": "fxa_...",
      "title_code": "explicit_command_failure",
      "severity": "high",
      "change_kind": "code_change",
      "recorded_at": "2026-09-08T18:00:00Z",
      "monitor_from": "2026-09-08T18:00:00Z",
      "fix_recurrence_state": "matching_evidence_observed",
      "fix_recurrence_count": 2,
      "same_anchor_session_observation_count": 1,
      "other_session_observation_count": 1,
      "historical_matching_evidence_count": 2,
      "active_attempt_count": 2,
      "observed_attempt_count": 1,
      "analysis_complete": false,
      "count_is_lower_bound": true,
      "future_comparison_available": true,
      "future_comparison_unavailable_reason": null,
      "last_recurrence_observed_at": "2026-09-08T19:14:00Z",
      "coverage": {
        "comparable_current": 3,
        "comparable_pending": 1,
        "comparable_failed": 0,
        "comparable_truncated": 0,
        "analysis_through": "2026-09-08T19:16:00Z",
        "complete": false
      }
    }
  ],
  "returned_count": 1,
  "limit": 20,
  "has_more": false,
  "next_cursor": null,
  "monitoring_view_cursor": "<opaque>",
  "evidence_evaluated_at": "2026-09-08T19:17:00Z"
}
```

`last_recurrence_observed_at` and `analysis_through` are explicit JSON nulls
when absent. `analysis_complete == coverage.complete`.
`count_is_lower_bound=true` only when observation count is positive and
coverage is incomplete; otherwise false.

Issue detail paginates attempts only. Each attempt contains summary recurrence
counts/status plus an observation-route reference. The separate recurrence
route returns bounded observation rows:

- recurrence ID;
- occurrence/session IDs;
- observed detector provenance;
- first/last qualifying event times;
- evidence status;
- `same_session_as_anchor`;
- no raw event payload.

Issue-detail response shape:

```json
{
  "schema_version": "belay.fix-monitoring.v1",
  "issue_id": "iss_...",
  "current_issue_available": false,
  "current_issue": null,
  "data": [
    {
      "annotation_id": "fxa_...",
      "subject": {
        "title_code": "explicit_command_failure",
        "severity": "high",
        "confidence": "high",
        "anchor_harness": "codex",
        "origin": "belay",
        "detector_id": "explicit_command_failure",
        "detector_version": "1",
        "fingerprint_version": "1"
      },
      "change_kind": "code_change",
      "recorded_at": "2026-09-08T18:00:00Z",
      "monitor_from": "2026-09-08T18:00:00Z",
      "state": "active",
      "retraction_reason": null,
      "retracted_at": null,
      "fix_recurrence_state": "matching_evidence_observed",
      "fix_recurrence_count": 1,
      "same_anchor_session_observation_count": 0,
      "other_session_observation_count": 1,
      "historical_matching_evidence_count": 1,
      "analysis_complete": true,
      "count_is_lower_bound": false,
      "future_comparison_available": true,
      "future_comparison_unavailable_reason": null,
      "last_recurrence_observed_at": "2026-09-08T19:14:00Z",
      "coverage": {
        "comparable_current": 1,
        "comparable_pending": 0,
        "comparable_failed": 0,
        "comparable_truncated": 0,
        "analysis_through": "2026-09-08T19:16:00Z",
        "complete": true
      },
      "anchor_evidence_currently_retained": "available",
      "recurrence_evidence": {
        "available": 1,
        "partial": 0,
        "pruned": 0,
        "unknown": 0
      },
      "observation_view_cursor": "<opaque>"
    }
  ],
  "returned_count": 1,
  "limit": 20,
  "has_more": false,
  "next_cursor": null,
  "monitoring_view_cursor": "<opaque>",
  "evidence_evaluated_at": "2026-09-08T19:17:00Z"
}
```

Observation response shape:

```json
{
  "schema_version": "belay.fix-monitoring.v1",
  "issue_id": "iss_...",
  "annotation_id": "fxa_...",
  "data": [
    {
      "recurrence_id": "fxo_...",
      "occurrence_id": "occ_...",
      "session_id": "ses_...",
      "fingerprint_version": "1",
      "origin": "belay",
      "detector_id": "explicit_command_failure",
      "detector_version": "1",
      "first_qualifying_event_at": "2026-09-08T19:13:00Z",
      "last_qualifying_event_at": "2026-09-08T19:14:00Z",
      "qualifying_citation_count": 2,
      "retained_event_ids": [
        "019921c0-7abc-7def-8abc-0123456789ab",
        "019921c1-7abc-7def-8abc-0123456789ab"
      ],
      "retained_event_count": 2,
      "missing_event_count": 0,
      "evidence_complete": true,
      "evidence_truncated": false,
      "evidence_currently_retained": "available",
      "same_session_as_anchor": false,
      "observed_at": "2026-09-08T19:15:00Z"
    }
  ],
  "returned_count": 1,
  "limit": 20,
  "has_more": false,
  "next_cursor": null,
  "evidence_evaluated_at": "2026-09-08T19:17:00Z"
}
```

Observation evidence is bounded to 50 retained event IDs, matching existing
exact event lookup limits. The browser may pass those IDs to the existing
session-constrained event lookup route; it never scans a broad timeline.

Evidence status mapping:

- `available`: retained sidecar count equals immutable
  `qualifying_citation_count`;
- `partial`: retained count is greater than zero but below the immutable count;
- `pruned`: immutable count is positive and retained count is zero;
- `unknown`: sidecar/count read could not be completed.

Retained event IDs are canonical lowercase UUIDv7 values and are validated
using the existing event-ID contract.

For `unknown`, the DTO is exact:

- `retained_event_ids=[]`;
- `retained_event_count=null`;
- `missing_event_count=null`;
- `evidence_truncated=null`;
- `evidence_currently_retained="unknown"`.

`evidence_complete` records detector completeness at observation time and does
not change with retention. `evidence_truncated=true` means more than 50
retained citations exist; the response returns the first 50 in canonical event
order and reports the full immutable count.

Detail includes:

- `current_issue_available`;
- `current_issue`, which is either:
  - a bounded current issue summary object when available; or
  - explicit JSON `null` in history-only mode;
- durable monitoring subject fields on every attempt;
- explicit null retraction fields for active attempts;
- explicit nullable subject fields.

Nullable subject fields are exactly `title_code`, `severity`, `confidence`, and
`anchor_harness`. They encode as JSON `null` when legacy backfill could not
prove them. Origin, detector ID/version, and fingerprint version are required
because they already exist on the annotation.

`current_issue_available=false` requires `current_issue=null`.
`current_issue_available=true` requires a bounded current issue summary object.
Every DTO/null invariant has explicit encoding and decoding tests.

Query contract:

- top list query names are `state`, `change_kind`, `severity`, `harness`,
  `recorded_after`, `issue_id`, `include_retracted`, `limit`, and `cursor`;
- all enum/filter values are lowercase fixed catalogs; `recorded_after` is
  RFC3339; IDs are exact canonical opaque IDs;
- top list allows one value per documented filter, limit, cursor;
- issue detail accepts `limit` plus either:
  - no cursor, which creates a fresh monitoring snapshot; or
  - exactly one `view_cursor` from an After-attempts row;
- later issue-detail pages accept exactly one `cursor`;
- first observation request accepts the attempt's exact
  `observation_view_cursor` and optional `limit`; later pages accept exactly
  one `cursor`;
- view/page cursor parameters are mutually exclusive;
- unknown/repeated parameters are 400;
- expired view/cursors or changed retention generation are 410;
- issue/annotation route mismatch is 404;
- storage failure is fixed 500 with no reflected values.

Invalid/expired/cross-endpoint/filter-mismatched cursors follow existing
400/410 behavior. Missing issue IDs return 404 only when neither current issue
projection nor durable fix history exists.

HTTP validation/error precedence is total:

1. loopback route and method match;
2. bearer authentication;
3. path ID syntax;
4. unknown/repeated query parameters and limit/filter syntax;
5. cursor/view-cursor mutual exclusion and envelope validation;
6. migration/capability readiness;
7. cursor expiry, projection compaction, or retention-generation mismatch;
8. issue/annotation route binding and durable-history existence;
9. repository read;
10. response encoding.

| Condition | Status/type |
|---|---|
| Missing/invalid bearer | 401 existing Local authentication problem |
| Wrong method/route | existing 404/405 behavior |
| Invalid ID/filter/limit/query/cursor envelope | 400 existing invalid-request problem |
| Historical monitoring catch-up still active | 503 `belay.local/monitoring-catchup-in-progress` with fixed retry guidance |
| Historical monitoring catch-up failed and is awaiting retry | 503 `belay.local/monitoring-catchup-failed` with fixed retry guidance and no internal failure detail |
| Expired/compacted cursor or retention generation changed | 410 `belay.local/cursor-expired` |
| Issue has neither current projection nor durable fix history | 404 fixed not-found |
| Annotation absent or belongs to another issue | 404 fixed not-found |
| Repository/encoding failure | 500 fixed Local read failure |

No problem detail reflects a request value, ID, cursor, scope, detector, or
stored event string.

### 6.10 Browser UX

Attention gains an independently paginated **After attempts** section before
the normal Issues list. It does not replace Attention and does not hide
verification evidence gaps.

The existing P0-02 filter currently labeled **Recurrence** is relabeled
**Session spread** in the browser; its API meaning remains `single|repeated`.
Post-attempt monitoring uses the separate fixed state filter.

Browser data ownership and refresh:

1. Opening from **After attempts** supplies that row's monitoring view cursor.
2. Opening from normal P0-02 Attention calls fresh monitoring detail without a
   cursor in parallel with existing issue/occurrence loading.
3. P0-04 monitoring detail is the authoritative rendered source for attempt
   history, monitoring state, and retraction state.
4. P0-03 `GET /fixes` remains a stable public compatibility route but is no
   longer a second browser-rendered attempt list.
5. Fix eligibility still loads independently from P0-03.
6. After successful record or retraction, the browser clears stale monitoring
   cursors, performs one fresh monitoring-detail load, and restores focus by
   annotation ID.
7. If monitoring refresh fails, the prior connected monitoring view remains
   visible with a scoped stale/error notice; matching sessions and fix actions
   continue independently.
8. Switching issues clears prior monitoring detail before the new response can
   render.

Summary card states use exact wording:

- `matching_evidence_observed`:
  **Exact matching evidence was observed after this attempt.**
  *This does not establish causality or whether the attempted change worked.*
- `monitoring_incomplete`:
  **Monitoring is incomplete.**
  *Some later Local activity is pending, failed, truncated, or only partially
  analyzed.*
- `awaiting_later_evidence`:
  **Awaiting later evidence.**
  *No comparable completed Local activity is available yet.*
- `no_later_match_observed`:
  **No later exact match was observed in retained, completed Local analysis.**
  *This does not verify resolution.*
- `comparison_unavailable`:
  **Comparison unavailable.**
  *The stored baseline cannot be compared under current exact-match
  semantics.*
- `retracted`:
  **Retracted declaration — excluded from active monitoring.**
- unknown:
  **Monitoring status unavailable.**

Presentation rules:

- matching-evidence-observed is attention/warning styling, not success styling;
- no-match is neutral, never green/check-mark success;
- state is always conveyed in text;
- incomplete coverage is visible even when recurrence exists;
- anchor and recurrence evidence retention are separate;
- a pruned observation remains observed with “Evidence pruned”;
- one click opens issue detail focused on the driving attempt;
- issue detail paginates attempts and lazily loads bounded observations;
- retraction remains available only for exact active annotations;
- same-anchor-session observations show:
  **This may be continuation within the original session.**
- summary and attempt rows expose same-anchor-session and other-session counts;
  whenever the same-session count is nonzero, the continuation caveat appears
  without requiring observation expansion;
- retracted attempts with historical observations show:
  **Matching evidence was recorded before this declaration was retracted.**
- all-retracted history is available through a visible filter;
- the former “recurrence monitoring is not yet available” P0-03 disclosure is
  removed only when both storage and browser P0-04 are present.

History-only detail uses monitoring-subject metadata when the current issue
projection is gone. It says current eligibility and matching sessions are
unavailable while preserving attempts and observations.

The UI discloses that payload-free monitoring metadata and positive
observations survive ordinary pruning even when cited evidence is no longer
retained.

Existing accessibility requirements remain:

- logical-ID focus restoration;
- logical IDs for monitoring cards, attempts, and observations;
- connected-node fallback when the driving attempt changes;
- stale detail is cleared before rendering another issue;
- keyboard operation and visible focus;
- 44px mobile controls;
- status text with appropriate live regions;
- safe text rendering and allowlisted attributes/classes;
- bounded loading with explicit Load more;
- independent partial-failure messaging for monitoring list, attempt detail,
  and observation history;
- no color-only meaning.

### 6.11 MCP and CLI

P0-04 adds no CLI command and no MCP tool. MCP remains exactly six read-only
tools under the current contract.

P0-05 may expose the P0-04 recurrence state as one input to read-only issue
diagnosis. It must consume the same repository/readmodel contract rather than
recompute recurrence from retained events.

Runtime wiring preserves current server-first recovery:

1. `local.Open` completes schema and normalized-time migration synchronously.
2. Local HTTP starts and all existing routes become available.
3. Analysis recovery starts under the launch context.
4. Historical monitoring catch-up, one-time Belay capability reanalysis, and
   recurrence worker startup follow analysis recovery.
5. Monitoring routes return the fixed catch-up 503 until metadata becomes
   `ready`.
6. Once ready, the periodic recurrence worker continues draining new jobs.
7. Context cancellation stops reconciler/worker goroutines and releases claims;
   readiness/progress/cursors remain durable.
8. Restart reclaims expired leases and resumes catch-up before marking ready.
9. MCP startup may run the recurrence worker for storage convergence but MCP's
   readmodel receives no monitoring or mutation capability in P0-04.

When readiness is `failed`, the retry scheduler moves it back to `catching_up`
only after `retry_at`; a manual Local restart may retry immediately. Failure
codes remain fixed payload-free internal diagnostics and are never reflected
in HTTP detail.

### 6.12 Retention and reset

Ordinary prune:

- never deletes monitoring subjects or observations;
- may delete annotation/observation event sidecars through event cascades;
- may degrade anchor/recurrence evidence status;
- may degrade negative coverage;
- increments retention generation whenever relevant rows are removed;
- never reverses a positive observation merely because evidence was
  pruned.

Full Local database reset removes recurrence state with all other Local state.

No per-observation delete/update/export is added.

### 6.13 Failure handling

- Projection failure before commit leaves no recurrence job.
- Projection retry derives the same job ID and converges.
- Job retry derives the same recurrence ID and converges.
- Concurrent projection/worker processes are protected by dirty-session CAS,
  bounded job claims, and unique IDs.
- Pending/failed/unprocessed jobs make negative coverage incomplete.
- Missing subject backfill yields `comparison_unavailable`, not guessed scope.
- Numbat positive-only capability never produces no-match.
- Global catch-up failure keeps monitoring routes fail-closed at fixed 503;
  bounded retry or restart may transition back to catching-up.
- Unsupported future enums render unknown.
- Read snapshot high-water beyond current maxima is invalid.
- Retention-generation mismatch expires the cursor with 410.
- Evidence lookup errors yield evidence status `unknown` without changing
  recurrence state.

## 7. Monitoring

Belay sends no telemetry. Payload-free local diagnostics may include:

- total monitoring subjects;
- active/retracted monitored attempts;
- counts by recurrence state;
- total observation counts;
- most recent recurrence observation time;
- observation conflict/replay count;
- pending/claimed/failed recurrence jobs and retry age;
- monitoring readiness, catch-up attempt count, and fixed failure-code count;
- comparison-unavailable count;
- monitoring evaluation duration and candidate count;
- evidence available/partial/pruned/unknown counts.

Diagnostics must not include identifiers or request/event values.

No hosted dashboard or alarm is added for Local alpha.

## 8. Resolved Decisions

1. Monitoring is automatic for active P0-03 attempts.
2. Exact cited-event time, not import or projection time alone, establishes a
   post-attempt recurrence.
3. Equality with `monitor_from` does not qualify.
4. The anchor session may qualify when it contains genuinely later cited
   evidence.
5. Positive observations are durable and append-only.
6. Negative no-match status is coverage-qualified and may degrade.
7. Retraction excludes an attempt but preserves its audit history.
8. Multiple attempts remain independent; no implicit supersession occurs.
9. One occurrence may count for multiple applicable attempts.
10. P0 performs no automatic observation supersession or invalidation.
11. Exact fingerprint scope is persisted on occurrences; session scope is not
    substituted for Numbat finding-specific scope.
12. Belay capability manifests support negative comparison. Current Numbat is
    positive-only.
13. Recurrence work is durably enqueued with projection and processed in
    bounded restart-safe jobs.
14. Normalized instant ordering, not timestamp text ordering, defines the
    strict post-attempt boundary.
15. Retention generation expires cursor chains whose coverage inputs changed.
16. P0-04 uses distinct post-attempt recurrence names and does not overload
    issue-list single/repeated recurrence.
17. The most actionable active attempt drives the durable “After attempts”
    summary; all-retracted history remains explicitly discoverable.
18. No-match is neutral and never presented as fix success.
19. MCP and CLI remain unchanged until their separately designed features.

## 9. Open Questions

None block implementation. Independent review must specifically validate:

- same-session qualification;
- exact Numbat fingerprint-scope backfill;
- Belay detector capability completeness and Numbat positive-only behavior;
- bounded job claim/retry/retention interaction;
- normalized timestamp migration;
- negative coverage semantics after retention;
- snapshot ordering across projection/read/retention and sequence high-waters;
- durable subject backfill for pre-011 annotations;
- list/detail/observation cursor handoff;
- history-only and all-retracted discovery;
- browser wording and no-success presentation.

## 10. Normative Acceptance Matrix

Every row requires an explicit automated test. “State” means the attempt state
at one frozen monitoring snapshot.

| Case | Input / setup | Required result |
|---|---|---|
| Exact later evidence | Same issue/fingerprint/version/origin/scope; cited event instant strictly after baseline | One `fxo_` observation; state `matching_evidence_observed` |
| Equal instant | Qualifying event instant equals `monitor_from` | No observation |
| Earlier late import | Imported after attempt but canonical event instant is earlier | No observation |
| Same-session continuation | Anchor session gains a strictly later cited event | Observation with `same_session_as_anchor=true`; summary and detail show continuation caveat |
| Other-session match | Later matching occurrence in another session | Observation with other-session count incremented |
| Projection-only change | New projection generation, no later cited event | No observation |
| Repeat matching projection | Same annotation/occurrence appears in later revision | Existing first observation is accepted; no duplicate and no failed job |
| Fingerprint-version change | Same detector/shape but different fingerprint version | No observation; future comparison unavailable/incomplete as applicable |
| Numbat positive | Exact retained Numbat occurrence after baseline | Positive observation allowed |
| Numbat absence | Later activity has no Numbat finding | Never `no_later_match_observed`; fixed `source_positive_only` qualifier |
| Detector not applicable | Belay detector reports no match and absence capability `not_applicable` | No supported capability and no no-match claim |
| Detector incomplete/failure | Detector prerequisites missing, failed, or truncated | `monitoring_incomplete` |
| Complete compatible absence | At least one completed supported Belay capability after baseline, complete job, no observation or other incomplete coverage | `no_later_match_observed` with neutral wording |
| Awaiting evidence | No compatible post-baseline capability/activity | `awaiting_later_evidence` |
| Scope unavailable | Legacy subject cannot prove exact fingerprint scope | `comparison_unavailable`; no guessed scope |
| Multiple attempts | One later occurrence follows two active baselines | One observation per applicable annotation |
| Newest attempt less actionable | Newer attempt is awaiting; older active attempt has an observation | Older observed attempt drives issue summary |
| Retraction before worker insert | Retraction commits before observation transaction | No new observation for that attempt |
| Retraction after observation | Observation exists, then attempt is retracted | State `retracted`, historical count/caveat retained, no future observations |
| All retracted | Issue projection absent and all attempts retracted | Hidden by default; visible with `include_retracted=true`; observed retracted attempt drives before unobserved |
| History-only detail | Current issue projection is absent but durable attempts exist | 200 with `current_issue_available=false`; attempts/observations usable; eligibility/matching sessions unavailable |
| More than 100 attempts | One job has 201 candidate annotations | Three deterministic batches; no omissions/duplicates |
| Crash after batch commit | Worker dies after observations/cursor commit | Reclaim resumes after persisted sequence without duplication |
| Crash before batch commit | Worker dies before transaction commit | Same batch safely reprocessed |
| Stale lease | Old worker writes after claim generation was reclaimed | CAS updates zero rows; stale results discarded |
| Concurrent stores | Two workers claim/process same work | One current claim wins; deterministic observation uniqueness |
| Migration interruption | Process stops during event/subject normalized-time backfill | Next open resumes marker; Store/server do not start until synchronous migration completes |
| Catch-up readiness | Server starts after schema migration but historical jobs/reanalysis remain | Existing routes work; P0-04 routes return fixed catch-up 503 |
| Catch-up failure/recovery | Catch-up coordinator fails, then retry succeeds | Readiness becomes failed; P0-04 routes return fixed failed 503; retry/restart transitions through catching-up to ready without partial results |
| Catch-up shutdown/restart | Context cancels while catch-up/worker owns leases | Goroutines stop, durable progress remains, expired leases are reclaimed on restart |
| Pre-011 positive catch-up | Any retained compatible completed revision, including a closed revision, has cited event after an existing attempt | Catch-up job creates the historical positive observation |
| Historical match then current no-match | Closed retained revision matched after attempt; current revision does not | Historical positive observation is recovered and remains authoritative |
| Pre-011 negative | Legacy current Belay projection lacks new capability manifest | No no-match until one-time reanalysis emits supported capability |
| Guard bypass | `UPDATE`, `DELETE`, and `INSERT OR REPLACE` target subject/observation/job-event tables | Unauthorized mutation rejected on fresh/upgraded/all pooled connections |
| Pending-job retention | Prune targets revision required by queued/failed job | Revision/citations retained; job remains processable |
| Completed-job retention | Prune removes obsolete completed job/revision | Job events cascade; retention generation increments atomically |
| Cursor retention race | Relevant prune occurs between cursor pages | Next page returns 410; no silent state/order change |
| New event after snapshot | Event arrives after captured event generation | Old cursor excludes it; fresh read may become incomplete |
| Equal-time pagination | Multiple attempt/observation rows share the primary timestamp | ID tie-breaker yields complete, duplicate-free pages; cursor preserves original limit |
| Evidence partial/pruned | Some/all observation citation sidecars are pruned | Positive state remains; exact evidence status/count changes only on fresh snapshot |
| Event-ID inspection | Observation has retained citations | At most 50 canonical lowercase UUIDv7 IDs returned and accepted by session-constrained lookup |
| Unknown evidence read | Sidecar/count read fails | Status unknown, IDs empty, retained/missing/truncated fields null |
| Unknown state/reason | Future/invalid enum reaches browser fixture | “Monitoring status unavailable”; neutral styling; no positive/retraction action inferred |
| Fresh normal issue navigation | User opens P0-02 issue without monitoring cursor | Fresh monitoring detail snapshot loads successfully |
| After-attempt navigation | User opens monitoring card | Detail uses row's view cursor and stays snapshot-consistent |
| Record/retract refresh | P0-03 write succeeds | Monitoring cursor cleared; one fresh detail reload; focus restored by annotation ID |
| Monitoring partial failure | Monitoring list/detail/observation request fails | Existing connected content remains with scoped error; Sessions/matching/fix actions remain usable |
| Accessibility | Keyboard/mobile fixtures for every monitoring state | Logical focus restore, connected fallback, live-region text, 44px controls, no color-only meaning |
| Safe rendering | Event/ID/catalog fixtures contain hostile strings | Text-only rendering and allowlisted classes/attributes; no HTML interpretation |
| MCP isolation | P0-04 fully wired | MCP remains exactly six read-only tools and receives no worker/mutation capability |
| Restart persistence | Record attempt/observation, close all handles, reopen same DB/key | Monitoring history/state and cursor validation remain correct |

## 11. Task Breakdown

1. Add canonical recurrence model, fixed enums, validation, and tests.
2. Add migration 011, exact occurrence fingerprint scope, normalized event
   ordering, analysis capabilities/watermarks, monitoring subjects, recurrence
   jobs, observations, citation sidecars, indexes, retention generation, and
   immutable guards.
3. Backfill only provable occurrence scopes/subjects and transactionally
   capture future subjects.
4. Add recurrence/job identity domains and tests.
5. Emit Belay negative-comparison capability manifests and Numbat
   positive-only manifests.
6. Atomically enqueue recurrence jobs with projection commits.
7. Implement bounded restart-safe recurrence worker and exact observation
   persistence.
8. Implement capability-qualified coverage and multi-high-water snapshot
   repositories.
9. Add readmodel list/detail/observation methods and endpoint-specific cursors.
10. Add Local HTTP list/detail/observation routes and real-store integration
    tests.
11. Add “After attempts” browser section, history-only detail, lazy observation
    pages, truthful states,
    evidence qualifiers, accessibility, and pagination.
12. Rename the browser's existing issue recurrence filter to Session spread.
13. Update README, Local requirements, read API, MCP boundary, launch QA, and
    release-surface validation.
14. Run unit, race, vet, migration, retention, restart, concurrency, privacy,
    browser, and release verification.
15. Independently review storage/projection, API/snapshot, and UX/truthfulness
    before implementation and again before commit.

## 12. Appendix

Primary references:

- `docs/design/p0-01-issue-fingerprints-and-detectors.md`
- `docs/design/p0-02-attention-inbox-and-matching-sessions.md`
- `docs/design/p0-03-fix-recording.md`
- `docs/contracts/read-api-v1.md`
- `docs/contracts/mcp-v1.md`
- `docs/launch/local-v0-requirements.md`
- `internal/canonical/model/issue.go`
- `internal/canonical/model/fix.go`
- `internal/storage/local/projection.go`
- `internal/storage/local/fix_repository.go`
- `internal/storage/local/retention.go`
- `internal/presentation/readmodel/service.go`
- `internal/presentation/localhttp/server.go`
