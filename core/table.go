package core

// Table is an open-addressing hash table from uint64 to uint32 with linear
// probing and backward-shift deletion (no tombstones, so churn never forces a
// rehash). After it has reached the capacity it needs, Get, Put and Delete
// allocate nothing; Put grows the table (and reports it) only when an insert
// would pass the load factor of 70 %.
//
// Any uint64 is a valid key, including 0.
type Table struct {
	keys  []uint64
	vals  []uint32
	used  []bool
	n     int
	limit int // maximum entries before growth
	shift uint
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
	oldK, oldV, oldU := t.keys, t.vals, t.used
	t.keys = make([]uint64, slots)
	t.vals = make([]uint32, slots)
	t.used = make([]bool, slots)
	t.limit = slots * tableLoad / 10
	t.shift = 64 - bits
	t.n = 0
	for i, u := range oldU {
		if u {
			t.insert(oldK[i], oldV[i])
		}
	}
}

func (t *Table) slot(k uint64) int { return int((k * 0x9E3779B97F4A7C15) >> t.shift) }

// insert assumes there is room.
func (t *Table) insert(k uint64, v uint32) {
	mask := len(t.keys) - 1
	i := t.slot(k)
	for t.used[i] {
		if t.keys[i] == k {
			t.vals[i] = v
			return
		}
		i = (i + 1) & mask
	}
	t.keys[i], t.vals[i], t.used[i] = k, v, true
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

// Clear removes every entry and keeps the capacity.
func (t *Table) Clear() {
	clear(t.used)
	t.n = 0
}

// Get returns the value stored for k.
func (t *Table) Get(k uint64) (uint32, bool) {
	mask := len(t.keys) - 1
	if PerfOn {
		PerfGets++
	}
	for i := t.slot(k); t.used[i]; i = (i + 1) & mask {
		if PerfOn {
			PerfProbes++
		}
		if t.keys[i] == k {
			return t.vals[i], true
		}
	}
	return 0, false
}

// Put stores v for k. It reports whether the table had to grow (which
// allocates); a table pre-sized with NewTable or Reserve never does.
func (t *Table) Put(k uint64, v uint32) (grew bool) {
	mask := len(t.keys) - 1
	for i := t.slot(k); t.used[i]; i = (i + 1) & mask {
		if t.keys[i] == k {
			t.vals[i] = v
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
	mask := len(t.keys) - 1
	i := t.slot(k)
	for ; ; i = (i + 1) & mask {
		if !t.used[i] {
			return false
		}
		if t.keys[i] == k {
			break
		}
	}
	// Backward shift: pull later entries of the probe run into the hole when
	// their ideal slot is not strictly inside (hole, entry].
	for {
		j := i
		for {
			j = (j + 1) & mask
			if !t.used[j] {
				t.used[i] = false
				t.n--
				return true
			}
			h := t.slot(t.keys[j])
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
		t.keys[i], t.vals[i] = t.keys[j], t.vals[j]
		i = j
	}
}
