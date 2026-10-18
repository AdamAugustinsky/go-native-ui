package widget

import "math/bits"

// NoItem is the item a Remap table maps removed items to, and the value that
// stands for "no item".
const NoItem = ^uint32(0)

// Selection is the selected items of a list or table, plus a cursor and an
// anchor. Items are the app's stable ids (a row of its model, an index into a
// listing), not display rows, so sorting and filtering leave the selection
// alone; a table of 1M items costs 125 KB, one bit each, and no pointers.
//
// The cursor is the item keyboard navigation is on, the anchor the item a
// range (Shift) extends from. Both are items too. When the app replaces its
// listing it either calls Remap, with the new index of every old item, or
// Reset.
//
// The zero Selection is empty and ready to use; Reserve sizes it up front so
// that selecting never allocates. It must not be copied once in use.
type Selection struct {
	bits []uint64
	alt  []uint64 // scratch of Remap
	n    int

	cur, anc       uint32
	hasCur, hasAnc bool
	ver            uint32
}

// Reserve makes room for items 0..items-1 and for a Remap of that many.
func (s *Selection) Reserve(items int) {
	w := (items + 63) >> 6
	if cap(s.bits) < w {
		s.bits = append(make([]uint64, 0, w), s.bits...)
	}
	if cap(s.alt) < w {
		s.alt = make([]uint64, 0, w)
	}
}

// grow makes the bitset cover item.
func (s *Selection) grow(item uint32) {
	w := int(item>>6) + 1
	if w > len(s.bits) {
		if w > cap(s.bits) {
			s.bits = append(make([]uint64, 0, w+w/2), s.bits...)
		}
		s.bits = s.bits[:w]
	}
}

// Has reports whether item is selected.
func (s *Selection) Has(item uint32) bool {
	w := int(item >> 6)
	return w < len(s.bits) && s.bits[w]>>(item&63)&1 != 0
}

// Count is the number of selected items.
func (s *Selection) Count() int { return s.n }

// Version changes with every change of the selection, the cursor or the
// anchor; an app that shows the selection (a status line) redoes it when the
// version moves.
func (s *Selection) Version() uint32 { return s.ver }

// Cursor is the item keyboard navigation is on.
func (s *Selection) Cursor() (item uint32, ok bool) { return s.cur, s.hasCur }

// Anchor is the item a range extends from.
func (s *Selection) Anchor() (item uint32, ok bool) { return s.anc, s.hasAnc }

// SetCursor moves the cursor to item without touching the selection.
func (s *Selection) SetCursor(item uint32) {
	s.cur, s.hasCur = item, true
	s.ver++
}

// SetAnchor sets the item ranges extend from.
func (s *Selection) SetAnchor(item uint32) {
	s.anc, s.hasAnc = item, true
	s.ver++
}

// Set selects or deselects item; the cursor and anchor stay.
func (s *Selection) Set(item uint32, on bool) {
	if s.Has(item) == on {
		return
	}
	s.ver++
	if on {
		s.grow(item)
		s.bits[item>>6] |= 1 << (item & 63)
		s.n++
	} else {
		s.bits[item>>6] &^= 1 << (item & 63)
		s.n--
	}
}

// Clear deselects everything; the cursor and anchor stay.
func (s *Selection) Clear() {
	if s.n != 0 {
		clear(s.bits)
		s.n = 0
	}
	s.ver++
}

// Reset empties the selection and forgets the cursor and anchor.
func (s *Selection) Reset() {
	s.Clear()
	s.hasCur, s.hasAnc = false, false
}

// Only selects item alone and makes it the cursor and the anchor.
func (s *Selection) Only(item uint32) {
	s.Clear()
	s.Set(item, true)
	s.cur, s.anc, s.hasCur, s.hasAnc = item, item, true, true
}

// Toggle flips item and makes it the cursor and the anchor.
func (s *Selection) Toggle(item uint32) {
	s.Set(item, !s.Has(item))
	s.cur, s.anc, s.hasCur, s.hasAnc = item, item, true, true
}

// SelectAll selects items 0..items-1; the cursor and anchor stay.
func (s *Selection) SelectAll(items int) {
	if items <= 0 {
		s.Clear()
		return
	}
	s.grow(uint32(items - 1))
	for i := range s.bits {
		s.bits[i] = 0
	}
	full := items >> 6
	for i := 0; i < full; i++ {
		s.bits[i] = ^uint64(0)
	}
	if rem := items & 63; rem != 0 {
		s.bits[full] = 1<<uint(rem) - 1
	}
	s.n = items
	s.ver++
}

// SelectRange selects exactly the items in display rows a to b, inclusive and
// in either order, and puts the cursor on the item of row b. order maps display
// rows to items (nil: row r shows item r). Without an anchor the item of row a
// becomes it.
func (s *Selection) SelectRange(order []uint32, a, b int) {
	end := itemAt(order, b)
	if a > b {
		a, b = b, a
	}
	s.Clear()
	if len(order) == 0 {
		s.setRun(a, b)
	} else {
		for r := a; r <= b; r++ {
			s.Set(order[r], true)
		}
	}
	if !s.hasAnc {
		s.SetAnchor(itemAt(order, a))
	}
	s.SetCursor(end)
}

// setRun selects items lo..hi (the selection is empty).
func (s *Selection) setRun(lo, hi int) {
	s.grow(uint32(hi))
	for lo <= hi && lo&63 != 0 {
		s.bits[lo>>6] |= 1 << uint(lo&63)
		s.n++
		lo++
	}
	for ; lo+63 <= hi; lo += 64 {
		s.bits[lo>>6] = ^uint64(0)
		s.n += 64
	}
	for ; lo <= hi; lo++ {
		s.bits[lo>>6] |= 1 << uint(lo&63)
		s.n++
	}
	s.ver++
}

// itemAt is the item shown in display row r.
func itemAt(order []uint32, r int) uint32 {
	if len(order) == 0 {
		return uint32(r)
	}
	return order[r]
}

// NextSet is the first selected item that is at least from, if there is one. A
// loop over all selected items is
//
//	for it, ok := s.NextSet(0); ok; it, ok = s.NextSet(it + 1) { … }
//
// which takes time in proportion to the bitset and the selected items, and
// allocates nothing.
func (s *Selection) NextSet(from uint32) (uint32, bool) {
	w := int(from >> 6)
	if w >= len(s.bits) {
		return 0, false
	}
	x := s.bits[w] >> (from & 63) << (from & 63)
	for {
		if x != 0 {
			return uint32(w<<6 + bits.TrailingZeros64(x)), true
		}
		w++
		if w >= len(s.bits) {
			return 0, false
		}
		x = s.bits[w]
	}
}

// Items appends the selected items, in increasing order, to dst.
func (s *Selection) Items(dst []uint32) []uint32 {
	for w, x := range s.bits {
		for x != 0 {
			dst = append(dst, uint32(w<<6+bits.TrailingZeros64(x)))
			x &= x - 1
		}
	}
	return dst
}

// Remap carries the selection, the cursor and the anchor over to a replaced
// listing of items items: m[old] is the new item of old item old, or NoItem
// if it is gone; items past the end of m are gone. Items that are not
// selected stay unselected.
func (s *Selection) Remap(m []uint32, items int) {
	w := (items + 63) >> 6
	if cap(s.alt) < w {
		s.alt = make([]uint64, 0, w)
	}
	nb := s.alt[:w]
	clear(nb)
	n := 0
	for wi, x := range s.bits {
		for x != 0 {
			old := wi<<6 + bits.TrailingZeros64(x)
			x &= x - 1
			if old < len(m) && m[old] != NoItem && int(m[old]) < items {
				ni := m[old]
				if nb[ni>>6]>>(ni&63)&1 == 0 {
					nb[ni>>6] |= 1 << (ni & 63)
					n++
				}
			}
		}
	}
	s.alt, s.bits = s.bits[:0], nb
	s.n = n
	remap := func(item uint32, ok bool) (uint32, bool) {
		if !ok || int(item) >= len(m) || m[item] == NoItem || int(m[item]) >= items {
			return 0, false
		}
		return m[item], true
	}
	s.cur, s.hasCur = remap(s.cur, s.hasCur)
	s.anc, s.hasAnc = remap(s.anc, s.hasAnc)
	s.ver++
}
