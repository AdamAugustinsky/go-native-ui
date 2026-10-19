// Package task runs work off the UI thread and hands the results back to it.
//
// A Pool[In, Out] runs one registered function on a fixed set of worker
// goroutines. The UI thread (or any single goroutine that owns the pool, the
// "owner") submits inputs and polls results; nothing it calls blocks, performs
// I/O or, once the pool is warm, allocates:
//
//	p := task.NewPool(task.Options{Wake: platform.Wake}, func(t task.Token, in Req) Resp {
//		return decode(t, in) // runs on a worker; poll t.Cancelled() in long loops
//	})
//	defer p.Close(time.Second)
//
//	tk, err := p.Submit(req, key)      // in an event handler: ErrFull when saturated
//	results = p.Poll(results[:0])      // each frame: the answers that arrived
//
// # Contract
//
//   - In is any value; Out holds no pointers (NewPool panics otherwise), so a
//     result is a copy of plain data and nothing the GC must follow crosses
//     threads. Errors are part of Out (a code, a length), not an error value.
//   - Everything the pool has accepted is bounded: at most Options.Queue jobs are
//     accepted and not yet polled, queued, running or finished. Submit returns
//     ErrFull instead of waiting or dropping. A finished job holds its slot
//     until Poll collects it, so an owner that never polls gets ErrFull, visibly.
//   - Latest wins, by key: Submit with a non-zero key that a job already has
//     replaces that job. A queued job's input is replaced in place; a running one
//     is cancelled (its Token says so) and its result is discarded; a finished
//     one that was not polled yet is discarded. Different keys never supersede
//     each other; key 0 never does.
//   - Results arrive in the order the jobs finished, through Poll, on the
//     owner's goroutine. The wake function given in Options is called (from a
//     worker, coalesced by platform.Wake) after a result became available.
//   - Keep declares work for as long as it keeps being declared (see Pool.Keep).
//   - Close waits a bounded time. Go cannot stop a goroutine: work that ignores
//     its Token keeps running and its result is discarded.
//
// A function that panics takes the process down, as any goroutine's panic does.
//
// # Waking the loop
//
// The pool wakes the UI loop through the function in Options.Wake, which today
// is platform.Wake. Results never travel through app.Post (that path is the
// application's own); they sit in the pool's slots until the frame Polls.
package task

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/core"
)

// ErrFull is returned by Submit and Keep when the pool already holds as many
// accepted jobs as its queue allows. The caller keeps the input and may retry.
var ErrFull = errors.New("task: queue full")

// ErrClosed is returned by Submit and Keep after Close.
var ErrClosed = errors.New("task: pool closed")

// ErrKey is returned by Keep for key 0, which names no work.
var ErrKey = errors.New("task: key 0 cannot be kept")

// Ticket names one accepted submission. It is never 0 and never reused within
// a pool.
type Ticket uint64

// Token is handed to the worker function. It reports whether the job was
// superseded, cancelled or abandoned by Close, and is valid only until the
// function returns.
type Token struct{ c *cancelState }

// Cancelled reports whether the job's result is no longer wanted. A long
// function polls it and returns early.
func (t Token) Cancelled() bool { return t.c.flag.Load() }

// Context returns a context.Context that is cancelled when Cancelled becomes
// true, for functions that call into APIs that take one (net/http, database
// drivers). It allocates; call it on the worker, once.
func (t Token) Context() context.Context { return t.c.context() }

// cancelState is the cancellation half of a job, shared by the owner (which
// sets it) and the worker (which reads it).
type cancelState struct {
	flag   atomic.Bool
	mu     core.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

func (c *cancelState) set() {
	c.mu.Lock()
	c.flag.Store(true)
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
}

func (c *cancelState) reset() {
	c.mu.Lock()
	c.flag.Store(false)
	if c.cancel != nil {
		c.cancel()
	}
	c.ctx, c.cancel = nil, nil
	c.mu.Unlock()
}

func (c *cancelState) context() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil {
		c.ctx, c.cancel = context.WithCancel(context.Background())
		if c.flag.Load() {
			c.cancel()
		}
	}
	return c.ctx
}

// Options configures a Pool. The zero value is usable.
type Options struct {
	// Workers is the number of goroutines that run jobs. 0 means
	// min(GOMAXPROCS, 4).
	Workers int
	// Queue is the most jobs the pool accepts at once: queued, running or
	// finished and not yet polled. 0 means 64.
	Queue int
	// Wake is called from a worker goroutine, without any lock held, after
	// a result became available to Poll. It must not block; platform.Wake is
	// the usual value. nil means none (the owner polls on its own schedule).
	Wake func()
}

// Result is one finished job as Poll reports it.
type Result[Out any] struct {
	// Ticket is the submission this answers.
	Ticket Ticket
	// Key is the key the job was submitted with (0 if none).
	Key uint64
	// Out is what the worker function returned.
	Out Out
}

const (
	stFree uint8 = iota
	stQueued
	stRunning
	stDone
	stZombie // running, but nobody wants the result any more
)

const none = int32(-1)

type job[In, Out any] struct {
	cancelState
	in         In
	out        Out
	key        uint64
	ticket     Ticket
	state      uint8
	prev, next int32
}

// Pool runs jobs on worker goroutines. Submit, Cancel, CancelKey, Poll, Keep and
// Sweep belong to one owner goroutine (the UI thread); Close may come from it
// too. See the package comment.
type Pool[In, Out any] struct {
	fn   func(Token, In) Out
	wake func()

	mu          core.Mutex
	jobs        []job[In, Out] // Queue + Workers slots: at most Workers can be zombies
	free        int32          // stack of free slots, linked through next
	queue, done list
	live        int // queued + running + done jobs that still count (not zombies)
	queueCap    int
	nextTicket  Ticket
	closed      bool
	idle        []bool
	parks       []*core.Parker
	doneN       atomic.Int32 // len(done), readable without mu
	refused     atomic.Bool  // a Keep was refused with ErrFull and no wake has been owed since
	shut        atomic.Bool  // closed, readable without mu

	wg sync.WaitGroup

	// Owner-only state of Keep and Sweep.
	kept []kept
}

type kept struct {
	key    uint64
	ticket Ticket
	seen   bool
}

// list is a doubly linked FIFO of job slots.
type list struct{ head, tail int32 }

// NewPool starts a pool whose workers run fn. It panics if Out holds pointers
// or fn is nil.
func NewPool[In, Out any](opt Options, fn func(Token, In) Out) *Pool[In, Out] {
	if fn == nil {
		panic("task: nil function")
	}
	if !core.PointerFree[Out]() {
		panic("task: Out must not hold pointers (strings, slices, maps, interfaces and pointers are not allowed)")
	}
	if opt.Workers <= 0 {
		opt.Workers = min(runtime.GOMAXPROCS(0), 4)
	}
	if opt.Queue <= 0 {
		opt.Queue = 64
	}
	p := &Pool[In, Out]{
		fn: fn, wake: opt.Wake,
		jobs:     make([]job[In, Out], opt.Queue+opt.Workers),
		queueCap: opt.Queue,
		queue:    list{none, none}, done: list{none, none},
		idle:  make([]bool, opt.Workers),
		parks: make([]*core.Parker, opt.Workers),
		kept:  make([]kept, 0, 16),
	}
	if p.wake == nil {
		p.wake = func() {}
	}
	p.free = none
	for i := len(p.jobs) - 1; i >= 0; i-- {
		p.jobs[i].next = p.free
		p.free = int32(i)
	}
	for i := range p.parks {
		p.parks[i] = core.NewParker()
	}
	for i := 0; i < opt.Workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}
	return p
}

// pushBack appends slot i to l. p.mu held.
func (p *Pool[In, Out]) pushBack(l *list, i int32) {
	j := &p.jobs[i]
	j.prev, j.next = l.tail, none
	if l.tail != none {
		p.jobs[l.tail].next = i
	} else {
		l.head = i
	}
	l.tail = i
}

// unlink removes slot i from l. p.mu held.
func (p *Pool[In, Out]) unlink(l *list, i int32) {
	j := &p.jobs[i]
	if j.prev != none {
		p.jobs[j.prev].next = j.next
	} else {
		l.head = j.next
	}
	if j.next != none {
		p.jobs[j.next].prev = j.prev
	} else {
		l.tail = j.prev
	}
	j.prev, j.next = none, none
}

// release returns slot i to the free stack. p.mu held.
func (p *Pool[In, Out]) release(i int32) {
	j := &p.jobs[i]
	var zi In
	var zo Out
	j.in, j.out = zi, zo
	j.key, j.ticket, j.state = 0, 0, stFree
	j.reset()
	j.prev, j.next = none, p.free
	p.free = i
}

// find returns the slot of the job that has key, or none. p.mu held.
func (p *Pool[In, Out]) find(key uint64) int32 {
	for i := range p.jobs {
		if j := &p.jobs[i]; j.key == key && (j.state == stQueued || j.state == stRunning || j.state == stDone) {
			return int32(i)
		}
	}
	return none
}

// drop discards the job in slot i (queued, running or done): a queued or done
// job's slot is freed, a running one is cancelled and freed when its worker
// returns. p.mu held.
func (p *Pool[In, Out]) drop(i int32) {
	j := &p.jobs[i]
	switch j.state {
	case stQueued:
		p.unlink(&p.queue, i)
		p.release(i)
	case stDone:
		p.unlink(&p.done, i)
		p.doneN.Add(-1)
		p.release(i)
	case stRunning:
		j.state, j.key = stZombie, 0
		j.set()
	default:
		return
	}
	p.live--
}

// wakeWorkerLocked unparks one idle worker, if any: a busy worker looks at the
// queue again when it finishes. p.mu held.
func (p *Pool[In, Out]) wakeWorkerLocked() {
	for i, idle := range p.idle {
		if idle {
			p.idle[i] = false
			p.parks[i].Unpark()
			return
		}
	}
}

// Submit accepts a job and returns its ticket, or ErrFull when the pool holds
// Options.Queue jobs already, or ErrClosed. It never blocks. A non-zero key
// supersedes the job that has the same key (latest wins; see the package
// comment).
func (p *Pool[In, Out]) Submit(in In, key uint64) (Ticket, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, ErrClosed
	}
	if key != 0 {
		if i := p.find(key); i != none {
			j := &p.jobs[i]
			switch j.state {
			case stQueued: // keeps its place in the line
				p.nextTicket++
				j.in, j.ticket = in, p.nextTicket
				t := j.ticket
				p.mu.Unlock()
				return t, nil
			case stDone: // an unseen result nobody will want: reuse the slot
				p.unlink(&p.done, i)
				p.doneN.Add(-1)
				var zo Out
				j.out = zo
				p.nextTicket++
				j.in, j.ticket, j.state = in, p.nextTicket, stQueued
				p.pushBack(&p.queue, i)
				p.wakeWorkerLocked()
				t := j.ticket
				p.mu.Unlock()
				return t, nil
			default: // running: cancel it; the new job takes another slot
				p.drop(i)
			}
		}
	}
	t, ok := p.enqueueLocked(in, key)
	p.mu.Unlock()
	if !ok {
		return 0, ErrFull
	}
	return t, nil
}

// enqueueLocked puts a new job in the queue. p.mu held.
func (p *Pool[In, Out]) enqueueLocked(in In, key uint64) (Ticket, bool) {
	if p.live >= p.queueCap {
		return 0, false
	}
	i := p.free // never none: live < Queue and at most Workers zombies
	j := &p.jobs[i]
	p.free = j.next
	p.nextTicket++
	j.in, j.key, j.ticket, j.state = in, key, p.nextTicket, stQueued
	p.pushBack(&p.queue, i)
	p.live++
	p.wakeWorkerLocked()
	return j.ticket, true
}

// Cancel discards the job t named, wherever it is: a queued job never runs, a
// running one sees Token.Cancelled and its result is discarded, a finished
// one's result is discarded. It reports whether a job was found.
func (p *Pool[In, Out]) Cancel(t Ticket) bool {
	if t == 0 {
		return false
	}
	p.mu.Lock()
	found := false
	for i := range p.jobs {
		j := &p.jobs[i]
		if j.ticket == t && (j.state == stQueued || j.state == stRunning || j.state == stDone) {
			p.drop(int32(i))
			found = true
			break
		}
	}
	p.mu.Unlock()
	if found {
		p.capacityFreed()
	}
	return found
}

// capacityFreed wakes the loop once if a Keep was refused for want of room: the
// declaration is retried only when a frame is built, and a cancelled job's
// discarded completion wakes nobody. Called without p.mu held.
func (p *Pool[In, Out]) capacityFreed() {
	if p.refused.Load() && p.refused.CompareAndSwap(true, false) {
		p.wake()
	}
}

// CancelKey discards the job submitted with key, like Cancel. It reports
// whether a job was found.
func (p *Pool[In, Out]) CancelKey(key uint64) bool {
	if key == 0 {
		return false
	}
	p.mu.Lock()
	i := p.find(key)
	if i != none {
		p.drop(i)
	}
	p.mu.Unlock()
	if i != none {
		p.capacityFreed()
	}
	return i != none
}

// Poll appends the finished jobs to dst, oldest first, and returns it; their
// slots are free again. It allocates only if dst's capacity is short, and it
// does not touch the pool's lock when nothing has finished.
func (p *Pool[In, Out]) Poll(dst []Result[Out]) []Result[Out] {
	if p.doneN.Load() == 0 {
		return dst
	}
	p.mu.Lock()
	for i := p.done.head; i != none; {
		j := &p.jobs[i]
		next := j.next
		dst = append(dst, Result[Out]{Ticket: j.ticket, Key: j.key, Out: j.out})
		p.release(i)
		p.live--
		i = next
	}
	p.done = list{none, none}
	p.doneN.Store(0)
	p.mu.Unlock()
	p.capacityFreed()
	return dst
}

// Pending is the number of accepted jobs whose results the owner has not
// polled (queued, running or finished).
func (p *Pool[In, Out]) Pending() int {
	p.mu.Lock()
	n := p.live
	p.mu.Unlock()
	return n
}

// Keep declares that the work named key should exist, called from Build every
// frame the declaration holds (Iced's subscriptions). The first call starts the
// work with in (a Submit with that key); later calls, while the declaration
// lasts, do nothing, whether the work has finished or not, so one declaration
// delivers one result. The input of a later call is ignored: give different
// inputs different keys. It returns the work's ticket, or ErrFull (nothing is
// recorded, so a later frame tries again; the pool calls Options.Wake once when
// room appears, by a Poll, Cancel or Sweep, so an app that builds frames only on
// events does get that frame), or ErrKey for key 0, or ErrClosed.
//
// A key that is declared in one frame and not in the next is cancelled by the
// Sweep at the end of that next frame; Keep and Sweep must be called from the
// same goroutine. Do not mix Keep and Submit on one key.
func (p *Pool[In, Out]) Keep(key uint64, in In) (Ticket, error) {
	if key == 0 {
		return 0, ErrKey
	}
	if p.shut.Load() {
		return 0, ErrClosed
	}
	for i := range p.kept {
		if k := &p.kept[i]; k.key == key {
			k.seen = true
			return k.ticket, nil
		}
	}
	t, err := p.Submit(in, key)
	if err != nil {
		if err == ErrFull {
			p.refused.Store(true) // capacityFreed wakes the loop when room appears
		}
		return 0, err
	}
	p.kept = append(p.kept, kept{key: key, ticket: t, seen: true})
	return t, nil
}

// Sweep ends a frame of Keep calls: every key kept earlier and not declared
// since the previous Sweep is cancelled (as CancelKey does) and forgotten. Call
// it once after Build.
func (p *Pool[In, Out]) Sweep() {
	n := 0
	for i := range p.kept {
		k := p.kept[i]
		if !k.seen {
			p.CancelKey(k.key)
			continue
		}
		k.seen = false
		p.kept[n] = k
		n++
	}
	p.kept = p.kept[:n]
}

// Close stops accepting jobs, discards everything queued or finished, cancels
// the running jobs (their Tokens report it) and waits up to wait for the workers
// to return. It reports whether they all did. Workers still inside the function
// after that are detached: they finish on their own, their results are
// discarded and they exit; Go cannot stop them sooner. Close may be called more
// than once.
func (p *Pool[In, Out]) Close(wait time.Duration) bool {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		p.shut.Store(true)
		for i := range p.jobs {
			p.drop(int32(i))
		}
	}
	p.mu.Unlock()
	for _, k := range p.parks {
		k.Unpark()
	}
	finished := make(chan struct{})
	go func() { p.wg.Wait(); close(finished) }()
	t := time.NewTimer(wait)
	defer t.Stop()
	ok := false
	select {
	case <-finished:
		ok = true
	case <-t.C:
	}
	for _, k := range p.parks {
		k.Close()
	}
	return ok
}

func (p *Pool[In, Out]) worker(w int) {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		i := p.queue.head
		if i == none {
			p.idle[w] = true
			p.mu.Unlock()
			if !p.parks[w].Park() {
				return
			}
			continue
		}
		p.unlink(&p.queue, i)
		j := &p.jobs[i]
		j.state = stRunning
		in := j.in
		p.mu.Unlock()

		out := p.fn(Token{&j.cancelState}, in)

		p.mu.Lock()
		deliver := j.state == stRunning && !p.closed
		if deliver {
			j.out, j.state = out, stDone
			var zi In
			j.in = zi
			p.pushBack(&p.done, i)
			p.doneN.Add(1)
		} else {
			p.release(i) // a zombie, or the pool closed
		}
		p.mu.Unlock()
		if deliver {
			p.wake()
		}
	}
}
