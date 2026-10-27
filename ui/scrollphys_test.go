package ui

import (
	"math"
	"testing"
)

// gestureRig is a 100×100 box scrolling a 100×1000 content, with the pointer
// over it; frames are 60 Hz with a clock, events carry their times.
type gestureRig struct {
	*harness
	box   ID
	now   int64
	build func(c *Context)
}

const frameNs = int64(1e9) / 60

func newGestureRig(t *testing.T) *gestureRig {
	r := &gestureRig{harness: newHarness(t, 300, 300), now: 1e9}
	r.build = func(c *Context) {
		c.NextSize(Px(100), Px(100))
		r.box = c.Open(ScrollY|Clip, "box")
		c.NextSize(Px(100), Px(1000))
		c.Box(0, "content")
		c.Close()
	}
	r.step()
	r.step()
	r.move(50, 50)
	return r
}

// step runs one frame at the next tick of the clock.
func (r *gestureRig) step() {
	r.now += frameNs
	r.in.Time = r.now
	r.frame(r.build)
}

// ev queues a scroll event (precise, at the current time) for the next frame.
func (r *gestureRig) ev(phase, momentum ScrollPhase, dx, dy float32) {
	r.in.AddScrollEvent(ScrollEvent{DX: dx, DY: dy, Phase: phase, Momentum: momentum, Precise: true, Time: r.now + frameNs/2})
}

// y is the displayed offset of box on Y (scroll plus stretch).
func (r *gestureRig) y(id ID) float64 {
	_, y := r.c.Scroll(id)
	_, s := r.c.ScrollStretch(id)
	return y + s
}

func (r *gestureRig) stretch(id ID) float64 { _, s := r.c.ScrollStretch(id); return s }

// pull rests the fingers, then pulls dy × n (one event a frame) without lifting.
func (r *gestureRig) pull(dy float32, n int) {
	r.ev(PhaseMayBegin, PhaseNone, 0, 0)
	r.step()
	for i := 0; i < n; i++ {
		p := PhaseChanged
		if i == 0 {
			p = PhaseBegan
		}
		r.ev(p, PhaseNone, 0, dy)
		r.step()
	}
}

func near64(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestRubberBandStretchAndSpring: past the top with fingers down the content
// shows the band's stretch, d·(1 − 1/(c·x/d + 1)), and the layout moves the
// content by it; lifting springs it back from rest as x0·e^(−k·t) on the
// frames' clock, to 0, and then no more frames are asked for.
func TestRubberBandStretchAndSpring(t *testing.T) {
	r := newGestureRig(t)
	r.pull(20, 10)
	if want := elasticStretch(-200, 100); !near64(r.stretch(r.box), want, 1e-9) {
		t.Fatalf("stretch %v after a 200 px pull in a 100 px view, want %v", r.stretch(r.box), want)
	}
	// the content is shown below the box's top by the stretch
	content := r.c.boxes[r.box].first
	if top := r.c.Rect(content)[1]; math.Abs(float64(top)+r.stretch(r.box)) > 1 {
		t.Fatalf("content top at %v, want %v", top, -r.stretch(r.box))
	}
	if _, y := r.c.Scroll(r.box); y != 0 {
		t.Fatalf("the scroll offset is %v while stretched; it stays clamped at 0", y)
	}
	s0 := r.stretch(r.box)
	tEnd := r.now + frameNs/2
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	frames := 0
	for r.c.Animating() || frames == 0 {
		r.step()
		frames++
		el := float64(r.now-tEnd) / 1e9
		if s, want := r.stretch(r.box), s0*math.Exp(-springK*el); s != 0 && !near64(s, want, 1e-9) {
			t.Fatalf("frame %d: stretch %v, want %v", frames, s, want)
		}
		if frames > 120 {
			t.Fatal("the spring never settles")
		}
	}
	if r.stretch(r.box) != 0 || r.y(r.box) != 0 {
		t.Fatalf("settled at %v", r.y(r.box))
	}
	// 0.5 px from |s0| at 12.5/s
	if want := math.Log(math.Abs(s0)/rubberStop) / springK * 60; math.Abs(float64(frames)-want) > 2 {
		t.Errorf("settled in %d frames, want about %.0f", frames, want)
	}
	r.step()
	if r.c.Animating() {
		t.Fatal("a settled box still asks for frames")
	}
}

// TestRubberBandIsSymmetric: pulling back inside the same gesture undoes the
// stretch by force, and past the edge the content scrolls again 1:1.
func TestRubberBandIsSymmetric(t *testing.T) {
	r := newGestureRig(t)
	r.pull(20, 10)
	for i := 0; i < 10; i++ {
		r.ev(PhaseChanged, PhaseNone, 0, -20)
		r.step()
		if want := elasticStretch(-200+20*float64(i+1), 100); !near64(r.stretch(r.box), want, 1e-6) {
			t.Fatalf("back %d: stretch %v, want %v", i+1, r.stretch(r.box), want)
		}
	}
	r.ev(PhaseChanged, PhaseNone, 0, -15)
	r.step()
	if r.stretch(r.box) != 0 || r.y(r.box) != 15 {
		t.Fatalf("past the edge: stretch %v offset %v, want 0 and 15", r.stretch(r.box), r.y(r.box))
	}
}

// TestBottomRubberBand: the band works past the end too, from scrolling into it.
func TestBottomRubberBand(t *testing.T) {
	r := newGestureRig(t)
	r.pull(-100, 9) // 900 px: exactly the end
	if r.y(r.box) != 900 || r.stretch(r.box) != 0 {
		t.Fatalf("at %v (stretch %v), want 900", r.y(r.box), r.stretch(r.box))
	}
	r.ev(PhaseChanged, PhaseNone, 0, -50)
	r.step()
	if want := elasticStretch(50, 100); !near64(r.stretch(r.box), want, 1e-9) {
		t.Fatalf("past the end: stretch %v, want %v", r.stretch(r.box), want)
	}
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	for i := 0; i < 60 && (r.c.Animating() || i == 0); i++ {
		r.step()
	}
	if r.y(r.box) != 900 {
		t.Fatalf("springs back to %v, want 900", r.y(r.box))
	}
}

// TestMayBeginHoldsTheSpring: fingers resting stop the spring (and the
// frames); lifting them without scrolling lets it go on from there; a gesture
// that begins takes hold of the stretch.
func TestMayBeginHoldsTheSpring(t *testing.T) {
	r := newGestureRig(t)
	r.pull(30, 10)
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.step()
	r.step()
	r.ev(PhaseMayBegin, PhaseNone, 0, 0)
	r.step()
	held := r.stretch(r.box)
	for i := 0; i < 5; i++ {
		r.step()
		if r.stretch(r.box) != held {
			t.Fatalf("the held stretch moved: %v → %v", held, r.stretch(r.box))
		}
	}
	if r.c.Animating() {
		t.Fatal("a held stretch asks for frames")
	}
	r.ev(PhaseCancelled, PhaseNone, 0, 0)
	tc := r.now + frameNs/2
	r.step()
	if want := held * math.Exp(-springK*float64(r.now-tc)/1e9); !near64(r.stretch(r.box), want, 1e-9) {
		t.Fatalf("resumed at %v, want %v", r.stretch(r.box), want)
	}
	// catch it again and push back: the force behind the stretch is its rebound force
	r.ev(PhaseMayBegin, PhaseNone, 0, 0)
	r.step()
	s := r.stretch(r.box)
	r.ev(PhaseBegan, PhaseNone, 0, -10)
	r.step()
	if want := elasticStretch(reboundForce(s, 100)+10, 100); !near64(r.stretch(r.box), want, 1e-6) {
		t.Fatalf("taken hold of: %v, want %v", r.stretch(r.box), want)
	}
}

// TestMayBeginStopsSmoothScroll: a wheel's glide stops where it is when
// fingers come to rest over the box.
func TestMayBeginStopsSmoothScroll(t *testing.T) {
	r := newGestureRig(t)
	r.in.AddScroll(0, -5, false) // 5 notches down: a glide of 240 px
	r.step()
	r.step()
	at := r.y(r.box)
	if at <= 0 || at >= 240 {
		t.Fatalf("the glide is at %v", at)
	}
	r.ev(PhaseMayBegin, PhaseNone, 0, 0)
	r.step()
	r.step()
	if r.y(r.box) != at || r.c.Animating() {
		t.Fatalf("after fingers rest: at %v (was %v), animating %v", r.y(r.box), at, r.c.Animating())
	}
}

// TestWheelAndPhaselessScrollAreClamped: a wheel, and precise deltas without
// a gesture (a Windows touchpad, scripts), never stretch.
func TestWheelAndPhaselessScrollAreClamped(t *testing.T) {
	r := newGestureRig(t)
	r.in.AddScroll(0, 5, false)
	r.step()
	r.in.AddScroll(0, 300, true)
	r.step()
	if r.y(r.box) != 0 || r.stretch(r.box) != 0 || r.c.Animating() {
		t.Fatalf("past the top: at %v, stretch %v, animating %v", r.y(r.box), r.stretch(r.box), r.c.Animating())
	}
	r.in.AddScroll(0, -2000, true)
	r.step()
	if r.y(r.box) != 900 || r.stretch(r.box) != 0 {
		t.Fatalf("past the end: at %v, stretch %v", r.y(r.box), r.stretch(r.box))
	}
}

// twoBoxes is a rig of two scroll boxes side by side, each 100×100 over
// 100×1000, A at x 0–100 and B at x 100–200.
func twoBoxes(t *testing.T) (*gestureRig, *ID) {
	r := newGestureRig(t)
	var b ID
	r.build = func(c *Context) {
		c.NextAxis(AxisX)
		c.NextSize(Px(200), Px(100))
		c.Open(0, "row")
		c.NextSize(Px(100), Px(100))
		r.box = c.Open(ScrollY|Clip, "a")
		c.NextSize(Px(100), Px(1000))
		c.Box(0, "ca")
		c.Close()
		c.NextSize(Px(100), Px(100))
		b = c.Open(ScrollY|Clip, "b")
		c.NextSize(Px(100), Px(1000))
		c.Box(0, "cb")
		c.Close()
		c.Close()
	}
	r.step()
	r.step()
	return r, &b
}

// TestGestureLatches: a gesture stays with the box it began over, through its
// momentum, when the pointer passes over another; the next scroll goes to
// the box under the pointer.
func TestGestureLatches(t *testing.T) {
	r, b := twoBoxes(t)
	r.move(50, 50)
	r.pull(-10, 3)
	r.move(150, 50)
	r.ev(PhaseChanged, PhaseNone, 0, -10)
	r.step()
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.step()
	r.ev(PhaseNone, PhaseBegan, 0, -10)
	r.step()
	r.ev(PhaseNone, PhaseChanged, 0, -10)
	r.step()
	if r.y(r.box) != 60 || r.y(*b) != 0 {
		t.Fatalf("A at %v, B at %v: the gesture and its momentum belong to A (want 60 and 0)", r.y(r.box), r.y(*b))
	}
	r.ev(PhaseNone, PhaseEnded, 0, 0)
	r.step()
	r.in.AddScroll(0, -1, true)
	r.step()
	r.pull(-5, 1)
	if r.y(r.box) != 60 || r.y(*b) != 6 {
		t.Fatalf("after the momentum: A at %v, B at %v, want 60 and 6", r.y(r.box), r.y(*b))
	}
}

// TestAxisLock: the gesture moves only on the axis its first events favour
// (vertical on a tie, until one wins), as usesPredominantAxisScrolling.
func TestAxisLock(t *testing.T) {
	r := newGestureRig(t)
	r.build = func(c *Context) {
		c.NextSize(Px(100), Px(100))
		r.box = c.Open(ScrollX|ScrollY|Clip, "box")
		c.NextSize(Px(1000), Px(1000))
		c.Box(0, "content")
		c.Close()
	}
	r.step()
	at := func() (float64, float64) { return r.c.Scroll(r.box) }
	r.ev(PhaseBegan, PhaseNone, -3, -10)
	r.step()
	r.ev(PhaseChanged, PhaseNone, -10, -3)
	r.step()
	if x, y := at(); x != 0 || y != 13 {
		t.Fatalf("locked vertical: at %v, %v, want 0, 13", x, y)
	}
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.ev(PhaseBegan, PhaseNone, -1, -1) // a tie: vertical, not locked
	r.step()
	r.ev(PhaseChanged, PhaseNone, -2, -1) // horizontal wins
	r.step()
	r.ev(PhaseChanged, PhaseNone, -3, -10)
	r.step()
	if x, y := at(); x != 5 || y != 14 {
		t.Fatalf("tie then horizontal: at %v, %v, want 5, 14", x, y)
	}
}

// nestedRig: an outer vertical box (100×100 over 300) holding, 50 px down,
// an inner 100×100 box scrolling on inner (its content 1000 on that axis).
func nestedRig(t *testing.T, inner Flags) (*gestureRig, *ID) {
	r := newGestureRig(t)
	var in ID
	r.build = func(c *Context) {
		c.NextSize(Px(100), Px(100))
		r.box = c.Open(ScrollY|Clip, "outer")
		c.NextSize(Px(100), Px(50))
		c.Box(0, "above")
		c.NextSize(Px(100), Px(100))
		in = c.Open(inner|Clip, "inner")
		w, h := float32(100), float32(1000)
		if inner == ScrollX {
			w, h = 1000, 100
		}
		c.NextSize(Px(w), Px(h))
		c.Box(0, "ic")
		c.Close()
		c.NextSize(Px(100), Px(150))
		c.Box(0, "below")
		c.Close()
	}
	r.step()
	r.step()
	r.move(50, 75) // over the inner box
	return r, &in
}

// TestNestedBoxesLatchTheInner: over an inner box at its top, a pull
// stretches the inner box, not the outer; crossing its edge mid-gesture does
// not hand the gesture over; a vertical gesture over an inner box that only
// scrolls horizontally goes to the outer one.
func TestNestedBoxesLatchTheInner(t *testing.T) {
	r, in := nestedRig(t, ScrollY)
	r.pull(10, 5)
	if r.stretch(*in) >= 0 || r.y(r.box) != 0 || r.stretch(r.box) != 0 {
		t.Fatalf("inner stretch %v, outer at %v (stretch %v): the inner one takes the pull", r.stretch(*in), r.y(r.box), r.stretch(r.box))
	}
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	for i := 0; i < 60; i++ {
		r.step()
	}
	r.pull(-10, 2) // inner to 20
	for i := 0; i < 6; i++ {
		r.ev(PhaseChanged, PhaseNone, 0, 10) // back up past its top
		r.step()
	}
	if _, y := r.c.Scroll(*in); y != 0 || r.stretch(*in) >= 0 || r.y(r.box) != 0 {
		t.Fatalf("crossing mid-gesture: inner at %v stretch %v, outer at %v", y, r.stretch(*in), r.y(r.box))
	}
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.step()

	r, in = nestedRig(t, ScrollX)
	r.pull(-10, 3)
	if x, _ := r.c.Scroll(*in); r.y(r.box) != 30 || x != 0 {
		t.Fatalf("a vertical gesture over a horizontal box: outer at %v, inner at %v; want 30 and 0", r.y(r.box), x)
	}
}

// TestMomentumBouncesOffTheEdge: momentum that runs into the top overshoots a
// little and springs back; the rest of it is ignored, and the box settles.
func TestMomentumBouncesOffTheEdge(t *testing.T) {
	r := newGestureRig(t)
	r.pull(-10, 10) // at 100
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.step()
	v := float32(30) // 30 px a frame toward the top: 1800 px/s
	peak := 0.0
	for i := 0; i < 40; i++ {
		m := PhaseChanged
		if i == 0 {
			m = PhaseBegan
		}
		r.ev(PhaseNone, m, 0, v)
		v *= 0.95
		r.step()
		peak = math.Min(peak, r.stretch(r.box))
	}
	r.ev(PhaseNone, PhaseEnded, 0, 0)
	for i := 0; i < 60 && r.c.Animating(); i++ {
		r.step()
		peak = math.Min(peak, r.stretch(r.box))
	}
	if r.y(r.box) != 0 || r.c.Animating() {
		t.Fatalf("settled at %v, animating %v", r.y(r.box), r.c.Animating())
	}
	// v·a·(1/k)/e at about 1600 px/s, plus the first overshoot
	t.Logf("overshoot %.2f px", peak)
	if peak > -3 || peak < -20 {
		t.Fatalf("overshoot %v px, want a slight one", peak)
	}
}

// TestScrollGestureAllocatesNothing: a whole gesture, its spring and a bounce
// allocate nothing once warm.
func TestScrollGestureAllocatesNothing(t *testing.T) {
	r := newGestureRig(t)
	seq := func() {
		r.pull(20, 5)
		r.ev(PhaseEnded, PhaseNone, 0, 0)
		for i := 0; i < 5; i++ {
			r.step()
		}
		r.ev(PhaseMayBegin, PhaseNone, 0, 0)
		r.step()
		r.ev(PhaseCancelled, PhaseNone, 0, 0)
		r.step()
		r.ev(PhaseNone, PhaseBegan, 0, 30)
		r.step()
		r.ev(PhaseNone, PhaseEnded, 0, 0)
		for i := 0; i < 40; i++ {
			r.step()
		}
	}
	seq()
	if n := testing.AllocsPerRun(5, seq); n != 0 {
		t.Fatalf("%v allocations per gesture", n)
	}
}

// TestScrollRecordsMerge: a frame keeps its events in order, one by one; a
// full frame of events that cannot merge (the start and end of gestures)
// refuses more rather than lose a transition.
func TestScrollRecordsMerge(t *testing.T) {
	var in Input
	in.AddScrollEvent(ScrollEvent{Phase: PhaseMayBegin, Precise: true})
	in.AddScrollEvent(ScrollEvent{Phase: PhaseBegan, DY: 1, Precise: true})
	in.AddScrollEvent(ScrollEvent{Phase: PhaseChanged, DY: 2, Precise: true, Time: 5})
	in.AddScrollEvent(ScrollEvent{Phase: PhaseChanged, DY: 3, Precise: true, Time: 6})
	in.AddScrollEvent(ScrollEvent{Phase: PhaseEnded, Precise: true})
	if in.NScrolls != 5 || in.Scrolls[2].DY != 2 || in.Scrolls[3].DY != 3 || in.Scrolls[3].Time != 6 || in.Scrolls[4].Phase != PhaseEnded {
		t.Fatalf("records %+v", in.Scrolls[:in.NScrolls])
	}
	in = Input{}
	refused := 0
	for i := 0; i < MaxScrolls+3; i++ {
		p := PhaseBegan
		if i%2 == 1 {
			p = PhaseEnded
		}
		if !in.AddScrollEvent(ScrollEvent{Phase: p, DY: 1, Precise: true}) {
			refused++
		}
	}
	for i, e := range in.Scrolls[:in.NScrolls] {
		if e.DY != 1 || (e.Phase == PhaseEnded) != (i%2 == 1) {
			t.Fatalf("full: record %d is %+v", i, e)
		}
	}
	if in.NScrolls != MaxScrolls || refused != 3 {
		t.Fatalf("full: %d records, %d refused", in.NScrolls, refused)
	}
}

// TestBatchingDoesNotChangeTheGesture: the same stream of events, delivered
// one a frame, all in one frame or three a frame, ends in the same place: the
// axis lock (a tie, then horizontal) and a momentum bounce off the top.
func TestBatchingDoesNotChangeTheGesture(t *testing.T) {
	type ev struct {
		phase, mom ScrollPhase
		dx, dy     float32
	}
	stream := []ev{{PhaseMayBegin, PhaseNone, 0, 0}, {PhaseBegan, PhaseNone, -1, -1}, {PhaseChanged, PhaseNone, -2, -1},
		{PhaseChanged, PhaseNone, -3, -10}, {PhaseChanged, PhaseNone, 0, 4}, {PhaseEnded, PhaseNone, 0, 0}}
	v := float32(40)
	for i := 0; i < 12; i++ {
		m := PhaseChanged
		if i == 0 {
			m = PhaseBegan
		}
		stream = append(stream, ev{PhaseNone, m, v, 0})
		v *= 0.9
	}
	stream = append(stream, ev{PhaseNone, PhaseEnded, 0, 0})
	run := func(per int) [4]float64 {
		r := newGestureRig(t)
		r.build = func(c *Context) {
			c.NextSize(Px(100), Px(100))
			r.box = c.Open(ScrollX|ScrollY|Clip, "box")
			c.NextSize(Px(1000), Px(1000))
			c.Box(0, "content")
			c.Close()
		}
		r.step()
		r.step()
		r.c.SetScroll(r.box, 0, 0)
		t0 := r.now
		for i, e := range stream {
			r.in.AddScrollEvent(ScrollEvent{DX: e.dx, DY: e.dy, Phase: e.phase, Momentum: e.mom, Precise: true,
				Time: t0 + int64(i+1)*8e6})
			if (i+1)%per == 0 || i == len(stream)-1 {
				r.now = t0 + int64(i+1)*8e6 - frameNs // step adds a frame
				r.step()
			}
		}
		r.now = t0 + int64(len(stream))*8e6 + 60e6 - frameNs // the bounce under way
		r.step()
		x, y := r.c.Scroll(r.box)
		sx, sy := r.c.ScrollStretch(r.box)
		return [4]float64{x, y, sx, sy}
	}
	one := run(1)
	if one[0] != 0 || one[1] != 1 || one[2] >= 0 {
		t.Fatalf("one event a frame: at %v, want x 0 (bouncing off the left edge) and y 1 (the tie)", one)
	}
	for _, per := range []int{3, len(stream)} {
		if got := run(per); got != one {
			t.Errorf("%d events a frame: %v, one a frame: %v", per, got, one)
		}
	}
}

// TestFocusLossEndsTheGesture: the window losing the keyboard focus in the
// middle of a pull, or while fingers rest on a spring, lets the stretch spring
// back to rest, and the old momentum is dropped.
func TestFocusLossEndsTheGesture(t *testing.T) {
	settle := func(r *gestureRig, what string) {
		t.Helper()
		for i := 0; i < 120 && (r.c.Animating() || i == 0); i++ {
			r.step()
		}
		if r.stretch(r.box) != 0 || r.c.Animating() {
			t.Fatalf("%s: stretch %v, animating %v", what, r.stretch(r.box), r.c.Animating())
		}
	}
	r := newGestureRig(t)
	r.pull(20, 10)
	r.in.Focused = false
	settle(r, "focus lost during a pull")
	r.in.Focused = true
	r.ev(PhaseNone, PhaseChanged, 0, -30) // the old gesture's momentum
	r.step()
	if r.y(r.box) != 0 {
		t.Fatalf("old momentum scrolled to %v", r.y(r.box))
	}

	r = newGestureRig(t)
	r.pull(20, 10)
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.step()
	r.ev(PhaseMayBegin, PhaseNone, 0, 0)
	r.step()
	r.in.Focused = false
	settle(r, "focus lost during a hold")
}

// TestRevealLetsGoOfTheStretch: an explicit jump or reveal of a stretched box
// shows exactly its target; AnchorScroll (a list restating its position) keeps
// the stretch.
func TestRevealLetsGoOfTheStretch(t *testing.T) {
	r := newGestureRig(t)
	r.pull(20, 10) // fingers stay down: the stretch would stay
	var content ID
	reveal := func(c *Context) {
		r.build(c)
		content = c.boxes[r.box].first
		c.ScrollIntoView(r.box, [4]float32{0, 50, 100, 150})
	}
	r.now += frameNs
	r.in.Time = r.now
	r.frame(reveal)
	for i := 0; i < 60; i++ {
		r.step()
	}
	if b := r.c.Rect(content)[1] + 150; b > 100.5 || r.stretch(r.box) != 0 {
		t.Fatalf("the revealed rectangle ends at %v in a 100 px view (stretch %v)", b, r.stretch(r.box))
	}

	r = newGestureRig(t)
	r.pull(20, 10)
	s := r.stretch(r.box)
	r.now += frameNs
	r.frame(func(c *Context) { r.build(c); c.AnchorScroll(r.box, 0, 0) })
	if r.stretch(r.box) != s {
		t.Fatalf("AnchorScroll changed the stretch: %v → %v", s, r.stretch(r.box))
	}
	r.frame(func(c *Context) { r.build(c); c.SetScroll(r.box, 0, 300) })
	if r.stretch(r.box) != 0 || r.y(r.box) != 300 {
		t.Fatalf("SetScroll: at %v, stretch %v", r.y(r.box), r.stretch(r.box))
	}
}

// TestScrollSignalPerBox: a frame whose scrolling goes to two boxes (the
// latched one's momentum, a wheel over the other) reports each box its own
// movement.
func TestScrollSignalPerBox(t *testing.T) {
	r, b := twoBoxes(t)
	r.move(50, 50)
	r.pull(-10, 2)
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.step()
	r.move(150, 50)
	r.ev(PhaseNone, PhaseBegan, 0, -7)
	r.in.AddScrollEvent(ScrollEvent{DY: -5, Precise: true, X: 150, Y: 50, Pos: true})
	var sa, sb Signal
	r.now += frameNs
	r.in.Time = r.now
	r.frame(func(c *Context) {
		r.build(c)
		sa, sb = c.Signal(r.box), c.Signal(*b)
	})
	if sa.Scroll != [2]float32{0, 7} || sb.Scroll != [2]float32{0, 5} {
		t.Fatalf("A moved %v, B %v; want 7 and 5", sa.Scroll, sb.Scroll)
	}
	if n := testing.AllocsPerRun(20, func() {
		r.ev(PhaseNone, PhaseChanged, 0, -1)
		r.in.AddScrollEvent(ScrollEvent{DY: -1, Precise: true, X: 150, Y: 50, Pos: true})
		r.step()
	}); n != 0 {
		t.Fatalf("%v allocations", n)
	}
}

// TestLatchUsesThePositionOfTheFirstEvent: a pointer move later in the frame
// than the gesture's first event does not choose the box it latches to.
func TestLatchUsesThePositionOfTheFirstEvent(t *testing.T) {
	r, b := twoBoxes(t)
	r.move(150, 50) // over B by the end of the frame
	r.in.AddScrollEvent(ScrollEvent{DY: -10, Phase: PhaseBegan, Precise: true, X: 50, Y: 50, Pos: true})
	r.step()
	if r.y(r.box) != 10 || r.y(*b) != 0 {
		t.Fatalf("A at %v, B at %v: the gesture began over A", r.y(r.box), r.y(*b))
	}
}

// TestWheelJumps: with WheelJumps (macOS) a wheel lands at once.
func TestWheelJumps(t *testing.T) {
	r := newGestureRig(t)
	r.c.WheelJumps = true
	r.in.AddScroll(0, -2, false)
	r.step()
	if r.y(r.box) != 96 || r.c.Animating() {
		t.Fatalf("at %v (animating %v), want 96 at once", r.y(r.box), r.c.Animating())
	}
}

// TestWheelScrollsASidewaysBox: a vertical wheel over a box that scrolls
// only on X scrolls it on X; precise deltas keep their axis.
func TestWheelScrollsASidewaysBox(t *testing.T) {
	r := newGestureRig(t)
	r.c.WheelJumps = true
	r.build = func(c *Context) {
		c.NextSize(Px(100), Px(100))
		r.box = c.Open(ScrollX|Clip, "row")
		c.NextSize(Px(1000), Px(100))
		c.Box(0, "content")
		c.Close()
	}
	r.step()
	r.in.AddScroll(0, -2, false)
	r.step()
	if x, _ := r.c.Scroll(r.box); x != 96 {
		t.Fatalf("at %v, want 96", x)
	}
	r.in.AddScroll(0, 5, true)
	r.step()
	if x, _ := r.c.Scroll(r.box); x != 96 {
		t.Fatalf("precise Y moved the row to %v", x)
	}
}

// TestOverflowKeepsTheGesture: a frame with more events than it holds, which
// cross from the fingers to the momentum and from a wheel to precise deltas,
// does what the same events do one a frame: the pull past the end is let go
// (the stretch springs back), and the wheel's notches and the precise pixels
// keep their units.
func TestOverflowKeepsTheGesture(t *testing.T) {
	var stream []ScrollEvent
	add := func(n int, e ScrollEvent) {
		for i := 0; i < n; i++ {
			stream = append(stream, e)
		}
	}
	at := func(e ScrollEvent) ScrollEvent { e.X, e.Y, e.Pos = 50, 50, true; return e }
	add(1, at(ScrollEvent{DY: -1, Phase: PhaseBegan, Precise: true}))
	add(69, at(ScrollEvent{DY: -2, Phase: PhaseChanged, Precise: true})) // to the end (900), then 99 px past it
	add(1, at(ScrollEvent{Phase: PhaseEnded, Precise: true}))
	add(1, at(ScrollEvent{Momentum: PhaseBegan, Precise: true}))
	add(2, at(ScrollEvent{Momentum: PhaseChanged, Precise: true}))
	add(1, at(ScrollEvent{Momentum: PhaseEnded, Precise: true}))
	add(3, at(ScrollEvent{DY: 1}))                // three notches back: 3 × 48
	add(3, at(ScrollEvent{DY: 5, Precise: true})) // 15 px
	add(2, at(ScrollEvent{DY: 1}))                // and two notches
	if len(stream) <= MaxScrolls {
		t.Fatal("the stream fits a frame")
	}
	run := func(perFrame int) (float64, float64) {
		r := newGestureRig(t)
		r.c.WheelJumps = true
		r.c.SetScroll(r.box, 0, 860)
		r.step()
		for i, e := range stream {
			e.Time = r.now + int64(i+1)*1e6
			if !r.in.AddScrollEvent(e) {
				t.Fatalf("event %d refused", i)
			}
			if (i+1)%perFrame == 0 || i == len(stream)-1 {
				r.step()
			}
		}
		for i := 0; i < 120; i++ {
			r.step()
		}
		if r.c.Animating() {
			t.Fatalf("%d events a frame: still animating, stretch %v", perFrame, r.stretch(r.box))
		}
		_, y := r.c.Scroll(r.box)
		return y, r.stretch(r.box)
	}
	want := 900.0 - 3*48 - 15 - 2*48
	for _, per := range []int{1, len(stream)} {
		if y, s := run(per); y != want || s != 0 {
			t.Errorf("%d events a frame: at %v, stretch %v; want %v, 0", per, y, s, want)
		}
	}
}

// TestEachScrollFindsItsBox: in one frame, a wheel over A and one over B
// scroll A and B; a gesture over A that ends and one that begins over B
// latch to A and B. Walking to the points allocates nothing.
func TestEachScrollFindsItsBox(t *testing.T) {
	r, b := twoBoxes(t)
	r.move(150, 50)
	r.in.AddScrollEvent(ScrollEvent{DY: -4, Precise: true, X: 50, Y: 50, Pos: true})
	r.in.AddScrollEvent(ScrollEvent{DY: -6, Precise: true, X: 150, Y: 50, Pos: true})
	r.step()
	if r.y(r.box) != 4 || r.y(*b) != 6 {
		t.Fatalf("wheels: A at %v, B at %v; want 4, 6", r.y(r.box), r.y(*b))
	}
	r.in.AddScrollEvent(ScrollEvent{DY: -10, Phase: PhaseBegan, Precise: true, X: 50, Y: 50, Pos: true})
	r.in.AddScrollEvent(ScrollEvent{Phase: PhaseEnded, Precise: true, X: 50, Y: 50, Pos: true})
	r.in.AddScrollEvent(ScrollEvent{DY: -20, Phase: PhaseBegan, Precise: true, X: 150, Y: 50, Pos: true})
	r.in.AddScrollEvent(ScrollEvent{DY: -5, Phase: PhaseChanged, Precise: true, X: 60, Y: 50, Pos: true}) // the latch holds
	r.step()
	if r.y(r.box) != 14 || r.y(*b) != 31 {
		t.Fatalf("gestures: A at %v, B at %v; want 14, 31", r.y(r.box), r.y(*b))
	}
	r.in.AddScrollEvent(ScrollEvent{Phase: PhaseEnded, Precise: true})
	r.step()
	if n := testing.AllocsPerRun(20, func() {
		r.in.AddScrollEvent(ScrollEvent{DY: -1, Precise: true, X: 50, Y: 50, Pos: true})
		r.in.AddScrollEvent(ScrollEvent{DY: -1, Precise: true, X: 150, Y: 50, Pos: true})
		r.step()
	}); n != 0 {
		t.Fatalf("%v allocations", n)
	}
}

// TestScrollSignalForEveryBox: a frame whose events go to MaxScrolls boxes
// reports each its own movement.
func TestScrollSignalForEveryBox(t *testing.T) {
	h := newHarness(t, 300, 300)
	var ids [MaxScrolls]ID
	var sig [MaxScrolls]Signal
	build := func(c *Context) {
		c.NextAxis(AxisY)
		c.Open(0, "grid")
		for row := 0; row < 8; row++ {
			c.NextAxis(AxisX)
			c.NextSize(Px(240), Px(30))
			c.Push(c.BoxKeyed(0, KeyN(c.ParentKey(), uint64(row)), nil))
			for col := 0; col < 8; col++ {
				i := row*8 + col
				c.NextSize(Px(30), Px(30))
				ids[i] = c.BoxKeyed(ScrollY|Clip, KeyN(c.ParentKey(), uint64(i)), nil)
				c.Push(ids[i])
				c.NextSize(Px(30), Px(300))
				c.Box(0, "content")
				c.Close()
				sig[i] = c.Signal(ids[i])
			}
			c.Close()
		}
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	h.move(5, 5)
	for i := range ids {
		r := h.c.Rect(ids[i])
		h.in.AddScrollEvent(ScrollEvent{DY: -float32(i%5 + 1), Precise: true, X: r[0] + 15, Y: r[1] + 15, Pos: true})
	}
	h.frame(build)
	for i := range ids {
		if want := float32(i%5 + 1); sig[i].Scroll != [2]float32{0, want} {
			t.Fatalf("box %d: Scroll %v, want %v", i, sig[i].Scroll, want)
		}
	}
}

// TestAccessibilityRevealDuringAStretch: an accessibility reveal of a box in
// a stretched view works from where the box really is in the content, and
// shows it whole.
func TestAccessibilityRevealDuringAStretch(t *testing.T) {
	r := newGestureRig(t)
	var target ID
	r.build = func(c *Context) {
		c.NextSize(Px(100), Px(100))
		c.NextAxis(AxisY)
		r.box = c.Open(ScrollY|Clip, "box")
		c.NextSize(Px(100), Px(80))
		c.Box(0, "above")
		c.NextSize(Px(100), Px(80))
		target = c.Box(0, "target") // [80, 160] in the content
		c.NextSize(Px(100), Px(840))
		c.Box(0, "below")
		c.Close()
	}
	r.step()
	r.pull(100, 25) // past the top, the fingers down
	if r.stretch(r.box) > -50 {
		t.Fatalf("stretch %v", r.stretch(r.box))
	}
	r.in.PushAction(SemRequest{Key: r.c.KeyOf(target), Action: ActScrollIntoView})
	r.step()
	for i := 0; i < 60 && r.c.Animating(); i++ {
		r.step()
	}
	b, v := r.c.Rect(target), r.c.Rect(r.box)
	if b[1] < v[1]-0.5 || b[3] > v[3]+0.5 {
		t.Fatalf("the target shows at %v–%v in a view at %v–%v (offset %v, stretch %v)", b[1], b[3], v[1], v[3], r.y(r.box), r.stretch(r.box))
	}
}

// TestGestureNotSeenToBeginIsNotUIs: the moves, end and momentum of a
// gesture whose Began ui never saw (a hosted native view took it, and AppKit
// keeps the gesture with it) scroll nothing; a gesture seen from its Began
// still scrolls, momentum included.
func TestGestureNotSeenToBeginIsNotUIs(t *testing.T) {
	r := newGestureRig(t)
	r.move(50, 50)
	r.ev(PhaseChanged, PhaseNone, 0, -10)
	r.step()
	r.ev(PhaseChanged, PhaseNone, 0, -10)
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.step()
	r.ev(PhaseNone, PhaseBegan, 0, -7)
	r.ev(PhaseNone, PhaseChanged, 0, -5)
	r.ev(PhaseNone, PhaseEnded, 0, 0)
	r.step()
	if y := r.y(r.box); y != 0 {
		t.Fatalf("a gesture that began elsewhere moved the box to %v", y)
	}
	r.ev(PhaseBegan, PhaseNone, 0, -10)
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.ev(PhaseNone, PhaseBegan, 0, -7)
	r.ev(PhaseNone, PhaseEnded, 0, 0)
	r.step()
	if y := r.y(r.box); y != 17 {
		t.Fatalf("a gesture seen from its Began, with its momentum, moved the box to %v, want 17", y)
	}
}
