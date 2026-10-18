package core

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// within fails the test if f does not return in d (a lost wake-up hangs).
func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not finish within %v", what, d)
	}
}

// parkers returns a pipe-backed Parker and the channel fallback, so the
// semantics tests run on both.
func parkers() map[string]func() *Parker {
	return map[string]func() *Parker{
		"pipe":     NewParker,
		"fallback": func() *Parker { return &Parker{ch: make(chan struct{}, 1)} },
	}
}

func TestParkerWakeBeforeWaitIsKept(t *testing.T) {
	for name, mk := range parkers() {
		t.Run(name, func(t *testing.T) {
			p := mk()
			defer p.Close()
			p.Unpark()
			p.Unpark() // coalesces into the same token
			within(t, 5*time.Second, "Park after Unpark", func() {
				if !p.Park() {
					t.Error("Park returned false on an open Parker")
				}
			})
			// The token was one: the next Park waits until another Unpark.
			var woke atomic.Bool
			go func() { p.Park(); woke.Store(true) }()
			time.Sleep(50 * time.Millisecond)
			if woke.Load() {
				t.Fatal("a second Park returned without a token")
			}
			p.Unpark()
			deadline := time.Now().Add(5 * time.Second)
			for !woke.Load() {
				if time.Now().After(deadline) {
					t.Fatal("Unpark did not wake a parked goroutine")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

// Many goroutines post work and Unpark; the one waiter must end up seeing all
// of it, whatever the interleaving of posts, wakes and parks.
func TestParkerNoLostWakeups(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(max(4, runtime.GOMAXPROCS(0))))
	for name, mk := range parkers() {
		t.Run(name, func(t *testing.T) {
			const wakers, per = 8, 2000
			p := mk()
			defer p.Close()
			var posted atomic.Int64
			var wg sync.WaitGroup
			for i := 0; i < wakers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < per; j++ {
						posted.Add(1)
						p.Unpark()
						if j%16 == 0 {
							runtime.Gosched()
						}
					}
				}()
			}
			var seen, wakes int64
			within(t, 30*time.Second, "waiter", func() {
				for seen < wakers*per {
					if n := posted.Load(); n > seen {
						seen = n
						continue
					}
					if !p.Park() {
						t.Error("Park returned false")
						return
					}
					wakes++
				}
			})
			wg.Wait()
			t.Logf("%d posts, %d wakes", seen, wakes)
		})
	}
}

// Strict alternation between two goroutines through two Parkers: every wake
// must arrive, and none may be consumed twice.
func TestParkerPingPong(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(max(4, runtime.GOMAXPROCS(0))))
	for name, mk := range parkers() {
		t.Run(name, func(t *testing.T) {
			const rounds = 20000
			a, b := mk(), mk()
			defer a.Close()
			defer b.Close()
			var turn atomic.Int64 // even: a's turn, odd: b's
			var wg sync.WaitGroup
			play := func(me, other *Parker, parity int64) {
				defer wg.Done()
				for i := int64(0); i < rounds; i++ {
					for turn.Load()%2 != parity {
						me.Park()
					}
					turn.Add(1)
					other.Unpark()
				}
			}
			wg.Add(2)
			go play(a, b, 0)
			go play(b, a, 1)
			within(t, 30*time.Second, "ping-pong", wg.Wait)
			if got := turn.Load(); got != 2*rounds {
				t.Fatalf("%d turns, want %d", got, 2*rounds)
			}
		})
	}
}

func TestParkerClose(t *testing.T) {
	for name, mk := range parkers() {
		t.Run(name, func(t *testing.T) {
			p := mk()
			var res atomic.Int32 // 0 parked, 1 returned true, 2 returned false
			go func() {
				if p.Park() {
					res.Store(1)
				} else {
					res.Store(2)
				}
			}()
			time.Sleep(50 * time.Millisecond)
			p.Close()
			deadline := time.Now().Add(5 * time.Second)
			for res.Load() == 0 {
				if time.Now().After(deadline) {
					t.Fatal("Close did not wake the parked goroutine")
				}
				time.Sleep(time.Millisecond)
			}
			if res.Load() != 2 {
				t.Error("a Park woken by Close returned true")
			}
			within(t, 5*time.Second, "operations after Close", func() {
				if p.Park() {
					t.Error("Park after Close returned true")
				}
				p.Unpark() // no panic, no block
				p.Close()  // idempotent
			})
		})
	}
}

// Close racing Unpark and Park from other goroutines must neither panic nor
// leave anything blocked.
func TestParkerCloseRace(t *testing.T) {
	for name, mk := range parkers() {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < 200; i++ {
				p := mk()
				var wg sync.WaitGroup
				wg.Add(3)
				go func() { defer wg.Done(); p.Park() }()
				go func() { defer wg.Done(); p.Unpark(); p.Unpark() }()
				go func() { defer wg.Done(); p.Close() }()
				within(t, 10*time.Second, "close race", wg.Wait)
				p.Close()
			}
		})
	}
}

// attempts is how often a zero-allocation measurement may be repeated. The
// runtime itself sometimes allocates once, in the window (starting an OS
// thread, a background GC worker); a program that allocates because of the code
// under test does so in every attempt.
const attempts = 3

// zeroAfterFirst reports whether every window but the first allocated nothing;
// the first one follows a collection and may hold its allocations.
func zeroAfterFirst(deltas []uint64) bool {
	for _, d := range deltas[1:] {
		if d != 0 {
			return false
		}
	}
	return true
}

// Parking and waking, across Ps, allocates nothing: the sudogs of sync.Cond
// and channels are what this type exists to avoid. A token passes round a ring
// of goroutines that all sleep in Park, so many are parked at once and move
// between Ps. The counters are process-wide; every window follows a collection,
// which empties the runtime's central sudog list, and nothing in the windows
// allocates on the test's behalf.
func TestParkerZeroAlloc(t *testing.T) {
	zeroAllocRing(t, NewParker)
}

// zeroAllocRing is TestParkerZeroAlloc for any constructor, so the test can
// be shown to fail on the channel fallback.
func zeroAllocRing(t *testing.T, mk func() *Parker) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(max(4, runtime.GOMAXPROCS(0))))
	const players, rounds, windows = 8, 4000, 4
	ps := make([]*Parker, players)
	for i := range ps {
		ps[i] = mk()
	}
	defer func() {
		for _, p := range ps {
			p.Close()
		}
	}()
	var turn, stop atomic.Int64
	var wg sync.WaitGroup
	play := func(i int) {
		defer wg.Done()
		for {
			for turn.Load()%players != int64(i) {
				if !ps[i].Park() || stop.Load() != 0 {
					return
				}
			}
			turn.Add(1)
			ps[(i+1)%players].Unpark()
		}
	}
	for i := range ps {
		wg.Add(1)
		go play(i)
	}
	waitTurns := func(n int64) {
		deadline := time.Now().Add(30 * time.Second)
		for turn.Load() < n {
			if time.Now().After(deadline) {
				stop.Store(1)
				t.Fatalf("stalled at turn %d of %d", turn.Load(), n)
			}
			time.Sleep(200 * time.Microsecond) // not Gosched: that makes the runtime start threads
		}
	}
	waitTurns(rounds) // warm up
	var deltas [windows]uint64
	for attempt := 0; attempt < attempts; attempt++ {
		var m0, m1 runtime.MemStats
		for w := range deltas {
			runtime.GC()
			time.Sleep(5 * time.Millisecond)
			runtime.ReadMemStats(&m0)
			waitTurns(turn.Load() + rounds)
			runtime.ReadMemStats(&m1)
			deltas[w] = m1.Mallocs - m0.Mallocs
		}
		if zeroAfterFirst(deltas[:]) {
			break
		}
		t.Logf("attempt %d: mallocs per window %v", attempt, deltas)
	}
	stop.Store(1)
	for _, p := range ps {
		p.Unpark()
	}
	within(t, 10*time.Second, "players", wg.Wait)
	if !zeroAfterFirst(deltas[:]) {
		t.Errorf("mallocs per window over %d park/wake rounds: %v, want 0 after the first", rounds, deltas)
	}
}

// parkedState returns the scheduler state of the goroutine blocked in
// Parker.Park ("IO wait", "chan receive", ...), or "" if there is none.
func parkedState() string {
	buf := make([]byte, 1<<20)
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		if !strings.Contains(g, "core.(*Parker).Park") {
			continue
		}
		head, _, _ := strings.Cut(g, "\n")
		if i, j := strings.Index(head, "["), strings.Index(head, "]"); i >= 0 && j > i {
			state, _, _ := strings.Cut(head[i+1:j], ",")
			return state
		}
	}
	return ""
}

// The goroutine in Park sleeps in the network poller ("IO wait") or, where a
// pipe read is a system call (Windows), in "syscall". Sleeping in a channel
// receive, sync.Cond or semaphore would take a sudog from the runtime, which
// is the allocation Parker exists to avoid; this is the direct check of how it
// sleeps, where the allocation counts of the other tests depend on how many
// sudogs happen to be cached.
func TestParkerSleepsWithoutSudog(t *testing.T) {
	p := NewParker()
	defer p.Close()
	if p.r == nil {
		t.Skip("no pipe could be made")
	}
	done := make(chan struct{})
	go func() { p.Park(); close(done) }()
	var state string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if state = parkedState(); state != "" && state != "runnable" && state != "running" {
			break // it has gone to sleep
		}
	}
	switch {
	case state == "":
		t.Fatal("no goroutine was found in Park")
	case state != "IO wait" && state != "syscall":
		t.Errorf("Park sleeps in state %q, want IO wait or syscall (not a sudog wait)", state)
	}
	p.Unpark()
	within(t, 5*time.Second, "Park", func() { <-done })
}
