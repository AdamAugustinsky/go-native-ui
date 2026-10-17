package platform

import "testing"

// kVK_ANSI_LeftBracket, RightBracket and Period: shortcuts like ⌘[ need them.
func TestMacPunctuationKeys(t *testing.T) {
	for code, want := range map[uint32]Key{0x21: KeyLeftBracket, 0x1E: KeyRightBracket, 0x2F: KeyPeriod, 0x00: KeyA, 0x2D: KeyA + 'N' - 'A'} {
		if got := macKey(code); got != want {
			t.Errorf("macKey(%#x) = %d, want %d", code, got, want)
		}
	}
}
