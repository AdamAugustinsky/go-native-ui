//go:build !gnui_release

package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gonativeui/internal/inspectwire"
	"gonativeui/ui"
)

func TestParseKey(t *testing.T) {
	p := ui.PrimaryMod()
	for _, tc := range []struct {
		in   string
		code ui.KeyCode
		mods ui.Mods
	}{
		{"enter", ui.KeyEnter, 0}, {"Return", ui.KeyEnter, 0}, {"ESC", ui.KeyEscape, 0}, {"a", 'A', 0}, {"Z", 'Z', 0}, {"7", '7', 0},
		{"cmd+shift+n", 'N', ui.ModSuper | ui.ModShift}, {"ctrl+alt+delete", ui.KeyDelete, ui.ModCtrl | ui.ModAlt},
		{"primary+z", 'Z', p}, {"option+left", ui.KeyLeft, ui.ModAlt}, {"f1", ui.KeyF1, 0}, {"shift+F20", ui.KeyF20, ui.ModShift},
		{"cmd+,", ',', ui.ModSuper}, {"pagedown", ui.KeyPageDown, 0}, {" tab ", ui.KeyTab, 0},
	} {
		code, mods, err := parseKey(tc.in)
		if err != nil || code != tc.code || mods != tc.mods {
			t.Errorf("parseKey(%q) = %d, %d, %v; want %d, %d", tc.in, code, mods, err, tc.code, tc.mods)
		}
	}
	for _, in := range []string{"", "banana", "cmd+", "+a", "hyper+a", "f0", "f21", "f01", "ab", "cmd+cmd2"} {
		if code, mods, err := parseKey(in); err == nil {
			t.Errorf("parseKey(%q) = %d, %d, nil", in, code, mods)
		}
	}
}

// shortTemp is a directory of the user's with a path short enough for a socket.
func shortTemp(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "gi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func newBridgeOffscreen(t *testing.T) (*Offscreen, *inspector) {
	t.Helper()
	o := newOverlayRig(t, &scene{}, 200, 150, 1)
	for i := 0; i < 3; i++ {
		o.Frame()
	}
	in, err := o.App.startInspector(filepath.Join(shortTemp(t), "s"))
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skipf("no agent bridge here: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(in.stop)
	return o, in
}

// A second do while one is playing is refused; a request nobody serves times
// out with a message that says why; a window that closes under a do fails it.
func TestBridgeBusyTimeoutAndClosedWindow(t *testing.T) {
	old := inspectGrace
	inspectGrace = 50 * time.Millisecond
	defer func() { inspectGrace = old }()
	o, in := newBridgeOffscreen(t)

	wait := inspectwire.Step{Action: "wait_for", Role: "Button", Name: "never", Ms: 600000}
	first := make(chan *inspectwire.Response, 1)
	go func() {
		first <- in.Handle(&inspectwire.Request{ID: "1", Op: "do", Step: wait, TimeoutMs: 5000})
	}()
	for i := 0; i < 1000 && o.App.script == nil; i++ { // serve until the first do is playing
		o.PumpBridge()
		time.Sleep(time.Millisecond)
	}
	if o.App.script == nil {
		t.Fatal("the do did not start")
	}
	done := make(chan *inspectwire.Response, 1)
	go func() {
		done <- in.Handle(&inspectwire.Request{ID: "2", Op: "do", Step: inspectwire.Step{Action: "settle"}})
	}()
	var busy *inspectwire.Response
	for i := 0; busy == nil && i < 2000; i++ {
		o.PumpBridge()
		select {
		case busy = <-done:
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if busy == nil || busy.OK || busy.Error.Code != inspectwire.ErrBusy {
		t.Fatalf("a do on a window that is playing one: %+v", busy)
	}
	// the window closes: the playing do fails, it does not hang
	o.App.UI = nil
	o.App.wins = nil
	o.PumpBridge()
	select {
	case r := <-first:
		if r.OK || r.Error.Code != inspectwire.ErrNoWindow || !strings.Contains(r.Error.Message, "closed") {
			t.Errorf("a do whose window closed: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the do hung after its window closed")
	}
	if o.App.script != nil {
		t.Error("the stepper stayed on the window")
	}
}

func TestBridgeRequestTimesOutWhenNobodyServes(t *testing.T) {
	old := inspectGrace
	inspectGrace = 20 * time.Millisecond
	defer func() { inspectGrace = old }()
	_, in := newBridgeOffscreen(t)
	start := time.Now()
	r := in.Handle(&inspectwire.Request{ID: "9", Op: "do", Step: inspectwire.Step{Action: "settle"}, TimeoutMs: 100})
	if r.OK || r.Error.Code != inspectwire.ErrTimeout || !strings.Contains(r.Error.Message, "frames") {
		t.Fatalf("%+v", r)
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 3*time.Second {
		t.Errorf("answered after %v", d)
	}
	// the abandoned job is skipped when the main thread comes to it, not run
	in.root.safePoint()
	if in.root.script != nil {
		t.Error("a request nobody waits for was started")
	}
}

func TestBridgeStopFailsWaitingRequestsAndRemovesTheSocket(t *testing.T) {
	_, in := newBridgeOffscreen(t)
	got := make(chan *inspectwire.Response, 1)
	go func() { got <- in.Handle(&inspectwire.Request{Op: "windows"}) }()
	time.Sleep(50 * time.Millisecond)
	in.stop()
	select {
	case r := <-got:
		if r.OK || r.Error.Code != inspectwire.ErrShuttingDown {
			t.Errorf("%+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a request still waits after stop")
	}
	if r := in.Handle(&inspectwire.Request{Op: "windows"}); r.OK || r.Error.Code != inspectwire.ErrShuttingDown {
		t.Errorf("after stop: %+v", r)
	}
}

// Without GNUI_INSPECT (or with it empty) the bridge is nothing: no socket, no
// goroutine, no hook at the safe point.
func TestBridgeUnsetCostsNothing(t *testing.T) {
	t.Setenv("GNUI_INSPECT", "")
	o := newOverlayRig(t, &scene{}, 100, 100, 1)
	before := runtime.NumGoroutine()
	stop := o.App.inspectStart()
	if o.App.inspDrain != nil {
		t.Error("the bridge is hooked into the safe point")
	}
	if n := runtime.NumGoroutine(); n != before {
		t.Errorf("%d goroutines, was %d", n, before)
	}
	stop()
	o.PumpBridge() // a safe point with no bridge does nothing
}

// serveUntil pumps the bridge and draws frames until done reports true.
func serveUntil(t *testing.T, o *Offscreen, what string, done func() bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if done() {
			return
		}
		o.PumpBridge()
		o.Frame()
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

// After the steps, whatever a last step left held is let go: the modifiers of
// a final shift+left or cmd+shift+n are not still down. (The player is not
// called again once its steps are done, so its own reset of them never ran.)
func TestBridgeReleasesTheModifiersOfAFinalKeyStep(t *testing.T) {
	o, in := newBridgeOffscreen(t)
	mods := ui.ModSuper | ui.ModShift
	o.App.tr.in.Mods = ui.ModAlt // what the system holds when the steps begin
	got := make(chan *inspectwire.Response, 1)
	go func() {
		got <- in.Handle(&inspectwire.Request{Op: "do", Steps: []inspectwire.Step{{Action: "key", Key: "cmd+shift+n"}}})
	}()
	var resp *inspectwire.Response
	serveUntil(t, o, "the answer", func() bool {
		select {
		case resp = <-got:
		default:
		}
		return resp != nil
	})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	o.Frame()
	if m := o.App.tr.in.Mods; m != ui.ModAlt {
		t.Errorf("after the steps the modifiers are %v, want what was held before them (%v; the key had %v)", m, ui.ModAlt, mods)
	}
}

// A click interrupted between its press and its release leaves the button
// down; the steps let it go however they end: a timeout, the bridge stopping.
func TestBridgeCancelsAClickThatIsInterrupted(t *testing.T) {
	for _, how := range []string{"timeout", "stop", "cancelled by the client"} {
		t.Run(how, func(t *testing.T) {
			o, in := newBridgeOffscreen(t)
			got := make(chan *inspectwire.Response, 1)
			go func() {
				got <- in.Handle(&inspectwire.Request{Op: "do", TimeoutMs: 60, Steps: []inspectwire.Step{
					{Action: "click", Role: "Button", Name: "Save"}, {Action: "settle"},
				}})
			}()
			ai := &o.App.tr.in
			serveUntil(t, o, "the press", func() bool { return ai.Down[ui.ButtonLeft] })
			switch how {
			case "timeout":
				time.Sleep(80 * time.Millisecond)
				o.PumpBridge()
				o.Frame()
			case "stop":
				in.stop()
			case "cancelled by the client":
				// the handler gives up (its own wait ran out): the next frame ends the steps
				for _, s := range in.active {
					s.job.cancelled.Store(true)
				}
				o.Frame()
			}
			if ai.Down[ui.ButtonLeft] {
				t.Errorf("the button is still down after the steps ended (%s)", how)
			}
			if o.App.script != nil {
				t.Errorf("the steps are still on the window")
			}
			select {
			case r := <-got:
				if r.OK {
					t.Errorf("an interrupted do answered ok")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no answer")
			}
		})
	}
	// input that came from elsewhere is not undone: a button the system has let go already
	o, in := newBridgeOffscreen(t)
	go in.Handle(&inspectwire.Request{Op: "do", Step: inspectwire.Step{Action: "click", Role: "Button", Name: "Save"}})
	ai := &o.App.tr.in
	serveUntil(t, o, "the press", func() bool { return ai.Down[ui.ButtonLeft] })
	ai.Down[ui.ButtonLeft] = false // a real release
	in.stop()
	if ai.Cancelled[ui.ButtonLeft] {
		t.Error("a button that was up already was cancelled")
	}
}

// openFDs counts the process's open file descriptors.
func openFDs(t *testing.T) int {
	t.Helper()
	for _, d := range []string{"/dev/fd", "/proc/self/fd"} {
		if ents, err := os.ReadDir(d); err == nil {
			return len(ents)
		}
	}
	t.Skip("no way to count file descriptors here")
	return 0
}

// A shot that times out gives its output directory back at once, not when the
// main thread next drains: with the safe point withheld the descriptors would
// otherwise pile up one per request.
func TestBridgeTimedOutShotsCloseTheirDirectoryAtOnce(t *testing.T) {
	oldW, oldG := inspectWait, inspectGrace
	inspectWait, inspectGrace = time.Millisecond, 5*time.Millisecond
	defer func() { inspectWait, inspectGrace = oldW, oldG }()
	_, in := newBridgeOffscreen(t)
	out := shortTemp(t)
	base := openFDs(t)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := in.Handle(&inspectwire.Request{Op: "shot", Path: filepath.Join(out, "a.png")})
			if r.OK || r.Error.Code != inspectwire.ErrTimeout {
				t.Errorf("%+v", r)
			}
		}()
	}
	wg.Wait()
	if n := openFDs(t); n > base+3 {
		t.Errorf("%d descriptors open after 50 timed-out shots (%d before): their directories were kept for the main thread", n, base)
	}
	// the main thread then finds them cancelled and runs nothing
	in.root.safePoint()
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Errorf("a shot nobody waited for was written: %v", ents)
	}
	if n := openFDs(t); n > base+3 {
		t.Errorf("%d descriptors open after the drain (%d before)", n, base)
	}
}
