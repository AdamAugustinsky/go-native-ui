package ui

import (
	"reflect"
	"testing"
	"unsafe"

	"gonativeui/core"
	"gonativeui/gpu"
)

func TestContextInitializedByValueKeepsStyles(t *testing.T) {
	draw := func(c *Context) gpu.Quad {
		t.Helper()
		c.Begin(&Input{}, 400, 300, 1, 1.0/60)
		c.NextSize(Px(75), Px(25))
		c.NextBG(0x112233FF)
		c.Box(DrawBackground, "box")
		var list gpu.DrawList
		c.End(&list)
		if len(list.Quads) != 1 {
			t.Fatalf("got %d quads, want one styled box", len(list.Quads))
		}
		return list.Quads[0]
	}
	want := draw(New(newFake()))
	c := *New(newFake())
	if got := draw(&c); got != want {
		t.Fatalf("value-initialized Context drew %+v, want %+v", got, want)
	}
}

func TestBoxFieldsHaveExplicitLifetime(t *testing.T) {
	typ := reflect.TypeOf(box{})
	if typ.NumField() != 2 {
		t.Fatal("box must only embed persist and boxFrame; put new persistent fields in persist and per-frame fields in boxFrame")
	}
	for i, want := range []reflect.Type{reflect.TypeOf(persist{}), reflect.TypeOf(boxFrame{})} {
		field := typ.Field(i)
		if !field.Anonymous || field.Type != want {
			t.Fatalf("box field %s must embed %s; put new fields inside persist or boxFrame according to their lifetime", field.Name, want.Name())
		}
	}
}

func TestDuplicateKeysPreserveDerivation(t *testing.T) {
	h := newHarness(t, 400, 300)
	for _, n := range []int{200, 9, 210, 2, 200} {
		h.frame(func(c *Context) {
			base := HashString(c.ParentKey(), "")
			// An explicit derived identity may occupy the next chain position.
			c.BoxKeyed(0, KeyN(base, 0), nil)
			want := base
			step := uint64(0)
			for i := 0; i < n; i++ {
				for want == KeyN(base, 0) {
					want = KeyN(want, step)
					step++
				}
				var id ID
				if i%2 == 0 {
					id = c.Spacer(Px(1), Px(1))
				} else {
					id = c.Box(0, "")
				}
				if got := c.KeyOf(id); got != want {
					t.Fatalf("duplicate %d of %d: %x, want %x", i, n, got, want)
				}
				want = KeyN(want, step)
				step++
			}
		})
	}
	if !core.PointerFree[box]() || !core.PointerFree[richRec]() {
		t.Fatal("box and rich records must stay pointer-free")
	}
}

// A workload of peaks and rests (a scripted session, then a pause) trims the
// frame scratch once, in a rest, and from then on keeps what the next peak
// grew it back to: neither the rests nor the peaks allocate. Before, every rest
// trimmed it and every peak grew it again.
func TestScratchTrimDoesNotCycle(t *testing.T) {
	h := newHarness(t, 400, 300)
	peak := func(c *Context) { c.fr.Cur().Alloc(64 << 10) }
	rest := func(c *Context) { c.BoxKeyed(0, 1, nil) }
	cycle := func() {
		h.frame(peak)
		for range 100 {
			h.frame(rest)
		}
	}
	cycle()
	if c := h.c.fr.Cur().Cap(); c >= 64<<10 {
		t.Fatalf("the rest did not trim the scratch: capacity %d", c)
	}
	cycle() // its peak grows the scratch back, which the rests now keep
	if n := testing.AllocsPerRun(5, cycle); n != 0 {
		t.Fatalf("a cycle of a peak and a rest allocates %g times (capacity %d)", n, h.c.fr.Cur().Cap())
	}
}

func TestTrimRetainsKeyedStateAndWarmCapacity(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.frame(func(c *Context) {
		for i := 0; i < 50000; i++ {
			c.BoxKeyed(0, Key(i+100), nil)
		}
		_ = c.Fmt().Str(string(make([]byte, 1<<20))).String()
		c.fr.Cur().Alloc(1 << 20)
	})
	peak := cap(h.c.boxes)*int(unsafe.Sizeof(box{})) + h.c.fr.Cur().Cap() + h.c.fmtA.Cap()
	var id ID
	build := func(c *Context) {
		c.NextSize(Px(100), Px(80))
		id = c.BoxKeyed(ScrollY|Focusable, 50100, nil)
		c.Push(id)
		c.Spacer(Px(1), Px(1000))
		c.Pop()
	}
	h.frame(build)
	h.c.boxes[id].scroll, h.c.boxes[id].scrollTgt = [2]float64{0, 42}, [2]float64{0, 42}
	h.c.SetFocus(50100)
	for i := 0; i < 61; i++ {
		h.frame(build)
	}
	if cap(h.c.boxes) > 256 || cap(h.c.order) > 256 || cap(h.c.prevOrder) > 256 || h.c.table.Cap() > 512 {
		t.Fatalf("retained capacities: slab %d order %d/%d table %d", cap(h.c.boxes), cap(h.c.order), cap(h.c.prevOrder), h.c.table.Cap())
	}
	if got := h.c.boxes[id]; got.key != 50100 || got.scroll[1] != 42 || h.c.FocusKey() != 50100 || h.c.boxes[got.first].parent != id {
		t.Fatalf("trim lost keyed state: %+v", got.persist)
	}
	after := cap(h.c.boxes)*int(unsafe.Sizeof(box{})) + h.c.fr.Cur().Cap() + h.c.fmtA.Cap()
	t.Logf("slab and scratch retention: %d -> %d bytes", peak, after)
	if n := testing.AllocsPerRun(100, func() { h.frame(build) }); n != 0 {
		t.Fatalf("trimmed frames allocate %g", n)
	}
}

func TestAnimationPrunesPreviousLiveSet(t *testing.T) {
	h := newHarness(t, 10, 10)
	h.frame(func(c *Context) {
		for i := 1; i <= 10000; i++ {
			c.KeepValue(Key(i), 0, 1)
		}
	})
	for i := 0; i < 4; i++ {
		h.frame(func(c *Context) {
			if got := c.KeepValue(7, 0, 2); got == 0 {
				t.Fatal("retained value disappeared")
			}
			c.KeepValue(7, 0, 3)
			c.AnimateValue(7, 4, 20)
		})
		if len(h.c.avOrder) != 1 || h.c.avPool.Len() != 2 || h.c.avTable.Len() != 1 {
			t.Fatal("duplicate touches or stale animation values")
		}
	}
	h.frame(nil)
	if h.c.avTable.Len() != 0 {
		t.Fatal("unbuilt value survived")
	}
}
