package app

import (
	"testing"
	"time"
)

func TestStallDecisions(t *testing.T) {
	const s = int64(time.Second)
	after := 2 * s
	for _, c := range []struct {
		name                          string
		p                             uint32
		since, deadline, present, now int64
		want                          bool
	}{
		{"a Build of 3 s", phaseBuild, 10 * s, 0, 9 * s, 13 * s, true},
		{"a Build of 1 s", phaseBuild, 10 * s, 0, 9 * s, 11 * s, false},
		{"frames presented meanwhile (a nested loop's)", phaseEvents, 10 * s, 0, 12*s + s/2, 13 * s, false},
		{"a pump that waits for an event", phasePump, 10 * s, 0, 9 * s, 100 * s, false},
		{"a pump 3 s past its timeout", phasePump, 10 * s, 10*s + s/10, 9 * s, 13*s + s/10, true},
		{"a pump within its timeout", phasePump, 10 * s, 20 * s, 9 * s, 15 * s, false},
		{"a menu held open", phaseMenus, 10 * s, 0, 9 * s, 100 * s, false},
		{"a dialog's loop", phaseDialogs, 10 * s, 0, 9 * s, 100 * s, false},
	} {
		if got := phaseStalled(c.p, c.since, c.deadline, c.present, c.now, after); got != c.want {
			t.Errorf("%s: phaseStalled = %v, want %v", c.name, got, c.want)
		}
	}
	if framesStalled(0, 100*s, after) {
		t.Error("nothing owed is no stall")
	}
	if !framesStalled(10*s, 12*s, after) || framesStalled(10*s, 11*s, after) {
		t.Error("a repaint owed for 2 s is a stall, for 1 s it is not")
	}
}
