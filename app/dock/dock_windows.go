//go:build windows

package dock

import (
	"fmt"
	"math"
	"syscall"
	"unsafe"

	"gonativeui/app"
	"gonativeui/internal/win32"
	"gonativeui/platform"
)

// Windows: the taskbar (the phase 8 plan's S1). The app's progress and badge
// are kept here and applied to every open window's taskbar button through
// ITaskbarList3, made on the main thread (an OLE STA once the app object
// exists) on first use, and to a window whose button the shell makes later
// (platform.OnTaskbarButton). The badge is an overlay icon drawn here with
// GDI: a disc in the badge red, the label in white, anti-aliased, at the
// small-icon size for the system DPI.
//
// Reference: shobjidl_core.h (ITaskbarList, ITaskbarList2, ITaskbarList3:
// HrInit 3, … MarkFullscreenWindow 8, SetProgressValue 9, SetProgressState
// 10, … SetOverlayIcon 18).

var (
	clsidTaskbarList = win32.GUID{Data1: 0x56FDF344, Data2: 0xFD6D, Data3: 0x11D0, Data4: [8]byte{0x95, 0x8A, 0x00, 0x60, 0x97, 0xC9, 0xA0, 0x90}}
	iidTaskbarList3  = win32.GUID{Data1: 0xEA1AFB91, Data2: 0x9E28, Data3: 0x4B86, Data4: [8]byte{0x90, 0xE9, 0x9E, 0x9F, 0x8A, 0x5E, 0xEF, 0xAF}}
)

const (
	vtHrInit           = 3
	vtSetProgressValue = 9
	vtSetProgressState = 10
	vtSetOverlayIcon   = 18

	tbpfNoProgress    = 0x0
	tbpfIndeterminate = 0x1
	tbpfNormal        = 0x2

	progressScale = 10000
	clsctxInproc  = 0x1
)

// tb is the taskbar state. Main thread.
var tb struct {
	list    uintptr // ITaskbarList3, 0 until first use
	failed  bool    // it could not be made: no taskbar (a server core)
	hooked  bool
	badge   string
	icon    uintptr  // the badge's HICON, 0 for none
	desc    []uint16 // the badge's description, NUL-terminated
	hwnds   []uintptr
	lastHR  int32 // the last ITaskbarList3 call's result (tests)
	applied int   // windows the state was applied to (tests)
	buttons int   // TaskbarButtonCreated messages seen (tests)
}

var (
	ole32         = win32.LoadSystem("ole32.dll")
	pCoCreateInst = win32.Proc(ole32, "CoCreateInstance")
)

// taskbarList makes ITaskbarList3 once and hooks the windows' buttons.
func taskbarList() (uintptr, error) {
	if !platform.AppStarted() {
		return 0, ErrNotStarted
	}
	if tb.list == 0 && !tb.failed {
		hr := win32.HR(call(pCoCreateInst, uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, clsctxInproc,
			uintptr(unsafe.Pointer(&iidTaskbarList3)), uintptr(unsafe.Pointer(&tb.list))))
		if hr < 0 || tb.list == 0 || win32.HR(win32.Com(tb.list, vtHrInit)) < 0 {
			win32.Release(tb.list)
			tb.list, tb.failed = 0, true
			tb.lastHR = hr
		}
	}
	if tb.list == 0 {
		return 0, fmt.Errorf("%w: no taskbar (ITaskbarList3: 0x%08X)", ErrUnsupported, uint32(tb.lastHR))
	}
	if !tb.hooked {
		tb.hooked = true
		platform.OnTaskbarButton(func(hwnd uintptr) {
			tb.buttons++
			apply(hwnd)
		})
	}
	return tb.list, nil
}

// apply puts the progress and the badge on hwnd's button.
func apply(hwnd uintptr) {
	if tb.list == 0 {
		return
	}
	tb.applied++
	switch p := progress; p {
	case NoProgress:
		tb.lastHR = win32.HR(win32.Com(tb.list, vtSetProgressState, hwnd, tbpfNoProgress))
	case Indeterminate:
		tb.lastHR = win32.HR(win32.Com(tb.list, vtSetProgressState, hwnd, tbpfIndeterminate))
	default:
		win32.Com(tb.list, vtSetProgressState, hwnd, tbpfNormal)
		tb.lastHR = win32.HR(win32.Com(tb.list, vtSetProgressValue, hwnd, uintptr(math.Round(p*progressScale)), progressScale))
	}
	var desc uintptr
	if len(tb.desc) > 0 {
		desc = uintptr(unsafe.Pointer(&tb.desc[0]))
	}
	if hr := win32.HR(win32.Com(tb.list, vtSetOverlayIcon, hwnd, tb.icon, desc)); hr < 0 {
		tb.lastHR = hr
	}
}

// applyAll applies the state to every open window and reports the last
// failure.
func applyAll() error {
	tb.hwnds = platform.TaskbarWindows(tb.hwnds[:0])
	var err error
	for _, h := range tb.hwnds {
		apply(h)
		if tb.lastHR < 0 {
			err = fmt.Errorf("dock: the taskbar refused the change (0x%08X)", uint32(tb.lastHR))
		}
	}
	return err
}

func osSetProgress(f float64) error {
	if _, err := taskbarList(); err != nil {
		return err
	}
	if f == progress {
		return nil
	}
	progress = f
	return applyAll()
}

func osSetBadge(label string) error {
	if _, err := taskbarList(); err != nil {
		return err
	}
	if label == tb.badge {
		return nil
	}
	var icon uintptr
	if label != "" {
		var err error
		if icon, err = drawBadge(label, smallIconSize()); err != nil {
			return err
		}
	}
	old := tb.icon
	tb.badge, tb.icon = label, icon
	tb.desc = tb.desc[:0]
	if label != "" {
		u, _ := syscall.UTF16FromString(label)
		tb.desc = append(tb.desc, u...)
	}
	err := applyAll()
	if old != 0 {
		win32.DestroyIcon(old) // the taskbar keeps its own copy
	}
	return err
}

func osBadge() string { return tb.badge }

func osBounce(critical bool) (Attention, error) {
	if !platform.AppStarted() {
		return 0, ErrNotStarted
	}
	return Attention(platform.RequestAttention(critical)), nil
}

func osCancelBounce(id Attention) { platform.CancelAttention(int(id)) }

func osSetPolicy(app.Policy) error { return ErrUnsupported }

func osPolicy() (app.Policy, error) { return app.PolicyRegular, ErrUnsupported }

// ---- The badge's icon ----

var (
	user32              = win32.LoadSystem("user32.dll")
	gdi32               = win32.LoadSystem("gdi32.dll")
	pGetSystemMetrics   = win32.Proc(user32, "GetSystemMetrics")
	pGetMetricsForDpi   = win32.Proc(user32, "GetSystemMetricsForDpi") // Windows 10 1607+
	pGetDpiForSystem    = win32.Proc(user32, "GetDpiForSystem")
	pDrawTextW          = win32.Proc(user32, "DrawTextW")
	pCreateCompatibleDC = win32.Proc(gdi32, "CreateCompatibleDC")
	pDeleteDC           = win32.Proc(gdi32, "DeleteDC")
	pCreateDIBSection   = win32.Proc(gdi32, "CreateDIBSection")
	pSelectObject       = win32.Proc(gdi32, "SelectObject")
	pDeleteObject       = win32.Proc(gdi32, "DeleteObject")
	pCreateFontW        = win32.Proc(gdi32, "CreateFontW")
	pSetTextColor       = win32.Proc(gdi32, "SetTextColor")
	pSetBkMode          = win32.Proc(gdi32, "SetBkMode")
	pGdiFlush           = win32.Proc(gdi32, "GdiFlush")
	fontFace            = mustUTF16("Segoe UI")
)

const (
	smCxSmIcon         = 49
	fwBold             = 700
	antialiasedQuality = 4
	transparentBk      = 1
	dtCenter           = 0x1
	dtVCenter          = 0x4
	dtSingleLine       = 0x20
	dtCalcRect         = 0x400
	dtNoPrefix         = 0x800
)

// badgeRed is the badge's colour (0xRRGGBB), the system badge red.
const badgeRed uint32 = 0xE81123

// bitmapInfo is BITMAPINFOHEADER (with no colour table: 32 bits per pixel).
type bitmapInfo struct {
	size          uint32
	width, height int32
	planes, bits  uint16
	compression   uint32
	sizeImage     uint32
	xppm, yppm    int32
	used, imp     uint32
}

type rect struct{ left, top, right, bottom int32 }

// smallIconSize is the small-icon size at the system DPI (an overlay's).
func smallIconSize() int {
	if pGetMetricsForDpi != 0 && pGetDpiForSystem != 0 {
		if n := int(call(pGetMetricsForDpi, smCxSmIcon, call(pGetDpiForSystem))); n > 0 {
			return n
		}
	}
	if n := int(call(pGetSystemMetrics, smCxSmIcon)); n > 0 {
		return n
	}
	return 16
}

// badgeGDI is drawBadge's scratch, on the heap for the calls.
var badgeGDI struct {
	bmi  bitmapInfo
	bits uintptr
	rc   rect
}

// drawBadge draws label in a disc as an n×n icon. Main thread.
func drawBadge(label string, n int) (uintptr, error) {
	text, err := syscall.UTF16FromString(label)
	if err != nil {
		return 0, fmt.Errorf("dock: the badge %q has a NUL", label)
	}
	g := &badgeGDI
	dc := call(pCreateCompatibleDC, 0)
	if dc == 0 {
		return 0, fmt.Errorf("dock: CreateCompatibleDC failed")
	}
	defer call(pDeleteDC, dc)
	g.bmi = bitmapInfo{width: int32(n), height: -int32(n), planes: 1, bits: 32} // top-down
	g.bmi.size = uint32(unsafe.Sizeof(g.bmi))
	dib := call(pCreateDIBSection, dc, uintptr(unsafe.Pointer(&g.bmi)), 0, uintptr(unsafe.Pointer(&g.bits)), 0, 0)
	if dib == 0 || g.bits == 0 {
		return 0, fmt.Errorf("dock: CreateDIBSection failed")
	}
	oldBmp := call(pSelectObject, dc, dib)
	call(pSetBkMode, dc, transparentBk)
	call(pSetTextColor, dc, 0xFFFFFF)

	// The largest bold face whose text fits the disc's inner width.
	inner := int32(math.Round(float64(n) * 0.78))
	if len(text) > 2 {
		inner = int32(n) // a long label may reach the edges
	}
	var font, oldFont uintptr
	for h := int32(math.Round(float64(n) * 0.72)); h >= 5; h-- {
		f := call(pCreateFontW, uintptr(-h), 0, 0, 0, fwBold, 0, 0, 0, 1, 0, 0, antialiasedQuality, 0, uintptr(unsafe.Pointer(fontFace)))
		if f == 0 {
			break
		}
		prev := call(pSelectObject, dc, f)
		if font != 0 {
			call(pDeleteObject, prev)
		} else {
			oldFont = prev
		}
		font = f
		g.rc = rect{}
		call(pDrawTextW, dc, uintptr(unsafe.Pointer(&text[0])), ^uintptr(0), uintptr(unsafe.Pointer(&g.rc)), dtCalcRect|dtSingleLine|dtNoPrefix)
		if g.rc.right-g.rc.left <= inner {
			break
		}
	}
	g.rc = rect{0, 0, int32(n), int32(n)}
	call(pDrawTextW, dc, uintptr(unsafe.Pointer(&text[0])), ^uintptr(0), uintptr(unsafe.Pointer(&g.rc)), dtCenter|dtVCenter|dtSingleLine|dtNoPrefix)
	call(pGdiFlush)
	call(pSelectObject, dc, oldBmp)
	if font != 0 {
		call(pSelectObject, dc, oldFont)
		call(pDeleteObject, font)
	}

	// The text's coverage is its green channel (white on black, greyscale
	// anti-aliasing); the disc's is its distance from the centre.
	px := make([]uint32, n*n)
	copy(px, unsafe.Slice((*uint32)(win32.Ptr(g.bits)), n*n))
	call(pDeleteObject, dib)
	c := float64(n) / 2
	rad := c - 0.25
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			i := y*n + x
			cov := float64((px[i]>>8)&0xFF) / 255
			d := math.Hypot(float64(x)+0.5-c, float64(y)+0.5-c)
			a := math.Max(0, math.Min(1, rad-d+0.5))
			if a == 0 {
				px[i] = 0
				continue
			}
			mix := func(sh uint) uint32 {
				bg := float64((badgeRed >> sh) & 0xFF)
				return uint32(math.Round(bg + (255-bg)*cov))
			}
			px[i] = uint32(math.Round(a*255))<<24 | mix(16)<<16 | mix(8)<<8 | mix(0)
		}
	}

	icon := win32.IconFromPixels(n, n, px)
	if icon == 0 {
		return 0, fmt.Errorf("dock: CreateIconIndirect failed")
	}
	return icon, nil
}

func mustUTF16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

// call calls fn; what a pointer argument converted in the call expression
// points to is moved to the heap.
//
//go:uintptrescapes
func call(fn uintptr, args ...uintptr) uintptr { return win32.Call(fn, args...) }
