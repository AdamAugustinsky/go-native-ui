//go:build darwin

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// A window with the native shell: the backdrop sits under our view in a
// container, the surface is told to be transparent, and the toolbar's tab
// group and buttons deliver their ids as EvCommand. Updating the toolbar
// with what it already shows allocates nothing.
func TestNativeShell(t *testing.T) {
	type result struct {
		opened                  bool
		err                     error
		backdrop                Backdrop
		transparent, toolbar    bool
		subviews                int
		viewOnTop, opaqueWindow bool
		cmds                    []uint64
		selected                int
		allocs                  float64
		plusEnabled             bool
	}
	var r result
	tabs := []ToolbarTab{{ID: 20, Symbol: "terminal", Label: "one"}, {ID: 21, Label: "two"}}
	onMain(func() {
		w, err := Open(Options{
			Title: "shell test", Width: 400, Height: 300, Hidden: true,
			Backdrop: BackdropGlass, Subtitle: "sub", ToolbarCompact: true,
			Toolbar: []ToolbarItem{
				{Kind: ToolbarTabs, ID: 3, Tabs: tabs, Selected: 0},
				{Kind: ToolbarFlexibleSpace},
				{Kind: ToolbarButton, ID: 7, Symbol: "plus", Label: "New"},
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
		r.backdrop, r.transparent, r.toolbar = w.Backdrop(), w.Native().Transparent, w.HasToolbar()
		subs := objc.Send(w.content, objc.Sel("subviews"))
		r.subviews = int(objc.Send(subs, objc.Sel("count")))
		r.viewOnTop = objc.Send(subs, objc.Sel("lastObject")) == w.view
		r.opaqueWindow = objc.SendBool(w.win, objc.Sel("isOpaque"))

		w.q.reset()
		delegate := objc.Send(w.win, objc.Sel("delegate"))
		tabsItem, plus := w.tb.find(3), w.tb.find(7)
		objc.Send1(tabsItem.seg, selSetSelectedSeg, 1) // the user picks the second tab
		objc.Send1(delegate, selTBAction, uintptr(tabsItem.seg))
		objc.Send1(delegate, selTBAction, uintptr(plus.obj)) // and clicks +
		for _, e := range w.q.ev {
			if e.Kind == EvCommand {
				r.cmds = append(r.cmds, e.Tag)
			}
		}
		w.SetToolbarTabs(3, tabs, 0) // the app selects the first one again
		r.selected = int(objc.Send(tabsItem.seg, selSelectedSeg))
		r.allocs = testing.AllocsPerRun(100, func() {
			w.SetToolbarTabs(3, tabs, 0)
			w.SetToolbarEnabled(7, false)
		})
		r.plusEnabled = objc.SendBool(plus.obj, objc.Sel("isEnabled"))
	})
	if r.err != nil || !r.opened {
		t.Skipf("cannot open a window here: %v", r.err)
	}
	if r.backdrop == BackdropNone || !r.transparent || r.opaqueWindow {
		t.Errorf("backdrop %d, surface transparent %v, window opaque %v: want a backdrop, a transparent surface, a non-opaque window", r.backdrop, r.transparent, r.opaqueWindow)
	}
	if r.subviews != 2 || !r.viewOnTop {
		t.Errorf("content view has %d subviews, our view on top: %v; want the effect view under ours", r.subviews, r.viewOnTop)
	}
	if !r.toolbar {
		t.Error("no toolbar")
	}
	if len(r.cmds) != 2 || r.cmds[0] != 21 || r.cmds[1] != 7 {
		t.Errorf("commands %v, want [21 7] (second tab, then +)", r.cmds)
	}
	if r.selected != 0 {
		t.Errorf("SetToolbarTabs selected segment %d, want 0", r.selected)
	}
	if r.allocs != 0 {
		t.Errorf("unchanged toolbar updates allocate %.1f times", r.allocs)
	}
	if r.plusEnabled {
		t.Error("SetToolbarEnabled(false) left + enabled")
	}
}
