package ui

import "testing"

// gestureScene is a viewer (Hoverable, 200×200 at the top left) holding a
// Clickable button, next to a second viewer (200×200 at x 200). The pointer
// starts over the first viewer's empty part.
type gestureScene struct {
	*harness
	a, b, btn ID
	got       map[string]Signal
}

func newGestureScene(t *testing.T) *gestureScene {
	s := &gestureScene{harness: newHarness(t, 400, 200), got: map[string]Signal{}}
	s.step()
	s.step()
	s.move(150, 150)
	return s
}

func (s *gestureScene) build(c *Context) {
	c.NextSize(Px(400), Px(200))
	c.NextAxis(AxisX)
	c.Open(0, "row")
	{
		c.NextSize(Px(200), Px(200))
		s.a = c.Open(Hoverable, "a")
		c.NextSize(Px(40), Px(40))
		s.btn = c.Box(Clickable, "btn")
		c.Close()
		c.NextSize(Px(200), Px(200))
		s.b = c.Open(Hoverable, "b")
		c.Close()
	}
	c.Close()
	s.got["a"], s.got["b"], s.got["btn"] = c.Signal(s.a), c.Signal(s.b), c.Signal(s.btn)
}

func (s *gestureScene) step() { s.frame(s.build) }

func (s *gestureScene) gesture(kind GestureKind, phase ScrollPhase, amount, x, y float32) {
	s.in.AddGesture(GestureEvent{Kind: kind, Phase: phase, Amount: amount, X: x, Y: y})
}

// TestPinchLatchesToTheBoxUnderTheFirstEvent: a pinch goes to the box under
// the pointer at Began, and stays with it when the fingers move over another
// box; a box above the latched one receives it as well; the amounts of a
// frame add up.
func TestPinchLatchesToTheBoxUnderTheFirstEvent(t *testing.T) {
	s := newGestureScene(t)
	s.gesture(GestureMagnify, PhaseBegan, 0.1, 150, 150)
	s.gesture(GestureMagnify, PhaseChanged, 0.05, 250, 150) // over the other viewer now
	s.step()
	if g := s.got["a"]; !near(g.Magnify, 0.15) || !g.GestureBegan || g.GestureEnded {
		t.Errorf("a: magnify %v began %v ended %v, want 0.15, began", g.Magnify, g.GestureBegan, g.GestureEnded)
	}
	if g := s.got["b"]; g.Magnify != 0 {
		t.Errorf("b got %v, though the pinch began over a", g.Magnify)
	}
	s.gesture(GestureMagnify, PhaseChanged, 0.2, 250, 150)
	s.move(250, 150) // the pointer itself has moved over b
	s.step()
	if g := s.got["a"]; !near(g.Magnify, 0.2) || g.GestureBegan {
		t.Errorf("a: magnify %v began %v, want 0.2 and not began", g.Magnify, g.GestureBegan)
	}
	if g := s.got["b"]; g.Magnify != 0 {
		t.Errorf("b got %v after the pointer moved over it mid-gesture", g.Magnify)
	}
	s.gesture(GestureMagnify, PhaseEnded, 0, 250, 150)
	s.step()
	if g := s.got["a"]; !g.GestureEnded {
		t.Error("a was not told the pinch ended")
	}
	// the next pinch begins over b and goes there
	s.gesture(GestureMagnify, PhaseBegan, 0.3, 250, 150)
	s.step()
	if g := s.got["b"]; !near(g.Magnify, 0.3) {
		t.Errorf("b: %v after a new pinch over it, want 0.3", g.Magnify)
	}
	if g := s.got["a"]; g.Magnify != 0 {
		t.Errorf("a: %v after the latch moved on", g.Magnify)
	}
}

// TestPinchReachesTheAncestorsOfTheBoxUnderTheFingers: the pointer is over a
// button in a viewer; the viewer gets the pinch too.
func TestPinchReachesTheAncestorsOfTheBoxUnderTheFingers(t *testing.T) {
	s := newGestureScene(t)
	s.move(20, 20) // over btn
	s.gesture(GestureMagnify, PhaseBegan, 0.25, 20, 20)
	s.step()
	for _, k := range []string{"btn", "a"} {
		if g := s.got[k]; !near(g.Magnify, 0.25) {
			t.Errorf("%s: magnify %v, want 0.25", k, g.Magnify)
		}
	}
	if g := s.got["b"]; g.Magnify != 0 {
		t.Errorf("b: %v", g.Magnify)
	}
}

// TestRotateAndPinchLatchSeparately: a rotation that begins after the pinch
// latches where it begins, and both end on their own.
func TestRotateAndPinchLatchSeparately(t *testing.T) {
	s := newGestureScene(t)
	s.gesture(GestureMagnify, PhaseBegan, 0.1, 150, 150)
	s.gesture(GestureRotate, PhaseBegan, 0.5, 250, 150)
	s.step()
	if g := s.got["a"]; !near(g.Magnify, 0.1) || g.Rotate != 0 {
		t.Errorf("a: %+v", g)
	}
	if g := s.got["b"]; !near(g.Rotate, 0.5) || g.Magnify != 0 {
		t.Errorf("b: %+v", g)
	}
	s.gesture(GestureRotate, PhaseEnded, -0.25, 250, 150)
	s.gesture(GestureMagnify, PhaseChanged, 0.1, 250, 150)
	s.step()
	if g := s.got["b"]; !near(g.Rotate, -0.25) || !g.GestureEnded || g.Magnify != 0 {
		t.Errorf("b after the rotation ended: %+v", g)
	}
	if g := s.got["a"]; !near(g.Magnify, 0.1) || g.GestureEnded {
		t.Errorf("a: %+v", g)
	}
}

// TestPinchWithoutPhasesGoesToTheBoxUnderEachEvent: a device that sends no
// phases (a Windows touchpad) latches nothing.
func TestPinchWithoutPhasesGoesToTheBoxUnderEachEvent(t *testing.T) {
	s := newGestureScene(t)
	s.gesture(GestureMagnify, PhaseNone, 0.1, 150, 150)
	s.step()
	s.gesture(GestureMagnify, PhaseNone, 0.1, 250, 150)
	s.step()
	if g := s.got["b"]; !near(g.Magnify, 0.1) {
		t.Errorf("b: %v", g.Magnify)
	}
	if g := s.got["a"]; g.Magnify != 0 || g.GestureBegan {
		t.Errorf("a: %+v", g)
	}
}

// TestSmartMagnifyGoesToTheBoxUnderTheTap: a one-shot event, no latch.
func TestSmartMagnifyGoesToTheBoxUnderTheTap(t *testing.T) {
	s := newGestureScene(t)
	s.gesture(GestureSmartMagnify, PhaseNone, 0, 250, 100)
	s.step()
	if !s.got["b"].SmartMagnify || s.got["a"].SmartMagnify {
		t.Errorf("smart magnify: a %v b %v, want only b", s.got["a"].SmartMagnify, s.got["b"].SmartMagnify)
	}
	s.step()
	if s.got["b"].SmartMagnify {
		t.Error("smart magnify reported twice")
	}
}

// TestPinchOverNothingIsLost: a pinch that begins where nothing is
// hit-testable goes nowhere, and does not latch onto what its later events
// are over.
func TestPinchOverNothingIsLost(t *testing.T) {
	s := newGestureScene(t)
	s.gesture(GestureMagnify, PhaseBegan, 0.1, 350, 500) // below the window
	s.gesture(GestureMagnify, PhaseChanged, 0.1, 150, 150)
	s.step()
	for _, k := range []string{"a", "b", "btn"} {
		if g := s.got[k]; g.Magnify != 0 {
			t.Errorf("%s got %v from a pinch that began over nothing", k, g.Magnify)
		}
	}
}

// TestPinchMissingItsBeganLatchesAtTheFirstEvent: the window was not
// listening when the gesture began.
func TestPinchMissingItsBeganLatchesAtTheFirstEvent(t *testing.T) {
	s := newGestureScene(t)
	s.gesture(GestureMagnify, PhaseChanged, 0.1, 250, 150)
	s.gesture(GestureMagnify, PhaseChanged, 0.1, 150, 150)
	s.step()
	if g := s.got["b"]; !near(g.Magnify, 0.2) {
		t.Errorf("b: %v, want 0.2", g.Magnify)
	}
}

// TestLosingTheKeyboardFocusEndsAGesture: the pinch does not stay latched
// across a window that stopped listening.
func TestLosingTheKeyboardFocusEndsAGesture(t *testing.T) {
	s := newGestureScene(t)
	s.gesture(GestureMagnify, PhaseBegan, 0.1, 150, 150)
	s.step()
	s.in.Focused = false
	s.step()
	s.in.Focused = true
	s.gesture(GestureMagnify, PhaseChanged, 0.1, 250, 150) // a Changed from a new pinch
	s.step()
	if g := s.got["b"]; !near(g.Magnify, 0.1) {
		t.Errorf("b: %v after focus returned, want a fresh latch at the new pinch", g.Magnify)
	}
}

// TestGestureFrameFull: a frame holds MaxGestures events and ClearFrame empties it.
func TestGestureFrameFull(t *testing.T) {
	var in Input
	for i := 0; i < MaxGestures; i++ {
		if !in.AddGesture(GestureEvent{Kind: GestureMagnify}) {
			t.Fatalf("gesture %d refused", i)
		}
	}
	if in.AddGesture(GestureEvent{}) {
		t.Error("a full frame took another gesture")
	}
	in.ClearFrame()
	if in.NGestures != 0 {
		t.Error("ClearFrame keeps gestures")
	}
}

// TestGestureFramesAllocateNothing: latching, resolving the ancestors and
// reporting do not allocate.
func TestGestureFramesAllocateNothing(t *testing.T) {
	s := newGestureScene(t)
	n := testing.AllocsPerRun(50, func() {
		s.gesture(GestureMagnify, PhaseBegan, 0.1, 150, 150)
		s.gesture(GestureRotate, PhaseChanged, 0.1, 150, 150)
		s.gesture(GestureMagnify, PhaseEnded, 0, 150, 150)
		s.gesture(GestureRotate, PhaseEnded, 0, 150, 150)
		s.step()
	})
	if n != 0 {
		t.Errorf("a frame with gestures allocates %.1f times", n)
	}
}
