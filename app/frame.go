package app

import (
	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
)

// maxLatencyNs bounds the input-to-commit samples; an older event time is a
// stale timestamp (the event waited for a hidden window), not a latency.
const maxLatencyNs = int64(5e9)

// build runs one ui frame of the Builder: Begin, Build, End, then clears the
// per-frame input.
func (a *App) build(w, h int, dt float32) {
	a.settle = a.settle || a.resized
	a.resized = false
	if a.dbg != nil {
		a.debugBuildStart()
	}
	a.useFontScale() // zoom.go
	a.ax.building = true
	a.UI.Begin(&a.tr.in, w, h, a.EffectiveScale(), dt)
	a.b.Build(a.UI)
	if a.dbg != nil {
		a.debugBuild()
	}
	a.UI.End(&a.list)
	a.ax.building = false
	if a.dbg != nil {
		a.dbg.mark(PhaseLayout)
	}
	a.applyMenuZoom() // zoom.go
	a.tr.endFrame()
}

// owedMask says why another frame must follow the one just built.
type owedMask uint8

const (
	owedAnimating owedMask = 1 << iota // something animates
	owedHover                          // input with the pointer inside: hit testing uses the previous layout, so what moved under a still pointer is only hovered one frame later
	owedSettle                         // a resize: ListRange, scrollbars and every builder that reads rectangles read the previous frame's, which had the old size
	owedInput                          // events wait for a frame of their own (translator.event)
	owedBench                          // a bench is running
)

// String lists the reasons as letters (A H S I B), "-" for none.
func (m owedMask) String() string { // for tests and logs; the dump uses appendTo
	if m == 0 {
		return "-"
	}
	var b [5]byte
	n := 0
	for i, c := range "AHSIB" {
		if m&(1<<uint(i)) != 0 {
			b[n] = byte(c)
			n++
		}
	}
	return string(b[:n])
}

// owedReasons computes why a frame is owed after the one just built.
func (a *App) owedReasons(animating, dirty bool) owedMask {
	var m owedMask
	if animating {
		m |= owedAnimating
	}
	if dirty && a.tr.in.Inside {
		m |= owedHover
	}
	if a.settle {
		m |= owedSettle
	}
	if a.tr.pending() {
		m |= owedInput
	}
	if (a.bench != nil || a.script != nil) && !a.scriptDone { // a script (a benchmark's, or the agent bridge's steps in a live window) plays a frame at a time
		m |= owedBench
	}
	return m
}

// owed reports whether another frame must follow the one just built.
func (a *App) owed(animating, dirty bool) bool { return a.owedReasons(animating, dirty) != 0 }

// arm asks the platform for the next frame and returns why one is owed. The
// request must precede the present (Wayland attaches the frame callback to the
// commit). The OS modal loop (live resize) normally drives the handler by
// itself, one call per size change, and an unneeded request would only add a
// stray tick after the drag; but whatever is owed that no size change will
// bring — the settle frame after a resize that is then held still, input
// carried over to the next frame — needs the request as much as an animation
// does, because the main loop cannot run until the modal loop ends.
func (a *App) arm(inHandler, animating, dirty bool, start int64) owedMask {
	why := a.owedReasons(animating, dirty)
	if !inHandler || why != 0 {
		a.req.RequestFrame()
		a.p.requested(start)
	} else {
		a.p.requestedAt = 0
	}
	return why
}

// frameDT is the time step of the frame starting at now. A frame that
// follows one that left nothing animating (the first, one after idle, one
// after input that moved nothing) steps by one display interval: the time
// before it, while nothing moved, belongs to no animation, and an animation
// that starts in it takes no leap (D103). So does a frame after a stall of
// more than 100 ms. Otherwise it is the time since the last frame's start.
//
// The display interval is the shortest of the last 16 steps between frames
// of a running animation (60 Hz until there are any), so a stall does not
// lengthen it and a move to a slower display does within 16 frames. It is
// given to the ui context
// (ui.Context.FrameInterval), which steps a value that starts to move during
// a late frame by no more than it.
func (a *App) frameDT(now int64) float32 {
	prev := a.lastStart
	a.lastStart = now
	if a.interval == 0 {
		a.interval = 1.0 / 60
	}
	d := float32(now-prev) / 1e9
	running := prev != 0 && a.owedWhy&owedAnimating != 0
	if running && d >= minInterval && d <= maxInterval {
		a.steps[a.nSteps%len(a.steps)] = d
		a.nSteps++
		a.interval = d
		for _, s := range a.steps[:min(a.nSteps, len(a.steps))] {
			a.interval = min(a.interval, s)
		}
	}
	if a.UI != nil {
		a.UI.FrameInterval = a.interval
	}
	if !running || d > 0.1 {
		return a.interval
	}
	return max(d, 0.0005)
}

// minInterval and maxInterval bound the display intervals frameDT learns
// from: 240 Hz to 24 Hz. A step outside them is a hitch or a burst, not the
// display's rate.
const (
	minInterval = 1.0 / 240
	maxInterval = 1.0 / 24
)

// drawFrame draws one frame into the surface's next frame: BeginFrame, the
// ui build (passes times, the last one is drawn), the draw list encode and
// EndFrame. It returns false, having changed no ui state, when BeginFrame
// finds no frame to draw into.
//
// The frame is acquired before the build, so a failed attempt consumes no
// input and runs no Build; a paced frame (macOS) is acquired and used within
// one tick.
func (a *App) drawFrame(inHandler bool, passes int) bool {
	metered := a.bench != nil && a.bench.measuring
	m := frameMeasure{}
	if metered {
		m.cpu0 = sysinfo.ThreadCPUNs()
	}
	was := a.stall.mark(phaseBegin, 0)
	defer a.stall.mark(was, -1) // a frame drawn from a nested loop gives its phase back, from now (a pump: its end unknown)
	if !a.Surf.BeginFrame(&a.fr) {
		return false
	}
	a.inFrame = true
	a.framing++            // no window's frame handler draws, and no safe point runs, inside it
	platform.HoldApp(true) // no app-wide event is delivered inside the frame
	if a.dbg != nil {
		a.debugFrameStart()
	}
	// What the app's code autoreleases during the frame is drained at its end
	// (macOS). The token lives on this stack, not in shared state: a nested
	// run loop that the app's code runs nests its own pools inside this one.
	pool := framePoolPush()
	defer a.abandonFrame(pool) // a panic before EndFrame: nothing stays held
	m.start = platform.Now()
	if metered {
		m.cpu1 = sysinfo.ThreadCPUNs()
	}
	evT, dirty := a.tr.take()
	dt := a.frameDT(m.start)
	if a.script != nil && !a.scriptDone {
		if !a.script.Script(a.scriptFrame, &a.tr.in) {
			a.scriptDone = true
		}
		a.scriptFrame++
		if evT == 0 {
			evT = m.start // synthetic input has no OS timestamp: its clock starts with the frame
		}
	}
	m.evT = evT
	a.tr.in.Time = m.start

	a.settle = false
	a.syncAppearance()
	a.syncWindow()
	a.stall.mark(phaseBuild, 0)
	for i := 0; i < passes; i++ {
		a.build(a.fr.W, a.fr.H, dt)
	}
	a.syncRegions()
	a.syncShell()
	a.syncAX()
	a.syncText(a.Win, m.start)
	a.syncColorPanel(a.Win)
	a.syncChrome(a.Win)
	if metered {
		m.cpu2 = sysinfo.ThreadCPUNs()
	}

	animating := a.UI.Animating()
	why := a.arm(inHandler, animating, dirty, m.start)
	if a.dbg != nil {
		a.debugOwed(why)
	}
	a.stall.mark(phaseEncode, 0)
	present := a.encodeFrame() // only what changed, on macOS (D92)
	if a.dbg != nil {
		a.dbg.mark(PhaseEncode)
	}
	if present {
		a.stall.mark(phasePresent, 0)
		a.Surf.EndFrame(&a.fr) // applies the shell changes syncShell staged, in the present's transaction
	}
	a.checkDamage()
	a.Win.FlushShell() // (nothing is left; a safety net), inside the frame's pool
	framePoolPop(pool)
	a.inFrame = false
	a.framing--
	platform.HoldApp(a.framing > 0)
	m.commit = platform.Now()
	if metered {
		m.cpu3 = sysinfo.ThreadCPUNs()
	}
	if a.dbg != nil {
		a.debugFrameEnd()
	}

	a.owedWhy = why
	a.more = why != 0
	a.fm = m
	a.frames++
	if a.bench == nil && evT != 0 {
		if l := m.commit - evT; l > 0 && l < maxLatencyNs {
			a.lat.add(ms(l))
		}
	}
	return true
}

// abandonFrame is drawFrame's cleanup when the frame did not reach EndFrame
// (a panic in Build, recovered further up): it releases the frame's drawable
// and command buffer, drains its pool and ends the frame, so the loop and
// the frame handler can draw again. After a presented frame it does nothing.
func (a *App) abandonFrame(pool uintptr) {
	if !a.inFrame {
		return
	}
	a.inFrame = false
	a.framing--
	a.ax.building = false // a Build that panicked never reached End
	platform.HoldApp(a.framing > 0)
	abandonSurfaceFrame(a.Surf, a.R, &a.fr)
	framePoolPop(pool)
}
