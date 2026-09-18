# Belay Contracts

These documents are the canonical cross-surface contracts for Belay Local and
Belay Teams. They begin as M0 candidates and become frozen only after the
Numbat adapter spike and contract tests pass.

Compatibility rules:

- Additive optional fields are allowed within a major contract version.
- Required-field removal, meaning changes, or enum narrowing require a new
  major version.
- Unknown fields must be ignored by readers and preserved only where explicitly
  required.
- Unknown enum values must not be interpreted as success or completion.
- Raw prompts, model completions, file contents, diffs, credentials, and
  unredacted environment values are structurally prohibited.
- Every generated artifact must identify the source schema version.
