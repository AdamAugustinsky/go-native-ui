//go:build darwin

package gpu

import "testing"

func TestInstanceRingShrinks(t *testing.T) {
	r := newTestRenderer(t)
	for i := range r.slots {
		if err := r.growSlot(&r.slots[i], 100000); err != nil {
			t.Fatal(err)
		}
	}
	list := &DrawList{Quads: []Quad{solid(0, 0, 4, 4, 0xFF0000FF)}}
	out := make([]byte, 4*4*4)
	frame := func() { r.RenderToRGBA(4, 4, black, list, out) }
	for range ringSlots * (instanceCalmFrames + 1) {
		frame()
	}
	for i := range r.slots {
		if got := r.slots[i].quads; got != initialQuads {
			t.Fatalf("slot %d retains %d quads after low demand", i, got)
		}
	}
	near(t, "after shrinking", (img{4, 4, out}).at(2, 2), [4]int{255, 0, 0, 255}, 0)
	if n := testing.AllocsPerRun(100, frame); n != 0 {
		t.Fatalf("warm shrunk ring: %g allocations", n)
	}
}

func TestLazyInstanceRing(t *testing.T) {
	r := newTestRenderer(t)
	checkEmpty := func() {
		t.Helper()
		for i := range r.slots {
			s := &r.slots[i]
			if s.buf != 0 || s.mem != nil || s.quads != 0 {
				t.Errorf("unused slot %d allocated %d instance bytes", i, s.quads*quadSize)
			}
		}
	}
	checkEmpty()
	for range ringSlots { // clear-only first pass, and every other ring slot
		m := render(r, 8, 8, black)
		near(t, "clear-only", m.at(4, 4), [4]int{0, 0, 0, 255}, 0)
		if r.DrawCalls() != 0 {
			t.Fatal("clear-only frame issued a draw")
		}
	}
	checkEmpty()
	list := &DrawList{Quads: []Quad{solid(2, 2, 6, 6, 0xFF0000FF)}}
	out := make([]byte, 8*8*4)
	frame := func() { r.RenderToRGBA(8, 8, black, list, out) }
	for n := range ringSlots {
		frame()
		m := img{8, 8, out}
		near(t, "first content", m.at(4, 4), [4]int{255, 0, 0, 255}, 0)
		near(t, "content clear", m.at(0, 0), [4]int{0, 0, 0, 255}, 0)
		for i := range r.slots {
			if (r.slots[i].buf != 0) != (i <= n) {
				t.Errorf("after content frame %d: slot %d allocation is not lazy", n, i)
			}
		}
	}
	if got := testing.AllocsPerRun(100, frame); got != 0 {
		t.Errorf("warm ring: %g allocations/frame, want zero", got)
	}
}
