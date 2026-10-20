//go:build linux

package platform

import (
	"testing"
	"time"
)

// TestWaylandPinchGestures: the compositor's pointer-gestures pinch comes out
// as EvMagnify / EvRotate at the pointer, as changes with phases, through
// libwayland and the generated protocol tables.
func TestWaylandPinchGestures(t *testing.T) {
	f := startFakeWL(t, "gestures")
	w := openFake(t, f)
	f.do("pointer 50 60")
	f.wait("ENTER", 5*time.Second)
	pumpUntil(t, w, 3*time.Second, func() bool { return f.seen("REQ zwp_pointer_gestures_v1.get_pinch_gesture") })

	var got []Event
	collect := func(n int) {
		t.Helper()
		pumpUntilEvents := time.Now().Add(5 * time.Second)
		for len(got) < n && time.Now().Before(pumpUntilEvents) {
			for _, e := range w.Wait(int64(20 * time.Millisecond)) {
				if e.Kind == EvMagnify || e.Kind == EvRotate {
					got = append(got, e)
				}
			}
		}
	}
	f.do("pinch_begin")
	f.do("pinch_update 1.5 0")
	f.do("pinch_update 1.5 10")
	f.do("pinch_end 0")
	collect(4)
	want := []struct {
		kind  EventKind
		phase ScrollPhase
		amt   float32
	}{
		{EvMagnify, PhaseBegan, 0.5},
		{EvRotate, PhaseBegan, -10}, // 10° clockwise is -10 counterclockwise
		{EvMagnify, PhaseEnded, 0},
		{EvRotate, PhaseEnded, 0},
	}
	if len(got) != len(want) {
		t.Fatalf("%d gesture events, want %d: %+v", len(got), len(want), got)
	}
	for i, e := range got {
		if e.Kind != want[i].kind || e.Phase != want[i].phase || e.Amount != want[i].amt || e.X != 50 || e.Y != 60 {
			t.Errorf("event %d = kind %v phase %v amount %v at %v,%v; want %+v at 50,60", i, e.Kind, e.Phase, e.Amount, e.X, e.Y, want[i])
		}
	}

	// A cancelled gesture says so.
	got = got[:0]
	f.do("pinch_begin")
	f.do("pinch_update 0.5 0")
	f.do("pinch_end 1")
	collect(2)
	if len(got) != 2 || got[0].Amount != -0.5 || got[1].Phase != PhaseCancelled {
		t.Errorf("cancelled pinch: %+v", got)
	}
}
