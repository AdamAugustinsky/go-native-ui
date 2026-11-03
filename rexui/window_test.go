package rexui_test

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
)

// TestBeginWindowRounded: a window with WindowOpts.Radius (a borderless
// panel) lays the field in rounded corners, in the field's own colours where
// the square window has them, edged by a hairline; over the material the
// scrim takes the corners and no hairline is drawn.
func TestBeginWindowRounded(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		var o rexui.WindowOpts
		backdrop := false
		g := newRig(t, m, nil, func(g *rig) {
			g.k.Theme().SetBackdrop(backdrop)
			rexui.BeginWindow(g.k, o)
			rexui.EndWindow(g.k)
		})
		W, H := float32(g.w), float32(g.h)
		at := func(x, y float32) (gpu.Quad, bool) {
			for _, q := range g.list.Quads {
				if q.Rect[0] == x && q.Rect[1] == y {
					return q, true
				}
			}
			return gpu.Quad{}, false
		}
		square, ok := at(0, 0)
		if !ok || square.Radii != ([4]float32{}) {
			t.Fatalf("%v: the square window's field starts with %+v (found %v), want a square cell at 0,0", m, square, ok)
		}

		o.Radius = 20 // 40 px at 2x
		g.frames(2)
		tl, ok := at(0, 0)
		if !ok || tl.Radii[0] != 40 {
			t.Fatalf("%v: the rounded field's top-left cell is %+v (found %v), want its corner 40 px round", m, tl, ok)
		}
		if tl.Rect[2] < 80 || tl.Rect[3] < 80 {
			t.Errorf("%v: the top-left cell %v is smaller than its rounded corner (2×40 px)", m, tl.Rect)
		}
		if tl.Color[0] != square.Color[0] {
			t.Errorf("%v: the rounded corner is %08x, the square window's %08x: not the field's colour", m, tl.Color[0], square.Color[0])
		}
		var br, edge bool
		for _, q := range g.list.Quads {
			if q.Rect[2] == W && q.Rect[3] == H && q.Radii[2] == 40 && q.Rect[0] > 0 {
				br = true
			}
			if q.Rect == [4]float32{0, 0, W, H} && q.Radii[0] == 40 && q.Border > 0 {
				edge = true
			}
		}
		if !br {
			t.Errorf("%v: no field cell rounds the bottom-right corner", m)
		}
		if !edge {
			t.Errorf("%v: no hairline edges the rounded window", m)
		}

		backdrop = true
		g.frames(2)
		scrim, ok := at(0, 0)
		if !ok || scrim.Rect != [4]float32{0, 0, W, H} || scrim.Radii[0] != 40 || scrim.Color[0]&0xFF == 0xFF {
			t.Errorf("%v: over the material the window starts with %+v (found %v), want a translucent scrim 40 px round", m, scrim, ok)
		}
		for _, q := range g.list.Quads {
			if q.Border > 0 {
				t.Errorf("%v: over the material a border was drawn (%+v); the system edges its material", m, q)
			}
		}
	}
}
