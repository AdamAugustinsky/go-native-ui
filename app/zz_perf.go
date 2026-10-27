package app

// THROWAWAY perf-survey instrumentation (never commit).

import (
	"fmt"
	"os"
	"runtime/pprof"

	"gonativeui/ui"
)

var perfProf *os.File
var perfDone bool

func perfBegin(a *App) {
	ui.PerfReset()
	if perfDone {
		return
	}
	if p := os.Getenv("GNUI_PERF_PROF"); p != "" && perfProf == nil {
		f, err := os.Create(p)
		if err == nil {
			perfProf = f
			pprof.StartCPUProfile(f)
		}
	}
}

func perfEnd(a *App, frames int) {
	if os.Getenv("GNUI_PERF_COUNTS") != "" && !perfDone {
		fmt.Fprintln(os.Stderr, "PERFCOUNTS", ui.PerfReport(frames))
	}
	if perfProf != nil {
		pprof.StopCPUProfile()
		perfProf.Close()
		perfProf = nil
	}
	perfDone = true
}
