# Just Talk

After editing the App Key or Access Key in the TUI, leave edit mode and press `s` to save. The next recording uses the new credentials without restarting. An ongoing recognition session keeps the credentials used to establish its connection.

[中文](README.md) · [Website](https://whoamihappyhacking.github.io/just-talk-go/)

Just Talk is a desktop voice input tool. It records audio with a global hotkey, sends it to streaming ASR, and then copies the recognized text to the clipboard or submits it directly into the focused input field.

It is built for people who want to type less and speak more while coding, chatting, writing notes, or working with long text.

## Screenshot

![Just Talk TUI](docs/screenshot-tui.png)

## Features

- Global hotkey recording with `toggle` and `hold` modes.
- Voice hotkeys are limited to keys suitable for global shortcuts: modifiers, function keys, Tab, CapsLock, arrow/navigation keys, and similar non-text keys. Letters, digits, punctuation, Space, and other text-producing keys are rejected.
- Doubao streaming ASR with optimized bidirectional streaming and second-pass recognition.
- Clipboard copy and automatic text submission.
- Always-on-top recording status overlay for Wayland, X11, macOS, and Windows.
- TUI configuration for hotkeys, mode, auto-submit, stop delay, hotwords, and related settings.
- ASR hotwords for project names, people names, English terms, and domain-specific vocabulary.
- Usage statistics for total sessions, total recognized characters, average speed, and recent speed.

## Platform Status

Linux, macOS, and Windows desktops are supported:

| Platform | Status | Notes |
| --- | --- | --- |
| Linux Wayland | Supported | Works with Sway / wlroots; hotkeys use evdev and require input permissions |
| Linux X11 | Supported | Uses native X11 global hotkeys |
| macOS | Supported | Global hotkeys use CGEventTap, recording uses CoreAudio, clipboard uses NSPasteboard, and overlay uses AppKit NSPanel |
| Windows 10/11 | Supported | Global key polling with a low-level keyboard-hook edge fallback, WinMM recording, Unicode clipboard, SendInput auto-submit, and a Win32 status overlay |

## Build

Just Talk uses native platform APIs. Linux and macOS builds require cgo; Windows uses direct Win32 calls from Go and does not require cgo.

Linux build dependencies:

```bash
# Arch Linux
sudo pacman -S --needed go gcc libx11 libxtst libxext wayland

# Debian / Ubuntu
sudo apt install golang-go build-essential libx11-dev libxtst-dev libxext-dev libxinerama-dev libwayland-dev
```

macOS build dependencies:

```bash
# Apple Command Line Tools provide clang and the macOS SDK. Full Xcode is not required.
xcode-select --install
```

Windows build dependency:

```powershell
# Install Go 1.25 or later. No ffmpeg, SoX, or C compiler is required.
winget install --id GoLang.Go --exact
```

Build for the current platform:

```bash
cd just-talk-go
CGO_ENABLED=1 go build -o build/just-talk ./cmd/just-talk
```

Windows PowerShell:

```powershell
cd just-talk-go
go build -o build\just-talk.exe .\cmd\just-talk
```

Install to `~/.local/bin/just-talk`:

```bash
# Make sure ~/.local/bin is in PATH. If not, add this line to ~/.bashrc or ~/.zshrc.
# export PATH="$HOME/.local/bin:$PATH"
build/just-talk --install
# or
make install
```

macOS must be built on macOS. The project does not provide a non-cgo build.

Install on Windows to `%LOCALAPPDATA%\Programs\Just Talk\just-talk.exe`:

```powershell
.\build\just-talk.exe --install
# If the directory is not already in PATH, follow the note printed by the command.
```

## Release Downloads

GitHub Releases provide prebuilt archives for:

- Linux amd64 / arm64
- macOS Intel / Apple Silicon
- Windows amd64 / arm64
- `SHA256SUMS.txt` checksum verification

The release workflow uses GoReleaser v2 with the official `goreleaser/goreleaser-action`. Linux, macOS, and Windows binaries are built natively on matching GitHub-hosted runners. Maintainers can build and publish a release by pushing a `v*` tag, for example:

```bash
git tag v0.7.0
git push origin v0.7.0
```

## Usage

Start the TUI:

```bash
just-talk
```

Run without the TUI:

```bash
just-talk --no-tui
```

Force a backend:

```bash
just-talk --backend wayland
just-talk --backend x11
```

Windows selects its native backend automatically. Check the microphone and configuration before first use:

```powershell
.\build\just-talk.exe --doctor
```

With “省流量” (Save traffic) off by default, the original WebSocket mode continuously caches audio in memory. Capture runs independently of WebSocket writes, so network stalls do not block the local audio cache. Connection failures, audio send failures, and missing final ASR results trigger up to three automatic reconnects after 1, 2, and 4 seconds. Each new connection replays the complete recording from the beginning without a key press. Writes allow 15 seconds and final results allow 30 seconds. Each ASR response may contain up to 8 MiB, preventing full transcripts and utterance metadata for longer recordings from repeatedly disconnecting at the default 32 KiB WebSocket limit. If recovery is exhausted, press `R` to start another recovery cycle; authentication or configuration failures wait for `R` immediately. Text is dispatched only after a complete final response, never from a partial result. Recording continues while offline. After recording stops, the cache and waiting state remain until success, `Esc`, or app exit. The TUI shows the failure and the prompt to press R; the overlay shows `R?`. Clipboard or auto-submit failures also retain the text and wait for `R` to retry output. With multiple failed recordings, `R` retries the most recent one. Press `Esc` to cancel the current recording and all pending retries. The memory cache is released on exit and is not recovered after restarting. If the microphone cannot start, fix the device or permissions and record again.

### Save traffic switch: recognize the whole recording after stopping

In the TUI, select **省流量**, press `e` to edit, Space to toggle, and Enter to save. Alternatively, add this under the existing `[voice]` section:

```toml
save_traffic = true # Default false preserves the original WebSocket mode
```

- **On**: record into the cache without connecting to ASR. After stopping and draining trailing audio, wrap the complete PCM recording in WAV and submit Base64 audio in one HTTP POST, returning the transcript directly. The endpoint is `https://openspeech.bytedance.com/api/v3/auc/bigmodel/recognize/flash`; no WebSocket, public file URL, or polling is involved.
- **Off**: keep the existing live WebSocket upload and automatic reconnect behavior. Saving the switch affects the **next recording**, not active recordings or pending retries.
- **Access**: reuse the existing App Key / Access Key, but separately enable `volc.bigasr.auc_turbo` for the application. File mode always uses this resource without changing the WebSocket `resource_id`. Check access and billing for each service in the console.
- **Retries**: each HTTP request has a five-minute deadline. Failures, silence responses, and empty transcripts keep the memory cache and wait for `R` to submit again. There is no automatic resubmission or hidden WebSocket fallback, avoiding repeated potential charges after uncertain failures. `Esc` or exit still discards the cache; **there is no disk backup or restart recovery yet**.
- **Limits**: the official limits are a WAV file of at most 100 MB and two hours of audio. Native PCM16/16 kHz/mono WAV reaches the size limit first (about 52 minutes); the client checks size before submitting. The service recommends direct uploads within 20 MB when possible. Long recordings increase upload latency and memory use.
- **Naming**: “Save traffic” means whole-recording submission after stopping, not guaranteed bandwidth savings. Base64 increases audio size by roughly one third, and waiting after stopping is generally longer. Neither mode calls a separate chat model to edit the transcript.

See the official [Flash whole-file HTTP API](https://www.volcengine.com/docs/6561/1631584) and [request parameters](https://www.volcengine.com/docs/6561/1354868).

## Automatic processing of recognition results

Just Talk outputs the final upstream ASR text directly. It does not call an additional chat model to summarize, polish, or restructure it. These WebSocket request parameters are currently fixed in `plugins/voice/asr.go`, without corresponding TOML/TUI switches:

| Parameter | Current value | Meaning |
| --- | --- | --- |
| `enable_punc` | `true` | Request automatic punctuation |
| `enable_itn` | `true` | Request inverse text normalization, such as converting spoken numbers to digits |
| `enable_ddc` | `false` | Do not request disfluency removal |
| `enable_nonstream` | `true` | Request second-pass recognition where supported by the endpoint/model; this is not a separate text-editing service |

These settings already existed in v0.0.3 and were not changed by the network recovery fix. Changes in punctuation, number formatting, or final wording alone do not establish that upstream recently introduced or enabled automatic text editing.

The default WebSocket endpoint is `wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async`, with default resource ID `volc.bigasr.sauc.duration`. Official documentation also lists the 2.0 resource `volc.seedasr.sauc.duration`; this project does not migrate to it automatically. The actual resource depends on the `resource_id` setting and account access. See the official [ASR capabilities](https://www.volcengine.com/docs/6561/1354871?lang=zh) and [ASR resource versions](https://www.volcengine.com/docs/6348/1807452?lang=zh), checked on 2026-09-10.

## Configuration

Default config path:

```text
# Linux / macOS
~/.config/just-talk/config.toml

# Windows
%APPDATA%\just-talk\config.toml
```

Recommended hotkey config:

```toml
[voice]
mode = "toggle"
push_to_talk = "Alt+Super"
```

`Alt+Super` with `toggle` mode is recommended. Press once to start recording, then press again to stop. This avoids hold-mode key conflicts with desktop environments or focused input fields. On Windows, the low-level keyboard hook only observes key edges and never consumes or replays modifiers, so standalone `Alt`, `Super`, and shortcuts such as `Alt+Tab` retain their normal system behavior. Modifier combinations require the exact configured set, and hook fallback state is checked against the unsuppressed physical key state so separate single-key presses cannot be combined into the shortcut.

Voice hotkeys only support keys suitable for global shortcuts:

- Supported: modifier-only combinations, such as `Alt+Super` and `Ctrl+Alt+Shift`.
- Supported: function keys `F1` through `F24`, such as `F9` and `Alt+F8`.
- Supported: non-text control and navigation keys, such as `Tab`, `Enter`, `Escape`, `Backspace`, `CapsLock`, `Up`, `Down`, `Left`, `Right`, `Home`, `End`, `PageUp`, `PageDown`, `Insert`, and `Delete`.
- Not supported: letters, digits, punctuation, Space, numpad digits, and numpad symbols that can enter text, such as `Alt+G`, `G`, `Alt+1`, and `Alt+Space`.

Hotword example:

```toml
[voice]
hotwords = ["Wayland", "Sway", "wl-copy", "wtype", "just-talk-go"]
```

macOS hotkey example:

```toml
[voice]
# Option is Alt; Command/Cmd is Super.
push_to_talk = "Option+Command"
```

On Windows, `Win` and `Super` both refer to the Windows logo key. If recording is unavailable, allow desktop applications to access the microphone under Windows Settings > Privacy & security > Microphone.

## What "省流量" (save_traffic) Actually Does

`voice.save_traffic` is a **transport switch, not a bandwidth optimization**. The name invites the wrong expectation. The real difference:

| | Off (default, streaming WebSocket) | On (whole-file HTTP) |
|---|---|---|
| When audio is sent | While recording | One submission after stopping |
| Encoding | Raw PCM | WAV as Base64, **about 33% larger** |
| Time to result | Shortly after stopping | After the whole upload plus recognition |
| Retry cost | A reconnect **replays the entire recording** | Sent once, never resubmitted automatically |
| Requires | `volc.bigasr.sauc.duration` | `volc.bigasr.auc_turbo`, enabled separately |

For a recording that succeeds on the first try, turning it on uses **more** data, not less. It pays off on a **weak network**: in streaming mode every reconnect resends the whole recording, so a bad link can transmit the same audio three or four times. Whole-file mode sends it once and never resubmits on its own, because a repeated submission may be billed again; press `R` to retry by hand.

In short: leave it off on a stable connection. Turn it on for longer recordings on a bad link, accepting a slower result and a larger single upload.

## Behaviour On A Weak Network

- Recording is decoupled from the network: capture continues while offline, audio is cached in memory, and network stalls never drop words.
- On a connection, send, or final-result failure, Just Talk reconnects automatically with 1/2/4-second backoff, up to three times, replaying the whole recording each time.
- Server codes that reject the request itself (`45xxxxxx`, such as invalid parameters or a malformed audio format) skip the backoff ladder and wait for `R`. Server-side faults (`55xxxxxx`) and the upload-stall timeout (`45000081`) still retry automatically.
- Authentication and configuration HTTP 4xx responses likewise wait for `R` instead of spending reconnect attempts.
- When automatic recovery is exhausted the recording is retained: `R` retries, `Esc` discards.

## Status Bar Integration

Besides the built-in floating capsule, Just Talk can publish recording state as JSON for an external status bar to render:

```toml
[overlay]
enabled = false          # turn off the floating capsule

[status_file]
enabled = true
path = ""                # empty selects $XDG_RUNTIME_DIR/just-talk/status.json
```

The file is rewritten in place only when the state changes, and removed on exit, so a missing file means "not running" rather than "idle".

The repository ships an Omarchy Quickshell bar widget that shows a recording dot with elapsed time while recording, hides itself when idle, and follows the active theme's colors. See [integrations/omarchy/README.md](integrations/omarchy/README.md) for installation and the status file format. waybar, eww, and polybar can read the same file.

## Troubleshooting

"录音几乎没有声音" after a recording means the capture was silent, which is a microphone problem rather than an ASR one. Start with the environment check:

```bash
just-talk --doctor
```

The "麦克风输入" check reports the measured peak. A peak of 0 on Linux usually means the input device is muted:

```bash
wpctl set-mute @DEFAULT_AUDIO_SOURCE@ 0
wpctl set-volume @DEFAULT_AUDIO_SOURCE@ 0.85
```

The same sentence pasted twice almost always means two instances are running. Wayland hotkeys read `/dev/input/event*` passively without grabbing the devices, so both processes see the same hotkey and each one recognizes and pastes it. A second instance now fails to start with an explicit error; on an older build, confirm only one process is left:

```bash
pgrep -a just-talk
```

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## Maintenance And Contributions

Just Talk is maintained by `whoamihappyhacking`.

This project does not accept pull requests. Issues are welcome for bug reports, usage feedback, and feature discussion.

## License

Just Talk is licensed under the GNU General Public License v3.0.

## Project website

Visit the [Just Talk website](https://whoamihappyhacking.github.io/just-talk-go/). GitHub Actions automatically deploys updates to `website/` pushed to `master`.

The static introduction in `website/` includes features, platform support, quick-start instructions, and a simulated demo that does not use the microphone. Preview it with:

```bash
python3 -m http.server 7788 --bind 0.0.0.0 --directory website
```
