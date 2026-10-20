//go:build darwin

package widgettest

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
	"gonativeui/ui"
)

// LiveApp is an application running in a real window while a test drives it
// from its own goroutine: the app's loop owns the main thread (RunMain), the
// test goroutine owns the test, and the two meet in three places.
//
//   - Do runs a function on the main thread between two frames, where AppKit
//     may be called: post events, read window state, answer a sheet.
//   - Until and Frames wait in frame time: the condition is evaluated on the
//     main thread after each frame the app builds, and a deadline is a number
//     of frames, so a display that is slow delays the test and does not fail
//     it. A frame is asked for as long as a wait is open, so an idle app draws
//     a frame only for a test that is waiting.
//   - Close ends the run.
//
// Every hand-off is bounded in wall time (LiveOptions): a window that never
// opens, a callback that never returns, a main thread that never answers and
// a run that never ends each end the test with a failure that says which, and
// never with a hang. A wait that gets no frame for a while looks at the
// environment (see stall.go): frames that do not come because the screen is
// locked, the window is covered or hidden, or the display link is dead while
// the run loop is alive skip the test and say why; frames that do not come
// with no such evidence fail it, on CI as well as locally, with the app's
// state line.
//
// There is one LiveApp at a time: the platform has one window until F4
// (more than one window of the framework's own). "Several windows" today
// means the other windows of the process (OpenPlainWindow), which is what
// the app-level flows need: a window other than ours takes the key, keeps
// the application alive, and counts for the Dock's reopen.
//
//	func TestMain(m *testing.M) { widgettest.RunMain(m) }
//
//	l := widgettest.StartLive(t, cfg, builder)
//	l.Do(func(a *app.App) { widgettest.NativeWindow(a).Click(40, 40, 0) })
//	l.Until("the button was pressed", 120, func() bool { return pressed })
type LiveApp struct {
	t    testing.TB
	sb   *sessionBuilder
	lim  LiveOptions
	end  chan struct{} // closed when the app's run has returned
	err  error         // its error, set before end is closed
	dead atomic.Bool   // a hand-off timed out: the main thread cannot be relied on

	closeOnce sync.Once
	plains    []PlainWindow // windows opened with OpenPlainWindow, closed with the run
}

// LiveOptions are the wall-time limits of a LiveApp's hand-offs. A zero field
// is its default.
type LiveOptions struct {
	// Start is how long the window may take to draw its first frame (90 s).
	Start time.Duration
	// Call is how long a function handed to the main thread may take to start
	// and, once started, to return (30 s).
	Call time.Duration
	// Close is how long the run may take to end after Quit (30 s).
	Close time.Duration
	// Stall is how long a wait may see no new frame before it looks at the
	// environment (60 s). The wait goes on while frames come, however slowly.
	Stall time.Duration
	// Wait is the cap on one wait in wall time, whatever the frames do (10 min).
	Wait time.Duration

	snap func(*app.App) envSnapshot // test seam: what a look at the environment reads (snapshotEnv)
}

func (o LiveOptions) withDefaults() LiveOptions {
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&o.Start, 90*time.Second)
	set(&o.Call, 30*time.Second)
	set(&o.Close, 30*time.Second)
	set(&o.Stall, 60*time.Second)
	set(&o.Wait, 10*time.Minute)
	return o
}

// sessionBuilder wraps the app's builder: it counts frames, runs the
// waiters after each, and keeps a frame coming while one is open.
type sessionBuilder struct {
	app.Builder
	ap    atomic.Pointer[app.App]
	built atomic.Int64
	hold  atomic.Bool   // test seam: frames are built but not counted, as if none were delivered
	first chan struct{} // closed after the first frame

	mu    sync.Mutex
	waits []*waiter
}

// waiter is an open Until.
type waiter struct {
	cond     func() bool
	deadline int64 // frame number past which the wait failed
	done     chan bool
}

func (b *sessionBuilder) Init(a *app.App) error {
	b.ap.Store(a)
	initDo()
	if in, ok := b.Builder.(app.Initer); ok {
		return in.Init(a)
	}
	return nil
}

func (b *sessionBuilder) Build(c *ui.Context) {
	b.Builder.Build(c)
	if b.hold.Load() {
		c.RequestFrame()
		return
	}
	n := b.built.Add(1)
	if n == 1 {
		close(b.first)
	}
	b.mu.Lock()
	keep := b.waits[:0]
	for _, w := range b.waits {
		switch {
		case w.cond():
			w.done <- true
		case n >= w.deadline:
			w.done <- false
		default:
			keep = append(keep, w)
		}
	}
	for i := len(keep); i < len(b.waits); i++ {
		b.waits[i] = nil
	}
	b.waits = keep
	open := len(keep) > 0
	b.mu.Unlock()
	if open {
		c.RequestFrame()
	}
}

var liveActive atomic.Bool

// StartLive opens the real window for cfg with the builder in it and returns
// once its first frame is drawn, the app's loop running on the main thread. It
// needs RunMain. The run is closed when the test ends. Where no window can be
// opened the test is skipped, but not in CI, where the runners have a display.
func StartLive(t testing.TB, cfg app.Config, b app.Builder) *LiveApp {
	t.Helper()
	return StartLiveWith(t, cfg, b, LiveOptions{})
}

// StartLiveWith is StartLive with the wall-time limits of the hand-offs set.
func StartLiveWith(t testing.TB, cfg app.Config, b app.Builder, o LiveOptions) *LiveApp {
	t.Helper()
	if !liveActive.CompareAndSwap(false, true) {
		t.Fatal("widgettest: a LiveApp is already running (the platform has one window), or an earlier one could not be closed")
	}
	l := &LiveApp{t: t, lim: o.withDefaults(), end: make(chan struct{}), sb: &sessionBuilder{Builder: b, first: make(chan struct{})}}
	go func() {
		defer close(l.end)
		OnMain(func() {
			platform.DebugAssumeKey(true) // the input posted to the window is a key window's, activated or not
			_, l.err = app.RunWith(cfg, l.sb, app.RunOptions{})
		})
	}()
	t.Cleanup(l.Close) // before the wait: a start that fails still ends the run
	timer := time.NewTimer(l.lim.Start)
	defer timer.Stop()
	select {
	case <-l.sb.first:
	case <-l.end:
		if l.err == nil {
			l.err = fmt.Errorf("the window closed before its first frame")
		}
		if os.Getenv("CI") == "" {
			t.Skipf("no window to open: %v", l.err)
		}
		t.Fatalf("opening the window on CI: %v", l.err)
	case <-timer.C:
		l.startTimeout()
	}
	return l
}

// startTimeout handles a start that drew no frame within the limit: the same
// look at the environment as a stalled wait, which skips or fails the test and
// says whether the main thread was still answering.
func (l *LiveApp) startTimeout() {
	l.t.Helper()
	l.stall("the first frame", l.lim.Start) // ends the test unless the frame came while it looked
}

// App is the running app. Use it only inside Do (it belongs to the main thread).
func (l *LiveApp) App() *app.App { return l.sb.ap.Load() }

// Built is the number of frames the app has built.
func (l *LiveApp) Built() int { return int(l.sb.built.Load()) }

// Close ends the run: the app quits and the window closes. It is called when
// the test ends; calling it earlier is fine. Each step is bounded: if the main
// thread does not take the quit within the limit, or the run does not end
// within the limit after it, the test fails and the LiveApp stays active (the
// platform is not usable by another).
func (l *LiveApp) Close() {
	l.closeOnce.Do(func() {
		select {
		case <-l.end:
			liveActive.Store(false)
			return
		default:
		}
		res := l.call(func(a *app.App) {
			for _, p := range l.plains {
				p.Close()
			}
			l.plains = nil
			a.Quit()
			platform.Wake()
		}, l.lim.Call)
		switch {
		case res.ended:
			liveActive.Store(false)
			return
		case res.timeout != "":
			l.t.Errorf("widgettest: closing: %s", res.timeout)
			return
		}
		timer := time.NewTimer(l.lim.Close)
		defer timer.Stop()
		select {
		case <-l.end:
			liveActive.Store(false)
		case <-timer.C:
			l.t.Errorf("widgettest: the app did not end %v after Quit", l.lim.Close)
		}
	})
}

// OpenPlainWindow opens another window of the process (see the package
// function) that the LiveApp closes, and releases, when it ends. Call it
// inside Do.
func (l *LiveApp) OpenPlainWindow(title string, x, y float64) PlainWindow {
	p := OpenPlainWindow(title, x, y)
	l.plains = append(l.plains, p)
	return p
}

// Do runs f on the main thread, between two frames, and waits until it has
// run. f may call AppKit (every helper of this package that takes an
// objc.ID) and may read and change the app; it runs inside an autorelease
// pool of its own, so what it creates is gone when it returns. f runs on the
// main thread, not on the test's goroutine: it must not call t.Fatal, t.Skip
// or runtime.Goexit, or Do or Until (it holds the main thread). If it does end
// its goroutine, the main thread's side of the call is still completed and
// the test fails here, naming the mistake; read what f saw into variables and
// report on the test goroutine. A call the main thread never takes up, or one
// that never returns, fails the test after LiveOptions.Call. If the app
// ended first, the test fails.
func (l *LiveApp) Do(f func(a *app.App)) {
	l.t.Helper()
	res := l.call(f, l.lim.Call)
	switch {
	case res.ended:
		l.t.Fatalf("widgettest: the app ended before a call reached the main thread (%v)", l.err)
	case res.timeout != "":
		l.t.Fatalf("widgettest: Do: %s", res.timeout)
	case res.goexit:
		l.t.Fatalf("widgettest: the function given to Do ended its goroutine (t.Fatal, t.Skip or runtime.Goexit called inside Do): it runs on the main thread; return from it and report on the test goroutine")
	case res.panicked != nil:
		l.t.Fatalf("widgettest: panic in Do: %v", res.panicked)
	}
}

// mainCall is a function queued for the main thread.
type mainCall struct {
	f         func(a *app.App)
	started   atomic.Bool
	abandoned atomic.Bool // the caller gave up: the main thread must not start it
	done      chan callResult
}

// callResult is how a call ended.
type callResult struct {
	panicked any
	goexit   bool   // f ended its goroutine (t.Fatal, t.Skip, runtime.Goexit)
	ended    bool   // the app ended first
	timeout  string // set when the main thread did not take it up or finish it in time: what happened
}

// call is Do that reports instead of failing the test. The completion is
// always sent: by the main thread when f returns, panics or ends its
// goroutine, and by the caller's timeout when the main thread does not.
func (l *LiveApp) call(f func(a *app.App), limit time.Duration) (res callResult) {
	c := &mainCall{f: f, done: make(chan callResult, 1)}
	if l.dead.Load() { // an earlier call timed out: do not queue behind it
		return callResult{timeout: "the main thread did not answer an earlier call; it is blocked"}
	}
	doMu.Lock()
	doQ = append(doQ, func() {
		if c.abandoned.Load() {
			return
		}
		c.started.Store(true)
		normal := false
		defer func() {
			r := recover()
			platform.Wake() // the loop turns once more: it flushes what f asked for (a dialog) and draws what f changed
			c.done <- callResult{panicked: r, goexit: !normal && r == nil}
		}()
		withPool(func() { f(l.sb.ap.Load()) })
		normal = true
	})
	doMu.Unlock()
	postDo()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case res = <-c.done:
		return res
	case <-l.end:
		select { // the closure may have run just before the end
		case res = <-c.done:
			return res
		default:
		}
		return callResult{ended: true}
	case <-timer.C:
		select {
		case res = <-c.done:
			return res
		default:
		}
		c.abandoned.Store(true)
		l.dead.Store(true)
		if c.started.Load() {
			return callResult{timeout: fmt.Sprintf("the function was still running on the main thread %v after it started: it is blocked (waiting on the test goroutine, a lock, or AppKit)", limit)}
		}
		return callResult{timeout: fmt.Sprintf("the main thread did not take up the call within %v: it is busy or blocked in an earlier callback", limit)}
	}
}

// Until waits, in frame time, until cond is true. cond is evaluated on the
// main thread after each frame the app builds (so it may read AppKit and the
// app, and must not block or call Do), and the wait fails the test after
// frames more frames. The deadline is in frames because that is what a slow
// display stretches: it delays the wait and does not end it. If no frame at
// all is drawn for LiveOptions.Stall, the wait looks at the environment: the
// test is skipped, with the reason, when it proves that no frame can be
// drawn (a locked screen, a hidden or covered window, a display link that
// does not tick while the run loop is alive), and fails, with the app's
// state line, when it does not. what names the wait in the failure.
func (l *LiveApp) Until(what string, frames int, cond func() bool) {
	l.t.Helper()
	if !l.wait(what, frames, cond) {
		l.t.Fatalf("widgettest: %s: not true within %d frames", what, frames)
	}
}

// Within is Until that reports the result instead of failing the test: it
// returns whether cond became true within frames frames.
func (l *LiveApp) Within(what string, frames int, cond func() bool) bool {
	l.t.Helper()
	return l.wait(what, frames, cond)
}

func (l *LiveApp) wait(what string, frames int, cond func() bool) bool {
	l.t.Helper()
	w := &waiter{cond: cond, done: make(chan bool, 1)}
	l.sb.mu.Lock()
	last := l.sb.built.Load() // the frame count at the last look: progress is a change from it
	w.deadline = last + int64(frames)
	l.sb.waits = append(l.sb.waits, w)
	l.sb.mu.Unlock()
	platform.Wake() // a frame, so that an idle app evaluates the condition
	stall := time.NewTimer(l.lim.Stall)
	defer stall.Stop()
	total := time.NewTimer(l.lim.Wait)
	defer total.Stop()
	for {
		select {
		case ok := <-w.done:
			return ok
		case <-l.end:
			l.t.Fatalf("widgettest: the app ended while waiting for %s (%v)", what, l.err)
		case <-total.C:
			l.diagnose(what, l.lim.Wait) // diagnostics only: frames did come, the wait just does not settle
			l.t.Fatalf("widgettest: %s: not settled within %v of wall time (%d frames built)", what, l.lim.Wait, l.sb.built.Load())
		case <-stall.C:
			if cur := l.sb.built.Load(); cur != last {
				last = cur // frames are coming; the frame deadline decides
			} else if l.stall(what, l.lim.Stall) {
				last = l.sb.built.Load() // the frames came back while it looked
			}
			stall.Reset(l.lim.Stall)
		}
	}
}

// Frames waits for n more frames to be built.
func (l *LiveApp) Frames(n int) {
	l.t.Helper()
	target := l.sb.built.Load() + int64(n)
	l.Until(fmt.Sprintf("%d frames", n), n+1, func() bool { return l.sb.built.Load() >= target })
}

// ---- the main thread's mailbox ----

var (
	doMu      sync.Mutex
	doQ       []func()
	doOnce    sync.Once
	doTarget  objc.ID
	doPerform objc.SEL
	doSel     objc.SEL
	postFrame = new(ffi.Frame)
	postMu    sync.Mutex
)

// initDo defines the class whose method runs the next function of the queue,
// and makes its one instance. Main thread, from Init.
func initDo() {
	doOnce.Do(func() {
		d := objc.Define("GNUIWidgettestDo", objc.Cls("NSObject"), nil)
		doSel = objc.Sel("gnuiDo:")
		doPerform = objc.Sel("performSelectorOnMainThread:withObject:waitUntilDone:")
		d.Method(doSel, "v@:@", func(*ffi.CB) {
			doMu.Lock()
			var f func()
			if len(doQ) > 0 {
				f = doQ[0]
				doQ = doQ[:copy(doQ, doQ[1:])]
			}
			doMu.Unlock()
			if f != nil {
				f()
			}
		})
		doTarget = objc.Send(objc.Send(d.Register(), objc.Sel("alloc")), objc.Sel("init"))
	})
}

// postDo asks the main thread's run loop to run the next queued function:
// performSelectorOnMainThread:withObject:waitUntilDone: in the common modes,
// which wakes the loop the app is waiting in and runs between its events,
// outside any frame. It is callable from any goroutine (a frame of its own,
// like platform's wake).
func postDo() {
	runtime.LockOSThread()
	postMu.Lock()
	postFrame.Reset().Arg(uintptr(doTarget)).Arg(uintptr(doPerform)).Arg(uintptr(doSel)).Arg(0).ArgBool(false).Call(objc.MsgSend)
	postMu.Unlock()
	runtime.UnlockOSThread()
}

// WindowNumber is the window server's number of the app's window (GnuiShot's
// -id); a LiveApp is a Windowed, so Capture takes it.
func (l *LiveApp) WindowNumber() int {
	var n int
	l.Do(func(a *app.App) { n = NativeWindow(a).Number() })
	return n
}
