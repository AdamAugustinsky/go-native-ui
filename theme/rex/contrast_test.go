package rex

import (
	"fmt"
	"testing"

	"gonativeui/internal/fakeui"
	"gonativeui/theme"
)

// The contrast floors of tokens v2 (D39, the plan's "Design tokens v2"),
// checked composited (theme.ContrastOn) on Rex's resolved roles:
//
//   - Primary text (Label, and the other text roles drawn on a fill: text on
//     a selection, on the sidebar, the title) is at least 4.5:1 on every
//     background it is used on, Rex's field and glass included.
//   - Secondary, tertiary and quaternary text and placeholders are at least
//     AppKit's own composited contrast for the role in the same appearance.
//     The plan names AppKit's contrast on windowBackgroundColor; AppKit itself
//     sets these roles on surfaces darker than its window (light Label2 is
//     3.95:1 on the window, 3.84:1 on the sidebar, 3.89:1 on a stripe), so
//     the floor is the weakest of AppKit's own pairs: the window, the group
//     box, the stripe, the sidebar, the toolbar capsule and the text field.
//     This floor is AppKit's, not a ratio of ours. Rex's own backgrounds
//     (the pane, the raised surface and the unfocused glass) are held to it,
//     less one 8-bit step (quantum).
//   - White on the accent and on accent selections is AppKit's rule (D39)
//     and is reported, not held to 4.5:1: the user picks the accent.
//   - Secondary roles are not paired with the bare field: light Label2 on it
//     would be about 3.65:1, weaker than anywhere AppKit puts it. Text over
//     the field (Rex's titlebar band, its tabs) is primary.
//
// The appearances are light and dark from the measured values everywhere,
// and on macOS also from the live system's (the high-contrast ones when the
// machine has Increase Contrast on: AppKit makes them only from the setting).

// quantum is the allowance under a floor for 8-bit colour: one step of 1/255
// in a background moves a contrast by up to about 0.5 %.
const quantum = 0.005

// appearance is one appearance's resolved Rex roles and the field it is drawn
// over.
type appearance struct {
	name  string
	c     theme.Colors
	model *model
	hc    bool
}

// liveAppearances returns the appearances read from the system; set on
// macOS.
var liveAppearances func(t *testing.T) []appearance

// rexRoles resolves Rex's roles in mode m with the system colours sys (nil:
// the theme's own).
func rexRoles(m theme.Mode, sys *theme.SystemValues) theme.Colors {
	r := Theme().Resolve(fakeui.New(), 2, m)
	if sys != nil {
		r.SetSystem(sys)
	}
	r.Mix(m.T())
	return r.Colors
}

func appearances(t *testing.T) []appearance {
	out := []appearance{
		{"light (measured)", rexRoles(theme.Light, nil), &lightModel, false},
		{"dark (measured)", rexRoles(theme.Dark, nil), &darkModel, false},
	}
	if liveAppearances != nil {
		out = append(out, liveAppearances(t)...)
	}
	return out
}

type ground struct {
	name      string
	bg, under uint32
}

// samples are the field's and the unfocused glass's colours over a window of
// the reference's size.
func samples(m *model) (field, glass []ground) {
	for y := float32(0); y <= 1278; y += 71 {
		for x := float32(0); x <= refW; x += 106 {
			f, g := m.bgAt(x, refW-x, y).rgba(255), m.fillAt(refW, x, y)
			field = append(field, ground{fmt.Sprintf("field(%v,%v)", x, y), f, f})
			glass = append(glass, ground{fmt.Sprintf("glass(%v,%v)", x, y), g, g})
		}
	}
	return field, glass
}

func TestContrastFloors(t *testing.T) {
	for _, a := range appearances(t) {
		t.Run(a.name, func(t *testing.T) {
			c := &a.c
			appkit := []ground{
				{"Window", c[theme.Window], c[theme.Window]},
				{"GroupBox", c[theme.GroupBox], c[theme.GroupBox]},
				{"AltRow", c[theme.AltRow], c[theme.Window]},
				{"SidebarFill", c[theme.SidebarFill], c[theme.SidebarFill]},
				{"CapsuleFill", c[theme.CapsuleFill], c[theme.CapsuleFill]},
				{"InputFill", c[theme.InputFill], c[theme.Window]},
				{"ControlBackground", c[theme.ControlBackground], c[theme.ControlBackground]},
				{"TextBackground", c[theme.TextBackground], c[theme.TextBackground]},
			}
			field, glass := samples(a.model)
			rex := append([]ground{
				{"Pane", c[theme.Pane], c[theme.Pane]},
				{"Raised", c[theme.Raised], c[theme.Raised]},
			}, glass...)

			// Primary text, on everything it is used on.
			all := append(append(append([]ground{}, appkit...), rex...), field...)
			all = append(all, ground{"Fill2 (button)", c[theme.Fill2], c[theme.Window]}, ground{"FillPressed", c[theme.FillPressed], c[theme.Window]},
				ground{"SelectionUnemph", c[theme.SelectionUnemph], c[theme.SelectionUnemph]}, ground{"SidebarSelUnemph", c[theme.SidebarSelUnemph], c[theme.SidebarSelUnemph]})
			worst := 99.0
			for _, g := range all {
				v := theme.ContrastOn(c[theme.Label], g.bg, g.under)
				worst = min(worst, v)
				if v < 4.5 {
					t.Errorf("Label on %s (%08X): %.2f:1, want >= 4.5", g.name, g.bg, v)
				}
			}
			t.Logf("Label: worst %.2f:1 over %d backgrounds", worst, len(all))
			for _, p := range []struct {
				name   string
				fg, bg theme.Role
				under  theme.Role
			}{
				{"SelectedText on TextSelection", theme.SelectedText, theme.TextSelection, theme.Window},
				{"SidebarLabel on SidebarFill", theme.SidebarLabel, theme.SidebarFill, theme.SidebarFill},
				{"TitleText on Window", theme.TitleText, theme.Window, theme.Window},
				{"Text on TextBackground", theme.Text, theme.TextBackground, theme.TextBackground},
				{"HeaderText on Window", theme.HeaderText, theme.Window, theme.Window},
			} {
				if v := theme.ContrastOn(c[p.fg], c[p.bg], c[p.under]); v < 4.5 {
					t.Errorf("%s: %.2f:1, want >= 4.5", p.name, v)
				} else {
					t.Logf("%s: %.2f:1", p.name, v)
				}
			}
			// White on the accent and on the selections the accent makes is
			// AppKit's rule (D39), whatever the user's accent gives: the blue
			// about 4.0:1 and 5.4:1, a pink accent less. Reported, not held.
			t.Logf("white on Accent %.2f:1, on Selection %.2f:1, on SidebarSel %.2f:1 (AppKit's white on the user's accent)",
				theme.ContrastOn(c[theme.OnAccent], c[theme.Accent], c[theme.Accent]),
				theme.ContrastOn(c[theme.OnSelection], c[theme.Selection], c[theme.Selection]),
				theme.ContrastOn(c[theme.OnAccent], c[theme.SidebarSel], c[theme.SidebarSel]))

			// Secondary roles: AppKit's own floor, Rex's backgrounds held to it.
			for _, role := range []theme.Role{theme.Label2, theme.Label3, theme.Label4, theme.Placeholder, theme.TextDisabled} {
				onWindow := theme.ContrastOn(c[role], c[theme.Window], c[theme.Window])
				floor := onWindow
				for _, g := range appkit {
					floor = min(floor, theme.ContrastOn(c[role], g.bg, g.under))
				}
				worst, at := 99.0, ""
				for _, g := range rex {
					if v := theme.ContrastOn(c[role], g.bg, g.under); v < worst {
						worst, at = v, g.name
					}
				}
				if worst < floor*(1-quantum) {
					t.Errorf("%v on %s: %.3f:1, below AppKit's own %.3f:1", role, at, worst, floor)
				}
				fw := 99.0
				for _, g := range field {
					fw = min(fw, theme.ContrastOn(c[role], g.bg, g.under))
				}
				t.Logf("%v: AppKit %.2f:1 on its window, %.2f:1 at its weakest; Rex's backgrounds %.2f:1 at worst (%s); the bare field would be %.2f:1 (not paired)",
					role, onWindow, floor, worst, at, fw)
			}
		})
	}
}
