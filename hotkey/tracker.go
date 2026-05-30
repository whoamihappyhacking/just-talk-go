package hotkey

import (
	"sync"
	"time"
)

// keyState represents the current state of a single key.
type keyState struct {
	pressed   bool
	pressedAt time.Time
}

// KeyStateTracker maintains the state of all keyboard keys and modifiers.
// It is used by platform providers that receive low-level key events
// (CGEventTap, WH_KEYBOARD_LL, evdev) to:
//
//  1. Track which keys/modifiers are currently held down
//  2. Detect modifier-only hotkeys (e.g., press and release Ctrl without
//     pressing any other key)
//  3. Detect function-key-only hotkeys
//  4. Detect standard modifier+key combos
//
// The tracker is not safe for concurrent use; the owning provider should
// serialize access (typically all events come from a single event loop thread).
type KeyStateTracker struct {
	mu sync.Mutex

	// Currently pressed keys (both physical and virtual)
	pressed map[KeyCode]keyState

	// Currently active modifier mask
	activeMods Modifier

	// Registered combos that we're watching for
	watched map[Combo]chan<- Event

	// Modifier-only combos that are currently active.
	activeModifierCombos map[Combo]bool

	// Per-key listener: keys that, when pressed alone (no other keys),
	// should fire an event. Used for modifier-only and key-only hotkeys.
	soloWatch map[KeyCode]Combo

	// The last non-modifier key that was pressed. If non-zero when a
	// modifier-only trigger releases, it means the modifier was used in
	// combination, not solo.
	lastNonModKey KeyCode
}

// NewKeyStateTracker creates a new KeyStateTracker.
func NewKeyStateTracker() *KeyStateTracker {
	return &KeyStateTracker{
		pressed:              make(map[KeyCode]keyState),
		watched:              make(map[Combo]chan<- Event),
		activeModifierCombos: make(map[Combo]bool),
		soloWatch:            make(map[KeyCode]Combo),
	}
}

// Watch registers a combo to watch for. When the combo is detected,
// events are sent to the provided channel.
func (t *KeyStateTracker) Watch(combo Combo, ch chan<- Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.watched[combo] = ch

	// If this is a solo hotkey (modifier-only or key-only), register for solo tracking
	if combo.IsModifierOnly() {
		t.activeModifierCombos[combo] = false
	}
	if combo.IsKeyOnly() {
		t.soloWatch[combo.Key] = combo
	}
}

// Unwatch removes a previously watched combo.
func (t *KeyStateTracker) Unwatch(combo Combo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.watched, combo)
	delete(t.activeModifierCombos, combo)

	if combo.IsModifierOnly() {
		return
	}
	if combo.IsKeyOnly() {
		if t.soloWatch[combo.Key] == combo {
			delete(t.soloWatch, combo.Key)
		}
	}
}

// KeyDown should be called when a physical key is pressed.
// It returns a list of events that should be emitted (may be empty).
func (t *KeyStateTracker) KeyDown(key KeyCode, now time.Time) []Event {
	t.mu.Lock()
	defer t.mu.Unlock()

	var events []Event

	// Track key state
	t.pressed[key] = keyState{pressed: true, pressedAt: now}

	// Track modifiers
	if mod := KeyCodeToModifier(key); mod != ModNone {
		t.activeMods |= mod
	}

	// Track last non-modifier key
	if !key.IsModifier() {
		t.lastNonModKey = key
	}

	// Check watched combos: standard modifier+key combos
	if !key.IsModifier() {
		combo := Combo{Mods: t.activeMods, Key: key}
		if _, ok := t.watched[combo]; ok {
			events = append(events, Event{Combo: combo, Type: KeyDown, Time: now})
		}
	} else {
		for combo, active := range t.activeModifierCombos {
			if active {
				continue
			}
			if t.activeMods&combo.Mods == combo.Mods {
				t.activeModifierCombos[combo] = true
				events = append(events, Event{Combo: combo, Type: KeyDown, Time: now})
			}
		}
	}

	// Check solo watched combos that are NOT modifier-only
	// (key-only combos fire on KeyDown)
	if combo, ok := t.soloWatch[key]; ok && !combo.IsModifierOnly() {
		events = append(events, Event{Combo: combo, Type: KeyDown, Time: now})
	}

	return events
}

// KeyUp should be called when a physical key is released.
// It returns a list of events that should be emitted (may be empty).
func (t *KeyStateTracker) KeyUp(key KeyCode, now time.Time) []Event {
	t.mu.Lock()
	defer t.mu.Unlock()

	var events []Event

	// Track key state
	delete(t.pressed, key)

	// Track modifiers
	if mod := KeyCodeToModifier(key); mod != ModNone {
		t.activeMods &^= mod
	}

	// Fire KeyUp for watched combos (non-modifier keys)
	if !key.IsModifier() {
		combo := Combo{Mods: t.activeMods, Key: key}
		if _, ok := t.watched[combo]; ok {
			events = append(events, Event{Combo: combo, Type: KeyUp, Time: now})
		}
	}

	// Fire KeyUp for key-only solo watches
	if combo, ok := t.soloWatch[key]; ok && !combo.IsModifierOnly() {
		events = append(events, Event{Combo: combo, Type: KeyUp, Time: now})
	}

	if key.IsModifier() {
		for combo, active := range t.activeModifierCombos {
			if !active {
				continue
			}
			if t.activeMods&combo.Mods != combo.Mods {
				t.activeModifierCombos[combo] = false
				events = append(events, Event{Combo: combo, Type: KeyUp, Time: now})
			}
		}
	}

	// Reset lastNonModKey if no non-modifier keys are pressed
	hasNonMod := false
	for k := range t.pressed {
		if !k.IsModifier() {
			hasNonMod = true
			break
		}
	}
	if !hasNonMod {
		t.lastNonModKey = KeyNone
	}

	return events
}

// ActiveMods returns the currently active modifier mask.
func (t *KeyStateTracker) ActiveMods() Modifier {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activeMods
}

// IsPressed returns true if the given key is currently pressed.
func (t *KeyStateTracker) IsPressed(key KeyCode) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.pressed[key]
	return ok
}

// Reset clears all state.
func (t *KeyStateTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pressed = make(map[KeyCode]keyState)
	for combo := range t.activeModifierCombos {
		t.activeModifierCombos[combo] = false
	}
	t.activeMods = ModNone
	t.lastNonModKey = KeyNone
}

// modsMatchSolo checks whether the current modifier state matches
// the solo combo's expected modifier mask, accounting for the fact
// that the triggering modifier key has just been released.
func (t *KeyStateTracker) modsMatchSolo(combo Combo) bool {
	// After releasing the trigger modifier, the remaining active mods
	// should be exactly the ones we expect (minus the solo modifier itself).
	// For a single-modifier combo (just Ctrl), after releasing Ctrl,
	// activeMods should be 0.
	// For a multi-modifier combo (Ctrl+Shift), after releasing the last
	// modifier, the remaining should match the rest.
	return t.activeMods&combo.Mods == 0
}

// modifierToKeyCode converts a Modifier mask to the corresponding KeyCode(s).
func (t *KeyStateTracker) modifierToKeyCode(mods Modifier) []KeyCode {
	var keys []KeyCode
	if mods&ModCtrl != 0 {
		keys = append(keys, KeyCtrl)
	}
	if mods&ModAlt != 0 {
		keys = append(keys, KeyAlt)
	}
	if mods&ModShift != 0 {
		keys = append(keys, KeyShift)
	}
	if mods&ModSuper != 0 {
		keys = append(keys, KeySuper)
	}
	return keys
}
