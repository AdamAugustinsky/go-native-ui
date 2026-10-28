//go:build darwin

package live

// More than one window (phase 6, F4, DECISIONS D30), driven through real
// windows: the LiveApp's window is the first, and a test opens more with
// app.OpenWindow. Each window has its own input, frame pacing and wakes; the
// app's renderer, text system and glyph atlas are shared; window changes
// asked for inside a frame wait for it; and a window opened and closed again
// and again leaves nothing behind.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/gpu"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// mwPane is a window of these tests: a box that fills it. It counts its
// frames and the input that reached it, takes a worker's results, may
// animate, and may run a test's function inside its Build.
type mwPane struct {
	builds  atomic.Int64
	presses atomic.Int64
	keys    atomic.Int64
	got     atomic.Int64 // worker results taken (results)
	animate atomic.Bool
	inBuild atomic.Bool
	wakeIn  atomic.Int64 // ns: the next Build asks for a frame that much later (ui.Context.WakeIn)
	results chan int
	init    func(a *app.App) error
	during  atomic.Pointer[func(c *ui.Context)] // run once, inside the next Build
}

func (p *mwPane) Init(a *app.App) error {
	if p.init != nil {
		return p.init(a)
	}
	return nil
}

// buildDepth is how many Builds of any window are running: more than one
// would be a Build entered from inside another (a frame handler drawing
// under a frame), which the app never does.
var buildDepth, nestedBuilds atomic.Int32

func (p *mwPane) Build(c *ui.Context) {
	if buildDepth.Add(1) > 1 {
		nestedBuilds.Add(1)
	}
	defer buildDepth.Add(-1)
	p.inBuild.Store(true)
	defer p.inBuild.Store(false)
	p.builds.Add(1)
	in := c.Input()
	for _, down := range in.Pressed {
		if down {
			p.presses.Add(1)
		}
	}
	p.keys.Add(int64(in.NKeys))
	for drained := p.results == nil; !drained; {
		select {
		case <-p.results:
			p.got.Add(1)
		default:
			drained = true
		}
	}
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Box(0, "bg")
	if f := p.during.Swap(nil); f != nil {
		(*f)(c)
	}
	if p.animate.Load() {
		c.RequestFrame()
	}
	if ns := p.wakeIn.Swap(0); ns > 0 {
		c.WakeIn(ns)
	}
}

func (p *mwPane) runInBuild(f func(c *ui.Context)) { p.during.Store(&f) }

func mwConfig(title string) app.Config { return app.Config{Title: title, Width: 360, Height: 240} }

// mwOpen opens a window of the LiveApp's app at x (points from the left of
// the screen, beside the first window, so that neither covers the other) and
// waits for its first frame; built reads the window's frame count.
func mwOpen(t *testing.T, l *widgettest.LiveApp, title string, x float64, b app.Builder, built func() int64) *app.Win {
	t.Helper()
	var w *app.Win
	var err error
	l.Do(func(a *app.App) {
		if w, err = a.OpenWindow(mwConfig(title), b); err == nil {
			w.SetFrame(x, 60, 360, 268)
		}
	})
	if err != nil {
		t.Fatalf("OpenWindow: %v", err)
	}
	l.Until(title+" drew its first frame", 240, func() bool { return built() > 0 })
	return w
}

// mwStates is each window's frame state (app.StateLine), for a failure's
// message: why a window drew nothing.
func mwStates(l *widgettest.LiveApp, wins ...*app.Win) string {
	var b []byte
	l.Do(func(*app.App) {
		now := platform.Now()
		for i, w := range wins {
			b = fmt.Appendf(b, "\n  window %d: ", i+1)
			if w.Open() {
				b = w.App().StateLine(b, now)
			} else {
				b = append(b, "closed"...)
			}
		}
	})
	return string(b)
}

// coveredSince reports whether w's window was hidden at some time since
// start (platform.Now): it is not visible now, or its visibility changed
// since. A covered window gets no frames (the display link does not tick for
// it), and the windows of other test processes, run in parallel by go test,
// open over these tests' ones. The evidence is the window server's, not the
// frame counts a test measures.
func coveredSince(l *widgettest.LiveApp, w *app.Win, start int64) (covered bool) {
	l.Do(func(*app.App) {
		if w.Open() {
			st := w.App().Win.DebugState()
			covered = !st.Visible || st.LastVisibleT >= start
		}
	})
	return covered
}

// settle lets the frames that a Do or an opening owes be drawn: the harness
// wakes every window after each call.
func settle() { time.Sleep(400 * time.Millisecond) }

// mainThreadSample is the main thread's native stack, sampled for a second
// (/usr/bin/sample on this process).
func mainThreadSample() string {
	out, err := exec.Command("/usr/bin/sample", strconv.Itoa(os.Getpid()), "1", "-mayDie").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("sample: %v\n%s", err, out)
	}
	lines := strings.Split(string(out), "\n")
	var b strings.Builder
	in := false
	n := 0
	for _, ln := range lines {
		if strings.Contains(ln, "Thread_") {
			if in {
				break
			}
			in = strings.Contains(ln, "main-thread")
		}
		if in && n < 220 {
			b.WriteString(ln)
			b.WriteByte('\n')
			n++
		}
	}
	if b.Len() == 0 {
		return string(out[:min(len(out), 4000)])
	}
	return b.String()
}

// stacks is every goroutine's stack (the main thread's tells where the app's
// loop is).
func stacks() []byte {
	buf := make([]byte, 1<<20)
	return buf[:runtime.Stack(buf, true)]
}

// waitFor polls cond from the test goroutine, without asking any window for
// a frame (Until would ask the first window for one), for at most d.
func waitFor(d time.Duration, cond func() bool) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// A click and a key posted to one window reach that window's ui only.
func TestEachWindowGetsItsOwnInput(t *testing.T) {
	first, second := &mwPane{}, &mwPane{}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	w2 := mwOpen(t, l, "second", 20, second, second.builds.Load)

	l.Do(func(*app.App) { widgettest.NativeWindow(w2.App()).Click(40, 40, 0) })
	if !waitFor(5*time.Second, func() bool { return second.presses.Load() == 1 }) {
		t.Fatalf("the second window's click: %d presses there", second.presses.Load())
	}
	l.Do(func(a *app.App) { widgettest.NativeWindow(a).Click(40, 40, 0) })
	l.Until("the first window saw its click", 120, func() bool { return first.presses.Load() == 1 })
	settle()
	if f, s := first.presses.Load(), second.presses.Load(); f != 1 || s != 1 {
		t.Errorf("presses: first %d, second %d; want one each", f, s)
	}

	// Keys: the second window has the keyboard focus.
	var restore func()
	l.Do(func(*app.App) {
		restore = platform.DebugAssumeKey(w2.App().Win)
		w2.Focus()
	})
	defer l.Do(func(*app.App) { restore() })
	k0 := first.keys.Load()
	l.Do(func(*app.App) { widgettest.NativeWindow(w2.App()).Press(ui.KeyCode('K'), 0) })
	if !waitFor(5*time.Second, func() bool { return second.keys.Load() > 0 }) {
		t.Fatalf("the second window got no key (first window: %d)", first.keys.Load()-k0)
	}
	settle()
	if n := first.keys.Load() - k0; n != 0 {
		t.Errorf("the first window got %d of the second window's keys", n)
	}
}

// quietTries is how often a measurement that wants a window to draw nothing
// is made before a frame there fails it: other test processes, run in
// parallel by go test, take the focus and cover windows, and a window shows
// those changes. A window that draws when it does not owe a frame does so
// every time.
const quietTries = 3

// Each window draws only when it owes a frame: one animates while the other
// draws nothing, and the other way round.
func TestWindowsPaceOnTheirOwn(t *testing.T) {
	first, second := &mwPane{}, &mwPane{}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	w2 := mwOpen(t, l, "second", 20, second, second.builds.Load)
	var w1 *app.Win
	l.Do(func(a *app.App) { w1 = a.Window() })

	measure := func(name string, anim, idle *mwPane, win *app.Win) {
		t.Helper()
		var other int64
		for try := 1; try <= quietTries; try++ {
			start := platform.Now()
			anim.animate.Store(true)
			l.Do(func(*app.App) { win.Wake() })
			settle()
			a0, i0 := anim.builds.Load(), idle.builds.Load()
			time.Sleep(time.Second)
			a1, i1 := anim.builds.Load(), idle.builds.Load()
			anim.animate.Store(false)
			settle()
			t.Logf("%s animating: %d frames in 1 s; the other window: %d", name, a1-a0, i1-i0)
			if a1-a0 < 10 {
				if coveredSince(l, win, start) { // a covered window does not draw
					if try == quietTries {
						t.Skipf("%s stays covered by other windows: it cannot animate", name)
					}
					continue
				}
				t.Errorf("%s animated %d frames in 1 s%s", name, a1-a0, mwStates(l, w1, w2))
				return
			}
			if other = i1 - i0; other == 0 {
				return
			}
		}
		t.Errorf("while %s animated, the other window drew %d frames, want 0 (%d tries)%s", name, other, quietTries, mwStates(l, w1, w2))
	}
	measure("the second window", second, first, w2)
	measure("the first window", first, second, w1)
}

// A display link that stops calling back (a window's CAMetalDisplayLink did,
// on CI's virtual display, after a pause and a resume) is replaced once the
// loop's unpaced fallback finds no drawable: the window draws again, through
// the layer's nextDrawable first, then paced by a new link.
func TestAStoppedDisplayLinkIsReplaced(t *testing.T) {
	first, second := &mwPane{}, &mwPane{}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	w2 := mwOpen(t, l, "second", 20, second, second.builds.Load)
	var w1 *app.Win
	l.Do(func(a *app.App) { w1 = a.Window() })
	settle()

	var stopped bool
	var st0 platform.DebugState
	l.Do(func(*app.App) { st0 = w2.App().Win.DebugState(); stopped = w2.App().Win.DebugStopLink() })
	if !stopped {
		t.Fatalf("the second window has no CAMetalDisplayLink to stop: %s", st0.Append(nil))
	}
	start, b0 := platform.Now(), second.builds.Load()
	second.animate.Store(true)
	defer second.animate.Store(false)
	l.Do(func(*app.App) { w2.Wake() })
	if !waitFor(5*time.Second, func() bool { return second.builds.Load() >= b0+30 }) {
		if coveredSince(l, w2, start) {
			t.Skip("the second window was covered by other windows: it gets no frames")
		}
		t.Fatalf("the second window drew %d frames in 5 s after its link stopped%s", second.builds.Load()-b0, mwStates(l, w1, w2))
	}
	var st platform.DebugState
	ticking := func() bool {
		l.Do(func(*app.App) { st = w2.App().Win.DebugState() })
		return st.MetalLink && st.Ticks > st0.Ticks
	}
	if !waitFor(3*time.Second, ticking) || st.Restarts != 1 {
		t.Errorf("after the stop: %d restarts, metal link %v, ticks %d (were %d); want one restart and a new metal link that ticks", st.Restarts, st.MetalLink, st.Ticks, st0.Ticks)
	}
}

// A composer in each window: each window's hosted NSTextView (the native
// editor, N4) takes what is typed into that window, and its signals reach
// that window's frames; the second window's editor, released, and the window,
// closed, leave no host behind.
func TestEachWindowHasItsOwnComposer(t *testing.T) {
	p1, p2 := &edApp{}, &edApp{}
	l := widgettest.StartLive(t, app.Config{Title: "composer 1", Width: 480, Height: 320}, p1)
	t.Cleanup(func() { l.Do(func(*app.App) { p1.ed.Release() }) })
	var hosts0 int64
	l.Do(func(*app.App) { hosts0 = platform.ShellObjects().Hosts })
	w2 := mwOpen(t, l, "composer 2", 20, p2, p2.builds.Load)
	var w1 *app.Win
	l.Do(func(a *app.App) { w1 = a.Window() })

	// typeInto types into w's editor: as keys when the window can be made
	// key (the pump hands them to the window's text view), else through the
	// text view's input client (an app that cannot activate here, as a
	// session in use by someone else).
	keys := true
	typeInto := func(w *app.Win, p *edApp, text string) {
		t.Helper()
		var win objc.ID
		l.Do(func(*app.App) {
			win = objc.ID(w.App().Win.Native().NSWindow)
			objc.Send1(widgettest.NSApp(), objc.Sel("activateIgnoringOtherApps:"), 1)
			objc.Send1(win, objc.Sel("makeKeyAndOrderFront:"), 0)
		})
		if keys && !waitFor(3*time.Second, func() bool {
			var key bool
			l.Do(func(*app.App) { key = objc.SendBool(win, objc.Sel("isKeyWindow")) })
			return key
		}) {
			keys = false
			t.Log("the windows cannot become key here: the text goes in through the text views' input clients, not as keys")
		}
		l.Do(func(*app.App) { p.ed.Focus() })
		if keys {
			l.Until("the editor has the keyboard", 120, func() bool { return p.ed.Focused() && p.sig.Focused })
			l.Do(func(*app.App) { widgettest.NativeWindow(w.App()).TypeText(text) })
		} else {
			l.Do(func(*app.App) { widgettest.TextInput(tv(p)).InsertText(text, widgettest.NoRange) })
		}
		if !waitFor(5*time.Second, func() bool { return string(p.ed.Text()) == text && p.changed > 0 }) {
			t.Fatalf("typed %q into the editor of its window: it holds %q, %d changes seen by its frames%s", text, p.ed.Text(), p.changed, mwStates(l, w1, w2))
		}
	}
	// Digits, with autocorrect off: a word may get a correction, and
	// NSCorrectionPanel's bubble runs a nested loop until the next key (the
	// loop's turns, a window's close, wait for it; editor_darwin_test.go
	// tests that loop).
	l.Do(func(*app.App) { p1.ed.SetAutocorrect(false); p2.ed.SetAutocorrect(false) })
	typeInto(w2, p2, "34")
	typeInto(w1, p1, "12")
	settle()
	if a, b := string(p1.ed.Text()), string(p2.ed.Text()); a != "12" || b != "34" {
		t.Errorf("the editors hold %q and %q, want \"12\" and \"34\"%s", a, b, mwStates(l, w1, w2))
	}
	if !p1.ed.Focused() || !p2.ed.Focused() {
		t.Errorf("the editors' focus: first %v, second %v; want both (each window keeps its first responder)", p1.ed.Focused(), p2.ed.Focused())
	}

	// The loop goes on turning: after the editor's release, after the
	// window's close.
	turns := func(what string) {
		t.Helper()
		b1, b2 := p1.builds.Load(), p2.builds.Load()
		l.Do(func(*app.App) { w1.Wake(); w2.Wake() })
		if !waitFor(5*time.Second, func() bool { return p1.builds.Load() > b1 }) {
			var pump string
			l.Do(func(*app.App) { pump = platform.DebugPumpState() })
			t.Fatalf("%s: the app's loop stopped (builds: first %d → %d, second %d → %d)\n%s%s\nthe main thread (sample):\n%s\n%s", what, b1, p1.builds.Load(), b2, p2.builds.Load(), pump, mwStates(l, w1, w2), mainThreadSample(), stacks())
		}
	}
	turns("before the release")
	l.Do(func(*app.App) { p2.ed.Release() }) // the app's: before its window closes (or after)
	turns("after the second window's editor was released")
	l.Do(func(*app.App) { w2.Close() })
	if !waitFor(5*time.Second, func() bool {
		var open bool
		l.Do(func(*app.App) { open = w2.Open() })
		return !open
	}) {
		var pump string
		l.Do(func(*app.App) { pump = platform.DebugPumpState() })
		t.Fatalf("the second window did not close\n%s%s\n%s", pump, mwStates(l, w1, w2), stacks())
	}
	turns("after the second window closed")
	var hosts int64
	l.Do(func(*app.App) { hosts = platform.ShellObjects().Hosts })
	if hosts != hosts0 {
		t.Errorf("hosts after the second window closed: %d, want %d (the first window's)", hosts, hosts0)
	}
}

// A worker's result reaches the window it is for (Win.Wake): that window
// draws and takes it, the other draws nothing; platform.Wake wakes them all,
// and a closed window's Wake does nothing.
func TestWorkerResultsReachTheirWindow(t *testing.T) {
	first := &mwPane{results: make(chan int, 16)}
	second := &mwPane{results: make(chan int, 16)}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	w2 := mwOpen(t, l, "second", 20, second, second.builds.Load)
	var w1 *app.Win
	l.Do(func(a *app.App) { w1 = a.Window() })
	settle()

	var others int64
	for try := 1; try <= quietTries; try++ {
		start := platform.Now()
		f0, s0, got0 := first.builds.Load(), second.builds.Load(), second.got.Load()
		go func() {
			for i := 0; i < 5; i++ {
				second.results <- i
				w2.Wake()
				time.Sleep(40 * time.Millisecond)
			}
		}()
		if took := func() bool { return second.got.Load() == got0+5 }; !waitFor(5*time.Second, took) {
			if !coveredSince(l, w2, start) {
				t.Fatalf("the second window took %d of 5 results%s", second.got.Load()-got0, mwStates(l, w1, w2))
			}
			// Covered, it parks; it takes the rest when it shows again.
			if try == quietTries || !waitFor(20*time.Second, took) {
				t.Skipf("the second window stays covered by other windows: it took %d of 5 results", second.got.Load()-got0)
			}
			t.Logf("try %d: the second window was covered by another window", try)
			continue
		}
		settle()
		if n := second.builds.Load() - s0; n < 1 || n > 10 {
			t.Errorf("the second window drew %d frames for 5 results", n)
		}
		if first.got.Load() != 0 {
			t.Fatalf("the first window took %d of the second window's results", first.got.Load())
		}
		if others = first.builds.Load() - f0; others == 0 {
			break
		}
		t.Logf("try %d: the first window drew %d frames meanwhile", try, others)
	}
	if others != 0 {
		t.Errorf("the first window drew %d frames for the second window's results (%d tries)", others, quietTries)
	}

	start := platform.Now()
	f0, s0 := first.builds.Load(), second.builds.Load()
	platform.Wake()
	if !waitFor(5*time.Second, func() bool { return first.builds.Load() > f0 && second.builds.Load() > s0 }) {
		if coveredSince(l, w1, start) || coveredSince(l, w2, start) {
			t.Skipf("platform.Wake: a window covered by other windows drew nothing (first %d frames, second %d)", first.builds.Load()-f0, second.builds.Load()-s0)
		}
		t.Errorf("platform.Wake: the first window drew %d frames, the second %d; want both%s", first.builds.Load()-f0, second.builds.Load()-s0, mwStates(l, w1, w2))
	}

	l.Do(func(*app.App) { w2.Close() })
	l.Until("the second window closed", 120, func() bool { return !w2.Open() })
	for try := 1; try <= quietTries; try++ {
		settle()
		f0 = first.builds.Load()
		w2.Wake()
		settle()
		if others = first.builds.Load() - f0; others == 0 {
			break
		}
	}
	if others != 0 {
		t.Errorf("a closed window's Wake made the first window draw %d frames (%d tries)", others, quietTries)
	}
}

// OpenWindow, Win.Close and a window command asked for inside a Build take
// effect after that frame, at the safe point: nothing changes under the
// frame, and the new window's Init does not run inside another's Build.
func TestWindowChangesInsideBuildWaitForTheFrame(t *testing.T) {
	first, second, third := &mwPane{}, &mwPane{}, &mwPane{}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	w2 := mwOpen(t, l, "second", 20, second, second.builds.Load)
	var initInsideBuild atomic.Int32
	initInsideBuild.Store(-1)
	third.init = func(*app.App) error {
		v := int32(0)
		if first.inBuild.Load() || second.inBuild.Load() {
			v = 1
		}
		initInsideBuild.Store(v)
		return nil
	}

	type seen struct {
		w3                        *app.Win
		err                       error
		openNow, secondOpen       bool
		titleNow, titleBefore     string
		windowsNow, windowsBefore int
		done                      bool
	}
	var s seen
	var a1 *app.App
	l.Do(func(a *app.App) { a1 = a })
	title := func() string { return nsString(objc.Send(objc.ID(a1.Win.Native().NSWindow), objc.Sel("title"))) }
	first.runInBuild(func(c *ui.Context) {
		s.titleBefore, s.windowsBefore = title(), len(a1.Windows(nil))
		s.w3, s.err = a1.OpenWindow(mwConfig("third"), third)
		w2.Close()
		a1.Window().SetTitle("first, renamed")
		s.openNow = s.w3 != nil && s.w3.Open()
		s.secondOpen = w2.Open()
		s.titleNow, s.windowsNow = title(), len(a1.Windows(nil))
		s.done = true
	})
	l.Until("the changes asked for in a frame were applied after it", 240, func() bool {
		return s.done && s.w3 != nil && s.w3.Open() && !w2.Open() && third.builds.Load() > 0
	})
	if s.err != nil {
		t.Fatalf("OpenWindow inside Build: %v", s.err)
	}
	if s.openNow || !s.secondOpen || s.windowsNow != s.windowsBefore || s.titleNow != s.titleBefore {
		t.Errorf("inside the frame: the new window open %v, the closed one still open %v, windows %d→%d, title %q→%q; want nothing changed until the frame ended",
			s.openNow, s.secondOpen, s.windowsBefore, s.windowsNow, s.titleBefore, s.titleNow)
	}
	if v := initInsideBuild.Load(); v != 0 {
		t.Errorf("the new window's Init ran inside a Build (%d)", v)
	}
	var after string
	var n int
	l.Do(func(a *app.App) { after, n = title(), len(a.Windows(nil)) })
	if after != "first, renamed" || n != 2 {
		t.Errorf("after the frame: title %q, %d windows; want the new title and two windows", after, n)
	}

	// A window closing itself inside its own Build draws that frame to the
	// end and goes after it.
	var selfOpen atomic.Bool
	third.runInBuild(func(c *ui.Context) { s.w3.Close(); selfOpen.Store(s.w3.Open()) })
	l.Do(func(*app.App) { s.w3.Wake() })
	l.Until("the third window closed itself", 240, func() bool { return !s.w3.Open() })
	if !selfOpen.Load() {
		t.Error("a window's Close inside its own Build closed it under the frame")
	}
}

// mwRich is a window with what a window can hold besides drawing: a hosted
// NSTextView, a Select whose native menu the test opens, and text; a sheet
// is shown on it from the test.
type mwRich struct {
	label  string // the text it shows ("" a default)
	k      *widget.Kit
	a      *app.App
	host   platform.HostID
	sel    int
	opts   []string
	builds atomic.Int64
	center atomic.Pointer[[2]float32] // the Select's centre in pixels
}

func (r *mwRich) Init(a *app.App) error {
	r.a, r.opts = a, []string{"One", "Two"}
	r.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	tv, sel := objc.Send(objc.Cls("NSTextView"), objc.Sel("alloc")), objc.Sel("initWithFrame:") // before the call is assembled: Sel uses the frame
	tv = objc.ID(objc.F.Reset().Arg(uintptr(tv)).Arg(uintptr(sel)).ArgRect(0, 0, 160, 40).Call(objc.MsgSend).R0())
	var err error
	r.host, err = a.Win.NewHost(platform.HostView, platform.HostOptions{View: uintptr(tv), Focusable: true})
	objc.Send(tv, objc.Sel("release")) // the host holds it
	return err
}

func (r *mwRich) Build(c *ui.Context) {
	if buildDepth.Add(1) > 1 {
		nestedBuilds.Add(1)
	}
	defer buildDepth.Add(-1)
	r.builds.Add(1)
	for {
		if _, ok := r.a.TakeDialog(); !ok {
			break
		}
	}
	k := r.k
	k.Begin(c)
	label := r.label
	if label == "" {
		label = "Glyphs of the second window: Ωμ∂ 0123"
	}
	k.Label(label)
	sig := k.Select("Choice", &r.sel, r.opts)
	b := c.Rect(sig.ID)
	r.center.Store(&[2]float32{(b[0] + b[2]) / 2, (b[1] + b[3]) / 2})
	c.NextSize(ui.Px(c.Pt(160)), ui.Px(c.Pt(40)))
	c.Host(uint32(r.host), 7, ui.HostFocusable)
	k.End()
}

// While one window's native menu tracks (a nested AppKit loop, where the
// app's own loop is suspended), another window that animates goes on
// drawing, through its frame handler; no Build ever starts inside another;
// and once the menu is closed the app's loop is its own again.
func TestOtherWindowsDrawWhileAMenuTracks(t *testing.T) {
	first := &mwPane{}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	r := &mwRich{}
	w := mwOpen(t, l, "second", 20, r, r.builds.Load)
	var during, opened atomic.Int64
	var done atomic.Bool
	l.Do(func(*app.App) {
		platform.DebugOnMenuOpen(func(m platform.DebugMenu) {
			opened.Add(1)
			a0 := first.builds.Load()
			deadline := time.Now().Add(3 * time.Second)
			for first.builds.Load() < a0+10 && time.Now().Before(deadline) {
				m.Frame() // runs the tracking loop until the menu's window's handler ran, or 1 s
			}
			during.Store(first.builds.Load() - a0)
			done.Store(true)
			m.Close()
		})
	})
	defer l.Do(func(*app.App) { platform.DebugOnMenuOpen(nil) })
	nested0 := nestedBuilds.Load()
	first.animate.Store(true)
	l.Do(func(a *app.App) {
		a.Window().Wake()
		if c := r.center.Load(); c != nil {
			widgettest.NativeWindow(w.App()).Click(c[0], c[1], 0)
		}
	})
	if !waitFor(10*time.Second, done.Load) {
		first.animate.Store(false)
		t.Fatalf("the second window's menu opened %d times", opened.Load())
	}
	first.animate.Store(false)
	t.Logf("the first window drew %d frames while the second window's menu tracked", during.Load())
	if during.Load() < 10 {
		t.Errorf("the first window drew %d frames while the second window's menu tracked, want it to go on animating", during.Load())
	}
	if n := nestedBuilds.Load() - nested0; n != 0 {
		t.Errorf("%d Builds started inside another", n)
	}
	settle()
	var native bool
	l.Do(func(*app.App) { native = platform.NativeLoop() })
	if native {
		t.Error("after the menu closed (cancelled as it opened), the app still counts as inside a native loop")
	}
}

// When the run ends, every window it opened ends with it: their surfaces
// are destroyed and their handles say closed.
func TestRunEndClosesEveryWindow(t *testing.T) {
	s0 := gpu.LiveSurfaces()
	first, second := &mwPane{}, &mwPane{}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	w := mwOpen(t, l, "second", 20, second, second.builds.Load)
	if n := gpu.LiveSurfaces(); n != s0+2 {
		t.Fatalf("%d surfaces with two windows open, want %d", n, s0+2)
	}
	l.Close()
	if w.Open() || gpu.LiveSurfaces() != s0 {
		t.Errorf("after the run: the second window open %v, %d surfaces (were %d)", w.Open(), gpu.LiveSurfaces(), s0)
	}
}

// leakCounts is everything a window's opening and closing could leave
// behind.
type leakCounts struct {
	win      platform.WindowObjects
	surfaces int64
	shell    platform.ShellCounts
	blocks   objc.Counts
	textures int
	windows  int
}

func (c leakCounts) same(o leakCounts) bool {
	return c.win == o.win && c.surfaces == o.surfaces && c.shell.Hosts == o.shell.Hosts && c.shell.Materials == o.shell.Materials &&
		c.blocks.Blocks == o.blocks.Blocks && c.blocks.KeptBlocks == o.blocks.KeptBlocks && c.blocks.Delegates == o.blocks.Delegates &&
		c.blocks.Observers == o.blocks.Observers && c.textures == o.textures && c.windows == o.windows
}

func (c leakCounts) String() string {
	return fmt.Sprintf("NSWindows %d views %d delegates %d (pending %d), surfaces %d, hosts %d, materials %d, blocks %d kept %d, retained delegates %d, observers %d, textures %d, app windows %d",
		c.win.Windows, c.win.Views, c.win.Delegates, c.win.Pending, c.surfaces, c.shell.Hosts, c.shell.Materials,
		c.blocks.Blocks, c.blocks.KeptBlocks, c.blocks.Delegates, c.blocks.Observers, c.textures, c.windows)
}

func readLeaks(a *app.App) leakCounts {
	live, _ := a.R.TextureCounts()
	return leakCounts{platform.LiveWindowObjects(), gpu.LiveSurfaces(), platform.ShellObjects(), objc.Leaks(), live, len(a.Windows(nil))}
}

// A second window opened and closed 20 times, each time with a hosted
// NSTextView, its native Select menu opened and a sheet shown (answered, or
// left open as the window closes), leaves nothing behind: no window, view,
// delegate, surface, host, material, block, observer, retained delegate or
// texture more than after the first time.
func TestSecondWindowOpenCloseLeaksNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("20 windows")
	}
	first := &mwPane{}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	var menus atomic.Int64
	l.Do(func(*app.App) {
		platform.DebugOnMenuOpen(func(m platform.DebugMenu) { menus.Add(1); m.Close() })
	})
	defer l.Do(func(*app.App) { platform.DebugOnMenuOpen(nil) })

	var base leakCounts
	const cycles = 20
	sheets := 0
	for i := 0; i < cycles; i++ {
		r := &mwRich{}
		w := mwOpen(t, l, fmt.Sprintf("cycle %d", i), 20, r, r.builds.Load)
		// The Select's native menu, opened by a click, closed by the hook.
		m0 := menus.Load()
		l.Do(func(*app.App) {
			if c := r.center.Load(); c != nil {
				widgettest.NativeWindow(w.App()).Click(c[0], c[1], 0)
			}
		})
		if !waitFor(5*time.Second, func() bool { return menus.Load() > m0 }) {
			t.Fatalf("cycle %d: the Select's menu did not open", i)
		}
		// A sheet: answered on even cycles, left open on odd ones.
		l.Do(func(*app.App) {
			if _, err := w.App().Alert(platform.AlertSpec{Message: "Sheet", Buttons: []string{"OK"}}); err != nil {
				t.Errorf("Alert: %v", err)
			}
		})
		var nswin objc.ID
		l.Do(func(*app.App) { nswin = objc.ID(w.App().Win.Native().NSWindow) })
		shown := waitFor(5*time.Second, func() bool {
			var ok bool
			l.Do(func(*app.App) { _, ok = widgettest.SheetOf(nswin) })
			return ok
		})
		if shown {
			sheets++
			if i%2 == 0 {
				l.Do(func(*app.App) {
					if sh, ok := widgettest.SheetOf(nswin); ok {
						sh.Press("OK")
					}
				})
			}
		}
		l.Do(func(*app.App) { w.Close() })
		l.Until(fmt.Sprintf("cycle %d's window closed", i), 240, func() bool { return !w.Open() })
		l.Frames(2) // a pump after the close: the window's objects are released
		var now leakCounts
		l.Do(func(a *app.App) { now = readLeaks(a) })
		if i == 0 {
			base = now
			continue
		}
		if !now.same(base) {
			t.Fatalf("after %d cycles: %v\nafter the first: %v", i+1, now, base)
		}
	}
	t.Logf("%d cycles, %d menus opened, %d sheets shown; counts after each: %v", cycles, menus.Load(), sheets, base)
	if sheets < cycles {
		t.Errorf("only %d of %d sheets were shown", sheets, cycles)
	}
}

// The app's windows share one renderer, one text system and one atlas: a
// second window, at another zoom and drawing glyphs the first never drew,
// makes no texture for the atlas pages that exist (one texture per page
// serves every window), only what its widgets make for themselves (as the
// first window's did: a kit's icon atlas) and the pages its glyphs may add;
// its closing frees its widgets' textures and nothing the first window draws
// with.
func TestWindowsShareTheRendererAndTheAtlas(t *testing.T) {
	first := &mwRich{label: "The first window's text"}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	l.Frames(2)
	type counts struct{ textures, pages int }
	read := func(a *app.App) counts {
		live, _ := a.R.TextureCounts()
		return counts{live, a.Text.Pages()}
	}
	var c0, c1, c2 counts
	l.Do(func(a *app.App) { c0 = read(a) })
	second := &mwRich{}
	w := mwOpen(t, l, "second", 20, second, second.builds.Load)
	var sameR, sameText bool
	l.Do(func(a *app.App) {
		b := w.App()
		sameR, sameText = b.R == a.R, b.Text == a.Text
		b.SetZoom(1.5)
	})
	if !sameR || !sameText {
		t.Fatalf("the second window's renderer shared %v, text system shared %v", sameR, sameText)
	}
	b0 := second.builds.Load()
	l.Do(func(*app.App) { w.Wake() })
	if !waitFor(5*time.Second, func() bool { return second.builds.Load() > b0 }) {
		t.Fatal("the second window drew no frame at zoom 1.5")
	}
	settle()
	l.Do(func(a *app.App) { c1 = read(a) })
	l.Do(func(*app.App) { w.Close() })
	l.Until("the second window closed", 240, func() bool { return !w.Open() })
	f0 := first.builds.Load()
	l.Frames(3)
	l.Do(func(a *app.App) { c2 = read(a) })
	t.Logf("textures and atlas pages: %+v with the first window, %+v with the second open at zoom 1.5, %+v after it closed", c0, c1, c2)
	widgets := c0.textures - 1 - c0.pages // the first window's widgets' (texture 0 is the renderer's)
	if got, want := c1.textures-c0.textures, widgets+c1.pages-c0.pages; got != want {
		t.Errorf("the second window added %d textures, want %d (its widgets' %d, %d new atlas pages): its text must be drawn from the shared page textures", got, want, widgets, c1.pages-c0.pages)
	}
	if got, want := c2.textures-c0.textures, c2.pages-c0.pages; got != want || first.builds.Load() <= f0 {
		t.Errorf("after the second window closed: %d textures more than before it (want the %d new pages'), the first window drew %d frames", got, want, first.builds.Load()-f0)
	}
}

// The window commands reach AppKit: title, represented file and URL, frame,
// document-edited mark, hide and show; and Screens lists the displays without allocating.
func TestWindowCommandsAndScreens(t *testing.T) {
	first := &mwPane{}
	l := widgettest.StartLive(t, mwConfig("first"), first)
	type got struct {
		title, file, url        string
		frame                   [4]float64
		edited, hidden, visible bool
		screens                 int
		allocs                  float64
		primary                 app.Screen
	}
	var g got
	l.Do(func(a *app.App) {
		w := a.Window()
		w.SetTitle("renamed")
		w.SetRepresentedFile("/tmp/gnui-represented.txt")
		w.SetFrame(40, 80, 400, 300)
		w.SetDocumentEdited(true)
	})
	settle()
	l.Do(func(a *app.App) {
		w, nswin := a.Window(), objc.ID(a.Win.Native().NSWindow)
		g.title = nsString(objc.Send(nswin, objc.Sel("title")))
		g.file = nsString(objc.Send(nswin, objc.Sel("representedFilename")))
		w.SetRepresentedURL("https://example.com/doc")
		x, y, wd, h := w.Frame()
		g.frame = [4]float64{x, y, wd, h}
		g.edited = objc.SendBool(nswin, objc.Sel("isDocumentEdited"))
		w.Hide()
	})
	settle()
	l.Do(func(a *app.App) {
		g.hidden = !objc.SendBool(objc.ID(a.Win.Native().NSWindow), objc.Sel("isVisible"))
		g.url = nsString(objc.Send(objc.Send(objc.ID(a.Win.Native().NSWindow), objc.Sel("representedURL")), objc.Sel("absoluteString")))
		a.Window().Show()
	})
	settle()
	l.Do(func(a *app.App) {
		g.visible = objc.SendBool(objc.ID(a.Win.Native().NSWindow), objc.Sel("isVisible"))
		dst := make([]app.Screen, 0, 8)
		dst = app.Screens(dst)
		g.screens = len(dst)
		if len(dst) > 0 {
			g.primary = dst[0]
		}
		g.allocs = testing.AllocsPerRun(20, func() { dst = app.Screens(dst) })
	})
	if g.title != "renamed" || !g.edited || !g.hidden || !g.visible {
		t.Errorf("title %q, edited %v, hidden after Hide %v, visible after Show %v", g.title, g.edited, g.hidden, g.visible)
	}
	if g.file != "/tmp/gnui-represented.txt" {
		t.Errorf("representedFilename after SetRepresentedFile: %q", g.file)
	}
	if g.url != "https://example.com/doc" {
		t.Errorf("representedURL after SetRepresentedURL: %q", g.url)
	}
	if g.frame != [4]float64{40, 80, 400, 300} {
		t.Errorf("frame after SetFrame(40, 80, 400, 300): %v", g.frame)
	}
	if g.screens < 1 || g.primary.Scale <= 0 {
		t.Errorf("screens: %d, the primary one %+v", g.screens, g.primary)
	}
	if g.allocs != 0 {
		t.Errorf("Screens with room allocated %.1f times", g.allocs)
	}
}

// SetZoomed zooms the window only when its state differs, and Zoomed reads
// AppKit's isZoomed: the maximized window an app restores at launch.
func TestWindowZoomed(t *testing.T) {
	l := widgettest.StartLive(t, mwConfig("zoomed"), &mwPane{})
	type state struct{ zoomed, native bool }
	read := func() (s state) {
		l.Do(func(a *app.App) {
			s = state{a.Window().Zoomed(), objc.SendBool(objc.ID(a.Win.Native().NSWindow), objc.Sel("isZoomed"))}
		})
		return s
	}
	if s := read(); s.zoomed || s.native {
		t.Fatalf("a new window is zoomed: %+v", s)
	}
	l.Do(func(a *app.App) { a.Window().SetZoomed(false) }) // unchanged: nothing happens
	settle()
	if s := read(); s.zoomed {
		t.Errorf("SetZoomed(false) of a window that is not zoomed zoomed it: %+v", s)
	}
	l.Do(func(a *app.App) { a.Window().SetZoomed(true) })
	var s state
	if !waitFor(5*time.Second, func() bool { s = read(); return s.native }) {
		t.Fatal("SetZoomed(true) did not zoom the window")
	}
	if !s.zoomed {
		t.Errorf("Zoomed() is false for a zoomed window: %+v", s)
	}
	l.Do(func(a *app.App) { a.Window().SetZoomed(true) }) // unchanged: it must not toggle back
	settle()
	if s = read(); !s.native {
		t.Errorf("SetZoomed(true) of a zoomed window zoomed it back: %+v", s)
	}
	l.Do(func(a *app.App) { a.Window().SetZoomed(false) })
	if !waitFor(5*time.Second, func() bool { s = read(); return !s.native }) {
		t.Fatal("SetZoomed(false) did not restore the window")
	}
}

// nsString is the Go string of an NSString. Main thread.
func nsString(s objc.ID) string { return string(objc.AppendUTF8(nil, s)) }
