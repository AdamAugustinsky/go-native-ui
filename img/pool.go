package img

import (
	"unsafe"

	"gonativeui/core"
)

// pixbuf is a pooled buffer: decoded pixels, or a copy of inline source
// bytes. Its memory is C memory on macOS (outside the Go heap, so decoded
// images do not pace the collector) and a Go array elsewhere.
type pixbuf struct {
	mem  unsafe.Pointer
	size int
	keep []byte // the Go array (not macOS), kept reachable here
}

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
	if len(p.unused) == 0 {
		p.mu.Unlock()
		return -1
	}
	i := p.unused[len(p.unused)-1]
	p.unused = p.unused[:len(p.unused)-1]
	p.inUse += int64(n)
	p.mu.Unlock()
	b := &p.bufs[i]
	b.mem, b.keep = allocMem(n, fr)
	b.size = n
	return i
}

// bytes is buffer i's memory, n bytes long.
func (p *bufPool) bytes(i int32, n int) []byte {
	return unsafe.Slice((*byte)(p.bufs[i].mem), n)
}

// put returns buffer i. Free memory beyond the pool's limits is released,
// largest first.
func (p *bufPool) put(i int32, fr *cframe) {
	p.mu.Lock()
	b := &p.bufs[i]
	p.inUse -= int64(b.size)
	p.free = append(p.free, i)
	p.freeBytes += int64(b.size)
	for len(p.free) > 0 && (p.freeBytes > p.maxFree || len(p.free) > maxFreeBufs) {
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
		freeMem(d.mem, fr)
		*d = pixbuf{}
		p.unused = append(p.unused, j)
	}
	p.mu.Unlock()
}

// drain releases every free buffer (Close).
func (p *bufPool) drain(fr *cframe) {
	p.mu.Lock()
	for _, j := range p.free {
		d := &p.bufs[j]
		freeMem(d.mem, fr)
		*d = pixbuf{}
		p.unused = append(p.unused, j)
	}
	p.free, p.freeBytes = p.free[:0], 0
	p.mu.Unlock()
}
