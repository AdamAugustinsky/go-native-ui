package gallery

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// rowNodes returns the table's row nodes of the last frame.
func (r *rig) rowNodes() []ui.SemNode {
	var rows []ui.SemNode
	for _, n := range r.Tree().Nodes {
		if n.Role == ui.RoleRow {
			rows = append(rows, n)
		}
	}
	return rows
}

// A million rows are not a million nodes: the table builds the visible ones,
// and each reports its place in the whole.
func TestVirtualRowsReportTheirPosition(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)
	rows := r.rowNodes()
	if len(rows) < 10 || len(rows) > 80 {
		t.Fatalf("%d row nodes in a table of a million rows, want only the visible ones", len(rows))
	}
	for i, n := range rows {
		if n.Pos != [2]int32{int32(i), 1_000_000} {
			t.Fatalf("row node %d reports position %v, want {%d 1000000}", i, n.Pos, i)
		}
	}
	// to the end: the last visible row is the last of the million
	r.focusOn(ui.RoleTable, "rows")
	r.press(ui.KeyEnd, 0)
	r.Frames(150)
	rows = r.rowNodes()
	last := rows[len(rows)-1]
	if last.Pos != [2]int32{999_999, 1_000_000} {
		t.Errorf("after End the last visible row reports %v, want {999999 1000000}", last.Pos)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Pos[0] != rows[i-1].Pos[0]+1 {
			t.Fatalf("visible rows %d and %d are not consecutive: %v, %v", i-1, i, rows[i-1].Pos, rows[i].Pos)
		}
	}
	// the labels agree with the positions
	if _, ok := r.Find(ui.RoleLabel, "item-999999"); !ok {
		t.Error("the last row's label is not built after End")
	}
	// and back to the top
	r.press(ui.KeyHome, 0)
	r.Frames(150)
	if first := r.rowNodes()[0]; first.Pos[0] != 0 {
		t.Errorf("after Home the first visible row reports %v, want position 0", first.Pos)
	}
}

// The selection of the table is on its rows, and a descending sort re-orders
// the rows under it.
func TestTableSelectionAndSortAreExported(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)
	r.Play(click(rowRole, "").Nth(2), click(rowRole, "").Nth(4).With(modShift), idle(2))
	var sel []int32
	for _, n := range r.rowNodes() {
		if n.State&ui.SemSelected != 0 {
			sel = append(sel, n.Pos[0])
		}
	}
	if len(sel) != 3 || sel[0] != 2 || sel[2] != 4 {
		t.Fatalf("selected rows %v after a click and a Shift+click, want 2, 3, 4", sel)
	}
	r.Play(click(ui.RoleColumnHeader, "ID"), idle(3))
	if _, ok := r.Find(ui.RoleLabel, "item-999999"); !ok {
		t.Error("sorting by ID descending does not show the last item first")
	}
	var again []int32
	for _, n := range r.rowNodes() {
		if n.State&ui.SemSelected != 0 {
			again = append(again, n.Pos[0])
		}
	}
	if len(again) != 0 {
		t.Errorf("rows at %v are selected at the top of the descending order, but the selected items are 2, 3, 4", again)
	}
}

// Values and states the controls export, set with the pointer.
func TestControlsExportTheirValuesAndStates(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)

	vol := r.Must(ui.RoleSlider, "Volume")
	if vol.Range != [4]float64{0, 100, 40, 1} {
		t.Fatalf("Volume exports %v, want {0 100 40 1}", vol.Range)
	}
	r.Play(click(ui.RoleSlider, "Volume"))
	r.Frames(2)
	n := r.Must(ui.RoleSlider, "Volume")
	if v := n.Range[2]; v < 40 || v > 60 { // the middle of the track
		t.Errorf("a click in the middle of the slider set %v, want about 50", v)
	}
	if got := float64(r.g.slider); got != n.Range[2] {
		t.Errorf("the app's value %v and the exported %v differ", got, n.Range[2])
	}

	if r.state(ui.RoleCheckbox, "Enable feature")&ui.SemChecked == 0 {
		t.Fatal("Enable feature starts unchecked")
	}
	r.Play(click(ui.RoleCheckbox, "Enable feature"), idle(2))
	if r.state(ui.RoleCheckbox, "Enable feature")&ui.SemChecked != 0 {
		t.Error("a click did not uncheck the checkbox")
	}
	// "All options" shows what the two options add up to: with the feature off
	// and the notifications off it is neither checked nor mixed
	if s := r.state(ui.RoleCheckbox, "All options"); s&(ui.SemMixed|ui.SemChecked) != 0 {
		t.Errorf("All options has state %b with both options off, want neither checked nor mixed", s)
	}
	r.Play(click(ui.RoleCheckbox, "All options"), idle(3))
	if !r.g.check || !r.g.sw {
		t.Errorf("a click on All options left the feature %v and the notifications %v", r.g.check, r.g.sw)
	}
	if r.state(ui.RoleCheckbox, "All options")&ui.SemChecked == 0 || r.state(ui.RoleSwitch, "Notifications")&ui.SemChecked == 0 {
		t.Error("the semantic tree does not show both options on")
	}
	r.Play(click(ui.RoleRadio, "Three"), idle(2))
	if r.state(ui.RoleRadio, "Three")&ui.SemChecked == 0 || r.state(ui.RoleRadio, "One")&ui.SemChecked != 0 {
		t.Error("clicking Three did not move the choice from One to Three")
	}
	r.Play(click(ui.RoleTab, "History"), idle(2))
	if r.state(ui.RoleTab, "History")&ui.SemSelected == 0 {
		t.Error("clicking History did not select the tab")
	}
	if _, ok := r.Find(ui.RoleLabel, "What happened to it."); !ok {
		t.Error("the History tab's text is not shown")
	}
	// disabled widgets say so
	disabled := 0
	for _, nd := range r.Tree().Nodes {
		if nd.State&ui.SemDisabled != 0 {
			disabled++
		}
	}
	if disabled != 11 { // the Disabled column of the state matrix: one widget per row
		t.Errorf("%d disabled nodes, want the 11 of the matrix's Disabled column", disabled)
	}
}

// The dialog is modal, and its actions reach the app.
func TestDialogIsModalAndItsActionsReachTheApp(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)
	g := r.g
	selectRows := func() {
		r.Play(click(rowRole, "").Nth(2), click(rowRole, "").Nth(4).With(modShift), idle(2))
		if g.table.Sel.Count() != 3 {
			t.Fatalf("%d rows selected, want 3", g.table.Sel.Count())
		}
	}
	open := func() {
		r.Play(click(roleButton, "Delete…"), waitFor(ui.RoleDialog, ""), settle(), idle(2))
	}
	selectRows()
	open()

	d, ok := r.Find(ui.RoleDialog, "Delete the selected rows?")
	if !ok || d.State&ui.SemModal == 0 {
		t.Fatalf("the dialog is %v (found %v), want a modal node", d.State, ok)
	}
	if n := r.Must(ui.RoleButton, "Delete"); n.State&ui.SemFocused == 0 {
		t.Error("the default action does not have the focus when the dialog opens")
	}

	// what is beneath it is inert: pointer, keys and shortcuts
	r.Play(click(ui.RoleCheckbox, "Enable feature"), idle(2))
	if !g.check {
		t.Error("a click on the checkbox beneath the dialog went through")
	}
	r.press('D', ui.PrimaryMod()|ui.ModShift)
	if g.Mode() != theme.Light {
		t.Error("a menu shortcut worked behind the dialog")
	}
	for i := 0; i < 12; i++ { // Tab stays inside the dialog
		r.press(ui.KeyTab, 0)
		n, ok := r.Focused()
		if !ok || (n.Role != ui.RoleButton || (string(r.Tree().Text(n.Name)) != "Cancel" && string(r.Tree().Text(n.Name)) != "Delete")) {
			t.Fatalf("Tab moved the focus out of the dialog to role %d %q (found %v)", n.Role, r.Tree().Text(n.Name), ok)
		}
	}
	// Cancel: the rows stay, the dialog closes
	r.Play(click(ui.RoleButton, "Cancel"), waitGone(ui.RoleDialog, ""), idle(2))
	if g.table.Sel.Count() != 3 {
		t.Errorf("Cancel changed the selection to %d rows", g.table.Sel.Count())
	}
	// Escape cancels as well
	open()
	r.press(ui.KeyEscape, 0)
	r.Frames(30)
	if _, ok := r.Find(ui.RoleDialog, ""); ok {
		t.Error("Escape did not close the dialog")
	}
	if g.table.Sel.Count() != 3 {
		t.Errorf("Escape changed the selection to %d rows", g.table.Sel.Count())
	}
	// Delete takes the action
	open()
	r.Play(click(ui.RoleButton, "Delete"), waitGone(ui.RoleDialog, ""), idle(2))
	if g.table.Sel.Count() != 0 {
		t.Errorf("Delete left %d rows selected", g.table.Sel.Count())
	}
	if string(g.status) != "Deleted" {
		t.Errorf("the status is %q, want Deleted", g.status)
	}
	// and the window works again
	r.Play(click(ui.RoleCheckbox, "Enable feature"), idle(2))
	if g.check {
		t.Error("the checkbox does not respond after the dialog closed")
	}
}

// The context menu of a row acts on that row, and choosing from it reaches the
// app's command.
func TestRowContextMenu(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)
	r.Play(rclick(rowRole, "").Nth(2), waitFor(ui.RoleMenuItem, "Copy Path"), idle(2))
	if r.g.table.Sel.Count() != 1 {
		t.Errorf("a right-click selected %d rows, want the one under the pointer", r.g.table.Sel.Count())
	}
	r.Play(click(ui.RoleMenuItem, "Copy Path"), waitGone(ui.RoleMenuItem, "Copy Path"), idle(2))
	if got := string(r.g.status); got != "Chose “Copy Path”" {
		t.Errorf("the status is %q after choosing Copy Path", got)
	}
}

// Cycling the appearance goes System, Light, Dark and round, and a change of
// the mode crossfades: the palette is in between for some frames, then settled.
func TestAppearanceCycleCrossfades(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)
	r.press('D', ui.PrimaryMod()|ui.ModShift) // System (light here) to forced light: no change of mode
	if r.g.Preference() != theme.ForceLight || r.Context().Animating() {
		t.Fatalf("the first press gives the preference %d, animating %v", r.g.Preference(), r.Context().Animating())
	}
	r.press('D', ui.PrimaryMod()|ui.ModShift)
	if r.g.Mode() != theme.Dark {
		t.Fatal("the second press did not switch to the dark mode")
	}
	if !r.Context().Animating() {
		t.Error("the mode switch does not animate")
	}
	r.Play(settle())
	if r.Context().Animating() {
		t.Error("the crossfade never settled")
	}
	r.press('D', ui.PrimaryMod()|ui.ModShift)
	if r.g.Preference() != theme.System || r.g.Mode() != theme.Light {
		t.Errorf("the third press gives the preference %d and the mode %v, want System and light", r.g.Preference(), r.g.Mode())
	}
}

// Once the buffers have grown, a frame of the gallery allocates nothing, in
// the quiet window and with each kind of popup open (the benchmark measures the
// same with the real text system and the whole session).
func TestSteadyFramesAllocateNothing(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(30)
	check := func(name string) {
		t.Helper()
		r.Frames(40) // animations done, caches warm
		if n := testing.AllocsPerRun(50, r.Frame); n != 0 {
			t.Errorf("%s: %v allocations per frame, want 0", name, n)
		}
	}
	check("idle")
	r.Play(click(ui.RoleMenuItem, "File"), waitFor(ui.RoleMenuItem, "Open Recent"), click(ui.RoleMenuItem, "Open Recent"), idle(2))
	check("menu and submenu open")
	r.press(ui.KeyEscape, 0)
	r.press(ui.KeyEscape, 0)
	r.Play(click(ui.RoleComboBox, "Format"), waitFor(ui.RoleListItem, "HTML"), idle(2))
	check("select open")
	r.press(ui.KeyEscape, 0)
	r.Play(move(roleButton, "Open…"), waitFor(ui.RoleTooltip, ""), idle(2))
	check("tooltip showing")
	r.Play(move(roleButton, "Save"), idle(2), click(rowRole, "").Nth(3), click(rowRole, "").Nth(6).With(modShift), idle(2))
	check("rows selected")
	r.Play(click(roleButton, "Delete…"), waitFor(ui.RoleDialog, ""), settle(), idle(2))
	check("dialog open")
}

// With native menus (macOS) the gallery draws no menu bar, and its frames
// allocate nothing either, also in the frames that ask for a native menu
// and that take its choice.
func TestNativeMenuFramesAllocateNothing(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Context().SetNativeMenus(true)
	r.Frames(40)
	if n := testing.AllocsPerRun(50, r.Frame); n != 0 {
		t.Errorf("idle: %v allocations per frame, want 0", n)
	}
	var req ui.MenuRequest
	ok := false
	for i := 0; i < 3 && !ok; i++ { // the first request grows the options' copy
		r.Play(click(ui.RoleComboBox, "Format"), idle(1))
		req, ok = r.Context().TakeMenuRequest()
	}
	if !ok || req.Kind != ui.MenuList {
		t.Fatalf("the select asked for %+v %v", req, ok)
	}
	steps := []step{click(ui.RoleComboBox, "Format"), idle(1)}
	n := testing.AllocsPerRun(20, func() {
		r.Play(steps...)
		r.Context().TakeMenuRequest()
	})
	if n != 0 {
		t.Errorf("a frame asking for the select's menu: %v allocations, want 0", n)
	}
}
