//go:build darwin

package live

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// frameLog builds the app's frames and keeps, for each, when it was built and
// the causes the debug overlay recorded for it (app.FrameRecord): the idle
// tests attribute every frame they see to its cause. They need the overlay on
// (wantOverlay).
type frameLog struct {
	app.Builder
	a      *app.App
	mu     sync.Mutex
	start  time.Time
	frames []loggedFrame
}

type loggedFrame struct {
	n      uint64        // the overlay's number of the frame (FrameRecord.N)
	at     time.Duration // when it was built, from the first frame
	causes []app.RepaintCause
	ended  bool // causes is the frame's record (or there is none: the overlay kept too few)
}

func (f *frameLog) Init(a *app.App) error {
	f.a = a
	if in, ok := f.Builder.(app.Initer); ok {
		return in.Init(a)
	}
	return nil
}

func (f *frameLog) Build(c *ui.Context) {
	f.Builder.Build(c)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fill()
	n := f.a.DebugFrames()
	if k := len(f.frames); k > 0 && f.frames[k-1].n == n {
		return // a second pass of the frame (the first frame builds twice)
	}
	if f.start.IsZero() {
		f.start = time.Now()
	}
	f.frames = append(f.frames, loggedFrame{n: n, at: time.Since(f.start)})
}

// fill takes the causes of the frames that have ended from the overlay's
// records. Main thread, f.mu held.
func (f *frameLog) fill() {
	last, ok := f.a.DebugFrame(0)
	if !ok {
		return
	}
	for i := len(f.frames) - 1; i >= 0 && !f.frames[i].ended; i-- {
		fr := &f.frames[i]
		if fr.n > last.N {
			continue // still being drawn
		}
		if r, ok := f.a.DebugFrame(int(last.N - fr.n)); ok && r.N == fr.n {
			fr.causes = append([]app.RepaintCause(nil), r.Causes[:r.NCauses]...)
		}
		fr.ended = true
	}
}

// finish returns the frames built so far, with the causes of the last. The
// call draws a frame of its own afterwards (LiveApp.Do), which is not among
// them.
func (f *frameLog) finish(l *widgettest.LiveApp) []loggedFrame {
	var frames []loggedFrame
	l.Do(func(*app.App) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.fill()
		frames = append(frames, f.frames...)
	})
	return frames
}

// count is the number of frames built so far.
func (f *frameLog) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.frames)
}

// wantOverlay turns on the debug overlay for the app the test starts (its
// repaint causes), and its log with log, keeping what the caller asked for.
func wantOverlay(t *testing.T, log bool) {
	v := os.Getenv("GNUI_DEBUG")
	if !strings.Contains(v, "overlay") && !strings.Contains(v, "log") {
		w := "overlay"
		if log {
			w = "overlay,log"
		}
		if v != "" {
			w = v + "," + w
		}
		t.Setenv("GNUI_DEBUG", w)
	}
}

// outsideCause says what in the system or in another app a cause stands for,
// when it is one that reaches a window which sits idle on a Mac in use: none
// of them is the app's own doing.
//
//   - CauseAppearance: the appearance or the system's values changed. AppKit
//     posts one some time after launch on a Mac with a mouse (the scroller
//     style turns legacy: bars that take room).
//   - CauseApp with EvActivated or EvDeactivated: another app took the
//     activation, or gave it back (D37: an app event draws what the Handler
//     may have changed). Other processes' windows do it all the time where
//     tests run side by side; until the overlay recorded app events these
//     frames showed as "menu" or "NO CAUSE".
//   - CauseVisible: another window covered or uncovered this one. A change
//     waits for the loop's next wake, so it comes with one of the above.
func outsideCause(c app.RepaintCause) (string, bool) {
	switch c.Kind {
	case app.CauseAppearance:
		return "the system's appearance or values changed", true
	case app.CauseVisible:
		return "another window covered or uncovered this one", true
	case app.CauseApp:
		if k := app.EventKind(c.Sub); k == app.EvActivated || k == app.EvDeactivated {
			return "another app took the activation or gave it back", true
		}
	}
	return "", false
}

// maxFollow bounds the frames of an animation that follows an outside cause:
// 1.5 s of frames at 120 Hz. The fade to new values takes the same number of
// frames however long the system takes to give them (a covered window gets
// about three a second).
const maxFollow = 180

// verdict is the attribution of one frame.
type verdict struct {
	loggedFrame
	ok  bool
	why string
}

func (v verdict) String() string {
	var cs []string
	for _, c := range v.causes {
		cs = append(cs, c.String())
	}
	if !v.ended {
		cs = append(cs, "(no record)")
	}
	return fmt.Sprintf("frame %d at %.3fs: %s: %s", v.n, v.at.Seconds(), strings.Join(cs, " + "), v.why)
}

// attribute judges frames[from:]. A frame is explained when each of its causes
// is an outside one (outsideCause) or an animation that follows a frame with
// an outside cause, at most maxFollow frames on. Frames built in the first
// grace of the app's life are not judged, but an outside cause among them is
// followed too. Frames before from are not followed: what they started is not
// explained.
func attribute(frames []loggedFrame, from int, grace time.Duration) []verdict {
	var out []verdict
	follow, seed := -1, uint64(0) // frames of the animation that follows frame seed; -1: none
	for _, f := range frames[from:] {
		v := verdict{loggedFrame: f}
		var outside []string
		anim, other := false, !f.ended || len(f.causes) == 0
		for _, c := range f.causes {
			if why, ok := outsideCause(c); ok {
				outside = append(outside, why)
			} else if c.Kind == app.CauseAnimation && follow >= 0 && follow < maxFollow {
				anim = true
			} else {
				other = true
			}
		}
		switch {
		case f.at <= grace:
			v.ok, v.why = true, "the window's first moments"
		case !f.ended:
			v.why = "the overlay kept no record of it"
		case other:
			v.why = "not the system's or another app's doing"
		case len(outside) > 0:
			v.ok, v.why = true, strings.Join(outside, "; ")
		default:
			v.ok, v.why = true, fmt.Sprintf("the animation that follows frame %d", seed)
		}
		switch {
		case len(outside) > 0:
			follow, seed = 0, f.n
		case anim && v.ok:
			follow++
		default:
			follow = -1
		}
		out = append(out, v)
	}
	return out
}

// report fails the test with the frames that are not explained, under what,
// logs the others after the grace, and returns how many failed it.
func report(t *testing.T, what string, vs []verdict, grace time.Duration) int {
	t.Helper()
	var bad []string
	for _, v := range vs {
		switch {
		case !v.ok:
			bad = append(bad, v.String())
		case v.at > grace:
			t.Logf("%v", v)
		}
	}
	if len(bad) > 0 {
		t.Errorf("%s (%d):\n\t%s", what, len(bad), strings.Join(bad, "\n\t"))
	}
	return len(bad)
}

// The attribution excuses what the system and other apps do, and the
// animation that follows it for a while, and nothing the app does by itself.
func TestFramesAreAttributedToTheirCauses(t *testing.T) {
	c := func(k app.Cause, sub app.EventKind) app.RepaintCause {
		return app.RepaintCause{Kind: k, Sub: uint8(sub)}
	}
	anim := c(app.CauseAnimation, 0)
	var frames []loggedFrame
	add := func(at time.Duration, cs ...app.RepaintCause) {
		frames = append(frames, loggedFrame{n: uint64(len(frames) + 1), at: at, causes: cs, ended: true})
	}
	add(0, c(app.CauseFirst, 0))
	add(2*time.Second, c(app.CauseApp, app.EvDeactivated))
	add(3*time.Second, c(app.CauseAppearance, 0))
	add(3*time.Second, anim) // the fade that follows, slowed by a covered window
	add(9*time.Second, anim, c(app.CauseVisible, 0))
	add(10*time.Second, c(app.CauseWake, 0))
	add(10*time.Second, anim) // what the wake started
	add(11*time.Second, c(app.CauseApp, app.EvService))
	add(12*time.Second, c(app.CauseVisible, 0), c(app.CauseTimer, 0))
	add(13*time.Second, c(app.CauseApp, app.EvActivated))
	for i := 0; i <= maxFollow; i++ {
		add(14*time.Second, anim)
	}
	want := []bool{true, true, true, true, true, false, false, false, false, true}
	for i := 0; i < maxFollow; i++ {
		want = append(want, true)
	}
	want = append(want, false) // an animation that does not settle
	vs := attribute(frames, 0, time.Second)
	for i, v := range vs {
		if v.ok != want[i] {
			t.Errorf("%v: explained %v, want %v", v, v.ok, want[i])
		}
	}
}

// An app left alone draws no frame between 1 s and 16 s after its launch,
// except for what the system and the other apps do to it. Each frame is
// attributed to the causes the debug overlay recorded (GNUI_DEBUG=overlay,log,
// added here unless the caller turned the overlay on): a change of the
// appearance or of the system's values (on a Mac whose mouse AppKit finds
// after launch the scroller style turns legacy), another app taking the
// activation or giving it back, another window covering or uncovering this
// one, and the animation that follows one of those. Every other frame fails
// the test, with its causes.
func TestIdleAppDrawsNothingAfterOneSecondButTheSystemsNews(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 16 s")
	}
	wantOverlay(t, true)
	fl := &frameLog{Builder: &probe{}}
	l := widgettest.StartLive(t, probeConfig(), fl)
	time.Sleep(16 * time.Second)
	frames := fl.finish(l)
	report(t, "frames after the first second that are not the system's or another app's doing", attribute(frames, 0, time.Second), time.Second)
}
