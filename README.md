# Belay

Belay is local workflow intelligence for developers using Claude Code and
Codex. It turns agent session history into clear sessions, recurring habits,
evidence-backed issues, and reusable guidance for future work.

Your data stays on your Mac. Belay stores secret-scrubbed session content in an
encrypted local database and serves its interface only on loopback. It does not
upload transcripts, run commands, or apply proposed changes to your projects.

## What Belay gives you

- **Sessions** — understand what happened without reading raw event logs.
- **Habits** — find repeated behaviors, corrections, and sources of wasted work.
- **Mission Packs** — give an agent relevant project guidance before it starts.
- **Evidence** — trace every reported issue back to the matching session turns.
- **Cost context** — estimate token spend using versioned public model prices.
- **Agent access** — let Claude Code or Codex query Belay through local MCP tools.

## Install

The current release is `0.0.1-alpha.8` for macOS on Apple Silicon.

```bash
curl -fsSL https://getbelay.vercel.app/install | bash
```

The alpha is unsigned and unnotarized. The installer verifies the downloaded
archive and its checksums before installation.

To uninstall Belay while preserving its encrypted local history:

```bash
curl -fsSL https://getbelay.vercel.app/install | bash -s -- --uninstall
```

## Get started

Run:

```bash
belay quickstart
```

Quickstart discovers supported session history, imports it, opens the local
app, and offers supported agent integration.

When running directly from an extracted package:

```bash
./bin/belay quickstart
./bin/belay quickstart --allow-codex-mcp-add
./bin/belay quickstart --no-mcp
./bin/belay local
```

Useful commands:

```bash
belay doctor
belay agents
belay analyze
belay local
belay telemetry status
belay updates status
```

## Agent integration

Belay can expose local evidence, issues, Mission Packs, and approved learning
records to Claude Code and Codex over stdio MCP.

Manage registration explicitly:

```bash
./bin/belay mcp-config install
./bin/belay mcp-config install --allow-codex-mcp-add
./bin/belay mcp-config status
./bin/belay mcp-config uninstall
```

Adding a missing Codex registration requires `allow-codex-mcp-add` because the
upstream operation has non-atomic duplicate-name behavior. Belay leaves foreign or unverifiable entries unchanged.

Belay suggestions have no instruction authority by default. A user must review
and approve them before using or applying them.

<details>
<summary>MCP tool reference</summary>

Session and evidence tools:

```text
list_sessions
get_session
get_session_timeline
query_activity
list_findings
get_stats
list_issues
get_issue
lookup_session_events
get_top_issues
get_issue_excerpts
```

Fix and guidance tools:

```text
get_fix_status
propose_fix
record_fix_applied
get_mission_pack
record_mission_pack_accepted
get_mission_pack_status
```

Experience-learning tools:

```text
list_experience_proposals
list_active_experiences
approve_experience
resolve_experience_proposal
prepare_experience_lifecycle
apply_experience_lifecycle
```

List and query cursors are tied to a stable ingestion snapshot so a changing
local store does not silently alter an in-progress result set.

</details>

## Privacy and security

- Transcript payloads are secret-scrubbed and encrypted locally.
- Encryption keys are stored in macOS Keychain.
- The browser app binds to loopback and uses a per-launch access token.
- Belay makes no model calls of its own.
- Session content is not included in telemetry or update checks.
- Your configured agent may process evidence returned through MCP according to
  that agent's own settings and provider policy.

Belay sends one de-identified install event and at most one active-use event per
day. Disable this at any time:

```bash
belay telemetry off
```

Belay also checks for a newer release at most once every 18 hours without
sending session content:

```bash
belay updates off
```

See [SECURITY.md](SECURITY.md) for the security model and vulnerability
reporting process.

## Current scope

- macOS on Apple Silicon
- Claude Code and Codex session readers
- Encrypted, single-machine storage
- Historical import and opt-in live monitoring
- Local browser and stdio MCP interfaces

Cost figures are API list-price equivalents calculated from recorded model and
token data. They are estimates, not provider bills. If the model or price is
unknown, Belay leaves the cost unavailable rather than guessing.

This alpha packages the pinned Numbat commit
`f0778c09dc48281aa93a3887d05096c0a1f3f9f7` for canonical event ingestion.

## Support

Public releases, installation help, and issue reporting are available through
`DoplexLabs/belay`. Do not include credentials, transcript content, private
source code, or company-confidential information in public issues.

## License notices

The release package includes the applicable Belay and bundled dependency
license notices.
