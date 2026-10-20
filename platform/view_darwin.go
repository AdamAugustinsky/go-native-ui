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
)

var clsMetalLink, clsRunLoop objc.ID // set by registerClasses

func registerViewDraw(v *objc.ClassDef) {
	v.Method(selViewBacking, "v@:", windowBackingChanged)
	v.Method(selDisplayLinkFired, "v@:@", viewDisplayLinkFired)
	v.Method(selMetalLinkUpdate, "v@:@@", viewMetalLinkUpdate)
}

// windowBackingChanged is viewDidChangeBackingProperties and the window
// delegate's windowDidChangeBackingProperties:.
func windowBackingChanged(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
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
	if w := theWindow.Load(); w != nil {
		w.linkTick(c, false)
	}
}

// viewMetalLinkUpdate is metalDisplayLink:needsUpdate: (link, update).
func viewMetalLinkUpdate(c *ffi.CB) {
	if w := theWindow.Load(); w != nil {
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
		if w.live || menuTracking() || w.drag.active || w.nested {
			w.runHandlerFrom(true) // a requested frame (animation, input) while AppKit's resize, menu tracking, drag session or another nested loop runs
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
	}
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
func TakeMetalDrawable() (drawable uintptr, linked bool) {
	w := theWindow.Load()
	if w == nil {
		return 0, false
	}
	d := w.drawable
	w.drawable = 0
	if d == 0 && w.metalLink {
		w.RequestFrame() // no frame is ready: the caller draws on the EvFrame that follows
	}
	return uintptr(d), w.metalLink
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
