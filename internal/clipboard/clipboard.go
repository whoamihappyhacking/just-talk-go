// Package clipboard provides cross-platform clipboard operations.
package clipboard

import (
	"context"
	"time"
)

// Clipboard provides Get/Set operations on the system clipboard.
type Clipboard struct {
	getCmd         []string
	setCmd         []string
	setPrimaryCmd  []string // X11 primary selection, nil if unsupported
	getFunc        func() (string, error)
	setFunc        func(string) error
	setPrimaryFunc func(string) error
}

// New creates a platform-specific Clipboard.
func New() (*Clipboard, error) {
	return newPlatformClipboard()
}

// Get reads the current clipboard content.
func (c *Clipboard) Get() (string, error) {
	if c.getFunc != nil {
		return c.getFunc()
	}
	return runCmd(c.getCmd)
}

// Set writes text to the clipboard.
func (c *Clipboard) Set(text string) error {
	if c.setFunc != nil {
		return c.setFunc(text)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runCmdWithInput(ctx, c.setCmd, text)
	return err
}

// SetPrimary writes text to the X11 primary selection (if supported).
func (c *Clipboard) SetPrimary(text string) error {
	if c.setPrimaryFunc != nil {
		return c.setPrimaryFunc(text)
	}
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

// NewFromCmd creates a Clipboard from explicit commands (for testing).
func newFromCmd(c clipCmd) *Clipboard {
	return &Clipboard{getCmd: c.get, setCmd: c.set, setPrimaryCmd: c.setPrimary}
}
