package app

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"runtime"
	"runtime/metrics"
	"slices"
	"time"

	"gonativeui/core"
	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/ui"
)

// SimCause is a set of reasons a simulated frame was owed.
type SimCause uint32

// Frame causes are combinable; continuation means animation/input settling.
const (
	SimInitial SimCause = 1 << iota
	SimInput
	SimResize
	SimVisibility
	SimWake
	SimDeadline
	SimContinuation
	SimAppearanceCause
	SimAccessibility
	SimDevice
)

func simEventCause(k platform.EventKind) SimCause {
	switch k {
	case platform.EvResize:
		return SimResize
	case platform.EvVisible:
		return SimVisibility
	case platform.EvWake:
		return SimWake
	case platform.EvAccessibility:
		return SimAccessibility
	case platform.EvFrame:
		return 0
	default:
		return SimInput
	}
}

// SimKind distinguishes trace events from frames and external injections.
type SimKind uint8

// Trace record kinds.
const (
	SimOpen SimKind = iota
	SimEvent
	SimFrame
	SimTimers
	SimWorkerWake
	SimAppearance
	SimDeviceLost
	SimAXQuery
	SimClipboard
)

// SimRecord is independent of real elapsed time, addresses and Go map order.
// DrawHash covers every quad and backdrop; SemanticHash covers the exported
// tree, including names, states, bounds, selection and announcements.
type SimRecord struct {
	// Kind through Count describe one ordered replay record.
	Kind                   SimKind
	Step                   uint64
	Time                   int64
	Window                 int
	Cause                  SimCause
	Event                  platform.Event
	Data                   string
	DrawHash, SemanticHash uint64
	W, H                   int
	Scale                  float32
	Dark                   bool
	Count                  int
}

func (s *Sim) record(r SimRecord) { r.Time = core.Now(); r.Step = s.step; s.trace = append(s.trace, r) }

// Trace returns a copy of the replay evidence. Real-time watchdog durations,
// heap samples and goroutine counts deliberately do not enter the hash.
func (s *Sim) Trace() []SimRecord { return slices.Clone(s.trace) }

// TraceHash is stable for the same seed and scenario on the same host/font
// environment. Different native font/renderer versions may shape differently.
func (s *Sim) TraceHash() uint64 {
	h := fnv.New64a()
	_ = binary.Write(h, binary.LittleEndian, s.opts.Seed)
	_ = json.NewEncoder(h).Encode(s.trace)
	return h.Sum64()
}

func simDrawHash(d *gpu.DrawList) uint64 {
	h := fnv.New64a()
	_ = binary.Write(h, binary.LittleEndian, uint64(len(d.Quads)))
	_ = binary.Write(h, binary.LittleEndian, d.Quads)
	_ = binary.Write(h, binary.LittleEndian, d.Backdrops)
	return h.Sum64()
}

func simSemanticHash(t *ui.SemTree) uint64 {
	if t == nil {
		return 0
	}
	h := fnv.New64a()
	_ = binary.Write(h, binary.LittleEndian, t.Nodes)
	_, _ = h.Write(t.Bytes)
	_ = binary.Write(h, binary.LittleEndian, t.Announce)
	_ = binary.Write(h, binary.LittleEndian, t.AnnounceSeq)
	_ = binary.Write(h, binary.LittleEndian, t.AnnouncePolite)
	_ = binary.Write(h, binary.LittleEndian, int64(t.Dropped))
	return h.Sum64()
}

// FrameHash returns the last frame's draw-list and semantic hashes.
func (s *Sim) FrameHash(id int) (draw, sem uint64) { v := s.windows[id]; return v.lastDraw, v.lastSem }

// SimCheck is a cheap invariant run after every step. Return an error to stop
// the scenario with its seed, step and recent trace. Expensive GC/allocation
// measurements are explicit checkpoints (Memory and CheckAllocations).
type SimCheck func(*Sim) error

// SimAudit checks the real layout audit of each finished window.
func SimAudit(s *Sim) error {
	for _, v := range s.windows {
		if !v.closed && v.frames > v.audited {
			if f := v.a.UI.Audit(nil); len(f) > 0 {
				return fmt.Errorf("window %d layout: %s", v.id, f[0].String())
			}
			v.audited = v.frames
		}
	}
	return nil
}

// SimOwedFrames rejects uncaused frames, including accidental idle polling.
func SimOwedFrames(s *Sim) error {
	for i := len(s.trace) - 1; i >= 0 && s.trace[i].Step == s.step; i-- {
		if r := s.trace[i]; r.Kind == SimFrame && r.Cause == 0 {
			return fmt.Errorf("window %d drew without a cause", r.Window)
		}
	}
	return nil
}

func (s *Sim) check() error {
	for _, f := range s.opts.Checks {
		if err := f(s); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sim) failure(err error) error {
	if s.err == nil {
		s.err = fmt.Errorf("sim seed=%d step=%d time=%d: %w\nrecent trace: %+v", s.opts.Seed, s.step, core.Now(), err, s.trace[max(0, len(s.trace)-8):])
	}
	return s.err
}

// SimReturnFrame checks an unchanged scene once it has settled after showing.
// Install it only across a span in which content, size and appearance stay
// unchanged and focus/selection match the captured frame.
func SimReturnFrame(id int, draw uint64) SimCheck {
	return func(s *Sim) error {
		v := s.windows[id]
		if !v.closed && !v.a.p.hidden && !v.a.shelved && !v.a.p.needDraw && v.frames > 0 && v.lastDraw != draw {
			return fmt.Errorf("window %d return frame %x, want %x", id, v.lastDraw, draw)
		}
		return nil
	}
}

// AddCheck appends a scenario invariant, for example SimReturnFrame.
func (s *Sim) AddCheck(f SimCheck) { s.opts.Checks = append(s.opts.Checks, f) }

// SimIdleReport counts actual frames during a measured idle span, by the full
// cause set. FramesPerHour is a rate, not a claim of an hour actually measured.
type SimIdleReport struct {
	// Duration is virtual elapsed time; ByCause groups complete cause sets.
	Duration      time.Duration
	Frames        int
	ByCause       map[SimCause]int
	FramesPerHour float64
}

// Idle advances d and rejects frames without one of allowed's causes. Pass
// zero for a completely settled hidden/static window, or SimDeadline for a
// deliberate cleanup/caret deadline (add SimContinuation for settling it).
func (s *Sim) Idle(d time.Duration, allowed SimCause) (r SimIdleReport, err error) {
	start := len(s.trace)
	r = SimIdleReport{Duration: d, ByCause: make(map[SimCause]int)}
	if err = s.Advance(d); err != nil {
		return r, err
	}
	for _, e := range s.trace[start:] {
		if e.Kind != SimFrame {
			continue
		}
		r.Frames++
		r.ByCause[e.Cause]++
		if e.Cause&allowed == 0 {
			err = s.failure(fmt.Errorf("idle frame in window %d: cause %x, allowed %x", e.Window, e.Cause, allowed))
		}
	}
	if d > 0 {
		r.FramesPerHour = float64(r.Frames) / d.Hours()
	}
	return r, err
}

// SimMemory records GC live bytes and the capacities trimmed in D86. Native
// GPU bytes are represented by instance capacity, not Go's heap counter.
type SimMemory struct {
	// HeapLive and Goroutines are process-wide; other fields are app capacities.
	HeapLive                             uint64
	Goroutines                           int
	Boxes, RunGlyphs, RunText, Instances int
	Subsystems                           map[string]int
}

// SimRetainer supplies builder-owned capacities (listing pools, VList backs,
// image caches) at explicit checkpoints. It is never called on a frame path.
type SimRetainer interface{ SimRetained() map[string]int }

// Memory first waits for workers, then collects the Go heap and samples the
// runtime's live-heap metric. Samples do not alter trace identity.
func (s *Sim) Memory() (m SimMemory, err error) {
	defer s.guard(&err)
	if err = s.ready(); err != nil {
		return m, err
	}
	if err = s.barrier(); err != nil {
		return m, err
	}
	runtime.GC()
	samples := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(samples)
	m.HeapLive = samples[0].Value.Uint64()
	m.Goroutines = runtime.NumGoroutine()
	m.Subsystems = make(map[string]int)
	for _, v := range s.windows {
		if v.closed {
			continue
		}
		m.Boxes += v.a.UI.RetainedBoxes()
		if r, ok := v.a.b.(SimRetainer); ok {
			for k, n := range r.SimRetained() {
				m.Subsystems[fmt.Sprintf("%d/%s", v.id, k)] = n
			}
		}
	}
	if s.shared.text != nil {
		_, m.RunGlyphs, m.RunText = s.shared.text.RunCapacity()
	}
	if s.shared.r != nil {
		m.Instances = s.shared.r.InstanceCapacity()
	}
	return m, nil
}

// CheckMemory enforces a scenario-specific retained floor after a long idle.
// Zero bounds are ignored; subsystem bounds, including zero, are exact maxima.
// Absolute heap/goroutine values depend on the process, so measure a baseline.
func (s *Sim) CheckMemory(maximum SimMemory) (SimMemory, error) {
	m, err := s.Memory()
	if err != nil {
		return m, err
	}
	if maximum.HeapLive > 0 && m.HeapLive > maximum.HeapLive {
		return m, s.failure(fmt.Errorf("heap live %d exceeds %d", m.HeapLive, maximum.HeapLive))
	}
	for _, p := range [][3]int{{m.Boxes, maximum.Boxes, 0}, {m.RunGlyphs, maximum.RunGlyphs, 1}, {m.RunText, maximum.RunText, 2}, {m.Instances, maximum.Instances, 3}, {m.Goroutines, maximum.Goroutines, 4}} {
		if p[1] > 0 && p[0] > p[1] {
			return m, s.failure(fmt.Errorf("retained capacity %d: %d exceeds %d", p[2], p[0], p[1]))
		}
	}
	for k, max := range maximum.Subsystems {
		if n, ok := m.Subsystems[k]; !ok || n > max {
			return m, s.failure(fmt.Errorf("retained %s: %d (present %v), maximum %d", k, n, ok, max))
		}
	}
	return m, nil
}

// CheckGoroutines waits a bounded REAL time for returning workers to leave
// the runtime after Close. It rejects growth over the caller's pre-run count;
// unrelated goroutines in the same process can invalidate that comparison.
func (s *Sim) CheckGoroutines(baseline int) error {
	end := time.Now().Add(s.opts.WorkerTimeout)
	for {
		n := runtime.NumGoroutine()
		if n <= baseline {
			return nil
		}
		if time.Now().After(end) {
			return s.failure(fmt.Errorf("goroutines %d exceed baseline %d", n, baseline))
		}
		time.Sleep(time.Millisecond)
	}
}
