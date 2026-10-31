package platform

// PointerLocation is where the pointer is on the screens, in the coordinates
// of ScreenInfo and the window frames (the primary display's top-left at 0,
// y growing downwards; points on macOS, physical pixels on Windows). ok is
// false where the platform does not say (Linux: Wayland keeps it from apps).
// A panel opened from the Dock's menu or the taskbar is placed by it, next
// to the app's icon.
func PointerLocation() (x, y float64, ok bool) { return pointerLocation() }
