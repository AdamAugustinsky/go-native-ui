package rex

import (
	"errors"
	"math"
	"testing"

	"gonativeui/cmd/rexlook/rex/rexrig"
)

// Cost of one frame's Begin + Build + End (layout, hit test, animation and
// emission of the draw list, with the atlas sync), without the GPU encode.
// The window loop's budget is 1 ms of main-thread CPU per frame including the
// encode.

func benchRig(b *testing.B) (*rexrig.Rig, *Rex) {
	g, err := rexrig.New(2114, 1278, 2)
	if errors.Is(err, rexrig.ErrNoDevice) || errors.Is(err, rexrig.ErrNoText) {
		b.Skipf("no offscreen renderer: %v", err)
	}
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(g.Close)
	x := New(g.TS, 2)
	x.AttachRenderer(g.R)
	g.Settle(x, 600)
	return g, x
}

func BenchmarkFrameIdle(b *testing.B) {
	g, x := benchRig(b)
	g.Frames(x, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Frame(x)
	}
	b.StopTimer()
	b.ReportMetric(float64(len(g.List.Quads)), "quads")
	b.ReportMetric(float64(g.C.Boxes()), "boxes")
}

func BenchmarkFramePointerScript(b *testing.B) {
	g, x := benchRig(b)
	s := &script{x: x, in: &g.In, frame: func() { g.Frame(x) }}
	for i := 0; i < 1800; i++ {
		s.step()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.step()
	}
	b.StopTimer()
	b.ReportMetric(float64(len(g.List.Quads)), "quads")
}

func BenchmarkFrameLayoutScript(b *testing.B) {
	g, x := benchRig(b)
	s := &script{x: x, in: &g.In, frame: func() { g.Frame(x) }}
	for i := 0; i < 1920; i++ {
		s.structural()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.structural()
	}
}

// Live resize rebuilds the background mesh and the fill of the unfocused panes every frame.
func BenchmarkFrameLiveResize(b *testing.B) {
	g, x := benchRig(b)
	n := 0
	step := func() {
		n++
		g.W = 1200 + int(600*math.Sin(float64(n)/17))
		g.H = 800 + int(300*math.Sin(float64(n)/23))
		g.Frame(x)
	}
	for i := 0; i < 300; i++ {
		step()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		step()
	}
}
