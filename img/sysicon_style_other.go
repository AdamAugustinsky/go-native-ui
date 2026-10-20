//go:build !darwin

package img

// IconStyle identifies the user's icon style setting, which changes the art
// of application icons on macOS 26 and later; it is 0 on every other system,
// whose icons have no such setting.
func IconStyle() uint64 { return 0 }
