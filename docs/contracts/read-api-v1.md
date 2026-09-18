# Belay Read API V1 Contract

- **Status:** Implemented Local Alpha surface, including Attention,
  P0-03 browser-only fix-attempt actions, P0-04 recurrence-monitoring reads,
  the P0-05 shared issue-evidence readmodel, P0-07 signal-family reads, and
  P0-08 Developer Brief/session diagnosis reads
- **Local base:** loopback-only, implementation-defined port
- **Future Teams base:** `/v1`

## Principle

Belay has one intended read contract with Local and future Teams adapters. The
Local Alpha implements only the routes explicitly listed below. Other resources
must not be represented as available Local Alpha or Teams functionality.

The snapshot-queryable issue repository is shared by the loopback HTTP
Attention Inbox and the read-only MCP issue-evidence tools. HTTP remains the
only adapter with the legacy browser fix-attempt/retraction routes and
recurrence-monitoring capabilities. MCP's newer additive tools store only a
bounded fix proposal or approved-application record.

P0-03 adds four Local-only routes for explicit browser fix-attempt declarations.
They are not future Teams read-contract claims and are separate from MCP's
bounded proposal/application records.

P0-04 adds three Local-only, read-only monitoring routes over exact compatible
fingerprint observations after a recorded attempt. They do not claim semantic
similarity, resolution, prevention, or fix success and are not exposed to MCP.

## Implemented Local Alpha routes

| Route | Local Alpha behavior |
|---|---|
| `GET /healthz` | loopback health response |
| `GET /v1/developer-brief` | fixed, bounded 24-hour composition over sessions and reviewed Attention |
| `GET /v1/user-insights` | bounded Habits session list with any stored harness-written debriefs |
| `GET /v1/user-insights/{session_key}/debrief` | one stored debrief; with `generate=1` runs the user's own installed harness first |
| `GET /v1/sessions` | filtered, cursor-paginated local sessions |
| `GET /v1/sessions/{id}` | one local session, metadata-only overview, and additive deterministic diagnosis |
| `GET /v1/sessions/{id}/events` | cursor-paginated local timeline |
| `GET /v1/sessions/{id}/events/lookup` | bounded exact cited-event lookup within one session |
| `GET /v1/activity` | filtered, cursor-paginated canonical activity |
| `GET /v1/findings` | filtered, cursor-paginated local findings |
| `GET /v1/attention-families` | query-time catalog families over one frozen exact-issue snapshot |
| `GET /v1/attention-families/{family_id}` | bounded same-snapshot exact members of one mapped family |
| `GET /v1/issues` | filtered, cursor-paginated deterministic issue summaries |
| `GET /v1/issues/{id}/occurrences` | issue detail and cursor-paginated exact matching sessions |
| `GET /v1/issues/{id}/fix-eligibility` | snapshot-bound eligibility and signed browser action token |
| `GET /v1/issues/{id}/fixes` | durable, cursor-paginated fix-attempt history |
| `POST /v1/issues/{id}/fixes` | append one browser-confirmed external fix-attempt declaration |
| `POST /v1/issues/{id}/fixes/{annotation_id}/retractions` | append one fixed-reason retraction |
| `GET /v1/fix-monitoring` | grouped, cursor-paginated post-attempt monitoring |
| `GET /v1/issues/{id}/fix-monitoring` | one issue's durable attempt monitoring/history |
| `GET /v1/issues/{id}/fixes/{annotation_id}/recurrences` | exact post-baseline observations for one attempt |
| `GET /v1/stats` | global Local summary only |

## Other future candidate routes

The following are not implemented by the Local Alpha:

- `GET /v1/machines`
- `GET /v1/workflows`
- `GET /v1/workflows/{id}/stats`
- `GET /v1/alerts`
- `GET /v1/exports/{id}`
- all hosted Teams routes, workspace authorization, and exports

## Common response rules

- JSON only in V1.
- Implemented Local Alpha list routes use opaque cursor pagination with
  deterministic ordering and immutable ingestion snapshots.
- Issue routes use a separate immutable issue-projection generation snapshot.
- Bounded default and maximum page sizes.
- Explicit `schema_version`, projection/metric versions, and
  coverage/confidence where values are derived.
- `data_through` indicates projection freshness.
- Event-derived strings are untrusted display data.
- Unknown outcomes are never hidden or coerced to success.

List response:

```json
{
  "schema_version": "belay.read.v1",
  "data": [],
  "next_cursor": null,
  "has_more": false,
  "returned_count": 0,
  "limit": 20,
  "data_through": "2026-09-08T18:12:45Z"
}
```

Local session, session-event, activity, and finding lists include:

- `returned_count` is the number of rows in `data`.
- `limit` is the effective bounded request limit.
- Session lists set `filters_applied=true` to confirm that filters were applied
  by the Local read service rather than only by a client.
- `has_more=true` means at least one additional matching row exists in the
  cursor snapshot.
- `next_cursor` is a non-empty opaque string exactly when `has_more=true`;
  otherwise it is `null`.

Cursors are versioned, endpoint-specific, bound to the normalized filter set,
and carry an immutable ingestion snapshot plus the last deterministic sort key.
Clients must not inspect or modify them. Reusing a cursor with different
filters, another endpoint, malformed encoding, or an unsupported version
returns `400 application/problem+json` without reflecting the cursor.

Rows appended after the first page are excluded from that cursor chain,
including late or out-of-order events that would otherwise change a session's
sort position. A fresh request without a cursor starts a new snapshot.

Deterministic ordering:

- Sessions: `ended_at DESC, session_id ASC`
- Session events: `source.sequence ASC, occurred_at ASC, event_id ASC`
- Activity: `occurred_at DESC, source.sequence DESC, event_id DESC`
- Findings: `detected_at DESC, finding_id DESC`

## Developer Brief

`GET /v1/developer-brief` accepts no query parameters. Any supplied parameter
returns the standard `400` invalid-request problem.

The response uses `schema_version=belay.read.v1` and
`projection_version=belay.developer-brief.v1`. It covers the fixed rolling 24
hours ending at `generated_at` and evaluates at most:

- 100 recent-session candidates;
- 20 reviewed Attention-family candidates;
- 20 stable evidence-gap candidates.

It returns at most five action cards and eight recent-work cards. It never
drains continuation pages or loads session overviews, timelines, events,
family members, issue occurrences, or generated prose.

Cards are admitted only from current stable reviewed Attention families,
current stable reviewed evidence gaps, and explicit agent-reported failed or
interrupted session outcomes. Unknown catalogs, unsupported imported
findings, experimental findings, non-current analysis, and actionless rows are
excluded.

The three reads are independent and are not represented as one atomic storage
snapshot. `coverage.sources` reports each source as `complete`, `truncated`, or
`unavailable`. Evaluated session counts become explicit lower bounds when the
bounded source reports more rows.

At least one usable source returns `200` with `status=ready|limited`. If all
three reads are unavailable, Local returns the fixed
`503 belay.local/developer-brief-unavailable` problem with detail:
`Belay could not assemble the recent developer brief.`

Action targets use required nullable `family_id`, `issue_id`, `session_id`, and
`view_cursor` fields. Brief output contains no raw commands, arguments, paths,
event payloads, fingerprint material, detector identifiers, source-signal
codes, or upstream prose.

The top-level shape is:

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
    "evaluated_session_count": 0,
    "session_count_is_lower_bound": false,
    "harnesses": [],
    "outcomes": {
      "succeeded": 0,
      "failed": 0,
      "interrupted": 0,
      "incomplete": 0,
      "unknown": 0
    },
    "history": {"live": 0, "historical": 0, "mixed": 0},
    "latest_observed_at": null
  },
  "action_cards": [],
  "recent_work": [],
  "coverage": {
    "complete": true,
    "sources": [],
    "issue_analysis": {
      "current_sessions": 0,
      "pending_sessions": 0,
      "failed_sessions": 0,
      "truncated_sessions": 0,
      "unscoped_sessions": 0,
      "analysis_through": "2026-09-09T17:00:00Z",
      "complete": true
    },
    "limitations": []
  }
}
```

Action-card kinds are `reviewed_finding`, `evidence_gap`, `session_outcome`,
and `failed_activity`. Next-step kinds are `open_attention_family`,
`open_issue`, and `open_session`.

## Habits (user insights)

`GET /v1/user-insights` serves the browser's Habits view. It accepts only an
optional `limit` query parameter between 1 and 25 (default 8). Any other
parameter, a non-numeric limit, or a limit outside that range returns the
standard `400` invalid-request problem.

The response uses `schema_version=belay.read.v1`,
`projection_version=belay.user-insights.v1`, and an `analysis_version` that
changes whenever a rule, threshold, or piece of copy changes. It evaluates at
most 100 recent transcript sessions and returns a debrief for at most `limit`
of them. A session is debriefed only when its transcript coverage is
`complete` and it holds at least two real user messages. Each debrief reads at
most 10,000 turns of its own session; longer sessions are reported under
`coverage.turns_truncated`.

Each session carries `debrief_status` (`missing`, `ready`, or `stale`) and a
nullable `debrief`: the record written by the user's own installed Claude Code
or Codex for that session. The top-level `harness` object reports whether such
a harness is installed and which one.

`GET /v1/user-insights/{session_key}/debrief` returns the stored debrief
(`404` when none exists). With `generate=1` it first builds a scrubbed,
bounded evidence packet from that one session (user messages, condensed
assistant text, collapsed tool-call runs, failing check output, timings, and
per-turn cost), runs the installed harness with a fixed instruction prompt and
JSON schema, sanitizes the output against the packet, stores it encrypted, and
returns it. `refresh=1` regenerates even when a current debrief exists. Any
other parameter returns the standard `400` problem. Generation can take
minutes and is only triggered by an explicit browser action or `belay
analyze`. Fixed problems: `503 belay.local/habits-harness-unavailable`,
`409 belay.local/habits-session-not-ready`, `502
belay.local/habits-generation-failed`, and `504
belay.local/habits-generation-timeout`.

A debrief holds `headline`, `task_summary`, ordered `phases` with a verdict
each, one to five `insights` (kind, title, what_you_did, what_it_cost,
ideal_path, say_this_instead, evidence_turns, confidence), `keep_doing`,
`prompt_length_read`, and `next_session_opener`, plus provenance (`harness`,
`model`, `prompt_version`, `input_hash`, `generated_at`) and any
`sanitization` notes. Evidence turn references are validated against the
packet; unknown references are dropped. The raw project identity is stripped
before the record leaves the read model.

Habits is deliberately separate from issues, Attention, the Report, Mission
Packs, and experience learning:

- it reads only the encrypted transcript store and never issue projections;
- its only comparison (`baseline`) is against the same developer's other
  complete sessions in the same project, and it is `null` below three such
  sessions;
- its only writes are its own encrypted debrief records;
- it never feeds the agent and is not exposed over MCP.

Each session carries `outcome`, a plain-language `verdict`, at most three
`findings`, an optional ready-made `opener`, deterministic `measurements`, and
the `baseline`. Findings carry `tone` (`improve` or `keep`),
`evidence_class` (`measurement` or `judgment`), plain-sentence `evidence`,
and nullable `time_cost_ms` and `dollar_cost`. Output contains no raw
commands, arguments, file paths, transcript prose, or event payloads.

When Local has no transcript repository it returns the fixed
`503 belay.local/user-insights-unavailable` problem with detail:
`Belay could not read retained transcript sessions for a debrief.`

## Local session list

`GET /v1/sessions` accepts:

- `limit` (default 20, maximum 100)
- `cursor`
- `harness` (case-insensitive exact match)
- `outcome`: raw projection value `incomplete`, `succeeded`, `failed`,
  `interrupted`, or `unknown`
- `history`: `historical`, `live`, or `mixed`
- `occurred_after` and `occurred_before`: RFC3339 bounds; sessions whose
  observed event interval overlaps the requested window are returned
- `query`: maximum 128 bytes, case-insensitive substring search over only the
  safe `session_id` and `harness` fields

Session summaries retain the compatibility `historical` boolean and add
`history`. `historical=true` means the session contains at least one
artifact-reconstructed event. `history` truthfully distinguishes sessions
whose stored events are all `historical`, all `live`, or `mixed`.

## Session projection outcomes

Session summaries use projection outcomes. `incomplete` means no
`session.end` terminal evidence was observed and must not be interpreted as
success. Completed projections preserve `succeeded`, `failed`, `interrupted`,
or `unknown` from terminal evidence.

`incomplete` is a session-projection state only. It does not extend the
canonical event outcome enum, which remains `succeeded`, `failed`,
`interrupted`, or `unknown`.

## Local session detail overview

`GET /v1/sessions/{id}` returns the session summary plus a deterministic
`overview` computed only from stored canonical metadata and an additive Local
HTTP-only diagnosis:

```json
{
  "schema_version": "belay.read.v1",
  "data": {
    "session_id": "ses_...",
    "harness": "codex",
    "started_at": "2026-09-08T18:00:00Z",
    "ended_at": "2026-09-08T18:05:00Z",
    "event_count": 12,
    "outcome": "incomplete",
    "historical": true,
    "history": "mixed",
    "overview": {
      "counts": {
        "commands": 2,
        "tool_calls": 3,
        "file_reads": 1,
        "file_writes": 1,
        "file_deletes": 0,
        "network_indicators": 1,
        "permission_events": 2,
        "explicit_failed_events": 1,
        "source_unreported_outcomes": 7,
        "findings": 1
      },
      "salient_resources": [
        {"kind": "file", "name": "src/main.go", "event_count": 2}
      ],
      "salient_resources_truncated": false,
      "observed_coverage": {
        "depths": ["artifact", "tool_call"],
        "confidences": ["high", "medium"]
      },
      "outcome": {
        "value": "incomplete",
        "source": "absence_of_session_end",
        "explanation": "The agent reported that the session ended but did not report an outcome."
      }
    }
  },
  "diagnosis": {
    "projection_version": "belay.session-diagnosis.v1",
    "status": "ready",
    "summary": {
      "state": "no_reviewed_action_available",
      "title": "No reviewed action was identified",
      "detail": "Belay did not find a reviewed actionable item in the bounded analysis available for this session."
    },
    "action_cards": [],
    "coverage": {
      "issue_source": "complete",
      "evaluated_issue_count": 0,
      "issue_candidates_truncated": false,
      "analysis": {
        "current_sessions": 1,
        "pending_sessions": 0,
        "failed_sessions": 0,
        "truncated_sessions": 0,
        "unscoped_sessions": 0,
        "analysis_through": "2026-09-08T18:05:01Z",
        "complete": true
      },
      "limitations": []
    }
  },
  "data_through": "2026-09-08T18:05:01Z"
}
```

Diagnosis performs one stable session-scoped issue-summary read with a maximum
of 20 candidates. It does not load issue occurrences, cited events, family
members, timelines, findings, fix history, or monitoring. An issue-enrichment
failure never changes successful core session-detail behavior: Local returns
the session with a limited or insufficient diagnosis and fixed limitation
copy. Cross-session issue occurrence/session counts are not presented as
session-local evidence. A reviewed issue remains explained on its diagnosis
card, but its next step is `open_session` with label `Review session activity`
and the exact selected `session_id`; diagnosis cards do not use an issue view
cursor that could open another session's occurrence.

The shared readmodel `SessionDetail` remains unchanged. MCP `get_session`
therefore does not receive the Local HTTP-only diagnosis field.

Counting rules are deliberately mechanical:

- `commands` counts `command.exec`, not result records.
- `tool_calls` counts `tool.call`, not result records.
- File and network counts correspond to their exact canonical event types.
- `permission_events` counts requested, approved, and denied events.
- Failed/unreported counts use raw canonical event outcomes.
- Findings are linked by stored session key.

Salient resources contain at most 20 distinct canonical resource records,
ordered by event count descending then kind/name ascending. Coverage arrays
contain sorted distinct observed values. No prompt, completion, reasoning,
command output, file content, diff, environment value, URL query, or semantic
task name is returned.

Outcome explanations are fixed display-safe strings:

- `incomplete` is sourced from `absence_of_session_end`.
- Completed projection values are sourced from `session.end`.
- Belay never infers task success, stalls, or semantic task names.

## Local activity and findings

`GET /v1/activity` accepts `limit` (maximum 200), `cursor`,
`occurred_after`, `occurred_before`, `harness`, `resource_kind`, and canonical
event `outcome`. Resource filtering scans the complete cursor snapshot; matches
are not silently omitted because they fall outside an internal candidate
window.

`GET /v1/findings` accepts `limit` (maximum 100), `cursor`, `since`, `severity`,
and optional exact `session_id`. Finding rows retain their cited canonical event
IDs. The cursor is bound to the normalized severity, time, and session filters.
Both endpoints return the truthful list metadata defined above.

`GET /v1/stats` is global-only in Local V1. It accepts no time or workflow
filters. Filtered or workflow statistics must not be advertised until the
underlying projection is implemented.

## P0 issue and Attention foundation

The internal issue repository groups retained detector occurrences only when
they have the same opaque, versioned fingerprint. A fingerprint represents an
exact deterministic detector signature within a compatible project scope. It
does not establish semantic similarity, shared intent, or a common root cause.

The implemented repository and HTTP presentation provide:

- stable public `issue_id` and exact `fingerprint_id` values;
- issue summaries aggregated from occurrence revisions visible at one
  projection generation;
- exact matching-session occurrences with immutable cited event IDs;
- origin separation between Belay detectors and immutable Numbat findings;
- per-session analysis state and list-level completeness;
- revisioned projection snapshots that remain stable while reconciliation
  processes new or late evidence;
- default separation of stable issues, experimental signals, and verification
  evidence gaps;
- bounded exact cited-event lookup within one selected session.

This is derived, rebuildable state. Canonical events and upstream Numbat
findings remain the evidence source. Unknown outcomes remain unknown, event
strings remain untrusted observations, and issue records never claim root
cause, correctness, safety, intent, or successful remediation.

### Issue summary shape

The implemented issue HTTP routes and P0-05 MCP issue tools use this logical
summary:

```json
{
  "issue_id": "iss_...",
  "fingerprint_id": "ifp_...",
  "fingerprint_version": "1",
  "origin": "belay",
  "detector_id": "explicit_command_failure",
  "detector_version": "1",
  "category": "command_failure",
  "title_code": "issue.explicit_command_failure",
  "severity": "medium",
  "confidence": "high",
  "scope_quality": "resolved",
  "first_observed_at": "2026-09-08T17:00:00Z",
  "last_observed_at": "2026-09-08T18:00:00Z",
  "occurrence_count": 3,
  "session_count": 2,
  "harnesses": ["claude", "codex"],
  "analysis_status": "current",
  "evidence_complete": true,
  "retained_history_only": false,
  "experimental": false
}
```

Identifiers are opaque. `issue_id` is the stable address used by detail
consumers. `fingerprint_id` may be used only for exact matching; clients must
not label matching fingerprints as semantic similarity or shared root cause.
Catalog codes are fixed display keys, not generated narratives.

At a projection snapshot:

- counts include all visible occurrences in the issue group;
- session count is the number of distinct visible session IDs;
- first/last timestamps cover retained Local history only;
- severity is the highest visible fixed severity rank;
- confidence is the lowest visible confidence;
- analysis status is the least-current visible status in this order:
  `failed`, `pending`, `truncated`, `current`;
- evidence is complete only when every visible occurrence is complete;
- harnesses are sorted and distinct;
- repeated means at least two distinct sessions;
- unscoped or conflicting sessions never establish cross-session recurrence.

Filters select issue groups. Returned counts and aggregate fields continue to
describe the complete visible group at the snapshot rather than only the
occurrences that matched a filter.

### Issue occurrence HTTP shape

An issue occurrence represents one exact fingerprint in one session. HTTP
detail uses the privacy-closed `belay.issue.v2` presentation:

```json
{
  "occurrence_id": "occ_...",
  "issue_id": "iss_...",
  "fingerprint_id": "ifp_...",
  "fingerprint_version": "1",
  "session_id": "ses_...",
  "harness": "codex",
  "origin": "belay",
  "provenance": {
    "detector_id": "explicit_command_failure",
    "detector_version": "1",
    "fingerprint_version": "1",
    "projection_version": "belay.issue.v1"
  },
  "category": "command_failure",
  "title_code": "issue.explicit_command_failure",
  "first_observed_at": "2026-09-08T17:58:00Z",
  "last_observed_at": "2026-09-08T18:00:00Z",
  "severity": "medium",
  "confidence": "high",
  "scope_quality": "resolved",
  "analysis_status": "current",
  "evidence_complete": true,
  "retained_history_only": false,
  "experimental": false,
  "evidence": {
    "cited_event_ids": ["01890f2e-6d4b-7c8a-9b0c-123456789abc"]
  }
}
```

`belay.issue.v2` retains exact fingerprint identity, sanitized detector
provenance, the required-nullable safe source signal code, and cited canonical
event IDs. It does not expose `origin_record_id`, evidence dimensions,
analysis generation, private fingerprint scope, or storage fields.
`evidence.cited_event_ids` refer to canonical events retrievable through the
exact session event lookup route. MCP continues to use its separate strict
allowlisted issue projection; this HTTP revision does not change any MCP tool
or schema.

## Implemented Attention family HTTP contract

Signal families are a read-only catalog rollup over exact issue summaries and
occurrences visible at one frozen issue snapshot. They do not replace exact
issue IDs, establish recurrence, infer one project/configuration/root cause, or
own fix and recurrence actions.

### `GET /v1/attention-families`

Accepted query parameters:

- `limit`: default 20, maximum 100;
- `cursor`: opaque family-list continuation;
- `severity`: effective family severity `info`, `low`, `medium`, `high`, or
  `critical`;
- `harness`: case-insensitive exact harness match over visible occurrences;
- `origin`: `belay` or `numbat`;
- `analysis_status`: `current`, `pending`, `failed`, or `truncated`;
- `observed_after`: RFC3339 lower bound over visible occurrences;
- `attention_kind`: `issue` or `evidence_gap`; default `issue`;
- `experimental`: `stable` or `include`; default `stable`.

A fresh request may contain filters and an explicit limit. A continuation
contains only `cursor`; the authenticated family cursor recovers all normalized
filters, effective limit, catalog version, snapshot metadata, and ordering
position. Category, recurrence, session, and fingerprint filters are
intentionally not accepted.

Families are ordered by effective severity descending, matched supporting issue
count descending, last observed descending, then the private internal family
key ascending. Grouping happens before `LIMIT`.

```json
{
  "schema_version": "belay.read.v1",
  "projection_version": "belay.attention-family.v1",
  "data": [{
    "family_id": "atf_...",
    "kind": "mapped_upstream",
    "representative_issue_id": "iss_...",
    "attention_kind": "issue",
    "severity": "low",
    "confidence": "high",
    "first_observed_at": "2026-09-08T17:00:00Z",
    "last_observed_at": "2026-09-08T18:00:00Z",
    "supporting_issue_count": 10,
    "occurrence_count": 10,
    "session_count": 10,
    "harnesses": ["claude-code"],
    "scope": {
      "resolved": 0,
      "lexical": 0,
      "unscoped": 10,
      "conflict": 0
    },
    "analysis_status": "current",
    "evidence_complete": true,
    "retained_history_only": false,
    "experimental": false,
    "catalog": {
      "catalog_version": "belay.attention-families.v1",
      "mapping_key": "attention.agent_guardrails_configuration",
      "mapping_version": "1",
      "grouping_version": "1",
      "display_title": "Agent safety confirmations may be disabled",
      "observation_statement": "A mapped upstream rule reported retained configuration evidence associated with disabled agent safety confirmations.",
      "caveat": "This does not establish one shared configuration, project, cause, malicious tampering, or an unsafe action.",
      "next_evidence_action": "inspect_exact_records"
    },
    "view_cursor": "opaque-family-view-cursor"
  }],
  "global_analysis_coverage": {
    "current_sessions": 120,
    "pending_sessions": 0,
    "failed_sessions": 0,
    "truncated_sessions": 0,
    "unscoped_sessions": 10,
    "analysis_through": "2026-09-08T18:05:01Z",
    "complete": true
  },
  "selection": {
    "attention_kind": "issue",
    "experimental": "stable",
    "includes_evidence_gaps": false,
    "includes_experimental": false
  },
  "next_cursor": null,
  "has_more": false,
  "returned_count": 1,
  "limit": 20
}
```

Stable Belay-native issues appear as one-member `exact_issue` families. Their
row `view_cursor` is an existing exact `issue_view` cursor and the client opens
the representative issue directly. Only admitted `mapped_upstream` rows use
family detail. Unknown, unsupported, missing-finding, mixed-version, or
otherwise incompatible upstream issues remain inspectable through
`GET /v1/issues` but are absent from default family Attention.

### `GET /v1/attention-families/{family_id}`

Mapped-family detail accepts exactly:

- initial read: required `view_cursor` from the family list and optional
  `limit`;
- continuation: `cursor` only.

No-cursor lookup, both cursor forms together, repeated/empty parameters,
unknown parameters, and limits outside 1 through 100 return `400`. Validation
order is request syntax (`400`), authenticated cursor/catalog/snapshot and
retention validity (`410`), then family existence (`404`). An expired snapshot
therefore never becomes a misleading not-found result.

The response repeats the family summary and returns exact child issue summaries
ordered by analysis precedence (`failed`, `pending`, `truncated`, `current`),
then `last_observed_at DESC`, then `issue_id ASC`. Each child includes a
same-snapshot exact `issue_view` cursor suitable for
`GET /v1/issues/{id}/occurrences`.

```json
{
  "schema_version": "belay.read.v1",
  "projection_version": "belay.attention-family.v1",
  "data": {
    "family": {},
    "members": [{
      "issue": {},
      "catalog": {},
      "view_cursor": "opaque-exact-issue-view-cursor"
    }]
  },
  "global_analysis_coverage": {},
  "view_cursor": "opaque-family-view-cursor",
  "next_cursor": null,
  "has_more": false,
  "returned_count": 1,
  "limit": 20
}
```

Family detail exposes no mutation, fix, recurrence, or family-level evidence
hydration action. The developer must select an exact child before using those
exact-issue capabilities.

### Family cursor semantics

Family list, mapped-family view, and member continuation cursors use a separate
strict authenticated envelope from exact issue cursor-v2. They bind the
database epoch, issue snapshot, retention generation, issued-at time,
normalized filters, attention kind, catalog version, selected family, page
size, and complete ordering position as applicable. They expire after 15
minutes. A compiled family-catalog version change returns `410` for prior
family cursors without changing or invalidating the exact issue cursor format.

## Implemented issue HTTP contract

### `GET /v1/issues`

Accepted query parameters:

- `limit`: default 20, maximum 100;
- `cursor`: opaque issue-list cursor;
- `severity`: exact fixed rank `info`, `low`, `medium`, `high`, or `critical`;
- `category`: exact fixed catalog category code;
- `harness`: case-insensitive exact harness match;
- `origin`: `belay` or `numbat`;
- `analysis_status`: `current`, `pending`, `failed`, or `truncated`;
- `observed_after`: RFC3339 lower bound selecting groups with a visible
  occurrence at or after the bound;
- `recurrence`: `single` or `repeated`;
- `session_id`: exact Belay session identifier;
- `fingerprint_id`: exact opaque fingerprint identifier;
- `attention_kind`: `issue`, `evidence_gap`, or `all`; default `issue`;
- `experimental`: `stable`, `include`, or `only`; default `stable`.

Ordering is severity descending (`critical`, `high`, `medium`, `low`, `info`),
repeated before single within a severity, `last_observed_at DESC`, then
`issue_id ASC`. A fresh request may include filters and an explicit limit. A
continuation contains only `cursor`; all normalized filters and the effective
limit are recovered from authenticated cursor-v2.
The default response excludes experimental signals and evidence gaps.
Verification evidence gaps are requested separately with
`attention_kind=evidence_gap`; they do not inflate the default issue count.

Response:

```json
{
  "schema_version": "belay.read.v1",
  "projection_version": "belay.issue.v1",
  "data": [],
  "analysis": {
    "current_sessions": 120,
    "pending_sessions": 2,
    "failed_sessions": 1,
    "truncated_sessions": 0,
    "unscoped_sessions": 8,
    "analysis_through": "2026-09-08T18:05:01Z",
    "complete": false
  },
  "selection": {
    "attention_kind": "issue",
    "experimental": "stable",
    "includes_evidence_gaps": false,
    "includes_experimental": false
  },
  "view_cursor": "opaque-rowless-view-cursor",
  "next_cursor": null,
  "has_more": false,
  "returned_count": 0,
  "limit": 20
}
```

`analysis.complete` is true only when no retained session is pending, failed, or
truncated at the response snapshot. Unscoped sessions may be fully analyzed but
cannot support cross-session recurrence. When `complete=false`, an empty result
means only that no issue is available from the completed portion. Consumers
must identify incomplete coverage and must not say that no issues exist.

`selection` is the server-normalized issue/evidence-gap and experimental
selection bound into the cursor. Consumers use it to qualify empty results and
must not infer a broader selection from client-side controls.

`view_cursor` carries the list's immutable issue-projection snapshot without a
row position. A client passes it to the initial detail request so list and
detail remain on the same logical view.

### `GET /v1/issues/{id}/occurrences`

Accepted query parameters:

- `limit`: default 20, maximum 100;
- `cursor`: opaque occurrence cursor bound to the issue ID;
- `view_cursor`: rowless cursor from `GET /v1/issues`, accepted only for the
  initial detail request.

Each parameter is accepted at most once, unknown or empty parameters return
`400`, and `limit` must be an integer from 1 through 100. `cursor` and
`view_cursor` are mutually exclusive; a continuation request contains only
`cursor`. Exact issue lookup includes experimental and evidence-gap rows
because selecting an opaque issue ID is explicit intent.

The response contains the issue summary at the cursor snapshot and occurrence
rows ordered by `last_observed_at DESC, occurrence_id ASC`, followed by the
common list metadata. The route returns `404 application/problem+json` when the
issue does not exist at a fresh snapshot. It returns a cursor error, rather
than `404`, when a supplied cursor is malformed, expired, or belongs to another
issue. Every successful response also returns a rowless `view_cursor` for the
same exact issue snapshot; Local browser fix eligibility may consume that
cursor without first locating the issue in a paginated issue list.

```json
{
  "schema_version": "belay.read.v1",
  "projection_version": "belay.issue.v2",
  "data": {
    "issue": {},
    "occurrences": []
  },
  "catalog": {
    "catalog_version": "belay.issue-explanations.v1",
    "catalog_status": "known",
    "title_code": "issue.explicit_command_failure",
    "observation_statement": "The source explicitly reported a failed command result.",
    "caveat": "A reported command failure does not by itself establish root cause or whether a later attempt succeeded.",
    "next_evidence_action": "inspect_cited_events"
  },
  "global_analysis_coverage": {
    "current_sessions": 120,
    "pending_sessions": 2,
    "failed_sessions": 1,
    "truncated_sessions": 0,
    "unscoped_sessions": 8,
    "analysis_through": "2026-09-08T18:05:01Z",
    "complete": false
  },
  "view_cursor": "opaque-rowless-view-cursor",
  "next_cursor": null,
  "has_more": false,
  "returned_count": 0,
  "limit": 20
}
```

`catalog` contains fixed, versioned observation, caveat, and evidence-navigation
content. It is not generated diagnosis or remediation advice.
`global_analysis_coverage` describes all retained sessions at the exact frozen
issue snapshot; filtering for one issue does not narrow that coverage.

### Issue cursor semantics

Issue list, view, and occurrence cursors use authenticated cursor-v2. They
carry a database-specific epoch, immutable projection generation, retention
generation, normalized filters and effective limit where applicable, route and
issue binding, deterministic sort position, and issued-at time. They expire
after 15 minutes.

- A fresh request without a cursor reads the current projection generation.
- Issue-list and occurrence continuation requests contain only `cursor`.
- Reconciliation after page one cannot add, remove, or reorder rows in that
  cursor chain.
- A malformed, bad-MAC, cross-route, issue-mismatched, or filter-mismatched cursor
  returns `400 application/problem+json` without reflecting cursor contents.
- A valid authenticated cursor with a stale database epoch, expired lifetime,
  or unavailable retained projection returns `410 application/problem+json`
  with fixed type
  `belay.local/cursor-expired`.
- Migration 012 expires every issue cursor-v1 and pre-reset fix eligibility or
  action token.
- On 410, the browser clears issue list/detail/occurrence and fix-eligibility
  state, refreshes both Attention lists, and requires explicit issue reselection
  and action retry. It never automatically resubmits a mutation.

### `GET /v1/sessions/{id}/events/lookup`

This route performs one bounded exact lookup of cited canonical events within
the selected session. It accepts only repeated `event_id` query parameters:

- one to 50 values;
- lowercase canonical UUIDv7 values;
- duplicates removed while preserving first-request order for missing-ID
  reporting;
- no cursor, free-form query, or other query parameter.

Returned events use canonical timeline ordering. An event belonging to another
session is reported as missing rather than returned. Missing IDs are ordinary
data, not `404`.

```json
{
  "schema_version": "belay.read.v1",
  "data": [],
  "requested_count": 2,
  "found_count": 1,
  "missing_count": 1,
  "missing_event_ids": ["01890f2e-6d4b-7c8a-9b0c-123456789abc"],
  "data_through": "2026-09-08T18:05:01Z"
}
```

## P0-03 Local browser fix-attempt contract

These routes record only a developer declaration that an external change was
attempted. Belay does not execute the change, inspect a diff, mark the issue
resolved, suppress future detections, or verify an outcome.

All responses use `schema_version=belay.fix.v1`. Fix actions are available only
through explicitly configured Local HTTP. They are not part of MCP or the CLI.

### Fixed catalogs

`change_catalog_version` is `fix-change.v1`. `change_kind` must be exactly one
of:

- `code_change`;
- `configuration_change`;
- `dependency_change`;
- `permission_change`;
- `environment_change`;
- `agent_instruction`;
- `project_rule`;
- `monitor_hook`;
- `other`.

Retraction `reason` must be exactly one of:

- `recorded_by_mistake`;
- `superseded`;
- `other`.

No route accepts a free-text note, command, path, diff, prompt, output, rule or
hook body, environment value, URL, client timestamp, client-selected annotation
ID, occurrence ID, fingerprint, state, or outcome.

### `GET /v1/issues/{id}/fix-eligibility`

Accepts exactly one non-empty `view_cursor` from the current Attention issue
view. The cursor is structurally decoded and freshness-checked, then the server
evaluates the complete issue aggregate and latest visible anchor in one read
transaction.

Eligibility requires:

- a visible stable issue at the supplied snapshot;
- aggregate analysis status `current`;
- non-experimental origin data;
- a category other than `evidence_gap`;
- `resolved` or `lexical` scope quality.

No visible issue rows return `404`. Aggregate status covers every visible
occurrence, so any non-current visible occurrence returns
`analysis_not_current`.

Eligible response:

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

Ineligible current issues return `200` with `eligible=false`,
`action_token=null`, `expires_at=null`, and one fixed reason:

- `analysis_not_current`;
- `experimental_signal`;
- `evidence_gap`;
- `scope_unavailable`.

The action token authenticates its structure, issue ID, immutable issue
projection snapshot, issued-at time, and expiry. Invalid cursor syntax returns
`400`; an expired or compacted issue view returns `410
belay.local/cursor-expired`.

### `POST /v1/issues/{id}/fixes`

Required headers:

```text
Authorization: Bearer <per-launch Local token>
Content-Type: application/json
Idempotency-Key: <canonical lowercase UUIDv4>
X-Belay-Intent: record-fix-attempt.v1
Origin: http://<exact numeric loopback listener address and port>
```

The request `Host` must equal the actual listener address and port. If
`Sec-Fetch-Site` is present, it must be `same-origin`. Missing, `null`,
cross-origin, DNS-name, wrong-port, or duplicate security headers are rejected.
Forwarded host and protocol headers are ignored. CORS is not enabled.

The body is limited to 1 KiB, must use identity encoding, must contain exactly
one JSON object, and rejects unknown fields, duplicate fields, or trailing JSON:

```json
{
  "action_token": "<signed opaque token>",
  "change_kind": "code_change"
}
```

The server authenticates token structure and MAC and verifies the signed issue
against the route before durable idempotency lookup. For a new declaration it
then validates token expiry and snapshot freshness, derives aggregate
eligibility, chooses the latest visible occurrence, and captures its exact
revision, generation, timestamps, fingerprint, session, detector provenance,
and cited event IDs in the same transaction.

First creation returns `201` and `replayed=false`. An identical request using
the same idempotency key returns the original row with `200` and
`replayed=true`, including after action-token expiry. Reusing that key with
different canonical intent returns `409 belay.local/idempotency-conflict`.

Create/replay response:

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

Active response DTOs encode `retraction_reason` and `retracted_at` explicitly as
JSON `null`.

### `GET /v1/issues/{id}/fixes`

Accepts only:

- `limit`: default 20, maximum 100;
- `cursor`: opaque fix-history cursor bound to the issue ID.

A fresh request captures independent annotation and retraction high-water marks.
Rows are ordered by `recorded_at DESC, annotation_id DESC`. New annotations and
new retractions after page one are excluded from the existing cursor chain.
Fix-history cursors do not expire under ordinary retention.

Response:

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

Each row uses the complete annotation DTO shown above. `state` is `active` or
`retracted`. Retracted rows contain their fixed `retraction_reason` and
`retracted_at`; active rows contain explicit nulls.

`evidence_currently_retained` is evaluated at page-read time:

- `available`: every originally cited event remains;
- `partial`: some cited events remain;
- `pruned`: no originally cited event remains;
- `unknown`: the annotation had no baseline citations.

A syntactically valid issue ID with no annotation rows returns an empty `200`
even if the issue projection no longer contains that issue. History remains
readable after issue disappearance and Local restart.

### `POST /v1/issues/{id}/fixes/{annotation_id}/retractions`

Uses the same bearer, exact listener `Host`/`Origin`, fetch-site, media type,
encoding, 1 KiB body, strict JSON, and UUIDv4 idempotency requirements as
creation, with:

```text
X-Belay-Intent: retract-fix-attempt.v1
```

Body:

```json
{"reason":"recorded_by_mistake"}
```

The original annotation is never changed or deleted. First append returns `201`;
an identical retry returns `200` and `replayed=true`. Reusing the key with
different canonical content returns `409 belay.local/idempotency-conflict`.
Trying to append another retraction with a different key returns `409
belay.local/already-retracted`.

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

### Fix error contract

Error responses are `application/problem+json`, contain a server-generated
`request_id`, and never reflect the bearer token, action token, idempotency key,
cursor, issue ID, annotation ID, or request body.

| Condition | Status/type |
|---|---|
| Missing or invalid bearer | `401 about:blank` |
| Missing/mismatched listener Origin or Host, bad fetch-site or intent | `403 belay.local/write-forbidden` |
| Unsupported media type or content encoding | `415 belay.local/unsupported-media-type` |
| Body over 1 KiB | `413 belay.local/request-too-large` |
| Malformed query/header/JSON/enum/ID/key/token | `400 about:blank` |
| Expired/compacted action snapshot for a new declaration | `410 belay.local/cursor-expired` |
| Issue or annotation not found | `404 about:blank` |
| Current issue is ineligible | `409 belay.local/ineligible-fix-annotation` |
| Idempotency key reused for different content | `409 belay.local/idempotency-conflict` |
| Annotation already retracted through another request | `409 belay.local/already-retracted` |
| Storage failure | `500 about:blank` |

### Persistence, retention, and privacy

Annotations and retractions are append-only Local state in the Keychain-backed
store. Their minimized records contain opaque identifiers, fixed catalog
values, timestamps, and request fingerprints; no free-text or evidence payload
is accepted. Opaque identities and request fingerprints are derived with
store-specific, domain-separated keys; raw idempotency keys are never persisted
or logged.

Ordinary event/finding/issue retention excludes annotation and retraction rows.
Event pruning cascades only annotation citation sidecars, which may change
read-time evidence status without deleting the declaration. History, create
replay, and retraction replay survive Local process restart when the same Local
database and Keychain key are used. A full Local database reset removes them
with the rest of Local state.

## P0-04 exact recurrence-monitoring contract

These authenticated, loopback-only reads report deterministic exact compatible
fingerprint observations after a fix-attempt's server-recorded `monitor_from`.
A matching observation is attention evidence, not proof that a fix failed. No
later match is not proof that a fix worked.
`historical_matching_evidence_count` aggregates qualifying post-baseline
observations across active and retracted attempts; it does not describe
pre-attempt evidence.

All responses use `schema_version=belay.fix-monitoring.v1`. Lists default to 20
rows and are bounded to 100. Arrays are non-null. Nullable fields are emitted as
explicit JSON `null`.

### Fixed monitoring catalogs

`fix_recurrence_state` is one of:

- `matching_evidence_observed`;
- `monitoring_incomplete`;
- `awaiting_later_evidence`;
- `no_later_match_observed`;
- `comparison_unavailable`;
- `retracted`.

`future_comparison_unavailable_reason` is null or one of:

- `scope_unavailable`;
- `source_positive_only`;
- `capability_unavailable`;
- `baseline_time_unavailable`;
- `fingerprint_version_unsupported`.

Future unknown recurrence-state values are exposed as neutral `unknown` so the
browser can render “Monitoring status unavailable” without inferring a known
comparison state. Unknown comparison-unavailability reasons fail closed to
`capability_unavailable`.

Evidence-retention state is `available`, `partial`, `pruned`, or `unknown`.
Coverage contains `comparable_current`, `comparable_pending`,
`comparable_failed`, `comparable_truncated`, nullable `analysis_through`, and
`complete`. `analysis_complete` equals `coverage.complete`.
`count_is_lower_bound=true` only for a positive observation count with
incomplete coverage.

### `GET /v1/fix-monitoring`

The top-level list returns one grouped row per issue. By default it includes
issues with active attempts; `include_retracted=true` also permits all-retracted
history rows.

Initial query parameters, each accepted at most once:

- `state`: exact recurrence-state catalog value;
- `change_kind`: exact `fix-change.v1` value;
- `severity`: `info`, `low`, `medium`, `high`, or `critical`;
- `harness`: exact normalized harness, maximum 128 bytes;
- `recorded_after`: RFC3339 instant;
- `issue_id`: exact canonical issue ID;
- `include_retracted`: exact lowercase `true` or `false`;
- `limit`: 1–100.

A continuation request contains only `cursor`. Grouping selects the driving
attempt before filters. Rows are ordered by state rank, known severity rank,
`COALESCE(last_recurrence_observed_at, recorded_at) DESC`, then `issue_id ASC`.
Equal positions remain deterministic.

Response:

```json
{
  "schema_version": "belay.fix-monitoring.v1",
  "data": [{
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
  }],
  "returned_count": 1,
  "limit": 20,
  "has_more": false,
  "next_cursor": null,
  "monitoring_view_cursor": "<opaque>",
  "evidence_evaluated_at": "2026-09-08T19:17:00Z"
}
```

All-retracted grouped rows have zero active/observed-attempt counts, state
`retracted`, empty current coverage with null `analysis_through`, and may retain
historical observation counts.

### `GET /v1/issues/{id}/fix-monitoring`

This route paginates attempts ordered by `recorded_at DESC, annotation_id DESC`.
An initial read accepts optional `limit` and either:

- no cursor, creating a fresh monitoring snapshot; or
- exactly one `view_cursor` transferred from the top-level
  `monitoring_view_cursor`.

Continuation accepts only `cursor`. `cursor` and `view_cursor` are mutually
exclusive.

Response:

```json
{
  "schema_version": "belay.fix-monitoring.v1",
  "issue_id": "iss_...",
  "current_issue_available": false,
  "current_issue": null,
  "data": [{
    "annotation_id": "fxa_...",
    "issue_id": "iss_...",
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
  }],
  "returned_count": 1,
  "limit": 20,
  "has_more": false,
  "next_cursor": null,
  "monitoring_view_cursor": "<opaque>",
  "evidence_evaluated_at": "2026-09-08T19:17:00Z"
}
```

`current_issue_available=false` requires `current_issue=null`; history remains
readable after the issue leaves the current projection. When available,
`current_issue` is the bounded issue-summary DTO. Nullable subject fields are
exactly `title_code`, `severity`, `confidence`, and `anchor_harness`. Active
attempts encode `retraction_reason` and `retracted_at` as null.

### `GET /v1/issues/{id}/fixes/{annotation_id}/recurrences`

The first observation request requires exactly one
`observation_view_cursor` from its attempt row and may include `limit`.
Continuation requests contain only `cursor`. Rows are ordered by
`first_qualifying_event_at DESC, recurrence_id DESC`.

Response:

```json
{
  "schema_version": "belay.fix-monitoring.v1",
  "issue_id": "iss_...",
  "annotation_id": "fxa_...",
  "data": [{
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
      "019921c0-7abc-7def-8abc-0123456789ab"
    ],
    "retained_event_count": 1,
    "missing_event_count": 1,
    "evidence_complete": true,
    "evidence_truncated": false,
    "evidence_currently_retained": "partial",
    "same_session_as_anchor": false,
    "observed_at": "2026-09-08T19:15:00Z"
  }],
  "returned_count": 1,
  "limit": 20,
  "has_more": false,
  "next_cursor": null,
  "evidence_evaluated_at": "2026-09-08T19:17:00Z"
}
```

`retained_event_ids` contains at most 50 canonical lowercase UUIDv7 event IDs
for the row's session. The existing session-constrained lookup route may fetch
them without scanning a broad timeline. `qualifying_citation_count` is the
immutable original count. `evidence_complete` records detector completeness at
observation time. `evidence_truncated=true` means more than 50 retained
citations exist.

For unknown evidence the exact DTO is:

- `retained_event_ids=[]`;
- `retained_event_count=null`;
- `missing_event_count=null`;
- `evidence_truncated=null`;
- `evidence_currently_retained="unknown"`.

### Monitoring snapshots, readiness, and errors

Monitoring uses dedicated opaque cursor kinds for the top list, list view,
issue attempts, per-attempt observation view, and observation pages. Cursors
bind the endpoint, normalized filters, route IDs, original page size, row
position, issued-at time, and one snapshot containing issue projection, event,
retention, annotation, retraction, recurrence-job, job-event, and observation
high-water marks.

The snapshot lifetime is 15 minutes. A changed retention generation also
expires the chain. View cursors are rowless snapshot transfers; page cursors
carry deterministic row position. Monitoring cursors are not accepted by
legacy session/activity/finding/issue routes, and legacy cursors are not
accepted here.

Schema migration completes before Local starts. Historical recurrence catch-up
runs after the HTTP server starts. During `catching_up` or `failed`, only the
three monitoring routes fail closed; all older Local reads and fix writes
remain available.

Validation/error precedence is authentication, path syntax, query syntax,
cursor syntax/binding, readiness, expiry/retention validity, resource
existence, repository read, then encoding.

| Condition | Status/type |
|---|---|
| Missing/invalid bearer | `401 about:blank` |
| Invalid/repeated/unknown query, ID, limit, filter, or cursor envelope | `400 about:blank` |
| Catch-up active | `503 belay.local/monitoring-catchup-in-progress` |
| Catch-up failed awaiting recovery | `503 belay.local/monitoring-catchup-failed` |
| Expired/compacted/retention-invalid cursor | `410 belay.local/cursor-expired` |
| Missing issue/history, missing annotation, or route-binding mismatch | `404 about:blank` |
| Repository/encoding failure | `500 about:blank` |

Problem details and server-generated request IDs never reflect request or
stored values. Cancellation leaves catch-up durable and retryable rather than
recording a false failure. Local restart resumes incomplete catch-up. Once
ready, a failed individual recurrence job contributes incomplete attempt
coverage and retries independently instead of disabling all monitoring.

## Authentication

- Local browser requests use a random per-launch token.
- Local fix writes additionally require exact same-origin intent bound to the
  actual numeric loopback listener, strict JSON, a route-specific intent header,
  and a canonical UUIDv4 idempotency key.
- Local MCP uses stdio only. It opens no network listener and has no loopback
  credential. The configured client spawns the process, and access is bounded by
  the logged-in user's process, filesystem, database, and Keychain permissions.
- Future Teams authentication and workspace authorization are not implemented
  by the Local Alpha.

## Standard filters

Implemented Local filters, where applicable:

- `occurred_after`
- `occurred_before`
- `harness`
- `history`
- `outcome`
- `resource_kind`
- `severity`
- `session_id` for findings
- issue filters documented under `GET /v1/issues`, including
  `attention_kind` and `experimental`
- family filters documented under `GET /v1/attention-families`; family and
  evidence-gap pagination remain independent
- monitoring filters documented under `GET /v1/fix-monitoring`, including
  exact recurrence state, change kind, recorded-after, issue ID, and
  include-retracted

## Errors

Errors use `application/problem+json` with:

- `type`
- `title`
- `status`
- `detail` without payload reflection
- `request_id`

## Acceptance tests

1. A timeline can be reconstructed using documented Local endpoints alone.
2. The Local browser performs no data read outside the implemented routes.
3. Local rejects non-loopback access.
4. Pagination remains stable with late and out-of-order events.
5. Malformed, cross-endpoint, and filter-mismatched cursors fail closed.
6. Resource-kind activity filtering is exhaustive within its cursor snapshot.
7. Issue pagination remains stable while reconciliation advances.
8. Attention defaults exclude experimental signals and keep Evidence gaps
   separate.
9. Incomplete analysis is reported truthfully and never converted into a
   complete empty-state claim.
10. Matching sessions use exact fingerprint equality, never semantic or
    shared-root-cause language.
11. Exact event lookup remains bounded, session-constrained, and rejects
    unrelated query parameters.
12. Fix writes fail closed without the actual listener-bound Origin/Host and
    explicit browser intent.
13. Fix creation/retraction are append-only, idempotent, payload-free, and
    survive Local restart and ordinary retention.
14. Exact recurrence monitoring remains a Local HTTP/browser capability. MCP
    fix proposal/application records remain bounded and do not edit project
    files or claim recurrence reduction.
15. Monitoring cursors bind every documented high-water, route ID, normalized
    filter, page size, and ordering position for 15 minutes.
16. Catch-up 503 affects only monitoring routes; cancellation/restart converges
    without exposing partial history as complete.
17. Durable observations and attempt history survive restart and issue
    disappearance; retention changes expire old cursors and truthfully degrade
    evidence on fresh reads.
18. Matching evidence is never described as fix failure, and no-match,
    incomplete, unavailable, or unknown evidence is never described as success.
19. Issue list and occurrence continuations send only their returned cursor;
    selection, effective limit, snapshot, and ordering remain cursor-bound.
20. Every successful issue page includes a view cursor; inconsistent
    `has_more`/`next_cursor` metadata fails closed in browser clients.
21. Issue detail catalog and global analysis coverage describe the same frozen
    snapshot as the issue and occurrence page.
22. Family grouping occurs before pagination and admits upstream members only
    through the fixed catalog plus exact retained raw rule-version validation.
23. Family list/detail/member cursors preserve one issue snapshot and catalog
    version; stale authenticated cursors return 410 before family existence is
    evaluated.
24. Exact child navigation receives a same-snapshot exact issue view cursor;
    family routes expose no fix, recurrence, evidence-hydration, or mutation
    action.
25. HTTP issue detail emits `belay.issue.v2` and never emits origin record IDs,
    evidence dimensions, analysis generation, or private fingerprint scope.

Teams shape compatibility and cross-workspace authorization remain future
acceptance requirements, not Local Alpha claims.
