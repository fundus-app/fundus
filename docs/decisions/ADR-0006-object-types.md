# ADR-0006: Six object types; topics are hubs; persons and projects are topic kinds

**Status:** accepted (implemented in `internal/model`)

## Context

The 0.2 draft named note, idea, task, topic, person, project and source. More types mean more classification errors and more tools.

## Decision

- Types: `capture`, `note` (kind `note` | `idea`), `task`, `topic` (kind `topic` | `person` | `project`), `source`, `conversation`.
- Topics are explicit objects from the start because links need a stable target and pinned summary blocks need a home. Names and aliases are unique after normalisation.
- Notes and tasks link to topics; backlinks are derived. Notes may relate to notes; tasks may link to notes.
- Projects are topics with `kind = "project"`; no separate lifecycle until one is needed.
- Every ID is prefixed (`cap_`, `note_`, `task_`, `topic_`, `src_`, `conv_`, `msg_`, `txn_`, `b_`) followed by a ULID.

## Consequences

Fewer prompts, fewer tools, simpler views. A project view is a topic page.
