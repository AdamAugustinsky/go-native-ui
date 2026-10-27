package rexui_test

// THROWAWAY perf-survey benchmark (never commit).

import (
	"fmt"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/rexui/catalog"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

func BenchmarkCatalogFrame(b *testing.B) {
	for p := 0; p < len(catalog.Pages); p++ {
		b.Run(fmt.Sprintf("page%d", p), func(b *testing.B) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			ts := fakeui.New()
			c := ui.New(ts)
			c.Reserve(8192)
			c.AtlasSync(&fakeui.Uploader{})
			k := base.NewKit(c, ts, rex.Theme())
			k.ReduceMotion = true
			cat := catalog.NewState(k)
			var in ui.Input
			in.Inside, in.Focused = true, true
			in.MouseX, in.MouseY = -100, -100
			var list gpu.DrawList
			list.Quads = make([]gpu.Quad, 0, 1<<14)
			cells, gw, gh := catalog.Grid(catalog.Page(p))
			W, H := int(2*gw), int(2*gh)
			var t int64
			frame := func() {
				t += 16_666_667
				in.Time = t
				c.Begin(&in, W, H, 2, 1.0/60)
				k.Begin(c)
				for _, cell := range cells {
					theme.At(c, 2*(cell.X+catalog.Margin), 2*(cell.Y+catalog.Margin), 2*catalog.Samples[cell.Sample].W, 2*catalog.Samples[cell.Sample].H)
					c.Push(c.BoxKeyed(theme.Floating, ui.KeyN(c.ParentKey(), uint64(cell.Sample)), nil))
					k.NextGap(0)
					k.Column("col")
					cat.Build(k, cell.Sample)
					k.EndColumn()
					c.Pop()
				}
				k.End()
				c.End(&list)
				in.ClearFrame()
			}
			for i := 0; i < 300; i++ {
				frame()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				frame()
			}
			b.StopTimer()
			b.ReportMetric(float64(c.Boxes()), "boxes")
			b.ReportMetric(float64(len(list.Quads)), "quads")
			b.ReportMetric(float64(len(cells)), "samples")
		})
	}
}
