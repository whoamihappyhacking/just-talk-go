//go:build windows

package doctor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/c/just-talk-go/config"
	"golang.org/x/sys/windows"
)

var (
	doctorWinMM                = windows.NewLazySystemDLL("winmm.dll")
	doctorProcWaveInGetNumDevs = doctorWinMM.NewProc("waveInGetNumDevs")
)

func runPlatform(cfg *config.Config, backend string) Report {
	if backend == "" {
		backend = "windows"
	}
	report := Report{
		Platform: "windows",
		Backend:  backend,
		Info: []string{
			"配置文件：" + config.DefaultPath(),
		},
		Checks: []Check{
			{Name: "全局热键", OK: true, Severity: Required, Detail: "Windows 全局按键监听可用"},
			windowsMicrophoneCheck(cfg),
			{Name: "剪贴板与自动上屏", OK: true, Severity: Required, Detail: "Windows 原生 API 可用"},
		},
	}
	if cfg.Voice.Enabled {
		report.Checks = append(report.Checks, windowsASRConfigCheck(cfg))
	}
	return report
}

func windowsMicrophoneCheck(cfg *config.Config) Check {
	count, _, _ := doctorProcWaveInGetNumDevs.Call()
	if count == 0 {
		return Check{
			Name: "麦克风录音", OK: false, Severity: Required,
			Detail: "没有检测到录音设备",
			Fix:    "连接或启用麦克风，并在 Windows 设置 → 隐私和安全性 → 麦克风中允许桌面应用访问。",
		}
	}
	detail := fmt.Sprintf("检测到 %d 个输入设备", count)
	if device := strings.TrimSpace(cfg.Voice.Device); device != "" {
		detail += "；配置设备=" + device
	}
	return Check{
		Name: "麦克风录音", OK: true, Severity: Required, Detail: detail,
		Notes: []string{"首次录音失败时，请检查 Windows 麦克风隐私权限。"},
	}
}

func windowsASRConfigCheck(cfg *config.Config) Check {
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
			Fix:    "在 " + filepath.Clean(config.DefaultPath()) + " 的 [voice] 中填写 " + strings.Join(missing, ", ") + "。",
		}
	}
	return Check{Name: "ASR 配置", OK: true, Severity: Warning, Detail: "resource_id=" + resourceID}
}
