//go:build !linux && !darwin

package doctor

import (
	"runtime"

	"github.com/c/just-talk-go/config"
)

func runPlatform(cfg *config.Config, backend string) Report {
	return Report{
		Platform: runtime.GOOS,
		Backend:  backend,
		Checks: []Check{
			{
				Name:     "平台支持",
				OK:       false,
				Severity: Required,
				Detail:   runtime.GOOS + " 暂未实现",
				Fix:      "当前版本先支持 Linux 的 Wayland 和 X11；macOS/Windows 后续再实现。",
			},
		},
	}
}
