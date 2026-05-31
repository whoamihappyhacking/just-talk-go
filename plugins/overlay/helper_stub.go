//go:build !darwin

package overlay

import (
	"fmt"
	"io"
)

func RunHelper(position string, scale float64, input io.Reader) error {
	return fmt.Errorf("overlay helper is only available on macOS")
}
