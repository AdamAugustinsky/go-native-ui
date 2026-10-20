//go:build darwin

package widgettest

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// Frames that do not come. A live test waits for frames, and a wait that sees
// none for a while has two explanations that must not be mixed up: the
// environment cannot draw (a locked screen, a window nobody can see, a
// display that does not tick), which says nothing about the code, or the code
// stopped drawing, which is the regression the test exists to catch. Skipping
// on the first would be fine; skipping on a guess hides the second. So a skip
// needs evidence from outside the app's own frame accounting, and a stall
// without it is a failure, on CI and locally alike.
//
// The evidence, read from the system and from the window, not from the frame
// counter:
//
//   - the screen is locked (CGSessionCopyCurrentDictionary: CGSSessionScreenIsLocked);
//   - the window is miniaturized, ordered out, or occluded (NSWindow's
//     isMiniaturized, isVisible and occlusionState): the display link does
//     not tick for a window nobody can see;
//   - a display stall: the main run loop keeps answering (a heartbeat: a call
//     handed to the main thread and returned, several times) while the display
//     link's tick counter does not move although a frame is armed and the link
//     is running. Frames are asked for by the wait, so a tick is owed; the run
//     loop is alive, so the app is not hung; the display is what is silent.
//
// A main thread that does not answer the heartbeat is not evidence of
// anything but a hang, and fails.

// envSnapshot is what one look at the environment reads on the main thread.
type envSnapshot struct {
	HaveApp     bool // there was an app to ask
	Locked      bool // the screen is locked
	LockedKnown bool // the session dictionary could be read
	NSVisible   bool // [NSWindow isVisible]
	Win         platform.DebugState
	Line        string // app.StateLine
}

// snapshotEnv reads the environment. Main thread.
func snapshotEnv(a *app.App) envSnapshot {
	var s envSnapshot
	s.Locked, s.LockedKnown = screenLocked()
	if a == nil || a.Win == nil {
		return s
	}
	s.HaveApp = true
	s.NSVisible = objc.SendBool(objc.ID(a.Win.Native().NSWindow), selIsVisible)
	s.Win = a.Win.DebugState()
	s.Line = string(a.StateLine(nil, platform.Now()))
	return s
}

var session struct {
	once sync.Once
	copy uintptr // CGSessionCopyCurrentDictionary
}

// screenLocked reports whether the login session's screen is locked, and
// whether the system could say (no window-server session, or no such call). Main thread.
func screenLocked() (locked, known bool) {
	session.once.Do(func() {
		h, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			session.copy, _ = purego.Dlsym(h, "CGSessionCopyCurrentDictionary")
		}
	})
	if session.copy == 0 || loadCoreGraphics() != nil {
		return false, false
	}
	d := objc.F.Call0(session.copy)
	if d == 0 {
		return false, false
	}
	defer objc.F.Call1(coreGraphics.release, d)
	v := objc.Send1(objc.ID(d), objc.Sel("objectForKey:"), uintptr(objc.Str("CGSSessionScreenIsLocked")))
	if v == 0 {
		return false, true // the key is there only while the screen is locked
	}
	return objc.SendBool(v, objc.Sel("boolValue")), true
}

// classifyStall says why no frame came, from two looks at the environment a
// little apart and the number of heartbeats the main thread answered between
// them. It returns the evidence that the environment cannot draw, or "" when
// there is none (the code stopped drawing, or something is wrong that this
// cannot see).
func classifyStall(s1, s2 envSnapshot, beats int) string {
	if s2.LockedKnown && s2.Locked {
		return "the screen is locked (CGSSessionScreenIsLocked)"
	}
	if !s2.HaveApp || !s2.Win.Valid {
		return ""
	}
	w := s2.Win
	switch {
	case w.Mini:
		return "the window is miniaturized: the display link does not tick for it"
	case !s2.NSVisible:
		return "the window is not visible (ordered out): the display link does not tick for it"
	case !w.Visible:
		return fmt.Sprintf("the window is occluded (occlusionState=%d, not visible): covered, or on another Space; the display link does not tick for it", w.Occlusion)
	case beats >= minBeats && s1.Win.Valid && w.Ticks == s1.Win.Ticks && w.FrameArmed && !w.LinkPausedOS:
		return fmt.Sprintf("the display does not tick: the main run loop answered %d heartbeats, a frame is armed and the display link is running, and its tick counter stayed at %d", beats, w.Ticks)
	}
	return ""
}

const (
	beatCount = 6                      // heartbeats of one look
	minBeats  = 4                      // the heartbeats that must be answered for the run loop to count as alive
	beatGap   = 250 * time.Millisecond // between heartbeats
)

// stallReport is what looking at a stall found.
type stallReport struct {
	resumed  bool   // a frame came while it looked: the wait goes on
	blocked  string // the main thread did not answer: a hang
	unserved string // the main thread sits in its event wait and AppKit does not serve it
	evidence string // the environment cannot draw: why
	text     string // the diagnostics, always printed
}

// diagnose looks at the environment of a wait that saw no frame for after:
// it takes the snapshot, answers several heartbeats on the main thread, takes
// another snapshot, and classifies. It prints what it found to stderr and the
// test log.
func (l *LiveApp) diagnose(what string, after time.Duration) stallReport {
	l.t.Helper()
	var r stallReport
	frames := l.sb.built.Load()
	limit := min(l.lim.Call, 5*time.Second)
	var s1, s2 envSnapshot
	take := func(s *envSnapshot) bool {
		snap := snapshotEnv
		if l.lim.snap != nil {
			snap = l.lim.snap
		}
		res := l.callIdle(func(a *app.App) { *s = snap(a) }, limit, limit)
		if res.idle {
			r.unserved = describe(res)
			return false
		}
		if res.timeout != "" || res.ended || res.goexit || res.panicked != nil {
			r.blocked = fmt.Sprintf("looking at the environment: %s", describe(res))
			return false
		}
		return true
	}
	if !take(&s1) {
		return l.finish(what, after, frames, s1, s2, 0, r)
	}
	beats, idleBeats := 0, 0
	for i := 0; i < beatCount; i++ {
		time.Sleep(beatGap)
		if l.sb.built.Load() != frames {
			r.resumed = true
			return l.finish(what, after, frames, s1, s2, beats, r)
		}
		res := l.callIdle(func(*app.App) {}, limit, limit)
		if res.timeout == "" && !res.ended && !res.goexit && res.panicked == nil {
			beats++
		} else if res.idle {
			idleBeats++
		}
	}
	if !take(&s2) {
		return l.finish(what, after, frames, s1, s2, beats, r)
	}
	if beats < minBeats && idleBeats > 0 { // alive, in its event wait, and not served
		r.unserved = fmt.Sprintf("the main thread answered %d of %d heartbeats; the others found it in its event wait, which AppKit did not end", beats, beatCount)
	} else if beats < minBeats {
		r.blocked = fmt.Sprintf("the main thread answered %d of %d heartbeats", beats, beatCount)
	} else {
		r.evidence = classifyStall(s1, s2, beats)
	}
	return l.finish(what, after, frames, s1, s2, beats, r)
}

func describe(res callResult) string {
	switch {
	case res.timeout != "":
		return res.timeout
	case res.ended:
		return "the app ended"
	case res.goexit:
		return "the call ended its goroutine"
	}
	return fmt.Sprintf("panic: %v", res.panicked)
}

// finish composes the diagnostics and prints them.
func (l *LiveApp) finish(what string, after time.Duration, frames int64, s1, s2 envSnapshot, beats int, r stallReport) stallReport {
	l.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "widgettest: no new frame for %v while waiting for %s (%d frames built so far)\n", after, what, frames)
	fmt.Fprintf(&b, "  heartbeats answered by the main thread: %d of %d\n", beats, beatCount)
	if s2.Line != "" {
		fmt.Fprintf(&b, "  state: %s\n", s2.Line)
	} else if s1.Line != "" {
		fmt.Fprintf(&b, "  state: %s\n", s1.Line)
	}
	if s2.LockedKnown || s1.LockedKnown {
		fmt.Fprintf(&b, "  screen locked: %v\n", s2.Locked || s1.Locked)
	}
	switch {
	case r.resumed:
		b.WriteString("  verdict: frames resumed while looking; the wait goes on\n")
	case r.blocked != "":
		fmt.Fprintf(&b, "  verdict: the main thread is blocked: %s\n", r.blocked)
	case r.unserved != "":
		fmt.Fprintf(&b, "  verdict: the main thread is idle in its event wait and AppKit does not serve it: %s\n", r.unserved)
	case r.evidence != "":
		fmt.Fprintf(&b, "  verdict: the environment cannot draw: %s\n", r.evidence)
	default:
		b.WriteString("  verdict: no evidence that the environment cannot draw; the window is visible and the screen is not locked\n")
	}
	r.text = b.String()
	fmt.Fprint(os.Stderr, r.text)
	l.t.Log(r.text)
	return r
}

// stall handles a wait (or a start) that saw no frame for after: it returns
// true if frames came back while it looked and the wait should go on, and
// otherwise ends the test: skipped with the reason when the environment
// cannot draw, failed when the main thread hangs or when nothing explains the
// silence.
func (l *LiveApp) stall(what string, after time.Duration) (resumed bool) {
	l.t.Helper()
	r := l.diagnose(what, after)
	switch {
	case r.resumed:
		return true
	case r.blocked != "":
		l.t.Fatalf("widgettest: waiting for %s: the main thread is blocked: %s", what, r.blocked)
	case r.unserved != "":
		l.t.Skipf("widgettest: no frame was drawn for %v while waiting for %s, and this environment cannot serve the application: the main thread is idle in its event wait and AppKit does not hand it work (%s)", after, what, r.unserved)
	case r.evidence != "":
		l.t.Skipf("widgettest: no frame was drawn for %v while waiting for %s, and this environment cannot draw: %s", after, what, r.evidence)
	}
	l.t.Fatalf("widgettest: waiting for %s: no frame was drawn for %v and nothing in the environment explains it (the window is visible, the screen is not locked, the display link is running): a regression in frame delivery", what, after)
	return false
}
