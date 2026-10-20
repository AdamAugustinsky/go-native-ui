//go:build linux

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func threadsMade() int { return pprof.Lookup("threadcreate").Count() }

// threadsChild runs in a fresh process (a test thread count is only
// meaningful in one that has not started its threads yet): it primes the
// threads (unless GNUI_NO_PRIME_THREADS), then makes burst goroutines each hold
// an OS thread at the same moment, as a frame spent in C calls while other
// goroutines wake does, and prints the thread counts before and after.
//
// Nothing else of the process may run meanwhile: the appearance follower,
// which package init starts, connects to the session bus and reads the
// portal, and its goroutines finishing during the burst once took a thread
// the burst then lacked (CI: "created 1 more"). The child waits for its first
// answer (or failure) first, and a collection, whose mark workers want a
// thread per processor, is off.
//
// The child runs with GOMAXPROCS=1 (runThreadsChild). With more processors,
// the scheduler starts spinning threads to look for work while the burst's
// goroutines wake, as many as it decides then: on a loaded runner one or two
// more than at the priming (a probe on the CI runners, 600 children each:
// 6 failed the burst loaded, 0 idle; a bare model of priming and burst made
// an extra thread 3 times at GOMAXPROCS 4 and never at 1). With one
// processor what is measured is what priming promises: the threads the
// burst's goroutines hold are the primed ones.
func threadsChild() {
	debug.SetGCPercent(-1)
	select {
	case <-portal.ready:
	case <-time.After(portalTimeout + time.Second):
		fmt.Println("the appearance follower did not finish")
		os.Exit(1)
	}
	c0 := threadsMade()
	primeThreads()
	c1 := threadsMade()
	var up, done sync.WaitGroup
	release := make(chan struct{})
	burst, _ := strconv.Atoi(os.Getenv("GNUI_THREADS_BURST"))
	for i := 0; i < burst; i++ {
		up.Add(1)
		done.Add(1)
		go func() {
			runtime.LockOSThread()
			up.Done()
			<-release
			runtime.UnlockOSThread()
			done.Done()
		}()
	}
	up.Wait()
	close(release)
	done.Wait()
	fmt.Println("THREADS", c0, c1, threadsMade())
}

func runThreadsChild(t *testing.T, burst int, env ...string) (c0, c1, c2 int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), append([]string{"GNUI_THREADS_CHILD=1", "GNUI_THREADS_BURST=" + strconv.Itoa(burst), "GOMAXPROCS=1"}, env...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	for _, l := range strings.Split(string(out), "\n") {
		if n, _ := fmt.Sscanf(l, "THREADS %d %d %d", &c0, &c1, &c2); n == 3 {
			return
		}
	}
	t.Fatalf("child printed no thread counts: %q", out)
	return
}

// TestPrimedThreadsAreReused: once Open's priming has run, goroutines holding
// several threads at once need no new thread (whose allocations would land in
// a measured frame); without it they do, which shows that the count measures
// something.
func TestPrimedThreadsAreReused(t *testing.T) {
	c0, c1, c2 := runThreadsChild(t, spareThreads-2) // two threads are left to the goroutines that wait for them
	if c1 <= c0 && c0 < spareThreads {
		t.Errorf("priming made no thread: %d -> %d", c0, c1)
	}
	if c2 != c1 {
		t.Errorf("after priming, holding most of the spare threads at once created %d more", c2-c1)
	}
	n0, n1, n2 := runThreadsChild(t, spareThreads+2, "GNUI_NO_PRIME_THREADS=1")
	if n1 != n0 {
		t.Errorf("GNUI_NO_PRIME_THREADS still made threads: %d -> %d", n0, n1)
	}
	if n2 <= n1 {
		t.Errorf("without priming the same load created no thread (%d -> %d): the test measures nothing", n1, n2)
	}
}
