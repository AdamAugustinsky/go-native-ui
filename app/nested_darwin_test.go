//go:build darwin

package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/ui"
)

// nestedChild is the environment variable that makes the test binary the
// child of TestNestedRunLoopInFrame, running one case.
const nestedChild = "GNUI_TEST_NESTED_CASE"

// The child runs its case on the main thread, which AppKit requires: init
// keeps the main goroutine there, and TestMain runs the case instead of the
// tests.
func init() {
	if os.Getenv(nestedChild) != "" {
		runtime.LockOSThread()
	}
}

func TestMain(m *testing.M) {
	if c := os.Getenv(nestedChild); c != "" {
		runNestedChild(c)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// AppKit calls that run a nested run loop or resize the window synchronously,
// made from Build, in the middle of a frame: AppKit calls the frame handler
// from inside them. They must not crash (a frame drawn from there once ran
// inside the frame in progress, whose EndFrame then popped a cleared pool
// token: SIGSEGV in objc_autoreleasePoolPop). Each runs in a child process,
// so that a crash fails the case instead of killing the test binary.
func TestNestedRunLoopInFrame(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a window and goes full screen")
	}
	for _, c := range []string{"fullscreen", "runloop-zoom"} {
		t.Run(c, func(t *testing.T) {
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), nestedChild+"="+c)
			out, err := cmd.CombinedOutput()
			s := string(out)
			if i := strings.Index(s, "SKIP:"); i >= 0 {
				t.Skip(strings.TrimSpace(s[i+5:]))
			}
			if err != nil || !strings.Contains(s, "nested "+c+": ok") {
				if len(s) > 3000 {
					s = s[:3000]
				}
				t.Fatalf("the child failed (%v):\n%s", err, s)
			}
			for _, l := range strings.Split(s, "\n") {
				if strings.HasPrefix(l, "nested ") {
					t.Log(l)
					if !strings.Contains(l, "nested builds 0,") {
						t.Error("a Build ran inside a Build")
					}
					if !strings.Contains(l, "refused 0,") {
						break
					}
					// Full screen re-enters wherever it runs. Whether a zoom
					// animates as a live resize depends on the machine (on
					// the CI runner it does not); there the case proves
					// nothing, which is not a failure.
					if c == "fullscreen" {
						t.Error("AppKit never called the frame handler inside the call: the case did not test re-entry")
					} else {
						t.Skip("AppKit did not animate the zoom as a live resize here: nothing re-entered")
					}
				}
			}
		})
	}
}

// nestedBuilder makes the case's AppKit call from Build, once, in the third
// frame (the window is on screen by then).
type nestedBuilder struct {
	a       *App
	c       string
	n       int
	done    bool
	inBuild bool
	nested  int       // Builds that began inside a Build
	targets []uintptr // the distinct drawable textures drawn into
	// green-button: whether each click was made
	clicks [2]bool
}

func (b *nestedBuilder) Init(a *App) error { b.a = a; return nil }

func (b *nestedBuilder) Build(c *ui.Context) {
	if b.inBuild {
		b.nested++
	}
	b.inBuild = true
	defer func() { b.inBuild = false }()
	if t := b.a.fr.Target; t != 0 && !slices.Contains(b.targets, t) {
		b.targets = append(b.targets, t)
	}
	c.RequestFrame()
	win := objc.ID(b.a.Win.Native().NSWindow)
	if b.c == "green-button" {
		fs.frames[fs.phase]++
		b.greenButton(win)
		return
	}
	if b.n++; b.done || b.n < 3 {
		return
	}
	b.done = true
	switch b.c {
	case "panic":
		panic(errBuildPanic)
	case "fullscreen":
		// The green button's action, synchronously: AppKit resizes the
		// window and calls back before it returns.
		objc.Send1(win, objc.Sel("toggleFullScreen:"), 0)
	case "runloop-zoom":
		// A nested run loop (what modal sessions and menu tracking run), in
		// which an animated zoom resizes the window. AppKit treats the
		// animation as a live resize and calls the frame handler at each
		// step, from inside both.
		sel, zoom := objc.Sel("performSelector:withObject:afterDelay:"), objc.Sel("zoom:")
		objc.F.Reset().Arg(uintptr(win)).Arg(uintptr(sel)).Arg(uintptr(zoom)).Arg(0).ArgF64(0.05).Call(objc.MsgSend)
		date, selDate := objc.Cls("NSDate"), objc.Sel("dateWithTimeIntervalSinceNow:")
		until := objc.ID(objc.F.Reset().Arg(uintptr(date)).Arg(uintptr(selDate)).ArgF64(1).Call(objc.MsgSend).R0())
		objc.Send1(objc.Send(objc.Cls("NSRunLoop"), objc.Sel("currentRunLoop")), objc.Sel("runUntilDate:"), uintptr(until))
	}
}

func runNestedChild(c string) {
	if c == "dragin" {
		runDragInChild() // drag_darwin_test.go
		return
	}
	b := &nestedBuilder{c: c}
	var st StartupReport
	a, err := openWindowed(Config{Title: "nested " + c, Width: 400, Height: 300}, b, &st, false)
	if err != nil {
		fmt.Println("SKIP: cannot open a window here:", err)
		return
	}
	// The frames the frame handler presents (live resize: AppKit's
	// animations), counted per full-screen phase.
	a.Win.SetFrameHandler(func() {
		n := a.frames
		a.frameHandler()
		fs.handlerFrames[fs.phase] += a.frames - n
	})
	if c == "green-button" {
		observeFullScreen(objc.ID(a.Win.Native().NSWindow))
	}
	var quit atomic.Bool
	life := 3 * time.Second
	if c == "green-button" {
		life = 20 * time.Second // the deadline; the case ends once the exit is over
	}
	time.AfterFunc(life, func() { quit.Store(true); platform.Wake() })
	a.hook = func() {
		if quit.Load() || c == "green-button" && fs.phase == fsDone && platform.Now()-fs.at[fsDone] > 300e6 {
			a.done = true
		}
	}
	var pan string
	if c == "panic" {
		pan = runPanicCase(a)
	}
	if err := a.loop(); err != nil {
		fmt.Println("loop:", err)
	}
	fmt.Printf("nested %s: ok, frames %d, refused %d, acted %v, nested builds %d, drawables %d", c, a.frames, a.reentries, b.done, b.nested, len(b.targets))
	if c == "green-button" {
		if st := a.Win.DebugState(); fs.at[fsDone] == 0 && !st.Visible {
			// A locked screen covers the window, and AppKit never
			// finishes the transition (no windowDidEnterFullScreen).
			fmt.Printf("\nSKIP: the window is not visible (occlusionState %d: a locked screen?), so full screen does not complete\n", st.Occlusion)
		}
		fmt.Printf(", entered %t, exited %t, windowed after exit %t, handler frames during entry %d of %d, during exit %d of %d",
			fs.at[fsFull] != 0, fs.at[fsDone] != 0, fs.windowedAfterExit,
			fs.handlerFrames[fsEntering], fs.frames[fsEntering], fs.handlerFrames[fsExiting], fs.frames[fsExiting])
	}
	fmt.Print(pan)
	fmt.Println()
	a.close()
}

// errBuildPanic is what the panic case's Build panics with.
var errBuildPanic = errors.New("a panic in Build")

// runPanicCase runs the loop until Build panics, recovers the panic, and
// reports what the frame left behind; the caller then runs the loop on,
// counting the frames it draws after the panic.
func runPanicCase(a *App) string {
	r := func() (r any) {
		defer func() { r = recover() }()
		a.loop()
		return nil
	}()
	inFrame, reset := a.inFrame, a.fr == gpu.Frame{}
	refused := a.reentries
	a.frameHandler() // live resize draws from here: it must not be refused
	n := a.frames
	a.hook = func() {
		if a.frames-n >= 10 {
			a.done = true
		}
	}
	return fmt.Sprintf(", recovered %t, in frame after %t, frame reset %t, handler refused after %t, frames from %d",
		r == errBuildPanic, inFrame, reset, a.reentries != refused, n)
}

// greenButton clicks the window's green button from the run loop, as a user
// does (performClick: on the zoom button, scheduled for the next turn, not
// sent from the frame): into full screen, and once the window has been in
// full screen for a second, out of it.
func (b *nestedBuilder) greenButton(win objc.ID) {
	b.n++
	i := -1
	switch {
	case b.n == 3:
		i = 0
	case !b.clicks[1] && fs.phase == fsFull && platform.Now()-fs.at[fsFull] > 1e9:
		i = 1
	}
	if i < 0 {
		return
	}
	b.clicks[i], b.done = true, true
	zoom := objc.Send1(win, objc.Sel("standardWindowButton:"), 2) // NSWindowZoomButton
	sel, click := objc.Sel("performSelector:withObject:afterDelay:"), objc.Sel("performClick:")
	objc.F.Reset().Arg(uintptr(zoom)).Arg(uintptr(sel)).Arg(uintptr(click)).Arg(0).ArgF64(0).Call(objc.MsgSend)
}

// The full-screen phases of the green-button case, as the window's
// notifications report them.
const (
	fsWindowed = iota
	fsEntering // windowWillEnterFullScreen to windowDidEnterFullScreen
	fsFull
	fsExiting // windowWillExitFullScreen to windowDidExitFullScreen
	fsDone
	fsPhases
)

var fs struct {
	phase             int
	at                [fsPhases]int64 // when each phase began
	handlerFrames     [fsPhases]int   // frames the frame handler presented in each
	frames            [fsPhases]int   // all the frames built in each
	windowedAfterExit bool            // no NSWindowStyleMaskFullScreen at windowDidExitFullScreen
	win               objc.ID
}

// observeFullScreen follows the window's four full-screen notifications.
func observeFullScreen(win objc.ID) {
	fs.win = win
	d := objc.Define("GNUITestFullScreenObserver", objc.Cls("NSObject"), nil)
	notes := [...]struct {
		sel, name string
		phase     int
	}{
		{"gnuiWillEnter:", "NSWindowWillEnterFullScreenNotification", fsEntering},
		{"gnuiDidEnter:", "NSWindowDidEnterFullScreenNotification", fsFull},
		{"gnuiWillExit:", "NSWindowWillExitFullScreenNotification", fsExiting},
		{"gnuiDidExit:", "NSWindowDidExitFullScreenNotification", fsDone},
	}
	for _, n := range notes {
		d.Method(objc.Sel(n.sel), "v@:@", func(*ffi.CB) { fullScreenPhase(n.phase) })
	}
	cls := d.Register()
	obs := objc.Send(objc.Send(cls, objc.Sel("alloc")), objc.Sel("init"))
	center := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	add := objc.Sel("addObserver:selector:name:object:")
	for _, n := range notes {
		sel, name := objc.Sel(n.sel), objc.Str(n.name)
		objc.F.Reset().Arg(uintptr(center)).Arg(uintptr(add)).Arg(uintptr(obs)).Arg(uintptr(sel)).Arg(uintptr(name)).Arg(uintptr(win)).Call(objc.MsgSend)
	}
}

func fullScreenPhase(p int) {
	fs.phase, fs.at[p] = p, platform.Now()
	if p == fsDone {
		fs.windowedAfterExit = objc.Send(fs.win, objc.Sel("styleMask"))&(1<<14) == 0 // NSWindowStyleMaskFullScreen
	}
}

// The user's way into full screen and out: the green button clicked from
// the run loop, not from a frame. Both transitions complete (the window's
// notifications; no full-screen style bit after the exit), the frame handler
// presents frames while each animation runs (live resize, between the
// will- and did- notifications), and nothing crashes.
func TestGreenButtonFullScreen(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a window and goes full screen")
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), nestedChild+"=green-button")
	out, err := cmd.CombinedOutput()
	s := string(out)
	if i := strings.Index(s, "SKIP:"); i >= 0 {
		t.Skip(strings.TrimSpace(s[i+5:]))
	}
	i := strings.Index(s, "nested green-button: ok")
	if err != nil || i < 0 {
		if len(s) > 3000 {
			s = s[:3000]
		}
		t.Fatalf("the child failed (%v):\n%s", err, s)
	}
	l := s[i:]
	if j := strings.IndexByte(l, '\n'); j >= 0 {
		l = l[:j]
	}
	t.Log(l)
	var frames, refused, nested, drawables, entry, entryAll, exit, exitAll int
	var acted, entered, exited, windowed bool
	if _, err := fmt.Sscanf(l, "nested green-button: ok, frames %d, refused %d, acted %t, nested builds %d, drawables %d, entered %t, exited %t, windowed after exit %t, handler frames during entry %d of %d, during exit %d of %d",
		&frames, &refused, &acted, &nested, &drawables, &entered, &exited, &windowed, &entry, &entryAll, &exit, &exitAll); err != nil {
		t.Fatalf("cannot read %q: %v", l, err)
	}
	if !entered || !exited || !windowed {
		t.Error("want the window into full screen and back out of it")
	}
	if entry == 0 || exit == 0 {
		t.Error("want frames presented by the frame handler while each animation runs")
	}
	if nested != 0 {
		t.Error("a Build ran inside a Build")
	}
}

// A panic in Build, recovered further up (a test harness, an app's own
// recover), leaves no frame half done: the frame is abandoned (its drawable
// and command buffer released, its pool drained), the frame handler is not
// refused afterwards, and the loop draws on.
func TestBuildPanicRecovered(t *testing.T) {
	if testing.Short() {
		t.Skip("opens a window")
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), nestedChild+"=panic")
	out, err := cmd.CombinedOutput()
	s := string(out)
	if i := strings.Index(s, "SKIP:"); i >= 0 {
		t.Skip(strings.TrimSpace(s[i+5:]))
	}
	i := strings.Index(s, "nested panic: ok")
	if err != nil || i < 0 {
		if len(s) > 3000 {
			s = s[:3000]
		}
		t.Fatalf("the child failed (%v):\n%s", err, s)
	}
	l := s[i:]
	if j := strings.IndexByte(l, '\n'); j >= 0 {
		l = l[:j]
	}
	t.Log(l)
	var frames, refused, nested, drawables, from int
	var acted, recovered, inFrame, reset, refusedAfter bool
	if _, err := fmt.Sscanf(l, "nested panic: ok, frames %d, refused %d, acted %t, nested builds %d, drawables %d, recovered %t, in frame after %t, frame reset %t, handler refused after %t, frames from %d",
		&frames, &refused, &acted, &nested, &drawables, &recovered, &inFrame, &reset, &refusedAfter, &from); err != nil {
		t.Fatalf("cannot read %q: %v", l, err)
	}
	if !recovered {
		t.Fatal("Build did not panic, or the panic was not the one raised")
	}
	if inFrame || !reset || refusedAfter {
		t.Error("want the panicked frame abandoned: not in a frame, the Frame reset, the frame handler not refused")
	}
	if frames-from < 10 {
		t.Errorf("drew %d frames after the panic, want 10", frames-from)
	}
}
