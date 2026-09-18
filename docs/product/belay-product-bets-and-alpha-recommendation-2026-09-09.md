# Belay Product Bets and Initial Alpha Recommendation

**Date:** September 9, 2026
**Status:** Product decision memo; implementation is not yet authorized by this document
**Decision:** Make Mission Packs the initial alpha product bet. Preserve Eval Forge as the strongest long-term moat and Swarm Governor as the later multi-agent expansion.

## Executive summary

Belay's most valuable near-term opportunity is to turn evidence from past agent sessions into useful context for the next session:

> Past sessions → recurring problems → relevant guidance → a better next session

The initial alpha should introduce **Mission Packs**: a small, previewable operating brief that helps Claude Code or Codex begin a task with the project's proven rules, verification commands, recurring failure patterns, and relevant working context.

This is the best alpha wedge because it:

- Produces immediate daily value for agentic power users.
- Reuses Belay's current transcripts, findings, excerpts, project identity, costs, MCP tools, skills, and Numbat telemetry.
- Tests the central product assumption: whether users trust Belay to select context their agent needs.
- Can be shipped incrementally without building replay infrastructure or a real-time multi-agent control plane.

The recommended product sequence is:

1. **Mission Packs create daily value.**
2. **Eval Forge proves which changes improve agent performance.**
3. **Swarm Governor coordinates those improvements across concurrent agents.**

Post-fix recurrence and cost verification remains intentionally parked for later.

## Context and source precedence

This memo preserves the findings from the current value-first product exploration. It does not amend the BRD or high-level technical design.

Where older documents conflict with the current milestone, the current milestone direction and implemented value-first branch take precedence. In particular, Belay Local now deliberately allows encrypted local transcript retention, cost-ranked transcript analysis, additional MCP tools, and user-approved harness configuration changes. Belay Local remains single-machine, loopback-only, and makes no network calls itself.

Product support remains limited to Claude Code and Codex until another harness has a native transcript and cost reader. Numbat's wider acquisition support does not by itself constitute full Belay product support.

## Foundation: the Belay Agent Session Trace

Numbat is a core engine inside Belay, but it is not the whole product. The useful division of responsibility is:

- **Numbat provides structured evidence about what happened:** commands, files, tools, MCP calls, permissions, provenance, confidence, safety findings, and timeline events.
- **Belay's transcript sidecar provides why it happened and what it cost:** user and assistant text, intent, corrections, token usage, time, dollars, and verbatim evidence.
- **Belay's intelligence and product layer decides what matters:** recurring issues, project-level patterns, recommendations, context packs, evaluations, and eventually multi-agent coordination.

These two evidence streams should converge into a fused **Agent Session Trace**. It should become the shared substrate for all three product bets.

```text
Numbat structured events ─┐
                          ├─ Agent Session Trace ─ Mission Packs
Transcript/cost sidecar ──┘                       ├─ Eval Forge
                                                  └─ Swarm Governor
```

The fusion should be additive. The existing encrypted store, canonical events, Numbat findings, transcript turns, and MCP surfaces should remain intact.

## Numbat findings to preserve

The pinned Numbat version is already a strong acquisition, reconstruction, and safety engine. Its capabilities include:

- Broad agent activity acquisition through at-rest extractors and live hooks.
- A normalized event vocabulary covering commands, files, tools, permissions, MCP activity, and related agent actions.
- Built-in safety rules plus custom CEL and sequence rules.
- Timeline reconstruction, provenance, confidence, indicator extraction, and case bundles.
- OTLP ingestion, multiple sinks, and optional pre-action enforcement.

Belay currently uses only part of this capability:

- It launches Numbat acquisition for Claude Code and Codex.
- It imports canonical events, findings, scan summaries, and diagnostics.
- The value-first transcript intelligence does not yet fully fuse Numbat's structured command, file, permission, and provenance fields.
- Indicators are not yet presented as a useful Safety read model.
- Enforcement is intentionally not part of the current product direction.

The immediate opportunity is therefore not to replace Numbat or expose every Numbat feature. It is to use its structured evidence more completely inside Belay's user-facing intelligence.

## Product bet 1: Mission Packs

### Job to be done

When starting a task, a developer wants their agent to know the project's important operating context without repeatedly explaining it or loading a large, stale instruction file.

### Product experience

The user invokes `/belay start` or chooses **Prepare next session**. Belay identifies the project from the current working directory and generates a concise, editable brief containing:

- The project's most relevant recurring problems and known traps.
- User-approved rules derived from repeated corrections.
- Proven build, test, lint, typecheck, and verification commands.
- Relevant failure signatures and how previous sessions resolved them.
- Current branch, worktree, repository, and task context.
- A short completion and verification checklist.
- Evidence and provenance for every recommendation.

The pack is previewed before use. Nothing is silently injected, and Belay does not automatically rewrite `CLAUDE.md` or `AGENTS.md`.

### Why Belay can win

Generic context files tell an agent what somebody remembered to write down. Belay can generate guidance from observed behavior across real sessions and show the evidence behind each instruction.

Numbat contributes reliable structured facts such as commands, file activity, tools, permissions, branches, and verification behavior. Transcript intelligence contributes intent, corrections, explanations, and costs. Their combination makes a Mission Pack both specific and traceable.

### Alpha fit

Mission Packs have the highest current-code leverage and fastest time to user value. They can be built from capabilities Belay already has without requiring deterministic task replay or real-time control of several agents.

### Principal risks

- Stale or irrelevant guidance could create more friction than it removes.
- A pack could become another oversized context file.
- Users may not trust inferred rules even when evidence is provided.
- Task-specific relevance is difficult without overusing semantic inference.

These risks should be managed with strict size limits, evidence citations, preview and editing, explicit approval, and a small first scope.

## Product bet 2: Eval Forge

### Job to be done

When changing a model, agent harness, skill, instruction file, or workflow, a developer wants evidence that the change improves performance on the failures that actually matter in their work.

### Product experience

Belay mines historical sessions into private, replayable **eval capsules**. A capsule contains a task setup, relevant evidence, expected behavioral constraints, verification criteria, and measurable failure conditions. The user can compare Claude Code, Codex, models, skills, or configurations on:

- Correctness and verification quality.
- Repeated corrections and instruction adherence.
- Files and commands used.
- Tokens, time, and dollars.
- Recurrence of known failure signatures.

A winning behavior can then be exported as a proposed rule, skill, or harness configuration change.

### Why Belay can win

Most evaluation systems start with synthetic benchmarks or require users to author evals manually. Belay can derive private evals from a developer's actual expensive failures and retain the verbatim proof.

The Agent Session Trace gives Eval Forge unusually rich material: task intent and corrections from transcripts, plus structured execution and verification evidence from Numbat.

### Alpha fit

Eval Forge is likely the strongest long-term moat, but it is not the best first alpha workflow. Reliable replay requires repository-state reconstruction, environment isolation, task-boundary extraction, expected-outcome definitions, and careful comparison methodology. A weak replay system could produce false confidence.

For the initial alpha, Belay should preserve the data and provenance needed for future eval capsules, but not ship full replay or model comparison.

### Trigger to build next

Prioritize Eval Forge when Mission Pack users trust Belay's recommendations but repeatedly ask:

> How do I know this rule, skill, or configuration actually works?

## Product bet 3: Swarm Governor

### Job to be done

When a developer runs several agents concurrently, they want to avoid duplicated investigation, conflicting edits, lost decisions, stalled agents, and expensive work that no longer contributes to the goal.

### Product experience

Belay presents a live local work graph across Claude Code and Codex sessions. It identifies:

- Agents investigating the same problem.
- Overlapping or conflicting file activity.
- Sessions blocked, looping, or burning tokens without progress.
- Compaction that risks losing important decisions.
- Work ready for a structured handoff or resume packet.

The first version should be advisory. It should explain the conflict or waste and let the user decide how to intervene. Belay should not become a generic agent launcher.

### Why Belay can win

Harnesses see their own session. Belay can observe multiple harnesses and sessions through one normalized evidence layer. Numbat's structured event stream is especially valuable here because coordination depends on commands, files, tools, branches, permissions, and timing—not transcript semantics alone.

### Alpha fit

Swarm Governor is highly relevant to frontier-lab and AI-native power users, but it has the highest initial delivery and trust risk. It requires low-latency fused traces, reliable task identity, concurrency-aware file and worktree models, and clear intervention boundaries.

It should follow evidence that alpha users regularly run at least three concurrent agents and experience duplicate work, edit conflicts, or poor handoffs.

## Alpha evaluation

Scores are 1–5. Higher is better; delivery risk is inverted in the weighted total so lower risk scores better.

| Criterion | Weight | Mission Packs | Eval Forge | Swarm Governor |
|---|---:|---:|---:|---:|
| Immediate user value | 25% | 5 | 4 | 3 |
| Leverage from current product | 20% | 5 | 3 | 2 |
| Differentiation | 20% | 4 | 5 | 4 |
| Quality of alpha learning | 15% | 5 | 4 | 3 |
| Trust and privacy fit | 10% | 5 | 4 | 3 |
| Delivery feasibility | 10% | 5 | 2 | 1 |
| **Weighted score** | **100%** | **4.8** | **3.8** | **2.8** |

### Interpretation

- **Best initial alpha wedge:** Mission Packs.
- **Strongest defensible long-term capability:** Eval Forge.
- **Best later expansion for parallel-agent teams:** Swarm Governor.

## Recommended Mission Pack alpha

### Smallest valuable workflow

1. The user invokes `/belay start` from a Claude Code or Codex project.
2. Belay identifies the project and asks for a short task description.
3. Belay generates one bounded, previewable pack.
4. The user edits or approves it.
5. The pack is copied or supplied to the current agent for that session only.

### Initial pack contents

- Up to three high-confidence known traps for the project.
- Correction-derived rules that the user has approved.
- Observed or discovered verification commands.
- Relevant project, branch, and worktree facts.
- A concise task completion checklist.
- A citation for every non-obvious assertion.

The pack should have a strict token budget and optimize for signal, not completeness. A starting target of roughly 1,500 tokens is reasonable but should be validated through manual alpha QA.

### Explicit exclusions

- Silent or automatic context injection.
- Automatic edits to persistent harness configuration.
- Full Eval Forge replay, model comparison, or repository snapshots.
- Multi-agent launching, routing, cancellation, or governance.
- Live automatic intervention in file collisions or duplicate work.
- Post-fix recurrence and cost verification.
- Product-support claims beyond Claude Code and Codex.

## Alpha success criteria

The alpha should test usefulness and trust before causal performance claims. Suggested criteria:

- A useful pack is available within one minute of completing quickstart.
- At least 60% of activated alpha users generate a pack.
- At least 40% use Mission Packs across three or more sessions.
- Users judge at least 80% of included guidance relevant.
- Fewer than 5% of packs contain materially incorrect or harmful guidance.
- Most users can understand, edit, and apply a pack without founder assistance.
- Interviews repeatedly produce the signal: “Belay reminded my agent of something important I would otherwise have had to explain again.”
- Directionally, pack-assisted sessions show fewer setup turns and lower time or tokens before the first productive action. This should be treated as exploratory evidence in the alpha, not a causal claim.

## Founder QA on the existing session dataset

Before broad alpha exposure, manually inspect Mission Pack prototypes across the existing multi-session dataset:

- Do at least three useful, project-specific instructions emerge?
- Are verification commands current and safe?
- Can each instruction be traced to a transcript turn or Numbat event?
- Does the pack omit stale rules and one-off failures?
- Is it concise enough to scan in under a minute?
- Would it have saved explanation or prevented a known failure in a real session?
- Which pack items come from transcript semantics versus Numbat's structured evidence?

## Decision gates for the later bets

Start Eval Forge when users trust Mission Packs and need proof that proposed configurations improve outcomes.

Start Swarm Governor when multiple users consistently run three or more concurrent agents and report duplicated work, conflicting edits, wasted sessions, or poor handoffs.

Keep fix-held, recurrence, and before-versus-after cost verification parked until the core alpha workflow has demonstrated repeated user value.
