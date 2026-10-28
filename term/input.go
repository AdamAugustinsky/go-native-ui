package term

import (
	"runtime"
	"unsafe"

	"gonativeui/internal/ghosttyvt"
)

// Key is a key for EncodeKey: a named key, or KeyNone for a key that types
// text, whose character KeyEvent.Text and Unshifted carry. term imports
// nothing from ui; rexui.Terminal maps ui's keys to these.
type Key uint16

// The named keys.
const (
	// KeyNone means the key types text (KeyEvent.Text, KeyEvent.Unshifted).
	KeyNone Key = iota
	// KeyEnter and the rest are the keys of their names; KeyF1..KeyF12 are
	// contiguous.
	KeyEnter
	KeyTab
	KeyBackspace
	KeyEscape
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyInsert
	KeyDelete
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	// KeySpace is the space bar.
	KeySpace
)

// ghosttyKeys maps the named keys to GhosttyKey (W3C key codes).
var ghosttyKeys = [...]int32{
	KeyEnter: 58, KeyTab: 64, KeyBackspace: 53, KeyEscape: 120,
	KeyUp: 78, KeyDown: 75, KeyLeft: 76, KeyRight: 77,
	KeyHome: 71, KeyEnd: 69, KeyPageUp: 74, KeyPageDown: 73, KeyInsert: 72, KeyDelete: 68,
	KeyF1: 121, KeyF2: 122, KeyF3: 123, KeyF4: 124, KeyF5: 125, KeyF6: 126,
	KeyF7: 127, KeyF8: 128, KeyF9: 129, KeyF10: 130, KeyF11: 131, KeyF12: 132,
	KeySpace: 63,
}

// runeKey is the GhosttyKey of the US-layout key that types r unshifted
// (letters, digits and the punctuation keys), 0 for none: the encoder needs
// it for Ctrl and Alt chords and the Kitty protocol.
func runeKey(r rune) int32 {
	switch {
	case r >= 'a' && r <= 'z':
		return 20 + int32(r-'a')
	case r >= 'A' && r <= 'Z':
		return 20 + int32(r-'A')
	case r >= '0' && r <= '9':
		return 6 + int32(r-'0')
	}
	switch r {
	case '`':
		return 1
	case '\\':
		return 2
	case '[':
		return 3
	case ']':
		return 4
	case ',':
		return 5
	case '=':
		return 16
	case '-':
		return 46
	case '.':
		return 47
	case '\'':
		return 48
	case ';':
		return 49
	case '/':
		return 50
	case ' ':
		return 63
	}
	return 0
}

// Mods are the modifiers of a key or a mouse event.
type Mods uint8

// The modifiers.
const (
	// ModShift, ModAlt, ModCtrl and ModSuper are the keys of their names
	// (Alt is Option on macOS).
	ModShift Mods = 1 << iota
	ModAlt
	ModCtrl
	ModSuper
)

func (m Mods) ghostty() uint16 {
	var g uint16
	if m&ModShift != 0 {
		g |= ghModShift
	}
	if m&ModAlt != 0 {
		g |= ghModAlt
	}
	if m&ModCtrl != 0 {
		g |= ghModCtrl
	}
	if m&ModSuper != 0 {
		g |= ghModSuper
	}
	return g
}

// KeyAction is a key's press, auto-repeat or release.
type KeyAction uint8

// The key actions.
const (
	// KeyPress is the default; KeyRepeat and KeyRelease are reported only
	// to programs that asked for them (the Kitty keyboard protocol).
	KeyPress KeyAction = iota
	KeyRepeat
	KeyRelease
)

// KeyEvent is a key for EncodeKey.
type KeyEvent struct {
	// Key is the named key, or KeyNone for a text key.
	Key Key
	// Text is what the key types with its modifiers (UTF-8; empty for a
	// named key or a chord that types nothing), Unshifted the character it
	// types without them ('c' for Ctrl+C and for Shift+C).
	Text      string
	Unshifted rune
	// Mods are the modifiers held; Action the press, repeat or release.
	Mods   Mods
	Action KeyAction
	// Composing: an input method is composing (the key goes to it).
	Composing bool
}

// EncodeKey appends the bytes the key sends to the program, as Ghostty
// encodes them in the terminal's current modes (application cursor and
// keypad keys, modifyOtherKeys, the Kitty keyboard protocol). Most keys are
// presses; a release appends nothing unless the program asked for releases.
// It allocates nothing beyond dst's growth.
func (t *Terminal) EncodeKey(dst []byte, e KeyEvent) []byte {
	if t.closed {
		return dst
	}
	key := int32(0)
	if int(e.Key) < len(ghosttyKeys) && e.Key != KeyNone {
		key = ghosttyKeys[e.Key]
	} else if e.Unshifted != 0 {
		key = runeKey(e.Unshifted)
	} else if len(e.Text) == 1 {
		key = runeKey(rune(e.Text[0]))
	}
	action := int32(1)
	switch e.Action {
	case KeyRepeat:
		action = 2
	case KeyRelease:
		action = 0
	}
	ev := uint64(t.keyEv)
	t.c().Uint(uint64(t.keyEnc)).Uint(uint64(t.h)).Do(fn(ghosttyvt.KeyEncoderSetoptFromTerminal))
	if t.optionAsAlt { // reset by the line above: the terminal's state does not hold it
		t.i32 = 1 // GHOSTTY_OPTION_AS_ALT_TRUE
		t.c().Uint(uint64(t.keyEnc)).Int32(keyOptMacOptionAsAlt).Ptr(unsafe.Pointer(&t.i32)).Do(fn(ghosttyvt.KeyEncoderSetopt))
	}
	t.c().Uint(ev).Int32(action).Do(fn(ghosttyvt.KeyEventSetAction))
	t.c().Uint(ev).Int32(key).Do(fn(ghosttyvt.KeyEventSetKey))
	t.c().Uint(ev).Uint16(e.Mods.ghostty()).Do(fn(ghosttyvt.KeyEventSetMods))
	t.c().Uint(ev).Bool(e.Composing).Do(fn(ghosttyvt.KeyEventSetComposing))
	t.keyText = append(t.keyText[:0], e.Text...)
	t.keyText = append(t.keyText, 0)
	t.c().Uint(ev).Ptr(unsafe.Pointer(&t.keyText[0])).Uint(uint64(len(e.Text))).Do(fn(ghosttyvt.KeyEventSetUTF8))
	un := e.Unshifted
	if un == 0 && len(e.Text) == 1 && e.Text[0] >= 'A' && e.Text[0] <= 'Z' {
		un = rune(e.Text[0] + 'a' - 'A')
	} else if un == 0 && len(e.Text) == 1 {
		un = rune(e.Text[0])
	}
	t.c().Uint(ev).Uint32(uint32(un)).Do(fn(ghosttyvt.KeyEventSetUnshiftedCodepoint))
	return t.encodeInto(dst, func(buf unsafe.Pointer, n int) int32 {
		return t.c().Uint(uint64(t.keyEnc)).Uint(ev).Ptr(buf).Uint(uint64(n)).Ptr(unsafe.Pointer(&t.u64)).
			Do(fn(ghosttyvt.KeyEncoderEncode)).Int32()
	})
}

// encodeInto runs an encoder into dst's spare capacity, growing it once
// when the encoder reports the size it needs (in t.u64).
func (t *Terminal) encodeInto(dst []byte, enc func(buf unsafe.Pointer, n int) int32) []byte {
	if cap(dst)-len(dst) < 64 {
		dst = append(dst, make([]byte, 64)...)[:len(dst)]
	}
	for range 2 {
		spare := dst[len(dst):cap(dst)]
		t.u64 = 0
		r := enc(unsafe.Pointer(&spare[:1][0]), len(spare))
		if r == resSuccess {
			return dst[:len(dst)+int(t.u64)]
		}
		if r != resOutOfSpace {
			return dst
		}
		dst = append(dst, make([]byte, int(t.u64))...)[:len(dst)]
	}
	return dst
}

// MouseButton is a mouse button, or a wheel direction.
type MouseButton uint8

// The mouse buttons.
const (
	// MouseNone is motion with no button held.
	MouseNone MouseButton = iota
	// MouseLeft, MouseMiddle and MouseRight are the buttons of their names.
	MouseLeft
	MouseMiddle
	MouseRight
	// WheelUp, WheelDown, WheelLeft and WheelRight are a wheel's notches
	// (buttons 4 to 7), sent as presses.
	WheelUp
	WheelDown
	WheelLeft
	WheelRight
)

// MouseAction is what the mouse did.
type MouseAction uint8

// The mouse actions.
const (
	// MousePressed, MouseReleased and MouseMoved are the actions of their
	// names.
	MousePressed MouseAction = iota
	MouseReleased
	MouseMoved
)

// MouseEvent is a pointer event for EncodeMouse.
type MouseEvent struct {
	// Action and Button are what happened; Mods the modifiers held.
	Action MouseAction
	Button MouseButton
	Mods   Mods
	// X and Y are the pointer's position in pixels from the grid's top left
	// corner (the cell size is Resize's).
	X, Y float32
	// AnyPressed: a button is held (for motion reports in mode 1002).
	AnyPressed bool
}

// mouseFloats is false where clib cannot pass the float pair the encoder's
// position takes: Windows on arm64 (clib's SyscallN path has no float
// registers there).
var mouseFloats = !(runtime.GOOS == "windows" && runtime.GOARCH == "arm64")

// EncodeMouse appends the report of e, if the program's mouse mode reports
// it, in the encoding it asked for (X10, UTF-8, SGR, urxvt, SGR pixels).
// On Windows on arm64 it appends nothing: the library takes the position
// as a pair of floats, which clib cannot pass there yet.
func (t *Terminal) EncodeMouse(dst []byte, e MouseEvent) []byte {
	if t.closed || !mouseFloats {
		return dst
	}
	ev := uint64(t.mouseEv)
	t.c().Uint(uint64(t.mouseEnc)).Uint(uint64(t.h)).Do(fn(ghosttyvt.MouseEncoderSetoptFromTerminal))
	t.flag = e.AnyPressed || (e.Action == MouseMoved && e.Button != MouseNone)
	t.c().Uint(uint64(t.mouseEnc)).Int32(mouseOptAnyPressed).Ptr(unsafe.Pointer(&t.flag)).Do(fn(ghosttyvt.MouseEncoderSetopt))
	t.c().Uint(ev).Int32(int32(e.Action)).Do(fn(ghosttyvt.MouseEventSetAction))
	switch e.Button {
	case MouseNone:
		t.c().Uint(ev).Do(fn(ghosttyvt.MouseEventClearButton))
	default:
		b := [...]int32{MouseLeft: 1, MouseRight: 2, MouseMiddle: 3, WheelUp: 4, WheelDown: 5, WheelLeft: 6, WheelRight: 7}[e.Button]
		t.c().Uint(ev).Int32(b).Do(fn(ghosttyvt.MouseEventSetButton))
	}
	t.c().Uint(ev).Uint16(e.Mods.ghostty()).Do(fn(ghosttyvt.MouseEventSetMods))
	t.c().Uint(ev).Float32x2(e.X, e.Y).Do(fn(ghosttyvt.MouseEventSetPosition))
	return t.encodeInto(dst, func(buf unsafe.Pointer, n int) int32 {
		return t.c().Uint(uint64(t.mouseEnc)).Uint(ev).Ptr(buf).Uint(uint64(n)).Ptr(unsafe.Pointer(&t.u64)).
			Do(fn(ghosttyvt.MouseEncoderEncode)).Int32()
	})
}

// EncodeFocus appends a focus report (ESC [I, ESC [O) when the program
// asked for them (mode 1004).
func (t *Terminal) EncodeFocus(dst []byte, focused bool) []byte {
	if t.closed || !t.mode(1004, false) {
		return dst
	}
	ev := int32(1)
	if focused {
		ev = 0
	}
	return t.encodeInto(dst, func(buf unsafe.Pointer, n int) int32 {
		return t.c().Int32(ev).Ptr(buf).Uint(uint64(n)).Ptr(unsafe.Pointer(&t.u64)).Do(fn(ghosttyvt.FocusEncode)).Int32()
	})
}

// EncodePaste appends pasted text as the program should receive it:
// bracketed (ESC [200~ … ESC [201~) when it asked for bracketed paste,
// with newlines as carriage returns when it did not, and with control
// bytes that could end the paste or run commands (ESC, NUL, DEL…) replaced
// by spaces.
func (t *Terminal) EncodePaste(dst, text []byte) []byte {
	if t.closed || len(text) == 0 {
		return dst
	}
	bracketed := t.mode(2004, false)
	t.pasteBuf = append(t.pasteBuf[:0], text...) // encoded in place by the library
	return t.encodeInto(dst, func(buf unsafe.Pointer, n int) int32 {
		copy(t.pasteBuf, text)
		return t.c().Ptr(unsafe.Pointer(&t.pasteBuf[0])).Uint(uint64(len(t.pasteBuf))).Bool(bracketed).Ptr(buf).Uint(uint64(n)).
			Ptr(unsafe.Pointer(&t.u64)).Do(fn(ghosttyvt.PasteEncode)).Int32()
	})
}

// PasteSafe reports whether text is safe to paste without asking the user:
// false when it holds a newline (it would run a command) or control bytes.
func (t *Terminal) PasteSafe(text []byte) bool {
	if t.closed || len(text) == 0 {
		return true
	}
	return t.c().Ptr(unsafe.Pointer(&text[0])).Uint(uint64(len(text))).Do(fn(ghosttyvt.PasteIsSafe)).Bool()
}
