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
	a.UI.Begin(&a.tr.in, w, h, a.scale, dt)
	a.b.Build(a.UI)
	a.UI.End(&a.list)
	a.tr.endFrame()
}

// owed reports whether another frame must follow the one just built: while
// something animates, for one frame after input with the pointer inside (hit
// testing uses the previous layout, so what moved under a still pointer is
// only hovered one frame later), after a resize wherever the pointer is
// (ListRange, scrollbars and every other builder that reads rectangles read
// the previous frame's, which had the old size), while events wait for their
// own frame (translator.event), and while a bench runs.
func (a *App) owed(animating, dirty bool) bool {
	return animating || (dirty && a.tr.in.Inside) || a.settle || a.tr.pending() || (a.bench != nil && !a.scriptDone)
}

// frameDT is the time step of the frame starting at now. The first frame,
// and a frame after a long pause (idle), use a nominal 60 Hz step so that an
// animation starting from rest does not jump.
func (a *App) frameDT(now int64) float32 {
	prev := a.lastStart
	a.lastStart = now
	if prev == 0 {
		return 1.0 / 60
	}
	d := float32(now-prev) / 1e9
	if d > 0.1 {
		return 1.0 / 60
	}
	return max(d, 0.0005)
}

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
	if !a.Surf.BeginFrame(&a.fr) {
		return false
	}
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

	a.settle = false
	for i := 0; i < passes; i++ {
		a.build(a.fr.W, a.fr.H, dt)
	}
	a.syncRegions()
	if metered {
		m.cpu2 = sysinfo.ThreadCPUNs()
	}

	// The request must precede the present (Wayland attaches the frame
	// callback to the commit). In the OS modal loop a frame is only asked for
	// while something animates: the resize itself drives the handler.
	animating := a.UI.Animating()
	if !inHandler || animating {
		a.Win.RequestFrame()
		a.p.requested(m.start)
	} else {
		a.p.requestedAt = 0
	}
	a.R.Encode(a.fr.CmdBuf, a.fr.Target, a.fr.W, a.fr.H, a.cfg.Clear, &a.list)
	a.Surf.EndFrame(&a.fr)
	m.commit = platform.Now()
	if metered {
		m.cpu3 = sysinfo.ThreadCPUNs()
	}

	// See owed.
	a.more = a.owed(animating, dirty)
	a.fm = m
	a.frames++
	if a.bench == nil && evT != 0 {
		if l := m.commit - evT; l > 0 && l < maxLatencyNs {
			a.lat.add(ms(l))
		}
	}
	return true
}
