//go:build darwin

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// A window with the native shell: the backdrop sits under our view and the
// buttons above it in a container, the surface is told to be transparent, a
// button click arrives as EvCommand, and keeping a button's state allocates
// nothing.
func TestNativeShell(t *testing.T) {
	type result struct {
		opened                  bool
		err                     error
		backdrop                Backdrop
		transparent             bool
		order                   []string // the container's subviews, bottom to top
		viewIndex               int
		opaqueWindow            bool
		cmds                    []uint64
		allocs                  float64
		plusEnabled, hasButtons bool
		plusX                   float64
	}
	var r result
	onMain(func() {
		w, err := Open(Options{
			Title: "shell test", Width: 400, Height: 300, Hidden: true,
			Titlebar: 46, TrafficLights: [2]float32{21, 22}, Backdrop: BackdropGlass,
			Buttons: []NativeButton{
				{ID: 2, Symbol: "command", Label: "Commands", X: -57, Y: 23},
				{ID: 7, Symbol: "plus", Label: "New", X: -23, Y: 23, Style: ButtonGlass},
			},
		})
		if err != nil {
			r.err = err
			return
		}
		defer w.Close()
		r.opened = true
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		r.backdrop, r.transparent, r.hasButtons = w.Backdrop(), w.Native().Transparent, w.HasButtons()
		subs := objc.Send(w.content, objc.Sel("subviews"))
		for i := 0; i < int(objc.Send(subs, objc.Sel("count"))); i++ {
			v := objc.Send1(subs, objc.Sel("objectAtIndex:"), uintptr(i))
			if v == w.view {
				r.viewIndex = i
			}
			r.order = append(r.order, string(objc.AppendUTF8(nil, objc.Send(v, objc.Sel("className")))))
		}
		r.opaqueWindow = objc.SendBool(w.win, objc.Sel("isOpaque"))
		plus := w.buttons[1].obj
		r.plusX = objc.SendRect(plus, selFrame).X

		w.q.reset()
		delegate := objc.Send(w.win, objc.Sel("delegate"))
		objc.Send1(delegate, selButtonAction, uintptr(w.buttons[1].obj)) // a click on +
		objc.Send1(delegate, selButtonAction, uintptr(w.buttons[0].obj)) // and on ⌘
		for _, e := range w.q.ev {
			if e.Kind == EvCommand {
				r.cmds = append(r.cmds, e.Tag)
			}
		}
		w.SetButtonEnabled(7, false)
		r.allocs = testing.AllocsPerRun(100, func() { w.SetButtonEnabled(7, false) })
		r.plusEnabled = objc.SendBool(plus, objc.Sel("isEnabled"))
	})
	if r.err != nil || !r.opened {
		t.Skipf("cannot open a window here: %v", r.err)
	}
	if r.backdrop == BackdropNone || !r.transparent || r.opaqueWindow {
		t.Errorf("backdrop %d, surface transparent %v, window opaque %v: want a backdrop, a transparent surface, a non-opaque window", r.backdrop, r.transparent, r.opaqueWindow)
	}
	if len(r.order) != 4 || r.viewIndex != 1 || !r.hasButtons {
		t.Errorf("container holds %v with our view at %d: want the effect view, ours, then the two buttons", r.order, r.viewIndex)
	}
	if r.plusX != 400-23-14 {
		t.Errorf("+ at x %v, want %v (23 pt from the right edge to its centre)", r.plusX, 400-23-14)
	}
	if len(r.cmds) != 2 || r.cmds[0] != 7 || r.cmds[1] != 2 {
		t.Errorf("commands %v, want [7 2]", r.cmds)
	}
	if r.allocs != 0 {
		t.Errorf("an unchanged SetButtonEnabled allocates %.1f times", r.allocs)
	}
	if r.plusEnabled {
		t.Error("SetButtonEnabled(false) left + enabled")
	}
}
