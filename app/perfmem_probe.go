//go:build perfmem

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"time"

	"gonativeui/platform"
)

func init() {
	if os.Getenv("PERF_EXACT_HEAP") == "1" {
		runtime.MemProfileRate = 1
	}
}

func perfCPU() func() {
	p := os.Getenv("PERF_CPU")
	if p == "" {
		return func() {}
	}
	f, err := os.Create(p)
	if err != nil {
		panic(err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		panic(err)
	}
	return func() { pprof.StopCPUProfile(); f.Close() }
}

func perfAfterIdle(a *App) {
	if os.Getenv("PERF_AFTER_IDLE") != "1" {
		return
	}
	a.bench, a.script, a.done = nil, nil, false
	perfIdle(a)
	a.loop()
}

func perfIdle(a *App) {
	perfTimeline(a)
	if os.Getenv("PERF_TIMELINE") == "1" {
		fmt.Fprintln(os.Stderr, "PERF_OPEN", platform.OpenTimeline())
	}
	if os.Getenv("PERF_FREE") == "1" {
		perfTrim()
	}
	if os.Getenv("PERF_IDLE") == "" {
		return
	}
	resources := perfResources(a)
	go func() {
		delay := 3 * time.Second
		if d, err := time.ParseDuration(os.Getenv("PERF_IDLE_DELAY")); err == nil {
			delay = d
		}
		time.Sleep(delay)
		perfWrite("idle", resources)
	}()
}

func perfResources(a *App) map[string]any {
	pages := make([][3]int, 0, a.Text.Pages())
	for i := 0; i < a.Text.Pages(); i++ {
		w, h, color := a.Text.PageInfo(i)
		bpp := 1
		if color {
			bpp = 4
		}
		pages = append(pages, [3]int{w, h, bpp})
	}
	r := map[string]any{"GPU": a.R.PerfMemory(), "AtlasPages": pages, "WindowPixels": [2]int{a.w, a.h}, "Frames": a.frames}
	if b, ok := a.b.(interface{ PerfMemory() any }); ok {
		r["BuilderMemory"] = b.PerfMemory()
	}
	return r
}

func perfCheckpoint(a *App, phase string) {
	if phase == "before" && os.Getenv("PERF_FREE") == "1" {
		perfTrim()
	}
	if os.Getenv("PERF_MEMORY") == "" {
		return
	}
	time.Sleep(2 * time.Second)
	perfWrite(phase, perfResources(a))
}

func perfTrim() {
	start := time.Now()
	debug.FreeOSMemory()
	fmt.Fprintln(os.Stderr, "PERF_FREE_NS", time.Since(start).Nanoseconds())
}

func perfWrite(phase string, resources map[string]any) {
	p := os.Getenv("PERF_MEMORY") + "-" + phase
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	resources["MemStats"] = m
	resources["PID"] = os.Getpid()
	data, err := json.MarshalIndent(resources, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(p+".json", data, 0600); err != nil {
		panic(err)
	}
	// The driver samples footprint/vmmap while the app remains alive. Do not GC
	// until after that observation: the first sample is the actual heap state.
	for i := 0; i < 600; i++ {
		if _, err := os.Stat(p + ".continue"); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if os.Getenv("PERF_HEAP") == "1" {
		runtime.GC()
		runtime.ReadMemStats(&m)
		data, _ = json.MarshalIndent(m, "", "  ")
		os.WriteFile(p+"-postgc.json", data, 0600)
		if f, err := os.Create(p + ".heap"); err == nil {
			pprof.WriteHeapProfile(f)
			f.Close()
		}
	}
}
