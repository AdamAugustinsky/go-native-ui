//go:build darwin

package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/internal/testlock"
	"gonativeui/platform"
)

// The user's way into full screen and out: the green button clicked from
// the run loop, not from a frame. Both transitions complete (the window's
// notifications; no full-screen style bit after the exit), the frame handler
// presents frames while each animation runs (live resize, between the
// will- and did- notifications), and nothing crashes.
//
// The macOS CI runner is slow and shared, which made this flaky:
//
//   - Its display stops delivering frames for seconds at a time (20-56 s
//     measured). The exit used to be clicked from a frame, so a stall after
//     the entry left it unclicked. Now the exit is clicked from the run
//     loop, 0.5 s after windowDidEnterFullScreen, and every deadline counts
//     frame time (gaps between frames count at most fsMaxGap), within fsWall
//     of clock for a stall that does not end.
//   - Other test binaries open windows at the same time. When another app
//     takes the front during a transition, AppKit may never finish it (no
//     did- notification): a plain AppKit app ([NSApp run], the same clicks)
//     wedged the same way on the runner, 6 times in 69 runs, always after
//     the app resigned active during the entry. An attempt whose transition
//     stalls after such interference is run again, up to fsAttempts times.
//
// It skips only on evidence that this environment cannot go full screen:
// the screen is locked, the window was never visible, or every attempt was
// interfered with. A ready, enabled button whose click does not start the
// transition fails within the existing frame/clock deadlines.
func TestGreenButtonFullScreen(t *testing.T) {
	testlock.Hold(t, "activation") // it makes the app active: one process at a time
	if testing.Short() {
		t.Skip("opens a window and goes full screen")
	}
	for attempt := 1; ; attempt++ {
		r, ok := greenButtonAttempt(t)
		if !ok {
			return
		}
		if r.stuck == 0 || !r.interfered {
			checkGreenButton(t, r)
			return
		}
		t.Logf("attempt %d: another app took the front during the transition (phase %d), and AppKit did not finish it", attempt, r.stuck)
		if attempt == fsAttempts {
			t.Skipf("in each of %d attempts another app took the front during the transition, and AppKit did not finish it", fsAttempts)
		}
	}
}

func TestGreenButtonWaitsForNativeReadiness(t *testing.T) {
	t.Setenv("GNUI_TEST_DELAY_FULLSCREEN_READY", "1")
	TestGreenButtonFullScreen(t)
}

// greenButtonAttempt runs the case in a child process; ok is false when the
// test is over (failed or skipped).
func greenButtonAttempt(t *testing.T) (r fsReport, ok bool) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), nestedChild+"=green-button")
	out, err := cmd.CombinedOutput()
	s := string(out)
	line := func(prefix string) string {
		i := strings.Index(s, prefix)
		if i < 0 {
			return ""
		}
		l := s[i:]
		if j := strings.IndexByte(l, '\n'); j >= 0 {
			l = l[:j]
		}
		return l
	}
	l := line("green-button: ")
	if err != nil || l == "" {
		if len(s) > 3000 {
			s = s[:3000]
		}
		t.Fatalf("the child failed (%v):\n%s", err, s)
	}
	t.Log(l)
	r.timeline = line("timeline: ")
	if strings.Contains(r.timeline, "clicked before native readiness") {
		t.Error(r.timeline)
	}
	if _, err := fmt.Sscanf(l, fsReportFormat, r.fields()...); err != nil {
		t.Fatalf("cannot read %q: %v", l, err)
	}
	return r, true
}

// checkGreenButton judges an attempt that was not interfered with.
func checkGreenButton(t *testing.T, r fsReport) {
	defer func() {
		if t.Failed() {
			t.Log(r.timeline)
		}
	}()
	switch {
	case r.locked:
		t.Skip("the screen is locked: AppKit does not finish a full-screen transition behind it")
	case !r.everVisible:
		t.Skip("the window was never visible (occluded from the start)")
	case !r.clicked:
		t.Fatal("the green button was not clicked: no frame came in time")
	case !r.buttonEnabled:
		t.Fatal("the window's green button is not enabled for full screen")
	case !r.started:
		t.Fatalf("AppKit never began the transition (no windowWillEnterFullScreen) in %d ms of frame time, %d ms of clock", r.frameTimeMs, r.wallMs)
	}
	if !r.entered || !r.exited || !r.windowed {
		t.Errorf("want the window into full screen and back out of it (stalled in phase %d; %d ms of frame time, %d ms of clock, longest gap between frames %d ms)", r.stuck, r.frameTimeMs, r.wallMs, r.maxGapMs)
	}
	if r.entered && r.entryHandler == 0 || r.exited && r.exitHandler == 0 {
		t.Error("want frames presented by the frame handler while each animation runs")
	}
	if r.nested != 0 {
		t.Error("a Build ran inside a Build")
	}
}

const (
	fsFrameTime = 15 * time.Second // frame time the case may take
	fsStuck     = 5 * time.Second  // frame time a transition may take (they take 0.5-1 s)
	fsAttempts  = 3
	fsMaxGap    = 100 * time.Millisecond
	fsWall      = 120 * time.Second // clock, for frames that stop for good
)

// fsReport is what the child reports, in fsReportFormat.
type fsReport struct {
	locked, everVisible, clicked, buttonEnabled, started bool
	entered, exited, windowed                            bool
	entryHandler, entryFrames, exitHandler, exitFrames   int
	nested                                               int
	frameTimeMs, wallMs, maxGapMs                        int64
	stuck                                                int  // the transition's phase (fsEntering, fsExiting) if AppKit did not finish it
	interfered                                           bool // another app took the front during it (the app resigned, or the window lost key)
	timeline                                             string
}

const fsReportFormat = "green-button: locked %t, ever visible %t, clicked %t, button enabled %t, started %t, entered %t, exited %t, windowed after exit %t, handler frames during entry %d of %d, during exit %d of %d, nested builds %d, frame time %d ms, wall %d ms, longest gap %d ms, stuck %d, interfered %t"

func (r *fsReport) fields() []any {
	return []any{&r.locked, &r.everVisible, &r.clicked, &r.buttonEnabled, &r.started, &r.entered, &r.exited, &r.windowed,
		&r.entryHandler, &r.entryFrames, &r.exitHandler, &r.exitFrames, &r.nested, &r.frameTimeMs, &r.wallMs, &r.maxGapMs, &r.stuck, &r.interfered}
}

// The full-screen phases, as the window's notifications report them.
const (
	fsWindowed = iota
	fsEntering // windowWillEnterFullScreen to windowDidEnterFullScreen
	fsFull
	fsExiting // windowWillExitFullScreen to windowDidExitFullScreen
	fsDone
	fsPhases
)

// fs is the child's state.
var fs struct {
	a             *App
	win, zoom     objc.ID
	phase         int
	at            [fsPhases]int64 // when each phase began
	handlerFrames [fsPhases]int   // frames the frame handler presented in each
	frames        [fsPhases]int   // all the frames built in each
	windowed      bool            // no NSWindowStyleMaskFullScreen at windowDidExitFullScreen
	clicked       bool
	enabled       bool
	everVisible   bool
	n             int
	t0, last      int64 // the clock at the start and at the previous frame
	frameTime     int64 // gaps between frames, each at most fsMaxGap
	maxGap        int64
	phaseFT       int64 // frameTime when the phase began
	stuck         int   // the transition that did not finish
	interfered    [fsPhases]bool

	// the timeline, printed when the case fails: phases, clicks, gaps,
	// window and app state changes, frames per second
	ev        []string
	state     string
	lastSec   int64
	lastFrame int
	lastTicks uint64
}

func runGreenButtonChild(a *App, b *nestedBuilder) {
	fs.a, fs.win = a, objc.ID(a.Win.Native().NSWindow)
	fs.zoom = objc.Send1(fs.win, objc.Sel("standardWindowButton:"), 2) // NSWindowZoomButton
	fs.t0 = platform.Now()
	// The frames the frame handler presents (live resize: AppKit's
	// animations), counted per phase.
	a.Win.SetFrameHandler(func() {
		n := a.frames
		a.frameHandler()
		fs.handlerFrames[fs.phase] += a.frames - n
	})
	observeFullScreen()
	tick := time.NewTicker(250 * time.Millisecond) // samples the state while frames stall
	defer tick.Stop()
	go func() {
		for range tick.C {
			platform.Wake()
		}
	}()
	locked := screenLocked()
	activateAt := fs.t0
	if os.Getenv("GNUI_TEST_DELAY_FULLSCREEN_READY") != "" {
		a.Win.Hide()
		activateAt += int64(2 * time.Second)
	}
	a.hook = func() {
		fsSample()
		now := platform.Now()
		if activateAt != 0 && now >= activateAt {
			objc.Send1(objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication")), objc.Sel("activateIgnoringOtherApps:"), 1)
			a.Win.Activate()
			activateAt = 0
		}
		switch {
		case locked:
			fsLog("the screen is locked")
		case fs.phase == fsDone && now-fs.at[fsDone] > 300e6:
		case (fs.phase == fsEntering || fs.phase == fsExiting) && fs.frameTime-fs.phaseFT > int64(fsStuck):
			fs.stuck = fs.phase
			fsLog("the transition did not finish")
		case fs.frameTime > int64(fsFrameTime):
			fsLog("out of frame time")
		case now-fs.t0 > int64(fsWall):
			fsLog("out of clock")
		default:
			return
		}
		a.done = true
	}
	if err := a.loop(); err != nil {
		fmt.Println("loop:", err)
	}
	fmt.Printf(fsReportFormat+"\n", screenLocked(), fs.everVisible, fs.clicked, fs.enabled, fs.at[fsEntering] != 0,
		fs.at[fsFull] != 0, fs.at[fsDone] != 0, fs.windowed,
		fs.handlerFrames[fsEntering], fs.frames[fsEntering], fs.handlerFrames[fsExiting], fs.frames[fsExiting],
		b.nested, fs.frameTime/1e6, (platform.Now()-fs.t0)/1e6, fs.maxGap/1e6, fs.stuck, fs.interfered[fs.stuck])
	fmt.Println("timeline: " + strings.Join(fs.ev, "; "))
}

// greenFrame counts frame time. Three frames do not imply that AppKit has
// made the window visible, active and key; wait for that before clicking.
func greenFrame(a *App, win objc.ID) {
	now := platform.Now()
	if fs.last != 0 {
		d := now - fs.last
		fs.frameTime += min(d, int64(fsMaxGap))
		fs.maxGap = max(fs.maxGap, d)
		if d > 300e6 {
			fsLog("gap %d ms", d/1e6)
		}
	}
	fs.last = now
	fs.frames[fs.phase]++
	if fs.n++; fs.n >= 3 && !fs.clicked {
		s := a.Win.DebugState()
		if !s.Visible || !s.AppActive || !s.Key {
			return
		}
		fs.enabled = objc.Send(fs.zoom, objc.Sel("isEnabled")) != 0
		fsClick(0)
		fs.clicked = true
	}
}

// fsClick clicks the green button from the run loop, after delay seconds,
// as a user does: the app comes to the front, and performClick: runs in a
// run-loop turn of its own, not in a frame.
func fsClick(delay float64) {
	if fs.phase == fsWindowed {
		s := fs.a.Win.DebugState()
		if !s.Visible || !s.AppActive || !s.Key {
			fsLog("clicked before native readiness: visible %v active %v key %v", s.Visible, s.AppActive, s.Key)
		}
	}
	fsLog("click after %.1f s", delay)
	app := objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication"))
	objc.Send1(app, objc.Sel("activateIgnoringOtherApps:"), 1)
	sel, click := objc.Sel("performSelector:withObject:afterDelay:"), objc.Sel("performClick:")
	objc.F.Reset().Arg(uintptr(fs.zoom)).Arg(uintptr(sel)).Arg(uintptr(click)).Arg(0).ArgF64(delay).Call(objc.MsgSend)
}

// observeFullScreen follows the window's four full-screen notifications,
// and the app's activation (for the timeline).
func observeFullScreen() {
	d := objc.Define("GNUITestFullScreenObserver", objc.Cls("NSObject"), nil)
	notes := [...]struct {
		sel, name string
		phase     int
		window    bool
	}{
		{"gnuiWillEnter:", "NSWindowWillEnterFullScreenNotification", fsEntering, true},
		{"gnuiDidEnter:", "NSWindowDidEnterFullScreenNotification", fsFull, true},
		{"gnuiWillExit:", "NSWindowWillExitFullScreenNotification", fsExiting, true},
		{"gnuiDidExit:", "NSWindowDidExitFullScreenNotification", fsDone, true},
		{"gnuiActive:", "NSApplicationDidBecomeActiveNotification", -1, false},
		{"gnuiResign:", "NSApplicationDidResignActiveNotification", -2, false},
	}
	for _, n := range notes {
		d.Method(objc.Sel(n.sel), "v@:@", func(*ffi.CB) { fullScreenNote(n.phase) })
	}
	cls := d.Register()
	obs := objc.Send(objc.Send(cls, objc.Sel("alloc")), objc.Sel("init"))
	center := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	add := objc.Sel("addObserver:selector:name:object:")
	for _, n := range notes {
		sel, name := objc.Sel(n.sel), objc.Str(n.name)
		var obj objc.ID
		if n.window {
			obj = fs.win
		}
		objc.F.Reset().Arg(uintptr(center)).Arg(uintptr(add)).Arg(uintptr(obs)).Arg(uintptr(sel)).Arg(uintptr(name)).Arg(uintptr(obj)).Call(objc.MsgSend)
	}
}

func fullScreenNote(p int) {
	switch p {
	case -1:
		fsLog("app active")
		return
	case -2:
		fsLog("app resigned")
		fs.interfered[fs.phase] = true
		return
	}
	fsLog("phase %d", p)
	fs.phase, fs.at[p], fs.phaseFT = p, platform.Now(), fs.frameTime
	switch p {
	case fsFull:
		fsClick(0.5) // out again, once the window is in full screen
	case fsDone:
		fs.windowed = objc.Send(fs.win, objc.Sel("styleMask"))&(1<<14) == 0 // NSWindowStyleMaskFullScreen
	}
}

func fsLog(format string, args ...any) {
	fs.ev = append(fs.ev, fmt.Sprintf("%.2fs ", float64(platform.Now()-fs.t0)/1e9)+fmt.Sprintf(format, args...))
}

// fsSample logs the window's state when it changes, and the frames and
// display-link ticks of each second.
func fsSample() {
	a := fs.a
	st := a.Win.DebugState()
	fs.everVisible = fs.everVisible || st.Visible
	if st.AppActive && !st.Key && fs.phase != fsWindowed {
		fs.interfered[fs.phase] = true // another app's window took key from ours
	}
	k := fmt.Sprintf("vis=%t occ=%d key=%t active=%t paused=%t/%t live=%t", st.Visible, st.Occlusion, st.Key, st.AppActive, st.LinkPaused, st.LinkPausedOS, st.Live)
	if k != fs.state {
		fs.state = k
		fsLog("state %s", k)
	}
	if now := platform.Now(); now-fs.lastSec >= 1e9 {
		fsLog("+%d frames +%d ticks", a.frames-fs.lastFrame, st.Ticks-fs.lastTicks)
		fs.lastSec, fs.lastFrame, fs.lastTicks = now, a.frames, st.Ticks
	}
}

// screenLocked reports whether the login session's screen is locked
// (CGSessionCopyCurrentDictionary's CGSSessionScreenIsLocked).
func screenLocked() bool {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return false
	}
	copyDict, err := purego.Dlsym(cg, "CGSessionCopyCurrentDictionary")
	if err != nil {
		return false
	}
	d := objc.ID(objc.F.Call0(copyDict))
	if d == 0 {
		return false
	}
	defer objc.Release(d)
	v := objc.Send1(d, objc.Sel("objectForKey:"), uintptr(objc.Str("CGSSessionScreenIsLocked")))
	return v != 0 && objc.Send(v, objc.Sel("boolValue")) != 0
}
