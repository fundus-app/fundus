# ADR-0001: Event-sourced storage on JSONL, no database

**Status:** accepted (implemented in `internal/store`)

## Context

Fundus promises that no confirmed input can be lost and that every change can be explained and undone. A personal instance produces at most tens of thousands of transactions over years. A database server would add operational and development weight without buying anything the guarantees need.

## Decision

- The canonical store is an append-only event log of transactions in JSONL segments under `<data>/events/<first-seq, 12 digits>.jsonl`. A segment rotates after 16 MiB.
- Each record is `{"seq","prev","hash","txn"}`; `hash` is SHA-256 over `prev + "\n" + <exact txn bytes>`. The chain is verified on every open and replay.
- Every append is fsynced before the in-memory state changes. New segment files are followed by a directory fsync.
- A JSON snapshot (`<data>/snapshots/state.json`) is written atomically on shutdown and every 200 transactions. It is validated against the log (seq and hash) and discarded when stale; the log is then replayed from the start.
- Damage confined to the tail of the last segment (truncated line, trailing garbage, bad hash on the final line) is copied to `<segment>.corrupt-<unix ts>` and cut. Damage anywhere else refuses to start with a message naming file and line.
- The log is never rewritten or compacted. MongoDB or any other database is not planned for the personal system; a `Store`-level replacement remains possible but is not designed for.

## Consequences

Backups are a copy of one directory. Undo works from before-images stored in the transaction. Start-up cost is bounded by log size, which is acceptable at personal scale. The full transaction history is held in memory (see `core.state.txns`); a paging strategy is needed before the log reaches hundreds of megabytes.
