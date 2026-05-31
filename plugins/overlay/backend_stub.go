//go:build (!linux && !darwin) || no_x11

package overlay

import (
	"fmt"

	"github.com/c/just-talk-go/config"
)

func newBackend(cfg config.OverlayConfig) (backend, error) {
	return nil, fmt.Errorf("overlay backend is not implemented for this platform")
}
