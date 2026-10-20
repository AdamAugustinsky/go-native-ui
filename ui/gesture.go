package ui

import "math"

// Trackpad gestures (docs/PHASE6-PLAN.md, "Keys and text input"): a pinch, a
// two-finger rotation and the two-finger double tap (smart zoom). A pinch and
// a rotation latch the way a scroll gesture does: the box under the pointer
// at the gesture's first event (Began) receives all of it, until it ends,
// wherever the fingers go, and so do its ancestors. The box must be
// hit-testable (Hoverable, Clickable, Focusable …); a pinch over nothing is
// lost. A device without gesture phases (a Windows touchpad's pinch) sends
// events with PhaseNone, each of which goes to the box under its own point.
// Signal.Magnify, Signal.Rotate and Signal.SmartMagnify report what a box got.

// GestureKind says what a GestureEvent is.
type GestureKind uint8

const (
	// GestureMagnify is a pinch: Amount is the change in scale.
	GestureMagnify GestureKind = iota
	// GestureRotate is a two-finger rotation: Amount is in radians.
	GestureRotate
	// GestureSmartMagnify is the two-finger double tap.
	GestureSmartMagnify
)

// GestureEvent is one trackpad gesture event of a frame (Input.Gestures).
type GestureEvent struct {
	// Kind is the gesture.
	Kind GestureKind
	// Phase is Began, Changed, Ended or Cancelled (ScrollPhase's values), or
	// PhaseNone from a device without phases.
	Phase ScrollPhase
	// Amount is the change in scale of a GestureMagnify (0.1 = 10 % bigger),
	// the rotation of a GestureRotate in radians (counterclockwise positive as
	// the user sees it); 0 for GestureSmartMagnify.
	Amount float32
	// X, Y is where the gesture is, in pixels like Input.MouseX.
	X, Y float32
	// Time is the event's time in platform.Now nanoseconds, 0 if unknown.
	Time int64
}

// AddGesture appends e to Gestures; it reports false, keeping nothing, when
// the frame is full (the app keeps the event for the next frame).
func (in *Input) AddGesture(e GestureEvent) bool {
	if in.NGestures >= MaxGestures {
		return false
	}
	in.Gestures[in.NGestures] = e
	in.NGestures++
	return true
}

const (
	// maxAncestry is how many levels of a box's ancestors the gesture
	// reaches, itself included.
	maxAncestry = 24
)

// gestRec is what a frame's gestures delivered to one latched box.
type gestRec struct {
	key          Key
	chain        [maxAncestry]Key // key, then its ancestors, in the previous frame's tree
	nChain       int
	mag, rot     float32
	smart        bool
	began, ended bool
	x, y         float32
}

// gestureState is the gesture bookkeeping of a Context.
type gestureState struct {
	latch  [2]Key  // the box a pinch (0) and a rotation (1) latched to
	active [2]bool // between Began and Ended
	recs   [MaxGestures]gestRec
	n      int
	// the hit-testable box under the last point hotAt walked to this frame
	aimX, aimY float32
	aimOK      bool
	aimKey     Key
}

// gestureInput applies the frame's gesture events in order (mouse calls it).
// A window that lost the keyboard focus ends the gestures in progress.
func (c *Context) gestureInput() {
	gs := &c.gs
	gs.n, gs.aimOK = 0, false
	in := &c.in
	for i := 0; i < in.NGestures; i++ {
		c.gestureEvent(&in.Gestures[i])
	}
	if !in.Focused {
		gs.latch, gs.active = [2]Key{}, [2]bool{}
	}
	for i := 0; i < gs.n; i++ {
		c.gestureChain(&gs.recs[i])
	}
}

func (c *Context) gestureEvent(e *GestureEvent) {
	gs := &c.gs
	if e.Kind == GestureSmartMagnify {
		if k := c.hotAt(e.X, e.Y); k != 0 {
			c.gestureRec(k, e).smart = true
		}
		return
	}
	i := 0 // the latch: a pinch's and a rotation's are separate
	if e.Kind == GestureRotate {
		i = 1
	}
	var k Key
	switch e.Phase {
	case PhaseNone:
		k = c.hotAt(e.X, e.Y)
	case PhaseBegan:
		gs.latch[i], gs.active[i] = c.hotAt(e.X, e.Y), true
		k = gs.latch[i]
	case PhaseChanged, PhaseStationary, PhaseEnded, PhaseCancelled:
		if !gs.active[i] { // its Began came while the window was not listening: this is where it starts
			gs.latch[i], gs.active[i] = c.hotAt(e.X, e.Y), true
		}
		k = gs.latch[i]
	default: // MayBegin: fingers resting
		return
	}
	if k != 0 {
		r := c.gestureRec(k, e)
		switch e.Kind {
		case GestureMagnify:
			r.mag += e.Amount
		case GestureRotate:
			r.rot += e.Amount
		}
		r.began = r.began || e.Phase == PhaseBegan
		r.ended = r.ended || e.Phase == PhaseEnded || e.Phase == PhaseCancelled
	}
	if e.Phase == PhaseEnded || e.Phase == PhaseCancelled {
		gs.latch[i], gs.active[i] = 0, false
	}
}

// gestureRec returns the record of box k, making one, and notes where e was.
// When the frame's records are all taken (as many as events, so never), the
// last one is reused.
func (c *Context) gestureRec(k Key, e *GestureEvent) *gestRec {
	gs := &c.gs
	for i := 0; i < gs.n; i++ {
		if gs.recs[i].key == k {
			gs.recs[i].x, gs.recs[i].y = e.X, e.Y
			return &gs.recs[i]
		}
	}
	if gs.n == len(gs.recs) {
		gs.n--
	}
	r := &gs.recs[gs.n]
	*r = gestRec{key: k, x: e.X, y: e.Y}
	gs.n++
	return r
}

// gestureChain fills in the keys of the latched box and its ancestors, from
// the tree of the previous frame.
func (c *Context) gestureChain(r *gestRec) {
	r.chain[0], r.nChain = r.key, 1
	v, ok := c.table.Get(uint64(r.key))
	if !ok {
		return
	}
	for id := c.boxes[v].parent; id != 0 && r.nChain < maxAncestry; id = c.boxes[id].parent {
		r.chain[r.nChain] = c.boxes[id].key
		r.nChain++
	}
}

// hotAt is the key of the hit-testable box under the point (x, y) in the last
// frame's layout, 0 if none: the box under the pointer when the point is the
// pointer's.
func (c *Context) hotAt(x, y float32) Key {
	if c.in.Inside && x == c.in.MouseX && y == c.in.MouseY {
		return c.hotKey
	}
	gs := &c.gs
	if !gs.aimOK || gs.aimX != x || gs.aimY != y {
		hot, k, hit, hk := c.hotID, c.scrollKey, c.sp.hit, c.hotKey
		c.hotID, c.scrollKey, c.sp.hit = 0, 0, 0
		if c.root != 0 && !math.IsNaN(float64(x)) {
			c.hitAll(x, y)
		}
		gs.aimX, gs.aimY, gs.aimOK = x, y, true
		gs.aimKey = 0
		if c.hotID != 0 {
			gs.aimKey = c.boxes[c.hotID].key
		}
		c.hotID, c.scrollKey, c.sp.hit, c.hotKey = hot, k, hit, hk
	}
	return gs.aimKey
}

// gestureOf adds what this frame's gestures delivered to box b (or to a box
// below it) to s.
func (c *Context) gestureOf(b *box, s *Signal) {
	gs := &c.gs
	for i := 0; i < gs.n; i++ {
		r := &gs.recs[i]
		in := false
		for j := 0; j < r.nChain; j++ {
			if r.chain[j] == b.key {
				in = true
				break
			}
		}
		if !in {
			continue
		}
		s.Magnify += r.mag
		s.Rotate += r.rot
		s.SmartMagnify = s.SmartMagnify || r.smart
		s.GestureBegan = s.GestureBegan || r.began
		s.GestureEnded = s.GestureEnded || r.ended
		s.GestureAt = [2]float32{r.x, r.y}
	}
}
