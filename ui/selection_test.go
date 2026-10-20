package ui

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"gonativeui/gpu"
)

// The scope rig builds a column of RichText boxes, 200 px wide, one line of
// the edit shaper's font 0 (8 px a character, 14 px a line) per text, item by
// item and part by part, for the items [lo, hi) of its model.

type selModel struct {
	ids   []uint64   // in document order
	parts [][]string // per item, its parts
	bytes [][][]byte // the same as bytes
	idx   map[uint64]int
}

func newSelModel(n int, parts func(i int) []string) *selModel {
	m := &selModel{idx: map[uint64]int{}}
	for i := 0; i < n; i++ {
		id := uint64(1000 + i*3) // ids are not indices
		m.ids = append(m.ids, id)
		ps := parts(i)
		m.parts = append(m.parts, ps)
		var bs [][]byte
		for _, p := range ps {
			bs = append(bs, []byte(p))
		}
		m.bytes = append(m.bytes, bs)
		m.idx[id] = i
	}
	return m
}

// selModel is an app-provided Enumerator.
func (m *selModel) IndexOf(id uint64) (int, bool) { i, ok := m.idx[id]; return i, ok }
func (m *selModel) First() (uint64, bool)         { return m.ids[0], len(m.ids) > 0 }
func (m *selModel) Last() (uint64, bool)          { return m.ids[len(m.ids)-1], len(m.ids) > 0 }
func (m *selModel) Next(id uint64) (uint64, bool) {
	i, ok := m.idx[id]
	if !ok || i+1 >= len(m.ids) {
		return 0, false
	}
	return m.ids[i+1], true
}

// remove deletes item i from the model.
func (m *selModel) remove(i int) {
	delete(m.idx, m.ids[i])
	m.ids = append(m.ids[:i], m.ids[i+1:]...)
	m.parts = append(m.parts[:i], m.parts[i+1:]...)
	m.bytes = append(m.bytes[:i], m.bytes[i+1:]...)
	for k := i; k < len(m.ids); k++ {
		m.idx[m.ids[k]] = k
	}
}

// AppendSelected joins the selected parts of an item with " | ".
func (m *selModel) AppendSelected(dst []byte, id uint64, fp uint16, fo uint32, tp uint16, to uint32) []byte {
	if _, ok := m.idx[id]; !ok {
		panic(fmt.Sprintf("the copy asked for item %d, which the model does not have", id))
	}
	first := true
	for p, s := range m.bytes[m.idx[id]] {
		b := SelectedBytes(s, uint16(p), fp, fo, tp, to)
		if b == nil {
			continue
		}
		if !first {
			dst = append(dst, " | "...)
		}
		first = false
		dst = append(dst, b...)
	}
	return dst
}

type scopeRig struct {
	*harness
	m      *selModel
	sc     SelectionScope
	o      ScopeOpts
	lo, hi int
	sig    ScopeSignal
	field  ID
	boxes  map[[2]uint64]ID // (item, part) -> box of the last frame
	built  map[uint64]bool
	clip   string
	spans  func(i, part int) []Span // the spans of a text, nil for none
	align  Align                    // the texts' alignment
	rsig   map[[2]uint64]RichSignal // the texts' signals of the last frame
	modal  bool                     // a modal overlay is open over the scope
	styles *RichStyles
	copies int // AppendSelected calls
	before func(c *Context)
}

func newScopeRig(t *testing.T, m *selModel, enum bool, lo, hi int) *scopeRig {
	r := &scopeRig{harness: newWrapHarness(t, 400, 600), m: m, lo: lo, hi: hi, boxes: map[[2]uint64]ID{}, built: map[uint64]bool{}, rsig: map[[2]uint64]RichSignal{}, styles: testStyles()}
	if enum {
		r.o.Items = m
	}
	r.o.Text = m
	r.c.ClipboardSet = func(b []byte) { r.clip = string(b) }
	r.step()
	return r
}

func (r *scopeRig) step() {
	r.frame(func(c *Context) {
		if r.before != nil {
			r.before(c)
		}
		clear(r.boxes)
		r.sig = c.BeginSelection(&r.sc, r.o)
		for i := r.lo; i < r.hi; i++ {
			id := r.m.ids[i]
			if r.o.Items == nil {
				id = uint64(i)
			}
			r.built[id] = true
			for p, s := range r.m.bytes[i] {
				c.NextWidth(Px(200))
				c.NextTextColor(colBody)
				if r.align != AlignLeft {
					c.NextAlign(r.align)
				}
				var sp []Span
				if r.spans != nil {
					sp = r.spans(i, p)
				}
				sig := c.RichText(KeyN(KeyN(0, id), uint64(p)), s, sp, r.styles, RichOpts{Item: id, Part: uint16(p)})
				r.boxes[[2]uint64{id, uint64(p)}] = sig.ID
				r.rsig[[2]uint64{id, uint64(p)}] = sig
			}
		}
		c.EndSelection()
		if r.modal {
			c.BeginOverlay(77, OverlayModal, [4]float32{300, 500, 390, 590}, PlaceCenter)
			c.NextSize(Px(40), Px(40))
			c.Box(Clickable, "ok")
			c.EndOverlay()
		}
	})
}

// at is the window point of byte off of the text (item, part), mid-line.
func (r *scopeRig) at(item uint64, part, off int) (x, y float32) {
	b := r.boxes[[2]uint64{item, uint64(part)}]
	rc := r.c.Rect(b)
	return rc[0] + float32(off)*8, rc[1] + 7
}

func (r *scopeRig) press(x, y float32) { r.pressN(x, y, 1) }

func (r *scopeRig) pressN(x, y float32, clicks uint8) {
	r.move(x, y)
	r.in.SetButton(0, true, clicks)
	r.step()
}

func (r *scopeRig) dragTo(x, y float32) {
	r.move(x, y)
	r.step()
}

func (r *scopeRig) release() {
	r.in.SetButton(0, false, 0)
	r.step()
}

func (r *scopeRig) key(code KeyCode, mods Mods) {
	r.in.PushKey(KeyEvent{Code: code, Mods: mods, Down: true})
	r.step()
}

// spans lists ForEachSelected's calls.
func spans(sc *SelectionScope) []string {
	var out []string
	ForEachSelected(sc, func(id uint64, fp uint16, fo uint32, tp uint16, to uint32) {
		out = append(out, fmt.Sprintf("%d:%d.%d-%d.%d", id, fp, fo, tp, to))
	})
	return out
}

// selQuads are the highlight quads of colour col in the last frame.
func selQuads(l *gpu.DrawList, col uint32) [][4]float32 {
	var out [][4]float32
	for _, q := range l.Quads {
		if q.Mode != gpu.ModeMaskR8 && q.Color[0] == col {
			out = append(out, q.Rect)
		}
	}
	return out
}

func words(n int) func(int) []string {
	return func(i int) []string { return []string{fmt.Sprintf("item %d says hello", i)} }
}

func TestSelectWithinAParagraphByDragging(t *testing.T) {
	r := newScopeRig(t, newSelModel(3, words(3)), false, 0, 3)
	r.press(r.at(1, 0, 2))
	r.dragTo(r.at(1, 0, 9))
	if !r.sig.Dragging || !r.sig.Changed || !r.sig.Focused {
		t.Fatalf("signal while dragging: %+v", r.sig)
	}
	r.release()
	from, to, ok := r.sc.Selected()
	if !ok || from != (Pos{1, 0, 2}) || to != (Pos{1, 0, 9}) {
		t.Fatalf("selected %v %v %v", from, to, ok)
	}
	q := selQuads(&r.list, r.c.TextEdit.Selection)
	rc := r.c.Rect(r.boxes[[2]uint64{1, 0}])
	if len(q) != 1 || q[0][0] != rc[0]+16 || q[0][2] != rc[0]+72 || q[0][1] != rc[1] || q[0][3] != rc[1]+14 {
		t.Fatalf("highlight %v, box %v", q, rc)
	}
	if got := spans(&r.sc); strings.Join(got, " ") != "1:0.2-0.9" {
		t.Fatalf("ForEachSelected %v", got)
	}
	// the accessibility hook: the element's selected range
	if s, e, ok := r.c.RichSelection(r.boxes[[2]uint64{1, 0}]); !ok || s != 2 || e != 9 {
		t.Fatalf("RichSelection %d %d %v", s, e, ok)
	}
	if _, _, ok := r.c.RichSelection(r.boxes[[2]uint64{0, 0}]); ok {
		t.Fatal("RichSelection of an unselected text")
	}
	// a window that is not active shows the unemphasised highlight
	r.in.Focused = false
	r.step()
	if len(selQuads(&r.list, r.c.TextEdit.SelectionInactive)) != 1 || len(selQuads(&r.list, r.c.TextEdit.Selection)) != 0 {
		t.Fatal("an inactive window keeps the emphasised highlight")
	}
	r.in.Focused = true
	r.step()
	// a click collapses it
	r.press(r.at(0, 0, 4))
	r.release()
	if _, _, ok := r.sc.Selected(); ok || len(selQuads(&r.list, r.c.TextEdit.Selection)) != 0 {
		t.Fatal("a click left a selection")
	}
}

func TestSelectAcrossItemsInDocumentOrder(t *testing.T) {
	r := newScopeRig(t, newSelModel(4, words(4)), false, 0, 4)
	// backwards: from item 2 up to item 0
	r.press(r.at(2, 0, 4))
	r.dragTo(r.at(0, 0, 5))
	r.release()
	if got := strings.Join(spans(&r.sc), " "); got != "0:0.5-65535.0 1:0.0-65535.0 2:0.0-0.4" {
		t.Fatalf("ForEachSelected %s", got)
	}
	a, f, _ := r.sc.Endpoints()
	if a != (Pos{2, 0, 4}) || f != (Pos{0, 0, 5}) {
		t.Fatalf("endpoints %v %v", a, f)
	}
	// items 0 and 1 continue past their ends: the highlight runs to the box's
	// edge (AppKit's), item 2's stops at its cut
	q := selQuads(&r.list, r.c.TextEdit.Selection)
	right := r.c.Rect(r.boxes[[2]uint64{0, 0}])[2]
	var ends []float32
	for _, rc := range q {
		ends = append(ends, rc[2])
	}
	if len(q) < 4 || q[len(q)-1][2] != r.c.Rect(r.boxes[[2]uint64{2, 0}])[0]+32 {
		t.Fatalf("highlight %v", q)
	}
	ext := 0
	for _, rc := range q {
		if rc[2] == right {
			ext++
		}
	}
	if ext != 2 {
		t.Fatalf("%d lines extended to the edge (%v), want 2", ext, ends)
	}
	// shift-click extends from the anchor
	r.in.Mods = ModShift
	r.press(r.at(3, 0, 1))
	r.in.Mods = 0
	r.release()
	if from, to, _ := r.sc.Selected(); from != (Pos{2, 0, 4}) || to != (Pos{3, 0, 1}) {
		t.Fatalf("shift-click: %v %v", from, to)
	}
}

func TestDoubleAndTripleClickSelectWordsAndParagraphs(t *testing.T) {
	m := newSelModel(3, func(i int) []string { return []string{"alpha beta gamma\ndelta epsilon"} })
	r := newScopeRig(t, m, false, 0, 3)
	x, y := r.at(1, 0, 7) // in "beta"
	r.press(x, y)
	r.release()
	r.pressN(x, y, 2)
	if from, to, _ := r.sc.Selected(); from != (Pos{1, 0, 6}) || to != (Pos{1, 0, 10}) {
		t.Fatalf("double click: %v %v", from, to)
	}
	// dragging after a double click extends by words, both ways
	r.dragTo(r.at(1, 0, 13)) // in "gamma"
	if from, to, _ := r.sc.Selected(); from != (Pos{1, 0, 6}) || to != (Pos{1, 0, 16}) {
		t.Fatalf("word drag forward: %v %v", from, to)
	}
	r.dragTo(r.at(1, 0, 2)) // in "alpha"
	if a, f, _ := r.sc.Endpoints(); a != (Pos{1, 0, 10}) || f != (Pos{1, 0, 0}) {
		t.Fatalf("word drag back: %v %v", a, f)
	}
	r.release()
	r.pressN(x, y, 3)
	if from, to, _ := r.sc.Selected(); from != (Pos{1, 0, 0}) || to != (Pos{1, 0, 17}) {
		t.Fatalf("triple click: %v %v (the paragraph with its newline)", from, to)
	}
	r.release()
}

func TestSelectAllAndCopyThroughAnAppEnumerator(t *testing.T) {
	const n = 10000
	m := newSelModel(n, words(n))
	r := newScopeRig(t, m, true, 5000, 5030) // the app builds 30 of the 10,000
	r.press(r.at(m.ids[5010], 0, 3))
	r.release()
	r.key('A', PrimaryMod())
	if !r.sig.Changed {
		t.Fatal("select all did not change the selection")
	}
	r.key('C', PrimaryMod())
	if !r.sig.Copy || !r.sig.Copied {
		t.Fatalf("copy: %+v", r.sig)
	}
	var want strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			want.WriteByte('\n')
		}
		want.WriteString(m.parts[i][0])
	}
	if r.clip != want.String() {
		t.Fatalf("copied %d bytes, want %d", len(r.clip), want.Len())
	}
	if len(r.built) != 30 {
		t.Fatalf("%d items built", len(r.built))
	}
	// the Edit menu's actions do the same
	r.clip = ""
	r.in.PushEdit(EditCopy)
	r.step()
	if r.clip != want.String() {
		t.Fatal("EditCopy did not copy")
	}
	// a focused text field takes the keys instead
	var buf TextBuffer
	buf.SetString("field")
	r.before = func(c *Context) {
		c.NextWidth(Px(100))
		r.field = c.TextField("f", &buf).ID
	}
	r.step()
	r.c.SetFocus(r.c.KeyOf(r.field))
	r.step()
	r.step()
	if r.c.SelectionFocused() {
		t.Fatal("the scope is focused while a text field has the focus")
	}
	r.clip = ""
	r.key('C', PrimaryMod())
	if r.sig.Copy || r.clip == want.String() {
		t.Fatalf("the scope copied while a text field had the focus (%+v)", r.sig)
	}
}

func TestEndpointsInUnbuiltItemsAndPartialParts(t *testing.T) {
	const n = 200
	m := newSelModel(n, func(i int) []string { return []string{fmt.Sprintf("body %d", i), fmt.Sprintf("caption %d", i)} })
	r := newScopeRig(t, m, true, 10, 20)
	// from the caption of a built item, part-way, to the end of an item that
	// was never built
	r.press(r.at(m.ids[12], 1, 3))
	r.release()
	r.sc.Set(Pos{m.ids[12], 1, 3}, Pos{m.ids[150], PartEnd, 0})
	r.key('C', PrimaryMod())
	var want []string
	want = append(want, "tion 12")
	for i := 13; i <= 150; i++ {
		want = append(want, fmt.Sprintf("body %d | caption %d", i, i))
	}
	if r.clip != strings.Join(want, "\n") {
		t.Fatalf("copied %q...", r.clip[:min(len(r.clip), 80)])
	}
	// a last partial part: the end in the body of an unbuilt item
	r.sc.Set(Pos{m.ids[40], 0, 2}, Pos{m.ids[12], 0, OffEnd})
	r.key('C', PrimaryMod())
	// (backwards, from the end of body 12: its caption on)
	if got := spans(&r.sc); got[0] != fmt.Sprintf("%d:0.%d-65535.0", m.ids[12], OffEnd) || got[len(got)-1] != fmt.Sprintf("%d:0.0-0.2", m.ids[40]) {
		t.Fatalf("cut points %v", got)
	}
	if !strings.HasPrefix(r.clip, "caption 12\nbody 13 | caption 13\n") || !strings.HasSuffix(r.clip, "\nbo") {
		t.Fatalf("copied %q", r.clip)
	}
	// the highlight of the built parts: caption 12 is selected, body 12 not
	if s, e, ok := r.c.RichSelection(r.boxes[[2]uint64{m.ids[12], 1}]); !ok || s != 0 || e != len("caption 12") {
		t.Fatalf("caption 12: %d %d %v", s, e, ok)
	}
	if _, _, ok := r.c.RichSelection(r.boxes[[2]uint64{m.ids[12], 0}]); ok {
		t.Fatal("body 12 is selected")
	}
}

func TestPressOutsideTheScopeUnfocusesIt(t *testing.T) {
	r := newScopeRig(t, newSelModel(3, words(3)), false, 0, 3)
	r.press(r.at(0, 0, 1))
	r.dragTo(r.at(0, 0, 6))
	r.release()
	if !r.c.SelectionFocused() {
		t.Fatal("not focused after selecting")
	}
	r.press(390, 590) // nothing there
	r.release()
	if r.c.SelectionFocused() {
		t.Fatal("still focused after a press elsewhere")
	}
	if _, _, ok := r.sc.Selected(); !ok {
		t.Fatal("a press outside cleared the selection")
	}
	if len(selQuads(&r.list, r.c.TextEdit.SelectionInactive)) != 1 {
		t.Fatal("the unfocused selection is not drawn unemphasised")
	}
	r.key('C', PrimaryMod())
	if r.sig.Copy {
		t.Fatal("an unfocused scope copied")
	}
}

func TestSelectionAllocatesNothing(t *testing.T) {
	const n = 1000
	m := newSelModel(n, words(n))
	r := newScopeRig(t, m, true, 100, 130)
	x0, y0 := r.at(m.ids[105], 0, 2)
	x1, y1 := r.at(m.ids[120], 0, 5)
	tick := 0
	r.c.ClipboardSet = func([]byte) {}
	cursors := make([]CursorRegion, 0, 256)
	frame := func() {
		tick++
		switch tick % 6 {
		case 0:
			r.in.SetButton(0, true, 1)
			r.move(x0, y0)
		case 1, 2:
			r.move(x1-float32(tick%3), y1)
		case 3:
			r.in.SetButton(0, false, 0)
		case 4:
			r.in.PushKey(KeyEvent{Code: 'A', Mods: PrimaryMod(), Down: true})
		case 5:
			r.in.PushKey(KeyEvent{Code: 'C', Mods: PrimaryMod(), Down: true})
		}
		r.step()
		cursors = r.c.CursorRegions(cursors[:0])
		ForEachSelected(&r.sc, func(uint64, uint16, uint32, uint16, uint32) {})
	}
	cycle := func() { // one whole cycle of the six steps above
		for i := 0; i < 6; i++ {
			frame()
		}
	}
	if n := cycleMallocs(cycle, 10, 20); n != 0 {
		t.Fatalf("%d allocations over 20 cycles of selecting and copying", n)
	}
}

// cycleMallocs runs warm cycles, then counts the exact heap allocations
// (MemStats.Mallocs) of n more: the first count of three that is 0, or the
// last. An allocation in every cycle, or one in n, shows in each count.
func cycleMallocs(cycle func(), warm, n int) uint64 {
	for i := 0; i < warm; i++ {
		cycle()
	}
	var d uint64
	for try := 0; try < 3; try++ {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < n; i++ {
			cycle()
		}
		runtime.ReadMemStats(&m1)
		if d = m1.Mallocs - m0.Mallocs; d == 0 {
			return 0
		}
	}
	return d
}

// The cost of a frame of 30 texts in a scope, with nothing selected and with
// everything (a highlight on every line).
func BenchmarkSelectionFrame(b *testing.B) {
	for _, all := range []bool{false, true} {
		b.Run(fmt.Sprintf("selected=%v", all), func(b *testing.B) {
			m := newSelModel(1000, func(i int) []string {
				return []string{strings.Repeat(fmt.Sprintf("word%d ", i), 3+i%9)}
			})
			r := newScopeRig(&testing.T{}, m, true, 100, 130)
			if all {
				r.sc.SelectAll()
			}
			r.step()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.step()
			}
		})
	}
}

// Review fixes (C4).

// With an app Enumerator that does not track edits, a selection inside one
// item that the model deleted is gone: Selected checks that the endpoints'
// items exist even when they are the same, and a copy does not ask the model
// for the missing item.
func TestSelectionInADeletedItemOfAnAppEnumerator(t *testing.T) {
	m := newSelModel(10, words(10))
	r := newScopeRig(t, m, true, 0, 10)
	id := m.ids[4]
	r.press(r.at(id, 0, 1))
	r.dragTo(r.at(id, 0, 6))
	r.release()
	if _, _, ok := r.sc.Selected(); !ok {
		t.Fatal("no selection")
	}
	m.remove(4)
	r.hi = 9
	r.step()
	if from, to, ok := r.sc.Selected(); ok {
		t.Fatalf("the selection %v to %v in a deleted item is still valid", from, to)
	}
	r.key('C', PrimaryMod()) // (the model panics if asked for the item)
	if r.sig.Copy {
		t.Fatal("copied a selection in a deleted item")
	}
}

// A move that arrives with the release, in the same frame, is where the drag
// ends.
func TestReleaseFrameMoveEndsTheDrag(t *testing.T) {
	r := newScopeRig(t, newSelModel(3, words(3)), false, 0, 3)
	r.press(r.at(1, 0, 2))
	r.move(r.at(1, 0, 9))
	r.in.SetButton(0, false, 0)
	r.step()
	if from, to, ok := r.sc.Selected(); !ok || from != (Pos{1, 0, 2}) || to != (Pos{1, 0, 9}) {
		t.Fatalf("selected %v to %v (%v), want 1.2 to 1.9", from, to, ok)
	}
	if r.sig.Dragging {
		t.Fatal("still dragging after the release")
	}
}

// Selecting inside a link by a drag or a double click does not activate it;
// a plain click does.
func TestSelectingInALinkDoesNotClickIt(t *testing.T) {
	m := newSelModel(3, words(3))
	r := newScopeRig(t, m, false, 0, 3)
	r.spans = func(i, part int) []Span {
		if i != 1 {
			return nil
		}
		return []Span{spanOf(m.parts[1][0], "says", 1, SpanLink, 42)}
	}
	r.step()
	link := [2]uint64{1, 0}
	x, y := r.at(1, 0, 8) // in "says"
	r.press(x, y)
	r.release()
	if r.rsig[link].Clicked != 42 {
		t.Fatalf("a plain click on the link: clicked %d", r.rsig[link].Clicked)
	}
	r.step()
	r.step() // (not a double click)
	r.press(x, y)
	r.dragTo(r.at(1, 0, 10))
	r.release()
	if _, _, ok := r.sc.Selected(); !ok || r.rsig[link].Clicked != 0 {
		t.Fatalf("a selection drag in the link: clicked %d", r.rsig[link].Clicked)
	}
	r.pressN(x, y, 2)
	r.release()
	if from, to, _ := r.sc.Selected(); from != (Pos{1, 0, 7}) || to != (Pos{1, 0, 11}) || r.rsig[link].Clicked != 0 {
		t.Fatalf("a double click in the link: %v to %v, clicked %d", from, to, r.rsig[link].Clicked)
	}
}

// A modal that opens over a scope ends its drag.
func TestModalCancelsASelectionDrag(t *testing.T) {
	r := newScopeRig(t, newSelModel(3, words(3)), false, 0, 3)
	r.press(r.at(0, 0, 1))
	r.dragTo(r.at(0, 0, 4))
	if !r.sig.Dragging {
		t.Fatal("not dragging")
	}
	r.modal = true
	r.step()
	r.dragTo(r.at(0, 0, 10))
	if _, f, _ := r.sc.Endpoints(); f != (Pos{0, 0, 4}) || r.sig.Dragging {
		t.Fatalf("the drag went on under the modal: focus %v, dragging %v", f, r.sig.Dragging)
	}
	if r.c.SelectionFocused() {
		t.Fatal("a scope under a modal takes the edit keys")
	}
	r.modal = false
	r.step()
	r.dragTo(r.at(0, 0, 12))
	if _, f, _ := r.sc.Endpoints(); f != (Pos{0, 0, 4}) {
		t.Fatalf("the cancelled drag came back: focus %v", f)
	}
}

// The highlight of centred and right-aligned text that continues past a line
// stays inside the box.
func TestContinuedHighlightStaysInAlignedBoxes(t *testing.T) {
	for _, al := range []Align{AlignCenter, AlignRight} {
		r := newScopeRig(t, newSelModel(3, words(3)), false, 0, 3)
		r.align = al
		r.sc.Set(Pos{0, 0, 2}, Pos{2, 0, 3})
		r.step()
		rc := r.c.Rect(r.boxes[[2]uint64{0, 0}])
		q := selQuads(&r.list, r.c.TextEdit.SelectionInactive)
		if len(q) < 3 {
			t.Fatalf("align %v: highlight %v", al, q)
		}
		for _, h := range q {
			if h[0] < rc[0] || h[2] > rc[2] {
				t.Fatalf("align %v: highlight %v outside the box %v", al, h, rc)
			}
		}
		// the glyph coverage moved with the text: item 1 is selected whole,
		// from its first glyph to the box's right edge
		r1 := r.c.Rect(r.boxes[[2]uint64{1, 0}])
		w := float32(len("item 1 says hello") * 8)
		left := r1[0] + (200-w)/2
		if al == AlignRight {
			left = r1[0] + 200 - w
		}
		found := false
		for _, h := range q {
			if h[1] == r1[1] && h[0] == left && h[2] == r1[2] {
				found = true
			}
		}
		if !found {
			t.Fatalf("align %v: no highlight from %v to %v on item 1 (%v)", al, left, r1[2], q)
		}
	}
}

// The scope looks its Enumerator's optional interfaces up when the app
// passes a different one, not in every frame.
func TestScopeLooksUpItsItemsOnlyWhenTheyChange(t *testing.T) {
	m := newSelModel(10, words(10))
	r := newScopeRig(t, m, true, 0, 10)
	n0 := itemsLookups
	for i := 0; i < 50; i++ {
		r.step()
	}
	if d := itemsLookups - n0; d != 0 {
		t.Fatalf("%d lookups over 50 frames with the same Enumerator", d)
	}
	m2 := newSelModel(10, words(10))
	r.o.Items = m2
	r.step()
	r.step()
	if d := itemsLookups - n0; d != 1 {
		t.Fatalf("%d lookups after a new Enumerator, want 1", d)
	}
}

// A copy larger than MaxKeptCopy does not keep its buffer; smaller ones do.
func TestLargeCopyReleasesItsBuffer(t *testing.T) {
	const n = 9000
	m := newSelModel(n, func(i int) []string { return []string{fmt.Sprintf("%08d ", i) + strings.Repeat("x", 120)} })
	r := newScopeRig(t, m, true, 0, 30)
	r.press(r.at(m.ids[3], 0, 2))
	r.release()
	r.key('A', PrimaryMod())
	r.key('C', PrimaryMod())
	if len(r.clip) <= MaxKeptCopy || !r.sig.Copied {
		t.Fatalf("copied %d bytes", len(r.clip))
	}
	if cap(r.c.sel.copyBuf) != 0 {
		t.Fatalf("a %d-byte copy kept a %d-byte buffer", len(r.clip), cap(r.c.sel.copyBuf))
	}
	r.press(r.at(m.ids[3], 0, 2))
	r.dragTo(r.at(m.ids[5], 0, 4))
	r.release()
	r.key('C', PrimaryMod())
	if c := cap(r.c.sel.copyBuf); c == 0 || c > MaxKeptCopy {
		t.Fatalf("a small copy kept %d bytes", c)
	}
	r.c.ReleaseSelectionCopy()
	if r.c.sel.copyBuf != nil {
		t.Fatal("ReleaseSelectionCopy kept the buffer")
	}
}
