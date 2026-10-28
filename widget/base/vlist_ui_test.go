package base

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/ui"
)

// noText is a Shaper for frames without text.
type noText struct{}

func (noText) Metrics(text.FontKey) text.Metrics {
	return text.Metrics{Ascent: 10, Descent: 3, LineHeight: 14}
}
func (noText) Shape(text.FontKey, []byte) text.RunRef                      { return 1 }
func (noText) Run(text.RunRef) text.RunView                                { return text.RunView{} }
func (noText) RunGeneration() uint32                                       { return 0 }
func (noText) Glyph(text.FaceID, uint16, uint8) text.GlyphEntry            { return text.GlyphEntry{} }
func (noText) AtlasGeneration() uint32                                     { return 0 }
func (noText) Pages() int                                                  { return 0 }
func (noText) PageInfo(int) (int, int, bool)                               { return 0, 0, false }
func (noText) Dirty(int) (x, y, w, h int, pix []byte, stride int, ok bool) { return }
func (noText) ClearDirty(int)                                              {}

// vt drives frames of one VList whose items are plain boxes of known heights.
type vt struct {
	t    *testing.T
	c    *ui.Context
	in   ui.Input
	dl   gpu.DrawList
	w, h int
	sc   float32 // the display scale (default 1)
	st   VListState
	o    VListOpts
	v    VList
	hOf  map[uint64]float32 // heights that differ from defHeight
	box  map[uint64]ui.ID   // the item boxes of the last frame
	edit func()             // runs in the next Build, before BeginVList
	// content, if set, builds the content of the items it reports it built
	content func(id uint64) bool
	// fixed, if set, says which items are built with ItemFixedX
	fixed func(id uint64) bool
}

func defHeight(id uint64) float32 { return float32(20 + id%5*10) }

func newVT(t *testing.T, n int, o VListOpts) *vt {
	h := &vt{t: t, c: ui.New(noText{}), w: 400, h: 600, o: o, hOf: map[uint64]float32{}, box: map[uint64]ui.ID{}}
	h.in.Inside, h.in.Focused = true, true
	h.in.MouseX, h.in.MouseY = 200, 300
	h.st.Reserve(n)
	h.st.Append(seq(1000, n))
	return h
}

func seq(from uint64, n int) []uint64 {
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = from + uint64(i)
	}
	return ids
}

func (h *vt) height(id uint64) float32 {
	if v, ok := h.hOf[id]; ok {
		return v
	}
	return defHeight(id)
}

func (h *vt) frame() {
	c := h.c
	sc := h.sc
	if sc == 0 {
		sc = 1
	}
	c.Begin(&h.in, h.w, h.h, sc, 1.0/60)
	if h.edit != nil {
		h.edit()
		h.edit = nil
	}
	clear(h.box)
	h.v = BeginVList(c, "list", &h.st, h.o)
	for i := h.v.First; i < h.v.Last; i++ {
		var id uint64
		if h.fixed != nil && h.fixed(h.st.f.id(i)) {
			id = h.v.ItemFixedX(i)
		} else {
			id = h.v.Item(i)
		}
		h.box[id] = c.Parent()
		if h.content != nil && h.content(id) {
			continue
		}
		c.NextSize(ui.Px(300), ui.Px(h.height(id)))
		c.Box(0, "content")
	}
	EndVList(&h.v)
	c.End(&h.dl)
	h.in.ClearFrame()
}

func (h *vt) frames(n int) {
	for i := 0; i < n; i++ {
		h.frame()
	}
}

// y is the screen top of item id in the last frame.
func (h *vt) y(id uint64) float32 {
	h.t.Helper()
	b, ok := h.box[id]
	if !ok {
		h.t.Fatalf("item %d was not built (built %d..%d)", id, h.v.First, h.v.Last)
	}
	return h.c.Rect(b)[1]
}

// top is the item at the top edge of the view and its screen top.
func (h *vt) top() (uint64, float32) {
	id, _ := h.st.Anchor()
	return id, h.y(id)
}

// scrollTo scrolls so that the view's top is off pixels into item i, and lets
// the heights settle.
func (h *vt) scrollTo(i int, off float64) {
	h.st.SetAnchor(h.st.ID(i), off)
	h.frames(4)
}

func (h *vt) mustTop(what string, id uint64, y float32) {
	h.t.Helper()
	gid, gy := h.top()
	if gid != id || gy != y {
		h.t.Fatalf("%s: the item at the top is %d at y %v, want %d at y %v", what, gid, gy, id, y)
	}
}

func TestVListPrependKeepsTheView(t *testing.T) {
	for _, stick := range []bool{false, true} {
		h := newVT(t, 1000, VListOpts{StickToBottom: stick})
		h.frames(3)
		h.scrollTo(500, 7)
		id, y := h.top()
		if y != -7 {
			t.Fatalf("the top item is at %v, want -7", y)
		}
		first := h.v.First
		h.edit = func() { h.st.Prepend(seq(1, 100)) }
		h.frame()
		h.mustTop("prepend", id, y)
		if h.v.First != first+100 {
			t.Fatalf("First %d after prepending 100, want %d", h.v.First, first+100)
		}
		h.frames(3)
		h.mustTop("prepend, settled", id, y)
	}
}

func TestVListDeletionKeepsTheView(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(500, 9)
	id, y := h.top()

	// above the anchor: some of it in the overscan, some far away
	h.edit = func() { h.st.Splice(490, 8, nil); h.st.Splice(10, 50, nil) }
	h.frame()
	h.mustTop("deletion above", id, y)

	// below
	h.edit = func() { i, _ := h.st.IndexOf(id); h.st.Splice(i+2, 3, nil) }
	h.frame()
	h.mustTop("deletion below", id, y)

	// the anchor item itself, with the view 9 px into it: the next item comes
	// to the top edge
	i, _ := h.st.IndexOf(id)
	next := h.st.ID(i + 1)
	h.edit = func() { h.st.Splice(i, 1, nil) }
	h.frame()
	h.mustTop("deletion of the anchor", next, 0)
	if _, off := h.st.Anchor(); off != 0 {
		t.Fatalf("offset %v after deleting the anchor, want 0", off)
	}

	// a run with the anchor in it
	i, _ = h.st.IndexOf(next)
	after := h.st.ID(i + 3)
	h.edit = func() { h.st.Splice(i-2, 5, nil) }
	h.frame()
	h.mustTop("deletion of a run holding the anchor", after, 0)

	// the anchor at the end of the list: the last survivor before the run
	n := h.st.Len()
	h.scrollTo(n-1, 0) // clamped to the bottom: the anchor is some item above
	id, _ = h.top()
	i, _ = h.st.IndexOf(id)
	before := h.st.ID(i - 1)
	h.edit = func() { h.st.Splice(i, n-i, nil) }
	h.frame()
	if a, _ := h.st.Anchor(); a == id {
		t.Fatal("the anchor survived its deletion")
	}
	if _, ok := h.st.IndexOf(before); !ok || h.st.Len() != i {
		t.Fatal("deleted the wrong items")
	}
	if !h.v.AtEnd {
		t.Fatal("not at the end after deleting the tail")
	}
}

func TestVListInsertionKeepsTheView(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(500, 3)
	id, y := h.top()
	h.edit = func() {
		h.st.Splice(498, 0, seq(1, 5))   // just above: in the overscan, measured next frame
		h.st.Splice(100, 0, seq(10, 40)) // far above: never measured
	}
	h.frame()
	h.mustTop("insertion above", id, y)
	h.frames(3)
	h.mustTop("insertion above, measured", id, y)

	i, _ := h.st.IndexOf(id)
	h.edit = func() { h.st.Splice(i+1, 0, seq(100, 3)) }
	h.frame()
	h.mustTop("insertion below", id, y)
	if got := h.y(100); got != y+h.height(id) {
		t.Fatalf("an item inserted after the anchor is at %v, want %v", got, y+h.height(id))
	}
}

func TestVListHeightChangesAboveKeepTheView(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(500, 4)
	id, y := h.top()
	// the items just above the anchor are built in the overscan; they grow
	for i := 495; i < 500; i++ {
		h.hOf[h.st.ID(i)] = 77
	}
	h.frames(3)
	h.mustTop("growth above", id, y)
	// the anchor itself grows: its top stays, the items below move
	below := h.st.ID(501)
	h.hOf[id] = 200
	h.frames(2)
	h.mustTop("growth of the anchor", id, y)
	if got := h.y(below); got != y+200 {
		t.Fatalf("the item after the grown anchor is at %v, want %v", got, y+200)
	}
}

func TestVListMoveKeepsTheView(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(500, 6)
	id, y := h.top()
	hgt, measured := h.st.Height(500)
	if !measured {
		t.Fatal("the anchor is not measured")
	}

	// a move that does not carry the anchor: from above it to below it
	h.edit = func() { h.st.Move(450, 10, 700) }
	h.frame()
	h.mustTop("move across the anchor", id, y)

	// a move that carries the anchor: the view goes with it
	i, _ := h.st.IndexOf(id)
	h.edit = func() { h.st.Move(i-1, 3, 20) }
	h.frame()
	h.mustTop("move of the anchor", id, y)
	if j, _ := h.st.IndexOf(id); j != 21 {
		t.Fatalf("the anchor is at %d after the move, want 21", j)
	}
	if got, ok := h.st.Height(21); !ok || got != hgt {
		t.Fatalf("the moved item's height is %v (measured %v), want %v", got, ok, hgt)
	}

	// heights travel with items moved out of view: measure an item, move it
	// far away and back into view
	h.frame()
	far := h.st.ID(25)
	fh, ok := h.st.Height(25)
	if !ok {
		t.Fatal("item 25 is in view and not measured")
	}
	h.edit = func() { h.st.Move(25, 1, 900) }
	h.frame()
	if got, ok := h.st.Height(900); !ok || got != fh || h.st.ID(900) != far {
		t.Fatalf("height after the move %v (measured %v), want %v", got, ok, fh)
	}
}

func TestVListReset(t *testing.T) {
	for _, stick := range []bool{false, true} {
		h := newVT(t, 1000, VListOpts{StickToBottom: stick})
		h.frames(3)
		h.scrollTo(500, 5)
		id, y := h.top()
		// the anchor is in the new list: it stays, at the same place
		h.edit = func() { h.st.Reset(append(seq(5000, 30), seq(1400, 300)...)) }
		h.frame()
		h.mustTop("reset with the anchor", id, y)

		// it is not: the top, or the bottom with StickToBottom
		h.edit = func() { h.st.Reset(seq(9000, 400)) }
		h.frame()
		if stick {
			if !h.v.AtEnd || h.v.Last != 400 {
				t.Fatalf("StickToBottom reset without the anchor: AtEnd %v, Last %d", h.v.AtEnd, h.v.Last)
			}
		} else {
			h.mustTop("reset without the anchor", 9000, 0)
		}
	}
}

func TestVListStickToBottom(t *testing.T) {
	h := newVT(t, 300, VListOpts{StickToBottom: true})
	h.frames(3)
	bottom := func(what string) {
		t.Helper()
		last := h.st.ID(h.st.Len() - 1)
		if got := h.y(last) + h.height(last); got != float32(h.h) || !h.v.AtEnd {
			t.Fatalf("%s: the last item ends at %v (AtEnd %v), want the bottom %d", what, got, h.v.AtEnd, h.h)
		}
	}
	bottom("start")
	// arrivals of heights the estimate does not know: the bottom holds in the
	// very frame they arrive in
	h.edit = func() { h.st.Append(seq(5000, 3)) }
	h.hOf[5002] = 97
	h.frame()
	bottom("arrivals")
	h.hOf[5002] = 180 // an image loads in the newest message
	h.frame()
	bottom("growth")
	h.hOf[h.st.ID(h.st.Len()-4)] = 150 // and in one above it
	h.frame()
	bottom("growth above")
	h.edit = func() { h.st.Prepend(seq(1, 100)) }
	h.frame()
	bottom("history prepended")

	// scrolled up a little: no longer pinned, arrivals do not move the view
	h.st.ScrollBy(-100)
	h.frames(2)
	if h.v.AtEnd {
		t.Fatal("still at the end after scrolling up")
	}
	id, y := h.top()
	h.edit = func() { h.st.Append(seq(6000, 5)) }
	h.frames(2)
	h.mustTop("arrival while scrolled up", id, y)

	// back to the bottom: pinned again
	h.st.ScrollBy(1e6)
	h.frames(2)
	h.edit = func() { h.st.Append(seq(7000, 2)) }
	h.frames(2)
	bottom("arrival after scrolling back")
}

func TestVListAtEndWithoutStick(t *testing.T) {
	h := newVT(t, 300, VListOpts{})
	h.frames(3)
	h.st.ScrollBy(1e6)
	h.frames(2)
	if !h.v.AtEnd {
		t.Fatal("not at the end")
	}
	id, y := h.top()
	h.edit = func() { h.st.Append(seq(5000, 5)) }
	h.frames(2)
	h.mustTop("arrival at the end without StickToBottom", id, y)
}

func TestVListScrollTo(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	// One request, landing on estimates (44) far from the heights (20 to 60):
	// it keeps the item centred while the items around it are measured.
	h.st.ScrollTo(700, AlignCenter)
	h.frames(6)
	id := h.st.ID(700)
	if mid := h.y(id) + h.height(id)/2; mid != float32(h.h)/2 {
		t.Fatalf("item 700 is centred at %v, want %v", mid, h.h/2)
	}
	if h.st.req {
		t.Fatal("the request is still settling with every height measured")
	}
	h.st.ScrollTo(10, AlignStart)
	h.frames(2)
	h.mustTop("ScrollTo start", h.st.ID(10), 0)
	h.st.ScrollTo(10, AlignNearest) // in view: nothing moves
	h.frame()
	h.mustTop("ScrollTo nearest in view", h.st.ID(10), 0)
}

// The wheel (precise deltas) and the keyboard go through the body's ui scroll
// offset: the view moves by exactly what they asked for.
func TestVListWheelAndKeys(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(300, 0)
	id, y := h.top()
	h.in.AddScroll(0, -13, true)
	h.frame()
	if got := h.y(id); got != y-13 {
		t.Fatalf("the wheel moved item %d to %v, want %v", id, got, y-13)
	}
	h.c.SetFocus(h.v.Key())
	h.frame()
	_, y = h.top()
	id, _ = h.top()
	h.in.PushKey(ui.KeyEvent{Code: ui.KeyDown, Down: true})
	h.frame()
	if got := h.y(id); got != y-40 {
		t.Fatalf("Down moved the item to %v, want %v", got, y-40)
	}
	h.in.PushKey(ui.KeyEvent{Code: ui.KeyEnd, Down: true})
	h.frames(2)
	if !h.v.AtEnd {
		t.Fatal("End did not reach the end")
	}
	h.in.PushKey(ui.KeyEvent{Code: ui.KeyHome, Down: true})
	h.frames(2)
	h.mustTop("Home", h.st.ID(0), 0)
}

// The marks report the survivors around a removed item, and a reset.
func TestVListMarks(t *testing.T) {
	var st VListState
	st.Append(seq(1, 10))
	st.SetMark(0, 5)
	st.SetMark(1, 2)
	st.Splice(3, 4, nil) // removes 4..7
	if m := st.Mark(0); !m.Removed || m.Next != 8 || m.Prev != 3 {
		t.Fatalf("mark on a removed item: %+v", m)
	}
	if m := st.Mark(1); m.Removed {
		t.Fatalf("mark on a surviving item: %+v", m)
	}
	st.Move(0, 2, 4) // ids survive a move
	if m := st.Mark(1); m.Removed || m.ID != 2 {
		t.Fatalf("mark after a move: %+v", m)
	}
	st.Reset(seq(1, 3))
	if m := st.Mark(1); !m.Reset || !m.Removed {
		t.Fatalf("mark after a reset: %+v", m)
	}
	if m := st.Mark(2); m.ID != NoID || m.Removed {
		t.Fatalf("unset mark: %+v", m)
	}
}

// A change of display scale keeps the position in points: the same item
// stays at the top, at the scaled offset, and the heights scale with it.
func TestVListScaleChange(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(400, 6)
	id, _ := h.top()
	hgt, _ := h.st.Height(400)
	h.sc = 2
	h.frame()
	if a, off := h.st.Anchor(); a != id || off != 6 {
		t.Fatalf("after the scale change the anchor is %d at %v pt, want %d at 6", a, off, id)
	}
	if got := h.y(id); got != -12 {
		t.Fatalf("after the scale change the top item is at %v px, want -12", got)
	}
	if got, _ := h.st.Height(400); got != 2*hgt {
		t.Fatalf("height %v after doubling the scale, want %v", got, 2*hgt)
	}
}

// A change of display scale during a rebuild neither publishes it (a reader
// that synced earlier in the frame has enumerated the old order) nor runs it
// to its end in that frame.
func TestVListScaleChangeKeepsTheFrameBoundary(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	defer func(d, s int) { vlDirect, vlSliceUnits = d, s }(vlDirect, vlSliceUnits)
	vlDirect, vlSliceUnits = 40, 97
	h.frames(3)
	h.edit = func() { h.st.Move(100, 1, 900) }
	h.frame()
	if !h.st.Pending() {
		t.Fatal("the move was not rebuilt")
	}
	h.sc = 2
	h.c.Begin(&h.in, h.w, h.h, h.sc, 1.0/60)
	h.st.Sync(h.c.Frame()) // a reader at the start of the frame
	rev, order := h.st.Rev(), orderHash(&h.st)
	v := BeginVList(h.c, "list", &h.st, h.o)
	for i := v.First; i < v.Last; i++ {
		v.Item(i)
	}
	EndVList(&v)
	h.c.End(&h.dl)
	if h.st.Rev() != rev || orderHash(&h.st) != order {
		t.Fatal("the scale change published the rebuild within the frame")
	}
	if !h.st.Pending() {
		t.Fatal("the scale change ran the rebuild to its end in one frame")
	}
}

// A rebuild finishes in an app that draws only while ui.Context.Animating:
// the list asks for frames while it is pending.
func TestVListRebuildFinishesWithoutInput(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	defer func(d, s int) { vlDirect, vlSliceUnits = d, s }(vlDirect, vlSliceUnits)
	vlDirect, vlSliceUnits = 40, 97
	for i := 0; i < 3 || h.c.Animating(); i++ {
		if i > 50 {
			t.Fatal("the list does not come to rest")
		}
		h.frame()
	}
	h.edit = func() { h.st.Move(100, 1, 900) }
	h.frame()
	for i := 0; h.c.Animating(); i++ {
		if i > 10000 {
			t.Fatal("the rebuild does not finish")
		}
		h.frame()
	}
	if h.st.Pending() {
		t.Fatal("the frames stopped with the rebuild pending")
	}
	if h.st.ID(900) != 1100 {
		t.Fatalf("item 900 is %d after the move, want 1100", h.st.ID(900))
	}
}

// Pinned to the bottom, MaxItems keeps the newest items: the last one is
// built, at the bottom.
func TestVListMaxItemsPinned(t *testing.T) {
	h := newVT(t, 300, VListOpts{StickToBottom: true, MaxItems: 4})
	for i := 0; i < 300; i++ {
		h.hOf[h.st.ID(i)] = 40
	}
	h.frames(3)
	if h.v.First != 296 || h.v.Last != 300 {
		t.Fatalf("built [%d, %d), want [296, 300)", h.v.First, h.v.Last)
	}
	last := h.st.ID(299)
	if got := h.y(last) + 40; got != float32(h.h) {
		t.Fatalf("the last item ends at %v, want %d", got, h.h)
	}
}

// Rows taller than any fixed box still end where they must: at the view's
// bottom while pinned, and at the anchor's top above it.
func TestVListTallRows(t *testing.T) {
	h := newVT(t, 300, VListOpts{StickToBottom: true})
	last := h.st.ID(299)
	h.hOf[last] = 100_000
	h.frames(3)
	if got := h.y(last) + h.height(last); got != float32(h.h) || !h.v.AtEnd {
		t.Fatalf("the tall last item ends at %v (AtEnd %v), want %d", got, h.v.AtEnd, h.h)
	}

	h = newVT(t, 300, VListOpts{})
	tall := h.st.ID(99)
	h.hOf[tall] = 100_000
	h.frames(2)
	h.scrollTo(100, 0)
	h.mustTop("below a tall item", h.st.ID(100), 0)
	if got := h.y(tall) + h.height(tall); got != 0 {
		t.Fatalf("the tall item above the anchor ends at %v, want 0", got)
	}
}

// BottomAlign: a conversation shorter than the view sits at its bottom, and
// stays there as messages arrive and history loads; without it the items
// start at the top.
func TestVListBottomAlign(t *testing.T) {
	for _, align := range []bool{false, true} {
		h := newVT(t, 5, VListOpts{StickToBottom: true, BottomAlign: align})
		for i := 0; i < 5; i++ {
			h.hOf[h.st.ID(i)] = 40
		}
		h.frame() // the first frame, before any layout
		first, last := h.st.ID(0), h.st.ID(4)
		wantFirst := float32(0)
		if align {
			wantFirst = 400
		}
		if got := h.y(first); got != wantFirst {
			t.Fatalf("BottomAlign %v: the first item is at %v, want %v", align, got, wantFirst)
		}
		if !align {
			continue
		}
		if got := h.y(last) + 40; got != float32(h.h) {
			t.Fatalf("the last item ends at %v, want %d", got, h.h)
		}
		h.edit = func() { h.st.Append(seq(5000, 2)) }
		h.hOf[5000], h.hOf[5001] = 40, 70
		h.frame()
		if got := h.y(5001) + 70; got != float32(h.h) {
			t.Fatalf("after an arrival the last item ends at %v, want %d", got, h.h)
		}
		if got := h.y(first); got != 290 {
			t.Fatalf("after an arrival the first item is at %v, want 290", got)
		}
		// longer than the view: pinned to the bottom as without BottomAlign
		h.edit = func() { h.st.Prepend(seq(1, 30)) }
		h.frames(3)
		if got := h.y(5001) + 70; got != float32(h.h) || !h.v.AtEnd {
			t.Fatalf("a long list ends at %v (AtEnd %v), want %d", got, h.v.AtEnd, h.h)
		}
	}
}

// An accessibility request to scroll an item built above the anchor (in the
// pile, which ui would scroll) into view scrolls the list to it.
func TestVListScrollIntoViewFromPile(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(500, 0)
	id := h.st.ID(498)
	if _, ok := h.box[id]; !ok {
		t.Fatal("item 498 is not built above the view")
	}
	h.in.PushAction(ui.SemRequest{Key: h.c.KeyOf(h.box[id]), Action: ui.ActScrollIntoView})
	h.frames(3)
	h.mustTop("scrolled into view", id, 0)
}

// settles runs frames until the list stops asking for them.
func (h *vt) settles(what string) {
	h.t.Helper()
	for i := 0; h.c.Animating(); i++ {
		if i > 100 {
			h.t.Fatalf("%s: the list keeps asking for frames", what)
		}
		h.frame()
	}
}

// A ScrollTo whose item goes away, or that never had one, is dropped: the
// list lets the app go idle.
func TestVListRequestForMissingItemEnds(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.st.ScrollTo(700, AlignCenter)
	h.edit = func() { h.st.Reset(nil) } // cleared before the request settles
	h.frame()
	h.settles("cleared with a request outstanding")
	if h.st.req {
		t.Fatal("the request is still outstanding")
	}

	h = newVT(t, 0, VListOpts{})
	h.frames(2)
	h.st.ScrollToID(1234, AlignStart)
	h.frame()
	h.settles("a request on an empty list")
}

// tallRow builds, for the item id, a row tall px high with a 30 px button at
// offset y from its top.
func (h *vt) tallRow(id uint64, tall, y float32, btn *ui.ID) func(uint64) bool {
	return func(got uint64) bool {
		if got != id {
			return false
		}
		c := h.c
		c.NextSize(ui.Px(300), ui.Px(tall))
		c.NextAxis(ui.AxisY)
		c.Push(c.Box(0, "row"))
		c.NextSize(ui.Px(10), ui.Px(y))
		c.Box(0, "above")
		c.NextSize(ui.Px(80), ui.Px(30))
		*btn = c.Box(ui.Clickable, "button")
		c.Pop()
		return true
	}
}

// Accessibility requests for a box inside a tall row in a pile show that box,
// where it is in the row: a visible one moves nothing, one out of view comes
// just into view.
func TestVListScrollIntoViewOfDescendant(t *testing.T) {
	for _, tc := range []struct {
		name  string
		btnY  float32 // the button's offset in the 100,000 px row
		wantY float32 // its screen top afterwards
	}{
		{"visible", 100_000 - 100, 600 - 100},
		{"above the view", 2000, 0},
	} {
		h := newVT(t, 300, VListOpts{StickToBottom: true})
		last := h.st.ID(299)
		h.hOf[last] = 100_000
		var btn ui.ID
		h.content = h.tallRow(last, 100_000, tc.btnY, &btn)
		h.frames(3)
		h.in.PushAction(ui.SemRequest{Key: h.c.KeyOf(btn), Action: ui.ActScrollIntoView})
		h.frame()
		h.settles(tc.name)
		if got := h.c.Rect(btn)[1]; got != tc.wantY {
			t.Fatalf("%s: the button is at %v, want %v", tc.name, got, tc.wantY)
		}
		if tc.name == "visible" && (!h.v.AtEnd || h.y(last)+100_000 != float32(h.h)) {
			t.Fatalf("%s: the list moved for a visible box", tc.name)
		}
	}
}

// A box inside a scroller of its own in a pile row is scrolled into view by
// that scroller (ui's nearest scrolling ancestor), or not at all when it
// shows in it; the list does not move.
func TestVListScrollIntoViewInNestedScroller(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(500, 0)
	row := h.st.ID(499)
	var inner, near, target ui.ID
	h.content = func(id uint64) bool {
		if id != row {
			return false
		}
		c := h.c
		c.NextSize(ui.Px(300), ui.Px(100))
		c.NextAxis(ui.AxisY)
		inner = c.Box(ui.ScrollY|ui.Clip, "inner")
		c.Push(inner)
		c.NextSize(ui.Px(300), ui.Px(40))
		c.Box(0, "spacer")
		c.NextSize(ui.Px(80), ui.Px(30))
		near = c.Box(ui.Clickable, "near") // in the nested view
		c.NextSize(ui.Px(300), ui.Px(730))
		c.Box(0, "spacer2")
		c.NextSize(ui.Px(80), ui.Px(30))
		target = c.Box(ui.Clickable, "target")
		c.Pop()
		return true
	}
	h.frames(3)
	h.mustTop("before", h.st.ID(500), 0)
	h.in.PushAction(ui.SemRequest{Key: h.c.KeyOf(near), Action: ui.ActScrollIntoView})
	h.frame()
	h.settles("nested, in view")
	h.mustTop("after a request for a box in view in a nested scroller", h.st.ID(500), 0)
	if _, sy := h.c.Scroll(inner); sy != 0 {
		t.Fatalf("the nested scroller moved to %v for a box in its view", sy)
	}
	h.in.PushAction(ui.SemRequest{Key: h.c.KeyOf(target), Action: ui.ActScrollIntoView})
	h.frame()
	h.settles("nested")
	h.mustTop("after a request inside a nested scroller", h.st.ID(500), 0)
	if _, sy := h.c.Scroll(inner); sy != 730 {
		t.Fatalf("the nested scroller is at %v, want 730 (its target at its bottom)", sy)
	}
}

// At a fractional scale the position holds in points across a scale change,
// and the items still tile on whole device pixels: each starts where the one
// above it ends.
func TestVListFractionalScale(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(400, 6)
	id, _ := h.top()
	h.sc = 1.5
	h.frames(3)
	if a, off := h.st.Anchor(); a != id || off != 6 {
		t.Fatalf("the anchor is %d at %v pt, want %d at 6", a, off, id)
	}
	if got := h.y(id); got != -9 {
		t.Fatalf("the top item is at %v px, want -9", got)
	}
	for i := h.v.First; i+1 < h.v.Last; i++ {
		a, b := h.st.ID(i), h.st.ID(i+1)
		r := h.c.Rect(h.box[a])
		if r[1] != float32(int(r[1])) || h.c.Rect(h.box[b])[1] != r[3] {
			t.Fatalf("items %d and %d: %v then %v, not tiled on pixels", i, i+1, r, h.c.Rect(h.box[b]))
		}
	}
}
