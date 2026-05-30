//go:build linux

package clipboard

import (
	"bytes"
	"context"
	"os"
	"os/exec"
)

func lookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func runCmd(args []string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.Output()
	return string(bytes.TrimSpace(out)), err
}

func runCmdWithInput(ctx context.Context, args []string, input string) (string, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewBufferString(input)
	out, err := cmd.Output()
	return string(bytes.TrimSpace(out)), err
}

func candidates() []clipCmd {
	x11 := clipCmd{
		// xclip (X11)
		get: []string{"xclip", "-o", "-selection", "clipboard"},
		set: []string{"xclip", "-selection", "clipboard"},
		// Write to both PRIMARY selection AND CUT_BUFFER0.
		// CUT_BUFFER0 is the classic X11 cut buffer that doesn't need
		// a running process — Shift+Insert reads from it directly.
		setPrimary: []string{"sh", "-c", "xclip -selection primary; xprop -root -format CUT_BUFFER0 8s -set CUT_BUFFER0 \"$(xclip -o -selection primary)\""},
	}
	wayland := clipCmd{
		// wl-clipboard (Wayland)
		get:        []string{"wl-paste"},
		set:        []string{"wl-copy", "--type", "text/plain;charset=utf-8"},
		setPrimary: []string{"wl-copy", "--primary", "--type", "text/plain;charset=utf-8"},
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		return []clipCmd{wayland}
	}
	return []clipCmd{x11, wayland}
}
