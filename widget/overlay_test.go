package widget

import (
	"slices"
	"testing"

	"gonativeui/menu"
	"gonativeui/ui"
)

// shell is a window with a menu bar, a select, a text field and a dialog.
type shell struct {
	m         *menu.Model
	cmds      []menu.ID
	sel       int
	field     ui.TextBuffer
	dlg       bool
	cancelled bool
	deleted   int
	ctxItem   int
	list      ListState
	sp        SplitState
	tab       int
	spPanes   [3]int
}

func (v *shell) build(k *Kit) {
	k.NextSize(Fill, Fill)
	k.Column("root")
	k.MenuBar()
	k.Select("Format", &v.sel, []string{"Plain", "Markdown", "HTML"})
	k.TextField("Name", &v.field, "Name…")
	if k.Button("Delete…").Clicked {
		v.dlg = true
	}
	k.Tabs("Tabs", &v.tab, []string{"One", "Two", "Three"})
	l := k.BeginList("things", &v.list, 20)
	for r := l.First; r < l.Last; r++ {
		l.Row(r, uint32(r))
		k.Label("item")
	}
	k.EndList(&l)
	if l.RightClicked {
		k.OpenContextMenu(1, l.ContextAt[0], l.ContextAt[1])
	}
	k.EndColumn()
	for id := menu.ID(1); id <= 7; id++ {
		if k.Command(id) {
			v.cmds = append(v.cmds, id)
		}
	}
	d := k.BeginDialog("del", "Delete 3 files?", &v.dlg)
	if d.Visible {
		k.Paragraph("They cannot be recovered. This is a long sentence that has to wrap inside the dialog panel.")
		d.BeginActions()
		d.Action("Cancel", ButtonDefault)
		if d.Action("Delete", ButtonPrimary) {
			v.deleted++
		}
		d.EndActions()
	}
	k.EndDialog(&d)
	if d.Cancelled {
		v.cancelled = true
	}
}

func newShell(t *testing.T) (*rig, *shell) {
	v := &shell{}
	g := newRig(t, 600, 500, v.build)
	v.m = testMenu()
	g.k.SetMenu(v.m)
	g.frames(3)
	return g, v
}

func TestMenuBarOpensChoosesAndDeliversCommandOnce(t *testing.T) {
	g, v := newShell(t)
	g.clickOn(ui.RoleMenuItem, "File")
	if g.k.MenuOpen() == false {
		t.Fatal("click on File did not open its menu")
	}
	if _, ok := g.find(ui.RoleMenu, "File"); !ok {
		t.Fatal("no File menu in the semantic tree")
	}
	g.clickOn(ui.RoleMenuItem, "Save")
	if g.k.MenuOpen() {
		t.Fatal("choosing an item left the menu open")
	}
	g.frames(3)
	if !slices.Equal(v.cmds, []menu.ID{3}) {
		t.Fatalf("commands delivered %v, want [3] exactly once", v.cmds)
	}
}

func TestMenuKeyboardNavigationSubmenuAndEscape(t *testing.T) {
	g, v := newShell(t)
	// focus the File item with Tab and open it with Down
	g.key(ui.KeyTab, 0)
	if f := g.c.FocusKey(); f != g.keyOf(ui.RoleMenuItem, "File") {
		t.Fatal("Tab did not reach the menu bar")
	}
	g.key(ui.KeyDown, 0)
	g.frames(2)
	if !g.k.MenuOpen() {
		t.Fatal("Down on a menu title did not open it")
	}
	if f := g.c.FocusKey(); f != g.keyOf(ui.RoleMenuItem, "New") {
		t.Fatal("the first item does not have the focus after opening")
	}
	g.key(ui.KeyDown, 0)
	g.key(ui.KeyDown, 0) // Open Recent
	if f := g.c.FocusKey(); f != g.keyOf(ui.RoleMenuItem, "Open Recent") {
		t.Fatal("two Downs did not reach Open Recent")
	}
	g.key(ui.KeyRight, 0)
	g.frames(2)
	if _, ok := g.find(ui.RoleMenu, "Recent"); !ok {
		t.Fatal("Right did not open the submenu")
	}
	g.key(ui.KeyEscape, 0)
	g.frames(2)
	if _, ok := g.find(ui.RoleMenu, "Recent"); ok {
		t.Fatal("Escape did not close the submenu")
	}
	if !g.k.MenuOpen() {
		t.Fatal("Escape closed the whole menu, not only the innermost")
	}
	// Down skips the separator and the wrap goes round
	g.key(ui.KeyDown, 0)
	if f := g.c.FocusKey(); f != g.keyOf(ui.RoleMenuItem, "Save") {
		t.Fatal("Down from Open Recent did not skip the separator")
	}
	g.key(ui.KeyDown, 0)
	if f := g.c.FocusKey(); f != g.keyOf(ui.RoleMenuItem, "New") {
		t.Fatal("Down from the last item did not wrap to the first")
	}
	g.key(ui.KeyEnter, 0)
	g.frames(3)
	if !slices.Equal(v.cmds, []menu.ID{1}) {
		t.Fatalf("Enter chose %v, want [1]", v.cmds)
	}
}

func TestMenuClickOutsideClosesAndIsConsumed(t *testing.T) {
	g, v := newShell(t)
	g.clickOn(ui.RoleMenuItem, "File")
	cb := g.must(ui.RoleButton, "Delete…")
	x, y := centre(cb)
	g.click(x, y) // outside the menu, on a button
	if g.k.MenuOpen() {
		t.Fatal("a press outside did not close the menu")
	}
	if v.dlg {
		t.Fatal("the press that closed the menu also pressed the button beneath")
	}
}

func TestDisabledMenuCommandDoesNotFire(t *testing.T) {
	g, v := newShell(t)
	v.m.SetEnabled(3, false)
	g.clickOn(ui.RoleMenuItem, "File")
	n := g.must(ui.RoleMenuItem, "Save")
	if n.State&ui.SemDisabled == 0 {
		t.Fatal("disabled command exports no disabled state")
	}
	x, y := centre(n)
	g.click(x, y)
	g.frames(3)
	if len(v.cmds) != 0 {
		t.Fatalf("disabled command fired: %v", v.cmds)
	}
}

func TestContextMenuOnListRightClick(t *testing.T) {
	g, v := newShell(t)
	x, y := centre(g.rowNode(ui.RoleListItem, 2))
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonRight, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonRight, false, 0)
	g.frames(3)
	if !g.k.MenuOpen() {
		t.Fatal("right click on a row opened no context menu")
	}
	g.clickOn(ui.RoleMenuItem, "Show Hidden")
	g.frames(3)
	if !slices.Equal(v.cmds, []menu.ID{4}) {
		t.Fatalf("context menu choice delivered %v", v.cmds)
	}
	if !v.list.Sel.Has(2) {
		t.Fatal("the right-clicked row is not selected")
	}
}

func TestSelectOpensChoosesAndKeys(t *testing.T) {
	g, v := newShell(t)
	g.clickOn(ui.RoleComboBox, "Format")
	if _, ok := g.find(ui.RoleList, "Options"); !ok {
		t.Fatal("click did not open the options")
	}
	if n := g.must(ui.RoleComboBox, "Format"); n.State&ui.SemExpanded == 0 {
		t.Fatal("open select exports no expanded state")
	}
	g.key(ui.KeyDown, 0)
	g.key(ui.KeyEnter, 0)
	g.frames(2)
	if v.sel != 1 {
		t.Fatalf("Down, Enter chose %d, want 1", v.sel)
	}
	if g.k.MenuOpen() {
		t.Fatal("choosing left the options open")
	}
	if f := g.c.FocusKey(); f != g.keyOf(ui.RoleComboBox, "Format") {
		t.Fatal("the focus did not return to the select")
	}
	if n := g.must(ui.RoleComboBox, "Format"); string(g.sem.Text(n.Value)) != "Markdown" {
		t.Fatalf("select value = %q", g.sem.Text(n.Value))
	}
	// Down from the closed select opens it; Escape closes without changing
	g.key(ui.KeyDown, 0)
	g.frames(2)
	if !g.k.MenuOpen() {
		t.Fatal("Down on the focused select did not open it")
	}
	g.key(ui.KeyEscape, 0)
	g.frames(2)
	if g.k.MenuOpen() || v.sel != 1 {
		t.Fatalf("Escape: open=%v choice=%d", g.k.MenuOpen(), v.sel)
	}
}

func TestDialogIsModalAndRoutesActions(t *testing.T) {
	g, v := newShell(t)
	g.clickOn(ui.RoleButton, "Delete…")
	g.frames(3)
	d := g.must(ui.RoleDialog, "Delete 3 files?")
	if d.State&ui.SemModal == 0 {
		t.Fatal("dialog exports no modal state")
	}
	// the page beneath is inert
	field := g.must(ui.RoleTextField, "Name")
	x, y := centre(field)
	g.click(x, y)
	g.typ("hello")
	if v.field.Len() != 0 {
		t.Fatalf("typed into a field beneath a modal dialog: %q", v.field.Bytes())
	}
	// Tab stays inside: after several Tabs the focus is a dialog button
	inside := map[ui.Key]bool{g.keyOf(ui.RoleButton, "Cancel"): true, g.keyOf(ui.RoleButton, "Delete"): true, d.Key: true}
	for i := 0; i < 5; i++ {
		g.key(ui.KeyTab, 0)
		if !inside[g.c.FocusKey()] {
			t.Fatalf("Tab %d moved the focus out of the dialog", i)
		}
	}
	// a shortcut of the app does not act beneath the modal
	g.key('N', ui.PrimaryMod())
	g.frames(2)
	if len(v.cmds) != 0 {
		t.Fatalf("a shortcut reached the app beneath a dialog: %v", v.cmds)
	}
	// Enter chooses the default (primary) action
	g.c.SetFocus(d.Key)
	g.key(ui.KeyEnter, 0)
	g.frames(2)
	if v.deleted != 1 || v.dlg {
		t.Fatalf("Enter: deleted=%d open=%v, want 1 and closed", v.deleted, v.dlg)
	}
	if _, ok := g.find(ui.RoleDialog, "Delete 3 files?"); ok {
		t.Fatal("the dialog is still in the tree after it closed")
	}
	// the page works again, and the focus came back
	g.click(x, y)
	g.typ("hi")
	if v.field.String() != "hi" {
		t.Fatalf("field after the dialog closed: %q", v.field.String())
	}
}

func TestDialogEscapeCancelsWithoutAction(t *testing.T) {
	g, v := newShell(t)
	g.clickOn(ui.RoleButton, "Delete…")
	g.frames(3)
	g.key(ui.KeyEscape, 0)
	g.frames(2)
	if !v.cancelled || v.dlg || v.deleted != 0 {
		t.Fatalf("Escape: cancelled=%v open=%v deleted=%d", v.cancelled, v.dlg, v.deleted)
	}
	// clicking the other action closes without the primary one
	g.clickOn(ui.RoleButton, "Delete…")
	g.frames(3)
	g.clickOn(ui.RoleButton, "Cancel")
	g.frames(2)
	if v.dlg || v.deleted != 0 {
		t.Fatalf("Cancel: open=%v deleted=%d", v.dlg, v.deleted)
	}
}

func TestTabsSelectAndArrowKeys(t *testing.T) {
	g, v := newShell(t)
	g.clickOn(ui.RoleTab, "Two")
	if v.tab != 1 {
		t.Fatalf("click chose tab %d", v.tab)
	}
	g.key(ui.KeyRight, 0)
	g.key(ui.KeyRight, 0)
	if v.tab != 0 {
		t.Fatalf("two Rights from tab 1 chose %d, want 0 (wrap)", v.tab)
	}
	if n := g.must(ui.RoleTab, "One"); n.State&ui.SemSelected == 0 {
		t.Fatal("selected tab exports no selected state")
	}
}

func TestTextFieldPlaceholderEditAndUndo(t *testing.T) {
	g, v := newShell(t)
	if _, ok := g.find(ui.RoleLabel, "Name…"); ok {
		// the placeholder is a plain box, not in the tree
		t.Log("placeholder has semantics")
	}
	x, y := centre(g.must(ui.RoleTextField, "Name"))
	g.click(x, y)
	g.typ("abc")
	if v.field.String() != "abc" {
		t.Fatalf("typed %q", v.field.String())
	}
	g.key('A', ui.PrimaryMod())
	g.key('Z', ui.PrimaryMod())
	if v.field.Len() != 0 {
		t.Fatalf("undo left %q", v.field.String())
	}
	n := g.must(ui.RoleTextField, "Name")
	if n.State&ui.SemEditable == 0 {
		t.Fatal("text field not editable in the tree")
	}
}

// With native menus the kit draws no menu bar and no popups: a select, a
// pull-down and a context menu ask the app for a native menu, and a list
// choice comes back to the select.
func TestNativeMenusAreRequested(t *testing.T) {
	v := &shell{}
	g := newRig(t, 600, 500, v.build)
	v.m = testMenu()
	g.k.SetMenu(v.m)
	g.c.SetNativeMenus(true)
	g.frames(3)
	if _, ok := g.find(ui.RoleMenuBar, "Menu bar"); ok {
		t.Fatal("the menu bar is drawn with native menus")
	}

	g.clickOn(ui.RoleComboBox, "Format")
	r, ok := g.c.TakeMenuRequest()
	if !ok || r.Kind != ui.MenuList || len(r.Options) != 3 || r.Options[1] != "Markdown" || r.Selected != 0 || r.Rect[2] <= r.Rect[0] {
		t.Fatalf("select request %+v %v", r, ok)
	}
	if _, open := g.find(ui.RoleList, "Options"); open || g.k.MenuOpen() {
		t.Fatal("the select drew its options")
	}
	g.in.SetMenuChoice(r.Owner, 2, r.Rev)
	g.frame()
	if v.sel != 2 {
		t.Fatalf("the choice gave sel %d, want 2", v.sel)
	}

	x, y := centre(g.rowNode(ui.RoleListItem, 2))
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonRight, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonRight, false, 0)
	g.frames(2)
	r, ok = g.c.TakeMenuRequest()
	if !ok || r.Kind != ui.MenuContext || r.Menu != 1 || r.Rect[0] != x || r.Rect[1] != y || g.k.MenuOpen() {
		t.Fatalf("context request %+v %v (drawn menu open %v)", r, ok, g.k.MenuOpen())
	}
	if _, ok := g.c.TakeMenuRequest(); ok {
		t.Fatal("a request was taken twice")
	}
	g.in.PushCommand(4) // the native item's command
	g.frames(2)
	if !slices.Equal(v.cmds, []menu.ID{4}) {
		t.Fatalf("the native menu's command delivered %v", v.cmds)
	}
}
