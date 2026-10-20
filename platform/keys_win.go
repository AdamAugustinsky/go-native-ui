package platform

// Windows key translation. It has no build constraint (and no Windows API
// calls) so the tests run on every OS.

var keyMap = buildKeyMap()

// vkProcessKey is the virtual key an input method substitutes for the keys it
// processes (VK_PROCESSKEY): the keystroke belongs to the composition, whatever
// its scancode says.
const vkProcessKey = 0xE5

// winKey translates the wParam and lParam of a WM_KEYDOWN, WM_KEYUP,
// WM_SYSKEYDOWN or WM_SYSKEYUP into the platform's scan code (0xE000 added for
// extended keys) and Key. ok is false for a key an input method consumed, down
// or up: it is not a key event for the app.
func winKey(wp, lp uintptr) (code uint32, k Key, ok bool) {
	if wp == vkProcessKey {
		return 0, KeyUnknown, false
	}
	code = uint32(lp>>16) & 0xFF
	if lp&(1<<24) != 0 {
		code |= 0xE000
	}
	if code < uint32(len(scanKeys)) {
		k = scanKeys[code]
	}
	if k == KeyUnknown && wp < uintptr(len(keyMap)) {
		k = keyMap[wp]
	}
	return code, k, true
}

// buildKeyMap maps Windows virtual-key codes to platform keys.
func buildKeyMap() (m [256]Key) {
	m[0x1B] = KeyEscape
	m[0x0D] = KeyEnter
	m[0x09] = KeyTab
	m[0x08] = KeyBackspace
	m[0x2E] = KeyDelete
	m[0x20] = KeySpace
	m[0x25] = KeyLeft
	m[0x27] = KeyRight
	m[0x26] = KeyUp
	m[0x28] = KeyDown
	m[0x24] = KeyHome
	m[0x23] = KeyEnd
	m[0x21] = KeyPageUp
	m[0x22] = KeyPageDown
	for i := 0; i < 12; i++ {
		m[0x70+i] = KeyF1 + Key(i) // VK_F1..VK_F12
	}
	for i := 0; i < 8; i++ {
		m[0x7C+i] = KeyF13 + Key(i) // VK_F13..VK_F20
	}
	// The keypad, by virtual key: they are VK_NUMPADn only while NumLock is
	// on; with it off the same keys send VK_HOME, VK_UP and so on, which are
	// the navigation keys above.
	for i := 0; i < 10; i++ {
		m[0x60+i] = KeyNum0 + Key(i) // VK_NUMPAD0..VK_NUMPAD9
	}
	m[0x6A], m[0x6B], m[0x6D], m[0x6E], m[0x6F] = KeyNumMultiply, KeyNumAdd, KeyNumSubtract, KeyNumDecimal, KeyNumDivide
	m[0x0C] = KeyNumClear // VK_CLEAR: keypad 5 with NumLock off
	m[0x92] = KeyNumEqual // VK_OEM_NEC_EQUAL: the keypad's =
	return m
}

// scanKeys maps set-1 scancodes of the letter, digit and punctuation keys to
// Key by physical (US) position, so they match macOS and Linux regardless of
// the keyboard layout (VK_A..VK_Z follow the layout).
var scanKeys = func() (t [0x36]Key) {
	t[0x1A], t[0x1B], t[0x34] = KeyLeftBracket, KeyRightBracket, KeyPeriod
	t[0x0C], t[0x0D], t[0x27], t[0x28], t[0x29], t[0x2B], t[0x33], t[0x35] =
		KeyMinus, KeyEqual, KeySemicolon, KeyQuote, KeyGrave, KeyBackslash, KeyComma, KeySlash
	for i, r := range "1234567890" {
		t[0x02+i] = Key0 + Key(r-'0')
	}
	rows := [...]struct {
		first   int
		letters string
	}{{0x10, "QWERTYUIOP"}, {0x1E, "ASDFGHJKL"}, {0x2C, "ZXCVBNM"}}
	for _, row := range rows {
		for i, r := range row.letters {
			t[row.first+i] = KeyA + Key(r-'A')
		}
	}
	return t
}()

// vkIsModifier reports whether virtual key wp is a modifier key (Shift,
// Control, Alt, the Windows keys, left or right): its press or release is an
// EvModifiers, not a key.
func vkIsModifier(wp uintptr) bool {
	switch wp {
	case 0x10, 0x11, 0x12, 0x5B, 0x5C, 0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5:
		return true
	}
	return false
}

// winNeedsChar reports whether Event.Char of virtual key wp needs the
// layout to say (MapVirtualKeyW): the VK_OEM_* keys, whose meaning differs
// between layouts; the letters are their own character.
func winNeedsChar(wp uintptr) bool { return wp >= 0xBA && wp <= 0xE2 }

// winChar is the character virtual key wp types with no modifier but Shift
// (Event.Char), lower-cased: a letter's virtual key is the letter on the
// user's layout (VK_A..VK_Z follow it), and an OEM key's is the printable
// character mapped (MapVirtualKeyW(vk, MAPVK_VK_TO_CHAR), whose top bit marks a
// dead key). 0 for the rest.
func winChar(wp uintptr, mapped uint32) rune {
	if wp >= 'A' && wp <= 'Z' {
		return rune(wp-'A') + 'a'
	}
	if !winNeedsChar(wp) || mapped&0x80000000 != 0 {
		return 0
	}
	if r := rune(mapped & 0xFFFF); r > 0x20 && r < 0x7F {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		return r
	}
	return 0
}

// A precision touchpad's pinch reaches a window that does not handle
// WM_POINTER or WM_GESTURE as WM_MOUSEWHEEL with MK_CONTROL set while the
// Control key is not down (the system adds the flag; Chromium and Firefox read
// it the same way), and no phases: each message is one step of the pinch.
const (
	mkControl = 0x0008
	// winPinchPerNotch is the change in scale one notch of wheel delta (120)
	// stands for, 0.1. The system gives no scale of its own; the figure is
	// a calibration to refine against a touchpad.
	winPinchPerNotch = 0.1
)

// winPinch reports whether a WM_MOUSEWHEEL with wParam wp is a touchpad pinch
// (ctrlDown: the Control key is physically down, then it is an ordinary
// Ctrl+wheel) and the change in scale it makes: forward, fingers apart, grows.
func winPinch(wp uintptr, ctrlDown bool) (amount float32, ok bool) {
	if wp&mkControl == 0 || ctrlDown {
		return 0, false
	}
	return float32(int16(wp>>16)) / 120 * winPinchPerNotch, true
}
