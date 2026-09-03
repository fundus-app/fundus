# ADR-0012: In-memory BM25 search rebuilt from state; embeddings later

**Status:** accepted (implemented in `internal/index`)

## Context

The MVP must find captures without an embedding model or an external index service.

## Decision

- An in-memory inverted index with BM25 ranking (title and aliases weighted ×3) is rebuilt from the object state at start and updated on every commit. No persistence.
- Tokenisation lowercases, folds diacritics and ß, drops short tokens and a small German/English stopword list, and strips one common suffix from longer tokens; the same function runs over documents and queries.
- Topic names and aliases are matched as phrases against capture text to seed triage context.
- Embeddings arrive later as a fully derived, deletable index; Bleve or an external service is not planned.

## Consequences

Search quality is lexical; the chat model compensates with query variation. Rebuild cost is proportional to state size, fine for personal scale.
