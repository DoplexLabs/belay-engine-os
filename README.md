# Belay

Belay is local workflow intelligence for developers using Claude Code, Codex,
Cursor, and Google Antigravity. It turns agent session history into clear
sessions, recurring habits, evidence-backed issues, and reusable guidance for
future work.

Your data stays on your machine. Belay stores secret-scrubbed session content
in an encrypted local database and serves its interface only on loopback. It
does not upload transcripts, run commands, or apply proposed changes to your
projects.

## What Belay gives you

- **Sessions** — understand what happened without reading raw event logs.
- **Habits** — find repeated behaviors, corrections, and sources of wasted work.
- **Mission Packs** — give an agent relevant project guidance before it starts.
- **Evidence** — trace every reported issue back to the matching session turns.
- **Cost context** — estimate token spend using versioned public model prices.
- **Agent access** — let Claude Code, Codex, Cursor, or Antigravity query Belay
  through local MCP tools.

## Install

The current release is `0.0.1-alpha.11`. It supports macOS on Apple Silicon and
Windows 10 (build 1809 or newer) and Windows 11 on x64 (`amd64`).

macOS:

```bash
curl -fsSL https://getbelay.vercel.app/install | bash
```

Windows, in PowerShell:

```powershell
irm https://getbelay.vercel.app/install.ps1 | iex
```

The bootstrap at `getbelay.vercel.app/install.ps1` passes `-AllowUntrusted`
for the current unsigned alpha. If you run the release `install.ps1` asset
directly, pass it yourself, because a piped `irm | iex` cannot carry flags:

```powershell
irm https://github.com/DoplexLabs/belay/releases/download/v0.0.1-alpha.11/install.ps1 -OutFile install.ps1
.\install.ps1 -AllowUntrusted -Quickstart
```

Both channels are unsigned alphas. The macOS build is not notarized, so
Gatekeeper warns on first launch; the Windows build is not Authenticode-signed,
so SmartScreen warns on first launch. Each installer verifies the downloaded
archive and its checksums before installation. The Windows installer places the
runtime under `%LOCALAPPDATA%\Belay\runtime` and a `belay.cmd` shim in
`%LOCALAPPDATA%\Belay\bin`, which it adds to the user `PATH`. Open a new
terminal after installing so the updated `PATH` is visible.

To uninstall Belay while preserving its encrypted local history:

```bash
curl -fsSL https://getbelay.vercel.app/install | bash -s -- --uninstall
```

`irm ... | iex` cannot pass arguments, so uninstall on Windows runs the script
itself:

```powershell
irm https://getbelay.vercel.app/install.ps1 -OutFile install.ps1
.\install.ps1 -Uninstall
```

Without saving a file:

```powershell
& ([scriptblock]::Create((irm https://getbelay.vercel.app/install.ps1))) -Uninstall
```

Uninstall removes the program, monitor hooks, and Belay MCP registration. It
preserves encrypted Belay history.

### Where Belay keeps local data

| Item | macOS | Windows |
|---|---|---|
| Belay home | `~/.belay` unless `BELAY_HOME` is set | `%USERPROFILE%\.belay` unless `BELAY_HOME` is set |
| Database | `~/.belay/belay.sqlite` | `%USERPROFILE%\.belay\belay.sqlite` |
| Data key | macOS Keychain, service `dev.doplex.belay.local.data-key.v1` | `%USERPROFILE%\.belay\keys\<store-id>.key`, wrapped with DPAPI |

On Windows the wrapped key file lives in the `keys` subfolder of the Belay home
and can be unwrapped only by the Windows account that created it. Deleting the
Belay home deletes the key with the database.

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

On Windows the packaged executable is `.\bin\belay.exe`, so the same commands
are `.\bin\belay.exe quickstart`, `.\bin\belay.exe quickstart --no-mcp`, and
so on.

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
records to Claude Code, Codex, Cursor, and Antigravity over stdio MCP.

Manage registration explicitly:

```bash
./bin/belay mcp-config install
./bin/belay mcp-config install --allow-codex-mcp-add
./bin/belay mcp-config status
./bin/belay mcp-config uninstall
```

Adding a missing Codex registration requires `allow-codex-mcp-add` because the
upstream operation has non-atomic duplicate-name behavior. Belay leaves foreign or unverifiable entries unchanged.

Cursor publishes no MCP configuration CLI, so Belay manages the file
`~/.cursor/mcp.json` itself (`%USERPROFILE%\.cursor\mcp.json` on Windows),
under the same ownership rules: exact identity comparison, foreign or
unverifiable `belay` entries preserved, an atomic private write, and every
other server and unknown key preserved. That write is a single atomic replace,
so it needs no opt-in flag. Cursor is detected only when `~/.cursor` is a real
directory. The manual equivalent is to add this to `~/.cursor/mcp.json`:

```json
{"mcpServers": {"belay": {"command": "/absolute/path/to/belay", "args": ["mcp"]}}}
```

Google Antigravity 2.0 likewise publishes no MCP configuration CLI, so Belay
manages its documented global file `~/.gemini/config/mcp_config.json` itself
(`%USERPROFILE%\.gemini\config\mcp_config.json` on Windows), under the same
ownership rules as Cursor: exact identity comparison, foreign or unverifiable
`belay` entries preserved, an atomic private write, and every other server and
unknown key preserved. That write is a single atomic replace, so it needs no
opt-in flag. Belay creates `~/.gemini/config` when it is absent and never
touches the legacy `~/.gemini/antigravity/mcp_config.json` or a workspace
`.agents/mcp_config.json`. Antigravity is detected only when
`~/.gemini/antigravity` is a real directory. The manual equivalent is to add
this to `~/.gemini/config/mcp_config.json`:

```json
{"mcpServers": {"belay": {"command": "/absolute/path/to/belay", "args": ["mcp"]}}}
```

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
- Encryption keys are stored in macOS Keychain on macOS, and wrapped with the
  per-user Data Protection API (DPAPI) on Windows.
- The browser app binds to loopback and uses a per-launch access token.
- Belay makes no model calls of its own.
- Session content is not included in telemetry or update checks.
- Your configured agent may process evidence returned through MCP according to
  that agent's own settings and provider policy.

Belay sends one de-identified install event and at most one active-use event per
day (a random ID, version, OS, CPU architecture, and which of Claude Code,
Codex, Cursor, and Antigravity are installed; never content, paths, or names). The receiver
keeps the request's country, not its address, and forwards the same fields to a hosted analytics
processor keyed by the random ID. Belay prints a one-line notice the first time
it sends. Disable this at any time, or set `BELAY_TELEMETRY=0` or
`DO_NOT_TRACK=1`:

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

- Apple Silicon macOS, and Windows 10 (1809+) or Windows 11 on x64 (`amd64`);
  see `docs/launch/windows-port.md`
- Windows on arm64 is an engineering build target that is built but not
  published; Intel macOS and Linux are unsupported
- Claude Code, Codex, and Cursor session readers, and hook-only Antigravity
  sessions
- Encrypted, single-machine storage
- Historical import and opt-in live monitoring
- Local browser and stdio MCP interfaces

Cursor support is new and no clean-machine run has covered it yet. Two limits
are worth knowing before you rely on it. Belay's own Cursor transcript reader
was built against synthetic fixtures and has not been validated against a real
Cursor transcript. And Belay's semantic analysis (deterministic-issue
refinement, lessons, and Habits debriefs) can now run through the Cursor CLI
(`agent`, or `cursor-agent` on older installs) as well as through Claude Code
or Codex, but that path was written from Cursor's published documentation
alone: the Cursor CLI was not installed on the development machine, so it was
not validated against a real run. `belay analyze --agent cursor` selects it
explicitly, and `--agent auto` prefers Claude Code, then Codex, then the
Cursor CLI, then the Antigravity CLI. Belay detects the CLI when
`cursor-agent` is on `PATH`, or when `agent` is on `PATH` together with a real
`~/.cursor` directory (`agent` is a generic name, so the directory is
required); the Cursor IDE itself is not needed. Belay runs it in Cursor's
read-only `--mode ask` inside a private temporary workspace, under your own
`agent login` or `CURSOR_API_KEY` sign-in, and passes no key and makes no
model call of its own. Cursor has no schema-bound output flag, so Belay
extracts the JSON document from the result text and rejects any response that
does not validate against its own output schema; the model is recorded as
unknown. A Cursor-only machine with the Cursor CLI installed and signed in
therefore gets semantic lessons and debriefs. Without any of the four CLIs it
still gets sessions, evidence, and deterministic issues only.

Google Antigravity 2.0 support is also new. It was developed on macOS against
Antigravity 2.0.1, where only detection, hook status, and an MCP registration
round trip were exercised; no live Antigravity session has been captured and
no clean-machine run has covered it yet. It is hook-only:
Antigravity keeps its conversations as encrypted `.pb` files, Belay has no
Antigravity transcript reader, and `belay scan` performs no Antigravity history
scan, so only sessions that happen after `belay quickstart` or
`belay hooks install` are recorded. An Antigravity session gets a session,
timeline evidence, and deterministic hook-based issues, but no transcript
excerpts, no Habits debriefs, and no transcript-derived lessons.

The Antigravity CLI (`agy`) can now run Belay's semantic analysis as well. It
is a separate product from the Antigravity IDE, whose own `agy` launcher only
opens the IDE: Belay detects the CLI only when `agy` is on `PATH`, does not
resolve into the IDE bundle (`Antigravity.app` or `~/.antigravity`), and
`~/.gemini/antigravity-cli` is a real directory. The IDE itself is not needed.
`belay analyze --agent antigravity` selects it explicitly, and `--agent auto`
falls back to it after Claude Code, Codex, and the Cursor CLI. Belay runs it
with `--sandbox` and its own JSON output schema, feeds one user message on
stdin, validates the structured result locally as well, and never passes
`--dangerously-skip-permissions`; headless `agy` soft-denies any tool that
needs approval. `agy` keeps its headless conversation under
`~/.gemini/antigravity-cli`, which Belay never reads. Like the Cursor path,
this was written from Google's published documentation and, because the
Antigravity CLI was not installed on the development machine, it was
not validated against a real run. The transcript situation above is
unchanged: an Antigravity CLI can now write debriefs for Claude Code, Codex,
and Cursor sessions, but Antigravity sessions themselves still have no
transcript to debrief.

Cost figures are API list-price equivalents calculated from recorded model and
token data. They are estimates, not provider bills. If the model or price is
unknown, Belay leaves the cost unavailable rather than guessing.

This alpha packages the pinned DoplexLabs Numbat commit
`b5172bb8bb8f1d68edc4f3b9462de7e248dc5243` for canonical event and explicit
session-lineage ingestion.

## Support

Public releases, installation help, and issue reporting are available through
`DoplexLabs/belay`. Do not include credentials, transcript content, private
source code, or company-confidential information in public issues.

## License notices

The release package includes the applicable Belay and bundled dependency
license notices.
