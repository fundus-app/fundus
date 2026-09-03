# Contributing

## Running it

```sh
make build            # bin/fundus
make test             # go test ./...
go test -race ./...   # store, core and index are concurrency-sensitive
cd app && flutter test
```

`bin/fundus serve --fake --data /tmp/fundus-dev --listen 127.0.0.1:7499 --log-level debug` runs a throwaway daemon without any API key. `--dev-cors` allows `flutter run -d chrome` against it.

## Layout

```
cmd/fundus/        daemon + CLI entry point
internal/model/     object types, ops, transactions, receipts
internal/doc/       block documents ↔ Markdown subset, block edits
internal/setup/     settings view and patch, model discovery, OAuth (PKCE)
internal/store/     event log segments, hash chain, snapshots
internal/core/      state, apply, undo, receipts, views, attention score, export
internal/ids/       prefixed ULIDs
internal/index/     in-memory BM25 search
internal/llm/       provider interface, OpenAI-compatible adapter, probe
internal/triage/    capture → structured result → validated ops; worker; heuristic provider
internal/chat/      conversation tool loop
internal/api/       HTTP routes, auth, SSE
internal/webui/     embedded Flutter web build
app/                Flutter client
docs/               concept, ADRs, API and data model
```

## The one rule

Nothing mutates state except a core op applied through `core.Commit`. Not the API, not the LLM runtime, not a test helper. If you need a new kind of change, add an op.

## Adding an op

1. Document its fields in the comment block of `internal/model/ops.go` (and add fields if needed; keep the struct flat).
2. Implement validation and mutation in `internal/core/apply.go`. Use `w.mutable` (records the before-image and checks `expected_rev`) or `w.create`. Never generate anything non-deterministic except through `op.ID` (written back into the op) or the block-ID generator.
3. Render its receipt line in `internal/core/receipt.go`.
4. Add a test in `internal/core` that commits it, replays the log into a fresh core and compares state; and one that undoes it.
5. Document it in `docs/api.md`. If the model should be able to use it, extend the vocabulary in `internal/triage/schema.go` and the mapping in `triage.Plan`.

## Adding a provider

Implement `llm.Provider` (`Name`, `Complete`) in `internal/llm`, register the config `type` in `llm.NewRegistry` and `config.Validate`, and make sure `llm.Probe` passes against a real endpoint. Model output must never be trusted: the triage and chat runtimes validate everything, so a provider only needs to return text and tool calls faithfully.

## Style

Standard `gofmt`, `go vet` clean, no new dependencies without a reason in the PR. Documentation is English. Prompts are English; user content stays in the user's language.

## Event log compatibility

Every op that was ever written must stay replayable: never remove an op kind from `internal/core/apply.go`, alias it instead (see `conversation.append` → `message.create`), and skip only tunable validation during replay (`workspace.replay`). `fundus verify --data DIR` replays a data directory from seq 1 in a scratch copy and compares the result with the snapshot; run it after changing `apply.go`.

## Test layers

| Layer | Command | What it proves |
|---|---|---|
| Unit and in-process integration | `go test -race ./...` | Event log, replay determinism, undo, actor limits, triage and chat pipelines with a scripted provider, the HTTP API with its browser protections. |
| CLI end to end | `go test ./cmd/fundus -run E2E` (part of `go test ./...`) | Builds the real binary, starts `fundus serve` on a free port with temporary directories, and drives it through the CLI and HTTP: capture, receipt, undo, export, backup, setup through the settings API, restart, `verify`. |
| Random walk | `go test ./internal/core -run RandomOps` | 300 random operations per seed, then snapshot vs full replay, a simulated crash on the last log line, and the search index after replay. |
| Fuzzing | `go test ./internal/doc -fuzz=FuzzParseMarkdownRoundTrip -fuzztime=30s` (also `FuzzSetMarkdown`, `internal/store FuzzParseRecord`, `internal/triage FuzzParseResult`) | Parsers that read foreign input never panic and keep their invariants. CI runs each target for a short time. |
| Live provider | `FUNDUS_LIVE_TESTS=1 OPENAI_API_KEY=… go test ./internal/triage ./internal/llm -run Live -v` | Real captures through a real model: task with resolved due date, a vague idea that must not become a task, "hm" parked or dismissed, three captures about one new project that must end up in one topic; plus a spoken sentence (espeak-ng) through the transcription endpoint. Costs a few cents; run before a release. |
| Client | `make app-test` | Flutter analyze and 38 widget/unit tests against a fake API. |
| Client end to end | `make app-e2e` | The real desktop app against a real daemon under Xvfb: first-run wizard, capture, receipt, undo. |

## Tool versions

Go: the version in `go.mod` (`go 1.27`) and `GO_VERSION` in `.github/workflows/ci.yml`. Flutter: pinned exactly in `app/pubspec.yaml` under `environment.flutter`; GitHub Actions (`flutter-version-file`) and the Dockerfile read it from there, so upgrade Flutter by changing that one line, running `flutter pub get`, and committing the lockfile. `flutter pub outdated` and `go list -m -u all` show what can be raised.

AppImage packaging (`make appimage`, `scripts/appimage.sh`) needs `desktop-file-utils`, `libfuse2`, `file` and `zip` on the build host; the release workflow installs them.
