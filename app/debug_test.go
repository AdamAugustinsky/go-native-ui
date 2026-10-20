package app

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"runtime"
	"strings"
	"testing"

	"gonativeui/text"
	"gonativeui/ui"
)

// scene is a Builder for the overlay's tests: a white page with a red "Save"
// button at (40, 60) of 120×40, and the switches the tests turn on.
type scene struct {
	a         *App
	font      text.FontKey
	fontScale float32

	anim       bool  // the button animates its hover value
	dup        bool  // two boxes with one key
	requests   int   // frames that still call RequestFrame
	wakeAtT    int64 // when set, every frame asks WakeIn for this platform time
	deadlineAt int64 // when set, Build declares this deadline
	deadlineK  ui.Key
	fired      bool
	target     ui.Key // the button's key
}

var buttonRect = [4]float32{40, 60, 160, 100}

func (b *scene) Init(a *App) error { b.a = a; return nil }

func (b *scene) Build(c *ui.Context) {
	if b.fontScale != c.Scale() {
		b.font = b.a.Text.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: c.Scale()})
		b.fontScale = c.Scale()
	}
	c.PushFont(b.font)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(white)
	c.Open(ui.DrawBackground, "page")
	c.NextPos(buttonRect[0], buttonRect[1])
	c.NextSize(ui.Px(buttonRect[2]-buttonRect[0]), ui.Px(buttonRect[3]-buttonRect[1]))
	c.NextBG(red)
	c.NextRole(ui.RoleButton)
	c.NextSemName("Save")
	fl := ui.FloatingX | ui.FloatingY | ui.DrawBackground | ui.Hoverable
	if b.anim {
		fl |= ui.HotAnimation
	}
	b.target = c.KeyOf(c.Box(fl, "target"))
	if b.dup {
		for i := 0; i < 2; i++ {
			c.NextPos(200, 60+float32(i)*50)
			c.NextSize(ui.Px(40), ui.Px(40))
			c.NextBG(green)
			c.Box(ui.FloatingX|ui.FloatingY|ui.DrawBackground, "same")
		}
	}
	if b.requests > 0 {
		b.requests--
		b.request(c)
	}
	if b.wakeAtT != 0 {
		if now := c.Input().Time; now < b.wakeAtT {
			c.WakeIn(b.wakeAtT - now)
		} else {
			b.wakeAtT = 0
		}
	}
	if b.deadlineAt != 0 && c.Deadline(b.deadlineK, b.deadlineAt) {
		b.fired = true
	}
	c.Close()
	c.PopFont()
}

// request is a function of its own so that the test can find its name among
// the symbolised callers.
func (b *scene) request(c *ui.Context) { c.RequestFrame() }

func newOverlayRig(t *testing.T, b Builder, wPx, hPx int, scale float32) *Offscreen {
	t.Helper()
	o, err := NewOffscreen(Config{Clear: [4]float32{1, 1, 1, 1}}, wPx, hPx, scale, b)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	t.Cleanup(o.Close)
	return o
}

// pointAt puts the pointer at (x, y) inside the window.
func pointAt(o *Offscreen, x, y float32) {
	o.tr.in.MouseX, o.tr.in.MouseY, o.tr.in.Inside = x, y, true
}

func frames(o *Offscreen, n int) *image.RGBA {
	var img *image.RGBA
	for i := 0; i < n; i++ {
		img, _ = o.Frame()
	}
	return img
}

func pix(img *image.RGBA, x, y int) uint32 { return rgba(img.Pix, img.Rect.Dx(), x, y) }

// diffIn counts the pixels of the rectangle that differ between two images.
func diffIn(a, b *image.RGBA, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if pix(a, x, y) != pix(b, x, y) {
				n++
			}
		}
	}
	return n
}

func clone(img *image.RGBA) *image.RGBA {
	c := image.NewRGBA(img.Rect)
	copy(c.Pix, img.Pix)
	return c
}

func dumpPNG(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	dir := os.Getenv("GNUI_DEBUG_TEST_PNG")
	if dir == "" {
		return
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	os.WriteFile(dir+"/"+name+".png", buf.Bytes(), 0o644)
}

// With the pointer over the button, the overlay outlines it in the inner
// colour, outlines the page it lies on, shows the card and draws the HUD; with
// the overlay off none of it is there.
func TestOverlayDrawsOutlinesAndHUD(t *testing.T) {
	const scale = 1
	off := newOverlayRig(t, &scene{}, 400, 300, scale)
	pointAt(off, 100, 80)
	base := clone(frames(off, 3))

	o := newOverlayRig(t, &scene{}, 400, 300, scale)
	o.EnableDebug(true)
	pointAt(o, 100, 80)
	img := frames(o, 4)
	dumpPNG(t, "overlay", img)

	// the button's outline: 2 px of the inner colour on the rectangle's edge
	for _, p := range [][2]int{{100, 60}, {100, 61}, {40, 80}, {159, 80}, {100, 99}} {
		if c := pix(img, p[0], p[1]); !near(c, 0xFF8A3DFF, 24) {
			t.Errorf("pixel %v is %08x, want the inner outline colour", p, c)
		}
	}
	// the page it lies on is outlined too, in the other colour, on the window's edge
	if c := pix(img, 200, 0); !near(c, 0x58A6FFB0, 0) && c == pix(base, 200, 0) {
		t.Errorf("the page has no outline at its top edge: %08x", c)
	}
	// the HUD is bottom left, the card next to the pointer, and the base has neither
	if n := diffIn(img, base, image.Rect(8, 190, 330, 292)); n < 500 {
		t.Errorf("the HUD changed only %d pixels in its corner", n)
	}
	if n := diffIn(img, base, image.Rect(116, 96, 392, 180)); n < 300 {
		t.Errorf("the card changed only %d pixels beside the pointer", n)
	}
	// the pointer outside the window: no outlines, no card, and the HUD stays
	pointAt(o, 100, 80)
	o.tr.in.Inside = false
	img2 := clone(frames(o, 3))
	if c := pix(img2, 100, 60); c != pix(base, 100, 60) {
		t.Errorf("the button is outlined with the pointer outside: %08x", c)
	}
	if n := diffIn(img2, base, image.Rect(8, 190, 330, 292)); n < 500 {
		t.Errorf("the HUD vanished with the pointer outside (%d pixels)", n)
	}
}

// The overlay is off unless it is asked for, and drawing it off changes no pixel.
func TestOverlayOffDrawsNothing(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 400, 300, 1)
	pointAt(o, 100, 80)
	a := clone(frames(o, 3))
	if o.DebugOn() || o.DebugFrames() != 0 {
		t.Fatal("the overlay is on without being asked")
	}
	o.EnableDebug(true)
	frames(o, 3)
	o.EnableDebug(false)
	b := frames(o, 3)
	if diffIn(a, b, a.Rect) != 0 {
		t.Error("turning the overlay off did not bring the frame back")
	}
}

func TestDebugEnvTurnsTheOverlayOn(t *testing.T) {
	t.Setenv("GNUI_DEBUG", "overlay")
	o := newOverlayRig(t, &scene{}, 100, 100, 1)
	if !o.DebugOn() {
		t.Error("GNUI_DEBUG=overlay did not turn the overlay on")
	}
	t.Setenv("GNUI_DEBUG", "something")
	o2 := newOverlayRig(t, &scene{}, 100, 100, 1)
	if o2.DebugOn() {
		t.Error("an unknown GNUI_DEBUG value turned the overlay on")
	}
}

// Two boxes built with one key in a frame are reported once, with the key
// and a count, and the overlay's own boxes are never among them.
func TestDuplicateKeysAreDetected(t *testing.T) {
	b := &scene{}
	o := newOverlayRig(t, b, 400, 300, 1)
	o.EnableDebug(true)
	pointAt(o, 100, 80)
	frames(o, 3)
	if d := o.UI.DebugDups(); len(d) != 0 {
		t.Fatalf("duplicates without any: %+v", d)
	}
	b.dup = true
	frames(o, 4)
	d := o.UI.DebugDups()
	if len(d) != 1 {
		t.Fatalf("%d duplicate keys, want 1: %+v", len(d), d)
	}
	if d[0].Count != 1 || d[0].Total < 4 || d[0].Key == 0 || d[0].Parent == 0 {
		t.Errorf("duplicate record %+v: want one collision a frame since it started", d[0])
	}
	if d[0].Frame != o.UI.DebugFrame() {
		t.Errorf("last seen in frame %d, now %d", d[0].Frame, o.UI.DebugFrame())
	}
	s := &o.dbg.snap
	if s.NDups != 1 || s.Dups[0].Key != d[0].Key {
		t.Errorf("the snapshot has %d duplicates", s.NDups)
	}
	b.dup = false
	frames(o, 2)
	if d := o.UI.DebugDups(); len(d) != 1 || d[0].Frame == o.UI.DebugFrame() {
		t.Errorf("a duplicate that stopped must stay listed but not current: %+v", d)
	}
}

// The recording is off when the overlay is: duplicates cost nothing and are
// not kept.
func TestDuplicateKeysAreNotRecordedWhenOff(t *testing.T) {
	o := newOverlayRig(t, &scene{dup: true}, 400, 300, 1)
	frames(o, 3)
	if d := o.UI.DebugDups(); len(d) != 0 {
		t.Errorf("%d duplicates recorded with the overlay off", len(d))
	}
}

// The layout audit's hook: findings are outlined in red, with the reason in
// the HUD and next to the box.
func TestAuditFindingsAreOutlinedInRed(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 400, 300, 1)
	o.EnableDebug(true)
	o.SetDebugAudit(func(dst []DebugFinding) []DebugFinding {
		return append(dst, DebugFinding{Key: 7, Rect: [4]float32{300, 20, 390, 90}, Reason: "overlap"})
	})
	base := clone(frames(o, 3))
	o.DebugRunAudit()
	img := frames(o, 3)
	dumpPNG(t, "audit", img)
	for _, p := range [][2]int{{345, 20}, {345, 21}, {300, 55}, {389, 55}, {345, 89}} {
		if c := pix(img, p[0], p[1]); !near(c, 0xFF2D2DFF, 24) {
			t.Errorf("pixel %v is %08x, want the finding's red", p, c)
		}
	}
	if o.dbg.snap.NFindings != 1 {
		t.Errorf("%d findings in the snapshot", o.dbg.snap.NFindings)
	}
	if n := diffIn(img, base, image.Rect(300, 92, 390, 112)); n < 100 {
		t.Errorf("the reason was not drawn above the box (%d pixels)", n)
	}
}

// A host replaces the panel: the snapshot goes to it every frame and the
// window draws no panel.
type countHost struct {
	n    int
	last InspectSnapshot
}

func (h *countHost) Inspect(s *InspectSnapshot) { h.n++; h.last = *s }

func TestInspectHostReceivesTheSnapshot(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 400, 300, 1)
	o.EnableDebug(true)
	pointAt(o, 100, 80)
	o.SetInspectPanel(true)
	withPanel := clone(frames(o, 3))
	h := &countHost{}
	o.SetInspectHost(h)
	img := frames(o, 3)
	if h.n != 3 {
		t.Errorf("the host got %d snapshots in 3 frames", h.n)
	}
	s := &h.last
	if s.NUnder < 2 || !s.Inside || s.W != 400 || s.Boxes < 2 {
		t.Errorf("snapshot %+v", struct {
			Under, W, Boxes int
			Inside          bool
		}{s.NUnder, s.W, s.Boxes, s.Inside})
	}
	in := s.Under[s.NUnder-1]
	if in.Role != ui.RoleButton || string(in.Name[:in.NameLen]) != "Save" || in.Rect != buttonRect || in.Flags&ui.Hoverable == 0 {
		t.Errorf("innermost box %+v", in)
	}
	if in.Clip != [4]float32{0, 0, 400, 300} {
		t.Errorf("clip %v, want the window", in.Clip)
	}
	// the panel is right-aligned; with a host it is not drawn
	if n := diffIn(withPanel, img, image.Rect(220, 0, 400, 100)); n < 300 {
		t.Errorf("the panel did not leave the window (%d pixels differ)", n)
	}
}

// A clipping ancestor shows in the clip of the box.
type clipped struct{ scene }

func (b *clipped) Build(c *ui.Context) {
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextPos(0, 0)
	c.Open(0, "root")
	c.NextPos(20, 20)
	c.NextSize(ui.Px(100), ui.Px(100))
	c.Open(ui.FloatingX|ui.FloatingY|ui.Clip, "clipper")
	c.NextPos(10, 10)
	c.NextSize(ui.Px(300), ui.Px(30))
	c.Box(ui.FloatingX|ui.FloatingY|ui.Hoverable, "wide")
	c.Close()
	c.Close()
}

func TestBoxClipIsTheAncestorsClip(t *testing.T) {
	o := newOverlayRig(t, &clipped{}, 400, 300, 1)
	o.EnableDebug(true)
	pointAt(o, 50, 45)
	frames(o, 3)
	s := &o.dbg.snap
	if s.NUnder == 0 {
		t.Fatal("no box under the pointer")
	}
	in := s.Under[s.NUnder-1]
	if in.Clip != [4]float32{20, 20, 120, 120} {
		t.Errorf("clip %v, want the clipper's rectangle", in.Clip)
	}
	if in.Rect != [4]float32{30, 30, 330, 60} {
		t.Errorf("rect %v", in.Rect)
	}
	// outside the clip the wide box is not under the pointer
	pointAt(o, 200, 45)
	frames(o, 2)
	for i := 0; i < s.NUnder; i++ {
		if s.Under[i].Rect[2] == 330 {
			t.Error("a clipped-away box is under the pointer")
		}
	}
}

// With the overlay on, a steady-state frame allocates nothing, with the
// pointer resting on boxes and with the inspector panel, a duplicate key and a
// finding on screen.
func TestOverlayFramesAllocateNothing(t *testing.T) {
	b := &scene{dup: true}
	o := newOverlayRig(t, b, 400, 300, 2)
	o.EnableDebug(true)
	o.SetInspectPanel(true)
	o.SetDebugAudit(func(dst []DebugFinding) []DebugFinding {
		return append(dst, DebugFinding{Key: 7, Rect: [4]float32{300, 20, 390, 90}, Reason: "alignment set with no text"})
	})
	o.DebugRunAudit()
	pointAt(o, 100, 80)
	frames(o, 400) // every character of the overlay's text is shaped and in the atlas by now
	allocs := testing.AllocsPerRun(200, func() { o.Frame() })
	if allocs != 0 {
		t.Errorf("%.2f allocations per frame with the overlay on, want 0", allocs)
	}
	if r, ok := o.DebugFrame(0); !ok || r.Allocs != 0 {
		t.Errorf("the HUD's own count of allocations is %d (ok %v), want 0", r.Allocs, ok)
	}
}

var metricSink []byte

func TestDebugMetricsAllocateNothing(t *testing.T) {
	d := &debugState{}
	d.mallocs()
	if n := testing.AllocsPerRun(100, func() { d.mallocs() }); n != 0 {
		t.Errorf("reading the allocation counter allocates %.1f times", n)
	}
	m0 := d.mallocs()
	for i := 0; i < 100; i++ {
		metricSink = make([]byte, 100+i)
	}
	if m := d.mallocs() - m0; m < 100 || m > 110 {
		t.Errorf("the counter saw %d allocations of 100", m)
	}
}

func TestSymbolsAreCachedAndNameTheCaller(t *testing.T) {
	var c symCache
	pc := callerPC()
	s := string(c.symbol(pc))
	if !strings.Contains(s, "debug_test.go:") || !strings.Contains(s, "TestSymbolsAreCachedAndNameTheCaller") {
		t.Errorf("symbol %q", s)
	}
	if n := testing.AllocsPerRun(100, func() { c.symbol(pc) }); n != 0 {
		t.Errorf("a cached symbol allocates %.1f times", n)
	}
}

//go:noinline
func callerPC() uintptr {
	var pcs [2]uintptr
	runtime.Callers(2, pcs[:])
	return pcs[0]
}
