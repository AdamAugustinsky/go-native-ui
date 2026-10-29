package widget

import (
	"runtime"
	"slices"
	"testing"

	"gonativeui/menu"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/base"
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

// A command with a role and no title of its own takes the title of the
// menu.Strings in use: in Brazilian Portuguese the drawn menu reads Copiar.
func TestDrawnMenuRoleTitleFollowsTheStrings(t *testing.T) {
	menu.SetStrings(rexui.StringsPtBR.Menu)
	t.Cleanup(func() { menu.SetStrings(menu.StringsEnglish) })
	m, err := menu.NewModel(
		[]menu.Command{{ID: 1, Role: menu.RoleCopy, Shortcut: menu.Shortcut{Key: 'C', Primary: true}}, {ID: 2, Role: menu.RoleSelectAll}},
		[]menu.Menu{{Title: "Editar", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 1}, {Kind: menu.ItemCommand, Command: 2}}}},
		[]int{0})
	if err != nil {
		t.Fatal(err)
	}
	v := &shell{}
	g := newRig(t, 600, 500, v.build)
	v.m = m
	g.k.SetMenu(m)
	g.frames(3)
	g.clickOn(ui.RoleMenuItem, "Editar")
	g.must(ui.RoleMenuItem, "Copiar")
	g.must(ui.RoleMenuItem, "Selecionar Tudo")
	if _, ok := g.find(ui.RoleMenuItem, "Copy"); ok {
		t.Error("the drawn menu still reads Copy")
	}
}

// ---- The menu bar from the keyboard (Windows' F10, Alt and mnemonics) ----

// altKey sends a key press with Alt, as the platform reports it: the
// character the key types on the layout, and the key.
func (g *rig) altKey(letter byte) {
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyCode(letter), Mods: ui.ModAlt, Down: true, Char: rune(letter) + 'a' - 'A'})
	g.frame()
}

// letter types a letter key alone (the key press; the text is not needed).
func (g *rig) letter(letter byte) {
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyCode(letter), Down: true, Char: rune(letter) + 'a' - 'A'})
	g.frame()
}

// altTap is Alt pressed and released on its own (ui.Input.AltTap).
func (g *rig) altTap() {
	g.in.AltTap = true
	g.frame()
}

// focusField clicks the text field and returns its key.
func (g *rig) focusField() ui.Key {
	g.click(centre(g.must(ui.RoleTextField, "Name")))
	return g.c.FocusKey()
}

// F10 and the Alt tap move the focus to the first title and give it back;
// Escape gives it back too, and the arrows move along the bar.
func TestMenuBarF10AndAltTapFocusTheBar(t *testing.T) {
	for _, how := range []string{"F10", "Alt"} {
		t.Run(how, func(t *testing.T) {
			g, _ := newShell(t)
			field := g.focusField()
			toggle := func() {
				if how == "F10" {
					g.key(ui.KeyF10, 0)
				} else {
					g.altTap()
				}
				g.frame()
			}
			toggle()
			if g.c.FocusKey() != g.keyOf(ui.RoleMenuItem, "File") {
				t.Fatal("the first title does not have the focus")
			}
			if !g.c.FocusVisible() {
				t.Error("the focus ring does not show on the title")
			}
			toggle()
			if g.c.FocusKey() != field {
				t.Fatal("pressed again, the focus did not go back to the field")
			}
			toggle()
			g.key(ui.KeyRight, 0)
			if g.c.FocusKey() != g.keyOf(ui.RoleMenuItem, "View") {
				t.Fatal("Right did not move to View")
			}
			g.key(ui.KeyEscape, 0)
			g.frame()
			if g.c.FocusKey() != field {
				t.Fatal("Escape on the bar did not give the focus back to the field")
			}
		})
	}
}

// Alt and a title's mnemonic opens its menu with the first item focused; an
// item's mnemonic chooses it, the command arrives once and the focus goes
// back to where it was. The field does not take Alt+F off macOS.
func TestMenuBarAltMnemonicOpensAndLetterChooses(t *testing.T) {
	g, v := newShell(t)
	var field ui.Key
	if runtime.GOOS != "darwin" {
		field = g.focusField()
	} else { // Option+F types a character in a macOS field: the focus is on a button
		field = g.keyOf(ui.RoleButton, "Delete…")
		g.c.SetFocus(field)
		g.frame()
	}
	g.altKey('F')
	g.frames(2)
	if !g.k.MenuOpen() {
		t.Fatal("Alt+F did not open the File menu")
	}
	if g.c.FocusKey() != g.keyOf(ui.RoleMenuItem, "New") {
		t.Fatal("the first item does not have the focus")
	}
	g.letter('S')
	g.frames(3)
	if g.k.MenuOpen() {
		t.Fatal("S left the menu open")
	}
	if !slices.Equal(v.cmds, []menu.ID{3}) {
		t.Fatalf("S chose %v, want [3] (Save) once", v.cmds)
	}
	if g.c.FocusKey() != field {
		t.Fatal("the focus did not go back to where it was")
	}
	if v.field.String() != "" {
		t.Fatalf("the letters reached the field: %q", v.field.String())
	}
}

// With the focus on a title, its mnemonic alone opens it; in a menu a
// submenu's mnemonic opens the submenu and its item's chooses it; Alt with
// the letter works in an open menu as well.
func TestMenuBarMnemonicsOnTheBarAndInSubmenus(t *testing.T) {
	g, v := newShell(t)
	g.key(ui.KeyF10, 0)
	g.frame()
	g.letter('V')
	g.frames(2)
	if _, ok := g.find(ui.RoleMenu, "View"); !ok {
		t.Fatal("V on the bar did not open View")
	}
	g.key(ui.KeyEscape, 0) // back to the title
	g.frames(2)
	if g.c.FocusKey() != g.keyOf(ui.RoleMenuItem, "View") {
		t.Fatal("Escape in the menu did not go back to its title")
	}
	g.key(ui.KeyLeft, 0)
	g.key(ui.KeyDown, 0)
	g.frames(2)
	// File: New (N), Open… (O), Open Recent (R: O is taken), Save (S)
	g.letter('R')
	g.frames(2)
	if _, ok := g.find(ui.RoleMenu, "Recent"); !ok {
		t.Fatal("R did not open Open Recent")
	}
	// Recent: Recent One (R), Recent Two (T: R is taken)
	g.altKey('T')
	g.frames(3)
	if !slices.Equal(v.cmds, []menu.ID{7}) {
		t.Fatalf("Alt+T chose %v, want [7] (Recent Two)", v.cmds)
	}
}

// Several items with one letter: the letter moves the focus between them and
// chooses nothing; an asked-for mnemonic (menu.Command.Mnemonic) wins.
func TestMenuMnemonicsShared(t *testing.T) {
	m, err := menu.NewModel(
		[]menu.Command{
			{ID: 1, Title: "Paste"}, {ID: 2, Title: "Print"}, {ID: 3, Title: "Exit", Mnemonic: 'x'},
		},
		[]menu.Menu{{Title: "File", Items: []menu.Item{
			{Kind: menu.ItemCommand, Command: 1}, {Kind: menu.ItemCommand, Command: 2}, {Kind: menu.ItemCommand, Command: 3},
		}}},
		[]int{0})
	if err != nil {
		t.Fatal(err)
	}
	v := &shell{}
	g := newRig(t, 600, 500, v.build)
	v.m = m
	g.k.SetMenu(m)
	g.frames(3)
	var mns base.Mnemonics
	for _, s := range []string{"Paste", "Print", "Exit"} {
		mns.Add(s, 0)
	}
	mns.Resolve()
	if l, _ := mns.At(1); l != 'R' {
		t.Fatalf("Print's mnemonic is %q, want R (P is Paste's)", l)
	}
	g.altKey('F')
	g.frames(2)
	g.letter('X')
	g.frames(3)
	if !slices.Equal(v.cmds, []menu.ID{3}) {
		t.Fatalf("X chose %v, want [3] (Exit, its asked-for letter)", v.cmds)
	}
}

// The mnemonics are underlined while the keyboard drives the bar, off macOS,
// and a frame of the open menu allocates nothing.
func TestMenuBarMnemonicUnderlines(t *testing.T) {
	g, _ := newShell(t)
	under := func(role ui.Role, name string) bool {
		b := g.must(role, name).Bounds
		for _, q := range g.list.Quads {
			r := q.Rect
			if r[3]-r[1] <= 2 && r[2] > r[0] && r[0] >= b[0] && r[2] <= b[2] && r[1] >= (b[1]+b[3])/2 && r[3] <= b[3] {
				return true
			}
		}
		return false
	}
	if under(ui.RoleMenuItem, "File") {
		t.Fatal("an underline shows before the keyboard reached the bar")
	}
	g.altKey('F')
	g.frames(3)
	if mac := runtime.GOOS == "darwin"; under(ui.RoleMenuItem, "File") == mac || under(ui.RoleMenuItem, "Save") == mac {
		t.Fatalf("underlines of File and Save: %v, %v; want %v (macOS shows none)", under(ui.RoleMenuItem, "File"), under(ui.RoleMenuItem, "Save"), !mac)
	}
	if n := testing.AllocsPerRun(20, g.frame); n != 0 {
		t.Fatalf("a frame with the menu open allocates %v times", n)
	}
}

// A focused component that takes F10 or Alt+letter keeps it: the bar takes
// them at the end of the frame, after the components. A dialog keeps the bar
// from the keyboard.
func TestMenuBarKeysGiveWayToTheFocusedComponent(t *testing.T) {
	g, v := newShell(t)
	took := 0
	build := g.build
	g.build = func(k *Kit) {
		build(k)
		c := k.Context()
		if c.TakeKey(ui.KeyF10, 0) || c.TakeKey('F', ui.ModAlt) {
			took++
		}
	}
	g.key(ui.KeyF10, 0)
	g.altKey('F')
	g.frames(2)
	g.build = build
	v.dlg = true // nor does the bar behind a dialog
	g.frames(3)
	g.key(ui.KeyF10, 0)
	g.altTap()
	g.altKey('F')
	g.frames(2)
	if took != 2 || g.k.MenuOpen() || g.c.FocusKey() == g.keyOf(ui.RoleMenuItem, "File") {
		t.Fatalf("the component took %d of 2 keys; menu open %v", took, g.k.MenuOpen())
	}
}
