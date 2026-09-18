# P0-08: Developer Brief and Deterministic Session Diagnosis

- **Status:** Implementation-ready after self-review
- **Date:** 2026-09-09
- **Target:** Belay Local individual developer alpha
- **Depends on:** P0-02, P0-06, and P0-07
- **Storage migration:** None
- **MCP change:** None; exactly nine existing read-only tools remain

## 1. Overview

Belay Local now has trustworthy session summaries, reviewed issue explanations,
evidence gaps, exact supporting sessions, and Attention families. The remaining
launch problem is orientation: a developer opening Belay still has to visit
several views and decide what matters.

P0-08 adds two deterministic presentation features:

1. a default **Developer Brief** covering the most recent 24 hours; and
2. an additive **Session Diagnosis** inside existing session detail.

The Developer Brief answers:

1. What happened recently?
2. What deserves action or review?
3. Why is each item shown?
4. What retained evidence supports it?
5. What should the developer inspect next?

Session Diagnosis answers the same questions for one selected session using the
existing metadata-only overview and session-scoped deterministic issues.

Neither feature uses an LLM, generates task names, infers root cause, infers
whether work is currently running, or claims that a fix succeeded. Both are
bounded readmodel compositions over existing repositories. They add no
canonical fields, storage tables, projector, retention responsibility, or
background worker.

## 2. Glossary

- **Developer Brief:** A fixed, rolling 24-hour Local summary containing
  actionable cards, recent-work cards, and explicit coverage limitations.
- **Action card:** A reviewed deterministic finding, evidence gap, or explicit
  failed/interrupted session outcome with a supported next-step target.
- **Recent-work card:** A compact session card derived only from session summary
  metadata.
- **Session Diagnosis:** A bounded deterministic explanation of what deserves
  review in one session.
- **Reviewed finding:** A stable issue or admitted Attention family with fixed
  Belay-owned catalog text and a supported next evidence action.
- **Usable source:** A repository read that completed successfully and returned
  structurally valid data, including a valid empty result.
- **Limited result:** A truthful result where a source failed, candidate bounds
  were reached, or issue analysis is incomplete.

## 3. Requirements

### 3.1 Functional requirements

1. Belay Local MUST open on the Developer Brief by default.
2. The brief MUST use a fixed rolling 24-hour window ending at the readmodel
   clock's `generated_at`.
3. The brief MUST contain at most five ranked action cards and eight recent-work
   cards.
4. Action cards MUST be derived only from:
   - stable reviewed Attention families;
   - stable reviewed evidence gaps;
   - explicit session outcomes `failed` or `interrupted`.
5. Unknown catalog entries, experimental issues, non-current issue analysis,
   and findings without a useful supported next action MUST NOT become default
   action cards.
6. Session outcome cards MUST be used only after reviewed finding and
   evidence-gap candidates have been considered.
7. Recent-work cards MUST use session summary metadata only. They MUST NOT load
   every session overview or timeline.
8. Every action card MUST contain a fixed observation, limitation, evidence
   summary, and one supported next step.
9. Every next step MUST navigate into an existing Attention family, exact issue,
   or Session surface.
10. Local HTTP `GET /v1/sessions/{id}` MUST add a deterministic diagnosis
    without changing the existing shared readmodel session summary, overview,
    or MCP output.
11. Session Diagnosis MUST use the existing overview and a bounded
    session-scoped stable issue query when issue capability is available.
12. A failure in issue enrichment MUST NOT make core session detail fail.
13. Empty results MUST distinguish:
    - no reviewed action card in the evaluated data;
    - incomplete analysis;
    - candidate truncation;
    - unavailable source data;
    - insufficient retained detail.
14. The existing Attention, Sessions, timeline, exact evidence, fix, monitoring,
    and MCP contracts MUST remain reachable and unchanged except for the
    additive session-detail field.

### 3.2 Truthfulness requirements

1. The brief MUST NOT claim that a session is currently active, running, idle,
   stalled, stopped, or complete.
2. It MUST NOT infer task intent, semantic task names, root cause, developer
   intent, correctness, safety, or remediation success.
3. A reported `succeeded`, `failed`, or `interrupted` outcome MUST always be
   attributed to the agent/source report.
4. `incomplete` and `unknown` outcomes MAY appear in recent-work counts and
   cards but MUST NOT become action cards by themselves.
5. An empty action list means only that no admitted action card was found in the
   bounded evaluated data.
6. A family card describes a reviewed catalog grouping, not exact recurrence,
   one project, one cause, or semantic similarity.
7. Issue and session sources are independent snapshot domains. The response
   MUST NOT claim cross-source atomicity.
8. Current-retention evidence and frozen issue snapshots MUST remain
   distinguishable when the developer drills into existing evidence routes.

### 3.3 Privacy and security requirements

1. No prompt, completion, reasoning, file content, diff, stdout, stderr,
   environment value, URL query, secret, raw command, command argument, or raw
   upstream prose may be added.
2. Developer Brief MUST NOT hydrate canonical events, family members, issue
   occurrences, or timelines.
3. Session Diagnosis MUST NOT hydrate issue occurrences or cited events.
4. Brief and diagnosis prose MUST come only from fixed Belay catalogs and fixed
   outcome/count templates.
5. Harness names and opaque IDs remain untrusted display data and MUST render
   through text nodes.
6. Technical fields such as fingerprint, scope quality, detector ID, mapping
   key, source signal code, projection generation, and raw origin MUST NOT
   appear in default Brief cards.
7. Existing loopback-only access, launch bearer authentication, no-store
   responses, CSP, referrer policy, and framing protections remain mandatory.
8. The new endpoint is read-only. No action token, mutation capability, hook
   change, or MCP write surface is added.

### 3.4 Compatibility requirements

1. Keep `schema_version=belay.read.v1`.
2. Add projection versions:
   - `belay.developer-brief.v1`;
   - `belay.session-diagnosis.v1`.
3. Existing Local HTTP `GET /v1/sessions/{id}` clients may ignore the new
   top-level `diagnosis` field.
4. Existing issue, family, session, and event cursors are unchanged.
5. The Developer Brief is not cursor-paginated.
6. MCP remains exactly nine read-only tools. P0-08 does not add Brief or
   diagnosis tools.

## 4. Current State / Background

The current readmodel already provides:

- `ListSessionsPage` with deterministic ordering, a rolling time filter, raw
  projection outcomes, history source, and up to 100 rows;
- `GetSession` with one metadata-only `SessionOverview`;
- `ListAttentionFamilies` with reviewed family catalog text, analysis coverage,
  exact action targets, and stable/default filtering;
- `ListIssues` with session filtering, evidence-gap separation, fixed catalog
  text, and exact issue view cursors;
- existing browser navigation into Attention families, exact issue detail,
  cited evidence, and Sessions.

The storage layer already computes session overview counts and salient
resources. Reading one session overview currently scans that session's stored
canonical event rows. P0-08 reuses that existing cost only for the selected
session. It does not issue `GetSession` for each recent-work card.

P0-07 makes default family Attention a reviewed product surface:

- stable Belay-native exact issues appear as one-member families;
- only explicitly admitted upstream findings appear as mapped families;
- unknown upstream findings remain outside default family Attention;
- fixed catalog text supplies observation, caveat, and next evidence action.

These contracts are sufficient for a useful Brief without a new storage
projection.

## 5. High Level Design

```text
GET /v1/developer-brief
          |
          v
readmodel Developer Brief composer
    |          |             |
    |          |             +--> recent sessions, max 100 candidates
    |          +----------------> stable evidence gaps, max 20 candidates
    +---------------------------> stable Attention families, max 20 candidates
          |
          v
fixed admission + deterministic ranking
          |
          +--> max 5 action cards
          +--> max 8 recent-work cards
          +--> explicit source/analysis limitations

GET /v1/sessions/{id}
          |
          +--> existing session summary + overview
          |
          +--> optional stable session-scoped issue query, max 20
                         |
                         v
              deterministic Session Diagnosis
```

### 5.1 Architectural decision: query-time composition

P0-08 MUST remain a readmodel/HTTP/browser feature.

It does not persist a Brief because:

- all inputs already have authoritative repositories;
- a 24-hour rolling view changes continuously;
- persistence would require a new migration, invalidation policy, retention
  behavior, and background reconciliation;
- the product value does not require historical Brief snapshots.

The composer calls existing readmodel methods and treats each result as an
independent source. It does not attempt to synthesize one storage generation
across the event-ingestion and issue-projection domains.

Each action target retains the opaque source view cursor needed by the existing
drill-down flow. If that cursor expires, the browser refreshes the Brief and
requires the user to reselect the card.

### 5.2 Developer Brief request contract

Add:

```text
GET /v1/developer-brief
```

P0 accepts no query parameters. Any unknown, repeated, or non-empty query
parameter returns the standard `400` invalid-request problem.

The window is:

```text
window_end   = readmodel clock in UTC
window_start = window_end - 24 hours
```

Session overlap uses the existing `occurred_after`/`occurred_before` semantics.
Issue-family and evidence-gap admission requires `last_observed_at` within the
window. Timestamps after `window_end` are excluded from Brief cards rather than
presented as future activity.

### 5.3 Developer Brief response

Exact logical shape:

```json
{
  "schema_version": "belay.read.v1",
  "projection_version": "belay.developer-brief.v1",
  "generated_at": "2026-09-09T17:00:00Z",
  "window": {
    "started_at": "2026-09-08T17:00:00Z",
    "ended_at": "2026-09-09T17:00:00Z",
    "duration_hours": 24
  },
  "status": "ready",
  "recent_summary": {
    "evaluated_session_count": 12,
    "session_count_is_lower_bound": false,
    "harnesses": ["claude-code", "codex"],
    "outcomes": {
      "succeeded": 4,
      "failed": 2,
      "interrupted": 1,
      "incomplete": 4,
      "unknown": 1
    },
    "history": {
      "live": 8,
      "historical": 3,
      "mixed": 1
    },
    "latest_observed_at": "2026-09-09T16:55:00Z"
  },
  "action_cards": [{
    "card_id": "family:atf_...",
    "kind": "reviewed_finding",
    "title": "Command failed",
    "observation": "The agent reported that a command failed.",
    "limitation": "This does not identify why the command failed or whether a later attempt succeeded.",
    "next_step": {
      "kind": "open_attention_family",
      "label": "Review cited evidence",
      "family_id": "atf_...",
      "issue_id": "iss_...",
      "session_id": null,
      "view_cursor": "opaque"
    },
    "evidence": {
      "last_observed_at": "2026-09-09T16:40:00Z",
      "session_count": 2,
      "occurrence_count": 3,
      "harnesses": ["claude-code", "codex"],
      "session_outcome": null,
      "event_count": null
    }
  }],
  "recent_work": [{
    "session_id": "ses_...",
    "harness": "codex",
    "started_at": "2026-09-09T16:00:00Z",
    "ended_at": "2026-09-09T16:20:00Z",
    "event_count": 42,
    "outcome": "failed",
    "history": "live",
    "outcome_explanation": "The agent reported that this session ended with a failure.",
    "next_step": {
      "kind": "open_session",
      "label": "Review session",
      "family_id": null,
      "issue_id": null,
      "session_id": "ses_...",
      "view_cursor": null
    }
  }],
  "coverage": {
    "complete": true,
    "sources": [{
      "source": "sessions",
      "status": "complete",
      "evaluated_count": 12,
      "has_more": false,
      "as_of": "2026-09-09T16:55:00Z"
    }, {
      "source": "reviewed_findings",
      "status": "complete",
      "evaluated_count": 4,
      "has_more": false,
      "as_of": "2026-09-09T16:50:00Z"
    }, {
      "source": "evidence_gaps",
      "status": "complete",
      "evaluated_count": 1,
      "has_more": false,
      "as_of": "2026-09-09T16:50:00Z"
    }],
    "issue_analysis": {
      "current_sessions": 12,
      "pending_sessions": 0,
      "failed_sessions": 0,
      "truncated_sessions": 0,
      "unscoped_sessions": 2,
      "analysis_through": "2026-09-09T16:50:00Z",
      "complete": true
    },
    "limitations": []
  }
}
```

DTO rules:

- `action_cards`, `recent_work`, `harnesses`, `sources`, and `limitations` are
  always non-null arrays.
- On an action card, `title` names what deserves review, `observation` is the
  fixed reason the card is shown, `evidence` states the bounded support, and
  `limitation` prevents a stronger interpretation.
- Action-card `kind` is
  `reviewed_finding|evidence_gap|session_outcome|failed_activity`.
- Next-step `kind` is
  `open_attention_family|open_issue|open_session`.
- `next_step` uses required nullable identifiers so clients do not infer target
  shape from missing fields.
- `status` is `ready|limited`.
- Source status is `complete|truncated|unavailable`.
- `coverage.complete=true` only when every source is complete, no candidate
  source has more rows, and issue analysis is complete.
- `status=limited` whenever `coverage.complete=false`.
- `as_of` is the source's truthful existing freshness value:
  session `data_through` or issue `analysis_through`. It is nullable when the
  source did not supply one.
- The endpoint returns `503 belay.local/developer-brief-unavailable` only when
  all three source reads are unavailable. Any usable source produces `200` with
  `status=limited` when necessary.

Fixed limitation codes and messages:

| Code | Fixed message |
|---|---|
| `sessions_unavailable` | Recent sessions could not be loaded. |
| `reviewed_findings_unavailable` | Reviewed findings could not be loaded. |
| `evidence_gaps_unavailable` | Verification evidence gaps could not be loaded. |
| `session_candidate_limit_reached` | More recent sessions exist than this brief evaluated. |
| `finding_candidate_limit_reached` | More reviewed findings exist than this brief evaluated. |
| `evidence_gap_candidate_limit_reached` | More evidence gaps exist than this brief evaluated. |
| `issue_analysis_incomplete` | Some stored sessions are still pending, failed, or only partially analyzed. |

Each limitation is:

```json
{"code": "issue_analysis_incomplete", "message": "fixed text"}
```

### 5.4 Source reads and hard bounds

The composer launches at most three independent reads under one child context:

1. recent sessions:
   - `ListSessionsPage`;
   - `Limit=100`;
   - `OccurredAfter=window_start`;
   - `OccurredBefore=window_end`;
2. reviewed findings:
   - `ListAttentionFamilies`;
   - `Limit=20`;
   - `ObservedAfter=window_start`;
   - `AttentionKind=issue`;
   - `Experimental=stable`;
3. evidence gaps:
   - `ListIssues`;
   - `Limit=20`;
   - `ObservedAfter=window_start`;
   - `AttentionKind=evidence_gap`;
   - `Experimental=stable`.

The implementation uses a two-second child deadline. A source timeout is a
source-unavailable partial result, not a Local startup or ingestion failure.
All goroutines use buffered result channels or an equivalent wait structure so
deadline/error paths cannot leak.

No source continuation is followed. `has_more=true` is represented as
truncation rather than silently draining pages.

### 5.5 Action-card admission

Reviewed family admission requires all of:

- `experimental=false`;
- `analysis_status=current`;
- `last_observed_at` inside the Brief window;
- non-empty fixed catalog title, observation, caveat, and supported next action;
- mapped family, or an exact family backed by a known Belay issue catalog;
- valid family/issue target identity and view cursor;
- at least one supporting session and occurrence.

Evidence-gap admission requires all of:

- `experimental=false`;
- `analysis_status=current`;
- `last_observed_at` inside the Brief window;
- `catalog_status=known`;
- fixed catalog title, observation, caveat, and supported next action;
- valid issue target identity and shared issue view cursor.

Session-outcome admission requires:

- outcome exactly `failed` or `interrupted`;
- session interval overlaps the Brief window;
- non-empty session ID and harness;
- fixed outcome explanation.

Suppress:

- unknown issue catalogs;
- unsupported imported findings;
- experimental findings;
- pending, failed, or truncated issue analysis;
- issue/family rows lacking a useful action;
- `succeeded`, `incomplete`, or `unknown` outcomes as action cards;
- generic findings counts with no reviewed explanation;
- generic permission-event counts, because the current overview does not
  distinguish requests, approvals, and denials reliably enough for diagnosis;
- any candidate requiring event hydration to explain itself.

### 5.6 Deterministic ranking and tie-breaking

The response does not expose a numerical score. Implementation uses this fixed
rank bucket:

| Rank | Candidate |
|---:|---|
| 700 | critical reviewed finding |
| 650 | high reviewed finding |
| 600 | failed session outcome |
| 500 | medium reviewed finding |
| 450 | interrupted session outcome |
| 400 | low reviewed finding |
| 300 | reviewed evidence gap |
| 200 | info reviewed finding |

The composer merges all admitted candidates and applies the table exactly.
At most two `session_outcome` cards may appear in the five-card result. After
two outcome cards are selected, further outcome candidates are skipped and the
next ranked reviewed finding or evidence gap is selected. This keeps explicit
failed/interrupted outcomes useful without allowing a busy day of failed
sessions to crowd out reviewed deterministic evidence.

Within the same rank:

1. `session_count DESC`;
2. `occurrence_count DESC`;
3. `last_observed_at DESC`;
4. kind order `reviewed_finding`, `evidence_gap`, `session_outcome`;
5. stable target identity ascending.

For session outcomes, `session_count=1`, `occurrence_count=1`, and
`last_observed_at=ended_at`.

Deduplication is exact only:

- one card per `family_id`;
- one card per evidence-gap `issue_id`;
- one outcome card per `session_id`.

P0 does not attempt semantic deduplication between a failed session and a
finding observed in that session. Outcome cards are fallback candidates, which
limits but does not falsely claim to eliminate overlap.

### 5.7 Recent summary and recent-work cards

The recent summary is computed only from the returned session candidate page.

- `evaluated_session_count` is the number evaluated.
- `session_count_is_lower_bound=true` exactly when `has_more=true`.
- Harnesses are normalized for empty values, distinct, and sorted.
- Outcome/history counts cover only evaluated sessions.
- `latest_observed_at` is the maximum returned `ended_at`, or `null`.

Recent-work cards use the first eight sessions in existing session order:

```text
ended_at DESC, session_id ASC
```

They include no issue/finding count, resource, command, or event detail beyond
the existing event count. Fixed outcome explanations are:

- `succeeded`: The agent reported that this session completed successfully.
- `failed`: The agent reported that this session ended with a failure.
- `interrupted`: The agent reported that this session was interrupted.
- `incomplete`: The agent did not report how this session ended.
- `unknown`: The agent reported that the session ended but did not report an
  outcome.

These templates describe source reporting only; they do not describe task
success beyond the explicit source outcome.

### 5.8 Session Diagnosis contract

Local HTTP `GET /v1/sessions/{id}` retains its existing response and adds
top-level `diagnosis` through an HTTP-only presentation wrapper:

```json
{
  "schema_version": "belay.read.v1",
  "data": {
    "session_id": "ses_...",
    "harness": "codex",
    "overview": {}
  },
  "diagnosis": {
    "projection_version": "belay.session-diagnosis.v1",
    "status": "ready",
    "summary": {
      "state": "reviewed_findings_available",
      "title": "This session has findings worth reviewing",
      "detail": "Belay found reviewed deterministic evidence linked to this session."
    },
    "action_cards": [],
    "coverage": {
      "issue_source": "complete",
      "evaluated_issue_count": 2,
      "issue_candidates_truncated": false,
      "analysis": {},
      "limitations": []
    }
  },
  "data_through": "2026-09-09T16:55:00Z"
}
```

Diagnosis status is `ready|limited|insufficient_detail`.

Summary state is one of:

- `reviewed_findings_available`;
- `reported_failure`;
- `reported_interruption`;
- `failed_activity_observed`;
- `no_reviewed_action_available`;
- `insufficient_detail`.

Summary text is fixed:

| State | Title | Detail |
|---|---|---|
| `reviewed_findings_available` | This session has findings worth reviewing | Belay found reviewed deterministic evidence linked to this session. |
| `reported_failure` | The agent reported that this session failed | Review the session activity and any cited findings for recorded failure evidence. |
| `reported_interruption` | The agent reported that this session was interrupted | Review the last recorded activity to understand where the record ended. |
| `failed_activity_observed` | Failed activity was recorded | One or more stored events explicitly reported failure. |
| `no_reviewed_action_available` | No reviewed action was identified | Belay did not find a reviewed actionable item in the bounded analysis available for this session. |
| `insufficient_detail` | Belay has limited detail for this session | Available metadata is not sufficient to provide a useful deterministic diagnosis. |

`no_reviewed_action_available` MUST be used only when the issue read succeeded,
was not truncated, and its analysis coverage is complete. Otherwise the state
is `insufficient_detail` or another positive observed state.

Session Diagnosis uses at most:

- the already-loaded shared `SessionDetail` returned by the existing core
  readmodel method;
- one `ListIssues` call with:
  - `Limit=20`;
  - exact `SessionID`;
  - `AttentionKind=all`;
  - `Experimental=stable`.

It does not call `GetIssue`, family detail, event lookup, timeline, findings, or
fix/monitoring repositories.

### 5.9 Session Diagnosis card rules

Use the same action-card DTO and fixed catalogs as the Brief.

Admit current, stable, known-catalog session-scoped issues. Issue summaries are
evidence that the exact issue has a visible occurrence in the selected session;
cross-session summary counts MUST NOT be presented as session-local counts.

Session Diagnosis cards therefore use:

- `issue_id`;
- fixed title, observation, limitation, and next evidence action;
- `last_observed_at`;
- the selected session ID as the navigation context;
- the rowless issue view cursor returned by the session-scoped issue list.

They omit family/session/occurrence aggregate counts.

Ranking:

1. current known issues by severity descending;
2. issue before evidence gap at the same severity;
3. `last_observed_at DESC`;
4. `issue_id ASC`;
5. explicit failed/interrupted session outcome;
6. generic `explicit_failed_events` fallback.

At most five cards are returned.

Deduplication:

- if `issue.explicit_command_failure` is admitted, suppress the generic
  `explicit_failed_events` fallback;
- if a reviewed issue exists, retain an explicit failed/interrupted session
  outcome only when a card slot remains;
- evidence gaps remain separate from issue claims.

The generic failed-activity card is allowed only when:

- `overview.counts.explicit_failed_events > 0`;
- no reviewed explicit-failure issue was admitted; and
- a card slot remains.

Its target is the selected Session timeline. It does not claim which command,
tool, or action failed.

### 5.10 Session Diagnosis failure isolation

Error precedence:

1. core shared `GetSession` not found/error retains existing HTTP and MCP
   behavior;
2. only after core session detail succeeds does diagnosis enrichment run;
3. issue capability unavailable, timeout, or query failure returns the session
   with `diagnosis.status=limited` or `insufficient_detail`;
4. no raw repository error enters the response.

Issue limitations:

| Code | Fixed message |
|---|---|
| `session_issue_analysis_unavailable` | Reviewed issue analysis could not be loaded for this session. |
| `session_issue_candidate_limit_reached` | More session findings exist than this diagnosis evaluated. |
| `session_issue_analysis_incomplete` | Issue analysis is incomplete for stored sessions. |
| `session_overview_unavailable` | The metadata overview is unavailable for this session. |

The current storage `GetSession` treats overview failure as core failure. P0-08
does not change that storage behavior. The `session_overview_unavailable`
limitation is reserved for test adapters or a future optional-overview split
and MUST NOT mask a current core read failure.

### 5.11 Browser hierarchy

Add three primary destinations in this order:

1. **Brief** — default;
2. **Attention**;
3. **Sessions**.

Developer Brief hierarchy:

1. heading: `Your last 24 hours`;
2. compact summary: evaluated sessions, agents, explicit reported outcomes;
3. `Needs review` action cards;
4. `Recent work` session cards;
5. collapsed `Coverage and limitations`;
6. links to full Attention and Sessions.

Action cards display:

1. fixed title;
2. fixed observation;
3. evidence summary;
4. fixed limitation;
5. one action button.

They MUST NOT display raw severity as the only reason for priority. Severity may
remain a badge.

Recent-work cards display:

- agent;
- observed interval;
- event count;
- live/imported/mixed source;
- source-qualified outcome;
- `Review session`.

Session detail hierarchy becomes:

1. source-qualified outcome;
2. `What deserves review` diagnosis;
3. existing recorded-work overview;
4. existing findings/resources;
5. optional full timeline.

Navigation:

- `open_attention_family` switches to Attention and opens the supplied family
  or exact representative using its view cursor;
- `open_issue` switches to Attention and opens exact issue detail using its
  view cursor;
- `open_session` uses existing direct session opening;
- 410 clears the stale Brief selection, refreshes the Brief, and requires
  reselection;
- partial Brief failure never clears or disables existing Attention/Sessions;
- back/focus restoration returns to the originating Brief or diagnosis card.

Accessibility:

- action and recent-work cards are one focusable control plus, if needed, one
  separate clearly labeled action; no nested interactive elements;
- headings and status changes use semantic structure and polite live regions;
- status is not color-only;
- mobile uses one vertical scroll;
- card order in the DOM matches visual/ranking order;
- empty/limited copy is visible to screen readers.

### 5.12 Empty, partial, and failure states

Developer Brief:

- no sessions, complete sources:
  - title: `No recorded agent activity in the last 24 hours`;
  - action list empty;
  - link to Sessions remains available;
- sessions but no action cards, complete analysis:
  - title: `No reviewed action was identified in the evaluated activity`;
  - limitation: `This is not a claim that all activity was successful or
    problem-free.`;
- any source unavailable or truncated:
  - retain usable cards;
  - show `Brief is limited`;
  - list fixed limitations;
- all sources unavailable:
  - HTTP 503;
  - browser shows a scoped Brief error;
  - Attention and Sessions navigation remain usable.

Session Diagnosis:

- positive deterministic cards: render cards and any coverage limitation;
- no cards with complete issue analysis: `no_reviewed_action_available`;
- no cards with incomplete/unavailable issue analysis and no useful overview
  signal: `insufficient_detail`;
- issue failure with explicit failed outcome: retain the failed-outcome card
  and mark diagnosis `limited`;
- issue failure never becomes `no_reviewed_action_available`.

## 6. Low Level Design

### 6.1 Readmodel

Add:

- `internal/presentation/readmodel/developer_brief.go`;
- `internal/presentation/readmodel/developer_brief_test.go`;
- `internal/presentation/readmodel/session_diagnosis.go`;
- `internal/presentation/readmodel/session_diagnosis_test.go`.

Minimal existing-file changes:

- keep shared `SessionDetail` and `GetSession` unchanged because the existing
  MCP `get_session` tool uses that exact type;
- add a readmodel `DiagnoseSession(ctx, SessionDetail)` method that consumes the
  already-loaded core detail and performs only bounded optional enrichment;
- add an HTTP-only `SessionDetailWithDiagnosis` presentation DTO that embeds or
  copies the existing four HTTP fields and adds top-level `diagnosis`;
- let family presentation carry a non-JSON internal reviewed-catalog status,
  computed while the raw representative issue is available, so the Brief can
  suppress an unknown exact-family catalog without adding fields to the
  existing family HTTP response;
- reuse `issueCatalog`, Attention family presentation, bounded limit helpers,
  and the injected clock;
- do not add a new repository interface unless implementation proves an
  existing service method cannot expose a required field.

Implementation should prefer small pure helpers:

- `briefWindow(now)`;
- `briefActionCandidates(...)`;
- `rankBriefActions(...)`;
- `recentSummary(...)`;
- `recentWorkCards(...)`;
- `diagnoseSession(...)`;
- `fixedOutcomeExplanation(...)`;
- `briefCoverage(...)`.

### 6.2 HTTP

Add a GET-only handler, preferably in:

- `internal/presentation/localhttp/developer_brief.go`;
- `internal/presentation/localhttp/developer_brief_test.go`.

Register:

```go
mux.Handle(
    "GET /v1/developer-brief",
    s.authorize(http.HandlerFunc(s.getDeveloperBrief)),
)
```

The handler:

- rejects every query parameter;
- calls only the readmodel service;
- maps all-source failure to fixed 503 problem type
  `belay.local/developer-brief-unavailable`;
- otherwise uses the existing JSON writer;
- inherits loopback/auth/security headers.

The existing Local HTTP session handler changes only as follows:

1. call shared `GetSession`;
2. preserve its error precedence;
3. call `DiagnoseSession` with the successful result;
4. write `SessionDetailWithDiagnosis`.

The shared `GetSession` result and existing MCP serialization remain unchanged.

### 6.3 Browser

Modify existing embedded assets during implementation:

- add Brief navigation and view in `index.html`;
- add Brief state/load/render/navigation logic in `app.js`;
- add responsive styles in `styles.css`;
- add focused asset-contract and server integration tests.

Do not duplicate catalog prose in browser constants. The response's fixed
readmodel text is authoritative. Browser fallback is limited to generic
payload-free loading/error copy.

### 6.4 Storage and canonical model

No storage or canonical changes are planned.

Do not:

- add a migration;
- add a Brief table;
- add a diagnosis projector;
- add event indexes speculatively;
- change issue/family identity;
- change retention or mutation guards.

If implementation discovers that the existing family query exceeds its already
accepted P0-07 bound, address that as a measured P0-07 query fix, not as new
P0-08 persisted state.

### 6.5 Contracts and MCP

Implementation updates:

- `docs/contracts/read-api-v1.md`;
- relevant launch/manual-QA documentation.

MCP contract and code remain unchanged:

- exactly nine tools;
- no Developer Brief tool;
- no Session Diagnosis tool or field until a separately reviewed additive MCP
  design;
- shared `readmodel.SessionDetail` remains byte-shape compatible with the
  existing MCP `get_session` output;
- no write capability.

## 7. Performance and Resource Bounds

Developer Brief:

- at most three source reads;
- session candidates: 100;
- family candidates: 20;
- evidence-gap candidates: 20;
- returned action cards: 5;
- returned recent-work cards: 8;
- no continuation requests;
- no event decoding, timeline reads, occurrence hydration, or family-member
  hydration;
- two-second child deadline;
- encoded response target below 64 KiB, enforced by a focused size test.

Session detail:

- retains the existing one-session overview cost;
- adds one issue-summary query with limit 20;
- performs no N+1 detail/evidence reads;
- issue enrichment uses a one-second child deadline;
- issue timeout cannot hold the session detail open beyond that bound.

Concurrency:

- Brief source reads may execute concurrently;
- every result channel is buffered or joined;
- context cancellation is observed;
- no goroutine continues after the request returns;
- no mutable package-level cache is added.

## 8. Monitoring

No telemetry or network reporting is added.

Optional existing Local diagnostics may record only fixed aggregate codes and
durations:

- Brief total duration bucket;
- source success/unavailable code;
- result status `ready|limited`;
- diagnosis status code.

Do not log IDs, harness names, titles, source codes, paths, cursor contents, or
repository error payloads.

## 9. Open Questions

None block P0 implementation.

Custom time windows, persisted Brief history, dismissal state, Brief MCP tools,
semantic task summaries, and capture-health/process-state claims are explicitly
deferred.

## 10. Acceptance Criteria

### Developer Brief

1. A fresh request covers exactly the preceding 24 hours from the injected
   clock.
2. It returns at most five action cards and eight recent-work cards.
3. Known current reviewed findings appear with fixed title, observation,
   limitation, evidence summary, and valid Attention target.
4. Evidence gaps remain distinguishable from findings.
5. Unknown imported, unknown catalog, experimental, and non-current issues do
   not become action cards.
6. Failed/interrupted session outcomes fill unused action slots without
   displacing higher-ranked reviewed findings.
7. Ranking and every tie-break are deterministic across repeated calls.
8. `has_more` becomes a visible limitation and lower-bound count; pages are not
   silently drained.
9. One source failure returns a useful `limited` response.
10. All source failures return fixed 503 without internal error text.
11. No Brief path loads session overview, timeline, family members,
    occurrences, or cited events.
12. No prohibited content or raw upstream prose appears in encoded output.

### Session Diagnosis

1. Existing Local HTTP session detail fields remain unchanged and diagnosis is
   additive through an HTTP-only wrapper.
2. Known current session-scoped issues become bounded diagnosis cards.
3. Cross-session issue summary counts are not presented as session-local
   counts.
4. Explicit failed/interrupted outcomes are attributed to the agent.
5. Generic failed activity appears only when no reviewed explicit-failure issue
   was admitted.
6. Issue capability failure still returns Local HTTP core session detail with a
   truthful limited diagnosis.
7. No issue results plus incomplete/unavailable analysis never produce the
   complete no-action state.
8. Diagnosis performs no occurrence or event hydration.

### Browser and compatibility

1. Brief is the default view and Attention/Sessions remain one action away.
2. Every Brief/diagnosis action reaches the intended existing view.
3. Expired target cursors refresh Brief state and require reselection.
4. Partial/failure states preserve other Local views and connected content.
5. Keyboard, focus restoration, live-region, narrow-screen, and safe text-node
   tests pass.
6. Existing issue/family/session routes and exactly nine MCP tools are
   unchanged.

## 11. Focused Test Plan

Readmodel:

- 24-hour lower/upper boundary and injected clock;
- exact source request filters and limits;
- ranking table and all tie-breaks;
- max-five/max-eight bounds;
- unknown/experimental/stale/actionless suppression;
- reviewed family and evidence-gap targets/cursors;
- failed/interrupted fallback ordering;
- exact deduplication;
- source truncation and lower-bound counts;
- one-source, two-source, and all-source failures;
- incomplete issue analysis;
- deterministic output under reordered input fixtures;
- encoded response below 64 KiB;
- prohibited-byte and untrusted-string canaries;
- session diagnosis state matrix, issue timeout, and fallback deduplication.

HTTP:

- auth and loopback inheritance;
- no-query strictness, including repeated and unknown parameters;
- 200 ready, 200 limited, and fixed 503;
- additive session-detail JSON compatibility;
- payload-free errors.

Browser:

- Brief is initial view;
- hierarchy and action order;
- exact navigation for family, issue, and session targets;
- expired cursor clear-before-refresh ordering;
- partial source copy;
- empty-state truthfulness;
- diagnosis rendering and no-action/insufficient-detail distinction;
- `textContent` rendering for all dynamic values;
- focus restoration, live regions, DOM order, and mobile single-scroll layout.

Compatibility:

- existing HTTP session response consumers ignoring `diagnosis`;
- unchanged shared `SessionDetail` and MCP `get_session` output;
- Attention and exact issue cursor behavior unchanged;
- existing fix/monitoring actions unchanged;
- exactly nine unchanged MCP tools.

## 12. Implementation Sequence

1. Freeze DTOs, enums, limitation codes, HTTP-only session wrapper, and fixed
   copy in readmodel tests.
2. Implement pure ranking, summary, coverage, and diagnosis helpers.
3. Implement bounded concurrent Brief composition.
4. Add failure-isolated diagnosis to the Local HTTP session-detail wrapper.
5. Add strict HTTP route and integration tests.
6. Add Brief browser view and session diagnosis panel.
7. Update read API and manual QA documentation.
8. Run focused readmodel/HTTP/browser tests, then full verification if the
   integrated branch permits.
9. Perform independent privacy/truthfulness and browser-contract review.
10. Founder QA using recent mixed Codex and Claude Code sessions.

## 13. Self-Review

### Product value

The initial concept risked becoming another dashboard of counters. The final
design makes action cards the primary output, requires every card to explain
why it exists and where to inspect next, and relegates counts to orientation.
Recent-work cards provide cross-agent context without pretending to summarize
task semantics.

### Truthfulness

The design originally risked treating a bounded page as a complete 24-hour
total and treating adjacent issue/session reads as one snapshot. The final
contract uses evaluated counts, lower-bound flags, per-source status, explicit
limitations, and no cross-source atomicity claim. Non-current issue analysis is
excluded from action cards rather than over-qualified in primary value.

### Privacy

The design initially considered hydrating event evidence into the Brief. That
was removed. The Brief reads no events, occurrences, family members, resources,
commands, or findings prose. Session Diagnosis reuses only existing overview
counts and fixed issue catalogs. Drill-down remains explicit through existing
bounded routes.

### Feasibility

The design uses existing service methods and production capability injection.
It needs no migration, canonical change, projector, lifecycle integration, or
new cursor format. Session Diagnosis adds one bounded issue query only in the
Local HTTP detail path and fails open around that enrichment. The shared
session type used by MCP remains unchanged.

### Overengineering

Deferred from P0:

- custom windows;
- Brief pagination;
- persisted Brief snapshots;
- dismissal/read state;
- semantic deduplication;
- generated narrative;
- new detectors;
- MCP changes;
- capture-health/process-state claims.

The remaining implementation is a small additive readmodel composition and UI
surface with explicit hard bounds.

## 14. Appendix

Related contracts and designs:

- `docs/design/p0-02-attention-inbox-and-matching-sessions.md`
- `docs/design/p0-06-value-first-local-ux.md`
- `docs/design/p0-07-attention-signal-families.md`
- `docs/contracts/read-api-v1.md`
- `docs/launch/individual-alpha-value-roadmap.md`
