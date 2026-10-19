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
	"gonativeui/internal/allocsite"
	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
)

type report struct {
	OS, Arch             string
	ExecToMainMs         float64
	MainToOpenMs         float64
	OpenToSurfaceMs      float64
	OpenStagesMs         []stageMs  // platform.Open's timeline, each stage's duration
	FirstWaitMs          float64    // the loop's first Wait (pending AppKit/OS events) before the first frame
	FirstPresentMs       [3]float64 // first Present: acquire drawable, encode+commit, first-frame flush
	ExecToFirstPresent   float64    `json:"ExecToFirstPresentMs"`
	ExecToOnGlassMs      float64    // first frame shown on the display (OS present feedback); -1 if unknown
	ExecToVisibleMs      float64    // first present AND occlusion state visible (an OS signal, not proof of pixels); -1 if never visible
	IdleSeconds          float64
	IdlePresents         int
	IdleCPUms            float64
	IdleWakeups          uint64 // package-idle wakeups (macOS Activity Monitor "Idle Wake Ups")
	IdleInterruptWakeups uint64
	IdleFootprintMB      float64 // empty-demo memory: footprint at the end of the idle window
	IdleRSSMB            float64
	WakeLatencyUs        stats
	Frames               int
	FramesPaced          bool // false: some measured frames were drawn without an EvFrame (window occluded / display asleep)
	UnpacedFrames        int  // measured frames drawn without an EvFrame
	// FrameSourceStalls counts the times the frame events stopped for over a
	// second while a frame was requested, and came back, during the animated
	// frames. A stall with evidence that the environment caused it (the
	// window state, and the main thread's heartbeat: see silenceInfo.cause)
	// restarted the measured frames, up to maxRestarts times
	// (MeasureRestarts). StallCause is the longest stall's cause
	// (StallTicks: display-link ticks during it; StallMainBusyMs: the
	// longest the main thread was out of its run loop then). AppBugStall is
	// the first stall that was the program's fault: it fails the gate, and
	// nothing after it clears it.
	FrameSourceStalls int
	LongestStallMs    float64
	MeasureRestarts   int
	StallCause        string `json:",omitempty"`
	StallTicks        uint64
	StallMainBusyMs   float64
	AppBugStall       string `json:",omitempty"`
	AppBugStallMs     float64
	UnpacedCause      string `json:",omitempty"` // why measured frames stayed unpaced
	UnpacedState      string `json:",omitempty"` // the window state when frames went unpaced for good
	// MainBusyMsMax is the longest the main thread spent out of its run
	// loop (between two Waits: events, the frame, its present) over the
	// measured frames, and PresentMsMax the longest Present.
	MainBusyMsMax     float64
	PresentMsMax      float64
	PresentFailures   int // Present returned false (occluded / device lost); retried after presentRetryNs
	FrameCPUus        float64
	FrameIntervalMs   stats
	FrameAllocObjects uint64
	FrameAllocBytes   uint64
	// AllocSites attributes the allocations of the windows that allocated;
	// OwnAllocObjects counts those of our code, which fail the gate even if
	// a later window was clean (DECISIONS D27).
	AllocSites           []allocsite.Site `json:",omitempty"`
	OwnAllocObjects      int64
	AllocWindows         [][2]uint64 // objects and bytes of each window measured for the allocation gate
	FrameGCs             uint64
	FootprintAfterAnimMB float64
	RSSAfterAnimMB       float64
	GoHeapMB             float64
}

type stats struct{ P50, P99, Max, Mean float64 }

type stageMs struct {
	Stage string
	Ms    float64
}

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
	// silenceNs is how long a requested frame may take before the loop calls
	// the frame source silent and draws unpaced (app's pacer does the same).
	silenceNs = int64(time.Second)
	// maxRestarts bounds how often a stall restarts the measured frames; later
	// stalls stay in them (their unpaced frames, if any, count as such).
	maxRestarts = 3
	// maxAllocWindows bounds the windows of animFrames measured for the
	// allocation gate (DECISIONS D27): it passes on the first that allocated
	// nothing. An allocation of the frame loop is in every window; a one-off
	// of the process (the runtime making a new M: CI, Windows arm64) misses
	// most.
	maxAllocWindows = 6
)

var (
	wakeSentAt atomic.Int64 // Now() when the worker called Wake (bench)
	wakeAck    = make(chan struct{}, 1)
	curPhase   atomic.Int32 // bench phase, for the -timeout message
	silentAt   atomic.Int64 // when the frame source went silent, 0 while it delivers (for the -timeout message)
	stallCount atomic.Int32
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
	allocSites := flag.Bool("allocsites", false, "bench: print the call stacks of the allocations attributed in the measured windows that allocated")
	testBlock := flag.Duration("test-block", 0, "bench: a test of the stall check: block the main thread this long once, in the 200th measured frame, with a frame requested (the gate must fail)")
	showFirst := flag.Bool("show-first", false, "startup: show the window at Open, before its first frame (old order, for comparison)")
	waitFirst := flag.Bool("wait-first", false, "startup: pump OS events before drawing the first frame (old order, for comparison)")
	deadline := flag.Duration("timeout", 0, "abort with exit status 2 if the run has not finished after this long (0: never)")
	flag.Parse()
	if *deadline > 0 {
		time.AfterFunc(*deadline, func() {
			msg := ""
			if t := silentAt.Load(); t != 0 {
				msg = fmt.Sprintf("; the frame events stopped %.1f s ago (%d earlier stalls)", float64(platform.Now()-t)/1e9, stallCount.Load())
			}
			fmt.Fprintf(os.Stderr, "spike: timed out after %v in bench phase %d%s\n", *deadline, curPhase.Load(), msg)
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

	win, err := platform.Open(platform.Options{Title: "gnui spike", Width: 960, Height: 600, Hidden: !*showFirst})
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	tOpen := platform.Now()
	rep.MainToOpenMs = ms(tOpen - tMain)
	prev := tMain
	for _, st := range platform.OpenTimeline() {
		rep.OpenStagesMs = append(rep.OpenStagesMs, stageMs{st.Name, ms(st.T - prev)})
		prev = st.T
	}
	w, h, scale := win.Size()
	surf, err := gpu.NewSurface(win.Native(), w, h, scale)
	if err != nil {
		fmt.Fprintln(os.Stderr, "surface:", err)
		os.Exit(1)
	}
	win.SetFrameSource(surf.FrameSource())
	if !*showFirst {
		surf.RevealOnFirstFrame(win.Show) // content-first: the window appears with its first frame
	}
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
		silent0                    platform.DebugState // the window state when the frame source went silent
		stalls                     stallLog
		waitRet                    int64   // when the last Wait returned
		harness                    int64   // the bench's own work (collections, profiles) since then
		longBusyEnd                int64   // when the main thread's last long stretch out of its run loop ended
		longBusyMs                 float64 // its length
		blocked                    bool    // -test-block done
		unpaced                    int     // measured frames presented without a frame event
		silence                    int64   // when it did (the request that got no frame)
		// ReadMemStats flushes the per-P allocation caches, so its deltas are
		// exact. runtime/metrics heap counters are credited a whole span at a
		// time and miss small allocations (see DECISIONS D11).
		mem0, mem1 runtime.MemStats
		snap0      allocsite.Snapshot // the allocation profile at the start of the window
		snap1      allocsite.Snapshot
		profiling  bool
		allocOnly  bool // measuring a further window for the allocation gate only
		winStart   int  // presents at the start of that window
		t          float32
	)
	// busySince is how long the main thread's last long stretch out of its
	// run loop was, if it ended after t (when a request went silent).
	busySince := func(t int64) float64 {
		if longBusyEnd >= t {
			return longBusyMs
		}
		return 0
	}
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

	tLoop0, tWait0 := platform.Now(), int64(0)
	for phase != phaseDone {
		timeout := int64(-1)
		if waitRet != 0 {
			now := platform.Now()
			busy := ms(now - waitRet - harness)
			if busy > mainBusyLimitMs {
				longBusyEnd, longBusyMs = now, busy
			}
			if phase == phaseAnimate && !allocOnly && presents-idlePresentsBase > animWarmup {
				rep.MainBusyMsMax = max(rep.MainBusyMsMax, busy)
			}
		}
		harness = 0
		if phase == phaseAnimate && !frameReady && paced && requestedAt != 0 && platform.Now()-requestedAt > silenceNs {
			paced, frameReady = false, true // display link silent: occluded window or display asleep
			silence, silent0 = requestedAt, win.DebugState()
			silentAt.Store(silence)
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
			if visibleAt != 0 {
				timeout = min(timeout, int64(5*time.Millisecond)) // poll for the on-glass feedback, set off-thread
			}
		}
		if presentRetryAt != 0 {
			if r := max(presentRetryAt-platform.Now(), 0); timeout < 0 || r < timeout {
				timeout = r
			}
		}
		var evs []platform.Event
		if firstPresent != 0 || *waitFirst {
			// The first Wait runs AppKit's deferred launch work (~50-90 ms on
			// the MacBook), so by default the first frame is drawn before it.
			evs = win.Wait(timeout)
			waitRet = platform.Now()
			if tWait0 == 0 {
				tWait0 = platform.Now()
				rep.FirstWaitMs = ms(tWait0 - tLoop0)
			}
		}
		for _, e := range evs {
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
				presentRetryAt = 0 // a fresh frame supersedes the retry backstop
				if !paced && phase == phaseAnimate {
					// The frame source is back after a silence (app's pacer
					// returns to paced frames the same way). The silence
					// is not measured: the animated frames start over.
					now := platform.Now()
					sl := silenceInfo{a: silent0, b: win.DebugState(), mainBusyMs: busySince(silence)}
					cause := sl.cause()
					d := ms(now - silence)
					stallCount.Add(1)
					if d >= stalls.LongestMs {
						rep.StallTicks, rep.StallMainBusyMs = sl.b.Ticks-sl.a.Ticks, sl.mainBusyMs
					}
					// A further allocation window does not care about
					// pacing: its stalls restart nothing.
					restart := stalls.end(cause, d, !allocOnly)
					paced, silence = true, 0
					silentAt.Store(0)
					if allocOnly {
					} else if restart {
						fmt.Fprintf(os.Stderr, "spike: the frame events stopped for %.1f s (%s); the measured frames start over\n", d/1e3, cause)
						idlePresentsBase, lastFrame, intervals, unpaced = presents, 0, intervals[:0], 0
					} else {
						fmt.Fprintf(os.Stderr, "spike: the frame events stopped for %.1f s (%s); measured as it was\n", d/1e3, cause)
					}
				}
				if phase == phaseAnimate && !allocOnly {
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
			if (*bench || *exitFirst) && firstPresent != 0 && ((visibleAt != 0 && surf.Trace.OnGlass.Load() != 0) || platform.Now()-firstPresent > visibleWaitNs) {
				rep.ExecToFirstPresent = sinceExec(firstPresent)
				rep.ExecToVisibleMs, rep.ExecToOnGlassMs = -1, -1
				if t := surf.Trace.OnGlass.Load(); t != 0 {
					rep.ExecToOnGlassMs = sinceExec(t)
				}
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
			if phase == phaseAnimate && (!allocOnly && presents-idlePresentsBase == animWarmup || allocOnly && presents == winStart) {
				h0 := platform.Now()
				if !profiling {
					// Every allocation is profiled from here on (the
					// startup above is measured without it); the storage
					// of the snapshots is made before the collection.
					profiling = true
					allocsite.Enable()
					snap0.Take()
					snap1.Take()
				}
				runtime.GC()
				snap0.Take()
				harness += platform.Now() - h0
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
			p0 := platform.Now()
			ok := surf.Present(c[0], c[1], c[2], 1)
			if phase == phaseAnimate && !allocOnly && presents-idlePresentsBase >= animWarmup {
				rep.PresentMsMax = max(rep.PresentMsMax, ms(platform.Now()-p0))
			}
			if ok {
				presents++
				if *testBlock > 0 && !blocked && phase == phaseAnimate && !allocOnly && presents-idlePresentsBase == animWarmup+200 {
					// What a frame callback that blocks looks like: the
					// frame is requested, and the main thread is not back
					// in its run loop for the tick that delivers it.
					blocked = true
					time.Sleep(*testBlock)
				}
				if !paced && phase == phaseAnimate && !allocOnly && presents-idlePresentsBase > animWarmup {
					unpaced++
				}
				frameReady = !paced
				if firstPresent == 0 {
					firstPresent = platform.Now()
					if tr := &surf.Trace; tr.Start != 0 && tr.Done != 0 {
						rep.FirstPresentMs = [3]float64{ms(tr.Acquired - tr.Start), ms(tr.Committed - tr.Acquired), ms(tr.Done - tr.Committed)}
					}
				}
			} else {
				rep.PresentFailures++
				presentRetryAt = platform.Now() + presentRetryNs
				// Paced: wait for the next frame. On macOS a tick's drawable lives
				// one tick, so a frame left unused earlier fails here, and
				// BeginFrame has already requested the next one.
				frameReady = !paced
			}
			needDraw = animating || presentRetryAt != 0 // a failed frame is still owed
			if phase == phaseAnimate && allocOnly && presents-winStart >= animFrames {
				runtime.ReadMemStats(&mem1)
				h0 := platform.Now()
				attribute(&rep, &mem0, &mem1, *allocSites, &snap0, &snap1)
				phase = allocWindow(&rep, &mem0, &mem1)
				harness += platform.Now() - h0
				winStart = presents
			}
			if phase == phaseAnimate && !allocOnly && presents-idlePresentsBase >= animWarmup+animFrames {
				cpu1 := cpuNs()
				runtime.ReadMemStats(&mem1)
				h0 := platform.Now()
				attribute(&rep, &mem0, &mem1, *allocSites, &snap0, &snap1) // before the report below allocates
				harness += platform.Now() - h0
				rep.Frames = presents - idlePresentsBase - animWarmup
				rep.FrameGCs = uint64(mem1.NumGC - mem0.NumGC)
				rep.UnpacedFrames = unpaced
				rep.FramesPaced = unpaced == 0
				if !rep.FramesPaced {
					st := win.DebugState()
					rep.UnpacedState = string(st.Append(nil))
					if silence != 0 { // still silent: this silence's cause
						sl := silenceInfo{a: silent0, b: st, mainBusyMs: busySince(silence)}
						rep.UnpacedCause = sl.cause()
						stalls.unpaced(rep.UnpacedCause, ms(platform.Now()-silence))
					} else {
						rep.UnpacedCause = "stalls past the restarts or without evidence: " + stalls.LongestCause
					}
				}
				rep.FrameCPUus = float64(cpu1-animCPU0) / 1e3 / float64(rep.Frames)
				phase = allocWindow(&rep, &mem0, &mem1)
				allocOnly, winStart = true, presents
			}
		}
		curPhase.Store(int32(phase))
	}

	if *bench {
		rep.FrameSourceStalls, rep.LongestStallMs, rep.MeasureRestarts = stalls.Stalls, stalls.LongestMs, stalls.Restarts
		rep.StallCause, rep.AppBugStall, rep.AppBugStallMs = stalls.LongestCause, stalls.AppBug, stalls.AppBugMs
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
			failed = append(failed, fmt.Sprintf("%d of %d measured frames were not vsync-paced, drawn without a frame callback (%s; %s); -allow-unpaced accepts a machine without a working display",
				rep.UnpacedFrames, rep.Frames, rep.UnpacedCause, rep.UnpacedState))
		}
		if rep.AppBugStall != "" {
			failed = append(failed, fmt.Sprintf("the frame events stopped for %.1f s because %s", rep.AppBugStallMs/1e3, rep.AppBugStall))
		}
		if rep.OwnAllocObjects != 0 {
			failed = append(failed, fmt.Sprintf("%d allocation(s) of our code in the measured windows (D27; -allocsites prints them)", rep.OwnAllocObjects))
		}
		if rep.IdlePresents != 0 {
			failed = append(failed, fmt.Sprintf("%d presents while idle", rep.IdlePresents))
		}
		if rep.FrameAllocObjects != 0 || rep.FrameAllocBytes != 0 {
			failed = append(failed, fmt.Sprintf("animated frames allocated in each of %d windows of %d frames (objects, bytes: %v)", len(rep.AllocWindows), animFrames, rep.AllocWindows))
		}
		if len(failed) > 0 {
			fmt.Fprintln(os.Stderr, "spike: gate failed:", strings.Join(failed, "; "))
			os.Exit(3)
		}
	}
}

// allocWindow records a measured window's allocations and returns the next
// phase: done once a window allocated nothing or maxAllocWindows were
// measured, else another window.
func allocWindow(rep *report, mem0, mem1 *runtime.MemStats) benchPhase {
	o, b := mem1.Mallocs-mem0.Mallocs, mem1.TotalAlloc-mem0.TotalAlloc
	rep.AllocWindows = append(rep.AllocWindows, [2]uint64{o, b})
	rep.FrameAllocObjects, rep.FrameAllocBytes = o, b
	if o == 0 && b == 0 {
		return phaseDone
	}
	fmt.Fprintf(os.Stderr, "spike: window %d of %d frames allocated %d objects / %d bytes\n", len(rep.AllocWindows), animFrames, o, b)
	if len(rep.AllocWindows) == maxAllocWindows {
		return phaseDone
	}
	return phaseAnimate
}

// attribute says whose a window's allocations were, right after its end
// values are read: the runtime's own one-offs may pass on a later window,
// an allocation of ours fails the gate whatever follows (DECISIONS D27).
func attribute(rep *report, mem0, mem1 *runtime.MemStats, sites bool, snap0, snap1 *allocsite.Snapshot) {
	if mem1.Mallocs == mem0.Mallocs && mem1.TotalAlloc == mem0.TotalAlloc {
		return
	}
	runtime.GC()
	snap1.Take()
	ws := allocsite.Diff(snap0, snap1)
	rep.AllocSites = append(rep.AllocSites, ws...)
	rep.OwnAllocObjects += allocsite.Objects(ws, allocsite.Ours)
	if sites {
		fmt.Fprintf(os.Stderr, "spike: allocations of a measured window (%d of our code):\n", allocsite.Objects(ws, allocsite.Ours))
		allocsite.Write(os.Stderr, ws)
	}
}

// cpuNs is process CPU time; it must not allocate (it runs inside the measured window).
func cpuNs() int64 { return sysinfo.CPUNs() }

func printJSON(r report) {
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
}
