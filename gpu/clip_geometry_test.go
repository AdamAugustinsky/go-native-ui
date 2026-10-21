//go:build darwin || linux || windows

package gpu

import "testing"

// A panel-sized border clipped to a strip must retain the fractional AA
// fringe, even when the strip's geometry is much smaller than the panel.
func TestClipGeometryFringe(t *testing.T) {
	r := newTestRenderer(t)
	q := solid(2, 2, 62, 62, 0)
	q.Border, q.BorderColor = 2, 0xFFFFFFFF
	q.Clip = [4]float32{2.75, 8.75, 3.25, 55.25}
	m := render(r, 64, 64, transparent, q)
	near(t, "strip left fringe", m.at(2, 20), [4]int{64, 64, 64, 64}, 1)
	near(t, "strip right fringe", m.at(3, 20), [4]int{64, 64, 64, 64}, 1)
	near(t, "strip top fringe", m.at(2, 8), [4]int{16, 16, 16, 16}, 1)
	near(t, "strip bottom fringe", m.at(2, 55), [4]int{16, 16, 16, 16}, 1)
	near(t, "clipped panel edge", m.at(60, 20), [4]int{}, 0)
	q.Clip = [4]float32{70, 70, 80, 80}
	m = render(r, 64, 64, transparent, q)
	for i, v := range m.pix {
		if v != 0 {
			t.Fatalf("disjoint clip: byte %d = %d, want zero", i, v)
		}
	}
}
