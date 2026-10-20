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
//     of frames, so a display that stalls for half a minute delays the test
//     and does not fail it. A frame is asked for as long as a wait is open, so
//     an idle app draws a frame only for a test that is waiting.
//   - Close ends the run.
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
	t   testing.TB
	sb  *sessionBuilder
	end chan struct{} // closed when the app's run has returned
	err error         // its error, set before end is closed

	closeOnce sync.Once
}

// sessionBuilder wraps the app's builder: it counts frames, runs the
// waiters after each, and keeps a frame coming while one is open.
type sessionBuilder struct {
	app.Builder
	a     *app.App
	built atomic.Int64
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
	b.a = a
	initDo()
	if in, ok := b.Builder.(app.Initer); ok {
		return in.Init(a)
	}
	return nil
}

func (b *sessionBuilder) Build(c *ui.Context) {
	b.Builder.Build(c)
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
	if !liveActive.CompareAndSwap(false, true) {
		t.Fatal("widgettest: a LiveApp is already running (the platform has one window)")
	}
	l := &LiveApp{t: t, end: make(chan struct{}), sb: &sessionBuilder{Builder: b, first: make(chan struct{})}}
	go func() {
		defer close(l.end)
		OnMain(func() { _, l.err = app.RunWith(cfg, l.sb, app.RunOptions{}) })
	}()
	select {
	case <-l.sb.first:
	case <-l.end:
		liveActive.Store(false)
		if l.err == nil {
			l.err = fmt.Errorf("the window closed before its first frame")
		}
		if os.Getenv("CI") == "" {
			t.Skipf("no window to open: %v", l.err)
		}
		t.Fatalf("opening the window on CI: %v", l.err)
	}
	t.Cleanup(l.Close)
	return l
}

// App is the running app. Use it only inside Do (it belongs to the main thread).
func (l *LiveApp) App() *app.App { return l.sb.a }

// Built is the number of frames the app has built.
func (l *LiveApp) Built() int { return int(l.sb.built.Load()) }

// Close ends the run: the app quits and the window closes. It is called when
// the test ends; calling it earlier is fine.
func (l *LiveApp) Close() {
	l.closeOnce.Do(func() {
		select {
		case <-l.end:
		default:
			l.doQuiet(func(a *app.App) {
				a.Quit()
				platform.Wake()
			})
			select {
			case <-l.end:
			case <-time.After(30 * time.Second):
				l.t.Error("widgettest: the app did not end 30 s after Quit")
			}
		}
		liveActive.Store(false)
	})
}

// Do runs f on the main thread, between two frames, and waits until it has
// run. f may call AppKit (every helper of this package that takes an
// objc.ID) and may read and change the app; it runs inside an autorelease
// pool of its own, so what it creates is gone when it returns. f must not
// call t.Fatal or t.Skip, or Do or Until (it holds the main thread). If the
// app ended first, the test fails.
func (l *LiveApp) Do(f func(a *app.App)) {
	l.t.Helper()
	if pv := l.doQuiet(f); pv != nil {
		l.t.Fatalf("widgettest: panic in Do: %v", pv)
	}
}

// doQuiet is Do that returns the panic value instead of failing.
func (l *LiveApp) doQuiet(f func(a *app.App)) (panicked any) {
	done := make(chan any, 1)
	doMu.Lock()
	doQ = append(doQ, func() {
		var pv any
		func() {
			defer func() { pv = recover() }()
			withPool(func() { f(l.sb.a) })
		}()
		platform.Wake() // the loop turns once more: it flushes what f asked for (a dialog) and draws what f changed
		done <- pv
	})
	doMu.Unlock()
	postDo()
	select {
	case pv := <-done:
		return pv
	case <-l.end:
		// the closure may have run just before the end
		select {
		case pv := <-done:
			return pv
		default:
		}
		l.t.Helper()
		l.t.Fatalf("widgettest: the app ended before a call reached the main thread (%v)", l.err)
		return nil
	}
}

// backstop is how long a wait lasts in wall time with no frame at all before
// the test gives up on the display.
const backstop = 90 * time.Second

// Until waits, in frame time, until cond is true. cond is evaluated on the
// main thread after each frame the app builds (so it may read AppKit and the
// app, and must not block or call Do), and the wait fails the test after
// frames more frames. The deadline is in frames because that is what a stalled
// display stops: a runner whose display sleeps for half a minute delays the
// wait and does not end it. If not one frame is drawn in 90 s of wall time the
// test is skipped: that is the evidence that this environment cannot do it.
// what names the wait in the failure.
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
	start := l.sb.built.Load()
	w.deadline = start + int64(frames)
	l.sb.waits = append(l.sb.waits, w)
	l.sb.mu.Unlock()
	platform.Wake() // a frame, so that an idle app evaluates the condition
	timer := time.NewTimer(backstop)
	defer timer.Stop()
	for {
		select {
		case ok := <-w.done:
			return ok
		case <-l.end:
			l.t.Fatalf("widgettest: the app ended while waiting for %s (%v)", what, l.err)
		case <-timer.C:
			if l.sb.built.Load() == start {
				l.t.Skipf("widgettest: no frame was drawn in %v while waiting for %s: the display is stalled or asleep", backstop, what)
			}
			timer.Reset(backstop) // frames are coming; the frame deadline decides
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
