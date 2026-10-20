//go:build darwin

package platform

import (
	"testing"

	"gonativeui/internal/objc"
)

// TestTabReachesControlsFollowsTheSetting: the answer is the Keyboard
// navigation setting as the system gives it, read when first asked, again when
// the application becomes active, and again when a Tab key goes down; between
// those the cached value stands, so a frame reads it for free. The setting
// itself is stood in for by a fake: a program must not change it.
func TestTabReachesControlsFollowsTheSetting(t *testing.T) {
	loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	setting := false
	reads := 0
	var steps []string
	var skip string
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		old, was := readFullKeyboardAccess, keynav
		defer func() { // (the observer stays registered)
			readFullKeyboardAccess, keynav.inited, keynav.all = old, was.inited, was.all
		}()
		readFullKeyboardAccess = func() bool { reads++; return setting }
		keynav.inited = false // as at the first call
		check := func(what string, want bool) {
			if got := TabReachesControls(); got != want {
				steps = append(steps, what+": "+boolStr(got)+", want "+boolStr(want))
			}
		}
		check("first read, setting off", false)
		setting = true
		before := reads
		check("changed, not yet re-read", false)
		if reads != before {
			steps = append(steps, "TabReachesControls read the system again")
		}
		postAppNotification("NSApplicationDidBecomeActiveNotification")
		check("after the application became active", true)
		setting = false
		check("changed again", true)
		w, why := openKeyRig()
		if why != "" {
			skip = why
			return
		}
		defer w.Close()
		w.q.reset()
		w.handle(nsKey(w, 10, "a", "a", 0, 0x00, 1)) // another key: no re-read
		check("after a letter key", true)
		w.handle(nsKey(w, 11, "\t", "\t", 0, 0x30, 2)) // a Tab release: no re-read either
		check("after a Tab release", true)
		w.handle(nsKey(w, 10, "\t", "\t", 0, 0x30, 3)) // Tab goes down
		check("after Tab went down", false)
		setting = true
		w.handle(nsKey(w, 10, "\t", "\t", flagShift, 0x30, 4)) // Shift+Tab too
		check("after Shift+Tab went down", true)
	})
	if skip != "" {
		t.Skip("cannot open a window here: " + skip)
	}
	for _, s := range steps {
		t.Error(s)
	}
}

// TestTabReachesControlsAsksAppKit: with the real reader the answer is
// -[NSApplication isFullKeyboardAccessEnabled], whichever way the machine has
// it.
func TestTabReachesControlsAsksAppKit(t *testing.T) {
	loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	var got, want bool
	onMain(func() {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		was := keynav
		defer func() { keynav.inited, keynav.all = was.inited, was.all }()
		keynav.inited = false
		app := objc.Send(objc.Cls("NSApplication"), selSharedApplication)
		want = objc.SendBool(app, selFullKeyboardAccess)
		got = TabReachesControls()
	})
	if got != want {
		t.Errorf("TabReachesControls = %v, isFullKeyboardAccessEnabled = %v", got, want)
	}
}
