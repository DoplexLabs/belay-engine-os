# Update check v1

Belay packaged builds may check for a newer compatible release without
blocking startup. The check is optional, fail-open, and independent from
product telemetry.

## Request

- Endpoint: the public GitHub releases API for `DoplexLabs/belay-engine`
- Method: `GET`
- Frequency: at most once every 18 hours per Belay home
- Headers: GitHub API accept/version headers, the installed Belay version in
  `User-Agent`, and the prior response ETag when available
- Body: none

Belay sends no telemetry ID, session content, paths, project identity,
prompts, transcripts, costs, or usage totals. GitHub and ordinary network
infrastructure can observe the source IP and standard HTTPS request metadata.

## Local state

Belay stores release metadata in `<BELAY_HOME>/updates.json` with owner-only
permissions: check time, ETag, latest compatible version, release name, public
release URL, publication time, reminder time, dismissed version, opt-out
state, and a bounded local error string.

A compatible release must be non-draft and contain an archive for the running
platform. Belay matches the release asset name by suffix:

| Running platform | Required asset suffix |
|---|---|
| `windows` (any architecture) | `-windows-<arch>.zip`, e.g. `-windows-amd64.zip` |
| every other platform, including macOS | `-darwin-arm64.tar.gz` |

Apple Silicon is the only published macOS archive, so a non-Windows build
always looks for `-darwin-arm64.tar.gz`; Intel macOS and Linux have no release
channel of their own. Belay compares semantic versions, including prereleases.
It never downloads or installs an update automatically.

## User controls

- `belay updates status`
- `belay updates check`
- `belay updates off`
- `belay updates on`
- `BELAY_UPDATE_CHECK=0`

The browser notice supports “Remind me later,” which waits another 18 hours,
and “Skip this version,” which remains hidden until a different release is
available.

Development builds do not check the network unless
`BELAY_UPDATE_ENDPOINT` is explicitly set for local testing.
