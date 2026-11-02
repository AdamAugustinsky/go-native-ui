package rexui

import "testing"

// A shortcut written once as macOS writes it reads as each platform writes
// it: the modifiers in Windows' order with their names, ⌥ included (A3's
// replacer left it as a symbol), and the rest kept.
func TestKeyTextPerPlatform(t *testing.T) {
	for mac, want := range map[string]string{
		"⌘⇧F":           "Ctrl+Shift+F",
		"⌥⌘F":           "Ctrl+Alt+F",
		"⇧⌥⌃K":          "Ctrl+Alt+Shift+K",
		"⌃`":            "Ctrl+`",
		"⌃⇧Tab":         "Ctrl+Shift+Tab",
		"⌘[ / ⌘]":       "Ctrl+[ / Ctrl+]",
		"⌘1–9":          "Ctrl+1–9",
		"⇧↩":            "Shift+Enter",
		"⎋":             "Esc",
		"J / K":         "J / K",
		"Botão direito": "Botão direito",
	} {
		if got := keyTextFor(mac, false); got != want {
			t.Errorf("off macOS %q is %q, want %q", mac, got, want)
		}
		if got := keyTextFor(mac, true); got != mac {
			t.Errorf("on macOS %q is %q, want it as written", mac, got)
		}
	}
}
