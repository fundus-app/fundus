# ADR-0005: Autonomy along the information-preserving axis

**Status:** accepted (implemented in `config.Policy`, `triage`, `chat`)

## Context

The 0.2 draft listed four permission levels but left open which changes need confirmation.

## Decision

The question is whether an action can lose or overwrite information.

- Additive actions (create notes, ideas, tasks, topics; append blocks; link topics; register mentions; complete tasks) run automatically with a receipt and undo.
- Reducing or overwriting actions (delete, archive, overwrite stated facts, move a user-set due date, edit pinned blocks) are the user's: through the Inspector, `POST /v1/commands` or the CLI. The model has no tool for them.
- `autonomy.min_confidence` (default 0.6): below it a capture is parked in the inbox with the model's question or summary and nothing is written.
- `autonomy.auto_create` (default true): when false every capture becomes a proposal in the inbox.
- `autonomy.max_ops_per_capture` (default 12) caps the blast radius of one capture.
- Corrections are appended, never rewritten over old statements.

## Consequences

A stronger model gets no more rights. The inbox is the only place where the model waits for the user.

## Enforcement (added 2026-09-03)

The additive-only rule is enforced in `internal/core/apply.go` by actor prefix, not only by the vocabulary offered to the model: transactions whose actor starts with `llm:` cannot reword tasks, clear or move user-set due dates, change importance or effort once set, reopen completed tasks, unlink topics, rename notes or topics, delete or pin blocks, replace blocks that carry no provenance (user-written), archive, restore or remove. Chat may undo only its own transactions in the current conversation and never with force. Captures can never be undone, only dismissed. `POST /v1/commands` carries a `user:` actor and keeps every power.

## Proposals (2026-09-03)

Parking is no longer a dead end: a parked capture keeps the model's proposed operations, and `POST /v1/captures/{id}/accept` applies them (optionally edited) with the user as actor and without another model call. With `auto_create = false` every capture and every chat write becomes such a proposal. Explicit instructions in chat write directly when `auto_create` is true; there is no confidence gate in chat because the user's instruction is explicit.
