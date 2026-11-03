package a3lab_test

import (
	"testing"

	"gonativeui/internal/fakeui"
	"gonativeui/theme"
	"gonativeui/theme/a3lab"
	"gonativeui/theme/rex"
)

// Apply's colours hold once the theme is resolved: rust as the accent, and the selections the
// ink's wash rather than the accent (the sidebar's follows the accent unless the theme owns it).
func TestApply(t *testing.T) {
	th := rex.Theme()
	a3lab.Apply(th)
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		r := th.Resolve(fakeui.New(), 2, m)
		tk := a3lab.Mode(m == theme.Dark)
		want := map[theme.Role]uint32{
			theme.Accent:      a3lab.Rust<<8 | 0xFF,
			theme.Field:       tk.Sidebar<<8 | 0xFF,
			theme.Pane:        tk.Bg<<8 | 0xFF,
			theme.Raised:      tk.Card<<8 | 0xFF,
			theme.Link:        tk.RustText<<8 | 0xFF,
			theme.SidebarSel:  theme.ColA(tk.Fg, tk.Active),
			theme.RowSelected: theme.ColA(tk.Fg, tk.Active),
		}
		for role, c := range want {
			if got := r.Colors[role]; got != c {
				t.Errorf("mode %d role %d: %08X, want %08X", m, role, got, c)
			}
		}
	}
}
