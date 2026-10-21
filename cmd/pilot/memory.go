package main

import (
	"os"
	"runtime"
	"strconv"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/pilot/pilot"
	"gonativeui/fs"
	"gonativeui/platform"
	"gonativeui/ui"
)

// memoryPilot deliberately keeps the two workloads separate: the application
// needs two visible listings, while the benchmark additionally warms its pool.
func memoryPilot(rows int, prewarm bool) *pilot.Pilot {
	if prewarm {
		return pilot.NewSynthBench(rows).Pilot
	}
	synth := new(pilot.SynthSource)
	o := pilot.Options{Home: "/", Loc: time.UTC, Src: fs.NewServiceWith(platform.Wake, synth.List)}
	for i, n := range [2]int{rows, max(rows/10, 100)} {
		l := new(fs.Listing)
		pilot.FillSynth(l, n, 0)
		fs.Sort(l, fs.SortName, false)
		ch := make(chan pilot.Ready, 1)
		ch <- pilot.Ready{Dir: "/synth/" + strconv.Itoa(n), L: l, At: platform.Now()}
		o.Initial[i] = ch
	}
	return pilot.New(o)
}

type memoryView struct {
	*pilot.Pilot
	rows    int
	prewarm bool
	delay   time.Duration
	timer   *time.Timer
}

func (v *memoryView) Build(c *ui.Context) {
	v.Pilot.Build(c)
	if v.timer == nil {
		v.timer = time.AfterFunc(v.delay, func() {
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			scope := "application-view (no benchmark prewarm)"
			if v.prewarm {
				scope = "prewarmed-benchmark-fixture (not application overhead)"
			}
			printJSON(struct {
				Scope              string
				PID, Rows          int
				GoHeapAfterGCBytes uint64 // not the process's native/GPU physical footprint
			}{scope, os.Getpid(), v.rows, m.HeapAlloc})
		})
	}
}

func runMemory(rows int, prewarm bool, delay time.Duration) error {
	rows = max(rows, 100)
	v := &memoryView{Pilot: memoryPilot(rows, prewarm), rows: rows, prewarm: prewarm, delay: delay}
	defer v.Close()
	defer func() {
		if v.timer != nil {
			v.timer.Stop()
		}
	}()
	return app.Run(pilot.Config(), v)
}
