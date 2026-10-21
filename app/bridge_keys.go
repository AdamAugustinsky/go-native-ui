//go:build !gnui_release

package app

import (
	"fmt"
	"strings"

	"gonativeui/ui"
)

// namedKeys are the keys the bridge names (inspectwire.Step.Key).
var namedKeys = map[string]ui.KeyCode{
	"enter": ui.KeyEnter, "return": ui.KeyEnter, "space": ui.KeySpace, "escape": ui.KeyEscape, "esc": ui.KeyEscape,
	"tab": ui.KeyTab, "backspace": ui.KeyBackspace, "delete": ui.KeyDelete, "left": ui.KeyLeft, "right": ui.KeyRight,
	"up": ui.KeyUp, "down": ui.KeyDown, "home": ui.KeyHome, "end": ui.KeyEnd, "pageup": ui.KeyPageUp, "pagedown": ui.KeyPageDown,
}

// modNames are the modifiers the bridge names.
var modNames = map[string]func() ui.Mods{
	"shift": func() ui.Mods { return ui.ModShift },
	"ctrl":  func() ui.Mods { return ui.ModCtrl }, "control": func() ui.Mods { return ui.ModCtrl },
	"alt": func() ui.Mods { return ui.ModAlt }, "option": func() ui.Mods { return ui.ModAlt }, "opt": func() ui.Mods { return ui.ModAlt },
	"cmd": func() ui.Mods { return ui.ModSuper }, "command": func() ui.Mods { return ui.ModSuper },
	"super": func() ui.Mods { return ui.ModSuper }, "meta": func() ui.Mods { return ui.ModSuper }, "win": func() ui.Mods { return ui.ModSuper },
	"primary": ui.PrimaryMod, "mod": ui.PrimaryMod,
}

// parseKey reads "cmd+shift+n", "enter", "f5": modifiers, then the key, joined
// by plus signs, in any case.
func parseKey(s string) (ui.KeyCode, ui.Mods, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "+")
	var mods ui.Mods
	for _, m := range parts[:len(parts)-1] {
		f := modNames[m]
		if f == nil {
			return 0, 0, fmt.Errorf("key %q: %q is not a modifier (shift, ctrl, alt, cmd, primary)", s, m)
		}
		mods |= f()
	}
	k := parts[len(parts)-1]
	if c, ok := namedKeys[k]; ok {
		return c, mods, nil
	}
	if len(k) >= 2 && k[0] == 'f' {
		var n int
		if _, err := fmt.Sscanf(k[1:], "%d", &n); err == nil && n >= 1 && n <= 20 && fmt.Sprint(n) == k[1:] {
			return ui.KeyF1 + ui.KeyCode(n-1), mods, nil
		}
	}
	if len(k) == 1 {
		switch c := k[0]; {
		case c >= 'a' && c <= 'z':
			return ui.KeyCode(c - 'a' + 'A'), mods, nil
		case c >= '0' && c <= '9', strings.IndexByte("[].,=-/;'\\`", c) >= 0:
			return ui.KeyCode(c), mods, nil
		}
	}
	return 0, 0, fmt.Errorf("key %q: %q is not a key (a letter, digit or one of []\\.,=-/;'`, or enter, escape, tab, space, backspace, delete, left, right, up, down, home, end, pageup, pagedown, f1 to f20)", s, k)
}
