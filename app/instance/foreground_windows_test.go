//go:build windows

package instance

import (
	"os"
	"testing"
)

// A secondary lets the primary take the foreground before it sends (the
// phase 8 plan's W1): the pipe server's process id, read from the pipe, is
// given AllowSetForegroundWindow. Here the primary is this process.
func TestSendLetsThePrimaryTakeTheForeground(t *testing.T) {
	dir := newDir(t)
	acquire(t, dir, Options{})
	_, s := acquire(t, dir, Options{})
	foregroundGiven.Store(0)
	if err := s.Send([]string{"x"}); err != nil {
		t.Fatal(err)
	}
	if got := foregroundGiven.Load(); got != uint32(os.Getpid()) {
		t.Errorf("foreground given to process %d, want the primary %d", got, os.Getpid())
	}
}
