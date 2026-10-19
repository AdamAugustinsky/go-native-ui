package app

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/platform"
	"gonativeui/ui"
)

type fakeTips struct {
	calls int
	last  []platform.TooltipRegion
}

func (f *fakeTips) SetTooltipRegions(r []platform.TooltipRegion) {
	f.calls++
	f.last = append(f.last[:0], r...)
}

// The tooltips of a finished frame reach the window as platform regions with
// the same rectangles and keys, only when tooltips are native, and without
// allocating once the buffers have grown.
func TestSyncTooltips(t *testing.T) {
	a := &App{UI: ui.New(fakeui.New())}
	win := &fakeTips{}
	build := func(c *ui.Context) {
		c.NextSize(ui.Px(100), ui.Px(40))
		c.NextPos(10, 20)
		id := c.Box(ui.Hoverable|ui.FloatingX|ui.FloatingY, "a")
		c.NativeTooltip(id, "Tip")
	}
	var in ui.Input
	var list gpu.DrawList
	frame := func() {
		a.UI.Begin(&in, 400, 300, 1, 1.0/60)
		build(a.UI)
		a.UI.End(&list)
	}
	frame()
	a.syncTooltips(win)
	if win.calls != 0 {
		t.Error("regions were handed over although tooltips are not native")
	}
	a.UI.SetNativeTooltips(true)
	frame()
	a.syncTooltips(win)
	want := a.UI.TooltipRegions(nil)
	if win.calls != 1 || len(win.last) != 1 || len(want) != 1 || win.last[0].Rect != want[0].Rect || win.last[0].Key != want[0].Key || win.last[0].Rect != [4]float32{10, 20, 110, 60} {
		t.Fatalf("calls %d, handed over %+v, ui regions %+v", win.calls, win.last, want)
	}
	if n := testing.AllocsPerRun(50, func() {
		frame()
		a.syncTooltips(win)
	}); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}

// Dialog results come out of TakeDialog oldest first.
func TestTakeDialog(t *testing.T) {
	a := &App{}
	if _, ok := a.TakeDialog(); ok {
		t.Fatal("a result from nothing")
	}
	for i := 1; i <= maxDialogResults+2; i++ {
		a.dlg = append(a.dlg, platform.DialogResult{Ticket: platform.DialogTicket(i)})
		if len(a.dlg) > maxDialogResults { // what onDialog does past the bound
			a.dlg = a.dlg[:copy(a.dlg, a.dlg[1:])]
		}
	}
	first, _ := a.TakeDialog()
	second, _ := a.TakeDialog()
	if first.Ticket != 3 || second.Ticket != 4 {
		t.Errorf("tickets %d, %d: the oldest two were dropped, want 3 and 4 first", first.Ticket, second.Ticket)
	}
	if _, err := (&App{}).Alert(platform.AlertSpec{Buttons: []string{"OK"}}); err == nil {
		t.Error("Alert without a window did not fail")
	}
}
