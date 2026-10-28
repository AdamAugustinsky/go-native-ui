package platform

import (
	"unsafe"

	"gonativeui/internal/win32"
)

// The drawn titlebar on Windows (the phase 8 plan's W9): a window opened with
// Options.Titlebar keeps WS_OVERLAPPEDWINDOW (its resize borders, Aero Snap,
// the minimise and maximise animations, the system menu) but has no caption
// of the system's: WM_NCCALCSIZE gives the client area the window's top, so
// the app draws its band there (the Rex toolbar), with the minimise, maximise
// and close buttons in the rectangle SystemButtons reports (46 × the band at
// 100 %, Windows 11's caption metrics; ui.Decorations.Caption tells the app to
// draw them). WM_NCHITTEST tells Windows what each point is: the three
// buttons as HTMINBUTTON, HTMAXBUTTON and HTCLOSE (so Windows 11's snap
// layouts open over our maximise button), the top resize border inside the
// client, the app's drag map as HTCAPTION (a press moves the window, a double
// click maximises it, a right click opens the system menu), the rest as the
// client. The pointer over the buttons arrives as WM_NC* messages, which are
// turned into the client's pointer events, so the app's buttons hover, press
// and click as any of its boxes; the click asks the window for the action
// (ui.Context.RequestWindowAction: Minimize, ToggleMaximize, RequestClose).
//
// A maximized window extends past its monitor by the resize border on every
// side; the client's top is moved down by as much, so the band stays on the
// screen. In full screen (ToggleFullScreen) there are no buttons.

const (
	wmNcCalcSize     = 0x0083
	wmNcHitTest      = 0x0084
	wmNcMouseMove    = 0x00A0
	wmNcLButtonDown  = 0x00A1
	wmNcLButtonUp    = 0x00A2
	wmNcLButtonDbl   = 0x00A3
	wmNcMouseLeave   = 0x02A2
	tmeNonClient     = 0x00000010
	smCySizeFrame    = 33
	smCxPaddedBorder = 92
)

var (
	pGetSystemMetrics       = win32.Proc(user32, "GetSystemMetrics")
	pGetSystemMetricsForDpi = win32.Proc(user32, "GetSystemMetricsForDpi") // Windows 10 1607+
)

// chromeState is a drawn titlebar's state (Options.Titlebar).
type chromeState struct {
	on         bool
	bandPt     float32 // the band's height in logical units (Options.Titlebar, SetTitlebar)
	ncTracking bool    // TrackMouseEvent(TME_LEAVE|TME_NONCLIENT) armed
	buttons    [3][4]float32
	tme        trackMouseEvent
}

// frameMsg handles the messages of a panel or a drawn titlebar; done false
// leaves the message to wndProc.
func (w *Window) frameMsg(hwnd, m, wp, lp uintptr) (r uintptr, done bool) {
	if w.panel != nil {
		return w.panelMsg(hwnd, m, wp, lp)
	}
	return w.chromeMsg(hwnd, m, wp, lp)
}

// chromeSetup recomputes the new window's frame without the caption.
func (w *Window) chromeSetup() {
	win32.Call(pSetWindowPos, w.hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged)
}

// resizeFrame is the depth of the resize border in pixels at the window's
// DPI (SM_CYSIZEFRAME and SM_CXPADDEDBORDER): what a maximized window
// extends past its monitor, and the top border's depth inside the client.
func (w *Window) resizeFrame() int32 {
	if pGetSystemMetricsForDpi != 0 {
		dpi := uintptr(w.scale*defaultDPI + 0.5)
		if pGetDpiForWindow != 0 && w.hwnd != 0 {
			if d := win32.Call(pGetDpiForWindow, w.hwnd); d != 0 {
				dpi = d
			}
		}
		return int32(win32.Call(pGetSystemMetricsForDpi, smCySizeFrame, dpi)) + int32(win32.Call(pGetSystemMetricsForDpi, smCxPaddedBorder, dpi))
	}
	return int32(win32.Call(pGetSystemMetrics, smCySizeFrame)) + int32(win32.Call(pGetSystemMetrics, smCxPaddedBorder))
}

// bandPx is the band's height in pixels.
func (w *Window) bandPx() float32 {
	return float32(int32(w.chrome.bandPt*w.scale + 0.5))
}

// captionButtons fills the chrome's button rectangles for the current size
// and returns them, nil when the window shows none (full screen).
func (w *Window) captionButtons() *[3][4]float32 {
	if !w.DrawnCaption() {
		return nil
	}
	w.chrome.buttons = captionRects(float32(w.w), w.bandPx(), w.scale)
	return &w.chrome.buttons
}

// isCaptionButton reports whether a hit-test code is one of the buttons.
func isCaptionButton(ht uintptr) bool { return ht == htMinButton || ht == htMaxButton || ht == htClose }

// chromeMsg handles a drawn titlebar's messages.
func (w *Window) chromeMsg(hwnd, m, wp, lp uintptr) (r uintptr, done bool) {
	switch m {
	case wmNcCalcSize:
		// lParam is a RECT, or NCCALCSIZE_PARAMS whose first member is one:
		// the system's borders on the left, right and bottom, none at the top.
		rc := (*rect)(win32.Ptr(lp))
		if rc == nil {
			return 0, false
		}
		top := rc.top
		r = defProc(hwnd, m, wp, lp)
		rc.top = top
		if !w.fullScreen && win32.Bool(win32.Call(pIsZoomed, hwnd)) {
			rc.top += w.resizeFrame()
		}
		return r, true

	case wmNcHitTest:
		if w.fullScreen {
			return 0, false
		}
		if r = defProc(hwnd, m, wp, lp); r != htClientArea {
			return r, true // the left, right and bottom borders
		}
		x, y := w.screenToClient(hwnd, lp)
		return chromeHit(x, y, float32(w.w), float32(w.resizeFrame()), w.Zoomed(), w.captionButtons(), &w.drags), true

	case wmNcMouseMove:
		// Over the band (caption, buttons, the top border): the app's
		// pointer, so that its buttons hover. Windows goes on with it (the
		// snap layouts' hover over maximise).
		if wp == htCaption || isCaptionButton(wp) {
			if !w.chrome.ncTracking {
				w.chrome.tme = trackMouseEvent{size: uint32(unsafe.Sizeof(w.chrome.tme)), flags: tmeLeave | tmeNonClient, hwndTrack: hwnd}
				w.chrome.ncTracking = win32.Bool(win32.Call(pTrackMouseEvent, uintptr(unsafe.Pointer(&w.chrome.tme))))
			}
			w.ptrX, w.ptrY = w.screenToClient(hwnd, lp)
			w.q.push(Event{Kind: EvPointerMove, X: w.ptrX, Y: w.ptrY, Time: Now()})
		}
		return 0, false

	case wmNcMouseLeave:
		w.chrome.ncTracking = false
		if !w.tracking && w.buttons == 0 {
			w.q.push(Event{Kind: EvPointerLeave, Time: Now()})
		}
		return 0, false

	case wmNcLButtonDown, wmNcLButtonDbl, wmNcLButtonUp:
		// A press on a button is the app's (DefWindowProc would track it
		// and draw the system's button): the client's press, captured, so
		// its release arrives as WM_LBUTTONUP and the app's button clicks.
		if !isCaptionButton(wp) {
			return 0, false
		}
		x, y := w.screenToClient(hwnd, lp)
		w.button(hwnd, ButtonLeft, m != wmNcLButtonUp, uintptr(uint16(int16(x)))|uintptr(uint16(int16(y)))<<16)
		return 0, true
	}
	return 0, false
}

// DrawnCaption reports whether the app draws the window's caption buttons
// (a drawn titlebar, Options.Titlebar, out of full screen): ui.Decorations.
// Caption.
func (w *Window) DrawnCaption() bool { return w.chrome.on && !w.fullScreen }

// SystemButtons is where the caption buttons of a drawn titlebar are, in
// logical units from the client's top-left: the minimise, maximise and close
// buttons at the right end of the band, CaptionButtonW each and the band's
// height (the app draws them there, and Windows hit-tests them there). Empty
// for a window with the system's caption.
func (w *Window) SystemButtons() [4]float32 {
	b := w.captionButtons()
	if b == nil || w.scale <= 0 {
		return [4]float32{}
	}
	s := w.scale
	return [4]float32{b[0][0] / s, 0, b[2][2] / s, b[0][3] / s}
}

// SetTitlebar changes the drawn titlebar's height (logical units; the app's
// zoom, app.Config.Titlebar times it). The button centre has no meaning on
// Windows. Main thread.
func (w *Window) SetTitlebar(height float32, lights [2]float32) {
	if w.chrome.on && height > 0 {
		w.chrome.bandPt = height
	}
}

// ClientDecorations is false: Windows draws the frame (the app draws at
// most the band of a drawn titlebar, DrawnCaption).
func (w *Window) ClientDecorations() bool { return false }

// Maximized reports whether the window is maximized.
func (w *Window) Maximized() bool { return w.Zoomed() }

// Minimize minimizes the window to the taskbar (a drawn minimise button).
func (w *Window) Minimize() { w.Miniaturize() }

// ToggleMaximize maximizes the window or restores it (a drawn maximise
// button).
func (w *Window) ToggleMaximize() { w.Zoom() }

// RequestClose asks the window to close, as its close button does: WM_CLOSE,
// which arrives as EvClose.
func (w *Window) RequestClose() {
	if w.hwnd != 0 {
		win32.Call(pPostMessageW, w.hwnd, wmClose, 0, 0)
	}
}
