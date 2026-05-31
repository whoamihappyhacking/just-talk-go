//go:build linux

package doctor

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/c/just-talk-go/config"
)

func runPlatform(cfg *config.Config, backend string) Report {
	backend = detectBackend(backend)
	report := Report{Platform: "linux", Backend: backend}

	switch backend {
	case "wayland":
		report.Checks = append(report.Checks, waylandChecks(cfg)...)
	case "x11":
		report.Checks = append(report.Checks, x11Checks(cfg)...)
	default:
		report.Checks = append(report.Checks, Check{
			Name: "热键后端", OK: false, Severity: Required,
			Detail: fmt.Sprintf("unknown backend %q", backend),
			Fix:    "使用 --backend wayland 或 --backend x11，或设置 JUST_TALK_BACKEND=wayland/x11。",
		})
	}

	report.Checks = append(report.Checks, platformDependencyChecks(backend)...)
	if cfg.Voice.Enabled {
		report.Checks = append(report.Checks, asrConfigCheck(cfg))
	}
	return report
}

func detectBackend(backend string) string {
	if backend != "" {
		return backend
	}
	if env := os.Getenv("JUST_TALK_BACKEND"); env != "" {
		return env
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		return "wayland"
	}
	if os.Getenv("DISPLAY") != "" {
		return "x11"
	}
	return "unknown"
}

func recordingBackendCheck() Check {
	return commandAnyCheck(
		"录音后端",
		Required,
		[]string{"arecord", "parec", "pw-record"},
		"安装当前实现支持的录音后端：alsa-utils(arecord)、pulseaudio-utils(parec) 或 pipewire-bin(pw-record)。",
	)
}

func waylandChecks(cfg *config.Config) []Check {
	checks := []Check{
		{
			Name: "Wayland 会话", OK: os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland",
			Severity: Required,
			Detail:   envDetail("WAYLAND_DISPLAY", "XDG_SESSION_TYPE"),
			Fix:      "在 Wayland/Sway 会话里运行，或用 --backend x11 切换到 X11。",
		},
	}

	checks = append(checks, inputAccessCheck())
	return checks
}

func x11Checks(cfg *config.Config) []Check {
	return []Check{
		{
			Name: "X11 会话", OK: os.Getenv("DISPLAY") != "",
			Severity: Required,
			Detail:   envDetail("DISPLAY"),
			Fix:      "在 X11 会话里运行，或用 --backend wayland 切换到 Wayland。",
		},
	}
}

func platformDependencyChecks(backend string) []Check {
	checks := []Check{
		recordingBackendCheck(),
	}

	switch backend {
	case "wayland":
		checks = append(checks,
			commandAllCheck("Wayland 剪贴板", Required, []string{"wl-copy", "wl-paste"}, "安装 wl-clipboard。"),
			commandAllCheck("Wayland 自动上屏", Required, []string{"wtype"}, "安装 wtype。"),
		)
	case "x11":
		checks = append(checks,
			commandAllCheck("X11 剪贴板", Required, []string{"xclip"}, "安装 xclip。"),
		)
	}
	return checks
}

func asrConfigCheck(cfg *config.Config) Check {
	var missing []string
	if strings.TrimSpace(cfg.Voice.AppKey) == "" {
		missing = append(missing, "app_key")
	}
	if strings.TrimSpace(cfg.Voice.AccessKey) == "" {
		missing = append(missing, "access_key")
	}
	resourceID := strings.TrimSpace(cfg.Voice.ResourceID)
	if resourceID == "" {
		resourceID = "volc.bigasr.sauc.duration"
	}
	if len(missing) > 0 {
		return Check{
			Name: "ASR 配置", OK: false, Severity: Warning,
			Detail: "缺少 " + strings.Join(missing, ", "),
			Fix:    "在 ~/.config/just-talk/config.toml 的 [voice] 中填写 " + strings.Join(missing, ", ") + "。",
		}
	}
	return Check{Name: "ASR 配置", OK: true, Severity: Warning, Detail: "resource_id=" + resourceID}
}

func inputAccessCheck() Check {
	events, err := filepath.Glob("/dev/input/event*")
	if err != nil || len(events) == 0 {
		return Check{Name: "输入设备权限", OK: false, Severity: Required, Detail: "没有找到 /dev/input/event*", Fix: "确认系统存在 evdev 输入设备。"}
	}
	for _, dev := range events {
		f, err := os.Open(dev)
		if err == nil {
			_ = f.Close()
			return Check{Name: "输入设备权限", OK: true, Severity: Required, Detail: dev + " 可读"}
		}
	}

	groupDetail := currentGroupDetail()
	return Check{
		Name:     "输入设备权限",
		OK:       false,
		Severity: Required,
		Detail:   "无法读取 /dev/input/event*；" + groupDetail,
		Fix:      "把当前用户加入 input 组：sudo usermod -aG input $USER，然后重新登录。",
	}
}

func envDetail(names ...string) string {
	var parts []string
	for _, name := range names {
		value := os.Getenv(name)
		if value == "" {
			value = "<empty>"
		}
		parts = append(parts, name+"="+value)
	}
	return strings.Join(parts, " / ")
}

func currentGroupDetail() string {
	u, err := user.Current()
	if err != nil {
		return "无法读取当前用户组"
	}
	gids, err := u.GroupIds()
	if err != nil {
		return "无法读取当前用户组"
	}
	for _, gid := range gids {
		g, err := user.LookupGroupId(gid)
		if err == nil && g.Name == "input" {
			return "当前用户已在 input 组，但当前会话可能尚未刷新"
		}
	}
	input, err := user.LookupGroup("input")
	if err != nil {
		return "系统没有 input 组"
	}
	if _, err := strconv.Atoi(input.Gid); err != nil {
		return "input 组存在"
	}
	return "当前用户不在 input 组"
}
