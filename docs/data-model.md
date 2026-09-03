# Data model

## Layers

1. **Captures** — raw input, append-only, never edited.
2. **Objects** — notes, tasks, topics, sources, conversations with stable IDs, revisions and provenance.
3. **Views** — derived on request (inbox, open, relevant, topic pages, search, receipts); never stored.

## Identifiers

`<prefix>_<ULID>`: `cap_`, `note_`, `task_`, `topic_`, `src_`, `conv_`, `msg_`, `txn_` (transactions), `b_` (blocks). ULIDs sort by creation time. Block IDs inside transactions are derived deterministically from `(txn id, op index, counter)` so log replay reproduces them.

## Common header (`Meta`)

| field | meaning |
|---|---|
| `id`, `type` | identity; `type` is `capture`, `note`, `task`, `topic`, `source`, `conversation`, `message` |
| `rev` | starts at 1, increases by exactly 1 per transaction that touches the object |
| `created_at`, `updated_at` | set from the transaction time (UTC) |
| `archived` | soft delete for notes, tasks, topics; captures use `dismissed` instead |

## Capture

`text` (unchanged input), `source` (`cli`, `api`, `desktop`, `mobile`, `chat`, `voice`, `import`, or the client name), `status`, `attempts` (incremented on every transition to `processing`), `result`, `conversation_id`, `answer` (the user's reply to a clarification question).

Statuses: `pending` (queued) → `processing` → `processed` | `needs_review` (model unsure, low confidence, or `auto_create = false`; nothing written) | `failed` (provider or validation error after retries; nothing written) → `dismissed` (user). `retry` returns any capture to `pending`. Stale `processing` captures are reset to `pending` when the daemon starts.

`result`: `classification`, `confidence`, `summary`, `question`, `reason`, `error`, `retryable`, `provider`, `model`, `proposal`, `processed_at`. Receipts are found by cause, not stored on the capture.

## Note

`kind` (`note` | `idea`), `title`, `body` (document), `topics[]`, `origins[]` (capture or source IDs), `related[]` (note IDs).

## Task

`text`, `state` (`open` | `waiting` | `later` | `done`), `due` (YYYY-MM-DD), `effort_minutes`, `importance` (0 unset, 1 low, 2 normal, 3 high), `waiting_on`, `topics[]`, `origins[]`, `notes[]`, `completed_at`, `mentions` (later captures that referred to the task).

Attention score (`core.Score`): importance 3 → +4, 2 → +2, 1 → +0.5; overdue +6, due today +5, within 3 days +3, within 7 days +1.5, any due date +0.5; mentions +0.75 each (capped at 3); created within 7 days +1; effort ≤ 30 min +0.5; a linked topic active in the last 14 days +1. Reasons are returned with the score.

## Topic

`kind` (`topic` | `person` | `project`), `name`, `aliases[]`, `summary` (document). Names and aliases are unique after normalisation (tokenised, folded). Backlinks come from notes and tasks that list the topic.

## Source

`url`, `title`, `fetched_at`, `excerpt`, `query`. Created by research (planned) or manually.

## Conversation

`title`, `message_count`, `last_message_at`. Messages are separate objects, see below.

## Document and blocks

```json
{"blocks":[{"id":"b_…","type":"paragraph","text":"…","sources":["cap_…"],"pinned":false}]}
```

Block types and fields: `heading` (`level` 1–3, `text`), `paragraph` (`text`), `list` (`items[]`, `ordered`), `quote` (`text`), `code` (`lang`, `text`), `callout` (`kind` ∈ `info`, `warning`, `question`, `external`; `text`), `task_ref` (`ref`), `source_ref` (`ref`, `text`). `sources` lists the captures/sources the block derives from; `pinned` blocks reject edits by `llm:` actors.

Markdown mapping: `#`/`##`/`###` headings; blank-line separated paragraphs (soft breaks kept); `- ` / `1. ` lists (indented lines continue an item); `> ` quotes; `> [!info] …` callouts; ```` ``` ```` code fences; `[[task_…]]` alone on a line; `[[src_…]] excerpt`. The round trip is lossless.

## Transactions and the event log

A transaction (`txn`) is the unit of the log, of undo and of the audit view:

| field | meaning |
|---|---|
| `seq` | 1-based, contiguous |
| `id`, `at`, `actor` | `txn_…`, UTC time, `user:<client>` / `llm:triage/<provider>/<model>` / `llm:chat/…` / `system` |
| `cause` | `{kind, id}`: `user`, `capture`, `conversation`, `undo`, `system` |
| `ops[]` | the typed operations as applied, including generated IDs |
| `before` | `{id: before-image JSON or null}` for every touched object |
| `touched[]` | object IDs in order of first touch |
| `summary` | the receipt text |
| `undo_of` | set on compensating transactions |

Log record (one line): `{"seq":N,"prev":"<sha256 hex or empty>","hash":"<sha256 hex>","txn":{…}}` with `hash = sha256(prev + "\n" + exact txn bytes)`.

Directory layout:

```
<data_dir>/
  events/000000000001.jsonl      segments named by first seq; rotate at 16 MiB
  events/000000000001.jsonl.corrupt-<ts>   damaged tail cut at start, kept for inspection
  snapshots/state.json           {"seq","hash","at","objects":[…]}, atomic write
```

Snapshots are written on shutdown and every 200 transactions; a snapshot whose `seq`/`hash` do not match the log is ignored and the log is replayed from the start. The log is never rewritten.

## Receipts

Rendered from the committed ops, never from model text: `txn_id`, `seq`, `at`, `actor`, `cause`, `lines[]` (`op`, `object_id`, `object_type`, `text`), `summary`, `undoable`, `undo_of`, `undone_by`, `quiet` (bookkeeping only).

## Exports

- JSON: every object plus all receipts (`GET /v1/export?format=json`).
- Markdown zip: `notes/<title>-<id8>.md`, `ideas/…`, `topics/…` with YAML front matter (`id`, `type`, `kind`, `topics`, `origins`, `aliases`), `tasks.md` grouped by state, `captures.md` newest first. IDs are kept in front matter and HTML comments so the export stays traceable.


## Messages (0.3.1)

Conversations no longer embed their messages. A `message` object carries `conversation_id`, `index` (0-based order), `role`, `text`, derived `blocks`, `capture_id` (user turns), `txn_ids` and `refs` (assistant turns) and `interrupted` (a system-inserted marker after a restart). The conversation object keeps `title`, `message_count` and `last_message_at`. This keeps before-images small: appending a message touches the message (new) and the conversation counters only.

## Proposals and retries (0.3.1)

`CaptureResult` gained `reason` (`unclear` | `low_confidence` | `proposal` | `discard` | `undone`), `proposal` (the model's operations in its own vocabulary, stored when a capture is parked so the user can accept them without a new model call) and `retryable` (transient provider failures the worker retries with exponential backoff, at most ten times).

## Stored receipts (0.3.1)

Transactions store `lines` and `summary`; the audit view renders those, never a re-rendering against later state, so a renamed task still shows the receipt as it was. Before-images live only in the log and are read back for undo.

## Time zone

`timezone` in the config (IANA name) defines "today" for due dates and the attention score, the timestamps shown to the model, and export formatting. Stored timestamps are UTC.
