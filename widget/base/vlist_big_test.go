package base

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/ui"
)

const million = 1_000_000

func mix64(x uint64) uint64 {
	x = (x ^ x>>30) * 0xBF58476D1CE4E5B9
	x = (x ^ x>>27) * 0x94D049BB133111EB
	return x ^ x>>31
}

// lean drives a VList of mixed-height items without allocating in the test
// itself: the heights come from the id (and the frame, for items that grow
// and shrink), the edits from a script.
type lean struct {
	c      *ui.Context
	in     ui.Input
	dl     gpu.DrawList
	st     VListState
	o      VListOpts
	v      VList
	frameN int
	wobble bool // some items change height every few frames
	built  int
	edit   func(f int)
}

func newLean(n int, o VListOpts) *lean {
	l := &lean{c: ui.New(noText{}), o: o}
	l.in.Inside, l.in.Focused = true, true
	l.in.MouseX, l.in.MouseY = 200, 300
	l.st.Reserve(n + n/8)
	l.st.Append(seq(0, n))
	l.c.Reserve(2048)
	return l
}

func (l *lean) height(id uint64) float32 {
	h := float32(18 + mix64(id)%12*14) // 18 to 172 px
	if l.wobble && id%7 == 0 && (l.frameN/3+int(id))%2 == 0 {
		h += 31
	}
	return h
}

func (l *lean) frame() {
	c := l.c
	c.Begin(&l.in, 400, 700, 1, 1.0/60)
	if l.edit != nil {
		l.edit(l.frameN)
	}
	l.v = BeginVList(c, "list", &l.st, l.o)
	for i := l.v.First; i < l.v.Last; i++ {
		id := l.v.Item(i)
		c.NextSize(ui.Px(300), ui.Px(l.height(id)))
		c.BoxKeyed(0, ui.KeyN(c.ParentKey(), 0), nil)
	}
	l.built += l.v.Last - l.v.First
	EndVList(&l.v)
	c.End(&l.dl)
	l.in.ClearFrame()
	l.frameN++
}

// orderHash hashes the whole order the front buffer shows.
func orderHash(st *VListState) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < st.Len(); i++ {
		h = (h ^ st.ID(i)) * 1099511628211
	}
	return h
}

func hashOf(ids []uint64) uint64 {
	h := uint64(14695981039346656037)
	for _, id := range ids {
		h = (h ^ id) * 1099511628211
	}
	return h
}

// A distant move and a mid-list splice on a million items take the sliced
// path: on every frame of the rebuild, every reader (the order, the
// Enumerator, the anchor, the marks) sees the old order or the new one, never
// a mix; edits made meanwhile are queued and land with the swap.
func TestVListSlicedRebuildIsCoherent(t *testing.T) {
	if testing.Short() {
		t.Skip("a million items")
	}
	type edit struct {
		name  string
		apply func(st *VListState)
		ref   func(ids []uint64) []uint64
	}
	for _, e := range []edit{
		{"distant move", func(st *VListState) { st.Move(250_000, 1, 750_000) }, func(ids []uint64) []uint64 {
			r := &refList{ids: ids}
			r.move(250_000, 1, 750_000)
			return r.ids
		}},
		{"mid-list splice", func(st *VListState) { st.Splice(500_000, 40_000, seq(5_000_000, 50_000)) }, func(ids []uint64) []uint64 {
			r := &refList{ids: ids, h: map[uint64]float32{}}
			r.splice(500_000, 40_000, seq(5_000_000, 50_000))
			return r.ids
		}},
	} {
		t.Run(e.name, func(t *testing.T) {
			l := newLean(million, VListOpts{})
			for i := 0; i < 3; i++ {
				l.frame()
			}
			l.st.SetAnchor(l.st.ID(400_000), 0) // the view in the middle, between the edits' ends
			l.frame()
			l.frame()
			old := seq(0, million)
			if orderHash(&l.st) != hashOf(old) {
				t.Fatal("unexpected start order")
			}
			newIDs := e.ref(slices.Clone(old))
			// queued meanwhile: an arrival at the end and a deletion near the top
			final := slices.Concat(newIDs[:10], newIDs[11:], []uint64{9_000_000})
			hOld, hNew, hFinal := hashOf(old), hashOf(newIDs), hashOf(final)
			anchor, _ := l.st.Anchor()
			l.st.SetMark(0, 400_002)

			e.apply(&l.st)
			if !l.st.Pending() {
				t.Fatal("the edit was applied in place: it should be long enough to rebuild")
			}
			frames := 0
			queued := false
			for l.st.Pending() {
				if frames > 2000 {
					t.Fatal("the rebuild does not finish")
				}
				l.frame()
				frames++
				if !queued {
					l.st.Append([]uint64{9_000_000})
					l.st.Splice(10, 1, nil)
					queued = true
				}
				h := orderHash(&l.st)
				if h != hOld && h != hNew && h != hFinal {
					t.Fatalf("frame %d: the readers see an order that is neither the old nor a new one", frames)
				}
				if l.st.Pending() && h != hOld {
					t.Fatalf("frame %d: the order changed while the rebuild is still pending", frames)
				}
				// the Enumerator and the anchor agree with that order
				a, _ := l.st.Anchor()
				i, ok := l.st.IndexOf(a)
				if !ok || l.st.ID(i) != a {
					t.Fatalf("frame %d: the anchor %d is not in the order", frames, a)
				}
				if nx, ok := l.st.Next(a); ok && (i+1 >= l.st.Len() || l.st.ID(i+1) != nx) {
					t.Fatalf("frame %d: Next disagrees with the order", frames)
				}
				if f, _ := l.st.First(); f != l.st.ID(0) {
					t.Fatalf("frame %d: First disagrees with the order", frames)
				}
				if m := l.st.Mark(0); m.Removed {
					t.Fatalf("frame %d: a mark on a surviving item reads removed", frames)
				}
			}
			l.frame()
			if orderHash(&l.st) != hFinal {
				t.Fatal("after the rebuild the order is not the edited one with the queued edits")
			}
			if a, _ := l.st.Anchor(); a != anchor {
				t.Fatalf("the anchor moved from %d to %d", anchor, a)
			}
			t.Logf("rebuilt over %d frames", frames)
			if frames < 3 {
				t.Fatalf("rebuilt in %d frames: not sliced", frames)
			}
		})
	}
}

// editTimes runs edit on a fresh million-item list (whose back buffer exists
// already) and returns the longest frame's edit work: the call itself, then
// each frame's Sync slice.
func editTimes(edit func(st *VListState)) (worst time.Duration, frames int) {
	l := newLean(million, VListOpts{})
	l.frame()
	// warm: allocate the back buffer once with a long edit
	l.st.Move(250_000, 1, 750_000)
	for l.st.Pending() {
		l.frame()
	}
	runtime.GC()
	t0 := time.Now()
	edit(&l.st)
	worst = time.Since(t0)
	for f := uint32(1); l.st.Pending(); f++ {
		t0 = time.Now()
		l.st.Sync(l.c.Frame() + f)
		worst = max(worst, time.Since(t0))
		frames++
	}
	return worst, frames
}

// The edit budget: on a million items, an edit costs at most 2 ms in any one
// frame, whether it is applied in place (moving the last item to the top) or
// rebuilt in slices (a move between the quarters, a splice in the middle).
func TestVListEditFrameBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("a million items")
	}
	const budget = 2 * time.Millisecond
	for _, tc := range []struct {
		name string
		edit func(st *VListState)
	}{
		{"move 999999 to 0", func(st *VListState) { st.Move(999_999, 1, 0) }},
		{"move 250000 to 750000", func(st *VListState) { st.Move(250_000, 1, 750_000) }},
		{"move a run of 100000", func(st *VListState) { st.Move(100_000, 100_000, 600_000) }},
		{"splice in the middle", func(st *VListState) { st.Splice(500_000, 1000, seq(5_000_000, 1000)) }},
		{"short splice near the top", func(st *VListState) { st.Splice(20_000, 5, seq(5_000_000, 3)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The best of three: a frame that a GC or the scheduler took the CPU
			// from is not the edit's.
			var worst time.Duration
			var frames int
			for a := 0; a < 3; a++ {
				w, f := editTimes(tc.edit)
				if a == 0 || w < worst {
					worst, frames = w, f
				}
				if worst <= budget {
					break
				}
			}
			t.Logf("%s: worst frame %.3f ms over %d frames", tc.name, float64(worst)/1e6, frames)
			if worst > budget {
				t.Fatalf("%s: a frame spent %.3f ms on the edit, budget 2 ms", tc.name, float64(worst)/1e6)
			}
		})
	}
}

// Scrolling a million mixed-height items, with items changing height,
// history prepended, messages appended, a short move and a rebuilt long one:
// once warm, the frames allocate nothing (exact ReadMemStats deltas, D11), and
// a frame (build, measure, layout, emit) stays far under the 4 ms budget.
func TestVListFramesAllocateNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("a million items")
	}
	l := newLean(million, VListOpts{StickToBottom: false})
	l.wobble = true
	next := uint64(10_000_000)
	var one [1]uint64
	var three [3]uint64
	l.edit = func(f int) {
		switch f % 120 {
		case 10: // history arrives above
			for i := range three {
				three[i] = next
				next++
			}
			l.st.Prepend(three[:])
		case 40:
			one[0] = next
			next++
			l.st.Append(one[:])
		case 70:
			if n := l.st.Len(); n > 2000 {
				l.st.Move(n/2, 2, n/2+500) // short: in place
			}
		case 100:
			l.st.Move(100_000, 1, 900_000) // long: rebuilt over the next frames
		}
	}
	l.in.MouseX, l.in.MouseY = 200, 300
	scroll := func(f int) {
		// a trackpad flick down, then back up: new items are built every frame
		dy := float32(-37)
		if (f/300)%2 == 1 {
			dy = 41
		}
		l.in.AddScroll(0, dy, true)
	}
	for i := 0; i < 400; i++ {
		scroll(l.frameN)
		l.frame()
	}
	const window = 360
	var mallocs, bytes uint64
	for attempt := 0; attempt < 5; attempt++ {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		t0 := time.Now()
		built0 := l.built
		a0, _ := l.st.Anchor()
		i0, _ := l.st.IndexOf(a0)
		for i := 0; i < window; i++ {
			scroll(l.frameN)
			l.frame()
		}
		el := time.Since(t0)
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		a1, _ := l.st.Anchor()
		if i1, _ := l.st.IndexOf(a1); i1 == i0 {
			t.Fatal("the window did not scroll")
		}
		perFrame := float64(el.Microseconds()) / window
		t.Logf("attempt %d: %d frames, %.1f items built per frame, %.0f µs per frame, %d mallocs, %d bytes",
			attempt+1, window, float64(l.built-built0)/window, perFrame, mallocs, bytes)
		if mallocs == 0 && bytes == 0 {
			if perFrame > 4000 && os.Getenv("CI") == "" {
				t.Fatalf("%.0f µs per frame, budget 4000", perFrame)
			}
			return
		}
	}
	t.Fatalf("the frames allocated in every window (last: %d mallocs, %d bytes)", mallocs, bytes)
}

// The first long edit of a list allocates its back buffer; how long that
// takes is reported, not gated (the operating system zeroes the pages).
func TestVListFirstRebuildCost(t *testing.T) {
	if testing.Short() {
		t.Skip("a million items")
	}
	var st VListState
	st.Reserve(million)
	st.Append(seq(0, million))
	t0 := time.Now()
	st.Move(250_000, 1, 750_000)
	t.Logf("the first long edit, allocating the back buffer: %.3f ms", float64(time.Since(t0))/1e6)
}

// BenchmarkVListFrame1M is one frame of a million-item list scrolling.
func BenchmarkVListFrame1M(b *testing.B) {
	l := newLean(million, VListOpts{})
	l.wobble = true
	for i := 0; i < 60; i++ {
		l.in.AddScroll(0, -37, true)
		l.frame()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.in.AddScroll(0, -37, true)
		l.frame()
	}
}

// BenchmarkVListMoveLastToTop1M moves the last of a million items to the top,
// one move per frame: O(1) in place, until the spare slots at the front run
// out and a rebuild recentres the list (amortised in).
func BenchmarkVListMoveLastToTop1M(b *testing.B) {
	var st VListState
	st.Reserve(million)
	st.Append(seq(0, million))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.Move(million-1, 1, 0)
		st.Sync(uint32(i))
	}
}

// BenchmarkVListDirectEdit measures what one slot of an in-place edit costs
// in the middle of a million items with random ids (the id table's probes are
// cache misses), over distances up to and past vlDirectMax: what sizes it.
func BenchmarkVListDirectEdit(b *testing.B) {
	ids := seq(0, million)
	for i := range ids {
		ids[i] = mix64(uint64(i) + 1)
	}
	for _, dist := range []int{vlDirectMax / 4, vlDirectMax, 4 * vlDirectMax} {
		b.Run(fmt.Sprintf("%dslots", dist), func(b *testing.B) {
			defer func(d int) { vlDirect = d }(vlDirect)
			vlDirect = 1 << 30
			var st VListState
			st.Reserve(million)
			st.Append(ids)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				at := 300_000 + i*7919%100_000
				if i%2 == 0 {
					st.Move(at, 1, at+dist-1)
				} else {
					st.Move(at+dist-1, 1, at)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(dist), "ns/slot")
		})
	}
}

// BenchmarkVListRebuild1M is a whole sliced rebuild of a distant move on a
// million items: the total CPU and the frames it is spread over.
func BenchmarkVListRebuild1M(b *testing.B) {
	var st VListState
	st.Reserve(million)
	st.Append(seq(0, million))
	st.Move(250_000, 1, 750_000)
	st.finishNow()
	b.ResetTimer()
	frames := 0
	for i := 0; i < b.N; i++ {
		st.Move(250_000, 1, 750_000)
		for f := uint32(0); st.Pending(); f++ {
			st.Sync(uint32(i)<<16 | f)
			frames++
		}
	}
	b.ReportMetric(float64(frames)/float64(b.N), "frames/op")
}
