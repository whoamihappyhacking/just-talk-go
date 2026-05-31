//go:build darwin && cgo

package doctor

// #cgo LDFLAGS: -framework ApplicationServices
// #include <ApplicationServices/ApplicationServices.h>
//
// static bool doctor_accessibility_trusted(void) {
// 	return AXIsProcessTrusted();
// }
//
// static void doctor_request_accessibility(void) {
// 	CFStringRef keys[] = {kAXTrustedCheckOptionPrompt};
// 	CFBooleanRef vals[] = {kCFBooleanTrue};
// 	CFDictionaryRef opts = CFDictionaryCreate(NULL,
// 		(const void **)keys, (const void **)vals, 1,
// 		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
// 	AXIsProcessTrustedWithOptions(opts);
// 	if (opts) CFRelease(opts);
// }
//
// static CGEventRef doctor_event_tap_callback(CGEventTapProxy proxy, CGEventType type,
// 	CGEventRef event, void *refcon) {
// 	return event;
// }
//
// static CFMachPortRef doctor_create_event_tap(void) {
// 	CGEventMask mask = CGEventMaskBit(kCGEventKeyDown)
// 		| CGEventMaskBit(kCGEventKeyUp)
// 		| CGEventMaskBit(kCGEventFlagsChanged);
// 	return CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
// 		kCGEventTapOptionListenOnly, mask, doctor_event_tap_callback, NULL);
// }
//
// static bool doctor_can_create_event_tap(void) {
// 	CFMachPortRef tap = doctor_create_event_tap();
// 	if (tap == NULL) return false;
// 	CFMachPortInvalidate(tap);
// 	CFRelease(tap);
// 	return true;
// }
import "C"

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/c/just-talk-go/config"
)

func runPlatform(cfg *config.Config, backend string) Report {
	if backend == "" {
		backend = "darwin"
	}
	terminal := detectTerminalApp()
	report := Report{
		Platform: "darwin",
		Backend:  backend,
		Info: []string{
			"当前终端：" + terminal.Name,
			"配置文件：" + configPathForDisplay(),
		},
	}
	report.Checks = append(report.Checks,
		accessibilityCheck(terminal),
		recordingBackendCheck(terminal),
	)
	return report
}

func accessibilityCheck(terminal terminalApp) Check {
	hotkey := terminalHotkeyHint()
	if bool(C.doctor_accessibility_trusted()) {
		return Check{
			Name:     "辅助功能权限",
			OK:       true,
			Severity: Required,
			Detail:   "已开启",
			Notes: []string{
				"用途：监听全局快捷键，并把识别结果自动上屏。",
				terminal.AccessibilityTargetNote(),
				hotkey,
			},
		}
	}
	C.doctor_request_accessibility()
	return Check{
		Name:     "辅助功能权限",
		OK:       false,
		Severity: Required,
		Detail:   "未开启",
		Notes: []string{
			"用途：监听全局快捷键，并把识别结果自动上屏。",
			"打开位置：系统设置 → 隐私与安全性 → 辅助功能",
			terminal.AccessibilityTargetNote(),
			hotkey,
		},
		Fix: "给 " + terminal.AuthTarget() + " 开启权限，然后重启终端。",
	}
}

func recordingBackendCheck(terminal terminalApp) Check {
	return Check{
		Name:     "麦克风录音",
		OK:       true,
		Severity: Required,
		Detail:   "可用",
		Notes: []string{
			"如果 macOS 弹出权限提示，请允许 " + terminal.AuthTarget() + " 使用麦克风。",
		},
	}
}

type terminalApp struct {
	Name   string
	Source string
}

func (t terminalApp) AuthTarget() string {
	if strings.TrimSpace(t.Name) == "" || t.Name == "未能识别" {
		return "当前终端应用"
	}
	if t.Name == "远程 SSH 会话" {
		return "实际启动 Just Talk 的桌面终端应用"
	}
	return t.Name
}

func (t terminalApp) AccessibilityTargetNote() string {
	if t.Name == "远程 SSH 会话" {
		return "提示：远程 SSH 下无法判断桌面终端；实际使用前，请在 macOS 桌面终端里再运行一次环境检查。"
	}
	return "需要勾选：" + t.AuthTarget()
}

func detectTerminalApp() terminalApp {
	if t := detectTerminalFromEnv(); t.Name != "" {
		return t
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_CLIENT") != "" {
		return terminalApp{Name: "远程 SSH 会话", Source: "ssh"}
	}
	if t := detectTerminalFromParents(); t.Name != "" {
		return t
	}
	return terminalApp{Name: "未能识别", Source: "unknown"}
}

func detectTerminalFromEnv() terminalApp {
	envChecks := []struct {
		key   string
		match func(string) string
	}{
		{"LC_TERMINAL", terminalNameFromValue},
		{"TERM_PROGRAM", terminalNameFromValue},
		{"WEZTERM_EXECUTABLE", func(string) string { return "WezTerm" }},
		{"WEZTERM_PANE", func(string) string { return "WezTerm" }},
		{"KITTY_WINDOW_ID", func(string) string { return "kitty" }},
		{"ALACRITTY_WINDOW_ID", func(string) string { return "Alacritty" }},
		{"GHOSTTY_RESOURCES_DIR", func(string) string { return "Ghostty" }},
		{"GHOSTTY_BIN_DIR", func(string) string { return "Ghostty" }},
		{"VSCODE_INJECTION", func(string) string { return "Visual Studio Code" }},
		{"TERM_PROGRAM_VERSION", func(v string) string {
			if strings.Contains(strings.ToLower(os.Getenv("TERM_PROGRAM")), "vscode") {
				return "Visual Studio Code"
			}
			return ""
		}},
	}
	for _, check := range envChecks {
		value := strings.TrimSpace(os.Getenv(check.key))
		if value == "" {
			continue
		}
		if name := check.match(value); name != "" {
			return terminalApp{Name: name, Source: check.key + "=" + value}
		}
	}
	return terminalApp{}
}

func terminalNameFromValue(value string) string {
	v := strings.ToLower(value)
	switch {
	case strings.Contains(v, "iterm"):
		return "iTerm2"
	case strings.Contains(v, "apple_terminal"), strings.Contains(v, "terminal"):
		return "Terminal"
	case strings.Contains(v, "ghostty"):
		return "Ghostty"
	case strings.Contains(v, "wezterm"):
		return "WezTerm"
	case strings.Contains(v, "vscode"):
		return "Visual Studio Code"
	case strings.Contains(v, "kitty"):
		return "kitty"
	case strings.Contains(v, "alacritty"):
		return "Alacritty"
	default:
		return ""
	}
}

func detectTerminalFromParents() terminalApp {
	for pid := os.Getppid(); pid > 1; {
		path, ppid, ok := processPathAndParent(pid)
		if !ok {
			break
		}
		if name := terminalNameFromPath(path); name != "" {
			return terminalApp{Name: name, Source: path}
		}
		if ppid <= 1 || ppid == pid {
			break
		}
		pid = ppid
	}
	return terminalApp{}
}

func processPathAndParent(pid int) (string, int, bool) {
	out, err := runCommand("ps", "-p", strconv.Itoa(pid), "-o", "ppid=", "-o", "comm=")
	if err != nil {
		return "", 0, false
	}
	line := strings.TrimSpace(out)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", 0, false
	}
	ppid, err := strconv.Atoi(fields[0])
	if err != nil {
		return "", 0, false
	}
	path := strings.Join(fields[1:], " ")
	return path, ppid, true
}

func terminalNameFromPath(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "iterm.app"), strings.Contains(lower, "iterm2"):
		return "iTerm2"
	case strings.Contains(lower, "terminal.app/"):
		return "Terminal"
	case strings.Contains(lower, "ghostty.app"), strings.Contains(lower, "/ghostty"):
		return "Ghostty"
	case strings.Contains(lower, "wezterm.app"), strings.Contains(lower, "wezterm"):
		return "WezTerm"
	case strings.Contains(lower, "visual studio code.app"), strings.Contains(lower, "code helper"):
		return "Visual Studio Code"
	case strings.Contains(lower, "kitty.app"), strings.Contains(lower, "/kitty"):
		return "kitty"
	case strings.Contains(lower, "alacritty.app"), strings.Contains(lower, "alacritty"):
		return "Alacritty"
	default:
		return ""
	}
}

func configPathForDisplay() string {
	if p := config.FindConfig(); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.config/just-talk/config.toml"
	}
	return filepath.Join(home, ".config", "just-talk", "config.toml")
}

func overlayPosition(cfg *config.Config) string {
	if strings.TrimSpace(cfg.Overlay.Position) != "" {
		return cfg.Overlay.Position
	}
	return "bottom-center"
}

func runCommand(name string, args ...string) (string, error) {
	data, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func terminalHotkeyHint() string {
	return "热键写法：Option 等价于 Alt，Command/Cmd 等价于 Super。"
}
