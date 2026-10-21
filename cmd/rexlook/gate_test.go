package main

import (
	"strings"
	"testing"

	"gonativeui/app"
	"gonativeui/internal/allocsite"
)

func clean(cpu float64) report {
	return report{BenchReport: app.BenchReport{Frames: 900, Paced: true, MainThreadCPUus: cpu}}
}

// On a shared runner the CPU budget is checked at 3× (-budget-scale 3): a
// frame at 2.5× the budget passes, one at 3.5× fails.
func TestGateCatastrophicCPU(t *testing.T) {
	if f := gateFailures(clean(2500), false, 3*defaultMaxCPUus); len(f) != 0 {
		t.Errorf("2.5× the budget failed at 3×: %v", f)
	}
	if f := gateFailures(clean(3500), false, 3*defaultMaxCPUus); len(f) != 1 || !strings.Contains(f[0], "main-thread CPU") {
		t.Errorf("3.5× the budget at 3×: %v", f)
	}
}

// An allocation of our code in a window fails although the reported window
// (the cleanest) allocated nothing.
func TestGateOwnAllocation(t *testing.T) {
	r := clean(500)
	r.OwnAllocObjects = 1
	r.AllocSites = []allocsite.Site{{Objects: 1, Bytes: 32, Class: allocsite.Ours, Func: "gonativeui/app.(*benchRun).after"}}
	if f := gateFailures(r, false, 0); len(f) != 1 || !strings.Contains(f[0], "of our code") {
		t.Errorf("gate failures %v", f)
	}
}
