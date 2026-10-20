package gallery

import (
	"testing"

	"gonativeui/ui"
)

// Select All goes to the list or table of the scene on show: a table that was
// focused in a scene left behind is not rebuilt, so what it had said about the
// keyboard must not outlive it.
func TestSelectAllActsOnTheSceneOnShow(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.goTo(scTable)
	r.Play(click(rowRole, "").Nth(3), idle(2))
	if n := r.g.table.Sel.Count(); n != 1 {
		t.Fatalf("one row of the table was clicked: %d selected", n)
	}
	r.goTo(scPlayground)
	// the list is below the form's fold, and the player scrolls the nearest
	// scrolling container around a target (the list itself): the form first
	r.Play(wheel(ui.RoleScrollArea, "Playground", -60, 12), idle(3), click(ui.RoleListItem, "List item 2"), idle(2))
	if n := r.g.list.Sel.Count(); n != 1 {
		t.Fatalf("one item of the list was clicked: %d selected", n)
	}
	r.Play(key('A', modPrim), idle(2))
	if n := r.g.list.Sel.Count(); n != listItems {
		t.Errorf("Select All in the Playground selected %d of the list's %d items", n, listItems)
	}
	if n := r.g.table.Sel.Count(); n != 1 {
		t.Errorf("Select All in the Playground changed the table that is not on show: %d rows selected, want 1", n)
	}
	// and in the Table it is the table's
	r.goTo(scTable)
	r.Play(click(rowRole, "").Nth(2), key('A', modPrim), idle(2))
	if n := r.g.table.Sel.Count(); n != r.g.rows {
		t.Errorf("Select All in the Table selected %d of %d rows", n, r.g.rows)
	}
	if n := r.g.list.Sel.Count(); n != listItems {
		t.Errorf("Select All in the Table changed the list that is not on show: %d items selected, want %d", n, listItems)
	}
}
