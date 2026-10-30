package core

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// SimClock is a test-only, process-wide clock. Install it before creating
// services, close those services before Restore, and never use parallel tests
// or native event pumps alongside it. Workers and timers have a stable order.
type SimClock struct {
	now     atomic.Int64
	mu      sync.Mutex
	seq     uint64
	timers  map[*Timer]struct{}
	workers []*simParker
	wake    atomic.Bool
}

// InstallSimClockForTesting installs the only simulation allowed in a process.
// Time starts at one second because frame deadlines use zero as a sentinel.
func InstallSimClockForTesting() (*SimClock, error) {
	c := &SimClock{timers: make(map[*Timer]struct{})}
	c.now.Store(int64(time.Second))
	if !simulation.CompareAndSwap(nil, c) {
		return nil, errors.New("core: a simulation is already installed")
	}
	return c, nil
}

// SimulationForTesting returns the active test clock, nil in production.
func SimulationForTesting() *SimClock { return simulation.Load() }

// Restore removes the test clock and cancels its outstanding timers. Services
// must already be closed; timers from this run never fire in the next run.
func (c *SimClock) Restore() {
	c.mu.Lock()
	for t := range c.timers {
		t.active = false
	}
	clear(c.timers)
	c.mu.Unlock()
	simulation.CompareAndSwap(c, nil)
}

// Wake coalesces a simulated platform wake. TakeWake consumes it.
func (c *SimClock) Wake() { c.wake.Store(true) }

// TakeWake consumes the coalesced wake from simulated workers and callbacks.
func (c *SimClock) TakeWake() bool { return c.wake.Swap(false) }

// Next returns the next timer's absolute deadline, or zero.
func (c *SimClock) Next() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var due int64
	for t := range c.timers {
		if due == 0 || t.due < due {
			due = t.due
		}
	}
	return due
}

// AdvanceTo jumps to at and fires due timers, including timers their callbacks
// create. Call Quiesce before deciding how far to jump. Backwards time and a
// zero-delay callback loop are errors. It returns the number of callbacks.
func (c *SimClock) AdvanceTo(at int64) (int, error) {
	if at < c.now.Load() {
		return 0, errors.New("core: simulation clock went backwards")
	}
	n := 0
	for {
		c.mu.Lock()
		var next *Timer
		for t := range c.timers {
			if t.due <= at && (next == nil || t.due < next.due || t.due == next.due && t.seq < next.seq) {
				next = t
			}
		}
		if next == nil {
			c.now.Store(at)
			c.mu.Unlock()
			return n, nil
		}
		if n == 100000 {
			c.mu.Unlock()
			return n, errors.New("core: simulation timer loop")
		}
		c.now.Store(max(c.now.Load(), next.due))
		delete(c.timers, next)
		next.active = false
		c.mu.Unlock()
		next.f()
		n++
	}
}

// Quiesce runs each ready worker until it parks, in registration order. It
// repeats until no worker has runnable work. The bound is REAL time: a stuck
// filesystem/decoder is an error, never evidence of completion. Full result
// rings may park with results pending; the next UI turn drains them.
func (c *SimClock) Quiesce(limit time.Duration) error {
	end := time.Now().Add(limit)
	for pass := 0; ; pass++ {
		if pass == 100000 {
			return errors.New("core: simulation workers never became idle")
		}
		progress := false
		for i, p := range c.workers {
			if f := p.failure.Load(); f != nil {
				return fmt.Errorf("worker %d: %w", i, f.err)
			}
			if p.closed.Load() {
				continue
			}
			if !p.parked {
				if err := p.wait(end); err != nil {
					return fmt.Errorf("worker %d: %w", i, err)
				}
			}
			if !p.pending.Swap(false) {
				continue
			}
			p.parked = false
			p.run <- struct{}{}
			if err := p.wait(end); err != nil {
				return fmt.Errorf("worker %d: %w", i, err)
			}
			progress = true
		}
		if !progress {
			return nil
		}
	}
}

type simParker struct {
	run     chan struct{}
	idle    chan struct{}
	stop    chan struct{}
	pending atomic.Bool
	closed  atomic.Bool
	parked  bool // simulation driver only
	failure atomic.Pointer[simFailure]
}

type simFailure struct{ err error }

func (p *simParker) wait(end time.Time) error {
	t := time.NewTimer(time.Until(end))
	defer t.Stop()
	select {
	case <-p.idle:
		p.parked = true
		if f := p.failure.Load(); f != nil {
			return f.err
		}
		return nil
	case <-p.stop:
		return nil
	case <-t.C:
		return errors.New("real-time quiescence timeout")
	}
}

func (p *simParker) park() bool {
	select {
	case p.idle <- struct{}{}:
	case <-p.stop:
		return false
	}
	select {
	case <-p.run:
		return !p.closed.Load()
	case <-p.stop:
		return false
	}
}

// BindSimWorkerForTesting makes this parker a deterministic worker gate if a
// test clock is installed. Call before starting its worker, then StartSimWorker
// at the worker's entry (after any setup acknowledged to its constructor).
func (p *Parker) BindSimWorkerForTesting() {
	if c := simulation.Load(); c != nil {
		p.sim = &simParker{run: make(chan struct{}, 1), idle: make(chan struct{}, 1), stop: make(chan struct{})}
		p.sim.pending.Store(true)
		c.workers = append(c.workers, p.sim)
	}
}

// StartSimWorker waits for the first simulated turn; it does nothing normally.
func (p *Parker) StartSimWorker() bool { return p.sim == nil || p.sim.park() }

// StopSimWorker releases a test gate before a service joins its workers.
func (p *Parker) StopSimWorker() {
	if p.sim != nil && p.sim.closed.CompareAndSwap(false, true) {
		close(p.sim.stop)
	}
}

// RecoverSimWorker reports a worker panic to the simulation driver. Defer it
// at worker entry. With no test clock it preserves normal panic behaviour.
func (p *Parker) RecoverSimWorker() {
	if v := recover(); v != nil {
		if p.sim == nil {
			panic(v)
		}
		p.sim.failure.Store(&simFailure{fmt.Errorf("panic: %v", v)})
		select {
		case p.sim.idle <- struct{}{}:
		default:
		}
	}
}
