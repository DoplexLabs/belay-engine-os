# Implementation Brief: P0-06 Value-First Belay Local UX

## How to Use This Brief

Before writing code:

1. Read this brief and `docs/design/p0-06-value-first-local-ux.md`.
2. Inspect the current implementation and preserve P0-01 through P0-05
   contracts.
3. Keep all new narrative content fixed and all event-derived values
   structured.

During implementation:

4. Work only in the assigned file scope.
5. Do not introduce raw commands, arguments, output, prompts, completions,
   reasoning, file content, or arbitrary upstream prose.
6. The user requested speed: do not run broad or long suites. Migration,
   redaction, cursor, MCP closure, and browser-state changes still require the
   focused checks explicitly listed below.

When done:

7. Report changed files, quick checks, deviations, and remaining manual QA.
8. Do not merge, deploy, publish, or incur costs.

## Overview

P0-06 changes Belay Local from an implementation-centric evidence browser into
a value-first product. It adds safe source-signal metadata for known upstream
findings, hardens command minimization, and reorganizes Attention and Sessions
around observed fact, evidence, and next action.

## Cross-Service Contracts

### Source signal code

```go
SourceSignalCode *string `json:"source_signal_code"`
```

- Nullable and bounded to 64 bytes.
- Grammar: `^[a-z0-9][a-z0-9_.-]{0,63}$`.
- Numbat-origin only in P0-06.
- Not new fingerprint material.
- Summary is non-null only when all visible occurrences agree.
- Required-but-nullable in every HTTP and MCP issue object.

### Known source-signal catalog

`numbat/tamper.guardrails_off` maps only to the fixed title, observation,
limitation, and next action in the design.

### Safe command display

Browser display order:

1. canonical `observation.summary`;
2. safe `resource.name`;
3. `Command activity`.

No reconstruction of omitted arguments is allowed.

### Issue catalog additions

Keep `catalog_version=belay.issue-explanations.v1`. Add required fields:

- `display_title`;
- nullable `source_signal_code`;
- `source_signal_catalog_version=belay.source-signals.v1`;
- `source_signal_catalog_status=known|unknown|not_applicable`.

Add `review_agent_permissions` to the fixed `next_evidence_action` enum. MCP
implementation version becomes `1.2.0`.

## Implementation Sequence

1. Service A: canonical, analysis, storage, migration.
2. Service B and Service C proceed in parallel after the source-signal model
   compiles.
3. Service B: readmodel, HTTP/browser, value-first UX.
4. Service C: MCP DTO/schema propagation and privacy review.
5. Integrate, build a manual-QA binary, and independently review.

## Service A: Canonical, Analysis, and Storage

- **Contextual Agent:** Leibniz
- **Owns:** `internal/canonical/**`, `internal/analysis/**`,
  `internal/storage/local/**`
- **Must not edit:** presentation, command runtime, public docs

### Tasks

1. Add nullable source-signal fields and strict validation.
2. Populate safe Numbat rule IDs during reconciliation without changing issue
   identity.
3. Harden command executable and option-name minimization.
4. Add migration 013, authorized safe backfill, explicit progress reset,
   crash-resumable summary rebuild, readiness repair, and atomic issue epoch
   rotation.
5. Extend storage reads/writes and summary aggregation.

### Acceptance criteria

- Environment assignments and unsafe first tokens never survive as executable
  or command summaries.
- Existing issue IDs and fingerprints remain stable.
- Safe Numbat codes survive occurrence and summary reads.
- Invalid/legacy/disagreeing codes become null.
- Existing databases rebuild before issue reads.
- Source-code aggregation uses complete-and-equal semantics; partial or
  disagreeing sets return null.

### Focused checks

- `gofmt` on changed Go files.
- Command canaries: environment prefixes, wrappers, metacharacters,
  substitutions, redirects, inline values, control characters, and unknown
  executables.
- Fresh store, upgraded store, authorized mutation, interruption/restart,
  readiness, epoch rotation, old-cursor expiry, null and disagreeing codes.
- `git diff --check`.

## Service B: Readmodel, HTTP, and Browser

- **Contextual Agent:** Averroes
- **Owns:** `internal/presentation/readmodel/**`,
  `internal/presentation/localhttp/**`
- **Must not edit:** canonical, storage, localmcp, command runtime

### Tasks

1. Add fixed source-signal catalog metadata and known
   `tamper.guardrails_off` explanation.
2. Make Attention cards answer what happened, spread/recency, reliability, and
   next inspection. First viewport order is Issues, Evidence gaps, then nonempty
   After attempts; coverage and advanced filters are disclosures.
3. Reorder issue detail around explanation and a cancellable automatic preview
   of at most three cited events from the newest occurrence.
4. Collapse fingerprint, detector, scope, coverage, and IDs under technical
   details.
5. Hide/disable only new fix monitoring creation when the server reports
   ineligibility; preserve all durable history and retraction flows.
6. Reframe session overview into needs attention, observed work, resources, and
   outcome. Select at most five deterministic highlights from currently loaded
   events and label them partial.
7. Prefer canonical command summary in timeline rows.

### Acceptance criteria

- No primary screen leads with `Numbat finding`, detector version, or opaque
  fingerprint for the known signal.
- No raw rule title or command argument is displayed.
- Unknown signals remain neutral.
- Missing/retained evidence limitations remain visible.
- All event-derived values use text nodes.
- Preview state cancels and clears on reselection, close, and 410.
- Mobile layouts have one vertical page scroll and no nested scroll trap.

### Focused checks

- `node --check internal/presentation/localhttp/assets/app.js`.
- Browser asset checks for first-viewport order, evidence preview bounds,
  cancellation/reset, technical disclosure, ineligible CTA/history
  preservation, command-summary precedence, partial highlights, and mobile
  structure.
- `gofmt` on changed Go files.
- `git diff --check`.

## Service C: MCP Contract and Privacy

- **Contextual Agent:** Noether
- **Owns:** `internal/presentation/localmcp/**`, MCP contract documentation
- **Must not edit:** canonical, storage, readmodel, localhttp, command runtime

### Tasks

1. Add nullable bounded source-signal metadata to closed MCP DTO schemas.
2. Preserve the exact trust wrapper and fixed narrative boundary.
3. Confirm no raw command or arbitrary upstream prose reaches MCP.
4. Update the MCP contract for the additive field and fixed catalog semantics.

### Acceptance criteria

- Exactly nine tools remain advertised.
- Existing six tool contracts are unchanged.
- New issue outputs remain recursively closed and bounded.
- Unknown source signals never alter tool narrative or errors.

### Focused checks

- `gofmt` on changed Go files.
- MCP null/string schema cases, recursive closure, known/unknown source signal,
  injection-shaped identifiers, fixed narrative, and nine-tool discovery.
- Verify prohibited command/source bytes are absent from MCP output.
- `git diff --check`.

## Integration Checkpoints

1. Migration and model compile.
2. Readmodel and MCP consumers agree on nullable source-signal fields.
3. Browser renders known source signal and safe command summary.
4. `go build -o dist/belay-p0-value ./cmd/belay`.
5. Manual QA on a copy or existing preview state:
   - known `tamper.guardrails_off` issue;
   - unknown fallback;
   - command timeline;
   - unscoped fix controls;
   - Sessions regression;
   - MCP issue list/detail.

## Risks

- Migration 013 must not serve stale summary revisions.
- Source signal codes are untrusted identifiers, not prose.
- Environment-prefix parsing must fail closed.
- Browser reordering must preserve accessibility and stale-cursor handling.
- Exact commands remain intentionally unavailable.
