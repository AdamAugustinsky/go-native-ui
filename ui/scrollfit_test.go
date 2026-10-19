package ui

import (
	"bufio"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The AppKit comparison: every scenario of testdata/appkit-scroll was replayed
// into a live NSScrollView by tools/appkitref/scroll.sh, which recorded its
// offset over time (NAME.appkit). Here the same events, at the times AppKit
// got them, drive a Context (frames at 120 Hz and at every event), and the
// offsets must track AppKit's:
//
//   - within 1.5 pt plus 6 % of AppKit's stretch past the edge,
//   - at some time within one frame (±8.4 ms) of the AppKit sample, after
//     removing a constant display latency fitted per scenario (−10 to 34 ms):
//     AppKit shows an event on the display frame after it (but moves its
//     clip view at once when the band is not involved), and its samples are
//     taken by a timer.
//
// The constants are also fitted from the recordings directly and must be
// within 3 % (the band's c) and 5 % (the spring's k) of ui's.

type fitEvent struct {
	t               float64 // ms, when AppKit got it
	line            bool
	phase, momentum ScrollPhase
	dx, dy          float64
	sched           float64
}

type fitScenario struct {
	name      string
	view, doc [2]float64
	start     [2]float64
	nested    bool
	evs       []fitEvent
	end       float64
	ref       [][4]float64 // t, x, y, outer y
}

func parsePhase(t *testing.T, s string) ScrollPhase {
	switch s {
	case "none":
		return PhaseNone
	case "maybegin":
		return PhaseMayBegin
	case "began":
		return PhaseBegan
	case "changed":
		return PhaseChanged
	case "ended":
		return PhaseEnded
	case "cancelled":
		return PhaseCancelled
	case "stationary":
		return PhaseStationary
	}
	t.Fatalf("bad phase %q", s)
	return 0
}

func loadFit(t *testing.T, name string) *fitScenario {
	t.Helper()
	sc := &fitScenario{name: name, view: [2]float64{400, 400}, doc: [2]float64{400, 2000}}
	num := func(s string) float64 {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	lines := func(path string, f func([]string)) {
		fh, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer fh.Close()
		s := bufio.NewScanner(fh)
		for s.Scan() {
			l := s.Text()
			if i := strings.IndexByte(l, '#'); i >= 0 {
				l = l[:i]
			}
			if f2 := strings.Fields(l); len(f2) > 0 {
				f(f2)
			}
		}
	}
	dir := filepath.Join("testdata", "appkit-scroll")
	lines(filepath.Join(dir, name+".txt"), func(f []string) {
		switch f[0] {
		case "view":
			sc.view = [2]float64{num(f[1]), num(f[2])}
		case "doc":
			sc.doc = [2]float64{num(f[1]), num(f[2])}
		case "start":
			sc.start = [2]float64{num(f[1]), num(f[2])}
		case "nested":
			sc.nested = true
		case "end":
			sc.end = num(f[1])
		case "ev":
			sc.evs = append(sc.evs, fitEvent{sched: num(f[1]), line: f[2] == "l", phase: parsePhase(t, f[3]),
				momentum: parsePhase(t, f[4]), dx: num(f[5]), dy: num(f[6])})
		}
	})
	n := 0
	lines(filepath.Join(dir, name+".appkit"), func(f []string) {
		if f[0] == "e" {
			sc.evs[n].t = num(f[2]) // when AppKit got it
			n++
			return
		}
		r := [4]float64{num(f[0]), num(f[1]), num(f[2])}
		if len(f) > 3 {
			r[3] = num(f[3])
		}
		sc.ref = append(sc.ref, r)
	})
	if n != len(sc.evs) {
		t.Fatalf("%s: AppKit got %d of %d events", name, n, len(sc.evs))
	}
	return sc
}

// run replays the scenario into a Context at 120 Hz and returns its offsets
// (scroll plus stretch) after every frame: t, x, y, outer y.
func (sc *fitScenario) run(t *testing.T) [][4]float64 {
	h := newHarness(t, 1000, 1200)
	h.c.WheelJumps = true // as app sets it on macOS
	var box, outer ID
	fl := Clip | ScrollY
	if sc.doc[0] > sc.view[0] {
		fl |= ScrollX
	}
	build := func(c *Context) {
		if sc.nested {
			c.NextSize(Px(float32(sc.view[0])), Px(float32(sc.view[1]+200)))
			outer = c.Open(ScrollY|Clip, "outer")
			c.NextSize(Px(float32(sc.view[0])), Px(200))
			c.Box(0, "above")
		}
		c.NextSize(Px(float32(sc.view[0])), Px(float32(sc.view[1])))
		box = c.Open(fl, "box")
		c.NextSize(Px(float32(sc.doc[0])), Px(float32(sc.doc[1])))
		c.Box(0, "doc")
		c.Close()
		if sc.nested {
			c.NextSize(Px(float32(sc.view[0])), Px(600))
			c.Box(0, "below")
			c.Close()
		}
	}
	h.frame(build)
	h.frame(func(c *Context) {
		build(c)
		c.SetScroll(box, sc.start[0], sc.start[1])
		if sc.nested {
			c.SetScroll(outer, 0, 100)
		}
	})
	h.frame(build)
	r := h.c.Rect(box)
	h.move((r[0]+r[2])/2, (r[1]+r[3])/2)
	// Frames at 120 Hz and at every event: AppKit moves its clip view as an
	// event comes (unless the band is involved), and its samples follow the
	// events; ui's springs run on the frames' clock either way.
	const dt = 1000.0 / 120
	var times []float64
	for tf := 0.0; tf <= sc.end; tf += dt {
		times = append(times, tf)
	}
	for _, e := range sc.evs {
		times = append(times, e.t)
	}
	slices.Sort(times)
	var out [][4]float64
	next, last := 0, 0.0
	for _, tf := range times {
		for ; next < len(sc.evs) && sc.evs[next].t <= tf; next++ {
			e := &sc.evs[next]
			se := ScrollEvent{DX: float32(e.dx), DY: float32(e.dy), Phase: e.phase, Momentum: e.momentum, Precise: !e.line,
				Time: int64(e.t * 1e6)}
			if e.line { // lines of 10 points, as notches of the wheel step (app's translator)
				se.DX, se.DY = float32(e.dx*10)/h.c.WheelStep, float32(e.dy*10)/h.c.WheelStep
			}
			h.in.AddScrollEvent(se)
		}
		step := tf - last
		last = tf
		h.in.Time = int64(tf * 1e6)
		h.c.Begin(&h.in, h.w, h.h, 1, float32(step/1000))
		build(h.c)
		h.c.End(&h.list)
		h.in.ClearFrame()
		x, y := h.c.Scroll(box)
		sx, sy := h.c.ScrollStretch(box)
		var oy float64
		if sc.nested {
			_, oy = h.c.Scroll(outer)
			_, s := h.c.ScrollStretch(outer)
			oy += s
		}
		out = append(out, [4]float64{tf, x + sx, y + sy, oy})
	}
	return out
}

// stretchPast is how far offset y is past the edges of [0, hi].
func stretchPast(y, hi float64) float64 {
	switch {
	case y < 0:
		return -y
	case y > hi:
		return y - hi
	}
	return 0
}

// compare reports the worst excess over the tolerance of ours against
// AppKit's samples with a display latency lag (ms), and where it is.
func (sc *fitScenario) compare(ours [][4]float64, lag float64) (worst float64, at [4]float64, got [4]float64) {
	worst = math.Inf(-1)
	hi := [2]float64{math.Max(0, sc.doc[0]-sc.view[0]), math.Max(0, sc.doc[1]-sc.view[1])}
	for _, ref := range sc.ref {
		best := math.Inf(1)
		var bestO [4]float64
		for _, o := range ours {
			if math.Abs(o[0]+lag-ref[0]) > 1000.0/120+0.1 {
				continue
			}
			e := math.Inf(-1)
			for a := 0; a < 2; a++ {
				tol := 1.5 + 0.06*stretchPast(ref[1+a], hi[a])
				e = math.Max(e, math.Abs(o[1+a]-ref[1+a])-tol)
			}
			if sc.nested {
				e = math.Max(e, math.Abs(o[3]-ref[3])-1.5)
			}
			if e < best {
				best, bestO = e, o
			}
		}
		if best > worst {
			worst, at, got = best, ref, bestO
		}
	}
	return
}

var fitNames = []string{
	"pull-5x40", "pull-20x40", "pull-20x150", "pull-20x150-v200", "pull-20x40-v200", "pull-20x40-v800",
	"reverse", "catch", "regrab", "wheel-top", "wheel-mid", "drag-mid", "axis1", "axis2", "axis3", "nest-top", "nest-cross",
}

// TestScrollTracksAppKit replays every scenario. Not compared: pull-bottom,
// where NSScrollView applies the first event past the bottom edge undamped
// (its pinned state lags an event: 30 pt more stretch), which ui does not
// copy.
func TestScrollTracksAppKit(t *testing.T) {
	for _, name := range fitNames {
		sc := loadFit(t, name)
		ours := sc.run(t)
		bestLag, best := 0.0, math.Inf(1)
		var at, got [4]float64
		for lag := -10.0; lag <= 34; lag++ {
			if w, a, g := sc.compare(ours, lag); w < best {
				bestLag, best, at, got = lag, w, a, g
			}
		}
		if best > 0 {
			t.Errorf("%s: off AppKit by %.2f pt beyond the tolerance at %.1f ms: AppKit %.1f, %.1f (outer %.1f), ui %.1f, %.1f (outer %.1f) (latency %v ms)",
				name, best, at[0], at[1], at[2], at[3], got[1], got[2], got[3], bestLag)
		} else {
			t.Logf("%s: within tolerance (margin %.2f pt, latency %v ms)", name, -best, bestLag)
		}
	}
}

// TestScrollConstantsFitAppKit fits the band's c and the spring's k to the
// recordings and checks ui's constants against them.
func TestScrollConstantsFitAppKit(t *testing.T) {
	var cs, ks []float64
	for _, name := range []string{"pull-5x40", "pull-20x40", "pull-20x150", "pull-20x150-v200", "pull-20x40-v200", "pull-20x40-v800"} {
		sc := loadFit(t, name)
		force := 0.0
		for _, e := range sc.evs {
			force += e.dy
		}
		peak := 0.0
		for _, r := range sc.ref {
			peak = math.Max(peak, -r[2])
		}
		// AppKit shows whole points, rounded up: the stretch is half a point less.
		s, d := peak-0.5, sc.view[1]
		cs = append(cs, d/force*(1/(1-s/d)-1))
		// the spring: ln|y| against t after the release, while it is clear of the rounding
		var rel float64
		for _, e := range sc.evs {
			if e.phase == PhaseEnded {
				rel = e.t
			}
		}
		var n, st, sy, stt, sty float64
		for _, r := range sc.ref {
			if r[0] > rel+10 && -r[2] > 4 && -r[2] < peak-1 {
				x, y := r[0]/1000, math.Log(-r[2])
				n, st, sy, stt, sty = n+1, st+x, sy+y, stt+x*x, sty+x*y
			}
		}
		if n >= 4 {
			ks = append(ks, -(n*sty-st*sy)/(n*stt-st*st))
		}
	}
	mean := func(v []float64) float64 {
		s := 0.0
		for _, x := range v {
			s += x
		}
		return s / float64(len(v))
	}
	c, k := mean(cs), mean(ks)
	t.Logf("fitted c = %.4f (each %.4f), k = %.2f/s (each %.2f); ui uses c = %v, k = %v", c, cs, k, ks, rubberC, springK)
	if math.Abs(c-rubberC)/rubberC > 0.03 {
		t.Errorf("the band's c fits %.4f, ui uses %v", c, rubberC)
	}
	if math.Abs(k-springK)/springK > 0.05 {
		t.Errorf("the spring's k fits %.2f, ui uses %v", k, springK)
	}
}

func TestScrollFitDump(t *testing.T) {
	name := os.Getenv("FITDUMP")
	if name == "" {
		t.Skip()
	}
	sc := loadFit(t, name)
	ours := sc.run(t)
	j := 0
	for _, r := range sc.ref {
		for j+1 < len(ours) && ours[j+1][0] <= r[0] {
			j++
		}
		t.Logf("%7.1f appkit %8.2f %8.2f   ui(%6.1f) %8.2f %8.2f", r[0], r[1], r[2], ours[j][0], ours[j][1], ours[j][2])
	}
}
