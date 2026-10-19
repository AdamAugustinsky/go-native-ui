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
