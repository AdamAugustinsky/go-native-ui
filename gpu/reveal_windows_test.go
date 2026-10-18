//go:build windows

package gpu

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
	"gonativeui/platform"
)

// Content-first startup on Windows: a window opened with Options.Hidden is
// cloaked until the first frame is presented, so the first thing on the screen
// is the frame, not an empty window. The test looks at the screen the way a
// user would: a sampler thread reads the pixel at the centre of the window's
// client area with BitBlt from the screen DC, as fast as it can, from before
// the first present to after the reveal. Behind the window it puts a green
// window of its own, so that "the desktop" has a known colour that a blank
// window (black, white or grey, whatever DWM starts a surface with) cannot be
// mistaken for.

var (
	user32  = win32.LoadSystem("user32.dll")
	gdi32   = win32.LoadSystem("gdi32.dll")
	dwmapi  = win32.LoadSystem("dwmapi.dll")
	pGetDC  = win32.Proc(user32, "GetDC")
	pRelDC  = win32.Proc(user32, "ReleaseDC")
	pMetric = win32.Proc(user32, "GetSystemMetrics")
	pPrintW = win32.Proc(user32, "PrintWindow")
	pClient = win32.Proc(user32, "ClientToScreen")
	pRegCls = win32.Proc(user32, "RegisterClassExW")
	pUnrCls = win32.Proc(user32, "UnregisterClassW")
	pCreate = win32.Proc(user32, "CreateWindowExW")
	pDestr  = win32.Proc(user32, "DestroyWindow")
	pSetPos = win32.Proc(user32, "SetWindowPos")
	pDefWnd = win32.Proc(user32, "DefWindowProcW")
	pCompDC = win32.Proc(gdi32, "CreateCompatibleDC")
	pCompBM = win32.Proc(gdi32, "CreateCompatibleBitmap")
	pSelect = win32.Proc(gdi32, "SelectObject")
	pBitBlt = win32.Proc(gdi32, "BitBlt")
	pPixel  = win32.Proc(gdi32, "GetPixel")
	pDelObj = win32.Proc(gdi32, "DeleteObject")
	pDelDC  = win32.Proc(gdi32, "DeleteDC")
	pBrush  = win32.Proc(gdi32, "CreateSolidBrush")
	pDwmGet = win32.Proc(dwmapi, "DwmGetWindowAttribute")
	pDwmSet = win32.Proc(dwmapi, "DwmSetWindowAttribute")
)

const (
	srcCopy             = 0x00CC0020
	captureBlt          = 0x40000000
	pwRenderFullContent = 2
	dwmaCloaked         = 14
	dwmaTransitionsOff  = 3
	hwndBottom          = 1
	wsPopup             = 0x80000000
	wsVisible           = 0x10000000
	wsExToolWindow      = 0x00000080
	swpNoSize           = 0x0001
	swpNoMove           = 0x0002
	swpNoActivate       = 0x0010

	green   = 0x0000FF00 // COLORREF 0x00BBGGRR
	magenta = 0x00FF00FF
)

type wndClass struct {
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

type screenPt struct{ x, y int32 }

// backdrop is a window covering the primary screen in one solid colour, kept
// below every other window of the process.
type backdrop struct {
	hwnd, brush uintptr
	cls         wndClass
	name        [16]uint16
}

func newBackdrop(t *testing.T, colour uintptr) *backdrop {
	t.Helper()
	b := &backdrop{}
	for i, c := range "gnuiBackdrop" {
		b.name[i] = uint16(c)
	}
	b.brush = win32.Call(pBrush, colour)
	b.cls = wndClass{size: uint32(unsafe.Sizeof(b.cls)), wndProc: pDefWnd, background: b.brush, className: &b.name[0]}
	if win32.Call(pRegCls, uintptr(unsafe.Pointer(&b.cls)))&0xFFFF == 0 {
		t.Skip("cannot register the backdrop window class")
	}
	cx, cy := win32.Call(pMetric, 0), win32.Call(pMetric, 1) // SM_CXSCREEN, SM_CYSCREEN
	b.hwnd = win32.Call(pCreate, wsExToolWindow, uintptr(unsafe.Pointer(&b.name[0])), 0, wsPopup|wsVisible, 0, 0, cx, cy, 0, 0, 0, 0)
	if b.hwnd == 0 {
		win32.Call(pUnrCls, uintptr(unsafe.Pointer(&b.name[0])), 0)
		t.Skip("cannot create the backdrop window")
	}
	win32.Call(pSetPos, b.hwnd, hwndBottom, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	return b
}

func (b *backdrop) destroy() {
	win32.Call(pDestr, b.hwnd)
	win32.Call(pUnrCls, uintptr(unsafe.Pointer(&b.name[0])), 0)
	win32.Call(pDelObj, b.brush)
}

// sample is one reading of the screen.
type sample struct {
	t      int64 // time.Now().UnixNano()
	colour uint32
}

// sampler reads the screen pixel at (x, y) in a loop on its own thread.
type sampler struct {
	x, y    int32
	stop    atomic.Bool
	seen    atomic.Uint32 // the latest colour
	samples []sample
	wg      sync.WaitGroup
}

func startSampler(x, y int32) *sampler {
	s := &sampler{x: x, y: y, samples: make([]sample, 0, 1<<16)}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		screen := win32.Call(pGetDC, 0)
		mem := win32.Call(pCompDC, screen)
		bm := win32.Call(pCompBM, screen, 1, 1)
		old := win32.Call(pSelect, mem, bm)
		for !s.stop.Load() && len(s.samples) < cap(s.samples) {
			win32.Call(pBitBlt, mem, 0, 0, 1, 1, screen, uintptr(s.x), uintptr(s.y), srcCopy|captureBlt)
			c := uint32(win32.Call(pPixel, mem, 0, 0))
			s.seen.Store(c)
			s.samples = append(s.samples, sample{time.Now().UnixNano(), c})
		}
		win32.Call(pSelect, mem, old)
		win32.Call(pDelObj, bm)
		win32.Call(pDelDC, mem)
		win32.Call(pRelDC, 0, screen)
	}()
	return s
}

func (s *sampler) finish() []sample {
	s.stop.Store(true)
	s.wg.Wait()
	return s.samples
}

func colourNear(a, b uint32) bool {
	d := func(x, y uint32) bool { return x+4 >= y && y+4 >= x }
	return d(a&0xFF, b&0xFF) && d(a>>8&0xFF, b>>8&0xFF) && d(a>>16&0xFF, b>>16&0xFF)
}

// printWindowPixel reads the centre of the window's own content through
// PrintWindow, which asks DWM for it whether or not it is on the screen.
func printWindowPixel(hwnd uintptr, w, h int32) uint32 {
	screen := win32.Call(pGetDC, 0)
	mem := win32.Call(pCompDC, screen)
	bm := win32.Call(pCompBM, screen, uintptr(w), uintptr(h))
	old := win32.Call(pSelect, mem, bm)
	win32.Call(pPrintW, hwnd, mem, pwRenderFullContent)
	c := uint32(win32.Call(pPixel, mem, uintptr(w/2), uintptr(h/2)))
	win32.Call(pSelect, mem, old)
	win32.Call(pDelObj, bm)
	win32.Call(pDelDC, mem)
	win32.Call(pRelDC, 0, screen)
	return c
}

func cloaked(hwnd uintptr) bool {
	v := new(uint32)
	return win32.HR(win32.Call(pDwmGet, hwnd, dwmaCloaked, uintptr(unsafe.Pointer(v)), 4)) >= 0 && *v != 0
}

func TestFirstFrameIsContent(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if pDwmGet == 0 || pDwmSet == 0 {
		t.Skip("no DWM")
	}

	win, err := platform.Open(platform.Options{Title: "gnui reveal test", Width: 400, Height: 300, Hidden: true})
	if err != nil {
		t.Skipf("no window: %v", err)
	}
	defer win.Close()
	hwnd := win.Native().HWND
	if !cloaked(hwnd) {
		t.Skip("DWM does not cloak windows here (composition off?)")
	}
	off := new(uint32)
	*off = 1
	win32.Call(pDwmSet, hwnd, dwmaTransitionsOff, uintptr(unsafe.Pointer(off)), 4) // no fade between the screen's colours
	w, h, scale := win.Size()
	surf, err := NewSurface(win.Native(), w, h, scale)
	if errors.Is(err, ErrNoDevice) {
		t.Skipf("no Direct3D 11 device: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer surf.Destroy()
	win.SetFrameSource(surf.FrameSource())

	bd := newBackdrop(t, green)
	defer bd.destroy()
	var revealedAt atomic.Int64
	var cloakedAtReveal bool
	surf.RevealOnFirstFrame(func() {
		cloakedAtReveal = cloaked(hwnd) // still hidden when the present has returned
		win.Show()
		revealedAt.Store(time.Now().UnixNano())
	})

	pt := &screenPt{w / 2, h / 2}
	win32.Call(pClient, hwnd, uintptr(unsafe.Pointer(pt)))
	smp := startSampler(pt.x, pt.y)
	pump := func(d time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); {
			win.Wait(int64(time.Millisecond))
		}
	}

	pump(150 * time.Millisecond) // the window is shown but cloaked: the screen shows the backdrop
	if c := smp.seen.Load(); !colourNear(c, green) {
		smp.finish()
		t.Skipf("the screen cannot be read here (centre pixel %06x, want the backdrop %06x)", c, green)
	}
	if !surf.Present(1, 0, 1, 1) {
		t.Logf("the first Present of a cloaked window reported it was not shown (occluded)")
	}
	t.Logf("first present: start-to-done %.2f ms", float64(surf.Trace.Done-surf.Trace.Start)/1e6)
	if revealedAt.Load() == 0 {
		t.Fatal("the first present did not reveal the window")
	}
	if !cloakedAtReveal {
		t.Error("the window was already uncloaked when the first present returned")
	}
	if cloaked(hwnd) {
		t.Error("the window is still cloaked after the reveal")
	}

	// Wait for the content to reach the screen.
	for end := time.Now().Add(3 * time.Second); !colourNear(smp.seen.Load(), magenta) && time.Now().Before(end); {
		win.Wait(int64(time.Millisecond))
	}
	pump(100 * time.Millisecond)
	samples := smp.finish()

	// What was on the screen, in order, as runs of one colour.
	type run struct {
		colour uint32
		n      int
		first  int64
	}
	var runs []run
	for _, s := range samples {
		if n := len(runs); n > 0 && runs[n-1].colour == s.colour {
			runs[n-1].n++
			continue
		}
		runs = append(runs, run{s.colour, 1, s.t})
	}
	sawContent := false
	for _, r := range runs {
		t.Logf("screen: %06x x%d (+%.1f ms from the reveal)", r.colour, r.n, float64(r.first-revealedAt.Load())/1e6)
		switch {
		case colourNear(r.colour, magenta):
			sawContent = true
		case colourNear(r.colour, green):
			if sawContent {
				t.Errorf("the backdrop came back after the content")
			}
		default:
			t.Errorf("colour %06x was on the screen: neither the desktop nor the frame", r.colour)
		}
	}
	if !sawContent {
		if c := printWindowPixel(hwnd, w, h); colourNear(c, magenta) {
			t.Skipf("the window has the frame (PrintWindow), but the screen capture never showed it: it cannot see windows here")
		}
		t.Error("the frame never reached the window or the screen")
	}
}
