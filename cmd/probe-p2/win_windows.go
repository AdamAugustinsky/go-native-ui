//go:build windows

package main

import (
	"encoding/binary"
	"errors"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Win32 plumbing of the probe: three window classes, the message pump and
// GDI reads of the screen and of PrintWindow. Constants are from winuser.h
// and wingdi.h.

var (
	user32 = win32.LoadSystem("user32.dll")
	gdi32  = win32.LoadSystem("gdi32.dll")
	k32    = win32.LoadSystem("kernel32.dll")
	dwmapi = win32.LoadSystem("dwmapi.dll")

	pRegisterClassExW     = win32.Proc(user32, "RegisterClassExW")
	pCreateWindowExW      = win32.Proc(user32, "CreateWindowExW")
	pDefWindowProcW       = win32.Proc(user32, "DefWindowProcW")
	pPeekMessageW         = win32.Proc(user32, "PeekMessageW")
	pTranslateMessage     = win32.Proc(user32, "TranslateMessage")
	pDispatchMessageW     = win32.Proc(user32, "DispatchMessageW")
	pShowWindow           = win32.Proc(user32, "ShowWindow")
	pUpdateWindow         = win32.Proc(user32, "UpdateWindow")
	pSetForegroundWindow  = win32.Proc(user32, "SetForegroundWindow")
	pDestroyWindow        = win32.Proc(user32, "DestroyWindow")
	pClientToScreen       = win32.Proc(user32, "ClientToScreen")
	pWindowFromPoint      = win32.Proc(user32, "WindowFromPoint")
	pBeginDeferWindowPos  = win32.Proc(user32, "BeginDeferWindowPos")
	pDeferWindowPos       = win32.Proc(user32, "DeferWindowPos")
	pEndDeferWindowPos    = win32.Proc(user32, "EndDeferWindowPos")
	pSetWindowPos         = win32.Proc(user32, "SetWindowPos")
	pGetDC                = win32.Proc(user32, "GetDC")
	pReleaseDC            = win32.Proc(user32, "ReleaseDC")
	pPrintWindow          = win32.Proc(user32, "PrintWindow")
	pSetDpiAwarenessCtx   = win32.Proc(user32, "SetProcessDpiAwarenessContext")
	pGetModuleHandleW     = win32.Proc(k32, "GetModuleHandleW")
	pCreateSolidBrush     = win32.Proc(gdi32, "CreateSolidBrush")
	pCreateCompatibleDC   = win32.Proc(gdi32, "CreateCompatibleDC")
	pCreateDIBSection     = win32.Proc(gdi32, "CreateDIBSection")
	pSelectObject         = win32.Proc(gdi32, "SelectObject")
	pBitBlt               = win32.Proc(gdi32, "BitBlt")
	pGdiFlush             = win32.Proc(gdi32, "GdiFlush")
	pDwmFlush             = win32.Proc(dwmapi, "DwmFlush")
	pDwmGetCompositionTim = win32.Proc(dwmapi, "DwmGetCompositionTimingInfo")

	wndProcCB = syscall.NewCallback(wndProc)
)

const (
	wsPopup        = 0x80000000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsClipChildren = 0x02000000
	wsClipSiblings = 0x04000000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080

	wsExNoRedirectionBitmap = 0x00200000

	wmEraseBkgnd = 0x0014
	pmRemove     = 0x0001
	swShow       = 5

	swpNoSize     = 0x0001
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	srcCopy           = 0x00CC0020
	captureBlt        = 0x40000000
	pwRenderFull      = 0x00000002  // PW_RENDERFULLCONTENT (Windows 8.1+)
	dpiPerMonitorV2   = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (HANDLE)-4
	colorGreen        = 0x0000FF00  // COLORREF 0x00BBGGRR
	colorGrey         = 0x00808080
	topW, topH        = 400, 300
	clipX, clipY      = 220, 50 // the container, in the top-level's client
	clipW, clipH      = 160, 200
	childX            = 10 // the child, in the container
	childW, childH    = 140, 20
	bandX0, bandX1    = 20, 180 // the swap chain's band, in the client
	sampleBand        = 100     // the columns read
	sampleChild       = clipX + childX + childW/2
	containerSampleY  = clipY + clipH - 10
	swapChainSampleY  = 150
	childRestY        = -10 // at rest the child pokes 10 px above the container
	childRestInsideY  = clipY + 5
	childRestOutsideY = clipY - 5
)

// wndClassEx is WNDCLASSEXW (winuser.h).
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

// msg is MSG (winuser.h).
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      [2]int32
	private uint32
}

// bitmapInfo is BITMAPINFO with one RGBQUAD (wingdi.h).
type bitmapInfo struct {
	size          uint32
	width, height int32
	planes, bits  uint16
	compression   uint32
	sizeImage     uint32
	xppm, yppm    int32
	clrUsed       uint32
	clrImportant  uint32
	colors        [1]uint32
}

// wins are the probe's windows and GDI objects; every out-parameter lives
// here (a package variable), as internal/com's convention requires.
var wins struct {
	inst              uintptr
	top, clip, child  uintptr
	wc                wndClassEx
	m                 msg
	pt                [2]int32
	bmi               bitmapInfo
	bits              uintptr
	memDC, dib        uintptr
	topName, clipName *uint16
	childName         *uint16
}

//go:uintptrescapes
func call(fn uintptr, args ...uintptr) uintptr { return win32.Call(fn, args...) }

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	if m == wmEraseBkgnd && hwnd == wins.top {
		return 1 // the swap chain paints the top-level
	}
	return call(pDefWindowProcW, hwnd, m, wp, lp)
}

func register(name *uint16, brush uintptr) error {
	wins.wc = wndClassEx{wndProc: wndProcCB, instance: wins.inst, background: brush, className: name}
	wins.wc.size = uint32(unsafe.Sizeof(wins.wc))
	if call(pRegisterClassExW, uintptr(unsafe.Pointer(&wins.wc)))&0xFFFF == 0 {
		return errors.New("RegisterClassExW failed")
	}
	return nil
}

func createWindow(ex, style uintptr, class *uint16, x, y, w, h int, parent uintptr) uintptr {
	return call(pCreateWindowExW, ex, uintptr(unsafe.Pointer(class)), 0, style,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), parent, 0, wins.inst, 0)
}

// makeWindows creates the top-level window, the container and the child.
func makeWindows() error {
	if pSetDpiAwarenessCtx != 0 {
		call(pSetDpiAwarenessCtx, dpiPerMonitorV2)
	}
	wins.inst = call(pGetModuleHandleW, 0)
	wins.topName, _ = syscall.UTF16PtrFromString("GNUIProbeP2Top")
	wins.clipName, _ = syscall.UTF16PtrFromString("GNUIProbeP2Clip")
	wins.childName, _ = syscall.UTF16PtrFromString("GNUIProbeP2Child")
	if err := register(wins.topName, 0); err != nil {
		return err
	}
	if err := register(wins.clipName, call(pCreateSolidBrush, colorGrey)); err != nil {
		return err
	}
	if err := register(wins.childName, call(pCreateSolidBrush, colorGreen)); err != nil {
		return err
	}
	ex := uintptr(wsExTopmost | wsExToolWindow)
	if *noRedirect {
		ex |= wsExNoRedirectionBitmap
	}
	wins.top = createWindow(ex, wsPopup|wsClipChildren, wins.topName, 100, 100, topW, topH, 0)
	if wins.top == 0 {
		return errors.New("CreateWindowExW (top-level) failed")
	}
	wins.clip = createWindow(0, wsChild|wsVisible|wsClipChildren|wsClipSiblings, wins.clipName, clipX, clipY, clipW, clipH, wins.top)
	wins.child = createWindow(0, wsChild|wsVisible|wsClipSiblings, wins.childName, childX, childRestY, childW, childH, wins.clip)
	if wins.clip == 0 || wins.child == 0 {
		return errors.New("CreateWindowExW (child) failed")
	}
	call(pShowWindow, wins.top, swShow)
	call(pUpdateWindow, wins.top)
	call(pSetForegroundWindow, wins.top)
	return nil
}

func pump() {
	for win32.Bool(call(pPeekMessageW, uintptr(unsafe.Pointer(&wins.m)), 0, 0, 0, pmRemove)) {
		call(pTranslateMessage, uintptr(unsafe.Pointer(&wins.m)))
		call(pDispatchMessageW, uintptr(unsafe.Pointer(&wins.m)))
	}
}

// moveChild puts the child's top at client y in one DeferWindowPos batch.
func moveChild(y int) {
	h := call(pBeginDeferWindowPos, 1)
	if h == 0 {
		call(pSetWindowPos, wins.child, 0, childX, uintptr(y-clipY), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
		return
	}
	h = call(pDeferWindowPos, h, wins.child, 0, childX, uintptr(y-clipY), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	if h != 0 {
		call(pEndDeferWindowPos, h)
	}
}

// clientOrigin is the client area's top left on the screen.
func clientOrigin() (int, int) {
	wins.pt = [2]int32{0, 0}
	call(pClientToScreen, wins.top, uintptr(unsafe.Pointer(&wins.pt)))
	return int(wins.pt[0]), int(wins.pt[1])
}

// windowAt is WindowFromPoint at a client point (POINT by value: one
// register, x in the low half).
func windowAt(x, y int) uintptr {
	ox, oy := clientOrigin()
	return call(pWindowFromPoint, uintptr(uint32(int32(ox+x)))|uintptr(uint32(int32(oy+y)))<<32)
}

// makeDIB makes the top-down 32-bit DIB the reads land in.
func makeDIB() error {
	screen := call(pGetDC, 0)
	defer call(pReleaseDC, 0, screen)
	wins.memDC = call(pCreateCompatibleDC, screen)
	wins.bmi = bitmapInfo{width: topW, height: -topH, planes: 1, bits: 32}
	wins.bmi.size = 40
	wins.dib = call(pCreateDIBSection, wins.memDC, uintptr(unsafe.Pointer(&wins.bmi)), 0, uintptr(unsafe.Pointer(&wins.bits)), 0, 0)
	if wins.memDC == 0 || wins.dib == 0 || wins.bits == 0 {
		return errors.New("CreateDIBSection failed")
	}
	call(pSelectObject, wins.memDC, wins.dib)
	return nil
}

// grabScreen reads the window's client area from the composed desktop.
func grabScreen() bool {
	ox, oy := clientOrigin()
	screen := call(pGetDC, 0)
	ok := win32.Bool(call(pBitBlt, wins.memDC, 0, 0, topW, topH, screen, uintptr(ox), uintptr(oy), srcCopy|captureBlt))
	call(pReleaseDC, 0, screen)
	call(pGdiFlush)
	return ok
}

// grabPrint reads the window through PrintWindow(PW_RENDERFULLCONTENT),
// which includes DirectX content.
func grabPrint() bool {
	ok := win32.Bool(call(pPrintWindow, wins.top, wins.memDC, pwRenderFull))
	call(pGdiFlush)
	return ok
}

// pixel is the DIB's pixel at client (x, y) as r, g, b.
func pixel(x, y int) (r, g, b uint8) {
	if x < 0 || y < 0 || x >= topW || y >= topH {
		return
	}
	p := unsafe.Slice((*byte)(win32.Ptr(wins.bits)), topW*topH*4)
	i := (y*topW + x) * 4
	return p[i+2], p[i+1], p[i] // BGRA
}

type colour uint8

const (
	cOther colour = iota
	cRed
	cGreen
	cBlue
	cGrey
	cBlack
)

func (c colour) String() string {
	return [...]string{"other", "red", "green", "blue", "grey", "black"}[c]
}

func classify(r, g, b uint8) colour {
	switch {
	case r > 190 && g < 70 && b < 70:
		return cRed
	case g > 190 && r < 70 && b < 70:
		return cGreen
	case b > 190 && r < 70 && g < 70:
		return cBlue
	case r < 20 && g < 20 && b < 20:
		return cBlack
	case r > 100 && r < 160 && g > 100 && g < 160 && b > 100 && b < 160:
		return cGrey
	}
	return cOther
}

func colourAt(x, y int) colour { return classify(pixel(x, y)) }

// topRow is the first row in [y0, y1) of column x with colour c, -1 if none.
func topRow(x, y0, y1 int, c colour) int {
	for y := y0; y < y1; y++ {
		if colourAt(x, y) == c {
			return y
		}
	}
	return -1
}

// blank reports whether the DIB holds nothing but black (an unreadable
// desktop).
func blank() bool {
	for _, p := range [][2]int{{sampleBand, swapChainSampleY}, {sampleChild, containerSampleY}, {5, 5}} {
		if colourAt(p[0], p[1]) != cBlack {
			return false
		}
	}
	return true
}

func dwmFlush() {
	if pDwmFlush != 0 {
		call(pDwmFlush)
	}
}

// timing is DWM_TIMING_INFO, read as bytes: dwmapi.h declares it under
// pshpack1.h (packed), 292 bytes. rateRefresh is at 4 (two UINT32s) and
// qpcRefreshPeriod at 12 (verify on first CI run: E_INVALIDARG means the
// size is wrong).
var timing [292]byte

func refresh() (num, den uint32, periodQPC uint64, hr int32) {
	if pDwmGetCompositionTim == 0 {
		return 0, 0, 0, win32.EProcNotFound
	}
	timing = [292]byte{}
	binary.LittleEndian.PutUint32(timing[0:], uint32(len(timing)))
	hr = win32.HR(call(pDwmGetCompositionTim, 0, uintptr(unsafe.Pointer(&timing[0]))))
	num = binary.LittleEndian.Uint32(timing[4:])
	den = binary.LittleEndian.Uint32(timing[8:])
	periodQPC = binary.LittleEndian.Uint64(timing[12:])
	return
}
