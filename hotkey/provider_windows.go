//go:build windows

package hotkey

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

	"golang.org/x/sys/windows"
)

// Windows DLL procedures
var (
	modUser32               = windows.NewLazySystemDLL("user32.dll")
	modKernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procGetAsyncKeyState    = modUser32.NewProc("GetAsyncKeyState")
	procSetWindowsHookExW   = modUser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = modUser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = modUser32.NewProc("CallNextHookEx")
	procGetMessageW         = modUser32.NewProc("GetMessageW")
	procPostThreadMessageW  = modUser32.NewProc("PostThreadMessageW")
	procGetModuleHandleW    = modKernel32.NewProc("GetModuleHandleW")
)

const (
	windowsPollInterval = 5 * time.Millisecond
	windowsHookResetLag = 50 * time.Millisecond
	whKeyboardLL        = 13
	wmQuit              = 0x0012
	llkhfUp             = 0x0080
)

// Windows virtual key codes not in windows package.
const (
	vkControl  = 0x11
	vkMenu     = 0x12 // Alt
	vkShift    = 0x10
	vkLWin     = 0x5B
	vkRWin     = 0x5C
	vkLShift   = 0xA0
	vkRShift   = 0xA1
	vkLControl = 0xA2
	vkRControl = 0xA3
	vkLMenu    = 0xA4
	vkRMenu    = 0xA5

	vkF1  = 0x70
	vkF2  = 0x71
	vkF3  = 0x72
	vkF4  = 0x73
	vkF5  = 0x74
	vkF6  = 0x75
	vkF7  = 0x76
	vkF8  = 0x77
	vkF9  = 0x78
	vkF10 = 0x79
	vkF11 = 0x7A
	vkF12 = 0x7B
	vkF13 = 0x7C
	vkF14 = 0x7D
	vkF15 = 0x7E
	vkF16 = 0x7F
	vkF17 = 0x80
	vkF18 = 0x81
	vkF19 = 0x82
	vkF20 = 0x83
	vkF21 = 0x84
	vkF22 = 0x85
	vkF23 = 0x86
	vkF24 = 0x87

	vkNumpad0 = 0x60
	vkNumpad1 = 0x61
	vkNumpad2 = 0x62
	vkNumpad3 = 0x63
	vkNumpad4 = 0x64
	vkNumpad5 = 0x65
	vkNumpad6 = 0x66
	vkNumpad7 = 0x67
	vkNumpad8 = 0x68
	vkNumpad9 = 0x69

	vkSpace    = 0x20
	vkReturn   = 0x0D
	vkBack     = 0x08
	vkTab      = 0x09
	vkEscape   = 0x1B
	vkCapital  = 0x14
	vkUp       = 0x26
	vkDown     = 0x28
	vkLeft     = 0x25
	vkRight    = 0x27
	vkHome     = 0x24
	vkEnd      = 0x23
	vkPrior    = 0x21
	vkNext     = 0x22
	vkInsert   = 0x2D
	vkDelete   = 0x2E
	vkSnapshot = 0x2C
	vkScroll   = 0x91
	vkPause    = 0x13
	vkNumLock  = 0x90
	vkMultiply = 0x6A
	vkAdd      = 0x6B
	vkSubtract = 0x6D
	vkDecimal  = 0x6E
	vkDivide   = 0x6F

	vkOem3      = 0xC0
	vkOemMinus  = 0xBD
	vkOemPlus   = 0xBB
	vkOem4      = 0xDB
	vkOem6      = 0xDD
	vkOem5      = 0xDC
	vkOem1      = 0xBA
	vkOem7      = 0xDE
	vkOemComma  = 0xBC
	vkOemPeriod = 0xBE
	vkOem2      = 0xBF
)

// Windows VK → unified KeyCode.
var winVKToKey = map[uint32]KeyCode{
	'A': KeyA, 'B': KeyB, 'C': KeyC, 'D': KeyD, 'E': KeyE,
	'F': KeyF, 'G': KeyG, 'H': KeyH, 'I': KeyI, 'J': KeyJ,
	'K': KeyK, 'L': KeyL, 'M': KeyM, 'N': KeyN, 'O': KeyO,
	'P': KeyP, 'Q': KeyQ, 'R': KeyR, 'S': KeyS, 'T': KeyT,
	'U': KeyU, 'V': KeyV, 'W': KeyW, 'X': KeyX, 'Y': KeyY, 'Z': KeyZ,

	'0': Key0, '1': Key1, '2': Key2, '3': Key3, '4': Key4,
	'5': Key5, '6': Key6, '7': Key7, '8': Key8, '9': Key9,

	vkNumpad0: KeyNum0, vkNumpad1: KeyNum1, vkNumpad2: KeyNum2,
	vkNumpad3: KeyNum3, vkNumpad4: KeyNum4, vkNumpad5: KeyNum5,
	vkNumpad6: KeyNum6, vkNumpad7: KeyNum7, vkNumpad8: KeyNum8,
	vkNumpad9: KeyNum9,

	vkControl: KeyCtrl, vkLControl: KeyCtrl, vkRControl: KeyCtrl,
	vkMenu: KeyAlt, vkLMenu: KeyAlt, vkRMenu: KeyAlt,
	vkShift: KeyShift, vkLShift: KeyShift, vkRShift: KeyShift,
	vkLWin: KeySuper, vkRWin: KeySuper,

	vkF1: KeyF1, vkF2: KeyF2, vkF3: KeyF3, vkF4: KeyF4,
	vkF5: KeyF5, vkF6: KeyF6, vkF7: KeyF7, vkF8: KeyF8,
	vkF9: KeyF9, vkF10: KeyF10, vkF11: KeyF11, vkF12: KeyF12,
	vkF13: KeyF13, vkF14: KeyF14, vkF15: KeyF15, vkF16: KeyF16,
	vkF17: KeyF17, vkF18: KeyF18, vkF19: KeyF19, vkF20: KeyF20,
	vkF21: KeyF21, vkF22: KeyF22, vkF23: KeyF23, vkF24: KeyF24,

	vkSpace: KeySpace, vkTab: KeyTab,
	vkReturn: KeyEnter, vkEscape: KeyEscape,
	vkBack: KeyBackspace, vkCapital: KeyCapsLock,
	vkUp: KeyArrowUp, vkDown: KeyArrowDown,
	vkLeft: KeyArrowLeft, vkRight: KeyArrowRight,
	vkHome: KeyHome, vkEnd: KeyEnd,
	vkPrior: KeyPageUp, vkNext: KeyPageDown,
	vkInsert: KeyInsert, vkDelete: KeyDelete,
	vkSnapshot: KeyPrintScreen, vkScroll: KeyScrollLock,
	vkPause: KeyPause, vkNumLock: KeyNumLock,
	vkMultiply: KeyNumMultiply, vkAdd: KeyNumAdd,
	vkSubtract: KeyNumSubtract, vkDecimal: KeyNumDecimal,
	vkDivide: KeyNumDivide,

	vkOem3: KeyBacktick, vkOemMinus: KeyMinus,
	vkOemPlus: KeyEqual, vkOem4: KeyLeftBracket,
	vkOem6: KeyRightBracket, vkOem5: KeyBackslash,
	vkOem1: KeySemicolon, vkOem7: KeyQuote,
	vkOemComma: KeyComma, vkOemPeriod: KeyPeriod,
	vkOem2: KeySlash,
}

// ---- Provider ----

type windowsProvider struct {
	opMu        sync.Mutex
	mu          sync.Mutex
	channels    map[Combo]chan<- Event
	comboState  map[Combo]bool
	pollKeyDown func(KeyCode) bool
	keyDown     func(KeyCode) bool
	lastMods    Modifier
	stopped     bool
	logger      *slog.Logger

	hookMu          sync.RWMutex
	hookDown        map[uint32]bool
	hookMismatch    Modifier
	hookMismatchAt  time.Time
	hookEvents      chan windowsHookDebugEvent
	debugHookEvents bool
	hook            windows.Handle
	hookThreadID    uint32
}

type windowsLowLevelKeyEvent struct {
	VirtualKey uint32
	ScanCode   uint32
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
}

type windowsHookDebugEvent struct {
	VirtualKey uint32
	ScanCode   uint32
	Flags      uint32
	Down       bool
}

type windowsHookPoint struct {
	X int32
	Y int32
}

type windowsHookMessage struct {
	Window  windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   windowsHookPoint
	Private uint32
}

var (
	windowsHookProviderMu sync.RWMutex
	windowsHookProvider   *windowsProvider
	windowsHookCallback   = syscall.NewCallback(windowsLowLevelHookProc)
)

// export windowsNewProvider
func NewProvider() (Provider, error) {
	p := &windowsProvider{
		channels:        make(map[Combo]chan<- Event),
		comboState:      make(map[Combo]bool),
		pollKeyDown:     windowsKeyDown,
		hookDown:        make(map[uint32]bool),
		hookEvents:      make(chan windowsHookDebugEvent, 128),
		debugHookEvents: os.Getenv("JUST_TALK_DEBUG_WINDOWS_KEYS") == "1",
		logger:          slog.Default().With("platform", "windows"),
	}
	p.keyDown = p.combinedKeyDown
	return p, nil
}

func (p *windowsProvider) Register(combo Combo) (<-chan Event, error) {
	return p.RegisterWithOptions(combo, RegisterOptions{})
}

func (p *windowsProvider) RegisterWithOptions(combo Combo, _ RegisterOptions) (<-chan Event, error) {
	p.opMu.Lock()
	defer p.opMu.Unlock()

	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return nil, fmt.Errorf("provider is stopped")
	}
	if _, exists := p.channels[combo]; exists {
		p.mu.Unlock()
		return nil, fmt.Errorf("hotkey %s already registered", combo)
	}

	p.mu.Unlock()

	ch := make(chan Event, 32)
	p.mu.Lock()
	p.channels[combo] = ch
	p.comboState[combo] = false
	p.mu.Unlock()
	return ch, nil
}

func (p *windowsProvider) Unregister(combo Combo) error {
	p.opMu.Lock()
	defer p.opMu.Unlock()

	p.mu.Lock()
	ch, exists := p.channels[combo]
	if !exists {
		p.mu.Unlock()
		return fmt.Errorf("hotkey %s not registered", combo)
	}
	delete(p.channels, combo)
	delete(p.comboState, combo)
	p.mu.Unlock()
	close(ch)
	return nil
}

func (p *windowsProvider) Start(ctx context.Context) error {
	if p.debugHookEvents {
		go p.logKeyboardHookEvents(ctx)
	}
	hookReady := make(chan error, 1)
	hookDone := make(chan struct{})
	go p.runKeyboardHook(ctx, hookReady, hookDone)
	select {
	case err := <-hookReady:
		if err != nil {
			p.logger.Warn("low-level keyboard hook unavailable; continuing with polling", "error", err)
		} else {
			p.logger.Info("low-level keyboard hook started")
		}
	case <-ctx.Done():
		p.requestKeyboardHookStop()
		<-hookDone
		return ctx.Err()
	}
	defer func() {
		p.requestKeyboardHookStop()
		<-hookDone
	}()

	p.logger.Info("global key polling started", "interval", windowsPollInterval, "hook_fallback", true)
	ticker := time.NewTicker(windowsPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			p.poll(now)
		}
	}
}

func (p *windowsProvider) Stop() error {
	p.opMu.Lock()
	defer p.opMu.Unlock()

	p.mu.Lock()

	if p.stopped {
		p.mu.Unlock()
		return nil
	}
	p.stopped = true

	channels := p.channels
	p.channels = make(map[Combo]chan<- Event)
	p.comboState = make(map[Combo]bool)
	p.mu.Unlock()
	p.requestKeyboardHookStop()

	// Close channels after removing them from dispatch.
	for _, ch := range channels {
		close(ch)
	}
	return nil
}

func (p *windowsProvider) Info() ProviderInfo {
	return ProviderInfo{
		Platform: "windows",
		Backend:  "GetAsyncKeyState + WH_KEYBOARD_LL",
		Features: []string{
			FeatureKeyDown, FeatureKeyUp, FeatureKeyPress,
			FeatureModifierOnly, FeatureFunctionKey, FeatureCombo,
		},
	}
}

func (p *windowsProvider) combinedKeyDown(key KeyCode) bool {
	if p.pollKeyDown != nil && p.pollKeyDown(key) {
		return true
	}
	return key.IsModifier() && p.hookKeyDown(key)
}

func (p *windowsProvider) hookKeyDown(key KeyCode) bool {
	p.hookMu.RLock()
	defer p.hookMu.RUnlock()
	for virtualKey, down := range p.hookDown {
		if down && winVKToKey[virtualKey] == key {
			return true
		}
	}
	return false
}

func (p *windowsProvider) setHookVirtualKey(virtualKey uint32, down bool) {
	p.hookMu.Lock()
	if down {
		p.hookDown[virtualKey] = true
	} else {
		delete(p.hookDown, virtualKey)
	}
	p.hookMu.Unlock()
}

func (p *windowsProvider) recordHookEvent(event windowsLowLevelKeyEvent) {
	down := event.Flags&llkhfUp == 0
	p.hookMu.Lock()
	wasDown := p.hookDown[event.VirtualKey]
	if down {
		p.hookDown[event.VirtualKey] = true
	} else {
		delete(p.hookDown, event.VirtualKey)
	}
	modifier := KeyCodeToModifier(winVKToKey[event.VirtualKey])
	if down && !wasDown && p.hookActiveModifiersLocked() != ModNone {
		physicalMods := windowsActiveModifiers(p.pollKeyDown)
		if modifier != ModNone {
			physicalMods |= modifier
		}
		p.reconcileHookModifiersLocked(physicalMods, time.Now(), true)
	}
	p.hookMu.Unlock()
	if wasDown != down && p.debugHookEvents {
		select {
		case p.hookEvents <- windowsHookDebugEvent{
			VirtualKey: event.VirtualKey,
			ScanCode:   event.ScanCode,
			Flags:      event.Flags,
			Down:       down,
		}:
		default:
		}
	}
}

func (p *windowsProvider) hookActiveModifiersLocked() Modifier {
	var mods Modifier
	for virtualKey, down := range p.hookDown {
		if down {
			mods |= KeyCodeToModifier(winVKToKey[virtualKey])
		}
	}
	return mods
}

func (p *windowsProvider) clearHookMismatchLocked() {
	p.hookMismatch = ModNone
	p.hookMismatchAt = time.Time{}
}

func (p *windowsProvider) activeModifiers(now time.Time) Modifier {
	physicalMods := windowsActiveModifiers(p.pollKeyDown)
	p.hookMu.Lock()
	reset := p.reconcileHookModifiersLocked(physicalMods, now, false)
	hookMods := p.hookActiveModifiersLocked()
	p.hookMu.Unlock()
	if reset != ModNone {
		p.logger.Debug("cleared stale hook modifier state", "mods", reset)
	}
	return physicalMods | hookMods
}

func (p *windowsProvider) reconcileHookModifiersLocked(physicalMods Modifier, now time.Time, force bool) Modifier {
	hookMods := p.hookActiveModifiersLocked()
	if hookMods == ModNone {
		p.clearHookMismatchLocked()
		return ModNone
	}
	missing := hookMods &^ physicalMods
	if missing == ModNone {
		p.clearHookMismatchLocked()
		return ModNone
	}
	if !force {
		if missing != p.hookMismatch || p.hookMismatchAt.IsZero() {
			p.hookMismatch = missing
			p.hookMismatchAt = now
			return ModNone
		}
		if now.Sub(p.hookMismatchAt) < windowsHookResetLag {
			return ModNone
		}
	}
	for virtualKey := range p.hookDown {
		if KeyCodeToModifier(winVKToKey[virtualKey])&missing != ModNone {
			delete(p.hookDown, virtualKey)
		}
	}
	p.clearHookMismatchLocked()
	return missing
}

func (p *windowsProvider) logKeyboardHookEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-p.hookEvents:
			p.logger.Debug("low-level keyboard event",
				"virtual_key", fmt.Sprintf("0x%02X", event.VirtualKey),
				"scan_code", fmt.Sprintf("0x%02X", event.ScanCode),
				"flags", fmt.Sprintf("0x%02X", event.Flags),
				"down", event.Down,
				"mapped_key", winVKToKey[event.VirtualKey],
			)
		}
	}
}

func (p *windowsProvider) runKeyboardHook(ctx context.Context, ready chan<- error, done chan<- struct{}) {
	defer close(done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	threadID := windows.GetCurrentThreadId()
	p.hookMu.Lock()
	p.hookThreadID = threadID
	p.hookMu.Unlock()

	windowsHookProviderMu.Lock()
	windowsHookProvider = p
	windowsHookProviderMu.Unlock()
	defer func() {
		windowsHookProviderMu.Lock()
		if windowsHookProvider == p {
			windowsHookProvider = nil
		}
		windowsHookProviderMu.Unlock()
		p.hookMu.Lock()
		p.hook = 0
		p.hookThreadID = 0
		p.hookDown = make(map[uint32]bool)
		p.clearHookMismatchLocked()
		p.hookMu.Unlock()
	}()

	module, _, _ := procGetModuleHandleW.Call(0)
	hook, _, err := procSetWindowsHookExW.Call(whKeyboardLL, windowsHookCallback, module, 0)
	if hook == 0 {
		ready <- fmt.Errorf("SetWindowsHookExW: %w", err)
		return
	}
	p.hookMu.Lock()
	p.hook = windows.Handle(hook)
	p.hookMu.Unlock()
	defer procUnhookWindowsHookEx.Call(hook)
	ready <- nil

	hookCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-hookCtx.Done()
		procPostThreadMessageW.Call(uintptr(threadID), wmQuit, 0, 0)
	}()

	var msg windowsHookMessage
	for {
		result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if result == 0 || result == ^uintptr(0) {
			return
		}
	}
}

func (p *windowsProvider) requestKeyboardHookStop() {
	p.hookMu.RLock()
	threadID := p.hookThreadID
	p.hookMu.RUnlock()
	if threadID != 0 {
		procPostThreadMessageW.Call(uintptr(threadID), wmQuit, 0, 0)
	}
}

func windowsLowLevelHookProc(nCode int32, wParam, lParam uintptr) uintptr {
	if nCode >= 0 && lParam != 0 {
		event := (*windowsLowLevelKeyEvent)(unsafe.Pointer(lParam))
		windowsHookProviderMu.RLock()
		provider := windowsHookProvider
		windowsHookProviderMu.RUnlock()
		if provider != nil {
			provider.recordHookEvent(*event)
		}
	}
	result, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return result
}

func (p *windowsProvider) poll(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	mods := p.activeModifiers(now)
	if mods != p.lastMods {
		p.logger.Debug("global modifier state changed", "mods", mods)
		p.lastMods = mods
	}
	for combo, ch := range p.channels {
		active := windowsComboActiveWithModifiers(combo, mods, p.keyDown)
		if p.comboState[combo] == active {
			continue
		}
		p.comboState[combo] = active
		eventType := KeyUp
		if active {
			eventType = KeyDown
		}
		select {
		case ch <- Event{Combo: combo, Type: eventType, Time: now}:
		default:
		}
	}
}

func windowsComboActive(combo Combo, keyDown func(KeyCode) bool) bool {
	return windowsComboActiveWithModifiers(combo, windowsActiveModifiers(keyDown), keyDown)
}

func windowsComboActiveWithModifiers(combo Combo, mods Modifier, keyDown func(KeyCode) bool) bool {
	if combo.IsModifierOnly() {
		return mods == combo.Mods
	}
	if !keyDown(combo.Key) {
		return false
	}
	if combo.IsKeyOnly() {
		return mods == ModNone
	}
	return mods == combo.Mods
}

func windowsActiveModifiers(keyDown func(KeyCode) bool) Modifier {
	if keyDown == nil {
		return ModNone
	}
	var mods Modifier
	if keyDown(KeyCtrl) {
		mods |= ModCtrl
	}
	if keyDown(KeyAlt) {
		mods |= ModAlt
	}
	if keyDown(KeyShift) {
		mods |= ModShift
	}
	if keyDown(KeySuper) {
		mods |= ModSuper
	}
	return mods
}

func windowsKeyDown(key KeyCode) bool {
	for virtualKey, mapped := range winVKToKey {
		if mapped != key {
			continue
		}
		state, _, _ := procGetAsyncKeyState.Call(uintptr(virtualKey))
		if uint16(state)&0x8000 != 0 {
			return true
		}
	}
	return false
}
