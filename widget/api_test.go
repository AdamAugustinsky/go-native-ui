package widget

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/theme"
	"gonativeui/ui"
)

// The smallest app, with no theme import and no floating box of its own: the
// background, then the widgets in flow layout over it.
func TestBeginWindowDrawsTheContentOverTheBackground(t *testing.T) {
	var clicked int
	g := newRig(t, 400, 300, func(k *Kit) {
		k.BeginWindow()
		k.Label("Hello")
		if k.Button("Go").Clicked {
			clicked++
		}
		k.Switch("Fast", new(bool))
		k.EndWindow()
	})
	g.frames(3)
	n := g.must(ui.RoleButton, "Go")
	if n.Bounds[2] > 400 || n.Bounds[3] > 300 || n.Bounds[0] <= 0 || n.Bounds[1] <= 0 {
		t.Fatalf("the button is at %v: not inside the padded window", n.Bounds)
	}
	l := g.must(ui.RoleLabel, "Hello")
	if !(l.Bounds[3] <= n.Bounds[1]) {
		t.Errorf("the label %v is not above the button %v: the content did not flow", l.Bounds, n.Bounds)
	}
	if !backgroundBeforeContent(g.list.Quads, 400, 300) {
		t.Error("the content is drawn before the whole background: the background is above it")
	}
	g.clickOn(ui.RoleButton, "Go")
	if clicked != 1 {
		t.Errorf("the button was clicked %d times, want 1", clicked)
	}
}

// backgroundBeforeContent reports whether the opaque tiles of the window
// background, drawn before the first glyph of the frame, cover the window: the
// content comes after the background.
func backgroundBeforeContent(qs []gpu.Quad, w, h float32) bool {
	var area float32
	for _, q := range qs {
		if q.Mode == gpu.ModeMaskR8 {
			break
		}
		if q.Mode == gpu.ModeShape && q.Border == 0 && q.Soft == 0 && q.Color[0]&0xFF == 0xFF {
			area += (q.Rect[2] - q.Rect[0]) * (q.Rect[3] - q.Rect[1])
		}
	}
	return area >= 0.99*w*h
}

// BeginWindow is absolute placement's companion, not its replacement: NextSize
// and NextPad change the window's box, and a floating box of the app's own
// still goes where it is told.
func TestBeginWindowTakesNextPadAndFloatingChildren(t *testing.T) {
	var pinned ui.ID
	g := newRig(t, 400, 300, func(k *Kit) {
		k.NextPad(40)
		k.BeginWindow()
		k.Label("A")
		theme.At(k.Context(), 300, 200, 50, 20)
		pinned = k.Context().Box(theme.Floating, "pinned")
		k.EndWindow()
	})
	g.frames(3)
	a := g.must(ui.RoleLabel, "A")
	if a.Bounds[0] != 40 || a.Bounds[1] != 40 {
		t.Errorf("with NextPad(40) the first label is at %v, want (40, 40)", a.Bounds[:2])
	}
	if r := g.c.Rect(pinned); r[0] != 300 || r[1] != 200 {
		t.Errorf("the floating box is at %v, want (300, 200)", r[:2])
	}
}

// The kit's context is the window's before the first frame and after End.
func TestKitContextOutlivesEnd(t *testing.T) {
	g := newRig(t, 200, 100, func(k *Kit) { k.BeginWindow(); k.EndWindow() })
	g.frames(2)
	if g.k.Context() != g.c {
		t.Error("the kit lost its context after End")
	}
}

// A text field reports its key, which lasts across frames, and Focus moves the
// keyboard there.
func TestFieldKeyAndFocus(t *testing.T) {
	var a, b ui.TextBuffer
	var fa, fb FieldSignal
	g := newRig(t, 400, 200, func(k *Kit) {
		k.BeginWindow()
		fa = k.TextField("a", &a, "")
		fb = k.TextField("b", &b, "")
		k.EndWindow()
	})
	g.frames(2)
	if fa.Key == 0 || fa.Key == fb.Key {
		t.Fatalf("the fields report keys %v and %v", fa.Key, fb.Key)
	}
	keyB := fb.Key
	g.k.Focus(keyB)
	g.frames(3)
	if g.c.FocusKey() != keyB {
		t.Fatalf("the focus is on %v after Focus(%v)", g.c.FocusKey(), keyB)
	}
	g.typ("xyz")
	if b.String() != "xyz" || a.String() != "" {
		t.Errorf("typing after Focus: a=%q b=%q", a.String(), b.String())
	}
	if fb.Key != keyB {
		t.Error("the key of a field changed between frames")
	}
}

// ListState.Item is the identity without an Order, so an app need not keep one.
func TestListStateItem(t *testing.T) {
	var st ListState
	if st.Item(7) != 7 {
		t.Errorf("Item(7) = %d without an Order", st.Item(7))
	}
	st.Order = []uint32{5, 3, 9}
	if st.Item(0) != 5 || st.Item(2) != 9 {
		t.Errorf("Item with an Order of %v: %d, %d", st.Order, st.Item(0), st.Item(2))
	}
	var ts TableState
	if ts.Item(4) != 4 {
		t.Error("a TableState has no Item")
	}
}

// Rows have an accessible name, so that assistive technology and tests find a
// row by its text: RowNamed gives it in a list, the first cell in a table.
func TestRowsHaveAccessibleNames(t *testing.T) {
	var ls ListState
	var ts TableState
	names := [...]string{"alpha", "beta", "gamma"}
	cols := []Column{{ID: 1, Title: "Name", Width: 100, Fill: true}, {ID: 2, Title: "Kind", Width: 60}}
	g := newRig(t, 400, 400, func(k *Kit) {
		k.BeginWindow()
		k.NextHeight(k.Fixed(120))
		l := k.BeginList("list", &ls, len(names))
		for r := l.First; r < l.Last; r++ {
			l.RowNamed(r, ls.Item(r), []byte(names[r]))
			k.NextWidth(Fill)
			k.LabelBytes([]byte(names[r]))
		}
		k.EndList(&l)
		k.NextHeight(k.Fixed(160))
		tb := k.BeginTable("table", &ts, cols, len(names))
		for r := tb.First; r < tb.Last; r++ {
			tb.Row(r, ts.Item(r))
			tb.CellText(0, names[r])
			tb.CellText(1, "kind")
		}
		k.EndTable(&tb)
		k.EndWindow()
	})
	g.frames(3)
	for _, n := range names {
		li, ok := g.find(ui.RoleListItem, n)
		if !ok {
			t.Errorf("no list item named %q", n)
			continue
		}
		if li.Pos[1] != int32(len(names)) {
			t.Errorf("list item %q reports position %v", n, li.Pos)
		}
		if _, ok := g.find(ui.RoleRow, n); !ok {
			t.Errorf("no table row named %q (the first cell names it)", n)
		}
	}
	if _, ok := g.find(ui.RoleRow, "kind"); ok {
		t.Error("a table row is named after its second cell")
	}
	// the name is the way in: click the row of "beta"
	g.clickOn(ui.RoleListItem, "beta")
	if !ls.Sel.Has(1) || ls.Sel.Count() != 1 {
		t.Errorf("clicking the list item named beta selected %d items (has 1: %v)", ls.Sel.Count(), ls.Sel.Has(1))
	}
}

// Two LabelBytes in one container are two boxes, with their own text, and the
// same two in the next frame, whatever their text becomes.
func TestTwoLabelBytesInOneContainer(t *testing.T) {
	text := [2]string{"first", "second"}
	var keys [2]ui.Key
	g := newRig(t, 400, 200, func(k *Kit) {
		k.BeginWindow()
		for i := range text {
			keys[i] = k.Context().KeyOf(k.LabelBytes([]byte(text[i])))
		}
		k.EndWindow()
	})
	g.frames(2)
	first := keys
	if first[0] == first[1] {
		t.Fatalf("two LabelBytes share the key %v", first[0])
	}
	a, b := g.must(ui.RoleLabel, "first"), g.must(ui.RoleLabel, "second")
	if a.Key != first[0] || b.Key != first[1] || a.Bounds[1] >= b.Bounds[1] {
		t.Errorf("the labels are %v and %v", a, b)
	}
	text = [2]string{"one", "two"}
	g.frames(2)
	if keys != first {
		t.Errorf("the keys changed with the text: %v, then %v", first, keys)
	}
	if g.must(ui.RoleLabel, "one").Key != first[0] || g.must(ui.RoleLabel, "two").Key != first[1] {
		t.Error("a label lost its identity when its text changed")
	}
}

// A Row centres its children across, so a switch and a button of different
// heights sit on one line; NextCross says otherwise, and a Column starts them.
func TestRowCentresItsChildren(t *testing.T) {
	var mode ui.Cross
	var set bool
	g := newRig(t, 600, 300, func(k *Kit) {
		k.BeginWindow()
		if set {
			k.NextCross(mode)
		}
		k.Row("r")
		k.Switch("Sw", new(bool))
		k.Button("A button")
		k.NextHeight(k.Fixed(60))
		k.Label("tall")
		k.EndRow()
		k.EndWindow()
	})
	g.frames(3)
	sw, bt := g.must(ui.RoleSwitch, "Sw"), g.must(ui.RoleButton, "A button")
	if sw.Bounds[3]-sw.Bounds[1] == bt.Bounds[3]-bt.Bounds[1] {
		t.Skip("the switch and the button are of one height: nothing to tell")
	}
	mid := func(n ui.SemNode) float32 { return (n.Bounds[1] + n.Bounds[3]) / 2 }
	if d := mid(sw) - mid(bt); d > 1 || d < -1 {
		t.Errorf("the centres of the switch (%v) and the button (%v) differ by %v px", mid(sw), mid(bt), d)
	}
	set, mode = true, ui.CrossStart
	g.frames(2)
	sw, bt = g.must(ui.RoleSwitch, "Sw"), g.must(ui.RoleButton, "A button")
	if sw.Bounds[1] != bt.Bounds[1] {
		t.Errorf("with CrossStart the tops are %v and %v", sw.Bounds[1], bt.Bounds[1])
	}
}
