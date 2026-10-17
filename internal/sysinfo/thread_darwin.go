//go:build darwin

package sysinfo

import (
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

const (
	threadBasicInfo      = 3  // THREAD_BASIC_INFO
	threadBasicInfoCount = 10 // sizeof(thread_basic_info) / sizeof(integer_t)
)

var (
	threadFrame                               = new(ffi.Frame)
	threadInfo                                = new([threadBasicInfoCount]int32)
	threadCount                               = new(uint32)
	fnPthreadSelf, fnMachThread, fnThreadInfo uintptr
)

func init() {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	fnPthreadSelf, _ = purego.Dlsym(lib, "pthread_self")
	fnMachThread, _ = purego.Dlsym(lib, "pthread_mach_thread_np")
	fnThreadInfo, _ = purego.Dlsym(lib, "thread_info")
}

// ThreadCPUNs returns the user+system CPU time consumed so far by the
// calling OS thread, or -1: thread_info(THREAD_BASIC_INFO) on the thread's
// mach port, which pthread_mach_thread_np returns without taking a port
// reference (mach_thread_self adds one per call). Microsecond resolution.
// (clock_gettime_nsec_np(CLOCK_THREAD_CPUTIME_ID) is finer but reads wrong
// under Rosetta.) It does not allocate. The goroutine must be locked to its
// thread (runtime.LockOSThread) for deltas to mean anything. Not safe for
// concurrent use.
func ThreadCPUNs() int64 {
	if fnThreadInfo == 0 {
		return -1
	}
	port := threadFrame.Call1(fnMachThread, threadFrame.Call0(fnPthreadSelf))
	*threadCount = threadBasicInfoCount
	kr := threadFrame.Reset().Arg32(uint32(port)).Arg32(threadBasicInfo).
		Arg(uintptr(unsafe.Pointer(threadInfo))).Arg(uintptr(unsafe.Pointer(threadCount))).Call(fnThreadInfo).R0()
	if int32(kr) != 0 {
		return -1
	}
	// user_time {seconds, microseconds}, system_time {seconds, microseconds}
	return (int64(threadInfo[0])+int64(threadInfo[2]))*1e9 + (int64(threadInfo[1])+int64(threadInfo[3]))*1e3
}
