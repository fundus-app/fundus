# ADR-0002: One OpenAI-compatible adapter first, capability probe

**Status:** accepted (implemented in `internal/llm`)

## Context

The user wants OpenAI, OpenRouter, Anthropic and Ollama from day one. All four expose a chat-completions endpoint in the OpenAI shape; they differ in how structured output and tool calls are supported.

## Decision

- `internal/llm.OpenAI` is the only real provider type (`type = "openai"` in config). It takes a base URL, an API key from an environment variable, optional headers and a structured-output mode.
- Structured output is requested as `response_format: json_schema`. In `auto` mode a 400 that mentions the schema downgrades the provider to `json_object` and then to prompt-only for the rest of the process. Model output is always validated by the runtime regardless of mode.
- `llm.Probe` checks reachability, schema-valid JSON on a German sentence, and tool calling; `fundus probe` and `GET /v1/llm/probe` expose it. A model that fails the structured probe must not be used for triage.
- A native Anthropic adapter (structured outputs, prompt caching, thinking) is planned as a second provider type once the compatibility layer proves limiting.
- A model-free heuristic provider (`type = "fake"`, `fundus serve --fake`) files captures with rules so the system runs without any key.

## Consequences

One code path to test. Provider differences surface as probe results, not as silent failures. Temperature and reasoning effort are passed through only when configured, because newer model families reject some parameters.
