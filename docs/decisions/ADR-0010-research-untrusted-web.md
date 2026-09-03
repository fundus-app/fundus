# ADR-0010: Web content is untrusted; the reader has no write tools

**Status:** accepted (research not yet implemented)

## Context

Research means feeding web pages to a model. Pages can contain instructions aimed at the model (prompt injection). A single loop that both reads pages and writes to the knowledge base would let a page write into the user's notes.

## Decision

- The research worker that fetches and summarises pages runs without any write tool. It returns findings marked as external, each with URL, retrieval time and location.
- Only the curator loop may store findings, as `source` objects and as `callout` blocks of kind `external` in notes. External claims never merge silently into existing statements.
- Fetching starts with plain HTTP plus readability extraction; a headless browser is a later, separately sandboxed process under the extension model, never in the daemon binary.
- Authenticated sessions, forms, purchases and general web automation are out of scope.

## Consequences

Injection can at worst mislead the summary, never the store. The static binary stays browser-free.
