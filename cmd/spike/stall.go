package main

import (
	"time"

	"gonativeui/platform"
)

// Causes of a silence of the frame source. Only some are excused: a restart
// of the measured frames needs independent evidence that the environment
// stopped the frames, not the program.
const (
	causeLinkSilent   = "the display link stopped ticking while the main thread kept servicing its run loop (the display, not the app)"
	causeOccluded     = "the window was not visible (the OS's occlusion state)"
	causeNotRequested = "no frame was requested (a bug of the loop)"
	causeLostFrames   = "the display link ticked but no frame event came (a bug of the frame delivery)"
	causeLinkPaused   = "the display link was paused while a frame was requested (a bug of the frame source)"
	causeMainBlocked  = "the main thread was out of its run loop for over 250 ms (the app's: it held up its own frames)"
	causeUnknown      = "unknown: no window state on this OS, so no evidence that the environment stopped the frames"
)

// mainBusyLimitMs is the longest the main thread may spend outside the run
// loop (between two Waits) during a silence for the silence still to be
// the display's: a frame here takes a few milliseconds, the silence that
// starts a stall one second.
const mainBusyLimitMs = 250

// silence is what is known about one silence of the frame source.
type silenceInfo struct {
	a, b platform.DebugState // the window state when the request went silent, and when the frames came back (or the run ended)
	// mainBusyMs is the longest the main thread was outside its run loop
	// in a stretch that ended after the request went silent: the heartbeat
	// that tells a silent display from a blocked app.
	mainBusyMs float64
}

// cause says why the frame events stopped.
func (s *silenceInfo) cause() string {
	a, b := &s.a, &s.b
	switch {
	case s.mainBusyMs > mainBusyLimitMs:
		return causeMainBlocked
	case !a.Valid || !b.Valid:
		return causeUnknown
	case !a.FrameArmed:
		return causeNotRequested
	case a.LinkPaused || a.LinkPausedOS || b.LinkPaused || b.LinkPausedOS:
		return causeLinkPaused
	case !a.Visible || !b.Visible:
		return causeOccluded
	case b.Ticks-a.Ticks > 1: // the tick that ends a silence delivers its frame
		return causeLostFrames
	}
	return causeLinkSilent
}

// appBug reports whether a stall cause is the program's fault.
func appBug(cause string) bool {
	switch cause {
	case causeNotRequested, causeLostFrames, causeLinkPaused, causeMainBlocked:
		return true
	}
	return false
}

// stallExtension is how much a loop turn dt after the previous one, during
// a silence of the frame source, extends -timeout, given the extension ext
// so far: all of it while the main loop keeps turning (dt within
// mainBusyLimitMs, its heartbeat), nothing for a longer turn (the program
// stopped, not the display), and at most maxStallExtension in all. CI's
// display stalls for 20-60 s, up to three in a run (maxRestarts of them
// restart the measured frames), and a timeout that counted them failed
// runs whose every stall was excused (43, 59 and 53 s of a 180 s timeout).
func stallExtension(ext, dt int64) int64 {
	if dt <= 0 || dt > mainBusyLimitMs*int64(time.Millisecond) {
		return 0
	}
	return max(min(dt, maxStallExtension-ext), 0)
}

const maxStallExtension = maxRestarts * 60 * int64(time.Second)

// excused reports whether a cause is evidence that the environment stopped
// the frames, so that the measured frames may start over without them.
func excused(cause string) bool { return cause == causeLinkSilent || cause == causeOccluded }

// stallLog accounts the stalls of a run. An app bug, once seen, stays: no
// later stall or restart clears it.
type stallLog struct {
	Stalls       int
	LongestMs    float64
	LongestCause string
	Restarts     int
	AppBug       string  // the first stall that was the program's fault
	AppBugMs     float64 // its length
}

// end records a stall of ms with the cause and says whether the measured
// frames start over: only if they may (canRestart), for an excused cause,
// before any app bug, and at most maxRestarts times.
func (l *stallLog) end(cause string, ms float64, canRestart bool) (restart bool) {
	l.Stalls++
	if ms > l.LongestMs || l.LongestCause == "" {
		l.LongestMs, l.LongestCause = max(ms, l.LongestMs), cause
	}
	l.unpaced(cause, ms)
	if !canRestart || !excused(cause) || l.AppBug != "" || l.Restarts >= maxRestarts {
		return false
	}
	l.Restarts++
	return true
}

// unpaced records the cause of frames left unpaced at the end of the run.
func (l *stallLog) unpaced(cause string, ms float64) {
	if appBug(cause) && l.AppBug == "" {
		l.AppBug, l.AppBugMs = cause, ms
	}
}
