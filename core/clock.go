package core

import (
	"sync"
	"sync/atomic"
	"time"
)

var simulation atomic.Pointer[SimClock]

// Now returns monotonic nanoseconds, in the native event clock's timebase.
func Now() int64 {
	if c := simulation.Load(); c != nil {
		return c.now.Load()
	}
	return systemNanotime()
}

// WallNow is time.Now, or the simulation's fixed epoch plus elapsed time.
// Use Now for durations and WallNow for APIs that store time.Time.
func WallNow() time.Time {
	if c := simulation.Load(); c != nil {
		return time.Unix(0, c.now.Load()).UTC()
	}
	return time.Now()
}

// Since measures elapsed behaviour time. Real I/O watchdogs must use time.Since.
func Since(t time.Time) time.Duration {
	if c := simulation.Load(); c != nil {
		return time.Unix(0, c.now.Load()).Sub(t)
	}
	return time.Since(t)
}

// Timer is an AfterFunc timer. Reset and Stop have time.Timer's semantics.
// Reset adopts the current clock, including when a shared pool survives a test.
type Timer struct {
	mu     sync.Mutex
	real   *time.Timer
	clock  *SimClock
	due    int64
	seq    uint64
	active bool
	f      func()
}

// AfterFunc calls f after d. A simulation runs callbacks on its advancing
// goroutine, in deadline then insertion order, never while holding a clock lock.
func AfterFunc(d time.Duration, f func()) *Timer {
	t := &Timer{f: f}
	t.Reset(d)
	return t
}

// Stop cancels a pending callback and reports whether it was still scheduled.
// It does not wait for a real callback that is already running.
func (t *Timer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.real != nil {
		return t.real.Stop()
	}
	if t.clock == nil {
		return false
	}
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	was := t.active
	t.active = false
	delete(t.clock.timers, t)
	return was
}

// Reset schedules the callback after d and reports whether it was pending.
// A stopped/expired shared timer adopts the clock of its new owner.
func (t *Timer) Reset(d time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := simulation.Load()
	was := false
	if t.clock != nil {
		t.clock.mu.Lock()
		was = t.active
		t.active = false
		delete(t.clock.timers, t)
		t.clock.mu.Unlock()
	}
	if t.real != nil {
		if c == nil {
			return t.real.Reset(d)
		}
		was = t.real.Stop() || was
		t.real = nil
	}
	t.clock = c
	if c == nil {
		t.real = time.AfterFunc(d, t.f)
		return was
	}
	c.mu.Lock()
	c.seq++
	t.seq, t.due, t.active = c.seq, c.now.Load()+int64(max(d, 0)), true
	c.timers[t] = struct{}{}
	c.mu.Unlock()
	return was
}
