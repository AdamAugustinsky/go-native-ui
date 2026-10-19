//go:build darwin

package app

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
					if strings.Contains(l, "refused 0,") {
						t.Error("AppKit never called the frame handler inside the call: the case did not test re-entry")
					}
					if !strings.Contains(l, "nested builds 0,") {
						t.Error("a Build ran inside a Build")
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
	// green-button: when the clicks were scheduled, the frames drawn in the
	// 1.5 s after each, and whether a frame saw the window in full screen
	clicks  [2]int64
	during  [2]int
	sawFull bool
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
		b.greenButton(win)
		return
	}
	if b.n++; b.done || b.n < 3 {
		return
	}
	b.done = true
	switch b.c {
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
	b := &nestedBuilder{c: c}
	var st StartupReport
	a, err := openWindowed(Config{Title: "nested " + c, Width: 400, Height: 300}, b, &st)
	if err != nil {
		fmt.Println("SKIP: cannot open a window here:", err)
		return
	}
	a.Win.SetFrameHandler(a.frameHandler)
	var quit atomic.Bool
	life := 3 * time.Second
	if c == "green-button" {
		life = 7 * time.Second
	}
	time.AfterFunc(life, func() { quit.Store(true); platform.Wake() })
	a.hook = func() {
		if quit.Load() {
			a.done = true
		}
	}
	if err := a.loop(); err != nil {
		fmt.Println("loop:", err)
	}
	fmt.Printf("nested %s: ok, frames %d, refused %d, acted %v, nested builds %d, drawables %d", c, a.frames, a.reentries, b.done, b.nested, len(b.targets))
	if c == "green-button" {
		fmt.Printf(", full screen seen %v, frames during entry %d, during exit %d, exit clicked %v", b.sawFull, b.during[0], b.during[1], b.clicks[1] != 0)
	}
	fmt.Println()
	a.close()
}

// greenButton clicks the window's green button from the run loop, as a user
// does (performClick: on the zoom button, scheduled for the next turn, not
// sent from the frame): into full screen, and 3 s later out of it, counting
// the frames drawn while each animation runs.
func (b *nestedBuilder) greenButton(win objc.ID) {
	now := platform.Now()
	for i, t := range b.clicks {
		if t != 0 && now-t < 1.5e9 {
			b.during[i]++
		}
	}
	if objc.Send(win, objc.Sel("styleMask"))&(1<<14) != 0 { // NSWindowStyleMaskFullScreen
		b.sawFull = true
	}
	b.n++
	i := -1
	switch {
	case b.n == 3:
		i = 0
	case b.clicks[0] != 0 && b.clicks[1] == 0 && now-b.clicks[0] > 3e9:
		i = 1
	}
	if i < 0 {
		return
	}
	b.clicks[i], b.done = now, true
	zoom := objc.Send1(win, objc.Sel("standardWindowButton:"), 2) // NSWindowZoomButton
	sel, click := objc.Sel("performSelector:withObject:afterDelay:"), objc.Sel("performClick:")
	objc.F.Reset().Arg(uintptr(zoom)).Arg(uintptr(sel)).Arg(uintptr(click)).Arg(0).ArgF64(0).Call(objc.MsgSend)
}

// The user's way into full screen and out: the green button clicked from
// the run loop, not from a frame. The app draws while each animation runs
// (from the frame handler, which nothing refuses here), sees the window in
// full screen, and nothing crashes.
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
	var frames, refused, nested, drawables, entry, exit int
	var acted, full, exitClicked bool
	if _, err := fmt.Sscanf(l, "nested green-button: ok, frames %d, refused %d, acted %t, nested builds %d, drawables %d, full screen seen %t, frames during entry %d, during exit %d, exit clicked %t",
		&frames, &refused, &acted, &nested, &drawables, &full, &entry, &exit, &exitClicked); err != nil {
		t.Fatalf("cannot read %q: %v", l, err)
	}
	if !full || !exitClicked || entry == 0 || exit == 0 || nested != 0 {
		t.Error("want the window seen in full screen, both clicks, frames drawn during both animations, no Build inside a Build")
	}
}
