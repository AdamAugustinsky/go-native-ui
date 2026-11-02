package rex

import (
	"testing"

	"gonativeui/theme"
)

// DarkCast gives the dark roles' opaque neutral greys the field's hue and
// keeps them over the system's colours; translucent fills, colours and the
// light roles stay as they were.
func TestDarkCastTintsOpaqueGreys(t *testing.T) {
	plain := ThemeWithField(FieldColors{})
	cool := ThemeWithField(FieldColors{DarkCast: [3]float32{0.99, 0.99, 1.03}})
	cast := 0
	for i, v := range plain.Roles[theme.Dark] {
		got := cool.Roles[theme.Dark][i]
		r, g, b, a := v>>24, v>>16&0xFF, v>>8&0xFF, v&0xFF
		grey := a >= 0xE6 && r == g && g == b
		switch {
		case grey && v != got:
			cast++
			if gb := got >> 8 & 0xFF; gb < got>>24 && r > 8 {
				t.Errorf("role %d: %08X cast to %08X, which is not cooler", i, v, got)
			}
			if !cool.Own[i] {
				t.Errorf("role %d was cast but not kept over the system's", i)
			}
		case !grey && v != got:
			t.Errorf("role %d (%08X, not an opaque grey) changed to %08X", i, v, got)
		}
		if plain.Roles[theme.Light][i] != cool.Roles[theme.Light][i] {
			t.Errorf("light role %d changed", i)
		}
	}
	if cast == 0 {
		t.Fatal("no grey took the cast")
	}
}
