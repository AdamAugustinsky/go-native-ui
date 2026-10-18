package widget

import (
	"testing"

	"gonativeui/ui"
)

// stacked is two dialogs open at once; the one built last lies above.
type stacked struct {
	open      [2]bool
	deleted   [2]int
	cancelled [2]int
	order     [2]int // build order: the dialog built first, second
	disabled  bool   // the primary action of dialog 0 is disabled
	field     ui.TextBuffer
	withField bool
	search    bool // the field is a SearchField
}

func (v *stacked) build(k *Kit) {
	for _, i := range v.order {
		name := [...]string{"first", "second"}[i]
		d := k.BeginDialog(name, "Dialog "+name, &v.open[i])
		if d.Visible {
			if v.withField && i == 0 {
				if v.search {
					k.SearchField("Name", &v.field, "")
				} else {
					k.TextField("Name", &v.field, "")
				}
			}
			d.BeginActions()
			d.Action("Cancel "+name, ButtonDefault)
			k.BeginDisabled(v.disabled && i == 0)
			if d.Action("OK "+name, ButtonPrimary) {
				v.deleted[i]++
			}
			k.EndDisabled()
			d.EndActions()
		}
		k.EndDialog(&d)
		if d.Cancelled {
			v.cancelled[i]++
		}
	}
}

func newStacked(t *testing.T, order [2]int) (*rig, *stacked) {
	v := &stacked{order: order}
	g := newRig(t, 600, 500, v.build)
	v.open = [2]bool{true, true}
	g.frames(6)
	return g, v
}

func TestStackedDialogsOnlyTheTopmostTakesEnterAndEscape(t *testing.T) {
	for _, order := range [][2]int{{0, 1}, {1, 0}} {
		g, v := newStacked(t, order)
		top, low := order[1], order[0]
		g.in.SetButton(ui.ButtonLeft, false, 0)
		// both dialogs have their default action focused on opening; move the
		// focus off the actions so that Enter takes the fallback path
		g.c.SetFocus(0)
		g.key(ui.KeyEnter, 0)
		g.frames(2)
		if v.deleted[top] != 1 || v.deleted[low] != 0 || v.open[top] || !v.open[low] {
			t.Fatalf("order %v: Enter: deleted=%v open=%v, want only dialog %d to act", order, v.deleted, v.open, top)
		}
		// the one beneath is topmost now
		g.c.SetFocus(0)
		g.key(ui.KeyEnter, 0)
		g.frames(2)
		if v.deleted[low] != 1 || v.open[low] {
			t.Fatalf("order %v: second Enter: deleted=%v open=%v", order, v.deleted, v.open)
		}
	}
	for _, order := range [][2]int{{0, 1}, {1, 0}} {
		g, v := newStacked(t, order)
		top, low := order[1], order[0]
		g.key(ui.KeyEscape, 0)
		g.frames(2)
		if v.cancelled[top] != 1 || v.cancelled[low] != 0 || v.open[top] || !v.open[low] {
			t.Fatalf("order %v: Escape: cancelled=%v open=%v, want only dialog %d closed", order, v.cancelled, v.open, top)
		}
		g.key(ui.KeyEscape, 0)
		g.frames(2)
		if v.cancelled[low] != 1 || v.open[low] {
			t.Fatalf("order %v: second Escape: cancelled=%v open=%v", order, v.cancelled, v.open)
		}
	}
}

func TestDisabledDefaultActionNeverTakesEnter(t *testing.T) {
	v := &stacked{order: [2]int{0, 1}, disabled: true}
	g := newRig(t, 600, 500, v.build)
	v.open[0] = true
	g.frames(6)
	g.c.SetFocus(0)
	g.key(ui.KeyEnter, 0)
	g.frames(2)
	if v.deleted[0] != 0 || !v.open[0] {
		t.Fatalf("a disabled default action took Enter: deleted=%d open=%v", v.deleted[0], v.open[0])
	}
	// enabled again, it does
	v.disabled = false
	g.frames(2)
	g.c.SetFocus(0)
	g.key(ui.KeyEnter, 0)
	g.frames(2)
	if v.deleted[0] != 1 {
		t.Fatalf("the enabled default action did not take Enter: %d", v.deleted[0])
	}
}

// Escape in a text field inside a dialog cancels the dialog; during an
// input-method composition it cancels the composition and nothing else.
func TestEscapeInDialogTextFieldCancelsDialog(t *testing.T) {
	v := &stacked{order: [2]int{0, 1}, withField: true}
	g := newRig(t, 600, 500, v.build)
	v.open[0] = true
	g.frames(6)
	g.click(centre(g.must(ui.RoleTextField, "Name")))
	g.typ("abc")
	if !g.c.TextFieldFocused() {
		t.Fatal("the field has no focus")
	}
	// composing: Escape cancels the composition only
	g.in.SetPreedit([]byte("か"), 3, 0)
	g.frames(2)
	g.key(ui.KeyEscape, 0)
	if !g.c.IMECancel() {
		t.Fatal("Escape did not cancel the composition")
	}
	if v.cancelled[0] != 0 || !v.open[0] {
		t.Fatalf("Escape during a composition also cancelled the dialog: %d open=%v", v.cancelled[0], v.open[0])
	}
	g.in.SetPreedit(nil, 0, 0)
	g.frames(2)
	g.key(ui.KeyEscape, 0)
	g.frames(2)
	if v.cancelled[0] != 1 || v.open[0] {
		t.Fatalf("Escape in the field did not cancel the dialog: cancelled=%d open=%v", v.cancelled[0], v.open[0])
	}
	if v.field.String() != "abc" {
		t.Fatalf("Escape changed the text: %q", v.field.String())
	}
}

// Escape in a search field inside a dialog clears the field and goes no
// further; with the field empty it cancels the dialog.
func TestEscapeClearsSearchFieldBeforeCancellingDialog(t *testing.T) {
	v := &stacked{order: [2]int{0, 1}, withField: true, search: true}
	g := newRig(t, 600, 500, v.build)
	v.open[0] = true
	g.frames(6)
	g.click(centre(g.must(ui.RoleSearchField, "Name")))
	g.typ("abc")
	g.key(ui.KeyEscape, 0)
	g.frames(2)
	if v.field.Len() != 0 || v.cancelled[0] != 0 || !v.open[0] {
		t.Fatalf("first Escape: text=%q cancelled=%d open=%v", v.field.String(), v.cancelled[0], v.open[0])
	}
	g.key(ui.KeyEscape, 0)
	g.frames(2)
	if v.cancelled[0] != 1 || v.open[0] {
		t.Fatalf("second Escape: cancelled=%d open=%v", v.cancelled[0], v.open[0])
	}
}
