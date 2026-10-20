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
	{Check: ui.AuditColumn, Where: `in Table "Files"`, Other: `ColumnHeader "Name"`,
		Reason: "a name cell starts with its file's icon centred in a 16-pt square at the title's x, and icons differ in ink (a folder, a page, a platform's fallback glyph), as AppKit's file icons do in their one 198–214 image view (finder capture: a folder's ink 199–213, a page's 200–212); the names after them line up"},
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
