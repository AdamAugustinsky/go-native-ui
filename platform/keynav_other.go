//go:build !darwin

package platform

// TabReachesControls reports whether Tab and Shift+Tab move the keyboard focus
// to every control (buttons, checkboxes, sliders ...) or only to text entry and
// lists. Windows and Linux reach every control: always true there. (macOS
// follows the Keyboard navigation setting; see keynav_darwin.go.)
func TabReachesControls() bool { return true }
