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
