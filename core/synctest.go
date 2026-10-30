package core

import (
	"sync/atomic"
	"time"
)

var synctestMode atomic.Int32

// UseSynctestForTesting makes the clock and Parkers work inside
// testing/synctest bubbles until the test ends. Now reads time.Now, which is
// a bubble's fake clock inside it, instead of the runtime's monotonic clock,
// which no bubble fakes. NewParker parks on a channel instead of the OS: a pipe
// read in the network poller (Unix) or a wait in a system call (Windows) is not
// durably blocked, so a bubble's clock would never move past a parked worker.
// Code outside a bubble keeps working, on the wall clock. Call it before the
// bubble starts, from a test that does not run in parallel with tests that
// measure time or allocations; cleanup is t's Cleanup.
//
// Go's linker refuses a link to time.runtimeIsBubbled from outside the
// standard library, so the switch is explicit: a bubble cannot be detected.
func UseSynctestForTesting(t interface{ Cleanup(func()) }) {
	synctestMode.Add(1)
	t.Cleanup(func() { synctestMode.Add(-1) })
}

// synctestNow is Now under UseSynctestForTesting: nanoseconds since the Unix
// epoch, which in a bubble start at its fake clock's 2000-01-01.
func synctestNow() int64 { return time.Now().UnixNano() }
