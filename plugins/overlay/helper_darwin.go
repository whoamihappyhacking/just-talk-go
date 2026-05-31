//go:build darwin && cgo

package overlay

// #cgo CFLAGS: -fblocks
// #cgo LDFLAGS: -framework AppKit -framework Foundation
// #include <stdlib.h>
// #include "overlay_darwin.h"
import "C"

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"unsafe"
)

type helperCommand struct {
	Cmd   string `json:"cmd"`
	Label string `json:"label,omitempty"`
	R     uint16 `json:"r,omitempty"`
	G     uint16 `json:"g,omitempty"`
	B     uint16 `json:"b,omitempty"`
}

func RunHelper(position string, scale float64, input io.Reader) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	pos := C.CString(position)
	defer C.free(unsafe.Pointer(pos))

	C.jt_overlay_helper_init(pos, C.double(scale))
	go readHelperCommands(input)
	C.jt_overlay_helper_run_app()
	return nil
}

func readHelperCommands(input io.Reader) {
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		var cmd helperCommand
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			fmt.Fprintf(os.Stderr, "overlay helper command parse error: %v\n", err)
			continue
		}
		switch cmd.Cmd {
		case "show":
			label := C.CString(cmd.Label)
			C.jt_overlay_helper_show(label, C.ushort(cmd.R), C.ushort(cmd.G), C.ushort(cmd.B))
			C.free(unsafe.Pointer(label))
		case "hide":
			C.jt_overlay_helper_hide()
		case "close":
			C.jt_overlay_helper_close()
			return
		}
	}
	C.jt_overlay_helper_close()
}
