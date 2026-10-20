package platform

import "testing"

func TestPinchTracker(t *testing.T) {
	var q eventQueue
	var p pinchTracker
	p.update(&q, 1.1, 0, 0, 0, 0, 0) // before begin: ignored
	if len(q.ev) != 0 {
		t.Fatalf("update before begin sent %+v", q.ev)
	}
	p.begin()
	p.update(&q, 1.0, 0, 5, 6, ModCtrl, 1) // nothing moved
	p.update(&q, 1.1, 0, 5, 6, 0, 2)
	p.update(&q, 1.32, 4, 5, 6, 0, 3) // grows 20 % over the last step, 4° clockwise
	p.update(&q, 1.32, 0, 5, 6, 0, 4) // nothing moved
	p.end(&q, false, 5, 6, 0, 5)
	want := []Event{
		{Kind: EvMagnify, Phase: PhaseBegan, Amount: 1.1/1 - 1},
		{Kind: EvMagnify, Phase: PhaseChanged, Amount: 1.32/1.1 - 1},
		{Kind: EvRotate, Phase: PhaseBegan, Amount: -4}, // counterclockwise positive
		{Kind: EvMagnify, Phase: PhaseEnded},
		{Kind: EvRotate, Phase: PhaseEnded},
	}
	if len(q.ev) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(q.ev), len(want), q.ev)
	}
	for i, e := range q.ev {
		w := want[i]
		if e.Kind != w.Kind || e.Phase != w.Phase || (e.Amount-w.Amount > 1e-5 || w.Amount-e.Amount > 1e-5) || e.X != 5 || e.Y != 6 {
			t.Errorf("event %d = %+v, want %+v at 5,6", i, e, w)
		}
	}
	if p.active {
		t.Error("end left the gesture active")
	}

	// A cancelled pure rotation never started a magnify; and a stray end is ignored.
	q.ev = q.ev[:0]
	p.begin()
	p.update(&q, 1, -10, 0, 0, 0, 0)
	p.end(&q, true, 0, 0, 0, 0)
	p.end(&q, true, 0, 0, 0, 0)
	if len(q.ev) != 2 || q.ev[0].Kind != EvRotate || q.ev[0].Amount != 10 || q.ev[1].Kind != EvRotate || q.ev[1].Phase != PhaseCancelled {
		t.Errorf("rotate-only gesture: %+v", q.ev)
	}
}
