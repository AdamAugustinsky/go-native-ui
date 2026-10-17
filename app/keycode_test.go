package app

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

func TestKeyCodePunctuation(t *testing.T) {
	for k, want := range map[platform.Key]ui.KeyCode{
		platform.KeyLeftBracket: ui.KeyLeftBracket, platform.KeyRightBracket: ui.KeyRightBracket,
		platform.KeyPeriod: ui.KeyPeriod, platform.KeyA: 'A', platform.Key0: '0',
	} {
		if got := keyCode(k); got != want {
			t.Errorf("keyCode(%d) = %d, want %d", k, got, want)
		}
	}
}
