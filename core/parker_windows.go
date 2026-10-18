package core

import (
	"sync/atomic"
	"syscall"
	"time"

	"gonativeui/internal/win32"
)

var (
	kernel32             = win32.LoadSystem("kernel32.dll")
	pCreateEventW        = win32.Proc(kernel32, "CreateEventW")
	pSetEvent            = win32.Proc(kernel32, "SetEvent")
	pWaitForSingleObject = win32.Proc(kernel32, "WaitForSingleObject")
)

const winInfinite = 0xFFFFFFFF

// parkerSys is an auto-reset event: SetEvent releases the one waiting thread
// and resets, or, with no waiter, stays signalled until the next wait takes
// it, which is the one-token semaphore itself. Park waits in
// WaitForSingleObject, a system call: the goroutine's thread blocks in the
// kernel and the runtime hands its P to another thread, with no sudog.
type parkerSys struct {
	h       uintptr
	waiting atomic.Int32 // goroutines in or entering wait; close frees h only after they leave
}

func (s *parkerSys) open() bool {
	if pCreateEventW == 0 || pSetEvent == 0 || pWaitForSingleObject == 0 {
		return false
	}
	h, _, _ := syscall.SyscallN(pCreateEventW, 0, 0, 0, 0) // default security, auto-reset, not signalled, unnamed
	s.h = h
	return h != 0
}

func (s *parkerSys) wait(closed *atomic.Bool) bool {
	s.waiting.Add(1)
	if closed.Load() { // Close sets closed before it counts the waiters
		s.waiting.Add(-1)
		return false
	}
	syscall.SyscallN(pWaitForSingleObject, s.h, winInfinite)
	s.waiting.Add(-1)
	return true
}

func (s *parkerSys) wake() { syscall.SyscallN(pSetEvent, s.h) }

func (s *parkerSys) wakeForClose() { syscall.SyscallN(pSetEvent, s.h) }

// close frees the event once no goroutine is in a wait on it.
func (s *parkerSys) close() {
	for s.waiting.Load() != 0 {
		time.Sleep(time.Millisecond)
	}
	win32.CloseHandle(s.h)
}
