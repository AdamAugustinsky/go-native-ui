package pilot

import (
	"sync/atomic"

	"gonativeui/platform"
)

// Wake wakes the window loop from any goroutine, like platform.Wake, but is
// safe before the window exists: platform.Wake with no window does nothing yet
// still marks a wake as pending, which would swallow every later one. A wake
// asked for before Init is delivered when Init runs.
func Wake() {
	if awake.Load() {
		platform.Wake()
		return
	}
	wakeLater.Store(true)
}

var awake, wakeLater atomic.Bool

// markAwake is called by Init, on the loop's thread, once the window exists.
func markAwake() {
	awake.Store(true)
	if wakeLater.Swap(false) {
		platform.Wake()
	}
}
