package pilot

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/fs"
	"gonativeui/ui"
)

// waitGroupDone reports whether wg finishes within d.
func waitGroupDone(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// Closing the Pilot does not wait for a read of the system's preferences that
// does not return; when it does return afterwards, it publishes nothing and
// wakes nobody.
func TestCloseDoesNotWaitForTheIconStyleRead(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var reads atomic.Int32
	old := iconStyle
	iconStyle = func() uint64 {
		reads.Add(1)
		<-gate
		return 7
	}
	t.Cleanup(func() { iconStyle = old })
	defer release() // (before the cleanups, whatever way the test ends)

	var wakes atomic.Int32
	pl := New(SceneOptions())
	pl.wake = func() { wakes.Add(1) }
	o := newOffscreen(t, pl)
	o.Frame()
	for n := 0; n < 500 && reads.Load() == 0; n++ {
		time.Sleep(time.Millisecond)
	}
	if reads.Load() == 0 {
		t.Fatal("the style was never read")
	}
	// (Close may take its second to stop the icon cache's workers; what it must
	// not do is wait for the read, which only this test ends.)
	closed := make(chan struct{})
	go func() { pl.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		release()
		t.Fatal("Close did not return with the style read blocked")
	}
	before := wakes.Load() // text prefetch may have completed before Close
	release()
	time.Sleep(50 * time.Millisecond)
	if pl.sys.latest.Load() != 0 || wakes.Load() != before {
		t.Errorf("a read that returned after Close published %d (want 0) and woke %d times", pl.sys.latest.Load(), wakes.Load()-before)
	}
	// and a request queued behind it is not started
	pl.sys.requestStyle()
	time.Sleep(20 * time.Millisecond)
	if n := reads.Load(); n != 1 {
		t.Errorf("%d reads of the style, want the one that was blocked", n)
	}
}

// Closing the Pilot while a probe of the places is blocked ends the worker once
// the probe returns, without publishing, waking or probing further.
func TestCloseStopsThePlacesWorker(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	var probes atomic.Int32
	old := iconStyle
	iconStyle = func() uint64 { return 0 } // (the style worker's own wake is not the one under test)
	t.Cleanup(func() { iconStyle = old })
	timer := time.AfterFunc(5*time.Second, release)
	defer timer.Stop()
	defer release()

	opt := SceneOptions()
	opt.Places, opt.RootTitle = nil, ""
	opt.Probe = Probe{
		IsDir:      func(string) bool { probes.Add(1); <-gate; return true },
		VolumeName: func() string { return "Disk" },
	}
	var wakes atomic.Int32
	pl := New(opt)
	pl.wake = func() { wakes.Add(1) }
	o := newOffscreen(t, pl)
	o.Frame()
	for n := 0; n < 500 && probes.Load() == 0; n++ {
		time.Sleep(time.Millisecond)
	}
	if probes.Load() != 1 {
		t.Fatalf("%d probes started, want the first one blocked", probes.Load())
	}
	ch := pl.probed
	pl.Close()
	before := wakes.Load() // text prefetch may have completed before Close
	release()
	if !waitGroupDone(&pl.probeWG, 2*time.Second) {
		t.Fatal("the places worker did not end after its probe returned")
	}
	if n := probes.Load(); n != 1 {
		t.Errorf("%d probes: the worker went on after Close", n)
	}
	if len(ch) != 0 || wakes.Load() != before {
		t.Errorf("after Close the worker published %d results and woke %d times", len(ch), wakes.Load()-before)
	}
}

// A path typed and sent, then abandoned (Esc, or Tab out of the field) before
// its listing arrives, does not change the folder when the listing does; a
// navigation started since is not touched.
func TestAbandonedPathNavigationDoesNotLand(t *testing.T) {
	for _, how := range []string{"Esc", "Tab"} {
		t.Run(how, func(t *testing.T) {
			r := newDrive(t)
			r.o.UI.SetTabReachesControls(false)
			pl := r.d.Pilot
			a := pl.panes[0]
			dir := a.dir
			r.key('L', pl.primary())
			r.text("/slow")
			r.key(ui.KeyEnter, 0)
			if !a.nav.pending {
				t.Fatal("setup: nothing is awaited")
			}
			if how == "Esc" {
				r.key(ui.KeyEscape, 0)
			} else {
				r.key(ui.KeyTab, 0)
			}
			r.frames(2)
			deliver(a, listing(fs.SortName, false, dirItems...)) // the slow folder, at last
			r.frames(3)
			if a.dir != dir || a.nav.pending {
				t.Errorf("the folder is %q (pending %v), want %q: the abandoned path landed", a.dir, a.nav.pending, dir)
			}
			if a.pathEdit {
				t.Error("the field is back")
			}
		})
	}
	t.Run("another navigation is left alone", func(t *testing.T) {
		r := newDrive(t)
		pl := r.d.Pilot
		a := pl.panes[0]
		r.key('L', pl.primary())
		r.text("/slow")
		r.key(ui.KeyEnter, 0)
		a.goTo("/elsewhere") // a click on a crumb, say, supersedes the path's
		req := a.nav.req
		r.key(ui.KeyEscape, 0)
		r.frames(2)
		if !a.nav.pending || a.nav.req != req {
			t.Error("Esc abandoned a navigation the field did not make")
		}
	})
}

// Editing the text takes the failure away from the status line as well, also
// before the relist that follows the failure has come back.
func TestEditingClearsTheFailureBeforeTheRelist(t *testing.T) {
	r := newDrive(t)
	pl := r.d.Pilot
	a := pl.panes[0]
	r.key('L', pl.primary())
	r.text("~/Nope")
	r.key(ui.KeyEnter, 0)
	bad := new(fs.Listing)
	bad.Err = os.ErrNotExist
	deliver(a, bad) // (the relist that follows is still out)
	r.frames(3)
	if !r.statusHas("No such file or directory") {
		t.Fatalf("setup: the reason is not shown: %q", r.statusLines())
	}
	r.text("x")
	r.frames(3)
	if r.statusHas("No such file or directory") {
		t.Errorf("the reason is still shown after an edit: %q", r.statusLines())
	}
}
