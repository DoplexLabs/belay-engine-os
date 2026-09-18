# P0-07: Attention Signal Families

- **Status:** Revised after self-review
- **Date:** 2026-09-08
- **Depends on:** P0-01, P0-02, P0-05, and P0-06
- **Reference roadmap:** `docs/launch/individual-alpha-value-roadmap.md`

## 1. Overview

Belay correctly stores and exposes one issue per exact deterministic
fingerprint. The founder's retained alpha dataset consequently shows ten
visually identical Attention cards for ten exact `tamper.guardrails_off`
issues across ten unscoped sessions.

P0-07 adds a read-only **signal-family view** over the existing materialized
issue snapshot. A family is a customer-facing catalog rollup; it does not
replace issue identity, claim exact recurrence, infer shared scope/root cause,
or own fix monitoring. Exact issue IDs, occurrences, citations, recurrence, and
fix records remain authoritative.

The family view is computed at query time from the existing snapshot-stable
issue-summary projection. It requires no schema migration, no second
asynchronous projector, and no change to ingestion, reconciliation, or issue
fingerprints.

## 2. Glossary

- **Exact issue:** Existing `iss_...` deterministic issue identity.
- **Signal mapping:** A fixed Belay catalog entry admitting one exact upstream
  signal code and reviewed source rule version into default Attention.
- **Signal family:** One list/detail grouping of admitted exact issues.
- **Catalog rollup:** Grouping by one known signal type. It does not state that
  members are the same scoped problem.
- **Member:** One existing exact issue summary in a family.
- **Matched member:** A member satisfying the current family-list filters.
- **Inspectable-only issue:** An exact upstream issue excluded from default
  family Attention but still available through `/v1/issues` and existing MCP.

## 3. Requirements

### Functional

1. Default browser Attention MUST use a family endpoint.
2. Stable Belay issues MUST appear as one-member `exact_issue` families.
3. Evidence-gap issues MUST remain in their independent Attention section and
   pagination chain.
4. An upstream issue MUST enter default family Attention only when origin,
   title/category, source signal code, raw source rule version, fingerprint
   version, and catalog status exactly match a fixed Belay mapping.
5. The initial mapping admits `tamper.guardrails_off`, raw rule version `1.1`,
   Numbat origin, `issue.numbat_finding`, `numbat_finding`, and fingerprint
   version `1`.
6. The repository MUST validate raw rule version through the retained finding
   joined from each occurrence; the 48-bit display digest alone is not an
   admission boundary.
7. Admitted upstream members sharing mapping key/version and fingerprint
   version MUST produce one `mapped_upstream` family across the selected
   retained snapshot.
8. The global mapped family is explicitly a signal-type summary. It MUST carry
   scope-quality counts and MUST NOT be described as recurrence, one issue, one
   configuration, one project, or one cause.
9. Unknown, invalid, mixed, or unsupported upstream issues MUST not enter
   default family Attention.
10. Family grouping MUST happen before `LIMIT`, so counts, order, and
    pagination operate on families.
11. Family detail MUST return a bounded same-snapshot page of exact child issue
    summaries.
12. One-member `exact_issue` cards MUST open existing exact issue detail
    directly. Only multi-member mapped families use family detail.
13. Family detail has no fix, recurrence, evidence-hydration, or mutation
    action. A developer selects an exact child before accessing those features.
14. Primary retained evidence for this signal MUST display:
    - `Fewer approval prompts enabled` inside this mapped family;
    - `This cited event recorded the session starting in or switching to a
      permission mode that asks for fewer approvals. Belay does not retain the
      configuration value or body.`
    Raw event types remain technical-only.
15. Exact fix controls MUST remain hidden until eligibility succeeds. The
    section appears only when eligibility succeeds, durable history exists, or
    a resumable draft exists.
16. Existing exact issue identities, summaries, citations, and all nine MCP tool
    contracts remain unchanged. Exact HTTP occurrence presentation advances to
    privacy-closed `belay.issue.v2` before alpha. P0-07 is browser-first;
    MCP-primary launch remains blocked until P0-09 provides family-aware
    workflows.

### Non-functional

1. Family reads use one read-only transaction and the existing issue snapshot,
   epoch, retention generation, and cursor lifetime.
2. Query-time grouping MUST be indexed and bounded after grouping with
   `LIMIT + 1`.
3. Family cursors MUST use a separate family envelope/version and bind
   normalized filters, issue snapshot metadata, family catalog version,
   ordering position, and issue attention kind.
4. Any family catalog version change MUST invalidate old family cursors.
5. Family-query failure MUST not affect ingestion, exact issue reads, Local
   startup, or MCP.
6. Browser dynamic values use text nodes; new hierarchy remains keyboard,
   screen-reader, mobile, and focus-restoration usable.
7. No raw upstream prose, source record ID, command output, private path,
   prompt, completion, file content, or secret may enter family identity or
   fixed prose.

## 4. Current State / Context

Numbat exact fingerprint material includes raw rule ID, rule version, and cited
event types before opaque identity derivation. That behavior is correct and
unchanged.

P0-06 added grammar-constrained `source_signal_code` and fixed presentation
copy for `tamper.guardrails_off`, but `/v1/issues` still returns every exact
issue. Client-side grouping after pagination would be incomplete and would
leave counts and `has_more` incorrect.

The existing materialized issue-summary snapshot already provides:

- exact issue identity;
- source signal code;
- origin, title, category, detector and fingerprint versions;
- severity, confidence, timestamps, analysis state;
- session and harness relationships;
- stable snapshot and retention semantics.

For mapped upstream admission, family queries additionally join issue
occurrence revisions visible at the frozen snapshot to retained findings and
require exact raw rule version `1.1`.

## 5. High Level Design

```text
existing issue-summary snapshot
             |
             v
  read-only family classification/grouping
             |
       +-----+------+
       |            |
       v            v
 family list    family members
       |            |
       +------ browser ------> existing exact issue detail
```

### 5.1 Shared catalog

Create `internal/canonical/sourcecatalog`.

Each mapping defines:

- catalog, mapping, and grouping version;
- mapping key;
- origin, title code, category;
- exact source signal code;
- reviewed raw source rule versions;
- fingerprint versions;
- fixed Belay Attention severity/priority;
- admission and fixed presentation codes.

The catalog stores raw reviewed version `1.1`; tests derive and verify its
existing opaque display digest. Source severity does not control family
ranking for mapped upstream families.

Initial mapping:

```text
mapping_key: attention.agent_guardrails_configuration
mapping_version: 1
grouping_version: 1
origin: numbat
title_code: issue.numbat_finding
category: numbat_finding
source_signal_code: tamper.guardrails_off
raw_rule_version: 1.1
fingerprint_version: 1
attention_kind: issue
attention_severity: low
```

Future rule versions remain inspectable-only until reviewed.

### 5.2 Family classification

`exact_issue` family:

- one stable Belay-native issue;
- internal group key `exact:<issue_id>`;
- opens exact detail directly.

`mapped_upstream` family:

- all matched issue summaries admitted by the same mapping key/version and
  fingerprint version;
- internal group key contains only fixed catalog identifiers;
- family counts describe retained records, not recurrence.

Unknown upstream and mixed/incompatible summaries are omitted from family
Attention but remain in exact reads.

### 5.3 Query-time repository

Add `QueryAttentionFamilies` and `QueryAttentionFamilyMembers` to the issue
repository capability.

The list query uses the existing issue materialized snapshot transaction:

1. select visible exact issue summaries at the frozen generation;
2. apply normalized user filters to member rows;
3. classify each row as exact, mapped, or excluded;
4. for upstream admission, require every selected member's occurrence revisions
   visible at the frozen snapshot to resolve to retained findings with the
   same session, exact source rule ID, and reviewed raw rule version; any
   missing or mismatched finding excludes the entire exact member;
5. aggregate classified rows by internal family key;
6. order families and apply `LIMIT + 1`.

No family rows are persisted.

Family order:

1. Belay-owned family severity rank descending;
2. matched supporting issue count descending;
3. last observed descending;
4. internal family key ascending.

Deterministic aggregate formulas:

- mapped severity: fixed mapping severity;
- exact severity: exact summary severity;
- confidence: minimum member confidence;
- first/last observed: min/max;
- analysis status precedence: failed, pending, truncated, current;
- evidence complete: true only when every matched member is complete;
- retained-history-only: true only when every matched member is history-only;
- supporting issue count: distinct matched exact issues;
- occurrence count: sum of matched exact issue occurrence counts;
- session count: distinct sessions across matched exact issues;
- scope counts: matched exact issue summaries by summary scope quality;
- harnesses: sorted union across matched members;
- representative child: analysis precedence, then latest observed, then issue
  ID ascending.

Filter domains are fixed:

- harness and observed-after select visible occurrence rows;
- analysis status selects exact issue summaries;
- origin selects classified family/member origin;
- severity selects effective severity after classification (fixed mapped
  severity or exact issue severity);
- `experimental=stable` excludes experimental exact members;
  `experimental=include` permits them, while mapped admission still requires
  the catalog entry to be default-visible.

An exact issue is selected when it has at least one visible occurrence matching
the occurrence-domain filters. Issue, occurrence, and distinct session counts
are recomputed from those matched visible occurrence rows rather than copied
from unfiltered issue-summary aggregates. All displayed counts therefore
describe the filtered selection. The UI says `Observed in N retained sessions
matching these filters` when filters are active.

Evidence gaps use `attention_kind=evidence_gap` and remain a separate request,
cursor, list, and empty state.

### 5.4 Identity and cursors

Family IDs use a store-keyed domain-separated identity with prefix `atf_`.
They are derived from the internal family key and catalog/grouping version but
are not persisted.

Add a separate authenticated `attentionFamilyCursorEnvelope` and version. It
uses the existing authenticated cursor codec but is decoded independently from
exact issue cursor-v2. Exact issue cursors and continuation behavior remain
unchanged.

Family cursor kinds:

- `attention_families`;
- `attention_family_members`;
- `attention_family_view`.

Family cursors bind:

- issue cursor epoch/snapshot/retention generation/issued-at;
- normalized family filters and `attention_kind`;
- family catalog version;
- severity rank, matched issue count, last observed, and internal group key;
- selected family identity for member pagination.

A compiled catalog version change makes old family cursors return 410 through
catalog-version mismatch. Exact issue cursors are otherwise unaffected.

Mapped-family detail requires the list-provided `attention_family_view` cursor;
there is no no-cursor mapped-family lookup because the non-persisted family ID
cannot recover snapshot, filter, or internal grouping-key context. One-member
exact families bypass this route and open exact issue detail.

Member order is analysis precedence (`failed`, `pending`, `truncated`,
`current`), then `last_observed_at DESC`, then `issue_id ASC`. Continuation
cursors bind the complete position.

### 5.5 Read API

Add:

```text
GET /v1/attention-families
GET /v1/attention-families/{family_id}
```

Family list supports:

- limit/cursor;
- severity;
- harness;
- origin;
- analysis status;
- observed after;
- attention kind;
- experimental stable/include.

It intentionally does not expose recurrence or fingerprint filters.

The default family Attention UI also removes the existing `Session spread` and
`Category` controls, clears any stale in-memory values during upgrade, omits
them from requests and active-filter copy, and does not persist them. Those
filters remain available only on the existing exact technical issue API; they
are not shown on the default family or evidence-gap browser surfaces.

Family summary includes:

- family ID/kind;
- mapping metadata;
- representative exact issue ID;
- fixed catalog;
- matched issue, occurrence, and session counts;
- harnesses and scope-quality counts;
- timestamps, confidence, analysis and evidence state;
- view cursor.

List and detail responses also return the existing unfiltered
`global_analysis_coverage` calculated from the same issue snapshot.

Family detail includes the same summary plus paginated exact child summaries
from the same snapshot. It returns an exact `issue_view` cursor suitable for
opening a selected child through the existing exact issue route.

Mapped-family detail accepts exactly two request forms:

- list-to-detail read: required `view_cursor` plus optional `limit`;
- member continuation: `cursor` only.

Continuation cursors bind family ID, normalized filters, catalog version,
effective limit, snapshot, and member ordering position. Validation order is:
request shape and syntax (400), authenticated cursor/catalog/snapshot/retention
validation (410 when expired), then family existence (404). A stale cursor never
turns into a misleading 404.

### 5.6 Exact occurrence privacy revision

Before alpha, exact issue detail advances `projection_version` from
`belay.issue.v1` to `belay.issue.v2` and replaces direct canonical occurrence
serialization with a presentation DTO.

The v2 DTO retains:

- occurrence, issue, and fingerprint IDs;
- fingerprint version;
- session ID and harness;
- origin;
- sanitized detector provenance;
- category, title code, and safe source signal code;
- timestamps, severity, confidence, and scope quality;
- analysis status;
- evidence completeness, retained-history-only, and experimental state;
- cited canonical event IDs.

It removes:

- `origin_record_id`;
- raw evidence dimensions;
- analysis generation;
- private fingerprint scope and storage internals.

This is an explicit pre-alpha privacy-breaking HTTP revision. Read API docs and
browser fixtures update together. Exact identity and MCP strict outputs do not
change; MCP continues using its separate allowlisted projection. Exact cursor-v2
does not need a format or epoch change. A valid short-lived exact cursor may
continue against the same frozen snapshot and receives the safer v2
presentation.

### 5.7 Browser

Default mapped family card:

```text
Fewer approval prompts enabled
Observed in 10 retained sessions · Claude Code · last observed 14h ago

Review supporting sessions and cited evidence
```

Disclosure:

> Grouped by one known signal type. The retained records may come from
> unrelated sessions or projects and do not establish recurrence or one cause.

Family detail:

1. what Belay observed;
2. fixed limitation;
3. session/harness/scope-quality summary;
4. exact affected-session records;
5. select one record to inspect cited evidence.

Child rows show:

- harness and observed time;
- occurrence/session spread;
- analysis and evidence state;
- plain-language scope reliability;
- `Inspect cited evidence`.

Opaque IDs and repeated family title stay out of primary child copy.

States:

- loading: skeleton/status without stale counts;
- empty complete: `No supported signals were reported in completed retained
  analysis`, based on `global_analysis_coverage`;
- empty incomplete: visible analysis qualifier;
- family query failure: `Attention grouping is unavailable; Sessions and exact
  retained data remain available`;
- child failure: local error preserving other children;
- 410: clear all family/member state, refresh parent list, require reselection;
- load-more: polite announcement with new family/member count.

Fix workflow state:

- hide creation controls while eligibility/history load;
- show section if eligibility succeeds, history exists, or resumable draft
  exists;
- eligibility error stays actionless;
- history failure is `history unavailable`, never `no history`;
- family detail never loads fix data.

Accessibility:

- family cards and child rows are each one button without nested controls;
- headings receive focus on open;
- back restores child, then family, then list focus;
- stale elements are removed before focus restoration;
- status is not color-only;
- touch targets are at least 44px;
- one vertical mobile scroll and no nested scroll traps.

### 5.8 MCP boundary

P0-07 changes no MCP tools, schemas, handlers, startup capabilities, or server
version. Golden compatibility tests cover:

- exactly nine names;
- unchanged strict input/output schemas;
- unchanged exact unknown-upstream visibility;
- family failures have no effect on any MCP call.

P0-07 is browser-first. Belay MUST NOT claim MCP/browser product parity or an
MCP-primary alpha until P0-09.

## 6. Low Level Design

### Canonical and catalog

- Add family models, filters, positions, pages, and validation under
  `internal/canonical/model`.
- Add fixed mapping registry under `internal/canonical/sourcecatalog`.
- Add domain-separated derived family identity without schema changes.

### Storage

- Add `attention_family_repository.go`.
- Reuse `issueMaterializedSnapshot`.
- Build SQL classification/grouping from visible issue-summary revisions,
  member relation tables, occurrences, and findings.
- Add indexes only if query-plan measurement proves existing indexes
  insufficient; any index-only migration requires a separate reviewed change.
- Keep exact projection, reconciliation, mutation guards, and retention writes
  untouched.

### Readmodel and HTTP

- Add family service DTOs and request validation.
- Add a separate authenticated family cursor envelope; do not change exact
  issue cursor-v2 decoding.
- Add two GET-only handlers with exact query allowlists.
- Reuse existing loopback auth and fixed error mapping.
- Introduce the `belay.issue.v2` sanitized exact occurrence DTO without changing
  exact cursor-v2 or MCP strict DTOs.

### Browser

- Add family list/detail state and rendering.
- Remove default-browser Session spread and Category controls/state from both
  family and evidence-gap requests.
- Preserve exact child navigation and existing issue detail.
- Add fixed configuration-evidence display.
- Implement complete fix visibility state matrix.
- Update accessibility and route contract fixtures.

## 7. Monitoring

No product telemetry is added.

Focused local diagnostics may record only fixed counts/durations:

- family query duration;
- matched/excluded family counts;
- catalog version;
- fixed query failure code.

No signal code, issue ID, session ID, path, or upstream prose enters ordinary
logs.

## 8. Open Questions

No blocking questions.

The global mapped family is an intentional product rollup, not a recurrence
identity. The exact child list and scope disclosure preserve the distinction.

## 9. Task Breakdown

1. Shared mapping catalog, canonical family contracts, and identity.
2. Query-time family repository and snapshot pagination.
3. Readmodel, cursors, and HTTP routes.
4. Browser family list/detail and exact child drill-down.
5. Fixed configuration-evidence rendering and fix-state gating.
6. Contract/docs updates and focused compatibility verification.
7. Independent integration review.
8. Founder manual QA on the retained dataset.

## 10. Manual QA Gate

1. Ten guardrail cards become one family card.
2. The card says it was observed in ten retained sessions—not ten recurrences.
3. Detail exposes all exact affected-session records.
4. Selecting a child opens existing cited evidence.
5. Primary evidence says `Fewer approval prompts enabled`; raw
   `config.agent` is technical-only.
6. Unknown upstream findings are absent from default family Attention.
7. Evidence gaps remain separately counted and paginated.
8. Family detail has no fix action.
9. Ineligible exact issues show no empty/actionable fix workflow.
10. Browser family failures do not affect Sessions, exact issue APIs, or MCP.
11. Existing nine MCP tools and exact issue reads remain functional.

## 11. P0-07.1 Manual-QA Correction: Actionable Attention

Founder QA rejected the first family-detail experience because it moved ten
visually duplicated cards into a detail page and exposed implementation
language instead of helping a developer decide what to do.

This correction changes presentation and adds bounded member context. It does
not change family identity, issue fingerprints, detector behavior, evidence
retention, MCP, or mutation contracts.

### 11.1 Product contract

The default browser experience MUST answer four questions in this order:

1. What did Belay find?
2. Why might it matter?
3. What should the developer do next?
4. Which sessions and cited observations support it?

Customer-facing copy MUST use the Belay product voice. It MUST NOT expose
`Numbat`, upstream/mapping/catalog vocabulary, raw signal codes, fingerprints,
scope quality, recurrence terminology, or fixed/generated implementation
qualifiers in default cards and explanations.

For `tamper.guardrails_off` raw rule version `1.1`, the reviewed Belay-owned
copy is:

- title/meaning: `Fewer approval prompts enabled`;
- observed fact: `Belay recorded a setting that lets actions already permitted
  by the agent run without asking for approval each time.`;
- limitation: `You may have enabled this intentionally. This finding does not
  show that an unsafe action occurred.`;
- action represented by `review_agent_permissions`: inspect the cited evidence
  and review the current permission mode; no change is needed when the mode was
  intentional.

This is permission-posture context. It does not claim malicious tampering,
unsafe behavior, or current configuration beyond the retained observation.

Every customer-copy catalog entry MUST pass the same four-part truthfulness
gate before it can be primary UI:

1. observed fact supported by retained evidence;
2. fixed meaning stated without cause or intent inference;
3. explicit limitation;
4. bounded evidence/review action, including no-action guidance where
   appropriate.

An imported source signal without a reviewed mapping MUST use the API catalog
status `unknown` and fixed copy:

- title: `Imported finding—not yet explained by Belay`;
- observation: `Belay retained this imported finding but does not yet have a
  reviewed explanation.`;
- limitation: `Review the cited evidence; Belay does not infer its impact or
  recommend a change.`

It MUST NOT receive a generic diagnosis or recommended change. Primary UI MUST
suppress that explanation; raw source-signal provenance may remain in
technical API fields but MUST NOT appear in primary UI.

### 11.2 Affected-session contract

Family detail remains a bounded page of exact issue members. For presentation,
each member row includes context from its deterministic latest matching
session, selected by occurrence `last_observed_at` descending, then session
key, occurrence ID, and revision ID ascending. A row MUST NOT imply that this
representative session is the member's only session: the exact issue
`session_count` remains authoritative, and `session_selection` is
`latest_matching_session`.

Each row MUST be distinguishable without opening it and contain:

- harness display name;
- the selected session's first retained activity date/time;
- a short local session identifier;
- cited evidence count for the member's matching occurrences in that selected
  session;
- cited evidence date or date range;
- one action: `Review evidence`.

The row MUST NOT lead with projection freshness, evidence completeness, scope
quality, exact-match identity, or issue occurrence counts. Those remain
available only where required in technical detail or API contracts.

The family member page applies its cursor and `LIMIT + 1` before session/event
enrichment. Family-list queries MUST NOT join or scan session events.

Session and cited-evidence context is computed from already retained minimized
events. Family membership, member ordering, and selected matching session are
bound to the frozen issue snapshot. Activity bounds and cited-event
availability reflect currently retained event rows and can become unavailable
after retention; they do not claim a second atomic event snapshot. No prompts,
completions, file contents, command output, raw finding prose, or new telemetry
may be added.

### 11.3 Session surfaces

Known supported findings in the Sessions overview MUST use the same Belay title
and MUST collapse duplicate raw finding cards for the same supported signal
within a session. Raw source rule identifiers remain internal/technical data.

### 11.4 Corrected manual QA gate

1. Attention shows one plain-language card for the supported warning.
2. No default Attention, issue-detail, or Sessions finding copy contains
   `Numbat`, `upstream`, `mapped`, `catalog`, `fingerprint`, `scope`, or
   `recurrence`.
3. Family detail states the observed fact, meaning, limitation, and bounded
   review action without claiming unsafe behavior.
4. Supporting rows are distinguishable by selected session date/identifier and
   cited evidence date/count; a multi-session exact issue is explicitly marked
   `latest_matching_session` and retains its full `session_count`.
5. Opening a session row still reaches its cited evidence.
6. A session with repeated copies of the supported raw finding shows one
   consolidated Belay warning.
7. Existing privacy, cursor, exact issue, and MCP contracts remain unchanged.
8. Unsupported imported signals are absent from primary Attention
   explanations, while technical API provenance remains available.
