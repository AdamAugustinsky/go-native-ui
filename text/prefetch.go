//go:build darwin || windows || linux

package text

import (
	"bytes"
	"runtime"
	"sync"

	"gonativeui/core"
)

// PrefetchTextLimit bounds the source bytes copied for one speculative run.
// Longer text uses Shape or a Shaper directly; prefetch never truncates it.
const PrefetchTextLimit = 1024

const (
	shapeFree uint8 = iota
	shapeQueued
	shapeRunning
	shapeReady
	shapeImporting
)

type shapeJob struct {
	state     uint8
	cancelled bool
	group     uint32
	key       uint64 // its entry in Prefetcher.index, 0 for none
	font      FontKey
	desc      FontDesc
	name      []byte
	result    ShapedRun
}

// Prefetcher owns a bounded pool of shapers and copied requests. Submit,
// Cancel and Poll belong to the UI thread; workers only touch their assigned
// jobs. Idle workers park without polling. One worker is the serial shaping
// fallback; multiple workers share no mutable font or native call state.
type Prefetcher struct {
	mu      core.Mutex
	jobs    []shapeJob
	index   *core.Table // live requests by group, font and bytes, to slot
	free    []int32     // free slots, a stack
	release []int32     // Cancel's scratch: ready results to close
	park    []*core.Parker
	wg      sync.WaitGroup
	wake    func()
	next    int
	queued  int
	ready   int
	closed  bool
}

// NewPrefetcher starts workers (clamped to 1..8), each on its own OS thread,
// with capacity request/result slots (clamped to workers..1024). wake runs on
// a worker when the ready queue becomes nonempty (or by Poll when its budget
// leaves ready work); it must be thread-safe and must not call the Prefetcher.
// Construction waits only for worker setup.
func NewPrefetcher(workers, capacity int, wake func()) (*Prefetcher, error) {
	workers = min(max(workers, 1), 8)
	capacity = min(max(capacity, workers), 1024)
	p := &Prefetcher{jobs: make([]shapeJob, capacity), index: core.NewTable(capacity),
		free: make([]int32, capacity), release: make([]int32, 0, capacity),
		park: make([]*core.Parker, workers), wake: wake}
	for i := range p.jobs {
		p.jobs[i].name = make([]byte, 0, PrefetchTextLimit)
		p.jobs[i].result.reserve(128)
		p.free[i] = int32(capacity - 1 - i)
	}
	started := make(chan error, workers)
	for i := range p.park {
		p.park[i] = core.NewParker()
		p.park[i].BindSimWorkerForTesting()
		p.wg.Add(1)
		go p.worker(i, started)
	}
	var first error
	for range p.park {
		if err := <-started; err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		p.Close()
		return nil, first
	}
	return p, nil
}

// Submit copies b and its font description into a free slot. group identifies
// a caller's cancellable range (for example a pane). It returns false if the
// pool is full, closed, the font is invalid or b exceeds PrefetchTextLimit.
// Cached and already queued requests succeed without adding work. No source
// listing or caller-owned bytes are held by a worker.
func (p *Prefetcher) Submit(group uint32, s *System, f FontKey, b []byte) bool {
	d, ok := s.FontDescription(f)
	if !ok || len(b) > PrefetchTextLimit {
		return false
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return false
	}
	if _, ok := s.CachedRun(f, b); ok {
		p.mu.Unlock()
		return true
	}
	key := hashRun(f, b) ^ uint64(group+1)*0xBF58476D1CE4E5B9 | 1
	v, indexed := p.index.Get(key)
	if indexed {
		if j := &p.jobs[v]; j.group == group && j.font == f && j.desc == d && bytes.Equal(j.name, b) {
			p.mu.Unlock()
			return true
		}
	}
	if len(p.free) == 0 {
		p.mu.Unlock()
		return false
	}
	i := p.free[len(p.free)-1]
	p.free = p.free[:len(p.free)-1]
	j := &p.jobs[i]
	j.group, j.font, j.desc, j.cancelled, j.key = group, f, d, false, 0
	if !indexed { // a different request under the same key goes unindexed
		j.key = key
		p.index.Put(key, uint32(i))
	}
	j.name = append(j.name[:0], b...)
	j.state = shapeQueued
	p.queued++
	wake := p.queued == 1 // a worker parks only when it finds no queued job
	p.mu.Unlock()
	if wake {
		for _, park := range p.park {
			park.Unpark()
		}
	}
	return true
}

// unindex drops job i's index entry; mu is held.
func (p *Prefetcher) unindex(i int) {
	j := &p.jobs[i]
	if j.key != 0 {
		if v, ok := p.index.Get(j.key); ok && int(v) == i {
			p.index.Delete(j.key)
		}
		j.key = 0
	}
}

// freeJob returns job i to the free stack; mu is held.
func (p *Prefetcher) freeJob(i int) {
	p.unindex(i)
	p.jobs[i].state, p.jobs[i].desc = shapeFree, FontDesc{}
	p.free = append(p.free, int32(i))
}

// Cancel discards queued and finished results for group; an in-flight native
// call may finish, but its result will be discarded. It never waits for it.
func (p *Prefetcher) Cancel(group uint32) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.release = p.release[:0]
	for i := range p.jobs {
		j := &p.jobs[i]
		if j.group != group {
			continue
		}
		switch j.state {
		case shapeQueued:
			p.queued--
			p.freeJob(i)
		case shapeRunning:
			j.cancelled = true
			p.unindex(i)
		case shapeReady:
			j.state = shapeImporting
			p.ready--
			p.unindex(i)
			p.release = append(p.release, int32(i))
		}
	}
	p.mu.Unlock()
	for _, i := range p.release {
		p.jobs[i].result.Close()
	}
	p.mu.Lock()
	for _, i := range p.release {
		p.freeJob(int(i))
	}
	p.mu.Unlock()
}

// Poll consumes at most limit results at a frame's safe point, returning the
// number successfully imported. It never waits for shaping. Released/reused
// font keys are checked by ImportRun, and failed imports fall back to Shape.
func (p *Prefetcher) Poll(s *System, limit int) int {
	imported := 0
	for i := 0; i < len(p.jobs) && limit > 0; i++ {
		p.mu.Lock()
		if p.ready == 0 || p.closed {
			p.mu.Unlock()
			break
		}
		j := &p.jobs[i]
		if j.state != shapeReady {
			p.mu.Unlock()
			continue
		}
		j.state = shapeImporting
		p.ready--
		p.mu.Unlock()
		if s.ImportRun(j.font, &j.result) != 0 {
			imported++
		}
		j.result.Close()
		p.mu.Lock()
		p.freeJob(i)
		p.mu.Unlock()
		limit--
	}
	p.mu.Lock()
	more := p.ready > 0 && !p.closed
	p.mu.Unlock()
	if more && p.wake != nil {
		p.wake()
	}
	return imported
}

// Ready reports how many results can be consumed without waiting. It is a
// snapshot; a worker may finish another result immediately afterwards.
func (p *Prefetcher) Ready() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0
	}
	return p.ready
}

func (p *Prefetcher) take() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return -1
	}
	for n := 0; n < len(p.jobs); n++ {
		i := (p.next + n) % len(p.jobs)
		if p.jobs[i].state == shapeQueued {
			p.jobs[i].state = shapeRunning
			p.queued--
			p.next = (i + 1) % len(p.jobs)
			return i
		}
	}
	return -1
}

func (p *Prefetcher) worker(index int, started chan<- error) {
	defer p.wg.Done()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewShaper()
	started <- err
	if err != nil {
		return
	}
	defer w.Close()
	if !p.park[index].StartSimWorker() {
		return
	}
	for {
		i := p.take()
		if i < 0 {
			if !p.park[index].Park() {
				return
			}
			continue
		}
		j := &p.jobs[i]
		ok := w.Shape(j.desc, j.name, &j.result)
		p.mu.Lock()
		discard := !ok || j.cancelled || p.closed
		wake := !discard && p.ready == 0
		if !discard {
			j.state = shapeReady
			p.ready++
		}
		p.mu.Unlock()
		if discard {
			j.result.Close()
			p.mu.Lock()
			if p.jobs != nil {
				p.freeJob(i)
			}
			p.mu.Unlock()
		} else if wake && p.wake != nil {
			p.wake()
		}
	}
}

// Close cancels pending work, joins workers, and releases unconsumed native
// faces and parkers. It is idempotent; do not call it from wake.
func (p *Prefetcher) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	for _, park := range p.park {
		park.Close()
	}
	p.wg.Wait()
	for i := range p.jobs {
		p.jobs[i].result.Close()
	}
	p.jobs = nil
}
