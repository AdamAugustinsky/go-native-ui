//go:build darwin

package platform

import (
	"os"

	"gonativeui/internal/objc"
)

// Live resize: while the user drags a window edge AppKit runs its own loop
// inside sendEvent, and the frame handler keeps the content in step.

// SetFrameHandler registers fn to be called, on the main thread, from inside
// AppKit's live-resize loop. While the user drags a window edge, sendEvent:
// does not return and Wait does not either, so the app's loop cannot draw;
// the handler is the way to keep the content in step with the window.
//
// It runs from windowDidResize: (size changed) and, while a frame is
// requested with RequestFrame, from display-link ticks. Take Pending, resize
// the surface on EvResize, build, draw, present. gpu.Surface presents those
// frames with presentsWithTransaction so each commits in the same Core
// Animation transaction as the window's new size. See enterLiveResize.
//
// Without a handler a live resize keeps the old behaviour: the app is not
// redrawn until the drag ends.
func (w *Window) SetFrameHandler(fn func()) { w.handler = fn }

// InLiveResize reports whether a live resize with a frame handler is in
// progress: the frame source is then not a CAMetalDisplayLink (which makes
// nextDrawable throw), so gpu.Surface acquires drawables with nextDrawable
// and presents with presentsWithTransaction. Main thread only.
//
// It reports whether any window is in a live resize; a surface asks about its
// own window with its Native's InLiveResize.
func InLiveResize() bool {
	for _, w := range openWindows() {
		if w.live {
			return true
		}
	}
	return false
}

// InLiveResize is the package's InLiveResize for the window n belongs to:
// another window's live resize leaves this one's frame source alone.
func (n Native) InLiveResize() bool {
	w := n.window()
	return w != nil && w.live
}

func (w *Window) runHandler() { w.runHandlerFrom(false) }

// runHandlerFrom runs the frame handler; tick says a display-link tick asked
// for it (otherwise windowDidResize: did).
func (w *Window) runHandlerFrom(tick bool) {
	if w.handler == nil || w.inHandler {
		return
	}
	w.inHandler = true
	w.handlerRuns++
	t0 := Now()
	w.handler()
	if traceLive && w.live {
		w.lt.handler(t0, Now(), tick)
	}
	w.inHandler = false
}

// traceLive (GNUI_TRACE_LIVE=1) prints how the frame handler behaves during
// a live resize: every slow call, and a summary when the resize ends.
var traceLive = os.Getenv("GNUI_TRACE_LIVE") != ""

// TraceLive reports whether GNUI_TRACE_LIVE is set (gpu adds its own timings).
func TraceLive() bool { return traceLive }

type liveTrace struct {
	start, last            int64
	resizeCalls, tickCalls int
	slow                   int
	maxNs, sumNs, maxGap   int64
}

func (t *liveTrace) begin() { *t = liveTrace{start: Now()} }

func (t *liveTrace) handler(t0, t1 int64, tick bool) {
	d := t1 - t0
	gap := int64(0)
	if t.last != 0 {
		gap = t0 - t.last
	}
	t.last = t1
	if tick {
		t.tickCalls++
	} else {
		t.resizeCalls++
	}
	t.sumNs += d
	t.maxNs, t.maxGap = max(t.maxNs, d), max(t.maxGap, gap)
	if d > 25e6 || gap > 100e6 {
		t.slow++
		src := "resize"
		if tick {
			src = "tick"
		}
		println("gnui live: handler from", src, "took", d/1e6, "ms, gap before it", gap/1e6, "ms, at", (t0-t.start)/1e6, "ms")
	}
}

func (t *liveTrace) end() {
	n := t.resizeCalls + t.tickCalls
	mean := int64(0)
	if n > 0 {
		mean = t.sumNs / int64(n) / 1e3
	}
	println("gnui live: resize lasted", (Now()-t.start)/1e6, "ms; handler calls", t.resizeCalls, "from windowDidResize,", t.tickCalls, "from ticks; mean", mean, "us, max", t.maxNs/1e6, "ms, max gap", t.maxGap/1e6, "ms, slow", t.slow)
}

// enterLiveResize starts the live-resize mode. A layer driven by a
// CAMetalDisplayLink refuses nextDrawable, so the metal link is invalidated
// and released and replaced by a plain view CADisplayLink (the one the first
// frames used); exitLiveResize re-attaches a new metal link.
func (w *Window) enterLiveResize() {
	if w.live || w.handler == nil {
		return
	}
	if w.drawable != 0 { // a tick's drawable must not outlive its link
		objc.Send(w.drawable, selReleaseObj)
		w.drawable = 0
	}
	if w.metalLink {
		link := w.makeViewLink()
		if link == 0 {
			return
		}
		objc.Send(w.link, selInvalidate)
		objc.Send(w.link, selReleaseObj)
		w.link, w.linkPaused, w.metalLink, w.relink = link, true, false, true
		if w.frameArmed {
			objc.Send1(w.link, selSetPaused, 0)
			w.linkPaused = false
		}
	}
	w.live = true
	if traceLive {
		w.lt.begin()
	}
	if debugIMP {
		println("live resize begins; metal link detached:", w.relink)
	}
}

// exitLiveResize leaves the live-resize mode and restores the
// CAMetalDisplayLink if the live resize had replaced it.
func (w *Window) exitLiveResize() {
	if !w.live {
		return
	}
	w.live = false
	if traceLive {
		w.lt.end()
	}
	if w.relink {
		w.relink = false
		w.attachMetalLink(w.frameLayer)
	}
}
