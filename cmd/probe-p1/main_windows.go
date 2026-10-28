//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

func init() { runtime.LockOSThread() } // main runs on the process's main thread

// Messages and styles (WinUser.h).
const (
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	wmApp             = 0x8000
	wmProbeQuit       = wmApp + 1
	wmProbePing       = wmApp + 2
	wmProbeBlock      = wmApp + 3 // ShutdownBlockReasonCreate on the window's own thread
	wmProbeUnblock    = wmApp + 4
	pingReply         = 0x1234
	endSessionLogoff  = 0x80000000 // ENDSESSION_LOGOFF
	wsPopup           = 0x80000000
	wsExToolWindow    = 0x00000080
	smtoAbortIfHung   = 0x0002
	hwndBroadcast     = 0xFFFF
)

// wndClassEx is WNDCLASSEXW (WinUser.h); Go's alignment gives C's layout.
type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

// msg is MSG (WinUser.h): 48 bytes on 64-bit.
type msg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       [2]int32
	lPrivate uint32
}

// osVersion is RTL_OSVERSIONINFOW (wdm.h).
type osVersion struct {
	size, major, minor, build, platform uint32
	csd                                 [128]uint16
}

type sessionAnswer struct {
	Sent     bool   `json:"sent"`
	Received bool   `json:"received"`
	Answered int    `json:"answered"` // what the window procedure returned
	Result   uint64 `json:"result"`   // what SendMessageTimeoutW reported back
	LParam   string `json:"lParam,omitempty"`
	Alive    bool   `json:"aliveAfter"` // IsWindow and a ping answered
}

type result struct {
	Probe   string `json:"probe"`
	GOARCH  string `json:"goarch"`
	Windows string `json:"windows"`

	Ole32LoadUs           float64 `json:"ole32LoadUs"`
	OleInitHR             string  `json:"oleInitHR"`
	OleInitColdUs         float64 `json:"oleInitColdUs"`
	OleInitSecondHR       string  `json:"oleInitSecondHR"`
	OleInitSecondUs       float64 `json:"oleInitSecondUs"`
	MsSinceStartAtOleInit float64 `json:"msSinceProcessStartAtOleInit"`
	MsSinceStartAfterOle  float64 `json:"msSinceProcessStartAfterOleInit"`

	HiddenWindow struct {
		Created bool `json:"created"`
		Visible bool `json:"visible"`
	} `json:"hiddenWindow"`
	TaskbarCreatedMsg   uint32 `json:"taskbarCreatedMsg"`
	ShutdownBlockReason struct {
		Created  bool   `json:"created"`
		Err      string `json:"err,omitempty"`
		ReadBack string `json:"readBack"`
	} `json:"shutdownBlockReason"`
	QueryEndSession struct {
		Allow         sessionAnswer `json:"allow"`
		Refuse        sessionAnswer `json:"refuse"`
		RefuseBlocked sessionAnswer `json:"refuseWithBlockReason"`
	} `json:"queryEndSession"`
	EndSession struct {
		False sessionAnswer `json:"wParamFalse"`
		True  sessionAnswer `json:"wParamTrue"`
	} `json:"endSession"`
	BroadcastReceived bool     `json:"broadcastReceived"`
	TimedOut          bool     `json:"timedOut"`
	Errors            []string `json:"errors"`
}

var (
	user32   = win32.LoadSystem("user32.dll")
	kernel32 = win32.LoadSystem("kernel32.dll")

	pRegisterClassEx    = win32.Proc(user32, "RegisterClassExW")
	pCreateWindowEx     = win32.Proc(user32, "CreateWindowExW")
	pDefWindowProc      = win32.Proc(user32, "DefWindowProcW")
	pGetMessage         = win32.Proc(user32, "GetMessageW")
	pTranslateMessage   = win32.Proc(user32, "TranslateMessage")
	pDispatchMessage    = win32.Proc(user32, "DispatchMessageW")
	pPostQuitMessage    = win32.Proc(user32, "PostQuitMessage")
	pPostMessage        = win32.Proc(user32, "PostMessageW")
	pSendMessageTimeout = win32.Proc(user32, "SendMessageTimeoutW")
	pIsWindow           = win32.Proc(user32, "IsWindow")
	pIsWindowVisible    = win32.Proc(user32, "IsWindowVisible")
	pDestroyWindow      = win32.Proc(user32, "DestroyWindow")
	pRegisterWindowMsg  = win32.Proc(user32, "RegisterWindowMessageW")
	pSBRCreate          = win32.Proc(user32, "ShutdownBlockReasonCreate")
	pSBRQuery           = win32.Proc(user32, "ShutdownBlockReasonQuery")
	pSBRDestroy         = win32.Proc(user32, "ShutdownBlockReasonDestroy")
	pGetModuleHandle    = win32.Proc(kernel32, "GetModuleHandleW")
	pQPC                = win32.Proc(kernel32, "QueryPerformanceCounter")
	pQPF                = win32.Proc(kernel32, "QueryPerformanceFrequency")
	pGetProcessTimes    = win32.Proc(kernel32, "GetProcessTimes")
	pGetCurrentProcess  = win32.Proc(kernel32, "GetCurrentProcess")
	pPreciseTime        = win32.Proc(kernel32, "GetSystemTimePreciseAsFileTime")
	pRtlGetVersion      = win32.Proc(win32.LoadSystem("ntdll.dll"), "RtlGetVersion")
)

// Out parameters live in package variables (internal/com's convention).
var (
	qpcVal, qpcFreq                   int64
	ftCreate, ftExit, ftKern, ftUser  int64
	ftNow                             int64
	osvi                              osVersion
	wc                                wndClassEx
	loopMsg                           msg
	sendRes                           uintptr // the sender thread's
	reasonBuf                         [256]uint16
	reasonLen                         uint32
	res                               result
	resMu                             sync.Mutex
	emitOnce                          sync.Once
	hwnd                              uintptr
	broadcastMsg                      uint32
	qesAnswers                        = [3]uintptr{1, 0, 0}
	qesCount, esCount                 int
	qesSeen                           [3]sessionAnswer
	esSeen                            [2]sessionAnswer
	broadcastSeen                     bool
	wndProcCB                         = syscall.NewCallback(wndProc)
	reasonText, className, broadcastN *uint16
)

func call(fn uintptr, args ...uintptr) (uintptr, syscall.Errno) {
	r, _, e := syscall.SyscallN(fn, args...)
	return r, e
}

func qpc() int64 {
	call(pQPC, uintptr(unsafe.Pointer(&qpcVal)))
	return qpcVal
}

func us(a, b int64) float64 { return float64(b-a) * 1e6 / float64(qpcFreq) }

// msSinceStart is the time since the process was created.
func msSinceStart() float64 {
	call(pGetProcessTimes, mustCall(pGetCurrentProcess), uintptr(unsafe.Pointer(&ftCreate)),
		uintptr(unsafe.Pointer(&ftExit)), uintptr(unsafe.Pointer(&ftKern)), uintptr(unsafe.Pointer(&ftUser)))
	call(pPreciseTime, uintptr(unsafe.Pointer(&ftNow)))
	return float64(ftNow-ftCreate) / 1e4
}

func mustCall(fn uintptr, args ...uintptr) uintptr {
	r, _ := call(fn, args...)
	return r
}

func hr(r uintptr) string { return fmt.Sprintf("0x%08X", uint32(r)) }

func fail(format string, a ...any) {
	resMu.Lock()
	res.Errors = append(res.Errors, fmt.Sprintf(format, a...))
	resMu.Unlock()
}

func emit(code int) {
	emitOnce.Do(func() {
		resMu.Lock()
		b, _ := json.MarshalIndent(&res, "", "  ")
		resMu.Unlock()
		os.Stdout.Write(append(b, '\n'))
		os.Exit(code)
	})
}

func wndProc(h, m, wp, lp uintptr) uintptr {
	switch {
	case m == wmQueryEndSession:
		if qesCount < len(qesSeen) {
			a := &qesSeen[qesCount]
			a.Received, a.LParam, a.Answered = true, fmt.Sprintf("0x%X", lp), int(qesAnswers[qesCount])
			qesCount++
			return uintptr(a.Answered)
		}
		return 1
	case m == wmEndSession:
		if esCount < len(esSeen) {
			esSeen[esCount].Received, esSeen[esCount].LParam = true, fmt.Sprintf("0x%X", lp)
			esCount++
		}
		return 0 // handled; the app does not exit before replying
	case m == wmProbePing:
		return pingReply
	case m == wmProbeBlock:
		blockReason()
		return 0
	case m == wmProbeUnblock:
		call(pSBRDestroy, h)
		return 0
	case m == wmProbeQuit:
		call(pPostQuitMessage, 0)
		return 0
	case broadcastMsg != 0 && m == uintptr(broadcastMsg):
		broadcastSeen = true
		return 0
	}
	r, _ := call(pDefWindowProc, h, m, wp, lp)
	return r
}

func main() {
	timeout := flag.Duration("timeout", 30*time.Second, "give up (exit 1) after this long")
	flag.Parse()
	res.Probe, res.GOARCH, res.Errors = "P1", runtime.GOARCH, []string{}
	time.AfterFunc(*timeout, func() {
		resMu.Lock()
		res.TimedOut = true
		resMu.Unlock()
		emit(1)
	})
	call(pQPF, uintptr(unsafe.Pointer(&qpcFreq)))
	osvi.size = uint32(unsafe.Sizeof(osvi))
	call(pRtlGetVersion, uintptr(unsafe.Pointer(&osvi)))
	res.Windows = fmt.Sprintf("%d.%d.%d", osvi.major, osvi.minor, osvi.build)

	// OleInitialize: the cold load and call, then a second call (S_FALSE).
	res.MsSinceStartAtOleInit = msSinceStart()
	t0 := qpc()
	ole32 := win32.LoadSystem("ole32.dll")
	t1 := qpc()
	pOleInit, pOleUninit := win32.Proc(ole32, "OleInitialize"), win32.Proc(ole32, "OleUninitialize")
	if pOleInit == 0 {
		fail("ole32.dll or OleInitialize missing")
		emit(1)
	}
	r, _ := call(pOleInit, 0)
	t2 := qpc()
	res.MsSinceStartAfterOle = msSinceStart()
	r2, _ := call(pOleInit, 0)
	t3 := qpc()
	res.Ole32LoadUs, res.OleInitColdUs, res.OleInitSecondUs = us(t0, t1), us(t1, t2), us(t2, t3)
	res.OleInitHR, res.OleInitSecondHR = hr(r), hr(r2)
	defer call(pOleUninit)
	defer call(pOleUninit)

	// The hidden app window (D68).
	className, _ = syscall.UTF16PtrFromString("gnuiProbeP1App")
	inst := mustCall(pGetModuleHandle, 0)
	wc = wndClassEx{wndProc: wndProcCB, instance: inst, className: className}
	wc.size = uint32(unsafe.Sizeof(wc))
	if a, e := call(pRegisterClassEx, uintptr(unsafe.Pointer(&wc))); a == 0 {
		fail("RegisterClassExW: %v", e)
		emit(1)
	}
	h, e := call(pCreateWindowEx, wsExToolWindow, uintptr(unsafe.Pointer(className)), 0, wsPopup,
		0, 0, 0, 0, 0, 0, inst, 0)
	if h == 0 {
		fail("CreateWindowExW: %v", e)
		emit(1)
	}
	hwnd = h
	res.HiddenWindow.Created = true
	res.HiddenWindow.Visible = mustCall(pIsWindowVisible, hwnd) != 0

	tc, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	res.TaskbarCreatedMsg = uint32(mustCall(pRegisterWindowMsg, uintptr(unsafe.Pointer(tc))))
	broadcastN, _ = syscall.UTF16PtrFromString("gnui-probe-p1-broadcast")
	broadcastMsg = uint32(mustCall(pRegisterWindowMsg, uintptr(unsafe.Pointer(broadcastN))))

	go sender()

	for {
		r, _ := call(pGetMessage, uintptr(unsafe.Pointer(&loopMsg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		call(pTranslateMessage, uintptr(unsafe.Pointer(&loopMsg)))
		call(pDispatchMessage, uintptr(unsafe.Pointer(&loopMsg)))
	}
	call(pDestroyWindow, hwnd)

	resMu.Lock()
	for i := range qesSeen {
		qesSeen[i].Sent, qesSeen[i].Result, qesSeen[i].Alive = sentQ[i].Sent, sentQ[i].Result, sentQ[i].Alive
	}
	res.QueryEndSession.Allow, res.QueryEndSession.Refuse, res.QueryEndSession.RefuseBlocked = qesSeen[0], qesSeen[1], qesSeen[2]
	for i := range esSeen {
		esSeen[i].Sent, esSeen[i].Result, esSeen[i].Alive = sentE[i].Sent, sentE[i].Result, sentE[i].Alive
	}
	res.EndSession.False, res.EndSession.True = esSeen[0], esSeen[1]
	res.BroadcastReceived = broadcastSeen
	resMu.Unlock()
	emit(0)
}

// sentQ and sentE are what the sender saw for each message, merged with
// what the window procedure saw once the loop ends (the procedure runs on
// the main thread, the sender on its own).
var (
	sentQ [3]sessionAnswer
	sentE [2]sessionAnswer
)

// send delivers a message to the window from this thread and reports
// whether it was answered and what came back.
func send(m, wp, lp uintptr) (bool, uintptr) {
	r, _ := call(pSendMessageTimeout, hwnd, m, wp, lp, smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&sendRes)))
	return r != 0, sendRes
}

func alive() bool {
	if mustCall(pIsWindow, hwnd) == 0 {
		return false
	}
	ok, v := send(wmProbePing, 0, 0)
	return ok && v == pingReply
}

// sender plays the session manager from another OS thread.
func sender() {
	runtime.LockOSThread()
	for i := 0; i < 2; i++ {
		ok, v := send(wmQueryEndSession, 0, endSessionLogoff)
		sentQ[i] = sessionAnswer{Sent: ok, Result: uint64(v), Alive: alive()}
	}
	// A block reason set, then a refusal (D68's "otherwise" path). The
	// reason is set by the window's thread: ShutdownBlockReasonCreate fails
	// with ERROR_ACCESS_DENIED from any other (WinUser.h docs).
	send(wmProbeBlock, 0, 0)
	ok, v := send(wmQueryEndSession, 0, endSessionLogoff)
	sentQ[2] = sessionAnswer{Sent: ok, Result: uint64(v), Alive: alive()}

	for i, wp := range []uintptr{0, 1} {
		ok, v := send(wmEndSession, wp, endSessionLogoff)
		sentE[i] = sessionAnswer{Sent: ok, Result: uint64(v), Alive: alive()}
	}
	send(wmProbeUnblock, 0, 0)

	// A broadcast reaches top-level windows only (not HWND_MESSAGE ones).
	call(pPostMessage, hwndBroadcast, uintptr(broadcastMsg), 0, 0)
	time.Sleep(500 * time.Millisecond)
	call(pPostMessage, hwnd, wmProbeQuit, 0, 0)
}

// blockReason sets and reads back the window's shutdown block reason (on
// the main thread, from the window procedure).
func blockReason() {
	reasonText, _ = syscall.UTF16PtrFromString("Saving the recording")
	r, e := call(pSBRCreate, hwnd, uintptr(unsafe.Pointer(reasonText)))
	resMu.Lock()
	defer resMu.Unlock()
	if r == 0 {
		res.ShutdownBlockReason.Err = e.Error()
		return
	}
	res.ShutdownBlockReason.Created = true
	reasonLen = uint32(len(reasonBuf))
	if q, _ := call(pSBRQuery, hwnd, uintptr(unsafe.Pointer(&reasonBuf[0])), uintptr(unsafe.Pointer(&reasonLen))); q != 0 {
		res.ShutdownBlockReason.ReadBack = syscall.UTF16ToString(reasonBuf[:])
	}
}
