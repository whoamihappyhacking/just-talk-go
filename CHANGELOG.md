# Changelog

All notable project changes are tracked here.

## Unreleased

- Classify ASR server error responses: codes that reject the request itself (`45xxxxxx`) skip the reconnect ladder and wait for an explicit retry, while server-side faults (`55xxxxxx`) and the upload-stall timeout still reconnect. Replaying a whole recording is the most expensive thing a weak link does, so a request the server already refused no longer costs three replays.
- Document what `voice.save_traffic` really is: a transport switch, not a bandwidth optimization. Whole-file mode sends about 33% more bytes as Base64 but never replays the recording after a dropped connection; the README and the TUI help now say so instead of implying savings.
- Fix a blurry recording capsule on HiDPI Wayland outputs. The layer surface never set a buffer scale, so a logical-resolution buffer was upscaled by the compositor. The surface now keeps its logical size while the pixel buffer follows the output scale.
- Redraw the recording capsule with antialiased text instead of a 5x7 bitmap font scaled by whole numbers, and shrink it from 150x42 to 70x24 logical pixels with no border: a status dot and a short code. Both Linux backends share one renderer.
- Report a silent recording as a microphone problem instead of an ASR failure. When the service returns no text, the cached audio's peak amplitude decides the message, so a muted or wrongly routed input device no longer reads as "ASR 未返回有效文字".
- Add a `--doctor` microphone check that records briefly and reports the captured peak, with a platform-specific command for unmuting the input device.
- Surface ASR server error responses immediately with their code and message. Previously an error frame was silently discarded, the final-result signal never arrived, and the session waited out the full 30-second timeout before reporting an unrelated cause.
- Add an opt-in `status_file` section that publishes recording state as JSON under `$XDG_RUNTIME_DIR/just-talk/status.json` for external status bars, plus an Omarchy Quickshell bar widget in `integrations/omarchy/` that renders it. The file is removed on exit so a widget can tell "not running" from "idle".

- Fix TUI configuration reloads retaining old ASR credentials: after saving with `s`, the next recording uses the updated App Key and Access Key without restarting.
- Add an opt-in “省流量” / `voice.save_traffic` switch (off by default). When enabled, cache locally until recording stops, then submit a complete WAV as Base64 in one synchronous Flash HTTP request using the separately enabled `volc.bigasr.auc_turbo` resource. Keep the original WebSocket mode, pin the mode per recording, bound uploads/results/timeouts, reject redirects, retain failed/silent/empty results for explicit R-triggered retry, and do not automatically resubmit potentially billable file requests. This is a transport-mode switch, not a bandwidth-savings guarantee; cache remains memory-only.
- Treat WebSocket sessions with no recognized text as retryable through R instead of silently completing and releasing their recordings.

- Raise the ASR WebSocket response limit from 32 KiB to a bounded 8 MiB so large transcripts with utterance metadata do not disconnect before the final result or repeatedly fail on retry.

- Tolerate stalled audio writes for 15 seconds and final responses for 30 seconds, retain transport errors, reject premature final results, and keep slow UI consumers from blocking ASR reception. Drain buffered Linux/macOS recorder audio before closing the read pipe.

- Add a responsive Chinese project introduction website with a simulated recording and R-triggered retry demo, platform-specific launch commands, static HTTP preview on port 7788, and automated GitHub Pages deployment linked from both READMEs.

- Cache recording audio in memory independently of ASR connections and automatically reconnect after connection, send, or final-result failures with 1/2/4-second backoff (three retries), replaying the complete recording on a fresh connection. Retain audio for R-triggered recovery if automatic attempts are exhausted, and skip automatic retries for non-transient HTTP client errors. Keep recognized text for R-triggered output retries, show recovery status in the TUI/overlay, and cancel pending recovery on Esc or shutdown.
- Require a final ASR response before dispatching text, preserve recordings stopped during connection setup, and measure recording duration from audio rather than network/retry wait time.

## v0.0.3 - 2026-07-29

- Reworked Windows modifier-only hotkeys so the low-level hook only observes key edges and never consumes or replays modifiers. `Alt+Super` works through exact physical-state matching, stale hook state is cleared without combining separate single-key presses, and system shortcuts such as `Alt+Tab` remain untouched.

## v0.0.2 - 2026-07-21

- Fixed intermittent Windows `Alt+Super` false activations where stale low-level hook state could make a later Alt-only press look like the full shortcut; Windows modifier combinations now require an exact modifier set and reset stale suppressed state after physical release.

## v0.0.1 - 2026-07-17

- Added a tag-triggered GoReleaser v2 pipeline using the official GitHub Action to build Linux, macOS, and Windows amd64/arm64 archives on native runners and publish SHA256 checksums.
- Added Windows 10/11 support with global key-state monitoring, native WinMM microphone capture, Unicode clipboard access, SendInput auto-submit, a click-through Win32 status overlay, and Windows environment checks.
- Added Windows-standard config, state, log, and per-user installation paths. Windows builds no longer require external recording or clipboard commands.
- Added an opt-in Windows microphone integration test using `JUST_TALK_TEST_WINDOWS_AUDIO=1`.
- Added a low-level Windows keyboard-hook fallback so modifier-only shortcuts such as `Alt+Super` still work when `GetAsyncKeyState` misses the physical Windows key.
- Consume Windows modifier-only voice shortcuts such as `Alt+Super` so they do not activate menus or toolbars in the focused application, while replaying unrelated modifier shortcuts normally.
- Fixed recording shutdown getting stuck while ASR audio writes were still in flight, and accept empty final ASR responses without waiting for a false timeout.
- Redesigned the Windows status overlay with per-pixel transparency, antialiased animated waveform bars, smoother capsule edges, and a soft shadow.
- Clarified README build and install setup steps for the repository directory and `~/.local/bin` PATH.
- Restricted voice hotkeys to non-text global shortcut keys, rejecting letters, digits, punctuation, Space, and similar text-producing keys.
- Avoid duplicate auto-submit on KDE Plasma by using uinput directly and not writing the Wayland primary selection there.
- TUI is now the default startup mode.
- Added persistent usage statistics for total sessions, recognized characters, average speed, and recent speed.
- Added configurable ASR hotwords.
- Added TUI help toggle with `h`.
- Improved Wayland clipboard and auto-submit behavior with `wl-copy` and `wtype`.
- Added Linux recording status overlay for X11 and Wayland.
- Added macOS support for global hotkeys, native recording, clipboard, auto-submit, recording status overlay, and environment checks.
- Removed non-cgo macOS fallback builds; Just Talk now requires cgo for native platform integration.
- Replaced the old Claude-specific agent guide with `AGENTS.md` and clarified build documentation.
- Improved toggle and hold hotkey behavior for fast repeated key presses.
- Show ASR connection and final-result timeout errors in the status UI/overlay instead of immediately falling back to idle.
- Added transient `Esc` cancel and `R` retry hotkeys while recording or showing retryable errors.
- Improved X11 overlay placement on multi-monitor setups and switched X11 rendering to an ARGB window for smoother rounded corners.
- Fixed a Wayland overlay shutdown race that could crash while closing the app, and surfaced Linux `arecord` microphone/device failures in the UI.
- Made `Esc` cancel active overlay states, including the final ASR wait state, and suppress output from canceled pending sessions.
- Improved Wayland overlay rounded-corner antialiasing, especially on KDE Plasma.
- Added `just-talk --install` and `make install` to install the binary into `~/.local/bin`.

## 2026-05-30

- Initial Linux-focused development snapshot.
- Supported Linux Wayland hotkeys via evdev.
- Supported Linux X11 hotkeys via native X11 grabs.
- Added Doubao streaming ASR integration.
- Added TUI configuration interface.
- Added automatic clipboard copy and auto-submit.
