package demo

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The layout audit finds nothing in any scene, light and dark, at the top and
// scrolled under the toolbar, with overlay and legacy scroll bars, with the
// settings' pop-up menu open.
func TestAuditLayout(t *testing.T) {
	for s := Scene(0); s < NumScenes; s++ {
		for _, pref := range []theme.Preference{theme.ForceLight, theme.ForceDark} {
			for i := range 3 {
				scrolled, legacy := i == 1, i == 2
				name := SceneNames[s]
				if pref == theme.ForceDark {
					name += " dark"
				}
				if scrolled {
					name += " scrolled"
				}
				if legacy {
					name += " legacy scrollers"
				}
				r, d := newRigOpts(t, Options{Scene: s, Preference: pref, Traffic: true, Still: true, Scrolled: scrolled, Legacy: legacy})
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
// AppKit capture it follows (none: the file list's name cells are exempt in
// place, fileList says why).
var auditAllow = []widgettest.Allow{}

func newRigOpts(t *testing.T, o Options) (*widgettest.Rig, *Demo) {
	t.Helper()
	l := &layouts[o.Scene]
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: int(2 * l.w), Height: int(2 * l.h), Scale: 2, Boxes: 8192})
	d := New(o)
	d.Attach(r.Context(), r.Text())
	r.Run(d)
	return r, d
}
