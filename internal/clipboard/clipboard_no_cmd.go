//go:build darwin || windows

package clipboard

import (
	"context"
	"fmt"
)

func runCmd(args []string) (string, error) {
	return "", fmt.Errorf("command clipboard is not available on this platform")
}

func runCmdWithInput(ctx context.Context, args []string, input string) (string, error) {
	return "", fmt.Errorf("command clipboard is not available on this platform")
}
