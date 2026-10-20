package app

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// spinnerScene is a window with a spinner in it, or an indeterminate
// progress bar (which asks for every frame, where the spinner wakes once a
// step).
type spinnerScene struct {
	a   *App
	k   *base.Kit
	bar bool
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
	if s.bar {
		s.k.NextWidth(s.k.Fixed(200))
		rexui.ProgressBar(s.k, "Loading", -1, rexui.ProgressOpts{})
	} else {
		rexui.Spinner(s.k, "Loading", rexui.SpinnerOpts{})
	}
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
	for n := 0; r.now < end; n++ {
		if n > 100_000 {
			r.t.Fatalf("the loop spins: at %d ns, pacer %+v", r.now, o.p)
		}
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

// A spinner and an indeterminate bar in a window that becomes covered,
// which then gets no frames to draw into: the loop attempts no frame for the
// spinner, whose timed wake waits while the window is hidden, and one for the
// bar, which parks the loop until an event (it used to retry every 50 ms,
// forever, without ever building a frame in which they could see
// Input.Hidden). Both draw again as soon as the window shows.
func TestHiddenWindowAttemptsNoFrames(t *testing.T) {
	for _, bar := range []bool{false, true} {
		s := &spinnerScene{bar: bar}
		o, err := NewOffscreen(Config{}, 400, 300, 2, s)
		if err != nil {
			skipOrFail(t, "offscreen rendering", err)
		}
		o.p = newPacer()
		o.req = &fakeReq{}
		r := &loopRig{t: t, o: o, now: 1e9}
		what, most := "spinner", 0
		if bar {
			what, most = "indeterminate bar", 1
		}

		r.run(1e9)
		if r.drawn < 5 || (!bar && o.wakeAt == 0) || (bar && !o.UI.Animating()) {
			t.Fatalf("%s showing: %d frames in 1 s, wake at %d", what, r.drawn, o.wakeAt)
		}

		o.onEvent(&platform.Event{Kind: platform.EvVisible}) // covered
		r.covered = true
		r.attempts, r.drawn = 0, 0
		r.run(2e9)
		if r.attempts > most {
			t.Fatalf("%s in a covered window: %d frame attempts in 2 s, want at most %d", what, r.attempts, most)
		}

		o.onEvent(&platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}) // shown
		r.covered = false
		r.drawn = 0
		r.run(200e6)
		if r.drawn == 0 || o.tr.in.Hidden || (!bar && o.wakeAt == 0) || (bar && !o.UI.Animating()) {
			t.Fatalf("%s shown again: %d frames in 200 ms, hidden %v, wake at %d", what, r.drawn, o.tr.in.Hidden, o.wakeAt)
		}
		o.Close()
	}
}

// A hidden window that can still draw (some report covered while they
// draw) draws on: its Build sees Input.Hidden, the bar stops asking for
// frames, and the window goes idle.
func TestHiddenWindowThatDrawsGoesIdle(t *testing.T) {
	s := &spinnerScene{bar: true}
	o, err := NewOffscreen(Config{}, 400, 300, 2, s)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	o.p = newPacer()
	o.req = &fakeReq{}
	r := &loopRig{t: t, o: o, now: 1e9}
	r.run(500e6)
	o.onEvent(&platform.Event{Kind: platform.EvVisible}) // reported covered, frames still come
	r.drawn = 0
	r.run(2e9)
	if r.drawn == 0 || r.drawn > 3 || o.UI.Animating() || o.p.needDraw {
		t.Fatalf("hidden but drawing: %d frames in 2 s, animating %v, owed %v; want a frame or two, then idle",
			r.drawn, o.UI.Animating(), o.p.needDraw)
	}
}
