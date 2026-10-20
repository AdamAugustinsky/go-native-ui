package platform

// Linux key identity (untagged: the tables are pure, so their tests run on every
// host). Letters and digits are identified by physical position
// (the evdev keycode, named after the US layout), as the Key contract asks;
// everything else is layout independent already and comes from the keysym.
// Code reports the evdev keycode on both backends (X11 keycode - 8).

// evdevKeys maps evdev keycodes (linux/input-event-codes.h: KEY_1=2 ..
// KEY_0=11, KEY_Q=16 .., KEY_A=30 .., KEY_Z=44 ..) to letter and digit keys.
var evdevKeys [128]Key

func init() {
	for i, c := range "qwertyuiop" {
		evdevKeys[16+i] = KeyA + Key(c-'a')
	}
	for i, c := range "asdfghjkl" {
		evdevKeys[30+i] = KeyA + Key(c-'a')
	}
	for i, c := range "zxcvbnm" {
		evdevKeys[44+i] = KeyA + Key(c-'a')
	}
	for i := 0; i < 9; i++ { // KEY_1..KEY_9
		evdevKeys[2+i] = Key0 + 1 + Key(i)
	}
	evdevKeys[11] = Key0 // KEY_0
	// Punctuation, named after the US layout like the letters: KEY_MINUS 12,
	// KEY_EQUAL 13, KEY_LEFTBRACE 26, KEY_RIGHTBRACE 27, KEY_SEMICOLON 39,
	// KEY_APOSTROPHE 40, KEY_GRAVE 41, KEY_BACKSLASH 43, KEY_COMMA 51,
	// KEY_DOT 52, KEY_SLASH 53.
	evdevKeys[12], evdevKeys[13] = KeyMinus, KeyEqual
	evdevKeys[26], evdevKeys[27] = KeyLeftBracket, KeyRightBracket
	evdevKeys[39], evdevKeys[40], evdevKeys[41], evdevKeys[43] = KeySemicolon, KeyQuote, KeyGrave, KeyBackslash
	evdevKeys[51], evdevKeys[52], evdevKeys[53] = KeyComma, KeyPeriod, KeySlash
}

// X keysyms (X11/keysymdef.h).
const (
	xkSpace      = 0x0020
	xkBackSpace  = 0xff08
	xkTab        = 0xff09
	xkReturn     = 0xff0d
	xkEscape     = 0xff1b
	xkHome       = 0xff50
	xkLeft       = 0xff51
	xkUp         = 0xff52
	xkRight      = 0xff53
	xkDown       = 0xff54
	xkPrior      = 0xff55
	xkNext       = 0xff56
	xkEnd        = 0xff57
	xkKPEnter    = 0xff8d
	xkF1         = 0xffbe
	xkF12        = 0xffc9
	xkDelete     = 0xffff
	xkKP0        = 0xffb0 // KP_0..KP_9 are contiguous
	xkKP9        = 0xffb9
	xkKPMultiply = 0xffaa
	xkKPAdd      = 0xffab
	xkKPSubtract = 0xffad
	xkKPDecimal  = 0xffae
	xkKPDivide   = 0xffaf
	xkKPEqual    = 0xffbd
	xkF20        = 0xffd1 // F13 is xkF12+1; F1..F35 are contiguous
	xkShiftL     = 0xffe1 // Shift_L .. Hyper_R: the modifier keys
	xkHyperR     = 0xffee
	xkISOLeftTab = 0xfe20
)

func linuxKey(evdev, sym uint32) Key {
	if evdev < uint32(len(evdevKeys)) && evdevKeys[evdev] != KeyUnknown {
		return evdevKeys[evdev]
	}
	switch sym {
	case xkEscape:
		return KeyEscape
	case xkReturn, xkKPEnter:
		return KeyEnter
	case xkTab, xkISOLeftTab:
		return KeyTab
	case xkBackSpace:
		return KeyBackspace
	case xkDelete:
		return KeyDelete
	case xkSpace:
		return KeySpace
	case xkLeft:
		return KeyLeft
	case xkRight:
		return KeyRight
	case xkUp:
		return KeyUp
	case xkDown:
		return KeyDown
	case xkHome:
		return KeyHome
	case xkEnd:
		return KeyEnd
	case xkPrior:
		return KeyPageUp
	case xkNext:
		return KeyPageDown
	}
	if sym >= xkF1 && sym <= xkF12 {
		return KeyF1 + Key(sym-xkF1)
	}
	if sym > xkF12 && sym <= xkF20 {
		return KeyF13 + Key(sym-xkF12-1)
	}
	if sym >= xkKP0 && sym <= xkKP9 {
		return KeyNum0 + Key(sym-xkKP0)
	}
	switch sym {
	case xkKPDecimal:
		return KeyNumDecimal
	case xkKPAdd:
		return KeyNumAdd
	case xkKPSubtract:
		return KeyNumSubtract
	case xkKPMultiply:
		return KeyNumMultiply
	case xkKPDivide:
		return KeyNumDivide
	case xkKPEqual:
		return KeyNumEqual
	}
	return KeyUnknown
}

// keysymIsModifier reports whether sym is a modifier key (Shift, Lock, Control,
// Meta, Alt, Super, Hyper), which is EvModifiers rather than EvKey.
func keysymIsModifier(sym uint32) bool { return sym >= xkShiftL && sym <= xkHyperR }

// keysymChar is Event.Char for a keysym: the printable ASCII character, a
// letter lowercased. Latin-1 and Unicode keysyms outside ASCII report 0, like an
// unknown layout character: shortcuts then fall back to the physical key.
func keysymChar(sym uint32) rune {
	if sym < 0x21 || sym > 0x7e {
		return 0
	}
	c := rune(sym)
	if c >= 'A' && c <= 'Z' {
		c += 'a' - 'A'
	}
	return c
}

// pointerButton maps a Linux input-event button code (BTN_LEFT 0x110, ...).
func evdevButton(code uint32) Button {
	switch code {
	case 0x110:
		return ButtonLeft
	case 0x111:
		return ButtonRight
	case 0x112:
		return ButtonMiddle
	case 0x113: // BTN_SIDE
		return ButtonBack
	case 0x114: // BTN_EXTRA
		return ButtonForward
	}
	return ButtonNone
}

// keysymMod is the Mods bit a modifier keysym controls (0 for Caps Lock and the
// others Mods does not carry).
func keysymMod(sym uint32) Mods {
	switch sym {
	case 0xffe1, 0xffe2: // Shift_L, Shift_R
		return ModShift
	case 0xffe3, 0xffe4: // Control_L, Control_R
		return ModCtrl
	case 0xffe7, 0xffe8, 0xffe9, 0xffea: // Meta_L, Meta_R, Alt_L, Alt_R
		return ModAlt
	case 0xffeb, 0xffec: // Super_L, Super_R
		return ModSuper
	}
	return 0
}
