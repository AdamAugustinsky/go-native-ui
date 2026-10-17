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
	sink = burn(60 * time.Millisecond)
	t1 := ThreadCPUNs()
	if d := time.Duration(t1 - t0); d < 30*time.Millisecond || d > 500*time.Millisecond {
		t.Errorf("burning 60 ms advanced the thread clock by %v", d)
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
