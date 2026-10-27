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
	// BottomAlign, with StickToBottom, puts the items of a list shorter than
	// its view at the bottom of the view rather than the top: a conversation
	// starts next to its composer, as messengers do.
	BottomAlign bool
	// MaxItems bounds the items built in a frame (default 256). Pinned to the
	// bottom, the newest items are the ones built.
	MaxItems int
	// Line is how far in points Up and Down scroll (default 40).
	Line float32
	// W and H size the list's box; the zero Size fills the parent.
	W, H ui.Size
	// BarRoom is the room in pixels the list leaves at its right for a
	// legacy scroll bar drawn over its outer box (Kit.VListBarRoom); 0
	// leaves none.
	BarRoom float32
	// ContentW, when positive, is the width in pixels of content wider than
	// the list (a file's longest line): the list then scrolls sideways too,
	// all its items by one offset. Every item is at least ContentW wide, and
	// the body scrolls on both axes, so a wheel's notches still scroll it up
	// and down, and Shift with them, or a trackpad's sideways deltas, scroll
	// it sideways. A styled list builds its horizontal bar from Body as its
	// vertical one (base.ScrollBoth).
	ContentW float32
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
	minW   float32 // VListOpts.ContentW: the items' least width, in pixels
	anchor int     // the anchor's index: the first item of the flow
	bottom bool    // every item is in the pile, which ends at the view's bottom
	short  bool    // bottom, and the list is shorter than the view (BottomAlign)
	pileY  float32 // where the floating pile ends, from the top of Body
	inPile bool
	s      float64 // the scroll offset of Body this frame
	cont   float64 // the content height declared this frame
}

// vlPileScroll is the scroll offset a pile is given: past any content, so
// that the layout clamps it to the content's end (vlist.go, openPile).
const vlPileScroll = 1e18

// vlRowH is the pitch of the virtual rows the body is declared with
// (ui.Context.ListRange): positions to 1/256 pixel in float64.
const vlRowH = float32(1.0 / 256)

// BeginVList starts a list of the items of st, as wide and high as its parent
// unless o says otherwise, scrolling vertically and taking the keyboard to
// scroll. It syncs st (VListState.Sync), measures the items built in the last
// frame, applies the frame's scrolling and ScrollTo, and chooses the items to
// build. While a rebuild is pending or the heights or a ScrollTo are
// settling, it requests the next frame (ui.Context.RequestFrame).
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
	// Heights and positions are in points; old is the scale the last frame
	// was laid out at, sc this frame's.
	old := st.sc()
	sc := float64(c.Scale())
	rescaled := st.scale > 0 && float64(st.scale) != sc
	st.scale = c.Scale()
	est := o.Estimate
	st.est = est

	// the heights of the items built in the last frame, from its layout
	last := st.built[st.cur]
	changed := false
	for _, id := range last {
		if b, ok := c.BoxOf(ui.KeyN(ikey, id)); ok {
			_, sz := c.ScrollExtent(b)
			if st.setHeight(id, float32(float64(sz[1])/old)) {
				changed = true
			}
		}
	}
	if st.ncand > 0 {
		st.pileRequests(c, okey, ikey, old)
	}
	if in := c.Input(); in.NActions > 0 {
		st.pileCandidates(c, in, ikey, last[:st.piled[st.cur]])
	}
	st.cur ^= 1
	if cap(st.built[st.cur]) < o.MaxItems {
		st.built[st.cur] = make([]uint64, 0, o.MaxItems)
	}
	st.built[st.cur] = st.built[st.cur][:0]
	st.piled[st.cur] = 0

	// the view and the scrolling since the last frame
	_, wh := c.Size()
	view := float64(wh) / sc
	var s0 float64
	have := false
	if b, ok := c.BoxOf(bkey); ok {
		_, sz := c.ScrollExtent(b)
		if sz[1] > 0 {
			view, have = float64(sz[1])/old, true
			_, s0 = c.Scroll(b)
			if st.hasLast && !rescaled {
				st.by += s0 - min(max(st.lastS, 0), max(st.lastContent-float64(sz[1]), 0))
			}
		}
	}
	d := st.by / old // ui's offsets are in the last frame's pixels
	byPt := d
	st.by = 0
	have = have && !rescaled // ui's offset is in the old pixels: set it again
	focused := c.FocusKey() == bkey
	jump := 0 // -1 top, 1 end
	// explicit: the keyboard or a ScrollTo moves the view this frame, which
	// lets go of a trackpad's stretch (ui.Context.SetScroll); otherwise the
	// offset only restates the anchor (ui.Context.AnchorScroll).
	explicit := false
	if focused {
		line := float64(o.Line)
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
		explicit = d != byPt || jump != 0
	}
	if (d != 0 || jump != 0) && st.settling {
		st.req = false // the user scrolled: a ScrollTo stops settling
	}

	// the position, in points
	f := &st.f
	if st.req && !st.job.on {
		if _, ok := f.index(st.reqID); !ok {
			st.req = false // its item is gone (or never was), and no rebuild may add it
		}
	}
	n := f.n
	total := 0.0
	if n > 0 {
		total = f.total(est)
	}
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
	case st.req && st.reqTarget(view, changed, o.MaxItems, &y):
		explicit = true
	case st.stuck && o.StickToBottom:
		y = maxY + d
	default:
		y = d
		if a, ok := f.index(st.anchorID); ok {
			y += f.top(a, est) + st.offset
		}
	}
	y = min(max(y, 0), maxY)
	st.stuck = o.StickToBottom && y >= maxY-0.5/sc
	st.atEnd = y >= maxY-0.5/sc

	v := VList{Len: n, Focused: focused, AtEnd: st.atEnd, c: c, st: st, okey: okey, key: ikey, minW: max(o.ContentW, 0)}
	if n > 0 {
		a, top := f.find(y, est)
		st.anchorID, st.offset = f.id(a), y-top
		ov := float64(o.Overscan)
		if o.Overscan <= 0 {
			ov = view / 4
		}
		first, _ := f.find(y-ov, est)
		lastI, _ := f.find(y+view+ov, est)
		lastI++
		// Pinned to the bottom with more than a view of content (or with
		// BottomAlign), the bottom edge is what must not move: every item
		// goes in the pile, which ends at the view's bottom, and the newest
		// items are the ones built. Otherwise the anchor's top must not move:
		// the flow starts at it, and the items above it are in the pile,
		// which ends at the anchor's top.
		v.bottom = st.stuck && (maxY > 0 || o.BottomAlign)
		v.short = v.bottom && maxY == 0
		if lastI-first > o.MaxItems {
			if v.bottom {
				first = lastI - o.MaxItems
			} else {
				first = max(first, a-o.MaxItems/4)
				lastI = min(lastI, first+o.MaxItems)
			}
		}
		v.First, v.Last, v.anchor = first, lastI, a
		if v.bottom {
			v.anchor = lastI
		} else {
			v.pileY = float32(-st.offset * sc)
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
	if o.BarRoom > 0 {
		c.NextPadding(0, 0, o.BarRoom, 0) // room for a legacy scroll bar
	}
	v.Outer = c.BoxKeyed(ui.Clip, okey, nil)
	c.Push(v.Outer)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(0)
	c.NextPadding(0, 0, 0, 0)
	c.NextRole(ui.RoleList)
	c.NextSemName(label)
	bf := ui.ScrollY | ui.Focusable | ui.FocusList
	if v.minW > 0 {
		bf |= ui.ScrollX // the items, as wide as the content, overflow it
	}
	v.Body = c.BoxKeyed(bf, bkey, nil)

	// The body scrolls over a content of the estimated total, declared as
	// 1/256-pixel virtual rows (ui.Context.ListRange) so that the first item
	// of the flow, the anchor, is placed in float64 at its offset above the
	// view's top. The scroll offset follows the position; while a glide of
	// ui's runs it is left alone (the items are placed from the anchor,
	// whatever the offset), unless it could not show the position.
	//
	// Pinned to the bottom, the pile of all the items is the flow's only
	// child, at the content's end, and the offset is set past the end: the
	// layout clamps it to the end with this frame's view height, so the
	// bottom holds in the first frame and while the view is resized too. A
	// short list with BottomAlign does not scroll: its pile ends at the
	// bottom of a box as high as the view.
	rows := int(math.Ceil(total * sc * 256))
	content := float64(rows) * float64(vlRowH)
	s := s0
	l := 0 // the flow's start, in virtual rows
	switch {
	case v.short:
		s = 0
		vlSetScroll(c, v.Body, s, explicit, v.minW > 0)
	case v.bottom:
		l = rows
		s = content
		vlSetScroll(c, v.Body, s, explicit, v.minW > 0)
	default:
		yPx, offPx := y*sc, st.offset*sc
		// An explicit move always sets the offset, even one the view is at
		// or while something animates: it lets go of a stretch.
		if explicit || !have || (math.Abs(s-yPx) > 0.25 && !c.Animating()) || s-offPx < -1e-9 || s > max(content-view*sc, 0)+1e-9 {
			vlSetScroll(c, v.Body, yPx, explicit, v.minW > 0)
			s = yPx
		}
		l = int(math.Round((s - offPx) * 256))
	}
	if rows > 0 {
		r := c.Rect(v.Body)
		vh := float64(r[3] - r[1])
		if vh <= 0 {
			vh = float64(wh)
		}
		sv := s
		if hi := float64(rows)*float64(vlRowH) - vh; sv > hi {
			sv = hi
		}
		sv = math.Max(sv, 0)
		vf := int(math.Floor(sv / float64(vlRowH)))
		c.ListRange(v.Body, rows, vlRowH, vf-l)
	} else {
		c.ListRange(v.Body, 0, vlRowH, 0)
	}
	v.s, v.cont = s, content
	c.Push(v.Body)
	if changed || st.req || st.ncand > 0 || st.Pending() {
		c.RequestFrame()
	}
	return v
}

// vlSetScroll sets the body's offset: as a jump (letting go of a trackpad's
// stretch) when the keyboard or a ScrollTo moved the view, else as the same
// place restated from the anchor, which keeps the stretch.
//
// A list that scrolls sideways (VListOpts.ContentW) keeps its sideways
// offset, its target and its stretch: only the vertical axis is set.
func vlSetScroll(c *ui.Context, body ui.ID, y float64, explicit, sideways bool) {
	switch {
	case sideways && explicit:
		c.SetScrollY(body, y)
	case sideways:
		c.AnchorScrollY(body, y)
	case explicit:
		c.SetScroll(body, 0, y)
	default:
		c.AnchorScroll(body, 0, y)
	}
}

// Key is the key of the list's scrolling box, the one that takes the keyboard
// focus: an app moves the focus to the list with ui.Context.SetFocus(v.Key()).
func (v *VList) Key() ui.Key { return v.c.KeyOf(v.Body) }

// reqTarget resolves a pending ScrollTo into the offset y of the view's top,
// and reports whether it did. The request stays until the target and the
// items between it and the view's top are measured and no height changed in
// the last frame (changed), so the alignment holds once the estimates are
// replaced, or vlSettleFrames have passed. A request for an id the list does
// not have is dropped, unless a rebuild may be adding it.
func (st *VListState) reqTarget(view float64, changed bool, maxItems int, y *float64) bool {
	f := &st.f
	i, ok := f.index(st.reqID)
	if !ok {
		if !st.job.on {
			st.req = false
		}
		return false
	}
	est := st.est
	top := f.top(i, est)
	h := f.top(i+1, est) - top
	if st.reqSub {
		top, h = top+st.reqOff, st.reqH
	}
	a := st.reqAlign
	if a == AlignNearest {
		cur := 0.0
		if ai, ok := f.index(st.anchorID); ok {
			cur = f.top(ai, est) + st.offset
		}
		switch {
		case top < cur || h > view:
			a = AlignStart
		case top+h > cur+view:
			a = AlignEnd
		default:
			*y = cur
			st.req = false
			return true
		}
		st.reqAlign = a // the side it chose, while it settles
	}
	switch a {
	case AlignStart:
		*y = top
	case AlignCenter:
		*y = top + h/2 - view/2
	default:
		*y = top + h - view
	}
	st.reqLeft--
	st.settling = true
	if st.reqLeft <= 0 || (!changed && st.measured(*y, i, maxItems)) {
		st.req = false
	}
	return true
}

// measured reports whether the items from the one at offset y down to item
// i, at most limit of them, were all measured.
func (st *VListState) measured(y float64, i, limit int) bool {
	f := &st.f
	from, _ := f.find(y, st.est)
	if from > i {
		from = i
	}
	if i-from >= limit {
		return false
	}
	for _, h := range f.hs[f.lo+from : f.lo+i+1] {
		if h < 0 {
			return false
		}
	}
	return true
}

// pileCandidates notes, for the accessibility requests of this frame to
// scroll a box into view (ui.ActScrollIntoView), the item built in a pile in
// the last frame that holds the box. ui applies a request to the box's
// nearest scrolling ancestor; when that is the pile, which does not scroll
// the list, pileRequests applies it to the list in the next frame.
func (st *VListState) pileCandidates(c *ui.Context, in *ui.Input, ikey ui.Key, piled []uint64) {
	for k := 0; k < in.NActions && st.ncand < len(st.cand); k++ {
		r := &in.Actions[k]
		if r.Action&ui.ActScrollIntoView == 0 {
			continue
		}
		b, ok := c.BoxOf(r.Key)
		if !ok {
			continue
		}
		br := c.Rect(b)
		for _, id := range piled {
			ib, ok := c.BoxOf(ui.KeyN(ikey, id))
			if !ok {
				continue
			}
			if ir := c.Rect(ib); br[0] >= ir[0] && br[2] <= ir[2] && br[1] >= ir[1] && br[3] <= ir[3] {
				st.cand[st.ncand] = vlCand{r.Key, id}
				st.ncand++
				break
			}
		}
	}
}

// pileRequests applies the requests pileCandidates noted in the last frame,
// if ui gave one to the pile (it scrolled the pile off its content's end,
// which nothing else does): the box is shown where it is in its item, and
// not moved when in view. A box in a scroller nearer to it than the pile was
// scrolled by ui alone.
func (st *VListState) pileRequests(c *ui.Context, okey, ikey ui.Key, scale float64) {
	n := st.ncand
	st.ncand = 0
	pb, ok := c.BoxOf(ui.KeyN(okey, 3))
	if !ok {
		return
	}
	content, _ := c.ScrollExtent(pb)
	if _, s := c.Scroll(pb); s == float64(content[1]) {
		return
	}
	for k := n - 1; k >= 0; k-- { // ui's last request is the one that holds
		cd := st.cand[k]
		b, ok1 := c.BoxOf(cd.key)
		ib, ok2 := c.BoxOf(ui.KeyN(ikey, cd.row))
		if !ok1 || !ok2 {
			continue
		}
		br, ir := c.Rect(b), c.Rect(ib)
		st.ScrollToID(cd.row, AlignNearest)
		st.reqSub = true
		st.reqOff = float64(br[1]-ir[1]) / scale
		st.reqH = float64(br[3]-br[1]) / scale
		return
	}
}

// Item starts item i, which the app builds as the children of the returned
// box: a box as wide as the list and as high as its children, keyed by the
// item's id, so its boxes keep their state through scrolling and edits. It
// ends the item before it. It returns the item's id, which the app builds by
// (its model may be ahead of the list while VListState.Pending). Styles set
// with ui's Next calls before Item apply to the item's box, except its size.
// Items must be built in order from First.
func (v *VList) Item(i int) uint64 { return v.item(i, false) }

// ItemFixedX is Item for an item that does not scroll sideways in a list
// that does (VListOpts.ContentW): a diff's file header, whose name and
// counts stay in view while the lines under it scroll. Its box is as wide
// as the view, not the content, and stays put across while the other items
// move by the sideways offset; it scrolls up and down with them. In a list
// without ContentW it is Item.
func (v *VList) ItemFixedX(i int) uint64 { return v.item(i, true) }

func (v *VList) item(i int, fixed bool) uint64 {
	v.EndItem()
	if i != v.next || i >= v.Last {
		panic("base: VList.Item: items must be built in order from First to Last-1")
	}
	v.next = i + 1
	c, st := v.c, v.st
	if i < v.anchor && !v.inPile {
		v.openPile()
	} else if i >= v.anchor && v.inPile {
		v.closePile()
	}
	id := st.f.id(i)
	var fl ui.Flags
	switch {
	case !fixed || v.minW == 0:
		c.NextSize(ui.Pct(1), ui.Children(1))
		v.wide()
	case v.inPile:
		// The pile moves across with the items: the item undoes its
		// sideways offset (and stretch), as wide as the view, whose width
		// is the last layout's (the pile is as wide as the content).
		sx, _ := c.Scroll(v.Body)
		tx, _ := c.ScrollStretch(v.Body)
		r := c.Rect(v.Body)
		fl = ui.FloatingX
		c.NextPos(float32(sx+tx), 0)
		c.NextSize(ui.Px(r[2]-r[0]), ui.Children(1))
	default:
		// In the flow, floating across puts it at the body's own left,
		// which the sideways offset does not move.
		fl = ui.FloatingX
		c.NextPos(0, 0)
		c.NextSize(ui.Pct(1), ui.Children(1))
	}
	c.NextRole(ui.RoleListItem)
	c.NextSemPos(i, v.Len)
	b := c.BoxKeyed(fl, ui.KeyN(v.key, id), nil)
	c.Push(b)
	v.open = true
	if bl := &st.built[st.cur]; len(*bl) < cap(*bl) {
		*bl = append(*bl, id)
		if v.inPile {
			st.piled[st.cur]++
		}
	}
	return id
}

// openPile opens the pile, a box of no height whose items end at its top:
// floating at pileY, the anchor's top, or, pinned to the bottom, the flow's
// only child at the content's end. It scrolls past its content, which the
// layout clamps to the content's height, so its items end exactly at its
// top whatever their heights turn out to be in this frame's layout, and
// however tall they are. With BottomAlign and a short list it sits at the
// bottom of a box as high as the view. Having no height, it is never under
// the pointer, so the wheel scrolls the body.
func (v *VList) openPile() {
	c := v.c
	if v.short {
		c.NextSize(ui.Pct(1), ui.Pct(1))
		v.wide()
		c.NextAxis(ui.AxisY)
		c.NextGap(0)
		c.NextPadding(0, 0, 0, 0)
		c.Push(c.BoxKeyed(0, ui.KeyN(v.okey, 4), nil))
		c.NextSize(ui.Px(0), ui.Pct(1))
		c.BoxKeyed(0, ui.KeyN(v.okey, 5), nil)
	}
	fl := ui.ScrollY
	if !v.bottom {
		// The pile floats, so it is not moved by a trackpad's stretch of the
		// body as the flow is: it ends where the anchor is shown.
		_, stretch := c.ScrollStretch(v.Body)
		c.NextPos(0, v.pileY-float32(stretch))
		// A list that scrolls sideways keeps it in the flow across, where
		// the sideways offset moves it with the items.
		fl |= ui.FloatingY
		if v.minW == 0 {
			fl |= ui.FloatingX
		}
	}
	c.NextSize(ui.Pct(1), ui.Px(0))
	v.wide()
	c.NextAxis(ui.AxisY)
	c.NextGap(0)
	c.NextPadding(0, 0, 0, 0)
	p := c.BoxKeyed(fl, ui.KeyN(v.okey, 3), nil)
	c.SetScroll(p, 0, vlPileScroll)
	c.Push(p)
	v.inPile = true
}

// wide gives the next box (an item, the pile) the content's width at least
// (VListOpts.ContentW).
func (v *VList) wide() {
	if v.minW > 0 {
		v.c.NextMinSize(v.minW, 0)
	}
}

// closePile closes the pile.
func (v *VList) closePile() {
	v.c.Pop()
	if v.short {
		v.c.Pop()
	}
	v.inPile = false
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
		v.closePile()
	}
	v.c.Pop()
	v.c.Pop()
	st := v.st
	st.lastS, st.lastContent, st.hasLast = v.s, v.cont, true
}

// *VListState is the Enumerator, ItemTracker and EdgeScroller of a selection
// scope over the list (ui.ScopeOpts.Items): the scope orders and walks the
// items by the list's ids, follows its endpoints' items through edits with
// marks VListMarks-2 and VListMarks-1, and scrolls the list when a drag goes
// past its edge.
var (
	_ ui.Enumerator   = (*VListState)(nil)
	_ ui.ItemTracker  = (*VListState)(nil)
	_ ui.EdgeScroller = (*VListState)(nil)
)

// TrackItem makes the mark of a selection scope's slot (0 or 1: marks
// VListMarks-2 and VListMarks-1) follow item id (ui.ItemTracker).
func (st *VListState) TrackItem(slot int, id uint64) { st.SetMark(VListMarks-2+slot, id) }

// TrackedItem is what became of the item a selection scope's slot follows
// (ui.ItemTracker).
func (st *VListState) TrackedItem(slot int) ui.ItemFate {
	m := st.Mark(VListMarks - 2 + slot)
	return ui.ItemFate{
		ID: m.ID, Removed: m.Removed, Reset: m.Reset,
		Next: m.Next, Prev: m.Prev, HasNext: m.Next != NoID, HasPrev: m.Prev != NoID,
	}
}
