package pilot

import (
	"image"
	"runtime"
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// selectedRowFill is the colour of the first selected row of the last frame,
// a few pixels inside its right edge (past the text), as 0xRRGGBB.
func selectedRowFill(t *testing.T, img *image.RGBA, c *ui.Context) uint32 {
	t.Helper()
	var tr ui.SemTree
	c.Semantics(&tr)
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleRow && n.State&ui.SemSelected != 0 {
			x, y := int(n.Bounds[2])-3, int(n.Bounds[1]+n.Bounds[3])/2
			i := img.PixOffset(x, y)
			return uint32(img.Pix[i])<<16 | uint32(img.Pix[i+1])<<8 | uint32(img.Pix[i+2])
		}
	}
	t.Fatal("no selected row")
	return 0
}

func closeTo(a, b uint32) bool {
	d := func(x, y uint32) bool { return x+2 >= y && y+2 >= x }
	return d(a>>16&255, b>>16&255) && d(a>>8&255, b>>8&255) && d(a&255, b&255)
}

// Pilot follows the system's accent: the selected rows are drawn in it, and
// a change of the accent while the window is open takes them to the new one.
func TestFollowsTheSystemAccent(t *testing.T) {
	const green, pink = 0x32A852, 0xE0307A
	r := newDrive(t)
	o := r.o
	r.rowAt(0, 1)
	r.click(0) // a selected row, in the pane that has the keyboard
	settle := func(accent uint32) *image.RGBA {
		o.UI.SetSystemAppearance(ui.Appearance{Known: true, Accent: accent<<8 | 0xFF})
		var img *image.RGBA
		for n := 0; n < 600; n++ {
			img, _ = o.Frame()
			if n > 3 && !o.UI.Animating() {
				break
			}
		}
		return img
	}
	for _, want := range []uint32{green, pink, green} {
		if got := selectedRowFill(t, settle(want), o.UI); !closeTo(got, want) {
			t.Errorf("accent %06X: the selected row is %06X", want, got)
		}
	}
}

// Reduce Transparency makes the glass opaque: the window marks regions for the
// system's materials (macOS, with materials in the window) and under the
// setting only the toolbar's bar is left, with the fallback colours drawn
// for the rest.
func TestReduceTransparencyTakesTheMaterialsAway(t *testing.T) {
	count := func(rt bool) (side, bar int) {
		pl := New(SceneOptions())
		o := newOffscreen(t, pl)
		pl.bk.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{ReduceTransparency: rt}})
		pl.bk.Materials = true
		for n := 0; n < 6; n++ {
			o.Frame()
		}
		for _, r := range o.UI.MaterialRegions(nil) {
			switch r.Area {
			case ui.AreaSidebar:
				side++
			case ui.AreaTitlebar:
				bar++
			}
		}
		return
	}
	side, bar := count(false)
	if runtime.GOOS == "darwin" && side != 1 || bar != 1 {
		t.Errorf("materials on: %d sidebar regions, %d titlebar regions (darwin wants 1 and 1)", side, bar)
	}
	if side, bar = count(true); side != 0 || bar != 1 {
		t.Errorf("Reduce Transparency: %d sidebar regions, %d titlebar regions, want 0 and 1", side, bar)
	}
}

// Reduce Motion stills the decorative motion: with the system's setting alone
// (nothing of Pilot's own pins it) the rates of the theme's motion are the
// instant ones.
func TestFollowsReduceMotion(t *testing.T) {
	motion := func(rm bool) theme.Motion {
		r := newDriveWith(t, func(o *Options) { o.Still = false })
		r.d.Pilot.bk.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{ReduceMotion: rm}})
		r.frames(3)
		return r.d.Pilot.bk.Theme().Motion
	}
	normal, still := motion(false), motion(true)
	if normal == normal.Reduced() {
		t.Fatal("the theme's motion is already still without the setting")
	}
	if still != normal.Reduced() {
		t.Errorf("with Reduce Motion the motion is %+v, want %+v", still, normal.Reduced())
	}
}
