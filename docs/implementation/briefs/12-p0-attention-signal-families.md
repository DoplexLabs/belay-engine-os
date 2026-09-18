# Implementation Brief: P0-07 Attention Signal Families

## How to Use This Brief

Before coding, read this brief and all references. Implement tasks in order,
follow existing repository/readmodel/browser patterns, and add focused tests
with each contract. Report changed files, verification, and deviations.

## Service

Belay Local issue repository, readmodel, loopback HTTP browser, and MCP
compatibility boundary.

## Summary

Add a query-time signal-family view over the existing materialized exact issue
snapshot. Collapse the launch dataset's ten mapped guardrail cards into one
truthful browser family without adding a migration or changing exact issue,
recurrence, fix-monitoring, or MCP contracts.

## Reference Materials

- `docs/design/p0-07-attention-signal-families.md`
- `docs/launch/individual-alpha-value-roadmap.md`
- `docs/design/p0-01-issue-fingerprints-and-detectors.md`
- `docs/design/p0-02-attention-inbox-and-matching-sessions.md`
- `docs/design/p0-05-mcp-issue-evidence.md`
- `docs/design/p0-06-value-first-local-ux.md`
- `docs/contracts/read-api-v1.md`
- `docs/contracts/mcp-v1.md`
- `internal/storage/local/issue_repository.go`
- `internal/presentation/readmodel/issue_cursor.go`
- `internal/presentation/localhttp/assets/app.js`

## Contracts

- Existing issue snapshot metadata is the family snapshot authority; admission
  uses occurrence revisions visible at that exact snapshot.
- Shared catalog is the sole upstream admission authority.
- Raw retained finding version `1.1`, not the short display digest alone, is
  required for initial upstream admission.
- Family counts describe filtered retained records, not recurrence.
- Exact issue identity/summary behavior and nine MCP tools remain unchanged.
  Exact HTTP occurrence presentation advances to privacy-closed
  `belay.issue.v2`.

## Tasks

### 1. Catalog, model, and identity

- Add fixed source catalog and family models.
- Add store-keyed `atf_` derivation without schema changes.
- Add a separate authenticated family cursor envelope/version and catalog
  binding; leave exact issue cursor-v2 decoding unchanged.

Acceptance:

- Only exact reviewed mapping inputs admit upstream issues.
- Catalog changes invalidate family cursors.
- Existing exact issue cursor-v2 behavior remains unchanged.

### 2. Query-time family repository

- Reuse the materialized issue snapshot.
- Classify/filter/group before `LIMIT`.
- Join occurrence revisions visible at the frozen snapshot to findings for
  same-session, exact rule-ID, and exact raw-version validation; exclude the
  entire member on any missing/mismatched finding.
- Add same-snapshot member pagination.
- Keep evidence-gap pagination independent.
- Apply occurrence-domain harness/time filters before recomputing exact issue,
  occurrence, and distinct session counts; apply severity only after family
  classification.

Acceptance:

- Ten launch-data exact issues return one family and ten members.
- Unknown/incompatible upstream issues are excluded.
- Filters and counts describe matched members.
- Responses include unfiltered global analysis coverage from the same snapshot.
- Family failure does not touch ingestion or exact reads.

### 3. Readmodel and HTTP

- Add list/detail DTOs, validation, separate family cursors, fixed catalogs, and
  two GET routes.
- Require list-provided `view_cursor` plus optional limit for initial mapped
  detail; allow cursor-only continuation; do not support no-cursor mapped-family
  lookup. Bind family key/ID, filters, attention kind, catalog, snapshot, limit,
  and member position. Enforce 400/410-before-404 precedence.
- Add sanitized `belay.issue.v2` exact occurrence DTO retaining
  fingerprint/provenance/citations while removing origin record, raw
  dimensions, analysis generation, and private internals.

Acceptance:

- Family list/detail share one issue snapshot.
- Exact child gets a valid same-snapshot exact issue view cursor.
- No family-level mutation or evidence hydration exists.

### 4. Browser family UX

- Replace default issue cards with family cards.
- Remove and clear the default Attention `Session spread` and `Category`
  filters; never send or display them for family/evidence-gap requests.
- Exact one-member cards open exact detail directly.
- Multi-member mapped cards open family detail.
- Render useful child identity and fixed scope disclosure.
- Add full loading/empty/error/410/focus/mobile states.

Acceptance:

- One guardrail card replaces ten duplicates.
- Copy avoids `Numbat`, `exact record`, and recurrence language in primary
  value.
- Every child remains inspectable.

### 5. Evidence and fix-state polish

- Map family-context `config.agent` evidence to fixed user language.
- Keep raw event type under technical details only.
- Hide fix creation until eligible.
- Render history-only and failure states truthfully.

Acceptance:

- No primary `config.agent` rows.
- No misleading family or ineligible fix CTA.

### 6. Compatibility and docs

- Update read API and manual QA docs.
- Add golden MCP isolation tests.
- State that MCP/browser family parity is a P0-09 release dependency.

## Focused Testing

- mapping admission/non-admission, including raw rule version;
- grouping, aggregate formulas, filters, evidence gaps, ordering;
- occurrence-domain filtered counts, effective-severity filtering, and
  experimental inclusion;
- family/member cursor tamper, expiry, snapshot, retention, catalog mismatch;
- 10-to-1 real-shape fixture;
- browser list/detail/child/focus/error/mobile fixtures;
- removal of Session spread/Category controls, request fields, persisted state,
  and active-filter copy;
- fixed configuration evidence and fix-state matrix;
- privacy-closed exact occurrence v2, unchanged exact cursor-v2 behavior, and
  exactly nine unchanged MCP tools;
- bounded query-plan/scale fixture over at least 25,000 issue summaries.

## Sequencing

1. Backend catalog/model/repository.
2. Readmodel/HTTP contract.
3. Browser after contract freeze.
4. Independent review.
5. Founder manual QA before P0-08.

## Risks

- Never change reconciler fingerprint material.
- Never label family membership recurrence.
- Do not remove fingerprint/provenance/citation fields required by exact browser
  and MCP behavior; remove only the prohibited v2 fields.
- Do not make family capability required by MCP startup.
- Do not silently fall back to duplicated exact cards if family reads fail.

## P0-07.1 Founder-QA Correction

Manual QA rejected the initial browser presentation. Apply the design addendum
in `docs/design/p0-07-attention-signal-families.md` before P0-08.

- Replace all default customer-facing Numbat/upstream/mapping/fingerprint/scope/
  recurrence copy with the fixed Belay product explanation.
- Use the reviewed `tamper.guardrails_off` v1.1 copy:
  `Fewer approval prompts enabled`; `Belay recorded a setting that lets actions
  already permitted by the agent run without asking for approval each time.`;
  `You may have enabled this intentionally. This finding does not show that an
  unsafe action occurred.` Direct the developer to inspect evidence and review
  the current permission mode, with no action when intentional.
- Evidence copy MUST say `This cited event recorded the session starting in or
  switching to a permission mode that asks for fewer approvals. Belay does not
  retain the configuration value or body.` It MUST NOT expose raw event type
  names.
- Require every customer explanation to include observed fact, meaning,
  limitation, and bounded action. Unsupported imported signals retain
  technical API provenance and `unknown` catalog status, use
  `Imported finding—not yet explained by Belay`, and receive no generic
  diagnosis or recommended change in primary UI.
- Add bounded family-member context from retained event relations after member
  `LIMIT + 1`: deterministic latest matching session, session activity bounds,
  cited event count, and cited evidence bounds.
- Preserve the exact member's full `session_count` and emit
  `session_selection=latest_matching_session`; never imply the selected session
  is the only session represented by an exact issue.
- Do not add session-event joins to family-list queries. Member identity/order
  and selected session use the frozen issue snapshot; event activity/evidence
  availability reflects current retention and is not a second atomic snapshot.
- Render a compact affected-session list with meaningful date and evidence
  differences instead of repeated issue-analysis cards.
- Consolidate duplicate instances of the known supported warning in each
  Sessions overview.
- Keep raw provenance and exact identity available to internal contracts without
  placing it in the primary browser experience.

Acceptance is the corrected manual QA gate in the design, plus focused
readmodel/storage/browser contract tests.
