package app

// pacer is the frame-pacing state machine of the loop. It is pure state: the
// loop feeds it events and the clock and does what it says, so its rules can
// be tested without a window.
//
// The rules (ARCHITECTURE §3, DECISIONS D18):
//
//   - Nothing is drawn unless something is owed (needDraw): input arrived, a
//     size or visibility change, the last frame left an animation running, or
//     a wake. An idle app presents nothing and blocks in Wait.
//   - A frame is drawn only when the platform has said a frame slot is free
//     (frameReady, set by EvFrame) and each draw consumes the slot. The
//     platform delivers the next EvFrame for the RequestFrame made during
//     that draw.
//   - A paced frame is valid only for its own tick (macOS hands over a
//     drawable with each tick). A draw attempt that finds no frame
//     (BeginFrame false) has not painted anything, so the repaint is still
//     owed: the slot is spent, a new one is requested, and the next EvFrame
//     redraws. This was the phase-2 display-link stall.
//   - When a requested frame does not come for silenceNs (the window is
//     occluded, the display is asleep, a link that stopped), the loop draws
//     unpaced: frames are attempted without waiting for EvFrame. If the
//     attempt fails again (nothing can be presented), it backs off by
//     retryNs instead of spinning. Any EvFrame returns the loop to paced
//     mode.
//   - While the window is hidden (minimised, covered, on another space) a
//     draw attempt that finds no frame parks the loop: no retries, no
//     waiting on the clock, until an event (the window shows, a frame
//     event, input). A covered window gets no frames to draw into, so the
//     retries would fail every retryNs, forever, and no Build would run to
//     let the animations see Input.Hidden. A hidden window that can still
//     draw (some report covered while they draw, an asleep display) draws
//     on, and its Build sees Input.Hidden. What is owed stays owed, and the
//     first frame after the window shows draws it.
type pacer struct {
	hidden      bool // the window is hidden (hide)
	parked      bool // hidden, and an attempt found no frame: wait for an event
	needDraw    bool
	frameReady  bool  // a frame slot is available
	paced       bool  // EvFrame is being delivered (false after the silence fallback)
	requestedAt int64 // when the outstanding frame request was made; 0 if none
	retryAt     int64 // do not attempt a draw before this time; 0 if none
}

const (
	silenceNs = int64(1e9)  // a requested frame this late means the frame source is silent
	retryNs   = int64(50e6) // unpaced backoff after a draw attempt that found no frame
)

// newPacer returns the state before the first frame: a frame is owed and a
// slot is assumed free (the first frame is drawn before the first Wait).
func newPacer() pacer { return pacer{needDraw: true, frameReady: true, paced: true} }

// invalidate says a repaint is owed (and a parked loop tries again).
func (p *pacer) invalidate() { p.needDraw, p.parked = true, false }

// frameEvent handles EvFrame: a slot is free, the frame source is alive.
func (p *pacer) frameEvent() {
	p.frameReady, p.paced, p.parked = true, true, false
	p.requestedAt, p.retryAt = 0, 0
}

// hide records that the window is hidden (on) or shows again. Shown, a
// repaint is owed, and the frame source is asked afresh, its silence while
// hidden being no evidence that it stopped.
func (p *pacer) hide(on bool) {
	if p.hidden == on {
		return
	}
	p.hidden, p.parked = on, false
	if !on {
		p.needDraw, p.retryAt = true, 0
		if !p.paced || !p.frameReady { // unless a frame event left a slot
			p.paced, p.frameReady, p.requestedAt = true, false, 0
		}
	}
}

// requested records that the loop called RequestFrame at now.
func (p *pacer) requested(now int64) { p.requestedAt = now }

// timeout is the Wait timeout in ns: -1 blocks until an event (idle, or a
// frame is awaited with nothing else to time), 0 polls (a draw can happen
// now).
func (p *pacer) timeout(now int64) int64 {
	if !p.needDraw || p.parked {
		return -1
	}
	if p.frameReady {
		if p.retryAt > now {
			return p.retryAt - now
		}
		return 0
	}
	if !p.paced || p.requestedAt == 0 {
		return 0 // poll will ask for a request
	}
	return max(p.requestedAt+silenceNs-now, 0) // wake in time to fall back to unpaced
}

// poll decides what to do after Wait returned and its events were handled.
// draw says to attempt a draw now; request says to call RequestFrame (and
// tell requested) because a frame is owed with none outstanding.
func (p *pacer) poll(now int64) (draw, request bool) {
	if !p.needDraw || p.parked {
		return false, false
	}
	if !p.frameReady {
		if p.paced {
			if p.requestedAt == 0 {
				return false, true
			}
			if now-p.requestedAt < silenceNs {
				return false, false
			}
			p.paced = false // silent: occluded window, sleeping display
		}
		p.frameReady = true
	}
	if p.retryAt != 0 {
		if now < p.retryAt {
			return false, false
		}
		p.retryAt = 0
	}
	return true, false
}

// drawn records a presented frame. more says whether another one is owed
// (animation running, input to settle). The slot is spent when paced; when
// unpaced the next frame can start at once.
func (p *pacer) drawn(more bool) {
	p.needDraw = more
	p.frameReady = !p.paced
	p.retryAt = 0
}

// failed records a draw attempt that found no frame to draw into. The
// repaint stays owed. Paced, the loop waits for the next EvFrame (it must
// have requested one); unpaced it retries after retryNs; hidden it parks.
func (p *pacer) failed(now int64) {
	p.needDraw = true
	p.frameReady = !p.paced
	switch {
	case p.hidden:
		p.parked = true // no frames while hidden: wait for an event
	case !p.paced:
		p.retryAt = now + retryNs
	}
}
