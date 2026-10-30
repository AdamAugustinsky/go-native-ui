package app

import (
	"errors"
	"fmt"
	"image"
	"math"
	"runtime"
	"time"

	"gonativeui/core"
	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/text"
	"gonativeui/ui"
)

// SimOptions configures a deterministic, headless app. No native event pump
// may run alongside it; the virtual clock is process-wide. Services created
// by Init join the worker barrier automatically (fs, text, img and mdview).
type SimOptions struct {
	// Seed labels failures and trace identity. Scenarios seed their own inputs.
	Seed          uint64
	Tick          time.Duration // zero: 60 Hz
	WorkerTimeout time.Duration // REAL time, zero: five seconds per barrier
	Render        bool          // render pixels as well as building real text/atlas/draw lists
	Checks        []SimCheck    // nil: SimAudit and SimOwedFrames
	MaxSteps      int           // per Advance; zero: one million, bounds runaway frame loops
}

// Sim runs one application's windows through the real translator, turn,
// timeout, pacer, build and frame arming logic. Only the surface and OS pump
// are replaced. The creating goroutine is OS-thread-locked until Close.
//
// Event queues input at the current virtual time. Advance runs to its target,
// jumping straight across idle intervals. Close builders' services before
// Close, or implement SimCloser; simulations must never run in parallel.
type Sim struct {
	clock      *core.SimClock
	opts       SimOptions
	shared     *appObject
	windows    []*simWindow
	trace      []SimRecord
	step       uint64
	err        error
	closed     bool
	clipboard  []byte
	appearance ui.Appearance
	allocs     bool
	stats      [2]runtime.MemStats
}

// SimCloser releases a builder's workers at simulated window close.
type SimCloser interface{ SimClose() }

type simWindow struct {
	s                 *Sim
	a                 *App
	id                int
	nextTick          int64
	closed            bool
	pixels            *image.RGBA
	ax                *platform.SimAX
	cause             SimCause
	text              []byte // borrowed only during dispatch
	pasteTag          uint64
	paste             bool
	lastDraw, lastSem uint64
	frames            int
	audited           int
}

// NewSim opens window zero. Further windows share its text system and GPU.
func NewSim(cfg Config, w, h int, scale float32, b Builder, opts SimOptions) (s *Sim, err error) {
	if !simSize(w, h, scale) || b == nil {
		return nil, errors.New("app: invalid simulation window")
	}
	if opts.Tick < 0 || opts.WorkerTimeout < 0 || opts.MaxSteps < 0 {
		return nil, errors.New("app: negative simulation option")
	}
	c, err := core.InstallSimClockForTesting()
	if err != nil {
		return nil, err
	}
	runtime.LockOSThread()
	s = &Sim{clock: c, opts: opts, shared: &appObject{}, appearance: ui.Appearance{Known: true}, trace: make([]SimRecord, 0, 256)}
	if s.opts.Tick == 0 {
		s.opts.Tick = time.Second / 60
	}
	if s.opts.WorkerTimeout == 0 {
		s.opts.WorkerTimeout = 5 * time.Second
	}
	if s.opts.MaxSteps == 0 {
		s.opts.MaxSteps = 1000000
	}
	if s.opts.Checks == nil {
		s.opts.Checks = []SimCheck{SimAudit, SimOwedFrames}
	}
	defer func() {
		if p := recover(); p != nil {
			err = s.failure(fmt.Errorf("panic in Init: %v", p))
		}
		if err != nil {
			s.Close()
			s = nil
		}
	}()
	if s.shared.text, err = text.New(); err != nil {
		return s, err
	}
	if s.shared.r, err = gpu.NewOffscreenRenderer(); err != nil {
		return s, err
	}
	s.shared.atlas = ui.NewSharedAtlas(s.shared.r)
	_, err = s.OpenWindow(cfg, w, h, scale, b)
	return s, err
}

func simSize(w, h int, scale float32) bool {
	return w > 0 && h > 0 && scale > 0 && !math.IsInf(float64(scale), 0) && !math.IsNaN(float64(scale))
}

// OpenWindow adds an independently paced headless window; its integer id is
// stable, including after other windows close. Init runs immediately.
func (s *Sim) OpenWindow(cfg Config, w, h int, scale float32, b Builder) (id int, err error) {
	defer s.guard(&err)
	if s.closed {
		return -1, errors.New("app: simulation closed")
	}
	if !simSize(w, h, scale) || b == nil {
		return -1, errors.New("app: invalid simulation window")
	}
	a := &App{appObject: s.shared, cfg: cfg, b: b, tr: newTranslator(), p: newPacer(), w: w, h: h, scale: scale, Text: s.shared.text, R: s.shared.r}
	v := &simWindow{s: s, a: a, id: len(s.windows), cause: SimInitial}
	a.sim, a.req, a.tr.scale = v, v, scale
	s.windows = append(s.windows, v)
	s.shared.wins = append(s.shared.wins, a)
	if err := a.initUI(); err != nil {
		return v.id, err
	}
	a.UI.ClipboardSet = s.setClipboard
	a.UI.SetSystemAppearance(s.appearance)
	a.initAX()
	v.ax = platform.NewSimAX(&a.ax)
	if cfg.Panel != nil {
		a.shelve(true)
	}
	s.record(SimRecord{Kind: SimOpen, Window: v.id, W: w, H: h, Scale: scale})
	return v.id, nil
}

// Window returns a window's App for assertions and scenario setup.
func (s *Sim) Window(id int) *App { return s.windows[id].a }

// Event dispatches a native-shaped event. data supplies text/preedit/paste/drop
// bytes; TextRef is local to this event. Events at one instant batch naturally.
func (s *Sim) Event(id int, e platform.Event, data ...[]byte) (err error) {
	defer s.guard(&err)
	if err = s.ready(); err != nil {
		return err
	}
	if id < 0 || id >= len(s.windows) || s.windows[id].closed {
		return errors.New("app: simulation window closed or missing")
	}
	v := s.windows[id]
	if e.Kind == platform.EvResize && !simSize(int(e.W), int(e.H), e.Scale) {
		return errors.New("app: invalid simulated resize")
	}
	if e.Time == 0 {
		e.Time = core.Now()
	}
	if len(data) > 0 {
		v.text = data[0]
		e.Text = platform.TextRef{Len: uint32(len(data[0]))}
	}
	defer func() { v.text = nil }()
	s.record(SimRecord{Kind: SimEvent, Window: id, Event: e, Data: string(v.text)})
	if e.Kind == platform.EvVisible && v.a.cfg.Panel != nil {
		v.a.shelve(e.Flags&platform.FlagDown == 0)
	}
	v.a.tr.wheelStep = v.a.UI.WheelStep
	before := v.a.p.inval
	v.a.dispatch(&e)
	if v.a.p.inval != before {
		v.cause |= simEventCause(e.Kind)
	}
	if e.Kind == platform.EvVisible && e.Flags&platform.FlagDown == 0 {
		v.nextTick = 0
	}
	if e.Kind == platform.EvClose && (v.a.closing || s.shared.done) {
		s.closeWindow(v)
	}
	return s.check()
}

// Advance runs every due timer, worker completion, window timeout and requested
// display tick through d, including work due exactly at the endpoint.
func (s *Sim) Advance(d time.Duration) (err error) {
	defer s.guard(&err)
	if d < 0 || int64(d) > math.MaxInt64-core.Now() {
		return errors.New("app: invalid simulation duration")
	}
	if err = s.ready(); err != nil {
		return err
	}
	end := core.Now() + int64(d)
	for n := 0; n < s.opts.MaxSteps; n++ {
		if err = s.barrier(); err != nil {
			return err
		}
		now := core.Now()
		s.step++
		for _, v := range s.windows {
			if v.closed {
				continue
			}
			if v.paste {
				v.paste = false
				e := platform.Event{Kind: platform.EvPaste, Tag: v.pasteTag, Time: now, Text: platform.TextRef{Len: uint32(len(s.clipboard))}}
				v.text = s.clipboard
				v.a.dispatch(&e)
				v.text = nil
				v.cause |= SimInput
				s.record(SimRecord{Kind: SimEvent, Window: v.id, Event: e, Data: string(s.clipboard)})
			}
			if v.nextTick != 0 && v.nextTick <= now {
				v.nextTick = 0
				if !v.a.p.hidden && !v.a.shelved {
					v.a.dispatch(&platform.Event{Kind: platform.EvFrame, Time: now})
				}
			}
			if v.a.wakeAt != 0 && now >= v.a.wakeAt && !v.a.p.hidden && !v.a.shelved {
				v.cause |= SimDeadline
			}
			if v.a.turn(now) {
				if v.a.p.hidden {
					v.a.p.failed(now)
					v.RequestFrame()
					v.a.p.requested(now)
				} else if err = s.draw(v, now); err != nil {
					return err
				}
			}
		}
		if err = s.check(); err != nil {
			return err
		}
		// A build may have submitted more work. Publish it before choosing
		// the next instant, including at Advance's endpoint.
		if err = s.barrier(); err != nil {
			return err
		}
		next := int64(math.MaxInt64)
		if t := s.clock.Next(); t != 0 {
			next = min(next, t)
		}
		for _, v := range s.windows {
			if v.closed {
				continue
			}
			if t := v.a.timeout(now); t >= 0 {
				next = min(next, now+t)
			}
			if v.nextTick != 0 {
				next = min(next, v.nextTick)
			}
			if v.paste {
				next = now
			}
		}
		if next > end {
			if now != end {
				_, err = s.clock.AdvanceTo(end)
				s.step++
				if err == nil {
					err = s.check()
				}
			}
			return err
		}
		fired, e := s.clock.AdvanceTo(max(now, next))
		if e != nil {
			return e
		}
		if fired > 0 {
			s.record(SimRecord{Kind: SimTimers, Count: fired, Window: -1})
		}
	}
	return errors.New("app: simulation step budget exhausted")
}

func (s *Sim) barrier() error {
	if err := s.clock.Quiesce(s.opts.WorkerTimeout); err != nil {
		return err
	}
	if s.clock.TakeWake() {
		for _, v := range s.windows {
			if !v.closed {
				v.a.dispatch(&platform.Event{Kind: platform.EvWake, Time: core.Now()})
				v.cause |= SimWake
			}
		}
		s.record(SimRecord{Kind: SimWorkerWake, Window: -1})
	}
	return nil
}

func (s *Sim) draw(v *simWindow, now int64) error {
	a := v.a
	if s.opts.Render && (v.pixels == nil || v.pixels.Rect.Dx() != a.w || v.pixels.Rect.Dy() != a.h) {
		v.pixels = image.NewRGBA(image.Rect(0, 0, a.w, a.h))
	}
	if s.allocs {
		runtime.ReadMemStats(&s.stats[0])
	}
	_, dirty := a.tr.take()
	a.tr.in.Time = now
	a.settle = false
	a.UI.SetSystemAppearance(s.appearance)
	a.build(a.w, a.h, a.frameDT(now))
	a.drawPlaceholders()
	a.syncText(v, now)
	v.ax.Sync()
	why := a.arm(false, a.UI.Animating(), dirty, now)
	if s.opts.Render {
		a.R.RenderToRGBA(a.w, a.h, a.cfg.Clear, &a.list, v.pixels.Pix)
	}
	a.owedWhy, a.more = why, why != 0
	a.p.drawn(a.more)
	a.frames++
	v.frames++
	a.lastDrawT = now
	if s.allocs {
		runtime.ReadMemStats(&s.stats[1])
		if n := s.stats[1].Mallocs - s.stats[0].Mallocs; n != 0 {
			return fmt.Errorf("window %d warmed frame allocated %d objects, %d bytes", v.id, n, s.stats[1].TotalAlloc-s.stats[0].TotalAlloc)
		}
	}
	tree, _ := a.UI.SemanticSnapshot()
	v.lastDraw, v.lastSem = simDrawHash(&a.list), simSemanticHash(tree)
	s.record(SimRecord{Kind: SimFrame, Window: v.id, Cause: v.cause, DrawHash: v.lastDraw, SemanticHash: v.lastSem})
	v.cause = 0
	if why != 0 {
		v.cause = SimContinuation
	}
	return nil
}

func (v *simWindow) RequestFrame() {
	if !v.a.p.hidden && !v.a.shelved && v.nextTick == 0 {
		v.nextTick = core.Now() + int64(v.s.opts.Tick)
	}
}
func (*simWindow) SetIMERect(float32, float32, float32, float32) {}
func (*simWindow) SetTextInput(bool)                             {}
func (*simWindow) SetSecureTextInput(bool)                       {}
func (*simWindow) DiscardComposition()                           {}
func (v *simWindow) RequestPaste(tag uint64)                     { v.pasteTag, v.paste = tag, true }

// Appearance changes the system mode and delivers its coalesced wake, even
// while hidden. The next built frame observes the new appearance.
func (s *Sim) Appearance(dark bool) (err error) {
	defer s.guard(&err)
	if err = s.ready(); err != nil {
		return err
	}
	s.appearance = ui.Appearance{Known: true, Dark: dark}
	s.record(SimRecord{Kind: SimAppearance, Window: -1, Dark: dark})
	for _, v := range s.windows {
		if !v.closed {
			v.a.dispatch(&platform.Event{Kind: platform.EvWake, Time: core.Now()})
			v.cause |= SimAppearanceCause
		}
	}
	return s.check()
}

// DeviceLost rebuilds the actual offscreen renderer and restores the shared
// text atlas; all windows owe a repaint, and image/icon caches observe Epoch.
func (s *Sim) DeviceLost() (err error) {
	defer s.guard(&err)
	if err = s.ready(); err != nil {
		return err
	}
	if err = s.shared.r.Rebuild(0, 0); err != nil {
		return err
	}
	for _, v := range s.windows {
		if !v.closed {
			v.a.UI.AtlasRestore()
			v.a.p.invalidate()
			v.cause |= SimDevice
		}
	}
	s.record(SimRecord{Kind: SimDeviceLost, Window: -1})
	return s.check()
}

// AXQuery simulates a query, including reactivation after 30 seconds idle.
func (s *Sim) AXQuery(id int) (tree *platform.AXTree, err error) {
	defer s.guard(&err)
	if err = s.ready(); err != nil {
		return nil, err
	}
	v := s.windows[id]
	was := v.a.UI.SemanticsActive()
	tree = v.ax.Query()
	if !was {
		v.a.p.invalidate()
		v.cause |= SimAccessibility
	}
	s.record(SimRecord{Kind: SimAXQuery, Window: id})
	return tree, s.check()
}

// Clipboard sets the isolated text clipboard. Copy and paste use it through
// the normal UI hooks; no system clipboard is read or changed.
func (s *Sim) Clipboard(b []byte) { s.setClipboard(b) }
func (s *Sim) setClipboard(b []byte) {
	s.clipboard = append(s.clipboard[:0], b...)
	s.record(SimRecord{Kind: SimClipboard, Window: -1, Data: string(b)})
}

// CheckAllocations enables exact, isolated per-frame allocation checks after
// a scenario has warmed its state. Disable it for growth and deliberate trims.
func (s *Sim) CheckAllocations(on bool) { s.allocs = on }

func (s *Sim) closeWindow(v *simWindow) {
	if v.closed {
		return
	}
	v.closed = true
	v.nextTick = 0
	v.a.closing = true
	if b, ok := v.a.b.(SimCloser); ok {
		b.SimClose()
	}
	for i, a := range s.shared.wins {
		if a == v.a {
			s.shared.wins = append(s.shared.wins[:i], s.shared.wins[i+1:]...)
			break
		}
	}
	if v.a.ownTex != nil {
		v.a.ownTex.free()
		v.a.ownTex = nil
	}
}

// Close joins builder-owned workers (SimCloser), frees native resources and
// restores the production clock. It is idempotent.
func (s *Sim) Close() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	defer runtime.UnlockOSThread()
	defer s.clock.Restore()
	for _, v := range s.windows {
		s.closeWindow(v)
	}
	if s.shared.r != nil {
		s.shared.r.Destroy()
		s.shared.r = nil
	}
	if s.shared.text != nil {
		s.shared.text.Close()
		s.shared.text = nil
	}
}

func (s *Sim) ready() error {
	if s.closed {
		return errors.New("app: simulation closed")
	}
	return s.err
}
func (s *Sim) guard(err *error) {
	if p := recover(); p != nil {
		*err = fmt.Errorf("panic: %v", p)
	}
	if *err != nil {
		*err = s.failure(*err)
	}
}

func (a *App) eventText(ref platform.TextRef) []byte {
	if a.sim != nil {
		return a.sim.text
	}
	if a.Win == nil {
		return nil
	}
	return a.Win.Text(ref)
}
