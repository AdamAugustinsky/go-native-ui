//go:build darwin

package objc

import (
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// Blocks (the Blocks ABI, as clang lays them out).
//
// Blocks we make come in two kinds:
//   - per-request blocks (completion handlers): stack-class literals with
//     two captured integers, a slot of a fixed table and the slot's
//     generation. An API that keeps one copies it (_Block_copy makes a heap
//     copy with its own captures, freed by Block_release), so a slot is never
//     recycled under a caller; see BlockKind and Request.
//   - process-lifetime blocks with no per-request state: global blocks
//     (NewGlobalBlock).
//
// Blocks we are given are called with CallBlock while the call that passed
// them runs, or kept past it with KeepBlock and called once with
// CallKeptBlock.

// Block flags.
const (
	blockIsGlobal     = 1 << 28
	blockHasSignature = 1 << 30
)

// blockLiteral is a block with two captured words.
type blockLiteral struct {
	isa      uintptr
	flags    int32
	reserved int32
	invoke   uintptr
	desc     uintptr // *blockDescriptor
	slot     uintptr // captures
	gen      uintptr
}

// blockDescriptor is Block_descriptor_1 followed by Block_descriptor_3 (no
// copy or dispose helpers: the captures are plain integers).
type blockDescriptor struct {
	reserved, size    uintptr
	signature, layout uintptr
}

var (
	blockCopy, blockRelease       uintptr
	stackBlockISA, globalBlockISA uintptr
)

// Malloc and Free are libc's malloc and free, resolved when the package
// initialises (before any goroutine of the program runs), for packages that
// keep C memory and must not look symbols up later: a dlsym waits for dyld's
// lock, which a thread loading a framework holds.
var Malloc, Free uintptr

func initBlocks() {
	h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}
	sym := func(n string) uintptr {
		p, err := purego.Dlsym(h, n)
		if err != nil {
			panic(err)
		}
		return p
	}
	blockCopy, blockRelease = sym("_Block_copy"), sym("_Block_release")
	stackBlockISA, globalBlockISA = sym("_NSConcreteStackBlock"), sym("_NSConcreteGlobalBlock")
	Malloc, Free = sym("malloc"), sym("free")
	for i := MaxBlocks - 1; i >= 0; i-- {
		slots[i].word.Store(1<<genShift | retiring) // free; the first request gets generation 1
		push(uint32(i))
	}
}

// ---- Per-request blocks ----

// MaxBlocks is the number of per-request blocks that can be outstanding at
// once, process-wide. When all are taken, BlockKind.New fails at once; the
// table never grows (it loses a slot only when a slot's generation wraps,
// after 2^32 requests on it).
const MaxBlocks = 256

// ErrBlocksFull is returned by BlockKind.New when every slot is taken.
var ErrBlocksFull = errors.New("objc: all per-request block slots are in use")

// A slot's word: the generation (high 32 bits), a retiring bit and the
// number of active claims (low 16 bits). The request that made the slot
// holds one claim until Sent; each callback running on it holds one.
//
// The generation rule: a generation is never issued twice by one slot, so
// a heap copy of an old literal can never match a later request. A slot's
// generation starts at 1 and moves on by one when its request completes or
// is cancelled. 0 is never issued: the step after 2^32-1 wraps to 0, and a
// slot at generation 0 is retired for good when freed (Counts.RetiredSlots)
// instead of going back to the free list.
const (
	claimMask = 1<<16 - 1
	retiring  = 1 << 16
	genShift  = 32
)

type blockSlot struct {
	word atomic.Uint64
	next atomic.Uint32 // free list
	ctx  uintptr
	lit  blockLiteral // the literal of the request using the slot
}

var (
	slots    [MaxBlocks]blockSlot
	freeHead atomic.Uint64 // ABA tag << 32 | (index+1); 0 = empty
)

func pop() (uint32, bool) {
	for {
		h := freeHead.Load()
		i := uint32(h)
		if i == 0 {
			return 0, false
		}
		next := slots[i-1].next.Load()
		if freeHead.CompareAndSwap(h, (h>>32+1)<<32|uint64(next)) {
			return i - 1, true
		}
	}
}

func push(i uint32) {
	for {
		h := freeHead.Load()
		slots[i].next.Store(uint32(h))
		if freeHead.CompareAndSwap(h, (h>>32+1)<<32|uint64(i+1)) {
			return
		}
	}
}

// claim takes a claim on slot i for a callback whose block captured gen. It
// fails (a stale call) if the generation moved on or the slot is retiring.
// A one-shot claim also completes the request in the same step: the
// generation moves on and the slot retires, so a second call is stale.
func claim(i uintptr, gen uint32, once bool) bool {
	s := &slots[i]
	for {
		w := s.word.Load()
		if uint32(w>>genShift) != gen || w&retiring != 0 || w&claimMask == claimMask {
			return false
		}
		nw := w + 1
		if once {
			nw = uint64(gen+1)<<genShift | retiring | (w&claimMask + 1)
		}
		if s.word.CompareAndSwap(w, nw) {
			return true
		}
	}
}

// release drops a claim; whoever drops the last claim of a retiring slot
// frees it.
func release(i uint32) {
	if w := slots[i].word.Add(^uint64(0)); w&retiring != 0 && w&claimMask == 0 {
		free(i)
	}
}

func free(i uint32) {
	liveBlocks.Add(-1)
	if slots[i].word.Load()>>genShift == 0 { // the generation wrapped
		retiredSlots.Add(1)
		return
	}
	push(i)
}

// A BlockKind is one kind of per-request block: its type signature and its
// handlers. Every request of the kind shares one ffi callback as the
// block's invoke function. Kinds are made at setup (NewBlockKind).
type BlockKind struct {
	desc   blockDescriptor
	sig    []byte
	invoke uintptr
	once   bool
	run    func(ctx uintptr, c *ffi.CB)
	stale  func(c *ffi.CB)
}

var kinds []*BlockKind // kept for the process lifetime

// NewBlockKind makes a kind of per-request block with the Objective-C type
// signature sig ("v16@?0c8@12" is void (^)(BOOL, id)).
//
// run handles a call that claimed its request: ctx is the value given to
// New, and c holds the block (argument 0) and the block's arguments (1...).
// With once set the request completes when it is first called (the
// completion handlers of the phase's APIs); otherwise it stays live until
// cancelled (a tap).
//
// stale handles a call that could not claim its request (cancelled,
// completed, or a duplicate): it may only make the native acknowledgement the
// API requires, from c's arguments, never touching a request's context; nil
// when the API needs none. Either handler may run on any thread.
func NewBlockKind(sig string, once bool, run func(ctx uintptr, c *ffi.CB), stale func(c *ffi.CB)) *BlockKind {
	k := &BlockKind{sig: append([]byte(sig), 0), once: once, run: run, stale: stale}
	k.desc = blockDescriptor{size: unsafe.Sizeof(blockLiteral{}), signature: uintptr(unsafe.Pointer(&k.sig[0]))}
	k.invoke = ffi.NewCallback(k.invoked)
	kinds = append(kinds, k)
	return k
}

func (k *BlockKind) invoked(c *ffi.CB) {
	b := (*blockLiteral)(c.Ptr(0)) // our literal or a heap copy of it
	if i := b.slot; i < MaxBlocks && claim(i, uint32(b.gen), k.once) {
		k.run(slots[i].ctx, c)
		release(uint32(i))
		return
	}
	staleCalls.Add(1)
	if k.stale != nil {
		k.stale(c)
	}
}

// A Request is one per-request block. The zero Request is not a request:
// its methods do nothing.
type Request struct{ slot, gen uint32 }

// New makes a request of kind k with context ctx (a ticket, an index into the
// caller's records) and returns it with its block literal ready to pass.
// It fails with ErrBlocksFull when MaxBlocks requests are outstanding. Any
// thread may call it; it allocates nothing.
//
// The literal is a stack-class block valid until Sent: pass Block() in one
// call, then call Sent when that call returns, whatever it did.
func (k *BlockKind) New(ctx uintptr) (Request, error) {
	i, ok := pop()
	if !ok {
		refused.Add(1)
		return Request{}, ErrBlocksFull
	}
	s := &slots[i]
	gen := uint32(s.word.Load() >> genShift)
	s.ctx = ctx
	s.lit = blockLiteral{
		isa:    stackBlockISA,
		flags:  blockHasSignature,
		invoke: k.invoke,
		desc:   uintptr(unsafe.Pointer(&k.desc)),
		slot:   uintptr(i),
		gen:    uintptr(gen),
	}
	liveBlocks.Add(1)
	s.word.Store(uint64(gen)<<genShift | 1) // the issuer's claim, until Sent
	return Request{i, gen}, nil
}

// Block returns the request's block literal, to pass in the call that hands
// it to the API.
func (r Request) Block() ID {
	if r.gen == 0 {
		return 0
	}
	return ID(unsafe.Pointer(&slots[r.slot].lit))
}

// Sent ends the life of the literal after the call that passed it returned:
// the literal is poisoned (a call through it, rather than through the copy
// an API should have made, is stale) and the issuer's claim is dropped.
// Call it exactly once per New.
func (r Request) Sent() {
	if r.gen == 0 {
		return
	}
	s := &slots[r.slot]
	s.lit.slot, s.lit.gen = ^uintptr(0), 0
	release(r.slot)
}

// Cancel ends the request: it moves the generation on and retires the slot
// in one step, so no callback can claim it any more. It never waits: the
// slot is freed when the last callback still running on it returns. It
// reports whether the request was live (not yet completed or cancelled).
func (r Request) Cancel() bool {
	if r.gen == 0 {
		return false
	}
	s := &slots[r.slot]
	for {
		w := s.word.Load()
		if uint32(w>>genShift) != r.gen || w&retiring != 0 {
			return false
		}
		if s.word.CompareAndSwap(w, uint64(r.gen+1)<<genShift|retiring|w&claimMask) {
			if w&claimMask == 0 {
				free(r.slot)
			}
			return true
		}
	}
}

// Live reports whether the request has not completed or been cancelled.
func (r Request) Live() bool {
	w := slots[r.slot].word.Load()
	return r.gen != 0 && uint32(w>>genShift) == r.gen && w&retiring == 0
}

// ---- Process-lifetime blocks ----

type globalBlock struct {
	lit  blockLiteral
	desc blockDescriptor
	sig  []byte
}

var (
	globalsMu sync.Mutex
	globals   []*globalBlock // kept for the process lifetime
)

// NewGlobalBlock makes a process-lifetime block with signature sig whose
// invoke function runs h (argument 0 is the block). It is a global block:
// the _NSConcreteGlobalBlock isa and the BLOCK_IS_GLOBAL flag, which is what
// _Block_copy tests, so copies return the block itself and it is never
// freed. Use it only for a fixed dispatcher that carries no per-request
// state. Setup only; it takes one ffi callback.
func NewGlobalBlock(sig string, h func(*ffi.CB)) ID {
	g := &globalBlock{sig: append([]byte(sig), 0)}
	g.desc = blockDescriptor{size: unsafe.Sizeof(blockLiteral{}), signature: uintptr(unsafe.Pointer(&g.sig[0]))}
	g.lit = blockLiteral{
		isa:    globalBlockISA,
		flags:  blockIsGlobal | blockHasSignature,
		invoke: ffi.NewCallback(h),
		desc:   uintptr(unsafe.Pointer(&g.desc)),
	}
	globalsMu.Lock()
	globals = append(globals, g)
	globalsMu.Unlock()
	return ID(unsafe.Pointer(&g.lit))
}

// ---- Blocks we are given ----

// BlockInvoke returns block b's invoke function, for calls whose arguments
// CallBlock cannot pass (floats): f.Reset().Arg(uintptr(b)).ArgF64(x).Call(BlockInvoke(b)).
func BlockInvoke(b ID) uintptr { return (*blockLiteral)(ffi.PtrFromC(uintptr(b))).invoke }

// CallBlock calls block b with integer or pointer arguments, using frame f,
// and returns its integer result. b must be valid: a block given in the call
// now running, or one kept with KeepBlock. It allocates nothing.
func CallBlock(f *ffi.Frame, b ID, args ...uintptr) uintptr {
	f.Reset().Arg(uintptr(b))
	for _, a := range args {
		f.Arg(a)
	}
	return f.Call(BlockInvoke(b)).R0()
}

// A KeptBlock holds a block given to us past the call that gave it, until
// it is called once. The zero value is empty.
type KeptBlock struct{ b atomic.Uintptr }

// KeepBlock copies block b (_Block_copy: a stack block becomes a heap copy,
// a heap block is retained) and stores the copy in k. It reports false, and
// keeps nothing, if b is nil or k already holds a block. The copy is the
// only way to keep a block we are given: the original is valid only during
// the call that passed it.
func KeepBlock(f *ffi.Frame, k *KeptBlock, b ID) bool {
	if b == 0 {
		return false
	}
	c := f.Call1(blockCopy, uintptr(b))
	if !k.b.CompareAndSwap(0, c) {
		f.Call1(blockRelease, c)
		return false
	}
	keptBlocks.Add(1)
	return true
}

// CallKeptBlock calls the block k holds with integer or pointer arguments,
// then releases it and empties k. It reports false if k was empty, so a
// block is called at most once even when two paths race to answer it.
func CallKeptBlock(f *ffi.Frame, k *KeptBlock, args ...uintptr) bool {
	b := k.b.Swap(0)
	if b == 0 {
		return false
	}
	CallBlock(f, ID(b), args...)
	f.Call1(blockRelease, b)
	keptBlocks.Add(-1)
	return true
}

// ReleaseKeptBlock releases the block k holds without calling it, and
// empties k: the call that gave it is never answered (the process is
// ending). It reports false if k was empty, so a kept block is called or
// released once, whichever of the two comes first.
func ReleaseKeptBlock(f *ffi.Frame, k *KeptBlock) bool {
	b := k.b.Swap(0)
	if b == 0 {
		return false
	}
	f.Call1(blockRelease, b)
	keptBlocks.Add(-1)
	return true
}

// Held reports whether k holds a block.
func (k *KeptBlock) Held() bool { return k.b.Load() != 0 }

// ---- Leak counters ----

var (
	liveBlocks, keptBlocks, delegates, observers, retiredSlots atomic.Int64
	staleCalls, refused                                        atomic.Uint64
)

// Counts is a snapshot of the leak counters, for the open/close leak gate:
// after a window or a service is closed, the live counts return to where
// they were.
type Counts struct {
	Blocks       int64  // per-request blocks whose slot is still taken
	KeptBlocks   int64  // blocks kept with KeepBlock and not yet called
	GlobalBlocks int64  // process-lifetime blocks (never freed)
	Delegates    int64  // objects retained with RetainDelegate and not released
	Observers    int64  // notification observers added and not removed
	StaleCalls   uint64 // calls of per-request blocks that could not claim their slot
	Refused      uint64 // requests refused because the block table was full
	RetiredSlots int64  // slots retired for good because their generation wrapped
}

// Leaks returns the current counts.
func Leaks() Counts {
	return Counts{
		Blocks:       liveBlocks.Load(),
		KeptBlocks:   keptBlocks.Load(),
		GlobalBlocks: globalBlocks(),
		Delegates:    delegates.Load(),
		Observers:    observers.Load(),
		StaleCalls:   staleCalls.Load(),
		Refused:      refused.Load(),
		RetiredSlots: retiredSlots.Load(),
	}
}

func globalBlocks() int64 {
	globalsMu.Lock()
	defer globalsMu.Unlock()
	return int64(len(globals))
}

var selAddObserver, selRemoveObserver SEL

// RetainDelegate retains a delegate or target object the framework owns
// and counts it (main thread).
func RetainDelegate(id ID) ID {
	delegates.Add(1)
	return Retain(id)
}

// ReleaseDelegate releases an object retained with RetainDelegate.
func ReleaseDelegate(id ID) {
	Release(id)
	delegates.Add(-1)
}

// AddObserver sends addObserver:selector:name:object: to a notification
// center and counts the observer (main thread).
func AddObserver(center, observer ID, sel SEL, name, object ID) {
	Send4(center, selAddObserver, uintptr(observer), uintptr(sel), uintptr(name), uintptr(object))
	observers.Add(1)
}

// RemoveObserver removes one registration made with AddObserver
// (removeObserver:name:object: with the same name and object).
func RemoveObserver(center, observer, name, object ID) {
	Send3(center, selRemoveObserver, uintptr(observer), uintptr(name), uintptr(object))
	observers.Add(-1)
}
