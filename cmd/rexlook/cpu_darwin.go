//go:build darwin

package main

import (
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// Main-thread CPU time: thread_info(mach_thread_self(), THREAD_BASIC_INFO).
// The call goes through internal/ffi, so measuring allocates nothing and can
// run inside the measured window.

const (
	threadBasicInfo      = 3
	threadBasicInfoCount = 10 // sizeof(thread_basic_info) / sizeof(integer_t)
)

var (
	tFrame      = new(ffi.Frame)
	tInfo       = new([threadBasicInfoCount]int32)
	tCount      = new(uint32)
	tThreadSelf uintptr // the main thread's mach port (taken once: every call adds a port reference)
	tThreadInfo uintptr
)

// initThreadCPU must run on the thread to be measured (the main thread).
func initThreadCPU() bool {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return false
	}
	self, err1 := purego.Dlsym(lib, "mach_thread_self")
	info, err2 := purego.Dlsym(lib, "thread_info")
	if err1 != nil || err2 != nil {
		return false
	}
	tThreadInfo = info
	tThreadSelf = tFrame.Call0(self)
	return true
}

// threadCPUNs is the user + system CPU time of the measured thread, or -1.
func threadCPUNs() int64 {
	if tThreadInfo == 0 {
		return -1
	}
	*tCount = threadBasicInfoCount
	kr := tFrame.Reset().Arg32(uint32(tThreadSelf)).Arg32(threadBasicInfo).
		Arg(uintptr(unsafe.Pointer(tInfo))).Arg(uintptr(unsafe.Pointer(tCount))).Call(tThreadInfo).R0()
	if int32(kr) != 0 {
		return -1
	}
	// user_time {seconds, microseconds}, system_time {seconds, microseconds}
	return (int64(tInfo[0])+int64(tInfo[2]))*1e9 + (int64(tInfo[1])+int64(tInfo[3]))*1e3
}
