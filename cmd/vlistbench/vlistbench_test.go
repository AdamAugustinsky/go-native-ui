package main

import (
	"os"
	"runtime"
	"testing"
	"time"

	"gonativeui/app"
)

// The window is small so that offscreen frames (with their readback) are cheap.
const tw, th = 480, 720 // pixels, at 2×

func newOffscreen(t testing.TB, ch *chat) *app.Offscreen {
	t.Helper()
	o, err := app.NewOffscreen(config, tw, th, 2, ch)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("offscreen rendering on CI: %v", err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	t.Cleanup(o.Close)
	return o
}

// The scripted session on a million messages, offscreen: the edits above the
// view never move it, the bottom holds while pinned (from the first frame),
// the distant move is rebuilt in slices, history loads; and once warm a whole
// session allocates nothing (exact ReadMemStats deltas, D27: the first clean
// window of up to five passes).
func TestTimelineSession(t *testing.T) {
	if testing.Short() {
		t.Skip("a million messages")
	}
	ch := newChat(defItems)
	const warm, period = 120, measFrames
	ch.script = ch.benchScript(warm, period, warm+6*period)
	ch.warmRebuild()
	o := newOffscreen(t, ch)
	frame := func() { o.Frame() }
	for i := 0; i < warm+period; i++ { // the first session warms the caches
		frame()
	}
	if ch.jumps != 0 || ch.unpinned != 0 {
		t.Fatalf("first session: %d jumps, %d frames unpinned", ch.jumps, ch.unpinned)
	}
	var mallocs, bytes uint64
	for a := 0; a < 5; a++ {
		r0, h0, a0 := ch.rebuilds, ch.histLoads, ch.arrivals
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < period; i++ {
			frame()
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		t.Logf("session %d: %d mallocs, %d bytes; %d rebuilds, %d history loads, %d arrivals; %.1f items built per frame, list frame max %.0f µs",
			a+2, mallocs, bytes, ch.rebuilds-r0, ch.histLoads-h0, ch.arrivals-a0,
			float64(ch.built)/float64(max(ch.buildCalls, 1)), float64(ch.editMax.Microseconds()))
		if ch.jumps != 0 || ch.unpinned != 0 {
			t.Fatalf("%d edits above the view moved it, %d pinned frames did not end at the bottom", ch.jumps, ch.unpinned)
		}
		if ch.rebuilds == r0 || ch.histLoads == h0 || ch.arrivals == a0 {
			t.Fatal("the session did not rebuild, load history and receive messages")
		}
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Fatalf("every session allocated (last: %d mallocs, %d bytes)", mallocs, bytes)
}

// The first frame of a list stuck to the bottom, before any layout told it
// its height, already ends at the bottom.
func TestFirstFrameEndsAtTheBottom(t *testing.T) {
	ch := newChat(5000)
	o := newOffscreen(t, ch)
	o.Frame()
	o.Frame() // its check reads the first frame's layout
	if ch.unpinned != 0 || !ch.st.AtEnd() {
		t.Fatalf("the first frame did not end at the bottom (AtEnd %v)", ch.st.AtEnd())
	}
}
