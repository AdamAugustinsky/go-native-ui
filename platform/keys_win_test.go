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
