# Security policy

## Supported release

Belay Local is preparing an unsigned Developer Alpha. It has no production
security-support SLA. Only the latest explicitly identified alpha artifact is
eligible for investigation; older local builds should be reproduced against the
current default branch before reporting.

## Report a vulnerability privately

Do not open a public issue for a suspected vulnerability or include secrets,
private agent history, raw prompts, transcripts, local paths, database files, or
Keychain material in a public report.

Use GitHub's **Security → Report a vulnerability** flow when private
vulnerability reporting is enabled. If that option is unavailable, contact the
repository owners privately through the organization before disclosure and ask
for a secure reporting channel.

Include only the minimum evidence needed:

- affected Belay commit and artifact version;
- macOS version and architecture;
- affected command or local surface;
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
  macOS Keychain-backed key;
- binds its browser API to loopback and requires a per-launch token;
- exposes a local stdio MCP catalog whose state-changing tools are limited to
  bounded user-governed records and lifecycle decisions;
- does not let MCP tools execute commands or write project files;
- sends only the fixed-field de-identified usage ping documented in
  `docs/contracts/telemetry-v1.md` and a content-free public release check
  documented in `docs/contracts/update-check-v1.md`; users can switch either
  one off.

Belay Local is not a tamper-proof boundary against a process running as the same
macOS user. The Developer Alpha is unsigned and unnotarized. Do not treat its
observations as forensic proof or run it on a machine where an unsigned alpha is
outside policy.

## Out of scope

Reports are not security vulnerabilities when they solely concern:

- known unsigned/unnotarized alpha packaging;
- absence of Intel, Linux, or Windows support;
- same-user modification of local files or processes;
- missing production update infrastructure;
- social engineering unrelated to Belay code or distribution.

These limitations may still be reported as normal bugs or launch feedback
through `SUPPORT.md`.
