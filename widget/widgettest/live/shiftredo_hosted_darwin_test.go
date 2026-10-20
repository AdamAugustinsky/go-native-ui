//go:build darwin

package live

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

// A real ⇧⌘Z (Win.Key builds it as AppKit does: the Shift letter's key
// equivalent in uppercase) matches Redo in the menu and redoes in the hosted
// NSTextView, as ⌘Z undoes (T3).
func TestEditorShiftRedoHosted(t *testing.T) {
	l, p := startEditor(t, platform.EditorOptions{})
	focusEditor(t, l, p)
	l.Type("hello")
	l.Until("typed", 120, textIs(p, "hello"))
	pressIn(l, ui.KeyCode('Z'), ui.ModSuper)
	l.Until("typing undone", 120, textIs(p, ""))
	pressIn(l, ui.KeyCode('Z'), ui.ModSuper|ui.ModShift)
	if !l.Within("typing redone by ⇧⌘Z", 120, textIs(p, "hello")) {
		t.Fatal("⇧⌘Z did not redo in the hosted NSTextView")
	}
}
