package demo

import (
	"testing"

	"gonativeui/internal/cielab"
	"gonativeui/theme"
)

// Rows scrolled under the toolbar show through its scroll edge as AppKit's
// do: Finder's selected rows under the band, at (440, 40) pt, against the
// Mac mini's captures (captures-macmini, finder-*-scrolled, 26B5101f) in
// light and dark, key and inactive, within ΔE 2.
func TestFinderRowsUnderTheBand(t *testing.T) {
	for _, c := range []struct {
		pref     theme.Preference
		inactive bool
		appkit   [3]uint8
	}{
		{theme.ForceLight, false, [3]uint8{210, 230, 255}}, {theme.ForceLight, true, [3]uint8{250, 250, 250}},
		{theme.ForceDark, false, [3]uint8{31, 49, 75}}, {theme.ForceDark, true, [3]uint8{45, 45, 45}},
	} {
		off, _, img, err := render(ShotOptions{Scene: Finder, Preference: c.pref, Inactive: c.inactive, Scrolled: true})
		if err != nil {
			t.Fatal(err)
		}
		off.Close()
		p := img.PixOffset(880, 80)
		got := [3]uint8{img.Pix[p], img.Pix[p+1], img.Pix[p+2]}
		if d := cielab.DeltaE2000(lab(got), lab(c.appkit)); d > 2 {
			t.Errorf("preference %d, inactive %v: %v under the band, AppKit's %v (ΔE %.1f)", c.pref, c.inactive, got, c.appkit, d)
		}
	}
}

func lab(c [3]uint8) cielab.Lab { return cielab.SRGBToLab(c[0], c[1], c[2]) }

// The file list's header, pinned below the band over the rows, keeps its line
// and the pocket's bar at rest beside a legacy scroll bar as with overlay
// ones: at (300, 66) the bar, at (300, 79.5) the line, against the Mac
// mini's finder-*-active-top captures, within ΔE 2.
func TestFinderHeaderInThePocket(t *testing.T) {
	for _, c := range []struct {
		pref      theme.Preference
		bar, line [3]uint8
	}{
		{theme.ForceLight, [3]uint8{255, 255, 255}, [3]uint8{229, 229, 229}},
		{theme.ForceDark, [3]uint8{38, 38, 38}, [3]uint8{64, 64, 64}},
	} {
		for _, legacy := range []bool{false, true} {
			off, _, img, err := render(ShotOptions{Scene: Finder, Preference: c.pref, Legacy: legacy})
			if err != nil {
				t.Fatal(err)
			}
			off.Close()
			for _, p := range []struct {
				y    int
				want [3]uint8
			}{{132, c.bar}, {159, c.line}} {
				o := img.PixOffset(600, p.y)
				got := [3]uint8{img.Pix[o], img.Pix[o+1], img.Pix[o+2]}
				if d := cielab.DeltaE2000(lab(got), lab(p.want)); d > 2 {
					t.Errorf("preference %d, legacy %v: %v at y %v pt, AppKit's %v (ΔE %.1f)", c.pref, legacy, got, float32(p.y)/2, p.want, d)
				}
			}
		}
	}
}
