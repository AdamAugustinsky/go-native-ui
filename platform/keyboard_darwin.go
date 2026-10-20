//go:build darwin

package platform

import (
	"unicode/utf8"

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
	selPerformKeyEquiv  = objc.Sel("performKeyEquivalent:")
	selCharsNoMods      = objc.Sel("charactersIgnoringModifiers")
)

func registerViewKeyboard(v *objc.ClassDef) {
	v.Method(selAcceptsFirstResp, "c@:", viewAcceptsFirstResponder)
	v.Method(selKeyDown, "v@:@", viewKeyDown)
	v.Method(selKeyUp, "v@:@", viewSwallowKey)
	v.Method(selFlagsChanged, "v@:@", viewSwallowKey)
	v.Method(selPerformKeyEquiv, "c@:@", viewPerformKeyEquivalent)
}

// viewPerformKeyEquivalent is performKeyEquivalent:, which AppKit sends down
// the view hierarchy before the key reaches the menu or keyDown:. A real key
// goes on to the subviews as NSView does (a native view in the window may
// claim it). An event with a zero timestamp is one the system made up (⌘.
// becomes a synthetic Escape for cancelOperation:, which a native view's
// Cancel button would take for a real Escape), so it is not offered to them.
func viewPerformKeyEquivalent(c *ffi.CB) {
	self, ev := objc.ID(c.Int(0)), objc.ID(c.Int(2))
	if objc.SendF64(ev, selTimestamp) == 0 {
		c.RetBool(false)
		return
	}
	c.RetBool(objc.SuperFrame(c.Frame(), viewDef.Superclass(), self, selPerformKeyEquiv).Arg(uintptr(ev)).Call(objc.MsgSendSuper).Bool())
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
	e := Event{Kind: EvKey, Key: macKey(code), Code: code, Mods: m, Time: t, Char: w.keyChar(ev, code)}
	w.lastMods = m                 // (EvModifiers reports changes against what the app last saw)
	if code == 0x30 && typ == 10 { // Tab: the Keyboard navigation setting may have changed since the app was active
		refreshKeynav()
	}
	if typ == 10 {
		e.Flags = FlagDown
		if objc.SendBool(ev, selIsARepeat) {
			e.Flags |= FlagRepeat
		}
	}
	if !w.hostFocused() { // while a host holds the first responder, the key is its own (host_darwin.go)
		w.q.push(e)
		if typ == 10 {
			keyIdx = len(w.q.ev) - 1
		}
	}
	return keyIdx
}

// keyChar is the character a key-down or key-up event types for a shortcut:
// the key's character on the selected layout with ⌘ held and no Shift
// (layoutChar), lower-cased; 0 when it types none in ASCII (AppKit's
// private-use characters for the function and arrow keys, control characters,
// another script's letters). Without layout data it is what the event says it
// types ignoring the modifiers but Shift.
func (w *Window) keyChar(ev objc.ID, code uint32) rune {
	if r, ok := layoutChar(code); ok {
		return r
	}
	str := objc.Send(ev, selCharsNoMods)
	if str == 0 {
		return 0
	}
	w.tmp = objc.AppendUTF8(w.tmp[:0], str)
	r, _ := utf8.DecodeRune(w.tmp)
	if r == utf8.RuneError || (r >= 0xF700 && r <= 0xF8FF) {
		return 0
	}
	return shortcutRune(r)
}

// modifiersEvent queues EvModifiers for a flagsChanged: event (a modifier
// key pressed or released on its own) when the modifiers differ from the last
// ones reported. Code is the raw code of the key that moved.
func (w *Window) modifiersEvent(ev objc.ID, t int64) {
	m := mods(uintptr(objc.Send(ev, selModifierFlags)))
	if m == w.lastMods {
		return
	}
	w.lastMods = m
	w.q.push(Event{Kind: EvModifiers, Mods: m, Code: uint32(objc.Send(ev, selKeyCode)) & 0xFFFF, Time: t})
}

// sampleMods is the modifiers held right now (+[NSEvent modifierFlags]); a
// variable so a test can stand in for the keyboard.
var sampleMods = func() Mods { return mods(uintptr(objc.Send(objc.Cls("NSEvent"), selModifierFlags))) }

// focusMods keeps EvModifiers true across a change of key window (see
// focusModifiers); held is sampled only on gaining focus.
func (w *Window) focusMods(gained bool, t int64) {
	var held Mods
	if gained {
		held = sampleMods()
	}
	focusModifiers(&w.q, &w.lastMods, held, t)
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
	if code < uint32(len(macKeys)) {
		return macKeys[code]
	}
	return KeyUnknown
}

// macKeys is macKey as a table, built from the kVK_* codes (HIToolbox/Events.h).
var macKeys = func() (t [0x80]Key) {
	named := map[uint32]Key{
		0x35: KeyEscape, 0x24: KeyEnter, 0x4C: KeyEnter, 0x30: KeyTab, 0x33: KeyBackspace, 0x75: KeyDelete,
		0x31: KeySpace, 0x7B: KeyLeft, 0x7C: KeyRight, 0x7E: KeyUp, 0x7D: KeyDown, 0x73: KeyHome, 0x77: KeyEnd,
		0x74: KeyPageUp, 0x79: KeyPageDown,
		// punctuation
		0x21: KeyLeftBracket, 0x1E: KeyRightBracket, 0x2F: KeyPeriod, 0x2B: KeyComma, 0x18: KeyEqual,
		0x1B: KeyMinus, 0x2C: KeySlash, 0x29: KeySemicolon, 0x27: KeyQuote, 0x2A: KeyBackslash, 0x32: KeyGrave,
		// keypad
		0x41: KeyNumDecimal, 0x43: KeyNumMultiply, 0x45: KeyNumAdd, 0x47: KeyNumClear, 0x4B: KeyNumDivide,
		0x4E: KeyNumSubtract, 0x51: KeyNumEqual,
	}
	for c, k := range named {
		t[c] = k
	}
	for i, c := range [10]uint32{0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5B, 0x5C} { // keypad 0..9
		t[c] = KeyNum0 + Key(i)
	}
	for i, c := range [12]uint32{0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D, 0x67, 0x6F} { // F1..F12
		t[c] = KeyF1 + Key(i)
	}
	for i, c := range [8]uint32{0x69, 0x6B, 0x71, 0x6A, 0x40, 0x4F, 0x50, 0x5A} { // F13..F20
		t[c] = KeyF13 + Key(i)
	}
	letters := map[uint32]byte{0x00: 'A', 0x0B: 'B', 0x08: 'C', 0x02: 'D', 0x0E: 'E', 0x03: 'F', 0x05: 'G', 0x04: 'H',
		0x22: 'I', 0x26: 'J', 0x28: 'K', 0x25: 'L', 0x2E: 'M', 0x2D: 'N', 0x1F: 'O', 0x23: 'P', 0x0C: 'Q', 0x0F: 'R',
		0x01: 'S', 0x11: 'T', 0x20: 'U', 0x09: 'V', 0x0D: 'W', 0x07: 'X', 0x10: 'Y', 0x06: 'Z'}
	for c, l := range letters {
		t[c] = KeyA + Key(l-'A')
	}
	for i, c := range [10]uint32{0x1D, 0x12, 0x13, 0x14, 0x15, 0x17, 0x16, 0x1A, 0x1C, 0x19} { // 0..9
		t[c] = Key0 + Key(i)
	}
	return
}()
