package ui

import "math"

// Trackpad scrolling as NSScrollView does it: gestures latch to a box, lock
// to an axis, and rubber-band past the edges with springs, while wheels stay
// hard-clamped (docs/PHASE6-PLAN.md, Contracts, "Scrolling"). The constants
// are fitted to a live NSScrollView driven by the same synthetic events
// (tools/appkitref/scroll.m, ui/testdata/appkit-scroll, scrollfit_test.go).

// ScrollPhase is the phase of a trackpad scroll gesture, or of the momentum
// that follows it (ScrollEvent). The values are AppKit's NSEventPhase, as a
// sequence rather than bits.
type ScrollPhase uint8

const (
	// PhaseNone: not part of a gesture (a wheel, or a device without phases).
	PhaseNone ScrollPhase = iota
	// PhaseMayBegin: fingers came to rest on the trackpad; a gesture may follow.
	PhaseMayBegin
	// PhaseBegan: the gesture (or the momentum) started.
	PhaseBegan
	// PhaseChanged: the gesture (or the momentum) moved.
	PhaseChanged
	// PhaseEnded: the fingers lifted (or the momentum ran out).
	PhaseEnded
	// PhaseCancelled: the gesture was cancelled (or fingers rested and lifted
	// without scrolling).
	PhaseCancelled
	// PhaseStationary: the fingers are down and still.
	PhaseStationary
)

// MaxScrolls is the number of scroll events a frame holds (Input.Scrolls).
const MaxScrolls = 64

// ScrollEvent is one scroll event of a frame (Input.Scrolls).
type ScrollEvent struct {
	// DX, DY are the deltas, with Input.ScrollX/ScrollY's signs and units:
	// pixels when Precise, wheel notches otherwise.
	DX, DY float32
	// Phase is the gesture's phase, Momentum the phase of the momentum that
	// follows the gesture (at most one of them is not PhaseNone).
	Phase, Momentum ScrollPhase
	// Precise: the deltas are pixels from a trackpad (or a precise wheel).
	Precise bool
	// Inverted: the device's direction is inverted ("natural" scrolling), for
	// controls that map a scroll to a value rather than to content; the deltas
	// already move the content the right way.
	Inverted bool
	// Time is the event's time in platform.Now nanoseconds, 0 if unknown.
	Time int64
	// X, Y are where the pointer was when the event came (pixels, like
	// Input.MouseX), when Pos is set: a wheel event, or the event that starts
	// a gesture, goes to the box under that point, wherever the pointer is by
	// the end of the frame. Without Pos it goes to the box under Input.MouseX,
	// MouseY.
	X, Y float32
	Pos  bool
}

// The rubber band and its spring, fitted to NSScrollView (scrollfit_test.go).
const (
	// rubberC is the slope of the band, c in the stretch d·(1 − 1/(c·x/d + 1))
	// of a pull x past an edge of a view of size d: AppKit's is 0.0737 (the
	// plan's 0.55 is UIScrollView's).
	rubberC = 0.0737
	// springK is the spring's rate in 1/s and springA how much of the
	// momentum's velocity goes into a bounce: the stretch springs back as
	// (x0 + v0·springA·t)·e^(−springK·t), AppKit's _NSElasticDeltaForTimeDelta
	// (WebKit's ScrollElasticityController constants 20/1.6 and 0.31). A
	// release from rest is x0·e^(−12.5·t), measured.
	springK = 12.5
	springA = 0.31
	// rubberStop is the stretch in pixels under which a spring is at rest,
	// once it has run rubberMinT seconds.
	rubberStop = 0.5
	rubberMinT = 0.024
	// velTimeout: deltas further apart than this carry no velocity (ns).
	velTimeout = 100e6
	// maxRubber is the number of boxes that can be stretched at once.
	maxRubber = 4
	// maxApplied is the number of boxes a frame's scrolling reports
	// movement for (Signal.Scroll): each scroll event moves one box at most,
	// so a frame's MaxScrolls events cannot fill it.
	maxApplied = MaxScrolls
)

// elasticStretch is the stretch shown for a pull of force past an edge of a
// view of size d (both signed the same way, pixels).
func elasticStretch(force, d float64) float64 {
	x := math.Abs(force)
	s := d * (1 - 1/(x*rubberC/d+1))
	return math.Copysign(s, force)
}

// reboundForce is the pull that shows stretch s: the inverse of
// elasticStretch, for a gesture that takes hold of a stretched box.
func reboundForce(s, d float64) float64 {
	x := math.Min(math.Abs(s), d*0.999)
	return math.Copysign(d/rubberC*(1/(1-x/d)-1), s)
}

// springAt is the spring's stretch t seconds after it started at x0 with
// velocity v0 (pixels per second, outward positive with x0's sign).
func springAt(x0, v0, t float64) float64 {
	return (x0 + v0*springA*t) * math.Exp(-springK*t)
}

type rbMode uint8

const (
	rbIdle   rbMode = iota
	rbFinger        // held by the fingers: force says how far they pulled
	rbSpring        // springing back
	rbHeld          // a spring the fingers stopped (MayBegin)
)

// rubber is the stretch of one box, per axis: negative past the start (the
// content pulled down or right), positive past the end.
type rubber struct {
	key       Key
	s, force  [2]float64
	x0, v0, t [2]float64
	t0        [2]int64 // when the spring started (platform.Now ns), 0 unknown
	mode      [2]rbMode
	fresh     [2]bool // the spring started this frame: it moves from the next
}

// gesture is the trackpad gesture in progress.
type gesture struct {
	fingers   bool // between Began and Ended or Cancelled
	momentum  bool // between momentum Began and Ended
	ignoreMom bool // the momentum bounced or was stopped: the rest is dropped
	seen      bool // the gesture's Began came here: its momentum may follow
	key       Key  // the box the gesture latched to, 0 before its first delta
	axis      int8 // the locked axis, -1 while undecided
	cum       [2]float64
	lastT     int64
	vel       [2]float64 // pixels per second of the last deltas
}

// scrollPhys is the trackpad state of a Context.
type scrollPhys struct {
	g       gesture
	rb      [maxRubber]rubber
	n       int
	hit     ID   // the scroll box under the pointer (hitTest)
	focused bool // the window had the keyboard focus in the last frame
	// the scroll box under the last point scrollAt walked to this frame
	aimX, aimY float32
	aimOK      bool
	aimKey     Key
	aimHit     ID
	// applied[:nApplied] is how far this frame's scrolling moved each box
	// it went to (Signal.Scroll).
	applied  [maxApplied]appliedRec
	nApplied int
}

type appliedRec struct {
	key Key
	d   [2]float32
}

// addApplied adds d pixels on axis a to what this frame's scrolling moved
// the box with key k.
func (c *Context) addApplied(k Key, a int, d float64) {
	sp := &c.sp
	i := 0
	for ; i < sp.nApplied && sp.applied[i].key != k; i++ {
	}
	if i == sp.nApplied {
		if i == len(sp.applied) {
			return // cannot happen: one box per event (maxApplied)
		}
		sp.nApplied++
		sp.applied[i] = appliedRec{key: k}
	}
	sp.applied[i].d[a] += float32(d)
}

// appliedTo is how far this frame's scrolling moved the box with key k.
func (c *Context) appliedTo(k Key) [2]float32 {
	for i := 0; i < c.sp.nApplied; i++ {
		if c.sp.applied[i].key == k {
			return c.sp.applied[i].d
		}
	}
	return [2]float32{}
}

// unstretch lets go of the stretch of the box with key k on the axes in mask
// (bit a): a jump or reveal of the box's content shows exactly its offset.
func (c *Context) unstretch(k Key, mask uint8) {
	if mask == 0 || c.sp.n == 0 {
		return
	}
	if r := c.rubberOf(k); r != nil {
		for a := 0; a < 2; a++ {
			if mask&(1<<a) != 0 {
				r.s[a], r.force[a], r.mode[a] = 0, 0, rbIdle
			}
		}
	}
}

// ScrollStretch returns how far box id is pulled past the edges of its
// content by a trackpad, in pixels: negative past the start, positive past
// the end, 0 when it is not stretched. The layout shows the content at the
// offset Scroll + ScrollStretch; scroll bars shrink their thumb by it.
func (c *Context) ScrollStretch(id ID) (x, y float64) {
	if r := c.rubberOf(c.boxes[id].key); r != nil {
		return r.s[0], r.s[1]
	}
	return 0, 0
}

// stretchOf is the stretch of box b on axis a (layout).
func (c *Context) stretchOf(b *box, a int) float64 {
	for i := 0; i < c.sp.n; i++ {
		if c.sp.rb[i].key == b.key {
			return c.sp.rb[i].s[a]
		}
	}
	return 0
}

func (c *Context) rubberOf(k Key) *rubber {
	for i := 0; i < c.sp.n; i++ {
		if c.sp.rb[i].key == k {
			return &c.sp.rb[i]
		}
	}
	return nil
}

// rubberFor returns the stretch record of k, making one: when all are taken,
// the one that is not the gesture's and stretches least is let go.
func (c *Context) rubberFor(k Key) *rubber {
	if r := c.rubberOf(k); r != nil {
		return r
	}
	if c.sp.n < maxRubber {
		c.sp.n++
	} else {
		drop, least := -1, math.Inf(1)
		for i := range c.sp.rb {
			r := &c.sp.rb[i]
			if m := math.Abs(r.s[0]) + math.Abs(r.s[1]); r.key != c.sp.g.key && m < least {
				drop, least = i, m
			}
		}
		if drop < 0 {
			drop = 0
		}
		c.sp.rb[drop] = c.sp.rb[c.sp.n-1]
	}
	r := &c.sp.rb[c.sp.n-1]
	*r = rubber{key: k}
	return r
}

// scrollInput applies the frame's scroll events in order (mouse calls it).
// When the window loses the keyboard focus, the gesture ends there: what is
// left of its momentum is dropped and every stretch springs back.
func (c *Context) scrollInput() {
	c.sp.nApplied, c.sp.aimOK = 0, false
	in := &c.in
	if in.NScrolls == 0 {
		if in.ScrollX != 0 || in.ScrollY != 0 { // set without AddScroll
			e := ScrollEvent{DX: in.ScrollX, DY: in.ScrollY, Precise: in.Precise}
			c.scrollEvent(&e)
		}
	}
	for i := 0; i < in.NScrolls; i++ {
		c.scrollEvent(&in.Scrolls[i])
	}
	if c.sp.focused && !in.Focused {
		c.sp.g = gesture{axis: -1, ignoreMom: true}
		c.release(in.Time)
	}
	c.sp.focused = in.Focused
}

func (c *Context) scrollEvent(e *ScrollEvent) {
	g := &c.sp.g
	switch {
	case e.Phase == PhaseMayBegin:
		_, hit := c.scrollAt(e)
		c.touchDown(hit, e.Time)
	case e.Phase == PhaseBegan:
		*g = gesture{fingers: true, axis: -1, seen: true}
		c.gestureDelta(e, false)
	case e.Phase == PhaseChanged || e.Phase == PhaseStationary:
		if !g.fingers {
			// A gesture whose Began went elsewhere (a hosted native view
			// took it): it is not ui's, nor is its momentum.
			g.seen = false
			return
		}
		c.gestureDelta(e, false)
	case e.Phase == PhaseEnded || e.Phase == PhaseCancelled:
		if g.fingers {
			c.gestureDelta(e, false)
		}
		g.fingers = false
		c.release(e.Time)
	case e.Momentum != PhaseNone:
		if !g.seen {
			return // the momentum of a gesture ui did not see begin
		}
		if e.Momentum == PhaseBegan && !g.ignoreMom {
			g.momentum = true
		}
		if !g.ignoreMom {
			c.gestureDelta(e, true)
		}
		if e.Momentum == PhaseEnded || e.Momentum == PhaseCancelled {
			g.momentum, g.ignoreMom = false, false
			if !g.fingers {
				g.key, g.seen = 0, false
			}
			c.release(e.Time)
		}
	default:
		c.scrollPlain(e)
	}
}

// scrollAt is the scroll box under event e (its key and its id in the last
// frame's tree): under the point it happened at (ScrollEvent.Pos), else under
// the pointer. A frame's events mostly share a point, which is walked to once.
func (c *Context) scrollAt(e *ScrollEvent) (Key, ID) {
	if !e.Pos || (c.in.Inside && e.X == c.in.MouseX && e.Y == c.in.MouseY) {
		return c.scrollKey, c.sp.hit
	}
	sp := &c.sp
	if !sp.aimOK || sp.aimX != e.X || sp.aimY != e.Y {
		hot, k, hit := c.hotID, c.scrollKey, sp.hit
		c.scrollKey, sp.hit = 0, 0
		if c.root != 0 {
			c.hitAll(e.X, e.Y)
		}
		sp.aimX, sp.aimY, sp.aimOK, sp.aimKey, sp.aimHit = e.X, e.Y, true, c.scrollKey, sp.hit
		c.hotID, c.scrollKey, sp.hit = hot, k, hit
	}
	return sp.aimKey, sp.aimHit
}

// scrollPlain applies a wheel event, or precise deltas without a gesture, to
// the box under it: hard-clamped, precise deltas at once and wheels smoothly
// (the target moves), or at once too with WheelJumps. A vertical wheel scrolls
// a box that scrolls only on X along X.
func (c *Context) scrollPlain(e *ScrollEvent) {
	key, _ := c.scrollAt(e)
	if key == 0 {
		return
	}
	v, ok := c.table.Get(uint64(key))
	if !ok {
		return
	}
	b := &c.boxes[v]
	mul := float64(1)
	if !e.Precise {
		mul = float64(c.WheelStep)
	}
	delta := [2]float64{-float64(e.DX) * mul, -float64(e.DY) * mul}
	// A wheel's notches over a box that scrolls only sideways scroll it
	// sideways: the box under the pointer takes them, and its Y would drop them.
	if !e.Precise && delta[0] == 0 && b.flags&scrollMask == ScrollX {
		delta = [2]float64{delta[1], 0}
	}
	for a := 0; a < 2; a++ {
		if delta[a] == 0 || !b.scrollOn(a) {
			continue
		}
		hi := math.Max(0, b.content[a]-float64(b.size[a]))
		old := b.scrollTgt[a]
		b.scrollTgt[a] = math.Min(math.Max(old+delta[a], 0), hi)
		if e.Precise || c.WheelJumps {
			b.scroll[a] = math.Min(math.Max(b.scroll[a]+delta[a], 0), hi)
			b.scrollTgt[a] = b.scroll[a]
		}
		c.addApplied(b.key, a, b.scrollTgt[a]-old)
	}
}

// touchDown: fingers rest on the trackpad at time now (ns, 0 unknown), over
// scroll box hit. The springs and smooth scrolls of hit and the boxes around
// it stop where they are, and what is left of a momentum is dropped.
func (c *Context) touchDown(hit ID, now int64) {
	g := &c.sp.g
	if g.momentum {
		g.ignoreMom = true
	}
	for id := hit; id != 0; id = c.boxes[id].parent {
		b := &c.boxes[id]
		if b.flags&scrollMask == 0 {
			continue
		}
		b.scrollTgt = b.scroll
		if r := c.rubberOf(b.key); r != nil {
			for a := 0; a < 2; a++ {
				if r.mode[a] == rbSpring {
					if t := springTime(r, a, now); t >= 0 {
						r.s[a] = springAt(r.x0[a], r.v0[a], t)
					}
					r.mode[a] = rbHeld
				}
			}
		}
	}
}

// release lets go, at time now (ns, 0 unknown), of every stretch the
// fingers do not hold: it springs back from rest.
func (c *Context) release(now int64) {
	g := &c.sp.g
	for i := 0; i < c.sp.n; i++ {
		r := &c.sp.rb[i]
		for a := 0; a < 2; a++ {
			held := r.mode[a] == rbHeld || (r.mode[a] == rbFinger && !(g.fingers && r.key == g.key))
			if held || (r.mode[a] == rbIdle && r.s[a] != 0) {
				c.spring(r, a, 0, now)
			}
		}
	}
}

// spring starts r springing back on axis a from its stretch with velocity v0
// at time t0 (the event's, platform.Now ns; 0 unknown). With a clock the
// spring is where it would be at the frame's time, as AppKit's is on the
// display frame after the event; without one it moves from the next frame.
func (c *Context) spring(r *rubber, a int, v0 float64, t0 int64) {
	if r.s[a] == 0 && v0 == 0 {
		r.mode[a] = rbIdle
		return
	}
	if t0 == 0 {
		t0 = c.in.Time
	}
	r.mode[a], r.x0[a], r.v0[a], r.t[a], r.t0[a], r.fresh[a] = rbSpring, r.s[a], v0, 0, t0, true
	c.animating = true
}

// springTime is how long r's spring on axis a has run at time now
// (platform.Now ns), or -1 when there is no clock to tell.
func springTime(r *rubber, a int, now int64) float64 {
	if now == 0 || r.t0[a] == 0 {
		return -1
	}
	return math.Max(0, float64(now-r.t0[a])/1e9)
}

// gestureDelta applies the delta of a gesture or momentum event to the box
// the gesture latched to, latching it first if needed (the first event with a
// delta latches, at the box under the pointer): along the locked axis, through
// the rubber band. A gesture that has latched stays with its box wherever
// the pointer goes.
func (c *Context) gestureDelta(e *ScrollEvent, momentum bool) {
	g := &c.sp.g
	mul := float64(1)
	if !e.Precise {
		mul = float64(c.WheelStep)
	}
	d := [2]float64{-float64(e.DX) * mul, -float64(e.DY) * mul}
	if d == [2]float64{} {
		return
	}
	a := g.lock(d)
	d[1-a] = 0
	g.track(e.Time, d, c.dt)
	if g.key == 0 {
		_, hit := c.scrollAt(e)
		g.key = c.latchTarget(a, hit)
		if g.key == 0 {
			return
		}
		// Fingers on another box let its held spring go on.
		for i := 0; i < c.sp.n; i++ {
			r := &c.sp.rb[i]
			for ax := 0; ax < 2; ax++ {
				if r.key != g.key && r.mode[ax] == rbHeld {
					c.spring(r, ax, 0, e.Time)
				}
			}
		}
	}
	v, ok := c.table.Get(uint64(g.key))
	if !ok {
		return // the box is gone: the rest of the gesture is lost, as in AppKit
	}
	b := &c.boxes[v]
	if !b.scrollOn(a) {
		return
	}
	if momentum {
		if r := c.rubberOf(g.key); r != nil && r.s[a] != 0 && r.mode[a] == rbSpring {
			if d[a]*r.s[a] > 0 { // outward into a spring: it plays out
				g.ignoreMom = true
				return
			}
		}
		if rem := c.push(b, a, d[a]); rem != 0 && rem*d[a] > 0 {
			// the momentum hit the edge: it bounces and is spent
			g.ignoreMom = true
			c.spring(c.rubberFor(g.key), a, g.vel[a], e.Time)
		}
		return
	}
	c.push(b, a, d[a])
}

// lock returns the axis an event with delta d moves on: the locked one, or
// while undecided the predominant one of the gesture so far (vertical on a
// tie, without locking), as NSScrollView's usesPredominantAxisScrolling.
func (g *gesture) lock(d [2]float64) int {
	if g.axis < 0 {
		g.cum[0] += math.Abs(d[0])
		g.cum[1] += math.Abs(d[1])
		switch {
		case g.cum[1] > g.cum[0]:
			g.axis = 1
		case g.cum[0] > g.cum[1]:
			g.axis = 0
		default:
			return 1
		}
	}
	return int(g.axis)
}

// track updates the gesture's velocity from an event at time t (ns) with
// delta d; without event times it uses the frame time dt.
func (g *gesture) track(t int64, d [2]float64, dt float32) {
	if t == 0 {
		if dt > 0 {
			g.vel = [2]float64{d[0] / float64(dt), d[1] / float64(dt)}
		}
		return
	}
	if el := t - g.lastT; g.lastT != 0 && el > 0 && el < velTimeout {
		s := float64(el) / 1e9
		g.vel = [2]float64{d[0] / s, d[1] / s}
	} else {
		g.vel = [2]float64{}
	}
	g.lastT = t
}

// latchTarget is the box a gesture on axis a latches to: from the scroll box
// hit (under the gesture's first delta) outward, the first that scrolls on a and has more content
// than view on it, else the first that scrolls on a (it rubber-bands, as an
// NSScrollView with a scroller does), else none.
func (c *Context) latchTarget(a int, hit ID) Key {
	var first Key
	for id := hit; id != 0; id = c.boxes[id].parent {
		b := &c.boxes[id]
		if !b.scrollOn(a) || b.flags&Disabled != 0 {
			continue
		}
		if b.content[a] > float64(b.size[a])+0.5 {
			return b.key
		}
		if first == 0 {
			first = b.key
		}
	}
	return first
}

// push moves box b by d pixels on axis a through its rubber band: a stretch
// is pulled first (by force: the band is symmetric), then the content
// scrolls up to an edge, and what goes past the edge stretches the band. It
// returns that rest (0 when the content took it all).
func (c *Context) push(b *box, a int, d float64) float64 {
	view := float64(b.size[a])
	if r := c.rubberOf(b.key); r != nil && r.s[a] != 0 {
		if r.mode[a] != rbFinger {
			r.force[a], r.mode[a] = reboundForce(r.s[a], view), rbFinger
		}
		f := r.force[a] + d
		if f*r.s[a] > 0 {
			r.force[a], r.s[a] = f, elasticStretch(f, view)
			return f
		}
		r.s[a], r.force[a], r.mode[a] = 0, 0, rbIdle // back over the edge: the rest scrolls
		d = f
	}
	hi := math.Max(0, b.content[a]-view)
	old := b.scroll[a]
	n := old + d
	rem := 0.0
	if n < 0 {
		rem, n = n, 0
	} else if n > hi {
		rem, n = n-hi, hi
	}
	b.scroll[a], b.scrollTgt[a] = n, n
	c.addApplied(b.key, a, n-old)
	if rem != 0 && view > 0 {
		r := c.rubberFor(b.key)
		r.mode[a], r.force[a], r.s[a] = rbFinger, rem, elasticStretch(rem, view)
	}
	return rem
}

// stepRubber moves the springs on by the frame time and drops the stretches
// that came to rest or whose box is gone (stepScroll calls it).
func (c *Context) stepRubber() {
	for i := 0; i < c.sp.n; {
		r := &c.sp.rb[i]
		if _, ok := c.table.Get(uint64(r.key)); !ok {
			c.dropRubber(i)
			continue
		}
		for a := 0; a < 2; a++ {
			if r.mode[a] != rbSpring {
				continue
			}
			if t := springTime(r, a, c.in.Time); t >= 0 {
				r.t[a] = t
			} else if r.fresh[a] {
				r.fresh[a] = false
			} else {
				r.t[a] += float64(c.dt)
			}
			s := springAt(r.x0[a], r.v0[a], r.t[a])
			if math.Abs(s) < rubberStop && r.t[a] >= rubberMinT {
				r.s[a], r.mode[a] = 0, rbIdle
				continue
			}
			r.s[a] = s
			c.animating = true
		}
		if r.s == [2]float64{} && r.mode == [2]rbMode{} {
			c.dropRubber(i)
			continue
		}
		i++
	}
}

func (c *Context) dropRubber(i int) {
	c.sp.n--
	c.sp.rb[i] = c.sp.rb[c.sp.n]
	c.sp.rb[c.sp.n] = rubber{}
}
