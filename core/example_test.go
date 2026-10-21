package core_test

import (
	"fmt"

	"gonativeui/core"
)

// A frame's strings live in an arena: append, keep a Span (not the slice,
// which moves when the arena grows), and Reset at the next frame. Once the
// arena has grown to the frame's size, nothing allocates.
func ExampleArena() {
	var a core.Arena
	a.Reserve(256)
	name := a.AppendString("notes")
	count := a.AppendBytes([]byte("3 items"))
	fmt.Println(a.String(name), string(a.Bytes(count)))
	a.Reset() // next frame: the capacity stays
	fmt.Println(a.Len())
	// Output:
	// notes 3 items
	// 0
}

// Worker goroutines hand fixed-size, pointer-free messages to the main thread
// through a bounded lock-free ring; the main thread (the one consumer) Pops
// them after a wake (platform.Wake). TryPush refuses when the ring is full
// instead of waiting or dropping.
func ExampleRing() {
	type done struct{ ID, Bytes uint32 }
	r := core.NewRing[done](4)
	r.TryPush(done{ID: 1, Bytes: 100}) // from any goroutine
	r.TryPush(done{ID: 2, Bytes: 250})
	for { // on the consumer
		m, ok := r.Pop()
		if !ok {
			break
		}
		fmt.Println(m.ID, m.Bytes)
	}
	// Output:
	// 1 100
	// 2 250
}

// A hash table from 64-bit keys to 32-bit values that never allocates after
// it has grown (Reserve sizes it up front).
func ExampleTable() {
	t := core.NewTable(16)
	t.Put(42, 7)
	v, ok := t.Get(42)
	_, missing := t.Get(43)
	fmt.Println(v, ok, missing)
	// Output: 7 true false
}

// A pool hands out slots by generation-tagged handles: a handle kept past its
// slot's Free never names whatever reuses the slot.
func ExamplePool() {
	p := core.NewPool(8)
	h := p.Alloc()
	fmt.Println(p.Valid(h))
	p.Free(h)
	h2 := p.Alloc() // reuses the slot
	fmt.Println(p.Valid(h), p.Valid(h2), h.Index() == h2.Index())
	// Output:
	// true
	// false true true
}
