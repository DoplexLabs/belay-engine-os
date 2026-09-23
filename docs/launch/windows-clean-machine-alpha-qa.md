# Belay Local Developer Alpha Windows clean-machine QA

This checklist is the manual release gate for
`belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64`.

It is the Windows twin of
[`clean-machine-alpha-qa.md`](clean-machine-alpha-qa.md). Gate numbers match
that document where the behavior is the same, so a defect can be compared
across platforms.

Complete it on a fresh Windows 11 (or Windows 10 build 1809 or newer) local user
account on x64, with working Codex and Claude Code installations and
representative local history. Do not use a founder's existing Belay state. Do
not use real secrets for privacy testing.

**This checklist has not been executed. No gate below has a recorded result.**
Until it passes, Windows behavior beyond cross-compilation, the native CI job,
and the read-only packaged smoke test is unverified on a real machine. Do not
cite this document as evidence that a gate passed.

A distributable test artifact must come from a clean checkout and contain
`belay_dirty=false` in `BUILD-INFO.txt`. A dirty validation artifact is never
eligible for this checklist and must not be distributed, even if automated
checksum and smoke checks pass.

`scripts\smoke-windows-preview.ps1` does not satisfy this checklist because it
intentionally avoids the DPAPI key store, the database, the browser, MCP
clients, and harness hooks.

The data-heavy gates below require a sanitized QA fixture or naturally occurring
test data with the stated counts. Record the fixture source, manifest, and
SHA-256. If the required dataset is unavailable, mark the affected gate
`BLOCKED`; do not waive it or substitute a smaller dataset.

Run PowerShell as the ordinary signed-in user. Do not use an elevated shell; no
gate below requires administrator rights.

## Test record

| Field | Evidence |
|---|---|
| Tester | |
| Tester affiliation/non-founder confirmation | |
| Date/time and timezone | |
| Windows edition and version (`winver`) | |
| Windows build number | |
| Processor architecture (`$env:PROCESSOR_ARCHITECTURE`) | |
| Machine model | |
| PowerShell version (`$PSVersionTable.PSVersion`) | |
| Account type (local or Microsoft account) | |
| Codex version and install method | |
| Claude Code version and install method | |
| Cursor version and install method | |
| Antigravity version and install method | |
| Cursor CLI (`agent` or `cursor-agent`) version, install location, and sign-in method | |
| Antigravity CLI (`agy`) version and install location | |
| Archive filename | |
| Archive SHA-256 | |
| Belay commit from `BUILD-INFO.txt` | |
| Numbat commit from `BUILD-INFO.txt` | |
| Evidence folder or ticket | |
| Overall result: `PASS` or `FAIL` | |
| Blocking defect links | |

For every gate below, fill in:

- **Result:** `PASS`, `FAIL`, or `BLOCKED`
- **Started/finished:** timestamps
- **Evidence:** command output, screenshot, screen recording, or attached file
- **Notes/defect:** concise observation and issue link when applicable

Redact usernames and unrelated local paths before sharing evidence.

## W01 — Clean account and artifact provenance

Procedure:

1. Confirm the Windows account has no existing `%USERPROFILE%\.belay` and no
   `BELAY_HOME` set:

   ```powershell
   Test-Path "$env:USERPROFILE\.belay"
   $env:BELAY_HOME
   ```

2. Confirm Codex and Claude Code are installed and each has historical
   activity under `%USERPROFILE%\.codex\sessions` and
   `%USERPROFILE%\.claude\projects` (or the paths named by `CODEX_HOME` and
   `CLAUDE_CONFIG_DIR`).
3. Record the archive and checksum source. It must be an explicitly authorized
   Doplex channel.
4. Extract the zip and inspect `BUILD-INFO.txt`.

Expected:

- `target=windows/amd64` is reported.
- No prior Belay state exists.
- Both harnesses have history.
- `belay_dirty=false`.
- Numbat commit is
  `b5172bb8bb8f1d68edc4f3b9462de7e248dc5243`.
- `signed=false` and `notarized=false` are understood by the tester.

Evidence:

- Result:
- Started/finished:
- Artifact source:
- `BUILD-INFO.txt` attachment:
- Harness history paths:
- Notes/defect:

## W02 — External checksum verification

Procedure, from the download directory:

```powershell
Get-FileHash -Algorithm SHA256 .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip
Get-Content .\belay-local-developer-alpha-v0.0.1-alpha.11-windows-amd64.zip.sha256
```

Expected: the hash printed by `Get-FileHash` matches the hash recorded in the
`.sha256` file, compared character by character and recorded in the evidence.

Evidence:

- Result:
- Started/finished:
- Terminal output:
- Notes/defect:

## W03 — SmartScreen-specific approval

Procedure:

1. Attempt `.\bin\belay.exe help`.
2. Attempt `.\bin\numbat.exe version`.
3. For each binary Windows blocks, choose **More info → Run anyway** in the
   SmartScreen dialog for that binary only, after the W02 checksum comparison.
4. Re-run both commands.
5. Do not disable SmartScreen, Microsoft Defender, or any system-wide
   protection, and do not run `Unblock-File` recursively over unrelated files.

Expected:

- Both `bin\belay.exe` and `bin\numbat.exe` run after any required per-binary
  approval.
- Numbat reports version marker `b5172bb8bb8f`.
- No system-wide security setting is disabled.
- The exact SmartScreen wording and whether the mark of the web was present are
  recorded, so the README and developer-alpha instructions can be corrected if
  they differ.

Evidence:

- Result:
- Started/finished:
- Initial `belay.exe` SmartScreen behavior:
- Initial `numbat.exe` SmartScreen behavior:
- Per-binary approval screenshots:
- Final command output:
- Notes/defect:

## W04 — DPAPI key creation and encrypted first run

From the extracted archive, start Belay with the packaged one-command path:

```powershell
.\bin\belay.exe quickstart --allow-codex-mcp-add
```

This invocation is explicit consent to install reversible monitor-only hooks,
the managed `/belay` skill, and the governed Belay MCP server in detected
Codex, Claude Code, Cursor, and Antigravity user configuration, scan supported
history, start Local, print its URL, and attempt to open the dashboard. The
flag permits Codex `mcp add` only after Belay strictly verifies that the `belay` entry is absent and records explicit
acceptance of the Codex CLI's non-atomic duplicate-name behavior. Run from an
extracted path containing a space so the executable remains one command
argument.

Expected:

- No manual Numbat path, SHA-256, or version-marker argument is required.
- The packaged sibling `bin\numbat.exe` is verified and a checksum-addressed
  copy is recorded under `%USERPROFILE%\.belay\bin\` as
  `numbat-<sha16>.exe`.
- Belay never prompts for a password, a credential dialog, or a DPAPI UI
  prompt.
- `%USERPROFILE%\.belay\belay.sqlite` is created.
- Exactly one wrapped key file exists and its name matches the store ID:

  ```powershell
  Get-ChildItem "$env:USERPROFILE\.belay\keys"
  ```

  The listing shows a single `store_*.key` file.
- `.\bin\belay.exe hooks status` reports the detected Codex, Claude Code,
  Cursor, and Antigravity monitor-only hooks installed or reports an objective
  per-harness reason that a hook was not applicable. The Cursor entries are in
  `%USERPROFILE%\.cursor\hooks.json`; the Antigravity entries are under the
  key `numbat` in `%USERPROFILE%\.gemini\config\hooks.json`.
- `.\bin\belay.exe mcp-config status` returns schema `belay.mcp-config.v1`,
  target order Codex, Claude, Cursor, then Antigravity, and reports each
  available detected target as `owned_current`.
- Codex add is never invoked while an entry named `belay` is present. Foreign
  or unverifiable Codex entries are preserved and never overwritten or removed.
- The Cursor registration is a direct write of `%USERPROFILE%\.cursor\mcp.json`
  with no opt-in flag: `mcpServers.belay` names the packaged `bin\belay.exe`
  with `["mcp"]`, the backslash path survives JSON encoding, and every other
  server and top-level key in the file is unchanged. Seed the file beforehand
  with an unrelated server and an unknown top-level key and confirm both
  survive.
- With a foreign entry named `belay` seeded in
  `%USERPROFILE%\.cursor\mcp.json`, install preserves it untouched and reports
  it rather than overwriting it.
- The Antigravity registration is a direct write of
  `%USERPROFILE%\.gemini\config\mcp_config.json` with no opt-in flag:
  `mcpServers.belay` names the packaged `bin\belay.exe` with `["mcp"]`, the
  backslash path survives JSON encoding, and every other server and top-level
  key in the file is unchanged. Seed the file beforehand with an unrelated
  server, a `serverUrl` remote entry, and an unknown top-level key and confirm
  all three survive. When `%USERPROFILE%\.gemini\config` did not exist, Belay
  created it. The legacy `%USERPROFILE%\.gemini\antigravity\mcp_config.json`
  is untouched.
- With a foreign entry named `belay` seeded in
  `%USERPROFILE%\.gemini\config\mcp_config.json`, install preserves it
  untouched and reports it rather than overwriting it.
- The managed `/belay` skill is installed for each detected harness, including
  `%USERPROFILE%\.cursor\skills\belay\SKILL.md` and
  `%USERPROFILE%\.gemini\config\skills\belay\SKILL.md`, and the quickstart
  stderr skill line reports one `agent=status` pair per supported harness
  (`skill codex=... claude=... cursor=... antigravity=...`).
- Quickstart stdout contains only the one-line tokenized Local URL. Fixed hook
  and MCP summaries appear on stderr without executable paths or raw agent-CLI
  output.
- `.\bin\belay.exe doctor` reports configuration, encrypted storage, Numbat
  pin, and discovery as healthy.

Evidence:

- Result:
- Started/finished:
- Exact command:
- `keys` directory listing:
- Redacted `%USERPROFILE%\.belay\config.json`:
- Redacted `doctor` output:
- Hook status:
- Cursor `%USERPROFILE%\.cursor\hooks.json` before/after:
- Antigravity `%USERPROFILE%\.gemini\config\hooks.json` before/after:
- MCP configuration status:
- Cursor `%USERPROFILE%\.cursor\mcp.json` before/after, including the seeded
  foreign entry:
- Antigravity `%USERPROFILE%\.gemini\config\mcp_config.json` before/after,
  including the seeded foreign entry, and the untouched legacy
  `%USERPROFILE%\.gemini\antigravity\mcp_config.json` if present:
- Skill install status, including
  `%USERPROFILE%\.cursor\skills\belay\SKILL.md` and
  `%USERPROFILE%\.gemini\config\skills\belay\SKILL.md`:
- Redacted quickstart stdout/stderr:
- Notes/defect:

## W04a — DPAPI key reuse, session change, and password change

Procedure:

1. With Belay stopped, reopen the store read-only and confirm it opens without
   any prompt:

   ```powershell
   .\bin\belay.exe sessions --db "$env:USERPROFILE\.belay\belay.sqlite"
   ```

2. Sign out of Windows, sign back in as the same account, and repeat step 1.
3. Restart the machine and repeat step 1.
4. Change the account password through the normal Windows change-password flow
   (not an administrator reset), sign in with the new password, and repeat
   step 1.
5. Copy `keys\<store-id>.key` and `belay.sqlite` to a second Windows local
   account on the same machine and attempt to open the copied database from
   that account.
6. Copy the key file over a second store's key file name and attempt to open
   that second store.

Expected:

- Steps 1–4 reopen the existing store and read sessions with no password
  prompt, no DPAPI UI, and no new key file. The `keys` directory still holds
  exactly one `store_*.key`.
- Belay never creates a replacement key for an existing store, and never opens
  encrypted rows as plaintext.
- Step 5 fails closed with a payload-free error: the second account cannot
  unwrap the key.
- Step 6 fails closed: the wrapping entropy is bound to the store ID, so a
  key file moved between stores does not unwrap.
- A forced administrator password reset is known to destroy DPAPI-protected
  secrets. Record whether it was tested; if it was, the expected result is the
  same fail-closed error, not a silent new key.

Evidence:

- Result:
- Started/finished:
- Sign-out/sign-in output:
- Restart output:
- Password-change output:
- Cross-account copy output:
- Cross-store copy output:
- `keys` directory listing before and after:
- Notes/defect:

## W05 — Install-to-first-insight and historical reconstruction

Measure from the first `belay quickstart` invocation until the browser shows at
least one real historical session from each harness.

Expected:

- The printed URL is loopback-only on `127.0.0.1` and contains a launch token in
  the fragment.
- The default browser is opened to the dashboard, or browser-open failure is
  reported without stopping Local and the printed URL works when pasted
  manually. Windows opens the browser through
  `rundll32 url.dll,FileProtocolHandler`; record which of the two paths
  occurred.
- First useful history appears within 15 minutes.
- At least one Codex and one Claude Code session appears.
- When Cursor is installed, at least one Cursor session appears from the
  historical scan of
  `%USERPROFILE%\.cursor\projects\<hash>\agent-transcripts\**\*.jsonl`.
  Belay's Cursor transcript reader has only synthetic fixtures behind it, so
  record whether real Cursor history parsed and attach a redacted count.
- When Antigravity is installed, no Antigravity session appears from the
  historical scan, because Antigravity is hook-only and `belay scan` performs
  no Antigravity scan. Confirm the scan reports no Antigravity history rather
  than an error; the first Antigravity session is expected only in W09b.
- Historical sessions are visibly marked.
- Timeline rows show immutable event IDs and source/coverage metadata.
- Backslash project paths render correctly and are not mistaken for escapes.

Evidence:

- Result:
- Started:
- First useful timeline:
- Elapsed time:
- Browser-open result and printed-URL fallback:
- Codex session screenshot:
- Claude Code session screenshot:
- Cursor session screenshot and parsed-history observation:
- Antigravity scan observation (no historical session, no error):
- Notes/defect:

## W06 — Duplicate replay

Stop `belay quickstart`, then run the scan twice using the saved configuration:

```powershell
.\bin\belay.exe scan > scan-first.json
.\bin\belay.exe scan > scan-second.json
```

Expected:

- Both scans complete without corrupting prior data.
- The second scan accepts zero duplicate canonical events.
- Session/event counts do not inflate after replay.
- The JSON files are valid UTF-8 without a byte-order mark and without CRLF
  corruption of the payload.

Evidence:

- Result:
- Started/finished:
- `scan-first.json`:
- `scan-second.json`:
- Before/after counts:
- Notes/defect:

## W07 — Local browser, core routes, findings, and statistics

Procedure: open the printed loopback URL in Edge and in one other installed
browser. Walk the dashboard, a session detail view, the timeline, findings, and
statistics. Then confirm the bind address:

```powershell
Get-NetTCPConnection -State Listen |
  Where-Object { $_.OwningProcess -eq (Get-Process belay).Id }
```

Expected:

- The listener is bound to `127.0.0.1` only. No `0.0.0.0`, no `::`, and no LAN
  address is listening.
- A request without the per-launch token is rejected.
- The dashboard, session detail, timeline, findings, and statistics views load
  and match the gate expectations in the macOS A07 series.
- No Windows Defender Firewall prompt to allow inbound network access appears.
  If one appears, record it and decline; a loopback-only listener must not need
  one.
- Run the macOS A07a–A07i equivalents (cursor chaining, session filters,
  overview truthfulness, session-scoped findings beyond the global 500-row
  boundary, injection-like rendering, fix declaration/retraction, restart
  durability, exact recurrence monitoring, and catch-up/retention) against this
  build and record each sub-result in the final table.

Evidence:

- Result:
- Started/finished:
- Listener output:
- Untokenized-request output:
- Browser screenshots:
- Firewall prompt observed (yes/no):
- A07a–A07i sub-results:
- Notes/defect:

## W08 — Explicit live hooks: Codex

The W04 `quickstart` command was the explicit hook-install consent. Confirm its
result without reinstalling:

```powershell
.\bin\belay.exe hooks status
```

Record how `codex` resolves on this machine, since an npm install provides a
`codex.cmd` shim rather than a native executable:

```powershell
Get-Command codex | Format-List Name, CommandType, Source
```

Perform one harmless, uniquely identifiable Codex action, such as listing files
in a disposable test directory whose path contains a space.

Expected:

- Only explicit install changes harness configuration.
- The installed state came from the explicit `quickstart` invocation.
- Codex hook status reports installed and configured.
- The hook command recorded in Codex configuration points at the installed
  `belay.exe` and survives quoting, including a path with a space.
- The Codex action completes normally.
- A corresponding new minimized event appears within 10 seconds while Belay is
  running.
- If the hook is executed through a `.cmd` shim, `cmd.exe`, or Git Bash, record
  which, and record whether a console window flashes.

Evidence:

- Result:
- Started/finished:
- `Get-Command codex` output:
- Hook status:
- Recorded hook command line:
- Safe action description:
- Timeline event ID/screenshot:
- Notes/defect:

## W09 — Explicit live hooks: Claude Code

Record how `claude` resolves, for the same `.cmd` shim reason:

```powershell
Get-Command claude | Format-List Name, CommandType, Source
```

Perform one harmless, uniquely identifiable Claude Code action in a disposable
test directory whose path contains a space.

Expected:

- Claude Code hook status reports installed and configured.
- The hook command points at the installed `belay.exe` and survives quoting.
- The Claude Code action completes normally.
- A corresponding new minimized event appears within 10 seconds while Belay is
  running.
- If the hook is executed through a `.cmd` shim, `cmd.exe`, or Git Bash, record
  which.

Evidence:

- Result:
- Started/finished:
- `Get-Command claude` output:
- Hook status:
- Recorded hook command line:
- Safe action description:
- Timeline event ID/screenshot:
- Notes/defect:

## W09a — Explicit live hooks and skill: Cursor

The W04 `quickstart` command was the explicit hook-install consent for Cursor
too. Confirm its result without reinstalling, then perform one harmless,
uniquely identifiable action in Cursor Agent chat in a disposable test
directory whose path contains a space.

Expected:

- Cursor hook status reports installed and configured, and
  `%USERPROFILE%\.cursor\hooks.json` contains only Belay's monitor-only
  entries beside whatever was already there.
- The recorded hook command points at the installed `belay.exe` and survives
  quoting, including a path with a space.
- The Cursor action completes normally.
- A corresponding new minimized event appears within 10 seconds while Belay is
  running, from the `live\cursor.ndjson` spool.
- `/belay start` in Cursor Agent chat returns a Mission Pack whose targets are
  Cursor-compatible (`AGENTS.md`, never `.claude/settings.json`).
- `.\bin\belay.exe hooks uninstall` followed by `.\bin\belay.exe hooks status`
  removes and then reports absent the Cursor hooks, leaving unrelated entries
  in `%USERPROFILE%\.cursor\hooks.json` in place. Reinstall with
  `.\bin\belay.exe hooks install` before continuing.
- If a hook is executed through `cmd.exe` or a shim, record which, and record
  whether a console window flashes.

Evidence:

- Result:
- Started/finished:
- Hook status:
- `%USERPROFILE%\.cursor\hooks.json` contents before/after:
- Recorded hook command line:
- Safe action description:
- Timeline event ID/screenshot:
- `/belay start` output in Cursor:
- Uninstall/reinstall output:
- Notes/defect:

## W09b — Explicit live hooks and skill: Antigravity

The W04 `quickstart` command was the explicit hook-install consent for
Antigravity too. Confirm its result without reinstalling, then perform one
harmless, uniquely identifiable action in Antigravity's agent chat in a
disposable test directory whose path contains a space. Record the Antigravity
version; Belay's Antigravity support was developed on macOS against
Antigravity 2.0.1 without a captured live session, and no clean-machine run
has covered it on Windows.

Expected:

- Antigravity hook status reports installed and configured, and
  `%USERPROFILE%\.gemini\config\hooks.json` contains only Belay's monitor-only
  entries (`PreToolUse`, `PostToolUse`, and `Stop`) under the key `numbat`
  beside whatever was already there.
- The recorded hook command points at the installed `belay.exe` and survives
  quoting, including a path with a space.
- The Antigravity action completes normally.
- A corresponding new minimized event appears within 10 seconds while Belay is
  running, from the `live\antigravity.ndjson` spool.
- The resulting Antigravity session shows timeline evidence and any
  deterministic hook-based issue, and shows no transcript excerpt, Habits
  debrief, or transcript-derived lesson; Belay has no Antigravity transcript
  reader. Record that the encrypted `.pb` files under
  `%USERPROFILE%\.gemini\antigravity\conversations\` were not read.
- `/belay start` in Antigravity's agent chat returns a Mission Pack whose
  targets are Antigravity-compatible (`.agents/rules/belay.md`, never
  `AGENTS.md`, `CLAUDE.md`, or `.claude/settings.json`).
- `.\bin\belay.exe hooks uninstall` followed by `.\bin\belay.exe hooks status`
  removes and then reports absent the Antigravity hooks, leaving unrelated
  entries in `%USERPROFILE%\.gemini\config\hooks.json` in place. Reinstall
  with `.\bin\belay.exe hooks install` before continuing.
- If a hook is executed through `cmd.exe` or a shim, record which, and record
  whether a console window flashes.

Evidence:

- Result:
- Started/finished:
- Antigravity version:
- Hook status:
- `%USERPROFILE%\.gemini\config\hooks.json` contents before/after:
- Recorded hook command line:
- Safe action description:
- Timeline event ID/screenshot:
- Session view showing no transcript excerpt or debrief:
- `/belay start` output in Antigravity:
- Uninstall/reinstall output:
- Notes/defect:

## W09c — Semantic analysis via the Cursor CLI and the Antigravity CLI

Belay's semantic analysis (deterministic-issue refinement, lessons, and Habits
debriefs) can run through the Cursor CLI and the Antigravity CLI as well as
Claude Code and Codex. Both paths were written from the vendors' published
documentation on macOS: neither CLI was installed on the development machine,
nothing about either has been run on Windows, and the Windows install
locations below are the vendors' documented ones, not observations. If a CLI
cannot be installed or signed in on the QA account, mark the affected half
`BLOCKED`; do not substitute the IDE for the CLI.

Precondition: W05 has already produced Claude Code, Codex, and Cursor sessions,
so there is something to analyze. Record every CLI version and resolved path in
the test record.

Procedure, Cursor CLI:

1. Install the Cursor CLI by Cursor's documented Windows method, sign in with
   `agent login` (or set `CURSOR_API_KEY` in the test shell), and record how
   it resolves:

   ```powershell
   Get-Command agent, cursor-agent -ErrorAction SilentlyContinue | Format-List Name, CommandType, Source
   Test-Path "$env:USERPROFILE\.cursor"
   ```

2. Run `.\bin\belay.exe analyze --agent cursor` and preserve stdout/stderr.
3. Open a Claude Code, Codex, or Cursor session in Local and look for a new
   semantic lesson or Habits debrief.
4. Rename `%USERPROFILE%\.cursor` temporarily, run
   `.\bin\belay.exe analyze --agent cursor` again, then restore it.

Procedure, Antigravity CLI:

5. Install the Antigravity CLI by Google's documented method; its documented
   Windows location is `%LOCALAPPDATA%\agy\bin`. Complete its own sign-in and
   record how `agy` resolves, so the CLI and the IDE's launcher can be told
   apart:

   ```powershell
   Get-Command agy -All | Format-List Name, CommandType, Source
   Test-Path "$env:USERPROFILE\.gemini\antigravity-cli"
   ```

6. Run `.\bin\belay.exe analyze --agent antigravity` and preserve
   stdout/stderr. While it runs, capture the `agy` command line (for example
   with `Get-CimInstance Win32_Process -Filter "Name like 'agy%'" | Select-Object CommandLine`).
7. Open a Claude Code, Codex, or Cursor session in Local and look for a new
   semantic lesson or Habits debrief; then open the W09b Antigravity session.
8. Run `/belay start` in Antigravity's agent chat, in Claude Code, in Codex,
   and in Cursor after the Antigravity-run analysis.
9. Run `.\bin\belay.exe analyze --agent auto` and record which CLI it
   selected.

Expected:

- Step 2 completes without error using only the tester's own Cursor sign-in.
  Belay passes no API key and makes no model call of its own. Record whether
  the CLI resolved as a native executable or a `.cmd` shim, and whether a
  console window flashes.
- After step 2, at least one Claude Code, Codex, or Cursor session shows a
  semantic lesson or Habits debrief, and the recorded model for that analysis
  is unknown (the Cursor JSON result carries none).
- In step 4, with `%USERPROFILE%\.cursor` absent, a bare `agent` on `PATH` is
  not detected and Belay reports the Cursor CLI unavailable rather than
  running a generic `agent` binary; with `cursor-agent` on `PATH` it is still
  detected.
- Step 6 completes without error. The captured `agy` command line contains
  `--sandbox` and `--json-schema` and does not contain
  `--dangerously-skip-permissions`; it is the CLI under
  `%LOCALAPPDATA%\agy\bin`, not the IDE launcher, and Belay never reads
  `%USERPROFILE%\.gemini\antigravity-cli`. The schema file path survives
  backslash quoting.
- After step 6, at least one Claude Code, Codex, or Cursor session shows a
  semantic lesson or Habits debrief written by the Antigravity CLI, and the
  W09b Antigravity session still shows no transcript excerpt or Habits
  debrief; the Antigravity CLI changes which CLI performs the analysis, not
  which sessions have transcripts.
- In step 8, guidance from the Antigravity-run analysis targets
  `.agents/rules/belay.md` in Antigravity, `CLAUDE.md` in Claude Code, and
  `AGENTS.md` in Codex and Cursor. Guidance from the Cursor-run analysis
  targets `AGENTS.md` in Cursor.
- Step 9 selects Claude Code while it is installed; the documented order is
  Claude Code, then Codex, then the Cursor CLI, then the Antigravity CLI.
  Record the actual selection.
- A result that fails Belay's output-schema validation, if one occurs, is
  reported as rejected and produces no lesson or debrief. Record any such
  rejection verbatim (redacted); do not treat it as a pass or a fail by
  itself.
- The Cursor and Antigravity review queues (`list_experience_proposals` with
  `harness: cursor` and `harness: antigravity`) still show proposals from
  every engine, one per candidate.

Evidence:

- Result:
- Started/finished:
- `Get-Command agent, cursor-agent` output, Cursor CLI version, sign-in
  method, and `%USERPROFILE%\.cursor` state:
- `analyze --agent cursor` output (redacted):
- Lesson or debrief screenshot after the Cursor-run analysis, with model:
- `%USERPROFILE%\.cursor`-absent detection output:
- `Get-Command agy -All` output, Antigravity CLI version, and
  `%USERPROFILE%\.gemini\antigravity-cli` state:
- Captured `agy` command line:
- `analyze --agent antigravity` output (redacted):
- Lesson or debrief screenshot after the Antigravity-run analysis:
- W09b Antigravity session view showing no transcript excerpt or debrief:
- `/belay start` target evidence in each client:
- `analyze --agent auto` selection:
- Any schema-validation rejection (redacted):
- Shim/console-window observations:
- Notes/defect:

## W10 — MCP registration and issue-intelligence contract

Use the registrations created by W04. Do not manually edit Codex or Claude
configuration. Run:

```powershell
.\bin\belay.exe mcp-config status
```

If a target was not registered in W04, register it explicitly:

```powershell
.\bin\belay.exe mcp-config install
.\bin\belay.exe mcp-config install --allow-codex-mcp-add
```

Then, from each client, exercise the governed catalog and the issue-evidence
loop: `list_issues` → preserve `view_cursor` → `get_issue` →
`lookup_session_events` for the cited IDs only. Prepare and approve a Mission
Pack with `/belay start` in each client for a concrete task, then again with no
concrete task.

Expected:

- `mcp-config status` returns schema `belay.mcp-config.v1` with the recorded
  Windows path of the installed `belay.exe`, correctly quoted.
- Registration adds no tool beyond the governed catalog, no HTTP transport, no
  bearer token, no working-directory override, and no environment secret.
- Each client lists the Belay MCP server after restart and can call
  `list_sessions`, `get_session`, `get_session_timeline`, `query_activity`,
  `list_findings`, `get_stats`, `list_issues`, `get_issue`,
  `lookup_session_events`, `get_top_issues`, `get_issue_excerpts`,
  `get_fix_status`, `get_mission_pack`, `record_mission_pack_accepted`,
  `get_mission_pack_status`, `list_experience_proposals`,
  `list_active_experiences`, `approve_experience`,
  `resolve_experience_proposal`, `prepare_experience_lifecycle`,
  `apply_experience_lifecycle`, `propose_fix`, and `record_fix_applied`.
- Codex add is never invoked against an existing entry. An existing foreign or
  unverifiable entry named `belay` is preserved, and explicit uninstall returns
  nonzero for it.
- `allow-codex-mcp-add` is required before Belay may invoke Codex add, and the
  tester records explicit acceptance of the Codex CLI's non-atomic
  duplicate-name behavior.
- Claude receives only Claude-compatible Mission Pack targets and Codex only
  Codex-compatible targets. With no concrete task, no unanchored semantic rule
  is invented, and an empty pack is reported as unavailable rather than offered
  for activation.
- No tool writes a project file or executes a command.

Evidence:

- Result:
- Started/finished:
- `mcp-config status` JSON:
- Client tool listings:
- Issue-loop transcript (redacted):
- Mission Pack transcripts (redacted):
- Notes/defect:

## W11 — Offline behavior

Disable Wi-Fi and any other active network adapter through Windows Settings or
`Disable-NetAdapter`, leaving the loopback interface alone. Do not rely on a
firewall rule alone.

Expected:

- Existing and historical sessions remain readable in the browser.
- Browser refresh and timeline reads succeed.
- Existing fix-attempt history remains readable; an eligible declaration and
  retraction can be recorded through loopback without hosted access.
- All tools in the governed MCP contract remain discoverable. Representative
  session/timeline reads, the issue list → detail → cited-event lookup loop,
  Mission Pack preparation/status, and experience review/lifecycle previews
  succeed.
- `.\bin\belay.exe mcp-config status` completes without a Belay
  product-network request. If a host CLI wrapper performs its own credential or
  network check, record that separately; quickstart must remain fail-open.
- No hosted login or Belay service is requested.
- A new supported live-hook event can be imported while offline.
- The update check and telemetry ping fail open and change no product behavior.
- If the configured client uses a remotely hosted model, distinguish that
  client's network behavior from Belay Local; it is not evidence of a Belay
  product-network request.

Evidence:

- Result:
- Started/finished:
- Adapter-disabled screenshot:
- Browser evidence:
- MCP evidence:
- Live-event evidence:
- Notes/defect:

## W12 — Privacy canary

Use this synthetic, non-secret marker in one harmless Codex or Claude Code test
prompt:

```text
BELAY_ALPHA_PRIVACY_CANARY_0d5f4f06
```

Do not place a real credential in the test. Export the relevant API and MCP
responses, then search only the canonical database files and payload-free logs.
The raw live spool is an upstream acquisition seam and is not part of this
canonical-persistence assertion.

Example byte checks:

```powershell
$canary = 'BELAY_ALPHA_PRIVACY_CANARY_0d5f4f06'
$belayHome = "$env:USERPROFILE\.belay"
Get-ChildItem -File -Recurse "$belayHome\logs" -ErrorAction SilentlyContinue |
    ForEach-Object { findstr /M /C:$canary $_.FullName }
Get-ChildItem -File "$belayHome\belay.sqlite*", "$belayHome\keys\*" |
    ForEach-Object { findstr /M /C:$canary $_.FullName }
```

`findstr /M` scans binary files and prints only the name of a file that
contains the literal. Every command above must print nothing.

Expected:

- The canary is absent from browser/API responses.
- The canary is absent from MCP structured and narrative output.
- The canary is absent from Belay logs.
- The canary is absent from SQLite database, WAL, and SHM bytes.
- The canary is absent from the wrapped key file bytes.
- Minimized event metadata still appears without the prompt body.
- Allowed MCP evidence is limited to documented minimized fields such as
  executable/tool names, bounded option names, project-relative paths or
  basenames, model/provider labels, and network scheme/host values.
- Windows user names and absolute `C:\Users\...` prefixes do not appear in
  minimized evidence.
- Record separately whether the configured MCP client/model transmitted tool
  results under its own policy. Do not attribute client/model processing to
  Belay Local.

Evidence:

- Result:
- Started/finished:
- API response attachments:
- MCP response attachments:
- Log/database search output:
- Key-file search output:
- Minimized event ID:
- Notes/defect:

## W13 — Fail-open process interruption

Procedure:

1. Keep monitor-only hooks installed.
2. Stop the running Belay Local process with `Ctrl-C`.
3. Run one harmless Codex action and one harmless Claude Code action.
4. Separately start a historical scan, interrupt only that test scan with
   `Ctrl-C`, and confirm an agent action still completes.
5. Restart Belay and inspect diagnostics/import continuity.

Expected:

- Neither harness action is blocked, delayed materially, or changed because
  Belay is stopped.
- Interrupting the test scan does not block either harness.
- Belay restarts and remains usable; no stale lock file prevents reopening the
  store after an abrupt stop.
- Partial acquisition is reported honestly; no success is fabricated.

Evidence:

- Result:
- Started/finished:
- Codex completion evidence:
- Claude Code completion evidence:
- Interrupted scan evidence:
- Restart/diagnostic evidence:
- Notes/defect:

## W14 — Installer, uninstall, and retained-data boundary

Procedure. First remove the two independently managed integrations:

```powershell
.\bin\belay.exe mcp-config uninstall
.\bin\belay.exe mcp-config status
.\bin\belay.exe hooks uninstall
.\bin\belay.exe hooks status
```

Do not manually remove or replace a foreign/unverifiable entry. Stop Belay,
then run the lower-side-effect path and stop it after the URL is printed:

```powershell
.\bin\belay.exe local --no-scan
```

Then exercise the published installer and its uninstall path:

```powershell
irm https://getbelay.vercel.app/install.ps1 | iex
belay doctor
irm https://getbelay.vercel.app/install.ps1 -OutFile install.ps1
.\install.ps1 -Uninstall
```

Expected:

- Every installed monitor hook, including Cursor's and Antigravity's, is
  removed or reported absent.
- Exact current or previously verified Belay MCP entries are removed and status
  reports them absent, including `mcpServers.belay` in
  `%USERPROFILE%\.cursor\mcp.json` and in
  `%USERPROFILE%\.gemini\config\mcp_config.json`; the rest of each file,
  including the seeded unrelated server, remote entry, and unknown key, is
  unchanged and `mcpServers` remains present. Foreign or unverifiable entries
  are preserved and make explicit uninstall nonzero.
- MCP uninstall does not remove hooks, Local history, the wrapped data key, or
  the extracted package; hook uninstall does not remove MCP configuration.
- Running `local --no-scan` does not reinstall either hook and does not open a
  browser.
- Both clients no longer advertise Belay MCP after restart.
- The installer places the runtime under `%LOCALAPPDATA%\Belay\runtime`, writes
  the `belay.cmd` shim to `%LOCALAPPDATA%\Belay\bin`, and adds that directory
  to the user `PATH` only. It does not modify the machine `PATH` and does not
  require elevation.
- `belay doctor` runs from a newly opened terminal through the shim.
- Re-running the installer upgrades in place without duplicating the `PATH`
  entry.
- `install.ps1 -Uninstall` removes the runtime, the shim, and the `PATH` entry,
  and leaves no other user `PATH` entry damaged. Record the `PATH` value before
  and after.
- `%USERPROFILE%\.belay` still exists after uninstall, including
  `keys\<store-id>.key` and `belay.sqlite`:

  ```powershell
  Get-ChildItem "$env:USERPROFILE\.belay"
  Get-ChildItem "$env:USERPROFILE\.belay\keys"
  ```

- Normal agent actions still work.
- The extracted package can be deleted without touching unrelated files.
- Deleting the Belay home is a separate destructive choice; no script performs
  it.

Evidence:

- Result:
- Started/finished:
- Hook uninstall/status:
- MCP uninstall/status JSON:
- Cursor `%USERPROFILE%\.cursor\mcp.json` and
  `%USERPROFILE%\.cursor\hooks.json` after uninstall:
- Antigravity `%USERPROFILE%\.gemini\config\mcp_config.json` and
  `%USERPROFILE%\.gemini\config\hooks.json` after uninstall:
- Post-uninstall `local` hook status/browser observation:
- Installer output and install locations:
- User `PATH` before/after:
- Uninstall output:
- Retained `.belay` and `keys` listing:
- Post-uninstall harness actions:
- Notes/defect:

## Final release-gate summary

| Gate | Required result | Actual result | Evidence/defect |
|---|---|---|---|
| W01 Provenance | PASS | | |
| W02 Checksum | PASS | | |
| W03 SmartScreen | PASS | | |
| W04 DPAPI key/storage | PASS | | |
| W04a DPAPI reuse/password change | PASS | | |
| W05 First insight | PASS | | |
| W06 Deduplication | PASS | | |
| W07 Browser/core routes | PASS | | |
| W07a Cursor snapshot | PASS | | |
| W07b Filters/activity completeness | PASS | | |
| W07c Overview truthfulness | PASS | | |
| W07d Session-scoped findings >500 | PASS | | |
| W07e Injection rendering | PASS | | |
| W07f Fix declaration/retraction | PASS | | |
| W07g Fix restart/MCP isolation | PASS | | |
| W07h Exact recurrence monitoring | PASS | | |
| W07i Monitoring catch-up/restart/retention | PASS | | |
| W08 Codex hooks | PASS | | |
| W09 Claude hooks | PASS | | |
| W09a Cursor hooks/skill | PASS | | |
| W09b Antigravity hooks/skill | PASS | | |
| W09c Cursor CLI / Antigravity CLI semantic analysis | PASS | | |
| W10 MCP | PASS | | |
| W11 Offline | PASS | | |
| W12 Privacy | PASS | | |
| W13 Fail open | PASS | | |
| W14 Installer/uninstall | PASS | | |

The artifact is not alpha-ready on Windows if any row is `FAIL` or `BLOCKED`.
Completing this checklist does not authorize publication, change repository
visibility, resolve name clearance, or substitute for explicit release
approval. It also does not substitute for
[`clean-machine-alpha-qa.md`](clean-machine-alpha-qa.md), which remains the
macOS gate.
