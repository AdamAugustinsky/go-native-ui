package platform

// pointerLocation is unknown on Linux: Wayland does not tell an app where
// the pointer is outside its windows.
func pointerLocation() (x, y float64, ok bool) { return 0, 0, false }
