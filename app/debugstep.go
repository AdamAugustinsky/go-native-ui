package app

import "gonativeui/platform"

// stepState is the clock and start of Offscreen.Step.
type stepState struct {
	clock   int64
	started bool
}

// Step runs one turn of the event loop for an app with no window, so that the
// loop's rules can be tested and the debug overlay's causes recorded without
// one: it hands evs to the app as the loop does, lets ns nanoseconds of a
// simulated clock pass (the time of ui.Input.Time, so Deadline, WakeIn and the
// caret blink come due), and draws a frame, with Frame, if the pacing rules say
// one is owed. It reports whether it drew. The first call draws the first
// frame. An idle app returns false and draws nothing, which is the rule
// (ARCHITECTURE §3); the frame after an animation, a hover or a resize is drawn
// as in a window.
//
// Step and Frame can be mixed, but Frame always draws and does not know what
// was owed.
func (o *Offscreen) Step(ns int64, evs ...platform.Event) (drew bool) {
	a := o.App
	st := &a.step
	if !st.started {
		st.started, st.clock = true, 1e9 // a clock of 0 means no clock
		a.p.invalidate()
	}
	st.clock += ns
	a.tr.in.Time = st.clock
	for i := range evs {
		a.dispatch(&evs[i])
	}
	if !a.turn(st.clock) { // a due wake, then the pacer's verdict (zero pacer: unpaced, no request)
		return false
	}
	dirty := a.tr.dirty
	if a.dbg != nil {
		a.debugFrameStart()
	}
	o.Frame()
	why := a.owedReasons(a.UI.Animating(), dirty)
	if a.dbg != nil {
		a.debugOwed(why) // Frame closed the record
	}
	a.owedWhy, a.more = why, why != 0
	a.frames++
	a.p.drawn(a.more)
	a.wakeAt = 0
	if w := a.UI.WakeAfter(); w > 0 {
		a.wakeAt = st.clock + w
	}
	return true
}
