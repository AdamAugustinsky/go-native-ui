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

// Without native menus ContextMenu draws the menu: a right-click opens it at
// the pointer with the items built for that click, a click on an item reports
// it once and closes the menu, a disabled item does nothing, a submenu opens
// under the pointer, Escape and a frame that does not build it close it, and
// Shift+F10 opens it from the keyboard with the first item focused.
func TestContextMenuDrawn(t *testing.T) {
	var built, item int
	var chosen, show bool
	show = true
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		if !show {
			return
		}
		s := rexui.ChatListRow(g.k, "r1", "Ada", "Lunch?", "10:42", rexui.ChatRowOpts{})
		if i, ok := rexui.ContextMenu(g.k, s.ID, func(m *[]ui.MenuItem) {
			built++
			*m = append(*m,
				ui.MenuItem{ID: cmArchive, Title: "Archive", Key: 'e', Mods: ui.ModCtrl},
				ui.MenuItem{ID: 3, Title: "Mute", Disabled: true},
				ui.MenuSeparator,
				ui.MenuItem{Title: "Labels", Sub: []ui.MenuItem{{ID: 100, Title: "Family", Mark: ui.MarkOn}, {ID: cmLabelWork, Title: "Work"}}},
				ui.MenuItem{ID: 9, Title: "Delete", Destructive: true})
		}); ok {
			item, chosen = i, true
		}
	}))
	has := func(role ui.Role, name string) (ui.SemNode, bool) {
		g.c.Semantics(&g.sem)
		for _, n := range g.sem.Nodes {
			if n.Role == role && string(g.sem.Text(n.Name)) == name {
				return n, true
			}
		}
		return ui.SemNode{}, false
	}
	row := g.node(ui.RoleListItem, "Ada")
	x, y := centre(row)
	rightClick := func() {
		g.in.MouseX, g.in.MouseY = x, y
		g.frame()
		g.in.SetButton(ui.ButtonRight, true, 1)
		g.frame()
		g.in.SetButton(ui.ButtonRight, false, 0)
		g.frame()
		g.frame()
	}

	rightClick()
	if built != 1 {
		t.Fatalf("items built %d times, want once", built)
	}
	if _, ok := g.c.TakeMenuRequest(); ok {
		t.Error("a native menu was requested without native menus")
	}
	arch, ok := has(ui.RoleMenuItem, "Archive")
	if !ok {
		t.Fatal("the drawn menu did not open")
	}
	if arch.Bounds[0] < x-1 || arch.Bounds[1] < y-1 {
		t.Errorf("the menu's first item at %v, want it below and right of the click (%v, %v)", arch.Bounds, x, y)
	}
	if n, _ := has(ui.RoleMenuItemCheckbox, "Family"); n.Role != 0 {
		t.Error("the submenu is open before it was asked for")
	}

	// a disabled item: nothing
	mute, _ := has(ui.RoleMenuItem, "Mute")
	g.click(centre(mute))
	if chosen {
		t.Fatal("a disabled item was chosen")
	}
	if _, ok := has(ui.RoleMenuItem, "Archive"); !ok {
		t.Fatal("a click on a disabled item closed the menu")
	}

	// the submenu opens under the pointer; its item is chosen
	labels, _ := has(ui.RoleMenuItem, "Labels")
	g.in.MouseX, g.in.MouseY = centre(labels)
	g.frames(3)
	work, ok := has(ui.RoleMenuItem, "Work")
	if !ok {
		t.Fatal("hovering Labels did not open its submenu")
	}
	if fam, ok := has(ui.RoleMenuItemCheckbox, "Family"); !ok || fam.State&ui.SemChecked == 0 {
		t.Errorf("Family is %+v (%v), want a checked item", fam, ok)
	}
	wx, wy := centre(work)
	g.in.MouseX = wx // along the row first, as a pointer does
	g.frame()
	g.click(wx, wy)
	if !chosen || item != cmLabelWork {
		t.Fatalf("chosen %v item %d, want %d", chosen, item, cmLabelWork)
	}
	chosen = false
	g.frames(2)
	if chosen {
		t.Error("the choice was delivered twice")
	}
	if _, ok := has(ui.RoleMenuItem, "Archive"); ok {
		t.Error("the menu stayed open after a choice")
	}

	// Escape closes it with no choice
	rightClick()
	g.key(ui.KeyEscape, 0)
	g.frame()
	if _, ok := has(ui.RoleMenuItem, "Archive"); ok || chosen {
		t.Errorf("after Escape: open %v, chosen %v", ok, chosen)
	}

	// a frame that does not build it closes it
	rightClick()
	show = false
	g.frames(2)
	show = true
	g.frames(2)
	if _, ok := has(ui.RoleMenuItem, "Archive"); ok {
		t.Error("the menu came back after its row was gone for a frame")
	}

	// the keyboard: Shift+F10 on the focused row opens it with the first
	// item focused; Down skips the disabled item and the separator, Enter
	// opens the submenu, Right as well with its first item focused
	g.click(x, y) // focus the row
	g.key(ui.KeyF10, ui.ModShift)
	g.frame()
	if _, ok := has(ui.RoleMenuItem, "Archive"); !ok {
		t.Fatal("Shift+F10 did not open the menu")
	}
	g.key(ui.KeyDown, 0)
	g.frame()
	g.key(ui.KeyRight, 0)
	g.frames(2)
	g.key(ui.KeyDown, 0)
	g.frame()
	g.key(ui.KeyEnter, 0)
	g.frame()
	if !chosen || item != cmLabelWork {
		t.Fatalf("by keyboard: chosen %v item %d, want %d", chosen, item, cmLabelWork)
	}

	// idle, closed: nothing allocated
	g.frames(3)
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Errorf("an idle frame allocates %v times", n)
	}
}

// The drawn menu's mnemonics: an item's Key without modifiers is its letter
// (Novo (N)), the others get a free letter of their titles; typing one, with
// or without Alt, chooses the item (or opens the submenu, whose letters then
// work), and a disabled item's letter does nothing. Apps need no letter
// handling of their own.
func TestContextMenuMnemonics(t *testing.T) {
	var item int
	var chosen bool
	g := newRig(t, theme.Light, nil, column(func(g *rig) {
		s := rexui.ChatListRow(g.k, "r1", "Ada", "Lunch?", "10:42", rexui.ChatRowOpts{})
		if i, ok := rexui.ContextMenu(g.k, s.ID, func(m *[]ui.MenuItem) {
			*m = append(*m,
				ui.MenuItem{ID: 1, Title: "Novo", Key: 'N'},
				ui.MenuItem{ID: 2, Title: "Limpar a tela", Key: 'l'},
				ui.MenuItem{ID: 3, Title: "Arquivar", Key: 'e', Mods: ui.ModCtrl},
				ui.MenuItem{ID: 4, Title: "Mudo", Disabled: true},
				ui.MenuSeparator,
				ui.MenuItem{Title: "Etiquetas", Sub: []ui.MenuItem{{ID: 100, Title: "Família"}, {ID: 101, Title: "Trabalho"}}})
		}); ok {
			item, chosen = i, true
		}
	}))
	row := g.node(ui.RoleListItem, "Ada")
	x, y := centre(row)
	open := func() {
		g.click(x, y)
		g.key(ui.KeyF10, ui.ModShift)
		g.frame()
	}
	letter := func(l byte, mods ui.Mods) {
		g.in.PushKey(ui.KeyEvent{Code: ui.KeyCode(l), Mods: mods, Down: true, Char: rune(l) + 'a' - 'A'})
		g.frame()
	}
	for _, c := range []struct {
		keys string
		mods ui.Mods
		want int
	}{
		{"L", 0, 2},         // its Key
		{"N", ui.ModAlt, 1}, // with Alt
		{"A", 0, 3},         // a Key with a modifier is a shortcut's hint: A, its title's first letter
		{"ET", 0, 101},      // the submenu, then its item
	} {
		open()
		chosen = false
		for i := range c.keys {
			letter(c.keys[i], c.mods)
			g.frame()
		}
		if !chosen || item != c.want {
			t.Errorf("%q with mods %v: chosen %v item %d, want %d", c.keys, c.mods, chosen, item, c.want)
		}
		g.frames(2)
	}
	open()
	chosen = false
	letter('M', 0) // disabled
	g.frames(2)
	if chosen {
		t.Errorf("a disabled item's letter chose %d", item)
	}
	if n := testing.AllocsPerRun(20, g.frame); n != 0 {
		t.Errorf("a frame of the open menu allocates %v times", n)
	}
}
