package core

import (
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func TestArena(t *testing.T) {
	var a Arena
	s1 := a.AppendString("hello")
	s2 := a.AppendBytes([]byte(" world"))
	if got := string(a.Bytes(s1)) + a.String(s2); got != "hello world" {
		t.Fatalf("got %q", got)
	}
	// Spans survive growth.
	for i := 0; i < 10000; i++ {
		a.AppendByte('x')
	}
	if a.String(s1) != "hello" || a.String(s2) != " world" {
		t.Fatal("spans lost after growth")
	}
	b := a.Alloc(16)
	for _, c := range b {
		if c != 0 {
			t.Fatal("Alloc not zeroed")
		}
	}
	a.Reset()
	if a.Len() != 0 || a.Cap() == 0 {
		t.Fatalf("Reset: len %d cap %d", a.Len(), a.Cap())
	}
	// Reset keeps capacity, and Alloc clears reused memory.
	a.AppendString("dirty dirty dirty")
	a.Reset()
	for _, c := range a.Alloc(8) {
		if c != 0 {
			t.Fatal("Alloc after Reset not zeroed")
		}
	}
	// number formatting
	a.Reset()
	a.AppendInt(-42)
	a.AppendByte(' ')
	a.AppendUint(7)
	a.AppendByte(' ')
	a.AppendFloat(3.14159, 2)
	if got := string(a.Tail(0)); got != "-42 7 3.14" {
		t.Fatalf("got %q", got)
	}
}

func TestCarve(t *testing.T) {
	type rec struct {
		A uint8
		B float64
		C [3]uint16
	}
	if !PointerFree[rec]() || PointerFree[struct{ p *int }]() || PointerFree[[]int]() || PointerFree[string]() {
		t.Fatal("PointerFree misjudges")
	}
	var a Arena
	a.AppendByte(1) // misalign the tail
	rs := Carve[rec](&a, 5)
	if len(rs) != 5 {
		t.Fatal(len(rs))
	}
	for i := range rs {
		rs[i].B = float64(i)
	}
	if rs[4].B != 4 {
		t.Fatal("carved slice not usable")
	}
	u := Carve[uint64](&a, 3)
	if len(u) != 3 || uintptr(unsafe.Pointer(&u[0]))%8 != 0 {
		t.Fatal("misaligned or short carve")
	}
}

func TestFrame(t *testing.T) {
	var f Frame
	f.Swap()
	s := f.Cur().AppendString("frame 1")
	f.Swap()
	if got := f.Prev().String(s); got != "frame 1" {
		t.Fatalf("previous frame data lost: %q", got)
	}
	f.Cur().AppendString("frame 2")
	f.Swap()
	if f.Cur().Len() != 0 {
		t.Fatal("current arena not reset")
	}
}

// A peak frame grows only the arena of its turn; the next frame gives the
// other one the same room, so the same peak on the other's turn does not
// allocate, and repeated peaks do not grow either arena further.
func TestFramePeakOnEitherTurn(t *testing.T) {
	var f Frame
	peak := make([]byte, 5000)
	f.Swap()
	f.Cur().AppendBytes(peak) // grows this frame's arena
	f.Swap()                  // a small frame on the other's turn
	f.Swap()
	f.Swap() // the peak again, on the other arena's turn
	before := unsafe.SliceData(f.Cur().buf)
	f.Cur().AppendBytes(peak)
	if unsafe.SliceData(f.Cur().buf) != before {
		t.Error("the peak on the other arena's turn grew its arena")
	}
	c0, c1 := cap(f.a[0].buf), cap(f.a[1].buf)
	for range 10 {
		f.Swap()
		f.Cur().AppendBytes(peak)
	}
	if cap(f.a[0].buf) != c0 || cap(f.a[1].buf) != c1 || c0 != c1 {
		t.Errorf("capacities %d and %d, then %d and %d after more peaks", c0, c1, cap(f.a[0].buf), cap(f.a[1].buf))
	}
}

func TestArenaNoAlloc(t *testing.T) {
	var a Arena
	a.Reserve(4096)
	n := testing.AllocsPerRun(100, func() {
		a.Reset()
		a.AppendString("abc")
		a.AppendInt(123456789)
		a.AppendFloat(1.5, 3)
		a.Alloc(100)
	})
	if n != 0 {
		t.Fatalf("%v allocs", n)
	}
}

func TestTable(t *testing.T) {
	tb := NewTable(4)
	ref := map[uint64]uint32{}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200000; i++ {
		k := uint64(rng.Intn(3000)) // small key space: lots of collisions and churn
		if k%7 == 0 {
			k = 0 // key 0 is valid
		}
		switch rng.Intn(3) {
		case 0, 1:
			v := rng.Uint32()
			tb.Put(k, v)
			ref[k] = v
		default:
			if got, want := tb.Delete(k), refHas(ref, k); got != want {
				t.Fatalf("Delete(%d) = %v, want %v", k, got, want)
			}
			delete(ref, k)
		}
		if tb.Len() != len(ref) {
			t.Fatalf("Len %d want %d", tb.Len(), len(ref))
		}
	}
	for k, v := range ref {
		if got, ok := tb.Get(k); !ok || got != v {
			t.Fatalf("Get(%d) = %d,%v want %d", k, got, ok, v)
		}
	}
	for k := uint64(0); k < 3000; k++ {
		if _, ok := tb.Get(k); ok != refHas(ref, k) {
			t.Fatalf("Get(%d) presence %v", k, ok)
		}
	}
}

func refHas(m map[uint64]uint32, k uint64) bool { _, ok := m[k]; return ok }

func TestTableClusteredKeys(t *testing.T) {
	// Sequential and strided keys stress the probe-run shifting.
	tb := NewTable(16)
	for i := uint64(0); i < 5000; i++ {
		tb.Put(i*1024, uint32(i))
	}
	for i := uint64(0); i < 5000; i += 2 {
		if !tb.Delete(i * 1024) {
			t.Fatal("missing", i)
		}
	}
	for i := uint64(0); i < 5000; i++ {
		v, ok := tb.Get(i * 1024)
		if ok != (i%2 == 1) || (ok && v != uint32(i)) {
			t.Fatalf("key %d: %d %v", i, v, ok)
		}
	}
}

func TestTableNoGrowthNoAlloc(t *testing.T) {
	tb := NewTable(1000)
	for i := uint64(0); i < 1000; i++ {
		if tb.Put(i, 1) {
			t.Fatalf("pre-sized table grew at %d", i)
		}
	}
	// Churn at the high-water mark never allocates or grows.
	n := testing.AllocsPerRun(50, func() {
		for i := uint64(0); i < 1000; i++ {
			tb.Delete(i)
		}
		for i := uint64(0); i < 1000; i++ {
			if tb.Put(i+5000, 2) {
				t.Fatal("grew")
			}
		}
		for i := uint64(0); i < 1000; i++ {
			tb.Delete(i + 5000)
		}
		for i := uint64(0); i < 1000; i++ {
			tb.Put(i, 1)
		}
	})
	if n != 0 {
		t.Fatalf("%v allocs", n)
	}
	// Growth is reported.
	small := NewTable(2)
	grew := false
	for i := uint64(0); i < 100; i++ {
		grew = small.Put(i, 0) || grew
	}
	if !grew || small.Len() != 100 {
		t.Fatalf("grew=%v len=%d", grew, small.Len())
	}
}

func TestPool(t *testing.T) {
	p := NewPool(2)
	a, b := p.Alloc(), p.Alloc()
	if a.Index() == b.Index() || !p.Valid(a) || !p.Valid(b) || p.Len() != 2 {
		t.Fatal("alloc")
	}
	if p.Valid(0) {
		t.Fatal("zero handle valid")
	}
	if !p.Free(a) || p.Free(a) {
		t.Fatal("double free accepted")
	}
	if p.Valid(a) {
		t.Fatal("stale handle valid")
	}
	c := p.Alloc() // reuses a's slot with a new generation
	if c.Index() != a.Index() || c.Gen() == a.Gen() || p.Valid(a) || !p.Valid(c) {
		t.Fatalf("reuse: a=%x c=%x", a, c)
	}
	if p.Len() != 2 || p.Slots() != 2 {
		t.Fatal(p.Len(), p.Slots())
	}
	if !p.FreeIndex(b.Index()) || p.Live(b.Index()) || p.Handle(b.Index()) != 0 {
		t.Fatal("FreeIndex")
	}
	if p.Handle(c.Index()) != c {
		t.Fatal("Handle")
	}
}

func TestPoolChurnNoAlloc(t *testing.T) {
	p := NewPool(8)
	hs := make([]Handle, 0, 100)
	for i := 0; i < 100; i++ {
		hs = append(hs, p.Alloc())
	}
	n := testing.AllocsPerRun(100, func() {
		for _, h := range hs {
			p.Free(h)
		}
		for i := range hs {
			hs[i] = p.Alloc()
		}
	})
	if n != 0 {
		t.Fatalf("%v allocs", n)
	}
}

type msg struct {
	Producer uint16
	Seq      uint32
}

func TestRingBasics(t *testing.T) {
	r := NewRing[msg](3)
	if r.Cap() != 4 {
		t.Fatal(r.Cap())
	}
	if _, ok := r.Pop(); ok {
		t.Fatal("pop from empty")
	}
	for i := 0; i < 4; i++ {
		if !r.TryPush(msg{Seq: uint32(i)}) {
			t.Fatal("push", i)
		}
	}
	if r.TryPush(msg{}) {
		t.Fatal("push into full ring succeeded")
	}
	if r.Len() != 4 {
		t.Fatal(r.Len())
	}
	for i := 0; i < 4; i++ {
		m, ok := r.Pop()
		if !ok || m.Seq != uint32(i) {
			t.Fatalf("pop %d: %+v %v", i, m, ok)
		}
	}
	// Wrap around many times.
	for i := 0; i < 100; i++ {
		if !r.TryPush(msg{Seq: uint32(i)}) {
			t.Fatal()
		}
		if m, ok := r.Pop(); !ok || m.Seq != uint32(i) {
			t.Fatal()
		}
	}
	if n := testing.AllocsPerRun(100, func() { r.TryPush(msg{}); r.Pop() }); n != 0 {
		t.Fatalf("%v allocs", n)
	}
}

// Many producers, one consumer: every message arrives exactly once and each
// producer's messages arrive in order.
func TestRingStress(t *testing.T) {
	const producers, perProducer = 8, 20000
	r := NewRing[msg](64)
	var wg sync.WaitGroup
	var full atomic.Int64
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for s := 0; s < perProducer; s++ {
				for !r.TryPush(msg{Producer: uint16(p), Seq: uint32(s)}) {
					full.Add(1)
					runtime.Gosched()
				}
			}
		}(p)
	}
	next := make([]uint32, producers)
	got := 0
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	for got < producers*perProducer {
		m, ok := r.Pop()
		if !ok {
			runtime.Gosched()
			continue
		}
		if m.Seq != next[m.Producer] {
			t.Fatalf("producer %d: got seq %d, want %d", m.Producer, m.Seq, next[m.Producer])
		}
		next[m.Producer]++
		got++
	}
	<-done
	if _, ok := r.Pop(); ok {
		t.Fatal("extra message")
	}
	t.Logf("%d messages, producers hit a full ring %d times", got, full.Load())
}

func TestPoolReserve(t *testing.T) {
	p := NewPool(4)
	var hs []Handle
	for i := 0; i < 3; i++ {
		hs = append(hs, p.Alloc())
	}
	p.Reserve(5000)
	for i, h := range hs { // existing slots survive
		if !p.Valid(h) || p.Handle(h.Index()) != h || int(h.Index()) != i {
			t.Fatalf("slot %d lost by Reserve", i)
		}
	}
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	for i := 0; i < 4000; i++ {
		p.Alloc()
	}
	for i := uint32(0); i < 4003; i++ {
		p.FreeIndex(i)
	}
	runtime.ReadMemStats(&m1)
	if n := m1.Mallocs - m0.Mallocs; n != 0 {
		t.Errorf("%d allocations growing to 4003 slots after Reserve(5000)", n)
	}
}
