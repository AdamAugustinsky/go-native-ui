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
	st   VListState
	o    VListOpts
	v    VList
	hOf  map[uint64]float32 // heights that differ from defHeight
	box  map[uint64]ui.ID   // the item boxes of the last frame
	edit func()             // runs in the next Build, before BeginVList
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
	c.Begin(&h.in, h.w, h.h, 1, 1.0/60)
	if h.edit != nil {
		h.edit()
		h.edit = nil
	}
	clear(h.box)
	h.v = BeginVList(c, "list", &h.st, h.o)
	for i := h.v.First; i < h.v.Last; i++ {
		id := h.v.Item(i)
		h.box[id] = c.Parent()
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
	h.st.ScrollTo(700, AlignCenter)
	h.frames(3) // the jump lands on estimates, then the heights settle around it
	h.st.ScrollTo(700, AlignCenter)
	h.frame()
	id := h.st.ID(700)
	if mid := h.y(id) + h.height(id)/2; mid != float32(h.h)/2 {
		t.Fatalf("item 700 is centred at %v, want %v", mid, h.h/2)
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

// A change of display scale scales the measured heights and the position:
// the same item stays at the top, at the scaled offset.
func TestVListScaleChange(t *testing.T) {
	h := newVT(t, 1000, VListOpts{})
	h.frames(3)
	h.scrollTo(400, 6)
	id, _ := h.top()
	hgt, _ := h.st.Height(400)
	h.c.Begin(&h.in, h.w, h.h, 2, 1.0/60)
	v := BeginVList(h.c, "list", &h.st, h.o)
	for i := v.First; i < v.Last; i++ {
		v.Item(i)
	}
	EndVList(&v)
	h.c.End(&h.dl)
	if a, off := h.st.Anchor(); a != id || off != 12 {
		t.Fatalf("after the scale change the anchor is %d at %v, want %d at 12", a, off, id)
	}
	if got, _ := h.st.Height(400); got != 2*hgt {
		t.Fatalf("height %v after doubling the scale, want %v", got, 2*hgt)
	}
}
