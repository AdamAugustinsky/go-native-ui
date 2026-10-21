package main

import (
	"runtime"
	"testing"

	"gonativeui/app"
	"gonativeui/widget/widgettest"
)

// A steady frame of a window of the demo allocates nothing: the labels with
// numbers are built with Context.Fmt, the window list is reused, and the
// options of the select are a variable. (The windows' commands, which run when
// a button is pressed, are not frames.)
func TestSteadyFramesAllocateNothing(t *testing.T) {
	d := &demo{n: 1}
	w := &window{d: d, n: 1, results: make(chan string, 8)}
	cfg := app.WindowConfig{Title: "Window 1", Width: 560, Height: 380, MinWidth: 420, MinHeight: 300}
	o := widgettest.NewOffscreen(t, cfg, int(cfg.Width), int(cfg.Height), 1, w)
	for range 300 {
		o.Frame()
	}
	runtime.GC()
	for range 300 {
		o.Frame()
	}
	var best uint64
	for i := 0; i < 12; i++ {
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for range 200 {
			o.Frame()
		}
		runtime.ReadMemStats(&m1)
		if m1.Mallocs == m0.Mallocs && m1.TotalAlloc == m0.TotalAlloc {
			return
		}
		if i == 0 || m1.Mallocs-m0.Mallocs < best {
			best = m1.Mallocs - m0.Mallocs
		}
	}
	t.Errorf("every one of 12 runs of 200 frames allocated (fewest: %d mallocs)", best)
}
