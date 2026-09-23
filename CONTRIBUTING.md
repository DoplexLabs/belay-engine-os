# Contributing to Belay Local

Belay Local is an MIT-licensed open-source edge project. Contributions should
preserve its endpoint-first, local-only, fail-open design.

## Before starting

1. Read `README.md`, the current code, and the applicable contract documents.
   Treat dated design and launch documents as historical context when they
   conflict with shipped behavior.
2. Search existing issues and discussions when the repository is public.
3. For a substantial or contract-changing proposal, open a design discussion
   before implementation.

## Non-negotiable boundaries

- Do not modify or patch Numbat source. Belay consumes a pristine pinned
  upstream revision through its executable interface.
- Do not add arbitrary MCP project-file writes, command execution,
  enforcement, remediation, or agent-action dependencies. State-changing MCP
  operations must remain bounded, local, and gated by explicit user
  confirmation.
- Do not add hosted model calls. Do not add outbound calls or fields beyond
  `docs/contracts/telemetry-v1.md` and `docs/contracts/update-check-v1.md`.
  Both paths must remain optional, fail-open, content-free, and off in dev
  builds.
- Keep canonical Belay events minimized. Prompt bodies, transcripts, reasoning
  text, file contents, raw endpoint identity, and raw evidence paths do not
  belong in canonical events.
- Transcript content may be retained only in the encrypted Local transcript
  sidecar, after secret scrubbing, and must never be exposed on a network
  surface.
- Do not include real customer data, credentials, personal paths, or transcripts
  in tests, issues, or pull requests.
- Agent actions must remain usable when Belay or Numbat fails.

## Development checks

Go 1.27 is required.

```bash
make verify
```

Every change must also keep the Windows target building and vetting cleanly:

```bash
make verify-windows
```

`verify-windows` cross-compiles and vets every package for `windows/amd64` and
builds `windows/arm64`. It runs from macOS or Linux and needs no Windows
machine. A change that breaks it is not mergeable.

For release-surface-only changes:

```bash
make verify-release-surface
```

On an Apple Silicon Mac, a local non-publishing package check is:

```bash
make alpha-readiness ALPHA_VERSION=0.0.1-alpha.11
```

On a clean checkout, a local non-publishing Windows archive check is:

```bash
make preview-windows ALPHA_VERSION=0.0.1-alpha.11 WINDOWS_ARCH=amd64
```

The packaging command requires a clean checkout by default. A dirty artifact
created with `BELAY_ALPHA_ALLOW_DIRTY=1` is for local validation only and must
not be distributed.

## Windows-affecting changes

A change that touches platform-conditional behavior — the data-key store,
executable discovery and suffixes, path handling, subprocess environments,
hook or MCP configuration writes, packaging, or the installers — must:

- keep `make verify-windows` passing;
- state in the pull request which gates of
  `docs/launch/windows-clean-machine-alpha-qa.md` it affects;
- be re-validated against that checklist on a real Windows machine before the
  Windows channel can claim the behavior. Cross-compilation and the read-only
  packaged smoke test do not satisfy those gates.

Do not assert Windows behavior in documentation that only a Windows run can
establish. The Windows clean-machine pass has not been executed yet.

## Pull requests

Keep changes focused and explain:

- the user-visible behavior;
- privacy and fail-open impact;
- tests and commands run;
- documentation or contract changes;
- known limitations;
- whether the change affects package contents or checksums.

Do not commit generated `/dist/` or `/bin/` output. Do not create tags, releases,
or deployments from a contribution unless maintainers explicitly authorize that
separate action.

## Security issues

Follow `SECURITY.md`. Do not disclose suspected vulnerabilities or sensitive
evidence in a public issue.
