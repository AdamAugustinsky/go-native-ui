package base

import "time"

// The sliced rebuild. An edit too long for one frame (or a Reset) is built
// into the back buffer from the front buffer and the edit, in phases that
// each advance by a bounded amount of work, in slices of vlSliceNs per Sync:
//
//	clear  empty the back buffer's heights and id table
//	copy   copy the ids and heights in their new order (from the front
//	       buffer's ranges and the inserted ids)
//	index  insert every id into the table (a Reset also takes the surviving
//	       heights from the front buffer here)
//	sum    the block sums, then the Fenwick tree
//	finish apply the queued edits that are short, replay the heights measured
//	       meanwhile, and mark the buffer ready
//
// The front buffer does not change during a rebuild except for heights,
// which are logged and replayed. Sync swaps the buffers once the back buffer
// is ready; a long edit still queued then starts the next rebuild.

type vlOpKind uint8

const (
	opSplice vlOpKind = iota
	opMove
	opReset
)

// vlOp is a queued edit: Splice(a, b, pool[off:off+n]), Move(a, b, c), or a
// Reset to pool[off:off+n].
type vlOp struct {
	kind    vlOpKind
	a, b, c int
	off, n  int
}

type vlLogEnt struct {
	id uint64
	h  float32
}

// vlSeg is a run of the new order: n items of the front buffer from logical
// index off, or n inserted ids from pool[off:].
type vlSeg struct {
	pool   bool
	off, n int
}

const (
	phClear uint8 = iota
	phCopy
	phIndex
	phSum
	phFinish
)

type vlJob struct {
	on, ready bool
	reset     bool
	phase     uint8
	segs      [4]vlSeg
	nseg      int
	si, so    int // the segment being copied and how much of it is done
	dst       int // items copied
	i         int // the cursor of the clear, index and sum phases
	nNew      int
}

func (j *vlJob) add(pool bool, off, n int) {
	if n > 0 {
		j.segs[j.nseg] = vlSeg{pool, off, n}
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

// startJob begins the rebuild of op from the front buffer.
func (st *VListState) startJob(op vlOp) {
	f := &st.f
	j := &st.job
	*j = vlJob{on: true}
	switch op.kind {
	case opSplice:
		at, rem := op.a, op.b
		j.add(false, 0, at)
		j.add(true, op.off, op.n)
		j.add(false, at+rem, f.n-at-rem)
		j.nNew = f.n - rem + op.n
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
		j.add(true, op.off, op.n)
		j.nNew = op.n
	}
	if st.qhead >= len(st.queue) {
		st.modelN = j.nNew
	}
	st.log = st.log[:0]

	// the back buffer: as large as the front one, and large enough for the
	// result with spare slots at both ends
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
	if tc := vlTableCap(j.nNew); len(b.tab.keys) < tc {
		if cap(b.tab.keys) >= tc {
			b.tab.keys, b.tab.pos = b.tab.keys[:tc], b.tab.pos[:tc]
		} else {
			b.tab.keys, b.tab.pos = make([]uint64, tc), make([]int32, tc)
		}
		b.tab.setCap(tc)
	}
	b.tab.n = 0
	b.lo, b.n, b.vbase = (c-j.nNew)/2, 0, 0
}

// step advances the rebuild by one slice.
func (st *VListState) step() {
	start := time.Now()
	units := 0
	for st.job.on && !st.job.ready {
		n := vlChunk
		if vlSliceUnits > 0 {
			n = min(n, vlSliceUnits-units)
		}
		units += st.work(n)
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
		st.work(1 << 30)
	}
	if st.job.ready {
		st.publish()
	}
}

// work does up to n units of the current phase and returns how many it did
// (at least 1 while the job runs).
func (st *VListState) work(n int) int {
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
			if s.pool {
				copy(b.ids[d:d+m], st.pool[s.off+j.so:])
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
			j.phase = phFinish
		}
		return max(did, 1)
	case phFinish:
		for st.qhead < len(st.queue) && st.applyShort(b, st.queue[st.qhead]) {
			st.qhead++
		}
		for _, e := range st.log {
			if i, ok := b.index(e.id); ok {
				b.setH(b.lo+i, e.h)
			}
		}
		st.log = st.log[:0]
		j.ready = true
		return 1
	}
	return 1
}

// applyShort applies a queued edit to b in place if it is short, and reports
// whether it did.
func (st *VListState) applyShort(b *vlBuf, op vlOp) bool {
	switch op.kind {
	case opSplice:
		cost, fits := b.spliceCost(op.a, op.b, op.n)
		if cost > vlDirect || (!fits && b.n > vlDirect) {
			return false
		}
		b.splice(op.a, op.b, st.pool[op.off:op.off+op.n])
	case opMove:
		cost, fits := b.moveCost(op.a, op.b, op.c)
		if cost > vlDirect || (!fits && b.n > vlDirect) {
			return false
		}
		st.scratch(op.b)
		b.move(op.a, op.b, op.c, st.tmpIDs, st.tmpHs)
	default:
		return false
	}
	return true
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
			next, prev, near := st.survivors(st.anchorID)
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
		if !st.markOn[i] || m.Removed {
			continue
		}
		if _, ok := nw.index(m.ID); !ok {
			m.Removed = true
			m.Next, m.Prev, _ = st.survivors(m.ID)
		}
	}
	st.f, st.bk = st.bk, st.f
	st.rev++
	st.job = vlJob{}
	if st.qhead < len(st.queue) {
		op := st.queue[st.qhead]
		st.qhead++
		st.startJob(op)
		return
	}
	st.queue, st.qhead = st.queue[:0], 0
	st.pool = st.pool[:0]
	st.modelN = st.f.n
}

// survivors finds, for an item of the front buffer that the back buffer does
// not have, the nearest items after and before it that it does have (NoID if
// none within vlWalkMax), and an item at about its place.
func (st *VListState) survivors(id uint64) (next, prev, near uint64) {
	f, nw := &st.f, &st.bk
	next, prev, near = NoID, NoID, NoID
	i, ok := f.index(id)
	if !ok {
		return
	}
	for j := i + 1; j < min(f.n, i+1+vlWalkMax); j++ {
		if _, ok := nw.index(f.id(j)); ok {
			next = f.id(j)
			break
		}
	}
	for j := i - 1; j >= max(0, i-vlWalkMax); j-- {
		if _, ok := nw.index(f.id(j)); ok {
			prev = f.id(j)
			break
		}
	}
	if nw.n > 0 {
		near = nw.id(min(i, nw.n-1))
	}
	return
}
