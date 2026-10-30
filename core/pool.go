package core

// Handle names a live Pool slot: the index in the low 32 bits and the slot's
// generation in the high 32 bits. A Handle whose slot was freed (and maybe
// reused) is no longer Valid. The zero Handle is never valid.
type Handle uint64

// Index is the slot index of h.
func (h Handle) Index() uint32 { return uint32(h) }

// Gen is the generation of h.
func (h Handle) Gen() uint32 { return uint32(h >> 32) }

func makeHandle(idx, gen uint32) Handle { return Handle(gen)<<32 | Handle(idx) }

// Pool hands out slot indices with a free list and generation counters. The
// caller keeps the data in its own pointer-free slice indexed by Index, and
// grows it when Alloc returns an index at or past its length (Slots reports
// the total).
//
// Alloc and Free allocate nothing once the pool has seen its peak size.
type Pool struct {
	gen  []uint32 // generation of each slot; starts at 1
	live []bool
	free []uint32 // stack of free slot indices
	n    int
}

// NewPool returns a pool with room for capacity slots before it grows.
func NewPool(capacity int) *Pool {
	return &Pool{
		gen:  make([]uint32, 0, capacity),
		live: make([]bool, 0, capacity),
		free: make([]uint32, 0, capacity),
	}
}

// Reserve makes room for n slots, so that allocating up to n slots in total
// (live plus free) never grows the pool. Existing slots are kept.
func (p *Pool) Reserve(n int) {
	if cap(p.gen) < n {
		p.gen = append(make([]uint32, 0, n), p.gen...)
	}
	if cap(p.live) < n {
		p.live = append(make([]bool, 0, n), p.live...)
	}
	// Free must never allocate: the free list can hold every slot.
	if cap(p.free) < max(n, 2*len(p.gen)) {
		p.free = append(make([]uint32, 0, max(n, 2*len(p.gen))), p.free...)
	}
}

// Alloc returns a free slot, reusing the most recently freed one first.
func (p *Pool) Alloc() Handle {
	var idx uint32
	if n := len(p.free); n > 0 {
		idx = p.free[n-1]
		p.free = p.free[:n-1]
		if Assertions {
			Assert(int(idx) < len(p.gen) && !p.live[idx], "pool: free list names a live or unknown slot")
		}
	} else {
		idx = uint32(len(p.gen))
		p.gen = append(p.gen, 1)
		p.live = append(p.live, false)
		if cap(p.free) < len(p.gen) { // Free must never allocate
			nf := make([]uint32, len(p.free), 2*len(p.gen))
			copy(nf, p.free)
			p.free = nf
		}
	}
	p.live[idx] = true
	p.n++
	if Assertions {
		Assert(p.n+len(p.free) == len(p.gen), "pool: live plus free slots differ from the slots made")
	}
	return makeHandle(idx, p.gen[idx])
}

// Valid reports whether h names a live slot of the current generation.
func (p *Pool) Valid(h Handle) bool {
	i := h.Index()
	return int(i) < len(p.gen) && p.live[i] && p.gen[i] == h.Gen()
}

// Free releases the slot of h and reports whether h was valid.
func (p *Pool) Free(h Handle) bool {
	if !p.Valid(h) {
		return false
	}
	return p.FreeIndex(h.Index())
}

// FreeIndex releases slot i if it is live.
func (p *Pool) FreeIndex(i uint32) bool {
	if int(i) >= len(p.gen) || !p.live[i] {
		return false
	}
	p.live[i] = false
	p.gen[i]++
	if p.gen[i] == 0 { // wrapped: keep the zero Handle invalid
		p.gen[i] = 1
	}
	if Assertions {
		// Free must never allocate: Alloc and Reserve keep room for every slot.
		Assert(len(p.free) < cap(p.free), "pool: free list would grow")
	}
	p.free = append(p.free, i)
	p.n--
	if Assertions {
		Assert(p.n >= 0 && p.n+len(p.free) == len(p.gen), "pool: live plus free slots differ from the slots made")
	}
	return true
}

// Live reports whether slot i is allocated.
func (p *Pool) Live(i uint32) bool { return int(i) < len(p.gen) && p.live[i] }

// Handle returns the current handle of live slot i (zero if i is free).
func (p *Pool) Handle(i uint32) Handle {
	if !p.Live(i) {
		return 0
	}
	return makeHandle(i, p.gen[i])
}

// Len is the number of live slots.
func (p *Pool) Len() int { return p.n }

// Slots is the number of slots ever created (live plus free). Indices
// returned by Alloc are always below it.
func (p *Pool) Slots() int { return len(p.gen) }
