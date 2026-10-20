//go:build darwin

package platform

import (
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The window delegate (GNUIWindowDelegate): close, resize, backing, focus,
// occlusion and live-resize notifications.

var (
	selShouldClose      = objc.Sel("windowShouldClose:")
	selDidResize        = objc.Sel("windowDidResize:")
	selDidChangeBacking = objc.Sel("windowDidChangeBackingProperties:")
	selDidBecomeKey     = objc.Sel("windowDidBecomeKey:")
	selDidResignKey     = objc.Sel("windowDidResignKey:")
	selDidChangeOcclus  = objc.Sel("windowDidChangeOcclusionState:")
	selWillStartLive    = objc.Sel("windowWillStartLiveResize:")
	selDidEndLive       = objc.Sel("windowDidEndLiveResize:")
)

func registerWindowDelegate(d *objc.ClassDef) {
	d.Method(selShouldClose, "c@:@", windowShouldClose)
	d.Method(selDidResize, "v@:@", windowDidResize)
	d.Method(selDidChangeBacking, "v@:@", windowBackingChanged)
	d.Method(selDidBecomeKey, "v@:@", windowDidBecomeKey)
	d.Method(selDidResignKey, "v@:@", windowDidResignKey)
	d.Method(selDidChangeOcclus, "v@:@", windowDidChangeOcclusion)
	d.Method(selWillStartLive, "v@:@", windowWillStartLiveResize)
	d.Method(selDidEndLive, "v@:@", windowDidEndLiveResize)
}

func windowShouldClose(c *ffi.CB) {
	if w := delegateWindow(objc.ID(c.Int(0))); w != nil {
		w.q.push(Event{Kind: EvClose, Time: Now()})
		// Asked outside an event (performClose: from code, the close
		// button's accessibility press), nothing else ends the pump's wait.
		WakeLoop()
	}
	c.RetBool(false)
}

func windowDidResize(c *ffi.CB) {
	if w := delegateWindow(objc.ID(c.Int(0))); w != nil {
		w.layoutChrome() // AppKit re-lays out the titlebar on resize
		w.fitBackdrop()
		if !w.live && w.handler != nil && objc.SendBool(w.win, selInLiveResize) {
			w.enterLiveResize() // the will-start notification was missed
		}
		w.backingChanged()
	}
}

func windowDidBecomeKey(c *ffi.CB) {
	if w := delegateWindow(objc.ID(c.Int(0))); w != nil {
		if nativeMenus.owner != w {
			w.installMenus(false) // the bar is the key window's before any later event
		}
		w.focus(true)
	}
}

func windowDidResignKey(c *ffi.CB) {
	if w := delegateWindow(objc.ID(c.Int(0))); w != nil {
		w.focus(false)
	}
}

// focus queues the window's EvFocus: it became the key window, or stopped
// being it.
func (w *Window) focus(key bool) {
	if !key && keyAssumed(w) { // a test's window stays key (DebugAssumeKey)
		return
	}
	if key {
		reg.lastKey = w
	}
	e := Event{Kind: EvFocus, Time: Now()}
	if key {
		e.Flags = FlagDown
	}
	w.q.push(e)
	w.focusMods(key, e.Time)
}

func windowDidChangeOcclusion(c *ffi.CB) {
	if w := delegateWindow(objc.ID(c.Int(0))); w != nil {
		e := Event{Kind: EvVisible, Time: Now()}
		w.visible, w.visibleT = 0, e.Time
		if uintptr(objc.Send(w.win, selOcclusionState))&2 != 0 { // NSWindowOcclusionStateVisible
			e.Flags = FlagDown
			w.visible = 1
		}
		w.q.push(e)
	}
}

func windowWillStartLiveResize(c *ffi.CB) {
	if w := delegateWindow(objc.ID(c.Int(0))); w != nil {
		w.enterLiveResize()
	}
}

func windowDidEndLiveResize(c *ffi.CB) {
	if w := delegateWindow(objc.ID(c.Int(0))); w != nil {
		w.exitLiveResize()
	}
}
