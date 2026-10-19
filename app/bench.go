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

	// PostGCWarm is the number of frames drawn after the collection that
	// precedes the measured window and before its baselines. runtime.GC empties
	// the runtime's central pools, so the first frames after it may allocate
	// once on a path that refills one; these frames refill them through the
	// paths the script exercises (default 0).
	PostGCWarm int

	// Windows is how many measured windows to run at most (default 1). They
	// follow each other without a collection in between; the run stops at the
	// first window with no allocation at all, and reports the window with the
	// fewest (BenchReport.Windows lists every one). A real per-frame or
	// per-cycle allocation is in every window; the runtime's own background
	// allocations (a new OS thread, the scavenger's timer heap, a sudog when
	// some goroutine parks through a channel or sync primitive) are one-offs
	// that miss most windows.
	Windows int

	// WindowStart runs just before a measured window's baselines are taken
	// and WindowEnd just after its end values are read, so a caller can
	// snapshot an allocation profile around exactly the measured frames.
	WindowStart, WindowEnd func()

	// Verbose prints the state dump (StateLine) to stderr while frames stop
	// coming, as RunOptions.Verbose does. A timeout always prints it.
	Verbose bool
}

// CycleAligned is optionally implemented by a Builder whose Scripter repeats a
// fixed session. RunBench then measures whole sessions: a window starts when
// the next frame begins one (AtCycleStart) and ends when the next one would
// begin again, so Frames is ignored and every window contains the entire
// session, which makes anything the session allocates show in every window.
type CycleAligned interface{ AtCycleStart() bool }

// BenchWindow is the allocation result of one measured window.
type BenchWindow struct {
	// Frames is the number of frames measured in the window.
	Frames int
	// Mallocs is the number of heap allocations over the window.
	Mallocs uint64
	// TotalAlloc is the number of heap bytes allocated over the window.
	TotalAlloc uint64
	// NumGC is the number of garbage collections completed over the window.
	NumGC uint32
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

	// Windows lists every measured window, in order; the fields above are those
	// of the one with the fewest allocations (the first of equals).
	Windows []BenchWindow
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
	if o.Windows <= 0 {
		o.Windows = 1
	}
	var st StartupReport
	a, err := openWindowed(cfg, b, &st, false)
	if err != nil {
		return BenchReport{}, err
	}
	defer a.close()
	a.verbose = o.Verbose
	if s, ok := b.(Scripter); ok {
		a.script = s
	}
	bn := &benchRun{o: o, deadline: platform.Now() + int64(o.Timeout)}
	bn.cyc, _ = b.(CycleAligned)
	n := o.Frames + 16
	if bn.cyc != nil {
		n = maxCycleFrames // a session's frames are not known beforehand
	}
	bn.frameCPU, bn.intervals, bn.lat = make([]float64, 0, n), make([]float64, 0, n), make([]float64, 0, n)
	bn.windows = make([]BenchWindow, 0, o.Windows)
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

// maxCycleFrames bounds the samples kept per window of a CycleAligned run
// (their storage is made up front so recording them allocates nothing); frames
// beyond it still count, they are just not sampled.
const maxCycleFrames = 16384

// benchRun is the state of a RunBench: the warm-up counter, the measured
// windows and their samples.
type benchRun struct {
	o        BenchOptions
	cyc      CycleAligned
	deadline int64

	presented int // frames drawn since the bench started
	gcDone    bool
	gcAt      int // presented when the collection ran
	measuredN int // frames in the current window
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

	windows []BenchWindow
	best    BenchReport
	bestIdx int
	report  BenchReport
	_       [0]func() // not comparable
}

func (b *benchRun) measured() int { return b.measuredN }

// before opens a measured window ahead of its first frame. The first is
// preceded by a collection so the window starts from a clean heap, then
// PostGCWarm frames, then (for a CycleAligned builder) the start of a session;
// later windows follow the previous one directly.
func (b *benchRun) before(a *App) {
	if b.measuring || b.finished || b.presented < b.o.Warm {
		return
	}
	if !b.gcDone {
		runtime.GC()
		b.gcDone, b.gcAt = true, b.presented
	}
	if b.presented-b.gcAt < b.o.PostGCWarm || (b.cyc != nil && !b.cyc.AtCycleStart()) {
		return
	}
	b.begin(a)
}

// begin takes the baselines of a window.
func (b *benchRun) begin(a *App) {
	if b.o.WindowStart != nil {
		b.o.WindowStart()
	}
	b.measuredN, b.sumBuild, b.sumEnc, b.unpaced = 0, 0, 0, 0
	b.quadsMax, b.boxesMax = 0, 0
	b.frameCPU, b.intervals, b.lat = b.frameCPU[:0], b.intervals[:0], b.lat[:0]
	runtime.ReadMemStats(&b.mem0)
	b.cpu0, b.tcpu0 = sysinfo.CPUNs(), sysinfo.ThreadCPUNs()
	b.measuring = true
}

// sample appends v unless the storage made up front is full.
func sample(s []float64, v float64) []float64 {
	if len(s) < cap(s) {
		s = append(s, v)
	}
	return s
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
		b.frameCPU = sample(b.frameCPU, float64(build+enc)/1e3)
		if b.prevStart != 0 {
			b.intervals = sample(b.intervals, ms(m.start-b.prevStart))
		}
		if m.evT != 0 {
			if l := m.commit - m.evT; l > 0 && l < maxLatencyNs {
				b.lat = sample(b.lat, ms(l))
			}
		}
		if !a.p.paced {
			b.unpaced++
		}
		b.quads, b.boxes = len(a.list.Quads), a.UI.Boxes()
		b.quadsMax, b.boxesMax = max(b.quadsMax, b.quads), max(b.boxesMax, b.boxes)
	}
	b.prevStart = m.start
	switch {
	case a.scriptDone:
		b.endWindow(a)
	case b.measuring && b.cyc != nil && b.cyc.AtCycleStart():
		b.endWindow(a)
	case b.measuring && b.cyc == nil && b.measuredN >= b.o.Frames:
		b.endWindow(a)
	}
}

// endWindow closes the window, then either opens the next one or finishes.
func (b *benchRun) endWindow(a *App) {
	if b.finished {
		return
	}
	if !b.measuring {
		b.finished = true
		a.done = true // the script ended during warm-up: nothing was measured
		return
	}
	runtime.ReadMemStats(&b.mem1)
	cpu1, tcpu1 := sysinfo.CPUNs(), sysinfo.ThreadCPUNs()
	b.measuring = false
	if b.o.WindowEnd != nil {
		b.o.WindowEnd()
	}
	n := float64(max(b.measuredN, 1))
	w := BenchWindow{Frames: b.measuredN, Mallocs: b.mem1.Mallocs - b.mem0.Mallocs,
		TotalAlloc: b.mem1.TotalAlloc - b.mem0.TotalAlloc, NumGC: b.mem1.NumGC - b.mem0.NumGC}
	rep := BenchReport{
		Frames:          b.measuredN,
		Paced:           b.unpaced == 0,
		UnpacedFrames:   b.unpaced,
		Mallocs:         w.Mallocs,
		TotalAlloc:      w.TotalAlloc,
		NumGC:           w.NumGC,
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
	b.windows = append(b.windows, w)
	if len(b.windows) == 1 || w.Mallocs < b.windows[b.bestIdx].Mallocs ||
		(w.Mallocs == b.windows[b.bestIdx].Mallocs && w.TotalAlloc < b.windows[b.bestIdx].TotalAlloc) {
		b.best, b.bestIdx = rep, len(b.windows)-1
	}
	if (w.Mallocs == 0 && w.TotalAlloc == 0) || len(b.windows) >= b.o.Windows || a.scriptDone {
		b.finished = true
		b.report = b.best
		b.report.Windows = b.windows
		a.done = true
		return
	}
	b.begin(a) // the next window follows directly
}
