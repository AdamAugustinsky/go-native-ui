//go:build windows

package platform

import (
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/testlock"
	"gonativeui/internal/win32"
)

// Hosts as child windows (W3, D66): placement, clipping, hiding, focus
// hand-off and host commands, pointer routing, and leaks.

var (
	htPMapWindowPoints = win32.Proc(user32, "MapWindowPoints")
	htPIsWindow        = win32.Proc(user32, "IsWindow")
	htPGetWindowTextW  = win32.Proc(user32, "GetWindowTextW")
	htPGetParent       = win32.Proc(user32, "GetParent")
)

// htTestRect is a window's rectangle in its parent's client coordinates.
var htTestRect rect

func childRect(t *testing.T, hwnd uintptr) [4]int32 {
	t.Helper()
	if !win32.Bool(win32.Call(pGetWindowRect, hwnd, uintptr(unsafe.Pointer(&htTestRect)))) {
		t.Fatalf("GetWindowRect(%#x) failed", hwnd)
	}
	win32.Call(htPMapWindowPoints, 0, win32.Call(htPGetParent, hwnd), uintptr(unsafe.Pointer(&htTestRect)), 2)
	return [4]int32{htTestRect.left, htTestRect.top, htTestRect.right, htTestRect.bottom}
}

func visible(hwnd uintptr) bool { return win32.Bool(win32.Call(pIsWindowVisible, hwnd)) }

// newControl makes a top-level window of a system class (EDIT, …) to hand
// to NewHost.
func newControl(t *testing.T, class string, style uintptr) uintptr {
	t.Helper()
	c, _ := syscall.UTF16PtrFromString(class)
	h := win32.Call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(c)), 0, wsPopup|style, 0, 0, 50, 20, 0, 0, win32.Call(pGetModuleHandleW, 0), 0)
	if h == 0 {
		t.Fatalf("CreateWindowExW(%s) failed", class)
	}
	return h
}

func place(w *Window, r ...HostRegion) {
	w.SetHosts(r)
	w.FlushShell()
}

func TestHostIsPlacedResizedClippedAndHidden(t *testing.T) {
	lockThread(t)
	w := openW(t, "hosts")
	settle(w)
	before := ShellObjects()
	id, err := w.NewHost(HostView, HostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := w.hosts.slot(id)
	view := w.HostView(id)
	if view == 0 || view != s.view || win32.Call(htPGetParent, view) != s.clip || win32.Call(htPGetParent, s.clip) != w.hwnd {
		t.Fatalf("view %#x in container %#x in window %#x: parents wrong", view, s.clip, w.hwnd)
	}
	if visible(s.clip) {
		t.Error("a new host is shown before SetHosts placed it")
	}
	if got := ShellObjects().Hosts - before.Hosts; got != 1 {
		t.Errorf("ShellObjects().Hosts grew by %d, want 1", got)
	}
	var sizes [][2]int32
	w.OnHostResize(id, func(cw, ch int32) { sizes = append(sizes, [2]int32{cw, ch}) })

	// Placed.
	place(w, HostRegion{ID: id, Rect: [4]float32{10, 20, 110, 70}, Clip: [4]float32{10, 20, 110, 70}})
	if !visible(s.clip) || !visible(view) {
		t.Fatal("placed host not visible")
	}
	if got := childRect(t, s.clip); got != [4]int32{10, 20, 110, 70} {
		t.Errorf("container at %v, want [10 20 110 70]", got)
	}
	if got := childRect(t, view); got != [4]int32{0, 0, 100, 50} {
		t.Errorf("view at %v in its container, want [0 0 100 50]", got)
	}
	// Resized.
	place(w, HostRegion{ID: id, Rect: [4]float32{10, 20, 210, 140}, Clip: [4]float32{10, 20, 210, 140}})
	if got := childRect(t, view); got != [4]int32{0, 0, 200, 120} {
		t.Errorf("resized view at %v, want [0 0 200 120]", got)
	}
	// Clipped by a scrolled list: the top 30 px are scrolled out.
	place(w, HostRegion{ID: id, Rect: [4]float32{10, -30, 210, 90}, Clip: [4]float32{10, 0, 210, 90}})
	if got := childRect(t, s.clip); got != [4]int32{10, 0, 210, 90} {
		t.Errorf("clipped container at %v, want [10 0 210 90]", got)
	}
	if got := childRect(t, view); got != [4]int32{0, -30, 200, 90} {
		t.Errorf("clipped view at %v, want [0 -30 200 90]", got)
	}
	if want := [][2]int32{{0, 0}, {100, 50}, {200, 120}}; len(sizes) != len(want) || sizes[0] != want[0] || sizes[1] != want[1] || sizes[2] != want[2] {
		t.Errorf("OnHostResize sizes %v, want %v (the clip moved the view, not its size)", sizes, want)
	}
	// A drawn popup covers it, then goes.
	place(w, HostRegion{ID: id, Rect: [4]float32{10, -30, 210, 90}, Clip: [4]float32{10, 0, 210, 90}, Hidden: true})
	if visible(s.clip) {
		t.Error("hidden host still visible")
	}
	place(w, HostRegion{ID: id, Rect: [4]float32{10, -30, 210, 90}, Clip: [4]float32{10, 0, 210, 90}})
	if !visible(s.clip) {
		t.Error("host not shown again after the popup")
	}
	// Scrolled out of the list completely, and left out of the list.
	place(w, HostRegion{ID: id, Rect: [4]float32{10, -200, 210, -80}, Clip: [4]float32{10, 0, 210, 0}})
	if visible(s.clip) {
		t.Error("host clipped away still visible")
	}
	place(w, HostRegion{ID: id, Rect: [4]float32{10, 20, 110, 70}, Clip: [4]float32{10, 20, 110, 70}})
	place(w)
	if visible(s.clip) {
		t.Error("host left out of SetHosts still visible")
	}
	// An unchanged list stages nothing.
	n := w.hosts.applied
	w.SetHosts(nil)
	if ShellPending() {
		t.Error("an unchanged host list was staged")
	}
	w.FlushShell()
	if w.hosts.applied != n {
		t.Error("an unchanged host list was applied")
	}

	clip := s.clip
	w.ReleaseHost(id)
	if win32.Bool(win32.Call(htPIsWindow, clip)) || win32.Bool(win32.Call(htPIsWindow, view)) {
		t.Error("ReleaseHost left its windows")
	}
	if w.HostView(id) != 0 {
		t.Error("a released id still resolves")
	}
	if got := ShellObjects().Hosts; got != before.Hosts {
		t.Errorf("ShellObjects().Hosts %d after release, want %d", got, before.Hosts)
	}
}

func TestHostButtonIsRefusedAndTheTableIsBounded(t *testing.T) {
	lockThread(t)
	w := openW(t, "host limits")
	if _, err := w.NewHost(HostButton, HostOptions{Symbol: "info.circle"}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("HostButton: %v, want ErrUnsupported", err)
	}
	ids := make([]HostID, 0, MaxHosts)
	for range MaxHosts {
		id, err := w.NewHost(HostGlass, HostOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if _, err := w.NewHost(HostView, HostOptions{}); !errors.Is(err, ErrTooManyHosts) {
		t.Errorf("host %d: %v, want ErrTooManyHosts", MaxHosts+1, err)
	}
	w.ReleaseHost(ids[3])
	id, err := w.NewHost(HostView, HostOptions{})
	if err != nil || id == ids[3] {
		t.Errorf("reused slot: id %#x (old %#x), %v: want a new generation", id, ids[3], err)
	}
}

// A host given an HWND (a system EDIT, as shelldemo hosts) takes the
// keyboard: EvHostFocus without EvFocus, keys typed reach the control and not
// ui, Tab and Escape and Enter it does not want come back as host commands,
// and the focus comes back.
func TestHostFocusHandOffAndCommands(t *testing.T) {
	lockThread(t)
	w := openW(t, "host focus")
	edit := newControl(t, "EDIT", 0)
	id, err := w.NewHost(HostView, HostOptions{View: edit, Focusable: true})
	if err != nil {
		t.Fatal(err)
	}
	if win32.Call(htPGetParent, edit) != w.hosts.slot(id).clip {
		t.Fatal("the EDIT was not moved into the host's container")
	}
	place(w, HostRegion{ID: id, Rect: [4]float32{10, 10, 200, 40}, Clip: [4]float32{10, 10, 200, 40}})
	// The window has the focus before it hands it on, so the only focus
	// change after FocusHost is the host's. It may not at first: another test
	// process's window has the foreground, and the window gained the focus
	// during FocusHost instead (EvFocus, arm64, p8-windows-flakes
	// 40825454957). The test holds the activation, as the tests that take the
	// foreground do, and waits for the focus.
	testlock.Hold(t, "activation")
	for end := time.Now().Add(5 * time.Second); !w.kbd && time.Now().Before(end); {
		win32.Call(pSetFocus, w.hwnd)
		settle(w)
	}
	settle(w)
	w.Events()

	w.FocusHost(id)
	got := pumpEvents([]*Window{w}, time.Second, func(g [][]Event) bool { return hasKind(g[0], EvHostFocus) })[0]
	if e := lastOf(got, EvHostFocus); e.Kind != EvHostFocus || HostID(e.Tag) != id {
		t.Fatalf("FocusHost: no EvHostFocus for %#x: %+v", id, got)
	}
	if hasKind(got, EvFocus) {
		t.Errorf("focus into a host reported the window's focus change: %+v", got)
	}
	if !w.hostFocused() || win32.Call(pGetFocus) != edit {
		t.Fatalf("the EDIT does not have the focus (GetFocus %#x)", win32.Call(pGetFocus))
	}

	// Typing reaches the control, not ui.
	win32.Call(pPostMessageW, edit, wmChar, 'x', 1)
	got = pumpEvents([]*Window{w}, 300*time.Millisecond, func([][]Event) bool { return false })[0]
	if hasKind(got, EvText) || hasKind(got, EvKey) {
		t.Errorf("a key for the host reached ui: %+v", got)
	}
	if s := windowText(edit); s != "x" {
		t.Errorf("EDIT text %q, want x", s)
	}

	// Tab, Escape and Enter, which a one-line EDIT does not ask for, come back.
	for _, k := range []struct {
		vk  uintptr
		key Key
	}{{vkTab, KeyTab}, {vkEscape, KeyEscape}, {vkReturn, KeyEnter}} {
		postKey(edit, k.vk)
		got = pumpEvents([]*Window{w}, time.Second, func(g [][]Event) bool { return hasKind(g[0], EvHostCommand) })[0]
		e := lastOf(got, EvHostCommand)
		if e.Kind != EvHostCommand || HostID(e.Tag) != id || e.Key != k.key {
			t.Errorf("vk %#x: no host command %v for %#x: %+v", k.vk, k.key, id, got)
		}
	}
	if s := windowText(edit); s != "x" {
		t.Errorf("EDIT text %q after the commands, want x (they must not reach it)", s)
	}

	// Back to the window.
	w.FocusHost(0)
	got = pumpEvents([]*Window{w}, time.Second, func(g [][]Event) bool { return hasKind(g[0], EvHostFocus) })[0]
	if e := lastOf(got, EvHostFocus); e.Kind != EvHostFocus || e.Tag != 0 {
		t.Errorf("FocusHost(0): no EvHostFocus(0): %+v", got)
	}
	if hasKind(got, EvFocus) {
		t.Errorf("focus back from a host reported the window's focus change: %+v", got)
	}

	// A move the window is not told about (SetFocus from host to host) is
	// found after the pump's dispatch.
	edit2 := newControl(t, "EDIT", 0)
	id2, err := w.NewHost(HostView, HostOptions{View: edit2, Focusable: true})
	if err != nil {
		t.Fatal(err)
	}
	place(w, HostRegion{ID: id, Rect: [4]float32{10, 10, 200, 40}, Clip: [4]float32{10, 10, 200, 40}},
		HostRegion{ID: id2, Rect: [4]float32{10, 50, 200, 80}, Clip: [4]float32{10, 50, 200, 80}})
	w.FocusHost(id)
	settle(w)
	w.Events()
	win32.Call(pSetFocus, edit2)
	win32.Call(pPostMessageW, w.hwnd, htWmNull, 0, 0)
	got = pumpEvents([]*Window{w}, time.Second, func(g [][]Event) bool { return hasKind(g[0], EvHostFocus) })[0]
	if e := lastOf(got, EvHostFocus); e.Kind != EvHostFocus || HostID(e.Tag) != id2 {
		t.Errorf("host to host: no EvHostFocus for %#x: %+v", id2, got)
	}

	// Releasing the focused host gives the focus back to the window.
	w.ReleaseHost(id2)
	got = pumpEvents([]*Window{w}, time.Second, func(g [][]Event) bool { return hasKind(g[0], EvHostFocus) })[0]
	if e := lastOf(got, EvHostFocus); e.Kind != EvHostFocus || e.Tag != 0 {
		t.Errorf("released focused host: no EvHostFocus(0): %+v", got)
	}
	if f := win32.Call(pGetFocus); f != w.hwnd {
		t.Errorf("focus %#x after release, want the window %#x", f, w.hwnd)
	}
}

// A multi-line EDIT asks for Enter and Tab (DLGC_WANTALLKEYS): they stay
// with it.
func TestHostControlKeepsTheKeysItWants(t *testing.T) {
	lockThread(t)
	w := openW(t, "host keys")
	const esMultiline, esWantReturn = 0x0004, 0x1000
	edit := newControl(t, "EDIT", esMultiline|esWantReturn)
	id, err := w.NewHost(HostView, HostOptions{View: edit, Focusable: true})
	if err != nil {
		t.Fatal(err)
	}
	place(w, HostRegion{ID: id, Rect: [4]float32{10, 10, 200, 80}, Clip: [4]float32{10, 10, 200, 80}})
	w.FocusHost(id)
	settle(w)
	w.Events()
	postKey(edit, vkReturn)
	got := pumpEvents([]*Window{w}, 300*time.Millisecond, func([][]Event) bool { return false })[0]
	if hasKind(got, EvHostCommand) {
		t.Errorf("Enter a multi-line EDIT wants came back: %+v", got)
	}
	if s := windowText(edit); s != "\r\n" {
		t.Errorf("multi-line EDIT text %q after Enter, want a line break", s)
	}
}

// The pointer over a host is the host's: WindowFromPoint finds the
// control, and WM_SETCURSOR for it is left to it.
func TestPointerOverHostIsTheHosts(t *testing.T) {
	lockThread(t)
	w := openW(t, "host pointer")
	edit := newControl(t, "EDIT", 0)
	id, err := w.NewHost(HostView, HostOptions{View: edit})
	if err != nil {
		t.Fatal(err)
	}
	place(w, HostRegion{ID: id, Rect: [4]float32{10, 10, 200, 40}, Clip: [4]float32{10, 10, 200, 40}})
	w.Show()
	settle(w)
	const htClientMove = htClient | wmMouseMove<<16
	if r := win32.Call(pSendMessageW, w.hwnd, wmSetCursor, edit, htClientMove); r != 0 {
		t.Errorf("WM_SETCURSOR for the hosted control returned %d: the window took the control's cursor", r)
	}
	if r := win32.Call(pSendMessageW, w.hwnd, wmSetCursor, w.hwnd, htClientMove); r != 1 {
		t.Errorf("WM_SETCURSOR for the window returned %d, want 1", r)
	}
	pt := point{20, 20}
	syscall.SyscallN(htPClientToScreen, w.hwnd, uintptr(unsafe.Pointer(&pt))) // keeps the local valid, unlike win32.Call
	if at := win32.Call(htPWindowFromPoint, uintptr(*(*uint64)(unsafe.Pointer(&pt)))); at != edit && at != w.hwnd {
		t.Logf("WindowFromPoint is %#x (another window covers ours on this runner)", at)
	} else if at != edit {
		t.Errorf("WindowFromPoint over the host is %#x, want the EDIT %#x", at, edit)
	}
}

// Twenty windows with hosts opened and closed leave no host, window or GDI
// object behind; a window that closes releases its hosts.
func TestHostCyclesLeakNothing(t *testing.T) {
	lockThread(t)
	cycle := func() {
		w, err := Open(Options{Title: "cycle", Width: 200, Height: 120, Hidden: true})
		if err != nil {
			t.Skipf("no window: %v", err)
		}
		a, _ := w.NewHost(HostView, HostOptions{})
		b, _ := w.NewHost(HostView, HostOptions{View: newControl(t, "EDIT", 0)})
		place(w, HostRegion{ID: a, Rect: [4]float32{0, 0, 50, 50}, Clip: [4]float32{0, 0, 50, 50}},
			HostRegion{ID: b, Rect: [4]float32{0, 60, 50, 90}, Clip: [4]float32{0, 60, 50, 90}})
		w.FocusHost(b)
		Pump(0)
		w.Close()
		Pump(0)
	}
	cycle() // classes, first-use allocations
	hosts := ShellObjects().Hosts
	gdi0, user0 := guiObjects()
	for range 20 {
		cycle()
	}
	gdi1, user1 := guiObjects()
	if got := ShellObjects().Hosts; got != hosts {
		t.Errorf("hosts alive %d after 20 cycles, want %d", got, hosts)
	}
	if user1 > user0+2 || gdi1 > gdi0+2 {
		t.Errorf("GUI objects grew over 20 cycles: USER %d → %d, GDI %d → %d", user0, user1, gdi0, gdi1)
	}
}

// D66's gate: frames that move hosts allocate nothing.
func TestMovingHostsAllocatesNothing(t *testing.T) {
	lockThread(t)
	w := openW(t, "host allocs")
	a, _ := w.NewHost(HostView, HostOptions{})
	b, _ := w.NewHost(HostView, HostOptions{View: newControl(t, "EDIT", 0)})
	var regs [2]HostRegion
	y := float32(0)
	allocs := testing.AllocsPerRun(200, func() {
		y = float32(int(y+3) % 100)
		regs[0] = HostRegion{ID: a, Rect: [4]float32{0, y, 60, y + 40}, Clip: [4]float32{0, max(y, 10), 60, y + 40}}
		regs[1] = HostRegion{ID: b, Rect: [4]float32{70, 100 - y, 160, 130 - y}, Clip: [4]float32{70, 100 - y, 160, 130 - y}, Hidden: int(y)%2 == 0}
		w.SetHosts(regs[:])
		w.FlushShell()
	})
	if allocs != 0 {
		t.Errorf("a frame that moves hosts allocates %v times, want 0", allocs)
	}
}

var (
	htPClientToScreen  = win32.Proc(user32, "ClientToScreen")
	htPWindowFromPoint = win32.Proc(user32, "WindowFromPoint")
	htTextBuf          [256]uint16
)

const htWmNull = 0

func windowText(hwnd uintptr) string {
	n := win32.Call(htPGetWindowTextW, hwnd, uintptr(unsafe.Pointer(&htTextBuf[0])), uintptr(len(htTextBuf)))
	return syscall.UTF16ToString(htTextBuf[:n])
}

func lastOf(evs []Event, k EventKind) Event {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == k {
			return evs[i]
		}
	}
	return Event{}
}

var (
	htGdi32        = win32.LoadSystem("gdi32.dll")
	htCreateBrush  = win32.Proc(htGdi32, "CreateSolidBrush")
	htCreateDC     = win32.Proc(htGdi32, "CreateCompatibleDC")
	htCreateDIB    = win32.Proc(htGdi32, "CreateDIBSection")
	htSelectObject = win32.Proc(htGdi32, "SelectObject")
	htDeleteObject = win32.Proc(htGdi32, "DeleteObject")
	htDeleteDC     = win32.Proc(htGdi32, "DeleteDC")
	htGdiFlush     = win32.Proc(htGdi32, "GdiFlush")
	htPrintWindow  = win32.Proc(user32, "PrintWindow")
	htRedClass     = mustUTF16("GoNativeUITestRed")
	htRedWC        wndClassEx
	htBMI          [11]uint32 // BITMAPINFOHEADER (40 bytes) and a spare
	htBits         uintptr
)

// redChild is a top-level window of a class whose background is red, to
// host.
func redChild(t *testing.T) uintptr {
	t.Helper()
	if htRedWC.size == 0 {
		htRedWC = wndClassEx{wndProc: pDefWindowProcW, instance: win32.Call(pGetModuleHandleW, 0), className: htRedClass,
			background: win32.Call(htCreateBrush, 0x000000FF)}
		htRedWC.size = uint32(unsafe.Sizeof(htRedWC))
		if win32.Call(pRegisterClassExW, uintptr(unsafe.Pointer(&htRedWC)))&0xFFFF == 0 {
			t.Fatal("RegisterClassExW failed")
		}
	}
	h := win32.Call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(htRedClass)), 0, wsPopup, 0, 0, 10, 10, 0, 0, htRedWC.instance, 0)
	if h == 0 {
		t.Fatal("CreateWindowExW failed")
	}
	return h
}

// clientPixels reads the window's client area as DWM composes it, as
// 0xRRGGBB pixels, row by row (cw wide): one PrintWindow.
func clientPixels(t *testing.T, hwnd uintptr, cw, ch int32) []uint32 {
	t.Helper()
	htBMI = [11]uint32{40, uint32(cw), uint32(-ch), 1 | 32<<16}
	dc := win32.Call(htCreateDC, 0)
	defer win32.Call(htDeleteDC, dc)
	bmp := win32.Call(htCreateDIB, dc, uintptr(unsafe.Pointer(&htBMI)), 0, uintptr(unsafe.Pointer(&htBits)), 0, 0)
	if bmp == 0 {
		t.Fatal("CreateDIBSection failed")
	}
	defer win32.Call(htDeleteObject, bmp)
	old := win32.Call(htSelectObject, dc, bmp)
	defer win32.Call(htSelectObject, dc, old)
	if !win32.Bool(win32.Call(htPrintWindow, hwnd, dc, 3)) {
		t.Skip("PrintWindow failed")
	}
	win32.Call(htGdiFlush)
	out := make([]uint32, int(cw*ch))
	for i, p := range unsafe.Slice((*uint32)(win32.Ptr(htBits)), int(cw*ch)) {
		out[i] = p & 0xFFFFFF
	}
	return out
}

// clientPixel reads pixel (x, y) of the window's client area as DWM
// composes it (PrintWindow with PW_CLIENTONLY | PW_RENDERFULLCONTENT), as
// 0xRRGGBB.
func clientPixel(t *testing.T, hwnd uintptr, cw, ch, x, y int32) uint32 {
	t.Helper()
	htBMI = [11]uint32{40, uint32(cw), uint32(-ch), 1 | 32<<16} // top-down, 32 bpp, BI_RGB
	dc := win32.Call(htCreateDC, 0)
	defer win32.Call(htDeleteDC, dc)
	bmp := win32.Call(htCreateDIB, dc, uintptr(unsafe.Pointer(&htBMI)), 0, uintptr(unsafe.Pointer(&htBits)), 0, 0)
	if bmp == 0 {
		t.Fatal("CreateDIBSection failed")
	}
	defer win32.Call(htDeleteObject, bmp)
	old := win32.Call(htSelectObject, dc, bmp)
	defer win32.Call(htSelectObject, dc, old)
	if !win32.Bool(win32.Call(htPrintWindow, hwnd, dc, 3)) {
		t.Skip("PrintWindow failed")
	}
	win32.Call(htGdiFlush)
	px := unsafe.Slice((*uint32)(win32.Ptr(htBits)), int(cw*ch))
	return px[y*cw+x] & 0xFFFFFF
}

// A GDI control hosted in a window opened with ChildWindows shows on
// screen where it was placed, clipped by its container (probe P2 found it
// invisible in a window without a redirection surface).
func TestGDIHostShowsInAChildWindowsWindow(t *testing.T) {
	lockThread(t)
	w, err := Open(Options{Title: "gdi host", Width: 300, Height: 200, ChildWindows: true})
	if err != nil {
		t.Skipf("no window: %v", err)
	}
	t.Cleanup(w.Close)
	id, err := w.NewHost(HostView, HostOptions{View: redChild(t)})
	if err != nil {
		t.Fatal(err)
	}
	// The child pokes 20 px above its container (a scrolled list).
	place(w, HostRegion{ID: id, Rect: [4]float32{40, 20, 140, 100}, Clip: [4]float32{40, 40, 140, 100}})
	settle(w)
	cw, ch, _ := w.Size()
	if in := clientPixel(t, w.hwnd, cw, ch, 90, 70); in != 0xFF0000 {
		t.Errorf("pixel inside the host is %06x, want red ff0000", in)
	}
	if above := clientPixel(t, w.hwnd, cw, ch, 90, 30); above == 0xFF0000 {
		t.Error("the host shows above its container: not clipped")
	}
	place(w, HostRegion{ID: id, Rect: [4]float32{40, 20, 140, 100}, Clip: [4]float32{40, 40, 140, 100}, Hidden: true})
	// Hidden: the window, with no swap chain here and nothing painted in
	// GDI, keeps the child's old pixels in its surface, so the pixels prove
	// nothing; an app's swap chain shows its own frame there.
	if visible(w.hosts.slot(id).clip) {
		t.Error("a hidden host still shows")
	}
}

// postKey posts a key press as the keyboard delivers it: WM_KEYDOWN with
// the key's scan code, which TranslateMessage turns into its WM_CHAR, and
// WM_KEYUP.
func postKey(hwnd, vk uintptr) {
	scan := win32.Call(pMapVirtualKeyW, vk, 0 /* MAPVK_VK_TO_VSC */)
	win32.Call(pPostMessageW, hwnd, wmKeyDown, vk, 1|scan<<16)
	win32.Call(pPostMessageW, hwnd, wmKeyUp, vk, 1|scan<<16|3<<30)
}

// OnHostVisible reports a host shown and hidden by the placement, after
// the batch; HostCommand queues the command a control in another process
// (a WebView2 page) reports (H2).
func TestHostVisibilityAndCommandForAControl(t *testing.T) {
	lockThread(t)
	w := openW(t, "host visibility")
	id, err := w.NewHost(HostView, HostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var seen []bool
	w.OnHostVisible(id, func(shown bool) { seen = append(seen, shown) })
	r := HostRegion{ID: id, Rect: [4]float32{10, 10, 100, 60}, Clip: [4]float32{10, 10, 100, 60}}
	place(w, r)
	place(w, r) // unchanged: no call
	r.Hidden = true
	place(w, r)
	place(w) // not built: still hidden, no call
	if want := []bool{false, true, false}; fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("OnHostVisible calls %v, want %v (now, shown, hidden)", seen, want)
	}
	w.Events()
	w.HostCommand(id, KeyTab, ModShift)
	w.HostCommand(id+1<<8, KeyTab, 0) // stale: nothing
	got := pumpEvents([]*Window{w}, time.Second, func(g [][]Event) bool { return hasKind(g[0], EvHostCommand) })[0]
	if e := lastOf(got, EvHostCommand); e.Kind != EvHostCommand || HostID(e.Tag) != id || e.Key != KeyTab || e.Mods != ModShift {
		t.Errorf("HostCommand: %+v", got)
	}
	n := 0
	for _, e := range got {
		if e.Kind == EvHostCommand {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d host commands, want 1 (a stale id queues none)", n)
	}
}
