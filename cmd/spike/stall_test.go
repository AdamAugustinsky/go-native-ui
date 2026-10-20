package main

import (
	"testing"
	"time"

	"gonativeui/platform"
)

// A window state with a frame requested, visible, the link running.
func armed(ticks uint64) platform.DebugState {
	return platform.DebugState{Valid: true, FrameArmed: true, Visible: true, Ticks: ticks}
}

func TestStallCause(t *testing.T) {
	paused := armed(5)
	paused.LinkPaused = true
	pausedOS := armed(5)
	pausedOS.LinkPausedOS = true
	hidden := armed(5)
	hidden.Visible = false
	idle := armed(5)
	idle.FrameArmed = false
	for _, c := range []struct {
		name string
		s    silenceInfo
		want string
	}{
		// The display stalled: no ticks while the main thread kept
		// returning to its run loop. This may restart.
		{"display stall, heartbeat alive", silenceInfo{a: armed(5), b: armed(6), mainBusyMs: 3}, causeLinkSilent},
		// The app blocked its main thread (a sleep in a frame callback):
		// no ticks either, but the heartbeat stopped.
		{"main thread blocked", silenceInfo{a: armed(5), b: armed(6), mainBusyMs: 1500}, causeMainBlocked},
		{"main thread blocked, no window state", silenceInfo{mainBusyMs: 1500}, causeMainBlocked},
		{"no window state (Windows, Linux)", silenceInfo{mainBusyMs: 2}, causeUnknown},
		{"ticks without frames", silenceInfo{a: armed(5), b: armed(40), mainBusyMs: 2}, causeLostFrames},
		{"link paused by us", silenceInfo{a: paused, b: armed(6), mainBusyMs: 2}, causeLinkPaused},
		{"link paused by the OS", silenceInfo{a: armed(5), b: pausedOS, mainBusyMs: 2}, causeLinkPaused},
		{"not requested", silenceInfo{a: idle, b: armed(6), mainBusyMs: 2}, causeNotRequested},
		{"occluded", silenceInfo{a: armed(5), b: hidden, mainBusyMs: 2}, causeOccluded},
	} {
		if got := c.s.cause(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for _, c := range []string{causeMainBlocked, causeUnknown, causeLostFrames, causeLinkPaused, causeNotRequested} {
		if excused(c) {
			t.Errorf("%q restarts the measured frames", c)
		}
	}
}

// An app bug found in a short stall is not overwritten by a longer stall of
// the display, which restarts nothing after it.
func TestAppBugStallIsSticky(t *testing.T) {
	var l stallLog
	if l.end(causeLostFrames, 1200, true) {
		t.Error("an app bug restarted the measured frames")
	}
	if l.end(causeLinkSilent, 20000, true) {
		t.Error("a display stall after an app bug restarted the measured frames")
	}
	if l.AppBug != causeLostFrames || l.AppBugMs != 1200 {
		t.Errorf("app bug %q (%v ms), want the lost frames of 1200 ms", l.AppBug, l.AppBugMs)
	}
	if l.LongestCause != causeLinkSilent || l.LongestMs != 20000 {
		t.Errorf("longest %q %v", l.LongestCause, l.LongestMs)
	}
}

func TestDisplayStallsRestartAtMostMaxRestarts(t *testing.T) {
	var l stallLog
	for i := range maxRestarts + 2 {
		if got, want := l.end(causeLinkSilent, 3000, true), i < maxRestarts; got != want {
			t.Errorf("stall %d restart %v, want %v", i, got, want)
		}
	}
	if l.AppBug != "" {
		t.Errorf("display stalls made an app bug: %q", l.AppBug)
	}
	if l.end(causeUnknown, 3000, true) {
		t.Error("a stall without evidence restarted")
	}
}

// A silence extends -timeout while the main loop keeps turning, by its
// length, up to maxStallExtension; a turn longer than the heartbeat (the
// program stopped) extends nothing.
func TestStallExtension(t *testing.T) {
	ms := int64(time.Millisecond)
	ext := int64(0)
	for range 50_000 { // 50,000 turns of 16 ms: 800 s of a silent display
		ext += stallExtension(ext, 16*ms)
	}
	if ext != maxStallExtension {
		t.Errorf("a long silence with the loop turning extended -timeout by %v, want the cap %v", time.Duration(ext), time.Duration(maxStallExtension))
	}
	if d := stallExtension(0, 16*ms); d != 16*ms {
		t.Errorf("a 16 ms turn extended by %v", time.Duration(d))
	}
	if d := stallExtension(0, (mainBusyLimitMs+1)*ms); d != 0 {
		t.Errorf("a turn longer than the heartbeat extended by %v", time.Duration(d))
	}
	if 43_000+59_000+53_000 > maxStallExtension/ms {
		t.Errorf("the cap %v does not cover CI's three stalls of 43, 59 and 53 s", time.Duration(maxStallExtension))
	}
}
