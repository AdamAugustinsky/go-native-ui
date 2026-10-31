package platform

import (
	"unsafe"

	"gonativeui/internal/win32"
)

// pointerLocation is GetCursorPos, in physical pixels as ScreenInfo is.
func pointerLocation() (x, y float64, ok bool) {
	var pt struct{ X, Y int32 }
	if pGetCursorPos == 0 || !win32.Bool(win32.Call(pGetCursorPos, uintptr(unsafe.Pointer(&pt)))) {
		return 0, 0, false
	}
	return float64(pt.X), float64(pt.Y), true
}
