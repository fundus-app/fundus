# ADR-0004: Documents are a Markdown subset mapped to blocks with stable IDs

**Status:** accepted (implemented in `internal/doc`)

## Context

Notes must be editable by the model, by the user in an Inspector, and exportable. A rich block editor in Flutter is a project of its own. Full-text rewrites by a model silently drop paragraphs.

## Decision

- Long content is a flat list of blocks: `heading`, `paragraph`, `list`, `quote`, `code`, `callout`, `task_ref`, `source_ref`. Inline formatting stays a Markdown-subset string.
- Every block has an ID. The core installs a deterministic ID generator per (transaction, op) so replaying the log reproduces identical documents.
- Every block records `sources` (capture or source IDs) and may be `pinned`; pinned blocks reject model edits.
- The block model is a strict Markdown subset with a lossless round trip (`ParseMarkdown`, `Document.Markdown`). The Inspector edits Markdown text; the core re-parses.
- Model edits are block-level operations (`append`, `prepend`, `insert_after`, `replace`, `delete`, `pin`, `unpin`) applied atomically.

## Consequences

Clients need only a small renderer. Provenance is block-granular, not sentence-granular. Nested lists survive as text lines inside an item rather than as a tree.
