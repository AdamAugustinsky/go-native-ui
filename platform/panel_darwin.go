//go:build darwin

package platform

import (
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Panel windows (the phase 8 plan's W2, D69). A panel is a GNUIPanel, an
// NSPanel subclass with GNUIWindow's overrides (host_darwin.go, winClass),
// made by Open for Options.Panel. It is borderless and non-activating: a
// click in it does not activate the app, and it is never in the Dock, the
// Window menu or the window cycle. Its level, Spaces, capture and
// deactivation follow the Panel's fields (configurePanel). It is
// transparent: the app clears to alpha 0 and draws its shape, or a backdrop
// (Options.Backdrop) gives it glass with the Panel's corner radius.
//
// Showing (showPanel): a NonActivating panel (a recording bar) is ordered
// front without becoming key, so the window the user is typing in keeps the
// keyboard, and it cannot become key (canBecomeKeyWindow NO), unless it is
// KeyWhileInactive: it becomes key then, the app left inactive. Any other
// panel (a palette) activates the app and becomes key, as a3-kb's palette
// does; HideOnDeactivate is then AppKit's hidesOnDeactivate, which needs
// the app to have been active. Hide is orderOut:, as for any window.

// panelStyle is NSWindowStyleMaskBorderless (0) |
// NSWindowStyleMaskNonactivatingPanel.
const panelStyle = 1 << 7

// Window levels and collection behaviours (NSWindow.h).
const (
	levelNormal   = 0  // NSNormalWindowLevel
	levelFloating = 3  // NSFloatingWindowLevel
	levelStatus   = 25 // NSStatusWindowLevel

	cbCanJoinAllSpaces    = 1 << 0
	cbMoveToActiveSpace   = 1 << 1
	cbStationary          = 1 << 4
	cbIgnoresCycle        = 1 << 6
	cbFullScreenAuxiliary = 1 << 8
)

var (
	selCanBecomeKey        = objc.SelZ("canBecomeKeyWindow\x00")
	selSetLevel            = objc.SelZ("setLevel:\x00")
	selLevel               = objc.SelZ("level\x00")
	selSetCollection       = objc.SelZ("setCollectionBehavior:\x00")
	selCollection          = objc.SelZ("collectionBehavior\x00")
	selSetSharingType      = objc.SelZ("setSharingType:\x00")
	selSharingType         = objc.SelZ("sharingType\x00")
	selSetHidesOnDeact     = objc.SelZ("setHidesOnDeactivate:\x00")
	selHidesOnDeact        = objc.SelZ("hidesOnDeactivate\x00")
	selSetHasShadow        = objc.SelZ("setHasShadow:\x00")
	selHasShadow           = objc.SelZ("hasShadow\x00")
	selInvalidateShadow    = objc.SelZ("invalidateShadow\x00")
	selSetExcludedFromMenu = objc.SelZ("setExcludedFromWindowsMenu:\x00")
	selExcludedFromMenu    = objc.SelZ("isExcludedFromWindowsMenu\x00")
	selIsOpaque            = objc.SelZ("isOpaque\x00")
	selClearColor          = objc.SelZ("clearColor\x00")
	selActivateIgnoring    = objc.SelZ("activateIgnoringOtherApps:\x00")
	selSetMasksToBounds    = objc.SelZ("setMasksToBounds:\x00")
)

// registerPanel adds GNUIPanel's own methods (host_darwin.go defines the
// class with GNUIWindow's).
func registerPanel(d *objc.ClassDef) {
	d.Method(selCanBecomeKey, "c@:", panelCanBecomeKey)
	d.Method(selCanBecomeMain, "c@:", func(c *ffi.CB) { c.RetBool(false) })
}

// panelCanBecomeKey is GNUIPanel's canBecomeKeyWindow: YES for an open panel
// that takes the keyboard, NO for a NonActivating one unless
// KeyWhileInactive (and a closed one).
func panelCanBecomeKey(c *ffi.CB) {
	w := panelKind.window(objc.ID(c.Int(0)))
	c.RetBool(w != nil && (!w.panel.NonActivating || w.panel.KeyWhileInactive))
}

// configurePanel applies the Panel's fields to the new GNUIPanel (Open).
func (w *Window) configurePanel() {
	p := w.panel
	level := uintptr(levelNormal)
	switch {
	case p.StatusBar:
		level = levelStatus
	case p.Floating:
		level = levelFloating
	}
	objc.Send1(w.win, selSetLevel, level)
	cb := uintptr(cbMoveToActiveSpace) // shown on the Space the user is on
	if p.AllSpaces {
		cb = cbCanJoinAllSpaces | cbStationary
	}
	objc.Send1(w.win, selSetCollection, cb|cbFullScreenAuxiliary|cbIgnoresCycle)
	if p.ExcludeFromCapture {
		objc.Send1(w.win, selSetSharingType, 0) // NSWindowSharingNone
	}
	objc.Send1(w.win, selSetHidesOnDeact, b2u(p.HideOnDeactivate)) // NSPanel's default is YES
	objc.Send1(w.win, selSetHasShadow, b2u(p.Shadow))
	objc.Send1(w.win, selSetExcludedFromMenu, 1)
	objc.Send1(w.win, selSetOpaqueWin, 0)
	objc.Send1(w.win, selSetBGColor, uintptr(objc.Send(objc.Cls("NSColor"), selClearColor)))
}

// showPanel is Show for a panel.
func (w *Window) showPanel() {
	w.attachPanel() // a child panel: placed and ordered over its parent
	switch {
	case w.panel.NonActivating && w.panel.KeyWhileInactive:
		// Key with the app left as it is: the panel's style
		// (NSWindowStyleMaskNonactivatingPanel) lets it become key while
		// the app stays inactive, so no other window of the app comes
		// forward.
		objc.Send1(w.win, selMakeKeyFront, 0)
		objc.Send(w.win, selOrderFrontRegardless)
		if !objc.SendBool(w.win, selIsKeyWindow) {
			w.focus(false)
		}
	case w.panel.NonActivating:
		objc.Send(w.win, selOrderFrontRegardless)
	default:
		// The app activates first, so that the panel becomes the key window
		// of an active app (hidesOnDeactivate needs one). From a hotkey's
		// press the system lets it take the front.
		objc.Send1(w.app, selActivateIgnoring, 1)
		objc.Send1(w.win, selMakeKeyFront, 0)
		objc.Send(w.win, selOrderFrontRegardless)
		if !objc.SendBool(w.win, selIsKeyWindow) {
			w.focus(false) // windowDidBecomeKey: reports it otherwise
		}
	}
	if w.panel.Shadow {
		objc.Send(w.win, selInvalidateShadow) // the shape is the content's: recompute it
	}
	if w.link != 0 {
		w.setRateRange(w.link)
	}
}

// fitPanelBackdrop gives a panel's backdrop the Panel's corner radius: the
// glass's cornerRadius (public on NSGlassEffectView), or the effect view's
// layer, masked, for the pre-glass material. Once: a panel's radius does not
// change.
func (w *Window) fitPanelBackdrop() {
	r := max(float64(w.panel.Radius), 0)
	if r == w.glassRadius {
		return
	}
	w.glassRadius = r
	if w.backdrop != BackdropWindow {
		objc.F.Reset().Arg(uintptr(w.effect)).Arg(uintptr(selSetCornerRadius)).ArgF64(r).Call(objc.MsgSend)
		return
	}
	objc.Send1(w.effect, selSetWantsLayerV, 1)
	if l := objc.Send(w.effect, selLayerOf); l != 0 {
		objc.F.Reset().Arg(uintptr(l)).Arg(uintptr(selSetCornerRadius)).ArgF64(r).Call(objc.MsgSend)
		objc.Send1(l, selSetMasksToBounds, 1)
	}
}

// IsPanel reports whether the window is a panel (Options.Panel).
func (w *Window) IsPanel() bool { return w.panel != nil }

// PanelState is what AppKit reports of a panel's configuration, read back
// from the NSWindow (tests, tools).
type PanelState struct {
	StyleMask          uint64 // styleMask
	CollectionBehavior uint64 // collectionBehavior
	Level              int64  // level
	SharingType        int64  // sharingType (0 none, 1 read-only)
	HidesOnDeactivate  bool   // hidesOnDeactivate
	HasShadow          bool   // hasShadow
	Opaque             bool   // isOpaque
	ExcludedFromMenu   bool   // isExcludedFromWindowsMenu
	CanBecomeKey       bool   // canBecomeKeyWindow
	CanBecomeMain      bool   // canBecomeMainWindow
	IsNSPanel          bool   // the window is an NSPanel
}

// DebugPanelState reads the window's panel configuration back from AppKit.
// Main thread.
func (w *Window) DebugPanelState() PanelState {
	return PanelState{
		StyleMask:          uint64(objc.Send(w.win, selStyleMask)),
		CollectionBehavior: uint64(objc.Send(w.win, selCollection)),
		Level:              int64(objc.Send(w.win, selLevel)),
		SharingType:        int64(objc.Send(w.win, selSharingType)),
		HidesOnDeactivate:  objc.SendBool(w.win, selHidesOnDeact),
		HasShadow:          objc.SendBool(w.win, selHasShadow),
		Opaque:             objc.SendBool(w.win, selIsOpaque),
		ExcludedFromMenu:   objc.SendBool(w.win, selExcludedFromMenu),
		CanBecomeKey:       objc.SendBool(w.win, selCanBecomeKey),
		CanBecomeMain:      objc.SendBool(w.win, selCanBecomeMain),
		IsNSPanel:          objc.SendBool1(w.win, selIsKindOfClass, uintptr(objc.Cls("NSPanel"))),
	}
}

// KeyWindow reports whether the window is the key window (tests, tools).
func (w *Window) KeyWindow() bool { return objc.SendBool(w.win, selIsKeyWindow) }
