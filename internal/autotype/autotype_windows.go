//go:build windows

package autotype

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"time"
	"unsafe"

	"github.com/c/just-talk-go/internal/clipboard"
	"golang.org/x/sys/windows"
)

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
)

const (
	inputKeyboard  = 1
	keyeventfKeyUp = 2
)

func pastePlatform(text string, logger *slog.Logger) error {
	cb, err := clipboard.New()
	if err != nil {
		return fmt.Errorf("clipboard: %w", err)
	}
	if err := cb.Set(text); err != nil {
		return fmt.Errorf("set clipboard: %w", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := simulatePaste(); err != nil {
		return fmt.Errorf("simulate paste: %w", err)
	}
	logger.Debug("autotype done", "text_len", len(text), "method", pasteMethod())
	return nil
}

func simulatePaste() error {
	// Simulate Ctrl down → V down → V up → Ctrl up
	keys := []struct {
		code uint16
		up   bool
	}{
		{0x11, false}, // VK_CONTROL down
		{0x56, false}, // VK_V down
		{0x56, true},  // VK_V up
		{0x11, true},  // VK_CONTROL up
	}

	inputSize := 28
	dataOffset := 4
	extraInfoOffset := 12
	if unsafe.Sizeof(uintptr(0)) == 8 {
		inputSize = 40
		dataOffset = 8
		extraInfoOffset = 16
	}
	inputs := make([]byte, inputSize*len(keys))
	for i, k := range keys {
		flags := uint32(0)
		if k.up {
			flags = keyeventfKeyUp
		}
		base := i * inputSize
		binary.LittleEndian.PutUint32(inputs[base:], inputKeyboard)
		binary.LittleEndian.PutUint16(inputs[base+dataOffset:], k.code)
		binary.LittleEndian.PutUint32(inputs[base+dataOffset+4:], flags)
		if unsafe.Sizeof(uintptr(0)) == 8 {
			binary.LittleEndian.PutUint64(inputs[base+dataOffset+extraInfoOffset:], 0)
		} else {
			binary.LittleEndian.PutUint32(inputs[base+dataOffset+extraInfoOffset:], 0)
		}
	}

	sent, _, err := procSendInput.Call(
		uintptr(len(keys)),
		uintptr(unsafe.Pointer(&inputs[0])), uintptr(inputSize),
	)
	if sent != uintptr(len(keys)) {
		return fmt.Errorf("SendInput inserted %d of %d keyboard events: %w", sent, len(keys), err)
	}
	return nil
}

func pasteMethod() string { return "windows/SendInput+Ctrl+V" }

func isWaylandSession() bool { return false }
