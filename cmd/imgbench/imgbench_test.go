package main

import (
	"os"
	"runtime"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/platform"
	"gonativeui/ui"
)

func newOffscreen(t testing.TB, b app.Builder) *app.Offscreen {
	t.Helper()
	// 1× keeps the readback of every frame cheap; the view holds the same
	// ~200 thumbnails at any scale.
	o, err := app.NewOffscreen(config, winW, winH, 1, b)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("offscreen rendering on CI: %v", err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	t.Cleanup(o.Close)
	return o
}

// TestCachedSessionsAllocateNothing runs the bench's script offscreen: the cold
// pass over the whole grid, then sessions scrolling about 200 cached
// thumbnails a frame, of which every one must be drawn, and a whole session
// must allocate nothing (exact ReadMemStats deltas, the first clean of five,
// D27).
func TestCachedSessionsAllocateNothing(t *testing.T) {
	b := newBenchGrid(defItems)
	o := newOffscreen(t, b)
	t.Cleanup(func() {
		if b.cache != nil {
			b.cache.Close(time.Second)
		}
	})
	deadline := time.Now().Add(180 * time.Second) // the standard decoders on a slow runner
	for b.s.phase != phSession || b.s.next != 0 {
		o.Frame()
		if time.Now().After(deadline) {
			t.Fatalf("the script did not reach the sessions (phase %d): %+v", b.s.phase, b.cache.Stats())
		}
	}
	period := b.s.period()
	for i := 0; i < period; i++ { // one session warms the frame path
		o.Frame()
	}
	var mallocs, bytes uint64
	for a := 0; a < 5; a++ {
		vis0, ready0 := b.s.visSum, b.s.readySum
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < period; i++ {
			o.Frame()
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		vis, ready := b.s.visSum-vis0, b.s.readySum-ready0
		t.Logf("session %d: %d mallocs, %d bytes; %.1f images in view per frame, %d placeholders", a+1, mallocs, bytes, float64(vis)/float64(period), vis-ready)
		if float64(vis)/float64(period) < 150 || ready != vis {
			t.Fatalf("%d images over %d frames, %d of them drawn: want about 200 a frame, all cached", vis, period, ready)
		}
		if mallocs == 0 && bytes == 0 {
			st := b.cache.Stats()
			t.Logf("cache: %d decodes (%.2f ms each on a worker), %d uploads, %d GPU evictions, %d pages, %.1f MB GPU",
				st.Decodes, float64(st.DecodeNs)/1e6/float64(max(st.Decodes, 1)), st.Uploads, st.EvictedGPU, st.Pages, float64(st.GPUBytes)/(1<<20))
			return
		}
	}
	t.Fatalf("every session allocated (last: %d mallocs, %d bytes)", mallocs, bytes)
}

// TestIdleOnceLoaded: once the view has loaded, a frame asks for no other:
// with nothing to decode or upload the window draws nothing.
func TestIdleOnceLoaded(t *testing.T) {
	g := newGrid(500, img.Options{})
	o := newOffscreen(t, g)
	t.Cleanup(func() {
		if g.cache != nil {
			g.cache.Close(time.Second)
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		o.Frame()
		if g.visible > 0 && g.ready == g.visible && !o.App.UI.Animating() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the view did not load: %d of %d ready, %+v", g.ready, g.visible, g.cache.Stats())
		}
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		o.Frame()
		if o.App.UI.Animating() {
			t.Fatalf("frame %d after loading asked for another frame", i)
		}
	}
}

// TestGate: a clean run passes; allocations, placeholders and a slow Poll
// fail it.
func TestGate(t *testing.T) {
	var ok report
	ok.Frames, ok.Paced, ok.VisibleMean, ok.VisibleReady = 144, true, 200, 1
	ok.Cache.EvictedGPU = 10
	ok.Cold.PollCPUMs = app.Stats{Max: 1, Mean: 0.2}
	if f := gateFailures(ok, 144, false, defaultMaxPollMs); len(f) != 0 {
		t.Fatalf("a clean run failed: %v", f)
	}
	for _, bad := range []func(r *report){
		func(r *report) { r.Mallocs = 1 },
		func(r *report) { r.VisibleReady = 0.99 },
		func(r *report) { r.Cold.PollCPUMs.Mean = 5 },
		func(r *report) { r.Cache.EvictedGPU = 0 },
	} {
		r := ok
		bad(&r)
		if f := gateFailures(r, 144, false, defaultMaxPollMs); len(f) == 0 {
			t.Errorf("a failing run passed: %+v", r)
		}
	}
}

// TestAnimatedSamples: the first four cells are animations (a GIF and an APNG
// of a disc, a GIF and an APNG of a colour ramp), made here; they decode and
// play while in view, and scrolled away they ask for nothing.
func TestAnimatedSamples(t *testing.T) {
	g := newGrid(200, img.Options{})
	g.anim = true
	o := newOffscreen(t, &clocked{grid: g})
	t.Cleanup(func() {
		if g.cache != nil {
			g.cache.Close(time.Second)
		}
	})
	deadline := time.Now().Add(60 * time.Second)
	for g.cache == nil || g.cache.Stats().Animations < 4 {
		o.Frame()
		time.Sleep(time.Millisecond)
		if time.Now().After(deadline) {
			t.Fatalf("animations never started: %+v", g.cache.Stats())
		}
	}
	for i := 0; i < 4; i++ {
		h, _, ok := g.cache.Get(img.Key{Hash: animHash + uint64(i), W: 56, H: 56, Flags: img.Animate})
		if !ok || !g.cache.Animated(h) {
			t.Errorf("sample %d (%s) is not an animation", i, g.samples[i].name)
		}
	}
	// In view they play: frames are decoded after the first.
	for end := time.Now().Add(5 * time.Second); g.cache.Stats().AnimDecodes < 8 && time.Now().Before(end); {
		o.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if n := g.cache.Stats().AnimDecodes; n < 8 {
		t.Errorf("%d frames decoded after the first in 5 s of view", n)
	}
	// Scrolled away, nothing more.
	g.scroll = 10 * pitchPt
	for i := 0; i < 5; i++ {
		o.Frame()
	}
	st := g.cache.Stats()
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 20; i++ {
		o.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if after := g.cache.Stats(); after.AnimDecodes > st.AnimDecodes+1 || after.AnimUploads != st.AnimUploads {
		t.Errorf("scrolled away: %d more frames decoded, %d uploaded", after.AnimDecodes-st.AnimDecodes, after.AnimUploads-st.AnimUploads)
	}
}

// clocked gives the offscreen frames a clock (they have none of their own): the
// real one, so that the workers and the animations keep time together.
type clocked struct{ *grid }

func (x *clocked) Script(_ int, in *ui.Input) bool {
	in.Time = platform.Now()
	return true
}
