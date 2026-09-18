# Belay Individual Alpha: Value Roadmap

- **Status:** Approved implementation sequence
- **Date:** 2026-09-08
- **Target customer:** Individual developers using multiple coding-agent
  harnesses and sessions
- **Reference strategy:**
  `research/belay-alpha-product-strategy.md.pdf`
- **Builds on:** P0-01 through P0-06

## 1. Launch decision

Belay's individual alpha is a local, privacy-first work-memory layer for
developers using multiple coding agents.

The alpha promise is:

> Belay shows a short list of distinct, trustworthy things worth inspecting,
> then explains recent retained agent activity without forcing the developer
> through raw event streams.

Belay does not yet promise real-time process awareness. Hook events establish
recently observed activity, not that a session is currently running, idle,
stuck, or stopped.

The browser is the evidence and drill-down surface. MCP lets a developer's
configured agent query the same bounded, cited evidence. Belay supplies
observations; the configured model interprets them.

## 2. Launch problems to solve

The current implementation has trustworthy evidence plumbing but does not yet
deliver enough obvious customer value:

1. Multiple cards can represent the same underlying source signal.
2. Unknown Numbat findings can become generic headline issues.
3. Evidence such as `config.agent` is technically accurate but not useful to a
   developer without Belay-owned interpretation.
4. Session timelines remain too prominent and too repetitive.
5. The current Key Sequence can select repeated `Command completed` events that
   explain nothing.
6. Session identity and outcome-unavailable metadata dominate more useful
   orientation.
7. MCP exposes precise low-level reads but does not sufficiently guide agents
   toward useful cross-session answers.

These are launch blockers. New dashboards, more counters, additional filters,
and broader detector coverage do not resolve them.

## 3. Locked product and trust boundaries

1. Default Attention contains only stable Belay detectors and explicitly mapped
   upstream source signals.
2. Unknown upstream findings remain inspectable but do not compete for default
   Attention.
3. Exact issue identities and cited occurrences remain authoritative and
   inspectable even when the presentation collapses redundant cards.
4. Belay never calls exact matching evidence the same root cause, intent, or
   semantic problem.
5. Session Story is deterministic. It does not infer task intent, progress,
   success, root cause, or completion.
6. Belay continues excluding prompts, completions, reasoning, file contents,
   diffs, command output, unsafe command arguments, secrets, and private
   absolute paths from canonical read surfaces.
7. Recent activity uses phrases such as `last supported event observed`.
   `active`, `running`, `idle`, `stuck`, and `stopped` remain prohibited without
   positive lifecycle evidence.
8. MCP remains read-only and preserves the existing nine-tool surface for the
   alpha.
9. Empty results describe the limits of retained analyzed evidence; they never
   mean that a session is safe, successful, complete, or free of issues.
10. Numbat remains implementation infrastructure. Customer-facing surfaces use
    Belay concepts and Belay-owned fixed vocabulary.

## 4. Implementation sequence

Each feature follows the established loop:

1. Write the feature tech design.
2. Self-review product value, privacy, correctness, failure modes, and contract
   compatibility.
3. Implement the approved slice.
4. Perform focused engineering verification.
5. Hand the build to the founder for the documented manual QA checkpoint.
6. Fix launch-blocking findings before beginning the next dependent slice.

### P0-07: Attention quality and consolidation

**Customer outcome:** Attention shows a small number of distinct,
understandable observations instead of repeated implementation-level findings.

**Scope:**

- Define fixed source-signal admission and product vocabulary.
- Collapse redundant presentation cards without deleting or mutating exact
  issue children.
- Group explicitly mapped upstream records by one versioned Belay signal family
  even when exact private scope is unavailable. This is a catalog summary, not
  an exact-match or recurrence claim. Scope quality remains visible and every
  exact issue remains inspectable.
- Nest supporting exact issues and occurrences beneath the primary observation.
- Remove raw `Numbat finding`, rule identifiers, `config.agent`, and other
  engine vocabulary from headline value.
- Hide experimental and unmapped upstream findings from default Attention.
- Hide or disable fix-attempt actions when the issue cannot support compatible
  recurrence monitoring.

**Not in scope:**

- Semantic similarity.
- Cross-project grouping.
- User dismissal or mutable suppression state.
- Root-cause grouping.

**Manual QA gate:**

- The repeated `tamper.guardrails_off` dataset produces one understandable
  top-level signal-family card with every exact issue retained as a child, not
  ten identical cards.
- Expanding the observation exposes every retained exact child and citation.
- No primary card or evidence summary says `Numbat finding` or `config.agent`.
- Unknown source findings cannot displace mapped actionable observations.

### P0-08: Deterministic Session Story

**Customer outcome:** Opening a session quickly explains the useful retained
evidence and what deserves inspection.

**Scope:**

- Replace Key Sequence with a bounded deterministic Session Story.
- Pair or group safe command observations and outcomes where supported.
- Collapse repeated equivalent command results into one statement with counts.
- Prioritize explicit failures, permission denials, mapped findings,
  verification evidence, meaningful resource changes, and the latest meaningful
  observed activity.
- Keep raw timeline as an optional drill-down.
- Make reconstructed capture and missing terminal outcome visible qualifiers,
  not the primary identity of every session.
- Return an honest insufficient-detail state when retained evidence cannot
  produce a useful story.

**Story contract:**

- harness and observed interval;
- historical, live-captured, or mixed source;
- source-qualified outcome;
- fixed observed-work counts;
- bounded safe resources;
- at most five deterministic notable statements;
- permission and explicit-failure counts;
- verification state when already established by supported detectors;
- partial, truncated, freshness, and coverage metadata.

**Manual QA gate:**

- A session with hundreds of repeated results does not show repeated
  `Command completed` highlights.
- The story makes explicit failures and what followed them visible.
- A developer can decide whether to inspect the session without first reading
  the raw timeline.
- No generated task name, unsafe command argument, private path, or inferred
  completion appears.

### P0-09: MCP work-memory workflows and onboarding

**Customer outcome:** A configured Codex or Claude session can answer useful
questions about recent retained work and Attention using Belay's cited evidence.

**Scope:**

- Preserve the existing nine read-only MCP tools.
- Enrich existing additive DTOs with Attention consolidation and Session Story
  fields.
- Rewrite tool descriptions and server instructions around product workflows:
  recent sessions, Attention, exact recurrence, one session's story, and cited
  evidence hydration.
- Publish fixed workflow guidance for configured agents.
- Make browser and MCP identity, counts, caveats, and citations agree.
- Design reversible one-command MCP registration for detected supported
  harnesses. Configuration changes require explicit consent and status/uninstall
  commands.
- Correct release-documentation drift about the MCP tool count.

**Acceptance query:**

> What needs my attention across my recent Codex and Claude sessions?

The configured agent must be able to return consolidated observations,
affected sessions and harnesses, fixed caveats, citations, and visible coverage
limits without claiming diagnosis or root cause.

**Manual QA gate:**

- A clean supported install reaches imported data, hooks, and MCP registration
  through one explicit onboarding command.
- Codex and Claude both discover exactly nine Belay tools.
- The acceptance query produces a useful cited answer on the founder's retained
  dataset.
- Browser and MCP agree on the same observation identities and counts.
- Every tool result remains read-only and marks observations as untrusted.

### P0-10: Recent activity and capture health

**Customer outcome:** Developers can distinguish recent retained activity from
stale or incomplete capture without Belay pretending to know process state.

**Scope:**

- Add `last supported event observed` and data-through context to sessions.
- Add a fixed capture-health contract separating:
  - harness detection;
  - hook support and configuration;
  - latest successful live import;
  - import lag or backlog;
  - analysis readiness;
  - historical scan completeness;
  - stale, degraded, artifact-only, and unknown states.
- Render capability consequences in plain language.
- Persist only fixed status and error codes; never raw paths, stdout, stderr,
  usernames, or host identifiers.

**Manual QA gate:**

- Removing a hook, rotating a spool, delaying import, or failing analysis
  produces a truthful degraded or unknown state.
- No surface labels a session active, idle, running, stopped, or stuck.
- Health failure never blocks Belay Local or agent activity.

### P0-11: Alpha integration and release gate

**Customer outcome:** The product consistently delivers the alpha promise on a
real multi-agent dataset and a clean installation.

**Required validation dataset:**

- the founder's retained dataset of approximately 125 sessions and 40,000
  events;
- a clean macOS installation;
- new live Codex and Claude Code sessions after hook installation.

**Release gates:**

1. No duplicate top-level Attention items for the same supported deterministic
   subject under the approved consolidation rules.
2. No primary `config.agent`, generic Numbat, or repeated
   `Command completed` content.
3. Every inspected session produces either a specific useful story or an honest
   insufficient-detail state.
4. A developer can identify what happened and what to inspect next without
   starting from the raw timeline.
5. The MCP acceptance query returns a useful, cited, cross-harness answer.
6. One-command onboarding is reversible and clearly communicates configuration
   changes.
7. No prohibited content appears in browser, HTTP, MCP, logs, canonical store,
   or release documentation.
8. Historical, partial, stale, pruned, pending, failed, and truncated evidence
   remain visibly qualified.
9. Documentation consistently describes nine MCP tools and the actual shipped
   commands.

## 5. Parallel spike: Numbat live-state feasibility

This spike does not block the recent-work-memory alpha.

Determine whether Codex and Claude Code can provide:

- explicit session lifecycle;
- heartbeat or positive liveness;
- hook-health history;
- sequence-gap and capture-continuity evidence;
- bounded low-latency delivery.

Only after these are reliable may Belay introduce a Live view or claim what an
agent is doing right now. Otherwise the product remains positioned around
recently observed retained activity.

## 6. Deferred until after the alpha

- Evidence-bundle export or file creation.
- New MCP tool aliases or an orchestration engine.
- Similar or semantic issue matching.
- Generated session narratives or task names.
- Cross-agent effectiveness rankings.
- Token and cost analytics.
- Custom rule authoring.
- Enforcement.
- Team, hosted, account, RBAC, and enterprise features.
- Backstop recovery actions.

Evidence bundles require a separate design because export introduces a new
disclosure, consent, file-permission, and sharing boundary.

## 7. Immediate action list

1. Write and review the P0-07 technical design.
2. Implement P0-07 and hand the consolidated Attention view to the founder for
   manual QA.
3. Write P0-08 using the accepted P0-07 presentation identity and implement
   Session Story.
4. Design and implement P0-09 MCP workflow parity and reversible one-command
   registration.
5. Design and implement P0-10 recent activity and capture health.
6. Run the P0-11 alpha gate against the retained and clean-machine datasets.
7. In parallel, complete the Numbat live-state spike and make a separate
   go/no-go decision for live-tense product positioning.
