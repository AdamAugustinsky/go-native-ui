//go:build darwin

package gpu

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Exercise the real rendering subtests under GC pressure, in a child so a
// cross-thread autorelease-pool crash is reported as a test failure. This is
// opt-in because it repeats the GPU/model comparison 100 times.
func TestBackdropThreadAffinity(t *testing.T) {
	if os.Getenv("GNUI_GPU_THREAD_STRESS") == "" {
		t.Skip("set GNUI_GPU_THREAD_STRESS=1 to stress subtest thread affinity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBackdropMatchesModel$", "-test.count=100")
	cmd.Env = append(os.Environ(), "GOGC=1", "GOMAXPROCS=8")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("backdrop subtests under GC pressure: %v\n%s", err, out)
	}
}
