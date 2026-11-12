package app

import (
	"math"

	"gonativeui/platform"
	"testing"
	"time"
)

func BenchmarkFrameClock(b *testing.B) {
	a := &App{p: newPacer(), req: &fakeReq{}}
	b.ReportAllocs()
	for b.Loop() {
		now := platform.Now()
		a.p.invalidate()
		a.p.frameEvent()
		if a.turn(now) {
			a.frameDT(platform.Now())
			a.p.drawn(false)
		}
	}
}

// The same O(1) jump handles seconds or hours; seconds avoid overflowing the
// nanosecond clock when a long benchmark runs millions of iterations.
func BenchmarkSimIdle(b *testing.B) {
	s, err := NewSim(Config{}, 100, 100, 1, new(simFlat), SimOptions{})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	if err = s.Advance(time.Second); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err = s.Advance(time.Second); err != nil {
			b.Fatal(err)
		}
	}
}

// A frame after one that left nothing animating steps by one display
// interval, however long the pause before it: an animation that starts in it
// takes no leap (D103). Frames of a running animation step by the time
// between them, and teach the interval.
func TestFrameDTAfterIdleIsOneInterval(t *testing.T) {
	a := &App{}
	const ms = int64(1e6)
	now := int64(1000 * ms)
	if dt := a.frameDT(now); dt != 1.0/60 {
		t.Fatalf("first frame: %v", dt)
	}
	a.owedWhy = 0
	for _, gap := range []int64{8, 50, 90, 3000} {
		now += gap * ms
		if dt := a.frameDT(now); dt != 1.0/60 {
			t.Errorf("after %d ms of idle: dt %v, want one interval", gap, dt)
		}
	}
	// A 120 Hz animation: the steps are its own, and the interval follows.
	a.owedWhy = owedAnimating
	for i := 0; i < 60; i++ {
		now += 8333333
		if dt := a.frameDT(now); math.Abs(float64(dt)-1.0/120) > 1e-4 {
			t.Fatalf("running frame %d: dt %v", i, dt)
		}
	}
	if math.Abs(float64(a.interval)-1.0/120) > 1e-4 {
		t.Errorf("learnt interval %v, want 1/120", a.interval)
	}
	a.owedWhy = 0
	now += 70 * ms
	if dt := a.frameDT(now); math.Abs(float64(dt)-1.0/120) > 1e-4 {
		t.Errorf("after idle at 120 Hz: dt %v, want 1/120", dt)
	}
	// A late frame of a running animation keeps its time, up to 100 ms.
	a.owedWhy = owedAnimating
	now += 40 * ms
	if dt := a.frameDT(now); math.Abs(float64(dt)-0.04) > 1e-4 {
		t.Errorf("late running frame: dt %v, want 0.04", dt)
	}
	if math.Abs(float64(a.interval)-1.0/120) > 1e-4 {
		t.Errorf("a late frame changed the interval: %v", a.interval)
	}
}
