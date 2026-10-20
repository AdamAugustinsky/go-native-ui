//go:build darwin

package widgettest

import (
	"sync"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/ui"
)

// sess is the smallest app that draws: an empty frame, and a frame again when
// asked.
type sess struct{ hook func(a *app.App) }

func (f *sess) Init(a *app.App) error {
	if f.hook != nil {
		f.hook(a)
	}
	return nil
}
func (f *sess) Build(c *ui.Context) {}

func sessConfig() app.Config { return app.Config{Title: "session", Width: 320, Height: 200} }

// quick are the limits of the scenarios that break a LiveApp on purpose.
var quick = LiveOptions{Start: 3 * time.Second, Call: 600 * time.Millisecond, Close: 600 * time.Millisecond, Stall: 1500 * time.Millisecond, Wait: 20 * time.Second}

// environment is a look at a healthy environment whatever this machine is
// (a locked screen, a hidden window): the screen unlocked, the window
// visible, the display ticking (a counter that moves with each look) with a
// frame armed and the link running.
func healthyEnv(*app.App) envSnapshot {
	ticks++
	return envSnapshot{HaveApp: true, LockedKnown: true, NSVisible: true, Line: "app{healthy}",
		Win: platform.DebugState{Valid: true, Visible: true, Occlusion: 2, FrameArmed: true, Ticks: ticks}}
}

var ticks uint64

func init() {
	// A callback that never returns: Do gives up, the cleanup is bounded, and
	// the child ends.
	RegisterLiveScenario("live-blocked-callback", func(t testing.TB) {
		l := StartLiveWith(t, sessConfig(), &sess{}, quick)
		l.Do(func(*app.App) { select {} })
	})
	// The main thread is busy in an earlier call: the next never starts.
	RegisterLiveScenario("live-blocked-before-start", func(t testing.TB) {
		l := StartLiveWith(t, sessConfig(), &sess{}, quick)
		go l.call(func(*app.App) { select {} }, time.Hour)
		time.Sleep(200 * time.Millisecond)
		l.Do(func(*app.App) {})
	})
	// The window's Init never returns: the first frame never comes, and the
	// main thread does not answer a look at the environment.
	RegisterLiveScenario("live-blocked-start", func(t testing.TB) {
		StartLiveWith(t, sessConfig(), &sess{hook: func(*app.App) { select {} }}, quick)
	})
	// t.Fatal and t.Skip called inside Do end the main thread's goroutine.
	RegisterLiveScenario("live-fatal-in-do", func(t testing.TB) {
		l := StartLiveWith(t, sessConfig(), &sess{}, quick)
		l.Do(func(*app.App) { t.Fatalf("from the main thread") })
	})
	RegisterLiveScenario("live-skip-in-do", func(t testing.TB) {
		l := StartLiveWith(t, sessConfig(), &sess{}, quick)
		l.Do(func(*app.App) { t.Skipf("from the main thread") })
	})
	// Frames that stop after some had come, with nothing in the environment to
	// explain it (the window is visible, the screen is not locked, the display
	// ticks): a regression, so a failure. Delivery is withheld by the session's
	// test seam, since the code under test is not what stops here.
	RegisterLiveScenario("live-stall-unexplained", func(t testing.TB) {
		o := quick
		o.snap = healthyEnv
		l := StartLiveWith(t, sessConfig(), &sess{}, o)
		l.Frames(5)
		l.sb.hold.Store(true)
		l.Frames(3)
	})
	// Frames that stop because nobody can see the window: the environment, so a
	// skip that says so.
	RegisterLiveScenario("live-stall-hidden", func(t testing.TB) {
		o := quick
		o.snap = func(a *app.App) envSnapshot {
			s := healthyEnv(a)
			s.NSVisible = false
			return s
		}
		l := StartLiveWith(t, sessConfig(), &sess{}, o)
		l.Frames(5)
		l.sb.hold.Store(true)
		l.Frames(3)
	})
	// The display does not tick while the run loop is alive: the same.
	RegisterLiveScenario("live-stall-display", func(t testing.TB) {
		o := quick
		o.snap = func(a *app.App) envSnapshot {
			s := healthyEnv(a)
			s.Win.Ticks = 77
			return s
		}
		l := StartLiveWith(t, sessConfig(), &sess{}, o)
		l.Frames(5)
		l.sb.hold.Store(true)
		l.Frames(3)
	})
}

func runLiveScenarioT(t *testing.T, name string) ScenarioResult {
	t.Helper()
	r := Scenario(t, name, 60*time.Second)
	t.Logf("%s: %v in %v\n%s", name, r.KV, r.Took.Round(time.Millisecond), r.Out)
	return r
}

func TestBlockedCallbackEndsInAFailure(t *testing.T) {
	r := runLiveScenarioT(t, "live-blocked-callback")
	if r.Get("outcome") != "fatal" || !strings.Contains(r.Out, "still running on the main thread") {
		t.Errorf("want a fatal naming the blocked callback")
	}
}

func TestBlockedMainThreadEndsInAFailure(t *testing.T) {
	r := runLiveScenarioT(t, "live-blocked-before-start")
	if r.Get("outcome") != "fatal" || !strings.Contains(r.Out, "did not take up the call") {
		t.Errorf("want a fatal naming the busy main thread")
	}
}

func TestBlockedStartEndsInAFailure(t *testing.T) {
	r := runLiveScenarioT(t, "live-blocked-start")
	if r.Get("outcome") != "fatal" || !strings.Contains(r.Out, "main thread is blocked") {
		t.Errorf("want a fatal naming the blocked main thread")
	}
}

func TestFatalInDoIsAReportedFailure(t *testing.T) {
	r := runLiveScenarioT(t, "live-fatal-in-do")
	if r.Get("outcome") != "fatal" || !strings.Contains(r.Out, "ended its goroutine") {
		t.Errorf("want the misuse named")
	}
}

func TestSkipInDoIsAReportedFailure(t *testing.T) {
	r := runLiveScenarioT(t, "live-skip-in-do")
	if r.Get("outcome") != "fatal" || !strings.Contains(r.Out, "ended its goroutine") {
		t.Errorf("want the misuse named, and the test failed")
	}
}

func TestStallAfterSomeFramesWithoutEvidenceFails(t *testing.T) {
	r := runLiveScenarioT(t, "live-stall-unexplained")
	if r.Get("outcome") != "fatal" || !strings.Contains(r.Out, "a regression in frame delivery") || !strings.Contains(r.Out, "state: app{") {
		t.Errorf("want a failure that says regression and prints the state line")
	}
}

func TestStallOfAHiddenWindowSkipsAndSaysWhy(t *testing.T) {
	r := runLiveScenarioT(t, "live-stall-hidden")
	if r.Get("outcome") != "skip" || !strings.Contains(r.Out, "not visible") {
		t.Errorf("want a skip that names the hidden window")
	}
}

func TestStallOfADeadDisplaySkipsAndSaysWhy(t *testing.T) {
	r := runLiveScenarioT(t, "live-stall-display")
	if r.Get("outcome") != "skip" || !strings.Contains(r.Out, "the display does not tick") {
		t.Errorf("want a skip that names the silent display")
	}
}

// What counts as evidence that the environment cannot draw, and what does
// not: a hidden window does, a display that stopped ticking under a live run
// loop does, and a missing frame with everything else in order does not.
func TestClassifyStall(t *testing.T) {
	ok := func(*app.App) envSnapshot { return healthyEnv(nil) }
	mod := func(f func(s *envSnapshot)) envSnapshot { s := ok(nil); f(&s); return s }
	base := mod(func(s *envSnapshot) { s.Win.Ticks = 5 })
	for _, c := range []struct {
		name  string
		s1    envSnapshot
		s2    envSnapshot
		beats int
		want  string // "" for no evidence
	}{
		{"locked screen", base, mod(func(s *envSnapshot) { s.Locked = true; s.Win.Ticks = 9 }), 6, "locked"},
		{"lock state unknown is not locked", base, mod(func(s *envSnapshot) { s.Locked, s.LockedKnown, s.Win.Ticks = true, false, 9 }), 6, ""},
		{"miniaturized", base, mod(func(s *envSnapshot) { s.Win.Mini = true; s.Win.Ticks = 9 }), 6, "miniaturized"},
		{"ordered out", base, mod(func(s *envSnapshot) { s.NSVisible = false; s.Win.Ticks = 9 }), 6, "not visible"},
		{"occluded", base, mod(func(s *envSnapshot) { s.Win.Visible, s.Win.Occlusion, s.Win.Ticks = false, 8192, 9 }), 6, "occluded"},
		{"display silent under a live run loop", base, mod(func(s *envSnapshot) { s.Win.Ticks = 5 }), 6, "does not tick"},
		{"ticks are moving: frames are lost elsewhere", base, mod(func(s *envSnapshot) { s.Win.Ticks = 9 }), 6, ""},
		{"no frame armed: nothing asked for a tick", base, mod(func(s *envSnapshot) { s.Win.FrameArmed = false }), 6, ""},
		{"link paused: our bug, not the display's", base, mod(func(s *envSnapshot) { s.Win.LinkPausedOS = true }), 6, ""},
		{"run loop barely answered", base, mod(func(s *envSnapshot) {}), 2, ""},
		{"no app", base, envSnapshot{}, 6, ""},
	} {
		got := classifyStall(c.s1, c.s2, c.beats)
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: evidence %q, want %q", c.name, got, c.want)
		}
	}
}

// The look at a real window reads what AppKit says about it.
func TestSnapshotReadsTheRealWindow(t *testing.T) {
	l := StartLive(t, sessConfig(), &sess{})
	l.Frames(2)
	var shown, hidden, back envSnapshot
	l.Do(func(a *app.App) {
		w := objc.ID(a.Win.Native().NSWindow)
		objc.Send1(w, objc.Sel("orderFront:"), 0)
		shown = snapshotEnv(a)
		objc.Send1(w, objc.Sel("orderOut:"), 0)
		hidden = snapshotEnv(a)
		objc.Send1(w, objc.Sel("orderFront:"), 0)
		back = snapshotEnv(a)
	})
	if !shown.HaveApp || !shown.Win.Valid || !shown.NSVisible || !back.NSVisible {
		t.Errorf("a shown window reads as hidden: %+v / %+v", shown, back)
	}
	if hidden.NSVisible {
		t.Error("an ordered-out window reads as visible")
	}
	if !strings.HasPrefix(shown.Line, "app{") {
		t.Errorf("no state line: %q", shown.Line)
	}
	var locked, known bool
	l.Do(func(*app.App) { locked, known = screenLocked() })
	t.Logf("screen locked: %v (known: %v)", locked, known)
}

var canary struct {
	once     sync.Once
	cls      objc.ID
	setAssoc uintptr
	mu       sync.Mutex
	freed    map[int]bool
}

// watch attaches an object to the window that records the window's
// deallocation as id.
func watch(w objc.ID, id int) {
	c := &canary
	c.once.Do(func() {
		c.freed = map[int]bool{}
		h, _ := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		c.setAssoc, _ = purego.Dlsym(h, "objc_setAssociatedObject")
		d := objc.Define("GNUIWidgettestCanary", objc.Cls("NSObject"), nil)
		d.Method(objc.Sel("dealloc"), "v@:", func(cb *ffi.CB) {
			c.mu.Lock()
			c.freed[tags[objc.ID(cb.Int(0))]] = true
			c.mu.Unlock()
		}) // the canary itself is leaked: no [super dealloc]
		c.cls = d.Register()
	})
	obj := objc.Send(objc.Send(c.cls, objc.Sel("alloc")), objc.Sel("init"))
	c.mu.Lock()
	tags[obj] = id
	c.mu.Unlock()
	key := objc.Sel("gnuiCanary")
	objc.F.Reset().Arg(uintptr(w)).Arg(uintptr(key)).Arg(uintptr(obj)).Arg(1).Call(c.setAssoc) // OBJC_ASSOCIATION_RETAIN
	objc.Release(obj)
}

var tags = map[objc.ID]int{}

func isFreed(id int) bool {
	canary.mu.Lock()
	defer canary.mu.Unlock()
	return canary.freed[id]
}

// A plain window is owned by whoever opened it: Close closes it and releases
// the reference the open took, once, so that the window goes away; a window
// that was only closed stays (its reference is the owner's to drop).
func TestPlainWindowIsReleasedOnce(t *testing.T) {
	l := StartLive(t, sessConfig(), &sess{})
	l.Frames(2)
	var kept, owned PlainWindow
	l.Do(func(a *app.App) {
		kept, owned = OpenPlainWindow("kept", 100, 100), OpenPlainWindow("owned", 140, 100)
		watch(kept.Window, 1)
		watch(owned.Window, 2)
		objc.Send(kept.Window, objc.Sel("close")) // closed, not released
		owned.Close()
		owned.Close() // again: nothing more is released (a second release would be a crash)
	})
	l.Frames(3)
	l.Do(func(a *app.App) {})
	if isFreed(1) {
		t.Error("a window that was only closed was deallocated")
	}
	if !isFreed(2) {
		t.Error("Close did not release the owned window: it was not deallocated")
	}
	l.Do(func(a *app.App) {
		if owned.Visible() {
			t.Error("a closed plain window is visible")
		}
		objc.Release(kept.Window) // the reference of the open: this test closed it by hand
	})
}

// The windows a LiveApp opens are closed and released when its run ends.
func TestLiveAppClosesItsPlainWindows(t *testing.T) {
	l := StartLive(t, sessConfig(), &sess{})
	l.Frames(2)
	var w PlainWindow
	l.Do(func(a *app.App) {
		w = l.OpenPlainWindow("mine", 100, 100)
		if !w.Visible() {
			t.Error("the plain window is not visible after opening")
		}
	})
	l.Close()
	if !w.gone() {
		t.Error("the plain window was not released when the run ended")
	}
}
