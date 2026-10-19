package base

// vlTable maps item ids to their positions: open addressing with linear
// probing over two pointer-free arrays, deletion by backward shift (no
// tombstones). A position is a "virtual position" (vpos): the logical index
// plus the buffer's base, so prepending moves the base and leaves every stored
// position as it is.
type vlTable struct {
	keys  []uint64 // NoID marks an empty slot
	pos   []int32
	mask  uint64
	shift uint8
	n     int
}

// vlMaxLoad is the load factor, in sixteenths, past which the table grows.
const vlMaxLoad = 11

// vlTableCap is the slot count of a table that holds n ids within the load
// factor: a power of two of at least 16.
func vlTableCap(n int) int {
	c := 16
	for c*vlMaxLoad/16 < n+1 {
		c <<= 1
	}
	return c
}

// alloc makes the table empty with room for n ids.
func (t *vlTable) alloc(n int) {
	c := vlTableCap(n)
	if cap(t.keys) >= c {
		t.keys, t.pos = t.keys[:c], t.pos[:c]
	} else {
		t.keys, t.pos = make([]uint64, c), make([]int32, c)
	}
	t.setCap(c)
	t.clear(0, c)
	t.n = 0
}

func (t *vlTable) setCap(c int) {
	t.mask = uint64(c - 1)
	t.shift = 64
	for x := c; x > 1; x >>= 1 {
		t.shift--
	}
}

// clear empties the slots [from, to); the caller resets n.
func (t *vlTable) clear(from, to int) {
	k := t.keys[from:to]
	for i := range k {
		k[i] = NoID
	}
}

func (t *vlTable) slot(id uint64) uint64 {
	if t.shift >= 64 {
		return 0
	}
	return (id * 0x9E3779B97F4A7C15) >> t.shift
}

// get returns the position of id.
func (t *vlTable) get(id uint64) (int32, bool) {
	if t.n == 0 || id == NoID {
		return 0, false
	}
	for i := t.slot(id); ; i = (i + 1) & t.mask {
		switch t.keys[i] {
		case id:
			return t.pos[i], true
		case NoID:
			return 0, false
		}
	}
}

// put adds id at position p. It panics if id is NoID or already present, and
// grows the table (an allocation) past the load factor.
func (t *vlTable) put(id uint64, p int32) {
	if id == NoID {
		panic("base: VListState: the id NoID is reserved")
	}
	if len(t.keys) == 0 || (t.n+1)*16 > len(t.keys)*vlMaxLoad {
		t.grow(t.n + 1)
	}
	for i := t.slot(id); ; i = (i + 1) & t.mask {
		switch t.keys[i] {
		case id:
			panic("base: VListState: duplicate id")
		case NoID:
			t.keys[i], t.pos[i] = id, p
			t.n++
			return
		}
	}
}

// set changes the position of id, which must be present.
func (t *vlTable) set(id uint64, p int32) {
	for i := t.slot(id); ; i = (i + 1) & t.mask {
		switch t.keys[i] {
		case id:
			t.pos[i] = p
			return
		case NoID:
			panic("base: VListState: id table out of step with the ids")
		}
	}
}

// add moves the position of id by d.
func (t *vlTable) add(id uint64, d int32) {
	for i := t.slot(id); ; i = (i + 1) & t.mask {
		switch t.keys[i] {
		case id:
			t.pos[i] += d
			return
		case NoID:
			panic("base: VListState: id table out of step with the ids")
		}
	}
}

// del removes id, which must be present, shifting the probe run back over the
// hole.
func (t *vlTable) del(id uint64) {
	i := t.slot(id)
	for t.keys[i] != id {
		if t.keys[i] == NoID {
			panic("base: VListState: id table out of step with the ids")
		}
		i = (i + 1) & t.mask
	}
	for j := (i + 1) & t.mask; ; j = (j + 1) & t.mask {
		k := t.keys[j]
		if k == NoID {
			break
		}
		h := t.slot(k)
		// k may fill the hole at i if its home h is not in (i, j] cyclically.
		if (j > i && (h <= i || h > j)) || (j < i && h <= i && h > j) {
			t.keys[i], t.pos[i] = k, t.pos[j]
			i = j
		}
	}
	t.keys[i] = NoID
	t.n--
}

// grow rehashes into a table with room for n ids.
func (t *vlTable) grow(n int) {
	oldK, oldP := t.keys, t.pos
	c := vlTableCap(max(n, 2*t.n))
	t.keys, t.pos = make([]uint64, c), make([]int32, c)
	t.setCap(c)
	t.clear(0, c)
	t.n = 0
	for i, k := range oldK {
		if k != NoID {
			t.put(k, oldP[i])
		}
	}
}
