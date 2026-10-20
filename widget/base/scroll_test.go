package base

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// scrollRig builds one vertical scroll view filling a 400×300 window over 40
// rows of 20 px, and keeps its bars and inner box.
func scrollRig(t *testing.T) (*rig, *[2]ScrollBar, *ui.ID) {
	return scrollRigOf(t, &ScrollState{})
}

// scrollRigOf is scrollRig with the view's state sv.
func scrollRigOf(t *testing.T, sv *ScrollState) (*rig, *[2]ScrollBar, *ui.ID) {
	var bars [2]ScrollBar
	var inner ui.ID
	g := newRig(t, 400, 300, 1, func(k *Kit) {
		v := k.BeginScroll("s", sv, ScrollVertical)
		inner = v.Inner
		for i := 0; i < 40; i++ {
			k.Space(20)
		}
		k.CloseScrollContent(&v)
		bars = k.ScrollBars(&v)
		for _, b := range bars {
			if b.On {
				k.BeginScrollBar(&b)
				k.Context().Pop()
			}
		}
		k.CloseScroll(&v)
	})
	return g, &bars, &inner
}

// TestOverlayBarsShowWhileScrolling: overlay bars show while the view
// scrolls, not because the pointer is over it, and fade out afterwards.
func TestOverlayBarsShowWhileScrolling(t *testing.T) {
	g, bars, _ := scrollRig(t)
	g.frames(90)
	b := &bars[1]
	if !b.On || b.Legacy || b.Visible || b.Show != 0 {
		t.Fatalf("an idle overlay bar: %+v", *b)
	}
	g.move(200, 150)
	g.frames(5)
	if b.Visible {
		t.Fatal("the pointer over the view shows the bar")
	}
	g.in.AddScrollEvent(ui.ScrollEvent{DY: -30, Phase: ui.PhaseBegan, Precise: true})
	g.frames(10)
	if !b.Visible || b.Show < 0.5 {
		t.Fatalf("scrolling: show %v", b.Show)
	}
	g.in.AddScrollEvent(ui.ScrollEvent{Phase: ui.PhaseEnded, Precise: true})
	g.frames(90)
	if b.Visible {
		t.Fatalf("a second after scrolling the bar still shows (%v)", b.Show)
	}
}

// TestLegacyBarsReserveTheirWidth: with the system's legacy scrollers the
// bar always shows, beside the content, which is narrower by AppKit's
// legacy scroller width for the bar's control size (17 pt regular, 13 small
// and mini; at 2× here, in a 400×300-pixel window); the knob moves in a slot 3 pt clear of the track's
// ends. The setting is followed live.
func TestLegacyBarsReserveTheirWidth(t *testing.T) {
	legacy := theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}}
	for _, tc := range []struct {
		size theme.Size
		want float32 // pt
	}{{theme.SizeRegular, 17}, {theme.SizeSmall, 13}, {theme.SizeMini, 13}} {
		g, bars, inner := scrollRigOf(t, &ScrollState{Size: tc.size})
		g.scale = 2
		g.k.fol.PinSystem(&legacy)
		g.frames(90)
		b, bw := &bars[1], tc.want*2
		if !b.Legacy || !b.Visible || b.Show != 1 || b.X != 400-bw || b.W != bw || b.H != 300 {
			t.Fatalf("size %d: a legacy bar: %+v, want %v px wide", tc.size, *b, bw)
		}
		if r := g.c.Rect(*inner); r[2]-r[0] != 400-bw {
			t.Fatalf("size %d: the content is %v wide beside a %v bar", tc.size, r[2]-r[0], bw)
		}
		if b.SlotOff != 6 || b.SlotLen != 300-12 || b.ThumbOff != 6 {
			t.Fatalf("size %d: slot %v+%v, thumb at %v; want 6+288, at 6", tc.size, b.SlotOff, b.SlotLen, b.ThumbOff)
		}
		g.k.fol.PinSystem(&theme.SystemValues{Known: true})
		g.frames(3)
		if r := g.c.Rect(*inner); b.Legacy || r[2]-r[0] != 400 {
			t.Fatalf("back to overlay bars: legacy %v, content %v wide", b.Legacy, r[2]-r[0])
		}
	}
}

// TestLegacyThumbDragsToTheEnds: dragging a legacy knob to the end of its
// slot scrolls to the end, and back to its start to the top.
func TestLegacyThumbDragsToTheEnds(t *testing.T) {
	g, bars, inner := scrollRig(t)
	g.k.fol.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}})
	g.frames(4)
	b := bars[1]
	x, y := b.X+b.W/2, b.ThumbOff+b.ThumbLen/2
	end := b.SlotOff + b.SlotLen - b.ThumbLen // the knob's top at the end of the slot
	g.move(x, y)
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.move(x, y+end-b.ThumbOff)
	g.frames(2)
	content, view := g.c.ScrollExtent(*inner)
	if _, sy := g.c.Scroll(*inner); sy != float64(content[1]-view[1]) {
		t.Fatalf("the knob at the slot's end scrolled to %v, want %v", sy, content[1]-view[1])
	}
	g.move(x, y)
	g.frames(2)
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	if _, sy := g.c.Scroll(*inner); sy != 0 {
		t.Fatalf("the knob back at the slot's start scrolled to %v", sy)
	}
}

// TestThumbShrinksWhenStretched: past an edge the thumb is shorter and sits
// against that end.
func TestThumbShrinksWhenStretched(t *testing.T) {
	g, bars, _ := scrollRig(t)
	g.move(200, 150)
	b := &bars[1]
	full := b.ThumbLen
	g.in.AddScrollEvent(ui.ScrollEvent{DY: 200, Phase: ui.PhaseBegan, Precise: true})
	g.frame()
	if b.ThumbLen >= full || b.ThumbOff != 0 {
		t.Fatalf("stretched past the top: thumb %v at %v (unstretched %v)", b.ThumbLen, b.ThumbOff, full)
	}
	g.in.AddScrollEvent(ui.ScrollEvent{Phase: ui.PhaseEnded, Precise: true})
	g.frames(60)
	if b.ThumbLen != full {
		t.Fatalf("after the spring the thumb is %v, want %v", b.ThumbLen, full)
	}
}

// listRig builds a 200-row list filling a 400×300 window, with its bar (a
// track and a thumb, as widget's recipe builds them).
func listRig(t *testing.T) (*rig, *ListState, *[200]RowState, *[2]ScrollBar, *ui.ID) {
	var st ListState
	var rs [200]RowState
	var bars [2]ScrollBar
	var list ui.ID
	g := newRig(t, 400, 300, 1, func(k *Kit) {
		c := k.Context()
		l := k.BeginList("rows", &st, 200, ListOpts{})
		list = l.s
		for r := l.First; r < l.Last; r++ {
			rs[r] = l.RowState(r, uint32(r))
			l.BeginRow(&rs[r], nil, 0)
		}
		l.CloseRows()
		v := l.ScrollView()
		bars = k.ScrollBars(&v)
		for _, b := range bars {
			if b.On {
				k.BeginScrollBar(&b)
				theme.At(c, 0, b.ThumbOff, b.W, b.ThumbLen)
				c.BoxKeyed(ui.DrawBackground|Floating, ui.KeyN(b.Key, 1), nil)
				c.Pop()
			}
		}
		k.CloseList(&l)
	})
	return g, &st, &rs, &bars, &list
}

// TestLegacyListBarCanBeDragged: a list leaves room for a legacy bar inside
// its clip, where the pointer can take it and drag the thumb.
func TestLegacyListBarCanBeDragged(t *testing.T) {
	g, _, _, bars, list := listRig(t)
	g.k.fol.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}})
	g.frames(4)
	b := &bars[1]
	if !b.On || !b.Legacy || b.X+b.W > 400 {
		t.Fatalf("the legacy bar: %+v (the list is 400 wide)", *b)
	}
	x, y := b.X+b.W/2, b.Y+b.ThumbOff+b.ThumbLen/2
	g.move(x, y)
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.move(x, y+100)
	g.frames(2)
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	if _, sy := g.c.Scroll(*list); sy <= 0 {
		t.Fatalf("dragging the thumb scrolled to %v", sy)
	}
}

// TestStretchedListPicksTheRowShown: while a trackpad stretches a list past
// its top, hover and a click find the row shown under the pointer.
func TestStretchedListPicksTheRowShown(t *testing.T) {
	g, st, rs, _, list := listRig(t)
	g.move(200, 150)
	g.in.AddScrollEvent(ui.ScrollEvent{Phase: ui.PhaseMayBegin, Precise: true})
	g.frame()
	for i := 0; i < 30; i++ {
		p := ui.PhaseChanged
		if i == 0 {
			p = ui.PhaseBegan
		}
		g.in.AddScrollEvent(ui.ScrollEvent{DY: 40, Phase: p, Precise: true})
		g.frame()
	}
	_, s := g.c.ScrollStretch(*list)
	if -s < 2*float64(st.RowPx()) {
		t.Fatalf("stretch %v: not past two rows", s)
	}
	r := g.c.Rect(*list)
	y := r[1] + st.PadPx() + 5*st.RowPx() + st.RowPx()/2 - float32(s) // row 5 as shown
	g.click(200, y)
	if !rs[5].Selected {
		for i := 0; i < 10; i++ {
			if rs[i].Selected {
				t.Fatalf("clicked row 5 as shown, selected row %d", i)
			}
		}
		t.Fatal("clicked row 5 as shown, nothing selected")
	}
}

// TestListRevealDuringAStretch: Reveal of a row that the offset shows but a
// trackpad's stretch past the top pushes out of the view brings it back in.
func TestListRevealDuringAStretch(t *testing.T) {
	g, st, _, _, list := listRig(t)
	g.move(200, 150)
	g.in.AddScrollEvent(ui.ScrollEvent{Phase: ui.PhaseMayBegin, Precise: true})
	g.frame()
	for i := 0; i < 30; i++ {
		p := ui.PhaseChanged
		if i == 0 {
			p = ui.PhaseBegan
		}
		g.in.AddScrollEvent(ui.ScrollEvent{DY: 40, Phase: p, Precise: true})
		g.frame()
	}
	r := g.c.Rect(*list)
	view := r[3] - r[1]
	row := int((view-st.PadPx())/st.RowPx()) - 1 // the last row the offset shows whole
	if b := st.RowTop(row) + st.RowPx(); b <= view {
		t.Fatalf("row %d ends at %v in a %v view: the stretch does not hide it", row, b, view)
	}
	st.Reveal(row)
	g.frames(30) // the fingers stay down
	if top := st.RowTop(row); top < 0 || top+st.RowPx() > view+0.5 {
		t.Fatalf("row %d shows at %v–%v in a %v view", row, top, top+st.RowPx(), view)
	}
}

// TestListScrollingAllocatesNothing: once warm, a list's and a VList's
// scrolling frames allocate nothing: a trackpad pull past the edge and its
// spring, a wheel at another point, a frame with more events than it holds,
// a Reveal, and a prepend while stretched.
func TestListScrollingAllocatesNothing(t *testing.T) {
	g, st, _, _, _ := listRig(t)
	g.move(200, 150)
	ev := func(e ui.ScrollEvent) { e.Precise, e.X, e.Y, e.Pos = true, 200, 150, true; g.in.AddScrollEvent(e) }
	list := func() {
		ev(ui.ScrollEvent{Phase: ui.PhaseMayBegin})
		g.frame()
		ev(ui.ScrollEvent{DY: 40, Phase: ui.PhaseBegan})
		g.frame()
		for i := 0; i < ui.MaxScrolls+10; i++ {
			ev(ui.ScrollEvent{DY: 3, Phase: ui.PhaseChanged})
		}
		g.frame()
		st.Reveal(5)
		g.frame()
		ev(ui.ScrollEvent{Phase: ui.PhaseEnded})
		g.in.AddScrollEvent(ui.ScrollEvent{DY: -2, X: 100, Y: 50, Pos: true}) // a wheel
		g.frames(40)
		g.in.AddScrollEvent(ui.ScrollEvent{DY: 2, X: 100, Y: 50, Pos: true})
		g.frames(40)
	}
	list()
	if n := testing.AllocsPerRun(5, list); n != 0 {
		t.Errorf("list: %v allocations per run", n)
	}

	h := newVT(t, 2000, VListOpts{})
	h.st.Reserve(2100)
	h.frames(3)
	ids, next := seq(1, 100), 0
	prepend := func() { h.st.Prepend(ids[next : next+1]); next++ }
	vlist := func() {
		h.pull(60, 6)
		h.edit = prepend
		h.frame()
		h.release()
	}
	vlist()
	if n := testing.AllocsPerRun(5, vlist); n != 0 {
		t.Errorf("vlist: %v allocations per run", n)
	}
}

// TestTableHeaderLeavesTheCorner: beside a legacy scroll bar a table's
// header stops at the bar's left edge, and the corner above the bar (as high
// as the header) is a box of its own, as AppKit's corner view; the Fill
// column narrows so that the columns end there too. With overlay bars the
// header spans the table.
func TestTableHeaderLeavesTheCorner(t *testing.T) {
	var st TableState
	cols := []Column{{ID: 1, Title: "Name", Width: 170, Fill: true}, {ID: 2, Title: "Size", Width: 20}} // 190 pt: wider than the rows beside the bar
	var head TableHead
	var bars [2]ScrollBar
	var outer ui.ID
	g := newRig(t, 400, 300, 2, func(k *Kit) {
		head = k.BeginTableHeader("files", &st, cols)
		head.BeginHeaderRow(0)
		for i := range head.Columns() {
			hc := head.HeaderCell(i)
			head.BeginHeaderCell(&hc, 0)
			head.EndHeaderCell(&hc)
		}
		head.EndHeaderRow()
		l := k.BeginTableBody(&head, 100, ListOpts{})
		for r := l.First; r < l.Last; r++ {
			rs := l.RowState(r, uint32(r))
			l.BeginRow(&rs, nil, 0)
			head.TextCell(&l, 0, StrBytes("name"), ui.DrawText)
		}
		l.CloseRows()
		v := l.ScrollView()
		outer = v.Outer
		bars = k.ScrollBars(&v)
		k.CloseList(&l)
		k.CloseTable(&head)
	})
	g.k.fol.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}})
	g.frames(4)
	b := bars[1]
	table, row := g.c.Rect(head.root), g.c.RectOf(head.hkey)
	corner := g.c.RectOf(ui.KeyN(head.tkey, 4))
	barX := g.c.Rect(outer)[0] + b.X
	if !b.Legacy || head.Corner() != 34 || b.W != 34 {
		t.Fatalf("corner %v, bar %+v; want both 34 px (17 pt)", head.Corner(), b)
	}
	if row[2] != barX || corner != [4]float32{barX, table[1], table[2], table[1] + head.HeaderHeight()} {
		t.Fatalf("the header ends at %v and the corner is %v; the bar is at %v, the table %v", row[2], corner, barX, table)
	}
	// the Fill column gives up room (as AppKit's last-column autoresizing)
	// so that the columns end where the rows do, at the bar
	if w := head.ColumnWidth(0) + head.ColumnWidth(1) + 2*head.Inset(); w != barX-table[0] {
		t.Fatalf("the columns take %v px beside the bar, the rows %v", w, barX-table[0])
	}
	g.k.fol.PinSystem(&theme.SystemValues{Known: true})
	g.frames(3)
	if row = g.c.RectOf(head.hkey); head.Corner() != 0 || row[2] != table[2] {
		t.Fatalf("overlay bars: corner %v, the header ends at %v of %v", head.Corner(), row[2], table[2])
	}
}
