# Fundus HTTP API v1

Base URL: `http://127.0.0.1:7433` by default. All request and response bodies are JSON (`Content-Type: application/json`). Responses carry `Cache-Control: no-store`.

## Authentication

- If `token` is empty in the config, no authentication is required. The daemon refuses to start on a non-loopback `listen` address without a token unless `--insecure` is given.
- If `token` is set, requests must send `Authorization: Bearer <token>`. Loopback clients are exempt unless `require_token_on_loopback = true`.
- `GET /v1/events` also accepts `?token=<token>` because `EventSource` cannot set headers.
- The optional header `X-Fundus-Client: <name>` names the client; it becomes the actor `user:<name>` in the audit log (default `user:api`). Only `[a-z0-9_-]` survive.
- `--dev-cors` allows cross-origin requests from `http://localhost:*` and `http://127.0.0.1:*` only (for `flutter run -d chrome`); it is refused with a non-loopback listen address.


### Browser protections

Every `/v1/` request, with or without a token, must pass three checks so that a web page open in a local browser cannot drive the daemon:

| Check | Failure | Notes |
|---|---|---|
| `Host` is loopback, the listen host, or listed in `allowed_hosts` | `421 bad_host` | Blocks DNS rebinding. Set `allowed_hosts = ["fundus.lan"]` when reaching the daemon by name. A daemon bound to all interfaces (`0.0.0.0`, `::`) accepts any Host; the token is its protection. |
| No cross-site `Origin` / `Sec-Fetch-Site: cross-site` | `403 cross_site` | Same-origin (the embedded UI) and non-browser clients pass. `--dev-cors` additionally allows `http://localhost:*` origins. |
| Bodies are `application/json` | `415 content_type` | Blocks HTML-form and `text/plain` simple requests. |

Requests carrying `X-Forwarded-For`, `Forwarded` or `X-Real-IP` are treated as remote and need the token even on loopback.

Other limits: at most 100 ops per `POST /v1/commands` (each capture-level plan is capped by `autonomy.max_ops_per_capture` and `autonomy.max_new_topics_per_capture`), one chat turn in flight per conversation and four overall (`409 busy`), at most 32 concurrent event streams (`503 too_many_subscribers`).

## Errors

```json
{"error": {"code": "not_found", "message": "object not found: note_…", "details": {…}}}
```

| Status | Code | When |
|---|---|---|
| 400 | `bad_request`, `invalid` | malformed body, failed core validation, pinned block |
| 401 | `unauthorized` | missing or wrong bearer token |
| 403 | `forbidden` | `object.restore`/`object.remove` outside undo |
| 404 | `not_found` | unknown object or transaction |
| 409 | `conflict`, `already_undone`, `undo_conflict` (details: `{txn_id, objects}`) | `expected_rev` mismatch; second undo; objects changed after the transaction |
| 502 | `chat_failed` | provider error during a conversation turn |
| 503 | `unavailable` | chat not configured |

## Routes

### Health and stats

`GET /v1/health` → `{"ok":true,"version":"dev","seq":42,"uptime_seconds":12,"triage":"openai/gpt-5.6-luna","chat":"openai/gpt-5.6-terra","dictation":true,"recovery":null}`. `recovery` is non-null when the event log had to cut a damaged tail at start.

`GET /v1/stats` → `{"captures","inbox","notes","ideas","open_tasks","topics","conversations","seq"}`.

### Captures

`POST /v1/captures` body `{"text": "…", "source": "cli", "id": "cap_…", "conversation_id": "conv_…"}` (`source`, `id`, `conversation_id` optional). Returns **202** with the capture and its receipts. A client-supplied `id` makes retries idempotent: an existing id returns **200** with the existing capture.

```json
{"id":"cap_01…","type":"capture","rev":1,"created_at":"…","updated_at":"…",
 "text":"Check why the Deye's second string …","source":"cli","status":"pending","receipts":[]}
```

Capture statuses: `pending` → `processing` → `processed` | `needs_review` | `failed`; `dismissed` by the user. `result` (when present) carries `classification`, `confidence`, `summary`, `question`, `error`, `provider`, `model`, `processed_at`.

`GET /v1/captures?status=<status>&limit=50` — newest first. `GET /v1/inbox` — captures in `pending`, `processing`, `needs_review`, `failed`. `GET /v1/captures/{id}`.

`POST /v1/captures/{id}/retry` body `{"answer": "…"}` (optional) — sets the capture back to `pending` (storing the answer for the model) and wakes the worker. Returns 202.

`POST /v1/captures/{id}/dismiss` → receipt.

### Objects and views

`GET /v1/objects/{id}` → `{"object": {…}, "receipts": [...], "backlinks": [{"id","type","title"}], "topics": [{"id","type","title"}], "markdown": "…"}`. `markdown` is the note body or topic summary rendered in the Markdown subset; `receipts` are the last 20 transactions that touched the object.

`GET /v1/notes?kind=note|idea&archived=1` → note views: the note plus `topic_names` and `preview`.

`GET /v1/tasks?state=open,waiting,later,done&archived=1` → task views sorted by attention score: the task plus `score`, `reasons`, `topic_names`.

`GET /v1/relevant?limit=10` → the top open tasks by attention score.

`GET /v1/topics?archived=1` → topics with `note_count`, `open_task_count`, `last_activity`. `GET /v1/topics/{id}` → `{"topic", "notes": [...], "tasks": [...]}`.

`GET /v1/search?q=…&types=note,task,topic,capture&limit=20&all=1` → `[{"id","type","title","score","preview","kind","state"}]`. Archived objects and captures are excluded unless `all=1`.

### Changes and undo

`GET /v1/changes?limit=50&before=<seq>&all=1` → receipts newest first. Quiet bookkeeping transactions (status flips, conversation appends) are hidden unless `all=1`.

Receipt:

```json
{"txn_id":"txn_01…","seq":9,"at":"…","actor":"llm:triage/openai/gpt-5.4-mini",
 "cause":{"kind":"capture","id":"cap_01…"},
 "lines":[{"op":"task.create","object_id":"task_01…","object_type":"task","text":"Created task \"…\" in Solar."}],
 "summary":"Created task \"…\" in Solar.","undoable":true,"undone_by":""}
```

Actors: `user:<client>`, `llm:triage/<provider>/<model>`, `llm:chat/<provider>/<model>`, `system`.

`GET /v1/changes/{txn}` → `{"receipt", "txn"}` including ops and before-images.

`POST /v1/changes/{txn}/undo` body `{"force": false}` → receipt of the compensating transaction, or 409 `undo_conflict`.

`POST /v1/commands` body `{"ops": [Op, …]}` → receipt. Applies core ops as the calling user (see the op table below).

### Conversations

`GET /v1/conversations?limit=50` → `[{"id","title","messages","updated_at"}]`. `POST /v1/conversations` body `{"title": "…"}` → 201 conversation. `GET /v1/conversations/{id}` → the conversation with its messages (`role`, `text`, `blocks`, `capture_id`, `txn_ids`, `refs`, `at`).

`POST /v1/conversations/{id}/messages` body `{"text": "…"}` → runs one turn and returns

```json
{"conversation_id":"conv_…","message":{"role":"assistant","text":"…","blocks":{…},"txn_ids":[…],"refs":["note_…"],"at":"…"},
 "receipts":[…],"steps":[{"kind":"tool_call","tool":"search","args":{…},"summary":"Searching for \"…\""}],
 "usage":{"input_tokens":1234,"output_tokens":210}}
```

Progress is also streamed as `chat.step` events. Assistant text cites objects as `[[note_…]]`.

### Export and probe

`GET /v1/export?format=json` → attachment `fundus-export.json` (`version`, `exported_at`, `seq`, `objects`, `changes`). `GET /v1/export?format=markdown` → zip with `notes/`, `ideas/`, `topics/`, `tasks.md`, `captures.md`.

`GET /v1/llm/probe?role=triage|chat&provider=…&model=…` → `{"provider","model","reachable","structured","tools","german","latency","errors","mode"}`.

### Events (SSE)

`GET /v1/events` streams `event: <type>` / `data: <json>` frames with a `: ping` comment every 15 s.

| Event | Payload |
|---|---|
| `hello` | `{"seq", "version"}` on connect |
| `txn.committed` | `{"type","at","payload": Receipt}` |
| `capture.changed` | `{"type","at","payload": Capture}` after any transaction touching a capture |
| `chat.step` | `{"type","at","payload": {"conversation_id", "step": Step}}` |


### Added in 0.3.1

| Route | Purpose |
|---|---|
| `POST /v1/captures?wait=<ms>` | Same as `POST /v1/captures`, but waits up to `wait` milliseconds (max 60000) for triage to finish and returns the final capture with receipts; a pending capture is returned when the window elapses. |
| `POST /v1/captures/{id}/accept` | Apply the proposal stored on a parked (`needs_review`) or failed capture without a model call. Body `{}` applies `result.proposal`; body `{"operations":[…]}` applies an edited set in the model vocabulary. The actor is the user. Returns the capture with receipts. |
| `POST /v1/captures/{id}/dismiss` | Now returns the capture with receipts (was: a receipt). |
| `POST /v1/captures/{id}/retry` | Returns `409 processing` while the capture is being processed. |
| `GET /v1/objects?ids=a,b,c` | Resolve many ids to `{id,type,title,preview,state,created_at}` (max 200), for citation and provenance chips. |
| `GET /v1/changes?after=<seq>` | Receipts with seq > after, oldest first, for resuming an event stream. |
| `GET /v1/backup` | A zip of the event log segments and the latest snapshot, taken under the write lock (consistent). |
| `POST /v1/conversations/{id}/messages` | Body may carry `"id": "cap_…"` (client-generated capture id) to make the turn idempotent: a repeat returns the stored reply. Conversations now return `messages` as message objects (`id`, `conversation_id`, `index`, `role`, `text`, `blocks`, `capture_id`, `txn_ids`, `refs`, `created_at`, `interrupted`). |
| `GET /v1/health` | Adds `timezone`, `ui` (embedded web UI present) and `warnings[]` (missing API keys, recovered log). 0.3.5 adds `dictation` (a usable transcription provider is configured). |
| `GET /v1/topics/{id}` | 0.3.5: `tasks` holds open, waiting and deferred tasks; completed ones move to `done_tasks`, most recently finished first. |
| `GET /v1/tasks?state=done` | 0.3.5: completed tasks are ordered by `completed_at`, newest first. |
| `object.archive` | Is what "Delete" in the client sends: the note, task or topic leaves every view and search, the receipt reads "Deleted …", undo (or `object.unarchive`, "Restored …") brings it back. Models may not archive; `object.remove` stays reserved for undo. Deleting a topic leaves its notes and tasks in place. |

SSE `capture.changed` payloads include `receipts[]`; `txn.committed` frames carry `id: <seq>`; clients resume with `GET /v1/changes?after=<last id>`. Receipts include `touched[]` (object ids the transaction changed) and an `object.changed` event `{id,type,rev,removed}` is published per touched non-capture object. Since 0.3.5 receipts also carry `affected[]`: topics whose member list changed (a note or task was created in them, linked or unlinked) without the topic record itself changing; each gets an `object.changed` event with `members: true` and its unchanged `rev`, so a topic page can refresh. Undo ignores `affected`.

Captures never appear in the Changes view (their `capture.create` receipts are quiet); they have their own views.

## Core operations (`POST /v1/commands`)

Edits (`note.revise`, `note.update`, `note.set_markdown`, `task.update`, `topic.update`, `topic.set_summary`, `topic.merge`, `object.archive`, `object.unarchive`) require `expected_rev`; the request is rejected with 400 otherwise. Additional ops since 0.3.1:

| Op | Fields | Effect |
|---|---|---|
| `note.set_markdown` | `id`, `expected_rev`, `markdown`, `origins?` | Replace the whole body; blocks whose content is unchanged keep their id, provenance and pinned flag. User only. |
| `topic.set_summary` | `id`, `expected_rev`, `markdown`, `origins?` | Same for a topic summary. User only. |
| `topic.merge` | `id` (survivor), `expected_rev`, `from` | Relink notes and tasks from `from` to `id`, add its name and aliases as aliases, archive `from`. User only. |
| `conversation.update` | `id`, `expected_rev`, `title` | Rename a conversation. |
| `message.create` | `conversation_id`, `message{role,text,capture_id?,txn_ids?,refs?}` | Append a message; `blocks` are derived from `text`. Replaces the former `conversation.append`. |


Every op is a flat object with `"op"` plus the fields below. Ops that modify existing objects accept `expected_rev`; when set it must equal the object's current `rev` or the whole command fails with 409. All ops of one command commit as one transaction.

| op | fields |
|---|---|
| `capture.create` | `id?`, `text`, `source?`, `conversation_id?`, `status?` |
| `capture.set_status` | `id`, `expected_rev?`, `status`, `result?`, `answer?` |
| `note.create` | `id?`, `kind` (`note`\|`idea`), `title`, `markdown`, `topics[]` (topic ids), `origins[]` (capture ids) |
| `note.revise` | `id`, `expected_rev?`, `edits[]` (`{action, block_id?, markdown?, sources?}` with actions `append`, `prepend`, `insert_after`, `replace`, `delete`, `pin`, `unpin`), `origins[]?` |
| `note.update` | `id`, `expected_rev?`, `title?`, `kind?`, `add_topics[]`, `remove_topics[]`, `add_related[]` |
| `task.create` | `id?`, `text`, `topics[]`, `origins[]`, `state?`, `due?` (YYYY-MM-DD), `effort_minutes?`, `importance?` (0–3), `waiting_on?` |
| `task.update` | `id`, `expected_rev?`, `text?`, `state?` (`open`\|`waiting`\|`later`\|`done`), `due?` (`""` clears), `effort_minutes?`, `importance?`, `waiting_on?`, `add_topics[]`, `remove_topics[]`, `add_notes[]`, `mention` |
| `topic.create` | `id?`, `name`, `kind?` (`topic`\|`person`\|`project`), `aliases[]` |
| `topic.update` | `id`, `expected_rev?`, `name?`, `kind?`, `aliases[]?`, `edits[]` (summary) |
| `source.create` | `id?`, `url`, `title?`, `text` (excerpt), `query?` |
| `conversation.create` | `id?`, `title?` |
| `conversation.update` / `message.create` | see the 0.3.1 table above; `conversation.append` in logs written before 0.3.1 is replayed as `message.create` |
| `object.archive` / `object.unarchive` | `id`, `expected_rev?` (captures cannot be archived) |
| `object.restore` / `object.remove` | reserved for undo; rejected with 403 otherwise |

Pinned blocks can only be edited by `user:` actors. Topic names and aliases must be unique after normalisation.

## Model operation vocabulary (triage and `apply_operations`)

The model never emits core ops. Its vocabulary (`internal/triage/schema.go`) is mapped by `triage.Plan`:

| model op | fields | becomes |
|---|---|---|
| `note.create` | `kind?`, `title`, `markdown`, `topics[]` | `note.create` with origins = the capture |
| `note.append` | `note_id`, `markdown`, `topics[]?` | `note.revise` (append, sources = capture) plus `note.update` for new topics |
| `task.create` | `text`, `state?`, `due?`, `effort_minutes?`, `importance?`, `waiting_on?`, `topics[]` | `task.create` |
| `task.complete` | `task_id` | `task.update` state `done` |
| `task.mention` | `task_id` | `task.update` with `mention` |
| `link` | `note_id` or `task_id`, `topics[]` | `note.update` / `task.update` adding topics, nothing else; used to bring earlier notes and tasks into a topic the model creates |

Existing topics attached by the model (in `topics[]` of any op) are kept only when the capture text or the object names the topic or one of its aliases, or shares a significant word with it (prefix match for compounds: "Heizung" evidences "Heizungsdaten"); otherwise the topic is dropped from that op and the rest of the plan stands. Topics created in the same plan are always kept. Small models otherwise attach unrelated topics on a whim.
| `task.update` | `task_id`, `text?`, `state?`, `due?`, `effort_minutes?`, `importance?`, `waiting_on?`, `topics[]` | `task.update` |
| `topic.create` | `name`, `kind?`, `aliases[]` | `topic.create` (skipped when the name already exists) |

`topics[]` entries may be existing topic ids or new names; unknown names create topics. The triage result also carries `classification` (`note`, `idea`, `task`, `question`, `info`, `correction`, `research`, `unclear`, `discard`), `confidence` (0–1), `summary` and `question` (required when `unclear`).

## What models may not do

Enforced in the core by actor prefix (`llm:`), independent of the prompt or provider:

- reword tasks, clear or move a due date the user set, change importance/effort once set, reopen completed tasks, unlink topics;
- rename notes or topics, change their kind, replace aliases, remove topic links;
- delete, pin or unpin blocks, or replace blocks written by the user (blocks without provenance);
- archive or unarchive anything; restore or remove objects (undo-only ops);
- undo transactions other than its own in the current conversation; force an undo.

Nobody can undo a `capture.create`: captures are the raw log. Dismiss them instead.

## Settings and setup

The daemon starts without any configuration. `GET /v1/health` reports `setup_needed: true` until the triage provider is usable (a key is stored, or the endpoint is local, or the heuristic is selected). Meanwhile captures are accepted and wait as `pending`.

| Route | Purpose |
|---|---|
| `GET /v1/settings` | Current settings: `path`, `listen`, `timezone`, `token_set`, `setup_needed`, `triage{provider,model}`, `chat{provider,model}`, `dictation{provider,model}` (empty model = off), `autonomy{…}`, `providers{name: {type, base_url, api_key_env, key_status: set|env|unset|none, key_hint, local, oauth, usable, transcription: audio|chat|none}}`. Keys are never returned. |
| `PUT /v1/settings` | Partial update: `{triage?, chat?, dictation?, timezone?, token?, autonomy?{min_confidence,auto_create,max_ops_per_capture,max_new_topics_per_capture}, providers?{name: {api_key?, base_url?, type?}}}`. Rules: an empty `api_key` clears the stored key (an environment key applies again); changing a provider's `base_url` host requires `api_key` in the same request and drops `api_key_env` (a provider that has no key at all, such as Ollama on another machine, may move freely); dictation follows the filing provider when that changes unless `dictation` is sent too, and its model is reset; keys are refused over plain `http://` to remote hosts; changing a role's provider resets its model, so send both; the token cannot be emptied while `listen` is a network address. Errors: 400 `invalid`, 500 `internal` (could not save). Applies atomically: providers are rebuilt first, the file is written (0600, fsync, atomic rename), then triage, chat and the time zone switch; the worker is kicked. `listen` and `data_dir` need a restart. Values set by flags or environment for this process (`FUNDUS_LISTEN`, `--data`, `--fake`, `FUNDUS_TOKEN`) are never written to the file. |
| `POST /v1/settings/test` | `{provider, model?, api_key?, base_url?}` → capability probe (`reachable`, `structured`, `tools`, `german`, `latency`, `errors[]`, `mode`) using the given key without saving it; always 200, look at `reachable`. Without `model`, the provider's model list is consulted (400 when none can be discovered). A different `base_url` requires `api_key` in the same request. |
| `GET /v1/setup/models?provider=x` | `{models: [ids…], suggested: {triage, chat, transcribe}, error?}` from the provider's `/models` endpoint with the stored key (`transcribe` is empty for providers that cannot hear). Unreachable or rejected endpoints come back as 200 with `error`. |
| `POST /v1/setup/models` | `{provider, api_key?, base_url?}` lists with an unsaved key (never put keys in URLs). A `base_url` that differs from the stored one requires `api_key` in the same request: stored keys never follow a new endpoint. |
| `POST /v1/transcribe` | `multipart/form-data` with field `audio` (WAV recommended, ≤ 25 MB; also mp3/webm/ogg/m4a) and optional `language` (BCP-47) → `{text, model}`. Uses the dictation provider: OpenAI through `/audio/transcriptions`, Gemini and OpenRouter through a chat completion with an `input_audio` part; topic names are passed as spelling hints. Nothing is captured: the client shows the text for review. Errors: 503 `dictation_unavailable`, 400 `bad_audio`, 502 `provider_error`. |
| `POST /v1/setup/oauth/start` | `{provider}` → `{url}`. Only providers with a connect flow (currently OpenRouter, PKCE). Open the URL in a browser; the daemon receives `GET /setup/oauth/callback?state=…&code=…`, exchanges the code for a key, stores it and shows a confirmation page. |

`fundus` with no arguments starts the daemon (or finds the running one, checking that its `instance` id in `/v1/health` matches `<data_dir>/instance`) and opens the UI in the browser (`fundus ui` opens it again later); without a terminal (desktop app, systemd) the daemon also logs to `$XDG_STATE_HOME/fundus/fundus.log` or the path given with `--log-file`/`FUNDUS_LOG_FILE`; `FUNDUS_OPEN=0` suppresses the browser, `FUNDUS_CONFIG`, `FUNDUS_DATA_DIR` and `FUNDUS_LISTEN` override paths and address.


Capture results carry `reason` (`unclear` | `low_confidence` | `proposal` | `discard` | `undone`) so clients can phrase why a capture was parked; `question` is set only for `unclear`.
