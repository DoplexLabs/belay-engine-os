# P0-09: One-Command Onboarding

- **Status:** Implemented contract, updated after Codex add safety review
- **Date:** 2026-09-09
- **Target:** Belay Local individual developer alpha
- **Depends on:** Existing packaged Numbat bootstrap, hook onboarding, Local
  runtime, and read-only MCP server
- **Storage migration:** None
- **MCP tool change:** None; the existing nine read-only tools remain unchanged

## 1. Overview

After extracting the alpha archive, an individual developer should need one
command:

```text
./bin/belay quickstart
```

The existing command already verifies and privately materializes packaged
Numbat, initializes Belay state and Keychain-backed storage, installs
monitor-only hooks for detected Codex and Claude Code installations, imports
live records, starts historical scanning, serves Local on loopback, prints its
URL, and attempts to open a browser.

P0-09 adds the missing step: configure detected Codex and Claude Code CLIs to
launch the existing read-only Belay MCP server. Claude may be configured
automatically when its status is safely understood. Codex add is fail-closed by
default because the Codex CLI's duplicate-name behavior is non-atomic; the
explicit one-command path is:

```text
./bin/belay quickstart --allow-codex-mcp-add
```

That flag permits `codex mcp add` only after Belay strictly verifies that the
`belay` entry is absent and records the user's acceptance of that CLI behavior.
Registration remains local, bounded, idempotent where the host CLI permits it,
ownership-safe, and fail-open. An existing foreign or unverifiable MCP entry
named `belay` is never overwritten or removed.

## 2. Glossary

- **MCP registration:** A Codex or Claude Code user configuration named
  `belay` whose stdio command launches the Belay executable with `mcp`.
- **Current identity:** The canonical absolute path of the currently running
  Belay executable plus the expected MCP arguments.
- **Recognized identity:** An exact prior Belay executable and argument tuple
  recorded by Belay after a verified successful installation.
- **Foreign entry:** An existing `belay` entry whose transport, scope, command,
  arguments, or relevant launch settings do not exactly match a current or
  recognized Belay identity.
- **Unverifiable entry:** An entry whose status output cannot be parsed and
  proven safe to mutate.
- **Detected target:** A supported agent CLI resolvable as an executable on the
  current process environment's `PATH`.

## 3. Requirements

### 3.1 Functional requirements

1. `belay quickstart` MUST remain explicit consent to initialize private Local
   state, modify detected Codex/Claude monitor-hook configuration, inspect
   supported MCP configuration, and apply only MCP changes allowed without the
   Codex add opt-in.
2. Quickstart MUST attempt safe MCP onboarding after state/Keychain
   initialization and before opening Local. Claude may be installed when its
   status is safely understood. Normal quickstart MUST NOT add an absent Codex
   entry.
3. `belay quickstart --no-mcp` MUST skip MCP configuration only. Numbat
   verification, state initialization, hooks, import/scan, Local, and browser
   behavior remain unchanged.
4. `belay quickstart --allow-codex-mcp-add` MUST be the explicit one-command
   path that permits Codex add after strict absence verification. The flag is
   explicit acceptance of the Codex CLI's non-atomic duplicate-name behavior.
5. Add:

   ```text
   belay mcp-config install [--allow-codex-mcp-add]
   belay mcp-config status
   belay mcp-config uninstall
   ```

6. The existing `belay mcp` stdio server command MUST remain unchanged.
7. Install MUST target only detected Codex and Claude Code CLIs.
8. Status and uninstall MUST inspect both supported targets when their CLI is
   available, even if no current agent artifacts are detected.
9. All subprocesses MUST be direct fixed-argument executions with bounded
   output and deadlines. No shell is allowed.
10. Quickstart MCP failures MUST be non-fatal and summarized before Local
   continues.
11. Explicit `mcp-config install|status|uninstall` MUST return nonzero when the
    requested operation is not safely completed.
12. Invoking `mcp-config install` is explicit consent to modify supported host
    MCP configuration within the default safety boundary. Codex add still
    requires `--allow-codex-mcp-add`.

### 3.2 Ownership and idempotency requirements

1. Belay MUST inspect the existing `belay` entry before every mutation.
2. An absent Claude entry may be installed after safe status classification.
   An absent Codex entry may be installed only with
   `--allow-codex-mcp-add`.
3. An exact current entry is an idempotent no-op.
4. An exact recognized prior Claude entry may be updated to the current
   identity. A recognized prior Codex entry MUST NOT be updated, migrated,
   replaced, or removed by install, even with `--allow-codex-mcp-add`. The user
   must verify it with `mcp-config status`, explicitly invoke
   `mcp-config uninstall`, verify absence, and then rerun the opt-in install.
5. A foreign, scope-ambiguous, or unverifiable entry MUST be preserved.
6. Uninstall MUST remove only an exact current or recognized Belay entry.
7. Belay MUST NOT infer ownership from the entry name alone, executable basename
   alone, an argument substring, or a path under an archive directory.
8. Belay MUST serialize its own MCP configuration operations per Belay home and
   re-inspect immediately before destructive removal.
9. External concurrent edits cannot be made transactional through the agent
   CLIs. Unknown post-mutation state MUST fail closed without another removal.

### 3.3 Privacy and security requirements

1. Registration launches only the existing local, read-only stdio server.
2. No prompt, completion, reasoning, file content, diff, stdout, stderr,
   environment value, secret, or event payload enters onboarding output.
3. Raw Codex/Claude command output MUST never be printed or returned in errors.
4. The Local bearer token is unrelated to MCP and MUST NOT be stored in agent
   configuration.
5. No HTTP MCP transport, URL, bearer token, environment secret, working
   directory, or shell wrapper may be installed.
6. The canonical Belay executable path MUST be absolute, NUL/control-free, a
   regular executable after symlink resolution, and passed as one argv element.
7. Custom Belay home paths MUST be absolute and passed as argv, never shell
   text.
8. The feature MUST make no Doplex, Anthropic, OpenAI, or other network request.
   If a host CLI wrapper performs credential or network checks, the bounded
   command may fail; quickstart still continues.
9. No paid service or account login is required by Belay.

### 3.4 Compatibility requirements

1. `belay local` and `belay local --install-hooks` MUST NOT begin installing MCP
   configuration.
2. The nine-tool MCP surface, protocol version, transport limits, trust wrapper,
   and read-only annotations remain unchanged.
3. Packaged registration uses these exact add commands when the target is
   safely eligible. The Codex command additionally requires the explicit
   opt-in:

   ```text
   codex mcp add belay -- ABS_BELAY mcp
   claude mcp add --scope user belay -- ABS_BELAY mcp
   ```

4. For an explicit non-default Belay home, append the existing supported server
   arguments:

   ```text
   ABS_BELAY mcp --home ABS_BELAY_HOME
   ```

   This is required because a future agent process cannot be assumed to inherit
   the quickstart process's `BELAY_HOME`.
5. Status uses exactly:

   ```text
   codex mcp get belay --json
   claude mcp get belay
   ```

6. Removal uses exactly:

   ```text
   codex mcp remove belay
   claude mcp remove --scope user belay
   ```

## 4. Pre-P0-09 Baseline

`cmd/belay/local.go` already provides:

- packaged sibling Numbat pin bootstrap;
- private config and Keychain-backed store initialization;
- fail-open monitor-hook onboarding;
- Local HTTP startup and browser opening;
- live spool import and polling;
- asynchronous historical scan;
- `belay mcp`, which opens the same private store and serves the existing
  read-only MCP server over bounded stdio.

`internal/localapp.ManageHooks` establishes the relevant operational pattern:
fixed supported targets, bounded discovery and commands, per-target results,
and fail-open quickstart behavior.

The baseline before P0-09 had no MCP registration manager or durable ownership
record. The implemented feature adds both under the contracts below.

## 5. High-Level Design

```text
./bin/belay quickstart
        |
        +--> verify/materialize pinned packaged Numbat
        +--> create/load private state and Keychain-backed store
        +--> install detected monitor-only hooks (existing, fail-open)
        +--> inspect/install detected MCP configs (new, fail-open)
        |       |
        |       +--> inspect exact existing entry
        |       +--> classify ownership
        |       +--> add absent entries when safe and explicitly permitted
        |       +--> update recognized prior Claude entries only
        |       +--> verify resulting entry
        |       +--> persist verified ownership identity
        |
        +--> start Local
        +--> import existing live spool data
        +--> poll live data
        +--> begin historical scan in background
        +--> print loopback URL
        +--> attempt browser open
```

MCP registration is configuration management, not a second MCP implementation.
The configured command starts `belay mcp` on demand when the agent connects.

### 5.1 Detection

MCP install detection is intentionally based on the agent CLI itself:

- Codex is detected when `codex` resolves to a regular executable.
- Claude is detected when `claude` resolves to a regular executable.

Numbat inventory remains authoritative for hook and historical-scan targets.
The two signals are not conflated: a developer may have historical artifacts
without a currently invocable CLI, or an installed CLI without prior artifacts.

`status` and `uninstall` do not use Numbat discovery. They inspect each
available supported CLI so a developer can diagnose or clean up configuration
after agent artifacts disappear.

`status` and `uninstall` MUST NOT initialize Local state, create a Keychain
item, or create an ownership manifest. They may read an existing manifest.
Standalone `install` may initialize private Belay configuration/directories to
obtain a stable installation identity and may create the private ownership
manifest. It does not require, create, or open the Local database and does not
create a Keychain key.

### 5.2 Expected MCP identity

Resolve the current Belay executable with:

1. `os.Executable`;
2. `filepath.Abs`;
3. `filepath.EvalSymlinks`;
4. `Lstat`/`Stat` validation that the result is a regular executable.

Expected arguments are:

```json
["mcp"]
```

for the default state root, or:

```json
["mcp", "--home", "/absolute/private/belay/home"]
```

for an effective non-default root.

An owned entry must also be:

- stdio transport;
- named exactly `belay`;
- Codex's normal user configuration, or Claude scope exactly `user`;
- free of configured URL, headers, bearer token, extra environment, shell,
  working-directory override, or additional arguments.

Any extra launch behavior makes the entry foreign.

### 5.3 Private ownership manifest

Add a separate private file rather than changing Local config version:

```text
$BELAY_HOME/mcp-config-ownership.json
```

Logical shape:

```json
{
  "version": 1,
  "installation_id": "inst_...",
  "targets": {
    "codex": {
      "scope": "user",
      "command": "/absolute/old/archive/bin/belay",
      "args": ["mcp"],
      "verified_at": "2026-09-09T20:00:00Z"
    }
  }
}
```

Rules:

- mode `0600`, parent directory `0700`;
- atomic write, no symlink following, bounded decode, closed known fields;
- no secrets, tokens, CLI output, agent data, or event data;
- an identity becomes recognized only after post-install status verification;
- manifest identity must have an absolute command and one of the supported exact
  argument forms;
- status equality with the manifest is still required before mutation;
- missing/corrupt manifest does not make an entry foreign by itself: an exact
  current identity remains owned;
- corrupt/unreadable manifest prevents recognizing prior identities but never
  permits mutation of them.

The manifest enables a newly extracted archive to update a recognized prior
Claude entry or explicitly uninstall a recognized prior Codex/Claude entry. It
does not authorize install to mutate a recognized prior Codex entry.

### 5.4 CLI adapters and parsing

Each target has a small fixed adapter:

| Operation | Codex argv | Claude argv |
|---|---|---|
| status | `mcp get belay --json` | `mcp get belay` |
| install | `mcp add belay -- ABS_BELAY ARGS...` | `mcp add --scope user belay -- ABS_BELAY ARGS...` |
| uninstall | `mcp remove belay` | `mcp remove --scope user belay` |

Runner requirements:

- `exec.CommandContext`, no shell;
- `Stdin=io.Discard`;
- allowlisted inherited environment only;
- set non-interactive/no-color hints where supported without carrying secrets;
- stdout maximum 64 KiB, stderr maximum 16 KiB;
- UTF-8/control sanitization internally;
- ten-second deadline per CLI command;
- thirty-second transaction deadline per target;
- Codex and Claude targets may run concurrently under one 35-second onboarding
  deadline;
- timeout/crash/output overflow becomes fixed `unavailable` or `failed`.

Claude mutation is supported only for release-tested status/add behavior that
is safe under the ownership state machine. Codex add is disabled by default
because its duplicate-name behavior is non-atomic. With
`--allow-codex-mcp-add`, Belay may invoke Codex add only after a strict,
immediately preceding status inspection proves the `belay` entry absent. The
flag is explicit acceptance of the remaining race outside Belay's lock. Belay
never invokes Codex add against a present, foreign, or unverifiable entry.

Codex status is strictly decoded from JSON. The decoder accepts documented
additive fields but requires one unambiguous stdio command, argument array, and
absence of disallowed transport/settings.

Claude status is human-readable. The adapter supports only release-tested
bounded output fixtures with unambiguous name, user scope, stdio transport,
command, arguments, and launch settings. Unknown layout, duplicate fields,
ANSI/control ambiguity, missing scope, or truncated output is `unverifiable`.
It is never interpreted optimistically.

An absent entry is recognized only from a release-tested official not-found
exit/output fixture. Any other nonzero status result is `unavailable`, not
`absent`.

### 5.5 State machine

Status classification:

```text
absent
owned_current
owned_recognized
foreign
unverifiable
unavailable
```

Install:

| Pre-state | Behavior |
|---|---|
| `absent` | Claude: add and verify. Codex: report unavailable unless `--allow-codex-mcp-add`, then add and verify |
| `owned_current` | no-op |
| `owned_recognized` | Claude: ownership-safe update with rollback. Codex: report unavailable and do not mutate, regardless of `--allow-codex-mcp-add` |
| `foreign` | preserve and report conflict |
| `unverifiable` | preserve and report unverifiable |
| `unavailable` | no mutation |

Uninstall:

| Pre-state | Behavior |
|---|---|
| `absent` | no-op success |
| `owned_current` or `owned_recognized` | re-inspect, remove, verify absent |
| `foreign` | preserve and return unsatisfied |
| `unverifiable` or `unavailable` | no mutation |

Status never mutates.

### 5.6 Claude update/rollback and Codex archive moves

Updating a recognized prior Claude identity requires remove/add because the
official CLI exposes no compare-and-swap update:

1. acquire the per-home Belay MCP lock;
2. inspect and prove the old identity is recognized;
3. remove the old owned Claude entry;
4. add the current Claude identity;
5. inspect and prove the current Claude identity;
6. atomically record the new Claude identity.

If add or verification fails:

1. inspect again;
2. if current identity is present, treat installation as successful;
3. if the entry is absent, re-add and verify the old recognized Claude
   identity;
4. if the entry is foreign or unverifiable, stop without mutation.

Codex install has no recognized-entry update path. For
`owned_recognized`, `owned_current`, `foreign`, `unverifiable`, or
`unavailable`, the opt-in flag does not authorize remove, replacement, or
migration. A current exact Codex entry remains an idempotent no-op. For an
archive move, the user must:

1. run `mcp-config status` and verify the recognized prior Codex identity;
2. explicitly run `mcp-config uninstall`;
3. run `mcp-config status` and verify `absent`;
4. run `mcp-config install --allow-codex-mcp-add`.

Fresh-install verification failure removes the new entry only if a subsequent
status proves it is still exactly the current identity. Otherwise Belay stops.

Quickstart does not roll back successful hooks because MCP registration fails,
and it does not roll back successful MCP registration because a hook or scan
fails. These are independent local capabilities.

The agent CLIs do not offer cross-process conditional mutation. Belay's lock
serializes Belay processes, and immediate re-inspection narrows races with
external edits. A concurrent non-Belay editor is outside the atomicity boundary;
any detected ambiguity fails closed.

### 5.7 Quickstart sequencing and flags

`quickstart` adds:

```text
--no-mcp                 do not inspect or modify Codex or Claude MCP configuration
--allow-codex-mcp-add    accept Codex CLI non-atomic duplicate-name behavior after strict absence verification
```

Invocation without `--no-mcp` is explicit consent to monitor-only hook changes,
MCP inspection, and safe Claude MCP changes. It is not consent to add an absent
Codex entry. `--allow-codex-mcp-add` supplies that separate explicit consent.
If both flags are supplied, `--no-mcp` wins because no MCP inspection or
mutation occurs.

Frozen ordering:

1. resolve and verify packaged Numbat;
2. create/load private config;
3. open Keychain-backed Local store;
4. attempt hooks;
5. attempt MCP onboarding unless `--no-mcp`, passing Codex add permission only
   when `--allow-codex-mcp-add` is present;
6. start loopback Local;
7. import existing live spool data and begin live polling;
8. begin historical scan asynchronously;
9. print URL to stdout;
10. attempt browser open unless `--no-open`;
11. remain attached to Local until cancellation.

Long historical scanning remains asynchronous so one-command onboarding opens
Local promptly.

`belay local` remains unchanged. MCP registration is not implied by
`belay local --install-hooks`.

## 6. Output and Exit Contract

### 6.1 Explicit command JSON

`belay mcp-config install|status|uninstall` writes exactly one JSON object to
stdout:

```json
{
  "schema_version": "belay.mcp-config.v1",
  "action": "install",
  "overall_status": "complete",
  "targets": [{
    "agent": "codex",
    "detected": true,
    "scope": "user",
    "status": "installed",
    "ownership": "current",
    "changed": true,
    "error_code": null
  }, {
    "agent": "claude",
    "detected": false,
    "scope": "user",
    "status": "skipped_not_detected",
    "ownership": "none",
    "changed": false,
    "error_code": null
  }]
}
```

Arrays are deterministic: Codex, then Claude. No executable or home path is
printed.

`overall_status` is `complete|partial`.

`ownership` is:

```text
current | recognized | none | foreign | unknown
```

Action-specific `status` values:

- install:
  `installed|already_installed|updated|skipped_not_detected|foreign_preserved|unverifiable|unavailable|failed|rollback_restored|rollback_failed`;
- status:
  `owned_current|owned_recognized|absent|foreign|unverifiable|unavailable`;
- uninstall:
  `removed|absent|foreign_preserved|unverifiable|unavailable|failed`.

`error_code` is null or one fixed value:

```text
cli_not_found
status_timeout
status_failed
status_output_too_large
status_unparseable
ownership_manifest_invalid
conflicting_entry
install_failed
verification_failed
remove_failed
rollback_failed
lock_timeout
invalid_executable
invalid_home
```

Raw subprocess text and wrapped errors are never serialized.

`updated`, `rollback_restored`, and `rollback_failed` apply to the recognized
Claude update path. Codex install never returns an update/rollback result for a
recognized prior entry; it reports unavailable without mutation.

Exit status:

- `status` exits zero when both available CLIs were successfully classified,
  including `absent` or `foreign`; unavailable/unverifiable targets make it
  nonzero.
- `install` exits zero only when every detected target is installed,
  already installed, or, for Claude only, safely updated. Undetected targets
  are neutral.
- `uninstall` exits zero only when every available target is absent or safely
  removed.
- JSON is still written before a nonzero return.

### 6.2 Quickstart human summary

Quickstart preserves stdout for the Local URL. MCP/hook status goes to stderr
using fixed payload-free lines:

```text
belay quickstart: hooks codex=configured claude=skipped_not_detected
belay quickstart: mcp codex=unavailable claude=installed
```

Hook summary values are exactly
`configured|failed|skipped_not_detected|unavailable`. MCP summary values are
exactly
`installed|already_installed|updated|foreign_preserved|unverifiable|unavailable|failed|rollback_restored|rollback_failed|skipped_not_detected|skipped_by_user`.

When any requested onboarding operation is incomplete:

```text
belay quickstart: onboarding incomplete; Local will continue
```

With `--no-mcp`:

```text
belay quickstart: mcp skipped_by_user
```

With `--allow-codex-mcp-add`, quickstart first emits the fixed payload-free
acceptance notice and may report `codex=installed` only after strict absence
verification, add, and post-install verification.

The existing URL remains one line on stdout. Browser-open, hook, MCP, live
import, and historical-scan failures remain non-fatal after Local prerequisites
succeed.

## 7. Low-Level Design

### 7.1 Local application package

Add a focused manager under `internal/localapp`, for example:

```go
type MCPConfigAction string

type MCPConfigResult struct {
    SchemaVersion string                  `json:"schema_version"`
    Action        MCPConfigAction         `json:"action"`
    OverallStatus string                  `json:"overall_status"`
    Targets       []MCPConfigTargetResult `json:"targets"`
}

type MCPConfigTargetResult struct {
    Agent     string  `json:"agent"`
    Detected  bool    `json:"detected"`
    Scope     string  `json:"scope"`
    Status    string  `json:"status"`
    Ownership string  `json:"ownership"`
    Changed   bool    `json:"changed"`
    ErrorCode *string `json:"error_code"`
}
```

Recommended internal seams:

- `ResolveBelayMCPIdentity(executable, home)`;
- `MCPConfigManager.Manage(ctx, action, installDetectedOnly)`;
- target adapters for Codex JSON and Claude text status;
- injected `LookPath`, command runner, clock, and manifest store for tests;
- one shared fixed-output runner, not reuse of the Numbat client;
- private ownership manifest loader/writer;
- per-home advisory lock.

The manager must not import storage, readmodel, or localmcp. It manages only
external launch configuration.

### 7.2 CLI

Add dispatch and help for `mcp-config`. It accepts:

```text
belay mcp-config ACTION [--home PATH] [--allow-codex-mcp-add]
```

No generic executable, command, argument, scope, transport, URL, or environment
flags are allowed. `--allow-codex-mcp-add` is accepted only for `install`.

Quickstart adds `--no-mcp` and `--allow-codex-mcp-add`, and carries both MCP
install intent and Codex add permission. Production wiring resolves the current
executable once and passes the same effective Belay home used by the runtime.

### 7.3 Files likely involved

New:

- `internal/localapp/mcp_config.go`;
- `internal/localapp/mcp_config_test.go`;
- `internal/localapp/mcp_config_manifest.go`;
- `internal/localapp/mcp_config_manifest_test.go`;
- `cmd/belay/mcp_config.go`;
- `cmd/belay/mcp_config_test.go`.

Minimal existing:

- `internal/localapp/config.go` and tests for new private path fields/helpers;
- `cmd/belay/main.go` and tests for dispatch/help;
- `cmd/belay/local.go` and tests for quickstart wiring and fail-open behavior;
- packaging/launch documentation listed in the implementation brief.

No canonical, storage migration, readmodel, Local HTTP, browser, or MCP tool
code change is required.

## 8. Failure Modes

| Failure | Required behavior |
|---|---|
| CLI not found | install skips target; status/uninstall report unavailable |
| status timeout/crash | no mutation; fixed unavailable result |
| unknown Claude output | no mutation; unverifiable |
| existing foreign `belay` | preserve; conflict result |
| add fails but post-status is current | treat as installed |
| add succeeds but verification is absent | fail; no blind remove |
| recognized Claude update add fails | restore old Claude identity only from confirmed absent state |
| recognized prior Codex install | no mutation; unavailable until explicit uninstall and absent-state opt-in install |
| rollback cannot be proven | stop; rollback failed; never remove unknown entry |
| manifest missing | current exact entry remains owned; old path is not recognized |
| manifest corrupt | current exact entry remains owned; report manifest warning |
| manifest write fails after install | keep verified config, report partial; no destructive rollback |
| lock timeout | no mutation; fail-open in quickstart |
| browser/hook/scan failure | preserve existing Local fail-open behavior |

## 9. Tests and Release Proof

### 9.1 Unit and command tests

- exact argv for all Codex and Claude operations;
- stdin is closed/discarded; no shell;
- default and custom-home MCP argument forms;
- output bounds, timeout, crash, cancellation, and cleanup;
- strict Codex JSON fixtures;
- strict Claude text fixtures for every supported release;
- Claude duplicate-name behavior is covered by release-tested fixtures;
- Codex add is denied without `--allow-codex-mcp-add`, and the opt-in path
  proves strict absence before invoking add;
- absent/owned-current/owned-recognized/foreign/unverifiable classification;
- no overwrite of foreign stdio, HTTP, wrong-scope, extra-arg, env, or cwd entry;
- install idempotency;
- recognized Claude update and rollback matrix;
- recognized prior Codex remains unchanged with and without the opt-in;
- Codex archive move requires status, explicit uninstall, absence verification,
  and opt-in reinstall;
- uninstall only exact owned identities;
- private atomic manifest, malformed manifest, symlink rejection, and mode checks;
- two concurrent Belay managers serialize and do not lose ownership state;
- fixed result JSON and payload-free errors;
- no executable/home path in command output;
- quickstart `--no-mcp`;
- quickstart default does not add absent Codex;
- quickstart and direct-install `--allow-codex-mcp-add`;
- direct-install consent and status/uninstall no-state-creation behavior;
- standalone install may initialize private config/directories but creates no
  Local database or Keychain item;
- quickstart MCP success, conflict, timeout, and partial failure all continue to
  Local and browser opening;
- `belay local` remains MCP-neutral;
- `belay mcp` still serves exactly nine read-only tools.

### 9.2 Isolated integration tests

Use temporary `HOME`, `CODEX_HOME`, and `CLAUDE_CONFIG_DIR` with fake CLIs.
Tests MUST never read or modify the developer's real agent configuration.

Release smoke on a clean macOS account:

1. extract archive into a path containing spaces;
2. run `./bin/belay quickstart --allow-codex-mcp-add --no-open`;
3. verify both exact registrations with official status commands;
4. rerun quickstart and prove no duplicate/change;
5. uninstall, run normal quickstart, and prove Claude may install while absent
   Codex remains unmodified;
6. place a foreign entry named `belay` and prove it is preserved;
7. move to a new extracted archive and prove Claude recognized ownership may be
   safely updated while Codex remains unchanged and unavailable;
8. verify the prior Codex identity, explicitly uninstall it, verify absence,
   rerun with `--allow-codex-mcp-add`, and prove the new identity is installed;
9. run `mcp-config uninstall`, verify absence, and prove hooks/Local data remain;
10. run standalone install in an empty home and prove it creates no database or
   Keychain item;
11. run with network disabled;
12. confirm no prompts and bounded completion when a CLI hangs.

## 10. Documentation and Release Updates

Implementation must update:

- `README.md` and `llms.txt`;
- `docs/launch/developer-preview.md`;
- `docs/launch/local-v0-requirements.md`;
- `docs/launch/clean-machine-alpha-qa.md`;
- `docs/contracts/mcp-v1.md` only to describe local registration, not new tools;
- archive/build smoke scripts to verify executable layout and exact linker
  Numbat pin behavior before quickstart smoke.

Claims must remain:

- local and read-only MCP;
- monitor-only hooks;
- no prompts/completions/file contents sent to Belay;
- no network or paid service required by Belay;
- partial onboarding can be inspected and retried;
- removing MCP configuration does not remove hooks or Local data.

## 11. Open Questions

None blocking. Claude's human-readable status format is an external compatibility
surface; each supported release must have a checked-in parser fixture and
release smoke proof. Unknown formats fail closed.

## 12. Task Breakdown

1. Implement private identity/manifest and lock.
2. Implement bounded fixed command runner and target adapters.
3. Implement strict status classification and ownership state machine.
4. Implement absent install, Claude-only update/rollback, Codex recognized-entry
   refusal, and explicit uninstall orchestration.
5. Add `mcp-config` CLI and exact JSON contract.
6. Wire quickstart consent, `--no-mcp`, `--allow-codex-mcp-add`, summary, and
   fail-open continuation.
7. Add focused unit/integration tests.
8. Update launch, MCP, archive, and clean-machine QA documentation.
9. Run isolated no-network release smoke.

## 13. Self-Review Decisions

The design was tightened in five areas before approval:

1. It does not treat the name `belay` as ownership.
2. It persists verified prior identities so archive upgrades are recoverable.
3. It handles custom Belay homes explicitly instead of assuming environment
   inheritance.
4. It treats Claude's unstructured status as fail-closed, not best-effort.
5. It preserves URL-only stdout and makes every onboarding failure independent
   so MCP configuration can never prevent Local from opening.
6. It keeps Codex add fail-closed by default and requires explicit acceptance
   of the CLI's non-atomic duplicate-name behavior after strict absence
   verification.
