//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// An unbundled process (this test binary, `go run`) used to get nothing from
// AppKit for five seconds after its window opened: restoration at launch
// (skipStateRestoration) kept the main thread in the event wait. The first
// launch of a process is the only one that shows it, so the test launches a
// process of its own.
func TestFirstEventDoesNotWaitForStateRestoration(t *testing.T) {
	if os.Getenv("GNUI_LAUNCH_CHILD") != "" {
		launchChild()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestFirstEventDoesNotWaitForStateRestoration$", "-test.v")
	cmd.Env = append(os.Environ(), "GNUI_LAUNCH_CHILD=1")
	out, err := cmd.CombinedOutput()
	var ms int64 = -1
	for _, l := range strings.Split(string(out), "\n") {
		fmt.Sscanf(l, "first-event-ms=%d", &ms)
	}
	if ms < 0 {
		if strings.Contains(string(out), "launch-child-skip") {
			t.Skipf("this machine cannot open a window: %s", out)
		}
		t.Fatalf("the launched process reported no first event (%v):\n%s", err, out)
	}
	t.Logf("first event %d ms after the process opened its window", ms)
	if ms > 3000 {
		t.Errorf("the first event came %d ms after the window opened: the main thread was not served (AppKit restoring windows?)", ms)
	}
}

// launchChild is the launched process: it opens a window and reports when the
// event wait first returns an event.
func launchChild() {
	var w *Window
	var err error
	onMain(func() { w, err = Open(Options{Title: "launch", Width: 300, Height: 200}) })
	if err != nil {
		fmt.Println("launch-child-skip", err)
		return
	}
	defer onMain(func() { w.Close() })
	start := time.Now()
	for time.Since(start) < 15*time.Second {
		n := 0
		onMain(func() { n = len(w.Wait(int64(100 * time.Millisecond))) })
		if n > 0 {
			fmt.Printf("first-event-ms=%d\n", time.Since(start).Milliseconds())
			return
		}
	}
	fmt.Println("launch-child-timeout")
}
