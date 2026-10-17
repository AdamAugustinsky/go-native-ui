package sysinfo

import (
	"runtime"
	"testing"
	"time"
)

// burn spins for about d of wall time and returns a value the compiler cannot drop.
func burn(d time.Duration) uint64 {
	var x uint64 = 1
	for end := time.Now().Add(d); time.Now().Before(end); {
		for i := 0; i < 1000; i++ {
			x = x*6364136223846793005 + 1442695040888963407
		}
	}
	return x
}

var sink uint64

func TestThreadCPUNs(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	t0 := ThreadCPUNs()
	if t0 < 0 {
		t.Fatal("ThreadCPUNs unavailable")
	}
	// Burn until the thread clock shows 60 ms (a shared CI runner may give
	// the thread only part of a core), then check it never ran ahead of the
	// wall clock. A unit mistake (µs as ns, or the reverse) fails either way.
	start := time.Now()
	for time.Duration(ThreadCPUNs()-t0) < 60*time.Millisecond && time.Since(start) < 3*time.Second {
		sink += burn(5 * time.Millisecond)
	}
	t1, wall := ThreadCPUNs(), time.Since(start)
	if d := time.Duration(t1 - t0); d < 60*time.Millisecond || d > wall+20*time.Millisecond {
		t.Errorf("busy for %v of wall time, the thread clock advanced by %v", wall, d)
	}
	// Sleeping consumes no CPU time.
	time.Sleep(80 * time.Millisecond)
	if d := time.Duration(ThreadCPUNs() - t1); d > 35*time.Millisecond {
		t.Errorf("sleeping 80 ms advanced the thread clock by %v", d)
	}
}

// Another thread's CPU time does not count.
func TestThreadCPUNsIsPerThread(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	t0 := ThreadCPUNs()
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		sink = burn(80 * time.Millisecond)
		close(done)
	}()
	<-done
	if d := time.Duration(ThreadCPUNs() - t0); d > 45*time.Millisecond {
		t.Errorf("a burning goroutine on another thread advanced this thread's clock by %v", d)
	}
}

func TestThreadCPUNsZeroAlloc(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ThreadCPUNs()
	if n := testing.AllocsPerRun(1000, func() { ThreadCPUNs() }); n != 0 {
		t.Errorf("ThreadCPUNs allocates %v objects per call", n)
	}
}
