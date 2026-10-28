//go:build darwin

package platform

import "gonativeui/internal/objc"

// Window commands (D30, phase 6's F4): what an app asks of one of its windows
// at a safe point (app.Win). Frames are in the space of ScreenInfo: points,
// the origin at the top-left of the primary display, y down, and the whole
// window, title bar included.

var (
	selOrderOut          = objc.Sel("orderOut:")
	selSetFrameDisplay   = objc.Sel("setFrame:display:")
	selToggleFullScreen  = objc.Sel("toggleFullScreen:")
	selSetDocumentEdited = objc.Sel("setDocumentEdited:")
	selSetRepFilename    = objc.Sel("setRepresentedFilename:")
	selSetRepURL         = objc.Sel("setRepresentedURL:")
	selRequestAttention  = objc.Sel("requestUserAttention:")
	selCancelAttention   = objc.Sel("cancelUserAttentionRequest:")
	selDeminiaturize     = objc.Sel("deminiaturize:")
	selScreens           = objc.Sel("screens")
	selIsZoomed          = objc.Sel("isZoomed")
)

// primaryTop is the top edge of the primary display in AppKit's space (y up
// from its bottom edge), where the top-left space's y is 0.
func primaryTop() float64 {
	scr := objc.Send(objc.Send(objc.Cls("NSScreen"), selScreens), selFirstObject)
	if scr == 0 {
		return 0
	}
	f := objc.SendRect(scr, selFrame)
	return f.Y + f.H
}

// SetTitle sets the window's title (the title bar, the Window menu, Mission
// Control). Main thread.
func (w *Window) SetTitle(title string) {
	pool := objc.F.Call0(objc.PoolPush)
	objc.Send1(w.win, selSetTitle, uintptr(objc.Str(title)))
	objc.F.Call1(objc.PoolPop, pool)
}

// SetRepresentedFile makes the window stand for a file: its title bar shows
// the file's icon (the proxy icon), which can be dragged out and ⌘-clicked
// for the path menu. "" clears it. Main thread.
func (w *Window) SetRepresentedFile(path string) {
	pool := objc.F.Call0(objc.PoolPush)
	objc.Send1(w.win, selSetRepFilename, uintptr(objc.Str(path)))
	objc.F.Call1(objc.PoolPop, pool)
}

// SetRepresentedURL makes the window stand for a URL (a file: URL as a file
// does, any other as the web page's proxy icon). "" clears it, and so does a
// string that is not a URL. Main thread.
func (w *Window) SetRepresentedURL(url string) {
	pool := objc.F.Call0(objc.PoolPush)
	var u objc.ID
	if url != "" {
		u = objc.Send1(objc.Cls("NSURL"), selURLWithString, uintptr(objc.Str(url)))
	}
	objc.Send1(w.win, selSetRepURL, uintptr(u))
	objc.F.Call1(objc.PoolPop, pool)
}

// FrameRect is the window's frame: the whole window, title bar included, in
// points from the top-left of the primary display. Main thread.
func (w *Window) FrameRect() (x, y, width, height float64) {
	f := objc.SendRect(w.win, selFrame)
	return f.X, primaryTop() - (f.Y + f.H), f.W, f.H
}

// SetFrameRect moves and sizes the window to a frame in FrameRect's space.
// AppKit keeps it at least the minimum content size. Main thread, at a safe
// point (a resize draws through the frame handler).
func (w *Window) SetFrameRect(x, y, width, height float64) {
	if width <= 0 || height <= 0 {
		return
	}
	bottom := primaryTop() - y - height // before the call is assembled: it uses the same frame
	objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetFrameDisplay)).
		ArgRect(x, bottom, width, height).ArgBool(true).Call(objc.MsgSend)
}

// ToggleFullScreen enters full screen or leaves it, animated (toggleFullScreen:).
// With a custom titlebar the buttons are AppKit's own in full screen:
// SystemButtons is empty there, so the app drops their inset, and they are
// not moved to Options.TrafficLights until the window is back
// (lights_darwin.go). Main thread, at a safe point: the animation runs the
// frame handler.
func (w *Window) ToggleFullScreen() { objc.Send1(w.win, selToggleFullScreen, 0) }

// FullScreen reports whether the window is in full screen, or entering it.
func (w *Window) FullScreen() bool {
	return w.fullScreen || objc.Send(w.win, selStyleMask)&(1<<14) != 0 // NSWindowStyleMaskFullScreen
}

// Miniaturize puts the window in the Dock. Main thread.
func (w *Window) Miniaturize() { objc.Send1(w.win, selMiniaturize, 0) }

// Miniaturized reports whether the window is in the Dock.
func (w *Window) Miniaturized() bool { return objc.SendBool(w.win, selIsMiniaturized) }

// Zoom toggles the window between the user size and the size that fits its
// content on the screen (zoom:, the green button's option-click and the
// Window menu's Zoom). Main thread.
func (w *Window) Zoom() { objc.Send1(w.win, selZoom, 0) }

// Zoomed reports whether the window is zoomed (isZoomed).
func (w *Window) Zoomed() bool { return objc.SendBool(w.win, selIsZoomed) }

// Activate brings the window to the front, out of the Dock if it is there,
// makes it the key window and activates the app. Main thread.
func (w *Window) Activate() {
	if objc.SendBool(w.win, selIsMiniaturized) {
		objc.Send1(w.win, selDeminiaturize, 0)
	}
	w.Show()
}

// Hide takes the window off the screen without closing it (orderOut:); Show
// or Activate bring it back. Main thread.
func (w *Window) Hide() { objc.Send1(w.win, selOrderOut, 0) }

// Visible reports whether the window is on screen (ordered in, possibly
// covered).
func (w *Window) Visible() bool { return objc.SendBool(w.win, selIsVisible) }

// SetDocumentEdited marks the window's document as having unsaved changes
// (the dot in the close button). Main thread.
func (w *Window) SetDocumentEdited(edited bool) {
	objc.Send1(w.win, selSetDocumentEdited, b2u(edited))
}

// RequestAttention bounces the app's Dock icon, once (informational) or until
// the app is activated (critical), and returns the request's id for
// CancelAttention; 0 when the app is active (nothing to ask). Main thread.
func RequestAttention(critical bool) int {
	if !appObj.started {
		return 0
	}
	typ := uintptr(10) // NSInformationalRequest
	if critical {
		typ = 0 // NSCriticalRequest
	}
	return int(objc.Send1(appObj.app, selRequestAttention, typ))
}

// CancelAttention ends a request RequestAttention made. Main thread.
func CancelAttention(id int) {
	if appObj.started && id > 0 {
		objc.Send1(appObj.app, selCancelAttention, uintptr(id))
	}
}
