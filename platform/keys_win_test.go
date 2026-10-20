package platform

import "testing"

// lparam builds the lParam of a WM_KEYDOWN: scancode, extended flag, previous
// state (repeat).
func lparam(scan uint32, extended bool) uintptr {
	lp := uintptr(1) | uintptr(scan)<<16
	if extended {
		lp |= 1 << 24
	}
	return lp
}

func TestWinKeyTranslation(t *testing.T) {
	cases := []struct {
		name string
		wp   uintptr
		lp   uintptr
		key  Key
		code uint32
	}{
		{"A by position", 'A', lparam(0x1E, false), KeyA, 0x1E},
		{"physical position beats layout", 'Q', lparam(0x1E, false), KeyA, 0x1E}, // AZERTY: the A key types Q
		{"left arrow", 0x25, lparam(0x4B, true), KeyLeft, 0xE04B},
		{"backspace", 0x08, lparam(0x0E, false), KeyBackspace, 0x0E},
		{"left bracket", 0xDB, lparam(0x1A, false), KeyLeftBracket, 0x1A},
	}
	for _, c := range cases {
		code, k, ok := winKey(c.wp, c.lp)
		if !ok || k != c.key || code != c.code {
			t.Errorf("%s: code %#x key %d ok %v, want %#x %d", c.name, code, k, ok, c.code, c.key)
		}
	}
}

// A key an input method consumed arrives as VK_PROCESSKEY with its physical
// scancode: it must not turn into KeyA..KeyZ (or any other key).
func TestWinKeyProcessKeyIsDropped(t *testing.T) {
	for _, scan := range []uint32{0x1E, 0x10, 0x32, 0x39, 0x0E, 0x1C, 0x02} {
		for _, ext := range []bool{false, true} {
			if code, k, ok := winKey(vkProcessKey, lparam(scan, ext)); ok {
				t.Errorf("VK_PROCESSKEY scan %#x ext=%v became key %d (code %#x)", scan, ext, k, code)
			}
		}
	}
}

// TestWinKeysPunctuationKeypadAndFunctionKeys: the new keys by position
// (punctuation) and by virtual key (keypad, F13-F20), whatever the layout.
func TestWinKeysPunctuationKeypadAndFunctionKeys(t *testing.T) {
	cases := []struct {
		name string
		wp   uintptr
		lp   uintptr
		key  Key
	}{
		{"minus by position", 0xBD, lparam(0x0C, false), KeyMinus},
		{"equals by position", 0xBB, lparam(0x0D, false), KeyEqual},
		{"semicolon", 0xBA, lparam(0x27, false), KeySemicolon},
		{"quote", 0xDE, lparam(0x28, false), KeyQuote},
		{"grave", 0xC0, lparam(0x29, false), KeyGrave},
		{"backslash", 0xDC, lparam(0x2B, false), KeyBackslash},
		{"comma", 0xBC, lparam(0x33, false), KeyComma},
		{"slash", 0xBF, lparam(0x35, false), KeySlash},
		{"position beats the layout: a German Ö key sends VK_OEM_3 from the semicolon position", 0xC0, lparam(0x27, false), KeySemicolon},
		{"keypad 7", 0x67, lparam(0x47, false), KeyNum0 + 7},
		{"keypad 0", 0x60, lparam(0x52, false), KeyNum0},
		{"keypad + ", 0x6B, lparam(0x4E, false), KeyNumAdd},
		{"keypad - ", 0x6D, lparam(0x4A, false), KeyNumSubtract},
		{"keypad *", 0x6A, lparam(0x37, false), KeyNumMultiply},
		{"keypad /", 0x6F, lparam(0x35, true), KeyNumDivide}, // extended: not the slash key
		{"keypad .", 0x6E, lparam(0x53, false), KeyNumDecimal},
		{"keypad 5 without NumLock", 0x0C, lparam(0x4C, false), KeyNumClear},
		{"keypad 7 without NumLock is Home", 0x24, lparam(0x47, false), KeyHome},
		{"keypad Enter", 0x0D, lparam(0x1C, true), KeyEnter},
		{"F13", 0x7C, lparam(0x64, false), KeyF13},
		{"F20", 0x83, lparam(0x6B, false), KeyF20},
		{"F12", 0x7B, lparam(0x58, false), KeyF12},
	}
	for _, c := range cases {
		_, k, ok := winKey(c.wp, c.lp)
		if !ok || k != c.key {
			t.Errorf("%s: key %d ok %v, want %d", c.name, k, ok, c.key)
		}
	}
}

// TestWinChar: the character a key types, which shortcuts match by.
func TestWinChar(t *testing.T) {
	for _, c := range []struct {
		name   string
		wp     uintptr
		mapped uint32
		want   rune
	}{
		{"a letter is its virtual key, lower-cased (AZERTY: the A position sends VK_Q)", 'Q', 0, 'q'},
		{"US comma", 0xBC, ',', ','},
		{"a German layout's OEM_PLUS is +", 0xBB, '+', '+'},
		{"OEM_2 on a layout where it is the slash", 0xBF, '/', '/'},
		{"an upper-case mapping is lowered", 0xC0, 'Ö', 0},
		{"a dead key types nothing yet", 0xDD, 0x80000000 | '^', 0},
		{"a control character", 0xBA, 0x1B, 0},
		{"space", 0xBA, ' ', 0},
		{"an arrow", 0x25, 0, 0},
		{"a digit key", '5', '5', 0},
		{"F13", 0x7C, 0, 0},
	} {
		if got := winChar(c.wp, c.mapped); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if winNeedsChar('A') || !winNeedsChar(0xBC) || winNeedsChar(0x25) {
		t.Error("winNeedsChar asks for the wrong keys")
	}
}

func TestWinModifierKeys(t *testing.T) {
	for _, vk := range []uintptr{0x10, 0x11, 0x12, 0x5B, 0x5C, 0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5} {
		if !vkIsModifier(vk) {
			t.Errorf("VK %#x is a modifier", vk)
		}
	}
	for _, vk := range []uintptr{'A', 0x14 /* caps lock */, 0x90 /* num lock */, 0x20, 0x09} {
		if vkIsModifier(vk) {
			t.Errorf("VK %#x is no modifier", vk)
		}
	}
}

// TestWinPinchIsControlWheelWithoutTheKey: MK_CONTROL in a wheel message with
// the Control key up is the touchpad's pinch; with it down it is Ctrl+wheel.
func TestWinPinchIsControlWheelWithoutTheKey(t *testing.T) {
	wheel := func(delta int16, flags uintptr) uintptr { return uintptr(uint16(delta))<<16 | flags }
	if a, ok := winPinch(wheel(120, mkControl), false); !ok || a != winPinchPerNotch {
		t.Errorf("one notch of pinch: %v %v", a, ok)
	}
	if a, ok := winPinch(wheel(-30, mkControl|0x0001), false); !ok || a != -winPinchPerNotch/4 {
		t.Errorf("a small step back (MK_LBUTTON also set): %v %v", a, ok)
	}
	if _, ok := winPinch(wheel(120, mkControl), true); ok {
		t.Error("Ctrl held down: a wheel, not a pinch")
	}
	if _, ok := winPinch(wheel(120, 0), false); ok {
		t.Error("a plain wheel is a pinch")
	}
}
