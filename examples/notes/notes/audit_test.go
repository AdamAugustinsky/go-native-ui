package notes

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// TestAuditLayout runs the layout audit on the window in light and dark mode,
// with each scroller style, in every state the window has: the note open at
// its top and scrolled under the toolbar, the sidebar hidden, no note open, a
// search that narrows the list, the drawn dialog open, and a note whose title
// is longer than the sidebar.
func TestAuditLayout(t *testing.T) {
	scrollers := []struct {
		name  string
		style theme.ScrollerStyle
	}{{"overlay", theme.ScrollerOverlay}, {"legacy", theme.ScrollerLegacy}}
	states := []struct {
		name  string
		setup func(r *rig)
	}{
		{"top", func(r *rig) {}},
		{"scrolled", func(r *rig) { r.d.Scrolled = true }},
		{"sidebar hidden", func(r *rig) { r.play(click(ui.RoleButton, "Sidebar")) }},
		{"no note open", func(r *rig) { r.d.side.Sel = 0 }},
		{"searching", func(r *rig) { r.play(click(ui.RoleSearchField, "Search"), typeText("o")) }},
		{"dialog", func(r *rig) { r.play(click(ui.RoleButton, "Remove…"), waitFor(ui.RoleDialog, "")) }},
		{"long title", func(r *rig) {
			r.play(click(ui.RoleTextField, "New note"), typeText("A title that is a good deal longer than the sidebar is wide, and then some"), key(ui.KeyEnter, 0))
		}},
	}
	for _, dark := range []bool{false, true} {
		for _, sc := range scrollers {
			for _, st := range states {
				r := newRigWith(t, dark, func(n *Notes) { n.drawnOnly = true })
				r.d.k.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: sc.style}})
				r.Frames(4)
				st.setup(r)
				r.Frames(6)
				name := "notes " + sc.name + " " + st.name
				if dark {
					name += " dark"
				}
				r.AuditLayout(name)
			}
		}
	}
}
