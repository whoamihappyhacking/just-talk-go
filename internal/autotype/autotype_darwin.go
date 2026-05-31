//go:build darwin && cgo

package autotype

// #cgo LDFLAGS: -framework ApplicationServices
//
// #include <ApplicationServices/ApplicationServices.h>
// #include <unistd.h>
//
// static void cgevent_cmd_v(void) {
// 	CGEventRef cmdDown = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)55, true);  // kVK_Command
// 	CGEventRef vDown   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)9, true);   // kVK_ANSI_V
// 	CGEventRef vUp     = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)9, false);
// 	CGEventRef cmdUp   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)55, false);
//
// 	CGEventSetFlags(vDown, kCGEventFlagMaskCommand);
// 	CGEventSetFlags(vUp, kCGEventFlagMaskCommand);
//
// 	CGEventPost(kCGSessionEventTap, cmdDown);
// 	usleep(15000);
// 	CGEventPost(kCGSessionEventTap, vDown);
// 	usleep(30000);
// 	CGEventPost(kCGSessionEventTap, vUp);
// 	usleep(15000);
// 	CGEventPost(kCGSessionEventTap, cmdUp);
//
// 	CFRelease(cmdDown); CFRelease(vDown); CFRelease(vUp); CFRelease(cmdUp);
// }
import "C"

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/c/just-talk-go/internal/clipboard"
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
	C.cgevent_cmd_v()
	return nil
}

func pasteMethod() string { return "darwin/CGEventPost+Cmd+V" }

func isWaylandSession() bool { return false }
