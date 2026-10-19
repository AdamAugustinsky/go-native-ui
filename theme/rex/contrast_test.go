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
//     AppKit itself sets these roles on surfaces darker than its window
//     (light Label2 is 3.95:1 on the window, 3.84:1 on the sidebar, 3.89:1
//     on a stripe), so the floor is the weakest of AppKit's own pairs (D39):
//     the window, the group box, the stripe, the sidebar, the toolbar capsule
//     and the text field. It is computed only from colours measured from
//     AppKit (the system's, live or measured, and theme.Measured's chrome
//     colours from D0), never from Rex's roles (appkitColors, floorSurfaces).
//     Rex's own backgrounds (the pane, the raised surface and the unfocused
//     glass) are held to it, less one 8-bit step (quantum).
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

// appearance is one appearance: Rex's resolved roles, AppKit's colours, and
// the field Rex draws over.
type appearance struct {
	name  string
	c     theme.Colors // Rex's roles
	ak    theme.Colors // AppKit's: the floor is computed from these only
	model *model
	hc    bool
}

// appkitColors are AppKit's colours in mode m: theme.Measured (the system
// colours and the window-chrome colours measured from live AppKit windows,
// D0), with the system's own colours sys in the System roles when given. No
// Rex role enters.
func appkitColors(m theme.Mode, sys *theme.SystemValues) theme.Colors {
	c := theme.Measured[m]
	if sys != nil && sys.Known {
		for i, v := range sys.Colors[m] {
			if v != 0 {
				c[i] = v
			}
		}
	}
	return c
}

// rexIdentity are the roles Rex defines itself (rex.go, roles.go): none of
// them may enter the floor.
var rexIdentity = map[theme.Role]bool{
	theme.Field: true, theme.Pane: true, theme.Raised: true, theme.PaneHairline: true,
	theme.PaneHighlight: true, theme.GlassTint: true, theme.Shadow: true, theme.Scrim: true,
}

// floorSurfaces are AppKit's own surfaces that it sets the secondary roles
// on, each with what it lies on: System roles and D0-measured chrome only.
var floorSurfaces = [][2]theme.Role{
	{theme.Window, theme.Window}, {theme.GroupBox, theme.GroupBox}, {theme.AltRow, theme.Window},
	{theme.SidebarFill, theme.SidebarFill}, {theme.CapsuleFill, theme.CapsuleFill}, {theme.InputFill, theme.Window},
	{theme.ControlBackground, theme.ControlBackground}, {theme.TextBackground, theme.TextBackground},
}

// floor is the floor of role in a: AppKit's, from a.ak only.
func (a *appearance) floor(role theme.Role) float64 { return appkitFloor(role, &a.ak) }

// appkitFloor is AppKit's weakest own contrast for role in the colours ak.
func appkitFloor(role theme.Role, ak *theme.Colors) float64 {
	floor := 99.0
	for _, g := range floorSurfaces {
		floor = min(floor, theme.ContrastOn(ak[role], ak[g[0]], ak[g[1]]))
	}
	return floor
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
		{"light (measured)", rexRoles(theme.Light, nil), appkitColors(theme.Light, nil), &lightModel, false},
		{"dark (measured)", rexRoles(theme.Dark, nil), appkitColors(theme.Dark, nil), &darkModel, false},
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
			c, ak := &a.c, &a.ak
			var appkit []ground
			for _, g := range floorSurfaces {
				appkit = append(appkit, ground{g[0].String(), ak[g[0]], ak[g[1]]})
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
				onWindow := theme.ContrastOn(ak[role], ak[theme.Window], ak[theme.Window])
				floor := a.floor(role)
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

// The floor comes from AppKit's colours alone: its surfaces are System roles
// or D0-measured chrome, and Rex changing its own roles (here every surface
// at once) does not move it.
func TestContrastFloorIsAppKitsOnly(t *testing.T) {
	for _, g := range floorSurfaces {
		for _, r := range g {
			if rexIdentity[r] {
				t.Errorf("the floor uses Rex's own role %v", r)
			}
		}
	}
	for _, a := range appearances(t) {
		rexDark := a
		for i := range rexDark.c {
			rexDark.c[i] = 0x000000FF
		}
		for _, role := range []theme.Role{theme.Label2, theme.Label3, theme.Placeholder} {
			if f, g := a.floor(role), rexDark.floor(role); f != g {
				t.Errorf("%s %v: the floor moved with Rex's roles (%.3f → %.3f)", a.name, role, f, g)
			}
			// The live or measured system colours are AppKit's: the roles
			// Rex leaves to the system are the same in both arrays.
			if a.c[role] != a.ak[role] {
				t.Errorf("%s %v: Rex %08X, AppKit %08X", a.name, role, a.c[role], a.ak[role])
			}
		}
	}
}
