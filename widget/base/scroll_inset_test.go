package base

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// A view inset under a toolbar's band starts its content below the inset at
// rest, scrolls it up under it, and starts its vertical bar below it; the
// range to scroll is the content's.
func TestScrollInsetContentAndBar(t *testing.T) {
	bars, v := new([2]ScrollBar), new(ScrollView)
	st := &ScrollState{}
	g := newRig(t, 400, 300, 2, func(k *Kit) {
		k.NextScrollInset(104)
		*v = k.BeginScroll("s", st, ScrollVertical)
		c := k.Context()
		c.NextSize(ui.Px(100), ui.Px(2000))
		c.BoxKeyed(0, ui.HashString(c.ParentKey(), "content"), nil)
		k.CloseScrollContent(v)
		*bars = k.ScrollBars(v)
		k.CloseScroll(v)
	})
	pinScroller(g, theme.ScrollerLegacy)
	g.frames(3)
	content := func() float32 {
		id, _ := g.c.BoxOf(ui.HashString(g.c.KeyOf(v.Inner), "content"))
		return g.c.Rect(id)[1]
	}
	if v.TopInset != 104 || content() != 104 {
		t.Fatalf("at rest the content starts at %v (inset %v), want 104", content(), v.TopInset)
	}
	b := &bars[1]
	if !b.On || b.Y != 104 || b.H != 300-104 || b.Max != 2000+104-300 {
		t.Fatalf("the bar %+v: want it from 104, 196 long, scrolling 1804", *b)
	}
	g.c.SetScroll(v.Inner, 0, 150)
	g.frames(2)
	if y := content(); y != 104-150 {
		t.Fatalf("scrolled 150: the content starts at %v, want -46 (under the inset)", y)
	}
}

// A list inset under a band builds the rows scrolled up under the inset, and
// revealing a row above the view brings it below the inset, not under it.
func TestListInsetRowsUnderAndReveal(t *testing.T) {
	var st ListState
	var first int
	const inset = 80
	g := newRig(t, 400, 300, 1, func(k *Kit) {
		k.NextScrollInset(inset)
		l := k.BeginList("rows", &st, 200, ListOpts{})
		first = l.First
		for r := l.First; r < l.Last; r++ {
			rs := l.RowState(r, uint32(r))
			l.BeginRow(&rs, nil, 0)
		}
		l.CloseRows()
		k.CloseList(&l)
	})
	g.frames(2)
	pad := st.PadPx()
	rowH := st.RowTop(1) - st.RowTop(0)
	st.SetTopRow(20, 0) // row 20 at the inset's end, rows 16.. under the band
	g.frames(2)
	if top := st.RowTop(20); top != pad {
		t.Fatalf("SetTopRow(20): row 20 at %v, want %v (below the inset)", top, pad)
	}
	under := 20 - int(inset/rowH) - 1
	if first > under {
		t.Fatalf("the first row built is %d: the rows under the band from %d are missing", first, under)
	}
	st.Reveal(10)
	g.frames(30)
	if top := st.RowTop(10); top < inset-0.5 || top > inset+0.5+pad-inset {
		t.Fatalf("Reveal(10): row 10 at %v, want it just below the inset (%v)", top, inset)
	}
}
