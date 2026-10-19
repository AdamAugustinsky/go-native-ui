package base

import (
	"math"
	"testing"

	"gonativeui/ui"
)

// Trackpad gestures on a VList: the body latches and rubber-bands like any
// scroll box, its piles (zero-height boxes scrolled to their end) never take
// a gesture, and the items stay together while stretched and after the
// spring, anchored or pinned to the bottom.

// gesture queues one precise trackpad event for the next frame.
func (h *vt) gesture(phase, momentum ui.ScrollPhase, dy float32) {
	h.in.AddScrollEvent(ui.ScrollEvent{DY: dy, Phase: phase, Momentum: momentum, Precise: true})
}

// pull rests the fingers on the list and pulls dy a frame, n times.
func (h *vt) pull(dy float32, n int) {
	h.gesture(ui.PhaseMayBegin, ui.PhaseNone, 0)
	h.frame()
	for i := 0; i < n; i++ {
		p := ui.PhaseChanged
		if i == 0 {
			p = ui.PhaseBegan
		}
		h.gesture(p, ui.PhaseNone, dy)
		h.frame()
	}
}

// release lifts the fingers and runs frames until nothing moves.
func (h *vt) release() {
	h.gesture(ui.PhaseEnded, ui.PhaseNone, 0)
	for i := 0; i < 120; i++ {
		h.frame()
		if !h.c.Animating() {
			return
		}
	}
	h.t.Fatal("the list never settles")
}

func (h *vt) stretch() float64 { _, s := h.c.ScrollStretch(h.v.Body); return s }

// together checks that the items seen in the view sit one under the other.
func (h *vt) together(what string) {
	h.t.Helper()
	var prevB float32
	prev := false
	for i := h.v.First; i < h.v.Last; i++ {
		id := h.st.ID(i)
		r := h.c.Rect(h.box[id])
		seen := r[3] > 0 && r[1] < float32(h.h)
		if seen && prev && math.Abs(float64(r[1]-prevB)) > 0.5 {
			h.t.Fatalf("%s: item %d starts at %v, the one before it ends at %v", what, id, r[1], prevB)
		}
		prevB, prev = r[3], seen
	}
}

func TestVListRubberBandsAtTheTop(t *testing.T) {
	h := newVT(t, 200, VListOpts{})
	h.frames(3)
	h.pull(20, 10)
	s := h.stretch()
	if s >= 0 {
		t.Fatalf("no stretch past the top: %v", s)
	}
	if y := h.y(1000); math.Abs(float64(y)+s) > 1 {
		t.Fatalf("the first item is at %v, want %v below the top", y, -s)
	}
	h.together("stretched")
	if id, off := h.st.Anchor(); id != 1000 || off != 0 {
		t.Fatalf("the anchor moved to %d+%v", id, off)
	}
	h.release()
	if y := h.y(1000); y != 0 || h.stretch() != 0 {
		t.Fatalf("after the spring the first item is at %v (stretch %v)", y, h.stretch())
	}
	h.together("settled")
}

func TestVListRubberBandsAtTheEnd(t *testing.T) {
	h := newVT(t, 200, VListOpts{})
	h.frames(3)
	h.in.AddScroll(0, -1e6, true) // to the end
	h.frames(6)
	if !h.v.AtEnd {
		t.Fatal("not at the end")
	}
	last := h.st.ID(h.v.Len - 1)
	h.pull(-25, 8)
	s := h.stretch()
	if s <= 0 {
		t.Fatalf("no stretch past the end: %v", s)
	}
	if b := h.c.Rect(h.box[last])[3]; math.Abs(float64(b)-(float64(h.h)-s)) > 1 {
		t.Fatalf("the last item ends at %v, want %v", b, float64(h.h)-s)
	}
	h.together("stretched")
	h.release()
	if b := h.c.Rect(h.box[last])[3]; b != float32(h.h) || !h.v.AtEnd {
		t.Fatalf("after the spring the last item ends at %v (at end %v)", b, h.v.AtEnd)
	}
	h.together("settled")
}

func TestVListPinnedRubberBands(t *testing.T) {
	h := newVT(t, 200, VListOpts{StickToBottom: true})
	h.frames(4)
	last := h.st.ID(h.v.Len - 1)
	if b := h.c.Rect(h.box[last])[3]; b != float32(h.h) {
		t.Fatalf("pinned: the last item ends at %v", b)
	}
	h.pull(-25, 8)
	s := h.stretch()
	if s <= 0 {
		t.Fatalf("no stretch past the bottom of a pinned list: %v", s)
	}
	for _, k := range []uint64{3, 4} { // the piles take no gesture
		if p, ok := h.c.BoxOf(ui.KeyN(h.v.okey, k)); ok {
			if _, ps := h.c.ScrollStretch(p); ps != 0 {
				t.Fatalf("a pile is stretched: %v", ps)
			}
		}
	}
	if b := h.c.Rect(h.box[last])[3]; math.Abs(float64(b)-(float64(h.h)-s)) > 1 {
		t.Fatalf("the last item ends at %v, want %v", b, float64(h.h)-s)
	}
	h.together("stretched")
	// a message arrives while stretched: the bottom stays pinned
	h.st.Append([]uint64{5000})
	h.frames(2)
	h.release()
	if b := h.c.Rect(h.box[5000])[3]; b != float32(h.h) || !h.v.AtEnd {
		t.Fatalf("after the spring the new item ends at %v (at end %v)", b, h.v.AtEnd)
	}
	h.together("settled")
	// and a pull the other way leaves the bottom as a scroll does
	h.pull(30, 3)
	h.release()
	if h.v.AtEnd {
		t.Fatal("scrolling up from the bottom did not leave it")
	}
}
