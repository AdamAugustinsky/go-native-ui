//go:build windows

package platform

import (
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/testlock"
	"gonativeui/internal/win32"
)

// Panel windows on Windows (the phase 8 plan's W2, D69). Each test runs on
// its own locked thread, as W1's do (windows_windows_test.go).

const (
	wsExAppWindow = 0x00040000
)

// openPanel opens a panel for a test, cloaked (as package app opens one)
// unless shown, and closes it at the end.
func openPanel(t *testing.T, p Panel) *Window {
	t.Helper()
	w, err := Open(Options{Title: "panel", Width: 320, Height: 90, Hidden: true, Panel: &p})
	if err != nil {
		t.Skipf("no panel: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

// hitTest asks the window what the client point (x, y) is (WM_NCHITTEST,
// sent with the point in screen coordinates).
func hitTest(w *Window, x, y int32) uintptr {
	pt := point{x, y}
	win32.Call(win32.Proc(user32, "ClientToScreen"), w.hwnd, uintptr(unsafe.Pointer(&pt)))
	return win32.Call(pSendMessageW, w.hwnd, wmNcHitTest, 0, uintptr(uint16(int16(pt.x)))|uintptr(uint16(int16(pt.y)))<<16)
}

// foreground opens an ordinary window and makes it the foreground window,
// or skips the test where the session does not allow it.
func foreground(t *testing.T) *Window {
	t.Helper()
	testlock.Hold(t, "activation")
	w := openW(t, "main")
	w.Show()
	w.Activate()
	settle(w)
	if !w.KeyWindow() {
		t.Skip("this session gives the test's window no foreground")
	}
	return w
}

// Each Panel field reads back as D69 says: WS_POPUP without a caption or a
// frame, a tool window (no taskbar button, not in Alt+Tab), topmost when
// Floating or StatusBar, never activated when NonActivating, out of captures
// when ExcludeFromCapture, DWM's shadow for Shadow and its corners for
// Radius; its drag regions are its caption.
func TestPanelReadBack(t *testing.T) {
	lockThread(t)
	cases := []struct {
		name string
		p    Panel
	}{
		{"zero", Panel{}},
		{"palette", Panel{Floating: true, HideOnDeactivate: true, Shadow: true, Radius: 12}},
		{"recording bar", Panel{StatusBar: true, NonActivating: true, AllSpaces: true, ExcludeFromCapture: true, Shadow: true, Radius: 4}},
	}
	for _, c := range cases {
		w := openPanel(t, c.p)
		settle(w)
		s := w.DebugPanelState()
		t.Logf("%s: %+v", c.name, s)
		if s.Style&wsPopup == 0 || s.Style&(wsCaption|wsThickFrame) != 0 {
			t.Errorf("%s: style %#x, want WS_POPUP with no caption or frame", c.name, s.Style)
		}
		if s.ExStyle&wsExToolWindow == 0 || s.ExStyle&wsExAppWindow != 0 {
			t.Errorf("%s: extended style %#x, want a tool window", c.name, s.ExStyle)
		}
		if top := s.ExStyle&wsExTopmost != 0; top != (c.p.Floating || c.p.StatusBar) {
			t.Errorf("%s: topmost %v", c.name, top)
		}
		if na := s.ExStyle&wsExNoActivate != 0; na != c.p.NonActivating {
			t.Errorf("%s: WS_EX_NOACTIVATE %v", c.name, na)
		}
		wantAff := uint32(0)
		if c.p.ExcludeFromCapture {
			wantAff = wdaExcludeFromCapture
		}
		if s.DisplayAffinity != wantAff && !(wantAff != 0 && s.DisplayAffinity == wdaMonitor) {
			t.Errorf("%s: display affinity %#x, want %#x", c.name, s.DisplayAffinity, wantAff)
		}
		if c.p.Shadow && !s.NCRendering {
			t.Errorf("%s: no non-client rendering: DWM draws no shadow", c.name)
		}
		wantCorner := uint32(dwmwcpDoNotRound)
		switch {
		case c.p.Radius > 6:
			wantCorner = dwmwcpRound
		case c.p.Radius > 0:
			wantCorner = dwmwcpRoundSmall
		}
		if s.Corner != wantCorner {
			t.Errorf("%s: corner preference %d, want %d", c.name, s.Corner, wantCorner)
		}
		if !w.IsPanel() || w.Visible() {
			t.Errorf("%s: IsPanel %v, visible %v before Show", c.name, w.IsPanel(), w.Visible())
		}
		w.SetDragRegions([]DragRegion{{Rect: [4]float32{0, 0, float32(w.w), 20}}, {Rect: [4]float32{0, 0, 30, 20}, Block: true}})
		if got := hitTest(w, 100, 10); got != htCaption {
			t.Errorf("%s: its drag region hit-tests as %d, want HTCAPTION", c.name, got)
		}
		if got, got2 := hitTest(w, 10, 10), hitTest(w, 100, 50); got != htClientArea || got2 != htClientArea {
			t.Errorf("%s: a block and the content hit-test as %d and %d, want HTCLIENT", c.name, got, got2)
		}
		if c.p.NonActivating {
			if r := win32.Call(pSendMessageW, w.hwnd, wmMouseActivate, w.hwnd, htClientArea|wmLButtonDown<<16); r != maNoActivate {
				t.Errorf("%s: WM_MOUSEACTIVATE answered %d, want MA_NOACTIVATE", c.name, r)
			}
		}
		w.Close()
	}
	w := openW(t, "plain")
	if w.IsPanel() || w.DrawnCaption() {
		t.Error("an ordinary window is a panel or draws its caption")
	}
}

// A NonActivating panel shown while another window has the foreground
// leaves it there, and is on the screen.
func TestPanelNonActivatingKeepsTheForeground(t *testing.T) {
	lockThread(t)
	main := foreground(t)
	p := openPanel(t, Panel{NonActivating: true, Floating: true})
	p.Show()
	settle(main, p)
	if !p.Visible() {
		t.Error("the shown panel is not visible")
	}
	if !main.KeyWindow() || p.KeyWindow() {
		t.Errorf("the panel took the foreground: main %v, panel %v", main.KeyWindow(), p.KeyWindow())
	}
}

// A palette (a panel that takes the keyboard) takes the foreground when
// shown; HideOnDeactivate hides it when another window is activated, which
// it reports as EvVisible, and Show brings it back.
func TestPanelHideOnDeactivate(t *testing.T) {
	lockThread(t)
	main := foreground(t)
	p := openPanel(t, Panel{Floating: true, HideOnDeactivate: true, Radius: 12, Shadow: true})
	settle(main, p)
	p.Show()
	got := pumpEvents([]*Window{p}, 2*time.Second, func(g [][]Event) bool { return p.KeyWindow() && hasKind(g[0], EvFocus) })
	if !p.Visible() || !p.KeyWindow() || !hasKind(got[0], EvFocus) {
		t.Fatalf("shown: visible %v, key %v, events %+v", p.Visible(), p.KeyWindow(), got[0])
	}
	main.Activate()
	got = pumpEvents([]*Window{p}, 2*time.Second, func(g [][]Event) bool { return !p.Visible() && hasKind(g[0], EvVisible) })
	if p.Visible() || p.DebugPanelState().HiddenByDeact != 1 {
		t.Fatalf("deactivated: visible %v, hidden by deactivation %d times; events %+v", p.Visible(), p.DebugPanelState().HiddenByDeact, got[0])
	}
	var vis *Event
	for i := range got[0] {
		if got[0][i].Kind == EvVisible {
			vis = &got[0][i]
		}
	}
	if vis == nil || vis.Flags&FlagDown != 0 {
		t.Errorf("no EvVisible (hidden) for the deactivation: %+v", got[0])
	}
	p.Show()
	settle(main, p)
	if !p.Visible() || !p.KeyWindow() {
		t.Errorf("shown again: visible %v, key %v", p.Visible(), p.KeyWindow())
	}
}

// A panel shown and hidden 100 times leaves no GDI or USER object behind.
func TestPanelShowHideLeaksNothing(t *testing.T) {
	lockThread(t)
	if pGetGuiResources == 0 {
		t.Skip("no GetGuiResources")
	}
	p := openPanel(t, Panel{Floating: true, Shadow: true, Radius: 12})
	p.Show()
	p.Hide()
	settle(p)
	gdi0, user0 := guiObjects()
	for range 100 {
		p.Show()
		Pump(0)
		p.Hide()
		Pump(0)
	}
	settle(p)
	gdi1, user1 := guiObjects()
	// The session's own objects move the counts by one either way on the
	// runners (41 → 40, 48 → 49 seen on windows-11-arm); a leak per Show or
	// Hide would add about 100.
	if gdi1 > gdi0+2 || user1 > user0+2 {
		t.Errorf("after 100 Show/Hide pairs: GDI %d → %d, USER %d → %d", gdi0, gdi1, user0, user1)
	}
	if p.Visible() {
		t.Error("the panel is visible after Hide")
	}
}
