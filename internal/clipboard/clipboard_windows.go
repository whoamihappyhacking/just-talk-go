//go:build windows

package clipboard

import systemclipboard "github.com/atotto/clipboard"

func newPlatformClipboard() (*Clipboard, error) {
	return &Clipboard{
		getFunc: systemclipboard.ReadAll,
		setFunc: systemclipboard.WriteAll,
	}, nil
}
