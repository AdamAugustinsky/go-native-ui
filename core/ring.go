package core

import "sync/atomic"

// Ring is a bounded lock-free queue for many producers and one consumer
// (Vyukov's bounded queue with a single-consumer fast path). Worker
// goroutines TryPush fixed-size messages; the main thread Pops them after
// being woken (platform.Wake).
//
// T is copied by value and should be pointer-free (ids, offsets, sizes): the
// ring then adds nothing for the garbage collector to scan, and a message is
// published by one atomic store. Nothing enforces this; PointerFree[T]
// can assert it at start-up.
//
// TryPush may be called from any goroutine. Pop, Len and Drain must be
// called from the single consumer only.
type Ring[T any] struct {
	buf  []ringSlot[T]
	mask uint64
	_    [56]byte
	head atomic.Uint64 // next position producers claim
	_    [56]byte
	tail uint64 // next position the consumer reads (consumer only)
}

type ringSlot[T any] struct {
	seq atomic.Uint64
	val T
}

// NewRing returns a ring that holds at least capacity messages (rounded up
// to a power of two, minimum 2).
func NewRing[T any](capacity int) *Ring[T] {
	n := 2
	for n < capacity {
		n *= 2
	}
	r := &Ring[T]{buf: make([]ringSlot[T], n), mask: uint64(n - 1)}
	for i := range r.buf {
		r.buf[i].seq.Store(uint64(i))
	}
	return r
}

// Cap is the capacity of the ring.
func (r *Ring[T]) Cap() int { return len(r.buf) }

// TryPush enqueues v and reports false, without blocking, if the ring is
// full.
func (r *Ring[T]) TryPush(v T) bool {
	for {
		pos := r.head.Load()
		s := &r.buf[pos&r.mask]
		d := int64(s.seq.Load()) - int64(pos)
		switch {
		case d == 0:
			if r.head.CompareAndSwap(pos, pos+1) {
				s.val = v
				s.seq.Store(pos + 1)
				return true
			}
		case d < 0:
			return false // the slot still holds an unconsumed message: full
		}
		// d > 0: another producer claimed pos; reload head.
	}
}

// Pop dequeues the oldest message. ok is false when none is ready. A message
// whose producer has claimed its slot but not published it yet counts as not
// ready (later messages wait behind it).
func (r *Ring[T]) Pop() (v T, ok bool) {
	pos := r.tail
	s := &r.buf[pos&r.mask]
	if s.seq.Load() != pos+1 {
		return v, false
	}
	v = s.val
	var zero T
	s.val = zero
	s.seq.Store(pos + r.mask + 1)
	r.tail = pos + 1
	return v, true
}

// Len is the number of messages claimed by producers and not yet popped
// (some may still be in flight). Consumer only.
func (r *Ring[T]) Len() int { return int(r.head.Load() - r.tail) }
