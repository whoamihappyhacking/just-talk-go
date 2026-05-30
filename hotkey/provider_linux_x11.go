//go:build linux && !no_x11

package hotkey

// #cgo LDFLAGS: -lX11
//
// #include <X11/Xlib.h>
// #include <X11/keysym.h>
// #include <X11/XKBlib.h>
// #include <stdlib.h>
// #include <string.h>
// #include <stdio.h>
//
// // ---- X11 error handler (prevents crash on BadAccess) ----
// static int x11_error_handler(Display *dpy, XErrorEvent *evt) {
// 	// Silently ignore X11 errors (BadAccess on grab is expected when
// 	// another client holds a more specific grab for the same keycode).
// 	(void)dpy; (void)evt;
// 	return 0;
// }
//
// static void x11_install_handler() {
// 	XSetErrorHandler(x11_error_handler);
// }
//
// // ---- Safe XGrabKey that doesn't crash on BadAccess ----
// static int x11_safe_grab_key(Display *dpy, int keycode, unsigned int modifiers, Window grab_window) {
// 	// owner_events=False: events always reported to us, won't fail on other clients' active grabs.
// 	// GrabModeAsync: keyboard event processing continues normally for other clients.
// 	XGrabKey(dpy, keycode, modifiers, grab_window, False, GrabModeAsync, GrabModeAsync);
// 	XSync(dpy, False);
// 	return 0;
// }
//
// static KeySym x11_keysym_from_name(const char *name) {
// 	return XStringToKeysym(name);
// }
import "C"

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// X11 modifier masks.
const (
	x11ShiftMask   = C.ShiftMask
	x11LockMask    = C.LockMask
	x11ControlMask = C.ControlMask
	x11Mod1Mask    = C.Mod1Mask // Alt
	x11Mod2Mask    = C.Mod2Mask // NumLock
	x11Mod3Mask    = C.Mod3Mask // ScrollLock
	x11Mod4Mask    = C.Mod4Mask // Super/Win
	x11Mod5Mask    = C.Mod5Mask
	x11AnyModifier = C.AnyModifier
)

// Ignored modifier masks for CapsLock/NumLock/ScrollLock tolerance.
// When grabbing a key, we must grab it with all combinations of these
// "lock" modifiers so the hotkey works regardless of lock state.
var x11IgnoredMasks = []uint{
	0,           // No lock modifier — the base case, most important
	x11LockMask, // CapsLock
	x11Mod2Mask, // NumLock
	x11Mod3Mask, // ScrollLock
	x11LockMask | x11Mod2Mask,
	x11LockMask | x11Mod3Mask,
	x11Mod2Mask | x11Mod3Mask,
	x11LockMask | x11Mod2Mask | x11Mod3Mask,
}

// X11 keysym → unified KeyCode.
var x11KeysymToUnified = map[C.KeySym]KeyCode{
	C.XK_a: KeyA, C.XK_b: KeyB, C.XK_c: KeyC, C.XK_d: KeyD, C.XK_e: KeyE,
	C.XK_f: KeyF, C.XK_g: KeyG, C.XK_h: KeyH, C.XK_i: KeyI, C.XK_j: KeyJ,
	C.XK_k: KeyK, C.XK_l: KeyL, C.XK_m: KeyM, C.XK_n: KeyN, C.XK_o: KeyO,
	C.XK_p: KeyP, C.XK_q: KeyQ, C.XK_r: KeyR, C.XK_s: KeyS, C.XK_t: KeyT,
	C.XK_u: KeyU, C.XK_v: KeyV, C.XK_w: KeyW, C.XK_x: KeyX, C.XK_y: KeyY, C.XK_z: KeyZ,

	C.XK_0: Key0, C.XK_1: Key1, C.XK_2: Key2, C.XK_3: Key3, C.XK_4: Key4,
	C.XK_5: Key5, C.XK_6: Key6, C.XK_7: Key7, C.XK_8: Key8, C.XK_9: Key9,

	C.XK_Control_L: KeyCtrl, C.XK_Control_R: KeyCtrl,
	C.XK_Alt_L: KeyAlt, C.XK_Alt_R: KeyAlt,
	C.XK_Shift_L: KeyShift, C.XK_Shift_R: KeyShift,
	C.XK_Super_L: KeySuper, C.XK_Super_R: KeySuper,

	C.XK_F1: KeyF1, C.XK_F2: KeyF2, C.XK_F3: KeyF3, C.XK_F4: KeyF4,
	C.XK_F5: KeyF5, C.XK_F6: KeyF6, C.XK_F7: KeyF7, C.XK_F8: KeyF8,
	C.XK_F9: KeyF9, C.XK_F10: KeyF10, C.XK_F11: KeyF11, C.XK_F12: KeyF12,
	C.XK_F13: KeyF13, C.XK_F14: KeyF14, C.XK_F15: KeyF15, C.XK_F16: KeyF16,
	C.XK_F17: KeyF17, C.XK_F18: KeyF18, C.XK_F19: KeyF19, C.XK_F20: KeyF20,
	C.XK_F21: KeyF21, C.XK_F22: KeyF22, C.XK_F23: KeyF23, C.XK_F24: KeyF24,

	C.XK_space: KeySpace, C.XK_Tab: KeyTab,
	C.XK_Return: KeyEnter, C.XK_Escape: KeyEscape,
	C.XK_BackSpace: KeyBackspace, C.XK_Caps_Lock: KeyCapsLock,
	C.XK_Up: KeyArrowUp, C.XK_Down: KeyArrowDown,
	C.XK_Left: KeyArrowLeft, C.XK_Right: KeyArrowRight,
	C.XK_Home: KeyHome, C.XK_End: KeyEnd,
	C.XK_Prior: KeyPageUp, C.XK_Next: KeyPageDown,
	C.XK_Insert: KeyInsert, C.XK_Delete: KeyDelete,

	C.XK_grave: KeyBacktick, C.XK_minus: KeyMinus,
	C.XK_equal: KeyEqual, C.XK_bracketleft: KeyLeftBracket,
	C.XK_bracketright: KeyRightBracket, C.XK_backslash: KeyBackslash,
	C.XK_semicolon: KeySemicolon, C.XK_apostrophe: KeyQuote,
	C.XK_comma: KeyComma, C.XK_period: KeyPeriod,
	C.XK_slash: KeySlash,
}

type x11Provider struct {
	mu       sync.Mutex
	channels map[Combo]chan<- Event
	stopped  bool

	display *C.Display
	root    C.Window

	// Track pressed keys to filter auto-repeat
	pressedKeys map[uint]bool

	// Track grabbed keycodes for cleanup
	grabbedKeys map[uint]bool

	// Pipe for signaling the event loop to stop
	stopFd int

	logger *slog.Logger
}

func newX11Provider() (Provider, error) {
	// Install X error handler to prevent BadAccess crashes
	C.x11_install_handler()

	display := C.XOpenDisplay(nil)
	if display == nil {
		return nil, fmt.Errorf("cannot open X display (DISPLAY=%s)", getEnvOrDefault("DISPLAY", "(unset)"))
	}

	return &x11Provider{
		channels:    make(map[Combo]chan<- Event),
		display:     display,
		root:        C.XDefaultRootWindow(display),
		pressedKeys: make(map[uint]bool),
		grabbedKeys: make(map[uint]bool),
		logger:      slog.Default().With("platform", "x11"),
	}, nil
}

func (p *x11Provider) Register(combo Combo) (<-chan Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stopped {
		return nil, fmt.Errorf("provider is stopped")
	}
	if _, exists := p.channels[combo]; exists {
		return nil, fmt.Errorf("hotkey %s already registered", combo)
	}

	ch := make(chan Event, 32) // buffered to avoid dropping events before dispatch goroutine starts
	p.channels[combo] = ch

	// Get all needed grabs for this combo
	grabs := p.comboToX11Grabs(combo)
	if len(grabs) == 0 {
		close(ch)
		delete(p.channels, combo)
		return nil, fmt.Errorf("unsupported key for X11: %s", combo)
	}

	// For each grab, register with all ignored modifier masks (CapsLock/NumLock tolerance).
	// Batch all grabs then sync once to avoid 64+ X server round-trips.
	for _, g := range grabs {
		for _, ignored := range x11IgnoredMasks {
			C.x11_safe_grab_key(p.display, C.int(g.keycode), C.uint(g.modMask|ignored), p.root)
		}
		p.grabbedKeys[uint(g.keycode)] = true
	}

	return ch, nil
}

func (p *x11Provider) Unregister(combo Combo) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, exists := p.channels[combo]
	if !exists {
		return fmt.Errorf("hotkey %s not registered", combo)
	}

	grabs := p.comboToX11Grabs(combo)
	// Release all grabs for this combo
	p.releaseGrabs(grabs)

	close(ch)
	delete(p.channels, combo)
	return nil
}

func (p *x11Provider) Start(ctx context.Context) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Create a pipe for stop signaling
	fds := make([]int, 2)
	if err := syscall.Pipe(fds); err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	p.stopFd = fds[0]

	// Flush pending X requests
	C.XFlush(p.display)

	// Get connection file descriptor for polling
	connFd := C.XConnectionNumber(p.display)

	// Watch for both X events and stop signal
	go func() {
		<-ctx.Done()
		// Write anything to unblock select/poll
		syscall.Write(fds[1], []byte{0})
	}()

	p.logger.Info("X11 event loop started")

	var event C.XEvent
	for {
		// Check if we have pending events
		pending := C.XPending(p.display)
		if pending > 0 {
			C.XNextEvent(p.display, &event)
			p.handleXEvent(&event)
			continue
		}

		// Poll with timeout to avoid busy-wait
		time.Sleep(10 * time.Millisecond)

		// Check context
		select {
		case <-ctx.Done():
			// Clean shutdown: release grabs, flush, close display
			p.mu.Lock()
			for combo := range p.channels {
				grabs := p.comboToX11Grabs(combo)
				p.releaseGrabs(grabs)
			}
			p.mu.Unlock()
			C.XFlush(p.display)
			C.XCloseDisplay(p.display)
			p.display = nil
			syscall.Close(p.stopFd)
			_ = fds[1]
			return ctx.Err()
		default:
		}

		// Drain any connection data
		_ = connFd
	}

	// Unreachable — kept for safety
}

func (p *x11Provider) handleXEvent(event *C.XEvent) {
	evtType := (*C.int)(unsafe.Pointer(event))
	switch *evtType {
	case C.KeyPress:
		p.handleKeyEvent(event, false)
	case C.KeyRelease:
		p.handleKeyEvent(event, true)
	}
}

func (p *x11Provider) handleKeyEvent(event *C.XEvent, isRelease bool) {
	// Access XKeyEvent fields
	keyEvt := (*C.XKeyEvent)(unsafe.Pointer(event))
	keycode := keyEvt.keycode
	state := keyEvt.state

	// Convert keycode to keysym before filtering releases. For modifier-only
	// grabs, one modifier may have been pressed before the passive grab became
	// active, so we can legitimately receive its KeyRelease without seeing its
	// KeyPress.
	keysym := C.XkbKeycodeToKeysym(p.display, C.KeyCode(keycode), 0, 0)
	key := x11KeysymToUnified[keysym]
	if key == KeyNone {
		return
	}

	// AutoRepeat: on X11, auto-repeat generates KeyRelease+KeyPress pairs.
	// We ignore auto-repeat KeyRelease by checking if the key is currently marked pressed.
	if isRelease {
		if !p.pressedKeys[uint(keycode)] {
			if !key.IsModifier() {
				// This is the auto-repeat "fake" release — ignore
				return
			}
		} else {
			delete(p.pressedKeys, uint(keycode))
		}
	} else {
		if p.pressedKeys[uint(keycode)] {
			// Auto-repeat press — ignore
			return
		}
		p.pressedKeys[uint(keycode)] = true
	}

	// Convert X11 state to Modifier
	mods := x11StateToMods(uint(state))

	now := time.Now()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Check registered combos
	for combo, ch := range p.channels {
		if p.comboMatches(combo, key, mods) {
			evt := Event{
				Combo: combo,
				Time:  now,
			}
			if isRelease {
				evt.Type = KeyUp
			} else {
				evt.Type = KeyDown
			}
			select {
			case ch <- evt:
			default:
			}
		}
	}
}

func (p *x11Provider) comboMatches(combo Combo, key KeyCode, mods Modifier) bool {
	// For modifier-only combos: check if the pressed key is one of the
	// required modifiers, and the full set of held modifiers matches.
	if combo.IsModifierOnly() {
		keyMod := KeyCodeToModifier(key)
		if keyMod == ModNone {
			return false
		}
		// The triggering key must be part of the combo
		if combo.Mods&keyMod == 0 {
			return false
		}
		// mods = modifiers held BEFORE this key event.
		// The full set of held modifiers = mods + keyMod.
		actualMods := mods | keyMod
		return actualMods == combo.Mods
	}

	// For standard combos: match key and modifiers exactly
	if combo.Key == key && combo.Mods == mods {
		return true
	}

	return false
}

func (p *x11Provider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stopped {
		return nil
	}
	p.stopped = true

	// Release all grabs for all combos
	for combo := range p.channels {
		grabs := p.comboToX11Grabs(combo)
		p.releaseGrabs(grabs)
	}
	for combo, ch := range p.channels {
		close(ch)
		delete(p.channels, combo)
	}

	return nil
}

func (p *x11Provider) Info() ProviderInfo {
	return ProviderInfo{
		Platform: "x11",
		Backend:  "XGrabKey",
		Features: []string{
			FeatureKeyDown, FeatureKeyUp, FeatureKeyPress,
			FeatureCombo, FeatureFunctionKey,
			// Note: modifier-only support is limited on X11 (see comboMatches)
			FeatureModifierOnly,
		},
	}
}

// ---- Helpers ----

// x11Grab specifies a single XGrabKey call.
type x11Grab struct {
	keycode C.KeyCode
	modMask uint
}

// comboToX11Grabs returns the list of XGrabKey calls needed for a combo.
// Multi-modifier-only combos produce multiple grabs (one per modifier as
// trigger key, with the remaining modifiers as the grab mask).
func (p *x11Provider) comboToX11Grabs(combo Combo) []x11Grab {
	if combo.IsModifierOnly() {
		return p.modifierOnlyGrabs(combo.Mods)
	}

	// Standard combo: find keysym and grab with full modifier mask
	var keysym C.KeySym
	for ks, uk := range x11KeysymToUnified {
		if uk == combo.Key && !uk.IsModifier() {
			keysym = ks
			break
		}
	}
	if keysym == 0 {
		return nil
	}

	keycode := C.XKeysymToKeycode(p.display, keysym)
	if keycode == 0 {
		return nil
	}

	return []x11Grab{{keycode: keycode, modMask: modsToX11Mask(combo.Mods)}}
}

// modifierOnlyGrabs returns grabs for a modifier-only combo.
// For single modifiers: grab the modifier key with mask=0.
// For multi-modifiers (e.g., Super+Alt): grab each modifier as the trigger
// key with all other modifiers as the grab mask. This covers all press orders.
func (p *x11Provider) modifierOnlyGrabs(mods Modifier) []x11Grab {
	modKeys := modifierBits(mods)
	if len(modKeys) == 0 {
		return nil
	}

	var grabs []x11Grab
	for i, trigger := range modKeys {
		// Other modifiers form the grab mask
		var prefixMask uint
		for j, other := range modKeys {
			if i != j {
				prefixMask |= modsToX11Mask(other)
			}
		}
		keysyms := modifierToKeysyms(trigger)
		if len(keysyms) == 0 {
			continue
		}
		for _, keysym := range keysyms {
			keycode := C.XKeysymToKeycode(p.display, keysym)
			if keycode == 0 {
				continue
			}
			grabs = append(grabs, x11Grab{keycode: keycode, modMask: prefixMask})
		}
	}
	return grabs
}

// modifierBits returns each individual Modifier bit in the mask as a slice.
func modifierBits(mods Modifier) []Modifier {
	var bits []Modifier
	for _, m := range []Modifier{ModCtrl, ModAlt, ModShift, ModSuper} {
		if mods&m != 0 {
			bits = append(bits, m)
		}
	}
	return bits
}

// modifierToKeysyms maps a single Modifier bit to all matching X11 keysyms.
func modifierToKeysyms(m Modifier) []C.KeySym {
	switch m {
	case ModCtrl:
		return []C.KeySym{C.XK_Control_L, C.XK_Control_R}
	case ModAlt:
		return []C.KeySym{C.XK_Alt_L, C.XK_Alt_R}
	case ModShift:
		return []C.KeySym{C.XK_Shift_L, C.XK_Shift_R}
	case ModSuper:
		return []C.KeySym{C.XK_Super_L, C.XK_Super_R}
	}
	return nil
}

// Legacy: kept for Unregister compatibility
func (p *x11Provider) comboToX11(combo Combo) (C.KeyCode, uint) {
	grabs := p.comboToX11Grabs(combo)
	if len(grabs) == 0 {
		return 0, 0
	}
	return grabs[0].keycode, grabs[0].modMask
}

func modsToX11Mask(mods Modifier) uint {
	var mask uint
	if mods&ModCtrl != 0 {
		mask |= x11ControlMask
	}
	if mods&ModAlt != 0 {
		mask |= x11Mod1Mask
	}
	if mods&ModShift != 0 {
		mask |= x11ShiftMask
	}
	if mods&ModSuper != 0 {
		mask |= x11Mod4Mask
	}
	return mask
}

func x11StateToMods(state uint) Modifier {
	var mods Modifier
	if state&x11ControlMask != 0 {
		mods |= ModCtrl
	}
	if state&x11Mod1Mask != 0 {
		mods |= ModAlt
	}
	if state&x11ShiftMask != 0 {
		mods |= ModShift
	}
	if state&x11Mod4Mask != 0 {
		mods |= ModSuper
	}
	return mods
}

// releaseGrabs ungrabs all XGrabKey registrations in the slice.
func (p *x11Provider) releaseGrabs(grabs []x11Grab) {
	for _, g := range grabs {
		for _, ignored := range x11IgnoredMasks {
			C.XUngrabKey(p.display, C.int(g.keycode), C.uint(g.modMask|ignored), p.root)
		}
		delete(p.grabbedKeys, uint(g.keycode))
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
