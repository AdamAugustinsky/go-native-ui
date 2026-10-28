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

// WinUser.h, shellapi.h.
const (
	wmKeyDown       = 0x0100
	wmLButtonDown   = 0x0201
	wmHotkey        = 0x0312
	wmApp           = 0x8000
	wmTray          = wmApp + 1
	wsPopup         = 0x80000000
	wsVisible       = 0x10000000
	wsOverlappedWin = 0x00CF0000
	wsExToolWindow  = 0x00000080
	swShowNormal    = 1
	pmRemove        = 0x0001
	qsAllInput      = 0x04FF
	inputMouse      = 0
	inputKeyboard   = 1
	keyEventUp      = 0x0002
	mouseLeftDown   = 0x0002
	mouseLeftUp     = 0x0004
	modAlt          = 0x0001
	modControl      = 0x0002
	modShift        = 0x0004
	modNoRepeat     = 0x4000
	vkShift         = 0x10
	vkControl       = 0x11
	vkMenu          = 0x12
	vkA             = 0x41
	vkF11           = 0x7A
	vkF12           = 0x7B
	desktopReadObj  = 0x0001
	nimAdd          = 0
	nimDelete       = 2
	nimSetVersion   = 4
	nifMessage      = 0x01
	nifIcon         = 0x02
	nifTip          = 0x04
	nifGUID         = 0x20
	nifShowTip      = 0x80
	notifyVersion4  = 4
	idiApplication  = 32512
	errHotkeyTaken  = 1409 // ERROR_HOTKEY_ALREADY_REGISTERED
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

// input is INPUT (WinUser.h): a DWORD type and, at offset 8, the union,
// whose largest member is MOUSEINPUT (32 bytes on 64-bit): 40 bytes.
type input struct {
	typ uint32
	_   uint32
	u   [32]byte
}

// keybdInput is KEYBDINPUT (24 bytes on 64-bit).
type keybdInput struct {
	vk, scan    uint16
	flags, time uint32
	extra       uintptr
}

// mouseInput is MOUSEINPUT (32 bytes on 64-bit).
type mouseInput struct {
	dx, dy                 int32
	data, flags, timestamp uint32
	extra                  uintptr
}

// notifyIconData is NOTIFYICONDATAW (shellapi.h, the Vista+ layout with
// guidItem and hBalloonIcon): 976 bytes on 64-bit. uVersion shares its
// union with uTimeout. Verify the size on the first CI run (Size below).
type notifyIconData struct {
	size        uint32
	hwnd        uintptr
	id          uint32
	flags       uint32
	callbackMsg uint32
	icon        uintptr
	tip         [128]uint16
	state       uint32
	stateMask   uint32
	info        [256]uint16
	version     uint32
	infoTitle   [64]uint16
	infoFlags   uint32
	guid        win32.GUID
	balloonIcon uintptr
}

type foreground struct {
	Before           string `json:"before"` // the class of the foreground window at start
	SetForeground1   bool   `json:"setForegroundFirst"`
	IsOursAfter1     bool   `json:"isOursAfterFirst"`
	AltTapTried      bool   `json:"altTapTried"`
	SetForeground2   bool   `json:"setForegroundSecond"`
	IsOurs           bool   `json:"isOurs"`
	After            string `json:"after"`
	FocusIsOurs      bool   `json:"focusIsOurs"`
	ForegroundIsNull bool   `json:"foregroundIsNull"`
}

type sendInput struct {
	KeySent       uint32 `json:"keyEventsSent"` // SendInput's count (0 is refused)
	KeyErr        string `json:"keyErr,omitempty"`
	KeyDowns      int    `json:"wmKeyDown"`
	CursorSet     bool   `json:"setCursorPos"`
	ClickSent     uint32 `json:"clickEventsSent"`
	ClickErr      string `json:"clickErr,omitempty"`
	LButtonDowns  int    `json:"wmLButtonDown"`
	ReachesWindow bool   `json:"reachesWindow"` // both arrived
}

type hotkey struct {
	Chord          string `json:"chord"`
	Registered     bool   `json:"registered"`
	RegisterErr    string `json:"registerErr,omitempty"`
	F12Err         string `json:"f12Err,omitempty"` // why F12 was refused, if it was
	SecondFails    bool   `json:"secondRegistrationFails"`
	SecondErr      uint32 `json:"secondErr"`
	SecondIsTaken  bool   `json:"secondErrIsAlreadyRegistered"`
	ChordSent      uint32 `json:"chordEventsSent"`
	ChordErr       string `json:"chordErr,omitempty"`
	WMHotkey       int    `json:"wmHotkey"`
	DeliveredByIDs []int  `json:"hotkeyIds"`
}

type tray struct {
	StructSize   int    `json:"structSize"`
	StaleDeleted bool   `json:"staleDeleted"`
	AddGUID      bool   `json:"addWithGUID"`
	AddGUIDErr   string `json:"addWithGUIDErr,omitempty"`
	AddID        bool   `json:"addWithIDOnly"`
	AddIDErr     string `json:"addWithIDOnlyErr,omitempty"`
	SetVersion4  bool   `json:"setVersion4"`
	Delete       bool   `json:"delete"`
}

type result struct {
	Probe          string     `json:"probe"`
	GOARCH         string     `json:"goarch"`
	SessionID      uint32     `json:"sessionId"`
	InputDesktop   bool       `json:"inputDesktop"`
	InputDeskErr   string     `json:"inputDesktopErr,omitempty"`
	TaskbarPresent bool       `json:"taskbarPresent"`
	Foreground     foreground `json:"foreground"`
	SendInput      *sendInput `json:"sendInput,omitempty"`
	Hotkey         *hotkey    `json:"hotkey,omitempty"`
	Tray           *tray      `json:"tray,omitempty"`
	TimedOut       bool       `json:"timedOut"`
	Errors         []string   `json:"errors"`
}

var (
	user32   = win32.LoadSystem("user32.dll")
	kernel32 = win32.LoadSystem("kernel32.dll")
	shell32  = win32.LoadSystem("shell32.dll")

	pRegisterClassEx   = win32.Proc(user32, "RegisterClassExW")
	pCreateWindowEx    = win32.Proc(user32, "CreateWindowExW")
	pDefWindowProc     = win32.Proc(user32, "DefWindowProcW")
	pPeekMessage       = win32.Proc(user32, "PeekMessageW")
	pTranslateMessage  = win32.Proc(user32, "TranslateMessage")
	pDispatchMessage   = win32.Proc(user32, "DispatchMessageW")
	pMsgWait           = win32.Proc(user32, "MsgWaitForMultipleObjects")
	pShowWindow        = win32.Proc(user32, "ShowWindow")
	pUpdateWindow      = win32.Proc(user32, "UpdateWindow")
	pDestroyWindow     = win32.Proc(user32, "DestroyWindow")
	pSetForeground     = win32.Proc(user32, "SetForegroundWindow")
	pGetForeground     = win32.Proc(user32, "GetForegroundWindow")
	pSetFocus          = win32.Proc(user32, "SetFocus")
	pGetFocus          = win32.Proc(user32, "GetFocus")
	pGetClassName      = win32.Proc(user32, "GetClassNameW")
	pFindWindow        = win32.Proc(user32, "FindWindowW")
	pSendInput         = win32.Proc(user32, "SendInput")
	pMapVirtualKey     = win32.Proc(user32, "MapVirtualKeyW")
	pSetCursorPos      = win32.Proc(user32, "SetCursorPos")
	pGetWindowRect     = win32.Proc(user32, "GetWindowRect")
	pRegisterHotKey    = win32.Proc(user32, "RegisterHotKey")
	pUnregisterHotKey  = win32.Proc(user32, "UnregisterHotKey")
	pOpenInputDesktop  = win32.Proc(user32, "OpenInputDesktop")
	pCloseDesktop      = win32.Proc(user32, "CloseDesktop")
	pLoadIcon          = win32.Proc(user32, "LoadIconW")
	pGetModuleHandle   = win32.Proc(kernel32, "GetModuleHandleW")
	pGetCurrentProcess = win32.Proc(kernel32, "GetCurrentProcessId")
	pPidToSession      = win32.Proc(kernel32, "ProcessIdToSessionId")
	pShellNotifyIcon   = win32.Proc(shell32, "Shell_NotifyIconW")
)

// probeGUID is the tray icon's GUID ({D0C2C5A1-7E3B-4F0E-9A51-6B2F8E0C7A71}).
var probeGUID = win32.GUID{Data1: 0xD0C2C5A1, Data2: 0x7E3B, Data3: 0x4F0E, Data4: [8]byte{0x9A, 0x51, 0x6B, 0x2F, 0x8E, 0x0C, 0x7A, 0x71}}

// Out parameters and counters live in package variables.
var (
	wc         wndClassEx
	loopMsg    msg
	inputs     [8]input
	nid        notifyIconData
	rect       [4]int32
	sessionID  uint32
	classBuf   [256]uint16
	res        result
	resMu      sync.Mutex
	emitOnce   sync.Once
	appHwnd    uintptr
	winHwnd    uintptr
	keyDowns   int
	lbDowns    int
	hotkeyIDs  []int
	wndProcCB  = syscall.NewCallback(wndProc)
	appClass   *uint16
	winClass   *uint16
	winTitle   *uint16
	trayClassN *uint16
)

func call(fn uintptr, args ...uintptr) (uintptr, syscall.Errno) {
	r, _, e := syscall.SyscallN(fn, args...)
	return r, e
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
	case h == winHwnd && m == wmKeyDown && wp == vkA:
		keyDowns++
	case h == winHwnd && m == wmLButtonDown:
		lbDowns++
	case m == wmHotkey:
		hotkeyIDs = append(hotkeyIDs, int(wp))
	}
	r, _ := call(pDefWindowProc, h, m, wp, lp)
	return r
}

// pump dispatches messages for up to d, or until done reports true.
func pump(d time.Duration, done func() bool) {
	end := time.Now().Add(d)
	for {
		for {
			r, _ := call(pPeekMessage, uintptr(unsafe.Pointer(&loopMsg)), 0, 0, 0, pmRemove)
			if r == 0 {
				break
			}
			call(pTranslateMessage, uintptr(unsafe.Pointer(&loopMsg)))
			call(pDispatchMessage, uintptr(unsafe.Pointer(&loopMsg)))
		}
		left := time.Until(end)
		if (done != nil && done()) || left <= 0 {
			return
		}
		call(pMsgWait, 0, 0, 0, uintptr(min(left, 50*time.Millisecond)/time.Millisecond), qsAllInput)
	}
}

func className(h uintptr) string {
	if h == 0 {
		return ""
	}
	n, _ := call(pGetClassName, h, uintptr(unsafe.Pointer(&classBuf[0])), uintptr(len(classBuf)))
	return syscall.UTF16ToString(classBuf[:n])
}

func key(i int, vk uint16, up bool) {
	scan, _ := call(pMapVirtualKey, uintptr(vk), 0) // MAPVK_VK_TO_VSC
	k := keybdInput{vk: vk, scan: uint16(scan)}
	if up {
		k.flags = keyEventUp
	}
	inputs[i] = input{typ: inputKeyboard}
	*(*keybdInput)(unsafe.Pointer(&inputs[i].u)) = k
}

func mouse(i int, flags uint32) {
	inputs[i] = input{typ: inputMouse}
	*(*mouseInput)(unsafe.Pointer(&inputs[i].u)) = mouseInput{flags: flags}
}

func send(n int) (uint32, string) {
	r, e := call(pSendInput, uintptr(n), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(input{}))
	if uint32(r) != uint32(n) {
		return uint32(r), e.Error()
	}
	return uint32(r), ""
}

func newWindow(exStyle, style uintptr, class, title *uint16, x, y, w, h uintptr) uintptr {
	r, e := call(pCreateWindowEx, exStyle, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), style,
		x, y, w, h, 0, 0, wc.instance, 0)
	if r == 0 {
		res.Errors = append(res.Errors, "CreateWindowExW: "+e.Error())
	}
	return r
}

func main() {
	timeout := flag.Duration("timeout", 60*time.Second, "give up (exit 1) after this long")
	wait := flag.Duration("wait", 2*time.Second, "how long to wait for each injected input to arrive")
	doInput := flag.Bool("input", true, "probe SendInput into our window")
	doHotkey := flag.Bool("hotkey", true, "probe RegisterHotKey")
	doTray := flag.Bool("tray", true, "probe Shell_NotifyIconW")
	flag.Parse()
	res.Probe, res.GOARCH, res.Errors = "P7", runtime.GOARCH, []string{}
	time.AfterFunc(*timeout, func() {
		resMu.Lock()
		res.TimedOut = true
		resMu.Unlock()
		emit(1)
	})

	pid, _ := call(pGetCurrentProcess)
	if r, _ := call(pPidToSession, pid, uintptr(unsafe.Pointer(&sessionID))); r != 0 {
		res.SessionID = sessionID
	}
	if d, e := call(pOpenInputDesktop, 0, 0, desktopReadObj); d != 0 {
		res.InputDesktop = true
		call(pCloseDesktop, d)
	} else {
		res.InputDeskErr = e.Error()
	}
	trayClassN, _ = syscall.UTF16PtrFromString("Shell_TrayWnd")
	tw, _ := call(pFindWindow, uintptr(unsafe.Pointer(trayClassN)), 0)
	res.TaskbarPresent = tw != 0

	// The windows: one class, a visible window and the hidden app window.
	appClass, _ = syscall.UTF16PtrFromString("gnuiProbeP7")
	winTitle, _ = syscall.UTF16PtrFromString("probe-p7")
	winClass = appClass
	wc.instance, _ = call(pGetModuleHandle, 0)
	wc.wndProc, wc.className = wndProcCB, appClass
	wc.size = uint32(unsafe.Sizeof(wc))
	if a, e := call(pRegisterClassEx, uintptr(unsafe.Pointer(&wc))); a == 0 {
		res.Errors = append(res.Errors, "RegisterClassExW: "+e.Error())
		emit(1)
	}
	appHwnd = newWindow(wsExToolWindow, wsPopup, appClass, nil, 0, 0, 0, 0)
	winHwnd = newWindow(0, wsOverlappedWin|wsVisible, winClass, winTitle, 100, 100, 480, 320)
	if appHwnd == 0 || winHwnd == 0 {
		emit(1)
	}
	call(pShowWindow, winHwnd, swShowNormal)
	call(pUpdateWindow, winHwnd)
	pump(200*time.Millisecond, nil)

	fg := &res.Foreground
	before, _ := call(pGetForeground)
	fg.Before = className(before)
	r, _ := call(pSetForeground, winHwnd)
	fg.SetForeground1 = r != 0
	pump(100*time.Millisecond, nil)
	now, _ := call(pGetForeground)
	fg.IsOursAfter1 = now == winHwnd
	if !fg.IsOursAfter1 {
		// An injected Alt tap makes this process the last input's owner,
		// which lifts the foreground lock (a known workaround).
		fg.AltTapTried = true
		key(0, vkMenu, false)
		key(1, vkMenu, true)
		send(2)
		pump(50*time.Millisecond, nil)
		r, _ = call(pSetForeground, winHwnd)
		fg.SetForeground2 = r != 0
		pump(100*time.Millisecond, nil)
		now, _ = call(pGetForeground)
	}
	fg.IsOurs, fg.After, fg.ForegroundIsNull = now == winHwnd, className(now), now == 0
	call(pSetFocus, winHwnd)
	f, _ := call(pGetFocus)
	fg.FocusIsOurs = f == winHwnd

	if *doInput {
		probeInput(*wait)
	}
	if *doHotkey {
		probeHotkey(*wait)
	}
	if *doTray {
		probeTray()
	}
	call(pDestroyWindow, winHwnd)
	call(pDestroyWindow, appHwnd)
	emit(0)
}

func probeInput(wait time.Duration) {
	s := &sendInput{}
	res.SendInput = s
	key(0, vkA, false)
	key(1, vkA, true)
	s.KeySent, s.KeyErr = send(2)
	pump(wait, func() bool { return keyDowns > 0 })
	s.KeyDowns = keyDowns

	call(pGetWindowRect, winHwnd, uintptr(unsafe.Pointer(&rect)))
	cx, cy := (rect[0]+rect[2])/2, (rect[1]+rect[3])/2
	r, _ := call(pSetCursorPos, uintptr(cx), uintptr(cy))
	s.CursorSet = r != 0
	mouse(0, mouseLeftDown)
	mouse(1, mouseLeftUp)
	s.ClickSent, s.ClickErr = send(2)
	pump(wait, func() bool { return lbDowns > 0 })
	s.LButtonDowns = lbDowns
	s.ReachesWindow = s.KeyDowns > 0 && s.LButtonDowns > 0
}

func probeHotkey(wait time.Duration) {
	h := &hotkey{}
	res.Hotkey = h
	mods := uintptr(modControl | modAlt | modShift | modNoRepeat)
	vk := uint16(vkF12)
	h.Chord = "Ctrl+Alt+Shift+F12"
	r, e := call(pRegisterHotKey, appHwnd, 1, mods, uintptr(vk))
	if r == 0 {
		// The SDK reserves F12 for debuggers (RegisterHotKey's remarks).
		h.F12Err = e.Error()
		vk, h.Chord = vkF11, "Ctrl+Alt+Shift+F11"
		r, e = call(pRegisterHotKey, appHwnd, 1, mods, uintptr(vk))
	}
	h.Registered = r != 0
	if !h.Registered {
		h.RegisterErr = e.Error()
		return
	}
	defer call(pUnregisterHotKey, appHwnd, 1)
	r, e = call(pRegisterHotKey, appHwnd, 2, mods, uintptr(vk))
	h.SecondFails, h.SecondErr = r == 0, uint32(e)
	h.SecondIsTaken = r == 0 && uint32(e) == errHotkeyTaken
	if r != 0 {
		call(pUnregisterHotKey, appHwnd, 2)
	}
	key(0, vkControl, false)
	key(1, vkMenu, false)
	key(2, vkShift, false)
	key(3, vk, false)
	key(4, vk, true)
	key(5, vkShift, true)
	key(6, vkMenu, true)
	key(7, vkControl, true)
	h.ChordSent, h.ChordErr = send(8)
	pump(wait, func() bool { return len(hotkeyIDs) > 0 })
	h.WMHotkey, h.DeliveredByIDs = len(hotkeyIDs), append([]int{}, hotkeyIDs...)
}

func probeTray() {
	t := &tray{StructSize: int(unsafe.Sizeof(nid))}
	res.Tray = t
	icon, _ := call(pLoadIcon, 0, idiApplication)
	fill := func(flags uint32) {
		nid = notifyIconData{hwnd: appHwnd, id: 1, flags: flags, callbackMsg: wmTray, icon: icon, guid: probeGUID}
		nid.size = uint32(unsafe.Sizeof(nid))
		copy(nid.tip[:], syscall.StringToUTF16("probe-p7"))
	}
	notify := func(m uintptr) (bool, string) {
		r, e := call(pShellNotifyIcon, m, uintptr(unsafe.Pointer(&nid)))
		if r == 0 {
			return false, fmt.Sprintf("%v (%d)", e, uint32(e))
		}
		return true, ""
	}
	// A stale icon with our GUID (a crash) is removed first.
	fill(nifGUID)
	t.StaleDeleted, _ = notify(nimDelete)

	fill(nifMessage | nifIcon | nifTip | nifShowTip | nifGUID)
	t.AddGUID, t.AddGUIDErr = notify(nimAdd)
	withGUID := t.AddGUID
	if !t.AddGUID {
		fill(nifMessage | nifIcon | nifTip | nifShowTip)
		t.AddID, t.AddIDErr = notify(nimAdd)
	}
	if !t.AddGUID && !t.AddID {
		return
	}
	nid.version = notifyVersion4
	t.SetVersion4, _ = notify(nimSetVersion)
	pump(100*time.Millisecond, nil)
	if withGUID {
		fill(nifGUID)
	} else {
		fill(0)
	}
	t.Delete, _ = notify(nimDelete)
}
