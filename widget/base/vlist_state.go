package base

// NoID is the one id value an item cannot have: it marks "no item".
const NoID = ^uint64(0)

// Align says where ScrollTo puts an item in the view.
type Align uint8

const (
	// AlignNearest scrolls by the least amount that shows the item, and not at
	// all when it is in view; an item taller than the view is aligned at its top.
	AlignNearest Align = iota
	// AlignStart puts the item's top at the top of the view.
	AlignStart
	// AlignCenter centres the item in the view.
	AlignCenter
	// AlignEnd puts the item's bottom at the bottom of the view.
	AlignEnd
)

// VListMarks is the number of marks a VListState keeps (SetMark).
const VListMarks = 4

// VListMark is what became of an item a mark follows (VListState.SetMark).
type VListMark struct {
	// ID is the item the mark was set on, NoID for an unset mark.
	ID uint64
	// Removed: an edit removed the item. Next and Prev are then the first
	// surviving item after the removed run and the last one before it (NoID
	// when there is none); a selection moves its focus to the start of Next and
	// its anchor to the end of Prev.
	Removed    bool
	Next, Prev uint64
	// Reset: the list was replaced by Reset (Removed is set too, and Next and
	// Prev are NoID): a selection clears itself.
	Reset bool
}

// vlDirectMax is the most slots an edit may touch in place, in the frame it
// is made; a longer edit is rebuilt in the back buffer over several frames.
// Measured (BenchmarkVListDirectEdit, random ids in a million, so the id
// table's probes miss the cache): 4 to 6 ns a slot on an Apple M4, so 65536
// slots cost about 0.4 ms, a fifth of the 2 ms edit budget, which leaves room
// for machines several times slower.
const vlDirectMax = 1 << 16

// vlDirect is vlDirectMax; tests lower it to drive short lists through the
// rebuild.
var vlDirect = vlDirectMax

// vlLogMax is the most height changes kept for the back buffer while it is
// being built; more are dropped (the items are measured again when shown).
const vlLogMax = 4096

// vlWalkMax bounds the search for a surviving neighbour when an item an anchor
// or mark followed disappeared in a rebuild.
const vlWalkMax = 1 << 16

// VListState is the state of a variable-height virtual list (BeginVList),
// owned by the app next to its model: the item ids in order, their measured
// heights, and the position, held as an anchor item and an offset into it so
// that edits keep the view still.
//
// The app's model and the state change together: the app applies an edit to
// its model and makes the matching Splice, Move or Reset in the same frame.
// Item ids are the app's, stable and unique; NoID is reserved.
//
// Edits:
//   - Splice inserts, removes or replaces a run; Prepend and Append are
//     Splice at 0 and at Len. Prepending and appending k items cost O(k); an
//     edit in the middle and a Move cost O(distance): the shorter side of the
//     list shifts and is re-indexed.
//   - An edit of an empty list is applied at once, however long (a list's
//     first load). Otherwise an edit that would touch more than a frame's
//     budget of items (or that needs the whole list moved within its arrays,
//     when the spare slots at one end run out) is rebuilt
//     in a back buffer, 1 ms per frame, while every reader keeps the front
//     buffer; the buffers are swapped at the next Sync after the rebuild is
//     done, so no reader sees a half-updated order. Edits made meanwhile are
//     queued and applied before the swap. Pending reports this; until then
//     the app's model is ahead of the list, and the app builds items by the
//     id the list gives (VList.Item), tolerating ids its model has dropped.
//
// The anchor (the item at the top of the view) follows the edits:
//   - an edit wholly above it shifts its index and leaves the view in place;
//     one below changes nothing on screen;
//   - deleting the anchor item moves the anchor to the first surviving item
//     after the removed run (the last one before it at the end of the list),
//     at offset 0;
//   - a Move that carries the anchor keeps it on its item, wherever it goes;
//     heights travel with their items;
//   - Reset keeps the anchor if its id is in the new list, and otherwise
//     starts at the top (at the bottom with VListOpts.StickToBottom).
//
// A VListState holds no pointers but its slice headers, and it must not be
// copied once in use. *VListState is the Enumerator a selection scope walks
// the list with (IndexOf, First, Last, Next).
type VListState struct {
	f, bk vlBuf // the front buffer and the back buffer (vlist_job.go)

	est   float32 // the estimate for unmeasured items, in pixels
	scale float32 // the display scale the heights were measured at

	// the position
	anchorID uint64
	offset   float64 // pixels from the anchor's top down to the view's top
	stuck    bool    // pinned to the bottom (StickToBottom)
	atEnd    bool
	started  bool  // the list has been laid out once
	place    uint8 // a pending placement after a Reset lost the anchor
	reqID    uint64
	reqAlign Align
	req      bool
	by       float64 // ScrollBy

	// what the scroll box held and the content it had at the last frame
	lastS, lastContent float64
	hasLast            bool

	built [2][]uint64 // the ids built in the last frame and in this one
	cur   int         // built[cur] is this frame's

	marks  [VListMarks]VListMark
	markOn [VListMarks]bool
	rev    uint64

	tmpIDs []uint64 // scratch for in-place moves
	tmpHs  []float32

	job    vlJob
	queue  []vlOp
	qhead  int
	pool   []uint64 // the inserted ids of the queued edits and the job
	log    []vlLogEnt
	modelN int // Len after the queued edits
	frame  uint32
	synced bool
}

const (
	placeNone uint8 = iota
	placeStart
)

// Reserve makes room for n items: edits within it, prepends and appends
// included, allocate nothing (the back buffer of a long edit is allocated
// the first time one happens).
func (st *VListState) Reserve(n int) {
	c := n + n/2 + 2*vlBlock
	if st.f.slots() < c {
		if st.f.n == 0 {
			st.f.alloc(c, c/2, n)
		} else {
			st.f.regrow(c)
		}
	}
	if len(st.f.tab.keys) < vlTableCap(n) {
		st.f.tab.grow(n)
	}
	if k := min(n, vlDirectMax); cap(st.tmpIDs) < k {
		st.tmpIDs, st.tmpHs = make([]uint64, k), make([]float32, k)
	}
	if cap(st.queue) < 64 {
		st.queue = make([]vlOp, 0, 64)
	}
	if cap(st.log) < vlLogMax {
		st.log = make([]vlLogEnt, 0, vlLogMax)
	}
}

// Len is the number of items (of the front buffer: see Pending).
func (st *VListState) Len() int { return st.f.n }

// ID is the id of item i.
func (st *VListState) ID(i int) uint64 {
	if uint(i) >= uint(st.f.n) {
		panic("base: VListState.ID: index out of range")
	}
	return st.f.id(i)
}

// IndexOf is the index of the item id, in O(1).
func (st *VListState) IndexOf(id uint64) (int, bool) { return st.f.index(id) }

// First is the id of the first item.
func (st *VListState) First() (uint64, bool) {
	if st.f.n == 0 {
		return NoID, false
	}
	return st.f.id(0), true
}

// Last is the id of the last item.
func (st *VListState) Last() (uint64, bool) {
	if st.f.n == 0 {
		return NoID, false
	}
	return st.f.id(st.f.n - 1), true
}

// Next is the id of the item after id.
func (st *VListState) Next(id uint64) (uint64, bool) {
	i, ok := st.f.index(id)
	if !ok || i+1 >= st.f.n {
		return NoID, false
	}
	return st.f.id(i + 1), true
}

// Prev is the id of the item before id.
func (st *VListState) Prev(id uint64) (uint64, bool) {
	i, ok := st.f.index(id)
	if !ok || i == 0 {
		return NoID, false
	}
	return st.f.id(i - 1), true
}

// Rev counts the edits that reached the front buffer: it changes whenever
// the order or the set of ids readers see changes.
func (st *VListState) Rev() uint64 { return st.rev }

// Pending reports that a long edit is being rebuilt or edits are queued: the
// readers see the order before them, and the app's model is ahead.
func (st *VListState) Pending() bool { return st.job.on }

// Anchor is the item at the top of the view as of the last frame and how far
// down into it the view's top is, in pixels. An app that restores a scroll
// position across runs saves these and calls SetAnchor.
func (st *VListState) Anchor() (id uint64, offset float64) { return st.anchorID, st.offset }

// SetAnchor puts the top of the view offset pixels into item id from the next
// frame, unpinning it from the bottom. An id the list does not have is
// ignored.
func (st *VListState) SetAnchor(id uint64, offset float64) {
	if _, ok := st.f.index(id); !ok {
		return
	}
	st.anchorID, st.offset, st.stuck, st.place, st.started = id, offset, false, placeNone, true
	st.by = 0
}

// AtEnd reports that the view showed the end of the list in the last frame.
// With VListOpts.StickToBottom the view then stays at the bottom as items
// arrive or grow.
func (st *VListState) AtEnd() bool { return st.atEnd }

// Height is the height of item i in pixels, and whether it was measured (an
// unmeasured item counts as the list's estimate).
func (st *VListState) Height(i int) (px float32, measured bool) {
	if uint(i) >= uint(st.f.n) {
		panic("base: VListState.Height: index out of range")
	}
	h := st.f.hs[st.f.lo+i]
	if h < 0 {
		return st.est, false
	}
	return h, true
}

// Top is the offset in pixels of the top of item i from the top of the list
// (i may be Len: the total height), with estimates for unmeasured items.
func (st *VListState) Top(i int) float64 {
	if st.f.n == 0 {
		return 0
	}
	return st.f.top(i, st.est)
}

// Total is the height of the list in pixels, with estimates for unmeasured
// items: what the scroll bar works from.
func (st *VListState) Total() float64 {
	if st.f.n == 0 {
		return 0
	}
	return st.f.total(st.est)
}

// ScrollTo shows item i in the next frame, aligned by a: a jump, for replies
// and search hits. It unpins the view from the bottom unless the item ends up
// at the bottom.
func (st *VListState) ScrollTo(i int, a Align) { st.ScrollToID(st.ID(i), a) }

// ScrollToID is ScrollTo by id. It waits for an id that a pending rebuild is
// adding.
func (st *VListState) ScrollToID(id uint64, a Align) { st.reqID, st.reqAlign, st.req = id, a, true }

// ScrollBy scrolls the view by dy pixels in the next frame (down when
// positive), as a drag past the list's edge does.
func (st *VListState) ScrollBy(dy float64) { st.by += dy }

// Invalidate forgets the measured height of item i: it counts as the
// estimate until it is built again. An item that is built is measured in
// every frame anyway; this is for items out of view whose content changed.
func (st *VListState) Invalidate(i int) { st.InvalidateID(st.ID(i)) }

// InvalidateID is Invalidate by id.
func (st *VListState) InvalidateID(id uint64) { st.setHeight(id, vlUnmeasured) }

// setHeight records a height for id in the front buffer, and in the log that
// a rebuild replays onto the back buffer.
func (st *VListState) setHeight(id uint64, h float32) bool {
	if st.job.on && len(st.log) < cap(st.log) {
		st.log = append(st.log, vlLogEnt{id, h})
	}
	i, ok := st.f.index(id)
	if !ok {
		return false
	}
	p := st.f.lo + i
	if st.f.hs[p] == h {
		return false
	}
	st.f.setH(p, h)
	return true
}

// SetMark makes mark i (0 to VListMarks-1) follow item id, so that the app
// learns what became of it when an edit removes it (Mark). A selection scope
// marks its endpoints' items.
// SetMark(i, NoID) clears the mark.
func (st *VListState) SetMark(i int, id uint64) {
	st.marks[i] = VListMark{ID: id, Next: NoID, Prev: NoID}
	st.markOn[i] = id != NoID
}

// Mark is what became of the item mark i follows.
func (st *VListState) Mark(i int) VListMark {
	if !st.markOn[i] {
		return VListMark{ID: NoID, Next: NoID, Prev: NoID}
	}
	return st.marks[i]
}

// Prepend inserts ids before the first item.
func (st *VListState) Prepend(ids []uint64) { st.Splice(0, 0, ids) }

// Append adds ids after the last item.
func (st *VListState) Append(ids []uint64) { st.Splice(st.modelLen(), 0, ids) }

// modelLen is the length of the app's model: Len after the queued edits.
func (st *VListState) modelLen() int {
	if st.job.on {
		return st.modelN
	}
	return st.f.n
}

// Splice removes the removed items from index at and inserts ids there
// (copied; they start unmeasured). Indices are the app model's, which is
// ahead of Len while Pending.
func (st *VListState) Splice(at, removed int, ids []uint64) {
	n := st.modelLen()
	if at < 0 || removed < 0 || at+removed > n {
		panic("base: VListState.Splice: range out of bounds")
	}
	if removed == 0 && len(ids) == 0 {
		return
	}
	op := vlOp{kind: opSplice, a: at, b: removed, off: len(st.pool), n: len(ids)}
	if st.job.on {
		st.pool = append(st.pool, ids...)
		st.enqueue(op)
		return
	}
	cost, fits := st.f.spliceCost(at, removed, len(ids))
	if st.f.n == 0 || (cost <= vlDirect && (fits || st.f.n <= vlDirect)) {
		st.removing(&st.f, at, removed, ids)
		st.f.splice(at, removed, ids)
		st.rev++
		return
	}
	st.pool = append(st.pool[:0], ids...)
	op.off = 0
	st.startJob(op)
}

// Move moves the run of k items at index from so that it starts at index to
// of the result (0 <= to <= Len-k), with its heights. Ids survive, so the
// anchor and the marks stay on their items.
func (st *VListState) Move(from, k, to int) {
	n := st.modelLen()
	if from < 0 || k < 0 || from+k > n || to < 0 || to+k > n {
		panic("base: VListState.Move: range out of bounds")
	}
	if k == 0 || from == to {
		return
	}
	op := vlOp{kind: opMove, a: from, b: k, c: to}
	if st.job.on {
		st.enqueue(op)
		return
	}
	if cost, fits := st.f.moveCost(from, k, to); cost <= vlDirect && (fits || st.f.n <= vlDirect) {
		st.scratch(k)
		st.f.move(from, k, to, st.tmpIDs, st.tmpHs)
		st.rev++
		return
	}
	st.pool = st.pool[:0]
	st.startJob(op)
}

func (st *VListState) scratch(k int) {
	if cap(st.tmpIDs) < k {
		st.tmpIDs, st.tmpHs = make([]uint64, k), make([]float32, k)
	}
	st.tmpIDs, st.tmpHs = st.tmpIDs[:cap(st.tmpIDs)], st.tmpHs[:cap(st.tmpHs)]
}

// Reset replaces the list with ids (copied). Items that stay keep their
// measured heights; the anchor stays if its id does, and the marks report a
// reset. Queued edits are dropped: the new list supersedes them.
func (st *VListState) Reset(ids []uint64) {
	st.job = vlJob{}
	st.queue, st.qhead = st.queue[:0], 0
	st.log = st.log[:0]
	st.pool = append(st.pool[:0], ids...)
	st.startJob(vlOp{kind: opReset, n: len(ids)})
	for i := range st.marks {
		if st.markOn[i] {
			st.marks[i] = VListMark{ID: st.marks[i].ID, Removed: true, Reset: true, Next: NoID, Prev: NoID}
		}
	}
	if st.f.n == 0 || len(ids) <= vlDirect/2 {
		st.finishNow()
	}
}

// removing updates the anchor and the marks for the removal of the run of
// removed items at at in b, before it happens.
func (st *VListState) removing(b *vlBuf, at, removed int, ins []uint64) {
	if removed == 0 {
		return
	}
	next, prev := NoID, NoID
	if at+removed < b.n {
		next = b.id(at + removed)
	}
	if at > 0 {
		prev = b.id(at - 1)
	}
	if st.started && inRun(b, st.anchorID, at, removed) {
		switch {
		case next != NoID:
			st.anchorID = next
		case prev != NoID:
			st.anchorID = prev
		case len(ins) > 0:
			st.anchorID = ins[0]
		default:
			st.anchorID = NoID
		}
		st.offset = 0
	}
	for i := range st.marks {
		m := &st.marks[i]
		if st.markOn[i] && !m.Removed && inRun(b, m.ID, at, removed) {
			m.Removed, m.Next, m.Prev = true, next, prev
		}
	}
}

// inRun reports whether id is one of the n items from at in b.
func inRun(b *vlBuf, id uint64, at, n int) bool {
	i, ok := b.index(id)
	return ok && i >= at && i < at+n
}
