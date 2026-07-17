# AGENTS.md

This file gives coding agents concise guidance for working in this repository.

## Project

Just Talk is a desktop voice input tool. It records with a global hotkey, sends audio to streaming ASR, then copies recognized text to the clipboard or submits it into the focused input field.

The supported desktop targets are Linux, macOS, and Windows.

## Build And Test

This project uses native platform APIs. Linux and macOS require cgo; Windows uses direct Win32 calls and builds with `CGO_ENABLED=0`.

```bash
make build              # Build for the current platform
make run                # Run on the current platform
make test               # Run all tests
go test ./...           # Faster default test command
go test ./... -tags no_x11
goreleaser check
CGO_ENABLED=1 go build -o build/just-talk ./cmd/just-talk
go build -o build/just-talk.exe ./cmd/just-talk  # Windows
JUST_TALK_TEST_WINDOWS_AUDIO=1 go test ./plugins/voice -run TestWindowsRecorderIntegration -v
JUST_TALK_TEST_WINDOWS_HOTKEY=1 go test ./hotkey -run TestWindowsHookFallbackIntegration -v
```

Release builds are configured by `.goreleaser.yaml` and `.github/workflows/release.yml`, using GoReleaser v2 through the official `goreleaser/goreleaser-action`. Pushing a `v*` tag builds and publishes Linux, macOS, and Windows archives for amd64 and arm64, plus `SHA256SUMS.txt`. Linux and macOS release binaries must remain native cgo builds on their respective GitHub-hosted runners. GoReleaser OSS split/merge is not available, so each native matrix runner creates one archive and the final job only merges those archives into the GitHub Release.

Do not add or preserve non-cgo macOS fallback builds. A build that compiles but cannot provide native hotkeys, recording, clipboard, auto-submit, or overlay is worse than an explicit build failure.

Useful runtime commands:

```bash
just-talk               # TUI mode, default
just-talk --no-tui      # daemon mode
just-talk --doctor      # startup environment check
just-talk --backend x11
just-talk --backend wayland
```

`JUST_TALK_BACKEND` or `--backend` can force `x11`, `wayland`, or `darwin`. `mock` exists for internal provider testing but is not part of the normal user path.

## Platform Dependencies

Linux:

- Wayland hotkeys use evdev and require readable `/dev/input/event*`, usually via the `input` group.
- Wayland clipboard and auto-submit use `wl-clipboard` and `wtype`.
- X11 hotkeys and overlay use native X11 through cgo.
- X11 auto-submit uses XTest and clipboard tools.

macOS:

- Global hotkeys use CGEventTap through `ApplicationServices`.
- Recording uses CoreAudio / AudioQueue.
- Clipboard uses NSPasteboard.
- Auto-submit posts native keyboard events.
- Overlay uses an AppKit `NSPanel` helper process.
- Users grant Accessibility and Microphone permissions to the terminal app that launches Just Talk, not to a separate `.app` bundle.
- Full Xcode is not required, but Apple Command Line Tools must provide `clang` and the macOS SDK.

Windows:

- Global hotkeys poll `GetAsyncKeyState` at 5 ms intervals and use `WH_KEYBOARD_LL` as a physical-key fallback. Providers emit state edges without key-repeat events.
- Suppressed modifier-only voice shortcuts such as `Alt+Super` are consumed by the low-level hook. Candidate modifier events are replayed with `SendInput` when they turn out to be unrelated shortcuts, so normal `Alt`, `Super`, and combinations such as `Alt+Tab` keep working.
- Recording uses native `winmm` wave input at 16 kHz, 16-bit mono PCM.
- Clipboard operations use the Win32 Unicode clipboard through the existing clipboard dependency.
- Auto-submit uses `SendInput` to post Ctrl+V.
- Overlay uses a no-activate, topmost, click-through Win32 window.
- Config is stored under `%APPDATA%\just-talk`; logs and stats use `%LOCALAPPDATA%`.
- No external ffmpeg, SoX, clipboard tool, cgo toolchain, or administrator privilege is required.

## Architecture

```text
cmd/just-talk/main.go
  -> config.Load
  -> doctor.Run
  -> hotkey.NewProvider
  -> engine.New
  -> load voice + overlay plugins
  -> TUI or daemon mode
```

Core packages:

- `hotkey/`: platform global hotkey providers plus shared combo/event types.
- `engine/`: plugin lifecycle and config reload orchestration.
- `plugins/voice/`: recorder, ASR streaming, hotkey behavior, clipboard/auto-submit dispatch, stats.
- `plugins/overlay/`: recording status capsule for Linux, macOS, and Windows.
- `internal/autotype/`: platform paste/auto-submit implementation.
- `internal/clipboard/`: platform clipboard implementation.
- `internal/doctor/`: startup environment checks.
- `internal/tui/`: Bubble Tea configuration UI.

## Hotkey Notes

`Combo` is `{Mods Modifier, Key KeyCode}`. Modifier-only hotkeys use `KeyNone`, for example `Option+Command` on macOS maps to `ModAlt|ModSuper` with `KeyNone`.

Providers should emit key down/up events promptly. Fast repeated toggle presses and hold-mode release handling are user-visible and have historically been fragile, so avoid changes that add blocking work to provider event loops or hotkey handlers.

Plugin hotkey registration must happen in `Plugin.Init()`, not in `Plugin.Start()`. The registry starts dispatch goroutines after plugin loading; registering late can drop events.

## Voice Pipeline Notes

Stopping a recording is intentionally split from hotkey handling:

- Hotkey handlers update state quickly.
- Recorder stop, final audio send, ASR final wait, clipboard writes, and auto-submit happen in background finish work.
- Debug logs should identify whether a stop is waiting on recorder, final audio send, ASR final, clipboard, or ASR client close.

Avoid double-output bugs: recognized final text should be dispatched once per user-stopped session. If changing ASR result handling, check both `Final()` and `Done()` paths.

## UI And Logging

TUI mode must not write normal logs to stdout/stderr because it corrupts the Bubble Tea layout. TUI logs go through the in-app log/status area; debug event details should only be visible when `--debug` is enabled.

`voice.SetOutput(io.Discard)` is intentional in TUI mode.

## Repository Rules

- Prefer existing package boundaries and platform-specific files with build tags.
- Keep user-facing doctor output short and action-oriented. Do not list implementation details as checks unless the user can act on them.
- README is bilingual: update both `README.md` and `README.en.md`.
- `CHANGELOG.md` should be updated for user-visible behavior changes.
- The project does not accept pull requests; issues are welcome.
