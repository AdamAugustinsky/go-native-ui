//go:build darwin

package platform

import (
	"fmt"
	"testing"
	"time"

	"gonativeui/internal/objc"
)

// testInstall puts a hand-made window of a test rig in the registry, as Open
// does for a real one: its GNUIView (when it is one) finds it, and so do the
// app-wide lookups. The returned function takes it out again. Unlike
// register, it touches no ivar of an object that is not of our classes (rigs
// stand a view in for the NSWindow).
func testInstall(w *Window) (remove func()) {
	i := 0
	for reg.slots[i] != nil {
		i++
	}
	reg.slots[i], w.slot, w.registered = w, i, true
	reg.open[reg.n] = w
	reg.n++
	isView := w.view != 0 && viewClass != 0 && objc.SendBool1(w.view, selIsKindOfClass, uintptr(viewClass))
	if isView {
		viewDef.Index().Set(w.view, uintptr(i+1))
	}
	return func() {
		if isView {
			viewDef.Index().Set(w.view, 0)
		}
		w.registered = false
		reg.slots[i] = nil
		for k := 0; k < reg.n; k++ {
			if reg.open[k] == w {
				copy(reg.open[k:reg.n], reg.open[k+1:reg.n])
				reg.n--
				reg.open[reg.n] = nil
				break
			}
		}
		if reg.lastKey == w {
			reg.lastKey = nil
		}
		forgetWindow(w)
	}
}

// A window opening while another is open cascades from it (below and right,
// both title bars in view) instead of covering it at the centre; the next
// cascades from that one; with every window closed the next one is centred
// again.
func TestWindowsOpenCascaded(t *testing.T) {
	var (
		err     error
		frames  [4][4]float64 // x, y (top-left space), width, height
		centres [2][4]float64
	)
	onMain(func() {
		Pump(0)
		centre := func(w *Window) [4]float64 {
			objc.Send(w.win, selCenter)
			x, y, wd, h := w.FrameRect()
			return [4]float64{x, y, wd, h}
		}
		var ws [3]*Window
		for i := range ws {
			if ws[i], err = Open(Options{Title: fmt.Sprint("cascade ", i), Width: 300, Height: 200, Hidden: true}); err != nil {
				return
			}
			x, y, wd, h := ws[i].FrameRect()
			frames[i] = [4]float64{x, y, wd, h}
			if i == 0 {
				centres[0] = centre(ws[i])
			}
		}
		for _, w := range ws {
			w.Close()
		}
		Pump(0)
		var w *Window
		if w, err = Open(Options{Title: "alone", Width: 300, Height: 200, Hidden: true}); err != nil {
			return
		}
		x, y, wd, h := w.FrameRect()
		frames[3] = [4]float64{x, y, wd, h}
		centres[1] = centre(w)
		w.Close()
		Pump(0)
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	for i := 1; i < 3; i++ {
		prev, cur := frames[i-1], frames[i]
		dx, dy := cur[0]-prev[0], cur[1]-prev[1]
		if dx < 10 || dy < 10 || dy > 60 {
			t.Errorf("window %d at %v, after one at %v: want it below and right by about a title bar", i, cur, prev)
		}
		if cur[2] != prev[2] || cur[3] != prev[3] {
			t.Errorf("window %d is %vx%v, want the size it asked for, %vx%v", i, cur[2], cur[3], prev[2], prev[3])
		}
	}
	// AppKit's centre follows the current screen/visible frame (including
	// menu-bar and Dock changes), not the coordinates of a previous window.
	for n, i := range []int{0, 3} {
		if frames[i] != centres[n] {
			t.Errorf("window %d opened with none open is at %v, want its current AppKit centre %v", i, frames[i], centres[n])
		}
	}
}

// A closed window's slot goes to the next window, and nothing meant for the
// closed one reaches the new one in its slot: a worker's late Wake, a key
// event that outlived it. 200 windows open and close in turn, and the closed
// ones' objects go with them.
func TestReusedSlotsGetNothingOfTheClosedWindow(t *testing.T) {
	const cycles = 200
	var (
		err        error
		base, open WindowObjects
		fails      []string
		control    bool
	)
	failf := func(i int, f string, a ...any) {
		if len(fails) < 10 {
			fails = append(fails, fmt.Sprintf("window %d: ", i)+fmt.Sprintf(f, a...))
		}
	}
	keysIn := func(q []Event) (keys, wakes int) {
		for _, e := range q {
			switch e.Kind {
			case EvKey, EvText:
				keys++
			case EvWake:
				wakes++
			}
		}
		return
	}
	settle := func() { // what was posted is taken by a pump that waits a little
		for i := 0; i < 3; i++ {
			Pump(int64(5 * time.Millisecond))
		}
	}
	onMain(func() {
		Pump(0)
		base = LiveWindowObjects()
		opts := Options{Title: "slot", Width: 200, Height: 120, Hidden: true}
		var prev *Window
		if prev, err = Open(opts); err != nil {
			return
		}
		for i := 1; i <= cycles && err == nil; i++ {
			pool := objc.F.Call0(objc.PoolPush) // the cycle's events (autoreleased, holding their windows) go with it
			slot, gen, mark := prev.slot, prev.gen, viewDef.Index().Get(prev.view)
			key := objc.Retain(keyDown(prev, "k", 0, 40)) // made while the window is open
			var held objc.ID
			if i%2 == 0 { // the event's window still exists (something holds it), closed
				held = objc.Retain(prev.win)
			}
			prev.Close()
			Pump(0)
			var cur *Window
			if cur, err = Open(opts); err != nil {
				objc.Release(key)
				objc.F.Call1(objc.PoolPop, pool)
				break
			}
			if cur.slot != slot || cur.gen == gen {
				failf(i, "slot %d gen %d after slot %d gen %d: want the freed slot, a new generation", cur.slot, cur.gen, slot, gen)
			}
			if w := slotWindow(mark); w != nil { // a value kept from the closed window (a callback's context)
				failf(i, "the closed window's slot mark finds a window (the one in its slot: %v)", w == cur)
			}
			if slotWindow(viewDef.Index().Get(cur.view)) != cur {
				failf(i, "the window's own slot mark does not find it")
			}
			prev.Wake()
			if held != 0 {
				// An event that holds its window: routed to none (AppKit's
				// alone), it is not posted here, as AppKit would keep the
				// closed window for the key's text input.
				if w := eventWindow(key); w != nil {
					failf(i, "a key event of the closed window, which still exists, goes to a window (the one in its slot: %v)", w == cur)
				}
				objc.Release(held)
			} else {
				objc.Send2(appObj.app, selPostEvent, uintptr(key), 0)
			}
			objc.Release(key)
			settle()
			if k, wk := keysIn(cur.Events()); k+wk != 0 {
				failf(i, "the closed window's key and wake reached the window in its slot: %d keys, %d wakes", k, wk)
			}
			objc.F.Call1(objc.PoolPop, pool)
			prev = cur
		}
		if err != nil {
			return
		}
		Pump(0)
		open = LiveWindowObjects() // the 200 closed: only the last one's objects left
		// The check sees a key and a wake of the window's own. (After a key,
		// the text input system holds on to the window it went to: the last
		// one is left open.)
		objc.Send2(appObj.app, selPostEvent, uintptr(keyDown(prev, "k", 0, 40)), 0)
		prev.Wake()
		settle()
		k, wk := keysIn(prev.Events())
		control = k > 0 && wk == 1
		prev.Close()
		Pump(0)
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	if !control {
		t.Fatal("a window's own key and wake did not reach it: the check would see nothing")
	}
	for _, f := range fails {
		t.Error(f)
	}
	if want := (WindowObjects{Windows: base.Windows + 1, Views: base.Views + 1, Delegates: base.Delegates + 1}); open != want {
		t.Errorf("after %d windows closed, with one open: %+v, want %+v", cycles, open, want)
	}
}

// A closed window's own objects (its GNUIWindow, GNUIView and delegate,
// with a host and a backdrop in it) are freed by the next pump, while the
// window still open keeps its own; nothing is freed by Close itself (an
// AppKit callback that asked for the close may still be using them).
func TestClosedWindowsFreeTheirObjects(t *testing.T) {
	var (
		err       error
		base, end WindowObjects
		shell0    ShellCounts
		shell1    ShellCounts
		fails     []string
		cycles    int
		failf     = func(f string, a ...any) {
			fails = append(fails, fmt.Sprintf("cycle %d: ", cycles)+fmt.Sprintf(f, a...))
		}
		plus = func(n int64) WindowObjects {
			return WindowObjects{Windows: base.Windows + n, Views: base.Views + n, Delegates: base.Delegates + n}
		}
		withoutPen = func(o WindowObjects) WindowObjects { o.Pending = 0; return o }
	)
	onMain(func() {
		Pump(0)
		base, shell0 = LiveWindowObjects(), ShellObjects()
		var a *Window
		if a, err = Open(Options{Title: "stays", Width: 300, Height: 200, Hidden: true}); err != nil {
			return
		}
		for cycles = 1; cycles <= 3; cycles++ {
			b, e := Open(Options{Title: "goes", Width: 300, Height: 200, Hidden: true, Backdrop: BackdropWindow})
			if e != nil {
				err = e
				break
			}
			pool := objc.F.Call0(objc.PoolPush) // as the app's frame or window setup holds one
			if _, e := b.NewHost(HostView, HostOptions{}); e != nil {
				failf("NewHost: %v", e)
			}
			if got := LiveWindowObjects(); got != plus(2) {
				failf("with two windows open: %+v, want %+v", got, plus(2))
			}
			refs := newWeakRefs(t, b.win, b.view, b.delegate)
			objc.F.Call1(objc.PoolPop, pool)
			b.Close()
			b.Close() // a second Close does nothing
			if got := LiveWindowObjects(); withoutPen(got) != plus(2) || got.Pending != 3 {
				failf("right after Close: %+v, want the objects alive and 3 pending (Close must not free them under a callback)", got)
			}
			Pump(0)
			if got := LiveWindowObjects(); got != plus(1) {
				failf("after Close and a pump: %+v, want %+v", got, plus(1))
			}
			if n := refs.alive(); n != 0 {
				failf("%d of the closed window's NSWindow, view and delegate still exist after the pump", n)
			}
		}
		a.Close()
		Pump(0)
		end, shell1 = LiveWindowObjects(), ShellObjects()
	})
	if err != nil {
		t.Skipf("cannot open a window here: %v", err)
	}
	for _, f := range fails {
		t.Error(f)
	}
	if end != base {
		t.Errorf("after closing both: %+v, want the baseline %+v", end, base)
	}
	if shell1.Hosts != shell0.Hosts || shell1.Materials != shell0.Materials {
		t.Errorf("hosts or materials left behind: %+v, baseline %+v", shell1, shell0)
	}
}
