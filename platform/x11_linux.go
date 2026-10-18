//go:build linux

package platform

import (
	"errors"
	"strconv"
	"strings"
	"unsafe"

	"gonativeui/internal/ffi"
)

// X11 backend (libX11.so.6 through dlopen). The window is created in
// physical pixels (logical size * Xft.dpi/96).
//
// XEvent decoding. XEvent is a 192-byte union (24 longs); w.x.xev holds it.
// Offsets are for LP64 (Linux amd64/arm64), derived from the field order in
// <X11/Xlib.h> (int=4, long/pointer/Window/Atom/Time=8, 8-byte alignment) and
// cross-checked with a ctypes layout of the same structs:
//
//	XAnyEvent:           type int @0, serial ulong @8, send_event Bool @16,
//	                     display @24, window @32
//	XKeyEvent:           ...window @32, root @40, subwindow @48, time @56,
//	                     x @64, y @68, x_root @72, y_root @76, state uint @80,
//	                     keycode uint @84
//	XButtonEvent:        same as XKeyEvent, button uint @84
//	XMotionEvent:        same, is_hint char @84
//	XCrossingEvent:      same up to y_root @76, mode int @80, detail int @84
//	XFocusChangeEvent:   window @32, mode int @40, detail int @44
//	XVisibilityEvent:    window @32, state int @40
//	XConfigureEvent:     event @32, window @40, x @48, y @52, width @56,
//	                     height @60, border_width @64, above @72
//	XClientMessageEvent: window @32, message_type Atom @40, format int @48,
//	                     data.l[0] @56
const (
	xoType      = 0
	xoState     = 80
	xoKeycode   = 84
	xoButton    = 84
	xoX         = 64
	xoY         = 68
	xoMode      = 80 // XCrossingEvent
	xoFocusMode = 40 // XFocusChangeEvent
	xoVisState  = 40
	xoCfgW      = 56
	xoCfgH      = 60
	xoMsgType   = 40
	xoMsgFormat = 48
	xoMsgData0  = 56
	xevBytes    = 192
)

// Event types (X.h) and masks.
const (
	xKeyPress         = 2
	xKeyRelease       = 3
	xButtonPress      = 4
	xButtonRelease    = 5
	xMotionNotify     = 6
	xEnterNotify      = 7
	xLeaveNotify      = 8
	xFocusIn          = 9
	xFocusOut         = 10
	xVisibilityNotify = 15
	xUnmapNotify      = 18
	xMapNotify        = 19
	xConfigureNotify  = 22
	xClientMessage    = 33

	xKeyPressMask         = 1 << 0
	xKeyReleaseMask       = 1 << 1
	xButtonPressMask      = 1 << 2
	xButtonReleaseMask    = 1 << 3
	xLeaveWindowMask      = 1 << 5
	xEnterWindowMask      = 1 << 4
	xPointerMotionMask    = 1 << 6
	xExposureMask         = 1 << 15
	xVisibilityChangeMask = 1 << 16
	xStructureNotifyMask  = 1 << 17
	xFocusChangeMask      = 1 << 21

	xShiftMask   = 1 << 0
	xControlMask = 1 << 2
	xMod1Mask    = 1 << 3
	xMod4Mask    = 1 << 6

	xVisibilityFullyObscured = 2
	scrollStep               = 3 * 16 // pixels per wheel click at scale 1
)

var xf struct {
	initThreads, openDisplay, closeDisplay, defaultScreen, rootWindow, createSimpleWindow uintptr
	selectInput, internAtom, setWMProtocols, storeName, mapWindow, flush                  uintptr
	setWMNormalHints                                                                      uintptr
	pending, nextEvent, connectionNumber, resourceManagerString                           uintptr
	lookupString, destroyWindow, setDetectableAutoRepeat                                  uintptr
	keysymToUTF8                                                                          uintptr // optional, libxkbcommon

	// selections (clipboard_x11_linux.go)
	setSelectionOwner, getSelectionOwner, convertSelection, getWindowProperty uintptr
	changeProperty, deleteProperty, sendEvent, free, setErrorHandler          uintptr

	createFontCursor, defineCursor uintptr
}

// xErrCB is Xlib's error handler: it ignores every error (see x11ClipInit).
var xErrCB uintptr

var xLoaded bool

var (
	nameWMDelete    = cstr("WM_DELETE_WINDOW")
	nameWMProtocols = cstr("WM_PROTOCOLS")
)

type x11 struct {
	dpy, win uintptr
	screen   int32
	wmDelete uintptr
	wmProto  uintptr
	atoms    [1]uintptr // XSetWMProtocols array
	xev      [24]int64  // one XEvent
	keysym   uint64     // XLookupString out parameter (KeySym is unsigned long)
	supp     int32      // XkbSetDetectableAutoRepeat out parameter (Bool)
	keyDown  [32]uint8  // pressed keycodes, to flag auto-repeat
	keyBuf   [32]byte
	utf8     [8]byte // xkb_keysym_to_utf8 output
	title    [128]byte
	hints    xSizeHints
	x11Clip
	cursors [numShapes]uintptr // XDefineCursor cursors by shape, 0: not made yet
}

func loadX11() error {
	if xLoaded {
		return nil
	}
	h, err := dlopen("libX11.so.6", "libX11.so")
	if err != nil {
		return err
	}
	if err := dlsyms(h, "libX11", []symRef{
		{"XInitThreads", &xf.initThreads},
		{"XOpenDisplay", &xf.openDisplay},
		{"XCloseDisplay", &xf.closeDisplay},
		{"XDefaultScreen", &xf.defaultScreen},
		{"XRootWindow", &xf.rootWindow},
		{"XCreateSimpleWindow", &xf.createSimpleWindow},
		{"XSelectInput", &xf.selectInput},
		{"XInternAtom", &xf.internAtom},
		{"XSetWMProtocols", &xf.setWMProtocols},
		{"XStoreName", &xf.storeName},
		{"XSetWMNormalHints", &xf.setWMNormalHints},
		{"XMapWindow", &xf.mapWindow},
		{"XFlush", &xf.flush},
		{"XPending", &xf.pending},
		{"XNextEvent", &xf.nextEvent},
		{"XConnectionNumber", &xf.connectionNumber},
		{"XResourceManagerString", &xf.resourceManagerString},
		{"XLookupString", &xf.lookupString},
		{"XDestroyWindow", &xf.destroyWindow},
		{"XkbSetDetectableAutoRepeat", &xf.setDetectableAutoRepeat},
		{"XSetSelectionOwner", &xf.setSelectionOwner},
		{"XGetSelectionOwner", &xf.getSelectionOwner},
		{"XConvertSelection", &xf.convertSelection},
		{"XGetWindowProperty", &xf.getWindowProperty},
		{"XChangeProperty", &xf.changeProperty},
		{"XDeleteProperty", &xf.deleteProperty},
		{"XSendEvent", &xf.sendEvent},
		{"XFree", &xf.free},
		{"XSetErrorHandler", &xf.setErrorHandler},
		{"XCreateFontCursor", &xf.createFontCursor},
		{"XDefineCursor", &xf.defineCursor},
	}); err != nil {
		return err
	}
	xErrCB = ffi.NewCallback(func(c *ffi.CB) { c.Ret(0) })
	// XLookupString only yields Latin-1; libxkbcommon converts any keysym to
	// UTF-8. Optional: without it, non-Latin-1 layouts produce no text.
	if xh, err := dlopen("libxkbcommon.so.0", "libxkbcommon.so"); err == nil {
		_ = dlsyms(xh, "libxkbcommon", []symRef{{"xkb_keysym_to_utf8", &xf.keysymToUTF8}})
	}
	xLoaded = true
	return nil
}

// xftDPI reads Xft.dpi from the RESOURCE_MANAGER string (96 if absent).
func xftDPI(res uintptr) float64 {
	if res == 0 {
		return 96
	}
	var b strings.Builder
	for i := uintptr(0); i < 1<<16; i++ {
		c := *(*byte)(ptr(res + i))
		if c == 0 {
			break
		}
		b.WriteByte(c)
	}
	for _, line := range strings.Split(b.String(), "\n") {
		if v, ok := strings.CutPrefix(line, "Xft.dpi:"); ok {
			if dpi, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && dpi >= 48 && dpi <= 960 {
				return dpi
			}
		}
	}
	return 96
}

func (w *Window) openX11(o Options) (err error) {
	if err := loadX11(); err != nil {
		return err
	}
	x := &w.x
	defer func() {
		if err != nil {
			w.closeX11()
		}
	}()

	fr.Call0(xf.initThreads) // EGL drivers may use the connection from helper threads
	x.dpy = fr.Call1(xf.openDisplay, 0)
	if x.dpy == 0 {
		return errors.New("x11: XOpenDisplay failed")
	}
	x.screen = int32(fr.Call1(xf.defaultScreen, x.dpy))
	root := fr.Call2(xf.rootWindow, x.dpy, uintptr(x.screen))

	dpi := xftDPI(fr.Call1(xf.resourceManagerString, x.dpy))
	w.s120 = int32(dpi*120/96 + 0.5) // scale = dpi/96 in 1/120 units
	w.lw, w.lh = int32(o.Width), int32(o.Height)
	w.pw, w.ph = w.physical(w.lw), w.physical(w.lh)

	// XCreateSimpleWindow(dpy, parent, x, y, w, h, border_width, border, background)
	x.win = fr.Reset().Arg(x.dpy).Arg(root).Arg32(0).Arg32(0).Arg32(uint32(w.pw)).Arg32(uint32(w.ph)).
		Arg32(0).Arg(0).Arg(0).Call(xf.createSimpleWindow).R0()
	if x.win == 0 {
		return errors.New("x11: XCreateSimpleWindow failed")
	}
	mask := uintptr(xKeyPressMask | xKeyReleaseMask | xButtonPressMask | xButtonReleaseMask |
		xEnterWindowMask | xLeaveWindowMask | xPointerMotionMask | xExposureMask |
		xVisibilityChangeMask | xStructureNotifyMask | xFocusChangeMask | xPropertyChangeMask)
	fr.Call3(xf.selectInput, x.dpy, x.win, mask)
	w.x11ClipInit()

	x.wmDelete = fr.Call3(xf.internAtom, x.dpy, uintptr(unsafe.Pointer(&nameWMDelete[0])), 0)
	x.wmProto = fr.Call3(xf.internAtom, x.dpy, uintptr(unsafe.Pointer(&nameWMProtocols[0])), 0)
	x.atoms[0] = x.wmDelete
	fr.Call4(xf.setWMProtocols, x.dpy, x.win, uintptr(unsafe.Pointer(&x.atoms[0])), 1)

	copy(x.title[:len(x.title)-1], o.Title)
	fr.Call3(xf.storeName, x.dpy, x.win, uintptr(unsafe.Pointer(&x.title[0])))
	if o.MinWidth > 0 || o.MinHeight > 0 {
		scale := float32(w.s120) / 120
		x.hints = xSizeHints{flags: pMinSize,
			minWidth: int32(float32(o.MinWidth)*scale + 0.5), minHeight: int32(float32(o.MinHeight)*scale + 0.5)}
		fr.Call3(xf.setWMNormalHints, x.dpy, x.win, uintptr(unsafe.Pointer(&x.hints)))
	}
	// Held keys then produce repeated KeyPress without KeyRelease in between.
	fr.Call3(xf.setDetectableAutoRepeat, x.dpy, 1, uintptr(unsafe.Pointer(&x.supp)))

	fr.Call2(xf.mapWindow, x.dpy, x.win)
	fr.Call1(xf.flush, x.dpy)

	w.pfd[0] = pollfd{fd: int32(fr.Call1(xf.connectionNumber, x.dpy)), events: pollIn}
	return nil
}

func (w *Window) closeX11() {
	x := &w.x
	if x.dpy == 0 {
		return
	}
	if x.win != 0 {
		fr.Call2(xf.destroyWindow, x.dpy, x.win)
	}
	fr.Call1(xf.flush, x.dpy)
	fr.Call1(xf.closeDisplay, x.dpy)
	x.dpy, x.win = 0, 0
}

func (x *x11) i32(off uintptr) int32  { return *(*int32)(unsafe.Add(unsafe.Pointer(&x.xev), off)) }
func (x *x11) u32(off uintptr) uint32 { return *(*uint32)(unsafe.Add(unsafe.Pointer(&x.xev), off)) }
func (x *x11) i64(off uintptr) int64  { return *(*int64)(unsafe.Add(unsafe.Pointer(&x.xev), off)) }

func (w *Window) waitX11(timeoutNs int64) {
	x := &w.x
	deadline := int64(-1)
	if timeoutNs >= 0 {
		deadline = nanotime() + timeoutNs
	}
	for {
		// XPending flushes the output buffer and reads what has arrived.
		for int32(fr.Call1(xf.pending, x.dpy)) > 0 {
			fr.Call2(xf.nextEvent, x.dpy, uintptr(unsafe.Pointer(&x.xev)))
			w.x11Event()
		}
		if w.frameReq {
			// The swap itself blocks on vsync (eglSwapInterval 1), so the
			// next frame may start as soon as the loop looks again.
			w.frameReq = false
			w.q.push(Event{Kind: EvFrame, Time: nanotime()})
		}
		w.flushResize()
		tmo := int64(-1)
		if deadline >= 0 {
			tmo = max(deadline-nanotime(), 0)
		}
		if len(w.q.ev) > 0 {
			tmo = 0
		}
		// Poll even when returning at once, so worker wakes are not starved
		// while frames keep the queue non-empty.
		w.pfd[0].events = pollIn
		w.pollWait(tmo)
		w.drainWake()
		if tmo == 0 {
			return
		}
	}
}

func xMods(state uint32) Mods {
	var m Mods
	if state&xShiftMask != 0 {
		m |= ModShift
	}
	if state&xControlMask != 0 {
		m |= ModCtrl
	}
	if state&xMod1Mask != 0 {
		m |= ModAlt
	}
	if state&xMod4Mask != 0 {
		m |= ModSuper
	}
	return m
}

func (w *Window) setVisible(v bool) {
	if w.visible == v {
		return
	}
	w.visible = v
	var f Flags
	if v {
		f = FlagDown
	}
	w.q.push(Event{Kind: EvVisible, Flags: f, Time: nanotime()})
}

func (w *Window) x11Event() {
	x := &w.x
	now := nanotime()
	scale := float32(w.s120) / 120
	switch x.i32(xoType) {
	case xKeyPress, xKeyRelease:
		down := x.i32(xoType) == xKeyPress
		kc := x.u32(xoKeycode)
		mods := xMods(x.u32(xoState))
		w.mods = mods
		n := int32(fr.Reset().Arg(uintptr(unsafe.Pointer(&x.xev))).Arg(uintptr(unsafe.Pointer(&x.keyBuf[0]))).
			Arg32(uint32(len(x.keyBuf))).Arg(uintptr(unsafe.Pointer(&x.keysym))).Arg(0).Call(xf.lookupString).R0())
		evdev := uint32(0)
		if kc >= 8 {
			evdev = kc - 8
		}
		ev := Event{Kind: EvKey, Key: linuxKey(evdev, uint32(x.keysym)), Code: evdev, Mods: mods, Time: now}
		bit := uint8(1) << (kc & 7)
		if down {
			ev.Flags = FlagDown
			if x.keyDown[kc>>3&31]&bit != 0 {
				ev.Flags |= FlagRepeat
			}
			x.keyDown[kc>>3&31] |= bit
		} else {
			x.keyDown[kc>>3&31] &^= bit
		}
		w.q.push(ev)
		if down {
			var text []byte
			if xf.keysymToUTF8 != 0 {
				// Shortcut chords produce no text.
				if mods&(ModCtrl|ModAlt|ModSuper) == 0 {
					m := int32(fr.Reset().Arg32(uint32(x.keysym)).Arg(uintptr(unsafe.Pointer(&x.utf8[0]))).
						Arg(uintptr(len(x.utf8))).Call(xf.keysymToUTF8).R0())
					if m > 1 { // byte count includes the terminating NUL
						text = x.utf8[:m-1]
					}
				}
			} else if n > 0 {
				if n == 1 && x.keyBuf[0] >= 0x80 { // Latin-1 -> UTF-8
					c := x.keyBuf[0]
					x.keyBuf[0], x.keyBuf[1] = 0xC0|c>>6, 0x80|c&0x3F
					n = 2
				}
				text = x.keyBuf[:n]
			}
			if len(text) > 0 && text[0] >= 0x20 && text[0] != 0x7f {
				w.q.pushText(text, now)
			}
		}
	case xButtonPress, xButtonRelease:
		down := x.i32(xoType) == xButtonPress
		px, py := float32(x.i32(xoX)), float32(x.i32(xoY))
		w.mods = xMods(x.u32(xoState))
		var b Button
		switch x.u32(xoButton) {
		case 1:
			b = ButtonLeft
		case 2:
			b = ButtonMiddle
		case 3:
			b = ButtonRight
		case 8:
			b = ButtonBack
		case 9:
			b = ButtonForward
		case 4, 5, 6, 7: // wheel: up, down, left, right
			if down {
				ev := Event{Kind: EvScroll, Mods: w.mods, Time: now}
				d := scrollStep * scale
				switch x.u32(xoButton) {
				case 4:
					ev.DY = d
				case 5:
					ev.DY = -d
				case 6:
					ev.DX = d
				case 7:
					ev.DX = -d
				}
				w.q.push(ev)
			}
			return
		default:
			return
		}
		ev := Event{Kind: EvPointerButton, Button: b, X: px, Y: py, Mods: w.mods, Time: now}
		if down {
			ev.Flags = FlagDown
		}
		w.q.push(ev)
	case xMotionNotify:
		w.mods = xMods(x.u32(xoState))
		w.ptrX, w.ptrY, w.ptrIn = float32(x.i32(xoX)), float32(x.i32(xoY)), true
		w.cursorSync(false)
		w.q.push(Event{Kind: EvPointerMove, X: float32(x.i32(xoX)), Y: float32(x.i32(xoY)), Mods: w.mods, Time: now})
	case xEnterNotify:
		if x.i32(xoMode) == 0 { // NotifyNormal
			w.ptrX, w.ptrY, w.ptrIn = float32(x.i32(xoX)), float32(x.i32(xoY)), true
			w.cursorSync(false)
			w.q.push(Event{Kind: EvPointerMove, X: float32(x.i32(xoX)), Y: float32(x.i32(xoY)), Mods: w.mods, Time: now})
		}
	case xLeaveNotify:
		if x.i32(xoMode) == 0 {
			w.ptrIn = false
			w.q.push(Event{Kind: EvPointerLeave, Time: now})
		}
	case xFocusIn, xFocusOut:
		if m := x.i32(xoFocusMode); m == 1 || m == 2 { // NotifyGrab, NotifyUngrab
			return
		}
		in := x.i32(xoType) == xFocusIn
		var f Flags
		if in {
			f = FlagDown
		} else {
			x.keyDown = [32]uint8{}
			w.mods = 0
		}
		w.q.push(Event{Kind: EvFocus, Flags: f, Time: now})
	case xMapNotify:
		w.setVisible(true)
	case xUnmapNotify:
		w.setVisible(false)
	case xVisibilityNotify:
		w.setVisible(x.i32(xoVisState) != xVisibilityFullyObscured)
	case xConfigureNotify:
		pw, ph := x.i32(xoCfgW), x.i32(xoCfgH)
		if pw > 0 && ph > 0 {
			w.pw, w.ph = pw, ph
			w.lw = int32((int64(pw)*120 + int64(w.s120)/2) / int64(w.s120))
			w.lh = int32((int64(ph)*120 + int64(w.s120)/2) / int64(w.s120))
		}
	case xSelectionRequest:
		w.x11SelectionRequest()
	case xSelectionClear:
		w.x11SelectionClear()
	case xSelectionNotify:
		w.x11SelectionNotify()
	case xPropertyNotify:
		w.x11PropertyNotify()
	case xClientMessage:
		if uintptr(x.i64(xoMsgType)) == x.wmProto && x.i32(xoMsgFormat) == 32 && uintptr(x.i64(xoMsgData0)) == x.wmDelete {
			w.q.push(Event{Kind: EvClose, Time: now})
		}
	}
}

// xSizeHints is Xlib's XSizeHints (LP64: flags is a C long).
type xSizeHints struct {
	flags                                          int64
	x, y, width, height                            int32
	minWidth, minHeight, maxWidth, maxHeight       int32
	widthInc, heightInc                            int32
	minAspectX, minAspectY, maxAspectX, maxAspectY int32
	baseWidth, baseHeight, winGravity              int32
}

const pMinSize = 1 << 4
