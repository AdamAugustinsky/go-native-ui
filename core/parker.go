package core

import (
	"os"
	"sync/atomic"
)

// Parker puts one goroutine to sleep and lets any goroutine wake it, without
// allocating after construction. It is a one-token semaphore: Unpark makes a
// token available (a second Unpark before the token is taken changes
// nothing) and Park waits for the token and takes it, so a wake that comes
// before the wait is not lost. A woken Park may find nothing to do (a wake
// can arrive for work an earlier Park already handled): callers re-check
// their condition in a loop, as with a condition variable.
//
//	for !work() {            // work reports whether there was anything to do
//		if !p.Park() { return } // closed
//	}
//
// Why not sync.Cond or a channel. A goroutine that blocks in either takes a
// sudog from its P's cache (runtime.acquireSudog) and heap-allocates one when
// that cache and the central list are empty. Goroutines sleep on one P and wake
// on another, so sudogs drift between Ps, and runtime.GC empties the central
// list: a program that must allocate exactly nothing in steady state still
// sees an occasional 112-byte allocation. Parker sleeps on a pipe instead. On
// Unix the read blocks in the network poller (runtime.netpollblock parks the
// goroutine with gopark on the pollDesc's own word, no sudog); on Windows it
// blocks in ReadFile, a system call. Neither goes near acquireSudog, which
// only channel operations, select, sync.Cond and contended sync.Mutex (the
// semaphore) call.
//
// One goroutine at a time may Park; Unpark and Close may be called from any
// goroutine, concurrently. The Parker holds two file descriptors until Close.
// If the operating system will not make a pipe (out of descriptors), NewParker
// still works with a channel, which has the semantics but not the
// zero-allocation property.
type Parker struct {
	state  atomic.Uint32 // 1: a token is pending (a byte is, or is about to be, in the pipe)
	closed atomic.Bool

	r, w *os.File
	rb   [1]byte // read buffer, the parked goroutine's
	wb   [1]byte

	wmu Mutex // serializes writes so the pipe's own write lock is never contended
	ch  chan struct{}
}

// NewParker returns a Parker with no token.
func NewParker() *Parker {
	p := new(Parker)
	r, w, err := os.Pipe()
	if err != nil {
		p.ch = make(chan struct{}, 1)
		return p
	}
	p.r, p.w = r, w
	return p
}

// Park waits until a token is available and takes it. It returns false when
// the Parker is closed (immediately if it already is), true otherwise.
func (p *Parker) Park() bool {
	if p.closed.Load() {
		return false
	}
	if p.r == nil {
		if _, ok := <-p.ch; !ok {
			return false
		}
	} else if _, err := p.r.Read(p.rb[:]); err != nil {
		return false
	}
	// Swap, not Store: it observes (and so synchronizes with) the Unpark
	// whose Swap set the flag, and an Unpark that lands between the read and
	// here is absorbed into this wake: its work was published before its
	// Swap, so the caller's re-check sees it.
	p.state.Swap(0)
	return !p.closed.Load()
}

// Unpark makes a token available, waking the parked goroutine if there is
// one. It does not block. When a token is already pending it does nothing
// (one write per wake, not per call).
func (p *Parker) Unpark() {
	if p.state.Swap(1) != 0 {
		return
	}
	p.wmu.Lock()
	if !p.closed.Load() {
		if p.w != nil {
			p.w.Write(p.wb[:])
		} else {
			select {
			case p.ch <- struct{}{}:
			default:
			}
		}
	}
	p.wmu.Unlock()
}

// Close releases the Parker's resources and makes Park return false, now and
// for a goroutine parked at this moment. It is safe to call more than once and
// concurrently with Unpark; Unpark after Close does nothing.
func (p *Parker) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	p.wmu.Lock()
	if p.w != nil {
		p.w.Write(p.wb[:]) // wake a reader parked in the pipe
		p.w.Close()
	} else {
		close(p.ch)
	}
	p.wmu.Unlock()
	if p.r != nil {
		p.r.Close()
	}
}
