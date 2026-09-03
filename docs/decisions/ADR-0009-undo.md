# ADR-0009: Transaction-level undo with before-images

**Status:** accepted (implemented in `core.Undo`)

## Context

A capture produces several operations at once. Undo must revert all of them, must not corrupt objects that changed later, and must not cause the system to redo the very processing the user rejected.

## Decision

- Every transaction stores the full JSON before-image of every object it touched (`null` for created objects) and the list of touched IDs.
- Undo builds a compensating transaction: `object.restore` with the before-image, or `object.remove` for objects the transaction created. Revisions only ever increase; a restored object gets `current rev + 1`.
- If any touched object has a revision other than the one the transaction left behind, undo fails with `409 undo_conflict` listing the objects. `force: true` restores anyway; the later changes remain undoable through their own transactions.
- A transaction can be undone once; undoing an undo is allowed (it is a new transaction).
- Undo never re-triggers automatic processing: a capture whose before-image was `pending` or `processing` is set to `needs_review` with a note instead of being restored, so the worker does not pick it up again.

## Consequences

Undo is exact and explainable. Before-images make transactions larger; acceptable at personal scale.
