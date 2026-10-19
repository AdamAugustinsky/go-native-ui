package base

import (
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"testing"
)

// refList is the plain model the state is checked against: the ids in order
// and the heights that were measured.
type refList struct {
	ids []uint64
	h   map[uint64]float32
}

func (r *refList) splice(at, removed int, ins []uint64) {
	for _, id := range r.ids[at : at+removed] {
		delete(r.h, id)
	}
	r.ids = slices.Concat(r.ids[:at], ins, r.ids[at+removed:])
}

func (r *refList) move(from, k, to int) {
	run := slices.Clone(r.ids[from : from+k])
	rest := slices.Concat(r.ids[:from], r.ids[from+k:])
	r.ids = slices.Concat(rest[:to], run, rest[to:])
}

func (r *refList) reset(ids []uint64) {
	keep := map[uint64]float32{}
	for _, id := range ids {
		if h, ok := r.h[id]; ok {
			keep[id] = h
		}
	}
	r.ids, r.h = slices.Clone(ids), keep
}

// check compares every reader of the front buffer with r.
func check(t *testing.T, st *VListState, r *refList, what string) {
	t.Helper()
	if st.Len() != len(r.ids) {
		t.Fatalf("%s: Len %d, want %d", what, st.Len(), len(r.ids))
	}
	const est = 10
	st.est = est
	var y float64
	for i, id := range r.ids {
		if got := st.ID(i); got != id {
			t.Fatalf("%s: ID(%d) = %d, want %d", what, i, got, id)
		}
		if j, ok := st.IndexOf(id); !ok || j != i {
			t.Fatalf("%s: IndexOf(%d) = %d, %v, want %d", what, id, j, ok, i)
		}
		h, ok := r.h[id]
		gh, gok := st.Height(i)
		if ok != gok || (ok && h != gh) {
			t.Fatalf("%s: Height(%d) = %v, %v, want %v, %v", what, i, gh, gok, h, ok)
		}
		if !ok {
			h = est
		}
		if i%7 == 0 || i == len(r.ids)-1 {
			if top := st.Top(i); top != y {
				t.Fatalf("%s: Top(%d) = %v, want %v", what, i, top, y)
			}
			if h > 0 {
				if fi, ftop := st.f.find(y+float64(h)/2, est); fi != i || ftop != y {
					t.Fatalf("%s: find(middle of %d) = %d at %v, want %d at %v", what, i, fi, ftop, i, y)
				}
			}
		}
		y += float64(h)
	}
	if tot := st.Total(); tot != y {
		t.Fatalf("%s: Total %v, want %v", what, tot, y)
	}
	if st.f.tab.n != len(r.ids) {
		t.Fatalf("%s: the id table holds %d ids for %d items", what, st.f.tab.n, len(r.ids))
	}
	// the Enumerator walks the same order
	if len(r.ids) > 0 {
		id, ok := st.First()
		for i := 0; ok; i++ {
			if id != r.ids[i] {
				t.Fatalf("%s: Next walk at %d gives %d, want %d", what, i, id, r.ids[i])
			}
			if i == len(r.ids)-1 {
				if l, _ := st.Last(); l != id {
					t.Fatalf("%s: Last %d, want %d", what, l, id)
				}
			}
			id, ok = st.Next(id)
		}
	}
}

// drain runs frames until no rebuild is pending.
func drain(t *testing.T, st *VListState, frame *uint32) {
	t.Helper()
	for i := 0; st.Pending(); i++ {
		if i > 100000 {
			t.Fatal("the rebuild does not finish")
		}
		*frame++
		st.Sync(*frame)
	}
}

// Random edits of every kind, short (in place) and long (rebuilt in the back
// buffer, several of them queued during a rebuild), against the plain model.
func TestVListEditsMatchModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		direct int // vlDirect
		slice  int // vlSliceUnits
		n      int
	}{
		{"in place", vlDirectMax, 0, 3000},
		{"rebuilt", 40, 97, 600},
		{"mixed", 300, 1500, 2000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func(d, s int) { vlDirect, vlSliceUnits = d, s }(vlDirect, vlSliceUnits)
			vlDirect, vlSliceUnits = tc.direct, tc.slice
			rng := rand.New(rand.NewPCG(1, uint64(tc.n)))
			var st VListState
			r := &refList{h: map[uint64]float32{}}
			next := uint64(1000)
			fresh := func(k int) []uint64 {
				ids := make([]uint64, k)
				for i := range ids {
					ids[i] = next
					next += uint64(1 + rng.IntN(3))
				}
				return ids
			}
			var frame uint32
			ins := fresh(tc.n)
			st.Append(ins)
			r.splice(0, 0, ins)
			drain(t, &st, &frame)
			check(t, &st, r, "initial append")
			for step := 0; step < 400; step++ {
				n := len(r.ids)
				var what string
				switch op := rng.IntN(10); {
				case op < 2:
					k := rng.IntN(60)
					ids := fresh(k)
					if rng.IntN(2) == 0 {
						st.Prepend(ids)
						r.splice(0, 0, ids)
						what = "prepend"
					} else {
						st.Append(ids)
						r.splice(n, 0, ids)
						what = "append"
					}
				case op < 5:
					at := rng.IntN(n + 1)
					rem := rng.IntN(min(n-at, 50) + 1)
					if rng.IntN(8) == 0 {
						rem = n - at // a long removal
					}
					ids := fresh(rng.IntN(40))
					st.Splice(at, rem, ids)
					r.splice(at, rem, ids)
					what = "splice"
				case op < 8:
					if n == 0 {
						continue
					}
					k := 1 + rng.IntN(min(n, 30))
					if rng.IntN(5) == 0 {
						k = 1 + rng.IntN(n)
					}
					from := rng.IntN(n - k + 1)
					to := rng.IntN(n - k + 1)
					st.Move(from, k, to)
					r.move(from, k, to)
					what = "move"
				case op < 9:
					// heights measured for items of the front buffer
					for j := 0; j < 20 && st.Len() > 0; j++ {
						i := rng.IntN(st.Len())
						id := st.ID(i)
						h := float32(rng.IntN(90))
						if rng.IntN(6) == 0 {
							st.InvalidateID(id)
							delete(r.h, id)
							continue
						}
						st.setHeight(id, h)
						if slices.Contains(r.ids, id) {
							r.h[id] = h
						}
					}
					what = "heights"
				default:
					// a new list that keeps some of the old ids
					ids := fresh(rng.IntN(tc.n))
					for j := range ids {
						if n > 0 && rng.IntN(3) == 0 {
							ids[j] = r.ids[rng.IntN(n)]
						}
					}
					ids = dedup(ids)
					st.Reset(ids)
					r.reset(ids)
					what = "reset"
				}
				// sometimes let a few edits queue up during a rebuild
				if rng.IntN(3) != 0 || step == 399 {
					drain(t, &st, &frame)
					check(t, &st, r, what)
				} else if st.Pending() {
					frame++
					st.Sync(frame)
				}
			}
		})
	}
}

func dedup(ids []uint64) []uint64 {
	seen := map[uint64]bool{}
	out := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// The table's backward-shift deletion keeps every remaining id reachable
// whatever the probe runs look like, also when they wrap around the end.
func TestVListTableDeletion(t *testing.T) {
	var tab vlTable
	tab.alloc(8)
	rng := rand.New(rand.NewPCG(7, 7))
	live := map[uint64]int32{}
	for i := 0; i < 20000; i++ {
		id := uint64(rng.IntN(40)) * uint64(len(tab.keys)) // ids that collide
		if rng.IntN(2) == 0 {
			id = uint64(rng.IntN(64))
		}
		if _, ok := live[id]; ok {
			tab.del(id)
			delete(live, id)
		} else {
			p := int32(rng.IntN(1000))
			tab.put(id, p)
			live[id] = p
		}
		if tab.n != len(live) {
			t.Fatalf("n %d, want %d", tab.n, len(live))
		}
		if i%97 == 0 {
			for id, p := range live {
				if got, ok := tab.get(id); !ok || got != p {
					t.Fatalf("step %d: get(%d) = %d, %v, want %d", i, id, got, ok, p)
				}
			}
		}
	}
}

// syncs runs n frames of Sync.
func syncs(st *VListState, frame *uint32, n int) {
	for i := 0; i < n; i++ {
		*frame++
		st.Sync(*frame)
	}
}

// A rebuilt deletion far longer than any search window moves the anchor and
// the marks to the exact survivors around the removed run.
func TestVListLongDeletionSurvivors(t *testing.T) {
	const n = 200_000
	var st VListState
	st.Reserve(n)
	st.Append(seq(0, n))
	st.SetAnchor(100_010, 3)
	st.SetMark(0, 100_005)
	st.SetMark(1, 169_999)
	st.Splice(100_000, 70_000, nil)
	if !st.Pending() {
		t.Fatal("the deletion was applied in place: it should be rebuilt")
	}
	var frame uint32
	drain(t, &st, &frame)
	if a, off := st.Anchor(); a != 170_000 || off != 0 {
		t.Fatalf("the anchor is %d at %v, want the first survivor 170000 at 0", a, off)
	}
	for i := 0; i < 2; i++ {
		if m := st.Mark(i); !m.Removed || m.Next != 170_000 || m.Prev != 99_999 {
			t.Fatalf("mark %d: %+v, want removed between 99999 and 170000", i, m)
		}
	}

	// a queued deletion during a rebuild, its survivors as exact
	st.SetAnchor(50_000, 0)
	st.Move(10, 1, 120_000)        // long: rebuilt
	st.Splice(40_000, 30_000, nil) // queued: ids 40001 to 70000
	drain(t, &st, &frame)
	if a, _ := st.Anchor(); a != 70_001 {
		t.Fatalf("after a queued deletion the anchor is %d, want 70001", a)
	}
	// queued and short: applied to the back buffer in place
	st.SetAnchor(1020, 0)
	st.Move(10, 1, 90_000)
	st.Splice(1000, 50, nil) // ids 1002 to 1051 (10 moved to the end before)
	drain(t, &st, &frame)
	if a, _ := st.Anchor(); a != 1052 {
		t.Fatalf("after a short queued deletion the anchor is %d, want 1052", a)
	}
}

// Queued edits are applied within the rebuild's slices, not all in the frame
// of the swap.
func TestVListQueuedEditsAreSliced(t *testing.T) {
	defer func(d, s int) { vlDirect, vlSliceUnits = d, s }(vlDirect, vlSliceUnits)
	vlDirect, vlSliceUnits = 1000, 2000
	var st VListState
	r := &refList{h: map[uint64]float32{}}
	st.Append(seq(0, 5000))
	r.splice(0, 0, seq(0, 5000))
	st.Move(100, 1, 4000)
	r.move(100, 1, 4000)
	for k := 0; k < 20; k++ { // short: 900 slots each
		st.Move(1000+k, 1, 1900+k)
		r.move(1000+k, 1, 1900+k)
	}
	var frame uint32
	for i := 0; st.Pending(); i++ {
		if i > 10000 {
			t.Fatal("the rebuild does not finish")
		}
		q, nq := st.qhead, len(st.queue)
		syncs(&st, &frame, 1)
		applied := st.qhead - q
		if !st.job.on {
			applied = nq - q // published: the rest of the queue landed
		}
		if applied > 2 {
			t.Fatalf("one slice of 2000 units applied %d queued edits of 900 slots", applied)
		}
	}
	check(t, &st, r, "queued moves")
}

// mallocs counts the heap allocations f makes.
func mallocs(f func()) uint64 {
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	f()
	runtime.ReadMemStats(&m1)
	return m1.Mallocs - m0.Mallocs
}

// Reserve covers both buffers: once the back buffer exists, a long edit with
// an append queued meanwhile, and growth up to the reservation after the
// swap, allocate nothing.
func TestVListReserveCoversRebuilds(t *testing.T) {
	const n = 100_000
	var st VListState
	st.Reserve(n)
	st.Append(seq(0, n/2))
	var frame uint32
	// two long edits (the same ids again, unmeasured): the first allocates
	// the back buffer, the second makes the reserved buffer the front one
	// again
	again := seq(5_000, 30_000)
	for k := 0; k < 2; k++ {
		st.Splice(5_000, 30_000, again)
		if !st.Pending() {
			t.Fatal("the splice was applied in place: it should be rebuilt")
		}
		drain(t, &st, &frame)
	}
	one := []uint64{0}
	page := make([]uint64, 1000)
	next := uint64(10_000_000)
	got := mallocs(func() {
		st.Splice(5_000, 30_000, again)
		for k := 0; k < 5; k++ { // queued during the rebuild
			one[0] = next
			next++
			st.Append(one)
			st.Prepend(one[:0])
		}
		for st.Pending() {
			frame++
			st.Sync(frame)
		}
		for st.Len()+len(page) <= n { // up to the reservation
			for i := range page {
				page[i] = next
				next++
			}
			st.Append(page)
			for st.Pending() {
				frame++
				st.Sync(frame)
			}
		}
	})
	if got != 0 {
		t.Fatalf("%d allocations within Reserve", got)
	}
	if st.Len() <= n-len(page) {
		t.Fatalf("Len %d: the appends did not reach the reservation", st.Len())
	}
}

// Heights measured during a long rebuild reach the back buffer: unchanged
// measurements do not fill the log, and once more heights change than it
// holds the rebuild reconciles them all.
func TestVListHeightsDuringRebuild(t *testing.T) {
	defer func(d, s int) { vlDirect, vlSliceUnits = d, s }(vlDirect, vlSliceUnits)
	vlDirect, vlSliceUnits = 40, 50
	const n = 20_000
	for _, distinct := range []bool{false, true} {
		var st VListState
		st.Reserve(n)
		st.Append(seq(0, n))
		r := &refList{ids: seq(0, n), h: map[uint64]float32{}}
		for i := 0; i < n; i++ {
			st.setHeight(uint64(i), 30)
			r.h[uint64(i)] = 30
		}
		st.Move(100, 1, 15_000)
		r.move(100, 1, 15_000)
		var frame uint32
		for st.job.phase != phIndex {
			syncs(&st, &frame, 1)
		}
		// the items built while the rebuild runs, measured every frame
		for f := 0; f < 40; f++ {
			for i := 0; i < 256; i++ {
				id := uint64(f*256 + i)
				h := float32(30)
				if distinct {
					h = float32(31 + f)
				}
				st.setHeight(id, h)
				r.h[id] = h
			}
			syncs(&st, &frame, 1)
		}
		if !distinct && (len(st.log) != 0 || st.job.recon) {
			t.Fatalf("unchanged heights were logged (%d, overflowed %v)", len(st.log), st.job.recon)
		}
		st.setHeight(7, 99) // a change after all that
		r.h[7] = 99
		drain(t, &st, &frame)
		check(t, &st, r, fmt.Sprintf("heights during a rebuild (distinct %v)", distinct))
	}
}

// A Reset to a few ids of a list with a large capacity is rebuilt in slices
// (its clear covers the capacity); a small list's is applied at once.
func TestVListSmallResetOfLargeList(t *testing.T) {
	var st VListState
	st.Reserve(200_000)
	st.Append(seq(0, 200_000))
	var frame uint32
	st.Move(10, 1, 150_000)
	drain(t, &st, &frame)
	st.Reset(seq(5_000_000, 10))
	if !st.Pending() {
		t.Fatal("a Reset over a large capacity ran in one frame")
	}
	drain(t, &st, &frame)
	check(t, &st, &refList{ids: seq(5_000_000, 10)}, "small reset")

	var small VListState
	small.Reserve(1000)
	small.Append(seq(0, 1000))
	small.Reset(seq(1, 300))
	if small.Pending() {
		t.Fatal("a Reset of a small list was not applied at once")
	}
}
