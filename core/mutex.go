package core

import (
	"runtime"
	"sync"
)

// Mutex is a mutual-exclusion lock whose Lock never parks the goroutine
// through the runtime's semaphore. sync.Mutex does: a goroutine that finds it
// held spins briefly and then sleeps on a sudog, which the runtime takes from
// the P's cache and heap-allocates when the cache and the central list (which
// every runtime.GC empties) are empty. That is an allocation the program did
// not ask for, in a path that is meant to allocate nothing. Mutex only ever
// calls sync.Mutex.TryLock, so it has no waiters, no sudog and (the lock is
// the standard library's, so the race detector sees it); a goroutine that
// loses spins on the lock word and, if the holder is slow, yields the P between
// attempts. (It never sleeps between attempts: the first time.Sleep of a
// goroutine allocates its timer.)
//
// Use it for the short critical sections (a few field updates, no I/O, no
// blocking calls) that the UI thread and background goroutines share; a long
// hold costs the waiters CPU where sync.Mutex would cost them nothing. It is
// not fair, not reentrant, and must not be copied after first use. The zero
// value is unlocked.
type Mutex struct{ m sync.Mutex }

// Lock locks m, retrying until it can.
func (m *Mutex) Lock() {
	if m.m.TryLock() {
		return
	}
	spins := mutexSpins
	if runtime.GOMAXPROCS(0) == 1 { // the holder cannot be running while we spin
		spins = 0
	}
	for i := 0; ; i++ {
		if i >= spins { // the holder is not running: let it
			runtime.Gosched()
		}
		if m.m.TryLock() {
			return
		}
	}
}

// mutexSpins is how many times Lock retries without yielding. A failed TryLock
// is a load of the lock word, so spinning is cheap and does not disturb the
// holder; the critical sections it is meant for are far shorter than the spin.
// Yielding is the fallback because it is expensive for a goroutine locked to an
// OS thread (the UI thread): the runtime hands its P to another thread, and
// starts one if none is idle, which allocates.
const mutexSpins = 1 << 14

// TryLock locks m if it is free and reports whether it did.
func (m *Mutex) TryLock() bool { return m.m.TryLock() }

// Unlock unlocks m. It is a fatal error if m is not locked.
func (m *Mutex) Unlock() { m.m.Unlock() }
