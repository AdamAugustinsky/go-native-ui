package ui

import (
	"runtime"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/text"
)

// scene is a non-trivial UI used by the allocation test and the benchmarks:
// a tab bar with hover/press animation, a 1M-row virtual list, a grid of
// cards and text cells, an animated progress bar and a box that appears and
// disappears. step() drives scripted input; nothing in it allocates once the
// shaper cache and the context are warm.
type scene struct {
	c      *Context
	in     Input
	list   gpu.DrawList
	frame  int
	tab    int
	rows   int
	listID ID
	w, h   int
	cards  int
	scroll float32
	script bool // move the pointer, click and scroll on a script
	f0, f1 text.FontKey
}

var tabNames = [...]string{"Files", "Search", "Git", "Run", "Debug", "Extensions"}

const (
	colBG     = 0x1E1F22FF
	colPanel  = 0x2B2D30FF
	colText   = 0xDFE1E5FF
	colAccent = 0x3574F0FF
)

func newScene(sh Shaper, w, h int) *scene {
	s := &scene{c: New(sh), f0: 0, f1: 1, rows: 1_000_000, w: w, h: h, cards: 40, scroll: -24}
	s.in.Inside, s.in.Focused = true, true
	s.c.Reserve(2048)
	s.list.Quads = make([]gpu.Quad, 0, 1<<16)
	return s
}

func (s *scene) step() {
	f := s.frame
	s.frame++
	in := &s.in
	if s.script {
		in.MouseX = float32(20 + (f*7)%(s.w-40))
		in.MouseY = float32(10 + (f*13)%(s.h-20))
		switch f % 90 {
		case 29: // aim at a tab
			in.MouseX, in.MouseY = float32(40+(f/90%3)*70), 20
		case 30:
			in.MouseX, in.MouseY = float32(40+(f/90%3)*70), 20
			in.SetButton(ButtonLeft, true, 1)
		case 33:
			in.MouseX, in.MouseY = float32(40+(f/90%3)*70), 20
			in.SetButton(ButtonLeft, false, 0)
		}
		if f%3 == 0 {
			in.AddScroll(0, s.scroll, true)
		}
		if f%50 == 0 {
			in.AddScroll(0, -1, false)
		}
	}
	s.build()
	in.ClearFrame()
}

func (s *scene) build() {
	c := s.c
	c.Begin(&s.in, s.w, s.h, 2, 1.0/60)
	f := s.frame
	c.PushFont(s.f0)
	c.PushTextColor(colText)
	c.PushAxis(AxisY)

	// Tab bar.
	c.NextSize(Pct(1), Px(40))
	c.NextBG(colPanel)
	c.NextAxis(AxisX)
	c.NextPadding(8, 6, 8, 6)
	c.NextGap(6)
	bar := c.Open(DrawBackground, "bar")
	for i, name := range tabNames {
		c.NextSize(TextSize(24), Pct(1))
		c.NextBG(colBG)
		c.NextRadius(6)
		c.NextAlign(AlignCenter)
		if i == s.tab {
			c.NextBG(colAccent)
		}
		id := c.Box(DrawBackground|DrawText|Clickable|HotAnimation|ActiveAnimation|DrawHotEffects, name)
		if c.Signal(id).Clicked {
			s.tab = i
		}
	}
	c.Close()
	_ = bar

	// Body.
	c.NextSize(Pct(1), Pct(1))
	c.NextAxis(AxisX)
	c.NextBG(colBG)
	body := c.Open(DrawBackground, "body")

	// Sidebar: virtual list.
	c.NextSize(Px(260), Pct(1))
	c.NextBG(colPanel)
	c.NextAxis(AxisY)
	c.NextShadow(Shadow{OffX: 2, Sigma: 4, Color: 0x00000080})
	c.NextBorder(0x00000040, 1)
	list := c.Box(DrawBackground|DrawShadow|DrawBorder|ScrollY|Clip|Hoverable, "list")
	s.listID = list
	first, last := c.ListRange(list, s.rows, 26, 3)
	c.Push(list)
	for r := first; r < last; r++ {
		c.NextSize(Pct(1), Px(26))
		c.NextPad(4)
		if r%2 == 0 {
			c.NextBG(0xFFFFFF08)
		}
		c.NextAlign(AlignLeft)
		id := c.BoxKeyed(DrawBackground|DrawText|Clickable|HotAnimation|DrawHotEffects|FadeOverflowX,
			KeyN(c.ParentKey(), uint64(r)), c.Fmt().Str("Row ").Int(r).Str(" - item").Bytes())
		_ = id
	}
	c.Pop()

	// Main: header, progress bar, cards grid, text table.
	c.NextSize(Pct(1), Pct(1))
	c.NextPad(12)
	c.NextGap(10)
	main := c.Open(Clip, "main")

	c.NextSize(Pct(1), TextSize(10))
	c.NextFont(s.f1)
	c.Label(c.Fmt().Str("Frame ").Int(f % 100).Str(" / tab ").Int(s.tab).String())

	// Animated progress: the target flips every 45 frames.
	target := float32(0.1)
	if (f/45)%2 == 0 {
		target = 0.9
	}
	p := c.AnimateValue(HashString(c.ParentKey(), "progress"), target, 12)
	c.NextSize(Pct(1), Px(8))
	c.NextBG(0xFFFFFF18)
	c.NextRadius(4)
	c.NextAxis(AxisX)
	c.Open(DrawBackground, "track")
	c.NextSize(Pct(p), Pct(1))
	c.NextBG(colAccent)
	c.NextRadius(4)
	c.Box(DrawBackground, "fill")
	c.Close()

	// A box that appears and disappears.
	if (f/20)%2 == 1 {
		c.NextSize(TextSize(16), TextSize(8))
		c.NextBG(0x57965CFF)
		c.NextRadius(4)
		c.NextPos(float32(s.w)-300, 80)
		c.Push(c.Root())
		c.Box(DrawBackground|DrawText|FloatingX|FloatingY, "Saved##toast")
		c.Pop()
	}

	// Cards.
	c.NextSize(Pct(1), Children(1))
	c.NextAxis(AxisX)
	c.NextGap(10)
	c.Open(0, "cards")
	for i := 0; i < s.cards; i++ {
		c.NextSize(Px(120), Children(1))
		c.NextPad(8)
		c.NextGap(4)
		c.NextBG(colPanel)
		c.NextRadius(8)
		c.NextBorder(0xFFFFFF20, 1)
		c.NextShadow(Shadow{OffY: 2, Sigma: 3, Color: 0x00000060})
		card := c.BoxKeyed(DrawBackground|DrawBorder|DrawShadow|Clickable|HotAnimation|Focusable|FocusAnimation,
			KeyN(c.ParentKey(), uint64(i)), nil)
		c.Push(card)
		c.NextSize(Pct(1), TextSize(0))
		c.Box(DrawText|FadeOverflowX, "A fairly long card title##t")
		c.NextSize(Pct(1), TextSize(0))
		c.NextTextColor(0x9DA0A8FF)
		c.Label(c.Fmt().Str("Card ").Int(i).String())
		c.NextSize(Pct(1), Px(18))
		c.NextAxis(AxisX)
		c.NextGap(4)
		c.Open(0, "btns")
		for j := 0; j < 3; j++ {
			c.NextSize(Px(30), Pct(1))
			c.NextBG(colBG)
			c.NextRadius(3)
			c.NextAlign(AlignCenter)
			c.BoxKeyed(DrawBackground|DrawText|Clickable|HotAnimation|DrawHotEffects, KeyN(c.ParentKey(), uint64(j)), c.Fmt().Int(j).Bytes())
		}
		c.Close()
		c.Pop()
	}
	c.Close()

	// Text table.
	c.NextSize(Pct(1), Pct(1))
	c.NextGap(2)
	c.Open(0, "table")
	for r := 0; r < 20; r++ {
		c.NextSize(Pct(1), Px(22))
		c.NextAxis(AxisX)
		c.Open(0, "")
		for col := 0; col < 8; col++ {
			c.NextSize(Pct(0.125), Pct(1))
			c.BoxKeyed(DrawText|FadeOverflowX, KeyN(c.ParentKey(), uint64(col)), c.Fmt().Int(r*8+col).Str(".txt").Bytes())
		}
		c.Close()
	}
	c.Close()

	c.Close() // main
	_ = main
	c.Close() // body
	_ = body
	c.PopAxis()
	c.PopTextColor()
	c.PopFont()
	c.End(&s.list)
}

func TestSceneSanity(t *testing.T) {
	s := newScene(newFake(), 1280, 800)
	s.script = true
	for i := 0; i < 200; i++ {
		s.step()
	}
	if n := s.c.Boxes(); n < 500 {
		t.Errorf("scene has only %d boxes", n)
	}
	t.Logf("%d boxes, %d quads", s.c.Boxes(), len(s.list.Quads))
	if len(s.list.Quads) < 1000 {
		t.Errorf("%d quads", len(s.list.Quads))
	}
	if s.tab == 0 {
		t.Log("note: scripted click never switched the tab")
	}
}

// measure runs frames of the scene and returns the exact allocation deltas.
func measure(step func(), frames int) (mallocs, bytes uint64) {
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	for i := 0; i < frames; i++ {
		step()
	}
	runtime.ReadMemStats(&m1)
	return m1.Mallocs - m0.Mallocs, m1.TotalAlloc - m0.TotalAlloc
}

// A frame (build, layout, hit test, animation, emission) allocates nothing
// once warm. The measurement runs in a few windows because the runtime can
// allocate on its own (timers, background workers): a per-frame allocation
// shows in every window, so one clean window proves the path is clean.
func TestZeroAllocFrames(t *testing.T) {
	s := newScene(newFake(), 1280, 800)
	s.script = true
	s.c.AtlasSync(&fakeUploader{})
	for i := 0; i < 900; i++ { // covers every state of the script and every label
		s.step()
	}
	runtime.GC()
	const frames, attempts = 900, 5
	var mallocs, bytes uint64
	for a := 0; a < attempts; a++ {
		mallocs, bytes = measure(s.step, frames)
		t.Logf("attempt %d: %d frames of %d boxes / %d quads: %d mallocs, %d bytes", a+1, frames, s.c.Boxes(), len(s.list.Quads), mallocs, bytes)
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("frames allocated in all %d windows (last: %d mallocs, %d bytes over %d frames)", attempts, mallocs, bytes, frames)
}

// Scrolling a 1M-row list one row per frame (the plan's benchmark).
func BenchmarkListScroll1M(b *testing.B) {
	benchScroll(b, newFake())
}

func benchScroll(b *testing.B, sh Shaper) {
	s := newScene(sh, 1280, 800)
	s.script = false
	s.scroll = -26 // one row per frame
	s.c.AtlasSync(&fakeUploader{})
	// Pointer over the list so the wheel reaches it.
	s.in.MouseX, s.in.MouseY = 100, 300
	scrollStep := func() {
		s.in.AddScroll(0, s.scroll, true)
		s.step()
	}
	for i := 0; i < 700; i++ {
		scrollStep()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scrollStep()
	}
	b.StopTimer()
	b.ReportMetric(float64(len(s.list.Quads)), "quads")
	b.ReportMetric(float64(s.c.Boxes()), "boxes")
}

// The measurement itself must see a planted allocation.
func TestAllocMeterDetects(t *testing.T) {
	s := newScene(newFake(), 1280, 800)
	for i := 0; i < 50; i++ {
		s.step()
	}
	var sink *[16]byte
	m, b := measure(func() { s.step(); sink = new([16]byte) }, 600)
	_ = sink
	if m < 600 || b < 600*16 {
		t.Fatalf("planted allocation read as %d mallocs, %d bytes", m, b)
	}
}
