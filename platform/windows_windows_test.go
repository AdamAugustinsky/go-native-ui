//go:build windows

package platform

import (
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

// The window registry, the app-wide pump and the app window on Windows (the
// phase 8 plan's W1, D68). Each test runs on its own locked thread, which
// owns its windows and, with none open from another thread, the app window.

var (
	pGetGuiResources  = win32.Proc(user32, "GetGuiResources")
	pGetCurrentProc   = win32.Proc(kernel32, "GetCurrentProcess")
	pSetEvent         = win32.Proc(kernel32, "SetEvent")
	pBlockReasonQuery = win32.Proc(user32, "ShutdownBlockReasonQuery")
)

const (
	grGDIObjects  = 0
	grUserObjects = 1
)

// lockThread keeps the test on one OS thread: its windows belong to it.
func lockThread(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
}

// openW opens a window for a test (hidden: cloaked, as the app opens them)
// and closes it at the end.
func openW(t *testing.T, title string) *Window {
	t.Helper()
	w, err := Open(Options{Title: title, Width: 320, Height: 200, Hidden: true})
	if err != nil {
		t.Skipf("no window: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

// pumpEvents pumps for at most d, collecting each window's events, until done
// says the wait is over.
func pumpEvents(ws []*Window, d time.Duration, done func(got [][]Event) bool) [][]Event {
	got := make([][]Event, len(ws))
	for end := time.Now().Add(d); ; {
		Pump(int64(5 * time.Millisecond))
		for i, w := range ws {
			got[i] = append(got[i], w.Events()...)
		}
		if done(got) || time.Now().After(end) {
			return got
		}
	}
}

func hasKind(evs []Event, k EventKind) bool {
	for _, e := range evs {
		if e.Kind == k {
			return true
		}
	}
	return false
}

func countKind(evs []Event, k EventKind) int {
	n := 0
	for _, e := range evs {
		if e.Kind == k {
			n++
		}
	}
	return n
}

// settle pumps until the windows have nothing more to say (the opening's
// focus, size and paint messages).
func settle(ws ...*Window) {
	pumpEvents(ws, 300*time.Millisecond, func([][]Event) bool { return false })
}

// Input is routed by HWND: what is sent to one window becomes its events,
// and the other window gets none of it.
func TestTwoWindowsRouteInputByHWND(t *testing.T) {
	lockThread(t)
	w1, w2 := openW(t, "one"), openW(t, "two")
	settle(w1, w2)
	win32.Call(pPostMessageW, w1.hwnd, wmMouseMove, 0, 20<<16|10)
	win32.Call(pPostMessageW, w2.hwnd, wmKeyDown, 0x41, 0x001E0001) // VK A, scan code 0x1E
	win32.Call(pPostMessageW, w2.hwnd, wmChar, 'a', 0x001E0001)
	got := pumpEvents([]*Window{w1, w2}, 2*time.Second, func(g [][]Event) bool {
		return hasKind(g[0], EvPointerMove) && hasKind(g[1], EvKey) && hasKind(g[1], EvText)
	})
	var move Event
	for _, e := range got[0] {
		if e.Kind == EvPointerMove {
			move = e
		}
	}
	if move.Kind != EvPointerMove || move.X != 10 || move.Y != 20 {
		t.Errorf("window one: no pointer move at (10, 20): %+v", got[0])
	}
	if hasKind(got[0], EvKey) || hasKind(got[0], EvText) {
		t.Errorf("window one got window two's key: %+v", got[0])
	}
	if !hasKind(got[1], EvKey) || !hasKind(got[1], EvText) {
		t.Errorf("window two: no key and text: %+v", got[1])
	}
	if hasKind(got[1], EvPointerMove) {
		t.Errorf("window two got window one's pointer: %+v", got[1])
	}
	if OpenCount() != 2 {
		t.Errorf("OpenCount %d, want 2", OpenCount())
	}
}

// newWaitable is an auto-reset event that stands in for a swap chain's
// frame-latency waitable: signalled, it is a free frame slot.
func newWaitable(t *testing.T) uintptr {
	t.Helper()
	h := win32.Call(win32.Proc(kernel32, "CreateEventW"), 0, 0, 1, 0) // auto-reset, signalled (SetFrameSource takes the first)
	if h == 0 {
		t.Fatal("CreateEventW failed")
	}
	t.Cleanup(func() { win32.CloseHandle(h) })
	return h
}

// Each window is paced on its own: a window's frame comes when its own
// waitable is signalled, whatever the other's, and a window that did not
// ask for a frame gets none.
func TestTwoWindowsArePacedIndependently(t *testing.T) {
	lockThread(t)
	w1, w2 := openW(t, "one"), openW(t, "two")
	e1, e2 := newWaitable(t), newWaitable(t)
	w1.SetFrameSource(e1)
	w2.SetFrameSource(e2)
	settle(w1, w2)
	frames := func(d time.Duration, want1, want2 int) {
		t.Helper()
		got := pumpEvents([]*Window{w1, w2}, d, func(g [][]Event) bool {
			return countKind(g[0], EvFrame) >= want1 && countKind(g[1], EvFrame) >= want2 && (want1 > 0 || want2 > 0)
		})
		if n1, n2 := countKind(got[0], EvFrame), countKind(got[1], EvFrame); n1 != want1 || n2 != want2 {
			t.Errorf("frames %d and %d, want %d and %d", n1, n2, want1, want2)
		}
	}

	w1.RequestFrame()
	w2.RequestFrame()
	win32.Call(pSetEvent, e1)
	frames(time.Second, 1, 0) // only the window whose slot is free
	frames(100*time.Millisecond, 0, 0)
	win32.Call(pSetEvent, e2)
	frames(time.Second, 0, 1) // the other one, once its slot is free
	win32.Call(pSetEvent, e1)
	frames(100*time.Millisecond, 0, 0) // a free slot with no frame asked for gives none

	// Both free at once: both frames in the same pump.
	w1.RequestFrame()
	w2.RequestFrame()
	win32.Call(pSetEvent, e2)
	Pump(int64(time.Second))
	if n1, n2 := countKind(w1.Events(), EvFrame), countKind(w2.Events(), EvFrame); n1 != 1 || n2 != 1 {
		t.Errorf("both slots free: frames %d and %d, want 1 and 1", n1, n2)
	}
}

// A window's own Wake reaches it alone; the app-wide Wake reaches every
// window. Closing one window keeps the other drawing and taking input.
func TestClosingOneWindowKeepsTheOther(t *testing.T) {
	lockThread(t)
	w1 := openW(t, "one")
	w2 := openW(t, "two")
	settle(w1, w2)
	w1.Wake()
	got := pumpEvents([]*Window{w1, w2}, time.Second, func(g [][]Event) bool { return hasKind(g[0], EvWake) })
	if !hasKind(got[0], EvWake) || hasKind(got[1], EvWake) {
		t.Errorf("a window's Wake: %d and %d wakes, want 1 and 0", countKind(got[0], EvWake), countKind(got[1], EvWake))
	}
	Wake()
	got = pumpEvents([]*Window{w1, w2}, time.Second, func(g [][]Event) bool { return hasKind(g[0], EvWake) && hasKind(g[1], EvWake) })
	if !hasKind(got[0], EvWake) || !hasKind(got[1], EvWake) {
		t.Errorf("the app's Wake: %d and %d wakes, want 1 and 1", countKind(got[0], EvWake), countKind(got[1], EvWake))
	}

	w1.Close()
	if OpenCount() != 1 {
		t.Fatalf("OpenCount %d after a close, want 1", OpenCount())
	}
	w2.RequestFrame() // no waitable: the next pump
	win32.Call(pPostMessageW, w2.hwnd, wmMouseMove, 0, 5<<16|5)
	w2.Wake()
	got = pumpEvents([]*Window{w2}, 2*time.Second, func(g [][]Event) bool {
		return hasKind(g[0], EvFrame) && hasKind(g[0], EvPointerMove) && hasKind(g[0], EvWake)
	})
	if !hasKind(got[0], EvFrame) || !hasKind(got[0], EvPointerMove) || !hasKind(got[0], EvWake) {
		t.Errorf("the window left open: %+v", got[0])
	}
}

// guiObjects is the process's GDI and USER object counts (GetGuiResources).
func guiObjects() (gdi, user uintptr) {
	p := win32.Call(pGetCurrentProc)
	return win32.Call(pGetGuiResources, p, grGDIObjects), win32.Call(pGetGuiResources, p, grUserObjects)
}

// 20 windows opened and closed leave no window, GDI or USER object and no
// host behind (exit criterion 1).
func TestOpenCloseLeaksNoWindowObjects(t *testing.T) {
	lockThread(t)
	if pGetGuiResources == 0 {
		t.Skip("no GetGuiResources")
	}
	cycle := func() {
		w, err := Open(Options{Title: "cycle", Width: 240, Height: 160, Hidden: true})
		if err != nil {
			t.Fatal(err)
		}
		settle(w)
		w.Close()
		Pump(0)
	}
	cycle() // the class, the cursors and the app window are made once
	gdi0, user0 := guiObjects()
	for range 20 {
		cycle()
	}
	gdi1, user1 := guiObjects()
	// Fewer is not a leak: what an earlier test made (a file dialog's shell
	// windows) can be freed by the system during the cycles.
	if gdi1 > gdi0 || user1 > user0 {
		t.Errorf("after 20 open/close cycles: GDI %d → %d, USER %d → %d", gdi0, gdi1, user0, user1)
	}
	if OpenCount() != 0 {
		t.Errorf("OpenCount %d, want 0", OpenCount())
	}
	if c := ShellObjects(); c.Hosts != 0 || c.Materials != 0 {
		t.Errorf("shell objects %+v", c)
	}
}

// The app runs with zero windows: WaitApp waits in the app window, wakes for
// a Wake and otherwise sleeps through its timeout without a wakeup.
func TestWaitAppWithNoWindow(t *testing.T) {
	lockThread(t)
	if !ensureAppWindow() {
		t.Fatal("no app window")
	}
	for WaitApp(0) { // what earlier tests left
	}
	p0, w0 := PumpStats()
	start := time.Now()
	if WaitApp(int64(300 * time.Millisecond)) {
		t.Error("WaitApp reported a wake with none")
	}
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Errorf("WaitApp returned after %v, want its 300 ms timeout", d)
	}
	if p1, w1 := PumpStats(); p1-p0 != 1 || w1 != w0 {
		t.Errorf("an idle wait: %d pumps and %d wakes, want 1 and 0", p1-p0, w1-w0)
	}
	go func() { time.Sleep(50 * time.Millisecond); Wake() }()
	start = time.Now()
	if !WaitApp(int64(5 * time.Second)) {
		t.Error("WaitApp did not report the wake")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the wake took %v", d)
	}
}

// blockReason reads the app window's shutdown block reason ("" for none).
func blockReason() (string, bool) {
	var buf [256]uint16
	n := uint32(len(buf))
	if r, _, _ := syscall.SyscallN(pBlockReasonQuery, reg.app.hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); !win32.Bool(r) { // keeps the locals valid, unlike win32.Call
		return "", false
	}
	return syscall.UTF16ToString(buf[:]), true
}

// WM_QUERYENDSESSION, sent to the hidden window, is a QuitLogout request:
// answered during it, the answer is the message's result; left unanswered,
// the session is told to wait with the app's reason until ReplyQuit, which
// removes the reason; WM_ENDSESSION ends with AppEvWillTerminate.
func TestSessionEndAskedThroughTheQuitPolicy(t *testing.T) {
	lockThread(t)
	s := &recSink{}
	withSink(t, s)
	if err := StartApp(AppOptions{QuitBlockReason: "gnui test: saving"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unblockShutdown()
		resetQuit()
		quitState.approved.Store(false)
		reg.app.ending = false
		quitState.opts = AppOptions{}
		// The test's thread goes back to the runtime: no apartment stays on it.
		win32.Call(win32.Proc(ole32, "OleUninitialize"))
		reg.app.oleThread = 0
	})
	if reg.app.oleThread != uint32(win32.Call(pGetCurrentThreadID)) {
		t.Error("OleInitialize did not run on the app's thread")
	}
	h := AppWindow()
	query := func() uintptr { return win32.Call(pSendMessageW, h, wmQueryEndSess, 0, endSessionLogoff) }
	reset := func() {
		resetQuit()
		quitState.approved.Store(false)
		reg.app.ending = false
		s.evs, s.data = nil, nil
	}

	// Allowed inside the request.
	s.reply = 1
	if r := query(); r != 1 || !QuitApproved() {
		t.Errorf("allowed inside: result %d, approved %v", r, QuitApproved())
	}
	if len(s.evs) != 1 || s.evs[0].Kind != AppEvQuitRequested || QuitReason(s.evs[0].A) != QuitLogout {
		t.Errorf("events %+v, want one QuitLogout request", s.evs)
	}

	// Refused inside the request.
	reset()
	s.reply = 2
	if r := query(); r != 0 || QuitApproved() || QuitPending() {
		t.Errorf("refused inside: result %d, approved %v, pending %v", r, QuitApproved(), QuitPending())
	}
	if _, on := blockReason(); on {
		t.Error("a refusal left a block reason")
	}

	// Answered after the request: the session waits with the reason.
	reset()
	s.reply = 0
	if r := query(); r != 0 || !QuitPending() {
		t.Fatalf("unanswered: result %d, pending %v", r, QuitPending())
	}
	if why, on := blockReason(); !on || why != "gnui test: saving" {
		t.Errorf("block reason %q (%v), want the app's", why, on)
	}
	if r := query(); r != 0 { // asked again while the answer is awaited: still waiting
		t.Errorf("asked again: result %d", r)
	}
	id := s.evs[0].ID
	if !ReplyQuit(id, true) || !QuitApproved() {
		t.Fatalf("ReplyQuit after: approved %v", QuitApproved())
	}
	if _, on := blockReason(); on {
		t.Error("the block reason stayed after the answer")
	}
	if r := query(); r != 1 {
		t.Errorf("asked again once allowed: result %d, want 1", r)
	}
	win32.Call(pSendMessageW, h, wmEndSession, 1, endSessionLogoff)
	if n := len(s.evs); n == 0 || s.evs[n-1].Kind != AppEvWillTerminate {
		t.Errorf("WM_ENDSESSION: last event %+v, want AppEvWillTerminate", s.evs)
	}

	// Answered after a logout that was cancelled meanwhile: no reason stays.
	SetAppSink(s) // the last drain closed the gate
	reset()
	if r := query(); r != 0 || !QuitPending() {
		t.Fatalf("second unanswered: result %d, pending %v", r, QuitPending())
	}
	win32.Call(pSendMessageW, h, wmEndSession, 0, endSessionLogoff)
	if _, on := blockReason(); on {
		t.Error("a cancelled logout left the block reason")
	}
	if !ReplyQuit(s.evs[0].ID, false) || QuitApproved() {
		t.Error("refusing after the cancel")
	}
}

// The window commands act on the window and read back.
func TestWindowCommands(t *testing.T) {
	lockThread(t)
	w := openW(t, "commands")
	w.Show() // uncloak
	settle(w)
	w.SetTitle("renamed ✓")
	var buf [64]uint16
	n := win32.Call(win32.Proc(user32, "GetWindowTextW"), w.hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if got := syscall.UTF16ToString(buf[:n]); got != "renamed ✓" {
		t.Errorf("title %q", got)
	}
	w.SetFrameRect(100, 120, 500, 400)
	settle(w)
	if x, y, wd, ht := w.FrameRect(); x != 100 || y != 120 || wd != 500 || ht != 400 {
		t.Errorf("frame %v %v %v %v, want 100 120 500 400", x, y, wd, ht)
	}
	w.Zoom()
	settle(w)
	if !w.Zoomed() {
		t.Error("Zoom did not maximize")
	}
	w.Zoom()
	settle(w)
	if w.Zoomed() {
		t.Error("Zoom again did not restore")
	}
	w.Hide()
	got := pumpEvents([]*Window{w}, time.Second, func(g [][]Event) bool { return hasKind(g[0], EvVisible) })
	if w.Visible() || !hasKind(got[0], EvVisible) {
		t.Errorf("Hide: visible %v, events %+v", w.Visible(), got[0])
	}
	w.Activate()
	settle(w)
	if !w.Visible() {
		t.Error("Activate did not show the hidden window")
	}
	w.Miniaturize()
	settle(w)
	if !w.Miniaturized() {
		t.Error("Miniaturize")
	}
	w.Activate()
	settle(w)
	if w.Miniaturized() {
		t.Error("Activate did not restore the minimized window")
	}
	w.ToggleFullScreen()
	settle(w)
	if !w.FullScreen() {
		t.Error("ToggleFullScreen did not enter")
	}
	w.ToggleFullScreen()
	settle(w)
	if w.FullScreen() {
		t.Error("ToggleFullScreen did not leave")
	}
}

// Inside a native modal loop the app's loop is suspended: the app window's
// timer gives every window that asked for a frame its EvFrame and runs its
// frame handler, a window that did not ask draws nothing, and a wake runs
// the app's dispatcher there and arms the woken windows' frames.
func TestModalLoopKeepsWindowsDrawing(t *testing.T) {
	lockThread(t)
	s := &recSink{}
	withSink(t, s)
	w1, w2 := openW(t, "animating"), openW(t, "idle")
	w1.Show() // uncloaked: the menu's owner is on the screen
	w2.Show()
	settle(w1, w2)
	var n1, n2 int
	w1.SetFrameHandler(func() {
		for _, e := range w1.Pending() {
			if e.Kind == EvFrame {
				n1++
				w1.RequestFrame() // animating
			}
		}
	})
	w2.SetFrameHandler(func() {
		for _, e := range w2.Pending() {
			if e.Kind == EvFrame {
				n2++
			}
		}
	})
	w1.RequestFrame()
	pEndMenu := win32.Proc(user32, "EndMenu")
	timer := syscall.NewCallback(func(hwnd, msg, id, _ uintptr) uintptr {
		win32.Call(win32.Proc(user32, "KillTimer"), 0, id)
		win32.Call(pEndMenu)
		return 0
	})
	pCreatePopupMenu := win32.Proc(user32, "CreatePopupMenu")
	pAppendMenuW := win32.Proc(user32, "AppendMenuW")
	pTrackPopupMenuEx := win32.Proc(user32, "TrackPopupMenuEx")
	pDestroyMenu := win32.Proc(user32, "DestroyMenu")
	menu := win32.Call(pCreatePopupMenu)
	item := mustUTF16("item")
	win32.Call(pAppendMenuW, menu, 0, 1, uintptr(unsafe.Pointer(item)))
	defer win32.Call(pDestroyMenu, menu)

	// The menu's own loop (TrackPopupMenuEx: WM_ENTERMENULOOP to the owner),
	// ended after 600 ms by a thread timer that calls EndMenu; a wake from
	// another goroutine arrives in the middle.
	win32.Call(pSetTimer, 0, 0, 600, timer)
	go func() { time.Sleep(250 * time.Millisecond); w2.Wake(); WakeLoop() }()
	safe0 := s.safe
	start := time.Now()
	const tpmReturnCmd = 0x0100
	win32.Call(pTrackPopupMenuEx, menu, tpmReturnCmd, 50, 50, w1.hwnd, 0)
	took := time.Since(start)
	runtime.KeepAlive(item)
	if took < 400*time.Millisecond {
		t.Skipf("the menu's loop ran %v: no menu here (the session cannot show one)", took)
	}
	if NativeLoop() {
		t.Error("NativeLoop still true after the loop")
	}
	if n1 < 10 {
		t.Errorf("the animating window drew %d frames in a %v modal loop, want about one per 16 ms", n1, took)
	}
	if n2 != 1 {
		t.Errorf("the idle window drew %d frames, want 1 (for its wake)", n2)
	}
	if s.safe == safe0 {
		t.Error("the dispatcher did not run inside the modal loop")
	}
}
