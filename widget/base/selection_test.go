package base

import (
	"slices"
	"testing"
)

func items(s *Selection) []uint32 { return s.Items(nil) }

func TestSelectionBasics(t *testing.T) {
	var s Selection
	s.Only(5)
	s.Toggle(9)
	s.Toggle(5)
	if got := items(&s); !slices.Equal(got, []uint32{9}) || s.Count() != 1 {
		t.Fatalf("after Only(5), Toggle(9), Toggle(5): %v (count %d), want [9]", got, s.Count())
	}
	if c, ok := s.Cursor(); !ok || c != 5 {
		t.Fatalf("cursor = %d,%v, want 5 (the last toggled)", c, ok)
	}
	s.SelectAll(130)
	if s.Count() != 130 || !s.Has(129) || s.Has(130) {
		t.Fatalf("SelectAll(130): count %d, has 129 %v, has 130 %v", s.Count(), s.Has(129), s.Has(130))
	}
	s.Set(64, false)
	if s.Count() != 129 || s.Has(64) {
		t.Fatalf("after deselecting one: count %d", s.Count())
	}
	s.Reset()
	if s.Count() != 0 {
		t.Fatal("Reset left items selected")
	}
	if _, ok := s.Cursor(); ok {
		t.Fatal("Reset left a cursor")
	}
}

// A range is by display row, so it follows the order, and it is still the same
// items after the order changes.
func TestSelectionRangeFollowsDisplayOrder(t *testing.T) {
	order := []uint32{7, 3, 9, 1, 5, 0}
	var s Selection
	s.Only(order[1]) // anchor: item 3 at row 1
	s.SelectRange(order, 1, 4)
	if got := items(&s); !slices.Equal(got, []uint32{1, 3, 5, 9}) {
		t.Fatalf("rows 1..4 selected items %v, want [1 3 5 9]", got)
	}
	if c, _ := s.Cursor(); c != 5 {
		t.Fatalf("cursor %d, want 5 (row 4)", c)
	}
	if a, _ := s.Anchor(); a != 3 {
		t.Fatalf("anchor %d, want 3", a)
	}
	// extending the other way round the anchor
	s.SelectRange(order, 1, 0)
	if got := items(&s); !slices.Equal(got, []uint32{3, 7}) {
		t.Fatalf("rows 1..0 selected %v, want [3 7]", got)
	}
	// identity order
	var id Selection
	id.Only(2)
	id.SelectRange(nil, 2, 200)
	if id.Count() != 199 || !id.Has(2) || !id.Has(200) || id.Has(201) || id.Has(1) {
		t.Fatalf("identity range 2..200: count %d", id.Count())
	}
}

// Sorting changes the order, not the items: the selection, the cursor and the
// anchor stay on their items.
func TestSelectionSurvivesSort(t *testing.T) {
	var s Selection
	s.Only(4)
	s.SelectRange([]uint32{0, 1, 2, 3, 4, 5}, 4, 2) // items 2,3,4
	want := []uint32{2, 3, 4}
	rev := []uint32{5, 4, 3, 2, 1, 0}
	if got := items(&s); !slices.Equal(got, want) {
		t.Fatal(got)
	}
	// the app reverses its order: nothing to tell the selection
	if c, _ := s.Cursor(); c != 2 {
		t.Fatalf("cursor %d", c)
	}
	// a range from the anchor in the new order is rows 1..3 = items 4,3,2
	s.SelectRange(rev, 1, 3)
	if got := items(&s); !slices.Equal(got, want) {
		t.Fatalf("after the sort, rows 1..3 selected %v, want %v", got, want)
	}
}

// Remap carries a selection across a replaced listing: items that moved stay
// selected, items that are gone are dropped, and so are the cursor and anchor
// when theirs is gone.
func TestSelectionRemap(t *testing.T) {
	var s Selection
	s.Reserve(10)
	s.Set(1, true)
	s.Set(4, true)
	s.Set(8, true)
	s.SetCursor(4)
	s.SetAnchor(8)
	// the new listing has 6 items: old 1→0, 4→3, 8 gone, old 0→5 (unselected)
	m := make([]uint32, 10)
	for i := range m {
		m[i] = NoItem
	}
	m[1], m[4], m[0] = 0, 3, 5
	s.Remap(m, 6)
	if got := items(&s); !slices.Equal(got, []uint32{0, 3}) || s.Count() != 2 {
		t.Fatalf("remapped selection %v (count %d), want [0 3]", got, s.Count())
	}
	if c, ok := s.Cursor(); !ok || c != 3 {
		t.Fatalf("cursor %d,%v, want 3", c, ok)
	}
	if _, ok := s.Anchor(); ok {
		t.Fatal("the anchor's item is gone but the anchor stayed")
	}
	if s.Has(5) {
		t.Fatal("an unselected item became selected")
	}
}

func TestSelectionAllocatesNothingWhenReserved(t *testing.T) {
	var s Selection
	s.Reserve(1 << 20)
	order := make([]uint32, 1<<20)
	for i := range order {
		order[i] = uint32(len(order) - 1 - i)
	}
	m := make([]uint32, 1<<20)
	for i := range m {
		m[i] = uint32(i)
	}
	if n := testing.AllocsPerRun(5, func() {
		s.Only(3)
		s.Toggle(70000)
		s.SelectRange(nil, 10, 900000)
		s.SelectRange(order, 5, 500000)
		s.SelectAll(1 << 20)
		s.Remap(m, 1<<20)
		s.Clear()
	}); n != 0 {
		t.Fatalf("selection operations allocated %v times", n)
	}
}

// NextSet walks the selected items in order, across words and past gaps.
func TestSelectionNextSet(t *testing.T) {
	var s Selection
	if _, ok := s.NextSet(0); ok {
		t.Fatal("an empty selection has a next item")
	}
	want := []uint32{0, 3, 63, 64, 200, 4097}
	for _, it := range want {
		s.Set(it, true)
	}
	var got []uint32
	for it, ok := s.NextSet(0); ok; it, ok = s.NextSet(it + 1) {
		got = append(got, it)
	}
	if !slices.Equal(got, want) {
		t.Errorf("walked %v, want %v", got, want)
	}
	if it, ok := s.NextSet(65); !ok || it != 200 {
		t.Errorf("NextSet(65) = %d, %v, want 200", it, ok)
	}
	if _, ok := s.NextSet(4098); ok {
		t.Error("NextSet past the last item found one")
	}
	if n := testing.AllocsPerRun(10, func() {
		for it, ok := s.NextSet(0); ok; it, ok = s.NextSet(it + 1) {
		}
	}); n != 0 {
		t.Errorf("a walk allocates %v times", n)
	}
}

// Growing the bitset must not bring back items that an earlier, larger
// selection left in recycled storage.
func TestSelectionGrowAfterRemapShowsNoStaleItems(t *testing.T) {
	var s Selection
	s.Only(100)
	s.Remap(nil, 1) // every item is gone
	s.Remap(nil, 1)
	if s.Count() != 0 {
		t.Fatalf("count %d after remapping everything away", s.Count())
	}
	s.Set(64, true)
	if s.Has(100) {
		t.Fatal("item 100 came back")
	}
	if got := s.Items(nil); !slices.Equal(got, []uint32{64}) || s.Count() != 1 {
		t.Fatalf("items %v, count %d, want [64] and 1", got, s.Count())
	}
	s.SelectAll(130)
	s.Remap(nil, 1)
	s.Remap(nil, 1)
	s.Set(129, true)
	if got := s.Items(nil); !slices.Equal(got, []uint32{129}) {
		t.Fatalf("items %v after SelectAll, remaps and growth, want [129]", got)
	}
}
