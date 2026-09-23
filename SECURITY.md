# Security policy

## Supported release

Belay Local is preparing an unsigned Developer Alpha. It has no production
security-support SLA. Only the latest explicitly identified alpha artifact is
eligible for investigation; older local builds should be reproduced against the
current default branch before reporting.

## Report a vulnerability privately

Do not open a public issue for a suspected vulnerability or include secrets,
private agent history, raw prompts, transcripts, local paths, database files,
Keychain material, or wrapped Windows data-key files in a public report.

Use GitHub's **Security → Report a vulnerability** flow when private
vulnerability reporting is enabled. If that option is unavailable, contact the
repository owners privately through the organization before disclosure and ask
for a secure reporting channel.

Include only the minimum evidence needed:

- affected Belay commit and artifact version;
- operating system and architecture: the macOS version and architecture, or the
  Windows edition, version, build number, and processor architecture;
- affected command or local surface, including the harness involved (Codex,
  Claude Code, Cursor, or Antigravity) and its version;
- reproduction using synthetic data;
- expected and observed behavior;
- impact assessment;
- whether monitor hooks were installed;
- confirmation that no real credential is attached.

Maintainers will acknowledge the report when a private channel is available,
triage severity and scope, and coordinate remediation and disclosure. No
publication timeline is promised for the Developer Alpha.

## Security boundaries

Belay Local:

- runs as the logged-in user without root;
- uses an unmodified checksum-pinned Numbat executable;
- minimizes data before canonical persistence;
- encrypts canonical payloads and scrubbed local transcript payloads using a
  platform-protected key: a macOS Keychain item on macOS, and on Windows a
  random key wrapped with the per-user Data Protection API (DPAPI, user scope,
  UI forbidden, wrapping entropy bound to the store ID) and stored as
  `keys/<store-id>.key` beside the database, unwrappable only by the Windows
  account that created it;
- binds its browser API to loopback and requires a per-launch token;
- writes exactly two files for Cursor, `~/.cursor/mcp.json` and
  `~/.cursor/hooks.json` (under `%USERPROFILE%` on Windows), because Cursor
  publishes no configuration CLI. Belay writes `mcp.json` itself: the `belay`
  entry is identity-checked before every mutation, a foreign or unverifiable
  entry of that name is preserved untouched, the replacement is an atomic
  owner-only write, and every other server and unknown key is preserved.
  `hooks.json` is written by the pinned Numbat and carries only Belay's own
  monitor-only hook entries, which `belay hooks uninstall` removes;
- writes two configuration files for Google Antigravity,
  `~/.gemini/config/mcp_config.json` and `~/.gemini/config/hooks.json` (under
  `%USERPROFILE%` on Windows), because Antigravity publishes no configuration
  CLI. Belay writes `mcp_config.json` itself under the same rules as Cursor:
  the `belay` entry is identity-checked before every mutation, a foreign or
  unverifiable entry of that name (for example a `serverUrl` remote entry or an
  entry with `env`) is preserved untouched, the replacement is an atomic
  owner-only write, every other server and unknown key is preserved byte for
  byte, `~/.gemini/config` is created when absent, and a symlinked path is
  refused. `hooks.json` is written by the pinned Numbat and carries only
  Belay's own monitor-only entries under the key `numbat`, which
  `belay hooks uninstall` removes. Beyond those files and the managed skill at
  `~/.gemini/config/skills/belay/SKILL.md`, Belay never touches the legacy
  `~/.gemini/antigravity/mcp_config.json`, a workspace
  `.agents/mcp_config.json`, or the global rules file `~/.gemini/GEMINI.md`,
  and never reads the encrypted conversations under
  `~/.gemini/antigravity/conversations/`;
- exposes a local stdio MCP catalog whose state-changing tools are limited to
  bounded user-governed records and lifecycle decisions;
- does not let MCP tools execute commands or write project files;
- sends only the fixed-field de-identified usage ping documented in
  `docs/contracts/telemetry-v1.md` (the receiver forwards those same fields to
  a hosted analytics processor keyed by the random ID and never retains the
  address) and a content-free public release check documented in
  `docs/contracts/update-check-v1.md`; users can switch either one off
  (`belay telemetry off`, `BELAY_TELEMETRY=0`, or `DO_NOT_TRACK=1`).

Belay Local is not a tamper-proof boundary against a process running as the same
operating-system user account. The Developer Alpha is unsigned: the macOS build
is neither signed nor notarized, and the Windows build is not
Authenticode-signed. Do not treat its observations as forensic proof or run it
on a machine where an unsigned alpha is outside policy.

## Out of scope

Reports are not security vulnerabilities when they solely concern:

- known unsigned alpha packaging, including the unnotarized macOS build and the
  Windows build without an Authenticode signature;
- absence of Intel macOS or Linux support;
- same-user modification of local files or processes;
- missing production update infrastructure;
- social engineering unrelated to Belay code or distribution.

These limitations may still be reported as normal bugs or launch feedback
through `SUPPORT.md`.
