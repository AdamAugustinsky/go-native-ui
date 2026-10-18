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
	pVisibl = win32.Proc(user32, "IsWindowVisible")
	pWinRct = win32.Proc(user32, "GetWindowRect")
	pIconic = win32.Proc(user32, "IsIconic")
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

// revealRun opens a window (cloaked until its first frame when hidden),
// presents magenta as the first frame and returns what the screen under the
// window showed, in order, as runs of one colour.
type run struct {
	colour uint32
	n      int
	first  int64
}

type revealResult struct {
	runs       []run
	revealedAt int64
	cloakedAt  bool // the window was still cloaked when the first present returned
	cloakedEnd bool // and was cloaked after the reveal
	printed    uint32
	w, h       int32
}

func revealRun(t *testing.T, hidden bool, again bool) revealResult {
	t.Helper()
	var r revealResult
	win, err := platform.Open(platform.Options{Title: "gnui reveal test", Width: 400, Height: 300, Hidden: hidden})
	if err != nil {
		t.Skipf("no window: %v", err)
	}
	defer win.Close()
	hwnd := win.Native().HWND
	if hidden && !cloaked(hwnd) {
		t.Skip("DWM does not cloak windows here (composition off?)")
	}
	off := new(uint32)
	*off = 1
	win32.Call(pDwmSet, hwnd, dwmaTransitionsOff, uintptr(unsafe.Pointer(off)), 4) // no fade between the screen's colours
	w, h, scale := win.Size()
	r.w, r.h = w, h
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
	if hidden {
		surf.RevealOnFirstFrame(func() {
			r.cloakedAt = cloaked(hwnd) // still hidden when the present has returned
			win.Show()
			r.cloakedEnd = cloaked(hwnd)
			revealedAt.Store(time.Now().UnixNano())
		})
	}

	pt := &screenPt{w / 2, h / 2}
	win32.Call(pClient, hwnd, uintptr(unsafe.Pointer(pt)))
	smp := startSampler(pt.x, pt.y)
	pump := func(d time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); {
			win.Wait(int64(time.Millisecond))
		}
	}

	pump(300 * time.Millisecond) // the backdrop is painted
	rc := new([4]int32)
	win32.Call(pWinRct, hwnd, uintptr(unsafe.Pointer(rc)))
	t.Logf("window: rect %v visible %v iconic %v cloaked %v; sampling %d,%d", *rc, win32.Bool(win32.Call(pVisibl, hwnd)), win32.Bool(win32.Call(pIconic, hwnd)), cloaked(hwnd), pt.x, pt.y)
	if c := smp.seen.Load(); !hidden || !colourNear(c, green) {
		t.Logf("before the first frame the centre pixel is %06x", c)
		if hidden {
			smp.finish()
			t.Skipf("the screen cannot be read here (centre pixel %06x, want the backdrop %06x)", c, green)
		}
	}
	start := time.Now().UnixNano()
	if !surf.Present(1, 0, 1, 1) {
		t.Logf("the first Present reported that the window was not shown (occluded)")
	}
	t.Logf("first present: start-to-done %.2f ms", float64(surf.Trace.Done-surf.Trace.Start)/1e6)
	if !hidden {
		revealedAt.Store(time.Now().UnixNano())
	}
	r.revealedAt = revealedAt.Load()
	if again {
		pump(50 * time.Millisecond)
		surf.Present(1, 0, 1, 1)
	}

	// Wait for the content to reach the screen.
	for end := time.Now().Add(3 * time.Second); !colourNear(smp.seen.Load(), magenta) && time.Now().Before(end); {
		win.Wait(int64(time.Millisecond))
	}
	pump(100 * time.Millisecond)
	for _, s := range smp.finish() {
		if s.t < start {
			continue // the backdrop being painted
		}
		if n := len(r.runs); n > 0 && r.runs[n-1].colour == s.colour {
			r.runs[n-1].n++
			continue
		}
		r.runs = append(r.runs, run{s.colour, 1, s.t})
	}
	r.printed = printWindowPixel(hwnd, w, h)
	return r
}

func TestFirstFrameIsContent(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if pDwmGet == 0 || pDwmSet == 0 {
		t.Skip("no DWM")
	}
	r := revealRun(t, true, false)
	if r.revealedAt == 0 {
		t.Fatal("the first present did not reveal the window")
	}
	if !r.cloakedAt {
		t.Error("the window was already uncloaked when the first present returned")
	}
	if r.cloakedEnd {
		t.Error("the window is still cloaked after the reveal")
	}
	sawContent := false
	for _, u := range r.runs {
		t.Logf("screen: %06x x%d (%+.1f ms from the reveal)", u.colour, u.n, float64(u.first-r.revealedAt)/1e6)
		switch {
		case colourNear(u.colour, magenta):
			sawContent = true
		case colourNear(u.colour, green):
			if sawContent {
				t.Errorf("the backdrop came back after the content")
			}
		default:
			t.Errorf("colour %06x was on the screen: neither the desktop nor the frame", u.colour)
		}
	}
	if !sawContent {
		if colourNear(r.printed, magenta) {
			t.Skipf("the window has the frame (PrintWindow), but the screen capture never showed it: it cannot see windows here")
		}
		t.Error("the frame never reached the window or the screen")
	}
}
