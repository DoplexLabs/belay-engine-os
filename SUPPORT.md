# Support and feedback

Belay Local is a Developer Alpha, not a production support offering.

## Where to report

When the repository is public and issue tracking is enabled:

- use an issue for reproducible bugs, packaging failures, documentation errors,
  and compatibility reports;
- use a discussion for product feedback, questions, and use cases;
- use the private process in `SECURITY.md` for suspected vulnerabilities.

If the repository is not publicly accessible, use the private tester channel
provided with the artifact. Repository access and artifact publication require
separate owner authorization.

## Useful bug evidence

Include:

- Belay version and commit from `BUILD-INFO.txt`;
- Numbat commit and binary SHA-256;
- on macOS, the macOS version and Apple Silicon model;
- on Windows, the Windows edition, version and build number
  (`winver`, or `[System.Environment]::OSVersion.Version`) and the processor
  architecture (`$env:PROCESSOR_ARCHITECTURE`);
- Codex, Claude Code, Cursor, or Antigravity version, and for a semantic
  analysis report which CLI ran it (`claude`, `codex`, `agent`/`cursor-agent`,
  or `agy`) and that CLI's version;
- exact command and sanitized output;
- whether the network was disabled;
- whether hooks were installed;
- expected and observed behavior;
- a minimal synthetic reproduction.

Never attach raw agent history, prompts, transcripts, real secrets, Keychain
material, wrapped Windows data-key files, or an unredacted Local database.

## Alpha scope

The supported alpha surface is:

- Apple Silicon macOS;
- Windows 10 (build 1809 or newer) and Windows 11 on x64 (`amd64`);
- Codex, Claude Code, Cursor, and Google Antigravity 2.0;
- one-command packaged onboarding with `./bin/belay quickstart`;
- a no-hook `./bin/belay local` path;
- historical scan;
- explicit monitor-only live hooks;
- loopback Local browser;
- governed local MCP tools for evidence, issues, fixes, Mission Packs, and
  experience learning;
- offline Local operation.

Windows on arm64 is an engineering build target: it is built in CI but is not
published and is not part of the supported surface.

Intel macOS, Linux, Teams, arbitrary MCP writes, MCP command execution,
enforcement, automatic updates, signed installation, and production support
guarantees are not part of the alpha.

## Known limitations

- The macOS binaries are unsigned and unnotarized, so Gatekeeper warns on first
  launch.
- The Windows binaries are not Authenticode-signed, so SmartScreen warns on
  first launch. Approve the specific binary after verifying its checksum; do
  not disable SmartScreen.
- The Windows clean-machine QA pass in
  `docs/launch/windows-clean-machine-alpha-qa.md` has not been executed yet. It
  is the gate for every Windows behavior claim that a cross-compile check
  cannot prove.
- On Windows, live hook behavior for Codex, Claude Code, Cursor, and
  Antigravity depends on the pinned Numbat build and on how each agent runs
  hooks. Belay does not patch Numbat; gaps are reported upstream.
- No clean-machine QA run has covered Cursor or Antigravity yet, on either
  platform.
- Belay's semantic analysis (deterministic-issue refinement, lessons, and
  Habits debriefs) can run through the Cursor CLI (`agent`, or `cursor-agent`
  on older installs) as well as Claude Code or Codex:
  `belay analyze --agent cursor` selects it, and `--agent auto` prefers Claude
  Code, then Codex, then the Cursor CLI, then the Antigravity CLI. That path
  was written from Cursor's published documentation; the Cursor CLI was not
  installed on the development machine, so it was not validated against a real
  run. It uses your own `agent login` or `CURSOR_API_KEY` sign-in, runs in
  Cursor's read-only `--mode ask`, and Belay passes no key of its own. Without
  any of the four CLIs, a machine still gets sessions, evidence, and
  deterministic issues only.
- Belay's Cursor transcript reader is built against synthetic fixtures and has
  not been validated against a real Cursor transcript.
- The Antigravity CLI (`agy`, a separate product from the Antigravity IDE,
  whose own `agy` launcher only opens the IDE) can also run that analysis:
  `belay analyze --agent antigravity` selects it. Belay runs it with
  `--sandbox`, never passes `--dangerously-skip-permissions`, validates the
  structured output against its own schema, and never reads the CLI's own
  conversation store under `~/.gemini/antigravity-cli`. That path was likewise
  written from Google's published documentation; the Antigravity CLI was not
  installed on the development machine, so that path too was
  not validated against a real run. It changes which CLI performs the
  analysis, not which sessions have transcripts: an Antigravity CLI can write
  debriefs for Claude Code, Codex, and Cursor sessions, while Antigravity
  sessions themselves still have no transcript to debrief.
- Antigravity is hook-only. The pinned Numbat performs no at-rest scan for it,
  so no historical Antigravity session is imported and only sessions after
  hooks are installed are recorded. Antigravity stores conversations as
  encrypted `.pb` files and Belay has no Antigravity transcript reader, so an
  Antigravity session has timeline evidence and deterministic hook-based issues
  but no transcript excerpts, Habits debriefs, or transcript-derived lessons.
  Absence-based detectors stay silent for Antigravity.
- Numbat is pinned to an approved research commit rather than a release tag.
- `get_stats` is global-only; filtered statistics are not implemented.
- List cursors are opaque, endpoint-specific, filter-bound, and valid only for
  their stable ingestion snapshot.
- Session filtering is intentionally limited to safe indexed metadata. It does
  not search prompt, transcript, file-content, or arbitrary payload text.
- Historical and live coverage depends on what each harness exposes.
- Unknown outcomes and incomplete sessions are deliberately not coerced to
  success.
- Dirty validation artifacts (`belay_dirty=true`) are never supported for
  distribution.
