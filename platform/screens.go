package platform

// ScreenInfo describes one display (F4, the multi-window and multi-screen work,
// extends it), in the coordinates of the platform's window
// frames: one space for all displays, the origin at the top-left of the primary
// display, y growing downwards (points on macOS, physical pixels on Windows).
type ScreenInfo struct {
	// ID names the display so that it can be found again ("" if unknown).
	ID string
	// X, Y, W, H are the whole display.
	X, Y, W, H float64
	// VisibleX, VisibleY, VisibleW and VisibleH are the part windows can use,
	// without the menu bar, the Dock or the taskbar.
	VisibleX, VisibleY, VisibleW, VisibleH float64
	// Scale is the display's pixels per unit (2 on a Retina display, 1.5 at
	// 144 dpi on Windows).
	Scale float64
}
