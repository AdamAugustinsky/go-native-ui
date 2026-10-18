package core

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMutexExcludes(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(max(4, runtime.GOMAXPROCS(0))))
	var m Mutex
	n := 0
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20000; j++ {
				m.Lock()
				n++ // a data race if Lock does not exclude (-race sees it)
				m.Unlock()
			}
		}()
	}
	within(t, 30*time.Second, "lockers", wg.Wait)
	if n != 8*20000 {
		t.Fatalf("n = %d", n)
	}
	if !m.TryLock() {
		t.Fatal("TryLock failed on a free mutex")
	}
	if m.TryLock() {
		t.Fatal("TryLock succeeded on a held mutex")
	}
	m.Unlock()
}

// A lock held for a while makes waiters retry for that long  and still get it.
func TestMutexSlowHolder(t *testing.T) {
	var m Mutex
	m.Lock()
	got := make(chan struct{})
	go func() { m.Lock(); m.Unlock(); close(got) }()
	time.Sleep(30 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("Lock returned while held")
	default:
	}
	m.Unlock()
	within(t, 5*time.Second, "waiter", func() { <-got })
}

// Contention never takes a runtime sudog, so it never allocates one: a
// sync.Mutex fought over by goroutines on several Ps does, once a collection
// has emptied the runtime's lists.
func TestMutexContentionZeroAlloc(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(max(4, runtime.GOMAXPROCS(0))))
	var m Mutex
	var stop atomic.Bool
	var wg sync.WaitGroup
	shared := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				m.Lock()
				for k := 0; k < 2000; k++ { // hold it long enough to be contended
					shared += k & 1
				}
				m.Unlock()
			}
		}()
	}
	time.Sleep(300 * time.Millisecond) // warm up: the runtime starts its threads, and this goroutine gets its sleep timer
	var deltas [4]uint64
	for attempt := 0; attempt < attempts; attempt++ {
		var m0, m1 runtime.MemStats
		for w := range deltas {
			runtime.GC() // empties the runtime's central sudog list for every window
			time.Sleep(20 * time.Millisecond)
			runtime.ReadMemStats(&m0)
			time.Sleep(50 * time.Millisecond)
			runtime.ReadMemStats(&m1)
			deltas[w] = m1.Mallocs - m0.Mallocs
		}
		if zeroAfterFirst(deltas[:]) {
			break
		}
		t.Logf("attempt %d: mallocs per window %v", attempt, deltas)
	}
	stop.Store(true)
	within(t, 10*time.Second, "lockers", wg.Wait)
	if !zeroAfterFirst(deltas[:]) {
		t.Errorf("mallocs per window under contention: %v, want 0 after the first", deltas)
	}
}
