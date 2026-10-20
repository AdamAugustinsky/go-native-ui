package app

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// spinnerScene is a window with a spinner in it.
type spinnerScene struct {
	a *App
	k *base.Kit
}

func (s *spinnerScene) Init(a *App) error {
	s.a, s.k = a, base.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (s *spinnerScene) Build(c *ui.Context) {
	s.k.Begin(c)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextPad(20)
	c.Open(0, "root")
	rexui.Spinner(s.k, "Loading", rexui.SpinnerOpts{})
	c.Close()
	s.k.End()
}

// loopRig runs the event loop's logic on a virtual clock: Wait's timeout,
// the events, the wake and the pacer's verdict (App.turn), and a draw that
// either finds a frame to draw into, as drawFrame does with the window
// showing, or does not (a covered window gets no drawables or frame events).
type loopRig struct {
	t        *testing.T
	o        *Offscreen
	now      int64
	covered  bool // the surface has no frames to give
	attempts int  // draw attempts
	drawn    int  // frames drawn
	nextEv   int64
}

const tick = int64(16_666_667)

// run runs the loop for d ns of the clock, or until Wait would block with
// nothing to wake it.
func (r *loopRig) run(d int64) {
	o := r.o
	end := r.now + d
	for r.now < end {
		t := o.timeout(r.now)
		if !r.covered && o.p.requestedAt != 0 && !o.p.frameReady {
			// the frame source ticks while the window shows
			if r.nextEv <= r.now {
				r.nextEv = r.now + tick
			}
			if t < 0 || r.now+t > r.nextEv {
				r.now = r.nextEv
				o.onEvent(&platform.Event{Kind: platform.EvFrame})
				r.step()
				continue
			}
		}
		if t < 0 {
			return // Wait blocks: nothing is drawn or attempted
		}
		r.now += max(t, 1)
		r.step()
	}
}

func (r *loopRig) step() {
	o := r.o
	if !o.turn(r.now) {
		return
	}
	r.attempts++
	if r.covered { // present's failed attempt
		o.p.failed(r.now)
		o.req.RequestFrame()
		o.p.requested(r.now)
		return
	}
	// drawFrame's build and its pacing, without a GPU
	o.tr.in.Time = r.now
	o.Frame()
	why := o.arm(false, o.UI.Animating(), false, r.now)
	o.wakeAt = 0
	if ns := o.UI.WakeAfter(); ns > 0 {
		o.wakeAt = r.now + ns
	}
	o.p.drawn(why != 0)
	r.drawn++
}

// A spinner in a window that becomes covered: the loop attempts no frame
// while it is hidden (it used to retry every 50 ms, forever, without ever
// building a frame in which the spinner could see Input.Hidden), and draws
// again as soon as the window shows.
func TestHiddenWindowAttemptsNoFrames(t *testing.T) {
	s := &spinnerScene{}
	o, err := NewOffscreen(Config{}, 400, 300, 2, s)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	o.p = newPacer()
	o.req = &fakeReq{}
	r := &loopRig{t: t, o: o, now: 1e9}

	r.run(1e9)
	if r.drawn < 5 || o.wakeAt == 0 {
		t.Fatalf("a showing spinner: %d frames in 1 s, wake at %d", r.drawn, o.wakeAt)
	}

	o.onEvent(&platform.Event{Kind: platform.EvVisible}) // covered
	r.covered = true
	r.attempts, r.drawn = 0, 0
	r.run(2e9)
	if r.attempts != 0 {
		t.Fatalf("a covered window: %d frame attempts in 2 s, want 0", r.attempts)
	}

	o.onEvent(&platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}) // shown
	r.covered = false
	r.run(200e6)
	if r.drawn == 0 || o.tr.in.Hidden || o.wakeAt == 0 {
		t.Fatalf("shown again: %d frames in 200 ms, hidden %v, wake at %d", r.drawn, o.tr.in.Hidden, o.wakeAt)
	}
}
