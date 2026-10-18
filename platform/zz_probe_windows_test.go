//go:build windows

package platform

import (
	"runtime"
	"testing"
	"time"
)

// Temporary: does a synchronous read (what RequestPaste did on the UI thread) really block on this owner?
func TestZZSyncReadBlocksOnHungOwner(t *testing.T) {
	w := openTestWindow(t)
	release := hungOwner(t, w)
	done := make(chan string, 1)
	go func() {
		runtime.LockOSThread()
		start := time.Now()
		_ = Clipboard()
		done <- time.Since(start).String()
	}()
	select {
	case d := <-done:
		t.Errorf("the synchronous read returned after %s: the owner does not block readers", d)
	case <-time.After(1500 * time.Millisecond):
		t.Logf("synchronous read still blocked after 1.5 s: the hang is real")
	}
	release()
	t.Logf("after release it returned after %s", <-done)
}
