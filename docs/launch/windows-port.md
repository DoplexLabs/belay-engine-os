# Belay Local on Windows

Status: Windows is an alpha channel alongside Apple Silicon macOS. The
supported surface is Windows 10 build 1809 or newer and Windows 11 on x64
(`amd64`). `windows/arm64` is an engineering build target: it is built and
vetted in CI but is not published.

The Windows build is unsigned. It carries no Authenticode signature and no
SmartScreen reputation, exactly as the macOS build carries no notarization.

This page records what the port does, how it is built, verified and installed,
what is proven today, and what is still open.

## What is verified today

These facts are established by code, cross-compilation, and the CI jobs in this
repository:

- `belay.exe` and the pinned Numbat fork both cross-compile for
  `windows/amd64` and `windows/arm64` with `CGO_ENABLED=0`. The pure-Go SQLite
  driver needs no C toolchain. `make verify-windows` builds and vets
  `windows/amd64` and builds `windows/arm64`.
- The Local data key is protected with the Windows Data Protection API
  (DPAPI) instead of macOS Keychain. Belay wraps the random 32-byte key in
  user scope with UI forbidden and stores the wrapped blob under
  `${BELAY_HOME:-%USERPROFILE%\.belay}\keys\<store-id>.key`. Only the same
  Windows account can unwrap it. The wrapping entropy includes the store ID,
  so a blob copied to another store does not unwrap. A missing or foreign key
  file fails closed; Belay never creates a replacement key for an existing
  store.
- Executable discovery uses the Windows extension allowlist
  (`.exe`, `.com`, `.bat`, `.cmd`) because Go exposes no executable mode bit
  on Windows. The packaged sibling is `bin\numbat.exe`, and the private
  verified copy is materialized as `bin\numbat-<sha16>.exe`.
- Subprocess environments forward the Windows profile, application-data,
  temp, `PATHEXT`, and `COMSPEC` variables in addition to the Unix allowlist.
  No other parent variables reach Numbat or the agent CLIs.
- Browser opening uses `rundll32 url.dll,FileProtocolHandler`.
- Release checks look for a `-windows-<arch>.zip` asset on Windows; see
  [`../contracts/update-check-v1.md`](../contracts/update-check-v1.md).
- Telemetry sends `os` as `windows` and `arch` as the Go architecture; see
  [`../contracts/telemetry-v1.md`](../contracts/telemetry-v1.md). No field is
  added for Windows.
- The MCP configuration lock, intelligence lease, and private-file helpers
  have non-Unix fallbacks and are exercised by the Windows CI job.
- `scripts/build-developer-preview.sh --os windows` produces a zip mirroring
  the macOS layout with `bin/belay.exe`, `bin/numbat.exe`, and an added
  `docs/windows-port.md`. `BUILD-INFO.txt` records `target=windows/<arch>`,
  `signed=false`, and `notarized=false`.
- `scripts/smoke-windows-preview.ps1` checks archive paths, the internal
  `SHA256SUMS`, the Numbat version marker, `belay.exe help`, and a read-only
  `belay.exe agents` run with disposable `USERPROFILE` and `--home`
  directories. It never installs hooks, creates a DPAPI key, opens the
  database, or opens a browser.

## What is not verified yet

Everything a real machine has to prove is listed as a gate in
[`windows-clean-machine-alpha-qa.md`](windows-clean-machine-alpha-qa.md). That
checklist has not been executed, so the following are expectations, not
results:

- DPAPI key creation without any prompt, and key reuse across sign-out,
  restart, and a normal password change.
- Hook installation, execution, and live capture for Codex and Claude Code,
  including agent CLIs installed by npm as `.cmd` shims.
- MCP registration through the host CLIs with a quoted Windows path.
- The loopback browser, offline operation, the privacy canary, fail-open
  interruption, and the installer's uninstall boundary.
- Semantic analysis through the Cursor CLI (`agent`, or `cursor-agent` on
  older installs) and the Antigravity CLI (`agy`). Both paths were written
  from the vendors' published documentation on macOS; neither CLI was
  installed on the development machine, and nothing about either has been run
  on Windows. The Antigravity CLI's documented Windows install location is
  `%LOCALAPPDATA%\agy\bin`; the Cursor CLI's Windows install location is not
  recorded here. Both are unvalidated expectations until gate W09c records a
  result.

Do not state any of these as verified until the checklist records a pass.

## Build a Windows archive

From macOS or Linux, in a clean checkout:

```bash
make verify-windows
make preview-windows ALPHA_VERSION=0.0.1-alpha.11 WINDOWS_ARCH=amd64
```

Expected output:

```text
dist/belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip
dist/belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip.sha256
```

The script never signs Windows binaries; `--codesign-identity` is rejected for
this target.

## Verify on a Windows machine

Copy the zip and its `.sha256` file to Windows, then in PowerShell:

```powershell
Get-FileHash -Algorithm SHA256 .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip
Get-Content .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip.sha256
scripts\smoke-windows-preview.ps1 -Archive .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip
```

Manual first run from the extracted package:

```powershell
.\bin\belay.exe local
```

Windows SmartScreen may warn because the binaries are unsigned. Use
**More info → Run anyway** for the specific binary only after verifying the
checksum. Do not disable SmartScreen.

## Install and uninstall

The published path is the PowerShell installer, which mirrors
`scripts/install.sh`:

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

It downloads the exact prerelease from the public `DoplexLabs/belay`
distribution repository, verifies the release checksum and the embedded package
checksums, rejects dirty builds, installs the runtime under
`%LOCALAPPDATA%\Belay\runtime`, writes a `belay.cmd` shim to
`%LOCALAPPDATA%\Belay\bin`, and adds that directory to the user `PATH`. It
needs no elevation. Open a new terminal afterwards so the updated `PATH` is
visible. Re-running it upgrades in place.

`irm ... | iex` cannot pass arguments, so uninstall runs the script itself:

```powershell
irm https://getbelay.vercel.app/install.ps1 -OutFile install.ps1
.\install.ps1 -Uninstall
```

or, without saving a file:

```powershell
& ([scriptblock]::Create((irm https://getbelay.vercel.app/install.ps1))) -Uninstall
```

Uninstall removes the runtime, the shim, and the `PATH` entry. It preserves
`%USERPROFILE%\.belay`, including the wrapped data keys.

## Continuous integration

- The macOS `verify` job cross-compiles and vets every package for
  `windows/amd64` and builds `windows/arm64`. A change that breaks the
  Windows build fails CI.
- A native `windows` job builds `belay.exe` and Numbat, runs the read-only
  smoke path, and runs the Go test suite. The smoke and test steps are marked
  `continue-on-error` while the punch list below is open, so they report
  rather than block. Remove that marker once they pass consistently.
- The developer-preview workflow builds the unsigned
  `windows-amd64.zip` and its `.sha256` and publishes them with the macOS
  assets and `install.ps1` when publication is explicitly authorized.

## Open punch list

- Authenticode signing and a SmartScreen reputation plan. Until then every
  first run shows a SmartScreen warning, which the README, the developer-alpha
  document, and the Windows QA checklist all describe.
- Execute [`windows-clean-machine-alpha-qa.md`](windows-clean-machine-alpha-qa.md)
  on a fresh Windows 11 account and record every gate. This is the gate for the
  Windows channel, not an optional extra.
- Validate the pinned Numbat fork's own Windows behavior: harness discovery,
  hook installation into Claude Code, Codex, Cursor, and Antigravity
  configuration, and live spool writes with backslash paths. Belay does not
  patch Numbat; gaps go upstream.
- Validate the Cursor surface on Windows: the `%USERPROFILE%\.cursor\mcp.json`
  write Belay performs itself, `%USERPROFILE%\.cursor\hooks.json`, the skill
  at `%USERPROFILE%\.cursor\skills\belay\SKILL.md`, and the transcript
  reader under `%USERPROFILE%\.cursor\projects`. No clean-machine run has
  covered Cursor on either platform, and the transcript reader has only
  synthetic fixtures behind it.
- Validate the Antigravity surface on Windows: detection of
  `%USERPROFILE%\.gemini\antigravity` as a real directory, the
  `%USERPROFILE%\.gemini\config\mcp_config.json` write Belay performs itself
  (including creating `%USERPROFILE%\.gemini\config` when absent),
  `%USERPROFILE%\.gemini\config\hooks.json`, the skill at
  `%USERPROFILE%\.gemini\config\skills\belay\SKILL.md`, and the
  `live\antigravity.ndjson` spool. Antigravity is hook-only with no historical
  scan and no transcript reader; its support was developed on macOS against
  Antigravity 2.0.1 without a captured live session, and no clean-machine run
  has covered it on either platform.
- Validate semantic analysis through the Cursor CLI and the Antigravity CLI on
  Windows (gate W09c): detection of `cursor-agent`, or of `agent` together
  with a real `%USERPROFILE%\.cursor` directory; detection of `agy` under its
  documented `%LOCALAPPDATA%\agy\bin` location rather than the IDE launcher,
  together with a real `%USERPROFILE%\.gemini\antigravity-cli` directory;
  backslash quoting of the `--json-schema` file path and the private
  `--workspace` directory; and whether either CLI resolves as a `.cmd` shim.
  Neither CLI was installed on the development machine, so none of this has
  been validated against a real run on any platform.
- Confirm `.cmd` shims for `claude` and `codex` installed by npm run correctly
  through `belay hooks` and `belay mcp-config` (Go executes batch files through
  `cmd.exe`), and that an installed `belay.exe` path containing a space
  survives quoting in the recorded hook and MCP commands.
- Run the native test suite on Windows and fix every failure. Tests that write
  `#!/bin/sh` fixtures or assert POSIX permission bits need Windows variants;
  the CI job currently reports rather than blocks.
- `winget` packaging, after Authenticode signing. It is not part of the alpha.
- `belay-eval` benchmark tooling still shells out to `cp` for dependency
  caches and cannot kill process groups on Windows. It is not part of the
  shipped package and does not block the alpha.

## Data locations

| Item | Windows path |
|---|---|
| Belay home | `%USERPROFILE%\.belay` unless `BELAY_HOME` is set |
| Database | `%USERPROFILE%\.belay\belay.sqlite` |
| Wrapped data keys | `%USERPROFILE%\.belay\keys\` |
| Verified Numbat copy | `%USERPROFILE%\.belay\bin\numbat-<sha16>.exe` |
| Installed runtime | `%LOCALAPPDATA%\Belay\runtime` |
| Command shim | `%LOCALAPPDATA%\Belay\bin\belay.cmd` |
| Claude Code history | `%USERPROFILE%\.claude\projects` unless `CLAUDE_CONFIG_DIR` is set |
| Codex history | `%USERPROFILE%\.codex\sessions` unless `CODEX_HOME` is set |
| Cursor history | `%USERPROFILE%\.cursor\projects\<hash>\agent-transcripts` (`BELAY_CURSOR_HOME` overrides the root for Belay's own reader only) |
| Cursor MCP registry | `%USERPROFILE%\.cursor\mcp.json`, written by Belay itself |
| Cursor hooks | `%USERPROFILE%\.cursor\hooks.json`, written by the pinned Numbat |
| Cursor skill | `%USERPROFILE%\.cursor\skills\belay\SKILL.md` |
| Antigravity app-data root | `%USERPROFILE%\.gemini\antigravity`, the detection directory; its `conversations\` are encrypted `.pb` files Belay never reads, and there is no historical scan |
| Antigravity MCP registry | `%USERPROFILE%\.gemini\config\mcp_config.json`, written by Belay itself; the legacy `%USERPROFILE%\.gemini\antigravity\mcp_config.json` is never touched |
| Antigravity hooks | `%USERPROFILE%\.gemini\config\hooks.json`, written by the pinned Numbat under the key `numbat` |
| Antigravity skill | `%USERPROFILE%\.gemini\config\skills\belay\SKILL.md` |
| Cursor CLI (semantic analysis) | `cursor-agent` on `PATH`, or `agent` on `PATH` with a real `%USERPROFILE%\.cursor` directory; Windows install location not recorded and unvalidated |
| Antigravity CLI (semantic analysis) | `agy` under the vendor-documented `%LOCALAPPDATA%\agy\bin`, with a real `%USERPROFILE%\.gemini\antigravity-cli` directory that Belay never reads; unvalidated on Windows |

Deleting the Belay home deletes the wrapped keys with the database. Moving the
database to another Windows account without its key file makes the encrypted
payloads unrecoverable by design.
