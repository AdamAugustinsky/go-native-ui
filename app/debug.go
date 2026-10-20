package app

import (
	"os"
	"runtime"
	"strconv"
	"strings"

	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The debug overlay (phase 6, T3): box outlines under the pointer, a frame
// HUD, the cause of every repaint, duplicate-key warnings and an inspector.
//
// # Turning it on
//
// GNUI_DEBUG=overlay in the environment turns it on for any app, in any
// build; GNUI_DEBUG=overlay,log also prints one line per frame to stderr
// (the causes, the CPU and the allocations). GNUI_DEBUG is a comma-separated
// list: the platform's "1" or "imp", which traces Objective-C calls, is
// another word of it and does not turn the overlay on. EnableDebug does the
// same from code.
//
// In a development build (every build without the gnui_release tag; gnui
// package always sets it) the app has a Debug menu on macOS, with Toggle
// Overlay (Cmd+Opt+Shift+D), Inspector (...I) and Run Layout Audit (...A);
// AppKit matches those by character on any layout. Where the app has no native
// menu bar, and on Windows and Linux, the same chords (Ctrl for Cmd) are
// recognised once, when the key event arrives, by the character the key types
// on the user's layout (platform.Event.Char), and never reach the app. A
// release build has no Debug menu, no chords and no inspector panel.
//
// The inspector is a panel in the window. The plan puts it in a second window;
// that waits for F4 (more than one window), and InspectHost is where it will
// plug in (inspect.go).
//
// # What it costs
//
// Off, it is a nil check at each place that records, and the gates run that
// way. On, a steady-state frame allocates nothing: the records are fixed
// arrays, the text of the overlay is made of one-character boxes whose runs
// are cached after the first frame, and the caller program counters are
// symbolised only when shown, once per counter (a small cache). The overlay
// builds a few hundred boxes and reads the thread's CPU clock a few times per
// frame.
//
// # Repaint causes
//
// Every frame records why it ran (Cause). A frame that ran with no recorded
// cause is CauseUnknown and is shown in red: it is the idle rule broken (an
// idle app draws no frames), or a call to the pacer that the overlay does not
// know. An idle app records no frames at all.

// Cause is why a frame ran.
type Cause uint8

// The causes of a repaint. Anything that makes the loop owe a frame is one of
// them; CauseUnknown means the overlay saw the frame owed with none of them.
const (
	// CauseUnknown: no cause was recorded. A bug, or a source the overlay does
	// not know.
	CauseUnknown Cause = iota
	// CauseFirst: the first frame of the window.
	CauseFirst
	// CauseInput: pointer, key, scroll, text or focus events.
	CauseInput
	// CauseResize: the window's size or scale changed.
	CauseResize
	// CauseVisible: the window became visible again.
	CauseVisible
	// CauseWake: a worker woke the loop (a task result, a Post).
	CauseWake
	// CauseAppearance: the system appearance (dark mode, accent) changed.
	CauseAppearance
	// CauseAnimation: the previous frame left something animating. Key is the
	// box whose hover, press or focus value was still moving; without one, a
	// RequestFrame (Callers is its call stack in a development build) or
	// another source of the UI (scrolling, the focus ring, an overlay).
	CauseAnimation
	// CauseTimer: a time asked for with WakeIn, a Deadline or a caret blink
	// came. Sub is the ui.DebugWakeKind and Key the Deadline's or the field's.
	CauseTimer
	// CauseHover: input with the pointer inside, so the hit test of the next
	// frame sees the layout this one made.
	CauseHover
	// CauseSettle: the frame after a resize.
	CauseSettle
	// CauseBacklog: events deferred to a frame of their own (a press and a
	// release in one batch).
	CauseBacklog
	// CauseBench: a bench is running.
	CauseBench
	// CauseHost: a hosted native view took or gave back the focus.
	CauseHost
	// CauseDialog: an alert or file panel ended.
	CauseDialog
	// CauseMenu: a native menu or command was chosen.
	CauseMenu
	// CauseDriver: the frame was asked for by the code that drives an app with
	// no window (Offscreen.Frame).
	CauseDriver
	// CauseDebug: the overlay, its inspector or its audit was turned on or off,
	// or the audit ran.
	CauseDebug

	numCauses
)

var causeNames = [numCauses]string{"NO CAUSE", "first", "input", "resize", "visible", "wake", "appearance",
	"anim", "timer", "hover", "settle", "backlog", "bench", "host", "dialog", "menu", "driver", "debug"}

// String is the short name of the cause.
func (c Cause) String() string {
	if c < numCauses {
		return causeNames[c]
	}
	return "cause" + strconv.Itoa(int(c))
}

const (
	maxCauses = 6 // distinct causes kept per frame
	callerPCs = 3 // frames of a recorded call stack
	histFrame = 64
)

// RepaintCause is one reason a frame ran.
type RepaintCause struct {
	// Kind is the cause.
	Kind Cause
	// Sub refines Kind (ui.DebugWakeKind for CauseTimer).
	Sub uint8
	// N is how many times it was noted before the frame ran.
	N uint16
	// Key is the box, Deadline or field behind it; 0 when none.
	Key ui.Key
	// Callers[:NCallers] are the program counters of the code that asked, in
	// development builds, for the causes that come from a call (RequestFrame,
	// WakeIn). Symbolised only when shown.
	Callers  [callerPCs]uintptr
	NCallers uint8
}

// Phases of a frame's CPU time (FrameRecord.CPUns).
const (
	PhaseBuild   = iota // from the frame's start through the Builder's Build
	PhaseOverlay        // the debug overlay's own boxes
	PhaseLayout         // ui.Context.End: layout, animation, drawing into the list
	PhaseEncode         // gpu.Renderer.Encode (or the whole offscreen render)
	PhasePresent        // EndFrame: the present
	numPhases
)

var phaseNames = [numPhases]string{"build", "ovl", "layout", "enc", "pres"}

// FrameRecord is what the overlay recorded about one frame.
type FrameRecord struct {
	// N counts the frames recorded since the overlay turned on, from 1.
	N       uint64
	Causes  [maxCauses]RepaintCause
	NCauses int
	// CausesLost counts causes that did not fit in Causes.
	CausesLost int
	// CPUns is the main thread's CPU time in each phase.
	CPUns [numPhases]int64
	// Allocs is the number of heap allocations between the frame's start and
	// its end (runtime.MemStats.Mallocs).
	Allocs uint64
	// Boxes is the number of boxes the app built, without the overlay's.
	Boxes int
	// Quads and DrawCalls are those of the draw list the frame produced.
	Quads, DrawCalls int
}

// TotalCPUns is the sum of the phases.
func (r *FrameRecord) TotalCPUns() int64 {
	var t int64
	for _, v := range r.CPUns {
		t += v
	}
	return t
}

// Unexplained reports whether the frame ran with no recorded cause.
func (r *FrameRecord) Unexplained() bool {
	for i := 0; i < r.NCauses; i++ {
		if r.Causes[i].Kind == CauseUnknown {
			return true
		}
	}
	return false
}

// debugState is the overlay's state; nil on the App when it is off.
type debugState struct {
	log, pcs bool
	panel    bool // the inspector panel is shown
	auditOn  bool // the findings are shown
	audit    func(dst []DebugFinding) []DebugFinding
	findings [maxFindings]DebugFinding
	nFind    int
	host     InspectHost
	texBytes func() int64 // the renderer's texture bytes, nil when it has no such method

	// causes noted since the last frame, and the frame being built
	pend  [maxCauses]RepaintCause
	npend int
	lost  int
	cur   FrameRecord
	open  bool // cur is a frame in progress
	inDraw,
	built bool // cur was opened by drawFrame / has had a build

	hist    [histFrame]FrameRecord
	nFrames uint64
	counts  [numCauses]uint64 // frames by cause
	unexpl  uint64

	// the timer the last frame armed (WakeAfter): what fires when wakeAt does
	wakeKind ui.DebugWakeKind
	wakeKey  ui.Key
	wakePC   [callerPCs]uintptr
	wakeNPC  int

	serial uint32 // platform.AppearanceSerial last seen by an EvWake

	cpuLast int64
	ms      runtime.MemStats
	mem0    uint64
	logged  int // duplicate keys already written to stderr

	snap InspectSnapshot
	draw drawState
	sym  symCache
	out  []byte
}

// debugInit reads GNUI_DEBUG; initUI calls it once the ui context exists.
func (a *App) debugInit() {
	v := os.Getenv("GNUI_DEBUG")
	if v == "" {
		return
	}
	on, log := false, false
	for _, f := range strings.Split(v, ",") {
		switch strings.TrimSpace(f) {
		case "overlay":
			on = true
		case "log":
			on, log = true, true
		}
	}
	if !on {
		return
	}
	a.enableDebug()
	a.dbg.log = log
}

// EnableDebug turns the debug overlay on or off (see the notes at the top of
// debug.go) and schedules the repaint that shows the change, so an idle app
// shows it at once (that frame's cause is CauseDebug). Turning it on allocates.
// Call it outside Build.
func (a *App) EnableDebug(on bool) {
	if on == (a.dbg != nil) {
		return
	}
	if on {
		a.enableDebug()
	} else {
		a.dbg = nil
		if a.UI != nil {
			a.UI.EnableDebug(false, false)
		}
	}
	a.debugRepaint()
}

func (a *App) enableDebug() {
	if a.dbg != nil {
		return
	}
	d := &debugState{pcs: devBuild, out: make([]byte, 0, 512)}
	d.serial = platform.AppearanceSerial()
	d.draw.init()
	d.audit = a.uiAudit
	// The renderer's texture bytes, once it reports them (a type assertion at
	// each frame would allocate its cache in the measured frames).
	if tb, ok := any(a.R).(interface{ TextureBytes() int64 }); ok {
		d.texBytes = tb.TextureBytes
	}
	a.dbg = d
	if a.UI != nil {
		a.UI.EnableDebug(true, d.pcs)
	}
}

// debugCmdBase is the first command id of the Debug menu (debug_dev.go): the top
// of the 16-bit command space, which an app's own ids should avoid; the app
// never sees these.
const debugCmdBase uint16 = 0xFFF0

// nativeSpec is the native menu bar: the model's, and in a development build
// the Debug menu.
func (a *App) nativeSpec() platform.MenuSpec {
	s := nativeMenuSpec(a.menu)
	if devBuild {
		a.debugMenu(&s)
	}
	return s
}

// debugRepaint owes one frame, with its own cause, for a change of what the
// overlay shows; it does not keep animating.
func (a *App) debugRepaint() {
	a.p.invalidate()
	if a.dbg != nil {
		a.dbg.note(CauseDebug, 0, 0, nil)
	}
}

// uiAudit is the default layout audit: ui.Context.Audit of the last frame,
// without the overlay's own boxes.
func (a *App) uiAudit(dst []DebugFinding) []DebugFinding {
	for _, f := range a.UI.DebugAudit(nil) {
		dst = append(dst, DebugFinding{Key: f.Key, Rect: f.Rect, Reason: f.String()})
	}
	return dst
}

// DebugOn reports whether the overlay is on.
func (a *App) DebugOn() bool { return a.dbg != nil }

// DebugFrames is the number of frames recorded since the overlay turned on.
func (a *App) DebugFrames() uint64 {
	if a.dbg == nil {
		return 0
	}
	return a.dbg.nFrames
}

// DebugFrame returns the record of the frame back frames before the last one
// that ended (0 is the last), of the 64 kept. It reports false when there is
// no such frame.
func (a *App) DebugFrame(back int) (FrameRecord, bool) {
	d := a.dbg
	if d == nil {
		return FrameRecord{}, false
	}
	last := d.nFrames // the last that ended
	if d.open {
		last--
	}
	if back < 0 || back >= histFrame || uint64(back) >= last {
		return FrameRecord{}, false
	}
	return d.hist[(last-1-uint64(back))%histFrame], true
}

// DebugCauseCount is the number of recorded frames that had cause k among
// their causes since the overlay turned on.
func (a *App) DebugCauseCount(k Cause) uint64 {
	if a.dbg == nil || k >= numCauses {
		return 0
	}
	return a.dbg.counts[k]
}

// DebugUnexplained is the number of recorded frames that ran with no cause.
func (a *App) DebugUnexplained() uint64 {
	if a.dbg == nil {
		return 0
	}
	return a.dbg.unexpl
}

// note records a cause for the next frame.
func (d *debugState) note(kind Cause, sub uint8, key ui.Key, pcs []uintptr) {
	for i := 0; i < d.npend; i++ {
		r := &d.pend[i]
		if r.Kind == kind && r.Sub == sub && r.Key == key {
			if r.N < 1<<16-1 {
				r.N++
			}
			return
		}
	}
	if d.npend == maxCauses {
		d.lost++
		return
	}
	r := &d.pend[d.npend]
	d.npend++
	*r = RepaintCause{Kind: kind, Sub: sub, Key: key, N: 1}
	r.NCallers = uint8(copy(r.Callers[:], pcs))
}

// ---- hooks the loop and the frame call (each behind a.dbg != nil) ----

// dispatch hands one platform event to onEvent and notes why it made a frame
// owed, if it did: each event on its own, whether or not a frame was already
// owed. In development builds it first gives the debug chords and commands
// their turn (debug_dev.go).
func (a *App) dispatch(e *platform.Event) {
	if devBuild && a.debugIngress(e) {
		return
	}
	d := a.dbg
	if d == nil {
		a.onEvent(e)
		return
	}
	n := a.p.inval
	a.onEvent(e)
	if a.p.inval == n {
		return
	}
	var k Cause
	switch e.Kind {
	case platform.EvResize:
		k = CauseResize
	case platform.EvVisible:
		k = CauseVisible
	case platform.EvWake:
		if s := platform.AppearanceSerial(); s != d.serial {
			d.serial, k = s, CauseAppearance
		} else {
			k = CauseWake
		}
	case platform.EvHostFocus, platform.EvHostCommand:
		k = CauseHost
	case platform.EvDialog:
		k = CauseDialog
	case platform.EvCommand:
		k = CauseMenu
	default:
		k = CauseInput
	}
	d.note(k, 0, 0, nil)
}

// debugTimer notes that the time the last frame asked for has come.
func (a *App) debugTimer() {
	d := a.dbg
	d.note(CauseTimer, uint8(d.wakeKind), d.wakeKey, d.wakePC[:d.wakeNPC])
}

// debugSweep notes kind when a frame is owed that nothing noted: for the
// loop's stages that make a frame owed without an event.
func (a *App) debugSweep(kind Cause) {
	if d := a.dbg; d != nil && a.p.needDraw && d.npend == 0 && !d.open {
		d.note(kind, 0, 0, nil)
	}
}

// debugOwed notes what the frame just built leaves owed (it will be the cause
// of the next one) and what time it armed. why is arm's result.
func (a *App) debugOwed(why owedMask) {
	d := a.dbg
	if why&owedAnimating != 0 {
		var keys [1]ui.Key
		if a.UI.DebugAnimating(keys[:]) > 0 {
			d.note(CauseAnimation, 0, keys[0], nil)
		} else {
			d.note(CauseAnimation, 0, 0, a.UI.DebugCallers(ui.DebugCallerRequestFrame))
		}
	}
	if why&owedHover != 0 {
		d.note(CauseHover, 0, 0, nil)
	}
	if why&owedSettle != 0 {
		d.note(CauseSettle, 0, 0, nil)
	}
	if why&owedInput != 0 {
		d.note(CauseBacklog, 0, 0, nil)
	}
	if why&owedBench != 0 {
		d.note(CauseBench, 0, 0, nil)
	}
	d.wakeKind, d.wakeKey = a.UI.DebugWake()
	d.wakeNPC = 0
	if d.wakeKind == ui.DebugWakeIn {
		d.wakeNPC = copy(d.wakePC[:], a.UI.DebugCallers(ui.DebugCallerWakeIn))
	}
}

// debugFrameStart opens the record of the frame drawFrame is about to build.
func (a *App) debugFrameStart() {
	d := a.dbg
	if d.open {
		d.closeFrame(a) // one a panic left open
	}
	d.inDraw = true
	d.openFrame(a)
}

// debugBuildStart opens the frame record at the first build of a frame that
// drawFrame did not open (an Offscreen's).
func (a *App) debugBuildStart() {
	d := a.dbg
	if d.inDraw && d.open {
		return
	}
	if d.open {
		d.closeFrame(a)
	}
	d.openFrame(a)
}

// debugFrameEnd closes the frame record after the present.
func (a *App) debugFrameEnd() {
	d := a.dbg
	if a.Win == nil {
		d.mark(PhaseEncode) // an Offscreen's render
	} else {
		d.mark(PhasePresent)
	}
	d.closeFrame(a)
	d.inDraw = false
}

func (d *debugState) openFrame(a *App) {
	d.open = true
	d.nFrames++
	d.cur = FrameRecord{N: d.nFrames}
	if d.nFrames == 1 && a.frames == 0 && (a.Win != nil || a.step.started) {
		d.note(CauseFirst, 0, 0, nil)
	}
	if d.npend == 0 {
		switch {
		case a.Win == nil && !a.step.started:
			d.note(CauseDriver, 0, 0, nil)
		case a.bench != nil:
			d.note(CauseBench, 0, 0, nil)
		default:
			d.note(CauseUnknown, 0, 0, nil)
		}
	}
	d.cur.Causes, d.cur.NCauses, d.cur.CausesLost = d.pend, d.npend, d.lost
	d.pend, d.npend, d.lost = [maxCauses]RepaintCause{}, 0, 0
	for i := 0; i < d.cur.NCauses; i++ {
		d.counts[d.cur.Causes[i].Kind]++
		if d.cur.Causes[i].Kind == CauseUnknown {
			d.unexpl++
		}
	}
	d.mem0 = d.mallocs()
	d.cpuLast = sysinfo.ThreadCPUNs()
}

// mallocs is the number of heap allocations so far. runtime/metrics would be
// cheaper (160 ns) but counts a span's allocations only when the span is
// retired, so a frame that allocates a little shows 0; ReadMemStats is exact and
// costs one stop-the-world, about 20 µs, twice per frame, with the overlay on.
func (d *debugState) mallocs() uint64 {
	runtime.ReadMemStats(&d.ms)
	return d.ms.Mallocs
}

// mark adds the CPU time since the last mark to phase ph.
func (d *debugState) mark(ph int) {
	t := sysinfo.ThreadCPUNs()
	if t >= 0 && d.cpuLast >= 0 && d.open {
		d.cur.CPUns[ph] += t - d.cpuLast
	}
	d.cpuLast = t
}

// closeFrame finishes the record in progress.
func (d *debugState) closeFrame(a *App) {
	if !d.open {
		return
	}
	d.open = false
	r := &d.cur
	if m := d.mallocs(); m >= d.mem0 {
		r.Allocs = m - d.mem0
	}
	r.Quads = len(a.list.Quads)
	if a.R != nil {
		r.DrawCalls = a.R.DrawCalls()
	}
	d.hist[(r.N-1)%histFrame] = *r
	if d.log {
		d.logFrame(r)
	}
}

// logFrame writes one line about r to stderr.
func (d *debugState) logFrame(r *FrameRecord) {
	b := d.out[:0]
	b = append(b, "gnui: frame "...)
	b = strconv.AppendUint(b, r.N, 10)
	b = append(b, ": "...)
	for i := 0; i < r.NCauses; i++ {
		if i > 0 {
			b = append(b, " + "...)
		}
		b = appendCause(b, &r.Causes[i])
		if c := &r.Causes[i]; c.NCallers > 0 {
			b = append(b, " at "...)
			b = append(b, d.sym.symbol(c.Callers[0])...)
		}
	}
	b = append(b, " cpu="...)
	b = strconv.AppendInt(b, r.TotalCPUns()/1000, 10)
	b = append(b, "us allocs="...)
	b = strconv.AppendUint(b, r.Allocs, 10)
	b = append(b, " boxes="...)
	b = strconv.AppendInt(b, int64(r.Boxes), 10)
	b = append(b, '\n')
	d.out = b[:0]
	os.Stderr.Write(b)
}

// appendCause writes c as "anim #00ab12" (the key in hex), without callers.
func appendCause(b []byte, c *RepaintCause) []byte {
	b = append(b, c.Kind.String()...)
	if c.Kind == CauseTimer {
		switch ui.DebugWakeKind(c.Sub) {
		case ui.DebugWakeDeadline:
			b = append(b, "(deadline)"...)
		case ui.DebugWakeField:
			b = append(b, "(caret)"...)
		case ui.DebugWakeIn:
			b = append(b, "(WakeIn)"...)
		}
	}
	if c.Key != 0 {
		b = append(b, " #"...)
		b = strconv.AppendUint(b, uint64(c.Key), 16)
	}
	if c.N > 1 {
		b = append(b, " x"...)
		b = strconv.AppendUint(b, uint64(c.N), 10)
	}
	return b
}

// ---- symbols of caller program counters ----

// symCache holds the text of the program counters shown, so that showing a
// counter again allocates nothing; a miss symbolises (it allocates).
type symCache struct {
	ent [16]struct {
		pc uintptr
		n  uint8
		s  [80]byte
	}
	next int
}

// symbol returns "file.go:42 Func" for pc. The slice is valid until the next
// call that misses the cache.
func (c *symCache) symbol(pc uintptr) []byte {
	for i := range c.ent {
		if c.ent[i].pc == pc {
			return c.ent[i].s[:c.ent[i].n]
		}
	}
	e := &c.ent[c.next]
	c.next = (c.next + 1) % len(c.ent)
	e.pc = pc
	e.n = uint8(copy(e.s[:], symbolize(pc)))
	return e.s[:e.n]
}

func symbolize(pc uintptr) string {
	f := runtime.FuncForPC(pc - 1)
	if f == nil {
		return "?"
	}
	file, line := f.FileLine(pc - 1)
	if i := strings.LastIndexByte(file, '/'); i >= 0 {
		file = file[i+1:]
	}
	name := f.Name()
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return file + ":" + strconv.Itoa(line) + " " + name
}
