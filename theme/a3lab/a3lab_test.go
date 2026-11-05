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
			theme.FocusRing:   theme.ColA(tk.RustLineOf, tk.RustLine), // --rust-line: rust's text colour in dark
			theme.PaneButton:  theme.ColA(tk.Fg, tk.Fg50),
		}
		for role, c := range want {
			if got := r.Colors[role]; got != c {
				t.Errorf("mode %d role %d: %08X, want %08X", m, role, got, c)
			}
		}
	}
}

// Apply gives the web app's text and line look: text without the system's
// font smoothing, the :focus-visible outline (2 pt, 1 pt off the control),
// 1 px borders (2 device pixels at 2×, still 1 at 1×) and --r-xl dialogs.
func TestApplyTextAndLines(t *testing.T) {
	th := rex.Theme()
	a3lab.Apply(th)
	for _, sc := range []float32{1, 2} {
		r := th.Resolve(fakeui.New(), sc, theme.Light)
		if !r.Antialiased || !r.UITheme().NoFontSmoothing {
			t.Errorf("scale %v: text is drawn with font smoothing", sc)
		}
		if r.Lines.Hairline != sc || r.Lines.FocusWidth != 2*sc || r.Lines.FocusOutset != 3*sc {
			t.Errorf("scale %v: hairline %v, focus ring %v wide %v out; want %v, %v, %v", sc, r.Lines.Hairline, r.Lines.FocusWidth, r.Lines.FocusOutset, sc, 2*sc, 3*sc)
		}
		if r.Radii.Sheet != 12*sc {
			t.Errorf("scale %v: sheet radius %v, want %v", sc, r.Radii.Sheet, 12*sc)
		}
	}
	// Rex alone keeps AppKit's: smoothing on, a hairline of one device pixel.
	r := rex.Theme().Resolve(fakeui.New(), 2, theme.Light)
	if r.Antialiased || r.Lines.Hairline != 1 {
		t.Errorf("Rex: antialiased %v, hairline %v px; want false, 1", r.Antialiased, r.Lines.Hairline)
	}
}
