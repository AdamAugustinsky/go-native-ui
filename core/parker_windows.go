package core

import (
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

var (
	kernel32             = win32.LoadSystem("kernel32.dll")
	pCreateEventW        = win32.Proc(kernel32, "CreateEventW")
	pSetEvent            = win32.Proc(kernel32, "SetEvent")
	pWaitForSingleObject = win32.Proc(kernel32, "WaitForSingleObject")
)

const winInfinite = 0xFFFFFFFF

// pMsgWait is MsgWaitForMultipleObjectsEx, resolved by the first ParkMsg (so
// that a program that never calls it does not load user32.dll).
var pMsgWait = sync.OnceValue(func() uintptr {
	return win32.Proc(win32.LoadSystem("user32.dll"), "MsgWaitForMultipleObjectsEx")
})

const (
	winQSAllInput       = 0x04FF // QS_ALLINPUT
	winMWMOInputAvail   = 0x0004 // MWMO_INPUTAVAILABLE
	winWaitObject0Plus1 = 1      // WAIT_OBJECT_0 + 1: the thread's message queue has input
)

// ParkMsg is Park for a goroutine whose OS thread (runtime.LockOSThread) owns
// windows: while it waits for the token it calls pump whenever the thread's
// message queue has input, so that the messages other threads and processes
// send to those windows are answered. pump must take the thread's messages
// (PeekMessage with PM_REMOVE) or ParkMsg spins. Windows only.
func (p *Parker) ParkMsg(pump func()) bool {
	if p.closed.Load() {
		return false
	}
	if p.ch != nil { // no event to wait on with the queue: look at both now and then
		for wait := true; wait; {
			select {
			case _, ok := <-p.ch:
				if !ok {
					return false
				}
				wait = false
			default:
				pump()
				time.Sleep(5 * time.Millisecond)
			}
		}
	} else if !p.sys.waitMsg(&p.closed, pump) {
		return false
	}
	p.state.Swap(0) // as in Park
	return !p.closed.Load()
}

func (s *parkerSys) waitMsg(closed *atomic.Bool, pump func()) bool {
	s.waiting.Add(1)
	if closed.Load() {
		s.waiting.Add(-1)
		return false
	}
	fn := pMsgWait()
	for {
		if fn == 0 {
			syscall.SyscallN(pWaitForSingleObject, s.h, winInfinite)
			break
		}
		r, _, _ := syscall.SyscallN(fn, 1, uintptr(unsafe.Pointer(&s.h)), winInfinite, winQSAllInput, winMWMOInputAvail)
		if uint32(r) != winWaitObject0Plus1 { // the event (the token, or Close), or a failure: return as Park would
			break
		}
		pump()
	}
	s.waiting.Add(-1)
	return true
}

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
