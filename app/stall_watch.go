//go:build darwin || windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/app/paths"
	"gonativeui/platform"
)

// stallWatch is the running watch, shared by the app object's windows.
type stallWatch struct {
	o     StallWatch
	after int64
	dir   string

	// written by the main thread, read by the watchdog
	phase    atomic.Uint32
	since    atomic.Int64 // platform.Now when the phase began
	deadline atomic.Int64 // pump: when its timeout ends (0: it waits for an event)
	present  atomic.Int64 // platform.Now of the last frame presented

	frames    chan StallReport // StallFrames from the loop (never blocks it)
	resumed   chan time.Duration
	stop      chan struct{}
	lastFile  string // the watchdog's last report, for the resumed line
	lastSampl time.Time
}

func newStallWatch(o StallWatch, id string) *stallWatch {
	if o.After <= 0 {
		o.After = 2 * time.Second
	}
	if o.Keep <= 0 {
		o.Keep = 10
	}
	dir := o.Dir
	if dir == "" {
		dir, _ = paths.Logs(id)
	}
	s := &stallWatch{o: o, after: int64(o.After), dir: dir,
		frames: make(chan StallReport, 1), resumed: make(chan time.Duration, 1), stop: make(chan struct{})}
	now := platform.Now()
	s.since.Store(now)
	s.present.Store(now)
	go s.run()
	return s
}

// mark records that the main thread enters phase p; timeout is the pump's
// (-1: it waits for an event). It returns the phase it leaves.
func (s *stallWatch) mark(p uint32, timeout int64) uint32 {
	if s == nil {
		return phaseIdle
	}
	now := platform.Now()
	d := int64(0)
	if p == phasePump && timeout >= 0 {
		d = now + timeout
	}
	s.deadline.Store(d)
	s.since.Store(now)
	return s.phase.Swap(p)
}

// presented records a frame on screen.
func (s *stallWatch) presented(now int64) {
	if s != nil {
		s.present.Store(now)
	}
}

func (s *stallWatch) close() {
	if s != nil {
		close(s.stop)
	}
}

func (s *stallWatch) run() {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	var reported int64 // the since of the phase last reported
	for {
		select {
		case <-s.stop:
			return
		case r := <-s.frames:
			s.write(r)
		case d := <-s.resumed:
			s.appendResumed(d)
		case <-t.C:
			p, since := s.phase.Load(), s.since.Load()
			now := platform.Now()
			if since != reported && phaseStalled(p, since, s.deadline.Load(), s.present.Load(), now, s.after) {
				reported = since
				s.write(StallReport{Kind: StallPhase, Phase: loopPhaseNames[p], For: time.Duration(now - since)})
				go s.waitPhase(p, since)
			}
		}
	}
}

// waitPhase reports how long the stuck phase lasted once the main thread
// leaves it.
func (s *stallWatch) waitPhase(p uint32, since int64) {
	for {
		select {
		case <-s.stop:
			return
		case <-time.After(100 * time.Millisecond):
		}
		if s.phase.Load() != p || s.since.Load() != since {
			select {
			case s.resumed <- time.Duration(platform.Now() - since):
			default:
			}
			return
		}
	}
}

// reportFrames hands a StallFrames report to the watchdog (main thread; never
// blocks).
func (s *stallWatch) reportFrames(r StallReport) {
	select {
	case s.frames <- r:
	default:
	}
}

// reportResumed says a reported StallFrames stall ended after d (main thread).
func (s *stallWatch) reportResumed(d time.Duration) {
	select {
	case s.resumed <- d:
	default:
	}
}

func (s *stallWatch) write(r StallReport) {
	if s.dir != "" && os.MkdirAll(s.dir, 0o755) == nil {
		stamp := time.Now().Format("20060102-150405.000")
		path := filepath.Join(s.dir, "stall-"+stamp+".log")
		var b strings.Builder
		fmt.Fprintf(&b, "stall: %s\nphase: %s\nfor: %.1fs\nwhen: %s\npid: %d (%s, %s/%s)\n",
			r.Kind, r.Phase, r.For.Seconds(), time.Now().Format(time.RFC3339Nano), os.Getpid(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		if r.State != "" {
			b.WriteString("state: " + r.State + "\n")
		}
		b.WriteString("\ngoroutines:\n")
		b.Write(stacks())
		if os.WriteFile(path, []byte(b.String()), 0o644) == nil {
			r.Path, s.lastFile = path, path
			if time.Since(s.lastSampl) >= time.Minute {
				s.lastSampl = time.Now()
				go nativeSample(strings.TrimSuffix(path, ".log") + "-sample.txt")
			}
			s.prune()
		}
	}
	if s.o.Report != nil {
		s.o.Report(r)
	}
}

func (s *stallWatch) appendResumed(d time.Duration) {
	if s.lastFile == "" {
		return
	}
	f, err := os.OpenFile(s.lastFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return
	}
	f.WriteString("\nresumed after " + strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s\n")
	f.Close()
}

// prune keeps the newest Keep reports (their samples go with them). The time
// stamp in the names sorts, and ReadDir returns them sorted.
func (s *stallWatch) prune() {
	ents, _ := os.ReadDir(s.dir)
	var logs []string
	for _, e := range ents {
		if n := e.Name(); strings.HasPrefix(n, "stall-") && strings.HasSuffix(n, ".log") {
			logs = append(logs, n)
		}
	}
	for i := 0; i < len(logs)-s.o.Keep; i++ {
		p := filepath.Join(s.dir, logs[i])
		os.Remove(p)
		os.Remove(strings.TrimSuffix(p, ".log") + "-sample.txt")
	}
}

// stacks is every goroutine's stack.
func stacks() []byte {
	buf := make([]byte, 256<<10)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) || len(buf) >= 16<<20 {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}

// checkFrames is the loop's StallFrames check for this window at now (main
// thread, each turn).
func (a *App) checkFrames(now int64) {
	s := a.stall
	if s == nil {
		return
	}
	if !a.p.needDraw || a.p.hidden || a.shelved {
		a.owedAt = 0
		return
	}
	if a.owedAt == 0 {
		a.owedAt = now
	}
	if a.stallAt == 0 && framesStalled(a.owedAt, now, s.after) {
		// Only a window a person can see stalls: AppKit may never have said
		// that a window is covered (opened under a locked screen, say). Asked
		// once per After while the repaint waits.
		if w := a.Win; w != nil {
			if st := w.DebugState(); st.Valid && !st.Visible {
				a.owedAt = now
				return
			}
		}
		a.stallAt = a.owedAt
		a.diagBuf = a.StateLine(a.diagBuf[:0], now)
		s.reportFrames(StallReport{Kind: StallFrames, Phase: loopPhaseNames[s.phase.Load()], For: time.Duration(now - a.owedAt), State: string(a.diagBuf)})
	}
}

// drewFrame records a frame this window presented at now.
func (a *App) drewFrame(now int64) {
	s := a.stall
	if s == nil {
		return
	}
	s.presented(now)
	a.owedAt = now // an animation owes the next one from here
	if a.stallAt != 0 {
		s.reportResumed(time.Duration(now - a.stallAt))
		a.stallAt = 0
	}
}
