//go:build darwin

package objc

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/ffi"
)

// The feasibility tests of the per-request blocks (D36): real _Block_copy
// and Block_release, calls through heap copies, from the main thread and
// from GCD threads.

// Test kinds: void (^)(NSInteger). run adds the argument to the context's
// counter; a context with a hold blocks inside the handler until released.
var (
	kindsOnce     sync.Once
	onceKind      *BlockKind
	multiKind     *BlockKind
	ranBy         [64]atomic.Int64 // per context: sum of arguments seen by run
	staleArgs     atomic.Int64     // sum of arguments seen by stale
	holdCtx       atomic.Int64     // context whose run blocks (0: none)
	holdEntered   = make(chan struct{}, 1)
	holdRelease   = make(chan struct{})
	callFromGCD   uintptr // dispatch callback: calls the block in ctx with argument 1
	frameSeenLock sync.Mutex
)

func testKinds() {
	kindsOnce.Do(func() {
		run := func(ctx uintptr, c *ffi.CB) {
			if int64(ctx) == holdCtx.Load() {
				holdEntered <- struct{}{}
				<-holdRelease
			}
			ranBy[ctx].Add(int64(c.Int(1)))
		}
		stale := func(c *ffi.CB) { staleArgs.Add(int64(c.Int(1))) }
		onceKind = NewBlockKind("v16@?0q8", true, run, stale)
		multiKind = NewBlockKind("v16@?0q8", false, run, stale)
		callFromGCD = ffi.NewCallback(func(c *ffi.CB) { CallBlock(c.Frame(), ID(c.Int(0)), 1) })
	})
}

func resetRan() {
	for i := range ranBy {
		ranBy[i].Store(0)
	}
	staleArgs.Store(0)
}

func blockCopyOf(b ID) ID { return ID(F.Call1(blockCopy, uintptr(b))) }
func blockReleaseOf(b ID) { F.Call1(blockRelease, uintptr(b)) }

func lit(b ID) *blockLiteral { return (*blockLiteral)(ffi.PtrFromC(uintptr(b))) }

// The flags: _Block_copy of a global block returns the block itself; of a
// per-request literal, a heap copy with the captures intact.
func TestBlockFlags(t *testing.T) {
	testKinds()
	g := NewGlobalBlock("v8@?0", func(*ffi.CB) {})
	if c := blockCopyOf(g); c != g {
		t.Errorf("_Block_copy of a global block returned %#x, want the block itself %#x", c, g)
	}
	blockReleaseOf(g) // a no-op for a global block

	r, err := onceKind.New(5)
	if err != nil {
		t.Fatal(err)
	}
	c := blockCopyOf(r.Block())
	r.Sent()
	if c == r.Block() {
		t.Fatal("_Block_copy of a per-request literal returned the literal")
	}
	if l := lit(c); l.slot != uintptr(r.slot) || l.gen != uintptr(r.gen) || l.invoke != onceKind.invoke {
		t.Errorf("the copy captured slot %d gen %d invoke %#x, want %d, %d, %#x", l.slot, l.gen, l.invoke, r.slot, r.gen, onceKind.invoke)
	}
	if l := lit(c); l.isa == stackBlockISA || l.isa == globalBlockISA {
		t.Errorf("the copy's isa is %#x, want the malloc-block class", l.isa)
	}
	resetRan()
	CallBlock(F, c, 3)
	if ranBy[5].Load() != 3 {
		t.Errorf("the copy's call ran with %d, want 3", ranBy[5].Load())
	}
	blockReleaseOf(c)
}

// A one-shot request runs once; the second call is stale. Its slot is freed
// and the live count returns to where it was.
func TestOneShotRequestRunsOnce(t *testing.T) {
	testKinds()
	base := Leaks()
	resetRan()
	r, _ := onceKind.New(7)
	c := blockCopyOf(r.Block())
	r.Sent()
	if !r.Live() {
		t.Fatal("the request is not live before its call")
	}
	CallBlock(F, c, 10)
	CallBlock(F, c, 20) // a duplicate: stale
	blockReleaseOf(c)
	if ranBy[7].Load() != 10 || staleArgs.Load() != 20 {
		t.Errorf("run saw %d, stale saw %d; want 10 and 20", ranBy[7].Load(), staleArgs.Load())
	}
	if r.Live() || r.Cancel() {
		t.Error("a completed request is still live or cancellable")
	}
	if got := Leaks(); got.Blocks != base.Blocks || got.StaleCalls != base.StaleCalls+1 {
		t.Errorf("leaks %+v, want %d live blocks and %d stale calls", got, base.Blocks, base.StaleCalls+1)
	}
}

// Late callbacks: a request is cancelled and its slot reused; a call through
// the old request's heap copy is stale and does not run the new request.
func TestLateCallAfterSlotReuse(t *testing.T) {
	testKinds()
	resetRan()
	old, _ := onceKind.New(1)
	oldCopy := blockCopyOf(old.Block())
	old.Sent()
	if !old.Cancel() {
		t.Fatal("Cancel of a live request reported false")
	}
	next, _ := onceKind.New(2)
	if next.slot != old.slot || next.gen == old.gen {
		t.Fatalf("the new request has slot %d gen %d; the test needs the old slot %d with a new generation", next.slot, next.gen, old.slot)
	}
	nextCopy := blockCopyOf(next.Block())
	next.Sent()

	CallBlock(F, oldCopy, 100)
	if ranBy[2].Load() != 0 || ranBy[1].Load() != 0 || staleArgs.Load() != 100 {
		t.Fatalf("the late call ran a handler (old %d, new %d) or missed stale (%d)", ranBy[1].Load(), ranBy[2].Load(), staleArgs.Load())
	}
	CallBlock(F, nextCopy, 5)
	if ranBy[2].Load() != 5 {
		t.Errorf("the new request's call ran with %d, want 5", ranBy[2].Load())
	}
	blockReleaseOf(oldCopy)
	blockReleaseOf(nextCopy)
}

// The literal itself is poisoned once the passing call returned: an API that
// kept it without copying would get a stale call, not someone else's.
func TestLiteralIsPoisonedAfterSent(t *testing.T) {
	testKinds()
	resetRan()
	r, _ := multiKind.New(3)
	b := r.Block()
	CallBlock(F, b, 1) // during the passing call: valid
	r.Sent()
	CallBlock(F, b, 2) // after it: stale
	if ranBy[3].Load() != 1 || staleArgs.Load() != 2 {
		t.Errorf("run saw %d, stale saw %d; want 1 and 2", ranBy[3].Load(), staleArgs.Load())
	}
	r.Cancel()
}

// A callback in flight during cancellation: a GCD thread claims the slot and
// blocks in its handler; cancelling returns at once; no new request gets the
// slot until the callback returns; a stale call meanwhile runs no handler;
// then the slot is reused with a new generation.
func TestCallbackInFlightDuringCancel(t *testing.T) {
	testKinds()
	resetRan()
	base := Leaks()
	r, _ := multiKind.New(9)
	c := blockCopyOf(r.Block())
	r.Sent()
	holdCtx.Store(9)
	defer holdCtx.Store(0)
	F.Call3(fnAsyncF, globalQueue(F, 2), uintptr(c), callFromGCD)
	select {
	case <-holdEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the GCD callback never entered its handler")
	}

	t0 := time.Now()
	if !r.Cancel() {
		t.Fatal("Cancel reported the request was not live")
	}
	if d := time.Since(t0); d > 50*time.Millisecond {
		t.Errorf("Cancel took %v with a callback running", d)
	}
	// Every other slot can be taken, but not the one in use.
	var taken []Request
	for {
		n, err := onceKind.New(10)
		if errors.Is(err, ErrBlocksFull) {
			break
		}
		if n.slot == r.slot {
			t.Fatalf("slot %d was handed out while a callback still ran on it", r.slot)
		}
		taken = append(taken, n)
	}
	if len(taken) != capacity(base)-1 {
		t.Errorf("took %d slots, want %d", len(taken), capacity(base)-1)
	}
	CallBlock(F, c, 50) // stale: retiring
	if staleArgs.Load() != 50 || ranBy[10].Load() != 0 {
		t.Errorf("the stale call: stale saw %d (want 50), a new request ran %d", staleArgs.Load(), ranBy[10].Load())
	}
	for _, n := range taken {
		n.Cancel()
		n.Sent()
	}

	holdRelease <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for Leaks().Blocks != base.Blocks {
		if time.Now().After(deadline) {
			t.Fatalf("the slot was not freed after the callback returned: %+v", Leaks())
		}
		runtime.Gosched()
	}
	if ranBy[9].Load() != 1 {
		t.Errorf("the blocked callback ran with %d, want 1", ranBy[9].Load())
	}
	again, _ := onceKind.New(11)
	defer func() { again.Cancel(); again.Sent() }()
	if again.slot != r.slot || again.gen != r.gen+1 {
		t.Errorf("after the callback: slot %d gen %d, want slot %d gen %d", again.slot, again.gen, r.slot, r.gen+1)
	}
	blockReleaseOf(c)
}

// capacity is the number of slots free at base.
func capacity(base Counts) int { return MaxBlocks - int(base.Blocks) - int(base.RetiredSlots) }

// setNextGeneration is the test hook for the generation rule: it sets the
// generation of the slot New hands out next (the head of the free list) and
// returns the slot.
func setNextGeneration(gen uint32) uint32 {
	i := uint32(freeHead.Load()) - 1
	slots[i].word.Store(uint64(gen)<<genShift | retiring)
	return i
}

// unretire is the test hook that puts a slot retired by a wrap back into the
// table, with generation 1.
func unretire(i uint32) {
	slots[i].word.Store(1<<genShift | retiring)
	retiredSlots.Add(-1)
	push(i)
}

// The generation never repeats: near the limit a slot issues 2^32-2 and
// 2^32-1, then is retired for good instead of issuing 0 or wrapping to 1,
// so old copies stay stale; a zero Request touches nothing.
func TestGenerationWrapRetiresTheSlot(t *testing.T) {
	testKinds()
	resetRan()
	base := Leaks()
	slot := setNextGeneration(0xfffffffe)
	defer unretire(slot) // the other tests need the whole table

	a, _ := onceKind.New(20)
	ca := blockCopyOf(a.Block())
	a.Sent()
	a.Cancel() // generation 2^32-1
	b, _ := onceKind.New(21)
	cb := blockCopyOf(b.Block())
	b.Sent()
	if a.slot != slot || b.slot != slot || a.gen != 0xfffffffe || b.gen != 0xffffffff {
		t.Fatalf("requests got slot %d gen %#x and slot %d gen %#x, want slot %d with the last two generations", a.slot, a.gen, b.slot, b.gen, slot)
	}
	CallBlock(F, cb, 1) // completes b: the generation wraps
	if ranBy[21].Load() != 1 {
		t.Fatalf("the last generation's request did not run")
	}
	if got := Leaks(); got.RetiredSlots != base.RetiredSlots+1 || got.Blocks != base.Blocks {
		t.Fatalf("after the wrap: %+v, want one more retired slot and %d live", got, base.Blocks)
	}
	var later []Request
	for range 4 {
		r, _ := onceKind.New(22)
		if r.slot == slot || r.gen == 0 {
			t.Fatalf("a retired slot was handed out again (slot %d gen %d)", r.slot, r.gen)
		}
		later = append(later, r)
	}
	staleBefore := staleArgs.Load()
	CallBlock(F, ca, 100)
	CallBlock(F, cb, 100)
	if staleArgs.Load()-staleBefore != 200 || ranBy[22].Load() != 0 {
		t.Errorf("old copies after the wrap: stale saw %d (want 200), a later request ran %d", staleArgs.Load()-staleBefore, ranBy[22].Load())
	}
	var zero Request
	if zero.Live() || zero.Cancel() || zero.Block() != 0 {
		t.Error("the zero Request acts on a slot")
	}
	zero.Sent()
	for _, r := range later {
		if !r.Live() {
			t.Error("a zero Request's methods ended a live request")
		}
		r.Sent()
		r.Cancel()
	}
	blockReleaseOf(ca)
	blockReleaseOf(cb)
}

// A full table refuses at once and counts the refusal.
func TestFullTableRefuses(t *testing.T) {
	testKinds()
	base := Leaks()
	var taken []Request
	for i := 0; i < capacity(base); i++ {
		r, err := onceKind.New(0)
		if err != nil {
			t.Fatalf("request %d refused: %v", i, err)
		}
		taken = append(taken, r)
	}
	if _, err := onceKind.New(0); !errors.Is(err, ErrBlocksFull) {
		t.Errorf("a request beyond the table returned %v", err)
	}
	if Leaks().Refused != base.Refused+1 {
		t.Error("the refusal was not counted")
	}
	for _, r := range taken {
		r.Sent()
		r.Cancel()
	}
	if Leaks().Blocks != base.Blocks {
		t.Errorf("%d blocks live after cancelling all, want %d", Leaks().Blocks, base.Blocks)
	}
}

// Blocks given to us: KeepBlock copies before storing (the original is a
// stack block that dies when the passing call returns), CallKeptBlock calls
// once and releases. The given block has copy and dispose helpers, so the
// release is observable.
var (
	givenOnce               sync.Once
	givenInvoke             uintptr
	givenCopies, givenFrees atomic.Int64
	givenSum                atomic.Int64
	givenDesc               *givenDescriptor
)

type givenDescriptor struct {
	reserved, size uintptr
	copy, dispose  uintptr
	signature, gcl uintptr
	sig            [8]byte
}

func TestKeepBlockCopiesAndReleases(t *testing.T) {
	givenOnce.Do(func() {
		givenInvoke = ffi.NewCallback(func(c *ffi.CB) { givenSum.Add(int64(lit(ID(c.Int(0))).slot + c.Int(1))) })
		givenDesc = &givenDescriptor{size: unsafe.Sizeof(blockLiteral{}), sig: [8]byte{'v', '1', '6', '@', '?', '0', 'q', '8'}}
		givenDesc.copy = ffi.NewCallback(func(*ffi.CB) { givenCopies.Add(1) })
		givenDesc.dispose = ffi.NewCallback(func(*ffi.CB) { givenFrees.Add(1) })
		givenDesc.signature = uintptr(unsafe.Pointer(&givenDesc.sig[0]))
	})
	// A stand-in for a stack block an API hands us; its first capture (slot)
	// is added to the argument.
	given := func(add uintptr) *blockLiteral {
		return &blockLiteral{
			isa: stackBlockISA, flags: 1<<25 | blockHasSignature, // BLOCK_HAS_COPY_DISPOSE
			invoke: givenInvoke, desc: uintptr(unsafe.Pointer(givenDesc)), slot: add,
		}
	}
	base := Leaks()
	c0, f0 := givenCopies.Load(), givenFrees.Load()
	stack := given(1000)
	var k KeptBlock
	if !KeepBlock(F, &k, ID(unsafe.Pointer(stack))) || !k.Held() {
		t.Fatal("KeepBlock kept nothing")
	}
	*stack = blockLiteral{} // the passing call returned: the stack block is gone
	other := given(2000)
	if KeepBlock(F, &k, ID(unsafe.Pointer(other))) {
		t.Error("KeepBlock replaced a block already kept")
	}
	if KeepBlock(F, new(KeptBlock), 0) {
		t.Error("KeepBlock kept a nil block")
	}
	if c, f := givenCopies.Load()-c0, givenFrees.Load()-f0; c != 2 || f != 1 || Leaks().KeptBlocks != base.KeptBlocks+1 {
		t.Errorf("copy helper ran %d times, dispose %d, kept count %d; want 2, 1 (the refused copy), %d", c, f, Leaks().KeptBlocks, base.KeptBlocks+1)
	}
	givenSum.Store(0)
	if !CallKeptBlock(F, &k, 7) || CallKeptBlock(F, &k, 7) {
		t.Error("CallKeptBlock did not call exactly once")
	}
	if givenSum.Load() != 1007 {
		t.Errorf("the kept block saw %d, want 1007 (its captures survived the copy)", givenSum.Load())
	}
	if f := givenFrees.Load() - f0; f != 2 || Leaks().KeptBlocks != base.KeptBlocks || k.Held() {
		t.Errorf("dispose ran %d times, kept count %d, held %v; want 2, %d, false", f, Leaks().KeptBlocks, k.Held(), base.KeptBlocks)
	}
	runtime.KeepAlive(stack)
	runtime.KeepAlive(other)
}

// Callback progress without a pool: 64 requests called at once from GCD
// threads on many queues, each handler calling the next block from inside
// (three levels deep, through its own frame), mixing one-shot completions
// and multi-shot observations. Every call is delivered and none waits for a
// frame: each nested level has a frame of its own.
var (
	progressOnce  sync.Once
	progressKinds [2]*BlockKind
	progressChain [64][4]ID // per chain: the copies of its four levels
	progressRan   [64][4]atomic.Int32
	progressFrame [64][4]uintptr
	progressStart uintptr
)

func TestCallbackProgress(t *testing.T) {
	progressOnce.Do(func() {
		run := func(ctx uintptr, c *ffi.CB) {
			chain, level := ctx>>2, ctx&3
			f := c.Frame()
			progressFrame[chain][level] = uintptr(unsafe.Pointer(f))
			progressRan[chain][level].Add(1)
			if level < 3 {
				CallBlock(f, progressChain[chain][level+1], 0)
			}
		}
		progressKinds[0] = NewBlockKind("v16@?0q8", true, run, nil)  // mandatory completion
		progressKinds[1] = NewBlockKind("v16@?0q8", false, run, nil) // observation
		progressStart = ffi.NewCallback(func(c *ffi.CB) { CallBlock(c.Frame(), progressChain[c.Int(0)][0], 0) })
	})
	base := Leaks()
	var reqs []Request
	defer func() {
		for _, r := range reqs {
			r.Cancel()
		}
	}()
	for chain := range progressChain {
		for level := range progressChain[chain] {
			r, err := progressKinds[(chain+level)%2].New(uintptr(chain<<2 | level))
			if err != nil {
				t.Fatal(err)
			}
			progressChain[chain][level] = blockCopyOf(r.Block())
			progressRan[chain][level].Store(0)
			r.Sent()
			reqs = append(reqs, r)
		}
	}
	g := F.Call0(fnGroupCreate)
	for chain := range progressChain {
		F.Call4(fnGroupAsyncF, g, globalQueue(F, chain), uintptr(chain), progressStart)
	}
	if F.Call2(fnGroupWait, g, F.Call2(fnDispatchTime, 0, 10e9)) != 0 { // DISPATCH_TIME_NOW + 10 s
		t.Fatal("the callbacks did not finish within 10 s")
	}
	F.Call1(fnDispatchRelease, g)
	for chain := range progressChain {
		for level := range progressChain[chain] {
			if n := progressRan[chain][level].Load(); n != 1 {
				t.Fatalf("chain %d level %d ran %d times, want 1", chain, level, n)
			}
			for other := 0; other < level; other++ {
				if progressFrame[chain][other] == progressFrame[chain][level] {
					t.Fatalf("chain %d: levels %d and %d shared a frame", chain, other, level)
				}
			}
		}
	}
	for chain := range progressChain {
		for level := range progressChain[chain] {
			blockReleaseOf(progressChain[chain][level])
		}
	}
	for _, r := range reqs {
		r.Cancel()
	}
	reqs = nil
	if got := Leaks(); got.Blocks != base.Blocks {
		t.Errorf("%d blocks live after the run, want %d", got.Blocks, base.Blocks)
	}
}

// Steady-state block requests allocate nothing: New, the call through the
// heap copy, the claim and release, Sent and Cancel.
func TestBlockRequestsAllocateNothing(t *testing.T) {
	testKinds()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cycle := func() {
		r, _ := onceKind.New(4)
		c := blockCopyOf(r.Block())
		r.Sent()
		CallBlock(F, c, 1)
		blockReleaseOf(c)
		m, _ := multiKind.New(4)
		CallBlock(F, m.Block(), 1)
		m.Sent()
		m.Cancel()
	}
	cycle()
	var per float64
	for round := 0; round < 3; round++ { // process-wide counters: one clean round passes
		if per = testing.AllocsPerRun(2000, cycle); per == 0 {
			return
		}
	}
	t.Errorf("a block request allocated %v objects", per)
}

func BenchmarkBlockRequest(b *testing.B) {
	testKinds()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r, _ := onceKind.New(4)
		c := blockCopyOf(r.Block())
		r.Sent()
		CallBlock(F, c, 1)
		blockReleaseOf(c)
	}
}
