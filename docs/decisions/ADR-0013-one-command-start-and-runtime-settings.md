# ADR-0013: One-command start and runtime settings

**Status:** accepted (2026-09-03)

## Context

The first draft required `config init`, an environment variable with an API key, `serve`, and then a client. The user's requirement is one command with no prior configuration, and a UI that can connect a model by itself, including sign-in flows where a provider offers them.

## Decision

- `fundus` with no arguments starts the daemon (or detects the running one) and opens the browser on the embedded UI. Everything else stays available as subcommands for scripts.
- The daemon starts without any configuration. A role whose provider is not usable (no key, no local endpoint, not the heuristic) simply has no provider: the worker waits, captures are stored and shown as pending, and `/v1/health` reports `setup_needed`.
- Configuration is owned by the daemon and edited through `GET/PUT /v1/settings`. The daemon writes `~/.config/fundus/config.toml` (mode 0600), rebuilds providers and reconfigures triage and chat without a restart. Keys are stored in that file and never returned by the API (status and last four characters only).
- Model discovery uses the provider's `/models` endpoint; a probe (`POST /v1/settings/test`) verifies reachability, structured output, tool calls and a German classification before anything is saved.
- OAuth is offered only where the provider documents a flow for third-party applications. Today that is OpenRouter (PKCE). OpenAI's "Sign in with ChatGPT" and Anthropic's Claude sign-in are reserved for their own products; Fundus does not impersonate those clients and asks for an API key instead. The OAuth code is generic so further providers can be added when they publish a flow.

## Consequences

- First run: `./fundus`, browser opens, wizard: provider → key or connect → test → models → done. Captures made before that are filed afterwards.
- Environment variables (`OPENAI_API_KEY` …) still work and show as `key_status: env`.
- The desktop app starts the daemon itself when none is running.

## Consequences added after review (2026-09-03)

- Not editable at runtime: `listen` and `data_dir` (restart). Editable at runtime: providers, keys, models, autonomy, time zone, token (a token cannot be removed while listening on a network address).
- Values that a flag or environment variable sets for one process (`--listen`, `--data`, `--fake`, `FUNDUS_*`) are never written to the config file; the file keeps its own values.
- A stored key never follows a changed `base_url`: changing the endpoint of a provider requires the key for that endpoint in the same request, and keys are refused over plain http to remote hosts. This keeps "keys are never returned" true even for local API clients.
- Runs without a terminal log to `$XDG_STATE_HOME/fundus/fundus.log` in addition to stderr, so a failed desktop start is diagnosable.
