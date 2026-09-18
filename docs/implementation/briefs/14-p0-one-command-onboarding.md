# Implementation Brief: P0-09 One-Command Onboarding

## How to Use This Brief

Before coding:

1. Read this brief and
   `docs/design/p0-09-one-command-onboarding.md` completely.
2. Inspect current `quickstart`, `ManageHooks`, private path/config helpers,
   browser opening, `belay mcp`, packaging, and launch QA.
3. Treat command shapes, ownership classification, output enums, deadlines,
   rollback behavior, and fail-open rules as frozen.

During implementation:

4. Use direct fixed-argument execution only.
5. Never touch a foreign or unverifiable `belay` MCP entry.
6. Keep quickstart stdout reserved for the Local URL.
7. Keep the existing MCP server and nine-tool surface unchanged.
8. Do not add network calls, telemetry, paid-service dependencies, or storage
   migrations.

When done:

9. Report changed paths, focused tests, isolated CLI fixtures, release-smoke
   evidence, deviations, and blockers.
10. Do not merge, publish, deploy, or incur costs without approval.

## 1. Goal

Make the extracted alpha archive genuinely one-command:

```text
./bin/belay quickstart
```

Quickstart must verify/materialize packaged Numbat, initialize private
Keychain-backed Local state, install monitor-only hooks for detected agents,
safely inspect supported MCP configuration, register Claude when status is
safely understood, import/scan, start Local, print its loopback URL, and attempt
to open the browser. Normal quickstart must not add an absent Codex entry. The
explicit one-command path for Codex is:

```text
./bin/belay quickstart --allow-codex-mcp-add
```

That flag permits Codex add only after strict absence verification and records
the user's acceptance of the Codex CLI's non-atomic duplicate-name behavior.

MCP registration failures are summarized and never prevent Local from opening.

## 2. Scope

### In scope

- ownership-safe Codex and Claude MCP configuration manager;
- private verified ownership manifest;
- bounded non-interactive subprocess runner;
- `belay mcp-config install|status|uninstall`;
- quickstart MCP consent, `--no-mcp`, and
  `--allow-codex-mcp-add`;
- deterministic JSON and quickstart summary contracts;
- Claude-only rollback and archive-upgrade behavior;
- Codex recognized-prior refusal plus explicit uninstall/absent-state reinstall;
- focused tests, launch docs, packaging smoke, and clean-machine QA.

### Out of scope

- changing `belay mcp` behavior or tools;
- HTTP/remote MCP;
- OAuth, login, hosted services, or network setup;
- modifying arbitrary MCP names, commands, args, env, scope, or transport;
- registering unsupported agents;
- changing `belay local --install-hooks`;
- storage schema/canonical/readmodel/browser changes;
- automatic hook uninstall when MCP is uninstalled;
- deleting Local data or Keychain entries.

## 3. Frozen Contracts

### 3.1 Commands

```text
belay quickstart [--no-mcp] [--allow-codex-mcp-add] [--no-open] [existing runtime flags]
belay mcp-config install [--allow-codex-mcp-add] [--home PATH]
belay mcp-config status [--home PATH]
belay mcp-config uninstall [--home PATH]
belay mcp
```

`quickstart` without `--no-mcp` is explicit consent to detected monitor-hook
changes, MCP inspection, and safe Claude user-configuration changes. It does
not consent to adding an absent Codex entry.
`--allow-codex-mcp-add` is the separate explicit opt-in for that operation.
Direct `mcp-config install` follows the same split. `belay local` remains
MCP-neutral.

### 3.2 Agent CLI argv

Default home:

```text
codex mcp get belay --json
codex mcp add belay -- ABS_BELAY mcp
codex mcp remove belay

claude mcp get belay
claude mcp add --scope user belay -- ABS_BELAY mcp
claude mcp remove --scope user belay
```

For a non-default effective Belay home, install args become:

```text
ABS_BELAY mcp --home ABS_BELAY_HOME
```

No other command shape is permitted.

### 3.3 Ownership

Owned means exact:

- entry name `belay`;
- stdio transport;
- expected scope;
- canonical current executable or exact verified manifest executable;
- exact supported argument vector;
- no URL, headers, bearer token, extra environment, cwd, shell, or extra args.

Foreign or unverifiable entries are preserved. Uninstall removes only exact
owned entries.

Codex add is never attempted against a present entry. It is disabled by default
and may run only with `--allow-codex-mcp-add` after strict status parsing proves
the `belay` entry absent. The flag explicitly accepts the host CLI's non-atomic
duplicate-name behavior.

### 3.4 Bounds

- per CLI command: 10 seconds;
- per target transaction: 30 seconds;
- overall MCP onboarding: 35 seconds;
- stdout capture: 64 KiB;
- stderr capture: 16 KiB;
- at most two concurrent target transactions;
- stdin discarded;
- no shell.

### 3.5 Result contract

Schema:

```text
belay.mcp-config.v1
```

Top level:

```json
{
  "schema_version": "belay.mcp-config.v1",
  "action": "status",
  "overall_status": "complete",
  "targets": []
}
```

Target order is always Codex then Claude. Each target has:

```json
{
  "agent": "codex",
  "detected": true,
  "scope": "user",
  "status": "owned_current",
  "ownership": "current",
  "changed": false,
  "error_code": null
}
```

Use only the status, ownership, overall-status, and error-code enums frozen in
the design. Never serialize paths or subprocess output.

### 3.6 Exit behavior

- explicit commands always write JSON if command parsing succeeded;
- explicit commands return nonzero when the requested operation is not safely
  satisfied;
- quickstart consumes the same results but always continues after MCP failure;
- state/Keychain/Numbat/Local startup failures retain their existing fatal
  behavior;
- browser, hook, MCP, import, and scan failures remain non-fatal after Local
  prerequisites succeed.

## 4. Service A: MCP Configuration Manager

- **Owns:** `internal/localapp/mcp_config*.go` and focused tests
- **May minimally edit:** `internal/localapp/config.go` for private path helpers
- **Must not edit:** storage, readmodel, localmcp tools, HTTP, browser

### Tasks

1. Define closed action/status/ownership/error enums and result DTOs.
2. Resolve and validate canonical current Belay identity.
3. Support exact default and custom-home MCP argument vectors.
4. Add direct fixed command runner with:
   - injected executable lookup and runner for tests;
   - allowlisted environment;
   - discarded stdin;
   - output bounds;
   - context deadline and process cleanup;
   - payload-free errors.
5. Add Codex adapter:
   - strict JSON status decode;
   - exact get/add/remove argv;
   - official absent fixture handling.
6. Add Claude adapter:
   - strict release-fixture text parsing;
   - exact user-scope get/add/remove behavior;
   - unknown or ambiguous output becomes unverifiable.
7. Add private version-1 ownership manifest:
   - installation identity binding;
   - atomic `0600` writes;
   - closed bounded decode;
   - no symlink following;
   - current and prior verified identities.
8. Add per-home advisory lock with bounded acquisition.
9. Keep Codex add disabled unless the request carries the explicit opt-in.
   With the opt-in, require a strict immediately preceding `absent`
   classification before add. Preserve present, foreign, and unverifiable
   entries.
10. Implement status classification.
11. Implement absent install and current no-op for both targets. Implement
    recognized update, verification, and rollback only for Claude. A recognized
    prior Codex install must return unavailable without mutation, regardless of
    the opt-in.
12. Implement uninstall with immediate ownership recheck and absence
    verification.
13. Ensure manifest writes happen only after verified state.
14. Ensure status and uninstall do not initialize Local state, create Keychain
    material, or create a missing ownership manifest. Standalone install may
    initialize private Belay configuration/directories for a stable
    installation ID and create ownership-manifest files, but must not create or
    open the Local database or create Keychain material.

### Acceptance

- no generic command execution API is exported;
- no foreign/unverifiable entry is mutated in any tested state;
- repeated install is a no-op;
- old Claude archive identity updates safely or is restored;
- old Codex archive identity remains unchanged until the user explicitly
  uninstalls it, verifies absence, and reruns the opt-in install;
- exact current/recognized uninstall works;
- all results are deterministic and payload-free.

### Focused tests

- exact argv and environment;
- path with spaces remains one argv element;
- symlink/current executable validation;
- custom-home args;
- timeout, crash, cancellation, output overflow, and no stdin prompt;
- Codex fixture matrix;
- Codex default-denied and explicit-opt-in add behavior;
- Claude fixture matrix and unknown-format rejection;
- strict absence proof immediately before Codex add;
- no Codex add against present, foreign, or unverifiable state;
- all ownership states;
- duplicate/add conflict behavior;
- post-error status reconciliation;
- fresh-install verification failure;
- Claude update rollback success/failure;
- Codex recognized-prior refusal with and without the opt-in;
- Codex archive-move status → explicit uninstall → absent → opt-in install;
- external foreign state discovered after mutation;
- manifest fresh/load/upgrade-equivalent/corrupt/symlink/mode cases;
- concurrent managers and lock timeout;
- JSON enum and no-path/no-payload canaries.

## 5. Service B: CLI and Quickstart Integration

- **Owns:** `cmd/belay/mcp_config.go`, `cmd/belay/main.go`,
  `cmd/belay/local.go`, and focused tests
- **Must not edit:** Local HTTP/browser/readmodel/localmcp behavior

### Tasks

1. Add `mcp-config` dispatch, help, action validation, and `--home`.
2. Write result JSON before returning aggregate operation error.
3. Add `--no-mcp` and `--allow-codex-mcp-add` to quickstart.
4. Update quickstart help to state:
   - invocation consents to monitor-only hooks;
   - invocation consents to MCP inspection and safe Claude registration;
   - normal quickstart does not add an absent Codex entry;
   - `--allow-codex-mcp-add` explicitly accepts Codex's non-atomic
     duplicate-name behavior after strict absence verification;
   - `--no-mcp` opts out only from MCP registration;
   - Local remains on-device and no prohibited content/telemetry is sent.
5. Extend launch options with MCP-install intent and Codex add permission.
6. After store initialization, run hook onboarding and MCP onboarding as
   independent fail-open operations.
7. Resolve one canonical Belay executable and effective home for registration.
8. Emit fixed stderr summary:

   ```text
   belay quickstart: hooks codex=... claude=...
   belay quickstart: mcp codex=... claude=...
   ```

9. Emit the fixed incomplete line if either requested subsystem is partial.
10. Preserve one-line URL stdout and existing browser/open behavior.
11. Preserve `belay local`, `belay local --install-hooks`, and `belay mcp`.

### Acceptance

- normal quickstart may register safely understood Claude but does not add
  absent Codex, and still opens Local;
- quickstart with `--allow-codex-mcp-add` may register Codex after strict
  absence verification;
- `--no-mcp` performs no MCP status or mutation command;
- one-agent detection touches only that agent;
- conflict/timeout/partial MCP failure still starts Local and attempts browser
  opening;
- explicit `mcp-config install` returns nonzero for the same partial result;
- no raw CLI output, path, or secret appears in summaries/errors;
- normal Local and MCP tests remain unchanged.

### Focused tests

- dispatch and help;
- quickstart consent/default, `--allow-codex-mcp-add`, and `--no-mcp`;
- exact sequencing around state initialization and Local start;
- zero, one, and two detected CLIs;
- install success/idempotency/conflict/timeout;
- summary target ordering and fixed copy;
- fixed hook summary values
  `configured|failed|skipped_not_detected|unavailable`;
- direct-install consent and status/uninstall no-state-creation behavior;
- URL remains stdout-only;
- browser failure remains fail-open;
- `local --install-hooks` does not invoke MCP manager;
- existing `mcp` subcommand still starts the server.

## 6. Service C: Documentation, Packaging, and Release QA

- **Owns:** launch/docs and packaging verification only
- **Must not claim:** new MCP tools, remote service, diagnosis, writes, or
  guaranteed success when agent CLI output is unsupported

### Required updates

- `README.md`;
- `llms.txt`;
- `docs/contracts/mcp-v1.md`;
- `docs/launch/local-v0-requirements.md`;
- `docs/launch/developer-preview.md`;
- `docs/launch/clean-machine-alpha-qa.md`;
- archive/build/alpha-readiness smoke tests as appropriate.

### Required documentation semantics

1. Quickstart changes detected hooks and safely understood Claude MCP
   configuration; absent Codex add requires
   `--allow-codex-mcp-add`.
2. Hooks are monitor-only and MCP remains read-only.
3. `--no-mcp` skips MCP registration.
4. `mcp-config status` inspects exact ownership.
5. Uninstall removes only exact Belay-owned MCP entries.
6. MCP uninstall does not remove hooks, Keychain data, or Local history.
7. A foreign `belay` entry is preserved and must be resolved by the user.
8. Archive moves may update a recognized prior Claude registration. Belay never
   updates or migrates a recognized prior Codex registration automatically.
   The user must verify it with `mcp-config status`, explicitly run
   `mcp-config uninstall`, verify absence, and then rerun the opt-in command.
9. Belay itself requires no network, login, or paid service.
10. Status parser incompatibility is reported as unverifiable and never causes
    mutation.

### Release smoke

Run with isolated user directories and network disabled:

1. clean extract with path spaces;
2. quickstart default proving absent Codex is not added;
3. quickstart with `--allow-codex-mcp-add`;
4. official status verification for both agents;
5. repeated quickstart no-op;
6. `--no-mcp`;
7. foreign-entry preservation;
8. archive move/recognized Claude update;
9. recognized prior Codex refusal, explicit uninstall, absence verification,
   and opt-in reinstall;
10. standalone install creates private config/ownership state but no database or
   Keychain material;
11. uninstall and absence verification;
12. hanging/crashing CLI fail-open;
13. exactly nine read-only MCP tools.

## 7. Sequencing

1. Service A DTOs, identity, runner, and status parsers.
2. Service A manifest, lock, ownership state machine, and rollback.
3. Service B explicit command.
4. Service B quickstart integration, Codex opt-in, and summaries.
5. Focused fake-CLI tests.
6. Service C docs and packaging smoke.
7. Clean-machine no-network release proof.

Service B must not guess Service A enums or duplicate status parsing. Service C
must use the final exact output contract.

## 8. Risks and Required Mitigations

| Risk | Mitigation |
|---|---|
| Host CLI output changes | strict release fixtures; unknown means unverifiable |
| Foreign entry named `belay` | exact ownership comparison; preserve |
| Archive path changes | Claude may use the verified prior-identity update path; Codex requires verified explicit uninstall and absent-state opt-in reinstall |
| Custom Belay home lost at agent launch | explicit `mcp --home ABS_HOME` args |
| CLI prompts/hangs | discarded stdin and bounded context |
| CLI leaks diagnostics | bounded private capture; fixed public errors |
| Two quickstarts race | per-home lock and post-operation verification |
| External config edit races | immediate recheck; stop on ambiguity |
| MCP failure blocks launch | quickstart consumes result and continues |
| Docs imply hosted/paid behavior | explicit local/read-only/no-network wording |

## 9. Completion Checklist

- [ ] Exact command shapes implemented.
- [ ] Ownership manifest private and atomic.
- [ ] Foreign/unverifiable entries never mutated.
- [ ] Install, Claude update/rollback, Codex recognized refusal, and uninstall
      matrix tested.
- [ ] Quickstart `--no-mcp` tested.
- [ ] Quickstart and direct-install `--allow-codex-mcp-add` tested.
- [ ] Default quickstart cannot add absent Codex.
- [ ] URL-only stdout preserved.
- [ ] Existing `belay mcp` and nine tools unchanged.
- [ ] No network dependency introduced.
- [ ] Focused tests and race tests pass for changed packages.
- [ ] Full `go test ./...`, `go vet ./...`, formatting, and diff check pass.
- [ ] Isolated clean-machine release smoke passes.
