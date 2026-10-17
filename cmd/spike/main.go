// Command spike is the phase-1 platform spike: a window that clears to a
// colour, logs input, wakes from a worker goroutine, and animates at vsync
// on demand.
//
// Interactive: space toggles animation, a worker changes the colour every
// 2 s, Esc or the close button quits.
//
// -bench runs a scripted sequence and prints a JSON report:
//
//	startup timeline → idle window (0 presents expected) → 200 worker
//	wakes (latency) → 120 warm-up + 600 measured animated frames (vsync
//	intervals, exact heap allocation deltas) → exit
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/gpu"
	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
)

type report struct {
	OS, Arch             string
	ExecToMainMs         float64
	MainToOpenMs         float64
	OpenToSurfaceMs      float64
	ExecToFirstPresent   float64 `json:"ExecToFirstPresentMs"`
	ExecToVisibleMs      float64 // first present AND occlusion state visible (an OS signal, not proof of pixels); -1 if never visible
	IdleSeconds          float64
	IdlePresents         int
	IdleCPUms            float64
	IdleWakeups          uint64 // package-idle wakeups (macOS Activity Monitor "Idle Wake Ups")
	IdleInterruptWakeups uint64
	IdleFootprintMB      float64 // empty-demo memory: footprint at the end of the idle window
	IdleRSSMB            float64
	WakeLatencyUs        stats
	Frames               int
	FramesPaced          bool // false: no EvFrame arrived (window occluded / display asleep); frames ran unpaced
	PresentFailures      int  // Present returned false (occluded / device lost); retried after presentRetryNs
	FrameCPUus           float64
	FrameIntervalMs      stats
	FrameAllocObjects    uint64
	FrameAllocBytes      uint64
	FrameGCs             uint64
	FootprintAfterAnimMB float64
	RSSAfterAnimMB       float64
	GoHeapMB             float64
}

type stats struct{ P50, P99, Max, Mean float64 }

func summarize(xs []float64) stats {
	if len(xs) == 0 {
		return stats{}
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	return stats{P50: s[len(s)/2], P99: s[(len(s)*99)/100], Max: s[len(s)-1], Mean: sum / float64(len(s))}
}

type benchPhase int

const (
	phaseStartup benchPhase = iota
	phaseIdle
	phaseWake
	phaseAnimate
	phaseDone
)

const (
	wakeCount     = 200
	animFrames    = 600 // measured, after animWarmup
	animWarmup    = 120
	visibleWaitNs = int64(3 * time.Second)
	// presentRetryNs backs off after a failed Present (e.g. DXGI_STATUS_OCCLUDED)
	// instead of spinning on it.
	presentRetryNs = int64(50 * time.Millisecond)
)

var (
	wakeSentAt atomic.Int64 // Now() when the worker called Wake (bench)
	wakeAck    = make(chan struct{}, 1)
	curPhase   atomic.Int32 // bench phase, for the -timeout message
	colorIndex atomic.Uint32
)

var palette = [...][3]float32{{0.11, 0.12, 0.14}, {0.10, 0.18, 0.28}, {0.22, 0.12, 0.10}, {0.10, 0.20, 0.13}}

func main() {
	tMain := platform.Now()
	age := sysinfo.ProcessAgeNs()
	bench := flag.Bool("bench", false, "run the scripted benchmark and print a JSON report")
	exitFirst := flag.Bool("exit-first", false, "exit after the first frame is presented and the window is visible")
	quiet := flag.Bool("quiet", false, "don't log events")
	idleDuration := flag.Duration("idle", 3*time.Second, "bench: idle window length")
	gate := flag.Bool("gate", false, "bench: exit with status 3 unless idle presented nothing, animated frames allocated nothing and were vsync-paced (CI regression gate)")
	allowUnpaced := flag.Bool("allow-unpaced", false, "gate: accept unpaced frames (no frame callbacks, e.g. display asleep)")
	deadline := flag.Duration("timeout", 0, "abort with exit status 2 if the run has not finished after this long (0: never)")
	flag.Parse()
	if *deadline > 0 {
		time.AfterFunc(*deadline, func() {
			fmt.Fprintf(os.Stderr, "spike: timed out after %v in bench phase %d\n", *deadline, curPhase.Load())
			os.Exit(2)
		})
	}

	ms := func(ns int64) float64 { return float64(ns) / 1e6 }
	rep := report{OS: runtime.GOOS, Arch: runtime.GOARCH, ExecToMainMs: -1}
	execNs := int64(-1) // Now() at exec, if known
	if age >= 0 {
		execNs = tMain - age
		rep.ExecToMainMs = ms(age)
	}
	sinceExec := func(t int64) float64 {
		if execNs < 0 {
			return -1
		}
		return ms(t - execNs)
	}

	win, err := platform.Open(platform.Options{Title: "gnui spike", Width: 960, Height: 600})
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	tOpen := platform.Now()
	rep.MainToOpenMs = ms(tOpen - tMain)
	w, h, scale := win.Size()
	surf, err := gpu.NewSurface(win.Native(), w, h, scale)
	if err != nil {
		fmt.Fprintln(os.Stderr, "surface:", err)
		os.Exit(1)
	}
	win.SetFrameWaitable(surf.FrameWaitable())
	rep.OpenToSurfaceMs = ms(platform.Now() - tOpen)

	if !*bench && !*exitFirst {
		go func() { // colour changes from a worker goroutine
			for range time.Tick(2 * time.Second) {
				colorIndex.Add(1)
				platform.Wake()
			}
		}()
	}

	var (
		phase                      = phaseStartup
		animating                  bool
		needDraw, frameReady       = true, true
		firstPresent, visibleAt    int64
		presents, idlePresentsBase int
		idleEnd                    int64
		idleSnap                   sysinfo.Snapshot
		wakeLat                    = make([]float64, 0, wakeCount)
		intervals                  = make([]float64, 0, animFrames+16) // headroom: append must never grow inside the measured window
		lastFrame                  int64
		requestedAt                int64
		presentRetryAt             int64
		paced                      = true
		animCPU0                   int64
		// ReadMemStats flushes the per-P allocation caches, so its deltas are
		// exact. runtime/metrics heap counters are credited a whole span at a
		// time and miss small allocations (see DECISIONS D11).
		mem0, mem1 runtime.MemStats
		t          float32
	)
	startWorkerWakes := func() {
		go func() {
			// One wake in flight at a time: Wake coalesces, so a wake sent
			// before the loop reported the previous one would be lost.
			for i := 0; i < wakeCount; i++ {
				time.Sleep(5 * time.Millisecond)
				wakeSentAt.Store(platform.Now())
				platform.Wake()
				<-wakeAck
			}
		}()
	}

	for phase != phaseDone {
		timeout := int64(-1)
		if phase == phaseAnimate && !frameReady && paced && requestedAt != 0 && platform.Now()-requestedAt > int64(time.Second) {
			paced, frameReady = false, true // display link silent: occluded window or display asleep
		}
		switch {
		case needDraw && frameReady && presentRetryAt == 0:
			timeout = 0
			if phase == phaseAnimate {
				// Unpaced frames still take the finite-deadline Wait path that
				// paced frames use, so the allocation gate covers it.
				timeout = 1
			}
		case phase == phaseAnimate && paced:
			timeout = max(requestedAt+int64(time.Second)-platform.Now(), 0)
		case phase == phaseIdle:
			timeout = max(idleEnd-platform.Now(), 0)
		case phase == phaseStartup && (*bench || *exitFirst) && firstPresent != 0:
			timeout = max(firstPresent+visibleWaitNs-platform.Now(), 0)
		}
		if presentRetryAt != 0 {
			if r := max(presentRetryAt-platform.Now(), 0); timeout < 0 || r < timeout {
				timeout = r
			}
		}
		for _, e := range win.Wait(timeout) {
			switch e.Kind {
			case platform.EvClose:
				phase = phaseDone
			case platform.EvResize:
				surf.Resize(e.W, e.H, e.Scale)
				needDraw = true
				if !*quiet && !*bench {
					fmt.Printf("resize %dx%d @%.2f\n", e.W, e.H, e.Scale)
				}
			case platform.EvVisible:
				if e.Flags&platform.FlagDown != 0 && visibleAt == 0 {
					visibleAt = platform.Now()
				}
				needDraw = true
			case platform.EvFrame:
				frameReady = true
				if phase == phaseAnimate {
					now := platform.Now()
					if lastFrame != 0 && presents-idlePresentsBase > animWarmup {
						intervals = append(intervals, ms(now-lastFrame))
					}
					lastFrame = now
				}
			case platform.EvWake:
				if phase == phaseWake {
					wakeLat = append(wakeLat, float64(platform.Now()-wakeSentAt.Load())/1e3)
					wakeAck <- struct{}{}
					if len(wakeLat) == wakeCount {
						phase = phaseAnimate
						animating, needDraw = true, true
						idlePresentsBase = presents
					}
				} else {
					needDraw = true
				}
			case platform.EvKey:
				if e.Flags&platform.FlagDown != 0 {
					switch e.Key {
					case platform.KeyEscape:
						phase = phaseDone
					case platform.KeySpace:
						if !*bench {
							animating = !animating
							needDraw = true
						}
					}
				}
				if !*quiet && !*bench {
					fmt.Printf("key %d code=%d mods=%#x down=%v repeat=%v\n", e.Key, e.Code, e.Mods, e.Flags&platform.FlagDown != 0, e.Flags&platform.FlagRepeat != 0)
				}
			case platform.EvText:
				if !*quiet && !*bench {
					fmt.Printf("text %q\n", win.Text(e.Text))
				}
			case platform.EvPointerButton, platform.EvScroll, platform.EvFocus:
				if !*quiet && !*bench {
					fmt.Printf("event kind=%d button=%d xy=(%.0f,%.0f) d=(%.1f,%.1f) flags=%#x\n", e.Kind, e.Button, e.X, e.Y, e.DX, e.DY, e.Flags)
				}
			}
		}

		if presentRetryAt != 0 && platform.Now() >= presentRetryAt {
			presentRetryAt, frameReady = 0, true
		}

		// Benchmark state machine.
		switch phase {
		case phaseStartup:
			if (*bench || *exitFirst) && firstPresent != 0 && (visibleAt != 0 || platform.Now()-firstPresent > visibleWaitNs) {
				rep.ExecToFirstPresent = sinceExec(firstPresent)
				rep.ExecToVisibleMs = -1
				if visibleAt != 0 {
					rep.ExecToVisibleMs = sinceExec(max(visibleAt, firstPresent))
				}
				if *exitFirst {
					printJSON(rep)
					return
				}
				phase = phaseIdle
				idleEnd = platform.Now() + int64(*idleDuration)
				idlePresentsBase = presents
				idleSnap = sysinfo.Read()
				needDraw = false
			}
		case phaseIdle:
			if platform.Now() >= idleEnd {
				s := sysinfo.Read()
				rep.IdleSeconds = idleDuration.Seconds()
				rep.IdleFootprintMB = float64(s.Footprint) / (1 << 20)
				rep.IdleRSSMB = float64(s.RSS) / (1 << 20)
				rep.IdlePresents = presents - idlePresentsBase
				rep.IdleCPUms = ms(s.CPUNs - idleSnap.CPUNs)
				rep.IdleWakeups = s.Wakeups - idleSnap.Wakeups
				rep.IdleInterruptWakeups = s.InterruptWakeups - idleSnap.InterruptWakeups
				phase = phaseWake
				startWorkerWakes()
			}
		}

		if needDraw && frameReady && presentRetryAt == 0 {
			if phase == phaseAnimate && presents-idlePresentsBase == animWarmup {
				runtime.GC()
				runtime.ReadMemStats(&mem0)
				animCPU0 = cpuNs()
			}
			c := palette[int(colorIndex.Load())%len(palette)]
			if animating {
				t += 1.0 / 60
				k := float32(0.5 + 0.5*math.Sin(float64(t)*2))
				c = [3]float32{c[0] + 0.25*k, c[1] + 0.1*k, c[2] + 0.3*(1-k)}
			}
			win.RequestFrame()
			requestedAt = platform.Now()
			if surf.Present(c[0], c[1], c[2], 1) {
				presents++
				frameReady = !paced
				if firstPresent == 0 {
					firstPresent = platform.Now()
				}
			} else {
				rep.PresentFailures++
				presentRetryAt = platform.Now() + presentRetryNs
			}
			needDraw = animating || presentRetryAt != 0 // a failed frame is still owed
			if phase == phaseAnimate && presents-idlePresentsBase >= animWarmup+animFrames {
				cpu1 := cpuNs()
				runtime.ReadMemStats(&mem1)
				rep.Frames = presents - idlePresentsBase - animWarmup
				rep.FrameAllocObjects = mem1.Mallocs - mem0.Mallocs
				rep.FrameAllocBytes = mem1.TotalAlloc - mem0.TotalAlloc
				rep.FrameGCs = uint64(mem1.NumGC - mem0.NumGC)
				rep.FramesPaced = paced
				rep.FrameCPUus = float64(cpu1-animCPU0) / 1e3 / float64(rep.Frames)
				phase = phaseDone
			}
		}
		curPhase.Store(int32(phase))
	}

	if *bench {
		rep.WakeLatencyUs = summarize(wakeLat)
		rep.FrameIntervalMs = summarize(intervals)
		time.Sleep(time.Second) // let transient startup memory settle a little
		s := sysinfo.Read()
		rep.FootprintAfterAnimMB = float64(s.Footprint) / (1 << 20)
		rep.RSSAfterAnimMB = float64(s.RSS) / (1 << 20)
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		rep.GoHeapMB = float64(m.HeapAlloc) / (1 << 20)
		printJSON(rep)
	}
	surf.Destroy()
	win.Close()
	if *bench && *gate {
		var failed []string
		if rep.Frames != animFrames {
			failed = append(failed, fmt.Sprintf("%d measured frames, want %d", rep.Frames, animFrames))
		}
		if !rep.FramesPaced && !*allowUnpaced {
			failed = append(failed, "frames were not vsync-paced (no frame callbacks)")
		}
		if rep.IdlePresents != 0 {
			failed = append(failed, fmt.Sprintf("%d presents while idle", rep.IdlePresents))
		}
		if rep.FrameAllocObjects != 0 || rep.FrameAllocBytes != 0 {
			failed = append(failed, fmt.Sprintf("animated frames allocated %d objects / %d bytes", rep.FrameAllocObjects, rep.FrameAllocBytes))
		}
		if len(failed) > 0 {
			fmt.Fprintln(os.Stderr, "spike: gate failed:", strings.Join(failed, "; "))
			os.Exit(3)
		}
	}
}

// cpuNs is process CPU time; it must not allocate (it runs inside the measured window).
func cpuNs() int64 { return sysinfo.CPUNs() }

func printJSON(r report) {
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
}
