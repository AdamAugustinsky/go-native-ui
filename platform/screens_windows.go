//go:build windows

package platform

import (
	"sync"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// monitorInfoEx mirrors MONITORINFOEXW (winuser.h): 104 bytes.
type monitorInfoEx struct {
	Size    uint32
	Monitor [4]int32 // left, top, right, bottom
	Work    [4]int32
	Flags   uint32
	Device  [32]uint16
}

var _ [unsafe.Sizeof(monitorInfoEx{}) - 104]struct{}
var _ [104 - unsafe.Sizeof(monitorInfoEx{})]struct{}

var scr struct {
	once            sync.Once
	enum, info, dpi uintptr
	cb              uintptr
	mu              sync.Mutex
	out             []ScreenInfo
}

// Screens returns the displays that are connected, the primary one first, in
// the virtual-screen coordinates that window frames use (physical pixels when the
// process is DPI-aware). It may be called from any goroutine.
func Screens() []ScreenInfo {
	scr.once.Do(func() {
		u := win32.LoadSystem("user32.dll")
		scr.enum, scr.info = win32.Proc(u, "EnumDisplayMonitors"), win32.Proc(u, "GetMonitorInfoW")
		scr.dpi = win32.Proc(win32.LoadSystem("shcore.dll"), "GetDpiForMonitor") // 0 before Windows 8.1
		scr.cb = syscall.NewCallback(func(mon, hdc, rc, data uintptr) uintptr {
			mi := monitorInfoEx{Size: uint32(unsafe.Sizeof(monitorInfoEx{}))}
			if r, _, _ := syscall.SyscallN(scr.info, mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
				return 1
			}
			s := ScreenInfo{
				ID: syscall.UTF16ToString(mi.Device[:]),
				X:  float64(mi.Monitor[0]), Y: float64(mi.Monitor[1]),
				W: float64(mi.Monitor[2] - mi.Monitor[0]), H: float64(mi.Monitor[3] - mi.Monitor[1]),
				VisibleX: float64(mi.Work[0]), VisibleY: float64(mi.Work[1]),
				VisibleW: float64(mi.Work[2] - mi.Work[0]), VisibleH: float64(mi.Work[3] - mi.Work[1]),
				Scale: 1,
			}
			if scr.dpi != 0 {
				var x, y uint32
				if hr, _, _ := syscall.SyscallN(scr.dpi, mon, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); int32(hr) >= 0 && x > 0 {
					s.Scale = float64(x) / 96
				}
			}
			if mi.Flags&1 != 0 { // MONITORINFOF_PRIMARY: first
				scr.out = append([]ScreenInfo{s}, scr.out...)
			} else {
				scr.out = append(scr.out, s)
			}
			return 1 // continue
		})
	})
	if scr.enum == 0 || scr.info == 0 {
		return nil
	}
	scr.mu.Lock()
	defer scr.mu.Unlock()
	scr.out = nil
	syscall.SyscallN(scr.enum, 0, 0, scr.cb, 0)
	out := scr.out
	scr.out = nil
	return out
}
