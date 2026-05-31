//go:build darwin && cgo

package clipboard

// #cgo LDFLAGS: -framework AppKit -framework Foundation
// #include <stdlib.h>
// #include "clipboard_darwin.h"
import "C"

import (
	"fmt"
	"unsafe"
)

func newPlatformClipboard() (*Clipboard, error) {
	return &Clipboard{
		getFunc: darwinGet,
		setFunc: darwinSet,
	}, nil
}

func darwinSet(text string) error {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))
	if C.jt_clipboard_set(cText) != 0 {
		return fmt.Errorf("NSPasteboard set failed")
	}
	return nil
}

func darwinGet() (string, error) {
	cText := C.jt_clipboard_get()
	if cText == nil {
		return "", fmt.Errorf("NSPasteboard get failed")
	}
	defer C.free(unsafe.Pointer(cText))
	return C.GoString(cText), nil
}
