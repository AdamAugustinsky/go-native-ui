package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// keynavForm is a text field, a button, a checkbox and a second text field.
func keynavForm() (func(g *rig), *ui.TextBuffer, *ui.TextBuffer) {
	var a, b ui.TextBuffer
	var on bool
	return column(func(g *rig) {
		rexui.TextField(g.k, "First", &a, "First", rexui.FieldOpts{})
		rexui.Button(g.k, "Save", rexui.ButtonOpts{})
		rexui.Checkbox(g.k, "Wrap", &on, rexui.CheckboxOpts{})
		rexui.TextField(g.k, "Second", &b, "Second", rexui.FieldOpts{})
	}), &a, &b
}

// visibleRings counts the focus ring quads that can be seen (a ring fading out
// is drawn for a frame or two at an alpha of a few percent).
func visibleRings(g *rig) int {
	ring := g.k.Theme().Colors[theme.FocusRing] >> 8
	n := 0
	for _, q := range g.list.Quads {
		if q.Border > 0 && q.BorderColor>>8 == ring && q.BorderColor&0xFF >= 0x40 {
			n++
		}
	}
	return n
}

// focusedName is the name of the control that has the focus, "" if none.
func focusedName(g *rig) string {
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		if n.State&ui.SemFocused != 0 {
			return string(g.sem.Text(n.Name))
		}
	}
	return ""
}

// TestFocusRingsFollowHowFocusArrived: with Tab reaching every control, a text
// field shows its ring when it is clicked or reached by Tab, while a button or
// a checkbox shows it only when the keyboard focused it (Tab), never when a
// click did. With the Keyboard navigation setting off Tab passes the controls
// by, and the ring of the text fields it reaches is the same.
func TestFocusRingsFollowHowFocusArrived(t *testing.T) {
	build, _, _ := keynavForm()
	g := newRig(t, theme.Light, nil, build)
	g.c.SetTabReachesControls(true)
	g.settle()

	g.click(centre(g.node(ui.RoleTextField, "First")))
	g.settle()
	if n := visibleRings(g); n == 0 {
		t.Error("a clicked text field shows no ring")
	}
	g.click(centre(g.node(ui.RoleButton, "Save")))
	g.settle()
	if focusedName(g) != "Save" {
		t.Fatalf("a click on the button left the focus on %q", focusedName(g))
	}
	if n := visibleRings(g); n != 0 {
		t.Errorf("a clicked button shows a ring (%d quads)", n)
	}
	g.click(centre(g.node(ui.RoleCheckbox, "Wrap")))
	g.settle()
	if n := visibleRings(g); n != 0 {
		t.Errorf("a clicked checkbox shows a ring (%d quads)", n)
	}
	g.key(ui.KeyTab, ui.ModShift) // the keyboard moves back to the button
	g.settle()
	if focusedName(g) != "Save" || visibleRings(g) == 0 {
		t.Errorf("Shift+Tab to the button: focus %q, %d ring quads, want the ring", focusedName(g), visibleRings(g))
	}

	// The setting off: Tab goes from one text field to the next, and each
	// shows its ring.
	g.c.SetTabReachesControls(false)
	g.click(centre(g.node(ui.RoleTextField, "First")))
	g.key(ui.KeyTab, 0)
	g.settle()
	if focusedName(g) != "Second" {
		t.Fatalf("Tab from the first field with the setting off reached %q", focusedName(g))
	}
	if visibleRings(g) == 0 {
		t.Error("the text field reached by Tab shows no ring")
	}
	g.key(ui.KeyTab, ui.ModShift)
	g.settle()
	if focusedName(g) != "First" {
		t.Errorf("Shift+Tab reached %q", focusedName(g))
	}
}
