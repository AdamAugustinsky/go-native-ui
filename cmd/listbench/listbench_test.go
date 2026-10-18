package main

import (
	"os"
	"runtime"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/ui"
)

// The window is small so that offscreen frames (with their readback) are cheap.
const tw, th = 640, 400 // pixels, at 2×

func newOffscreen(t testing.TB, l *listApp) *app.Offscreen {
	t.Helper()
	o, err := app.NewOffscreen(config, tw, th, 2, l)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("offscreen rendering on CI: %v", err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	t.Cleanup(o.Close)
	return o
}

func TestRowText(t *testing.T) {
	// The strings come from the frame arena, so check them through a context.
	c := ui.New(nil)
	var in ui.Input
	c.Begin(&in, 100, 100, 1, 1.0/60)
	for _, tc := range []struct {
		row  int
		name string
	}{{0, "file_0000000"}, {1234, "file_0001234"}, {999999, "file_0999999"}} {
		got := string(rowName(c, tc.row))
		if len(got) != len("file_0000000.dat") || got[:12] != tc.name {
			t.Errorf("rowName(%d) = %q, want %q.ext", tc.row, got, tc.name)
		}
	}
	if d := string(rowDate(c, 7)); len(d) != len("2026-03-14 09:41") || d[4] != '-' || d[10] != ' ' || d[13] != ':' {
		t.Errorf("rowDate = %q", d)
	}
	if rowSize(c, 3) == nil {
		t.Error("empty size")
	}
}

// ListRange at the ends of the million rows: row 0 is built first, row
// 999999 last, and never more rows than the viewport plus overscan.
func TestListRangeAtTheEnds(t *testing.T) {
	l := newListApp(defRows)
	o := newOffscreen(t, l)

	run := func(n int) {
		for i := 0; i < n; i++ {
			o.Frame()
		}
	}
	run(3)
	if l.first != 0 {
		t.Fatalf("at the top the first built row is %d", l.first)
	}
	maxRows := int(l.listH/l.rowPx) + 2 + 2*overscan
	if n := l.last - l.first; n < 3 || n > maxRows {
		t.Fatalf("built %d rows at the top, viewport allows %d", n, maxRows)
	}

	l.jump = defRows - 1
	run(3)
	if l.last != defRows {
		t.Fatalf("at the bottom the last built row is %d, want %d", l.last, defRows)
	}
	if n := l.last - l.first; n < 3 || n > maxRows {
		t.Fatalf("built %d rows at the bottom, viewport allows %d", n, maxRows)
	}
	if want := float64(defRows)*float64(l.rowPx) - float64(l.listH); l.scroll != want {
		t.Errorf("scroll %.0f at the bottom, want the end of the content %.0f", l.scroll, want)
	}

	l.jump = 900000
	run(3)
	if l.first > 900000 || l.last <= 900000 || l.last > 900000+maxRows {
		t.Fatalf("jump to 900000 built rows [%d, %d)", l.first, l.last)
	}
}

// scripted makes the next frames take their input from f, whose frame
// argument counts from 0 at this call.
func scripted(l *listApp, f func(frame int, in *ui.Input)) {
	n := 0
	l.script = func(_ int, in *ui.Input) bool {
		in.Inside, in.Focused = true, true
		f(n, in)
		n++
		return true
	}
}

func TestClickSelectsAndKeysMoveAndScroll(t *testing.T) {
	l := newListApp(defRows)
	o := newOffscreen(t, l)
	for i := 0; i < 3; i++ {
		o.Frame()
	}
	rh := l.rowPx
	rowY := func(r int) float32 { return l.listY0 + (float32(r)+0.5)*rh }

	// Click row 4 (the pointer is placed one frame before the press).
	scripted(l, func(f int, in *ui.Input) {
		in.MouseX, in.MouseY = 200, rowY(4)
		switch f {
		case 2:
			in.SetButton(ui.ButtonLeft, true, 1)
		case 3:
			in.SetButton(ui.ButtonLeft, false, 0)
		}
	})
	for i := 0; i < 6; i++ {
		o.Frame()
	}
	if l.selected != 4 {
		t.Fatalf("selected row %d after clicking row 4", l.selected)
	}

	// Scrolling away keeps the selection (it is by row index).
	scripted(l, func(f int, in *ui.Input) { in.MouseX, in.MouseY = 200, rowY(4); in.AddScroll(0, -100*rh, true) })
	for i := 0; i < 3; i++ {
		o.Frame()
	}
	if l.selected != 4 || l.first <= 4 {
		t.Fatalf("selection %d, first built row %d: want 4 kept while scrolled away", l.selected, l.first)
	}

	// Down moves the selection by one and scrolls it into view.
	scripted(l, func(f int, in *ui.Input) {
		in.MouseX, in.MouseY = 200, rowY(4)
		if f == 0 {
			in.PushKey(ui.KeyEvent{Code: ui.KeyDown, Down: true})
		}
	})
	for i := 0; i < 90; i++ { // the scroll approaches its target over several frames
		o.Frame()
	}
	if l.selected != 5 {
		t.Fatalf("selected %d after Down, want 5", l.selected)
	}
	top := float64(5) * float64(rh)
	if l.scroll > top || l.scroll+float64(l.listH) < top+float64(rh) {
		t.Fatalf("row 5 [%.0f, %.0f) is outside the view [%.0f, %.0f)", top, top+float64(rh), l.scroll, l.scroll+float64(l.listH))
	}

	// End selects the last row and brings it into view; Home goes back.
	scripted(l, func(f int, in *ui.Input) {
		if f == 0 {
			in.PushKey(ui.KeyEvent{Code: ui.KeyEnd, Down: true})
		}
	})
	for i := 0; i < 3; i++ {
		o.Frame()
	}
	if l.selected != defRows-1 {
		t.Fatalf("End selected %d", l.selected)
	}
	scripted(l, func(f int, in *ui.Input) {
		if f == 0 {
			in.PushKey(ui.KeyEvent{Code: ui.KeyHome, Down: true})
		}
	})
	for i := 0; i < 3; i++ {
		o.Frame()
	}
	if l.selected != 0 || l.first != 0 {
		t.Fatalf("Home: selected %d, first built %d", l.selected, l.first)
	}
}

func TestThumbDragScrolls(t *testing.T) {
	l := newListApp(defRows)
	o := newOffscreen(t, l)
	for i := 0; i < 3; i++ {
		o.Frame()
	}
	if l.thumbY1 <= l.thumbY0 {
		t.Fatal("no thumb")
	}
	x := float32(tw) - 14 // on the thumb: the track is 28 px wide at 2×, the thumb 20 px inside it
	y0 := (l.thumbY0 + l.thumbY1) / 2
	scripted(l, func(f int, in *ui.Input) {
		in.MouseX, in.MouseY = x, y0
		switch {
		case f == 1:
			in.SetButton(ui.ButtonLeft, true, 1)
		case f >= 2 && f < 12:
			in.MouseY = y0 + float32(f-1)*10 // drag down 100 px over ten frames
		case f == 12:
			in.MouseY = y0 + 100
			in.SetButton(ui.ButtonLeft, false, 0)
		}
	})
	for i := 0; i < 16; i++ {
		o.Frame()
	}
	room := float64(l.trackH) - float64(l.thumbY1-l.thumbY0)
	span := float64(defRows)*float64(l.rowPx) - float64(l.listH)
	want := 100 * span / room
	if d := l.scroll - want; d > 2 || d < -2 {
		t.Fatalf("scroll %.0f after dragging the thumb 100 px, want %.0f", l.scroll, want)
	}
	if l.dragging {
		t.Fatal("still dragging after the release")
	}

	// A click on the track below the thumb pages down by a viewport.
	before := l.scroll
	below := l.thumbY1 + 30
	scripted(l, func(f int, in *ui.Input) {
		in.MouseX, in.MouseY = float32(tw)-2, below // in the track's margin, outside the thumb
		switch f {
		case 1:
			in.SetButton(ui.ButtonLeft, true, 1)
		case 2:
			in.SetButton(ui.ButtonLeft, false, 0)
		}
	})
	for i := 0; i < 90; i++ {
		o.Frame()
	}
	if d := l.scroll - before - float64(l.listH); d > 2 || d < -2 {
		t.Fatalf("a track click moved the scroll by %.0f, want a page of %.0f", l.scroll-before, l.listH)
	}
}

// scrolls through new rows every frame, and the frames after warm-up allocate
// nothing: the run cache is reserved, so even the evictions of a long scroll
// are free. rowsPerFrame is high enough that the window passes the run
// cache's limit of 65536 runs (three per row).
func zeroAllocScroll(t *testing.T, rowsPerFrame, warm, window int, wantEvictions bool) {
	l := newListApp(defRows)
	l.scrollRows = rowsPerFrame
	total := warm + window*5 + 10
	l.script = l.benchScript(warm, total, total) // one pass of the events, as before
	o := newOffscreen(t, l)
	frame := func() { o.Frame() }
	for i := 0; i < warm; i++ {
		frame()
	}
	const attempts = 5
	var mallocs, bytes uint64
	for a := 0; a < attempts; a++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		var m0, m1 runtime.MemStats
		gen0 := o.Text.RunGeneration()
		runtime.ReadMemStats(&m0)
		for i := 0; i < window; i++ {
			frame()
		}
		runtime.ReadMemStats(&m1)
		evictions := o.Text.RunGeneration() - gen0
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		t.Logf("attempt %d: %d frames, %d rows each, %d quads: %d mallocs, %d bytes", a+1, window, l.last-l.first, len(o.Quads()), mallocs, bytes)
		if mallocs == 0 && bytes == 0 {
			if wantEvictions && evictions == 0 {
				t.Fatal("the run cache did not evict inside the measured window: the scroll was too short to test eviction")
			}
			t.Logf("%d run cache evictions inside the window", evictions)
			return
		}
	}
	t.Errorf("scroll frames allocated in all %d windows (last: %d mallocs, %d bytes over %d frames)", attempts, mallocs, bytes, window)
}

// The bench script (scroll, hover, key, click and the jumps to row 900000 and
// back) over >= 600 warmed frames.
func TestScrollFramesAllocateNothing(t *testing.T) {
	zeroAllocScroll(t, 3, 480, 700, false)
}

// A scroll long enough to evict the run cache several times.
func TestLongScrollAllocatesNothingAcrossEvictions(t *testing.T) {
	if testing.Short() {
		t.Skip("long")
	}
	zeroAllocScroll(t, 14, 1000, 2600, true)
}
