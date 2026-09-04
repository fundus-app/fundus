# Fundus client (Flutter)

The reference client for the Fundus daemon. One code base for Linux desktop
and the web build that the daemon embeds and serves at `/`.

Four areas, nothing else: **Capture** (the field at the top, `Ctrl K`),
**Conversation**, **Views** (Inbox, Relevant, Open, Ideas, Notes, Topics,
Waiting, Later, Changes) and the **Inspector** (read, trace, edit directly).

## Develop

```sh
export PATH="/path/to/flutter/bin:$PATH"   # only if the SDK is not on PATH

# a daemon to talk to (fake provider = no API key needed)
go run ./cmd/fundus serve --fake --dev-cors

cd app
flutter pub get
flutter run -d linux                     # desktop
CHROME_EXECUTABLE=/usr/bin/chromium flutter run -d chrome   # web, needs --dev-cors on the daemon
flutter analyze && flutter test
```

The desktop build talks to `http://127.0.0.1:7433` by default; change server
URL and token in Settings (`Ctrl ,`). The web build uses the origin it was
served from, so the embedded UI needs no configuration.

## Build

```sh
make ui         # web build, copied into internal/webui/web for `go build`
make build      # Go binary with the UI embedded → bin/fundus
make ui-linux   # desktop bundle in app/build/linux/x64/release/bundle/
```

## Quick capture window

`fundus_app --quick-capture` opens a small always-on-top window with only
the capture field. Enter files the thought and closes the window; Esc cancels.
Bind it to a global shortcut in your compositor:

```kdl
// niri config.kdl
binds {
    Mod+Shift+C { spawn "/path/to/bundle/fundus_app" "--quick-capture"; }
}
```

```
# sway
bindsym $mod+Shift+c exec /path/to/bundle/fundus_app --quick-capture
```

## Dictation

The microphone button in the capture bar (and in the quick-capture window)
records while you talk, then sends the WAV to `POST /v1/transcribe` and puts
the transcript into the field for review; nothing is captured until you press
Enter. `Ctrl Shift K` toggles recording, `Esc` stops it. The button only shows
when a dictation model is connected (`GET /v1/health` → `dictation: true`).

- Linux: records through PulseAudio's client library (`libpulse`, which
  every desktop ships; PipeWire answers through its pulse compatibility). No
  helper binaries, so it also works inside Flatpak and Snap. When no source
  can be opened the button reports "Microphone not available".
- Web: `MediaRecorder`/`getUserMedia`; the browser asks for permission.

The second e2e test records through the real plugin against a daemon that
has a dictation model. It skips itself unless `FUNDUS_DICTATION_URL` is set;
the recipe (null sink, `espeak-ng`, `PULSE_SOURCE`/`PULSE_SINK`) is at the top
of `integration_test/first_run_test.dart`.

## One-command start

The desktop app starts the daemon for you: when nothing answers at the
configured (loopback) URL it looks for a `fundus` binary next to the app
executable, then on `PATH`, runs `fundus serve` detached, and waits up to
five seconds. A `--daemon-path=/path/to/fundus` argument overrides the search.
If no binary is found the app shows the command to run by hand.

On first start (no model configured) the app opens the setup wizard: choose
OpenAI, Anthropic, OpenRouter (key or "Connect with OpenRouter"), Ollama, or
"No model for now"; test the connection; pick a triage and a chat model. You
can capture before that is done; captures wait in the inbox as pending. The
same panel lives in Settings → Model & provider, next to the autonomy toggles.

## End-to-end test

`integration_test/first_run_test.dart` drives the real Linux desktop build
against a real daemon: it starts `fundus serve` on a free port with fresh
config and data, walks the first-run wizard ("No model for now"), captures a
task, checks the receipt, undoes it from the pill, and opens Settings. It needs
a display:

```sh
make build                       # bin/fundus
xvfb-run -a make app-e2e         # X11 virtual display
# or, on a Wayland desktop, a headless compositor:
WLR_BACKENDS=headless sway -c /dev/null &   # then WAYLAND_DISPLAY=wayland-N GDK_BACKEND=wayland make app-e2e
```

On failure the test prints the failing step, the daemon log tail, `/v1/health`,
`/v1/inbox` and a trimmed widget tree, and writes them (plus a screenshot where
supported) to `app/build/e2e/`; CI uploads that folder as an artifact.

`FUNDUS_BIN` points the test at another daemon binary. The app accepts
`--server=http://host:port` to pin the daemon address for one run.

## Launch arguments

| Argument | Effect |
|---|---|
| `--quick-capture` | small capture-only window (see above) |
| `--view=relevant` | start in a view: inbox, relevant, open, ideas, notes, topics, waiting, later, changes, conversation |
| `--open=note_01…` | start with an object selected in the inspector |
| `--daemon-path=…` | fundus binary to start when no daemon answers |
| `--server=http://…` | pin the daemon address for this run (not persisted) |

## Keyboard

| Keys | Action |
|---|---|
| `Ctrl K`, `Ctrl N` | focus capture |
| `Enter` / `Shift Enter` | submit / new line |
| `Ctrl F` | search |
| `Ctrl 1…9` | switch view |
| `Ctrl ,` | settings |
| `Esc` | close search / selection |

## Structure

```
lib/api/      client (HTTP + SSE) and JSON models
lib/state/    Settings, AppState (views, selection, pending captures), ChatState
lib/ui/       theme, shell, capture bar, views, inspector, conversation, blocks renderer
test/         inline parser, model parsing, capture bar flow, block renderer
assets/fonts  Inter, Fraunces, JetBrains Mono (variable fonts, bundled)
```
