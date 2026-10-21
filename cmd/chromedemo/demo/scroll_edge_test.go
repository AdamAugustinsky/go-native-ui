package demo

import (
	"sort"
	"testing"

	"gonativeui/internal/cielab"
	"gonativeui/theme"
)

// Finder's rows scrolled under the toolbar, seen through its scroll edge, in
// strips across the pocket clear of the toolbar's and the header's own parts
// on every platform (the band above and below the capsules, the header above
// and below its titles and separators), against AppKit's (captures-macmini,
// finder-*-scrolled, 26B5101f; its two selected rows tint it), light and
// dark, key and inactive.
//
// With the blur (gpu.Renderer.Backdrops) each pixel row's mean across the
// strips is within ΔE2000 1 of AppKit's on average, and 1.5 at worst (a
// level in dark), where the rows under it are plain (an inactive window:
// its selection is grey; measured 0.13 / 0.25, at worst 0.8 / 1.1), and 1.5
// and 2.5 where the key window's blue selection is (0.8 / 1.1, at worst 1.4
// / 2.2: AppKit's pocket is a little more saturated over blue in dark).
// Without it, the edge is opaque (ScrollEdgeLook.Under): no row shows (no
// pixel more than ΔE 2 from the pocket's median) and the pocket is the colour
// AppKit's averages to, within ΔE 2.
func TestFinderRowsUnderTheBand(t *testing.T) {
	regions := [][4]int{{190, 1, 880, 7}, {190, 45, 880, 51}, {190, 54, 880, 57}, {190, 74, 880, 79}} // points
	for _, c := range []struct {
		pref     theme.Preference
		inactive bool
		appkit   [3]uint8 // the mean over the regions
		rows     []uint32 // AppKit's mean of every other pixel row of the regions, top down
		plain    bool     // nothing saturated under the pocket
	}{
		{theme.ForceLight, false, [3]uint8{229, 241, 255}, []uint32{
			0xFFFFFF, 0xFFFFFF, 0xFFFFFF, 0xFFFFFF, 0xFFFFFF, 0xFFFFFF, 0xD4E7FE, 0xD4E7FE, 0xD4E7FE, 0xD3E7FE,
			0xD3E6FE, 0xD3E6FE, 0xD4E7FF, 0xD4E7FF, 0xD4E7FF, 0xE1EFFF, 0xE3F0FF, 0xE5F1FF, 0xE7F2FF, 0xE9F3FF}, false},
		{theme.ForceLight, true, [3]uint8{252, 252, 252}, []uint32{
			0xFFFFFF, 0xFFFFFF, 0xFFFFFF, 0xFFFFFF, 0xFFFFFF, 0xFFFFFF, 0xF8F8F8, 0xF9F9F9, 0xF9F9F9, 0xF9F9F9,
			0xF9F9F9, 0xF9F9F9, 0xF9FAF9, 0xF9FAF9, 0xF9F9F9, 0xFBFCFB, 0xFCFCFC, 0xFCFCFC, 0xFCFCFC, 0xFCFDFC}, true},
		{theme.ForceDark, false, [3]uint8{35, 46, 60}, []uint32{
			0x272727, 0x272727, 0x272727, 0x272727, 0x282828, 0x282828, 0x22324A, 0x21324A, 0x21324A, 0x21324A,
			0x20324A, 0x20324A, 0x20324A, 0x20324A, 0x20324A, 0x222E3E, 0x222D3D, 0x232D3B, 0x232D3A, 0x242C38}, false},
		{theme.ForceDark, true, [3]uint8{43, 43, 43}, []uint32{
			0x272727, 0x272727, 0x272727, 0x272727, 0x282828, 0x282828, 0x2F2F2F, 0x2F2F2F, 0x2F2F2F, 0x2E2E2E,
			0x2E2E2E, 0x2E2E2E, 0x2E2E2E, 0x2E2E2E, 0x2E2E2E, 0x2B2C2B, 0x2B2B2B, 0x2B2B2B, 0x2B2B2B, 0x2A2A2A}, true},
	} {
		off, dm, img, err := render(ShotOptions{Scene: Finder, Preference: c.pref, Inactive: c.inactive, Scrolled: true})
		if err != nil {
			t.Fatal(err)
		}
		blur := off.R.Backdrops()
		dm.Close() // the file icons' cache
		off.Close()
		type pixel struct {
			c    [3]uint8
			x, y int
		}
		var px []pixel
		var sum [3]int
		var rows [][3]uint8
		for _, r := range regions {
			for y := 2 * r[1]; y < 2*r[3]; y++ {
				var row [3]int
				n := 0
				for x := 2 * r[0]; x < 2*r[2]; x++ {
					p := img.PixOffset(x, y)
					v := [3]uint8{img.Pix[p], img.Pix[p+1], img.Pix[p+2]}
					for i := range row {
						row[i] += int(v[i])
					}
					n++
					if x%2 != 0 {
						continue
					}
					px = append(px, pixel{v, x, y})
					for i := range sum {
						sum[i] += int(v[i])
					}
				}
				rows = append(rows, [3]uint8{uint8((row[0] + n/2) / n), uint8((row[1] + n/2) / n), uint8((row[2] + n/2) / n)})
			}
		}
		if blur {
			limit := [2]float64{1.5, 2.5} // mean, row
			if c.plain {
				limit = [2]float64{1, 1.5}
			}
			worst, mean := 0.0, 0.0
			for i, want := range c.rows {
				got := rows[2*i]
				d := cielab.DeltaE2000(lab(got), lab([3]uint8{uint8(want >> 16), uint8(want >> 8), uint8(want)}))
				mean += d / float64(len(c.rows))
				if d > worst {
					worst = d
				}
				if d > limit[1] {
					t.Errorf("preference %d, inactive %v: the pocket's row %d is %v, AppKit's %06X (ΔE %.1f, want at most %.1f)", c.pref, c.inactive, 2*i, got, want, d, limit[1])
				}
			}
			t.Logf("preference %d, inactive %v: the blurred pocket's rows from AppKit's: ΔE2000 mean %.2f, max %.2f", c.pref, c.inactive, mean, worst)
			if mean > limit[0] {
				t.Errorf("preference %d, inactive %v: the blurred pocket's rows are ΔE2000 %.2f from AppKit's on average, want at most %.1f", c.pref, c.inactive, mean, limit[0])
			}
			continue
		}
		sort.Slice(px, func(i, j int) bool {
			a, b := px[i].c, px[j].c
			return int(a[0])+int(a[1])+int(a[2]) < int(b[0])+int(b[1])+int(b[2])
		})
		med := px[len(px)/2]
		worst, at := 0.0, med
		for _, v := range px {
			if d := cielab.DeltaE2000(lab(v.c), lab(med.c)); d > worst {
				worst, at = d, v
			}
		}
		if worst > 2 {
			t.Errorf("preference %d, inactive %v: rows show through the scroll edge (%v at (%d, %d) px, ΔE %.1f from the pocket's median %v)",
				c.pref, c.inactive, at.c, at.x, at.y, worst, med.c)
		}
		n := len(px)
		mean := [3]uint8{uint8((sum[0] + n/2) / n), uint8((sum[1] + n/2) / n), uint8((sum[2] + n/2) / n)}
		if d := cielab.DeltaE2000(lab(mean), lab(c.appkit)); d > 2 {
			t.Errorf("preference %d, inactive %v: the pocket %v, AppKit's %v (ΔE %.1f)", c.pref, c.inactive, mean, c.appkit, d)
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
			off, dm, img, err := render(ShotOptions{Scene: Finder, Preference: c.pref, Legacy: legacy})
			if err != nil {
				t.Fatal(err)
			}
			dm.Close() // the file icons' cache
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

// The settings sidebar's search field is filled as the Mac mini's AppKit
// fills it (captures-macmini settings-*-top, the field's middle at (150, 68)
// pt): #E9E9E9 / #565656, #E3E3E3 / #4D4D4D inactive, within ΔE 1.
func TestSidebarSearchFill(t *testing.T) {
	for _, c := range []struct {
		pref     theme.Preference
		inactive bool
		appkit   [3]uint8
	}{
		{theme.ForceLight, false, [3]uint8{233, 233, 233}}, {theme.ForceLight, true, [3]uint8{227, 227, 227}},
		{theme.ForceDark, false, [3]uint8{86, 86, 86}}, {theme.ForceDark, true, [3]uint8{77, 77, 77}},
	} {
		off, _, img, err := render(ShotOptions{Scene: Settings, Preference: c.pref, Inactive: c.inactive})
		if err != nil {
			t.Fatal(err)
		}
		off.Close()
		p := img.PixOffset(300, 136)
		got := [3]uint8{img.Pix[p], img.Pix[p+1], img.Pix[p+2]}
		if d := cielab.DeltaE2000(lab(got), lab(c.appkit)); d > 1 {
			t.Errorf("preference %d, inactive %v: the field's fill %v, AppKit's %v (ΔE %.1f)", c.pref, c.inactive, got, c.appkit, d)
		}
	}
}

// Settings' back and forward have AppKit's divider between them
// (captures-macmini settings-*-top): 1 pt wide at x 266 pt, from y 16 to 36
// pt, #C8C8C8 / #BCBCBC inactive in light, #707070 / #6E6E6E in dark, within
// ΔE 2; the capsule shows on either side of it.
func TestSettingsNavDivider(t *testing.T) {
	for _, c := range []struct {
		pref     theme.Preference
		inactive bool
		appkit   uint8
	}{
		{theme.ForceLight, false, 200}, {theme.ForceLight, true, 188},
		{theme.ForceDark, false, 112}, {theme.ForceDark, true, 110},
	} {
		off, _, img, err := render(ShotOptions{Scene: Settings, Preference: c.pref, Inactive: c.inactive})
		if err != nil {
			t.Fatal(err)
		}
		off.Close()
		at := func(x, y int) [3]uint8 {
			p := img.PixOffset(x, y)
			return [3]uint8{img.Pix[p], img.Pix[p+1], img.Pix[p+2]}
		}
		want := lab([3]uint8{c.appkit, c.appkit, c.appkit})
		for _, p := range [][2]int{{532, 33}, {533, 52}, {532, 70}} {
			if d := cielab.DeltaE2000(lab(at(p[0], p[1])), want); d > 2 {
				t.Errorf("preference %d, inactive %v: the divider at %v px is %v, AppKit's %d (ΔE %.1f)", c.pref, c.inactive, p, at(p[0], p[1]), c.appkit, d)
			}
		}
		for _, p := range [][2]int{{531, 52}, {534, 52}, {532, 30}, {532, 73}} {
			if d := cielab.DeltaE2000(lab(at(p[0], p[1])), want); d < 5 {
				t.Errorf("preference %d, inactive %v: the divider reaches %v px", c.pref, c.inactive, p)
			}
		}
	}
}
