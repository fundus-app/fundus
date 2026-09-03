# ADR-0007: Flutter desktop plus embedded Flutter web in one Go binary; CLI as dev tool

**Status:** accepted

## Context

The user wants a single static Go binary, Flutter as the UI, and a Docker image; mobile is not the first priority.

## Decision

- `fundus` is one static binary (`CGO_ENABLED=0`) containing daemon and CLI. `fundus serve` runs the daemon; every other subcommand is an HTTP client.
- The Flutter app in `app/` targets Linux desktop and web from one code base. The web build is embedded via `internal/webui` (`go:embed`) and served at `/` with an SPA fallback; when no build is embedded a placeholder page explains how to build it.
- Desktop-only features (global-shortcut capture window, tray) live in the native build. A `--quick-capture` launch mode is planned so a compositor keybinding can open the capture window.
- The CLI is a developer and scripting tool, kept minimal.
- Mobile targets, share target, widget and push-to-talk come later; clients hold no canonical data.

## Consequences

One artifact to deploy for the server case. Native desktop remains a separate bundle (Flutter Linux produces an executable plus libraries), packaged as an AppImage by GitHub Actions; Flatpak later.

## Amendment (2026-09-03)

The native desktop apps are the primary distribution: Linux AppImage, macOS app, Windows zip, each with the `fundus` daemon bundled next to the app executable so the app can start it itself. They are built by GitHub Actions on `main` and on tags. The single binary with the embedded web UI remains for servers, containers and headless use.
