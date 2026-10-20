package base

import (
	"fmt"
	"strings"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/text"
	"gonativeui/ui"
)

// Selection scopes over a VList (C4, the tests of PHASE6-PLAN's "Rich text
// and selection"): a list of messages of wrapped text, one RichText per item,
// selected through the list's Enumerator.

type selVT struct {
	t     *testing.T
	c     *ui.Context
	in    ui.Input
	dl    gpu.DrawList
	w, h  int
	st    VListState
	o     VListOpts
	v     VList
	sc    ui.SelectionScope
	sig   ui.ScopeSignal
	msgs  map[uint64][]byte // the model's text of each item
	boxes map[uint64]ui.ID  // the texts of the last frame
	built map[uint64]bool   // every item ever built
	clip  string
	edit  func() // runs in the next Build, before BeginVList
	// noScope builds the frame without the selection scope
	noScope bool
	next    uint64 // the next id
}

// layText is the fake text system with a paragraph layouter whose cache is
// reserved, as an app reserves the text system's (text.System.ReserveParas),
// so that scrolling through new messages allocates nothing.
type layText struct {
	*fakeui.Text
	*text.BasicLayouter
}

func newLayText() layText {
	ft := fakeui.New()
	l := text.NewBasicLayouter(ft)
	l.ReserveParas(text.ParaBudget{Paras: 1024, Glyphs: 1 << 16, Text: 1 << 16})
	return layText{ft, l}
}

func newSelVT(t *testing.T, n int) *selVT {
	h := &selVT{t: t, c: ui.New(newLayText()), w: 400, h: 300, msgs: map[uint64][]byte{}, boxes: map[uint64]ui.ID{}, built: map[uint64]bool{}, next: 1000}
	h.c.ClipboardSet = func(b []byte) { h.clip = string(b) }
	h.in.Inside, h.in.Focused = true, true
	h.in.MouseX, h.in.MouseY = -100, -100
	h.st.Reserve(n + 200)
	h.st.Append(h.newIDs(n))
	h.dl.Quads = make([]gpu.Quad, 0, 1<<12)
	h.frames(3)
	return h
}

// newIDs adds n messages to the model and returns their ids.
func (h *selVT) newIDs(n int) []uint64 {
	ids := make([]uint64, n)
	for i := range ids {
		id := h.next
		h.next++
		ids[i] = id
		h.msgs[id] = []byte(fmt.Sprintf("message %d", id) + strings.Repeat(" lorem ipsum", int(id%7)))
	}
	return ids
}

// AppendSelected is the model's text for a copy.
func (h *selVT) AppendSelected(dst []byte, id uint64, fp uint16, fo uint32, tp uint16, to uint32) []byte {
	return append(dst, ui.SelectedBytes(h.msgs[id], 0, fp, fo, tp, to)...)
}

func (h *selVT) frame() {
	c := h.c
	c.Begin(&h.in, h.w, h.h, 1, 1.0/60)
	if h.edit != nil {
		h.edit()
		h.edit = nil
	}
	clear(h.boxes)
	h.v = BeginVList(c, "transcript", &h.st, h.o)
	if !h.noScope {
		h.sig = c.BeginSelection(&h.sc, ui.ScopeOpts{Items: &h.st, View: h.v.Outer, Text: h})
	}
	for i := h.v.First; i < h.v.Last; i++ {
		id := h.v.Item(i)
		h.built[id] = true
		c.NextTextColor(0xE0E0E0FF)
		h.boxes[id] = c.RichText(ui.KeyN(c.ParentKey(), 0), h.msgs[id], nil, nil, ui.RichOpts{Item: id}).ID
	}
	if !h.noScope {
		c.EndSelection()
	}
	EndVList(&h.v)
	c.End(&h.dl)
	h.in.ClearFrame()
}

func (h *selVT) frames(n int) {
	for i := 0; i < n; i++ {
		h.frame()
	}
}

// at is a window point in the first line of item id's text, n characters in.
func (h *selVT) at(id uint64, n int) (x, y float32) {
	h.t.Helper()
	b, ok := h.boxes[id]
	if !ok {
		h.t.Fatalf("item %d is not built (%d..%d)", id, h.v.First, h.v.Last)
	}
	r := h.c.Rect(b)
	return r[0] + float32(n)*6.5 + 1, r[1] + 3
}

// inView is an item whose text is wholly in view, k items from the top.
func (h *selVT) inView(k int) uint64 {
	h.t.Helper()
	for i := h.v.First; i < h.v.Last; i++ {
		id := h.st.ID(i)
		if r := h.c.Rect(h.boxes[id]); r[1] >= 0 {
			if k == 0 {
				return id
			}
			k--
		}
	}
	h.t.Fatal("no item in view")
	return 0
}

func (h *selVT) press(x, y float32, mods ui.Mods) {
	h.in.MouseX, h.in.MouseY = x, y
	h.in.Mods = mods
	h.in.SetButton(0, true, 1)
	h.frame()
	h.in.Mods = 0
}

func (h *selVT) release() {
	h.in.SetButton(0, false, 0)
	h.frame()
}

func (h *selVT) key(code ui.KeyCode) {
	h.in.PushKey(ui.KeyEvent{Code: code, Mods: ui.PrimaryMod(), Down: true})
	h.frame()
}

// want is the text of the items from a (from byte fo) to b (to byte to),
// newline-separated, from the model.
func (h *selVT) want(a uint64, fo int, b uint64, to int) string {
	var out []string
	i, _ := h.st.IndexOf(a)
	j, _ := h.st.IndexOf(b)
	for k := i; k <= j; k++ {
		m := h.msgs[h.st.ID(k)]
		lo, hi := 0, len(m)
		if k == i {
			lo = fo
		}
		if k == j {
			hi = to
		}
		out = append(out, string(m[lo:hi]))
	}
	return strings.Join(out, "\n")
}

func (h *selVT) copy() string {
	h.t.Helper()
	h.clip = ""
	h.key('C')
	if !h.sig.Copied {
		h.t.Fatalf("no copy: %+v", h.sig)
	}
	return h.clip
}

func TestVListSelectionDragAcrossThreeViewportsAutoScrolls(t *testing.T) {
	h := newSelVT(t, 3000)
	h.st.SetAnchor(h.st.ID(2000), 0)
	h.frames(4)
	start := h.inView(6)
	si, _ := h.st.IndexOf(start)
	x, y := h.at(start, 4)
	h.press(x, y, 0)
	// the pointer goes 80 px above the list and stays there
	h.in.MouseX, h.in.MouseY = x, -80
	scrolled := float32(0)
	for f := 0; f < 400 && scrolled > -3.2*float32(h.h); f++ {
		h.frame()
		if !h.sig.Dragging {
			t.Fatal("the drag ended")
		}
		scrolled += h.sig.EdgeScroll
	}
	if scrolled > -3*float32(h.h) {
		t.Fatalf("auto-scroll went %v px", scrolled)
	}
	// the pointer comes back into the list (the last step of scrolling
	// lands), then onto the top text in view, and lets go there
	h.in.MouseY = float32(h.h) / 2
	h.frames(2)
	top := h.inView(0)
	ti, _ := h.st.IndexOf(top)
	h.in.MouseX, h.in.MouseY = h.at(top, 2)
	h.frame()
	h.release()
	t.Logf("auto-scrolled %v px over items %d to %d", scrolled, ti, si)
	if si-ti < 6 {
		t.Fatalf("the drag passed only items %d to %d", ti, si)
	}
	from, to, ok := h.sc.Selected()
	if !ok || from != (ui.Pos{Item: top, Off: 2}) || to != (ui.Pos{Item: start, Off: 4}) {
		t.Fatalf("selected %v to %v (%v), want %d.2 to %d.4", from, to, ok, top, start)
	}
	// everything it passed is selected, and copies
	n := 0
	ui.ForEachSelected(&h.sc, func(id uint64, fp uint16, fo uint32, tp uint16, to uint32) {
		if i, _ := h.st.IndexOf(id); i != ti+n {
			t.Fatalf("item %d out of order", id)
		}
		n++
	})
	if n != si-ti+1 {
		t.Fatalf("%d items selected, want %d", n, si-ti+1)
	}
	if got := h.copy(); got != h.want(top, 2, start, 4) {
		t.Fatalf("copied %q", got[:min(len(got), 60)])
	}
}

func TestVListSelectAllAndCopyBuildsNothing(t *testing.T) {
	h := newSelVT(t, 10000)
	id := h.inView(2)
	h.pressAt(h.at(id, 1))
	h.release()
	h.key('A')
	if !h.sig.Changed || !h.sig.Focused {
		t.Fatalf("select all: %+v", h.sig)
	}
	first, last := h.st.ID(0), h.st.ID(h.st.Len()-1)
	got := h.copy()
	if got != h.want(first, 0, last, len(h.msgs[last])) {
		t.Fatalf("copied %d bytes", len(got))
	}
	t.Logf("%d items built, %d bytes copied", len(h.built), len(got))
	if len(h.built) > 30 {
		t.Fatalf("%d of the 10,000 items were built", len(h.built))
	}
	// the end is PartEnd in an item that was never built
	if _, f, _ := h.sc.Endpoints(); f.Part != ui.PartEnd || h.built[f.Item] {
		t.Fatalf("focus %v, built %v", f, h.built[f.Item])
	}
}

// press with the pointer at a point (for h.press(h.at(...))).
func (h *selVT) pressAt(x, y float32) { h.press(x, y, 0) }

func TestVListSelectionOverSkippedRows(t *testing.T) {
	h := newSelVT(t, 8000)
	a := h.inView(1)
	h.pressAt(h.at(a, 3))
	h.release()
	// a jump far down: the rows in between are never laid out
	h.st.ScrollTo(6000, AlignStart)
	h.frames(20)
	b := h.inView(1)
	x, y := h.at(b, 5)
	h.press(x, y, ui.ModShift)
	h.release()
	ai, _ := h.st.IndexOf(a)
	bi, _ := h.st.IndexOf(b)
	skipped := 0
	for i := ai + 1; i < bi; i++ {
		if !h.built[h.st.ID(i)] {
			skipped++
		}
	}
	if skipped < 5000 {
		t.Fatalf("only %d rows were skipped", skipped)
	}
	if got := h.copy(); got != h.want(a, 3, b, 5) {
		t.Fatalf("copied %d bytes, want %d", len(got), len(h.want(a, 3, b, 5)))
	}
}

// selectRange selects from item a byte fo to item b byte to with a press and
// a shift-click, both in view.
func (h *selVT) selectRange(a uint64, fo int, b uint64, to int) {
	h.pressAt(h.at(a, fo))
	h.release()
	x, y := h.at(b, to)
	h.press(x, y, ui.ModShift)
	h.release()
}

func TestVListPrependMidSelectionKeepsIt(t *testing.T) {
	h := newSelVT(t, 500)
	h.st.SetAnchor(h.st.ID(300), 0)
	h.frames(4)
	a, b := h.inView(1), h.inView(4)
	h.selectRange(a, 2, b, 6)
	a0, f0, _ := h.sc.Endpoints()
	c0 := h.copy()
	h.edit = func() { h.st.Prepend(h.newIDs(100)) }
	h.frame()
	h.frame()
	if h.sig.Changed {
		t.Fatal("prepending changed the selection")
	}
	if a1, f1, _ := h.sc.Endpoints(); a1 != a0 || f1 != f0 {
		t.Fatalf("endpoints %v %v, were %v %v", a1, f1, a0, f0)
	}
	if c1 := h.copy(); c1 != c0 || c0 != h.want(a, 2, b, 6) {
		t.Fatalf("copied %q, was %q", c1, c0)
	}
}

func TestVListDeletingAnEndpointsItemMovesIt(t *testing.T) {
	h := newSelVT(t, 500)
	h.st.SetAnchor(h.st.ID(100), 0)
	h.frames(4)
	a, b := h.inView(1), h.inView(5)
	h.selectRange(a, 3, b, 5)
	bi, _ := h.st.IndexOf(b)
	after := h.st.ID(bi + 1)
	// the focus's item goes: the focus moves to the start of the next one
	h.edit = func() { h.st.Splice(bi, 1, nil) }
	h.frame()
	if !h.sig.Changed {
		t.Fatal("no Changed when the focus's item was deleted")
	}
	if _, f, _ := h.sc.Endpoints(); f != (ui.Pos{Item: after}) {
		t.Fatalf("focus %v, want the start of %d", f, after)
	}
	prevB := h.st.ID(bi - 1)
	if got := h.copy(); got != h.want(a, 3, prevB, len(h.msgs[prevB])) {
		t.Fatalf("copied %q", got)
	}
	// the anchor's item goes: the anchor moves to the end of the one before
	ai, _ := h.st.IndexOf(a)
	before, next := h.st.ID(ai-1), h.st.ID(ai+1)
	h.edit = func() { h.st.Splice(ai, 1, nil) }
	h.frame()
	if a1, _, _ := h.sc.Endpoints(); !h.sig.Changed || a1 != (ui.Pos{Item: before, Part: ui.PartEnd}) {
		t.Fatalf("anchor %v (changed %v), want the end of %d", a1, h.sig.Changed, before)
	}
	if got := h.copy(); got != h.want(next, 0, prevB, len(h.msgs[prevB])) {
		t.Fatalf("copied %q", got)
	}
	// both endpoints in one item that goes: nothing is left
	c := h.inView(2)
	h.selectRange(c, 1, c, 4)
	ci, _ := h.st.IndexOf(c)
	h.edit = func() { h.st.Splice(ci, 1, nil) }
	h.frame()
	if _, _, ok := h.sc.Selected(); ok || !h.sig.Changed {
		t.Fatal("a selection in a deleted item survived")
	}
	// a reset clears the selection
	h.selectRange(h.inView(1), 0, h.inView(3), 2)
	ids := make([]uint64, h.st.Len())
	for i := range ids {
		ids[i] = h.st.ID(i)
	}
	h.edit = func() { h.st.Reset(ids) }
	h.frame()
	if _, _, ok := h.sc.Endpoints(); ok {
		t.Fatal("a reset kept the selection")
	}
}

func TestVListSelectionAllocatesNothing(t *testing.T) {
	h := newSelVT(t, 1000000)
	h.c.ClipboardSet = func([]byte) {}
	h.st.SetAnchor(h.st.ID(500000), 0)
	h.frames(4)
	a := h.inView(2)
	tick := 0
	frame := func() {
		tick++
		switch k := tick % 40; {
		case k == 0:
			h.in.MouseX, h.in.MouseY = h.at(h.inView(2), 3)
			h.in.SetButton(0, true, 1)
		case k < 10: // dragging past the bottom: auto-scroll
			h.in.MouseY = float32(h.h) + 40
		case k == 10:
			h.in.SetButton(0, false, 0)
		case k < 30: // scrolling the selection with the wheel
			h.in.AddScroll(0, -float32(1+tick%3), false)
		case k == 30:
			h.in.PushKey(ui.KeyEvent{Code: 'C', Mods: ui.PrimaryMod(), Down: true})
		}
		h.frame()
	}
	_ = a
	cycle := func() { // one whole cycle of the 40 frames above
		for i := 0; i < 40; i++ {
			frame()
		}
	}
	for i := 0; i < 5; i++ {
		cycle()
	}
	// the exact count over whole cycles (the first clean of three tries): an
	// allocation once a cycle shows
	var n uint64
	for try := 0; try < 3; try++ {
		if n = mallocs(func() {
			for i := 0; i < 5; i++ {
				cycle()
			}
		}); n == 0 {
			break
		}
	}
	if n != 0 {
		t.Fatalf("%d allocations over 5 cycles of dragging, scrolling and copying", n)
	}
	for i := 0; i < 5; i++ { // (a cycle ends with its press)
		frame()
	}
	if _, _, ok := h.sc.Selected(); !ok {
		t.Fatal("no selection")
	}
}

// Review fixes (C4).

// An endpoint whose item goes moves to its item's survivors; when edits
// before the next BeginSelection remove those too, it moves on to theirs:
// edits in place, and a sliced rebuild that publishes while no scope is
// built.
func TestVListSurvivorsFollowLaterEdits(t *testing.T) {
	for _, sliced := range []bool{false, true} {
		h := newSelVT(t, 500)
		h.st.SetAnchor(h.st.ID(100), 0)
		h.frames(4)
		a, b := h.inView(2), h.inView(6)
		h.selectRange(a, 3, b, 5)
		ai, _ := h.st.IndexOf(a)
		bi, _ := h.st.IndexOf(b)
		prev2, next2 := h.st.ID(ai-2), h.st.ID(bi+2)
		// the focus's item, then the item after it; the anchor's, then the
		// one before it
		h.edit = func() {
			h.st.Splice(bi, 1, nil)
			h.st.Splice(ai, 1, nil)
			if sliced {
				vlDirect = 2 // the next two are rebuilt in slices
			}
			h.st.Splice(bi-1, 1, nil)
			h.st.Splice(ai-1, 1, nil)
		}
		if sliced {
			h.noScope = true
			h.frame()
			if !h.st.Pending() {
				t.Fatal("the edits were not rebuilt in slices")
			}
			for f := 0; f < 200 && h.st.Pending(); f++ {
				h.frame()
			}
			vlDirect = vlDirectMax
			h.noScope = false
		}
		h.frame()
		a1, f1, ok := h.sc.Endpoints()
		if _, _, sel := h.sc.Selected(); !ok || !sel {
			t.Fatalf("sliced %v: the selection was cleared", sliced)
		}
		if a1 != (ui.Pos{Item: prev2, Part: ui.PartEnd}) || f1 != (ui.Pos{Item: next2}) {
			t.Fatalf("sliced %v: endpoints %v %v, want the end of %d and the start of %d", sliced, a1, f1, prev2, next2)
		}
	}
}

// After a Reset cleared the selection, a selection in the item the focus was
// in holds (both of the scope's marks follow again).
func TestVListReselectAfterReset(t *testing.T) {
	h := newSelVT(t, 300)
	a, b := h.inView(1), h.inView(3)
	h.selectRange(a, 1, b, 2)
	ids := make([]uint64, h.st.Len())
	for i := range ids {
		ids[i] = h.st.ID(i)
	}
	h.edit = func() { h.st.Reset(ids) }
	h.frame()
	if _, _, ok := h.sc.Endpoints(); ok {
		t.Fatal("the reset kept the selection")
	}
	h.selectRange(b, 1, b, 4)
	h.frames(3)
	if from, to, ok := h.sc.Selected(); !ok || from != (ui.Pos{Item: b, Off: 1}) || to != (ui.Pos{Item: b, Off: 4}) {
		t.Fatalf("reselected %v to %v (%v)", from, to, ok)
	}
}
