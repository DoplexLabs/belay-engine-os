# P0-06: Value-First Belay Local UX

- **Status:** Revised after independent security and product review
- **Scope:** Belay Local Attention, issue evidence, session overview, event
  timeline, and safe source-signal presentation
- **Out of scope:** raw prompts, completions, reasoning, file contents, command
  output, exact command arguments, Teams, generated diagnosis, and deployment

## 1. Overview

Belay Local currently exposes trustworthy deterministic evidence, but much of
the product vocabulary reflects internal implementation concepts: detector
versions, opaque fingerprints, raw event types, scope quality, and generic
upstream finding labels. A developer must reconstruct the practical meaning
from those internals.

P0-06 changes the primary experience to answer four questions in order:

1. What happened?
2. Why should I care?
3. What evidence supports it?
4. What can I inspect or do next?

Technical provenance remains available, but is secondary and collapsed by
default. The change also fixes the command-summary minimization boundary before
displaying richer command metadata.

## 2. Glossary

- **Source signal code:** A strictly validated, code-shaped identifier supplied
  by an upstream detector, for example `tamper.guardrails_off`.
- **Fixed source-signal catalog:** Belay-owned, versioned explanatory text for
  explicitly recognized source signal codes.
- **Safe command summary:** A minimized command label containing only a
  strictly validated executable basename and option names. It excludes
  positional arguments and values.
- **Technical details:** Provenance and debugging metadata such as detector
  version, fingerprint, scope quality, source rule ID, event ID, and coverage.
- **Needs attention:** Explicit failures, denied permissions, recognized
  findings, interrupted sessions, or verification gaps. It is not generated
  diagnosis.

## 3. Requirements

### 3.1 Functional

1. Attention cards must lead with a fixed human-readable observation.
2. Issue detail must present observation, limitation, cited evidence, pattern,
   and next action before technical metadata.
3. Known upstream source signals must use fixed Belay-owned titles and
   explanations.
4. Unknown or invalid upstream signals must use a neutral fallback and must not
   interpolate arbitrary upstream prose into primary UI.
5. `tamper.guardrails_off` must be presented as an observational configuration
   warning, not proof of malicious tampering.
6. Session detail must prioritize needs-attention signals, observed work,
   resources, and truthful outcome before raw counts.
7. Timeline rows must use human action labels. Command rows must prefer the
   canonical safe summary over the executable-only resource label.
8. Cited evidence must be visible near the issue explanation and remain
   explicitly bounded by retention and available coverage.
9. Fix-attempt controls must not imply recurrence monitoring is available for
   unscoped issues.
10. Analysis coverage, fingerprints, detector versions, scope quality, raw
    event types, and event IDs must remain available under technical details.

### 3.2 Privacy and security

1. No full command, positional argument, option value, environment assignment,
   shell body, redirect, substitution, path, URL query, prompt, completion,
   reasoning, output, file content, or diff may be introduced.
2. Command executable and option names must come from fixed Belay-owned
   allowlists in addition to matching strict grammars.
3. Environment-prefixed or structurally unsafe commands must fall back to a
   neutral command label.
4. Source signal codes must match
   `^[a-z0-9][a-z0-9_.-]{0,63}$`.
5. Upstream finding titles and observed-command fields must not become primary
   UI prose.
6. Every event-derived string must continue to render through text nodes.
7. HTTP and MCP must expose only explicit, bounded fields for the new source
   signal metadata.
8. Compound shell syntax, environment prefixes, shell wrappers, substitutions,
   redirects, pipes, control characters, and unknown executables must fall
   back without retaining any raw-command bytes.

### 3.3 Compatibility

1. Existing issue fingerprints and issue IDs remain unchanged.
2. Existing issue cursors expire when migration 013 rotates the issue epoch.
3. Existing six MCP tools remain unchanged.
4. The three P0-05 issue tools add only nullable, bounded metadata.
5. Existing Local databases migrate in place and rebuild the current issue
   summary projection before serving issue reads.

## 4. Current State / Background

Numbat finding `rule_id` and `rule_version` already participate in private issue
identity, but every upstream issue is projected with the same
`issue.numbat_finding` title code. The source rule is therefore lost at the
issue presentation boundary.

Canonical command mapping currently keeps an executable basename and at most
seven option names. Positional arguments and option values are discarded.
However, an environment assignment in the first token may be mistaken for an
executable, and the browser currently prefers `resource.name` over the richer
safe summary.

The browser already receives:

- fixed issue catalog statements and caveats;
- issue occurrence and cited-event metadata;
- session overview counts and salient resources;
- truthful outcome and reconstruction metadata.

The primary gap is information hierarchy, plus the missing source-signal code
needed to explain known upstream findings.

## 5. High Level Design

```text
Numbat finding
   |
   | validated rule_id only
   v
IssueOccurrence.source_signal_code
   |
   v
revisioned issue summary projection
   |
   +--> HTTP/MCP issue DTOs
   |
   v
fixed Belay source-signal catalog
   |
   v
value-first Attention presentation

Numbat command record
   |
   | strict executable/option-name minimization
   v
canonical observation.summary
   |
   v
human timeline row
```

### 5.1 Source signal metadata

Add nullable `source_signal_code` to issue occurrences and summaries. It is
populated only for a safe code-shaped upstream rule ID. It is not included in
fingerprint material because the existing fingerprint already includes the
original rule ID and version.

Summary aggregation sets the field only when all visible occurrences agree.
Disagreement fails closed to `null`.

Migration 013 is implemented as an authorized, crash-resumable Go migration:

1. adds nullable columns with database constraints to `issue_occurrences` and
   `issue_summary_revisions`;
2. enters the existing `projection_rebuild` mutation authorization scope;
3. marks issue summaries not-ready, clears materialized revisions and migration
   progress, and backfills safe Numbat codes by joining `origin_record_id` to
   `findings`;
4. rebuilds current summaries in resumable batches;
5. sets `materialized_generation=current_generation`, assigns a newly generated
   cursor epoch, and marks readiness `ready` in one final transaction;
6. leaves invalid, missing, legacy, and Belay-origin values null.

Startup resumes after interruption. A completed migration-012 marker cannot
skip the migration-013 rebuild. Aggregation emits a source code only when
`COUNT(source_signal_code)=COUNT(*)` and `MIN(source_signal_code)=
MAX(source_signal_code)`; otherwise it emits null.

### 5.2 Fixed source-signal catalog

Catalog lookups use `(origin, source_signal_code)` and return fixed content.

Initial known signal:

- `numbat/tamper.guardrails_off`
  - Title: `Agent safety confirmations may be disabled`
  - Observation: `Numbat reported retained configuration evidence associated
    with disabled agent guardrails.`
  - Limitation: `This does not prove malicious tampering, identify who changed
    the configuration, or establish that an unsafe action occurred.`
  - Next action: `Review the cited configuration evidence and the agent's
    permission settings.`

Unknown safe codes use this complete fixed fallback:

- Title: `Upstream Numbat finding`
- Observation: `A configured Numbat rule reported retained evidence.`
- Limitation: `Belay does not interpret this source rule and does not infer
  cause, impact, or remediation from its identifier.`
- Next action: `Inspect the cited events and source rule ID.`

Invalid codes use the same fallback without exposing the invalid value. Raw
safe codes appear only under technical details.

The existing `belay.issue-explanations.v1` catalog contract remains
authoritative. Its exact additive shape is:

```json
{
  "catalog_version": "belay.issue-explanations.v1",
  "catalog_status": "known",
  "title_code": "issue.numbat_finding",
  "display_title": "Agent safety confirmations may be disabled",
  "observation_statement": "fixed text",
  "caveat": "fixed text",
  "next_evidence_action": "review_agent_permissions",
  "source_signal_code": "tamper.guardrails_off",
  "source_signal_catalog_version": "belay.source-signals.v1",
  "source_signal_catalog_status": "known"
}
```

`source_signal_code` is required-but-nullable on summary, occurrence, catalog,
HTTP, and MCP issue objects. Catalog status is
`known|unknown|not_applicable`. `display_title` and narrative fields are always
fixed Belay content. MCP implementation version becomes `1.2.0`.

### 5.3 Safe command summaries

The canonical mapper accepts an executable only when:

- the command contains no shell metacharacters, control characters, newline,
  substitution, redirect, or compound syntax;
- its first token is not an assignment or an absolute path;
- it is not a wrapper such as `env`, `sh`, `bash`, `zsh`, `fish`, `sudo`, or
  `xargs`;
- its basename matches `^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`; and
- the basename belongs to the fixed display allowlist.

The initial executable allowlist covers common developer tools already observed
by Belay, including Git, Go, Node package managers, Cargo/Rust, Python/pytest,
TypeScript/lint/format tools, ripgrep/grep, sed, find, pwd, ls, cat, head, tail,
wc, jq, gh, and make. Unknown tools use a neutral command representation.

Option names are displayed only when they exactly match a fixed global safe
option allowlist and
`^-{1,2}[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`. Inline and separated values are
never displayed. Names that merely resemble sensitive values, including
arbitrary `-D...` and token/password variants, are not allowlisted.

The browser displays:

1. `observation.summary`;
2. otherwise safe `resource.name`;
3. otherwise `Command activity`.

Exact command capture is not added in P0-06.

### 5.4 Attention hierarchy

Attention list:

1. fixed human title;
2. session spread, harnesses, and recency;
3. compact reliability qualifier;
4. fixed inspect action.

Issue detail:

1. observed fact;
2. important limitation;
3. cited evidence preview;
4. matching-session pattern;
5. next inspection action;
6. fix-attempt history when recurrence is supportable;
7. collapsed technical details.

The first viewport order is:

1. Issues;
2. Evidence gaps;
3. After attempts, hidden when empty;
4. compact analysis status and advanced filters in disclosures.

The whole issue card remains one accessible button; it does not contain nested
action buttons.

Issue selection automatically loads at most three cited events from the newest
occurrence, ordered canonically. The preview:

- is cancellable when selection changes;
- does not move focus;
- distinguishes retained and missing IDs;
- states that event hydration uses current retention rather than the frozen
  issue snapshot;
- provides an explicit `Inspect all cited evidence` action;
- clears data, loading, error, and missing states on close or HTTP 410.

### 5.5 Session hierarchy

Session detail:

1. truthful outcome status;
2. needs-attention summary;
3. observed work;
4. salient resources;
5. condensed key sequence;
6. expandable full retained timeline.

The key sequence contains at most five highlights from the currently loaded
events, in canonical order. Selection priority is explicit failures, denied
permissions, findings-linked events, command results, commands, file writes,
then other tool calls. Duplicate lifecycle events are excluded. The UI labels
the result `Highlights from the currently loaded retained events` and never
implies full-session semantic coverage.

No semantic task name, root cause, success inference, or remediation is
generated.

## 6. Low Level Design

### 6.1 Canonical and analysis

- Add nullable `SourceSignalCode` to `model.IssueOccurrence` and
  `model.IssueSummary`.
- Add a shared safe source-code validator.
- In Numbat reconciliation, copy safe `finding.RuleID`.
- Keep fingerprint material unchanged.
- Harden `commandName` and `commandSummary` against environment assignments,
  shell fragments, unsafe executable tokens, and malformed option names.

### 6.2 Storage

- Add migration `013_value_first_source_signals.sql`.
- Extend occurrence and summary revision writes/reads.
- Backfill only safe Numbat rule IDs.
- Rotate the persisted issue cursor epoch.
- Rebuild current summaries and coverage using the migration-012 readiness
  mechanism.
- Extend migration authorization so 013 may perform only the named projection
  rebuild operations.

### 6.3 Readmodel and MCP

- Extend issue summary, occurrence, catalog, and MCP DTOs with nullable bounded
  source-signal metadata.
- Add fixed catalog version `belay.source-signals.v1`.
- Keep all narrative content fixed.
- Add the nullable field to recursively closed MCP schemas.
- Preserve `catalog_version=belay.issue-explanations.v1` and add the exact
  source-signal fields defined above.

### 6.4 Browser

- Replace detector-centric titles with catalog titles.
- Move cited evidence directly below the explanation.
- Collapse technical provenance.
- Suppress only the new fix-attempt CTA when the server reports ineligibility.
  Preserve durable history, retraction, retained drafts, and history-only
  detail regardless of current issue eligibility.
- Prefer safe command summary in event rows.
- Reframe session overview into Needs attention, Work observed, Resources, and
  Outcome.
- Technical event disclosure uses an explicit allowlist only: event ID,
  occurred time, human action, outcome, exit code, duration, safe summary,
  safe resource kind/name, coverage depth/confidence, historical state, and
  redaction counts. It never renders raw canonical JSON, installation IDs,
  source run/record IDs, deduplication keys, tool-call IDs, or diff hashes.
- Prevent nested scroll traps at narrow/mobile widths; the page owns vertical
  scrolling and disclosures expand in-flow.

## 7. Monitoring

No network service or production alarm is introduced. Local QA diagnostics
should expose migration readiness and catalog fallback counts through existing
Local diagnostics only; no telemetry is added.

## 8. Open Questions

None block implementation. Exact-command Local-only sensitive mode is a
separate future design requiring explicit consent and a privacy-contract
revision.

## 9. Task Breakdown

1. Canonical safety and source-signal contract.
2. Migration 013 and materialized summary propagation.
3. Readmodel/MCP fixed catalog propagation.
4. Attention value-first redesign.
5. Session story and timeline redesign.
6. Focused migration/restart, cursor, privacy, MCP-schema, and browser-state
   checks.
7. Independent privacy, storage, and UX review.
8. Quick compile/syntax checks and manual QA binary.

## 10. Appendix

Relevant prior designs:

- `docs/design/p0-01-issue-fingerprints-and-detectors.md`
- `docs/design/p0-05-mcp-issue-evidence.md`
- `docs/contracts/transmitted-fields-v1.md`
