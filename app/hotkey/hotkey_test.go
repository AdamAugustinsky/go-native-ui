package hotkey_test

import (
	"errors"
	"testing"

	"gonativeui/app"
	"gonativeui/app/hotkey"
	"gonativeui/platform"
	"gonativeui/ui"
)

func TestValid(t *testing.T) {
	for _, c := range []struct {
		s    hotkey.Shortcut
		want bool
	}{
		{hotkey.Shortcut{Key: 'K', Mods: ui.ModAlt, Primary: true}, true},
		{hotkey.Shortcut{Key: ui.KeySpace, Mods: ui.ModCtrl}, true},
		{hotkey.Shortcut{Key: 'K'}, false},
		{hotkey.Shortcut{Key: 'K', Mods: ui.ModShift}, false},
		{hotkey.Shortcut{Mods: ui.ModCtrl}, false},
	} {
		if got := hotkey.Valid(c.s); got != c.want {
			t.Errorf("Valid(%+v) = %v", c.s, got)
		}
	}
	if err := hotkey.Register(0, hotkey.Shortcut{Key: 'K', Primary: true}); !errors.Is(err, hotkey.ErrInvalid) {
		t.Errorf("Register(0) = %v", err)
	}
}

// TestRegisterContract is the wave-0 stub's behaviour: a valid chord is
// refused as unsupported (or, once W7 lands, before the app starts).
func TestRegisterContract(t *testing.T) {
	err := hotkey.Register(1, hotkey.Shortcut{Key: 'K', Mods: ui.ModAlt, Primary: true})
	if !errors.Is(err, errors.ErrUnsupported) && !errors.Is(err, hotkey.ErrNotStarted) {
		t.Fatalf("Register = %v", err)
	}
	if _, ok := hotkey.Registered(1); ok {
		t.Fatal("a refused hotkey is registered")
	}
	hotkey.Unregister(1)
}

func TestPressed(t *testing.T) {
	e := app.Event{Kind: app.EvService, Source: platform.SourceHotkey, ID: 7}
	if id, ok := hotkey.Pressed(&e); !ok || id != 7 {
		t.Fatalf("Pressed = %d %v", id, ok)
	}
	for _, e := range []app.Event{{Kind: app.EvService, Source: platform.SourceTray, ID: 7}, {Kind: app.EvCommand, ID: 7}} {
		if _, ok := hotkey.Pressed(&e); ok {
			t.Errorf("Pressed(%+v)", e)
		}
	}
}
