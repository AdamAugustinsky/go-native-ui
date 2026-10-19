package main

import (
	"strings"
	"testing"

	"gonativeui/app"
)

func gateReport(listP99, listMax, editP99, editMax float64) report {
	return report{BenchReport: app.BenchReport{Frames: measFrames, Paced: true},
		ListWorkUs: app.Stats{P99: listP99, Max: listMax}, EditFrameUs: app.Stats{P99: editP99, Max: editMax},
		EditFrameMaxUs: editMax, Rebuilds: 1, HistoryLoads: 1, Arrivals: 1}
}

// At -budget-scale 3 the list's and the edits' P99 and maximum are checked at
// 3× their budgets.
func TestGateCatastrophicTimes(t *testing.T) {
	const s = 3
	maxList, maxEdit := float64(s*defaultMaxListUs), float64(s*defaultMaxEditUs)
	if f := gateFailures(gateReport(2.5*defaultMaxListUs, 2.9*defaultMaxListUs, 2.5*defaultMaxEditUs, 2.9*defaultMaxEditUs), false, maxList, maxEdit); len(f) != 0 {
		t.Errorf("under 3× failed: %v", f)
	}
	for _, c := range []struct {
		name string
		r    report
		want string
	}{
		{"list P99", gateReport(3.1*defaultMaxListUs, 3.1*defaultMaxListUs, 0, 0), "P99"},
		{"list max", gateReport(1000, 3.1*defaultMaxListUs, 0, 0), "in the list's work"},
		{"edit P99", gateReport(1000, 1000, 3.1*defaultMaxEditUs, 3.1*defaultMaxEditUs), "edits and BeginVList per frame have a P99"},
		{"edit max", gateReport(1000, 1000, 1000, 3.1*defaultMaxEditUs), "in edits and BeginVList"},
	} {
		f := gateFailures(c.r, false, maxList, maxEdit)
		if len(f) == 0 || !strings.Contains(strings.Join(f, "; "), c.want) {
			t.Errorf("%s over 3×: %v", c.name, f)
		}
	}
}
