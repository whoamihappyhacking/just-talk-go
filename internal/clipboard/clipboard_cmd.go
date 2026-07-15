//go:build linux

package clipboard

import "fmt"

func newPlatformClipboard() (*Clipboard, error) {
	cmd, err := detectCommand()
	if err != nil {
		return nil, err
	}
	return newFromCmd(cmd), nil
}

// detectCommand returns the best available clipboard command for this platform.
func detectCommand() (clipCmd, error) {
	for _, c := range candidates() {
		if path, err := lookPath(c.get[0]); err == nil {
			_ = path
			return c, nil
		}
	}
	return clipCmd{}, fmt.Errorf("no clipboard tool found")
}
