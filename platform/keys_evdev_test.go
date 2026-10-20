package platform

import "testing"

func TestLinuxKeyPunctuationByPosition(t *testing.T) {
	// evdev codes (input-event-codes.h) name the US-layout position, whatever
	// the layout's keysym is.
	for _, c := range []struct {
		evdev, sym uint32
		want       Key
	}{
		{12, '-', KeyMinus}, {13, '=', KeyEqual}, {26, '[', KeyLeftBracket}, {27, ']', KeyRightBracket},
		{39, ';', KeySemicolon}, {40, '\'', KeyQuote}, {41, '`', KeyGrave}, {43, '\\', KeyBackslash},
		{51, ',', KeyComma}, {52, '.', KeyPeriod}, {53, '/', KeySlash},
		{53, 0xe9 /* é on a French layout's slash position */, KeySlash},
	} {
		if got := linuxKey(c.evdev, c.sym); got != c.want {
			t.Errorf("linuxKey(%d, %#x) = %v, want %v", c.evdev, c.sym, got, c.want)
		}
	}
}

func TestLinuxKeyKeypadAndFunction(t *testing.T) {
	for i := uint32(0); i < 10; i++ {
		// The keypad's evdev codes are not in evdevKeys, so the keysym decides.
		if got := linuxKey(82, xkKP0+i); got != KeyNum0+Key(i) {
			t.Errorf("KP_%d = %v, want %v", i, got, KeyNum0+Key(i))
		}
	}
	for _, c := range []struct {
		sym  uint32
		want Key
	}{
		{xkKPDecimal, KeyNumDecimal}, {xkKPAdd, KeyNumAdd}, {xkKPSubtract, KeyNumSubtract},
		{xkKPMultiply, KeyNumMultiply}, {xkKPDivide, KeyNumDivide}, {xkKPEqual, KeyNumEqual},
		{xkF12, KeyF12}, {xkF12 + 1, KeyF13}, {xkF20, KeyF20}, {xkF20 + 1, KeyUnknown},
	} {
		if got := linuxKey(0, c.sym); got != c.want {
			t.Errorf("linuxKey(0, %#x) = %v, want %v", c.sym, got, c.want)
		}
	}
	if linuxKey(0, xkKPEnter) != KeyEnter {
		t.Error("KP_Enter should stay KeyEnter")
	}
}

func TestKeysymChar(t *testing.T) {
	for _, c := range []struct {
		sym  uint32
		want rune
	}{
		{'a', 'a'}, {'A', 'a'}, {'z', 'z'}, {'1', '1'}, {';', ';'}, {'~', '~'},
		{' ', 0}, {0x7f, 0}, {0xe9, 0}, {xkReturn, 0}, {xkF1, 0}, {0x1000416 /* Cyrillic via Unicode keysym */, 0},
	} {
		if got := keysymChar(c.sym); got != c.want {
			t.Errorf("keysymChar(%#x) = %q, want %q", c.sym, got, c.want)
		}
	}
}

func TestKeysymModifiers(t *testing.T) {
	for _, c := range []struct {
		sym uint32
		isM bool
		bit Mods
	}{
		{0xffe1, true, ModShift}, {0xffe4, true, ModCtrl}, {0xffe9, true, ModAlt},
		{0xffeb, true, ModSuper}, {0xffe5 /* Caps_Lock */, true, 0},
		{'a', false, 0}, {xkF1, false, 0}, {xkReturn, false, 0},
	} {
		if keysymIsModifier(c.sym) != c.isM || keysymMod(c.sym) != c.bit {
			t.Errorf("sym %#x: modifier=%v bit=%v, want %v %v", c.sym, keysymIsModifier(c.sym), keysymMod(c.sym), c.isM, c.bit)
		}
	}
}
