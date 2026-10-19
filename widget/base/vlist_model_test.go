package base

import (
	"math/rand/v2"
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
