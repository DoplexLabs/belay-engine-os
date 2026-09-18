# Belay Local storage lifecycle

Belay Local stores minimized envelope/index fields in plaintext SQLite columns
and encrypts canonical event JSON plus finding citation arrays before they
reach SQLite.

## Encryption and key lifecycle

- Payload encoding is `aes256gcm.v1`: AES-256-GCM with a fresh 96-bit nonce per
  write and a binary envelope containing an eight-byte magic value, envelope
  version, nonce length, nonce, and authenticated ciphertext.
- Authenticated context binds every payload to the envelope version, persisted
  random store ID, record type, record ID, and column name. Copying ciphertext
  between rows, columns, or databases fails authentication.
- A random 32-byte data key is stored in macOS Keychain under service
  `dev.doplex.belay.local.data-key.v1` and the database's random store ID.
  Hardware names, usernames, and Teams identities are not used.
- The persisted store ID moves with the database. An encrypted database with a
  missing or wrong key fails closed; Belay never creates a replacement key or
  opens encrypted rows as plaintext.
- Keychain command failures return payload-free errors. The key is provided to
  `/usr/bin/security -q -i` as part of one complete command on standard input,
  not in process arguments. Command-input mode is noninteractive and must never
  prompt the user for password data. Each invocation has a five-second timeout.
- Direct `/usr/bin/security` invocation is an M0 packaging mechanism. Before a
  signed Local application ships, key access must move to signed-app Keychain
  ACL integration and be verified against notarized release artifacts.

## Plaintext M0 upgrade

Migration `002` marks legacy payload columns as `plaintext.v0`, creates store
metadata plus the normalized `finding_event_citations` relation, and
strengthens append-only triggers.
On the first successful open:

1. Belay loads or creates the store key and writes a key-verification envelope.
2. Legacy upstream finding citations are resolved to exactly one Belay event ID
   in the same source run/session. An unresolved or ambiguous citation fails
   closed.
3. All legacy payloads, citation relations, and encoding markers are updated in
   one exclusively locked transaction.
4. `secure_delete`, verified `wal_checkpoint(TRUNCATE)` results, `VACUUM`, and a
   temporary rollback-journal transition remove obsolete plaintext page and
   WAL bytes.
5. If an external reader blocks WAL truncation, open returns a retryable
   maintenance error and preserves `plaintext_cleanup_required = 1`.
6. Open succeeds only after cleanup is complete. Interrupted cleanup is
   retried on the next valid-key open.

The upgrade temporarily holds legacy payloads and encrypted replacements in
memory so the row conversion can commit atomically. This is acceptable for M0
but should be measured against large dogfood databases before launch.

## Explicit retention and pruning

There is no launch retention default. Callers must provide at least one
positive bound:

- `max-age`: payload occurrence/detection age
- `max-events`: canonical event count
- `max-bytes`: combined encrypted event/finding payload bytes, excluding
  SQLite page and index overhead

The command is non-destructive by default. It reports current and eligible
record counts and payload bytes without payload content:

```console
belay prune --db /path/to/belay.sqlite --max-age 168h
```

Deletion requires explicit confirmation:

```console
belay prune --db /path/to/belay.sqlite --max-age 168h --apply
```

Events are deleted in deterministic occurrence-time, source-sequence, event-ID
order. Findings cite canonical Belay event IDs through a normalized relation;
pruning any cited event deletes its dependent findings in the same
transaction. Local pruning never reads Teams acknowledgement state.

The result separates committed deletion counts from post-delete maintenance.
If WAL checkpointing or compaction is blocked, deletion remains explicitly
reported as committed and `maintenance.state` is `pending` with a retryable
code. Re-running the same `--apply` command retries maintenance even when no
additional records are eligible.

Ordinary event/finding updates and deletes remain trigger-rejected. Each store
open installs a process-local mutation authorization function, and upgrade or
prune enables it only around an exclusive transaction. Authorization resets on
every return path and process crash; there is no persistent bypass row.
