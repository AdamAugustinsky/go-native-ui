package app

import (
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
