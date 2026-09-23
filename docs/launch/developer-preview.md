# Belay Local Developer Alpha

Prepared version: `0.0.1-alpha.11`

This is an individual-developer alpha for private, accountless observation of
local Codex, Claude Code, Cursor, and Google Antigravity activity. It is
Local-only. Belay Teams is not included.

The founder-led external alpha is unsigned on both channels. Its release
workflow still requires a clean tree, the full suite, checksum verification,
packaged smoke testing, and explicit publication authorization. A later broad
release remains gated on Developer ID signing and Apple notarization for macOS,
and on Authenticode signing for Windows.

The alpha is:

- Apple Silicon macOS (`darwin/arm64`) and Windows 10 build 1809 or newer and
  Windows 11 on x64 (`windows/amd64`)
- validated only for Codex and Claude Code; Cursor and Antigravity are
  supported across the engine but no clean-machine run has covered them yet
- distributed only after a separate human authorization
- an explicitly unsupported prerelease rather than a production channel

Neither channel is signed. The macOS archive is not notarized, so Gatekeeper
warns on first launch. The Windows archive is not Authenticode-signed, so
SmartScreen warns on first launch.

`windows/arm64` is built and vetted in CI as an engineering target. It is not
published and is not an alpha support claim. Intel macOS remains an engineering
build target but is not an alpha support claim until an Intel clean-machine run
passes. Linux has no supported channel.

Local storage depends on macOS Keychain on macOS and on the Windows Data
Protection API (DPAPI) on Windows; see
[`windows-port.md`](windows-port.md) and
[`../storage/local-storage-lifecycle.md`](../storage/local-storage-lifecycle.md).

The macOS clean-machine gate in
[`clean-machine-alpha-qa.md`](clean-machine-alpha-qa.md) is the evidence for
every macOS behavior claim below. Its Windows twin,
[`windows-clean-machine-alpha-qa.md`](windows-clean-machine-alpha-qa.md), has
not been executed yet. Until it passes on a real Windows machine, the verified
Windows facts are limited to what cross-compilation, the native CI job, and the
read-only packaged smoke test establish.

## Exact dependency pin

The package contains the pinned DoplexLabs Numbat fork at:

```text
b5172bb8bb8f1d68edc4f3b9462de7e248dc5243
```

The build verifies the commit, clean tree, license hashes, binary checksum, and
version marker `b5172bb8bb8f`. This commit adds the generic Numbat 0.4 session
lineage contract while retaining the upstream Apache-2.0 license and notices.

## Build an unsigned Apple Silicon archive

Use a clean checkout on an Apple Silicon Mac:

```bash
make verify
make preview ALPHA_VERSION=0.0.1-alpha.11 ALPHA_ARCH=arm64
```

Or run the complete automated, non-publishing readiness path:

```bash
make alpha-readiness ALPHA_VERSION=0.0.1-alpha.11
```

Expected output:

```text
dist/belay-local-developer-alpha-v0.0.1-alpha.11-darwin-arm64.tar.gz
dist/belay-local-developer-alpha-v0.0.1-alpha.11-darwin-arm64.tar.gz.sha256
```

The commands do not sign, notarize, tag, publish, release, deploy, or contact a
paid service. Generated `/dist/` and `/bin/` directories are ignored by Git.

For an offline or pre-fetched build, supply a pristine checkout at the exact
commit:

```bash
scripts/build-developer-preview.sh \
  --version 0.0.1-alpha.11 \
  --arch arm64 \
  --numbat-source /absolute/path/to/pristine/numbat \
  --output-dir ./dist
```

The build uses a command-scoped redirect for Numbat's historical
`github.com/google/cel-go` module location. It does not edit Numbat or global Git
configuration.

## Build an unsigned Windows archive

The Windows archive cross-compiles from macOS or Linux with `CGO_ENABLED=0`;
the pure-Go SQLite driver needs no C toolchain. Use a clean checkout:

```bash
make verify-windows
make preview-windows ALPHA_VERSION=0.0.1-alpha.11 WINDOWS_ARCH=amd64
```

`verify-windows` builds and vets every package for `windows/amd64` and builds
`windows/arm64`.

Expected output:

```text
dist/belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip
dist/belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip.sha256
```

`WINDOWS_ARCH=arm64` produces the engineering archive
`belay-local-developer-alpha-v0.0.1-alpha.11-windows-arm64.zip`. It is not
published.

The underlying command is the same build script with an explicit target OS:

```bash
scripts/build-developer-preview.sh \
  --os windows \
  --version 0.0.1-alpha.11 \
  --arch amd64 \
  --output-dir ./dist
```

The script never signs Windows binaries: `BUILD-INFO.txt` records
`target=windows/<arch>`, `signed=false`, and `notarized=false`.
`--codesign-identity` is rejected for this target.

## Archive contract

The macOS archive contains:

- `bin/belay`
- `bin/numbat`
- Belay's MIT `LICENSE`
- Numbat's exact `LICENSE` and `THIRD_PARTY_LICENSES.txt`
- `BUILD-INFO.txt`
- internal `SHA256SUMS`
- `README.md`
- `llms.txt`
- `docs/developer-alpha.md`
- `docs/clean-machine-alpha-qa.md`

The Windows zip mirrors that layout with Windows executable names and one
added document:

- `bin/belay.exe`
- `bin/numbat.exe`
- Belay's MIT `LICENSE`
- Numbat's exact `LICENSE` and `THIRD_PARTY_LICENSES.txt`
- `BUILD-INFO.txt`
- internal `SHA256SUMS`
- `README.md`
- `llms.txt`
- `docs/developer-alpha.md`
- `docs/clean-machine-alpha-qa.md`
- `docs/windows-port.md`

Given the same Belay tree, Numbat commit, Go toolchain, architecture, version,
and `SOURCE_DATE_EPOCH`, archive metadata and entry ordering are deterministic.

## Verify the artifact (macOS)

Keep the archive and companion checksum together:

```bash
cd /path/to/downloads
shasum -a 256 -c \
  belay-local-developer-alpha-v0.0.1-alpha.11-darwin-arm64.tar.gz.sha256
```

From a source checkout, run the disposable smoke test:

```bash
scripts/smoke-developer-preview.sh \
  ./dist/belay-local-developer-alpha-v0.0.1-alpha.11-darwin-arm64.tar.gz
```

The smoke test verifies archive paths, internal checksums, architecture, the
Numbat version marker, embedded checksum bootstrap from sibling `bin/numbat`,
help, and read-only inventory with temporary user and Belay homes. It supplies
no manual Numbat path, hash, or marker. It never installs hooks, accesses the
real Keychain, opens the Local database or browser, or reads real agent
configuration. If `sandbox-exec` is usable, runtime checks run with network
access denied.

This smoke test does not replace the manual Keychain, browser, MCP, hooks,
privacy, offline, and fail-open gates in
[`clean-machine-alpha-qa.md`](clean-machine-alpha-qa.md).

## Verify the artifact (Windows)

Copy the zip and its `.sha256` file to the Windows machine, then in PowerShell
compare the printed hash with the contents of the checksum file:

```powershell
Get-FileHash -Algorithm SHA256 .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip
Get-Content .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip.sha256
```

From a source checkout, run the disposable smoke test:

```powershell
scripts\smoke-windows-preview.ps1 -Archive .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip
```

It checks archive paths, the internal `SHA256SUMS`, the Numbat version marker,
`belay.exe help`, and a read-only `belay.exe agents` run with disposable
`USERPROFILE` and `--home` directories. It never installs hooks, creates a
DPAPI-wrapped data key, opens the database, or opens a browser.

This smoke test does not replace the manual gates in
[`windows-clean-machine-alpha-qa.md`](windows-clean-machine-alpha-qa.md), which
has not been executed yet.

## Unsigned macOS warning

macOS may block first launch because the binaries are unsigned and downloaded
from the internet. After independently verifying the checksum:

1. Try to run the specific `belay` binary once.
2. Open **System Settings → Privacy & Security**.
3. Choose **Open Anyway** for that specific binary and confirm.

Control-clicking the specific binary in Finder and choosing **Open** is another
per-binary path. Do not disable Gatekeeper or any system-wide protection.

## Unsigned Windows warning

The Windows binaries are not Authenticode-signed and have no SmartScreen
reputation, so Windows may block the first run of a downloaded file. After
independently verifying the checksum with `Get-FileHash`:

1. Run the specific binary once.
2. In the SmartScreen dialog, choose **More info**.
3. Choose **Run anyway** for that binary only.

Do not disable SmartScreen, Defender, or any system-wide protection, and do not
approve a binary whose checksum you have not verified. Unblocking a downloaded
archive with `Unblock-File` applies to that file only.

## Install-to-first-insight path

For the published external alpha on macOS:

```bash
curl -fsSL https://getbelay.vercel.app/install | bash
```

On Windows, in PowerShell:

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

Each installer downloads the exact prerelease from the public
`DoplexLabs/belay` distribution repository. It verifies the release checksum and embedded package
checksums, rejects dirty builds, and installs a stable `belay` command under the
current user. The bootstrap explicitly opts into the current unsigned alpha;
that exception is not used by the future signed channel. Re-running it upgrades
the installation. The Windows installer places the runtime under
`%LOCALAPPDATA%\Belay\runtime` and a `belay.cmd` shim in
`%LOCALAPPDATA%\Belay\bin`, which it adds to the user `PATH`; open a new
terminal afterwards so the updated `PATH` is visible.

For a local validation artifact, use the manual extraction path below.

Extract the package and keep both binaries together:

```bash
tar -xzf belay-local-developer-alpha-v0.0.1-alpha.11-darwin-arm64.tar.gz
cd belay-local-developer-alpha-v0.0.1-alpha.11-darwin-arm64

./bin/belay quickstart
```

On Windows, extract the zip and run the packaged executable from the extracted
directory:

```powershell
Expand-Archive .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip -DestinationPath .
cd .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64

.\bin\belay.exe quickstart
```

Every `./bin/belay <command>` below is `.\bin\belay.exe <command>` on Windows.

Expected path:

1. Belay resolves sibling `bin/numbat`, verifies the checksum and version
   embedded at package build time, and copies it into private Local state.
2. Belay creates private state under `${BELAY_HOME:-~/.belay}`
   (`%USERPROFILE%\.belay` on Windows unless `BELAY_HOME` is set) and stores
   the random Local data key in the platform key store: macOS Keychain on
   macOS, or a DPAPI-wrapped `keys\<store-id>.key` file beside the database on
   Windows.
3. Belay installs reversible monitor-only hooks for detected Codex, Claude
   Code, Cursor, and Antigravity installations, and installs the managed
   `/belay` skill for each detected harness (`~/.cursor/skills/belay/SKILL.md`
   for Cursor, `~/.gemini/config/skills/belay/SKILL.md` for Antigravity).
4. Belay safely inspects detected MCP configuration. Claude Code, Cursor, and
   Antigravity registration may proceed automatically when that target's
   status is safely understood. Normal quickstart does not add an absent Codex
   entry.
5. Numbat inventories local harnesses and Belay scans supported history.
   Antigravity has no historical scan: it is hook-only, so its first session
   appears only after the hooks are installed.
6. Belay starts Local, prints a tokenized `127.0.0.1` URL, and attempts to open
   it in the default browser.
7. The dashboard shows minimized historical sessions. If browser opening fails,
   copy the printed URL into a browser.

The target is first useful history within 15 minutes. Record actual timing in
the clean-machine checklist; the alpha must not claim this gate passed without
external evidence.

`quickstart` is explicit consent to monitor-only hook changes and to safe MCP
inspection plus any Claude Code, Cursor, or Antigravity registration that Belay
can verify.
Hooks never enforce, approve, deny, or block an agent action. MCP registration launches
only `belay mcp` over stdio and adds no write-capable tool.

To include Codex MCP registration in the same command:

```bash
./bin/belay quickstart --allow-codex-mcp-add
```

This flag permits `codex mcp add` only after Belay strictly verifies that the
`belay` entry is absent. It means the user explicitly accepts the Codex CLI's
non-atomic duplicate-name behavior. Belay never invokes that add operation
against an existing entry; foreign or unverifiable entries are preserved and
never overwritten or removed.

To install hooks but skip MCP inspection and registration:

```bash
./bin/belay quickstart --no-mcp
```

For users who do not want hook installation, the lower-side-effect path is:

```bash
./bin/belay local
```

`local` verifies packaged Numbat, imports existing live records, scans supported
history, starts the loopback server, and prints the dashboard URL. It does not
install hooks, change MCP configuration, or open a browser. A user can later
opt in with `./bin/belay hooks install` and
`./bin/belay mcp-config install --allow-codex-mcp-add`.

## Live hooks

Inspect or reverse the quickstart hook setup independently:

```bash
./bin/belay hooks status
./bin/belay hooks uninstall
```

Only detected Codex, Claude Code, Cursor, and Antigravity installations are
configured. Cursor's hooks are Numbat's monitor-only hooks in
`~/.cursor/hooks.json` (`beforeSubmitPrompt`, `preToolUse`, `beforeReadFile`,
`postToolUse`, `postToolUseFailure`, `stop`, `sessionStart`, `sessionEnd`,
`subagentStart`, and `subagentStop`), and their live records land in the
private spool `live/cursor.ndjson`. Antigravity's hooks are Numbat's
monitor-only hooks (`PreToolUse`, `PostToolUse`, and `Stop`) under the key
`numbat` in Antigravity's documented global hook file
`~/.gemini/config/hooks.json`, and their live records land in the private spool
`live/antigravity.ndjson`. Antigravity is hook-only: the pinned Numbat lists it
with no at-rest scan and rejects `numbat scan --agent antigravity`, so
`belay scan` and quickstart never run a historical Antigravity scan and only
sessions that happen after the hooks are installed are recorded. Ordinary
`local`, `scan`, `agents`, `mcp`, and `doctor` commands do not install hooks.

On Windows, hook installation and live capture depend on the pinned Numbat
build and on how each agent CLI executes hooks, including npm-installed `.cmd`
shims for `claude` and `codex`. Belay does not patch Numbat. That behavior is
unproven until the Windows hook gates in
[`windows-clean-machine-alpha-qa.md`](windows-clean-machine-alpha-qa.md) pass.

## Local MCP

Normal quickstart may register Claude Code, Cursor, and Antigravity
automatically when that target's status is safely understood. Codex add remains
fail-closed unless the explicit opt-in flag is present. Inspect or retry the
exact ownership-safe registration:

```bash
./bin/belay mcp-config status
./bin/belay mcp-config install --allow-codex-mcp-add
```

Direct install may register Claude Code, Cursor, and Antigravity automatically
when that target's status is safely understood. `--allow-codex-mcp-add` is required before Belay may invoke Codex
add, and only after strict absence verification. Belay inspects the existing
entry before every mutation. An exact current Codex entry is an unchanged
no-op. Codex install does not update, migrate, replace, or remove a recognized
prior, foreign, unverifiable, or unavailable entry, even with the opt-in flag.
After an archive move, inspect a recognized prior Codex entry with
`mcp-config status`; after verifying ownership, explicitly run
`mcp-config uninstall`, verify absence, and then rerun
`mcp-config install --allow-codex-mcp-add`. A foreign, scope-ambiguous, or
unverifiable entry named `belay` is preserved and must be resolved by the user.
Unsupported host-CLI status output is reported as unverifiable; Belay does not
guess.

Cursor publishes no MCP configuration CLI, so its target is the file
`~/.cursor/mcp.json` (`%USERPROFILE%\.cursor\mcp.json` on Windows), which
Belay reads and rewrites itself under the same ownership rules as the CLI
targets: exact identity comparison, manifest-recorded priors, and a foreign or
unverifiable `belay` entry preserved and never overwritten. The write is one
atomic private rename of a fully rendered document, so it can neither duplicate
nor half-apply an entry; it needs no opt-in flag, and `--allow-codex-mcp-add`
stays a Codex-only flag that does not affect Cursor. Every other server, remote
entry, and unknown top-level key is preserved. Cursor is detected only when
`~/.cursor` is a real directory; a file, a symlink, or an absent path is not a
detected install. `docs/contracts/mcp-v1.md` states the complete rule set.

Google Antigravity 2.0 also publishes no MCP configuration CLI, so its target
is Antigravity's documented global file `~/.gemini/config/mcp_config.json`
(`%USERPROFILE%\.gemini\config\mcp_config.json` on Windows), which Belay reads
and rewrites itself under the same ownership rules as Cursor: exact identity
comparison, manifest-recorded priors, and a foreign or unverifiable `belay`
entry (for example a `serverUrl` remote entry or an entry carrying `env`)
preserved and never overwritten. The write is atomic and idempotent, so it
needs no opt-in flag, and `--allow-codex-mcp-add` stays a Codex-only flag that
does not affect Antigravity. Every other server and unknown top-level key is
preserved byte for byte. Belay creates `~/.gemini/config` when it is absent
and refuses a symlinked path. It never touches the legacy
`~/.gemini/antigravity/mcp_config.json` or a workspace
`.agents/mcp_config.json`. Antigravity is detected only when
`~/.gemini/antigravity`, Antigravity 2.0's app-data root, is a real directory;
a symlink does not count. The `mcp-config` usage line lists the targets as
`Targets: codex (CLI), claude (CLI), cursor (~/.cursor/mcp.json), antigravity (~/.gemini/config/mcp_config.json).`

Standalone `mcp-config install` may initialize private Belay configuration and
directories so it can retain a stable installation/ownership ID. It does not
create or open the Local database and does not create data-key material in
macOS Keychain or in the Windows DPAPI key directory.

Belay MCP runs locally over stdio and exposes a governed tool catalog:

- `list_sessions`
- `get_session`
- `get_session_timeline`
- `query_activity`
- `list_findings`
- `get_stats`
- `list_issues`
- `get_issue`
- `lookup_session_events`
- `get_top_issues`
- `get_issue_excerpts`
- `get_fix_status`
- `get_mission_pack`
- `record_mission_pack_accepted`
- `get_mission_pack_status`
- `list_experience_proposals`
- `list_active_experiences`
- `approve_experience`
- `resolve_experience_proposal`
- `prepare_experience_lifecycle`
- `apply_experience_lifecycle`
- `propose_fix`
- `record_fix_applied`

Results are bounded structured data marked `untrusted_observations: true`. MCP
cannot install hooks, execute commands, modify project files, or perform
remediation. State-changing tools persist only bounded records or lifecycle
decisions after explicit user confirmation. `propose_fix` stores but never
applies a unified diff limited to allowlisted harness configuration files.
`record_fix_applied` stores the file
hash after explicit user approval and external application.

The issue-evidence loop is:

1. call `list_issues` and inspect normalized selection and analysis coverage;
2. preserve its `view_cursor` for `get_issue`;
3. inspect the fixed catalog observation, caveat, exact matching occurrences,
   and cited event IDs;
4. call `lookup_session_events` only for the cited IDs needed;
5. let the configured calling agent interpret the bounded evidence.

Deterministic issues remain traceable to retained turns. Mission Pack guidance
is an inactive proposal until the user explicitly approves it for the current
session; transcript evidence remains untrusted.

### Mission Packs

The MCP implementation is `1.7.0`; Mission Packs use
`mission-pack.det.v3`. The managed `/belay` skill calls `get_mission_pack` with
the actual host harness (`claude`, `codex`, `cursor`, or `antigravity`),
current project and intent, and a task hint only when the user has stated a
concrete active task.

Mission Packs are deliberately conservative:

- no current harness means no semantic operating rules;
- unanchored semantic rules must match the active task and be supported across
  at least two sessions;
- an issue-specific pack includes only that issue and its supported rule;
- stale, low-confidence, and unsupported rules are omitted;
- cross-harness targets are safely adapted to `CLAUDE.md` or `AGENTS.md` when
  equivalent, otherwise suppressed; a Cursor pack folds `CLAUDE.md` and Codex
  rules into `AGENTS.md` and suppresses `.claude/settings.json` items; an
  Antigravity pack folds `CLAUDE.md`, `AGENTS.md`, and Codex rules into
  `.agents/rules/belay.md`, because Antigravity reads project rules only from
  `.agents/rules/*.md`, and suppresses `.claude/settings.json` items;
- no more than three verification commands are shown, selected for the intent;
- an empty pack is reported as unavailable and cannot be activated.

Preparing or approving a Mission Pack does not verify that a later change held,
prevented recurrence, or reduced cost.

For a quick manual check, start a concrete task in each client and run
`/belay start` (`$belay start` in Codex; `/belay start` in Cursor's Agent chat,
where the managed skill lives at `~/.cursor/skills/belay/SKILL.md` and passes
`harness: cursor`; `/belay start` in Antigravity's agent chat, where the
managed skill lives at `~/.gemini/config/skills/belay/SKILL.md` and passes
`harness: antigravity`). Confirm Claude Code receives only Claude-compatible
targets, Codex receives only Codex-compatible targets, Cursor receives only
`AGENTS.md` targets, Antigravity receives only `.agents/rules/belay.md`
targets, and unrelated historical corrections do not appear. Then run `/belay start` without a concrete task: no unanchored
semantic rule should be invented. If the result is empty, the client should
stop without asking the user to activate it. The complete release gate is A10
in [`clean-machine-alpha-qa.md`](clean-machine-alpha-qa.md).

Alpha limitations:

- `get_stats` returns global Local counts only. Time-window and workflow filters
  are not implemented.
- Session, timeline, activity, and finding lists implement bounded opaque cursor
  pagination over a stable ingestion snapshot.
- Session cursors are bound to normalized harness, outcome, history, time, and
  query filters. Finding cursors are also bound to optional `session_id`.
- Resource-kind activity filtering scans the complete cursor snapshot; older
  matches are not omitted by an internal candidate-window bound.
- Belay's semantic analysis (deterministic-issue refinement, lessons, and
  Habits debriefs) can run through the Cursor CLI as well as through Claude
  Code or Codex. `belay analyze --agent auto|claude|codex|cursor|antigravity`
  and `quickstart --analyze-agent` accept all four; `auto` prefers Claude Code,
  then Codex, then the Cursor CLI, then the Antigravity CLI. The Cursor CLI is
  the binary `agent` (older installs also have `cursor-agent`), installed by
  `curl https://cursor.com/install -fsS | bash` into `~/.local/bin`. Belay
  detects it when `cursor-agent` is on `PATH`, or when `agent` is on `PATH`
  together with a real `~/.cursor` directory; `agent` is a generic name, so
  the directory is required, and a bare `agent` without `~/.cursor` is not
  detected. The Cursor IDE itself is not required. Belay runs
  `agent -p "<fixed instruction>" --output-format json --mode ask --trust --workspace <private temp dir>`
  with the full prompt on stdin; `--mode ask` is Cursor's read-only mode.
  Authentication is the user's own (`agent login` or `CURSOR_API_KEY`); Belay
  passes no key and makes no model call of its own. Cursor has no schema-bound
  output flag, so Belay extracts the JSON document from the result text and
  validates it against its own output schema before using it; a response that
  does not validate is rejected, and the model is recorded as unknown because
  the JSON result carries none. This path was written from Cursor's published
  documentation: the Cursor CLI was not installed on the development machine,
  so it was not validated against a real run. A Cursor-only machine with the
  Cursor CLI installed and signed in now gets semantic lessons and debriefs;
  without any of the four CLIs, behavior is unchanged (sessions, evidence, and
  deterministic issues only). The Cursor review queue
  (`list_experience_proposals` with `harness: cursor`) still shows proposals
  from any engine whose scope applies to Cursor, one per candidate.
- Belay's Cursor transcript reader, which scans
  `~/.cursor/projects/<hash>/agent-transcripts/**/*.jsonl` into the encrypted
  secret-scrubbed sidecar, is built against synthetic fixtures and has not been
  validated against a real Cursor transcript. `BELAY_CURSOR_HOME` overrides
  that root for Belay's reader only; Numbat always scans `~/.cursor`.
- No clean-machine run has covered Cursor. On Windows its paths are
  `%USERPROFILE%\.cursor\...`.
- The Antigravity CLI can also run Belay's semantic analysis. It is the
  binary `agy`, installed by
  `curl -fsSL https://antigravity.google/cli/install.sh | bash` into
  `~/.local/bin/agy` (`%LOCALAPPDATA%\agy\bin` on Windows), and it is a
  separate product from the Antigravity IDE, whose own `agy` launcher only
  opens the IDE. Belay detects the CLI only when `agy` is on `PATH`, does not
  resolve into the IDE bundle (`Antigravity.app` or `~/.antigravity`), and
  `~/.gemini/antigravity-cli` is a real directory; an `agy` that opens the IDE
  is not the CLI and is not selected. The IDE itself is not required. Belay
  runs
  `agy --input-format stream-json --output-format stream-json --json-schema <schema file> --sandbox`,
  feeds one user message on stdin, reads the `structured_output` of the result
  event (falling back to the response text), and validates it against the
  schema locally as well. Headless `agy` soft-denies any tool that needs
  approval, and Belay never passes `--dangerously-skip-permissions`. `agy`
  keeps the headless conversation under `~/.gemini/antigravity-cli`, which
  Belay never reads. This path was written from Google's published
  documentation: the Antigravity CLI was not installed on the development
  machine, so it was not validated against a real run. An analysis run by the
  Antigravity CLI prefers the target `.agents/rules/belay.md`, which Mission
  Packs adapt to `CLAUDE.md` for Claude Code and `AGENTS.md` for Codex and
  Cursor; Cursor-run analysis prefers `AGENTS.md` as before. This changes
  which CLI performs the analysis, not which sessions have transcripts: an
  Antigravity CLI can now write debriefs for Claude Code, Codex, and Cursor
  sessions, while Antigravity sessions themselves still have no transcript to
  debrief (next bullet). The Antigravity review queue
  (`list_experience_proposals` with `harness: antigravity`) still shows
  proposals from any engine whose scope applies to Antigravity, one per
  candidate.
- Antigravity is hook-only. Antigravity stores conversations as encrypted `.pb`
  files under `~/.gemini/antigravity/conversations/`, and Belay has no
  Antigravity transcript reader, so an Antigravity session gets a session,
  timeline evidence, and deterministic hook-based issues, but no transcript
  excerpts, no Habits debriefs, and no transcript-derived lessons.
  Antigravity's hook payloads name a per-conversation `transcript.jsonl` under
  `~/.gemini/antigravity/brain/<id>/.system_generated/logs/`, but on the
  validation machine no such file existed for any conversation, so Belay does
  not read it and makes no claim about it.
- Absence-based detectors stay silent for Antigravity, because Belay has not
  verified a session-terminal or approval event stream from its hooks.
- Antigravity support was developed on macOS against Antigravity 2.0.1, where
  only detection, hook status, and an MCP registration round trip were
  exercised; no live Antigravity session has been captured and no
  clean-machine run has covered Antigravity. On Windows its paths are
  `%USERPROFILE%\.gemini\config\...` and `%USERPROFILE%\.gemini\antigravity`.

### Manual MCP troubleshooting

Belay's supported configuration commands use the agent CLIs and are preferred.
If installation is unavailable, inspect the conflict first:

```bash
./bin/belay mcp-config status
```

For a verified absent entry, the equivalent official CLI commands are:

```bash
codex mcp add belay -- /absolute/path/to/belay-alpha/bin/belay mcp
claude mcp add --scope user belay -- /absolute/path/to/belay-alpha/bin/belay mcp
```

Cursor has no such command. Its manual equivalent is to edit
`~/.cursor/mcp.json` and add the `belay` entry by hand, leaving every other key
in place:

```json
{"mcpServers": {"belay": {"command": "/absolute/path/to/belay-alpha/bin/belay", "args": ["mcp"]}}}
```

Antigravity has no such command either. Its manual equivalent is to edit
`~/.gemini/config/mcp_config.json` (creating `~/.gemini/config` if it does not
exist) and add the same `belay` entry by hand, leaving every other key in
place:

```json
{"mcpServers": {"belay": {"command": "/absolute/path/to/belay-alpha/bin/belay", "args": ["mcp"]}}}
```

For an explicit non-default Belay home, use the exact server arguments:

```bash
codex mcp add belay -- /absolute/path/to/belay-alpha/bin/belay mcp --home /absolute/path/to/private/belay-home
claude mcp add --scope user belay -- /absolute/path/to/belay-alpha/bin/belay mcp --home /absolute/path/to/private/belay-home
```

The Cursor and Antigravity equivalents carry the same arguments, in
`~/.cursor/mcp.json` and `~/.gemini/config/mcp_config.json` respectively:

```json
{"mcpServers": {"belay": {"command": "/absolute/path/to/belay-alpha/bin/belay", "args": ["mcp", "--home", "/absolute/path/to/private/belay-home"]}}}
```

Do not replace an existing entry solely because it is named `belay`. Do not
configure HTTP transport, a bearer token, environment secrets, a shell wrapper,
or a working-directory override. Restart the client after registration.

## Privacy and outcome limitations

- Local sends no product telemetry and performs no Belay-hosted model calls.
- Prompt bodies, transcripts, reasoning text, file contents, raw endpoint
  identity, and raw evidence paths are excluded from canonical events.
- Minimized envelope/index columns are plaintext SQLite metadata. Canonical
  event JSON and finding citation arrays are encrypted with AES-256-GCM.
- Command summaries may include an executable name and bounded option names.
  File resources may include a project-relative path or basename. Network
  resources may include scheme and host.
- Local protects against accidental disclosure, not a malicious process running
  as the same operating-system user account.
- Unknown event outcomes remain visibly unreported. Sessions without a
  `session.end` observation are `Incomplete`, never inferred successful.
- Historical reconstruction and findings depend on what the pinned Numbat
  adapters can observe; absence of evidence is not evidence of absence.

Do not use real secrets as privacy-test inputs. Use the synthetic canary
specified in the clean-machine checklist.

## Offline behavior

Source builds normally need network access for Git and Go dependencies. Once an
archive exists, the Local browser, encrypted store, historical scan, live spool
import, and stdio MCP require no hosted Belay service or internet connection.
The browser binds to `127.0.0.1` and JSON routes require a random per-launch
token.

MCP registration itself makes no Belay, Doplex, Anthropic, OpenAI, or other
network request and requires no paid service or account login from Belay. A
host agent CLI wrapper may independently perform credential or network checks;
if that bounded command fails, quickstart reports incomplete onboarding and
continues opening Local.

Offline behavior must be demonstrated manually because the packaged smoke test
may run without an enforceable `sandbox-exec` environment. The Windows smoke
test has no sandbox equivalent, so its offline gate is entirely manual: disable
the network adapters and follow
[`windows-clean-machine-alpha-qa.md`](windows-clean-machine-alpha-qa.md).

## Data-key troubleshooting

On macOS, Belay creates its encryption key through `/usr/bin/security`
command-input mode without asking for login-password data. If a build prints
`password data for new item:`, stop it and do not enter a password. Record the
failure and use a build containing the noninteractive Keychain fix.

On Windows, Belay wraps the same random key with DPAPI in user scope with UI
forbidden, so key creation and reuse never prompt. The wrapped blob is
`<belay home>\keys\<store-id>.key` and only the Windows account that created
it can unwrap it. A missing, foreign, or copied key file fails closed; Belay
never creates a replacement key for an existing store.

## Uninstall and cleanup

Remove the two independently managed integrations:

```bash
./bin/belay mcp-config uninstall
./bin/belay hooks uninstall
```

`mcp-config uninstall` removes only an exact current or previously verified
Belay-owned user registration. It preserves a foreign or unverifiable `belay`
entry and returns a nonzero result when safe removal cannot be confirmed. It
does not remove monitor hooks, Local data, the stored data key, or the
extracted package. `hooks uninstall` does not remove MCP configuration.

Confirm the resulting state:

```bash
./bin/belay mcp-config status
./bin/belay hooks status
```

After both integrations are absent, stop Belay and delete the extracted package
directory if desired.

Local data remains under `${BELAY_HOME:-~/.belay}`. Deleting that directory is a
separate destructive choice and is never performed by readiness scripts.
Keychain entries use service `dev.doplex.belay.local.data-key.v1`; remove a
corresponding entry through Keychain Access only after intentionally deleting
its database.

On Windows, run the installed program's uninstall path instead of deleting the
runtime by hand:

```powershell
irm https://getbelay.vercel.app/install.ps1 -OutFile install.ps1
.\install.ps1 -Uninstall
```

`irm ... | iex` cannot pass arguments, so either download the script as above or
run
`& ([scriptblock]::Create((irm https://getbelay.vercel.app/install.ps1))) -Uninstall`.
Uninstall removes `%LOCALAPPDATA%\Belay\runtime`, the `belay.cmd` shim in
`%LOCALAPPDATA%\Belay\bin`, and that directory's user `PATH` entry. It
preserves `%USERPROFILE%\.belay`, including `keys\<store-id>.key`. Deleting
the Belay home deletes the wrapped key with the database and makes the
encrypted payloads unrecoverable by design.

## Clean-tree distribution rule

A distributable Developer Alpha must be built from a clean checkout and its
`BUILD-INFO.txt` must contain `belay_dirty=false`. Setting
`BELAY_ALPHA_ALLOW_DIRTY=1` permits local packaging validation only. A dirty
artifact is never a release candidate and must not be shared, uploaded, or
published even when checksum and smoke checks pass.

## Production blockers

- Apple Developer ID signing and notarization
- Windows Authenticode signing and a SmartScreen reputation plan
- A passing Windows clean-machine run of
  [`windows-clean-machine-alpha-qa.md`](windows-clean-machine-alpha-qa.md)
- A production Numbat release tag with the required schema, or renewed exception
- Signed installer and update-channel design
- Intel macOS, Linux, Cursor, Antigravity, and broader harness clean-machine
  validation
- Formal release/tag and supported-upgrade policy

MIT license selection is complete and is not a remaining blocker.
