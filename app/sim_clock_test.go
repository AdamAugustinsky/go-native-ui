package app

import (
	"gonativeui/platform"
	"testing"
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
