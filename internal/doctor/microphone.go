package doctor

import (
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"time"

	"github.com/c/just-talk-go/config"
	"github.com/c/just-talk-go/plugins/voice"
)

// Long enough to cross one capture period on every backend, short enough that
// --doctor still feels immediate.
const micProbeDuration = 400 * time.Millisecond

// A muted input device records silence that looks identical to a working
// microphone until the ASR returns nothing. Probing here names it up front.
func microphoneCheck(cfg *config.Config) Check {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	peak, err := voice.ProbeInputLevel(quiet, cfg.Voice.Device, micProbeDuration)
	switch {
	case err != nil:
		return Check{
			Name: "麦克风输入", OK: false, Severity: Warning,
			Detail: "无法打开录音设备：" + shortLine(err.Error()),
			Fix:    "确认录音后端可用，并在配置里选择正确的输入设备。",
		}
	case peak == 0:
		return Check{
			Name: "麦克风输入", OK: false, Severity: Warning,
			Detail: "只录到纯静音（峰值 0）", Fix: micUnmuteHint(),
		}
	case peak < micQuietPeak:
		return Check{
			Name: "麦克风输入", OK: false, Severity: Warning,
			Detail: fmt.Sprintf("输入信号极弱（峰值 %d/32768）", peak),
			Fix:    "调高麦克风输入音量，或靠近麦克风后重试。",
		}
	}
	return Check{
		Name: "麦克风输入", OK: true, Severity: Warning,
		Detail: fmt.Sprintf("峰值 %d/32768", peak),
	}
}

// Below this peak a recording carries no usable speech even if the device is
// technically open.
const micQuietPeak = 64

func micUnmuteHint() string {
	switch runtime.GOOS {
	case "linux":
		return "解除麦克风静音并调高输入音量：wpctl set-mute @DEFAULT_AUDIO_SOURCE@ 0，或用 alsamixer 检查。"
	case "darwin":
		return "在系统设置 > 声音 > 输入里选择正确的麦克风并调高输入音量。"
	case "windows":
		return "在设置 > 系统 > 声音 > 输入里取消静音并调高输入音量。"
	}
	return "取消系统麦克风静音并调高输入音量。"
}

func shortLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
