//go:build linux

package hotkey

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// evdev event types and codes
const (
	evKey      = 0x01
	evSyn      = 0x00
	keyRelease = 0
	keyPress   = 1
	keyRepeat  = 2

	// evdev grab ioctl
	eviocGrab = 0x40044590
)

// input_event struct as defined in <linux/input.h>
type inputEvent struct {
	Time  syscall.Timeval
	Type  uint16
	Code  uint16
	Value int32
}

// Linux input key codes → unified KeyCode.
// These are the standard linux/input-event-codes.h values.
var evdevKeyToUnified = map[uint16]KeyCode{
	// Letters
	16: KeyQ, 17: KeyW, 18: KeyE, 19: KeyR, 20: KeyT,
	21: KeyY, 22: KeyU, 23: KeyI, 24: KeyO, 25: KeyP,
	30: KeyA, 31: KeyS, 32: KeyD, 33: KeyF, 34: KeyG,
	35: KeyH, 36: KeyJ, 37: KeyK, 38: KeyL,
	44: KeyZ, 45: KeyX, 46: KeyC, 47: KeyV, 48: KeyB,
	49: KeyN, 50: KeyM,

	// Digits
	2: Key1, 3: Key2, 4: Key3, 5: Key4, 6: Key5,
	7: Key6, 8: Key7, 9: Key8, 10: Key9, 11: Key0,

	// Numpad
	71: KeyNum7, 72: KeyNum8, 73: KeyNum9,
	74: KeyNumSubtract,
	75: KeyNum4, 76: KeyNum5, 77: KeyNum6,
	78: KeyNumAdd,
	79: KeyNum1, 80: KeyNum2, 81: KeyNum3,
	82: KeyNum0, 83: KeyNumDecimal,

	// Modifiers (left/right merged)
	29: KeyCtrl, 97: KeyCtrl, // LEFTCTRL, RIGHTCTRL
	56: KeyAlt, 100: KeyAlt, // LEFTALT, RIGHTALT
	42: KeyShift, 54: KeyShift, // LEFTSHIFT, RIGHTSHIFT
	125: KeySuper, 126: KeySuper, // LEFTMETA, RIGHTMETA

	// Function keys
	59: KeyF1, 60: KeyF2, 61: KeyF3, 62: KeyF4,
	63: KeyF5, 64: KeyF6, 65: KeyF7, 66: KeyF8,
	67: KeyF9, 68: KeyF10, 87: KeyF11, 88: KeyF12,
	183: KeyF13, 184: KeyF14, 185: KeyF15, 186: KeyF16,
	187: KeyF17, 188: KeyF18, 189: KeyF19, 190: KeyF20,

	// Navigation
	57: KeySpace, 15: KeyTab,
	28: KeyEnter, 1: KeyEscape,
	14: KeyBackspace, 58: KeyCapsLock,
	103: KeyArrowUp, 108: KeyArrowDown,
	105: KeyArrowLeft, 106: KeyArrowRight,
	102: KeyHome, 107: KeyEnd,
	104: KeyPageUp, 109: KeyPageDown,
	110: KeyInsert, 111: KeyDelete,

	// Punctuation
	41: KeyBacktick, 12: KeyMinus, 13: KeyEqual,
	26: KeyLeftBracket, 27: KeyRightBracket,
	43: KeyBackslash, 39: KeySemicolon, 40: KeyQuote,
	51: KeyComma, 52: KeyPeriod, 53: KeySlash,
}

type waylandProvider struct {
	mu       sync.Mutex
	channels map[Combo]chan<- Event
	tracker  *KeyStateTracker
	stopped  bool

	deviceFds []int

	logger *slog.Logger
}

func newWaylandProvider() (Provider, error) {
	return &waylandProvider{
		channels: make(map[Combo]chan<- Event),
		tracker:  NewKeyStateTracker(),
		logger:   slog.Default().With("platform", "wayland"),
	}, nil
}

func (p *waylandProvider) Register(combo Combo) (<-chan Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stopped {
		return nil, fmt.Errorf("provider is stopped")
	}
	if _, exists := p.channels[combo]; exists {
		return nil, fmt.Errorf("hotkey %s already registered", combo)
	}

	ch := make(chan Event, 32)
	p.channels[combo] = ch
	p.tracker.Watch(combo, ch)
	return ch, nil
}

func (p *waylandProvider) Unregister(combo Combo) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, exists := p.channels[combo]
	if !exists {
		return fmt.Errorf("hotkey %s not registered", combo)
	}

	p.tracker.Unwatch(combo)
	close(ch)
	delete(p.channels, combo)
	return nil
}

func (p *waylandProvider) Start(ctx context.Context) error {
	// Find all keyboard event devices
	devices, err := findKeyboardDevices()
	if err != nil {
		return fmt.Errorf("find keyboard devices: %w", err)
	}
	if len(devices) == 0 {
		return fmt.Errorf("no input event devices found under /dev/input/")
	}

	p.logger.Info("found keyboard devices", "count", len(devices), "devices", devices)

	// Open keyboard devices for passive reads. Do not EVIOCGRAB here: grabbing
	// steals the keyboard from the compositor, which is not acceptable under
	// Sway/Wayland for a background hotkey listener.
	for _, dev := range devices {
		fd, err := unix.Open(dev, unix.O_RDONLY|unix.O_NONBLOCK, 0)
		if err != nil {
			p.logger.Warn("cannot open device", "device", dev, "error", err)
			continue
		}

		p.deviceFds = append(p.deviceFds, fd)
		p.logger.Debug("opened input device", "device", dev)
	}

	if len(p.deviceFds) == 0 {
		return fmt.Errorf("could not open any keyboard devices; add the user to the input group or grant read access to /dev/input/event*")
	}

	p.logger.Info("opened keyboard devices", "count", len(p.deviceFds))

	// Read events from all devices
	return p.readLoop(ctx)
}

func (p *waylandProvider) readLoop(ctx context.Context) error {
	buf := make([]byte, unsafe.Sizeof(inputEvent{}))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Poll each device
		for _, fd := range p.deviceFds {
			n, err := unix.Read(fd, buf)
			if err != nil {
				if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
					continue
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}

			if n < int(unsafe.Sizeof(inputEvent{})) {
				continue
			}

			evt := (*inputEvent)(unsafe.Pointer(&buf[0]))
			p.processEvent(evt)
		}

		// Small sleep to avoid busy-waiting
		// In a production implementation, we'd use epoll/poll instead
		time.Sleep(1 * time.Millisecond)
	}
}

func (p *waylandProvider) processEvent(evt *inputEvent) {
	if evt.Type != evKey {
		return
	}

	key := evdevKeyToUnified[evt.Code]
	if key == KeyNone {
		return
	}

	now := time.Now()

	var events []Event
	switch evt.Value {
	case keyPress:
		events = p.tracker.KeyDown(key, now)
	case keyRelease:
		events = p.tracker.KeyUp(key, now)
	case keyRepeat:
		// Ignore auto-repeat for now
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, e := range events {
		if ch, ok := p.channels[e.Combo]; ok {
			select {
			case ch <- e:
			default:
			}
		}
	}
}

func (p *waylandProvider) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stopped {
		return nil
	}
	p.stopped = true

	for _, fd := range p.deviceFds {
		unix.Close(fd)
	}
	p.deviceFds = nil

	// Close channels
	for c, ch := range p.channels {
		close(ch)
		delete(p.channels, c)
		p.tracker.Unwatch(c)
	}

	return nil
}

func (p *waylandProvider) Info() ProviderInfo {
	return ProviderInfo{
		Platform: "wayland",
		Backend:  "evdev",
		Features: []string{
			FeatureKeyDown, FeatureKeyUp, FeatureKeyPress,
			FeatureModifierOnly, FeatureFunctionKey, FeatureCombo,
			FeatureSuppressEvent,
		},
	}
}

// findKeyboardDevices scans /dev/input/event* for keyboard devices.
func findKeyboardDevices() ([]string, error) {
	entries, err := os.ReadDir("/dev/input/")
	if err != nil {
		return nil, fmt.Errorf("read /dev/input/: %w", err)
	}

	var devices []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "event") {
			continue
		}
		devPath := "/dev/input/" + entry.Name()

		devices = append(devices, devPath)
	}

	return devices, nil
}
