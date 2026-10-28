//go:build windows

package platform

import (
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

// The drawn titlebar on Windows (the phase 8 plan's W9): the window's frame
// without the system's caption, its hit test, the maximized inset, the
// pointer over the caption buttons, and the button width against the
// system's own caption.

const (
	htLeft        = 10
	htBottom      = 15
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000
	wsSysMenu     = 0x00080000

	dwmaCaptionButtonBounds = 5 // DWMWA_CAPTION_BUTTON_BOUNDS
)

var pClientToScreen = win32.Proc(user32, "ClientToScreen")

// openChrome opens a window with a drawn titlebar of band points, shown.
func openChrome(t *testing.T, band float32) *Window {
	t.Helper()
	w, err := Open(Options{Title: "chrome", Width: 640, Height: 400, Hidden: true, Titlebar: band})
	if err != nil {
		t.Skipf("no window: %v", err)
	}
	t.Cleanup(w.Close)
	w.Show()
	settle(w)
	return w
}

// clientOrigin is the client area's top-left in screen pixels.
func clientOrigin(w *Window) point {
	pt := point{}
	win32.Call(pClientToScreen, w.hwnd, uintptr(unsafe.Pointer(&pt)))
	return pt
}

// screenHit hit-tests the screen point (x, y).
func screenHit(w *Window, x, y int32) uintptr {
	return win32.Call(pSendMessageW, w.hwnd, wmNcHitTest, 0, uintptr(uint16(int16(x)))|uintptr(uint16(int16(y)))<<16)
}

// The window keeps WS_OVERLAPPEDWINDOW (snap, the animations, the system
// menu) and its client area reaches the window's top; the caption buttons
// are reported where the hit test finds them, and every region of the band
// hit-tests as it should: the buttons (snap layouts over maximise), the top
// border, the drag map, its blocks, and the system's side and bottom
// borders. 150 % and 200 % are TestChromeHitTestPerScale's.
func TestChromeFrameAndHitTest(t *testing.T) {
	lockThread(t)
	w := openChrome(t, 46)
	style := uint32(win32.Call(pGetWindowLong, w.hwnd, uintptr(gwlStyleArg)))
	if style&(wsCaption|wsThickFrame|wsMinimizeBox|wsMaximizeBox|wsSysMenu) != wsCaption|wsThickFrame|wsMinimizeBox|wsMaximizeBox|wsSysMenu {
		t.Errorf("style %#x lost part of WS_OVERLAPPEDWINDOW", style)
	}
	x, y, ww, wh := w.FrameRect()
	o := clientOrigin(w)
	t.Logf("scale %v, window %v %v %v×%v, client at %v, %d×%d, resize border %d", w.scale, x, y, ww, wh, o, w.w, w.h, w.resizeFrame())
	if float64(o.y) != y {
		t.Errorf("the client's top is %d, the window's %v: the caption is still there", o.y, y)
	}
	side := (int32(ww) - w.w) / 2
	if bottom := int32(wh) - w.h; bottom != side {
		t.Errorf("borders: %d at the sides, %d at the bottom", side, bottom)
	}
	if want := int32(640*w.scale + 0.5); w.w != want || w.h != int32(400*w.scale+0.5) {
		t.Errorf("client %d×%d, want the asked size %d×%d", w.w, w.h, want, int32(400*w.scale+0.5))
	}
	if !w.DrawnCaption() {
		t.Fatal("DrawnCaption is false")
	}
	sb := w.SystemButtons()
	if wantW := 3 * float32(int32(CaptionButtonW*w.scale+0.5)) / w.scale; sb[2] != float32(w.w)/w.scale || sb[2]-sb[0] != wantW || sb[1] != 0 || sb[3] != float32(int32(46*w.scale+0.5))/w.scale {
		t.Errorf("SystemButtons %v", sb)
	}

	band := int32(46*w.scale + 0.5)
	w.SetDragRegions([]DragRegion{{Rect: [4]float32{0, 0, float32(w.w), float32(band)}}, {Rect: [4]float32{100, 0, 200, float32(band)}, Block: true}})
	b := captionRects(float32(w.w), float32(band), w.scale)
	mid := band / 2
	cx := func(i int) int32 { return int32((b[i][0] + b[i][2]) / 2) }
	for _, c := range []struct {
		name string
		x, y int32
		want uintptr
	}{
		{"minimise", cx(0), mid, htMinButton},
		{"maximise", cx(1), mid, htMaxButton},
		{"close", cx(2), mid, htClose},
		{"band", 400, mid, htCaption},
		{"a block in the band", 150, mid, htClientArea},
		{"top border", 400, 1, htTop},
		{"content", 400, band + 20, htClientArea},
	} {
		if got := hitTest(w, c.x, c.y); got != c.want {
			t.Errorf("%s (%d, %d): %d, want %d", c.name, c.x, c.y, got, c.want)
		}
	}
	if got := screenHit(w, o.x-side/2, o.y+200); got != htLeft {
		t.Errorf("the left border: %d, want HTLEFT", got)
	}
	if got := screenHit(w, o.x+200, o.y+w.h+side/2); got != htBottom {
		t.Errorf("the bottom border: %d, want HTBOTTOM", got)
	}

	// A zoomed band (SetTitlebar, the app's zoom) moves the buttons' bottom.
	w.SetTitlebar(60, [2]float32{})
	if sb := w.SystemButtons(); sb[3] != float32(int32(60*w.scale+0.5))/w.scale {
		t.Errorf("SystemButtons after SetTitlebar(60): %v", sb)
	}
	w.SetTitlebar(46, [2]float32{})

	// Full screen has no buttons.
	w.ToggleFullScreen()
	settle(w)
	if w.DrawnCaption() || w.SystemButtons() != [4]float32{} {
		t.Errorf("full screen: DrawnCaption %v, SystemButtons %v", w.DrawnCaption(), w.SystemButtons())
	}
	w.ToggleFullScreen()
	settle(w)
	if o2 := clientOrigin(w); !w.DrawnCaption() || o2.y != o.y {
		t.Errorf("out of full screen: DrawnCaption %v, client top %d (was %d)", w.DrawnCaption(), o2.y, o.y)
	}
}

// Maximized, the window extends past its monitor by the resize border; the
// client area is moved down by as much, so it is the monitor's work area
// exactly and the band is on the screen, with no top border to resize by.
// Restored, the client's top is the window's again.
func TestChromeMaximizeKeepsTheClientOnTheMonitor(t *testing.T) {
	lockThread(t)
	w := openChrome(t, 46)
	_, y0, _, _ := w.FrameRect()
	w.ToggleMaximize()
	settle(w)
	if !w.Maximized() {
		t.Fatal("ToggleMaximize did not maximize")
	}
	mon := win32.Call(pMonitorFromWindow, w.hwnd, monitorNearest)
	w.mi.size = uint32(unsafe.Sizeof(w.mi))
	if !win32.Bool(win32.Call(pGetMonitorInfoW, mon, uintptr(unsafe.Pointer(&w.mi)))) {
		t.Fatal("GetMonitorInfoW")
	}
	o, a := clientOrigin(w), w.mi.work
	got := rect{o.x, o.y, o.x + w.w, o.y + w.h}
	t.Logf("maximized: client %v, work area %v", got, a)
	if got != a {
		t.Errorf("maximized: the client %v is not the work area %v", got, a)
	}
	if h := hitTest(w, 400, 1); h == htTop {
		t.Error("maximized: the band's top still resizes")
	}
	w.ToggleMaximize()
	settle(w)
	if w.Maximized() {
		t.Fatal("ToggleMaximize did not restore")
	}
	_, y1, _, _ := w.FrameRect()
	if o := clientOrigin(w); float64(o.y) != y1 || y1 != y0 {
		t.Errorf("restored: client top %d, window top %v (was %v)", o.y, y1, y0)
	}
}

// The pointer over the caption buttons (WM_NC* messages, as the system
// sends them for HTCLOSE) becomes the client's pointer events, so the app's
// drawn button hovers and clicks; RequestClose is the window's EvClose.
func TestChromeCaptionButtonsGetThePointer(t *testing.T) {
	lockThread(t)
	w := openChrome(t, 46)
	settle(w)
	band := int32(46*w.scale + 0.5)
	b := captionRects(float32(w.w), float32(band), w.scale)
	cx, cy := int32((b[2][0]+b[2][2])/2), band/2
	o := clientOrigin(w)
	lp := uintptr(uint16(int16(o.x+cx))) | uintptr(uint16(int16(o.y+cy)))<<16
	win32.Call(pPostMessageW, w.hwnd, wmNcMouseMove, htClose, lp)
	win32.Call(pPostMessageW, w.hwnd, wmNcLButtonDown, htClose, lp)
	win32.Call(pPostMessageW, w.hwnd, wmLButtonUp, 0, uintptr(cx)|uintptr(cy)<<16)
	got := pumpEvents([]*Window{w}, 2*time.Second, func(g [][]Event) bool { return countKind(g[0], EvPointerButton) >= 2 })
	var move, down, up bool
	for _, e := range got[0] {
		switch {
		case e.Kind == EvPointerMove && int32(e.X) == cx && int32(e.Y) == cy:
			move = true
		case e.Kind == EvPointerButton && e.Flags&FlagDown != 0 && int32(e.X) == cx && int32(e.Y) == cy:
			down = true
		case e.Kind == EvPointerButton && e.Flags&FlagDown == 0:
			up = true
		}
	}
	if !move || !down || !up {
		t.Errorf("over close at (%d, %d): move %v, press %v, release %v; events %+v", cx, cy, move, down, up, got[0])
	}
	w.RequestClose()
	got = pumpEvents([]*Window{w}, 2*time.Second, func(g [][]Event) bool { return hasKind(g[0], EvClose) })
	if !hasKind(got[0], EvClose) {
		t.Error("RequestClose: no EvClose")
	}
}

// The caption buttons are as wide as the system's: DWM's caption button
// bounds of an ordinary window, a third each, against CaptionButtonW at the
// window's scale.
func TestCaptionMetricsMatchTheSystem(t *testing.T) {
	lockThread(t)
	w := openW(t, "system caption")
	w.Show()
	settle(w)
	var r rect
	if hr := win32.HR(win32.Call(pDwmGetWindowAttrW, w.hwnd, dwmaCaptionButtonBounds, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r))); hr < 0 {
		t.Skipf("DWMWA_CAPTION_BUTTON_BOUNDS: %#x", uint32(hr))
	}
	each := float32(r.right-r.left) / 3
	want := float32(int32(CaptionButtonW*w.scale + 0.5))
	t.Logf("the system's caption buttons: %v, %.1f×%d each at scale %v (ours %v×band)", r, each, r.bottom-r.top, w.scale, want)
	if d := each - want; d < -2 || d > 2 {
		t.Errorf("the system's caption button is %.1f px wide, ours %v", each, want)
	}
}
