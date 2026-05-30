//go:build darwin && !cgo

package autotype

import "fmt"

func simulatePaste() error {
	return fmt.Errorf("autotype on macOS requires cgo")
}

func pasteMethod() string { return "darwin/unavailable" }

func isWaylandSession() bool { return false }
