package doctor

import (
	"os/exec"
	"strings"
)

func commandAllCheck(name string, severity Severity, commands []string, fix string) Check {
	var missing []string
	var found []string
	for _, cmd := range commands {
		if path, err := exec.LookPath(cmd); err == nil {
			found = append(found, cmd+"="+path)
		} else {
			missing = append(missing, cmd)
		}
	}
	if len(missing) > 0 {
		return Check{Name: name, OK: false, Severity: severity, Detail: "缺少 " + strings.Join(missing, ", "), Fix: fix}
	}
	return Check{Name: name, OK: true, Severity: severity, Detail: strings.Join(found, " / ")}
}

func commandAnyCheck(name string, severity Severity, commands []string, fix string) Check {
	for _, cmd := range commands {
		if path, err := exec.LookPath(cmd); err == nil {
			return Check{Name: name, OK: true, Severity: severity, Detail: cmd + "=" + path}
		}
	}
	return Check{Name: name, OK: false, Severity: severity, Detail: "缺少 " + strings.Join(commands, ", "), Fix: fix}
}
