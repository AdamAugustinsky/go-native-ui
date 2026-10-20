//go:build windows

package platform

import (
	"sync"

	"gonativeui/internal/win32"
)

var dpiOnce sync.Once

// setDPIAwareness makes the process per-monitor DPI aware (version 2), or system
// DPI aware on a Windows too old for that. It must come before anything reads
// monitor sizes or creates a window, because an unaware process is lied to: it
// sees every monitor at 96 dpi with virtualised coordinates. Open and Screens
// both call it, and the policy is set once (it cannot be changed afterwards).
func setDPIAwareness() {
	dpiOnce.Do(func() {
		if pSetDpiAwarenessCtx == 0 || !win32.Bool(win32.Call(pSetDpiAwarenessCtx, ^uintptr(3))) { // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
			if pSetProcessDPIAware != 0 {
				win32.Call(pSetProcessDPIAware)
			}
		}
	})
}
