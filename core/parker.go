package core

import "sync/atomic"

// Parker puts one goroutine to sleep and lets any goroutine wake it, without
// allocating after construction. It is a one-token semaphore: Unpark makes a
// token available (a second Unpark before the token is taken changes
// nothing) and Park waits for the token and takes it, so a wake that comes
// before the wait is not lost. A woken Park may find nothing to do (a wake
// can arrive for work an earlier Park already handled): callers re-check
// their condition in a loop, as with a condition variable.
//
//	for !work() {               // work reports whether there was anything to do
//		if !p.Park() { return } // closed
//	}
//
// Why not sync.Cond or a channel. A goroutine that blocks in either takes a
// sudog from its P's cache (runtime.acquireSudog) and heap-allocates one when
// that cache and the central list are empty. Goroutines sleep on one P and wake
// on another, so sudogs drift between Ps, and runtime.GC empties the central
// list: a program that must allocate exactly nothing in steady state still
// sees an occasional 112-byte allocation. Parker sleeps somewhere else. On Unix
// it reads a pipe: the read blocks in the network poller, where
// runtime.netpollblock parks the goroutine with gopark on the pollDesc's own
// word. On Windows it waits for an auto-reset event in a system call
// (WaitForSingleObject); a pipe would not do there, because os.Pipe reads and
// writes go through overlapped I/O (internal/poll.execIO), which takes its
// operation records from a sync.Pool that every collection empties. Neither
// goes near acquireSudog, which only channel operations, select, sync.Cond and
// a contended sync.Mutex (the semaphore) call.
//
// One goroutine at a time may Park; Unpark and Close may be called from any
// goroutine, concurrently. A Parker holds an operating-system resource (two
// file descriptors on Unix, one handle on Windows) until Close, and a goroutine
// in Park holds an OS thread on Windows. If the resource cannot be made (out
// of descriptors), NewParker still works, with a channel, which has the
// semantics but not the zero-allocation property.
type Parker struct {
	sim    *simParker    // test-only worker gate; nil in production
	state  atomic.Uint32 // 1: a token is pending (the OS object is, or is about to be, signalled)
	closed atomic.Bool

	wmu Mutex         // serializes Unpark's signal and Close, so neither races the other's use of the OS object
	ch  chan struct{} // the fallback when the OS object could not be made
	sys parkerSys     // the operating-system object (parker_unix.go, parker_windows.go)
}

// NewParker returns a Parker with no token.
func NewParker() *Parker {
	p := new(Parker)
	if !p.sys.open() {
		p.ch = make(chan struct{}, 1)
	}
	return p
}

// Park waits until a token is available and takes it. It returns false when
// the Parker is closed (immediately if it already is), true otherwise.
func (p *Parker) Park() bool {
	if p.sim != nil {
		return p.sim.park()
	}
	if p.closed.Load() {
		return false
	}
	if p.ch != nil {
		if _, ok := <-p.ch; !ok {
			return false
		}
	} else if !p.sys.wait(&p.closed) {
		return false
	}
	// Swap, not Store: it observes (and so synchronizes with) the Unpark
	// whose Swap set the flag, and an Unpark that lands between the wait and
	// here is absorbed into this wake: its work was published before its
	// Swap, so the caller's re-check sees it.
	p.state.Swap(0)
	return !p.closed.Load()
}

// Unpark makes a token available, waking the parked goroutine if there is
// one. It does not block. When a token is already pending it does nothing
// (one system call per wake, not per call).
func (p *Parker) Unpark() {
	if p.sim != nil {
		p.sim.pending.Store(true)
		return
	}
	if p.state.Swap(1) != 0 {
		return
	}
	p.wmu.Lock()
	if !p.closed.Load() {
		if p.ch != nil {
			select {
			case p.ch <- struct{}{}:
			default:
			}
		} else {
			p.sys.wake()
		}
	}
	p.wmu.Unlock()
}

// Close releases the Parker's resources and makes Park return false, now and
// for a goroutine parked at this moment. It is safe to call more than once and
// concurrently with Unpark; Unpark after Close does nothing.
func (p *Parker) Close() {
	p.StopSimWorker()
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	p.wmu.Lock()
	if p.ch != nil {
		close(p.ch)
	} else {
		p.sys.wakeForClose()
	}
	p.wmu.Unlock()
	if p.ch == nil {
		p.sys.close()
	}
}
