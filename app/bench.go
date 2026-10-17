package app

import (
	"errors"
	"runtime"
	"time"

	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
)

// BenchOptions configure RunBench.
type BenchOptions struct {
	Warm    int           // frames drawn before measuring starts (default 120)
	Frames  int           // measured frames (default 600)
	Timeout time.Duration // give up after this long (default 60 s)

	// WindowStart runs just before the measured window's baselines are taken
	// and WindowEnd just after its end values are read, so a caller can
	// snapshot an allocation profile around exactly the measured frames.
	WindowStart, WindowEnd func()
}

// BenchReport is the result of RunBench; it marshals to JSON as is.
type BenchReport struct {
	Frames int  // measured frames
	Paced  bool // every measured frame waited for a platform frame event (false: unpaced fallback, e.g. an asleep display)
	// UnpacedFrames counts measured frames drawn without a frame event.
	UnpacedFrames int

	// Exact heap allocation deltas over the measured frames (ReadMemStats).
	Mallocs    uint64
	TotalAlloc uint64
	NumGC      uint32

	// Main-thread CPU per frame, in µs. MainThreadCPUus is everything the
	// thread did over the window divided by the frames (event pump, waiting
	// excluded since it costs no CPU); the Build/Encode split is the sum of
	// the frame's own two parts: Begin..End of ui (build, layout, emit,
	// atlas upload) and BeginFrame, renderer Encode and EndFrame. FrameCPUus
	// is the distribution of their per-frame sum. A thread clock with a coarse
	// tick (Windows) makes the distribution coarse; the means stay valid.
	MainThreadCPUus float64
	BuildCPUus      float64
	EncodeCPUus     float64
	FrameCPUus      Stats
	ProcessCPUus    float64

	FrameIntervalMs Stats // between consecutive frame starts

	// InputToCommitMs: from the start of the frame (scripted input has no
	// OS timestamp) to the return of EndFrame, per frame. This is the CPU-side
	// lower bound of input-to-photon. Run measures the same from real event
	// timestamps (RunResult.InputToCommitMs).
	InputToCommitMs Stats
	// InputToPhotonMs would add the display's present feedback; it is nil
	// because gpu reports it only for a surface's first frame.
	InputToPhotonMs *Stats

	Quads, QuadsMax int // draw-list quads of the last / largest frame
	DrawCalls       int // of the last frame
	Boxes, BoxesMax int // ui boxes built, last / largest frame
}

// RunBench opens a real window and runs the Builder through warm-up and
// measured frames, driven by the Builder's Scripter (if it has one), and
// reports allocation, CPU, pacing and latency over the measured frames. The
// loop is the one Run uses, except that a frame is always owed, so the run
// shows what continuous animation costs. If the display never delivers frame
// events the loop falls back to unpaced frames and says so (Paced false).
//
// The Scripter's frame counter starts at the first frame after the startup
// frame. If the script ends before the measured frames are done, Frames is
// what was measured.
func RunBench(cfg Config, b Builder, o BenchOptions) (BenchReport, error) {
	if o.Warm <= 0 {
		o.Warm = 120
	}
	if o.Frames <= 0 {
		o.Frames = 600
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	var st StartupReport
	a, err := openWindowed(cfg, b, &st)
	if err != nil {
		return BenchReport{}, err
	}
	defer a.close()
	if s, ok := b.(Scripter); ok {
		a.script = s
	}
	bn := &benchRun{
		o:         o,
		deadline:  platform.Now() + int64(o.Timeout),
		frameCPU:  make([]float64, 0, o.Frames+16),
		intervals: make([]float64, 0, o.Frames+16),
		lat:       make([]float64, 0, o.Frames+16),
	}
	a.bench = bn
	a.p.invalidate()
	if err := a.loop(); err != nil {
		return BenchReport{}, err
	}
	if !bn.finished {
		return BenchReport{}, errors.New("app: bench ended before its measured frames (window closed)")
	}
	return bn.report, nil
}

// benchRun is the state of a RunBench: the warm-up counter, the measured
// window and its samples.
type benchRun struct {
	o         BenchOptions
	deadline  int64
	presented int // frames drawn since the bench started
	measuredN int
	measuring bool
	finished  bool

	mem0, mem1  runtime.MemStats
	cpu0, tcpu0 int64
	sumBuild    int64
	sumEnc      int64
	unpaced     int
	prevStart   int64
	quads       int
	boxes       int
	frameCPU    []float64
	intervals   []float64
	lat         []float64
	quadsMax    int
	boxesMax    int
	report      BenchReport
	_           [0]func() // not comparable
}

func (b *benchRun) measured() int { return b.measuredN }

// before opens the measured window ahead of the first measured frame: a GC so
// the window starts from a clean heap, then the baselines.
func (b *benchRun) before(a *App) {
	if b.measuring || b.finished || b.presented != b.o.Warm {
		return
	}
	runtime.GC()
	if b.o.WindowStart != nil {
		b.o.WindowStart()
	}
	runtime.ReadMemStats(&b.mem0)
	b.cpu0, b.tcpu0 = sysinfo.CPUNs(), sysinfo.ThreadCPUNs()
	b.measuring = true
}

// after accounts a drawn frame and closes the window when it is complete.
func (b *benchRun) after(a *App) {
	b.presented++
	m := &a.fm
	if b.measuring {
		b.measuredN++
		build := m.cpu2 - m.cpu1
		enc := (m.cpu1 - m.cpu0) + (m.cpu3 - m.cpu2)
		b.sumBuild += build
		b.sumEnc += enc
		b.frameCPU = append(b.frameCPU, float64(build+enc)/1e3)
		if b.prevStart != 0 {
			b.intervals = append(b.intervals, ms(m.start-b.prevStart))
		}
		if m.evT != 0 {
			if l := m.commit - m.evT; l > 0 && l < maxLatencyNs {
				b.lat = append(b.lat, ms(l))
			}
		}
		if !a.p.paced {
			b.unpaced++
		}
		b.quads, b.boxes = len(a.list.Quads), a.UI.Boxes()
		b.quadsMax, b.boxesMax = max(b.quadsMax, b.quads), max(b.boxesMax, b.boxes)
	}
	b.prevStart = m.start
	if (b.measuring && b.measuredN >= b.o.Frames) || a.scriptDone {
		b.finish(a)
	}
}

func (b *benchRun) finish(a *App) {
	if b.finished {
		return
	}
	b.finished = true
	if !b.measuring {
		a.done = true // the script ended during warm-up: nothing was measured
		return
	}
	runtime.ReadMemStats(&b.mem1)
	cpu1, tcpu1 := sysinfo.CPUNs(), sysinfo.ThreadCPUNs()
	if b.o.WindowEnd != nil {
		b.o.WindowEnd()
	}
	n := float64(max(b.measuredN, 1))
	b.report = BenchReport{
		Frames:          b.measuredN,
		Paced:           b.unpaced == 0,
		UnpacedFrames:   b.unpaced,
		Mallocs:         b.mem1.Mallocs - b.mem0.Mallocs,
		TotalAlloc:      b.mem1.TotalAlloc - b.mem0.TotalAlloc,
		NumGC:           b.mem1.NumGC - b.mem0.NumGC,
		MainThreadCPUus: float64(tcpu1-b.tcpu0) / 1e3 / n,
		BuildCPUus:      float64(b.sumBuild) / 1e3 / n,
		EncodeCPUus:     float64(b.sumEnc) / 1e3 / n,
		FrameCPUus:      Summarize(b.frameCPU),
		ProcessCPUus:    float64(cpu1-b.cpu0) / 1e3 / n,
		FrameIntervalMs: Summarize(b.intervals),
		InputToCommitMs: Summarize(b.lat),
		Quads:           b.quads, QuadsMax: b.quadsMax,
		DrawCalls: a.R.DrawCalls(),
		Boxes:     b.boxes, BoxesMax: b.boxesMax,
	}
	a.done = true
}
