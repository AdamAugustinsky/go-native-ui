package demo

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The layout audit finds nothing in any scene, light and dark, at the top and
// scrolled under the toolbar, with the settings' pop-up menu open.
func TestAuditLayout(t *testing.T) {
	for s := Scene(0); s < NumScenes; s++ {
		for _, pref := range []theme.Preference{theme.ForceLight, theme.ForceDark} {
			for _, scrolled := range []bool{false, true} {
				name := SceneNames[s]
				if pref == theme.ForceDark {
					name += " dark"
				}
				if scrolled {
					name += " scrolled"
				}
				r, d := newRigOpts(t, Options{Scene: s, Preference: pref, Traffic: true, Still: true, Scrolled: scrolled})
				r.Frames(8)
				r.AuditLayout(name, auditAllow...)
				if s == Settings {
					r.Click(ui.RoleComboBox, "Accent colour")
					r.Frames(8)
					if !d.k.MenuOpen() {
						t.Fatalf("%s: the pop-up menu did not open", name)
					}
					r.AuditLayout(name+" with a pop-up menu open", auditAllow...)
				}
			}
		}
	}
}

// auditAllow are the findings that are right as they are, each with the
// AppKit capture it follows.
var auditAllow = []widgettest.Allow{
	{Check: ui.AuditColumn, Where: `Row "Invoices" in Table "Files"`,
		Reason: "a folder's symbol and a page's differ in width, centred in the same 16-pt square at the cell's start, as AppKit's file icons differ in ink in their one 198–214 image view (finder capture: a folder 199–213, a page 200–212); the names after them line up"},
}

func newRigOpts(t *testing.T, o Options) (*widgettest.Rig, *Demo) {
	t.Helper()
	l := &layouts[o.Scene]
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: int(2 * l.w), Height: int(2 * l.h), Scale: 2, Boxes: 8192})
	d := New(o)
	d.Attach(r.Context(), r.Text())
	r.Run(d)
	return r, d
}
