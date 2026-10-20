//go:build darwin

package locale

import (
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/internal/cf"
)

func TestDarwinReadsTheSystem(t *testing.T) {
	info := read()
	t.Logf("%+v", info)
	if len(info.Languages) == 0 {
		t.Error("NSLocale preferredLanguages is empty")
	}
	if info.Region == "" {
		t.Error("no region")
	}
}

// The OS posts NSCurrentLocaleDidChangeNotification when the user changes the
// setting, which a test cannot do; posting it ourselves checks the wiring: the
// observer is installed, runs on the posting thread, bumps the serial and wakes.
func TestDarwinNotification(t *testing.T) {
	var wakes atomic.Int32
	oldWake := wake
	wake = func() { wakes.Add(1) }
	t.Cleanup(func() { wake = oldWake })

	Current() // installs the observer
	before := Serial()
	for i := 1; i <= 2; i++ {
		done := make(chan struct{})
		go func() { // another thread than the one that set up
			defer close(done)
			fr := cf.Acquire()
			defer cf.Release(fr)
			defer cf.Pool(fr)()
			center := cf.Msg(fr, cf.Cls(fr, "NSNotificationCenter"), cf.Sel(fr, "defaultCenter"))
			cf.Msg(fr, center, cf.Sel(fr, "postNotificationName:object:"), syms.didChg, 0)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("posting the notification hangs")
		}
		if got := Serial(); got != before+uint32(i) {
			t.Fatalf("after %d notifications the serial is %d, want %d", i, got, before+uint32(i))
		}
	}
	if wakes.Load() != 2 {
		t.Errorf("%d wakes, want 2", wakes.Load())
	}
	if !stale.Load() {
		t.Error("the cache was not marked stale")
	}
	if len(Current().Languages) == 0 {
		t.Error("Current after a change has no languages")
	}
}
