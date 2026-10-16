//go:build darwin

package sysinfo

import (
	"os"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// rusage_info_v4 prefix (<sys/resource.h>); we only read up to ri_proc_start_abstime.
type rusageInfo struct {
	uuid            [16]byte
	userTime        uint64
	systemTime      uint64
	pkgIdleWkups    uint64
	interruptWkups  uint64
	pageins         uint64
	wiredSize       uint64
	residentSize    uint64
	physFootprint   uint64
	procStartAbs    uint64
	procExitAbs     uint64
	childUser       uint64
	childSystem     uint64
	childIdleWk     uint64
	childIntrWk     uint64
	childPageins    uint64
	childElapsedAbs uint64
	rest            [64]uint64 // remaining v4 fields
}

var (
	mu        sync.Mutex
	fr        = new(ffi.Frame)
	info      = new(rusageInfo)
	tb        = new([2]uint32) // mach_timebase_info: numer, denom
	fnRusage  uintptr
	fnAbsTime uintptr
	pid       = uintptr(os.Getpid())
)

func init() {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	fnRusage, _ = purego.Dlsym(lib, "proc_pid_rusage")
	fnAbsTime, _ = purego.Dlsym(lib, "mach_absolute_time")
	tbFn, _ := purego.Dlsym(lib, "mach_timebase_info")
	fr.Call1(tbFn, uintptr(unsafe.Pointer(tb)))
}

func absToNs(abs uint64) int64 { return int64(abs * uint64(tb[0]) / uint64(tb[1])) }

func rusage() bool {
	return fnRusage != 0 && fr.Call3(fnRusage, pid, 4 /* RUSAGE_INFO_V4 */, uintptr(unsafe.Pointer(info))) == 0
}

// ProcessAgeNs returns nanoseconds since the kernel created this process.
func ProcessAgeNs() int64 {
	mu.Lock()
	defer mu.Unlock()
	if !rusage() {
		return -1
	}
	now := uint64(fr.Call0(fnAbsTime))
	return absToNs(now - info.procStartAbs)
}

// Read returns current process metrics.
func Read() Snapshot {
	mu.Lock()
	defer mu.Unlock()
	var s Snapshot
	if rusage() {
		s.Footprint = info.physFootprint
		s.RSS = info.residentSize
		s.Wakeups = info.pkgIdleWkups + info.interruptWkups
	}
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		s.CPUNs = ru.Utime.Nano() + ru.Stime.Nano()
	}
	return s
}
