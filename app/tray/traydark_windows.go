//go:build windows && !gnui_publicapi

package tray

import (
	"sync"

	"gonativeui/internal/win32"
)

// Dark popup menus (D76): uxtheme's undocumented ordinals 135
// (SetPreferredAppMode; AllowDarkModeForApp before Windows 10 1903, which
// takes the same 1), 133 (AllowDarkModeForWindow) and 136
// (FlushMenuThemes). With "allow dark", menus follow the system's app
// theme. The gnui_publicapi build leaves them out (traydark_public_windows.go)
// and Options.PublicAPIOnly skips them; menus are light then.

var dark struct {
	once                   sync.Once
	appMode, window, flush uintptr
	done                   bool
}

const allowDark = 1

func darkMenus(owner uintptr) {
	dark.once.Do(func() {
		ux := win32.LoadSystem("uxtheme.dll")
		dark.appMode, dark.window, dark.flush = procOrdinal(ux, 135), procOrdinal(ux, 133), procOrdinal(ux, 136)
	})
	if dark.appMode == 0 || dark.flush == 0 {
		return
	}
	if !dark.done {
		dark.done = true
		call(dark.appMode, allowDark)
		if dark.window != 0 {
			call(dark.window, owner, 1)
		}
	}
	call(dark.flush)
}

var pGetProcAddress = win32.Proc(win32.LoadSystem("kernel32.dll"), "GetProcAddress")

// procOrdinal is GetProcAddress by ordinal.
func procOrdinal(dll uintptr, ord uint16) uintptr {
	if dll == 0 {
		return 0
	}
	return call(pGetProcAddress, dll, uintptr(ord))
}
