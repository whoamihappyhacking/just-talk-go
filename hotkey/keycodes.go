package hotkey

// KeyCode constants provide a cross-platform unified key code namespace.
//
// The codes are organized in blocks:
//
//	0        - KeyNone (reserved)
//	1..255   - Regular keys (A-Z, 0-9, punctuation — mapped to platform scan/virtual codes)
//	256..289  - Modifier virtual keys (Ctrl, Alt, Shift, Super, and their sides)
//	290..313  - Function keys (F1-F24)
//	314..349  - Navigation keys (arrows, home, end, etc.)
//	350..    - Reserved for future expansion
const (
	KeyNone KeyCode = 0
)

const (
	// ---- Letters (1..26) ----
	KeyA KeyCode = 1 + iota
	KeyB
	KeyC
	KeyD
	KeyE
	KeyF
	KeyG
	KeyH
	KeyI
	KeyJ
	KeyK
	KeyL
	KeyM
	KeyN
	KeyO
	KeyP
	KeyQ
	KeyR
	KeyS
	KeyT
	KeyU
	KeyV
	KeyW
	KeyX
	KeyY
	KeyZ
)

const (
	// ---- Digits (27..36) ----
	Key0 KeyCode = 27 + iota
	Key1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9
)

const (
	// ---- Numpad digits (37..46) ----
	KeyNum0 KeyCode = 37 + iota
	KeyNum1
	KeyNum2
	KeyNum3
	KeyNum4
	KeyNum5
	KeyNum6
	KeyNum7
	KeyNum8
	KeyNum9
)

const (
	// ---- Punctuation & symbols (47..65) ----
	KeySpace        KeyCode = 47
	KeyBacktick     KeyCode = 48 // `
	KeyMinus        KeyCode = 49 // -
	KeyEqual        KeyCode = 50 // =
	KeyLeftBracket  KeyCode = 51 // [
	KeyRightBracket KeyCode = 52 // ]
	KeyBackslash    KeyCode = 53 // \
	KeySemicolon    KeyCode = 54 // ;
	KeyQuote        KeyCode = 55 // '
	KeyComma        KeyCode = 56 // ,
	KeyPeriod       KeyCode = 57 // .
	KeySlash        KeyCode = 58 // /
	KeyTab          KeyCode = 59
	KeyEnter        KeyCode = 60
	KeyEscape       KeyCode = 61
	KeyBackspace    KeyCode = 62
	KeyCapsLock     KeyCode = 63
	KeyPrintScreen  KeyCode = 64
	KeyScrollLock   KeyCode = 65
	KeyPause        KeyCode = 66
	KeyInsert       KeyCode = 67
	KeyDelete       KeyCode = 68
	KeyHome         KeyCode = 69
	KeyEnd          KeyCode = 70
	KeyPageUp       KeyCode = 71
	KeyPageDown     KeyCode = 72
)

const (
	// ---- Arrow keys (73..76) ----
	KeyArrowUp    KeyCode = 73
	KeyArrowDown  KeyCode = 74
	KeyArrowLeft  KeyCode = 75
	KeyArrowRight KeyCode = 76
)

const (
	// ---- Numpad operators (77..82) ----
	KeyNumMultiply KeyCode = 77 // *
	KeyNumAdd      KeyCode = 78 // +
	KeyNumSubtract KeyCode = 79 // -
	KeyNumDivide   KeyCode = 80 // /
	KeyNumDecimal  KeyCode = 81 // .
	KeyNumEnter    KeyCode = 82
	KeyNumLock     KeyCode = 83
)

// ---- Modifier virtual keys (256..267) ----
// These are virtual key codes for the modifier keys themselves,
// used when registering modifier-only hotkeys. The side-specific codes
// identify the physical key; the generic codes are for sources that only
// report a modifier family.
const (
	KeyCtrl  KeyCode = 256
	KeyAlt   KeyCode = 257
	KeyShift KeyCode = 258
	KeySuper KeyCode = 259

	KeyLeftCtrl   KeyCode = 260
	KeyRightCtrl  KeyCode = 261
	KeyLeftAlt    KeyCode = 262
	KeyRightAlt   KeyCode = 263
	KeyLeftShift  KeyCode = 264
	KeyRightShift KeyCode = 265
	KeyLeftSuper  KeyCode = 266
	KeyRightSuper KeyCode = 267
)

// ---- Function keys (290..313) ----
const (
	KeyF1  KeyCode = 290
	KeyF2  KeyCode = 291
	KeyF3  KeyCode = 292
	KeyF4  KeyCode = 293
	KeyF5  KeyCode = 294
	KeyF6  KeyCode = 295
	KeyF7  KeyCode = 296
	KeyF8  KeyCode = 297
	KeyF9  KeyCode = 298
	KeyF10 KeyCode = 299
	KeyF11 KeyCode = 300
	KeyF12 KeyCode = 301
	KeyF13 KeyCode = 302
	KeyF14 KeyCode = 303
	KeyF15 KeyCode = 304
	KeyF16 KeyCode = 305
	KeyF17 KeyCode = 306
	KeyF18 KeyCode = 307
	KeyF19 KeyCode = 308
	KeyF20 KeyCode = 309
	KeyF21 KeyCode = 310
	KeyF22 KeyCode = 311
	KeyF23 KeyCode = 312
	KeyF24 KeyCode = 313
)

// keyNames maps KeyCode to human-readable name.
var keyNames = map[KeyCode]string{
	KeyNone: "None",

	KeyA: "A", KeyB: "B", KeyC: "C", KeyD: "D", KeyE: "E",
	KeyF: "F", KeyG: "G", KeyH: "H", KeyI: "I", KeyJ: "J",
	KeyK: "K", KeyL: "L", KeyM: "M", KeyN: "N", KeyO: "O",
	KeyP: "P", KeyQ: "Q", KeyR: "R", KeyS: "S", KeyT: "T",
	KeyU: "U", KeyV: "V", KeyW: "W", KeyX: "X", KeyY: "Y", KeyZ: "Z",

	Key0: "0", Key1: "1", Key2: "2", Key3: "3", Key4: "4",
	Key5: "5", Key6: "6", Key7: "7", Key8: "8", Key9: "9",

	KeyNum0: "Num0", KeyNum1: "Num1", KeyNum2: "Num2", KeyNum3: "Num3", KeyNum4: "Num4",
	KeyNum5: "Num5", KeyNum6: "Num6", KeyNum7: "Num7", KeyNum8: "Num8", KeyNum9: "Num9",

	KeySpace:        "Space",
	KeyBacktick:     "`",
	KeyMinus:        "-",
	KeyEqual:        "=",
	KeyLeftBracket:  "[",
	KeyRightBracket: "]",
	KeyBackslash:    "\\",
	KeySemicolon:    ";",
	KeyQuote:        "'",
	KeyComma:        ",",
	KeyPeriod:       ".",
	KeySlash:        "/",
	KeyTab:          "Tab",
	KeyEnter:        "Enter",
	KeyEscape:       "Escape",
	KeyBackspace:    "Backspace",
	KeyCapsLock:     "CapsLock",
	KeyPrintScreen:  "PrintScreen",
	KeyScrollLock:   "ScrollLock",
	KeyPause:        "Pause",
	KeyInsert:       "Insert",
	KeyDelete:       "Delete",
	KeyHome:         "Home",
	KeyEnd:          "End",
	KeyPageUp:       "PageUp",
	KeyPageDown:     "PageDown",

	KeyArrowUp:    "Up",
	KeyArrowDown:  "Down",
	KeyArrowLeft:  "Left",
	KeyArrowRight: "Right",

	KeyNumMultiply: "Num*",
	KeyNumAdd:      "Num+",
	KeyNumSubtract: "Num-",
	KeyNumDivide:   "Num/",
	KeyNumDecimal:  "Num.",
	KeyNumEnter:    "NumEnter",
	KeyNumLock:     "NumLock",

	KeyCtrl:  "Ctrl",
	KeyAlt:   "Alt",
	KeyShift: "Shift",
	KeySuper: "Super",

	KeyLeftCtrl:   "LeftCtrl",
	KeyRightCtrl:  "RightCtrl",
	KeyLeftAlt:    "LeftAlt",
	KeyRightAlt:   "RightAlt",
	KeyLeftShift:  "LeftShift",
	KeyRightShift: "RightShift",
	KeyLeftSuper:  "LeftSuper",
	KeyRightSuper: "RightSuper",

	KeyF1: "F1", KeyF2: "F2", KeyF3: "F3", KeyF4: "F4",
	KeyF5: "F5", KeyF6: "F6", KeyF7: "F7", KeyF8: "F8",
	KeyF9: "F9", KeyF10: "F10", KeyF11: "F11", KeyF12: "F12",
	KeyF13: "F13", KeyF14: "F14", KeyF15: "F15", KeyF16: "F16",
	KeyF17: "F17", KeyF18: "F18", KeyF19: "F19", KeyF20: "F20",
	KeyF21: "F21", KeyF22: "F22", KeyF23: "F23", KeyF24: "F24",
}

// String returns a human-readable name for the key code.
func (k KeyCode) String() string {
	if name, ok := keyNames[k]; ok {
		return name
	}
	return "Unknown"
}

// IsModifier returns true if the key code is a modifier virtual key.
func (k KeyCode) IsModifier() bool {
	return k >= KeyCtrl && k <= KeyRightSuper
}

// IsFunctionKey returns true if the key code is a function key (F1-F24).
func (k KeyCode) IsFunctionKey() bool {
	return k >= KeyF1 && k <= KeyF24
}

// IsTextKey returns true for keys that normally produce text when pressed in
// a focused input field.
func (k KeyCode) IsTextKey() bool {
	if k >= KeyA && k <= KeyZ {
		return true
	}
	if k >= Key0 && k <= Key9 {
		return true
	}
	if k >= KeyNum0 && k <= KeyNum9 {
		return true
	}
	switch k {
	case KeySpace, KeyBacktick, KeyMinus, KeyEqual,
		KeyLeftBracket, KeyRightBracket, KeyBackslash,
		KeySemicolon, KeyQuote, KeyComma, KeyPeriod, KeySlash,
		KeyNumMultiply, KeyNumAdd, KeyNumSubtract, KeyNumDivide, KeyNumDecimal:
		return true
	default:
		return false
	}
}

// KeyCodeToModifier converts a modifier KeyCode to the corresponding Modifier bits.
// Side-specific keys include their side bit. Returns ModNone if the key code
// is not a modifier.
func KeyCodeToModifier(k KeyCode) Modifier {
	switch k {
	case KeyCtrl:
		return ModCtrl
	case KeyAlt:
		return ModAlt
	case KeyShift:
		return ModShift
	case KeySuper:
		return ModSuper
	case KeyLeftCtrl:
		return ModLeftCtrl
	case KeyRightCtrl:
		return ModRightCtrl
	case KeyLeftAlt:
		return ModLeftAlt
	case KeyRightAlt:
		return ModRightAlt
	case KeyLeftShift:
		return ModLeftShift
	case KeyRightShift:
		return ModRightShift
	case KeyLeftSuper:
		return ModLeftSuper
	case KeyRightSuper:
		return ModRightSuper
	default:
		return ModNone
	}
}

// coversKey reports whether a held key satisfies a query for want: the
// same key, or any side of a generic modifier family.
func (k KeyCode) coversKey(want KeyCode) bool {
	if k == want {
		return true
	}
	wantMod := KeyCodeToModifier(want)
	return wantMod != ModNone && !wantMod.HasSide() && KeyCodeToModifier(k).Families() == wantMod
}
