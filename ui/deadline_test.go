package ui

import "testing"

func TestDeadlineFiresOnceAsAWake(t *testing.T) {
	h := newHarness(t, 100, 100)
	const at = int64(5e9)
	var fires []int64
	run := func(now int64, declared bool) (wake int64) {
		h.in.Time = now
		h.frame(func(c *Context) {
			if declared && c.Deadline(7, at) {
				fires = append(fires, now)
			}
		})
		return h.c.WakeAfter()
	}
	if w := run(1e9, true); w != at-1e9 {
		t.Errorf("wake %d, want %d", w, at-1e9)
	}
	if w := run(3e9, true); w != at-3e9 {
		t.Errorf("wake %d, want the time left %d", w, at-3e9)
	}
	if len(fires) != 0 {
		t.Fatalf("fired early: %v", fires)
	}
	if w := run(at+1, true); w != 0 || len(fires) != 1 {
		t.Errorf("at the deadline: wake %d, fires %v", w, fires)
	}
	run(at+2e9, true)
	if len(fires) != 1 {
		t.Errorf("fired again while still declared: %v", fires)
	}
	// Not declared for a frame: forgotten, so a new declaration is a new deadline.
	run(at+3e9, false)
	run(at+4e9, true)
	if len(fires) != 2 {
		t.Errorf("a redeclared deadline in the past should fire: %v", fires)
	}
}

func TestDeadlineChangedTimeRearms(t *testing.T) {
	h := newHarness(t, 100, 100)
	fired := func(now, at int64) bool {
		var f bool
		h.in.Time = now
		h.frame(func(c *Context) { f = c.Deadline(1, at) })
		return f
	}
	if !fired(10, 5) {
		t.Fatal("did not fire")
	}
	if fired(20, 5) {
		t.Fatal("fired twice")
	}
	if !fired(30, 25) {
		t.Fatal("a new time did not rearm the deadline")
	}
}

func TestDeadlineNoAllocAndNoClock(t *testing.T) {
	h := newHarness(t, 100, 100)
	h.in.Time = 0
	h.frame(func(c *Context) {
		if c.Deadline(1, 100) {
			t.Error("fired without a clock")
		}
	})
	h.in.Time = 1
	build := func(c *Context) { c.Deadline(1, 1e12); c.Deadline(2, 2e12) }
	for i := 0; i < 5; i++ {
		h.frame(build)
	}
	if a := testing.AllocsPerRun(100, func() { h.frame(build) }); a != 0 {
		t.Errorf("a frame with two pending deadlines allocates %v", a)
	}
}
