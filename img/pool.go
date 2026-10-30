package img

import (
	"sync/atomic"

	"gonativeui/core"
)

// bufPool hands out buffers by index. The table has a fixed length, so a
// buffer's record never moves: its owner reads it without the lock once the
// handover (through the task pool or the lock) has ordered the write.
type bufPool struct {
	mu        core.Mutex
	bufs      []pixbuf
	unused    []int32 // records with no memory
	free      []int32 // records with memory, not in use
	freeBytes int64
	maxFree   int64
	inUse     int64 // bytes handed out

	// puts counts the buffers returned. An animation that found no buffer
	// waits for it to change (waiters counts them) instead of asking again
	// on a timer: put then calls wake, once, to start a frame that notices.
	puts    atomic.Uint64
	waiters atomic.Int32
	wake    func()
}

const maxFreeBufs = 64

func newBufPool(records int, maxFree int64) *bufPool {
	p := &bufPool{
		bufs:    make([]pixbuf, records),
		unused:  make([]int32, records),
		free:    make([]int32, 0, maxFreeBufs+1),
		maxFree: maxFree,
	}
	for i := range p.unused {
		p.unused[i] = int32(records - 1 - i)
	}
	return p
}

// get returns a buffer of at least n bytes, or -1 if every record is taken.
// A free buffer is reused when it is no more than twice too large; otherwise
// new memory is allocated (on the caller's goroutine, with frame fr).
func (p *bufPool) get(n int, fr *cframe) int32 {
	p.mu.Lock()
	best := -1
	for k, i := range p.free {
		if s := p.bufs[i].size; s >= n && s <= 2*n && (best < 0 || s < p.bufs[p.free[best]].size) {
			best = k
		}
	}
	if best >= 0 {
		i := p.free[best]
		p.free[best] = p.free[len(p.free)-1]
		p.free = p.free[:len(p.free)-1]
		p.freeBytes -= int64(p.bufs[i].size)
		p.inUse += int64(p.bufs[i].size)
		p.mu.Unlock()
		return i
	}
	if len(p.unused) == 0 && len(p.free) > 0 { // none fits, but a free buffer's record can be used again
		big := 0
		for k, j := range p.free {
			if p.bufs[j].size > p.bufs[p.free[big]].size {
				big = k
			}
		}
		j := p.free[big]
		p.free[big] = p.free[len(p.free)-1]
		p.free = p.free[:len(p.free)-1]
		d := &p.bufs[j]
		p.freeBytes -= int64(d.size)
		d.free(fr)
		*d = pixbuf{}
		p.unused = append(p.unused, j)
	}
	if len(p.unused) == 0 {
		p.mu.Unlock()
		return -1
	}
	i := p.unused[len(p.unused)-1]
	p.unused = p.unused[:len(p.unused)-1]
	p.inUse += int64(n)
	p.mu.Unlock()
	b := &p.bufs[i]
	b.alloc(n, fr)
	return i
}

// size is buffer i's capacity: what it holds in memory, which can be up to
// twice what was asked for (get). Its owner reads it without the lock.
func (p *bufPool) size(i int32) int64 { return int64(p.bufs[i].size) }

// bytes is buffer i's memory, n bytes long.
func (p *bufPool) bytes(i int32, n int) []byte {
	return p.bufs[i].bytes(n)
}

// put returns buffer i. Free memory beyond the pool's limits is released,
// largest first.
func (p *bufPool) put(i int32, fr *cframe) {
	p.mu.Lock()
	b := &p.bufs[i]
	p.inUse -= int64(b.size)
	p.free = append(p.free, i)
	p.freeBytes += int64(b.size)
	p.release(p.maxFree, fr)
	p.mu.Unlock()
	p.puts.Add(1)
	if p.waiters.Load() > 0 && p.wake != nil {
		p.wake()
	}
}

// release frees free buffers, largest first, until they hold at most limit
// bytes and there are at most maxFreeBufs of them. The lock is held.
func (p *bufPool) release(limit int64, fr *cframe) {
	for len(p.free) > 0 && (p.freeBytes > limit || len(p.free) > maxFreeBufs) {
		big := 0
		for k, j := range p.free {
			if p.bufs[j].size > p.bufs[p.free[big]].size {
				big = k
			}
		}
		j := p.free[big]
		p.free[big] = p.free[len(p.free)-1]
		p.free = p.free[:len(p.free)-1]
		d := &p.bufs[j]
		p.freeBytes -= int64(d.size)
		d.free(fr)
		*d = pixbuf{}
		p.unused = append(p.unused, j)
	}
}

// trim frees free buffers, largest first, until they hold at most limit
// bytes (and never more than the pool's own limit).
func (p *bufPool) trim(limit int64, fr *cframe) {
	p.mu.Lock()
	p.release(min(max(limit, 0), p.maxFree), fr)
	p.mu.Unlock()
}

// spare is the bytes the free buffers hold.
func (p *bufPool) spare() int64 {
	p.mu.Lock()
	n := p.freeBytes
	p.mu.Unlock()
	return n
}

// held is the number of buffers that hold memory (C memory on macOS),
// handed out or free.
func (p *bufPool) held() int {
	p.mu.Lock()
	n := len(p.bufs) - len(p.unused)
	p.mu.Unlock()
	return n
}

// drain releases every free buffer (Close).
func (p *bufPool) drain(fr *cframe) {
	p.mu.Lock()
	for _, j := range p.free {
		d := &p.bufs[j]
		d.free(fr)
		*d = pixbuf{}
		p.unused = append(p.unused, j)
	}
	p.free, p.freeBytes = p.free[:0], 0
	p.mu.Unlock()
}
