//go:build darwin

package platform

import (
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Keyboard: key events and modifier flags. registerViewKeyboard adds the
// view's key methods to GNUIView; the input method side (insertText:, marked
// text) is in textinput_darwin.go.

var (
	selKeyCode          = objc.Sel("keyCode")
	selIsARepeat        = objc.Sel("isARepeat")
	selArrayWithObject  = objc.Sel("arrayWithObject:")
	selInterpretKeys    = objc.Sel("interpretKeyEvents:")
	selAcceptsFirstResp = objc.Sel("acceptsFirstResponder")
	selKeyDown          = objc.Sel("keyDown:")
	selKeyUp            = objc.Sel("keyUp:")
	selFlagsChanged     = objc.Sel("flagsChanged:")
)

func registerViewKeyboard(v *objc.ClassDef) {
	v.Method(selAcceptsFirstResp, "c@:", viewAcceptsFirstResponder)
	v.Method(selKeyDown, "v@:@", viewKeyDown)
	v.Method(selKeyUp, "v@:@", viewSwallowKey)
	v.Method(selFlagsChanged, "v@:@", viewSwallowKey)
}

func viewAcceptsFirstResponder(c *ffi.CB) { c.RetBool(true) }

// viewKeyDown hands the key to the input method (interpretKeyEvents:), which
// calls back insertText:, setMarkedText: or doCommandBySelector:.
func viewKeyDown(c *ffi.CB) {
	arr := objc.Send1(clsNSArray, selArrayWithObject, c.Int(2))
	objc.Send1(objc.ID(c.Int(0)), selInterpretKeys, uintptr(arr))
}

// viewSwallowKey is keyUp: and flagsChanged:, swallowed so that they do not beep.
func viewSwallowKey(*ffi.CB) {}

// keyEvent queues the EvKey of a key-down or key-up event and returns the
// index of a key-down's in the queue (-1 for a key-up).
func (w *Window) keyEvent(ev objc.ID, typ uintptr, t int64) (keyIdx int) {
	keyIdx = -1
	code := uint32(objc.Send(ev, selKeyCode)) & 0xFFFF
	m := mods(uintptr(objc.Send(ev, selModifierFlags)))
	e := Event{Kind: EvKey, Key: macKey(code), Code: code, Mods: m, Time: t}
	if typ == 10 {
		e.Flags = FlagDown
		if objc.SendBool(ev, selIsARepeat) {
			e.Flags |= FlagRepeat
		}
	}
	w.q.push(e)
	if typ == 10 {
		keyIdx = len(w.q.ev) - 1
	}
	if typ == 10 && m == ModSuper && code == 0x0C { // Cmd+Q (there is no menu yet)
		w.q.push(Event{Kind: EvClose, Time: t})
	}
	return keyIdx
}

func mods(flags uintptr) Mods {
	var m Mods
	if flags&(1<<17) != 0 {
		m |= ModShift
	}
	if flags&(1<<18) != 0 {
		m |= ModCtrl
	}
	if flags&(1<<19) != 0 {
		m |= ModAlt
	}
	if flags&(1<<20) != 0 {
		m |= ModSuper
	}
	return m
}

// macKey maps a kVK_* virtual key code to Key.
func macKey(code uint32) Key {
	switch code {
	case 0x35:
		return KeyEscape
	case 0x24, 0x4C:
		return KeyEnter
	case 0x30:
		return KeyTab
	case 0x33:
		return KeyBackspace
	case 0x75:
		return KeyDelete
	case 0x31:
		return KeySpace
	case 0x7B:
		return KeyLeft
	case 0x7C:
		return KeyRight
	case 0x7E:
		return KeyUp
	case 0x7D:
		return KeyDown
	case 0x73:
		return KeyHome
	case 0x77:
		return KeyEnd
	case 0x74:
		return KeyPageUp
	case 0x79:
		return KeyPageDown
	}
	if code < uint32(len(macLetters)) && macLetters[code] != 0 {
		return macLetters[code]
	}
	for i, c := range macFKeys {
		if c == code {
			return KeyF1 + Key(i)
		}
	}
	return KeyUnknown
}

var macFKeys = [12]uint32{0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D, 0x67, 0x6F}

var macLetters = func() (t [0x30]Key) {
	t[0x21], t[0x1E], t[0x2F] = KeyLeftBracket, KeyRightBracket, KeyPeriod
	letters := map[uint32]byte{0x00: 'A', 0x0B: 'B', 0x08: 'C', 0x02: 'D', 0x0E: 'E', 0x03: 'F', 0x05: 'G', 0x04: 'H',
		0x22: 'I', 0x26: 'J', 0x28: 'K', 0x25: 'L', 0x2E: 'M', 0x2D: 'N', 0x1F: 'O', 0x23: 'P', 0x0C: 'Q', 0x0F: 'R',
		0x01: 'S', 0x11: 'T', 0x20: 'U', 0x09: 'V', 0x0D: 'W', 0x07: 'X', 0x10: 'Y', 0x06: 'Z'}
	for c, l := range letters {
		t[c] = KeyA + Key(l-'A')
	}
	digits := [10]uint32{0x1D, 0x12, 0x13, 0x14, 0x15, 0x17, 0x16, 0x1A, 0x1C, 0x19}
	for i, c := range digits {
		t[c] = Key0 + Key(i)
	}
	return t
}()
