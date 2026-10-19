package widgettest

import (
	"os"
	"runtime"
	"sync/atomic"
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
)

// Live windows. Offscreen frames take the platform out of the picture, which
// is how a bug that lives between the platform and the layout (the system
// drawing something over the content) escapes every offscreen test. RunLive
// opens the real window, lets the app draw, and reports what the system
// itself says about it.
//
// AppKit creates windows on the main thread only. A test package that wants
// real windows calls RunMain from its TestMain, which keeps the main thread
// for them and runs the tests on another goroutine:
//
//	func TestMain(m *testing.M) { widgettest.RunMain(m) }

func init() { runtime.LockOSThread() } // keeps the main goroutine on the main thread for RunMain

var (
	mainQ       = make(chan func())
	mainRunning atomic.Bool
)

// RunMain runs the tests of a package that opens real windows (RunLive) and
// exits with their result. Call it from TestMain; the main thread then serves
// OnMain while the tests run on another goroutine.
func RunMain(m *testing.M) {
	mainRunning.Store(true)
	done := make(chan int, 1)
	go func() { done <- m.Run() }()
	for {
		select {
		case f := <-mainQ:
			f()
		case code := <-done:
			os.Exit(code)
		}
	}
}

// OnMain runs f on the main thread and waits for it. f must not call t.Skip or
// t.Fatal. It needs RunMain in the package's TestMain.
func OnMain(f func()) {
	if !mainRunning.Load() {
		panic("widgettest: OnMain needs RunMain in TestMain")
	}
	d := make(chan struct{})
	mainQ <- func() { defer close(d); f() }
	<-d
}

// Live is what RunLive learned about a real window.
type Live struct {
	// Context is the window's ui context, holding the boxes of the last frame
	// drawn: ask it for the boxes under a rectangle (ui.Context.ContentIn).
	Context *ui.Context
	// Scale is the window's pixels per point.
	Scale float32
	// Frames is how many frames the app built.
	Frames int
	// Buttons is where the OS says it draws the window buttons over the content,
	// read from the system (macOS: the standardWindowButton: frames, asked of
	// AppKit), in points from the content's top left; empty where there are
	// none over the content or the OS has no such call here.
	Buttons [4]float32
	// PlatformButtons is what the platform package reported to the app as
	// the same rectangle (platform.Window.SystemButtons), which Buttons checks.
	PlatformButtons [4]float32
}

// ButtonsPx is Buttons in pixels, the unit of ui.Context.ContentIn.
func (l *Live) ButtonsPx() [4]float32 {
	return [4]float32{l.Buttons[0] * l.Scale, l.Buttons[1] * l.Scale, l.Buttons[2] * l.Scale, l.Buttons[3] * l.Scale}
}

// RunLive opens the real window for cfg with the builder in it, lets it draw
// frames frames, reads what the system says about the window (Live.Buttons),
// and closes it. The test is skipped where no window can be opened, but not in
// CI, where the runners have a display. It needs RunMain.
func RunLive(t testing.TB, cfg app.Config, b app.Builder, frames int) *Live {
	t.Helper()
	l := &Live{}
	lb := &liveBuilder{Builder: b, l: l, frames: frames}
	var err error
	OnMain(func() { _, err = app.RunWith(cfg, lb, app.RunOptions{}) })
	if err != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("no window to open: %v", err)
		}
		t.Fatalf("opening the window on CI: %v", err)
	}
	if lb.n < frames {
		t.Fatalf("the window ended after %d of %d frames", lb.n, frames)
	}
	return l
}

// liveBuilder builds the app's frames, asks for more until there are enough,
// then reads the window and quits.
type liveBuilder struct {
	app.Builder
	l      *Live
	a      *app.App
	frames int
	n      int
}

func (b *liveBuilder) Init(a *app.App) error {
	b.a = a
	b.l.Context = a.UI
	if in, ok := b.Builder.(app.Initer); ok {
		return in.Init(a)
	}
	return nil
}

func (b *liveBuilder) Build(c *ui.Context) {
	b.Builder.Build(c)
	if b.n++; b.n < b.frames {
		c.RequestFrame()
		return
	}
	_, _, b.l.Scale = b.a.Size()
	b.l.Frames = b.n
	b.l.Buttons = readSystemButtons(b.a)
	b.l.PlatformButtons = platformSystemButtons(b.a)
	b.a.Quit()
}

// platformSystemButtons is what the platform's window says, where it says.
func platformSystemButtons(a *app.App) [4]float32 {
	if s, ok := any(a.Win).(interface{ SystemButtons() [4]float32 }); ok && a.Win != nil {
		return s.SystemButtons()
	}
	return [4]float32{}
}

// Empty reports whether r (x0, y0, x1, y1) has no area.
func empty(r [4]float32) bool { return r[2] <= r[0] || r[3] <= r[1] }

// CheckClearOfSystemButtons fails the test unless the OS draws window buttons
// over the window's content and nothing the app built is under them: no box
// that draws text or an image, or has a semantic role (anything the user can
// see or operate), overlaps the rectangle the system says the buttons are in.
// It also checks that the platform reported that rectangle to the app as the
// system has it (within half a point), since the layout is built from the
// platform's report. The window drag band and the backgrounds may lie under
// the buttons: the system's buttons sit on the band.
func (l *Live) CheckClearOfSystemButtons(t testing.TB) {
	t.Helper()
	if empty(l.Buttons) {
		t.Fatalf("the system reports no window buttons over the content (%v): the window has no custom titlebar, or the test cannot see them", l.Buttons)
	}
	for i := range l.Buttons {
		if d := l.Buttons[i] - l.PlatformButtons[i]; d > 0.5 || d < -0.5 {
			t.Errorf("the system draws the window buttons at %v pt, the platform reported %v", l.Buttons, l.PlatformButtons)
			break
		}
	}
	r := l.ButtonsPx()
	for _, b := range l.Context.ContentIn(r, nil) {
		what := "draws text"
		switch {
		case b.Image:
			what = "draws an image"
		case !b.Text:
			what = "has a role"
		}
		t.Errorf("a box that %s (role %d, key %#x) at %v px is under the window buttons at %v px (%v pt, scale %v)",
			what, b.Role, uint64(b.Key), b.Rect, r, l.Buttons, l.Scale)
	}
}
