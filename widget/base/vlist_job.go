package base

import (
	"math/bits"
	"time"
)

// The sliced rebuild. An edit too long for one frame (or a Reset) is built
// into the back buffer from the front buffer and the edit, in phases that
// each advance by a bounded amount of work, in slices of vlSliceNs per Sync:
//
//	clear   empty the back buffer's heights and id table
//	copy    copy the ids and heights in their new order (from the front
//	        buffer's ranges; the inserted ids were copied in at the start)
//	index   insert every id into the table (a Reset also takes the surviving
//	        heights from the front buffer here, a Splice clears the removed
//	        bits of the items it inserts again)
//	sum     the block sums, then the Fenwick tree
//	replay  bring the heights measured during the copy up to date
//	recon   only after the log overflowed: take every surviving height from
//	        the front buffer
//	queue   apply the queued edits that are short, one at a time
//
// The front buffer does not change during a rebuild except for heights. A
// height that changes before the back buffer is indexed is logged (by id,
// once per change); from the replay on it is written to both buffers. Sync
// swaps the buffers once the back buffer is ready; a long edit still queued
// then starts the next rebuild.

type vlOpKind uint8

const (
	opSplice vlOpKind = iota
	opMove
	opReset
)

// vlOp is a queued edit: Splice(a, b, pool[off:off+n]), Move(a, b, c), or a
// Reset to n ids.
type vlOp struct {
	kind    vlOpKind
	a, b, c int
	off, n  int
}

// vlSeg is a run of the new order: n items of the front buffer from logical
// index off, or (ins) n inserted ids, already in place in the back buffer.
type vlSeg struct {
	ins    bool
	off, n int
}

const (
	phClear uint8 = iota
	phCopy
	phIndex
	phSum
	phReplay
	phRecon
	phQueue
)

type vlJob struct {
	on, ready bool
	reset     bool
	recon     bool // the log overflowed: reconcile every height
	phase     uint8
	segs      [4]vlSeg
	nseg      int
	si, so    int // the segment being copied and how much of it is done
	dst       int // items copied
	i         int // the cursor of the other phases
	nNew      int
	insAt     int // the inserted ids' place in the new order (a Splice's)
	insN      int
}

func (j *vlJob) add(ins bool, off, n int) {
	if n > 0 {
		j.segs[j.nseg] = vlSeg{ins, off, n}
		j.nseg++
	}
}

// vlSliceNs is the work a rebuild does per Sync (D27's 1 ms slice).
const vlSliceNs = int64(time.Millisecond)

// vlChunk is the work between two looks at the clock.
const vlChunk = 4096

// vlSliceUnits, when positive, replaces the clock: each Sync does that many
// units of work (tests use it to make the number of frames exact).
var vlSliceUnits int

// vlSlotNs is what a slot of an in-place edit costs at most, cache-cold, in
// nanoseconds (vlDirectMax): how a slice judges whether a queued edit still
// fits in it.
const vlSlotNs = 23

// enqueue queues an edit made while a rebuild runs.
func (st *VListState) enqueue(op vlOp) {
	st.queue = append(st.queue, op)
	switch op.kind {
	case opSplice:
		st.modelN += op.n - op.b
	case opReset:
		st.modelN = op.n
	}
}

// startJob begins the rebuild of op from the front buffer; ins are the ids
// it inserts (a Splice's or a Reset's), copied into the back buffer now.
func (st *VListState) startJob(op vlOp, ins []uint64) {
	f := &st.f
	j := &st.job
	*j = vlJob{on: true}
	insAt := 0
	switch op.kind {
	case opSplice:
		at, rem := op.a, op.b
		j.add(false, 0, at)
		j.add(true, 0, op.n)
		j.add(false, at+rem, f.n-at-rem)
		j.nNew = f.n - rem + op.n
		insAt = at
		j.insAt, j.insN = at, op.n
	case opMove:
		from, k, to := op.a, op.b, op.c
		if to < from {
			j.add(false, 0, to)
			j.add(false, from, k)
			j.add(false, to, from-to)
			j.add(false, from+k, f.n-from-k)
		} else {
			j.add(false, 0, from)
			j.add(false, from+k, to-from)
			j.add(false, from, k)
			j.add(false, to+k, f.n-to-k)
		}
		j.nNew = f.n
	case opReset:
		j.reset = true
		j.add(true, 0, op.n)
		j.nNew = op.n
	}
	if st.qhead >= len(st.queue) {
		st.modelN = j.nNew
	}
	if cap(st.log) == 0 {
		st.log = make([]uint64, 0, vlLogMax)
	}
	st.log = st.log[:0]

	// the items of the front buffer the edit and the queued ones remove
	// (survivors; a Reset's are not looked for)
	w := (f.n + 63) / 64
	if cap(st.gone) < w {
		st.gone = make([]uint64, w)
	}
	st.gone = st.gone[:w]
	clear(st.gone)
	if op.kind == opSplice {
		setBits(st.gone, op.a, op.a+op.b)
	}

	// the back buffer: as large as the front one, and large enough for the
	// result with spare slots at both ends; its table holds what Reserve
	// asked for, so that the reservation survives the swap
	b := &st.bk
	c := max(f.slots(), j.nNew+j.nNew/2+2*vlBlock)
	c = (c + vlBlock - 1) &^ (vlBlock - 1)
	nb := c >> vlBlockShift
	if cap(b.ids) < c {
		b.ids, b.hs = make([]uint64, c), make([]float32, c)
	}
	b.ids, b.hs = b.ids[:c], b.hs[:c]
	if cap(b.bm) < nb {
		b.bm, b.bu = make([]float64, nb), make([]int32, nb)
		b.fm, b.fu = make([]float64, nb+1), make([]int32, nb+1)
	}
	b.bm, b.bu, b.fm, b.fu = b.bm[:nb], b.bu[:nb], b.fm[:nb+1], b.fu[:nb+1]
	if tc := vlTableCap(max(j.nNew, st.resN)); len(b.tab.keys) < tc {
		if cap(b.tab.keys) >= tc {
			b.tab.keys, b.tab.pos = b.tab.keys[:tc], b.tab.pos[:tc]
		} else {
			b.tab.keys, b.tab.pos = make([]uint64, tc), make([]int32, tc)
		}
		b.tab.setCap(tc)
	}
	b.tab.n = 0
	b.lo, b.n, b.vbase = (c-j.nNew)/2, 0, 0
	copy(b.ids[b.lo+insAt:], ins)
}

// jobCost estimates the work of the job just started, in the units of
// vlDirect (an item re-indexed): the clear and the sums cover the back
// buffer's whole capacity, and touching a slot there costs about a
// sixteenth of indexing an item.
func (st *VListState) jobCost() int {
	b := &st.bk
	n := st.job.nNew
	return n + (2*len(b.hs)+len(b.tab.keys)+n)/16
}

// step advances the rebuild by one slice.
func (st *VListState) step() {
	start := time.Now()
	units := 0
	for st.job.on && !st.job.ready {
		n := vlChunk
		var room int // the units left in the slice
		if vlSliceUnits > 0 {
			room = vlSliceUnits - units
			n = min(n, room)
		} else {
			room = int((vlSliceNs - int64(time.Since(start))) / vlSlotNs)
		}
		did := st.work(n, room, units == 0)
		if did == 0 {
			return
		}
		units += did
		if vlSliceUnits > 0 {
			if units >= vlSliceUnits {
				return
			}
		} else if time.Since(start) >= time.Duration(vlSliceNs) {
			return
		}
	}
}

// finishNow runs the rebuild to its end and publishes it.
func (st *VListState) finishNow() {
	for st.job.on && !st.job.ready {
		st.work(1<<30, 1<<30, true)
	}
	if st.job.ready {
		st.publish()
	}
}

// work does about n units of the current phase and returns how many it did.
// It returns 0, doing nothing, when the next step (a queued edit) is larger
// than the room left in the slice and the slice has already done some work
// (not fresh): it waits for the next slice.
func (st *VListState) work(n, room int, fresh bool) int {
	j := &st.job
	b := &st.bk
	f := &st.f
	switch j.phase {
	case phClear:
		nh, total := len(b.hs), len(b.hs)+len(b.tab.keys)
		s, e := j.i, min(j.i+n, total)
		if s < nh {
			clear(b.hs[s:min(e, nh)])
		}
		if e > nh {
			b.tab.clear(max(s, nh)-nh, e-nh)
		}
		j.i = e
		if e >= total {
			j.phase, j.i = phCopy, 0
		}
		return max(e-s, 1)
	case phCopy:
		did := 0
		for did < n && j.si < j.nseg {
			s := j.segs[j.si]
			m := min(n-did, s.n-j.so)
			d := b.lo + j.dst
			if s.ins {
				if !j.reset {
					hs := b.hs[d : d+m]
					for i := range hs {
						hs[i] = vlUnmeasured
					}
				}
			} else {
				p := f.lo + s.off + j.so
				copy(b.ids[d:d+m], f.ids[p:p+m])
				copy(b.hs[d:d+m], f.hs[p:p+m])
			}
			j.so += m
			j.dst += m
			did += m
			if j.so == s.n {
				j.si, j.so = j.si+1, 0
			}
		}
		if j.si >= j.nseg {
			j.phase, j.i = phIndex, 0
		}
		return max(did, 1)
	case phIndex:
		e := min(j.i+n, j.nNew)
		for i := j.i; i < e; i++ {
			p := b.lo + i
			id := b.ids[p]
			b.tab.put(id, int32(i))
			if i-j.insAt < j.insN && i >= j.insAt {
				// an item the Splice removed and inserts again survives
				if fi, ok := f.index(id); ok {
					st.gone[fi>>6] &^= 1 << (fi & 63)
				}
			}
			if j.reset {
				h := vlUnmeasured
				if fi, ok := f.index(id); ok {
					h = f.hs[f.lo+fi]
				}
				b.hs[p] = h
			}
		}
		did := e - j.i
		j.i = e
		if j.i >= j.nNew {
			j.phase, j.i = phSum, 0
		}
		return max(did, 1)
	case phSum:
		nb := len(b.bm)
		blocks := max(n>>vlBlockShift, 1)
		e := min(j.i+blocks, nb)
		for k := j.i; k < e; k++ {
			b.bm[k], b.bu[k] = b.blockSum(k)
		}
		did := (e - j.i) << vlBlockShift
		j.i = e
		if j.i >= nb {
			b.fenBuild()
			b.n = j.nNew
			j.phase, j.i = phReplay, 0
		}
		return max(did, 1)
	case phReplay:
		// From here on heights are written to both buffers (heightChanged);
		// the log holds the ids that changed before: the front buffer has
		// their latest heights.
		e := min(j.i+n, len(st.log))
		for _, id := range st.log[j.i:e] {
			st.copyHeight(id)
		}
		did := e - j.i
		j.i = e
		if j.i >= len(st.log) {
			st.log = st.log[:0]
			j.phase, j.i = phRecon, 0
			if !j.recon {
				j.phase = phQueue
			}
		}
		return max(did, 1)
	case phRecon:
		e := min(j.i+n, b.n)
		for i := j.i; i < e; i++ {
			if fi, ok := f.index(b.ids[b.lo+i]); ok {
				if h := f.hs[f.lo+fi]; b.hs[b.lo+i] != h {
					b.setH(b.lo+i, h)
				}
			}
		}
		did := e - j.i
		j.i = e
		if j.i >= b.n {
			j.phase, j.i = phQueue, 0
		}
		return max(did, 1)
	case phQueue:
		if st.qhead >= len(st.queue) {
			j.ready = true
			return 1
		}
		op := st.queue[st.qhead]
		cost, short := st.shortCost(b, op)
		if !short {
			j.ready = true // the swap starts its rebuild
			return 1
		}
		if cost > room && !fresh {
			return 0
		}
		st.applyShort(b, op)
		st.qhead++
		return max(cost, 1)
	}
	return 1
}

// heightChanged passes a height change of the front buffer on to the
// rebuild: logged while the back buffer is not indexed yet, written to it
// from then on.
func (st *VListState) heightChanged(id uint64, h float32) {
	j := &st.job
	if j.phase >= phReplay {
		if i, ok := st.bk.index(id); ok {
			st.bk.setH(st.bk.lo+i, h)
		}
		return
	}
	if j.recon {
		return
	}
	if len(st.log) < cap(st.log) {
		st.log = append(st.log, id)
	} else {
		j.recon = true
	}
}

// copyHeight sets the back buffer's height of id to the front buffer's.
func (st *VListState) copyHeight(id uint64) {
	b, f := &st.bk, &st.f
	bi, ok := b.index(id)
	if !ok {
		return
	}
	if fi, ok := f.index(id); ok {
		if h := f.hs[f.lo+fi]; b.hs[b.lo+bi] != h {
			b.setH(b.lo+bi, h)
		}
	}
}

// shortCost is the slots a queued edit touches in b, and whether it is short
// enough to apply in place.
func (st *VListState) shortCost(b *vlBuf, op vlOp) (cost int, short bool) {
	var fits bool
	switch op.kind {
	case opSplice:
		cost, fits = b.spliceCost(op.a, op.b, op.n)
	case opMove:
		cost, fits = b.moveCost(op.a, op.b, op.c)
	default:
		return 0, false
	}
	return cost, cost <= vlDirect && (fits || b.n <= vlDirect)
}

// applyShort applies a short queued edit to b in place. The items of the
// front buffer it removes are marked gone, those it inserts again not.
func (st *VListState) applyShort(b *vlBuf, op vlOp) {
	switch op.kind {
	case opSplice:
		for _, id := range b.ids[b.lo+op.a : b.lo+op.a+op.b] {
			if fi, ok := st.f.index(id); ok {
				st.gone[fi>>6] |= 1 << (fi & 63)
			}
		}
		ins := st.pool[op.off : op.off+op.n]
		for _, id := range ins {
			if fi, ok := st.f.index(id); ok {
				st.gone[fi>>6] &^= 1 << (fi & 63)
			}
		}
		b.splice(op.a, op.b, ins)
	case opMove:
		st.scratch(op.b)
		b.move(op.a, op.b, op.c, st.tmpIDs, st.tmpHs)
	}
}

// Sync is the frame boundary of the state: the first call in a frame
// advances a rebuild by one slice and, once it is done, swaps the buffers.
// BeginVList calls it; an app or a selection scope that reads the state
// earlier in the frame calls it first, with ui.Context.Frame, so that the
// whole frame sees one order.
func (st *VListState) Sync(frame uint32) {
	if st.synced && frame == st.frame {
		return
	}
	st.frame, st.synced = frame, true
	if st.job.on {
		st.step()
		if st.job.ready {
			st.publish()
		}
	}
}

// publish swaps in the back buffer: the anchor and the marks whose items are
// gone move to their nearest survivors (by the old order), then the queue's
// next long edit, if any, starts its own rebuild.
func (st *VListState) publish() {
	nw := &st.bk
	reset := st.job.reset
	if st.started && st.anchorID != NoID {
		if _, ok := nw.index(st.anchorID); !ok {
			next, prev, near := st.survivors(st.anchorID, reset)
			switch {
			case reset:
				st.anchorID, st.place = NoID, placeStart
			case next != NoID:
				st.anchorID = next
			case prev != NoID:
				st.anchorID = prev
			default:
				st.anchorID = near
			}
			st.offset = 0
		}
	}
	if st.started && reset && st.anchorID == NoID {
		st.place = placeStart
	}
	for i := range st.marks {
		m := &st.marks[i]
		if !st.markOn[i] || m.Reset {
			continue
		}
		if m.Removed {
			// the survivors a removed mark points to may go too: they move
			// on to theirs
			if _, ok := nw.index(m.Next); !ok && m.Next != NoID {
				m.Next, _, _ = st.survivors(m.Next, reset)
			}
			if _, ok := nw.index(m.Prev); !ok && m.Prev != NoID {
				_, m.Prev, _ = st.survivors(m.Prev, reset)
			}
			continue
		}
		if _, ok := nw.index(m.ID); !ok {
			m.Removed = true
			m.Next, m.Prev, _ = st.survivors(m.ID, reset)
		}
	}
	st.f, st.bk = st.bk, st.f
	st.rev++
	st.job = vlJob{}
	if st.qhead < len(st.queue) {
		op := st.queue[st.qhead]
		st.qhead++
		var ins []uint64
		if op.kind == opSplice {
			ins = st.pool[op.off : op.off+op.n]
		}
		st.startJob(op, ins)
		return
	}
	st.queue, st.qhead = st.queue[:0], 0
	st.pool = st.pool[:0]
	st.modelN = st.f.n
}

// survivors finds, for an item of the front buffer that the back buffer does
// not have, the nearest items after and before it that the rebuild kept
// (NoID if none), and an item of the back buffer at about its place.
func (st *VListState) survivors(id uint64, reset bool) (next, prev, near uint64) {
	f, nw := &st.f, &st.bk
	next, prev, near = NoID, NoID, NoID
	i, ok := f.index(id)
	if !ok {
		return
	}
	if !reset {
		if j := nextClear(st.gone, i+1, f.n); j >= 0 {
			next = f.id(j)
		}
		if j := prevClear(st.gone, i-1); j >= 0 {
			prev = f.id(j)
		}
	}
	if nw.n > 0 {
		near = nw.id(min(i, nw.n-1))
	}
	return
}

// setBits sets the bits [lo, hi) of g.
func setBits(g []uint64, lo, hi int) {
	for lo < hi && lo&63 != 0 {
		g[lo>>6] |= 1 << (lo & 63)
		lo++
	}
	for ; lo+64 <= hi; lo += 64 {
		g[lo>>6] = ^uint64(0)
	}
	for ; lo < hi; lo++ {
		g[lo>>6] |= 1 << (lo & 63)
	}
}

// nextClear is the first index from i below n whose bit in g is clear, or -1.
func nextClear(g []uint64, i, n int) int {
	for i < n {
		w := ^g[i>>6] >> (i & 63)
		if w != 0 {
			if j := i + bits.TrailingZeros64(w); j < n {
				return j
			}
			return -1
		}
		i = (i | 63) + 1
	}
	return -1
}

// prevClear is the last index from i down to 0 whose bit in g is clear, or -1.
func prevClear(g []uint64, i int) int {
	for i >= 0 {
		w := ^g[i>>6] << (63 - i&63)
		if w != 0 {
			return i - bits.LeadingZeros64(w)
		}
		i = i&^63 - 1
	}
	return -1
}
