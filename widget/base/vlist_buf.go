package base

import "math/bits"

// A vlBuf is one complete index of a list: the ids in order, their heights,
// the height sums and the id table. A VListState has a front buffer, which
// every reader uses, and, once a long edit has needed it, a back buffer that
// such edits are built in (vlist_job.go).
//
// The ids and heights live in arrays with spare slots at both ends. Slot p of
// the arrays is "physical" index p; logical index i (the item's place in the
// list) is slot lo+i. The id table stores virtual positions, vpos = vbase+i:
// prepending k items lowers vbase and lo by k and leaves the stored positions
// of the others as they are, so prepend and append cost O(k), and an edit in
// the middle re-indexes only the side it shifts (the shorter one).
//
// Heights: hs[p] >= 0 is a measured height in pixels; vlUnmeasured marks an
// item never measured, which counts as the list's estimate. Slots outside the
// list hold 0, so the sums up to slot lo are 0 and the top of item i is the
// sum over slots [0, lo+i). The sums are kept per block of vlBlock slots
// (measured pixels and the number of unmeasured items), with a Fenwick tree
// over the blocks: a height change costs O(log blocks), the top of an item or
// the item at an offset O(log blocks + vlBlock).
type vlBuf struct {
	ids []uint64
	hs  []float32
	bm  []float64 // per block: the sum of the measured heights
	bu  []int32   // per block: the number of unmeasured items
	fm  []float64 // Fenwick tree of bm, 1-based, len nb+1
	fu  []int32   // Fenwick tree of bu
	lo  int       // the slot of logical index 0
	n   int
	// vbase is the virtual position of logical index 0.
	vbase int32
	tab   vlTable
}

const (
	vlBlockShift = 6
	vlBlock      = 1 << vlBlockShift
	// vlUnmeasured is the height of an item that was never measured.
	vlUnmeasured float32 = -1
)

// alloc gives b room for c slots, empty, with the data to start at lo, and an
// id table for tabN ids. Arrays that are large enough are reused.
func (b *vlBuf) alloc(c, lo, tabN int) {
	c = (c + vlBlock - 1) &^ (vlBlock - 1)
	nb := c >> vlBlockShift
	if cap(b.ids) < c {
		b.ids, b.hs = make([]uint64, c), make([]float32, c)
	} else {
		b.ids, b.hs = b.ids[:c], b.hs[:c]
		clear(b.hs)
	}
	if cap(b.bm) < nb {
		b.bm, b.bu = make([]float64, nb), make([]int32, nb)
		b.fm, b.fu = make([]float64, nb+1), make([]int32, nb+1)
	} else {
		b.bm, b.bu = b.bm[:nb], b.bu[:nb]
		b.fm, b.fu = b.fm[:nb+1], b.fu[:nb+1]
		clear(b.bm)
		clear(b.bu)
		clear(b.fm)
		clear(b.fu)
	}
	b.lo, b.n, b.vbase = lo, 0, 0
	if len(b.tab.keys) < vlTableCap(tabN) {
		b.tab.alloc(tabN)
	} else {
		b.tab.clear(0, len(b.tab.keys))
		b.tab.n = 0
	}
}

// slots is the number of slots.
func (b *vlBuf) slots() int { return len(b.hs) }

func (b *vlBuf) id(i int) uint64 { return b.ids[b.lo+i] }

// index is the logical index of id.
func (b *vlBuf) index(id uint64) (int, bool) {
	v, ok := b.tab.get(id)
	if !ok {
		return 0, false
	}
	return int(v - b.vbase), true
}

// hval is the height slot p counts for, with the estimate est.
func hval(h, est float32) float64 {
	if h < 0 {
		return float64(est)
	}
	return float64(h)
}

// --- the height sums ---

// fenAdd adds (dm, du) to block k (0-based).
func (b *vlBuf) fenAdd(k int, dm float64, du int32) {
	for j := k + 1; j < len(b.fm); j += j & -j {
		b.fm[j] += dm
		b.fu[j] += du
	}
}

// fenBuild rebuilds the Fenwick tree from the block sums in O(blocks).
func (b *vlBuf) fenBuild() {
	nb := len(b.bm)
	copy(b.fm[1:], b.bm)
	copy(b.fu[1:], b.bu)
	b.fm[0], b.fu[0] = 0, 0
	for j := 1; j <= nb; j++ {
		if p := j + (j & -j); p <= nb {
			b.fm[p] += b.fm[j]
			b.fu[p] += b.fu[j]
		}
	}
}

// blockSum sums the slots of block k.
func (b *vlBuf) blockSum(k int) (m float64, u int32) {
	for _, h := range b.hs[k<<vlBlockShift : (k+1)<<vlBlockShift] {
		if h < 0 {
			u++
		} else {
			m += float64(h)
		}
	}
	return m, u
}

// resum recomputes the sums of the blocks that hold slots [p0, p1) after
// their heights changed. With many blocks it rebuilds the tree instead of
// updating it block by block.
func (b *vlBuf) resum(p0, p1 int) {
	if p1 <= p0 {
		return
	}
	k0, k1 := p0>>vlBlockShift, (p1-1)>>vlBlockShift+1
	whole := (k1 - k0) > len(b.bm)/16
	for k := k0; k < k1; k++ {
		m, u := b.blockSum(k)
		if !whole {
			b.fenAdd(k, m-b.bm[k], u-b.bu[k])
		}
		b.bm[k], b.bu[k] = m, u
	}
	if whole {
		b.fenBuild()
	}
}

// resumAll recomputes every block and the tree.
func (b *vlBuf) resumAll() {
	for k := range b.bm {
		b.bm[k], b.bu[k] = b.blockSum(k)
	}
	b.fenBuild()
}

// setH sets the height of slot p.
func (b *vlBuf) setH(p int, h float32) {
	old := b.hs[p]
	if old == h {
		return
	}
	b.hs[p] = h
	var dm float64
	var du int32
	if old < 0 {
		du--
	} else {
		dm -= float64(old)
	}
	if h < 0 {
		du++
	} else {
		dm += float64(h)
	}
	k := p >> vlBlockShift
	b.bm[k] += dm
	b.bu[k] += du
	b.fenAdd(k, dm, du)
}

// prefix is the sum of the heights of slots [0, p).
func (b *vlBuf) prefix(p int, est float32) float64 {
	k := p >> vlBlockShift
	var m float64
	var u int32
	for j := k; j > 0; j -= j & -j {
		m += b.fm[j]
		u += b.fu[j]
	}
	for _, h := range b.hs[k<<vlBlockShift : p] {
		if h < 0 {
			u++
		} else {
			m += float64(h)
		}
	}
	return m + float64(u)*float64(est)
}

// top is the offset of the top of logical item i (i may be n: the total).
func (b *vlBuf) top(i int, est float32) float64 { return b.prefix(b.lo+i, est) }

// total is the height of the whole list.
func (b *vlBuf) total(est float32) float64 { return b.prefix(b.lo+b.n, est) }

// find returns the item that holds offset y, clamped to the items, and its
// top. It needs n > 0.
func (b *vlBuf) find(y float64, est float32) (i int, top float64) {
	if y <= 0 {
		return 0, 0
	}
	nb := len(b.bm)
	e := float64(est)
	pos := 0
	var acc float64
	for step := 1 << (bits.Len(uint(nb)) - 1); step > 0; step >>= 1 {
		if q := pos + step; q <= nb {
			if v := acc + b.fm[q] + float64(b.fu[q])*e; v <= y {
				pos, acc = q, v
			}
		}
	}
	// pos blocks lie wholly at or above y: the item is in block pos.
	end := b.lo + b.n
	p := pos << vlBlockShift
	if p < b.lo {
		p = b.lo // only empty slots before the list
	}
	for ; p < end; p++ {
		h := hval(b.hs[p], est)
		if acc+h > y {
			return p - b.lo, acc
		}
		acc += h
	}
	// y is at or past the end: the last item.
	i = b.n - 1
	return i, b.top(i, est)
}

// --- in-place edits ---

// shift moves slots [p0, p1) by d slots, ids and heights, re-indexing the
// moved ids by d (their vpos moves with them), empties the slots left behind
// and resums the slots it touched.
func (b *vlBuf) shift(p0, p1, d int) {
	if d == 0 || p1 <= p0 {
		return
	}
	copy(b.ids[p0+d:p1+d], b.ids[p0:p1])
	copy(b.hs[p0+d:p1+d], b.hs[p0:p1])
	for _, id := range b.ids[p0+d : p1+d] {
		b.tab.add(id, int32(d))
	}
	if d > 0 {
		clear(b.hs[p0:min(p0+d, p1)])
	} else {
		clear(b.hs[max(p1+d, p0):p1])
	}
	b.resum(min(p0, p0+d), max(p1, p1+d))
}

// room makes sure that slots [lo-front, lo+n+back) exist, moving the data
// within the arrays (no allocation) or into larger ones. Moving the data does
// not change any vpos, so the table is untouched.
func (b *vlBuf) room(front, back int) {
	if b.lo >= front && b.lo+b.n+back <= b.slots() {
		return
	}
	need := front + b.n + back
	c := b.slots()
	if need > c {
		b.regrow(need + need/2 + 2*vlBlock)
		c = b.slots()
	}
	spare := c - need
	lo := front + spare/2
	if lo == b.lo {
		return
	}
	copy(b.ids[lo:lo+b.n], b.ids[b.lo:b.lo+b.n])
	copy(b.hs[lo:lo+b.n], b.hs[b.lo:b.lo+b.n])
	if lo > b.lo {
		clear(b.hs[b.lo:min(lo, b.lo+b.n)])
	} else {
		clear(b.hs[max(lo+b.n, b.lo) : b.lo+b.n])
	}
	b.lo = lo
	b.resumAll()
}

// roomFits reports whether room(front, back) would not move the data.
func (b *vlBuf) roomFits(front, back int) bool {
	return b.lo >= front && b.lo+b.n+back <= b.slots()
}

// regrow moves the data into new arrays of c slots, centred.
func (b *vlBuf) regrow(c int) {
	c = (c + vlBlock - 1) &^ (vlBlock - 1)
	ids, hs := make([]uint64, c), make([]float32, c)
	lo := (c - b.n) / 2
	copy(ids[lo:], b.ids[b.lo:b.lo+b.n])
	copy(hs[lo:], b.hs[b.lo:b.lo+b.n])
	b.ids, b.hs, b.lo = ids, hs, lo
	nb := c >> vlBlockShift
	b.bm, b.bu = make([]float64, nb), make([]int32, nb)
	b.fm, b.fu = make([]float64, nb+1), make([]int32, nb+1)
	b.resumAll()
}

// spliceCost is the number of slots an in-place splice touches, and whether
// it fits the spare slots on the side it shifts (otherwise the whole list
// moves within, or into larger, arrays first).
func (b *vlBuf) spliceCost(at, removed, k int) (cost int, fits bool) {
	d := k - removed
	if at <= b.n-at-removed {
		return at + removed + k, d <= 0 || b.roomFits(d, 0)
	}
	return b.n - at - removed + removed + k, d <= 0 || b.roomFits(0, d)
}

// splice replaces the removed items from at with ids (unmeasured), in place.
// It shifts whichever side of the edit is shorter.
func (b *vlBuf) splice(at, removed int, ids []uint64) {
	k := len(ids)
	d := k - removed
	for _, id := range b.ids[b.lo+at : b.lo+at+removed] {
		b.tab.del(id)
	}
	clear(b.hs[b.lo+at : b.lo+at+removed])
	b.resum(b.lo+at, b.lo+at+removed)
	if at <= b.n-at-removed {
		// the head [0, at) shifts by -d
		if d > 0 {
			b.room(d, 0)
		}
		b.shift(b.lo, b.lo+at, -d)
		b.lo -= d
		b.vbase -= int32(d)
	} else {
		if d > 0 {
			b.room(0, d)
		}
		b.shift(b.lo+at+removed, b.lo+b.n, d)
	}
	b.n += d
	p0 := b.lo + at
	for j, id := range ids {
		b.ids[p0+j], b.hs[p0+j] = id, vlUnmeasured
		b.tab.put(id, b.vbase+int32(at+j))
	}
	b.resum(p0, p0+k)
}

// moveCost is the number of slots an in-place move touches, and whether it
// fits the spare slots.
func (b *vlBuf) moveCost(from, k, to int) (cost int, fits bool) {
	between, outside := b.movePaths(from, k, to)
	if between <= outside {
		return k + between, true
	}
	// closing the gap from the head frees k slots at the front
	lo, n := b.lo, b.n-k
	if from <= b.n-from-k {
		lo += k
	}
	if to <= n-to {
		return k + outside, lo >= k
	}
	return k + outside, lo+n+k <= b.slots()
}

// movePaths is what the two ways of moving cost: shifting the items between
// the run's two places, or taking the run out at one end and in at the other,
// each from the shorter side.
func (b *vlBuf) movePaths(from, k, to int) (between, outside int) {
	between = max(from, to) - min(from, to)
	outside = min(from, b.n-from-k) + min(to, b.n-k-to)
	return between, outside
}

// move puts the run of k items at from at index to of the result, in place,
// heights with their items. tmpIDs and tmpHs hold at least k values.
func (b *vlBuf) move(from, k, to int, tmpIDs []uint64, tmpHs []float32) {
	if k == 0 || from == to {
		return
	}
	copy(tmpIDs[:k], b.ids[b.lo+from:b.lo+from+k])
	copy(tmpHs[:k], b.hs[b.lo+from:b.lo+from+k])
	if between, outside := b.movePaths(from, k, to); between <= outside {
		// the items between the two places shift by k toward the run's old place
		if to < from {
			b.shift(b.lo+to, b.lo+from, k)
		} else {
			b.shift(b.lo+from+k, b.lo+to+k, -k)
		}
		p0 := b.lo + to
		for j := 0; j < k; j++ {
			b.ids[p0+j], b.hs[p0+j] = tmpIDs[j], tmpHs[j]
			b.tab.set(tmpIDs[j], b.vbase+int32(to+j))
		}
		b.resum(p0, p0+k)
		return
	}
	// Take the run out, closing the gap from the shorter side, then put it in
	// at to, opening it from the shorter side.
	for j := 0; j < k; j++ {
		b.tab.del(tmpIDs[j])
	}
	clear(b.hs[b.lo+from : b.lo+from+k])
	b.resum(b.lo+from, b.lo+from+k)
	if from <= b.n-from-k {
		b.shift(b.lo, b.lo+from, k)
		b.lo += k
		b.vbase += int32(k)
	} else {
		b.shift(b.lo+from+k, b.lo+b.n, -k)
	}
	b.n -= k
	if to <= b.n-to {
		b.room(k, 0)
		b.shift(b.lo, b.lo+to, -k)
		b.lo -= k
		b.vbase -= int32(k)
	} else {
		b.room(0, k)
		b.shift(b.lo+to, b.lo+b.n, k)
	}
	b.n += k
	p0 := b.lo + to
	for j := 0; j < k; j++ {
		b.ids[p0+j], b.hs[p0+j] = tmpIDs[j], tmpHs[j]
		b.tab.put(tmpIDs[j], b.vbase+int32(to+j))
	}
	b.resum(p0, p0+k)
}

// rescale multiplies every measured height by f (a change of display scale).
func (b *vlBuf) rescale(f float32) {
	for p := b.lo; p < b.lo+b.n; p++ {
		if h := b.hs[p]; h > 0 {
			b.hs[p] = h * f
		}
	}
	b.resumAll()
}
