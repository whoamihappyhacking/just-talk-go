// Package clipboard provides cross-platform clipboard operations.
package clipboard

import (
	"context"
	"fmt"
	"time"
)

// Clipboard provides Get/Set operations on the system clipboard.
type Clipboard struct {
	getCmd        []string
	setCmd        []string
	setPrimaryCmd []string // X11 primary selection, nil if unsupported
}

// New creates a platform-specific Clipboard.
func New() (*Clipboard, error) {
	cmd, err := detectCommand()
	if err != nil {
		return nil, err
	}
	return newFromCmd(cmd), nil
}

// Get reads the current clipboard content.
func (c *Clipboard) Get() (string, error) {
	return runCmd(c.getCmd)
}

// Set writes text to the clipboard.
func (c *Clipboard) Set(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runCmdWithInput(ctx, c.setCmd, text)
	return err
}

// SetPrimary writes text to the X11 primary selection (if supported).
func (c *Clipboard) SetPrimary(text string) error {
	if c.setPrimaryCmd == nil {
		return nil // not supported on this platform
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runCmdWithInput(ctx, c.setPrimaryCmd, text)
	return err
}

type clipCmd struct {
	get        []string
	set        []string
	setPrimary []string
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

// NewFromCmd creates a Clipboard from explicit commands (for testing).
func newFromCmd(c clipCmd) *Clipboard {
	return &Clipboard{getCmd: c.get, setCmd: c.set, setPrimaryCmd: c.setPrimary}
}
