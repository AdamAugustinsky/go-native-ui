package base

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
)

// styleRig builds a scroll view on axes filling a 400×300-pixel window at
// 2×, over content cw × ch pixels, and keeps its bars and boxes.
func styleRig(t *testing.T, axes ScrollAxes, cw, ch float32) (g *rig, bars *[2]ScrollBar, v *ScrollView) {
	bars, v = new([2]ScrollBar), new(ScrollView)
	st := &ScrollState{}
	g = newRig(t, 400, 300, 2, func(k *Kit) {
		*v = k.BeginScroll("s", st, axes)
		c := k.Context()
		c.NextSize(ui.Px(cw), ui.Px(ch))
		c.BoxKeyed(0, ui.HashString(c.ParentKey(), "content"), nil)
		k.CloseScrollContent(v)
		*bars = k.ScrollBars(v)
		k.CloseScroll(v)
	})
	g.scale = 2
	return g, bars, v
}

func pinScroller(g *rig, s theme.ScrollerStyle) {
	g.k.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: s}})
}

// A change of the system's scroller style lays the view out again in the
// frame after it: the content narrows by the legacy bar (17 pt) and widens
// back with overlay bars, which take no room.
func TestScrollerStyleChangeRelaysOut(t *testing.T) {
	g, bars, v := styleRig(t, ScrollVertical, 100, 2000)
	g.frames(3)
	inner := func() float32 { r := g.c.Rect(v.Inner); return r[2] - r[0] }
	if w := inner(); w != 400 || bars[1].Legacy {
		t.Fatalf("overlay: the content is %v wide (legacy %v), want 400", w, bars[1].Legacy)
	}
	pinScroller(g, theme.ScrollerLegacy)
	g.frame()
	if w := inner(); w != 400-34 || !bars[1].Legacy || bars[1].X != 400-34 || bars[1].W != 34 {
		t.Fatalf("one frame after the change to legacy: the content is %v wide, bar %+v; want 366 beside a 34-px bar", w, bars[1])
	}
	pinScroller(g, theme.ScrollerOverlay)
	g.frame()
	if w := inner(); w != 400 || bars[1].Legacy {
		t.Fatalf("one frame after the change back to overlay: the content is %v wide", w)
	}
}

// With legacy bars a view that scrolls both ways leaves room for both, and
// each bar stops at the other (the corner stays empty); one whose content
// fits on an axis leaves no room for that axis's bar.
func TestLegacyBarsBothAxes(t *testing.T) {
	g, bars, v := styleRig(t, ScrollBoth, 2000, 2000)
	pinScroller(g, theme.ScrollerLegacy)
	g.frames(3)
	r := g.c.Rect(v.Inner)
	if r[2]-r[0] != 366 || r[3]-r[1] != 266 {
		t.Fatalf("both axes: the content is %v × %v, want 366 × 266", r[2]-r[0], r[3]-r[1])
	}
	h, vb := &bars[0], &bars[1]
	if !h.On || !vb.On || vb.H != 266 || h.W != 366 || h.Y != 266 || vb.X != 366 {
		t.Fatalf("both axes: horizontal %+v, vertical %+v", *h, *vb)
	}

	g, bars, v = styleRig(t, ScrollBoth, 100, 2000) // only taller than the view
	pinScroller(g, theme.ScrollerLegacy)
	g.frames(3)
	r = g.c.Rect(v.Inner)
	if r[2]-r[0] != 366 || r[3]-r[1] != 300 || bars[0].On {
		t.Fatalf("vertical overflow only: the content is %v × %v, horizontal bar %v; want 366 × 300, none", r[2]-r[0], r[3]-r[1], bars[0].On)
	}
}

// A VList given VListBarRoom leaves a legacy bar's width at its right while
// it overflows, as BeginScroll does, so its rows end before the bar drawn
// over its outer box; with overlay bars it takes none.
func TestVListLeavesRoomForALegacyBar(t *testing.T) {
	var v VList
	st, bars := &VListState{}, &ScrollState{}
	ids := make([]uint64, 100)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	st.Reset(ids)
	g := newRig(t, 400, 300, 2, func(k *Kit) {
		c := k.Context()
		v = BeginVList(c, "rows", st, VListOpts{Estimate: 20, BarRoom: k.VListBarRoom("rows", bars)})
		for i := v.First; i < v.Last; i++ {
			v.Item(i)
			c.NextSize(ui.Pct(1), ui.Px(40))
			c.BoxKeyed(0, ui.HashString(c.ParentKey(), "row"), nil)
			v.EndItem()
		}
		EndVList(&v)
	})
	g.scale = 2
	body := func() float32 { r := g.c.Rect(v.Body); return r[2] - r[0] }
	g.frames(3)
	if w := body(); w != 400 {
		t.Fatalf("overlay bars: the rows are %v wide, want 400", w)
	}
	pinScroller(g, theme.ScrollerLegacy)
	g.frames(2)
	if w := body(); w != 400-34 {
		t.Fatalf("legacy bars: the rows are %v wide, want 366 beside a 34-px bar", w)
	}
}
