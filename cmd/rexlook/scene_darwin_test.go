//go:build darwin

package main

import (
	"math"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

// The scene must rebuild for any window size and scale (live resize, a 1×
// display) and keep its layout: three panes inside the gutters, an opaque
// white focused pane, and a per-frame rebuild that allocates nothing.
func TestSceneSizes(t *testing.T) {
	runtime.LockOSThread()
	ts, err := text.New()
	if err != nil {
		t.Skip("no text system:", err)
	}
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		t.Skip("no Metal device:", err)
	}
	defer r.Destroy()

	sc := newScene(ts, r, 2114, 1278, 2, false)
	for _, c := range []struct {
		w, h  int
		scale float32
	}{{2114, 1278, 2}, {1057, 639, 1}, {1600, 1000, 2}, {900, 600, 1}, {3000, 1800, 3}} {
		sc.build(c.w, c.h, c.scale)
		sc.frame()
		if sc.adv <= 0 || len(sc.list.Quads) < 500 {
			t.Fatalf("%dx%d@%v: empty scene (%d quads, advance %v)", c.w, c.h, c.scale, len(sc.list.Quads), sc.adv)
		}
		if got := float64(sc.adv) / float64(c.scale); math.Abs(got-7.5) > 0.05 {
			t.Errorf("%dx%d@%v: cell advance %.3f pt, want 7.5", c.w, c.h, c.scale, got)
		}
		p1, p2, p3 := sc.layout()
		for _, p := range []rect{p1, p2, p3} {
			if p.x0 < 0 || p.y0 < 0 || p.x1 > float32(c.w) || p.y1 > float32(c.h) || p.x1-p.x0 < 100 || p.y1-p.y0 < 100 {
				t.Errorf("%dx%d@%v: pane %+v outside the window", c.w, c.h, c.scale, p)
			}
		}
		if !(p1.x1 < p3.x0 && p1.y1 < p2.y0) {
			t.Errorf("%dx%d@%v: panes overlap: %+v %+v %+v", c.w, c.h, c.scale, p1, p2, p3)
		}
		pix := make([]byte, c.w*c.h*4)
		r.RenderToRGBA(c.w, c.h, clearColor, &sc.list, pix)
		at := func(x, y float32) [3]byte {
			o := (int(y)*c.w + int(x)) * 4
			return [3]byte{pix[o], pix[o+1], pix[o+2]}
		}
		// The band between the header and the first text row holds no text:
		// the focused pane is opaque white there.
		if got := at(p1.x0+150*c.scale, p1.y0+34*c.scale); got != [3]byte{255, 255, 255} {
			t.Errorf("%dx%d@%v: focused pane interior = %v, want white", c.w, c.h, c.scale, got)
		}
		// The gutter between the panes shows the background, not white.
		if got := at((p1.x1+p3.x0)/2, (p1.y0+p1.y1)/2); got == [3]byte{255, 255, 255} || got[0] < 150 {
			t.Errorf("%dx%d@%v: gutter = %v", c.w, c.h, c.scale, got)
		}
		// A warmed frame rebuild allocates nothing.
		if n := testing.AllocsPerRun(5, sc.frame); n != 0 {
			t.Errorf("%dx%d@%v: frame allocates %v objects", c.w, c.h, c.scale, n)
		}
	}
}
