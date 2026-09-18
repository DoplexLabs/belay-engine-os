# Belay Local Developer Alpha

Prepared version: `0.0.1-alpha.8`

This is an individual-developer alpha for private, accountless observation of
local Codex and Claude Code activity. It is Local-only. Belay Teams is not included.

The founder-led external alpha is unsigned and unnotarized. Its release
workflow still requires a clean tree, the full suite, checksum verification,
packaged smoke testing, and explicit publication authorization. A later broad
release remains gated on Developer ID signing and Apple notarization.

The alpha is:

- Apple Silicon macOS only (`darwin/arm64`)
- validated only for Codex and Claude Code
- distributed only after a separate human authorization
- an explicitly unsupported prerelease rather than a production channel

Intel macOS remains an engineering build target but is not an alpha support
claim until an Intel clean-machine run passes. Linux and Windows are out of
scope. Local storage depends on macOS Keychain.

## Exact dependency pin

The package contains pristine Numbat at:

```text
f0778c09dc48281aa93a3887d05096c0a1f3f9f7
```

The build verifies the commit, clean tree, license hashes, binary checksum, and
version marker `f0778c09dc48`. This commit is an approved research exception,
not a stable upstream release tag. Production remains blocked on a suitable
released Numbat tag or a renewed explicit exception.

## Build an unsigned Apple Silicon archive

Use a clean checkout on an Apple Silicon Mac:

```bash
make verify
make preview ALPHA_VERSION=0.0.1-alpha.8 ALPHA_ARCH=arm64
```

Or run the complete automated, non-publishing readiness path:

```bash
make alpha-readiness ALPHA_VERSION=0.0.1-alpha.8
```

Expected output:

```text
dist/belay-local-developer-alpha-v0.0.1-alpha.8-darwin-arm64.tar.gz
dist/belay-local-developer-alpha-v0.0.1-alpha.8-darwin-arm64.tar.gz.sha256
```

The commands do not sign, notarize, tag, publish, release, deploy, or contact a
paid service. Generated `/dist/` and `/bin/` directories are ignored by Git.

For an offline or pre-fetched build, supply a pristine checkout at the exact
commit:

```bash
scripts/build-developer-preview.sh \
  --version 0.0.1-alpha.8 \
  --arch arm64 \
  --numbat-source /absolute/path/to/pristine/numbat \
  --output-dir ./dist
```

The build uses a command-scoped redirect for Numbat's historical
`github.com/google/cel-go` module location. It does not edit Numbat or global Git
configuration.

## Archive contract

The archive contains:

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

Given the same Belay tree, Numbat commit, Go toolchain, architecture, version,
and `SOURCE_DATE_EPOCH`, archive metadata and entry ordering are deterministic.

## Verify the artifact

Keep the archive and companion checksum together:

```bash
cd /path/to/downloads
shasum -a 256 -c \
  belay-local-developer-alpha-v0.0.1-alpha.8-darwin-arm64.tar.gz.sha256
```

From a source checkout, run the disposable smoke test:

```bash
scripts/smoke-developer-preview.sh \
  ./dist/belay-local-developer-alpha-v0.0.1-alpha.8-darwin-arm64.tar.gz
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

## Unsigned macOS warning

macOS may block first launch because the binaries are unsigned and downloaded
from the internet. After independently verifying the checksum:

1. Try to run the specific `belay` binary once.
2. Open **System Settings → Privacy & Security**.
3. Choose **Open Anyway** for that specific binary and confirm.

Control-clicking the specific binary in Finder and choosing **Open** is another
per-binary path. Do not disable Gatekeeper or any system-wide protection.

## Install-to-first-insight path

For the published external alpha:

```bash
curl -fsSL https://getbelay.vercel.app/install | bash
```

The short installer downloads the exact prerelease from the public
`DoplexLabs/belay` distribution repository. It verifies the release checksum and embedded package
checksums, rejects dirty builds, and installs a stable `belay` command under the
current user. The bootstrap explicitly opts into the current unsigned alpha;
that exception is not used by the future signed channel. Re-running it upgrades
the installation.

For a local validation artifact, use the manual extraction path below.

Extract the package and keep both binaries together:

```bash
tar -xzf belay-local-developer-alpha-v0.0.1-alpha.8-darwin-arm64.tar.gz
cd belay-local-developer-alpha-v0.0.1-alpha.8-darwin-arm64

./bin/belay quickstart
```

Expected path:

1. Belay resolves sibling `bin/numbat`, verifies the checksum and version
   embedded at package build time, and copies it into private Local state.
2. Belay creates private state under `${BELAY_HOME:-~/.belay}` and macOS
   Keychain stores the random Local data key.
3. Belay installs reversible monitor-only hooks for detected Codex and Claude
   Code installations.
4. Belay safely inspects detected MCP configuration. Claude registration may
   proceed automatically when its status is safely understood. Normal
   quickstart does not add an absent Codex entry.
5. Numbat inventories local harnesses and Belay scans supported history.
6. Belay starts Local, prints a tokenized `127.0.0.1` URL, and attempts to open
   it in the default browser.
7. The dashboard shows minimized historical sessions. If browser opening fails,
   copy the printed URL into a browser.

The target is first useful history within 15 minutes. Record actual timing in
the clean-machine checklist; the alpha must not claim this gate passed without
external evidence.

`quickstart` is explicit consent to monitor-only hook changes and to safe MCP
inspection plus any Claude registration that Belay can verify. Hooks never
enforce, approve, deny, or block an agent action. MCP registration launches
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

Only detected Codex and Claude Code installations are configured. Ordinary
`local`, `scan`, `agents`, `mcp`, and `doctor` commands do not install hooks.

## Local MCP

Normal quickstart may register Claude automatically when its status is safely
understood. Codex add remains fail-closed unless the explicit opt-in flag is
present. Inspect or retry the exact ownership-safe registration:

```bash
./bin/belay mcp-config status
./bin/belay mcp-config install --allow-codex-mcp-add
```

Direct install may register Claude automatically when its status is safely
understood. `--allow-codex-mcp-add` is required before Belay may invoke Codex
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

Standalone `mcp-config install` may initialize private Belay configuration and
directories so it can retain a stable installation/ownership ID. It does not
create or open the Local database and does not create Keychain material.

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
the actual host harness (`claude` or `codex`), current project and intent, and
a task hint only when the user has stated a concrete active task.

Mission Packs are deliberately conservative:

- no current harness means no semantic operating rules;
- unanchored semantic rules must match the active task and be supported across
  at least two sessions;
- an issue-specific pack includes only that issue and its supported rule;
- stale, low-confidence, and unsupported rules are omitted;
- cross-harness targets are safely adapted to `CLAUDE.md` or `AGENTS.md` when
  equivalent, otherwise suppressed;
- no more than three verification commands are shown, selected for the intent;
- an empty pack is reported as unavailable and cannot be activated.

Preparing or approving a Mission Pack does not verify that a later change held,
prevented recurrence, or reduced cost.

For a quick manual check, start a concrete task in each client and run
`/belay start`. Confirm Claude receives only Claude-compatible targets, Codex
receives only Codex-compatible targets, and unrelated historical corrections
do not appear. Then run `/belay start` without a concrete task: no unanchored
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

For an explicit non-default Belay home, use the exact server arguments:

```bash
codex mcp add belay -- /absolute/path/to/belay-alpha/bin/belay mcp --home /absolute/path/to/private/belay-home
claude mcp add --scope user belay -- /absolute/path/to/belay-alpha/bin/belay mcp --home /absolute/path/to/private/belay-home
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
  as the same macOS user.
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
may run without an enforceable `sandbox-exec` environment.

## Keychain troubleshooting

Belay creates its encryption key through `/usr/bin/security` command-input mode
without asking for login-password data. If a build prints
`password data for new item:`, stop it and do not enter a password. Record the
failure and use a build containing the noninteractive Keychain fix.

## Uninstall and cleanup

Remove the two independently managed integrations:

```bash
./bin/belay mcp-config uninstall
./bin/belay hooks uninstall
```

`mcp-config uninstall` removes only an exact current or previously verified
Belay-owned user registration. It preserves a foreign or unverifiable `belay`
entry and returns a nonzero result when safe removal cannot be confirmed. It
does not remove monitor hooks, Local data, Keychain entries, or the extracted
package. `hooks uninstall` does not remove MCP configuration.

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

## Clean-tree distribution rule

A distributable Developer Alpha must be built from a clean checkout and its
`BUILD-INFO.txt` must contain `belay_dirty=false`. Setting
`BELAY_ALPHA_ALLOW_DIRTY=1` permits local packaging validation only. A dirty
artifact is never a release candidate and must not be shared, uploaded, or
published even when checksum and smoke checks pass.

## Production blockers

- Apple Developer ID signing and notarization
- A production Numbat release tag with the required schema, or renewed exception
- Signed installer and update-channel design
- Intel, Linux, and broader harness clean-machine validation
- Formal release/tag and supported-upgrade policy

MIT license selection is complete and is not a remaining blocker.
