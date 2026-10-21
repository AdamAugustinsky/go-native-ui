//go:build darwin

package platform

import (
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The view: drawing, the display link (CADisplayLink, then CAMetalDisplayLink)
// and the backing properties. registerViewDraw adds the view's frame-pacing
// methods to GNUIView.

var (
	selDisplayLinkWith  = objc.Sel("displayLinkWithTarget:selector:")
	selScreen           = objc.Sel("screen")
	selMaxFPS           = objc.Sel("maximumFramesPerSecond")
	selSetRateRange     = objc.Sel("setPreferredFrameRateRange:")
	selAddToRunLoop     = objc.Sel("addToRunLoop:forMode:")
	selMainRunLoop      = objc.Sel("mainRunLoop")
	selViewBacking      = objc.Sel("viewDidChangeBackingProperties")
	selDisplayLinkFired = objc.Sel("gnuiDisplayLink:")
	selMetalLinkUpdate  = objc.Sel("metalDisplayLink:needsUpdate:")
	selInitWithLayer    = objc.Sel("initWithMetalLayer:")
	selSetFrameLatency  = objc.Sel("setPreferredFrameLatency:")
	selUpdateDrawable   = objc.Sel("drawable")
	selAutorelease      = objc.Sel("autorelease")
	selTrimDrawables    = objc.Sel("gnuiTrimDrawables")
	selDrawableSize     = objc.Sel("drawableSize")
	selSetDrawableSize  = objc.Sel("setDrawableSize:")
	selCancelPerformSel = objc.Sel("cancelPreviousPerformRequestsWithTarget:selector:object:")
)

var clsMetalLink, clsRunLoop, clsNSObject objc.ID // set by registerClasses

func registerViewDraw(v *objc.ClassDef) {
	v.Method(selViewBacking, "v@:", windowBackingChanged)
	v.Method(selDisplayLinkFired, "v@:@", viewDisplayLinkFired)
	v.Method(selMetalLinkUpdate, "v@:@@", viewMetalLinkUpdate)
	v.Method(selTrimDrawables, "v@:", viewTrimDrawables)
}

// windowBackingChanged is viewDidChangeBackingProperties and the window
// delegate's windowDidChangeBackingProperties:.
func windowBackingChanged(c *ffi.CB) {
	if w := viewOrDelegateWindow(objc.ID(c.Int(0))); w != nil {
		w.backingChanged()
	}
}

// backingChanged queues the new size or scale; inside a live resize it also
// draws, in step with the window.
func (w *Window) backingChanged() {
	if w.pushResize() && w.live {
		w.runHandler()
	}
}

// viewDisplayLinkFired is gnuiDisplayLink: (the CADisplayLink of the view).
func viewDisplayLinkFired(c *ffi.CB) {
	if w := viewWindow(objc.ID(c.Int(0))); w != nil {
		w.linkTick(c, false)
	}
}

// viewMetalLinkUpdate is metalDisplayLink:needsUpdate: (link, update).
func viewMetalLinkUpdate(c *ffi.CB) {
	if w := viewWindow(objc.ID(c.Int(0))); w != nil {
		w.linkTick(c, true)
	}
}

// linkTick is one tick of the display link; metal says it is the
// CAMetalDisplayLink's, which hands over a drawable.
func (w *Window) linkTick(c *ffi.CB, metal bool) {
	if !metal && w.metalLayer != 0 && !w.live {
		layer := w.metalLayer
		w.metalLayer = 0
		w.attachMetalLink(layer) // its first tick delivers this frame, with a drawable
		return
	}
	now := Now()
	w.ticks, w.ticksSinceFrame, w.lastTickT = w.ticks+1, w.ticksSinceFrame+1, now
	// A drawable is held for one tick at most: one kept through an idle
	// period would leave the link without a free drawable (2 in the
	// pool), and it stops ticking.
	if w.drawable != 0 {
		objc.Send(w.drawable, selReleaseObj)
		w.drawable = 0
	}
	if w.frameArmed {
		w.frameArmed, w.idleTicks = false, 0
		if metal { // metalDisplayLink:needsUpdate: (link, update)
			w.drawable = objc.Retain(objc.Send(objc.ID(c.Int(3)), selUpdateDrawable))
		}
		w.ticksSinceFrame, w.lastFrameT = 0, now
		if w.frameOut && now-w.frameOutT > nestedAfter { // an earlier tick's frame never reached Wait: a nested loop runs
			w.nested = true
		}
		if !w.frameOut { // one undelivered EvFrame is enough (a nested loop may last)
			w.q.push(Event{Kind: EvFrame, Time: now})
			w.frameOut, w.frameOutT = true, now
		}
		objc.Send2(w.app, selPostEvent, uintptr(w.wakeEv), 0) // unblock nextEventMatchingMask
		if NativeLoop() || w.nested {
			// A requested frame (animation, input, a worker's result) while
			// AppKit runs a loop of its own, for this window or another (a
			// live resize, menu tracking, a drag session, its wait for a quit
			// answer), or another nested loop runs: the app's pump is
			// suspended, so the window's frame handler draws (D30).
			w.runHandlerFrom(true)
		}
		return
	}
	if w.frameOut && w.handler != nil && !w.inHandler && now-w.frameOutT > nestedAfter {
		// A frame was queued a tick ago and Wait has not returned it: the
		// main thread is in a loop that does not return to the app's (a
		// correction bubble's, a block's). Arm a frame, whose tick brings a
		// drawable and draws through the frame handler.
		w.nested = true
		w.RequestFrame()
		return
	}
	// Keep ticking through short gaps: pausing after every tick made the
	// link re-sync on each RequestFrame (uneven intervals on ProMotion).
	// Not while a frame waits for the app: the ticks must see a nested loop.
	if w.idleTicks++; w.idleTicks >= linkIdleTicks && !w.linkPaused && !w.frameOut {
		objc.Send1(w.link, selSetPaused, 1)
		w.linkPaused = true
		w.pauses++
		if w.metalLink {
			w.scheduleTrim(trimAfter)
		}
	}
}

// An idle window's drawables. A CAMetalDisplayLink keeps as many drawables
// as its frames have in flight, whatever the layer's maximumDrawableCount:
// four window-sized ones at 120 Hz, three at 60 Hz, all in turn (the view's
// link with nextDrawable keeps the layer's two). The link needs them while
// frames come at the display's rate; an idle window needs only the one it
// shows. So trimAfterNs after the last frame, trimDrawables sets the layer's
// drawableSize to another size and back: the layer lets go of the drawables
// not in use (the one on screen stays), and the link makes new ones when
// frames come again. Not at each pause: drawables made again after every
// pause (typing, a short scroll) made frames late.
const trimAfterNs = int64(5e9)

// trimAfter is trimAfterNs, or a test's (DebugTrimAfter).
var trimAfter = trimAfterNs

// scheduleTrim asks for trimDrawables d from now, unless a request is
// pending (it looks again when it runs).
func (w *Window) scheduleTrim(d int64) {
	if w.trimDue {
		return
	}
	w.trimDue = true
	objc.F.Reset().Arg(uintptr(w.view)).Arg(uintptr(selPerformAfterDelay)).Arg(uintptr(selTrimDrawables)).Arg(0).ArgF64(float64(d) / 1e9).Call(objc.MsgSend)
}

// viewTrimDrawables is gnuiTrimDrawables, scheduleTrim's request.
func viewTrimDrawables(c *ffi.CB) {
	if w := viewWindow(objc.ID(c.Int(0))); w != nil {
		w.trimDrawables()
	}
}

// trimDrawables gives back the drawables of a window idle for trimAfterNs
// (see trimAfterNs). A window drawing again, or not driven by a metal link,
// keeps them: its next pause asks again.
func (w *Window) trimDrawables() {
	w.trimDue = false
	if !w.metalLink || !w.linkPaused || w.live || w.frameLayer == 0 {
		return
	}
	if left := trimAfter - (Now() - w.lastFrameT); left > 0 {
		w.scheduleTrim(left) // it drew since the request
		return
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	f := objc.F.Reset().Arg(uintptr(w.frameLayer)).Arg(uintptr(selDrawableSize)).Call(objc.MsgSend)
	dw, dh := f.F64(0), f.F64(1)
	other := 1.0
	if dw == 1 && dh == 1 {
		other = 2
	}
	objc.F.Reset().Arg(uintptr(w.frameLayer)).Arg(uintptr(selSetDrawableSize)).ArgPoint(other, other).Call(objc.MsgSend)
	objc.F.Reset().Arg(uintptr(w.frameLayer)).Arg(uintptr(selSetDrawableSize)).ArgPoint(dw, dh).Call(objc.MsgSend)
	w.trims++
}

// cancelTrim drops a pending trimDrawables request (the window closes).
func (w *Window) cancelTrim() {
	if !w.trimDue {
		return
	}
	w.trimDue = false
	objc.F.Reset().Arg(uintptr(clsNSObject)).Arg(uintptr(selCancelPerformSel)).Arg(uintptr(w.view)).Arg(uintptr(selTrimDrawables)).Arg(0).Call(objc.MsgSend)
}

// makeViewLink creates the view's CADisplayLink, retained, paused and added
// to the main run loop in the common modes (so it also ticks inside AppKit's
// tracking loops). It returns 0 before macOS 14.
func (w *Window) makeViewLink() objc.ID {
	link := objc.Retain(objc.Send2(w.view, selDisplayLinkWith, uintptr(w.view), uintptr(selDisplayLinkFired)))
	if link == 0 {
		return 0
	}
	objc.Send2(link, selAddToRunLoop, uintptr(objc.Send(clsRunLoop, selMainRunLoop)), uintptr(commonModes))
	objc.Send1(link, selSetPaused, 1)
	w.setRateRange(link)
	return link
}

// RequestFrame arms one EvFrame at the next display refresh.
func (w *Window) RequestFrame() {
	if !w.frameArmed {
		w.armedT = Now()
	}
	w.frameArmed = true
	if w.linkPaused {
		objc.Send1(w.link, selSetPaused, 0)
		w.linkPaused = false
	}
}

// SetFrameSource switches frame pacing to a CAMetalDisplayLink on the given
// CAMetalLayer (the GPU surface's FrameSource). Each tick then hands over a
// drawable (TakeMetalDrawable), so presenting never blocks in nextDrawable;
// the view's CADisplayLink blocked the main thread for most of each frame.
//
// A layer driven by a CAMetalDisplayLink refuses nextDrawable, but the first
// frame is drawn before the run loop first spins (draw-first startup), so
// the switch happens on the view link's first tick, which always follows the
// first present.
func (w *Window) SetFrameSource(layer uintptr) {
	if clsMetalLink != 0 {
		w.metalLayer, w.frameLayer = objc.ID(layer), objc.ID(layer)
	}
}

// attachMetalLink replaces the view's CADisplayLink with a CAMetalDisplayLink
// on layer. Called from the view link's own tick (invalidating it there is
// allowed; it is autoreleased, not released, while on the stack) and when a
// live resize ends.
func (w *Window) attachMetalLink(layer objc.ID) {
	link := objc.Send(clsMetalLink, selAlloc)
	link = objc.Send1(link, selInitWithLayer, uintptr(layer))
	if link == 0 {
		return
	}
	objc.Send1(link, selSetDelegate, uintptr(w.view))
	objc.F.Reset().Arg(uintptr(link)).Arg(uintptr(selSetFrameLatency)).ArgF32(metalLinkLatency).Call(objc.MsgSend)
	w.setRateRange(link)
	objc.Send2(link, selAddToRunLoop, uintptr(objc.Send(clsRunLoop, selMainRunLoop)), uintptr(commonModes))
	objc.Send1(link, selSetPaused, 1)
	objc.Send(w.link, selInvalidate)
	objc.Send(w.link, selAutorelease)
	w.link, w.linkPaused, w.metalLink = link, true, true
	if w.frameArmed {
		objc.Send1(w.link, selSetPaused, 0)
		w.linkPaused = false
	}
}

// metalLinkLatency is CAMetalDisplayLink.preferredFrameLatency: how many
// frames ahead of display the drawable is handed over.
const metalLinkLatency = 1

// TakeMetalDrawable returns the drawable delivered with the current frame
// tick, retained (the caller releases it after presenting), or 0. A tick's
// drawable is valid until the next tick, so draw on EvFrame. linked reports
// that a CAMetalDisplayLink drives the layer: then a 0 drawable means no
// frame is ready (one is requested), and the caller must not call
// nextDrawable (the layer throws). Main thread only.
//
// It is the first window's (a one-window app's); a surface of any window
// uses its Native's TakeDrawable.
func TakeMetalDrawable() (drawable uintptr, linked bool) {
	if reg.n == 0 {
		return 0, false
	}
	return reg.open[0].takeDrawable()
}

// TakeDrawable is TakeMetalDrawable for the window n belongs to (each window
// has its own display link and drawables). Main thread only.
func (n Native) TakeDrawable() (drawable uintptr, linked bool) {
	if w := n.window(); w != nil {
		return w.takeDrawable()
	}
	return 0, false
}

// window is the open window n belongs to, nil if it is closed (or n was made
// by hand).
func (n Native) window() *Window {
	if w := n.w; w != nil && w.registered {
		return w
	}
	for _, w := range openWindows() {
		if uintptr(w.view) == n.NSView {
			return w
		}
	}
	return nil
}

func (w *Window) takeDrawable() (drawable uintptr, linked bool) {
	d := w.drawable
	w.drawable = 0
	if d == 0 && w.metalLink {
		if w.linkStopped() && w.restartLink() {
			return 0, false // this frame takes the layer's nextDrawable, as the first one does
		}
		w.RequestFrame() // no frame is ready: the caller draws on the EvFrame that follows
	}
	return uintptr(d), w.metalLink
}

// linkStopNs is how long a running CAMetalDisplayLink may leave an armed
// frame of a visible window without a tick before it counts as stopped.
const linkStopNs = int64(1e9)

// linkStopped reports a CAMetalDisplayLink that stopped calling back: a frame
// has been armed for linkStopNs, the link runs, no tick came since, and the
// window can be seen (a covered or miniaturized window gets no ticks, and
// nextDrawable would block for it). The app's loop asks for a drawable
// without a tick only after such a silence (its unpaced fallback, app/pacer.go),
// and a linked layer has none to give: without a restart the window would
// never draw again. Seen on CI's virtual display: a window's link stopped
// after it was paused and resumed while another window of the app had just
// drawn.
func (w *Window) linkStopped() bool {
	now := Now()
	return w.frameArmed && !w.linkPaused && !w.live &&
		now-w.armedT >= linkStopNs && now-w.lastTickT >= linkStopNs && now-w.restartT >= linkStopNs &&
		uintptr(objc.Send(w.win, selOcclusionState))&2 != 0 && !objc.SendBool(w.win, selIsMini)
}

// restartLink replaces a stopped CAMetalDisplayLink by a view CADisplayLink,
// as at startup: the caller draws with the layer's nextDrawable, and the view
// link's first tick attaches a new CAMetalDisplayLink (linkTick).
func (w *Window) restartLink() bool {
	link := w.makeViewLink()
	if link == 0 {
		return false
	}
	now := Now()
	// Once per restart (docs/research/displaylink-stall.md): the stall is not
	// understood yet, and each one is evidence.
	println("gnui: display link of window", w.slot, "stopped calling back: armed", (now-w.armedT)/1e6, "ms ago, last tick", (now-w.lastTickT)/1e6, "ms ago, ticks", w.ticks, "pauses", w.pauses, "windows open", reg.n, "; replaced (restart", w.restarts+1, ")")
	objc.Send(w.link, selInvalidate)
	objc.Send(w.link, selReleaseObj)
	w.link, w.metalLink, w.metalLayer = link, false, w.frameLayer
	w.restarts, w.restartT = w.restarts+1, now
	objc.Send1(w.link, selSetPaused, 0) // a frame is armed
	w.linkPaused = false
	return true
}

func (w *Window) setRateRange(link objc.ID) {
	// Ask for the display's full rate while animating (ProMotion would
	// otherwise pick a lower adaptive rate).
	if fps := float32(objc.Send(objc.Send(w.win, selScreen), selMaxFPS)); fps > 0 {
		objc.F.Reset().Arg(uintptr(link)).Arg(uintptr(selSetRateRange)).ArgFloat3(fps/2, fps, fps).Call(objc.MsgSend)
	}
}

// linkIdleTicks is how many display-link ticks without a frame request pass
// before the link is paused (idle must stay at zero wakeups).
const linkIdleTicks = 4

// nestedAfter is how long an EvFrame may wait for the app's loop before the
// ticks take the main thread for a nested loop's (a few refreshes: Wait
// returns at once after a tick, unless the run loop never gets back to it).
const nestedAfter = int64(50e6)
