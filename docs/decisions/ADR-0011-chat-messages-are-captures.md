# ADR-0011: Chat messages are captures; conversations are objects

**Status:** accepted (implemented in `internal/chat`)

## Context

Thoughts get expressed in the chat as often as in the capture field. Two entry paths with different guarantees would violate "capture first".

## Decision

- Every user message in a conversation is stored as a capture with `source = "chat"` and `status = "processed"` before the model is called. The conversation, not triage, handles it.
- Conversations are objects (`conv_…`) with an append-only message list; assistant messages record the transaction IDs they caused and the object IDs they cite.
- Changes the model files from a conversation carry the user message's capture as provenance.

## Consequences

Chat history is durable, exportable and auditable like everything else. Undo of chat-caused changes works through the normal receipts.

## Amendment (2026-09-03)

Messages are separate `message` objects referencing their conversation, not an append-only list inside the conversation object. Reason: with full before-images per transaction, an embedded list made every turn re-store the whole history (quadratic log and memory growth). The API shape for clients is unchanged: `GET /v1/conversations/{id}` returns the conversation with its `messages` assembled in order.
