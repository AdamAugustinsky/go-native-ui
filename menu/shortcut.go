package menu

import "gonativeui/ui"

// AppendLabel appends the text a menu shows for the shortcut: "⌘⇧N" on macOS
// (modifiers in the order ⌃⌥⇧⌘), "Ctrl+Shift+N" elsewhere (Ctrl, Alt, Shift,
// Super). It appends nothing for the zero shortcut.
func (s Shortcut) AppendLabel(dst []byte) []byte {
	return appendLabel(dst, s, ui.PrimaryMod() == ui.ModSuper)
}

func appendLabel(dst []byte, s Shortcut, mac bool) []byte {
	if s.IsZero() {
		return dst
	}
	mods := s.Mods
	if s.Primary {
		if mac {
			mods |= ui.ModSuper
		} else {
			mods |= ui.ModCtrl
		}
	}
	if mac {
		if mods&ui.ModCtrl != 0 {
			dst = append(dst, "⌃"...)
		}
		if mods&ui.ModAlt != 0 {
			dst = append(dst, "⌥"...)
		}
		if mods&ui.ModShift != 0 {
			dst = append(dst, "⇧"...)
		}
		if mods&ui.ModSuper != 0 {
			dst = append(dst, "⌘"...)
		}
		return appendKey(dst, s.Key, true)
	}
	if mods&ui.ModCtrl != 0 {
		dst = append(dst, "Ctrl+"...)
	}
	if mods&ui.ModAlt != 0 {
		dst = append(dst, "Alt+"...)
	}
	if mods&ui.ModShift != 0 {
		dst = append(dst, "Shift+"...)
	}
	if mods&ui.ModSuper != 0 {
		dst = append(dst, "Super+"...)
	}
	return appendKey(dst, s.Key, false)
}

func appendKey(dst []byte, k ui.KeyCode, mac bool) []byte {
	if k >= 0x21 && k < 0x7F { // letters, digits and punctuation are their ASCII value
		return append(dst, byte(k))
	}
	names := [...]struct{ mac, other string }{
		ui.KeyEnter - ui.KeyEnter:     {"↩", "Enter"},
		ui.KeySpace - ui.KeyEnter:     {"Space", "Space"},
		ui.KeyEscape - ui.KeyEnter:    {"⎋", "Esc"},
		ui.KeyTab - ui.KeyEnter:       {"⇥", "Tab"},
		ui.KeyBackspace - ui.KeyEnter: {"⌫", "Backspace"},
		ui.KeyDelete - ui.KeyEnter:    {"⌦", "Del"},
		ui.KeyLeft - ui.KeyEnter:      {"←", "Left"},
		ui.KeyRight - ui.KeyEnter:     {"→", "Right"},
		ui.KeyUp - ui.KeyEnter:        {"↑", "Up"},
		ui.KeyDown - ui.KeyEnter:      {"↓", "Down"},
		ui.KeyHome - ui.KeyEnter:      {"↖", "Home"},
		ui.KeyEnd - ui.KeyEnter:       {"↘", "End"},
		ui.KeyPageUp - ui.KeyEnter:    {"⇞", "PgUp"},
		ui.KeyPageDown - ui.KeyEnter:  {"⇟", "PgDn"},
	}
	if i := int(k) - int(ui.KeyEnter); k >= ui.KeyEnter && i < len(names) {
		if mac {
			return append(dst, names[i].mac...)
		}
		return append(dst, names[i].other...)
	}
	return append(dst, '?')
}
