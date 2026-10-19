//go:build linux

package platform

import (
	"testing"
	"unsafe"
)

// TestWaylandFingerScrollPhases: a finger scroll's axis events become a
// gesture (Began, then Changed) that axis_stop ends; a wheel's carry no phase.
func TestWaylandFingerScrollPhases(t *testing.T) {
	w := &Window{s120: 120}
	w.wy.seatV = 8
	var args [4]uint64
	a := unsafe.Pointer(&args)
	send := func(op uint32, v ...uint32) {
		args = [4]uint64{}
		for i, x := range v {
			args[i] = uint64(x)
		}
		w.onPointer(op, a)
	}
	axis := func(src uint32, val float32) {
		send(6, src)                          // axis_source
		send(4, 0, 0, uint32(int32(val*256))) // axis(time, vertical, value)
		send(5)                               // frame
	}
	axis(1, 3)
	axis(1, 4)
	send(7, 0, 0) // axis_stop
	send(5)
	send(7, 0, 1) // the other axis' stop: nothing more
	axis(0, 10)   // a wheel
	want := []struct {
		phase ScrollPhase
		dy    float32
		flags Flags
	}{{PhaseBegan, -3, FlagPrecise}, {PhaseChanged, -4, FlagPrecise}, {PhaseEnded, 0, FlagPrecise}, {PhaseNone, -10, 0}}
	if len(w.q.ev) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(w.q.ev), len(want), w.q.ev)
	}
	for i, e := range w.q.ev {
		if e.Kind != EvScroll || e.Phase != want[i].phase || e.DY != want[i].dy || e.Flags != want[i].flags || e.Momentum != PhaseNone {
			t.Errorf("event %d: %+v, want phase %v dy %v flags %v", i, e, want[i].phase, want[i].dy, want[i].flags)
		}
	}
}

// TestWaylandScrollFrameHasBothAxes: the axis events of one wl_pointer frame
// make one scroll with both axes, whatever order the compositor sends them
// in, and a finger scroll ends only when every axis it moved has stopped. A
// seat older than version 5 (no frames) sends each axis event at once.
func TestWaylandScrollFrameHasBothAxes(t *testing.T) {
	w := &Window{s120: 120}
	w.wy.seatV = 8
	var args [4]uint64
	a := unsafe.Pointer(&args)
	send := func(op uint32, v ...uint32) {
		args = [4]uint64{}
		for i, x := range v {
			args[i] = uint64(x)
		}
		w.onPointer(op, a)
	}
	fixed := func(v float32) uint32 { return uint32(int32(v * 256)) }
	send(6, 1)              // axis_source: finger
	send(4, 0, 1, fixed(2)) // horizontal first
	send(4, 0, 0, fixed(1)) // then vertical
	send(5)
	send(6, 1)
	send(7, 0, 1) // the horizontal axis stops: the vertical one goes on
	send(5)
	send(6, 1)
	send(4, 0, 0, fixed(3))
	send(5)
	send(6, 1)
	send(7, 0, 0) // the vertical axis stops too
	send(5)
	want := []Event{
		{Kind: EvScroll, DX: -2, DY: -1, Flags: FlagPrecise, Phase: PhaseBegan},
		{Kind: EvScroll, DY: -3, Flags: FlagPrecise, Phase: PhaseChanged},
		{Kind: EvScroll, Flags: FlagPrecise, Phase: PhaseEnded},
	}
	check := func(what string, want []Event) {
		t.Helper()
		if len(w.q.ev) != len(want) {
			t.Fatalf("%s: %d events, want %d: %+v", what, len(w.q.ev), len(want), w.q.ev)
		}
		for i, e := range w.q.ev {
			g := Event{Kind: e.Kind, DX: e.DX, DY: e.DY, Flags: e.Flags, Phase: e.Phase}
			if g != want[i] {
				t.Errorf("%s: event %d: %+v, want %+v", what, i, g, want[i])
			}
		}
	}
	check("seat v8", want)

	w = &Window{s120: 120}
	w.wy.seatV = 4
	send(4, 0, 1, fixed(2))
	send(4, 0, 0, fixed(1))
	check("seat v4", []Event{{Kind: EvScroll, DX: -2}, {Kind: EvScroll, DY: -1}})
}

// TestWaylandWheelNotches: a wheel's notches (axis_value120 from seat
// version 8, axis_discrete before it) scroll as many X11 wheel clicks,
// whatever pixel value the compositor sends with them; a high-resolution
// wheel's fraction of a notch scrolls that fraction.
func TestWaylandWheelNotches(t *testing.T) {
	var args [4]uint64
	a := unsafe.Pointer(&args)
	var w *Window
	send := func(op uint32, v ...uint32) {
		args = [4]uint64{}
		for i, x := range v {
			args[i] = uint64(x)
		}
		w.onPointer(op, a)
	}
	neg := func(v int32) uint32 { return uint32(v) }
	fixed := func(v float32) uint32 { return uint32(int32(v * 256)) }
	for _, tc := range []struct {
		seat  uint32
		op    uint32 // 9 axis_value120, 8 axis_discrete
		notch int32  // in the event's units
		value float32
		want  float32
	}{
		{8, 9, 120, 15, -scrollStep},
		{8, 9, 30, 3.75, -scrollStep / 4},
		{8, 9, -240, -30, 2 * scrollStep},
		{6, 8, 2, 30, -2 * scrollStep},
	} {
		w = &Window{s120: 120}
		w.wy.seatV = tc.seat
		send(6, 0) // axis_source: wheel
		send(tc.op, 0, neg(tc.notch))
		send(4, 0, 0, fixed(tc.value))
		send(5)
		if len(w.q.ev) != 1 || w.q.ev[0].DY != tc.want || w.q.ev[0].Flags != 0 || w.q.ev[0].Phase != PhaseNone {
			t.Errorf("seat v%d, op %d, %d: %+v, want DY %v", tc.seat, tc.op, tc.notch, w.q.ev, tc.want)
		}
	}
}

// TestWaylandScrollCarriesThePointer: a scroll while the pointer is in the
// window says where it is (FlagPos).
func TestWaylandScrollCarriesThePointer(t *testing.T) {
	w := &Window{s120: 120}
	w.wy.seatV = 8
	w.ptrIn, w.ptrX, w.ptrY = true, 30, 70
	var args [4]uint64
	args[1], args[2] = 0, uint64(uint32(int32(4*256)))
	w.onPointer(4, unsafe.Pointer(&args))
	args = [4]uint64{}
	w.onPointer(5, unsafe.Pointer(&args))
	if len(w.q.ev) != 1 || w.q.ev[0].Flags&FlagPos == 0 || w.q.ev[0].X != 30 || w.q.ev[0].Y != 70 {
		t.Fatalf("%+v", w.q.ev)
	}
}
