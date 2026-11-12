package theme

import (
	"testing"

	"gonativeui/internal/fakeui"
)

// lowContrast is an app's palette, light, whose secondary and tertiary text
// and off rim are too faint on its own surfaces: a warm grey window, a
// lighter sidebar.
func lowContrast() Colors {
	c := Measured[Light]
	c[Window], c[Pane], c[ControlBackground], c[TextBackground] = 0xF4F1ECFF, 0xFBF9F6FF, 0xFBF9F6FF, 0xFFFFFFFF
	c[SidebarFill], c[GroupBox], c[Raised], c[InputFill] = 0xEDE9E2FF, 0xEFEBE5FF, 0xFFFFFFFF, 0xFFFFFFFF
	c[Label], c[Text], c[ControlText] = 0x2B2522FF, 0x2B2522FF, 0x2B2522FF
	c[Label2] = 0x9A928CFF      // about 2.8:1 on the window
	c[Placeholder] = 0xB3ABA5FF // about 2.1:1
	c[Label3] = 0xC9C2BCFF      // about 1.6:1
	c[ControlRim] = 0x2B252226  // the label at 15 %: about 1.4:1
	return c
}

// The guard moves each guarded role that reads under its floor on one of
// its surfaces toward Label until it passes, and leaves the rest alone.
func TestContrastGuardRaisesAnAppsFaintRoles(t *testing.T) {
	th := &Theme{Roles: [2]Colors{lowContrast(), Measured[Dark]}}
	r := th.Resolve(fakeui.New(), 2, Light)
	in := lowContrast()
	for _, g := range guardedRoles {
		before, after := worstContrast(in[g.role], &in), worstContrast(r.Colors[g.role], &r.Colors)
		t.Logf("%v: %08X %.2f:1 → %08X %.2f:1 (floor %.1f)", g.role, in[g.role], before, r.Colors[g.role], after, g.floor)
		if after < g.floor {
			t.Errorf("%v reads %.2f:1 on its weakest surface after the guard, want %.1f", g.role, after, g.floor)
		}
		if before >= g.floor && r.Colors[g.role] != in[g.role] {
			t.Errorf("%v passed (%.2f:1) and was moved", g.role, before)
		}
	}
	// it moved toward the label, not away: the same hue family, darker
	if Luminance(r.Colors[Label2]) >= Luminance(in[Label2]) {
		t.Errorf("Label2 was not moved toward Label: %08X → %08X", in[Label2], r.Colors[Label2])
	}
	// the surfaces and the label are the app's
	for _, role := range [...]Role{Window, Pane, SidebarFill, Label, Accent} {
		if r.Colors[role] != in[role] {
			t.Errorf("%v moved: %08X → %08X", role, in[role], r.Colors[role])
		}
	}
}

// A palette whose label cannot be read on its own surfaces keeps the roles
// the app gave it: the guard mends by moving toward the label, and here that
// would only paint every role in the label's colour.
func TestContrastGuardLeavesAPaletteItCannotMend(t *testing.T) {
	in := lowContrast()
	in[Window], in[Label] = 0x112233FF, 0x102030FF // the label on the window: about 1:1
	th := &Theme{Roles: [2]Colors{in, Measured[Dark]}}
	r := th.Resolve(fakeui.New(), 2, Light)
	for _, g := range guardedRoles {
		if r.Colors[g.role] != in[g.role] {
			t.Errorf("%v: %08X → %08X, want it left alone", g.role, in[g.role], r.Colors[g.role])
		}
	}
}

// A theme that means its values (ExactColors), and a strict AppKit theme,
// keep them; so does a theme that follows the system's colours, for the
// roles the system supplies.
func TestContrastGuardStaysOff(t *testing.T) {
	in := lowContrast()
	for name, th := range map[string]*Theme{
		"ExactColors":  {Roles: [2]Colors{in, Measured[Dark]}, ExactColors: true},
		"StrictAppKit": {Roles: [2]Colors{in, Measured[Dark]}, StrictAppKit: true},
	} {
		r := th.Resolve(fakeui.New(), 2, Light)
		for _, g := range guardedRoles {
			if r.Colors[g.role] != in[g.role] {
				t.Errorf("%s: %v moved %08X → %08X", name, g.role, in[g.role], r.Colors[g.role])
			}
		}
	}
	th := &Theme{Roles: [2]Colors{in, Measured[Dark]}, SystemColors: true}
	r := th.Resolve(fakeui.New(), 2, Light)
	for _, role := range [...]Role{Label2, Label3, Placeholder} {
		if r.Colors[role] != in[role] {
			t.Errorf("following the system: %v moved %08X → %08X", role, in[role], r.Colors[role])
		}
	}
	if worstContrast(r.Colors[ControlRim], &r.Colors) < FloorIndicator {
		t.Error("following the system: the theme's own ControlRim was not guarded")
	}
	// a System role the theme owns is the theme's, and guarded
	th.Own[Label2] = true
	r = th.Resolve(fakeui.New(), 2, Light)
	if worstContrast(r.Colors[Label2], &r.Colors) < FloorText {
		t.Error("an owned Label2 was not guarded")
	}
}

// AppKit's own colours (a theme with no roles, or roles left at Measured's)
// come out as they are, light and dark: AppKit's Label2 is 3.95:1 on white
// and it is AppKit's to keep.
func TestContrastGuardKeepsAppKitsOwn(t *testing.T) {
	for m := range 2 {
		r := (&Theme{}).Resolve(fakeui.New(), 2, Mode(m))
		for _, g := range guardedRoles {
			want := Measured[m][g.role]
			if g.role.Source() == SourceTheme {
				continue // ControlRim is the theme's, and passes
			}
			if r.Colors[g.role] != want {
				t.Errorf("%v %v: %08X, Measured %08X", Mode(m), g.role, r.Colors[g.role], want)
			}
		}
		if v := worstContrast(r.Colors[ControlRim], &r.Colors); v < FloorIndicator {
			t.Errorf("mode %d: Measured's ControlRim reads %.2f:1", m, v)
		} else {
			t.Logf("mode %d: ControlRim %08X reads %.2f:1 at worst; Fill1 alone (the unchecked box) %.2f:1", m, r.Colors[ControlRim], v, worstContrast(r.Colors[Fill1], &r.Colors))
		}
	}
}

// An app that puts its palette into the labels gets the icon, hover and rim
// roles in its own colours unless it sets them.
func TestRolesFollowTheLabels(t *testing.T) {
	c := Measured[Light]
	c[Label], c[Label2] = 0x3A2E5CFF, 0x6B5F8AFF
	c[PaneButtonHover] = 0x112233FF // set by the app: kept
	r := (&Theme{Roles: [2]Colors{c, Measured[Dark]}, ExactColors: true}).Resolve(fakeui.New(), 2, Light)
	for _, w := range []struct {
		role Role
		want uint32
	}{
		{PaneButton, 0x6B5F8AFF}, {PaneButtonHover, 0x112233FF}, {PaneButtonChip, 0x3A2E5CFF},
		{RowHover, 0x3A2E5C00 | Measured[Light][RowHover]&0xFF}, {FillHover, 0x3A2E5C00 | Measured[Light][FillHover]&0xFF},
		{ControlRim, 0x3A2E5C00 | Measured[Light][ControlRim]&0xFF},
	} {
		if r.Colors[w.role] != w.want {
			t.Errorf("%v: %08X, want %08X", w.role, r.Colors[w.role], w.want)
		}
	}
	// a theme that keeps the labels keeps every follower
	r = (&Theme{}).Resolve(fakeui.New(), 2, Light)
	for _, f := range labelFollowers {
		if r.Colors[f.role] != Measured[Light][f.role] {
			t.Errorf("%v moved without a new label", f.role)
		}
	}
}

// The guard runs when the theme is resolved, not per frame: a mode switch
// and a retint allocate nothing.
func TestContrastGuardCostsNoFrame(t *testing.T) {
	th := &Theme{Roles: [2]Colors{lowContrast(), Measured[Dark]}}
	r := th.Resolve(fakeui.New(), 2, Light)
	if n := testing.AllocsPerRun(100, func() {
		r.Mix(0.5)
		r.Retint(0x00AA88FF, 0x22CCAAFF)
		r.Retint(0, 0)
		r.Mix(0)
	}); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}
