# ADR-0014: Background maintenance is additive; everything else is a proposal

**Status:** accepted, implemented in 0.3.7 (`internal/maintenance`, `internal/embed`)

## Context

Phase 2 of the concept asks for curation that runs by itself: topics for untagged notes, duplicate detection and merging, automatic topic summaries, a change overview. The user also wants the model to help with open tasks when allowed. All of that must respect the trust model ([ADR-0005](ADR-0005-autonomy-policy.md)): the model adds, it never rewrites or removes what the user wrote, and every change has a receipt and an undo.

## Decision

- A scheduler in the daemon runs maintenance daily at a configured local time (or on an interval, or on demand). Each run is a report: per job what was checked, changed and proposed, kept as history under the data directory, never in the event log.
- Jobs only apply changes that preserve information: removing links to deleted topics (integrity), adding topic links (untagged), adding "related" links between likely duplicates, and writing or replacing the one automatic summary block a topic carries (marked "Automatic summary (date)", never a user-written or pinned block). All of them are ordinary transactions by `system:maintenance` or `llm:maintenance/<provider>/<model>` with the cause `maintenance/<run id>`.
- Anything that merges, archives or creates content on the user's behalf is a proposal: a capture from source `maintenance` in the inbox with a one-sentence question, the lines of what accepting does, and the core operations to apply. Accepting applies them as the user; dismissing drops them; a pair is not proposed again for 60 days.
- Topic links from maintenance pass the same lexical evidence check as triage links; a model's say-so alone never links.
- Assist has three levels. `off` (default). `propose`: drafts made from the user's own notes land in the inbox. `auto`: drafts become notes linked to the task and research starts by itself. The model is told to help only when the notes hold enough; "none" is the expected answer for most tasks.
- Embeddings are derived data: vectors from an OpenAI-compatible endpoint, cached per model under `<data_dir>/embeddings`, rebuilt from the state. Search fuses lexical and vector ranks; triage context, the conversation's search tool and duplicate detection use the same search. Without an embedding model everything stays lexical.

## Consequences

- A user who never looks at the inbox loses nothing: maintenance never merges or deletes by itself.
- Model cost is bounded per run: at most 40 untagged items, 10 summaries, 5 assisted tasks, and duplicate pairs in batches of ten.
- The run history answers "what did it do last night" without reading the event log.
