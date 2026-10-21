package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

const (
	cmArchive = iota + 1
	cmLabelWork
)

// A right-click on a row asks for a native menu made of the items the app
// builds for that click; the outcome comes back to the row in a later frame,
// chosen or dismissed; a frame with no click builds nothing and allocates
// nothing.
func TestOnContextMenu(t *testing.T) {
	var built int
	var item int
	var chosen bool
	var row ui.ID
	labels := []string{"Family", "Work"}
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		s := rexui.ChatListRow(g.k, "r1", "Ada", "Lunch?", "10:42", rexui.ChatRowOpts{})
		row = s.ID
		if i, ok := rexui.OnContextMenu(g.k, s.ID, func(m *[]ui.MenuItem) {
			built++
			sub := []ui.MenuItem{{ID: 100, Title: labels[0], Mark: ui.MarkOn}, {ID: cmLabelWork, Title: labels[1]}}
			*m = append(*m,
				ui.MenuItem{ID: cmArchive, Title: "Archive", Symbol: "archivebox", Key: 'e', Mods: ui.ModSuper},
				ui.MenuItem{ID: 3, Title: "Mute", Disabled: true},
				ui.MenuSeparator,
				ui.MenuItem{Title: "Labels", Sub: sub})
		}); ok {
			item, chosen = i, true
		}
	}))
	g.c.SetNativeMenus(true)
	n := g.node(ui.RoleListItem, "Ada")
	x, y := centre(n)
	rightClick := func() {
		g.in.MouseX, g.in.MouseY = x, y
		g.frame()
		g.in.SetButton(ui.ButtonRight, true, 1)
		g.frame()
		g.in.SetButton(ui.ButtonRight, false, 0)
		g.frame()
	}

	rightClick()
	r, ok := g.c.TakeMenuRequest()
	if !ok || r.Kind != ui.MenuItems || len(r.Items) != 4 || built != 1 {
		t.Fatalf("request %+v (%v), built %d times; want the 4 items built once", r, ok, built)
	}
	if r.Rect[0] != x || r.Rect[1] != y || r.Items[3].Sub[0].Mark != ui.MarkOn || !r.Items[1].Disabled {
		t.Errorf("request anchor %v / items %+v", r.Rect, r.Items)
	}

	g.in.SetMenuResult(r.Owner, cmLabelWork, ui.MenuChosen)
	g.frame()
	if !chosen || item != cmLabelWork {
		t.Fatalf("chosen %v item %d, want %d", chosen, item, cmLabelWork)
	}
	chosen = false
	g.frame()
	if chosen {
		t.Error("the choice was delivered twice")
	}

	// dismissed: no item
	g.in.SetMenuResult(r.Owner, 0, ui.MenuDismissed)
	g.frame()
	if chosen {
		t.Error("a dismissed menu reported a choice")
	}
	g.in.SetMenuResult(r.Owner, 0, ui.MenuDismissed)
	g.c.Begin(&g.in, g.w, g.h, g.scale, 1.0/60)
	if _, out := g.c.ContextMenuResult(row); out != ui.MenuDismissed {
		t.Errorf("outcome %v, want dismissed", out)
	}
	g.c.End(&g.list)
	g.in.ClearFrame()

	// without a click there is no work: nothing built, nothing allocated
	built = 0
	g.in.MouseX, g.in.MouseY = x, y
	g.frames(3)
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Errorf("an idle frame allocates %v times", n)
	}
	if built != 0 {
		t.Errorf("items built %d times without a click", built)
	}
	if _, ok := g.c.TakeMenuRequest(); ok {
		t.Error("a request without a click")
	}

	// no native menus: nothing opens
	g.c.SetNativeMenus(false)
	rightClick()
	if _, ok := g.c.TakeMenuRequest(); ok {
		t.Error("a menu was requested without native menus")
	}
}
