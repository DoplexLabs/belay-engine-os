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
- macOS version and Apple Silicon model;
- Codex or Claude Code version;
- exact command and sanitized output;
- whether the network was disabled;
- whether hooks were installed;
- expected and observed behavior;
- a minimal synthetic reproduction.

Never attach raw agent history, prompts, transcripts, real secrets, Keychain
material, or an unredacted Local database.

## Alpha scope

The supported alpha surface is:

- Apple Silicon macOS;
- Codex and Claude Code;
- one-command packaged onboarding with `./bin/belay quickstart`;
- a no-hook `./bin/belay local` path;
- historical scan;
- explicit monitor-only live hooks;
- loopback Local browser;
- governed local MCP tools for evidence, issues, fixes, Mission Packs, and
  experience learning;
- offline Local operation.

Intel, Linux, Windows, Teams, arbitrary MCP writes, MCP command execution,
enforcement, automatic updates, signed installation, and production support
guarantees are outside the alpha.

## Known limitations

- The binaries are unsigned and unnotarized.
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
