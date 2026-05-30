//go:build linux

package autotype

// #cgo LDFLAGS: -lX11 -lXtst
//
// #include <X11/Xlib.h>
// #include <X11/keysym.h>
// #include <X11/extensions/XTest.h>
// #include <stdlib.h>
// #include <unistd.h>
//
// static void xtest_shift_insert(Display *dpy) {
// 	KeyCode shift = XKeysymToKeycode(dpy, XK_Shift_L);
// 	KeyCode insert = XKeysymToKeycode(dpy, XK_Insert);
// 	if (shift == 0 || insert == 0) return;
//
// 	XTestFakeKeyEvent(dpy, shift, True, 0);
// 	XFlush(dpy);
// 	usleep(15000);
//
// 	XTestFakeKeyEvent(dpy, insert, True, 0);
// 	XFlush(dpy);
// 	usleep(30000);
//
// 	XTestFakeKeyEvent(dpy, insert, False, 0);
// 	XFlush(dpy);
// 	usleep(15000);
//
// 	XTestFakeKeyEvent(dpy, shift, False, 0);
// 	XFlush(dpy);
// }
import "C"

import (
	"fmt"
	"os"
	"os/exec"
)

func isWaylandSession() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland"
}

func simulatePaste() error {
	// Check for Wayland
	if isWaylandSession() {
		return simulatePasteWayland()
	}
	return simulatePasteX11()
}

func simulatePasteX11() error {
	dpy := C.XOpenDisplay(nil)
	if dpy == nil {
		return fmt.Errorf("cannot open X display")
	}
	defer C.XCloseDisplay(dpy)

	C.xtest_shift_insert(dpy)
	return nil
}

func simulatePasteWayland() error {
	// Try ydotool
	if _, err := exec.LookPath("ydotool"); err == nil {
		// ydotool key 42:1 110:1 110:0 42:0  (Shift+Insert)
		return exec.Command("ydotool", "key", "42:1", "110:1", "110:0", "42:0").Run()
	}
	// Try dotool
	if _, err := exec.LookPath("dotool"); err == nil {
		return exec.Command("dotool", "key", "shift+insert").Run()
	}
	// Try wtype
	if _, err := exec.LookPath("wtype"); err == nil {
		return exec.Command("wtype", "-M", "shift", "-k", "Insert", "-m", "shift").Run()
	}
	return fmt.Errorf("no Wayland input tool found (install ydotool, dotool, or wtype)")
}

func pasteMethod() string {
	if isWaylandSession() {
		if _, err := exec.LookPath("ydotool"); err == nil {
			return "wayland/ydotool+Shift+Insert"
		}
		if _, err := exec.LookPath("dotool"); err == nil {
			return "wayland/dotool+Shift+Insert"
		}
		if _, err := exec.LookPath("wtype"); err == nil {
			return "wayland/wtype+Shift+Insert"
		}
		return "wayland/unknown"
	}
	return "x11/XTest+Shift+Insert"
}
