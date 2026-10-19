// Package base holds the headless primitives of the widget layer (D40):
// behaviour, focus, semantics and visual state, built on ui and plain data,
// with the styling left to the components above them.
//
// # Variable-height lists
//
// BeginVList builds a virtual list whose items have any height: only the items
// in view and in the overscan are built, and each is measured after layout and
// remembered, so a list of a million messages costs what a screenful does.
// The position is an item and an offset into it (the anchor), held by the
// item's id, so that loading history above, deleting, inserting and reordering
// keep the view still (VListState):
//
//	var st base.VListState // next to the app's model
//	st.Reserve(n)
//	st.Append(ids)
//
//	v := base.BeginVList(c, "transcript", &st, base.VListOpts{Estimate: 64, StickToBottom: true})
//	for i := v.First; i < v.Last; i++ {
//		id := v.Item(i)
//		app.buildMessage(c, id) // any boxes
//	}
//	base.EndVList(&v)
//
// # Allocation
//
// A frame allocates nothing once warm, scrolling and measuring included, and
// so do edits within Reserve.
package base

import (
	"math"

	"gonativeui/ui"
)

// VListOpts configures a VList.
type VListOpts struct {
	// Estimate is the height in points of an item that was never measured
	// (default 44). Unmeasured items count as the estimate in the total and
	// the scroll bar until they are built.
	Estimate float32
	// Overscan is how far in points above and below the view items are built
	// and measured (default: a quarter of the view's height).
	Overscan float32
	// StickToBottom pins the view to the bottom while it is there: items that
	// arrive or grow (an image loading) keep the bottom in view. The list
	// starts at the bottom, and a Reset that loses the anchor goes back there.
	StickToBottom bool
	// MaxItems bounds the items built in a frame (default 256).
	MaxItems int
	// Line is how far in points Up and Down scroll (default 40).
	Line float32
	// W and H size the list's box; the zero Size fills the parent.
	W, H ui.Size
}

// VList is an open variable-height list (BeginVList). Build the items First to
// Last-1 with Item, in order, then EndVList.
type VList struct {
	// First and Last are the items to build: those in view, widened by the
	// overscan.
	First, Last int
	// Len is the number of items.
	Len int
	// Focused: the list has the keyboard focus (it takes Up, Down, Page Up,
	// Page Down, Home and End, and the primary modifier with Up and Down).
	Focused bool
	// AtEnd: the view shows the end of the list.
	AtEnd bool
	// Outer is the list's clipping box, Body the box that scrolls and holds
	// the items: a styled list builds its scroll bar from them
	// (ui.Context.ScrollExtent and Scroll of Body; the extent is the
	// estimated total).
	Outer, Body ui.ID

	c      *ui.Context
	st     *VListState
	okey   ui.Key
	key    ui.Key // the parent of the item keys
	next   int
	open   bool
	anchor int     // the anchor's index: the first item of the flow
	bottom bool    // every item is in the pile, which ends at the view's bottom
	pileY  float32 // where the floating pile ends, from the top of Body
	pileH  float32 // the height of the pile in the flow, when bottom
	inPile bool
	s      float64 // the scroll offset of Body this frame
	cont   float64 // the content height declared this frame
}

// vlPileH is the height of a pile: the box that holds the items above the
// anchor, bottom-aligned. It is far taller than what it holds, and small
// enough that float32 positions in it stay exact to 1/128 pixel.
const vlPileH = 1 << 16

// vlRowH is the pitch of the virtual rows the body is declared with
// (ui.Context.ListRange): positions to 1/256 pixel in float64.
const vlRowH = float32(1.0 / 256)

// BeginVList starts a list of the items of st, as wide and high as its parent
// unless o says otherwise, scrolling vertically and taking the keyboard to
// scroll. It syncs st (VListState.Sync), measures the items built in the last
// frame, applies the frame's scrolling and ScrollTo, and chooses the items to
// build.
func BeginVList(c *ui.Context, label string, st *VListState, o VListOpts) VList {
	st.Sync(c.Frame())
	_, ident := ui.SplitLabel(label)
	okey := ui.HashString(c.ParentKey(), ident)
	bkey := ui.KeyN(okey, 1)
	ikey := ui.KeyN(okey, 2)
	if o.Estimate <= 0 {
		o.Estimate = 44
	}
	if o.MaxItems <= 0 {
		o.MaxItems = 256
	}
	if o.Line <= 0 {
		o.Line = 40
	}
	rescaled := false
	if sc := c.Scale(); st.scale != sc {
		if st.scale > 0 {
			rescaled = true
			// A rare event (the window moved to another display): finish any
			// rebuild now rather than rescale two buffers.
			for st.job.on {
				st.finishNow()
			}
			r := sc / st.scale
			st.f.rescale(r)
			st.offset *= float64(r)
		}
		st.scale = sc
	}
	est := c.Pt(o.Estimate)
	st.est = est

	// the heights of the items built in the last frame, from its layout
	// (unless that was at another scale)
	last := st.built[st.cur]
	if rescaled {
		last = last[:0]
	}
	changed := false
	for _, id := range last {
		if b, ok := c.BoxOf(ui.KeyN(ikey, id)); ok {
			_, sz := c.ScrollExtent(b)
			if st.setHeight(id, sz[1]) {
				changed = true
			}
		}
	}
	st.cur ^= 1
	if cap(st.built[st.cur]) < o.MaxItems {
		st.built[st.cur] = make([]uint64, 0, o.MaxItems)
	}
	st.built[st.cur] = st.built[st.cur][:0]
	if changed {
		c.RequestFrame() // the next frame lays out with the heights it now knows
	}

	// the view and the scrolling since the last frame
	_, wh := c.Size()
	view := float64(wh)
	var s0 float64
	have := false
	if b, ok := c.BoxOf(bkey); ok {
		_, sz := c.ScrollExtent(b)
		if sz[1] > 0 {
			view, have = float64(sz[1]), true
			_, s0 = c.Scroll(b)
		}
	}
	d := st.by
	st.by = 0
	if have && st.hasLast && !rescaled {
		d += s0 - min(max(st.lastS, 0), max(st.lastContent-view, 0))
	}
	have = have && !rescaled // ui's offset is in the old pixels: set it again
	focused := c.FocusKey() == bkey
	jump := 0 // -1 top, 1 end
	if focused {
		line := float64(c.Pt(o.Line))
		page := max(view-line, line)
		prim := ui.PrimaryMod()
		switch {
		case c.TakeKey(ui.KeyUp, 0):
			d -= line
		case c.TakeKey(ui.KeyDown, 0):
			d += line
		case c.TakeKey(ui.KeyPageUp, 0):
			d -= page
		case c.TakeKey(ui.KeyPageDown, 0):
			d += page
		case c.TakeKey(ui.KeyHome, 0), c.TakeKey(ui.KeyUp, prim):
			jump = -1
		case c.TakeKey(ui.KeyEnd, 0), c.TakeKey(ui.KeyDown, prim):
			jump = 1
		}
	}

	// the position
	f := &st.f
	n := f.n
	total := st.Total()
	maxY := max(total-view, 0)
	var y float64
	switch {
	case n == 0:
		if !st.started || st.place != placeNone {
			st.stuck = o.StickToBottom
			st.started, st.place = true, placeNone
		}
	case !st.started || st.place != placeNone:
		st.stuck = o.StickToBottom
		if st.stuck {
			y = maxY
		}
		st.started, st.place = true, placeNone
	case jump < 0:
		st.stuck = false
	case jump > 0:
		y = maxY
	case st.req && st.reqTarget(view, &y):
	case st.stuck && o.StickToBottom:
		y = maxY + d
	default:
		y = d
		if a, ok := f.index(st.anchorID); ok {
			y += f.top(a, est) + st.offset
		}
	}
	y = min(max(y, 0), maxY)
	st.stuck = o.StickToBottom && y >= maxY-0.5
	st.atEnd = y >= maxY-0.5

	v := VList{Len: n, Focused: focused, AtEnd: st.atEnd, c: c, st: st, okey: okey, key: ikey}
	if n > 0 {
		a, top := f.find(y, est)
		st.anchorID, st.offset = f.id(a), y-top
		ov := float64(c.Pt(o.Overscan))
		if o.Overscan <= 0 {
			ov = view / 4
		}
		first, _ := f.find(y-ov, est)
		lastI, _ := f.find(y+view+ov, est)
		lastI++
		if lastI-first > o.MaxItems {
			first = max(first, a-o.MaxItems/4)
			lastI = min(lastI, first+o.MaxItems)
		}
		v.First, v.Last, v.anchor = first, lastI, a
		// Pinned to the bottom with more than a view of content, the bottom
		// edge is what must not move: every item goes in the pile, which
		// ends at the view's bottom. Otherwise the anchor's top must not
		// move: the flow starts at it, and the items above it are in the
		// pile, which ends at the anchor's top.
		v.bottom = st.stuck && maxY > 0
		if v.bottom {
			v.anchor = lastI
		} else {
			v.pileY = float32(-st.offset)
		}
	} else {
		st.anchorID, st.offset = NoID, 0
	}
	v.next = v.First

	// the boxes
	w, h := o.W, o.H
	if w == (ui.Size{}) {
		w = ui.Pct(1)
	}
	if h == (ui.Size{}) {
		h = ui.Pct(1)
	}
	c.NextSize(w, h)
	v.Outer = c.BoxKeyed(ui.Clip, okey, nil)
	c.Push(v.Outer)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(0)
	c.NextPadding(0, 0, 0, 0)
	c.NextRole(ui.RoleList)
	c.NextSemName(label)
	v.Body = c.BoxKeyed(ui.ScrollY|ui.Focusable, bkey, nil)

	// The body scrolls over a content of the estimated total, declared as
	// 1/256-pixel virtual rows (ui.Context.ListRange) so that the first item
	// of the flow, the anchor, is placed in float64 at its offset above the
	// view's top. The scroll offset follows the position; while a glide of
	// ui's runs it is left alone (the items are placed from the anchor,
	// whatever the offset), unless it could not show the position.
	//
	// Pinned to the bottom, the pile of all the items is the flow's only
	// child, ending at the content's end, and the offset is set past the end:
	// the layout clamps it to the end with this frame's view height, so the
	// bottom holds in the first frame and while the view is resized too.
	rows := int(math.Ceil(total * 256))
	content := float64(rows) * float64(vlRowH)
	s := s0
	l := 0 // the flow's start, in virtual rows
	if v.bottom {
		v.pileH = float32(min(content, vlPileH))
		l = rows - int(v.pileH*256)
		s = content
		c.SetScroll(v.Body, 0, s)
	} else {
		if !have || (math.Abs(s-y) > 0.25 && !c.Animating()) || s-st.offset < -1e-9 || s > max(content-view, 0)+1e-9 {
			c.SetScroll(v.Body, 0, y)
			s = y
		}
		l = int(math.Round((s - st.offset) * 256))
	}
	if rows > 0 {
		r := c.Rect(v.Body)
		vh := float64(r[3] - r[1])
		if vh <= 0 {
			vh = float64(wh)
		}
		sc := s
		if hi := float64(rows)*float64(vlRowH) - vh; sc > hi {
			sc = hi
		}
		sc = math.Max(sc, 0)
		vf := int(math.Floor(sc / float64(vlRowH)))
		c.ListRange(v.Body, rows, vlRowH, vf-l)
	} else {
		c.ListRange(v.Body, 0, vlRowH, 0)
	}
	v.s, v.cont = s, content
	c.Push(v.Body)
	return v
}

// Key is the key of the list's scrolling box, the one that takes the keyboard
// focus: an app moves the focus to the list with ui.Context.SetFocus(v.Key()).
func (v *VList) Key() ui.Key { return v.c.KeyOf(v.Body) }

// reqTarget resolves a pending ScrollTo into the offset y of the view's top,
// and reports whether it did. A request for an id the list does not have is
// dropped, unless a rebuild may be adding it.
func (st *VListState) reqTarget(view float64, y *float64) bool {
	f := &st.f
	i, ok := f.index(st.reqID)
	if !ok {
		if !st.job.on {
			st.req = false
		}
		return false
	}
	st.req = false
	top := f.top(i, st.est)
	h := f.top(i+1, st.est) - top
	switch st.reqAlign {
	case AlignStart:
		*y = top
	case AlignCenter:
		*y = top + h/2 - view/2
	case AlignEnd:
		*y = top + h - view
	default:
		cur := 0.0
		if a, ok := f.index(st.anchorID); ok {
			cur = f.top(a, st.est) + st.offset
		}
		switch {
		case top < cur || h > view:
			*y = top
		case top+h > cur+view:
			*y = top + h - view
		default:
			*y = cur
		}
	}
	return true
}

// Item starts item i, which the app builds as the children of the returned
// box: a box as wide as the list and as high as its children, keyed by the
// item's id, so its boxes keep their state through scrolling and edits. It
// ends the item before it. It returns the item's id, which the app builds by
// (its model may be ahead of the list while VListState.Pending). Styles set
// with ui's Next calls before Item apply to the item's box, except its size.
// Items must be built in order from First.
func (v *VList) Item(i int) uint64 {
	v.EndItem()
	if i != v.next || i >= v.Last {
		panic("base: VList.Item: items must be built in order from First to Last-1")
	}
	v.next = i + 1
	c, st := v.c, v.st
	if i < v.anchor && !v.inPile {
		v.openPile()
	} else if i >= v.anchor && v.inPile {
		c.Pop()
		v.inPile = false
	}
	id := st.f.id(i)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleListItem)
	c.NextSemPos(i, v.Len)
	b := c.BoxKeyed(0, ui.KeyN(v.key, id), nil)
	c.Push(b)
	v.open = true
	if bl := &st.built[st.cur]; len(*bl) < cap(*bl) {
		*bl = append(*bl, id)
	}
	return id
}

// openPile opens the pile: a box taller than its items whose bottom is where
// they must end, the anchor's top (floating at pileY) or, pinned to the
// bottom, the content's end (the flow's only child). Its first child is a
// spacer that gives way by the items' height (strictness 0), so the items end
// exactly at the pile's bottom, whatever their heights turn out to be in this
// frame's layout.
func (v *VList) openPile() {
	c := v.c
	var fl ui.Flags
	if v.bottom {
		c.NextSize(ui.Pct(1), ui.Px(v.pileH))
	} else {
		c.NextSize(ui.Pct(1), ui.Px(vlPileH))
		c.NextPos(0, v.pileY-vlPileH)
		fl = ui.FloatingX | ui.FloatingY
	}
	c.NextAxis(ui.AxisY)
	c.NextGap(0)
	c.NextPadding(0, 0, 0, 0)
	p := c.BoxKeyed(fl, ui.KeyN(v.okey, 3), nil)
	c.Push(p)
	c.NextSize(ui.Px(0), ui.Pct(1))
	c.BoxKeyed(0, ui.KeyN(v.okey, 4), nil)
	v.inPile = true
}

// EndItem ends the item being built. Item and EndVList call it; an app calls
// it when it builds boxes between items that are not part of either (there
// should be none: the list's layout is its items).
func (v *VList) EndItem() {
	if v.open {
		v.c.Pop()
		v.open = false
	}
}

// EndVList ends the list.
func EndVList(v *VList) {
	v.EndItem()
	if v.inPile {
		v.c.Pop()
		v.inPile = false
	}
	v.c.Pop()
	v.c.Pop()
	st := v.st
	st.lastS, st.lastContent, st.hasLast = v.s, v.cont, true
}
