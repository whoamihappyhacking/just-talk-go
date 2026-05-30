//go:build darwin

package clipboard

import (
	"bytes"
	"context"
	"os/exec"
)

func lookPath(name string) (string, error) { return exec.LookPath(name) }
func runCmd(args []string) (string, error) {
	out, err := exec.Command(args[0], args[1:]...).Output()
	return string(bytes.TrimSpace(out)), err
}
func runCmdWithInput(ctx context.Context, args []string, input string) (string, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewBufferString(input)
	out, err := cmd.Output()
	return string(bytes.TrimSpace(out)), err
}
func candidates() []clipCmd {
	return []clipCmd{
		{get: []string{"pbpaste"}, set: []string{"pbcopy"}},
	}
}
