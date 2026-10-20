//go:build darwin

package platform

import (
	"fmt"
	"testing"

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
