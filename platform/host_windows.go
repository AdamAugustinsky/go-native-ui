//go:build windows

package platform

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Hosted native views on Windows (W3, D66): each host is a child window (the
// host's view) inside a container child window of the top-level window, whose
// rectangle is the host box's clip, so a host in a scrolled list is cut by
// the list. Children sit above the swap chain's content (P2); the top-level
// window has WS_CLIPCHILDREN. Hosts are made and released at safe points
// (NewHost, ReleaseHost); their geometry comes from ui after layout
// (SetHosts) and is applied in one DeferWindowPos batch right after the
// frame's Present (the app's FlushShell after the surface's EndFrame).
//
// Routing:
//   - the pointer over a visible host is the host's: Windows hit-tests child
//     windows, so ui gets the WM_MOUSELEAVE of the top-level window (an
//     EvPointerLeave), and WM_SETCURSOR for a child is left to the child;
//   - a wheel turn the host does not use travels up to the top-level window
//     (DefWindowProc), which queues it for ui;
//   - keys go to the window with the keyboard focus: while a host holds it,
//     they are the host's and are not queued for ui.
//
// Focus hand-off: the window's focus state follows the focus window
// (WM_SETFOCUS and WM_KILLFOCUS of the top-level window, and GetFocus after
// each batch of dispatched messages while hosts exist), so EvFocus reports
// the window and EvHostFocus the host. A deactivated window that had a host
// focused gives it back the focus when it is activated again. Tab, Shift+Tab,
// Escape and Enter that the focused control does not ask for
// (WM_GETDLGCODE, as IsDialogMessage asks) come back as EvHostCommand, so the
// app's own focus traversal continues.

// hostSlot is one host of the window.
type hostSlot struct {
	live         bool
	gen          uint32
	kind         HostKind
	clip, view   uintptr // the container child window and the host's view in it (both destroyed with the host)
	rect, clipR  [4]float32
	shown        bool
	onSize       func(w, h int32) // OnHostResize
	onShow       func(shown bool) // OnHostVisible
	lastW, lastH int32            // the view's size last placed
}

// hostState is the window's hosts.
type hostState struct {
	slots [MaxHosts]hostSlot
	want  [MaxHosts]HostRegion
	nWant int
	n     int // live hosts
	dirty bool
	focus int // 1 + the slot holding the keyboard focus, 0: the window itself (or nothing)
	// restore is 1 + the slot that had the focus when the window was
	// deactivated: it gets the focus back on activation.
	restore int
	applied int // applyHosts calls (tests)
	// flips are the slots whose visibility the batch changed (OnHostVisible)
	flips  [MaxHosts]int8
	nFlips int
}

// hostWin is the Window's part for hosts and the keyboard focus.
type hostWin struct {
	hosts hostState
	// kbd: the keyboard focus is in the window (itself or one of its hosts),
	// as last reported with EvFocus.
	kbd bool
	// gdiSurface: the window has a redirection surface (Options.ChildWindows).
	gdiSurface bool
}

const (
	wsChild         = 0x40000000
	wsClipChildren  = 0x02000000
	wsClipSiblings  = 0x04000000
	wsCaption       = 0x00C00000
	wsThickFrame    = 0x00040000
	wmActivate      = 0x0006
	wmGetDlgCode    = 0x0087
	waInactive      = 0
	dlgcWantTab     = 0x0002
	dlgcWantAllKeys = 0x0004
	vkTab           = 0x09
	vkReturn        = 0x0D
	vkEscape        = 0x1B
	swpShowWindow   = 0x0040
	swpHideWindow   = 0x0080
	swpNoCopyBits   = 0x0100
)

var (
	pSetParent      = win32.Proc(user32, "SetParent")
	pIsChild        = win32.Proc(user32, "IsChild")
	pGetFocus       = win32.Proc(user32, "GetFocus")
	pSetFocus       = win32.Proc(user32, "SetFocus")
	pBeginDeferPos  = win32.Proc(user32, "BeginDeferWindowPos")
	pDeferWindowPos = win32.Proc(user32, "DeferWindowPos")
	pEndDeferPos    = win32.Proc(user32, "EndDeferWindowPos")
	pGetWindow      = win32.Proc(user32, "GetWindow")

	hostClipClassName = mustUTF16("GoNativeUIHostClip")
	hostViewClassName = mustUTF16("GoNativeUIHostView")
	hostClasses       struct {
		done       bool
		clip, view wndClassEx
		viewProc   uintptr
	}

	liveHosts, shellApplies atomic.Int64

	// errNoButtons is NewHost's error for HostButton: Windows has no symbol
	// buttons to host; the app draws its buttons.
	errNoButtons = fmt.Errorf("platform: native buttons (HostButton) on %s: %w", runtime.GOOS, errors.ErrUnsupported)
)

// registerHostClasses registers the container's class (DefWindowProc, no
// background: it never draws) and the plain view's (a Go procedure for
// OnHostResize).
func registerHostClasses(hinst uintptr) error {
	if hostClasses.done {
		return nil
	}
	hostClasses.clip = wndClassEx{wndProc: pDefWindowProcW, instance: hinst, className: hostClipClassName}
	hostClasses.clip.size = uint32(unsafe.Sizeof(hostClasses.clip))
	if win32.Call(pRegisterClassExW, uintptr(unsafe.Pointer(&hostClasses.clip)))&0xFFFF == 0 {
		return errors.New("platform: RegisterClassExW (host container) failed")
	}
	if hostClasses.viewProc == 0 {
		hostClasses.viewProc = syscall.NewCallback(hostViewProc)
	}
	hostClasses.view = wndClassEx{wndProc: hostClasses.viewProc, instance: hinst, className: hostViewClassName}
	hostClasses.view.size = uint32(unsafe.Sizeof(hostClasses.view))
	if win32.Call(pRegisterClassExW, uintptr(unsafe.Pointer(&hostClasses.view)))&0xFFFF == 0 {
		return errors.New("platform: RegisterClassExW (host view) failed")
	}
	hostClasses.done = true
	return nil
}

func (h *hostState) id(i int) HostID { return HostID(h.slots[i].gen<<8 | uint32(i+1)) }

// slot is the live host id, or nil.
func (h *hostState) slot(id HostID) *hostSlot {
	i := int(id&0xFF) - 1
	if i < 0 || i >= MaxHosts {
		return nil
	}
	s := &h.slots[i]
	if !s.live || s.gen != uint32(id)>>8 {
		return nil
	}
	return s
}

// Backdrop is the material the window got: BackdropNone on Windows (native
// glass waits, D54). Experimental.
func (w *Window) Backdrop() Backdrop { return BackdropNone }

// HasButtons reports whether the window has native buttons: never on
// Windows. Experimental.
func (w *Window) HasButtons() bool { return false }

// SetButtonEnabled is a no-op on Windows (no native buttons). Experimental.
func (w *Window) SetButtonEnabled(id uint16, enabled bool) {}

// HasMaterials reports whether the window shows material regions: never on
// Windows.
func (w *Window) HasMaterials() bool { return false }

// SetMaterials is a no-op on Windows: the app draws its materials.
func (w *Window) SetMaterials(r []MaterialRegion) {}

// NewHost creates a hosted native view, hidden until SetHosts places it: a
// container child window and, inside it, the view. HostView takes the HWND
// in HostOptions.View, which becomes the container's child (its style is
// made WS_CHILD); without one, and for HostGlass, the view is a plain child
// window of ours, into which the app puts its own child windows (HostView
// returns it). HostButton is refused on Windows (the app draws its buttons).
// At a safe point (not inside a build), main thread; it may allocate. At
// most MaxHosts per window (ErrTooManyHosts).
func (w *Window) NewHost(kind HostKind, o HostOptions) (HostID, error) {
	if kind == HostButton {
		return 0, errNoButtons
	}
	if w.hwnd == 0 {
		return 0, ErrNoHosts
	}
	h := &w.hosts
	i := -1
	for j := range h.slots {
		if !h.slots[j].live {
			i = j
			break
		}
	}
	if i < 0 {
		return 0, ErrTooManyHosts
	}
	if err := registerHostClasses(w.hinst); err != nil {
		return 0, err
	}
	clip := win32.Call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(hostClipClassName)), 0,
		wsChild|wsClipChildren|wsClipSiblings, 0, 0, 0, 0, w.hwnd, 0, w.hinst, 0)
	if clip == 0 {
		return 0, errors.New("platform: CreateWindowExW (host container) failed")
	}
	view := o.View
	if view != 0 {
		st := win32.Call(pGetWindowLong, view, gwlStyleArg)
		st = st&^(wsPopup|wsCaption|wsThickFrame) | wsChild | wsVisible | wsClipSiblings
		win32.Call(pSetWindowLong, view, gwlStyleArg, st)
		win32.Call(pSetParent, view, clip)
		if !win32.Bool(win32.Call(pIsChild, clip, view)) {
			win32.Call(pDestroyWindow, clip)
			return 0, errors.New("platform: SetParent (host view) failed")
		}
		win32.Call(pSetWindowPos, view, 0, 0, 0, 0, 0, swpNoZOrder|swpNoActivate|swpFrameChanged)
	} else {
		view = win32.Call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(hostViewClassName)), 0,
			wsChild|wsVisible|wsClipChildren|wsClipSiblings, 0, 0, 0, 0, clip, 0, w.hinst, 0)
		if view == 0 {
			win32.Call(pDestroyWindow, clip)
			return 0, errors.New("platform: CreateWindowExW (host view) failed")
		}
	}
	s := &h.slots[i]
	gen := s.gen + 1
	if gen >= 1<<24 {
		gen = 1
	}
	*s = hostSlot{live: true, gen: gen, kind: kind, clip: clip, view: view}
	h.n++
	h.dirty = true
	liveHosts.Add(1)
	return h.id(i), nil
}

// ReleaseHost removes host id and destroys its container and the view in it
// (a HostView's own window too: an app that keeps its window, such as a
// browser's controller, closes it first). The keyboard focus, if the host
// held it, goes back to the window. At a safe point, main thread. A stale id
// does nothing.
func (w *Window) ReleaseHost(id HostID) {
	s := w.hosts.slot(id)
	if s == nil {
		return
	}
	i := int(id&0xFF) - 1
	if w.hosts.focus == i+1 {
		if w.hwnd != 0 {
			win32.Call(pSetFocus, w.hwnd) // WM_SETFOCUS queues EvHostFocus(0)
		}
		if w.hosts.focus == i+1 { // the window could not take it
			w.hosts.focus = 0
			w.q.push(Event{Kind: EvHostFocus, Time: Now()})
		}
	}
	if w.hosts.restore == i+1 {
		w.hosts.restore = 0
	}
	win32.Call(pDestroyWindow, s.clip)
	gen := s.gen
	*s = hostSlot{gen: gen}
	w.hosts.n--
	w.hosts.dirty = true
	liveHosts.Add(-1)
}

// releaseAllHosts releases every host (Close).
func (w *Window) releaseAllHosts() {
	for i := range w.hosts.slots {
		if w.hosts.slots[i].live {
			w.ReleaseHost(w.hosts.id(i))
		}
	}
	w.hosts = hostState{}
}

// HostView is host id's view, an HWND: the window given in
// HostOptions.View, or the plain child window NewHost made (put child windows
// of your own in it). 0 for a stale id. Main thread.
func (w *Window) HostView(id HostID) uintptr {
	if s := w.hosts.slot(id); s != nil {
		return s.view
	}
	return 0
}

// OnHostResize calls fn with the view's new size in pixels each time the
// placement changes it (after SetHosts, in the frame's batch), and once now
// with its size. A hosted control that does not follow its window's size by
// itself (a WebView2 controller's bounds) is sized from it. nil removes it.
// Windows only. Main thread.
func (w *Window) OnHostResize(id HostID, fn func(width, height int32)) {
	s := w.hosts.slot(id)
	if s == nil {
		return
	}
	s.onSize = fn
	if fn != nil {
		fn(s.lastW, s.lastH)
	}
}

// OnHostVisible calls fn each time the placement shows or hides host id (a
// host not built in a frame, or hidden under a popup, is hidden), after the
// frame's batch, and once now with whether it shows. A hosted control that
// works while hidden unless told (a WebView2 controller, which is made
// invisible and suspended) follows it. nil removes it. Windows only. Main
// thread.
func (w *Window) OnHostVisible(id HostID, fn func(shown bool)) {
	s := w.hosts.slot(id)
	if s == nil {
		return
	}
	s.onShow = fn
	if fn != nil {
		fn(s.shown)
	}
}

// HostCommand queues EvHostCommand for host id, as a Tab, Shift+Tab, Escape
// or Enter the host's control does not use: for a control whose keys the
// pump never sees (a WebView2 page, in its own process, which reports a Tab
// out of the page as MoveFocusRequested). Main thread. A stale id does
// nothing.
func (w *Window) HostCommand(id HostID, k Key, m Mods) {
	if w.hosts.slot(id) != nil {
		w.hostCommand(int(id&0xFF)-1, k, m)
	}
}

// SetHosts sets where the hosts are this frame (ui's host regions). It is
// copied and compared with the last list: unchanged, nothing happens;
// changed, the geometry is staged and applied right after the next present
// (FlushShell). Hosts not in the list are hidden; stale ids are ignored.
// Main thread; allocation-free.
func (w *Window) SetHosts(r []HostRegion) {
	h := &w.hosts
	if len(r) > MaxHosts {
		r = r[:MaxHosts]
	}
	if h.nWant == len(r) && h.want == hostArray(r) {
		return
	}
	copy(h.want[:], r)
	clear(h.want[len(r):])
	h.nWant = len(r)
	h.dirty = true
}

func hostArray(r []HostRegion) (a [MaxHosts]HostRegion) {
	copy(a[:], r)
	return a
}

// FocusHost gives the keyboard focus to host id's view (a HostGlass's or a
// plain view's first child, if it has one), or with id 0 to the window
// itself. EvHostFocus follows. Main thread, at a safe point.
func (w *Window) FocusHost(id HostID) {
	if w.hwnd == 0 {
		return
	}
	target := w.hwnd
	if id != 0 {
		s := w.hosts.slot(id)
		if s == nil {
			return
		}
		target = s.view
		if o := s.kind; o == HostGlass || isHostViewClass(s.view) {
			if c := win32.Call(pGetWindow, s.view, 5 /* GW_CHILD */); c != 0 {
				target = c
			}
		}
	}
	win32.Call(pSetFocus, target)
	w.syncFocus(win32.Call(pGetFocus))
}

// isHostViewClass reports whether hwnd is a plain view NewHost made.
func isHostViewClass(hwnd uintptr) bool {
	return hostClasses.done && hostViewOf(hwnd) != nil
}

// FlushShell applies the staged host geometry: one DeferWindowPos batch.
// The app calls it right after the frame's present. Nothing happens when
// nothing is staged. Main thread.
func (w *Window) FlushShell() {
	if w.hosts.dirty {
		w.applyHosts()
	}
}

// ShellPending reports whether a window has staged host changes. Main
// thread.
func ShellPending() bool {
	for _, w := range openWindows() {
		if w.hosts.dirty {
			return true
		}
	}
	return false
}

// ApplyShell applies every window's staged host changes. Main thread.
func ApplyShell() {
	for _, w := range openWindows() {
		w.FlushShell()
	}
}

// ShellObjects counts the native shell objects alive: hosts, and the
// applies of staged geometry to windows that have hosts (each in step with
// a present: Synced equals Applies, since a batch is applied right after the
// present it was drawn for).
func ShellObjects() ShellCounts {
	a := shellApplies.Load()
	return ShellCounts{Hosts: liveHosts.Load(), Applies: a, Synced: a}
}

// applyHosts places, shows and hides the hosts as want says. The
// containers, children of the window, move in one DeferWindowPos batch (a
// batch takes windows of one parent only); each view, alone in its
// container, is placed first with SetWindowPos.
func (w *Window) applyHosts() {
	h := &w.hosts
	h.dirty = false
	h.applied++
	if h.n == 0 || w.hwnd == 0 {
		return
	}
	shellApplies.Add(1)
	dwp := win32.Call(pBeginDeferPos, uintptr(h.n))
	for i := range h.slots {
		s := &h.slots[i]
		if !s.live {
			continue
		}
		var r *HostRegion
		id := h.id(i)
		for j := 0; j < h.nWant; j++ {
			if h.want[j].ID == id {
				r = &h.want[j]
				break
			}
		}
		show := r != nil && !r.Hidden && r.Clip[2] > r.Clip[0] && r.Clip[3] > r.Clip[1]
		if show {
			cx, cy := px(r.Clip[0]), px(r.Clip[1])
			cw, ch := px(r.Clip[2])-cx, px(r.Clip[3])-cy
			f := uintptr(swpNoZOrder | swpNoActivate)
			if !s.shown {
				f |= swpShowWindow
			}
			if !s.shown || r.Clip != s.clipR {
				dwp = deferPos(dwp, s.clip, cx, cy, cw, ch, f)
			}
			if !s.shown || r.Clip != s.clipR || r.Rect != s.rect {
				vx, vy := px(r.Rect[0])-cx, px(r.Rect[1])-cy
				vw, vh := px(r.Rect[2])-px(r.Rect[0]), px(r.Rect[3])-px(r.Rect[1])
				win32.Call(pSetWindowPos, s.view, 0, uintptr(vx), uintptr(vy), uintptr(vw), uintptr(vh), swpNoZOrder|swpNoActivate)
				if vw != s.lastW || vh != s.lastH {
					s.lastW, s.lastH = vw, vh
					if s.onSize != nil {
						s.onSize(vw, vh)
					}
				}
			}
			s.rect, s.clipR = r.Rect, r.Clip
		} else if s.shown {
			dwp = deferPos(dwp, s.clip, 0, 0, 0, 0, swpNoZOrder|swpNoActivate|swpNoMove|swpNoSize|swpHideWindow)
		}
		if show != s.shown && s.onShow != nil {
			h.flips[h.nFlips] = int8(i)
			h.nFlips++
		}
		s.shown = show
	}
	if dwp != 0 {
		win32.Call(pEndDeferPos, dwp)
	}
	for _, i := range h.flips[:h.nFlips] { // after the batch: the windows show as they say
		if s := &h.slots[i]; s.live && s.onShow != nil {
			s.onShow(s.shown)
		}
	}
	h.nFlips = 0
}

// deferPos adds one window to the batch; when the batch failed (0), the
// window is placed at once.
func deferPos(dwp, hwnd uintptr, x, y, cw, ch int32, flags uintptr) uintptr {
	if dwp != 0 {
		if n := win32.Call(pDeferWindowPos, dwp, hwnd, 0, uintptr(x), uintptr(y), uintptr(cw), uintptr(ch), flags); n != 0 {
			return n
		}
	}
	win32.Call(pSetWindowPos, hwnd, 0, uintptr(x), uintptr(y), uintptr(cw), uintptr(ch), flags)
	return 0
}

// px rounds a pixel coordinate.
func px(v float32) int32 {
	if v < 0 {
		return int32(v - 0.5)
	}
	return int32(v + 0.5)
}

// hostOf is 1 + the slot of the host that hwnd is (or is inside), 0 if
// none.
func (w *Window) hostOf(hwnd uintptr) int {
	if hwnd == 0 || hwnd == w.hwnd || w.hosts.n == 0 {
		return 0
	}
	for i := range w.hosts.slots {
		s := &w.hosts.slots[i]
		if s.live && (hwnd == s.clip || win32.Bool(win32.Call(pIsChild, s.clip, hwnd))) {
			return i + 1
		}
	}
	return 0
}

// hostFocused reports whether a host holds the keyboard focus: keys then go
// to it, not to ui.
func (w *Window) hostFocused() bool { return w.hosts.focus != 0 }

// syncFocus brings the window's focus state up to date with focus window f
// (GetFocus, or what WM_SETFOCUS and WM_KILLFOCUS name): EvFocus when the
// focus came into the window or left it, EvHostFocus when the host holding
// it changed. While the focus is outside the window the host that had it
// stays recorded, as the first responder of an inactive window does.
func (w *Window) syncFocus(f uintptr) {
	if w.hwnd == 0 {
		return
	}
	in := f != 0 && (f == w.hwnd || win32.Bool(win32.Call(pIsChild, w.hwnd, f)))
	t := Now()
	if in != w.kbd {
		w.kbd = in
		if in {
			reg.focus = w
			w.q.push(Event{Kind: EvFocus, Flags: FlagDown, Time: t})
			focusModifiers(&w.q, &w.lastMods, currentMods(), t) // keys pressed while another window had the focus
		} else {
			w.q.push(Event{Kind: EvFocus, Time: t})
			focusModifiers(&w.q, &w.lastMods, 0, t) // a release elsewhere never arrives
		}
	}
	if !in {
		return
	}
	if hf := w.hostOf(f); hf != w.hosts.focus {
		w.hosts.focus = hf
		e := Event{Kind: EvHostFocus, Time: t}
		if hf != 0 {
			e.Tag = uint64(w.hosts.id(hf - 1))
		}
		w.q.push(e)
	}
}

// onActivate is the top-level window's WM_ACTIVATE: on deactivation it
// remembers the host that held the focus; on activation it gives that host
// the focus back (DefWindowProc would give it to the window itself) and
// reports true, so DefWindowProc is not called.
func (w *Window) onActivate(wp uintptr) bool {
	if uloword(wp) == waInactive {
		w.hosts.restore = w.hosts.focus
		return false
	}
	r := w.hosts.restore
	w.hosts.restore = 0
	if uhiword(wp) != 0 || r == 0 || !w.hosts.slots[r-1].live {
		return false
	}
	w.FocusHost(w.hosts.id(r - 1))
	return true
}

// hostCommand queues EvHostCommand for slot i.
func (w *Window) hostCommand(i int, k Key, m Mods) {
	w.q.push(Event{Kind: EvHostCommand, Tag: uint64(w.hosts.id(i)), Key: k, Mods: m, Time: Now()})
}

// hostKey is the pump's look at a queued key message before it is
// translated and dispatched: Tab, Shift+Tab, Escape and Enter going to a
// control inside a host, which does not ask for them (WM_GETDLGCODE), come
// back to the app as EvHostCommand and reach the control no further. It
// reports whether it took the message.
func hostKey(m *msg) bool {
	if m.message != wmKeyDown || liveHosts.Load() == 0 {
		return false
	}
	var k Key
	switch m.wParam {
	case vkTab:
		k = KeyTab
	case vkEscape:
		k = KeyEscape
	case vkReturn:
		k = KeyEnter
	default:
		return false
	}
	for _, w := range openWindows() {
		i := w.hostOf(m.hwnd)
		if i == 0 {
			continue
		}
		code := win32.Call(pSendMessageW, m.hwnd, wmGetDlgCode, m.wParam, uintptr(unsafe.Pointer(m)))
		if code&dlgcWantAllKeys != 0 /* and DLGC_WANTMESSAGE, the same bit */ || k == KeyTab && code&dlgcWantTab != 0 {
			return false
		}
		w.hostCommand(i-1, k, currentMods()&ModShift)
		return true
	}
	return false
}

// syncHostFocus follows the focus window after a batch of dispatched
// messages, for the moves Windows tells the top-level window nothing about
// (from one host to another, or out of a host to another app). Only windows
// with hosts look.
func syncHostFocus() {
	if liveHosts.Load() == 0 {
		return
	}
	f := uintptr(0)
	got := false
	for _, w := range openWindows() {
		if w.hosts.n == 0 {
			continue
		}
		if !got {
			f, got = win32.Call(pGetFocus), true
		}
		w.syncFocus(f)
	}
}

// hostViewOf is the window and slot whose plain view is hwnd.
func hostViewOf(hwnd uintptr) *hostSlot {
	for _, w := range openWindows() {
		for i := range w.hosts.slots {
			if s := &w.hosts.slots[i]; s.live && s.view == hwnd {
				return s
			}
		}
	}
	return nil
}

// hostViewProc is the plain view's window procedure: it draws nothing (the
// container's area shows the swap chain's pixels until the app's own child
// windows cover it).
func hostViewProc(hwnd, m, wp, lp uintptr) uintptr {
	if m == wmEraseBkgnd {
		return 1
	}
	return win32.Call(pDefWindowProcW, hwnd, m, wp, lp)
}
