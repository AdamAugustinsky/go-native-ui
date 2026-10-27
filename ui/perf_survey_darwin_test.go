//go:build darwin

package ui

import (
	"runtime"
	"testing"
)

func TestPerfSteadyRich(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := newFreshScene(t, true)
	for i := 0; i < 10; i++ {
		s.frame()
	}
	before := s.ts.ParaStats()
	for i := 0; i < 10; i++ {
		s.frame()
	}
	after := s.ts.ParaStats()
	if after.Misses != before.Misses {
		t.Fatal("unchanged frame relaid out text")
	}
	t.Logf("24 unchanged rich messages/frame: para hits %d misses %d over 10 frames; emitted quads %d", after.Hits-before.Hits, after.Misses-before.Misses, len(s.list.Quads))
}

func BenchmarkPerfSteadyRich(b *testing.B) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := newFreshScene(b, true)
	for i := 0; i < 100; i++ {
		s.frame()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.frame()
	}
}
