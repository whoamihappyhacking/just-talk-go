//go:build linux && no_x11

package hotkey

import "fmt"

func newX11Provider() (Provider, error) {
	return nil, fmt.Errorf("X11 backend disabled at build time")
}
