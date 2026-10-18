package pilot

import (
	"gonativeui/fs"
	"gonativeui/widget"
)

// view is a directory listing as a pane shows it: the listing, which entries
// are visible (hidden files, the type-to-filter query), and the table state
// that holds the selection and the keyboard cursor. Items of the selection
// are entry indices of the listing, the stable ids of the table's rows, so
// filtering and sorting leave the selection alone and Remap carries it over to
// a new listing of the same directory. Everything is pointer-free apart from
// the listing and the slices, which are reused.
type view struct {
	l     *fs.Listing
	lease uint32 // the Source lease l came with (0: not leased)

	// rows lists the visible entries in display order when some are filtered
	// out (filtered); otherwise every entry is visible in l.Order order.
	rows     []uint32
	filtered bool

	// ts is the table's state: its Sel is the selection, cursor and anchor by
	// entry index; its Order is rows or l.Order (set by order, every frame,
	// since rows grows while a filter job runs); its Scroll and Sort are the
	// table's own.
	ts widget.TableState

	// the total size of the selection, as of the version of the selection it was
	// computed for
	sizeVer   uint32
	sizeValid bool
	size      int64
}

func (v *view) reset() {
	v.l, v.lease, v.rows, v.filtered = nil, 0, v.rows[:0], false
	v.ts.Sel.Reset()
	v.ts.Order = nil
	v.sizeValid = false
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

// order is the display order for the table: the visible entries by row.
func (v *view) order() []uint32 {
	switch {
	case v.l == nil:
		return nil
	case v.filtered:
		return v.rows
	}
	return v.l.Order
}

// rowOf scans for the row that shows entry e, -1 if none (O(rows)).
func (v *view) rowOf(e int32) int {
	if e < 0 || v.l == nil {
		return -1
	}
	for r, x := range v.order() {
		if int32(x) == e {
			return r
		}
	}
	return -1
}

func (v *view) isSel(e int32) bool { return e >= 0 && v.ts.Sel.Has(uint32(e)) }

// nSel is the number of selected entries.
func (v *view) nSel() int { return v.ts.Sel.Count() }

// cursor is the entry the keyboard cursor is on, -1 if none.
func (v *view) cursor() int32 {
	if it, ok := v.ts.Sel.Cursor(); ok {
		return int32(it)
	}
	return -1
}

// selectOnly selects entry e alone and makes it the cursor and the anchor.
func (v *view) selectOnly(e int32) { v.ts.Sel.Only(uint32(e)) }

// sizeOf is the size that counts toward the selection's total (directories: 0).
func (v *view) sizeOf(e int32) int64 {
	if x := &v.l.Entries[e]; x.Kind != fs.KindDir {
		return x.Size
	}
	return 0
}

// selBytes is the total size of the selected entries. It is worked out when
// the selection has changed since, and is O(selected) then.
func (v *view) selBytes() int64 {
	sel := &v.ts.Sel
	if v.sizeValid && v.sizeVer == sel.Version() {
		return v.size
	}
	var sum int64
	if v.l != nil {
		for it, ok := sel.NextSet(0); ok; it, ok = sel.NextSet(it + 1) {
			if int(it) < len(v.l.Entries) {
				sum += v.sizeOf(int32(it))
			}
		}
	}
	v.size, v.sizeVer, v.sizeValid = sum, sel.Version(), true
	return sum
}
