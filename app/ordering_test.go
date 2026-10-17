package app

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

// pair is a Builder with two buttons, A at x 10..110 and B at x 200..300, that
// records their signals each frame.
type pair struct {
	sa     struct{ A, B ui.Signal }
	frames int
}

func (p *pair) Build(c *ui.Context) {
	c.NextSize(ui.Px(100), ui.Px(40))
	c.NextPos(10, 10)
	a := c.Box(ui.Clickable|ui.FloatingX|ui.FloatingY, "A")
	c.NextSize(ui.Px(100), ui.Px(40))
	c.NextPos(200, 10)
	b := c.Box(ui.Clickable|ui.FloatingX|ui.FloatingY, "B")
	p.sa.A, p.sa.B = c.Signal(a), c.Signal(b)
	p.frames++
}

func newPairRig(t *testing.T) (*Offscreen, *pair) {
	t.Helper()
	p := &pair{}
	o, err := NewOffscreen(Config{}, 400, 100, 1, p)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	t.Cleanup(o.Close)
	return o, p
}

func moveTo(x, y float32) *platform.Event {
	return ev(platform.EvPointerMove, func(e *platform.Event) { e.X, e.Y = x, y })
}

// settle runs frames while the app says another is owed.
func settle(o *Offscreen, max int) int {
	n := 0
	for ; n < max; n++ {
		o.Frame()
		if !o.owed(o.UI.Animating(), false) {
			return n + 1
		}
	}
	return n
}

// Press on A, then move and release over B before the next frame. ui used to
// see both edges at B: B captured the press and was clicked. Now the press is
// a frame of its own at A, and the release over B clicks nobody.
func TestPressOnAReleaseOverBClicksNobody(t *testing.T) {
	o, p := newPairRig(t)
	o.onEvent(moveTo(50, 30))
	settle(o, 10)
	o.onEvent(press(50, 30, 1, platform.ButtonLeft))
	o.onEvent(moveTo(250, 30))
	o.onEvent(release(250, 30, 2, platform.ButtonLeft))
	if len(o.tr.carry) == 0 {
		t.Fatal("the move and the release must wait for the next frame")
	}
	o.Frame()
	if !p.sa.A.Pressed || p.sa.B.Pressed {
		t.Errorf("frame 1: the press belongs to A at the position where it happened: A %+v B %+v", p.sa.A, p.sa.B)
	}
	if !o.owed(false, false) {
		t.Fatal("the deferred events must make the next frame owed")
	}
	o.Frame()
	if p.sa.B.Pressed || p.sa.B.Clicked || p.sa.B.Released {
		t.Errorf("B was pressed or clicked by a press that happened over A: %+v", p.sa.B)
	}
	if !p.sa.A.Released || p.sa.A.Clicked {
		t.Errorf("A's press ends with a release away from A, not a click: %+v", p.sa.A)
	}
	if len(o.tr.carry) != 0 {
		t.Error("events left over")
	}
}

// Release then press again between two frames. Both edges used to be set with
// Down true; ui captured the new press and cleared it in the same frame, and
// the following release found nothing to click.
func TestReleaseThenPressBetweenFrames(t *testing.T) {
	o, p := newPairRig(t)
	o.onEvent(moveTo(50, 30))
	settle(o, 10) // hit testing uses the previous layout
	o.onEvent(press(50, 30, 1, platform.ButtonLeft))
	settle(o, 10)
	if !p.sa.A.Dragging {
		t.Fatal("A does not hold the press")
	}
	o.onEvent(release(50, 30, 2_000_000_000, platform.ButtonLeft))
	o.onEvent(press(50, 30, 2_000_000_001, platform.ButtonLeft))
	o.Frame()
	if !p.sa.A.Clicked || p.sa.A.Pressed {
		t.Errorf("frame 1 is the release only: %+v", p.sa.A)
	}
	o.Frame()
	if !p.sa.A.Pressed || !p.sa.A.Dragging || p.sa.A.Released {
		t.Errorf("frame 2 is the new press, held: %+v", p.sa.A)
	}
	o.onEvent(release(50, 30, 3_000_000_000, platform.ButtonLeft))
	o.Frame()
	if !p.sa.A.Clicked {
		t.Errorf("the second press must click on its release: %+v", p.sa.A)
	}
}

// Losing focus with a button held over the box is a cancellation: the box is
// not clicked (a pane's close button when the user switches apps).
func TestFocusLossDoesNotClick(t *testing.T) {
	o, p := newPairRig(t)
	o.onEvent(moveTo(50, 30))
	settle(o, 10) // hit testing uses the previous layout
	o.onEvent(press(50, 30, 1, platform.ButtonLeft))
	settle(o, 10)
	if !p.sa.A.Dragging {
		t.Fatal("A does not hold the press")
	}
	o.onEvent(ev(platform.EvFocus, nil)) // lost; the pointer stays over A
	o.Frame()
	if a := p.sa.A; a.Released || a.Clicked || a.DoubleClicked || a.Dragging {
		t.Errorf("focus loss reported %+v", a)
	}
	// Back in the window, a plain press-release still clicks.
	o.onEvent(ev(platform.EvFocus, func(e *platform.Event) { e.Flags = platform.FlagDown }))
	o.onEvent(press(50, 30, 5_000_000_000, platform.ButtonLeft))
	o.Frame()
	o.onEvent(release(50, 30, 5_000_000_100, platform.ButtonLeft))
	o.Frame()
	if !p.sa.A.Clicked {
		t.Errorf("a click after focus returned: %+v", p.sa.A)
	}
}

// Events keep their order across the carry-over, text included.
func TestDeferredEventsKeepOrderAndText(t *testing.T) {
	tr := newTranslator()
	tr.event(press(5, 5, 1, platform.ButtonLeft), nil)
	tr.event(release(5, 5, 2, platform.ButtonLeft), nil) // waits
	tr.event(ev(platform.EvText, nil), []byte("ab"))
	tr.event(ev(platform.EvText, nil), []byte("cd"))
	tr.event(press(5, 5, 3, platform.ButtonLeft), nil) // waits behind them
	if got := string(tr.in.Text[:tr.in.NText]); got != "" {
		t.Fatalf("text typed after a deferred event was applied first: %q", got)
	}
	tr.endFrame() // frame 2: release, text; the second press conflicts with the release
	if got := string(tr.in.Text[:tr.in.NText]); got != "abcd" {
		t.Fatalf("text %q", got)
	}
	if !tr.in.Released[ui.ButtonLeft] || tr.in.Pressed[ui.ButtonLeft] || len(tr.carry) == 0 {
		t.Fatalf("frame 2: released %v pressed %v deferred %d", tr.in.Released, tr.in.Pressed, len(tr.carry))
	}
	tr.endFrame() // frame 3: the press
	if !tr.in.Pressed[ui.ButtonLeft] || !tr.in.Down[ui.ButtonLeft] || len(tr.carry) != 0 {
		t.Fatalf("frame 3: pressed %v down %v deferred %d", tr.in.Pressed, tr.in.Down, len(tr.carry))
	}
}

// Steady state: a stream of fast clicks and drags allocates nothing.
func TestTranslatorCarryAllocatesNothing(t *testing.T) {
	tr := newTranslator()
	p1, mv, r1 := press(5, 5, 1, platform.ButtonLeft), moveTo(50, 5), release(50, 5, 2, platform.ButtonLeft)
	txt, p2 := ev(platform.EvText, nil), press(50, 5, 3, platform.ButtonLeft)
	xy := []byte("xy")
	step := func() {
		tr.event(p1, nil)
		tr.event(mv, nil)
		tr.event(r1, nil)
		tr.event(txt, xy)
		tr.event(p2, nil)
		for i := 0; i < 4; i++ {
			tr.endFrame()
		}
	}
	for i := 0; i < 20; i++ {
		step()
	}
	if n := testing.AllocsPerRun(100, step); n != 0 {
		t.Errorf("%v allocs per burst", n)
	}
}

// listScene is a virtual list that fills the window: ListRange reads the
// list's previous-frame height, so after a resize it is one frame behind.
type listScene struct{ first, last int }

func (l *listScene) Build(c *ui.Context) {
	c.NextSize(ui.Pct(1), ui.Pct(1))
	list := c.Box(ui.ScrollY|ui.Clip, "list")
	l.first, l.last = c.ListRange(list, 100000, 20, 0)
	c.Push(list)
	for r := l.first; r < l.last; r++ {
		c.NextSize(ui.Pct(1), ui.Px(20))
		c.BoxKeyed(0, ui.KeyN(7, uint64(r)), nil)
	}
	c.Pop()
}

// One large height increase, then no input at all: the frame that shows the
// new size is followed by the one that builds the rows for it, whether or not
// the pointer is in the window.
func TestResizeOwesASettleFrameWithoutThePointer(t *testing.T) {
	l := &listScene{}
	o, err := NewOffscreen(Config{}, 400, 300, 1, l)
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	settle(o, 10)
	if want := 300 / 20; l.last < want {
		t.Fatalf("%d rows built for 300 px", l.last)
	}
	if o.tr.in.Inside {
		t.Fatal("the pointer is inside")
	}
	o.onEvent(ev(platform.EvResize, func(e *platform.Event) { e.W, e.H, e.Scale = 400, 3000, 1 }))
	n := settle(o, 10)
	if want := 3000 / 20; l.last < want {
		t.Errorf("after the resize and %d frames the list builds rows up to %d, want at least %d (stale previous-frame height)", n, l.last, want)
	}
	if n < 2 {
		t.Errorf("the resize drew %d frame(s)", n)
	}
	if o.owed(false, false) {
		t.Error("a frame is still owed after the settle frame")
	}
}

// A live-resize report only says pacing resumed for frames a frame-source tick
// delivered. A run in which no tick ever arrived (asleep display) is
// inconclusive however many fallback frames it drew.
func TestLivePacingVerdict(t *testing.T) {
	for _, c := range []struct {
		before, paced int
		want          string
	}{
		{0, 0, PacingInconclusive},
		{0, 20, PacingInconclusive}, // paced frames without any tick before: not credible either
		{12, 20, PacingResumed},
		{12, 25, PacingResumed},
		{12, 19, PacingFailed},
		{12, 0, PacingFailed},
	} {
		if got := livePacingVerdict(c.before, c.paced, 20); got != c.want {
			t.Errorf("before %d, paced after %d: %q, want %q", c.before, c.paced, got, c.want)
		}
	}
}
