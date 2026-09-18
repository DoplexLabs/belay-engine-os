# Candidate Contract: Teams Transmitted Fields V1

- **Status:** Candidate; blocks Teams enrollment implementation
- **Contract ID:** `belay.transmission.v1`

## Principle

Belay Teams receives the minimized normalized events required to reconstruct
timelines and calculate auditable projections. It does not receive raw
transcripts, prompts, completions, secrets, or a fingerprint-only substitute.

The enrollment UI and CLI must display this contract before upload is enabled.

## Allowed field classes

| Field class | Examples | Constraints |
|---|---|---|
| Random identifiers | event, session, batch IDs | No hardware serials or OS advertising IDs |
| Product versions | wrapper, adapter, Numbat, schema | Semantic/release identifiers only |
| Time | occurrence and observation timestamps | UTC; bounded clock-skew metadata |
| Source metadata | harness, hook/artifact/OTLP source | Enumerated or bounded strings |
| Action metadata | event type, tool/action family, outcome | No prompt or completion text |
| Resource metadata | repository-relative path, executable name, hostname | Redacted, normalized, bounded |
| Bounded summary | sanitized command/action summary | Maximum 512 UTF-8 bytes after redaction |
| Numeric results | exit code, duration, counts | No raw output streams |
| Coverage metadata | depth, source, confidence | Versioned |
| Redaction metadata | policy version and removal counts | Never includes removed values |
| Historical metadata | historical flag and reconstruction source | No artifact body |
| Detection evidence | rule ID/version and event references | No privately authored rule body unless explicitly public |

## Resource normalization

- File paths are repository-relative where a repository root is known.
- Home directories and absolute path prefixes are removed.
- Hosts contain a normalized hostname or address class; URLs drop userinfo,
  query strings, and fragments.
- Commands contain the executable and a locally redacted bounded summary.
- Tool arguments are excluded by default. An adapter may allowlist individual
  structured arguments only after a fixture proves they cannot contain content
  classes prohibited below.

## Always prohibited

- Prompts, completions, hidden reasoning, and chat transcripts
- File contents, patches, diffs, clipboard contents, or editor buffers
- Full stdout/stderr, terminal history, or shell environment
- Credentials, secrets, cookies, authorization headers, private keys
- Raw absolute user paths
- Raw request/response bodies
- Arbitrary unbounded extension objects
- Data collected before Teams enrollment unless the user separately selects a
  historical import

## Limits

- Event serialized size: candidate maximum 16 KiB
- Bounded display string: candidate maximum 512 bytes
- Attribute key count: candidate maximum 32
- Attribute string value: candidate maximum 256 bytes
- Batch limits are defined by the Teams ingest contract.

Limits must be adjusted from dogfood measurements before freeze.

## Required tests

1. Seed credentials in every adapter fixture and prove they do not cross the
   transmission boundary.
2. Seed prompts, completions, file bodies, stdout, environment values, absolute
   paths, URLs with tokens, and oversized values; every item is rejected or
   irreversibly minimized locally.
3. Packet-capture an enrolled endpoint and compare every body field against this
   allowlist.
4. Verify Local creates no outbound request when Teams is disabled.
5. Verify historical records are absent until separate import consent.
