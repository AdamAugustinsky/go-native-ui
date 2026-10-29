package platform

// The drawn window chrome on Windows (the phase 8 plan's W9): the geometry of
// the caption buttons the app draws, and the hit test that tells Windows what
// each point of the client area is (WM_NCHITTEST). It is plain arithmetic,
// kept free of the OS so that it is tested on every OS
// (chrome_hit_test.go); chrome_windows.go applies it.

// Hit-test results (winuser.h), as WM_NCHITTEST returns them.
const (
	htClientArea = 1
	htCaption    = 2
	htMinButton  = 8
	htMaxButton  = 9
	htTop        = 12
	htTopLeft    = 13
	htTopRight   = 14
	htClose      = 20
)

// CaptionButtonW is the width of one caption button in logical units:
// Windows 11's minimise, maximise and close are 46 wide at 100 %, as
// Windows 10's are (DWMWA_CAPTION_BUTTON_BOUNDS of a standard window,
// TestCaptionMetricsMatchTheSystem). They are as tall as the band they are
// in: 32 for the system's own caption, the app's titlebar height
// (Options.Titlebar) for a drawn one.
const CaptionButtonW = 46

// captionRects are the minimise, maximise and close buttons of a client area
// clientW pixels wide at scale (pixels per logical unit), in a band bandH
// pixels high: flush with the top-right corner, close last, each
// CaptionButtonW wide (whole pixels, so that the three tile).
func captionRects(clientW, bandH, scale float32) [3][4]float32 {
	bw := float32(int32(CaptionButtonW*scale + 0.5))
	var r [3][4]float32
	for i := range r {
		x1 := clientW - float32(2-i)*bw
		r[i] = [4]float32{x1 - bw, 0, x1, bandH}
	}
	return r
}

// chromeHit is the hit test of a drawn titlebar window's client area (x, y
// in client pixels; DefWindowProc has already answered for the left, right
// and bottom borders, which stay the system's): the caption buttons first
// (so that Windows 11 shows its snap layouts over maximise, and the close
// button reaches the window's top edge), then the top resize border inside
// the client (frame pixels deep; none while maximized), then the app's drag
// map (the band moves the window), else the client. buttons are
// captionRects' (nil: none).
func chromeHit(x, y, clientW, frame float32, maximized bool, buttons *[3][4]float32, drag *dragMap) uintptr {
	if buttons != nil {
		for i, code := range [3]uintptr{htMinButton, htMaxButton, htClose} {
			if inRect(buttons[i], x, y) {
				return code
			}
		}
	}
	if !maximized && y >= 0 && y < frame {
		switch {
		case x < frame:
			return htTopLeft
		case x >= clientW-frame:
			return htTopRight
		}
		return htTop
	}
	if drag != nil && drag.at(x, y) {
		return htCaption
	}
	return htClientArea
}

// panelHit is the hit test of a panel (a WS_POPUP window without a frame):
// its drag map moves it, the rest is its content.
func panelHit(x, y float32, drag *dragMap) uintptr {
	if drag.at(x, y) {
		return htCaption
	}
	return htClientArea
}
