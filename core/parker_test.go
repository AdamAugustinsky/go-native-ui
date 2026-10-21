package core

import (
	"fmt"
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

// stallLimit is how long a test that passes wakes around waits without any
// progress before it calls a wake-up lost. A round takes microseconds, or a
// millisecond where a wake is a thread switch on a busy virtual machine (the
// Windows runners take 12-30 s for the tests of this file), so a slow machine
// keeps advancing, and a lost wake-up stops for good.
const stallLimit = 10 * time.Second

// progress waits until done is closed. It fails the test, printing dump() and
// the stacks of every goroutine, when count (the work done so far) has not
// advanced for stallLimit, or when the deadline of the test binary (go test
// -timeout) is close, so that the failure is this one with its state and not
// the binary's panic.
func progress(t *testing.T, what string, done <-chan struct{}, count func() int64, dump func() string) {
	t.Helper()
	start := time.Now()
	end, hasEnd := t.Deadline()
	last, lastAt := count(), start
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-tick.C:
			n := count()
			if n != last {
				last, lastAt = n, now
			}
			var why string
			switch {
			case now.Sub(lastAt) >= stallLimit:
				why = fmt.Sprintf("made no progress for %v", now.Sub(lastAt).Round(time.Millisecond))
			case hasEnd && end.Sub(now) < 10*time.Second:
				why = "is still running at the test deadline"
			default:
				continue
			}
			el := now.Sub(start)
			t.Fatalf("%s %s: %d done in %v (%.0f/s)\n%s\n%s", what, why, n, el.Round(time.Millisecond),
				float64(n)/el.Seconds(), dump(), stacks())
		}
	}
}

// stacks returns the stacks of every goroutine.
func stacks() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

// parkerState describes a Parker for a failure message.
func parkerState(p *Parker) string {
	return fmt.Sprintf("{token pending %d, closed %v, fallback %v (%d buffered), sys %+v}",
		p.state.Load(), p.closed.Load(), p.ch != nil, len(p.ch), &p.sys)
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
			var seen, wakes atomic.Int64
			done := make(chan struct{})
			go func() {
				defer close(done)
				for seen.Load() < wakers*per {
					if n := posted.Load(); n > seen.Load() {
						seen.Store(n)
						continue
					}
					if !p.Park() {
						t.Error("Park returned false")
						return
					}
					wakes.Add(1)
				}
			}()
			progress(t, "waiter", done, seen.Load, func() string {
				return fmt.Sprintf("seen %d of %d posts (%d posted), %d wakes; %s", seen.Load(), wakers*per, posted.Load(), wakes.Load(), parkerState(p))
			})
			wg.Wait()
			t.Logf("%d posts, %d wakes", seen.Load(), wakes.Load())
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
						if !me.Park() {
							return // closed: the test failed
						}
					}
					turn.Add(1)
					other.Unpark()
				}
			}
			wg.Add(2)
			go play(a, b, 0)
			go play(b, a, 1)
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			progress(t, "ping-pong", done, turn.Load, func() string {
				return fmt.Sprintf("turn %d of %d (even: a's); a %s; b %s", turn.Load(), 2*rounds, parkerState(a), parkerState(b))
			})
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

// cleanWindow reports whether a window after the first allocated nothing (the
// first follows a collection and may hold its allocations). The counters are
// process-wide, so the runtime's own one-offs (a new thread, its timer heap)
// can land in any window, more often on a busy CI runner; an allocation per
// park, wake or lock (thousands per window) is in every window and fails. The
// rule is the bench gate's (D27).
func cleanWindow(deltas []uint64) bool {
	for _, d := range deltas[1:] {
		if d == 0 {
			return true
		}
	}
	return false
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
	// waitTurns polls (progress would allocate inside the measured windows)
	// and fails the same way when the turns stop.
	waitTurns := func(n int64) {
		last, lastAt := turn.Load(), time.Now()
		for turn.Load() < n {
			if now := time.Now(); turn.Load() != last {
				last, lastAt = turn.Load(), now
			} else if now.Sub(lastAt) >= stallLimit {
				stop.Store(1)
				var b strings.Builder
				for i, p := range ps {
					fmt.Fprintf(&b, "player %d %s\n", i, parkerState(p))
				}
				t.Fatalf("the ring made no progress for %v at turn %d of %d (player %d's)\n%s%s",
					now.Sub(lastAt).Round(time.Millisecond), last, n, last%players, b.String(), stacks())
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
		if cleanWindow(deltas[:]) {
			break
		}
		t.Logf("attempt %d: mallocs per window %v", attempt, deltas)
	}
	stop.Store(1)
	for _, p := range ps {
		p.Unpark()
	}
	within(t, 10*time.Second, "players", wg.Wait)
	if !cleanWindow(deltas[:]) {
		t.Errorf("mallocs per window over %d park/wake rounds: %v, want a window after the first with 0", rounds, deltas)
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
	if p.ch != nil {
		t.Skip("no OS object could be made")
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
