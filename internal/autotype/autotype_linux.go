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
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bendahl/uinput"
)

const (
	uinputDev  = "/dev/uinput"
	keyDelayMs = 15
)

func isWaylandSession() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland"
}

func pastePlatform(text string, logger *slog.Logger) error {
	if isWaylandSession() {
		return pasteWayland(text, logger)
	}
	return pasteX11(text, logger)
}

// On X11: sets PRIMARY + CLIPBOARD via xclip, simulates Shift+Insert, restores.
func pasteX11(text string, logger *slog.Logger) error {
	orig, _ := runClipboard("xclip", "-o", "-selection", "clipboard")

	if err := pipeToCmd(text, "xclip", "-selection", "clipboard"); err != nil {
		return fmt.Errorf("set clipboard: %w", err)
	}

	primaryCmd := exec.Command("xclip", "-selection", "primary")
	primaryIn, err := primaryCmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("primary pipe: %w", err)
	}
	primaryCmd.Start()
	primaryIn.Write([]byte(text))
	primaryIn.Close()

	time.Sleep(50 * time.Millisecond)
	if err := simulatePaste(); err != nil {
		primaryCmd.Process.Kill()
		primaryCmd.Wait()
		return fmt.Errorf("simulate paste: %w", err)
	}

	time.Sleep(300 * time.Millisecond)
	primaryCmd.Process.Kill()
	primaryCmd.Wait()

	if orig != "" && orig != text {
		pipeToCmd(orig, "xclip", "-selection", "clipboard")
	}

	logger.Debug("autotype done", "text_len", len(text))
	return nil
}

func pasteWayland(text string, logger *slog.Logger) error {
	if err := pipeToCmd(text, "wl-copy", "--type", "text/plain;charset=utf-8"); err != nil {
		return fmt.Errorf("set Wayland clipboard: %w", err)
	}
	if !isKDEPlasma() {
		if err := pipeToCmd(text, "wl-copy", "--primary", "--type", "text/plain;charset=utf-8"); err != nil {
			return fmt.Errorf("set Wayland primary selection: %w", err)
		}
	}

	time.Sleep(50 * time.Millisecond)
	if err := simulatePaste(); err != nil {
		return fmt.Errorf("simulate paste: %w", err)
	}

	logger.Debug("autotype done", "text_len", len(text), "method", pasteMethod())
	return nil
}

func runClipboard(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func pipeToCmd(input string, args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := stdin.Write([]byte(input)); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return err
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
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
	if !isKDEPlasma() {
		if _, err := exec.LookPath("wtype"); err == nil {
			if err := exec.Command("wtype", "-M", "shift", "-k", "Insert", "-m", "shift").Run(); err == nil {
				return nil
			}
		}
	}
	return simulatePasteUinput()
}

func isKDEPlasma() bool {
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + " " + os.Getenv("DESKTOP_SESSION"))
	return strings.Contains(desktop, "kde") || strings.Contains(desktop, "plasma") || os.Getenv("KDE_SESSION_VERSION") != ""
}

func simulatePasteUinput() error {
	keyboard, err := uinput.CreateKeyboard(uinputDev, []byte("just-talk virtual keyboard"))
	if err != nil {
		return fmt.Errorf("create uinput keyboard: %w", err)
	}
	defer keyboard.Close()

	time.Sleep(80 * time.Millisecond)
	if err := keyboard.KeyDown(uinput.KeyLeftshift); err != nil {
		return fmt.Errorf("uinput shift down: %w", err)
	}
	time.Sleep(keyDelayMs * time.Millisecond)
	if err := keyboard.KeyDown(uinput.KeyInsert); err != nil {
		return fmt.Errorf("uinput insert down: %w", err)
	}
	time.Sleep(keyDelayMs * time.Millisecond)
	if err := keyboard.KeyUp(uinput.KeyInsert); err != nil {
		return fmt.Errorf("uinput insert up: %w", err)
	}
	time.Sleep(keyDelayMs * time.Millisecond)
	if err := keyboard.KeyUp(uinput.KeyLeftshift); err != nil {
		return fmt.Errorf("uinput shift up: %w", err)
	}
	return nil
}

func pasteMethod() string {
	if isWaylandSession() {
		if !isKDEPlasma() {
			if _, err := exec.LookPath("wtype"); err == nil {
				return "wayland/wtype+Shift+Insert"
			}
		}
		return "wayland/uinput+Shift+Insert"
	}
	return "x11/XTest+Shift+Insert"
}
