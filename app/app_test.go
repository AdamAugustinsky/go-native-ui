package app

import (
	"image"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"gonativeui/app/paths"
	"gonativeui/text"
	"gonativeui/ui"
)

// skipOrFail skips when this machine cannot render offscreen, except on CI,
// where that is a failure: the CI runners (Metal, D3D11 WARP, Mesa llvmpipe)
// are meant to render.
func skipOrFail(t testing.TB, what string, err error) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf("%s on CI: %v", what, err)
	}
	t.Skipf("%s: %v", what, err)
}

const (
	white = 0xFFFFFFFF
	blue  = 0x2040C0FF
	red   = 0xC02020FF
	green = 0x20C040FF
)

// tiny is the smallest useful Builder: a background, a hoverable box that is
// red, or green while hovered, and a label that counts frames.
type tiny struct {
	a         *App
	font      text.FontKey
	fontScale float32
	n         int // label number; every frame shapes a new string when distinct
	distinct  bool
	script    func(frame int, in *ui.Input) bool
	inits     int
	box       ui.ID
	hovered   bool
}

func (b *tiny) Init(a *App) error { b.a, b.inits = a, b.inits+1; return nil }

func (b *tiny) Script(frame int, in *ui.Input) bool {
	if b.script == nil {
		return false
	}
	return b.script(frame, in)
}

func (b *tiny) Build(c *ui.Context) {
	if b.fontScale != c.Scale() {
		b.font = b.a.Text.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: c.Scale()})
		b.fontScale = c.Scale()
	}
	c.PushFont(b.font)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextPad(20)
	c.NextGap(10)
	c.NextBG(blue)
	c.Open(ui.DrawBackground, "bg")
	c.NextSize(ui.Px(100), ui.Px(50))
	if b.hovered {
		c.NextBG(green)
	} else {
		c.NextBG(red)
	}
	b.box = c.Box(ui.DrawBackground|ui.Hoverable, "box")
	b.hovered = c.Signal(b.box).Hovering
	b.n++
	n := b.n
	if !b.distinct {
		n %= 50
	}
	c.NextSize(ui.TextSize(0), ui.TextSize(0))
	c.Box(ui.DrawText, "x") // a stable box keeps the layout of the label's row simple
	c.NextSize(ui.TextSize(0), ui.TextSize(0))
	c.BoxKeyed(ui.DrawText, ui.KeyN(c.ParentKey(), 1), c.Fmt().Str("frame ").Int(n).Bytes())
	c.Close()
	c.PopFont()
}

func rgba(pix []byte, w, x, y int) uint32 {
	o := (y*w + x) * 4
	return uint32(pix[o])<<24 | uint32(pix[o+1])<<16 | uint32(pix[o+2])<<8 | uint32(pix[o+3])
}

func near(a, b uint32, tol int) bool {
	for s := 0; s < 32; s += 8 {
		d := int(a>>s&255) - int(b>>s&255)
		if d > tol || d < -tol {
			return false
		}
	}
	return true
}

func TestRenderOffscreenPixels(t *testing.T) {
	const w, h = 400, 300
	b := &tiny{}
	b.script = func(frame int, in *ui.Input) bool {
		in.Inside = true // the pointer sits on the box from frame 0; it is hot from frame 1 on
		in.MouseX, in.MouseY = 60, 40
		return true
	}
	img, err := RenderOffscreen(Config{Clear: [4]float32{0, 0, 0, 1}}, w, h, 1, b, 4)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	if b.inits != 1 {
		t.Fatalf("Init ran %d times", b.inits)
	}
	if got := img.Bounds().Size(); got.X != w || got.Y != h {
		t.Fatalf("image %v", got)
	}
	if got := rgba(img.Pix, w, 300, 250); !near(got, blue, 1) {
		t.Errorf("background %#08x, want %#08x", got, blue)
	}
	if got := rgba(img.Pix, w, 60, 40); !near(got, green, 1) {
		t.Errorf("hovered box %#08x, want %#08x (the Scripter moved the pointer onto it)", got, green)
	}
	if got := rgba(img.Pix, w, 130, 40); !near(got, blue, 1) {
		t.Errorf("right of the box %#08x, want background", got)
	}

	// Without the script the same builder paints the box red.
	b2 := &tiny{}
	o, err := NewOffscreen(Config{Clear: [4]float32{0, 0, 0, 1}}, w, h, 1, b2)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	var img2 *image.RGBA
	for i := 0; i < 4; i++ {
		img2, _ = o.Frame()
	}
	if got := rgba(img2.Pix, w, 60, 40); !near(got, red, 1) {
		t.Errorf("unhovered box %#08x, want %#08x", got, red)
	}
	// Text: light pixels over the blue background below the box, if the machine
	// has a font (CI images do; a bare container may not).
	if o.Text.Run(o.Text.Shape(b2.font, []byte("frame 1"))).Advance == 0 {
		t.Log("no usable system font: text pixels not checked")
		return
	}
	light := 0
	for y := 90; y < 140; y++ {
		for x := 20; x < 200; x++ {
			if p := rgba(img2.Pix, w, x, y); p>>24 > 0x80 && p>>16&255 > 0x80 {
				light++
			}
		}
	}
	if light < 40 {
		t.Errorf("only %d light text pixels under the box", light)
	}
}

func TestOffscreenScriptEndsTheRun(t *testing.T) {
	b := &tiny{}
	b.script = func(frame int, in *ui.Input) bool { return frame < 2 }
	o, err := NewOffscreen(Config{}, 100, 100, 1, b)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	for i, want := range []bool{true, true, false} {
		if _, more := o.Frame(); more != want {
			t.Fatalf("frame %d: more = %v, want %v", i, more, want)
		}
	}
	if b.n != 3 {
		t.Fatalf("%d builds, want 3 (the frame that ends the script is still built)", b.n)
	}
}

// The per-frame app path (Script, input translation state, Build through the
// interface, ui, atlas upload, Encode and readback) allocates nothing once
// warm, with new strings shaped every frame: the run cache is reserved and
// the glyph atlas fills up. Exact deltas over >= 600 frames, in a few windows
// because the runtime can allocate on its own (timers, background workers): a
// per-frame allocation shows in every window.
func TestOffscreenFramesAllocateNothing(t *testing.T) {
	b := &tiny{distinct: true}
	b.script = func(frame int, in *ui.Input) bool {
		in.Inside = true
		in.MouseX, in.MouseY = float32(10+frame*7%300), float32(10+frame*13%250)
		if frame%90 == 30 {
			in.SetButton(ui.ButtonLeft, true, 1)
		} else if frame%90 == 33 {
			in.SetButton(ui.ButtonLeft, false, 0)
		}
		if frame%3 == 0 {
			in.AddScroll(0, -5, true)
		}
		return true
	}
	o, err := NewOffscreen(Config{}, 400, 300, 2, b)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	o.Text.ReserveRuns(4096, 4096*12, 4096*12)
	o.UI.Reserve(256)
	for i := 0; i < 400; i++ { // warm: boxes, draw list, atlas, run cache
		o.Frame()
	}
	const frames, attempts = 700, 5
	var mallocs, bytes uint64
	for a := 0; a < attempts; a++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < frames; i++ {
			o.Frame()
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		t.Logf("attempt %d: %d frames, %d mallocs, %d bytes (%d quads)", a+1, frames, mallocs, bytes, len(o.Quads()))
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("frames allocated in all %d windows (last: %d mallocs, %d bytes over %d frames)", attempts, mallocs, bytes, frames)
}

// The measurement must see a planted allocation.
func TestOffscreenAllocMeterDetects(t *testing.T) {
	b := &tiny{}
	o, err := NewOffscreen(Config{}, 200, 100, 1, b)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	for i := 0; i < 20; i++ {
		o.Frame()
	}
	var sink *[16]byte
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	for i := 0; i < 100; i++ {
		o.Frame()
		sink = new([16]byte)
	}
	runtime.ReadMemStats(&m1)
	_ = sink
	if m1.Mallocs-m0.Mallocs < 100 {
		t.Fatalf("planted allocation read as %d mallocs", m1.Mallocs-m0.Mallocs)
	}
}

// A Scripter that says false is not asked again: the script is over for good.
// A test that plays several scripts keeps its Scripter saying true between them
// (widget/widgettest.Rig does).
func TestOffscreenStopsAskingAScripterThatSaidFalse(t *testing.T) {
	calls := 0
	b := &tiny{script: func(frame int, in *ui.Input) bool {
		calls++
		return frame < 2
	}}
	o, err := NewOffscreen(Config{}, 100, 100, 1, b)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
		return
	}
	defer o.Close()
	var more [6]bool
	for i := range more {
		_, more[i] = o.Frame()
	}
	if calls != 3 || more != [6]bool{true, true, false, true, true, true} {
		t.Errorf("the Scripter was asked %d times (want 3), more = %v: only the frame where it said false reports the end", calls, more)
	}
}

// SetKeepRunning decides at the next last-window close, as AppConfig.KeepRunning
// does at launch (macOS; elsewhere the app always ends with its last window).
func TestSetKeepRunningChangesWhatTheLastCloseDoes(t *testing.T) {
	if !manyWindows {
		t.Skip("KeepRunning applies where an app can have no window")
	}
	mk := func() *App {
		a := &App{appObject: &appObject{mainApp: true}}
		a.wins = []*App{a}
		return a
	}
	a := mk()
	a.SetKeepRunning(true)
	a.closeRequested()
	if a.done || !a.closing {
		t.Errorf("kept running: done %v, closing %v, want the window to close and the app to stay", a.done, a.closing)
	}
	a = mk()
	a.SetKeepRunning(true)
	a.SetKeepRunning(false)
	a.closeRequested()
	if !a.done {
		t.Error("after SetKeepRunning(false) the last window's close did not end the app")
	}
}

func TestDevelopmentID(t *testing.T) {
	for exe, want := range map[string]string{
		"/tmp/go-build123/b001/exe/notes": "dev.gonativeui.notes",
		"/x/Pilot Tool.exe":               "dev.gonativeui.Pilot-Tool",
		"/x/my app.v2":                    "dev.gonativeui.my-app",
		"/x/.hidden":                      "dev.gonativeui.app",
		"":                                "dev.gonativeui.app",
		"/x/" + strings.Repeat("n", 300):  "dev.gonativeui." + strings.Repeat("n", 100),
	} {
		got := developmentID(exe)
		if got != want {
			t.Errorf("developmentID(%q) = %q, want %q", exe, got, want)
		}
		if !paths.ValidID(got) {
			t.Errorf("developmentID(%q) = %q is not a valid directory name", exe, got)
		}
	}
}
