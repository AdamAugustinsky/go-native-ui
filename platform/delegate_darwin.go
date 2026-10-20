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
	if w := theWindow.Load(); w != nil {
		w.q.push(Event{Kind: EvClose, Time: Now()})
	}
	c.RetBool(false)
}

func windowDidResize(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		w.layoutChrome() // AppKit re-lays out the titlebar on resize
		w.fitBackdrop()
		if !w.live && w.handler != nil && objc.SendBool(w.win, selInLiveResize) {
			w.enterLiveResize() // the will-start notification was missed
		}
		w.backingChanged()
	}
}

func windowDidBecomeKey(*ffi.CB) { windowFocus(true) }
func windowDidResignKey(*ffi.CB) { windowFocus(false) }

func windowFocus(key bool) {
	if w := theWindow.Load(); w != nil {
		e := Event{Kind: EvFocus, Time: Now()}
		if key {
			e.Flags = FlagDown
		}
		w.q.push(e)
		w.focusMods(key, e.Time)
	}
}

func windowDidChangeOcclusion(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		e := Event{Kind: EvVisible, Time: Now()}
		w.visible, w.visibleT = 0, e.Time
		if uintptr(objc.Send(w.win, selOcclusionState))&2 != 0 { // NSWindowOcclusionStateVisible
			e.Flags = FlagDown
			w.visible = 1
		}
		w.q.push(e)
	}
}

func windowWillStartLiveResize(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		w.enterLiveResize()
	}
}

func windowDidEndLiveResize(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		w.exitLiveResize()
	}
}
