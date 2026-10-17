package pilot

import (
	"math/bits"

	"gonativeui/fs"
)

// view is a directory listing as a pane shows it: the listing, which entries
// are visible (hidden files, the type-to-filter query), and the selection and
// keyboard cursor by entry index. Everything is pointer-free apart from the
// listing and the slices, which are reused.
type view struct {
	l     *fs.Listing
	lease uint32 // the Source lease l came with (0: not leased)

	// rows lists the visible entries in display order when some are filtered
	// out (filtered); otherwise every entry is visible in l.Order order.
	rows     []uint32
	filtered bool

	sel      []uint64 // selection: a bit per entry index
	nSel     int
	selBytes int64

	cur, anchor       int32 // entry index of the cursor and of the range anchor, -1: none
	curRow, anchorRow int   // their rows now, -1: not visible or not known yet

	ver uint32 // counts changes of the selection and the cursor
}

func (v *view) reset() {
	v.l, v.lease, v.rows, v.filtered = nil, 0, v.rows[:0], false
	v.clearSel()
	v.cur, v.anchor, v.curRow, v.anchorRow = -1, -1, -1, -1
}

// nEntries is the number of entries in the listing.
func (v *view) nEntries() int {
	if v.l == nil {
		return 0
	}
	return len(v.l.Entries)
}

// nRows is the number of visible rows.
func (v *view) nRows() int {
	if v.l == nil {
		return 0
	}
	if v.filtered {
		return len(v.rows)
	}
	return len(v.l.Order)
}

// entryAt is the entry shown in row r.
func (v *view) entryAt(r int) int32 {
	if v.filtered {
		return int32(v.rows[r])
	}
	return int32(v.l.Order[r])
}

// rowOf scans for the row that shows entry e, -1 if none (O(rows)).
func (v *view) rowOf(e int32) int {
	if e < 0 || v.l == nil {
		return -1
	}
	if v.filtered {
		for r, x := range v.rows {
			if int32(x) == e {
				return r
			}
		}
		return -1
	}
	for r, x := range v.l.Order {
		if int32(x) == e {
			return r
		}
	}
	return -1
}

func (v *view) isSel(e int32) bool {
	w := int(e >> 6)
	return w < len(v.sel) && v.sel[w]>>(uint(e)&63)&1 != 0
}

// sizeOf is the size that counts toward the selection's total (directories: 0).
func (v *view) sizeOf(e int32) int64 {
	if x := &v.l.Entries[e]; x.Kind != fs.KindDir {
		return x.Size
	}
	return 0
}

func (v *view) setSel(e int32, on bool) {
	if v.isSel(e) == on {
		return
	}
	w, m := int(e>>6), uint64(1)<<(uint(e)&63)
	if on {
		v.sel[w] |= m
		v.nSel++
		v.selBytes += v.sizeOf(e)
	} else {
		v.sel[w] &^= m
		v.nSel--
		v.selBytes -= v.sizeOf(e)
	}
}

// sizeSel makes the bitset cover n entries (growing only past its capacity).
func (v *view) sizeSel(n int) {
	w := (n + 63) >> 6
	if cap(v.sel) < w {
		v.sel = make([]uint64, withSlack(w))
	}
	v.sel = v.sel[:w]
	clear(v.sel)
	v.nSel, v.selBytes = 0, 0
}

func (v *view) clearSel() {
	v.ver++
	if v.nSel != 0 {
		clear(v.sel)
	}
	v.nSel, v.selBytes = 0, 0
}

// selectOnly selects entry e alone and makes it the cursor and the anchor.
func (v *view) selectOnly(e int32, row int) {
	v.ver++
	v.clearSel()
	v.setSel(e, true)
	v.cur, v.anchor, v.curRow, v.anchorRow = e, e, row, row
}

// toggle flips entry e and makes it the cursor and anchor.
func (v *view) toggle(e int32, row int) {
	v.ver++
	v.setSel(e, !v.isSel(e))
	v.cur, v.anchor, v.curRow, v.anchorRow = e, e, row, row
}

// selectRange selects exactly the rows between the anchor and row r (the
// anchor stays) and puts the cursor on r. Without an anchor it is selectOnly.
func (v *view) selectRange(r int) {
	v.ver++
	if v.anchorRow < 0 || v.anchorRow >= v.nRows() {
		v.selectOnly(v.entryAt(r), r)
		return
	}
	lo, hi := v.anchorRow, r
	if lo > hi {
		lo, hi = hi, lo
	}
	v.clearSel()
	for i := lo; i <= hi; i++ {
		v.setSel(v.entryAt(i), true)
	}
	v.cur, v.curRow = v.entryAt(r), r
}

// selectAll selects every visible row, keeping the cursor.
func (v *view) selectAll() {
	v.ver++
	n := v.nRows()
	if n == 0 {
		return
	}
	v.clearSel()
	if !v.filtered {
		// everything: set whole words, then count and sum sequentially
		for i := range v.sel {
			v.sel[i] = ^uint64(0)
		}
		if rem := len(v.l.Entries) & 63; rem != 0 {
			v.sel[len(v.sel)-1] = 1<<uint(rem) - 1
		}
		v.nSel = len(v.l.Entries)
		var sum int64
		for i := range v.l.Entries {
			if e := &v.l.Entries[i]; e.Kind != fs.KindDir {
				sum += e.Size
			}
		}
		v.selBytes = sum
		return
	}
	for _, e := range v.rows {
		v.setSel(int32(e), true)
	}
}

// moveCursor puts the cursor on row r. extend keeps the anchor and selects the
// range to it; otherwise the row alone is selected and becomes the anchor.
func (v *view) moveCursor(r int, extend bool) {
	n := v.nRows()
	if n == 0 {
		return
	}
	r = min(max(r, 0), n-1)
	if extend {
		v.selectRange(r)
		return
	}
	v.selectOnly(v.entryAt(r), r)
}

// recount recomputes nSel and selBytes from the bits (after a bulk change).
func (v *view) recount() {
	n, b := 0, int64(0)
	for w, x := range v.sel {
		n += bits.OnesCount64(x)
		for x != 0 {
			i := w<<6 + bits.TrailingZeros64(x)
			x &= x - 1
			b += v.sizeOf(int32(i))
		}
	}
	v.nSel, v.selBytes = n, b
}
