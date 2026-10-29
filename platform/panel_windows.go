package platform

import (
	"unsafe"

	"gonativeui/internal/win32"
)

// Panel windows on Windows (the phase 8 plan's W2, D69). A panel is a
// WS_POPUP window with WS_EX_TOOLWINDOW, so it has no frame and no caption
// and is in neither the taskbar nor Alt+Tab; WS_EX_TOPMOST keeps a Floating
// (or StatusBar) panel above ordinary windows, and WS_EX_NOACTIVATE keeps a
// NonActivating one (a recording bar) from ever taking the keyboard, even
// when clicked. Its drag regions move it (WM_NCHITTEST answers HTCAPTION
// there). DWM gives it its corners (DWMWA_WINDOW_CORNER_PREFERENCE: round,
// or small round for a Radius up to 6, on Windows 11; square on Windows 10)
// and, for Shadow, its shadow (non-client rendering on and the frame
// extended 1 px into the client). ExcludeFromCapture is
// SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE), WDA_MONITOR (a black
// rectangle in captures) before Windows 10 2004. The swap chain stays opaque.
//
// Opening: the panel is centred on the monitor under the pointer (Config.Place
// moves it) and shown without activation, cloaked when Options.Hidden (as
// package app opens it): no panel takes the foreground before Show.
// Show uncloaks it and brings it to the top; a panel that takes the keyboard
// (not NonActivating) is made the foreground window, which Windows allows to
// the app that received the input that led to it (a hotkey's press, a tray
// click). HideOnDeactivate hides the panel when it stops being the active
// window (WM_ACTIVATE, inactive), to any window, the app's own included.
// AllSpaces needs nothing: a tool window belongs to no virtual desktop and
// shows on the one the user is on, as every panel does.

const panelsSupported = true

const (
	wsExTopmost    = 0x00000008
	wsExNoActivate = 0x08000000
	gwlExStyle     = -20

	wmMouseActivate = 0x0021
	maNoActivate    = 3

	swShowNoActivate = 4
	swShowNA         = 8
	hwndTopmost      = ^uintptr(0) // HWND_TOPMOST, (HWND)-1

	wdaMonitor            = 1
	wdaExcludeFromCapture = 0x11

	dwmaNCRenderingEnabled = 1  // DWMWA_NCRENDERING_ENABLED (get)
	dwmaNCRenderingPolicy  = 2  // DWMWA_NCRENDERING_POLICY (set)
	dwmaCornerPreference   = 33 // DWMWA_WINDOW_CORNER_PREFERENCE, Windows 11
	dwmncrpEnabled         = 2
	dwmwcpDoNotRound       = 1
	dwmwcpRound            = 2
	dwmwcpRoundSmall       = 3

	monitorDefaultToNearest = 2
)

// margins is MARGINS (DwmExtendFrameIntoClientArea).
type margins struct{ left, right, top, bottom int32 }

var (
	pSetWindowDisplayAffinity = win32.Proc(user32, "SetWindowDisplayAffinity")
	pGetWindowDisplayAffinity = win32.Proc(user32, "GetWindowDisplayAffinity")
	pMonitorFromPoint         = win32.Proc(user32, "MonitorFromPoint")
	pDwmGetWindowAttrW        = win32.Proc(dwmapi, "DwmGetWindowAttribute")
	pDwmExtendFrame           = win32.Proc(dwmapi, "DwmExtendFrameIntoClientArea")
)

// panelState is a panel's own state, kept in the Window (syscall storage
// included: no pointer to a stack local reaches the OS).
type panelState struct {
	corner    uint32 // the corner preference asked of DWM (0: none)
	cornerOK  bool   // DWM took it (Windows 11)
	policy    uint32 // DWMWA_NCRENDERING_POLICY's value
	read      uint32 // DwmGetWindowAttribute's and GetWindowDisplayAffinity's out-parameter
	marg      margins
	affinity  uint32 // the display affinity set (0: none)
	affinOK   bool
	hidesSeen int // HideOnDeactivate hid the panel (tests)
}

// frameKind sets the window's styles from the options before it is created:
// a panel's, or a drawn titlebar's (chrome_windows.go).
func (w *Window) frameKind(o *Options) {
	w.exStyle = wsExNoRedirectionBitmap
	if o.Panel != nil {
		p := *o.Panel
		w.panel = &p
		w.style = wsPopup
		w.exStyle |= wsExToolWindow
		if p.Floating || p.StatusBar {
			w.exStyle |= wsExTopmost
		}
		if p.NonActivating {
			w.exStyle |= wsExNoActivate
		}
		return
	}
	if o.Titlebar > 0 {
		w.chrome = chromeState{on: true, bandPt: o.Titlebar}
	}
}

// showCmd is the ShowWindow command Open uses: a panel is shown without
// being activated (cloaked, when Hidden, until Show).
func (w *Window) showCmd() uintptr {
	if w.panel != nil {
		return swShowNoActivate
	}
	return swShowNormal
}

// frameSetup applies what needs the window: a panel's DWM attributes,
// capture exclusion and place; a drawn titlebar's frame (chrome_windows.go).
func (w *Window) frameSetup() {
	if w.chrome.on {
		w.chromeSetup()
		return
	}
	p := w.panel
	if p == nil {
		return
	}
	ps := &w.ps
	if pDwmSetWindowAttrW != 0 {
		ps.corner = dwmwcpDoNotRound
		switch {
		case p.Radius > 6:
			ps.corner = dwmwcpRound
		case p.Radius > 0:
			ps.corner = dwmwcpRoundSmall
		}
		ps.cornerOK = win32.HR(win32.Call(pDwmSetWindowAttrW, w.hwnd, dwmaCornerPreference, uintptr(unsafe.Pointer(&ps.corner)), 4)) >= 0
		if p.Shadow {
			ps.policy = dwmncrpEnabled
			win32.Call(pDwmSetWindowAttrW, w.hwnd, dwmaNCRenderingPolicy, uintptr(unsafe.Pointer(&ps.policy)), 4)
			if pDwmExtendFrame != 0 {
				ps.marg = margins{1, 1, 1, 1}
				win32.Call(pDwmExtendFrame, w.hwnd, uintptr(unsafe.Pointer(&ps.marg)))
			}
		}
	}
	if p.ExcludeFromCapture && pSetWindowDisplayAffinity != 0 {
		ps.affinity = wdaExcludeFromCapture
		if ps.affinOK = win32.Bool(win32.Call(pSetWindowDisplayAffinity, w.hwnd, wdaExcludeFromCapture)); !ps.affinOK {
			ps.affinity = wdaMonitor // before Windows 10 2004
			ps.affinOK = win32.Bool(win32.Call(pSetWindowDisplayAffinity, w.hwnd, wdaMonitor))
		}
	}
	w.centrePanel()
}

// centrePanel centres the panel on the work area of the monitor under the
// pointer (a palette opens where the user is looking).
func (w *Window) centrePanel() {
	if pMonitorFromPoint == 0 || !win32.Bool(win32.Call(pGetCursorPos, uintptr(unsafe.Pointer(&w.pt)))) {
		return
	}
	mon := win32.Call(pMonitorFromPoint, uintptr(uint32(w.pt.x))|uintptr(uint32(w.pt.y))<<32, monitorDefaultToNearest)
	w.mi.size = uint32(unsafe.Sizeof(w.mi))
	if mon == 0 || !win32.Bool(win32.Call(pGetMonitorInfoW, mon, uintptr(unsafe.Pointer(&w.mi)))) ||
		!win32.Bool(win32.Call(pGetWindowRect, w.hwnd, uintptr(unsafe.Pointer(&w.rc)))) {
		return
	}
	a := w.mi.work
	pw, ph := w.rc.right-w.rc.left, w.rc.bottom-w.rc.top
	x, y := a.left+(a.right-a.left-pw)/2, a.top+(a.bottom-a.top-ph)/2
	win32.Call(pSetWindowPos, w.hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

// showPanel is Show for a panel: uncloaked, shown (without activation for a
// NonActivating one), brought to the top of its band (topmost when
// Floating), and, unless NonActivating, made the foreground window.
func (w *Window) showPanel() {
	if w.hwnd == 0 {
		return
	}
	if w.cloaked {
		w.cloaked = false
		w.setCloak(false)
	}
	na := w.panel.NonActivating
	if w.hidden {
		w.hidden = false
		cmd := uintptr(swShow)
		if na {
			cmd = swShowNA
		}
		win32.Call(pShowWindow, w.hwnd, cmd)
	}
	after := uintptr(hwndTop)
	if w.exStyle&wsExTopmost != 0 {
		after = hwndTopmost
	}
	flags := uintptr(swpNoMove | swpNoSize | swpNoOwnerZOrder)
	if na {
		flags |= swpNoActivate
	}
	win32.Call(pSetWindowPos, w.hwnd, after, 0, 0, 0, 0, flags)
	if !na {
		win32.Call(pSetForegroundWindow, w.hwnd)
	}
}

// panelMsg handles a panel's own messages; done false leaves the message
// to wndProc.
func (w *Window) panelMsg(hwnd, m, wp, lp uintptr) (r uintptr, done bool) {
	switch m {
	case wmNcHitTest:
		if r = defProc(hwnd, m, wp, lp); r != htClientArea {
			return r, true
		}
		x, y := w.screenToClient(hwnd, lp)
		return panelHit(x, y, &w.drags), true
	case wmMouseActivate:
		if w.panel.NonActivating {
			return maNoActivate, true
		}
	case wmActivate:
		if w.panel.HideOnDeactivate && wp&0xFFFF == waInactive && !w.opening && !w.hidden {
			w.ps.hidesSeen++
			w.Hide() // WM_SHOWWINDOW reports it (EvVisible)
		}
	}
	return 0, false
}

// screenToClient converts the screen point packed in a mouse message's
// lParam to client pixels.
func (w *Window) screenToClient(hwnd, lp uintptr) (x, y float32) {
	w.pt = point{loword(lp), hiword(lp)}
	win32.Call(pScreenToClient, hwnd, uintptr(unsafe.Pointer(&w.pt)))
	return float32(w.pt.x), float32(w.pt.y)
}

// IsPanel reports whether the window is a panel (Options.Panel).
func (w *Window) IsPanel() bool { return w.panel != nil }

// KeyWindow reports whether the window is the foreground window (tests,
// tools).
func (w *Window) KeyWindow() bool {
	return w.hwnd != 0 && win32.Call(pGetForegroundWindow) == w.hwnd
}

// PanelState is what Windows reports of a panel's configuration, read back
// from the window (tests, tools).
type PanelState struct {
	Style, ExStyle  uint32 // GWL_STYLE and GWL_EXSTYLE
	DisplayAffinity uint32 // GetWindowDisplayAffinity: WDA_EXCLUDEFROMCAPTURE (0x11), WDA_MONITOR (1) or WDA_NONE
	NCRendering     bool   // DWMWA_NCRENDERING_ENABLED: DWM draws its shadow
	Corner          uint32 // the DWMWA_WINDOW_CORNER_PREFERENCE asked for (DWM has no getter)
	CornerTaken     bool   // DWM accepted it (Windows 11)
	HiddenByDeact   int    // times HideOnDeactivate hid the panel
}

// DebugPanelState reads the window's panel configuration back from Windows.
// Main thread.
func (w *Window) DebugPanelState() PanelState {
	var s PanelState
	if w.hwnd == 0 {
		return s
	}
	ps := &w.ps
	s.Style = uint32(win32.Call(pGetWindowLong, w.hwnd, uintptr(gwlStyleArg)))
	s.ExStyle = uint32(win32.Call(pGetWindowLong, w.hwnd, gwlExStyleArg))
	if pGetWindowDisplayAffinity != 0 {
		ps.read = 0
		if win32.Bool(win32.Call(pGetWindowDisplayAffinity, w.hwnd, uintptr(unsafe.Pointer(&ps.read)))) {
			s.DisplayAffinity = ps.read
		}
	}
	if pDwmGetWindowAttrW != 0 {
		ps.read = 0
		if win32.HR(win32.Call(pDwmGetWindowAttrW, w.hwnd, dwmaNCRenderingEnabled, uintptr(unsafe.Pointer(&ps.read)), 4)) >= 0 {
			s.NCRendering = ps.read != 0
		}
	}
	s.Corner, s.CornerTaken, s.HiddenByDeact = ps.corner, ps.cornerOK, ps.hidesSeen
	return s
}

// gwlExStyleArg is GWL_EXSTYLE as the uintptr argument (a negative index).
var gwlExStyleArg = func() uintptr { i := int32(gwlExStyle); return uintptr(i) }()
