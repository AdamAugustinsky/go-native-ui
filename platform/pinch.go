package platform

// pinchTracker turns a pointer-gestures pinch (zwp_pointer_gesture_pinch_v1)
// into EvMagnify and EvRotate. The protocol reports the scale as an absolute
// factor since the gesture began and the rotation in degrees clockwise since
// the previous update; the events carry the change instead (Amount), like
// macOS's, and a gesture type begins only when it first moves, so a pure
// zoom never starts a rotation. It is pure so its tests run on every host.
type pinchTracker struct {
	active  bool
	scale   float32 // the absolute scale of the last update
	started [2]bool // [0] magnify, [1] rotate: a Began was sent
}

// pinchKinds pairs the tracker's slots with their event kinds.
var pinchKinds = [2]EventKind{EvMagnify, EvRotate}

// begin starts a gesture (the scale is 1 at its start).
func (p *pinchTracker) begin() { *p = pinchTracker{active: true, scale: 1} }

// update reports a step: scale is the absolute factor since begin, rotation
// the degrees clockwise since the previous step. x, y is where the pointer is.
func (p *pinchTracker) update(q *eventQueue, scale, rotation, x, y float32, mods Mods, t int64) {
	if !p.active {
		return
	}
	var amt [2]float32
	if p.scale > 0 && scale > 0 {
		amt[0] = scale/p.scale - 1
		p.scale = scale
	}
	amt[1] = -rotation // clockwise -> counterclockwise positive
	for i, a := range amt {
		if a == 0 {
			continue
		}
		ph := PhaseChanged
		if !p.started[i] {
			ph, p.started[i] = PhaseBegan, true
		}
		q.push(Event{Kind: pinchKinds[i], Amount: a, Phase: ph, X: x, Y: y, Mods: mods, Time: t})
	}
}

// end finishes the gesture, or cancels it, for each type that began.
func (p *pinchTracker) end(q *eventQueue, cancelled bool, x, y float32, mods Mods, t int64) {
	if !p.active {
		return
	}
	ph := PhaseEnded
	if cancelled {
		ph = PhaseCancelled
	}
	for i, started := range p.started {
		if started {
			q.push(Event{Kind: pinchKinds[i], Phase: ph, X: x, Y: y, Mods: mods, Time: t})
		}
	}
	*p = pinchTracker{}
}
