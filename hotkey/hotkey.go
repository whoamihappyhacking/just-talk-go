// Package hotkey provides cross-platform global hotkey registration and event handling.
//
// Supports four platforms: X11, Wayland, macOS, Windows.
// Each platform has its own provider implementation selected via build tags.
//
// Key features:
//   - Modifier-only hotkeys (e.g., pressing just Ctrl)
//   - Function-key-only hotkeys (e.g., pressing just F1)
//   - Full combinations (e.g., Ctrl+Shift+F)
//   - KeyDown, KeyUp, and KeyPress event types for push-to-talk scenarios
package hotkey

import (
	"strings"
	"time"
)

// Modifier represents modifier key bitmask.
//
// Each modifier has a family bit (ModAlt) and optional side bits. A family
// bit alone accepts either physical key; ModRightAlt carries both the family
// bit and the right-side bit, so it only accepts the right key while still
// satisfying family checks such as mods&ModAlt != 0.
type Modifier uint32

const (
	ModNone  Modifier = 0
	ModCtrl  Modifier = 1 << iota // 2
	ModAlt                        // 4
	ModShift                      // 8
	ModSuper                      // 16 (Win / Cmd)
)

const (
	sideLeftCtrl Modifier = 1 << (iota + 5)
	sideRightCtrl
	sideLeftAlt
	sideRightAlt
	sideLeftShift
	sideRightShift
	sideLeftSuper
	sideRightSuper
)

// Side-specific modifiers.
const (
	ModLeftCtrl   = ModCtrl | sideLeftCtrl
	ModRightCtrl  = ModCtrl | sideRightCtrl
	ModLeftAlt    = ModAlt | sideLeftAlt
	ModRightAlt   = ModAlt | sideRightAlt
	ModLeftShift  = ModShift | sideLeftShift
	ModRightShift = ModShift | sideRightShift
	ModLeftSuper  = ModSuper | sideLeftSuper
	ModRightSuper = ModSuper | sideRightSuper
)

const (
	modFamilyMask = ModCtrl | ModAlt | ModShift | ModSuper
	modSideMask   = sideLeftCtrl | sideRightCtrl | sideLeftAlt | sideRightAlt |
		sideLeftShift | sideRightShift | sideLeftSuper | sideRightSuper
)

type modifierFamily struct {
	name        string
	mod         Modifier
	left, right Modifier
}

var modifierFamilies = []modifierFamily{
	{"Ctrl", ModCtrl, ModLeftCtrl, ModRightCtrl},
	{"Alt", ModAlt, ModLeftAlt, ModRightAlt},
	{"Shift", ModShift, ModLeftShift, ModRightShift},
	{"Super", ModSuper, ModLeftSuper, ModRightSuper},
}

// Families returns m without side information.
func (m Modifier) Families() Modifier { return m & modFamilyMask }

// HasSide reports whether m requires a specific physical side for any family.
func (m Modifier) HasSide() bool { return m&modSideMask != 0 }

// Includes reports whether held contains every modifier, and every side,
// that required names. Extra held modifiers are ignored.
func (held Modifier) Includes(required Modifier) bool {
	return held&required == required
}

// Satisfies reports whether held matches required exactly: the same
// modifier families are held, and every side that required names is held.
// Unsided requirements accept either physical key.
func (held Modifier) Satisfies(required Modifier) bool {
	return held.Families() == required.Families() && held.Includes(required)
}

// withUnknownSides marks both sides as held for every family that carries
// no side information, for comparisons against sided state.
func (m Modifier) withUnknownSides() Modifier {
	for _, f := range modifierFamilies {
		if m&f.mod != 0 && m&(f.left|f.right)&modSideMask == 0 {
			m |= f.left | f.right
		}
	}
	return m
}

// String returns a human-readable representation of the modifier mask.
func (m Modifier) String() string {
	if m == ModNone {
		return "None"
	}
	var parts []string
	for _, f := range modifierFamilies {
		if m&f.mod == 0 {
			continue
		}
		left, right := m.Includes(f.left), m.Includes(f.right)
		if left {
			parts = append(parts, "Left"+f.name)
		}
		if right {
			parts = append(parts, "Right"+f.name)
		}
		if !left && !right {
			parts = append(parts, f.name)
		}
	}
	return strings.Join(parts, "+")
}

// KeyCode is a unified key code across all platforms.
type KeyCode uint32

// Combo defines a hotkey combination.
//
// Special cases:
//
//	Combo{ModNone, KeyF1}        → F1 alone
//	Combo{ModCtrl, KeyNone}      → Ctrl alone (modifier-only)
//	Combo{ModCtrl, KeyF}         → Ctrl+F
//	Combo{ModCtrl|ModShift, KeyNone} → Ctrl+Shift
type Combo struct {
	Mods Modifier
	Key  KeyCode
}

// IsModifierOnly returns true if this combo is a modifier-only hotkey.
func (c Combo) IsModifierOnly() bool {
	return c.Key == KeyNone && c.Mods != ModNone
}

// IsKeyOnly returns true if this combo is a key-only (no modifiers) hotkey.
func (c Combo) IsKeyOnly() bool {
	return c.Key != KeyNone && c.Mods == ModNone
}

// String returns a human-readable representation of the combo.
func (c Combo) String() string {
	if c.IsModifierOnly() {
		return c.Mods.String()
	}
	if c.IsKeyOnly() {
		return c.Key.String()
	}
	return c.Mods.String() + "+" + c.Key.String()
}

// EventType describes the type of hotkey event.
type EventType int

const (
	KeyDown  EventType = iota // Key pressed down
	KeyUp                     // Key released
	KeyPress                  // Key pressed and quickly released (simulates a "click")
)

// String returns a human-readable event type name.
func (e EventType) String() string {
	switch e {
	case KeyDown:
		return "KeyDown"
	case KeyUp:
		return "KeyUp"
	case KeyPress:
		return "KeyPress"
	default:
		return "Unknown"
	}
}

// Event represents a hotkey event.
type Event struct {
	Combo Combo
	Type  EventType
	Time  time.Time
}

// releaseEndsCombo reports whether releasing key ends an active combo, for
// providers that see key edges but not the full held-key state. A modifier
// release only counts when it is the side the combo requires; releasing the
// unused left Alt must not stop a RightAlt hotkey.
func releaseEndsCombo(combo Combo, key KeyCode) bool {
	if combo.Key == key {
		return true
	}
	mod := KeyCodeToModifier(key)
	if mod == ModNone || combo.Mods&mod.Families() == 0 {
		return false
	}
	required := combo.Mods.sidesOf(mod)
	return required == 0 || !mod.HasSide() || mod&required != 0
}

// sidesOf returns the side bits m names for the family of mod.
func (m Modifier) sidesOf(mod Modifier) Modifier {
	for _, f := range modifierFamilies {
		if mod&f.mod != 0 {
			return m & (f.left | f.right) & modSideMask
		}
	}
	return ModNone
}
