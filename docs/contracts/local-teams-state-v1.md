# Candidate Contract: Local and Teams State V1

- **Status:** Candidate
- **Contract ID:** `belay.mode-state.v1`

## States

| State | Local available | Teams upload | Meaning |
|---|---:|---:|---|
| `local_only` | yes | no | Default after installation |
| `enrollment_pending` | yes | no | User started but has not completed enrollment |
| `teams_active` | yes | yes | Valid Teams credential and affirmative consent |
| `teams_paused` | yes | no | User disabled upload without deleting Local data |
| `teams_revoked` | yes | no | Server or user revoked the machine credential |
| `workspace_deleted` | yes | no | Hosted workspace is terminally deleted |

## Allowed transitions

- Install → `local_only`
- `local_only` → `enrollment_pending`
- `enrollment_pending` → `teams_active` or `local_only`
- `teams_active` ↔ `teams_paused`
- `teams_active` or `teams_paused` → `teams_revoked`
- Any enrolled state → `workspace_deleted` after a verified terminal response
- A new explicit enrollment may move a revoked/deleted installation back through
  `enrollment_pending`

## Invariants

- Local timeline, detection, and MCP remain available in every state.
- No event is queued or uploaded before `teams_active`.
- New post-enrollment events are eligible for Teams by default.
- Pre-enrollment Local history requires a separate import selection and consent.
- Pausing or revoking Teams does not delete Local history.
- Workspace deletion purges Teams credentials and outbound queue records.
- Workspace deletion does not silently remove Local history.
- A deleted workspace's data cannot re-enter Teams without a new enrollment and
  explicit historical import.
