# Fundus: Concept 0.3

**Status:** concept 0.3.7, backend and client implemented (see [Status](#status))
**Category:** personal, self-hosted knowledge and task system with an LLM as the primary operating and organising layer
**License:** AGPL-3.0 ([ADR-0008](decisions/ADR-0008-license.md))

## 1. Summary

Fundus is not a note-taking app with a chat bolted on, and it is not a general-purpose agent. The product starts at the LLM: the user enters thoughts, ideas, facts, questions and intentions as freely as possible, by text or voice. The system understands the input, asks only when the ambiguity actually matters, and maintains a durable knowledge and task base on its own.

Notes, topics, idea lists, tasks and priorities are visible, editable views onto that base. Underneath the model sits a deterministic core with stable IDs, transactions, revisions, provenance and undo. The model may interpret and organise, but it can never silently invent facts, overwrite raw input or make a task disappear.

Fundus is open source, self-hosted and model-independent. One Go daemon owns the data and exposes a versioned JSON API. Native Flutter clients for desktop and mobile, an embedded Flutter web build, and a CLI are interchangeable surfaces on the same API. Local and remote models are supported through interchangeable providers. The first release does exactly five things:

1. capture a thought immediately,
2. find information again reliably,
3. structure knowledge automatically,
4. recognise and manage tasks,
5. research on request and store results with sources.

## 2. Problem

Existing products fall into four groups:

- **Classic note systems** (Obsidian, QOwnNotes, SilverBullet) expect the user to maintain files, folders, tags, links and structure. LLM features are add-ons.
- **Task managers** (Todoist, GTD tooling) demand prioritisation, scheduling, reviews and upkeep. Maintaining the system becomes a task of its own.
- **AI note-takers** (Pocket and similar) focus on recording, transcription and summarising. They produce content but keep no durable, freely usable personal knowledge model.
- **General agents** (OpenClaw, Hermes and similar) also want to drive the browser, shell, messaging, automation and multi-agent orchestration. The strong use case gets diluted and the trust surface becomes needlessly large.

The gap is a tightly focused system that maintains personal information and tasks for the user without imposing an organising method.

## 3. Thesis

People want to hold on to thoughts and use them later. They usually do not want to design a taxonomy, maintain metadata or file every entry into a productivity system.

> The user supplies thoughts and intentions. The LLM does the ongoing editorial work. A deterministic core guarantees that nothing gets lost or distorted unnoticed.

The LLM is neither the database nor the memory. It is the personal editor and operator of a transparent, auditable system.

## 4. Principles

**4.1 Capture first.** Recording a thought must cost no more than opening a scratchpad. Every input is stored unchanged with a timestamp before anything else happens. Processing may fail without the original being lost.

**4.2 LLM-first, not AI-assisted.** The primary interaction is natural language. No forms, no folder pickers, no task property panels. From "At some point I should look into whether the heating data could be visualised sensibly with Grafana. Maybe as a small project." the system may derive an idea under a home-automation topic, a loose task without an invented due date, a link to existing Grafana notes, and optionally a research suggestion.

**4.3 Determinism below the model.** Every change goes through a typed operation (`note.create`, `note.revise`, `task.create`, `task.update`, `topic.create`, …). The model has no write access to files or tables. The core validates, writes atomically and returns a receipt that describes what was actually written. Every change is traceable and reversible.

**4.4 Raw data is preserved.** Original input, recordings, transcripts and imported sources are never replaced by a summary. Derived content points back to its origins down to the block level.

**4.5 Organisation without an obligation to organise.** Folders, tags, projects, priorities and due dates are optional. The system may suggest them or use them internally for views, but never demands them. Unimportant tasks may simply exist for years without being re-prioritised.

**4.6 Open and interchangeable.** Free choice of chat, reasoning, embedding, speech-to-text and text-to-speech models; local or hosted; an OpenAI-compatible API as the lowest common denominator; JSON as the documented canonical format; Markdown and JSON export and import; no cloud account required.

**4.7 Narrow responsibility.** Fundus manages knowledge, ideas and tasks. Every additional capability must improve the core loop. Search and research tools belong to the core as far as they find, check and store information with sources. General website operation, purchases, messaging, shell access and arbitrary automation do not.

## 5. Core flow

**5.1 Capture.** Inputs arrive through equivalent channels: the desktop app, a global-shortcut capture window, the CLI (`fundus capture`), the HTTP API, the mobile app (share target, widget, push-to-talk; later), and the conversation view. Commands such as "remember this", "remind me" or "look this up" raise clarity but are never required.

Capture is two-phase. The API stores the input, fsyncs it and answers immediately with an ID. The LLM step runs as a background job; its receipt arrives later over the event stream. This is what keeps capture under five seconds regardless of model latency.

**5.2 Understand.** A fast triage step classifies the capture as note, idea, task/intention, question, new information on an existing topic, correction, research request, or unclear. It runs as a single structured-output call: the runtime retrieves candidate topics, notes and open tasks first, hands them to the model together with the capture, and receives one JSON document with a classification, a confidence, a summary and a list of proposed operations ([ADR-0003](decisions/ADR-0003-triage-single-shot.md)).

**5.3 File.** The runtime validates the document, resolves topic names to IDs (creating topics only for recurring subjects; when a topic is created, the notes and open tasks the model was shown that name it are attached to it, so a subject that only becomes a topic on its third capture still gathers the first two), maps the model's vocabulary onto core operations, and commits everything as one transaction. Depending on the result it may append to an existing note, create a note or idea, create or complete a task, register another mention of an open task, or link topics.

**5.4 Confirm.** After every transaction the user receives a short receipt, for example:

> Created idea "LLM-first knowledge appliance" in Note systems and Hardware. Created task "Sketch the MVP data model".

The receipt is rendered by the core from the committed operations, not from a claim the model makes. Every receipt carries a transaction ID that can be undone.

**5.5 Reuse.** Later, the user asks naturally: "What were my thoughts on building my own note system?", "Which ideas have I not followed up in the last months?", "What could I sensibly get done in an hour today?". Answers cite stored objects by ID and point to original captures. Uncertainty and contradictions are shown, not resolved silently.

## 6. User interface

The reference client is a Flutter application without HTML or WebView content rendering. The same code runs on Linux, Windows, macOS, Android and iOS, and as a web build that the Go binary embeds and serves. It has four areas.

**6.1 Capture.** A minimal input for text, voice, files and links, reachable from every view. On the desktop a small capture window bound to a global shortcut (for example as a Niri scratchpad); on mobile the share target, widget and push-to-talk.

**6.2 Conversation.** The primary working surface for questions, changes and research. Tool actions appear as compact, verifiable receipts. The model works in a bounded tool loop with read tools (search, get, list) and one write tool that goes through the same validated operation vocabulary as triage. Every user message in a conversation is also a capture ([ADR-0011](decisions/ADR-0011-chat-messages-are-captures.md)).

**6.3 Views.** Maintained by the system, fully transparent, all derived from the same objects: Inbox (unresolved captures), Open, Relevant (attention-ranked), Ideas, Notes, Topics, Waiting, Later, Research (planned) and Changes (the audit log with undo).

**6.4 Inspector.** A conventional detail view for reading and manually editing notes, tasks, topics, sources and links. It is the emergency exit from the LLM abstraction. Note bodies are edited as Markdown text; the core parses the text back into blocks, which keeps the Inspector a plain editor rather than a block editor ([ADR-0004](decisions/ADR-0004-document-model.md)).

**6.5 Native content rendering.** Content travels as a small typed block structure (`heading`, `paragraph`, `list`, `quote`, `code`, `callout`, `task_ref`, `source_ref`) with a Markdown-subset string for inline formatting. Every block has a stable ID. Clients render blocks natively; Markdown is imported and exported losslessly.

## 7. Task model without GTD pressure

A task needs only a stable ID, text, state, creation time and origin. Optional fields are due date, effort, importance, topics, related notes and what it is waiting on. States are `open`, `waiting`, `later` and `done`; archiving is a separate flag.

Instead of a stored priority, the Relevant view computes an attention score from evidence that exists: explicit importance, due date proximity, repeated mentions, recent capture, small effort ("quick win") and activity on linked topics. Every contribution is returned as a readable reason. The score is a reasoned ordering, never a stored fact.

## 8. Knowledge model

Three layers ([ADR-0006](decisions/ADR-0006-object-types.md)):

**8.1 Captures.** Unchanged input, append-only, with source, status and the result of processing. The evidence and recovery layer.

**8.2 Objects.** `note` (kind `note` or `idea`), `task`, `topic` (kind `topic`, `person` or `project`), `source` and `conversation`. Each carries ID, type, revision, timestamps, links and provenance. Long content is a block document; each block records the captures or sources it was derived from and can be pinned so the model leaves it alone.

**8.3 Views.** Derived presentations: topic pages, ranked task lists, search results, receipts. Views are never stored as separate data.

## 9. Research

Research is an optional core function, not a browser agent. A research task has a concrete question, defined sources, stored findings, a model-written result with uncertainties and contradictions, and a timestamp with the model used. Results are not mixed into existing notes: external claims stay recognisable as external and keep URL, retrieval time and location.

The reader that processes web content is an isolated worker without write tools. It returns marked findings; only the curator loop may store them ([ADR-0010](decisions/ADR-0010-research-untrusted-web.md)). Plain fetching plus readability extraction comes first; a headless browser is a later, separately sandboxed tool.

## 10. Trust and autonomy

The deciding axis is whether an action preserves information ([ADR-0005](decisions/ADR-0005-autonomy-policy.md)):

- **Read** (search, compare, answer): automatic.
- **Derive** (views, summaries, links, scores): automatic and logged.
- **Create and append** (notes, ideas, tasks, topics, links, mentions): automatic by default with an immediate receipt and undo. Below a configurable confidence the capture is parked in the inbox with the model's question. Automatic creation can be switched off entirely, in which case every capture becomes a proposal.
- **Reduce or overwrite** (delete, archive, overwrite stated facts, move a user-set due date, edit pinned content): the user does it through the Inspector or CLI. The model has no tool for it.

These rules do not depend on the model. A stronger model gets no more rights.

## 11. Architecture

**11.1 Core daemon.** One static Go binary (`fundus`) runs the daemon and the CLI. It is the only writing process: HTTP API with bearer-token auth, transaction log, object state, in-memory search index, background job for triage, provider abstraction, audit and undo. Default deployment is the user's desktop on loopback; a Docker/Podman image with the embedded web UI serves the home-server case.

**11.2 Storage** ([ADR-0001](decisions/ADR-0001-event-sourced-storage.md)). Append-only JSONL segments form the canonical event log; every record is hash-chained to its predecessor and fsynced before the in-memory state changes. A JSON snapshot speeds up start and is always rebuilt from the log when it does not match. A damaged tail is copied aside and cut; damage anywhere else stops the daemon. There is no database and none is planned for the personal system.

**11.3 API-first** ([api.md](api.md)). Commands mutate and return receipts with an undo reference; queries read; events stream over SSE. Commands that modify existing objects carry an expected revision; the LLM runtime fills it in from the objects it showed the model. MCP would be an optional adapter, never the core.

**11.4 LLM runtime** ([ADR-0002](decisions/ADR-0002-providers.md)). Roles (triage, chat; later embedding, speech) each bind to a provider and model. One adapter speaks to every OpenAI-compatible endpoint: OpenAI, OpenRouter, Anthropic's compatibility layer, Ollama and similar. It requests JSON-schema output and degrades to JSON mode or prompt-only when an endpoint rejects it. A capability probe checks reachability, structured output on a German sentence and tool calling before a model is trusted. A model-free heuristic provider makes the system usable without any key.

**11.5 Tool runtime.** Models only see typed tools with explicit rights. Triage sees no tools at all. Chat sees `search`, `get`, `list`, `apply_operations` and `undo`. Research tools follow the extension model.

**11.6 Clients** ([ADR-0007](decisions/ADR-0007-clients.md)). Flutter desktop as the reference client; the same code built for the web and embedded in the daemon; CLI as the developer and scripting tool; mobile later. Clients keep no canonical data; offline mode and sync are separate later features.

**11.7 Extensions.** Unix philosophy rather than in-process plugins: a tool is a separate process or HTTP service with a manifest, typed JSON in and out, and explicit capabilities. The core decides about rights, timeouts, network access and whether to accept results.

## 12. Adopted ideas

From **Mem**: push-to-remember capture, visible rather than hidden long-term memory, a continuously maintained picture of topics and tasks, proactive hints only when likely useful. From **Saner.AI**: brain dumps decomposed into knowledge and actions, tasks detected in any note, sorting as a service of the system. From **SilverBullet and zk**: durable open exports, wiki links as the model for typed relations and backlinks, programmatic views, no platform dependency, and isolated extensions without core access. From **Pocket and voice note-takers**: always-available recording, original plus transcript retained, voice as an equal channel. From **OpenClaw, Hermes and Open Walnut**: natural language as control, persistent sessions, explicit tools for tasks and memory, visible tool execution.

Deliberately not adopted: universal agent, shell access, coding orchestration, agent swarms, general browser automation, arbitrary plugin execution in the core.

## 13. MVP

**Included:** the Go daemon as one static binary; JSONL event log and snapshots; versioned JSON API with SSE; Flutter client for Linux and the embedded web build; text capture via CLI, API and desktop shortcut window; conversation view; notes, ideas, tasks, topics with stable IDs; Inbox, Open, Relevant, Ideas, Notes, Topics, Waiting, Later and Changes; in-memory search; OpenAI-compatible providers and the heuristic fallback; typed operations; receipts, audit log and undo; JSON and Markdown export.

**Deferred:** Android and other mobile targets, a native Anthropic adapter, controlled refresh of stale research.

**Not included:** calendar sync, e-mail and messaging, team features, collaborative editing, a separate web frontend codebase, full offline mode and bidirectional sync, kanban and gantt, habit tracking, general automation, shell access or browser control, autonomous actions outside the knowledge and task system, hardware.

## 14. Example interactions

**Quick idea.** User: "A small LLM remote for the office would actually be useful, maybe with an e-ink display, but hardware comes later." Receipt: Created idea "LLM remote for the office". Linked to Knowledge appliance. No task created.

**Implicit task.** User: "I still need to check why the second string on the Deye inverter sometimes delivers no power." Receipt: Created task "Check why the Deye's second string sometimes delivers no power". No due date. Linked to Solar.

**Knowledge question.** User: "Why did we drop SilverBullet again?" Answer (in conversation, citing [[note_…]] IDs): Not fully rejected. Markdown, programmable views and the lean server were positives; the browser requirement, TypeScript/Lua complexity and several architecture changes spoke against it. Sources: four notes and two saved research results.

**Prioritising without upkeep.** User: "I have an hour and do not want to start anything big." Answer: Three matching open items. Best fit: "Sketch the MVP data model": no due date, estimated 30–45 minutes, mentioned twice recently.

## 15. Risks

**Wrong filing.** The model turns a remark into a task or creates duplicates. Countermeasures: original capture kept, transaction-level undo, receipts, mentions instead of duplicates, the confidence threshold, `auto_create` switch.

**Silent distortion.** Summaries change the meaning of earlier statements. Countermeasures: block-level provenance, pinned blocks, append-over-rewrite for corrections, separation of original, derived and external content.

**Model change.** Another model handles structured output or tools worse. Countermeasures: capability probe, strict schema validation with one corrective round trip and no write on failure, automatic downgrade of the structured-output mode, the heuristic fallback.

**Feature creep.** The project drifts towards a general agent or project manager. Countermeasure: every feature must directly improve capture, recall, curation, tasks or research; everything else lives outside the core.

**Trust and habit.** The user only keeps using the system if entries neither vanish nor change uncontrollably. Countermeasures: local raw data, transparent receipts, direct manual control, an audit log, backups by copying one directory.

## 16. Phases

**Phase 0 (done): technical spike.** Capture → structured operation → persisted object → receipt; JSONL log, snapshots and undo; typed document blocks; provider switch; tests with deliberately bad model output.

**Phase 1 (in progress): personally usable core.** Inbox, notes, ideas, tasks, topics; search and relations; CLI; Flutter client for Linux and web; desktop capture window; Markdown and JSON export; one user, one daemon.

**Phase 2: curation.** Automatic topic summaries, duplicate detection and merging, background maintenance with a change overview, speech input.

**Phase 3: self-hosted multi-device.** Device tokens, further Flutter targets, TLS guidance, optional encrypted storage, local read cache.

**Phase 4: research and proactivity.** Research tasks with sources, controlled refresh of stale content, restrained hints, optional TTS.

## 17. Success criteria

A thought can be captured in under five seconds; at least 95% of captures can be found again without manual sorting; no confirmed input can be lost through a model or index error; every automatic change can be explained and undone; after several weeks the user spends less time on system upkeep than before; a provider switch needs no data migration; the product stays useful without semantic search and without a cloud model.

## 18. Positioning

*Capture anything. Let your AI maintain the rest. Keep every fact under your control.*

Not a "second brain" the user must tend; not a to-do system that enforces daily planning; not a chatbot with an unreliable memory; not a general agent with full computer access; not tied to one LLM or cloud.

## 19. Decisions taken

| Topic | Decision | ADR |
|---|---|---|
| Storage | JSONL event log with hash chain, snapshots, no database | [0001](decisions/ADR-0001-event-sourced-storage.md) |
| Providers | one OpenAI-compatible adapter first, native Anthropic later, probe | [0002](decisions/ADR-0002-providers.md) |
| Triage vs. chat | single-shot structured output vs. bounded tool loop | [0003](decisions/ADR-0003-triage-single-shot.md) |
| Documents | Markdown subset ↔ blocks with stable IDs and provenance | [0004](decisions/ADR-0004-document-model.md) |
| Autonomy | information-preserving axis, confidence threshold, `auto_create` | [0005](decisions/ADR-0005-autonomy-policy.md) |
| Objects | six types; topics as hubs; persons and projects are topic kinds | [0006](decisions/ADR-0006-object-types.md) |
| Clients | Flutter desktop + embedded web in one binary; CLI as dev tool | [0007](decisions/ADR-0007-clients.md) |
| License | AGPL-3.0 | [0008](decisions/ADR-0008-license.md) |
| Undo | transaction-level with before-images; never re-triggers processing | [0009](decisions/ADR-0009-undo.md) |
| Research | reader has no write tools; external claims stay marked | [0010](decisions/ADR-0010-research-untrusted-web.md) |
| Conversations | messages are captures; conversations are objects | [0011](decisions/ADR-0011-chat-messages-are-captures.md) |
| Search | in-memory BM25 rebuilt from state; embeddings fused in since 0.3.7 | [0012](decisions/ADR-0012-search.md), [0014](decisions/ADR-0014-maintenance-and-assist.md) |

Also decided: Empoche contributes no code (Vue 2/Electron/MongoDB, 2019); MongoDB is not planned; the repository is a monorepo (`cmd/`, `internal/`, `app/`, `docs/`); the UI is English-only for now with translations later.
| One-command start | `fundus` alone starts the daemon and opens the UI; settings are edited at runtime via the API | [0013](decisions/ADR-0013-one-command-start-and-runtime-settings.md) |

## 20. Status

Implemented: the Go daemon with event log, snapshots, hash-chain verification, tail recovery and a directory lock; all core operations with revision checks and core-enforced limits for model actors; stored receipts, audit log and transaction-level undo; captures with the two-phase flow, parked proposals with accept, automatic retry of transient failures; triage with schema validation, corrective retry, confidence parking, topic caps, same-title dedupe and the heuristic provider; conversation with tools, per-turn id scoping, idempotent turns and messages as objects; views including the attention score; in-memory search; JSON and Markdown export plus consistent backups; the HTTP API with SSE resume, browser protections and request limits; the CLI; the Flutter client for Linux desktop and web (embedded in the binary), packaged as a Linux AppImage with the daemon inside; macOS and Windows apps are built by CI. Planned: research, speech, mobile, embeddings, maintenance jobs.

## Changelog from 0.2

- Deployment fixed: desktop daemon on loopback first; single static binary embeds the Flutter web build; Docker image for the server case. Mobile moved behind desktop.
- Provider set fixed to OpenAI-compatible endpoints (OpenAI, OpenRouter, Anthropic compatibility layer, Ollama) with a capability probe and a heuristic fallback.
- "Open product decisions" replaced by "Decisions taken" with ADRs.
- Object types reduced to capture, note (note|idea), task, topic (topic|person|project), source, conversation. Projects are no longer a separate type.
- Triage defined as single-shot structured output; tool loop reserved for conversation and research.
- Capture explicitly two-phase (acknowledgement, then receipt).
- Block IDs, block-level provenance and pinned blocks specified; Inspector edits Markdown text.
- Undo specified as transaction-level with before-images and conflict handling; undo never re-triggers processing.
- Autonomy re-framed along the information-preserving axis with a confidence threshold and an `auto_create` switch.
- Web content declared untrusted; the research reader gets no write tools.
- MongoDB and Empoche code ruled out; search fixed to an in-memory index.
- License set to AGPL-3.0; repo public, English-only UI.

### 0.3.7 (2026-09-04, curation)

- Background maintenance ([ADR-0014](decisions/ADR-0014-maintenance-and-assist.md)): a daily run (or on demand) with integrity fixes, topics for untagged notes and tasks, duplicate detection with related links and merge proposals in the inbox, one automatic summary block per topic, and an optional assist level that drafts from the user's notes or starts research for open tasks. Run history under the data directory; every change a normal transaction with receipt and undo.
- Embeddings: vectors from any OpenAI-compatible `/embeddings` endpoint cached per model under the data directory; search, triage context, the conversation's search tool and duplicate detection fuse lexical and semantic ranks.
- Proposal captures gained `lines` and `core_proposal`, so the inbox can carry decisions that are not triage results.

### 0.3.6 (2026-09-04, research)

- Research implemented as designed in §9 and ADR-0010: a reader with `web_search` and `fetch_page` and no write tools; a hardened fetcher (no private destinations, bounded size and hops, readability reduction); Brave, SearXNG or the provider's own search as backends; the curator stores sources, one note with the answer in an `external` callout and `[n]` citations, and completes the task, all in one undoable transaction. Started by itself when triage files a research task (unless `research.manual`), from "Research this", or from the conversation (`research` tool); progress streams as `research.progress` events.

### 0.3.5 (2026-09-03, dictation, Gemini, current models)

- Dictation: microphone in the capture bar, `POST /v1/transcribe`, `[dictation]` role in the config; OpenAI via `/audio/transcriptions`, Gemini and OpenRouter via `input_audio` in a chat completion; topic names as spelling hints; the transcript is reviewed before capture.
- Google Gemini as a provider through its OpenAI-compatible endpoint; Ollama accepts any server address without a key.
- Delete for notes, tasks and topics in the client (`object.archive`, receipt "Deleted …", undo restores); a Done view of completed tasks ordered by completion time and a folded "Done" section on topic pages (`done_tasks`); topics can be unlinked from a note or task in place. The triage prompt links an existing topic only when the capture is clearly about it.
- Receipts and `object.changed` events name the topics whose members changed (`affected[]`, `members: true`), so an open topic page refreshes when earlier notes are linked into it; the client also refreshes the open detail on every committed transaction.
- Defaults moved to the current generation (OpenAI `gpt-5.6-luna` for filing, `gpt-5.6-terra` for conversation, Gemini `gemini-3.5-flash-lite` / `gemini-3.8-flash`); the wizard shows the provider's model list with the suggestions preselected, and version parsing keeps suggestions current when a new family appears.

### 0.3.4 (2026-09-03, topic catch-up and polish)

- Triage vocabulary gains `link` (attach a shown note or task to topics); when a plan creates a topic, the runtime also attaches the shown notes and open tasks that name it, and the objects created alongside it. Object ids in `topics` are refused instead of becoming topic names.
- The user message names the capture's language when common function words make it obvious, which stops small models from answering an English capture in the language of the surrounding context.
- Receipts read as one sentence: "Created task “X” in Fundus, due Fri 12 Sep." No "No due date."; pure topic links read "Linked note “X” to Fundus."
- Client: no object ids, revisions, actor strings or provider slugs in the primary interface; receipts render references inline; detail views carry one fact row instead of chips.
- Releases: every push to main refreshes the rolling `edge` pre-release; version tags with or without the `v` prefix publish a release. Linux ships as AppImage, Flatpak bundle and snap (Snap Store publishing gated on a store token), all from one tested bundle.

### 0.3.3 (2026-09-03, name)

- Product renamed from the working title Curator to Fundus: binary `fundus`, config under `~/.config/fundus`, data under `~/.local/share/fundus`, environment variables `FUNDUS_*`, client header `X-Fundus-Client`, Flutter app `fundus_app`.

### 0.3.2 (2026-09-03, one-command start)

- `fundus` with no arguments starts the daemon and opens the UI; the first run offers a setup wizard in the UI: provider, key or OAuth connect (OpenRouter), model test and model choice. Settings are changed at runtime through `PUT /v1/settings`; providers are rebuilt without a restart. Captures made before a model is connected wait in the inbox.

### 0.3.1 (2026-09-03, after the review round)

- Security: Host/Origin/Content-Type checks on every API request, forwarded requests need the token, dev CORS limited to localhost origins, no token-less non-loopback listen.
- Model powers are enforced in the core by actor: additive only, no rewording, clearing, renaming, deleting, archiving or forced undo; chat may undo only its own transactions of the current conversation; captures are never undone.
- Proposals as data with an accept endpoint; automatic retry of transient failures; the clarification round-trip keeps the question.
- Messages as objects; stored receipts; before-images out of memory; deterministic block ids per transaction; directory lock; snapshot problems fall back to a full replay.
- New ops `note.set_markdown`, `topic.set_summary`, `topic.merge`; `wait=` on capture; `objects?ids=`; `backup`; SSE resume via `changes?after=`.
- Time zone configuration; prompt hardening against injected instructions; topic sprawl cap; duplicate-title notes append instead of duplicating.
