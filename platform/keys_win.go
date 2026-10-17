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
	return m
}

// scanKeys maps set-1 scancodes of the letter and digit keys to Key by
// physical (US) position, so letters/digits match macOS and Linux regardless
// of the keyboard layout (VK_A..VK_Z follow the layout).
var scanKeys = func() (t [0x35]Key) {
	t[0x1A], t[0x1B], t[0x34] = KeyLeftBracket, KeyRightBracket, KeyPeriod
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
