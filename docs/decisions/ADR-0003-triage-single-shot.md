# ADR-0003: Triage is single-shot structured output; chat is a bounded tool loop

**Status:** accepted (implemented in `internal/triage`, `internal/chat`)

## Context

Filing a capture needs context (existing topics, related notes, open tasks) and a small set of possible actions. Letting the model search and write through a tool loop makes every capture slow, expensive and dependent on tool-calling quality, which varies a lot between providers.

## Decision

- Triage retrieves context before the model call: topics mentioned by name, all topics (capped), and the top lexical matches among notes and open tasks. The model answers with one JSON document (`classification`, `confidence`, `summary`, `question`, `operations`).
- The runtime validates the document structurally, feeds one validation error back for a single corrective turn, and otherwise fails the capture without writing. It maps the model's operation vocabulary (`note.create`, `note.append`, `task.create`, `task.complete`, `task.mention`, `task.update`, `topic.create`) onto core ops, resolves topic names, injects `expected_rev` from the objects it showed, and commits all ops plus the capture status change as one transaction.
- Conversation uses a tool loop with at most ten steps and five tools: `search`, `get`, `list`, `apply_operations` (same vocabulary and the same `triage.Plan` mapping) and `undo`. The model never receives core ops directly.

## Consequences

Triage works on any endpoint that can emit JSON. Tool-calling quality only affects chat. Both paths share validation and provenance handling.
