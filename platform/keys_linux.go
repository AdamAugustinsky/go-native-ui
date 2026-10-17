//go:build linux

package platform

// Linux key identity. Letters and digits are identified by physical position
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
	// KEY_LEFTBRACE, KEY_RIGHTBRACE, KEY_DOT
	evdevKeys[26], evdevKeys[27], evdevKeys[52] = KeyLeftBracket, KeyRightBracket, KeyPeriod
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
	return KeyUnknown
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
