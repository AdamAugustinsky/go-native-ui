package core

// Table is an open-addressing hash table from uint64 to uint32 with linear
// probing and backward-shift deletion (no tombstones, so churn never forces a
// rehash). After it has reached the capacity it needs, Get, Put and Delete
// allocate nothing; Put grows the table (and reports it) only when an insert
// would pass the load factor of 70 %.
//
// Any uint64 is a valid key, including 0.
type Table struct {
	slots []tableSlot
	n     int
	limit int // maximum entries before growth
	shift uint
}

// val is the stored value plus one. The extra bit keeps MaxUint32 usable.
type tableSlot struct {
	key uint64
	val uint64
}

const tableLoad = 7 // out of 10

// NewTable returns a table that holds capacity entries without growing.
func NewTable(capacity int) *Table {
	t := &Table{}
	t.resize(capacity)
	return t
}

func (t *Table) resize(capacity int) {
	slots := 8
	bits := uint(3)
	for slots*tableLoad/10 < capacity {
		slots *= 2
		bits++
	}
	old := t.slots
	t.slots = make([]tableSlot, slots)
	t.limit = slots * tableLoad / 10
	t.shift = 64 - bits
	t.n = 0
	for _, s := range old {
		if s.val != 0 {
			t.insert(s.key, uint32(s.val-1))
		}
	}
}

func (t *Table) slot(k uint64) int { return int((k * 0x9E3779B97F4A7C15) >> t.shift) }

// insert assumes there is room.
func (t *Table) insert(k uint64, v uint32) {
	mask := len(t.slots) - 1
	i := t.slot(k)
	for t.slots[i].val != 0 {
		if t.slots[i].key == k {
			t.slots[i].val = uint64(v) + 1
			return
		}
		i = (i + 1) & mask
	}
	t.slots[i] = tableSlot{k, uint64(v) + 1}
	t.n++
}

// Len is the number of entries.
func (t *Table) Len() int { return t.n }

// Cap is the number of entries the table holds before it grows.
func (t *Table) Cap() int { return t.limit }

// Reserve grows the table so that it holds n entries without further growth.
func (t *Table) Reserve(n int) {
	if n > t.limit {
		t.resize(n)
	}
}

// Trim releases excess capacity, keeping every entry and room for n entries.
// It allocates only when n and Len are below a quarter of the capacity.
func (t *Table) Trim(n int) {
	if n = max(n, t.n); n < t.limit/4 {
		t.resize(n)
	}
}

// Clear removes every entry and keeps the capacity.
func (t *Table) Clear() {
	clear(t.slots)
	t.n = 0
}

// Get returns the value stored for k.
func (t *Table) Get(k uint64) (uint32, bool) {
	mask := len(t.slots) - 1
	for i := t.slot(k); t.slots[i].val != 0; i = (i + 1) & mask {
		if t.slots[i].key == k {
			return uint32(t.slots[i].val - 1), true
		}
	}
	return 0, false
}

// Put stores v for k. It reports whether the table had to grow (which
// allocates); a table pre-sized with NewTable or Reserve never does.
func (t *Table) Put(k uint64, v uint32) (grew bool) {
	mask := len(t.slots) - 1
	for i := t.slot(k); t.slots[i].val != 0; i = (i + 1) & mask {
		if t.slots[i].key == k {
			t.slots[i].val = uint64(v) + 1
			return false
		}
	}
	if t.n+1 > t.limit {
		t.resize(2 * t.limit)
		grew = true
	}
	t.insert(k, v)
	return grew
}

// Delete removes k and reports whether it was present.
func (t *Table) Delete(k uint64) bool {
	mask := len(t.slots) - 1
	i := t.slot(k)
	for ; ; i = (i + 1) & mask {
		if t.slots[i].val == 0 {
			return false
		}
		if t.slots[i].key == k {
			break
		}
	}
	// Backward shift: pull later entries of the probe run into the hole when
	// their ideal slot is not strictly inside (hole, entry].
	for {
		j := i
		for {
			j = (j + 1) & mask
			if t.slots[j].val == 0 {
				t.slots[i] = tableSlot{}
				t.n--
				return true
			}
			h := t.slot(t.slots[j].key)
			var between bool
			if i <= j {
				between = i < h && h <= j
			} else {
				between = i < h || h <= j
			}
			if !between {
				break
			}
		}
		t.slots[i] = t.slots[j]
		i = j
	}
}
