# Local MCP V1 Contract

- **Status:** Governed Local Alpha evidence and experience surface
- **Local protocol:** V1
- **Implementation version:** `1.7.0`
- **Hosted:** V1.1

## Boundary

MCP exposes Belay's local read contract plus bounded, explicit user-governed
records and lifecycle decisions. Belay supplies bounded structured evidence;
the developer's configured calling agent decides how to interpret it.

Belay does not:

- call or orchestrate a model;
- execute a command, write a file, or modify an agent;
- expose P0-04 recurrence monitoring through MCP.

The fully configured Local server advertises these tools:

1. `list_sessions`
2. `get_session`
3. `get_session_timeline`
4. `query_activity`
5. `list_findings`
6. `get_stats`
7. `list_issues`
8. `get_issue`
9. `lookup_session_events`
10. `get_top_issues`
11. `get_issue_excerpts`
12. `get_fix_status`
13. `get_mission_pack`
14. `record_mission_pack_accepted`
15. `get_mission_pack_status`
16. `list_experience_proposals`
17. `list_active_experiences`
18. `approve_experience`
19. `resolve_experience_proposal`
20. `prepare_experience_lifecycle`
21. `apply_experience_lifecycle`
22. `propose_fix`
23. `record_fix_applied`

Read tools perform no mutation. State-changing tools are non-destructive,
closed-world operations that write only bounded records or lifecycle
transitions to Belay's encrypted local store after explicit user confirmation.
They cannot apply a diff, write a project file, execute a command, or authorize
remediation. No prompts, resources, logging, completions, shell, arbitrary
filesystem, or recurrence-monitoring capability is advertised.

## Local registration

The packaged one-command path:

```text
./bin/belay quickstart
```

safely inspects detected Codex and Claude Code user-scope MCP configuration.
Claude registration may proceed automatically when its status is safely
understood. Normal quickstart does not add an absent Codex entry. The explicit
one-command path that permits that add is:

```text
./bin/belay quickstart --allow-codex-mcp-add
```

The flag permits `codex mcp add` only after Belay strictly verifies that the
`belay` entry is absent. It records the user's acceptance of the Codex CLI's
non-atomic duplicate-name behavior. Belay never invokes that add operation
against an existing entry; foreign or unverifiable entries are preserved and
never overwritten or removed.

Registration configures only the existing local stdio command:

```text
ABS_BELAY mcp
```

or, for an explicit non-default Belay home:

```text
ABS_BELAY mcp --home ABS_BELAY_HOME
```

Registration adds no tool, HTTP transport, URL, bearer token, environment
secret, shell wrapper, or working-directory override. `quickstart --no-mcp`
skips MCP inspection and registration while preserving the rest of quickstart.

The explicit configuration commands are:

```text
belay mcp-config install [--allow-codex-mcp-add] [--home PATH]
belay mcp-config status [--home PATH]
belay mcp-config uninstall [--home PATH]
```

Direct install follows the same split: Claude may be installed when its status
is safely understood, while Codex add requires
`--allow-codex-mcp-add`. Standalone install may create private Belay
configuration and directories to establish a stable installation/ownership
identity. It does not create or open the Local database and does not create
Keychain material.

For Codex, the opt-in permits add only from a strictly verified absent state.
Install does not update, migrate, replace, or remove a current, recognized
prior, foreign, unverifiable, or unavailable Codex entry. A current exact entry
is left unchanged. After an archive move, the user must inspect a recognized
prior Codex entry with `mcp-config status`, explicitly invoke
`mcp-config uninstall`, verify absence, and then rerun
`mcp-config install --allow-codex-mcp-add`.

Install and uninstall mutate only entries allowed by their explicit action and
the exact ownership checks. A foreign, scope-ambiguous, or unverifiable entry
named `belay` is preserved. Unknown host-CLI status formats fail closed.
Explicit uninstall removes only an exact current or previously verified
Belay-owned identity. MCP uninstall does not remove monitor hooks, Local
history, Keychain material, or the extracted package.

Registration is local and requires no Doplex service, hosted login, paid
service, or product-network request. A host agent CLI wrapper may independently
perform credential or network checks; its failure does not change this MCP tool
contract and does not prevent quickstart from opening Local.

## Recommended issue-evidence loop

1. Call `list_issues`.
2. Check `selection` and `analysis.complete`.
3. Preserve the returned `view_cursor`.
4. Call `get_issue` with the selected `issue_id` and `view_cursor`.
5. Read the fixed catalog observation and caveat.
6. Inspect exact matching occurrences and their cited event IDs.
7. Call `lookup_session_events` only for the cited IDs needed.
8. Use session, timeline, or activity tools for broader context if necessary.
9. The configured calling agent may reason over the evidence. That reasoning is
   not generated by Belay.

An exact fingerprint match means equality under Belay's compatible,
versioned, deterministic fingerprint contract. It does not establish semantic
similarity, shared intent, shared root cause, or remediation success.

## Original six backward-compatible tools

### `list_sessions`

Lists bounded session summaries.

Inputs:

- `limit` (default 20, maximum 100)
- `cursor`
- `since` (compatibility alias for `occurred_after`)
- `occurred_after`, `occurred_before`
- `harness`
- `outcome`
- `history` (`historical`, `live`, or `mixed`)
- `query` (maximum 128 bytes; session ID and harness only)

### `get_session`

Returns one session summary, source, deterministic overview, coverage,
confidence, and metric versions.

Input: `session_id`.

### `get_session_timeline`

Returns a bounded ordered event page.

Inputs: `session_id`, optional `cursor`, and `limit` (default 100, maximum
500).

### `query_activity`

Queries canonical activity using bounded time, action/resource, harness, and
outcome filters. It accepts no arbitrary SQL or regex.

Inputs: `occurred_after`, `occurred_before`, `harness`, `resource_kind`,
`outcome`, optional `cursor`, and `limit` (default 50, maximum 200).

### `list_findings`

Lists bounded local findings with supporting event IDs.

Inputs: `since`, `severity`, exact `session_id`, optional `cursor`, and `limit`
(default 20, maximum 100).

### `get_stats`

Returns versioned global Local summary metrics. It accepts no time-window or
workflow filter.

The names, input schemas, success shapes, limits, cursor behavior, and fixed
errors of these six tools remain compatible for complete JSON-RPC messages no
larger than 256 KiB.

## Issue tools

### `list_issues`

Returns bounded issue summaries from one immutable issue-projection snapshot.
Stable ordinary issues are selected by default. Verification evidence gaps and
experimental signals require explicit selection.

Inputs:

- `limit` (default 20, maximum 100)
- `cursor`
- `severity`: `info`, `low`, `medium`, `high`, or `critical`
- `category`: exact catalog category
- `harness`: case-insensitive exact match
- `origin`: `belay` or `numbat`
- `analysis_status`: `current`, `pending`, `failed`, or `truncated`
- `observed_after`: RFC3339/RFC3339Nano lower bound
- `recurrence`: `single` or `repeated`
- `session_id`: exact session identifier
- `fingerprint_id`: exact opaque fingerprint identifier
- `attention_kind`: `issue`, `evidence_gap`, or `all`; default `issue`
- `experimental`: `stable`, `include`, or `only`; default `stable`

A fresh request may include filters and an explicit limit. A continuation
contains only `cursor`; normalized filters and the effective limit are carried
inside the authenticated cursor.

The response includes:

```json
{
  "schema_version": "belay.read.v1",
  "projection_version": "belay.issue.v1",
  "data": [],
  "analysis": {
    "current_sessions": 0,
    "pending_sessions": 0,
    "failed_sessions": 0,
    "truncated_sessions": 0,
    "unscoped_sessions": 0,
    "analysis_through": "2026-09-08T18:05:01Z",
    "complete": true
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

Every issue summary contains required-but-nullable
`source_signal_code`. Non-null values are limited to Numbat-origin identifiers
matching `^[a-z0-9][a-z0-9_.-]{0,63}$`; Belay-origin, invalid, missing, legacy,
or disagreeing values are `null`. The identifier is untrusted technical
metadata and does not alter Belay's narrative.

When `analysis.complete=false`, an empty result means only that no matching
issue is available from completed analysis. It is not a claim that no issue
exists.

### `get_issue`

Returns one issue summary, a bounded occurrence page, fixed catalog metadata,
and global retained-session analysis coverage from the same frozen issue
snapshot.

Inputs:

- required `issue_id`;
- optional `limit` (default 20, maximum 100);
- optional `view_cursor` for list-to-detail transfer;
- optional `cursor` for occurrence continuation.

`cursor` and `view_cursor` are mutually exclusive. A continuation contains
only `issue_id` and `cursor`; its limit and snapshot are cursor-bound.

The additive readmodel shape is:

```json
{
  "schema_version": "belay.read.v1",
  "projection_version": "belay.issue.v1",
  "data": {
    "issue": {
      "source_signal_code": null
    },
    "occurrences": [
      {
        "source_signal_code": null
      }
    ]
  },
  "catalog": {
    "catalog_version": "belay.issue-explanations.v1",
    "catalog_status": "known",
    "title_code": "issue.explicit_command_failure",
    "display_title": "Command failed",
    "observation_statement": "The source explicitly reported a failed command result.",
    "caveat": "A reported command failure does not by itself establish root cause or whether a later attempt succeeded.",
    "next_evidence_action": "inspect_cited_events",
    "source_signal_code": null,
    "source_signal_catalog_version": "belay.source-signals.v1",
    "source_signal_catalog_status": "not_applicable"
  },
  "global_analysis_coverage": {
    "current_sessions": 0,
    "pending_sessions": 0,
    "failed_sessions": 0,
    "truncated_sessions": 0,
    "unscoped_sessions": 0,
    "analysis_through": "2026-09-08T18:05:01Z",
    "complete": true
  },
  "view_cursor": "opaque-rowless-view-cursor",
  "next_cursor": null,
  "has_more": false,
  "returned_count": 0,
  "limit": 20
}
```

Catalog text is fixed, versioned presentation content. Its
`next_evidence_action` values navigate evidence; they do not recommend a fix.
The enum is `inspect_cited_events`, `inspect_matching_sessions`,
`inspect_verification_events`, or `review_agent_permissions`. Unknown future
title codes receive neutral fixed fallback text.

MCP projects these authoritative readmodel catalog fields unchanged. It does
not derive titles, explanations, caveats, actions, or source-signal semantics
from issue metadata. The recursively closed output schema strictly requires
`catalog_version=belay.issue-explanations.v1` and the complete catalog shape
shown above; an incompatible or malformed readmodel catalog produces the fixed
`belay_mcp/read_failed` tool error rather than a reconstructed response.

For the imported source signal `tamper.guardrails_off`, the catalog returns the
fixed Belay-owned title `Fewer approval prompts enabled`, the observation
`Belay recorded a setting that lets actions already permitted by the agent run
without asking for approval each time.`, and the caveat `This setting may be
intentional. The record does not show whether an action bypassed a prompt or
caused harm.` It also returns
`next_evidence_action=review_agent_permissions` and
`source_signal_catalog_status=known`.

Other imported source codes without a reviewed mapping return the fixed title
`Imported finding—not yet explained by Belay`, the neutral observation `Belay
retained this imported finding but does not yet have a reviewed explanation.`,
the caveat `Review the cited evidence; Belay does not infer its impact or
recommend a change.`, `next_evidence_action=inspect_cited_events`, and
`source_signal_catalog_status=unknown`. A safe source code may remain structured
technical metadata, but it is never interpolated into customer narrative or
errors. Invalid or unavailable source codes use the same fallback with
`source_signal_code=null`. Issues without an imported source signal use
`source_signal_catalog_status=not_applicable`.

### `lookup_session_events`

Retrieves only explicitly requested canonical event IDs from one explicitly
selected session.

Inputs:

- `session_id` (required, maximum 256 bytes);
- `event_ids` (1–50 lowercase canonical UUIDv7 values).

Duplicate IDs are deduplicated. Missing IDs preserve first-request order;
returned events use canonical source order. An unknown session reports all
unique requested IDs as missing. Missing means unavailable from the selected
retained session; wrong-session, pruned, absent, and otherwise unavailable
evidence are intentionally indistinguishable.

The result states:

- `snapshot_scope=current_ingestion`;
- `issue_snapshot_bound=false`;
- `evidence_evaluated_at`;
- `missing_semantics=unavailable_from_selected_retained_session`.

The MCP event DTO is a recursively closed allowlist with this exact field tree:

- top level:
  - `schema_version`;
  - `event_id`;
  - `occurred_at`;
  - `observed_at`;
  - `session_id`;
  - `source`;
  - `observation`;
  - `coverage`;
  - `redaction`;
  - `historical`;
- `source`:
  - `engine`;
  - `engine_version`;
  - `schema_version`;
  - `record_type`;
  - `kind`;
  - `agent`;
  - `adapter_version`;
  - `sequence`;
- `observation`:
  - `type`;
  - `actor`;
  - `action`;
  - `outcome`;
  - optional `exit_code`;
  - optional `duration_ms`;
  - optional `summary`;
  - optional `resource`;
  - optional `details`;
- `observation.resource`:
  - `kind`;
  - `name`;
- `observation.details`:
  - optional `decision`;
  - optional `approval_required`;
  - optional `approval_decision`;
  - optional `mcp_server`;
  - optional `mcp_tool`;
  - optional `model`;
  - optional `model_provider`;
  - optional `cli_version`;
  - optional `sub_agent`;
  - optional `diff_bytes`;
  - optional bounded `tags`;
- `coverage`:
  - `depth`;
  - `confidence`;
- `redaction`:
  - `policy_version`;
  - `fields_removed`;
  - `secrets_removed`;
- `historical`:
  - `is_historical`;
  - optional `reconstruction_source`.

There is no top-level `harness` field. `session_id` remains top-level, and the
agent value appears only as `source.agent`.

Installation IDs, source run/record IDs, deduplication keys, tool-call IDs,
diff hashes, prompt bodies, transcripts, completions, reasoning, command
output, file contents, secrets, action tokens, idempotency keys, internal scope
HMACs, job tokens, and unknown future fields are structurally absent.

### `get_mission_pack`

Returns a bounded, evidence-backed proposal for one project. Its schema is
`belay.mission-pack.v1`; its deterministic generator is
`mission-pack.det.v3`.

Inputs:

- `cwd`: optional absolute project path, maximum 4096 bytes;
- `issue_id`: optional exact issue selector, maximum 512 bytes;
- `intent`: optional `general`, `debug`, `implement`, `refactor`, `review`, or
  `release`; defaults to `general`;
- `harness`: optional `claude` or `codex`;
- `task_hint`: optional active-task description, maximum 280 Unicode
  characters.

At least one of `cwd` or `issue_id` is required. Managed `/belay` calls pass
the actual host harness and pass a `task_hint` only when a concrete active task
exists; `/belay start` itself is not a task hint. Other callers may omit
`harness`, but a pack without the current harness contains no semantic
operating rules.

For an unanchored project pack, semantic correction-cluster rules require a
task hint with meaningful lexical overlap and support from at least two
distinct sessions. An explicit `issue_id` limits issue guidance to that issue
and its supported linked rule. Belay does not fill an otherwise irrelevant
pack with unrelated historical rules.

Semantic output is abstention-safe:

- stale insights are not proposed as rules;
- fixes and clusters below `0.8` confidence are omitted;
- the analyzer may omit an issue when its evidence does not support a durable
  rule;
- Claude targets are limited to Claude-compatible configuration, with
  `AGENTS.md` or Codex rule guidance safely adapted to `CLAUDE.md`;
- Codex targets are limited to Codex-compatible configuration, with
  `CLAUDE.md` guidance safely adapted to `AGENTS.md`;
- incompatible targets, including Claude settings proposed to Codex, are
  suppressed.

Verification selection is intent-aware and returns at most three commands.
Observed successful and configured-and-observed commands retain priority;
release-specific commands are promoted only for release intent.

Every non-empty pack is an inactive proposal with
`instruction_authority=none`, `evidence_state=untrusted`, and
`activation_required=true`. If no actionable trap, operating rule, or
verification command remains, the pack returns `status=empty`,
`guidance_state=unavailable`, and `activation_required=false`; clients must not
offer activation. Mission Pack preparation does not verify that a later change
held, prevented recurrence, or reduced cost.

## Governed Mission Pack and experience tools

- `record_mission_pack_accepted` records acceptance of one exact eligible
  `pack_id` after the user explicitly approves the rendered pack. It returns a
  receipt used for later status reads and does not edit project files.
- `get_mission_pack_status` reads one exact receipt and keeps instruction
  delivery, verifier evidence, and task outcome separate.
- `list_experience_proposals` returns at most five bounded proposals for an
  exact project and harness. Deferred proposals are included only when the
  caller explicitly requests early review.
- `list_active_experiences` reads at most five active, user-approved guidance
  items for an exact project.
- `approve_experience` records an explicit as-proposed, narrowed, or
  user-edited approval. Approval does not activate guidance.
- `resolve_experience_proposal` records an explicit defer or reject decision.
- `prepare_experience_lifecycle` is a read-only stale-safe preview that returns
  a single-use action token for activation or pause.
- `apply_experience_lifecycle` activates or pauses one exact experience only
  after separate explicit confirmation and a valid prepared token.

Action tokens, hidden identifiers, and evidence metadata are workflow data,
not user-facing instruction text. Clients must not infer approval, activation,
pause, receipt acceptance, or proposal disposition from ambiguous language.

## Trust wrapper and privacy

Every new-tool success uses:

```json
{
  "untrusted_observations": true,
  "trust": {
    "classification": "untrusted_observations",
    "instruction_authority": "none",
    "must_not_authorize_actions": true
  },
  "readmodel": {}
}
```

Stored strings appear only as structured data, never in tool descriptions,
fixed narrative content, errors, or diagnostics. Evidence text must not be
treated as an instruction or, by itself, as authorization to run a command or
invoke another tool.

Source signal codes are untrusted identifiers. Even when they contain
instruction-like words within the allowed code grammar, they do not change
tool descriptions, trust metadata, catalog prose, next actions, or fixed error
codes.

The governed MCP server makes no product-network request and can run without
network access. The Belay process separately supports the optional telemetry
and public release checks documented in `telemetry-v1.md` and
`update-check-v1.md`. A configured MCP client or remotely hosted model may
process or transmit tool results according to that product's policy and the
user's configuration. Belay does not control or sandbox that actor and does
not claim prompt-injection immunity.

Allowed minimized evidence can include executable/tool names, bounded option
names, project-relative paths or basenames, model/provider labels, and network
scheme/host values.

## Cursor and error contract

Issue list, view, and occurrence cursors use authenticated cursor-v2:

- database-specific epoch;
- normalized filters and effective limit where applicable;
- snapshot and retention generation;
- route kind and issue ID where applicable;
- deterministic row position;
- issued-at time.

They expire after 15 minutes. Bad encoding, bad MAC, cross-tool, cross-route,
cross-filter, and cross-issue use are invalid. A valid authenticated cursor
with a stale epoch, expired lifetime, or unavailable retained snapshot is
expired. Migration 012 expires all issue cursor-v1 values and pre-reset fix
eligibility/action tokens.

New-tool errors contain exactly one fixed code:

- `belay_mcp/invalid_input`
- `belay_mcp/invalid_cursor`
- `belay_mcp/cursor_expired`
- `belay_mcp/issue_not_found`
- `belay_mcp/read_busy`
- `belay_mcp/read_timeout`
- `belay_mcp/result_too_large`
- `belay_mcp/cancelled`
- `belay_mcp/read_failed`

No identifier, cursor, filter, evidence value, SQL error, or encrypted-payload
detail is reflected.

## Bounds

- Complete stdio JSON-RPC frame: 256 KiB.
- Raw arguments for each new tool: 16 KiB.
- Structured new-tool result: 2 MiB, with no silent truncation.
- At most four new-tool calls admitted concurrently.
- At most one admitted new-tool call performing a database read.
- One 15-second deadline includes database-slot queue time.
- No request-level retry.

## Explicitly absent

- `diagnose_issue`
- `record_fix`
- `recurrence_since`
- arbitrary `write_*`, `execute_*`, or `remediate_*` tools
- arbitrary project-file mutation or command execution
- arbitrary shell, filesystem, browser, hook, or network access
- P0-03 fix history/actions and P0-04 monitoring repositories

Browser-only fix-attempt declarations and recurrence monitoring remain
authenticated Local HTTP capabilities and are not reachable through MCP.
