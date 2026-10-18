//go:build linux

package platform

import (
	"os"
	"runtime"
	"sync"
)

// The Go runtime starts an OS thread (an M) whenever a goroutine becomes
// runnable, a processor is idle and no thread is: the allocations of that
// thread (several objects) land wherever it happens first. On Linux the UI
// goroutine spends milliseconds inside C calls (eglSwapBuffers on a software
// renderer, XFlush, poll), long enough for the runtime to hand its processor
// to other work, and in a measured window that is the first time some other
// goroutine (a file-system worker, a GC worker) wakes at the same moment, so
// the thread appeared there as a handful of allocations. primeThreads makes
// the runtime create its spare threads at Open, where allocation is free, and
// they stay parked for the life of the process.

// spareThreads is how many spare threads Open makes sure exist.
const spareThreads = 6

var primeOnce sync.Once

// primeThreads starts spareThreads goroutines that each lock their thread
// and wait until all have, so that the runtime has to have that many threads
// at the same moment; they then unlock and exit and the threads go idle.
// GNUI_NO_PRIME_THREADS=1 skips it (for measurements).
func primeThreads() {
	primeOnce.Do(func() {
		if os.Getenv("GNUI_NO_PRIME_THREADS") != "" {
			return
		}
		var up, done sync.WaitGroup
		release := make(chan struct{})
		up.Add(spareThreads)
		done.Add(spareThreads)
		for i := 0; i < spareThreads; i++ {
			go func() {
				runtime.LockOSThread()
				up.Done()
				<-release
				runtime.UnlockOSThread() // the thread goes back to the idle pool
				done.Done()
			}()
		}
		up.Wait()
		close(release)
		done.Wait()
	})
}
