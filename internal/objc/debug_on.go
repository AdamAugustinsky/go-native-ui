//go:build darwin && gnuidebug

package objc

import "syscall"

// mainThread is the kernel id of the thread package initialisation ran on:
// the process's main thread (the runtime locks the main goroutine to it
// during init).
var mainThread uintptr

func initMainCheck() { mainThread = threadSelfID() }

// checkMain panics when the main-thread frame F is used on another thread.
// It reads the thread id with a raw system call, so it needs no frame.
func checkMain() {
	if threadSelfID() != mainThread {
		panic("objc: the main-thread frame F used off the main thread")
	}
}

func threadSelfID() uintptr {
	id, _, _ := syscall.RawSyscall(syscall.SYS_THREAD_SELFID, 0, 0, 0)
	return id
}
