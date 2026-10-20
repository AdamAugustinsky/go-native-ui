package ui

import (
	"gonativeui/gpu"
	"gonativeui/text"
)

// Selectable text (D42). A selection scope makes the RichText boxes built
// inside it selectable together, in document order, the way a transcript of
// messages is selected: press and drag across messages, double-click a word,
// triple-click a paragraph, shift-click to extend, ⌘A for everything and ⌘C
// to copy.
//
//	var sc ui.SelectionScope // app-owned, next to the model
//
//	v := base.BeginVList(c, "transcript", &st, opts)
//	sig := c.BeginSelection(&sc, ui.ScopeOpts{Items: &st, View: v.Outer, Text: model})
//	for i := v.First; i < v.Last; i++ {
//		id := v.Item(i)
//		c.RichText(key, body, spans, &styles, ui.RichOpts{Item: id, Part: 0})
//	}
//	c.EndSelection()
//	base.EndVList(&v)
//
// # Endpoints are app data
//
// A selection is two endpoints, the anchor (where it began) and the focus
// (where it ends, moved by a drag), each a Pos: the app's stable item id, a
// text part of the item (its body, a quote, a caption: RichOpts.Part, in
// order within the item) and a byte offset into that part. Nothing about an
// endpoint refers to a box, so a selection survives its items scrolling out
// of a virtual list, which builds only what is in view: the rows in between
// are selected without being built, and a copy walks them through the
// scope's Enumerator. Two sentinels let an endpoint sit in an item nobody has
// measured: Part PartEnd is the end of the whole item and Off OffEnd the end
// of a part.
//
// # Order
//
// The scope orders endpoints by the Enumerator's IndexOf, then part, then
// offset (the sentinels sort last). *base.VListState is one: its order is the
// list's, O(1), and it follows items through edits. Outside a VList the app
// implements Enumerator over its model (ScopeOpts.Items); with no Enumerator
// the item ids are 0, 1, 2… in document order, as many as the scope's texts
// declared in the last frame.
//
// # Edits
//
// Inserting items anywhere leaves the endpoints alone: ids are stable. When
// Items is also an ItemTracker (base.VListState is) and an edit removes the
// item an endpoint is in, the anchor moves to the end of the last surviving
// item before it and the focus to the start of the first one after it (to the
// other one when there is none on that side), and the next BeginSelection
// reports Changed. A selection the removal empties is cleared, and so is one
// whose list was replaced (VListState.Reset). With another Enumerator an
// endpoint whose item is gone hides the selection until the app sets it again.
//
// # The pointer
//
// A press on a text of the scope places the caret there (the selection is
// empty), a drag moves the focus to the position under the pointer, which
// may be on any text of the scope. A double click selects a word and a
// triple click a paragraph of one part, and dragging after them extends by
// words or paragraphs. Shift extends the selection to the press. The click
// count is Input.Clicks (the app's own multi-click count until the platform
// supplies the system's). With ScopeOpts.View, a press on the view's
// background (not on another clickable box) starts a selection at the
// nearest text too, and a drag past the view's top or bottom edge scrolls
// on by itself, faster the farther the pointer is (Items' ScrollBy when it is
// an EdgeScroller, as base.VListState is, or the View box's own scroll
// offset), with the focus at the start of the first text built or the end of
// the last: a drag through three screens selects everything it passed. The
// move that comes with the release is where the drag ends. A gesture that
// selects text on a link (a drag that moves, a double or triple click, a
// shift-click) does not click it; a plain click does. A modal overlay that
// opens over the scope ends its drag, and the scope does not take the edit
// keys beneath it.
//
// # Keys and the clipboard
//
// The scope takes the edit keys while it is focused: the last press in the
// window began in it, the keyboard focus has not moved since (a press on one
// of its texts takes the focus from a text field), the window is active and
// no text field has the focus (SelectionFocused). It then takes primary + A
// (select all: from the first item's start to the last item's end, building
// nothing) and primary + C, and the edit actions EditSelectAll and EditCopy
// (a native Edit menu; package menu turns commands with those roles into them
// while the scope is focused). Copy hands the app the range: with
// ScopeOpts.Text the scope assembles the plain text (AppendSelection) and
// puts it on the clipboard through Context.ClipboardSet; without it
// ScopeSignal.Copy asks the app to. Only plain text is copied.
//
// # Look
//
// The highlight is TextEditStyle.Selection, the system's highlight colour
// (the kit sets it from theme.TextSelection, which follows the user's
// Highlight colour, not the accent), and TextEditStyle.SelectionInactive
// (unemphasizedSelectedTextBackgroundColor) while the scope is not focused or
// the window is inactive. It is drawn behind the glyphs with the paragraph's
// range rectangles (bidirectional lines get one per visual stretch), and a
// selection that goes on past a line's end covers the rest of the line, as
// in AppKit. Only built texts are drawn.
//
// # Accessibility
//
// RichSelection gives the selected byte range of one RichText box (an
// element), and Selected the scope's range: what assistive technologies'
// selected-text queries read (N7).
//
// # Allocation
//
// Pressing, dragging, scrolling a selection, ⌘A, ForEachSelected and copying
// allocate nothing once warm (the copy's buffer is kept for the next, up to
// MaxKeptCopy; see ScopeOpts.Text for what a copy costs).

const (
	// PartEnd as a Pos.Part is the end of the whole item, after all its parts
	// (Off is then ignored).
	PartEnd uint16 = 0xFFFF
	// OffEnd as a Pos.Off is the end of the part.
	OffEnd uint32 = 0xFFFFFFFF
)

// Pos is a selection endpoint: byte Off of part Part of the app's item Item.
// See SelectionScope.
type Pos struct {
	// Item is the app's id of the item.
	Item uint64
	// Part is the text part of the item (RichOpts.Part), or PartEnd.
	Part uint16
	// Off is a byte offset into the part's text, or OffEnd.
	Off uint32
}

// Enumerator orders and walks the items of a selection scope in document
// order. *base.VListState implements it in O(1) from the list's ids.
type Enumerator interface {
	// IndexOf is the item's current position, for ordering endpoints, and
	// whether the item exists.
	IndexOf(id uint64) (int, bool)
	// First and Last are the first and last items.
	First() (uint64, bool)
	Last() (uint64, bool)
	// Next is the item after id.
	Next(id uint64) (uint64, bool)
}

// ItemFate is what became of an item an ItemTracker follows.
type ItemFate struct {
	// ID is the item followed (the last TrackItem of the slot).
	ID uint64
	// Removed: an edit removed it. Next is then the first surviving item
	// after the removed run and Prev the last before it, where HasNext and
	// HasPrev say there is one.
	Removed          bool
	Next, Prev       uint64
	HasNext, HasPrev bool
	// Reset: the whole list was replaced (Removed is set too).
	Reset bool
}

// ItemTracker is an Enumerator that follows items through the edits of its
// list, so that a scope can move an endpoint whose item is removed. A scope
// uses slots 0 (the anchor) and 1 (the focus). *base.VListState implements
// it with its marks.
type ItemTracker interface {
	// TrackItem makes slot follow item id.
	TrackItem(slot int, id uint64)
	// TrackedItem is what became of the item slot follows.
	TrackedItem(slot int) ItemFate
}

// EdgeScroller scrolls the view of a scope's items by dy pixels in the next
// frame (down when positive): a drag past the edge of ScopeOpts.View calls
// it. *base.VListState implements it.
type EdgeScroller interface {
	ScrollBy(dy float64)
}

// SelectionText gives a scope the text of its items for copying.
type SelectionText interface {
	// AppendSelected appends to dst the plain text of item id from part
	// fromPart at byte fromOff to part toPart at byte toOff (exclusive),
	// either of which may be a sentinel (PartEnd, OffEnd); SelectedBytes cuts
	// one part's text. The parts in between are whole.
	AppendSelected(dst []byte, id uint64, fromPart uint16, fromOff uint32, toPart uint16, toOff uint32) []byte
}

// ScopeOpts are the options of a selection scope (BeginSelection).
type ScopeOpts struct {
	// Items orders and walks the items (see Enumerator); nil: the ids are 0
	// to n-1, n one more than the largest the scope's texts declared in the
	// last frame. It must be comparable (a pointer, such as
	// *base.VListState): the scope compares it with the last frame's to
	// look up its optional interfaces (ItemTracker, EdgeScroller) only when
	// it changes.
	Items Enumerator
	// View is the box the items are seen through (a VList's Outer), 0 for
	// none: a press on its background starts a selection, and a drag past
	// its top or bottom edge scrolls (see SelectionScope).
	View ID
	// Text, when set, is the text a copy assembles and puts on the clipboard
	// (Context.ClipboardSet). The copy is synchronous and builds the whole
	// selection's text in memory, O(selected bytes) in time and space: ⌘A
	// then ⌘C over a long transcript costs its whole text. The buffer is kept
	// for the next copy up to MaxKeptCopy bytes and released after a larger
	// one (ReleaseSelectionCopy releases it at once). An app that must
	// stream, or bound the work, leaves Text nil, takes ScopeSignal.Copy and
	// walks the range itself with ForEachSelected.
	Text SelectionText
}

// ScopeSignal is what BeginSelection reports.
type ScopeSignal struct {
	// Changed: the selection changed (a press, a drag, select all, or an edit
	// that removed an endpoint's item).
	Changed bool
	// Copy: the user asked to copy a non-empty selection (primary + C, or
	// EditCopy). With ScopeOpts.Text the text is on the clipboard already
	// (Copied); otherwise the app copies the range (ForEachSelected,
	// AppendSelection).
	Copy, Copied bool
	// Focused: the scope takes the edit keys (see SelectionFocused).
	Focused bool
	// Dragging: a drag is moving the focus.
	Dragging bool
	// EdgeScroll is how far, in pixels, a drag past the view's edge scrolled
	// the items this frame (negative: up).
	EdgeScroll float32
}

// SelectionScope is a selection over the texts built between BeginSelection
// and EndSelection, held by the app (a value next to its model: it must not
// be copied once in use). See the file comment above (selection.go) for the
// rules. A scope belongs to one window.
type SelectionScope struct {
	anchor, focus Pos
	has           bool // the endpoints are set (the selection may be empty)

	items    Enumerator   // the last BeginSelection's, nil: seqN ids
	tracker  ItemTracker  // items as an ItemTracker, nil if it is not one
	scroller EdgeScroller // items as an EdgeScroller, nil if it is not one
	seqN     uint64
	cid      uint32 // the scope's id in its context, 0 before the first BeginSelection

	drag     uint8 // selDrag* while the left button is held after a press
	uLo, uHi Pos   // the word or paragraph the drag began on
	layer    uint8 // the overlay the scope is built in, 1 + its index; 0 for the main tree

	tracked [2]uint64 // what the tracker's slots follow
	trackOn [2]bool
}

const (
	selDragNone uint8 = iota
	selDragChar
	selDragWord
	selDragPara
)

// maxScopeDepth is how deeply selection scopes may nest.
const maxScopeDepth = 4

// selState is a context's selection state (Context.sel).
type selState struct {
	frame  uint32 // the frame the records below belong to
	nextID uint32
	open   [maxScopeDepth]int32 // the open scopes: indices into scopes
	nOpen  int
	scopes []*SelectionScope // this frame's scopes, in BeginSelection order
	cur    []selText         // this frame's texts in scopes, in build order
	prev   []selText         // the last frame's

	// the focused scope: its id, the keyboard focus when it took it, and
	// the frame it was last built in
	focusID    uint32
	focusKey   Key
	focusFrame uint32
	focusLayer uint8

	// the press that began the last gesture on a text (its box), and
	// whether the gesture selected text (a drag that moved, a double or
	// triple click, a shift-click): then a link under it is not clicked
	gestureKey Key
	gestureSel bool

	copyBuf []byte // the last copy's text, kept for the next
}

// selText is a RichText box of a scope.
type selText struct {
	scope uint32
	key   Key
	item  uint64
	part  uint16
	n     uint32 // the length of its text
}

// newFrame starts the records of frame f: the last frame's texts become the
// previous ones (or none, when no scope was built in it).
func (s *selState) newFrame(f uint32) {
	if s.frame == f {
		return
	}
	if s.frame+1 == f {
		s.prev, s.cur = s.cur, s.prev[:0]
	} else {
		s.prev, s.cur = s.prev[:0], s.cur[:0]
	}
	clear(s.scopes)
	s.scopes = s.scopes[:0]
	s.nOpen = 0
	s.frame = f
}

// itemsLookups counts setItems's lookups of the optional interfaces (tests).
var itemsLookups int

// scopeSeq is the Enumerator of a scope without Items.
type scopeSeq SelectionScope

func (q *scopeSeq) IndexOf(id uint64) (int, bool) { return int(id), id < q.seqN }
func (q *scopeSeq) First() (uint64, bool)         { return 0, q.seqN > 0 }
func (q *scopeSeq) Last() (uint64, bool)          { return q.seqN - 1, q.seqN > 0 }
func (q *scopeSeq) Next(id uint64) (uint64, bool) { return id + 1, id+1 < q.seqN }

// setItems makes e the scope's Enumerator. The interfaces it also implements
// (ItemTracker, EdgeScroller) are looked up only when the app passes a
// different value (==: an Enumerator must be comparable, as pointers are): a
// type assertion to an interface may allocate the first times it runs.
func (sc *SelectionScope) setItems(e Enumerator) {
	if e == sc.items {
		return
	}
	itemsLookups++
	sc.items = e
	sc.tracker, _ = e.(ItemTracker)
	sc.scroller, _ = e.(EdgeScroller)
	sc.trackOn = [2]bool{}
}

// enum is the scope's Enumerator.
func (sc *SelectionScope) enum() Enumerator {
	if sc.items != nil {
		return sc.items
	}
	return (*scopeSeq)(sc)
}

// itemCmp orders the items a and b: -1, 0 or 1, and false when either is
// unknown.
func (sc *SelectionScope) itemCmp(a, b uint64) (int, bool) {
	e := sc.enum()
	if a == b {
		_, ok := e.IndexOf(a)
		return 0, ok
	}
	ia, ok1 := e.IndexOf(a)
	ib, ok2 := e.IndexOf(b)
	switch {
	case !ok1 || !ok2:
		return 0, false
	case ia < ib:
		return -1, true
	case ia > ib:
		return 1, true
	}
	return 0, true
}

// cmp orders the positions a and b.
func (sc *SelectionScope) cmp(a, b Pos) (int, bool) {
	r, ok := sc.itemCmp(a.Item, b.Item)
	if r != 0 || !ok {
		return r, ok
	}
	switch {
	case a.Part < b.Part:
		return -1, true
	case a.Part > b.Part:
		return 1, true
	case a.Part == PartEnd || a.Off == b.Off:
		return 0, true
	case a.Off < b.Off:
		return -1, true
	}
	return 1, true
}

// Selected returns the selection in document order, from before to, and
// whether it is non-empty (with both endpoints' items present).
func (sc *SelectionScope) Selected() (from, to Pos, ok bool) {
	sc.sync()
	return sc.selected()
}

// selected is Selected without sync.
func (sc *SelectionScope) selected() (from, to Pos, ok bool) {
	if !sc.has {
		return
	}
	r, known := sc.cmp(sc.anchor, sc.focus)
	switch {
	case !known || r == 0:
		return Pos{}, Pos{}, false
	case r < 0:
		from, to = sc.anchor, sc.focus
	default:
		from, to = sc.focus, sc.anchor
	}
	if from.Part == PartEnd && to.Part == 0 && to.Off == 0 {
		if n, ok := sc.enum().Next(from.Item); ok && n == to.Item {
			return Pos{}, Pos{}, false // the end of an item and the start of the next: nothing between
		}
	}
	return from, to, true
}

// Endpoints returns the anchor and the focus as set, and whether they are.
func (sc *SelectionScope) Endpoints() (anchor, focus Pos, ok bool) {
	sc.sync()
	return sc.anchor, sc.focus, sc.has
}

// Set sets the anchor and the focus.
func (sc *SelectionScope) Set(anchor, focus Pos) {
	sc.anchor, sc.focus, sc.has = anchor, focus, true
	sc.track()
}

// Clear removes the selection.
func (sc *SelectionScope) Clear() {
	sc.anchor, sc.focus, sc.has = Pos{}, Pos{}, false
	sc.drag = selDragNone
	sc.trackOn = [2]bool{}
}

// SelectAll selects from the start of the first item to the end of the last
// (the Enumerator's), building nothing, and reports whether there is an
// item.
func (sc *SelectionScope) SelectAll() bool {
	e := sc.enum()
	first, ok1 := e.First()
	last, ok2 := e.Last()
	if !ok1 || !ok2 {
		return false
	}
	sc.Set(Pos{Item: first}, Pos{Item: last, Part: PartEnd})
	return true
}

// track makes the tracker follow the endpoints' items.
func (sc *SelectionScope) track() {
	t := sc.tracker
	if t == nil || !sc.has {
		return
	}
	for i, id := range [2]uint64{sc.anchor.Item, sc.focus.Item} {
		if !sc.trackOn[i] || sc.tracked[i] != id {
			t.TrackItem(i, id)
			sc.tracked[i], sc.trackOn[i] = id, true
		}
	}
}

// sync moves the endpoints whose items an edit removed (see the file
// comment), and reports whether it changed them.
func (sc *SelectionScope) sync() bool {
	t := sc.tracker
	if t == nil || !sc.has {
		return false
	}
	moved := 0
	for i := 0; i < 2; i++ {
		p := &sc.anchor
		if i == 1 {
			p = &sc.focus
		}
		if !sc.trackOn[i] || sc.tracked[i] != p.Item {
			continue
		}
		f := t.TrackedItem(i)
		if f.ID != p.Item || !f.Removed {
			continue
		}
		sc.trackOn[i] = false
		start, end := Pos{Item: f.Next}, Pos{Item: f.Prev, Part: PartEnd}
		switch {
		case f.Reset || !f.HasNext && !f.HasPrev:
			sc.Clear()
			return true
		case i == 0 && f.HasPrev, i == 1 && !f.HasNext:
			*p = end
		default:
			*p = start
		}
		moved++
	}
	if moved == 0 {
		return false
	}
	if _, _, ok := sc.selected(); !ok {
		sc.Clear()
		return true
	}
	sc.track()
	return true
}

// cut returns the part [s, e) of the bytes of part part of item (n bytes
// long) that the selection from..to covers, and whether the selection goes on
// into a later item.
func (sc *SelectionScope) cut(from, to Pos, item uint64, part uint16, n int) (s, e int, cont, ok bool) {
	r, known := sc.itemCmp(from.Item, item)
	if !known || r > 0 {
		return
	}
	if r == 0 {
		switch {
		case from.Part > part:
			return
		case from.Part == part:
			s = int(min(from.Off, uint32(n)))
		}
	}
	e = n
	r, known = sc.itemCmp(to.Item, item)
	if !known || r < 0 {
		return
	}
	if r == 0 {
		switch {
		case to.Part < part:
			return
		case to.Part == part:
			e = int(min(to.Off, uint32(n)))
		}
	} else {
		cont = true
	}
	return s, e, cont, s < e || cont && s == n
}

// selIter walks the items of a selection.
type selIter struct {
	sc       *SelectionScope
	from, to Pos
	id       uint64
	done     bool
}

func (sc *SelectionScope) iter() selIter {
	from, to, ok := sc.Selected()
	return selIter{sc: sc, from: from, to: to, id: from.Item, done: !ok}
}

// next returns the next item of the selection with its cut points; items
// the selection covers nothing of (it starts at the end of the first, or
// ends at the start of the last) are skipped.
func (it *selIter) next() (id uint64, fp uint16, fo uint32, tp uint16, to uint32, ok bool) {
	for !it.done {
		id = it.id
		fp, fo, tp, to = 0, 0, PartEnd, 0
		if id == it.from.Item {
			fp, fo = it.from.Part, it.from.Off
		}
		if id == it.to.Item {
			tp, to = it.to.Part, it.to.Off
			it.done = true
		} else if n, more := it.sc.enum().Next(id); more {
			it.id = n
		} else {
			it.done = true
		}
		if fp == PartEnd || tp == 0 && to == 0 || fp == tp && fo >= to {
			continue
		}
		return id, fp, fo, tp, to, true
	}
	return 0, 0, 0, 0, 0, false
}

// ForEachSelected walks the selection of sc in document order through its
// Enumerator, calling fn once per item it covers: whole items get (0, 0,
// PartEnd, 0), the first and the last their cut points (the endpoints, with
// the sentinels where they are). Items it covers nothing of are skipped. It
// builds nothing and allocates nothing; the app fetches each item's text
// from its model.
func ForEachSelected(sc *SelectionScope, fn func(id uint64, fromPart uint16, fromOff uint32, toPart uint16, toOff uint32)) {
	it := sc.iter()
	for {
		id, fp, fo, tp, to, ok := it.next()
		if !ok {
			return
		}
		fn(id, fp, fo, tp, to)
	}
}

// AppendSelection appends the plain text of the selection of sc to dst, the
// items' text from src (SelectionText.AppendSelected) separated by newlines,
// and returns it: what a scope's copy puts on the clipboard.
func AppendSelection(dst []byte, sc *SelectionScope, src SelectionText) []byte {
	it := sc.iter()
	first := true
	for {
		id, fp, fo, tp, to, ok := it.next()
		if !ok {
			return dst
		}
		if !first {
			dst = append(dst, '\n')
		}
		first = false
		dst = src.AppendSelected(dst, id, fp, fo, tp, to)
	}
}

// SelectedBytes returns the bytes of b, the text of part part of an item,
// that the cut points (fromPart, fromOff) to (toPart, toOff) of the item
// cover (see SelectionText): all of it for a part between them, nil for one
// outside.
func SelectedBytes(b []byte, part uint16, fromPart uint16, fromOff uint32, toPart uint16, toOff uint32) []byte {
	if part < fromPart || part > toPart || part == PartEnd {
		return nil
	}
	s, e := 0, len(b)
	if part == fromPart {
		s = int(min(fromOff, uint32(len(b))))
	}
	if part == toPart {
		e = int(min(toOff, uint32(len(b))))
	}
	if s >= e {
		return nil
	}
	return b[s:e]
}

// BeginSelection opens the selection scope sc: the RichText boxes built until
// EndSelection are its texts (RichOpts.Item and Part say where each is). It
// applies the frame's pointer, keys and edit actions to the selection against
// the last frame's layout, and moves endpoints whose items were removed; see
// SelectionScope. Scopes nest up to four deep; a text belongs to the
// innermost.
func (c *Context) BeginSelection(sc *SelectionScope, o ScopeOpts) ScopeSignal {
	s := &c.sel
	s.newFrame(c.frame)
	if s.nOpen >= maxScopeDepth {
		panic("ui: selection scopes nested too deep")
	}
	if sc.cid == 0 {
		s.nextID++
		sc.cid = s.nextID
	}
	sc.setItems(o.Items)
	sc.layer = 0
	if c.nparents > 0 {
		sc.layer = c.boxes[c.parents[c.nparents-1]].ovl
	}
	s.open[s.nOpen] = int32(len(s.scopes))
	s.nOpen++
	s.scopes = append(s.scopes, sc)

	a0, f0, h0 := sc.anchor, sc.focus, sc.has
	var sig ScopeSignal
	sc.sync()
	c.selPointer(sc, &o, &sig)
	if s.focusID == sc.cid {
		s.focusFrame = c.frame
	}
	sig.Focused = c.scopeFocused(sc)
	if sig.Focused {
		in := &c.in
		prim := PrimaryMod()
		all, cp := false, false
		if c.fieldOwner == 0 {
			for i := 0; i < in.NEdit; i++ {
				switch in.Edit[i] {
				case EditSelectAll:
					all = true
				case EditCopy:
					cp = true
				}
			}
		}
		all = c.TakeKey('A', prim) || all
		cp = c.TakeKey('C', prim) || cp
		if all {
			sc.drag = selDragNone
			sc.SelectAll()
		}
		if cp {
			c.selCopy(sc, &o, &sig)
		}
	}
	sc.track()
	sig.Changed = sc.anchor != a0 || sc.focus != f0 || sc.has != h0
	sig.Dragging = sc.drag != selDragNone
	return sig
}

// EndSelection closes the innermost selection scope.
func (c *Context) EndSelection() {
	s := &c.sel
	if s.nOpen == 0 || s.frame != c.frame {
		panic("ui: EndSelection without BeginSelection")
	}
	s.nOpen--
	sc := s.scopes[s.open[s.nOpen]]
	if sc.items == nil {
		n := uint64(0)
		for i := range s.cur {
			if t := &s.cur[i]; t.scope == sc.cid {
				n = max(n, t.item+1)
			}
		}
		sc.seqN = n
	}
}

// SelectionFocused reports whether a selection scope takes the edit keys:
// the last press in the window began in it, the keyboard focus has not moved
// since, it was built in the last frame or this one, the window is active and
// no text field has the focus. Package menu gives such a scope the commands
// with the Copy and Select All roles, as edit actions (Context.QueueEdit).
func (c *Context) SelectionFocused() bool {
	s := &c.sel
	return s.focusID != 0 && s.focusFrame+1 >= c.frame && c.focusKey == s.focusKey && c.in.Focused && !c.fieldFocus &&
		!c.layerBlocked(s.focusLayer)
}

// layerBlocked reports whether a modal overlay above the input layer l (1 +
// an overlay's index, 0 for the main tree) takes the input.
func (c *Context) layerBlocked(l uint8) bool { return c.modalIdx >= 0 && int(l)-1 < c.modalIdx }

// scopeFocused reports whether sc is the focused scope (SelectionFocused).
func (c *Context) scopeFocused(sc *SelectionScope) bool {
	return c.sel.focusID == sc.cid && c.SelectionFocused()
}

// selCopy copies the selection of sc (ScopeSignal.Copy).
func (c *Context) selCopy(sc *SelectionScope, o *ScopeOpts, sig *ScopeSignal) {
	if _, _, ok := sc.Selected(); !ok {
		return
	}
	sig.Copy = true
	if o.Text == nil || c.ClipboardSet == nil {
		return
	}
	s := &c.sel
	s.copyBuf = AppendSelection(s.copyBuf[:0], sc, o.Text)
	c.ClipboardSet(s.copyBuf)
	sig.Copied = true
	if cap(s.copyBuf) > MaxKeptCopy {
		s.copyBuf = nil
	}
}

// MaxKeptCopy is the largest buffer a context keeps from one selection copy
// (ScopeOpts.Text) to the next: copies up to it allocate nothing once warm;
// the buffer of a larger one is released after it.
const MaxKeptCopy = 1 << 20

// ReleaseSelectionCopy releases the buffer kept from the last selection copy.
func (c *Context) ReleaseSelectionCopy() { c.sel.copyBuf = nil }

// selPointer applies the frame's press and drag to sc.
func (c *Context) selPointer(sc *SelectionScope, o *ScopeOpts, sig *ScopeSignal) {
	in := &c.in
	s := &c.sel
	if in.Pressed[0] && !c.pressConsumed {
		start := false
		if k := c.pressedKey[0]; k != 0 {
			for i := range s.prev {
				if t := &s.prev[i]; t.scope == sc.cid && t.key == k {
					start = true
					break
				}
			}
		} else if o.View != 0 && !c.layerBlocked(sc.layer) && inRect(c.boxes[o.View].rect, in.MouseX, in.MouseY) && c.hotUnder(o.View) {
			start = true
		}
		switch {
		case start:
			c.selPress(sc, o)
		case s.focusID == sc.cid:
			s.focusID = 0
			sc.drag = selDragNone
		}
		return
	}
	if sc.drag == selDragNone {
		return
	}
	switch {
	case in.Cancelled[0] || c.layerBlocked(sc.layer):
		// the window lost the button, or a modal opened over the scope
		sc.drag = selDragNone
	case !in.Down[0]:
		// released: the move that came with the release counts, without
		// another step of auto-scrolling
		if in.Released[0] {
			c.selDrag(sc, o, sig, true)
		}
		sc.drag = selDragNone
	default:
		c.selDrag(sc, o, sig, false)
	}
}

// inRect reports whether (x, y) is in r.
func inRect(r [4]float32, x, y float32) bool { return x >= r[0] && x < r[2] && y >= r[1] && y < r[3] }

// hotUnder reports whether the box under the pointer is view or in it, or
// there is none.
func (c *Context) hotUnder(view ID) bool {
	for id, n := c.hotID, 0; id != 0 && n < maxDepth; id, n = c.boxes[id].parent, n+1 {
		if id == view {
			return true
		}
	}
	return c.hotID == 0
}

// selPress starts a selection at the press (selPointer).
func (c *Context) selPress(sc *SelectionScope, o *ScopeOpts) {
	in := &c.in
	p, b, ok := c.selHit(sc, o, in.MouseX, in.MouseY)
	if !ok {
		return
	}
	switch clicks := max(in.Clicks[0], 1); {
	case clicks >= 2:
		mode := selDragWord
		if clicks >= 3 {
			mode = selDragPara
		}
		lo, hi := unitBounds(b, int(p.Off), mode)
		sc.uLo, sc.uHi = Pos{p.Item, p.Part, uint32(lo)}, Pos{p.Item, p.Part, uint32(hi)}
		sc.Set(sc.uLo, sc.uHi)
		sc.drag = mode
	case in.Mods&ModShift != 0 && sc.has:
		sc.Set(sc.anchor, p)
		sc.drag = selDragChar
	default:
		sc.Set(p, p)
		sc.drag = selDragChar
	}
	// a press on selectable text takes the keyboard focus from a text field,
	// as a click in a text view takes first responder
	if v, ok := c.table.Get(uint64(c.focusKey)); ok && c.focusKey != 0 && c.boxes[v].field {
		c.focusKey = 0
	}
	s := &c.sel
	s.focusID, s.focusKey, s.focusFrame, s.focusLayer = sc.cid, c.focusKey, c.frame, sc.layer
	s.gestureKey, s.gestureSel = c.pressedKey[0], sc.drag != selDragChar || sc.anchor != sc.focus
}

// selOwnsClick reports whether the press on the box with key k that ends
// this frame selected text, so that the box does not also report a click
// (a link under a selection drag or a double click).
func (c *Context) selOwnsClick(k Key) bool {
	s := &c.sel
	return s.gestureSel && s.gestureKey == k && k != 0
}

// selDrag moves the focus of sc to the pointer, scrolling past the view's
// edge unless final (the release; selPointer).
func (c *Context) selDrag(sc *SelectionScope, o *ScopeOpts, sig *ScopeSignal, final bool) {
	defer c.selMoved(sc)
	in := &c.in
	var p Pos
	var b []byte
	ok := false
	if o.View != 0 && !final {
		vb := &c.boxes[o.View]
		r := vb.rect
		var out float32
		switch {
		case in.MouseY < r[1]:
			out = min(in.MouseY-r[1], -c.Pt(4))
		case in.MouseY >= r[3]:
			out = max(in.MouseY-r[3]+1, c.Pt(4))
		}
		if out != 0 {
			p, b, ok = c.selEdge(sc, out < 0)
			d := out * 12 * c.dt
			switch {
			case sc.scroller != nil:
				sc.scroller.ScrollBy(float64(d))
			case vb.flags&ScrollY != 0:
				c.SetScroll(o.View, vb.scroll[0], vb.scroll[1]+float64(d))
			}
			sig.EdgeScroll = d
			c.RequestFrame()
		}
	}
	if !ok {
		p, b, ok = c.selHit(sc, o, in.MouseX, in.MouseY)
	}
	if !ok {
		return
	}
	if sc.drag == selDragChar {
		sc.Set(sc.anchor, p)
		return
	}
	lo, hi := unitBounds(b, int(p.Off), sc.drag)
	if r, _ := sc.cmp(p, sc.uLo); r < 0 {
		sc.Set(sc.uHi, Pos{p.Item, p.Part, uint32(lo)})
	} else {
		sc.Set(sc.uLo, Pos{p.Item, p.Part, uint32(hi)})
	}
}

// selMoved notes a drag that made the selection non-empty (selOwnsClick).
func (c *Context) selMoved(sc *SelectionScope) {
	if sc.anchor != sc.focus {
		c.sel.gestureSel = true
	}
}

// selBox returns the box of the last frame's text t, with a layout to hit
// test.
func (c *Context) selBox(t *selText) (ID, *box, bool) {
	v, ok := c.table.Get(uint64(t.key))
	if !ok {
		return 0, nil, false
	}
	b := &c.boxes[v]
	if b.lastFrame+1 != c.frame && b.lastFrame != c.frame || b.hid || !b.rp.ok || b.rp.gen != c.lay.ParaGeneration() {
		return 0, nil, false
	}
	return ID(v), b, true
}

// selHit returns the position of sc nearest to the window point (x, y) in
// the last frame's layout, and the text of its part: in the text under the
// point, or the nearest one (vertically first), at its start when the point
// is above it and its end when below. With a view, only texts in it count.
func (c *Context) selHit(sc *SelectionScope, o *ScopeOpts, x, y float32) (Pos, []byte, bool) {
	s := &c.sel
	var vr [4]float32
	if o.View != 0 {
		vr = c.boxes[o.View].rect
	}
	best := -1
	var bid ID
	var bdy, bdx float32
	for i := range s.prev {
		t := &s.prev[i]
		if t.scope != sc.cid {
			continue
		}
		id, b, ok := c.selBox(t)
		if !ok {
			continue
		}
		r := b.rect
		if o.View != 0 && (r[3] <= vr[1] || r[1] >= vr[3]) {
			continue
		}
		dy, dx := axisDist(y, r[1], r[3]), axisDist(x, r[0], r[2])
		if best < 0 || dy < bdy || dy == bdy && dx < bdx {
			best, bid, bdy, bdx = i, id, dy, dx
		}
	}
	if best < 0 {
		return Pos{}, nil, false
	}
	t := &s.prev[best]
	b := &c.boxes[bid]
	pv, _ := c.prevRich(b)
	r := b.rect
	off := 0
	switch {
	case y < r[1]:
	case y >= r[3]:
		off = len(pv.Text)
	default:
		off, _ = c.RichHit(bid, x, y)
	}
	return Pos{Item: t.item, Part: t.part, Off: uint32(off)}, pv.Text, true
}

// axisDist is how far v is outside [lo, hi).
func axisDist(v, lo, hi float32) float32 {
	switch {
	case v < lo:
		return lo - v
	case v >= hi:
		return v - hi
	}
	return 0
}

// selEdge returns the start of the first text of sc built in the last frame
// (top) or the end of the last, with its part's text.
func (c *Context) selEdge(sc *SelectionScope, top bool) (Pos, []byte, bool) {
	s := &c.sel
	var bp Pos
	var bt []byte
	found := false
	for i := range s.prev {
		t := &s.prev[i]
		if t.scope != sc.cid {
			continue
		}
		_, b, ok := c.selBox(t)
		if !ok {
			continue
		}
		p := Pos{Item: t.item, Part: t.part}
		pv, _ := c.prevRich(b)
		if !top {
			p.Off = uint32(len(pv.Text))
		}
		if found {
			r, known := sc.cmp(p, bp)
			if !known || top && r >= 0 || !top && r <= 0 {
				continue
			}
		}
		bp, bt, found = p, pv.Text, true
	}
	return bp, bt, found
}

// unitBounds returns the word (selDragWord) or paragraph (selDragPara: the
// text between newlines, with the newline that ends it) of b at off.
func unitBounds(b []byte, off int, mode uint8) (lo, hi int) {
	off = min(max(off, 0), len(b))
	if mode == selDragWord {
		return wordBounds(b, off)
	}
	lo, hi = off, off
	for lo > 0 && b[lo-1] != '\n' {
		lo--
	}
	for hi < len(b) && b[hi] != '\n' {
		hi++
	}
	if hi < len(b) {
		hi++
	}
	return lo, hi
}

// selOpen is the innermost open selection scope, nil for none.
func (c *Context) selOpen() *SelectionScope {
	s := &c.sel
	if s.nOpen == 0 || s.frame != c.frame {
		return nil
	}
	return s.scopes[s.open[s.nOpen-1]]
}

// richSel returns the selected bytes [s, e) of the RichText record r (its
// text n bytes long), whether the selection goes on past its end into a
// later item, and the highlight's colour.
func (c *Context) richSel(r *richRec, n int) (s, e int, cont bool, col uint32, ok bool) {
	if r.selScope == 0 || int(r.selScope) > len(c.sel.scopes) || c.sel.frame != c.frame {
		return
	}
	sc := c.sel.scopes[r.selScope-1]
	from, to, any := sc.Selected()
	if !any {
		return
	}
	s, e, cont, ok = sc.cut(from, to, r.item, r.part, n)
	col = c.TextEdit.SelectionInactive
	if c.scopeFocused(sc) {
		col = c.TextEdit.Selection
	}
	return s, e, cont, col, ok
}

// RichSelection returns the selected byte range [from, to) of the body of the
// RichText box id, built this frame in a selection scope (during the build or
// after End, until the next Begin), and whether any of it is selected: the
// selected text range of that element for assistive technologies.
func (c *Context) RichSelection(id ID) (from, to int, ok bool) {
	if id == 0 || int(id) >= len(c.boxes) {
		return
	}
	b := &c.boxes[id]
	r := c.recOf(b)
	if r == nil {
		return
	}
	s, e, _, _, ok := c.richSel(r, len(c.fr.Cur().Bytes(b.txt)))
	if !ok || s >= e {
		return 0, 0, false
	}
	return s, e, true
}

// emitRichSel draws the selection highlight of the RichText box b (record
// r, paragraph pv) behind its glyphs: content origin (x0, y0), width w.
func (c *Context) emitRichSel(l *gpu.DrawList, b *box, r *richRec, pv *text.ParaView, x0, y0, w float32, clip [4]float32) {
	s, e, cont, col, ok := c.richSel(r, len(pv.Text))
	if !ok || col&255 == 0 {
		return
	}
	if cont {
		e = len(pv.Text) + 1
	}
	al := b.align
	c.rects = pv.SelectionRectsAligned(s, e, w, func(i int) float32 { return alignDX(pv, i, w, al) }, c.rects[:0])
	col = b.col(col)
	for _, q := range c.rects {
		rr := [4]float32{snap(x0 + q[0]), snap(y0 + q[1]), snap(x0 + q[2]), snap(y0 + q[3])}
		if rr[2] > rr[0] {
			l.Add(gpu.Quad{Rect: rr, Clip: clip, Color: [4]uint32{col, col, col, col}})
		}
	}
}
