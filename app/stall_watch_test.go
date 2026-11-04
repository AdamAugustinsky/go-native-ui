//go:build darwin || windows

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonativeui/platform"
)

// The watchdog itself, without a window: the main thread "stuck" in a Build
// writes a report with the phase and the goroutines while it is stuck, and the
// stall's length once it is over; only the newest Keep reports stay.
func TestStallWatchWritesReports(t *testing.T) {
	sampled := make(chan string, 4)
	old := nativeSample
	nativeSample = func(p string) { sampled <- p }
	t.Cleanup(func() { nativeSample = old })

	dir := t.TempDir()
	for i := 0; i < 3; i++ { // older reports, beyond Keep
		os.WriteFile(filepath.Join(dir, "stall-20000101-00000"+string(rune('0'+i))+".000.log"), []byte("old"), 0o644)
	}
	got := make(chan StallReport, 4)
	s := newStallWatch(StallWatch{After: 100 * time.Millisecond, Dir: dir, Keep: 2, Report: func(r StallReport) { got <- r }}, "dev.test")
	defer s.close()

	s.present.Store(platform.Now() - int64(time.Second))
	s.mark(phaseBuild, 0)
	var r StallReport
	select {
	case r = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no report for a Build stuck past After")
	}
	if r.Kind != StallPhase || r.Phase != "build" || r.Path == "" {
		t.Fatalf("report %+v", r)
	}
	b, _ := os.ReadFile(r.Path)
	if txt := string(b); !strings.Contains(txt, "phase: build") || !strings.Contains(txt, "goroutine ") || !strings.Contains(txt, "TestStallWatchWritesReports") {
		t.Fatalf("the report lacks the phase or the stacks:\n%.400s", txt)
	}
	select {
	case p := <-sampled:
		if p != strings.TrimSuffix(r.Path, ".log")+"-sample.txt" {
			t.Errorf("sampled to %s", p)
		}
	case <-time.After(2 * time.Second):
		t.Error("no native sample was asked for")
	}

	s.mark(phaseLoop, 0) // the main thread moves on
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ = os.ReadFile(r.Path)
		if strings.Contains(string(b), "resumed after ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the report never got the stall's length")
		}
		time.Sleep(20 * time.Millisecond)
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "stall-*.log"))
	if len(logs) != 2 {
		t.Errorf("%d reports kept, want 2: %v", len(logs), logs)
	}
}
