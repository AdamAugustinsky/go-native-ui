package platform

import (
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// The runtime's nanotime on Windows reads the interrupt time, which only
// advances once per timer tick (0.5-15.6 ms). CI showed wake latencies of
// exactly 0 with it, so Windows uses QueryPerformanceCounter instead.
var (
	pQPC    = win32.Proc(kernel32, "QueryPerformanceCounter")
	qpcFreq = func() int64 {
		var f int64
		syscall.SyscallN(win32.Proc(kernel32, "QueryPerformanceFrequency"), uintptr(unsafe.Pointer(&f)))
		return f
	}()
)

// nanotime returns monotonic nanoseconds from QueryPerformanceCounter. It
// doesn't allocate: v stays on the stack (syscall.SyscallN keeps the
// converted pointer alive for the call).
func nanotime() int64 {
	var v int64
	syscall.SyscallN(pQPC, uintptr(unsafe.Pointer(&v)))
	return v/qpcFreq*1e9 + v%qpcFreq*1e9/qpcFreq
}
