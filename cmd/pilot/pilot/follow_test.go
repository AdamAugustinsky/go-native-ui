package pilot

import (
	"image"
	"runtime"
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// near counts the pixels within 2 levels of the colour 0xRRGGBB.
func near(img *image.RGBA, rgb uint32) (n int) {
	r, g, b := int(rgb>>16&255), int(rgb>>8&255), int(rgb&255)
	d := func(a uint8, w int) bool { return int(a) >= w-2 && int(a) <= w+2 }
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if d(img.Pix[i], r) && d(img.Pix[i+1], g) && d(img.Pix[i+2], b) {
			n++
		}
	}
	return n
}

// Pilot follows the system's accent: the selected rows are drawn in it, and
// a change of the accent while the window is open takes them to the new one.
func TestFollowsTheSystemAccent(t *testing.T) {
	const green, pink = 0x32A852, 0xE0307A
	pl := New(SceneOptions())
	o := newOffscreen(t, pl)
	o.UI.SetSystemAppearance(ui.Appearance{Known: true, Accent: green<<8 | 0xFF})
	settle := func() (img *image.RGBA) {
		for n := 0; n < 600; n++ {
			img, _ = o.Frame()
			if n > 3 && !o.UI.Animating() {
				break
			}
		}
		return img
	}
	img := settle()
	if g := near(img, green); g < 50 {
		t.Errorf("%d pixels in the system's green accent, want the selected rows in it", g)
	}
	o.UI.SetSystemAppearance(ui.Appearance{Known: true, Accent: pink<<8 | 0xFF})
	img = settle()
	if g, p := near(img, green), near(img, pink); g != 0 || p < 50 {
		t.Errorf("after the accent changed: %d pixels in the old accent, %d in the new", g, p)
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

// Reduce Motion stills the decorative motion: the focus ring of a field
// that just got the keyboard is at its full strength at once and nothing is
// left animating, where without it the ring fades in.
func TestReduceMotionStillsTheFocusRing(t *testing.T) {
	run := func(rm bool) (animatingAfter int) {
		r := newDriveWith(t, func(o *Options) { o.Still = false }) // the system's setting alone decides
		r.d.Pilot.bk.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{ReduceMotion: rm}})
		r.frames(4)
		r.key('F', ui.ModSuper)
		for n := 0; n < 3; n++ {
			r.frames(1)
			if r.o.UI.Animating() {
				animatingAfter++
			}
		}
		return
	}
	if on, off := run(true), run(false); on != 0 || off == 0 {
		t.Errorf("frames still animating after ⌘F: %d with Reduce Motion, %d without (want 0 and some)", on, off)
	}
}
