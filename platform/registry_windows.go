package platform

// The window registry on Windows (phase 8 W1, D68; D30 ported): the types
// the multi-window pump is built on. Wave 0 fixes them and their small
// bookkeeping; W1 replaces theWindow with reg.win and makes the hidden app
// window real. Nothing here is used by the one-window path yet.
//
// One app-wide pump waits on every window's frame-latency waitable plus the
// message queue in one MsgWaitForMultipleObjectsEx. That call takes at most
// MAXIMUM_WAIT_OBJECTS-1 = 63 handles besides the queue (winbase.h), which
// is the budget: MaxWindows windows, the wake event, the appearance watcher
// and one spare.

// MaxWindows is how many windows can be open at once on Windows (the wait
// budget above; macOS allows 64).
const MaxWindows = 60

// Wait handles besides the windows' waitables (indices into waitSet.h after
// the windows).
const (
	waitWake       = iota // the wake event (osWake from any thread)
	waitAppearance        // the registry watcher of the system appearance
	waitSpare             // reserved (capture's or a service's event)
	numAppHandles
)

// maxWaitHandles is the whole budget given to MsgWaitForMultipleObjectsEx.
const maxWaitHandles = MaxWindows + numAppHandles

// winRegistry is every open window, in the order they opened, and the
// hidden app window (D68).
type winRegistry struct {
	slots [MaxWindows]*Window // by slot; nil: free
	open  [MaxWindows]*Window // the open windows, in the order they opened
	n     int
	gen   uint32 // the last generation given
	app   appWindow
}

// appWindow is the window that is never shown (D68): a top-level WS_POPUP
// with WS_EX_TOOLWINDOW, so it is in neither the taskbar nor Alt+Tab, and,
// unlike a message-only window, receives broadcasts. It owns the wake
// message, the tray's and hotkeys' messages, WM_QUERYENDSESSION and
// WM_ENDSESSION, TaskbarCreated and the second instance's hand-off, so the
// app runs with zero windows.
type appWindow struct {
	hwnd           uintptr
	taskbarCreated uint32 // RegisterWindowMessageW("TaskbarCreated")
	ending         bool   // between WM_QUERYENDSESSION allowed and WM_ENDSESSION
	blockReason    bool   // ShutdownBlockReasonCreate is in effect
}

// waitSet is the handle array of one wait and which window each handle is
// for, rebuilt only when windows open or close or arm their waitables.
type waitSet struct {
	h   [maxWaitHandles]uintptr
	win [maxWaitHandles]*Window // nil for the app handles
	n   int
}

// add puts w in the first free slot and at the end of the open order. It
// returns false when MaxWindows are open.
func (r *winRegistry) add(w *Window) (slot int, ok bool) {
	if r.n == MaxWindows {
		return -1, false
	}
	for i := range r.slots {
		if r.slots[i] == nil {
			r.slots[i] = w
			r.open[r.n] = w
			r.n++
			r.gen++
			return i, true
		}
	}
	return -1, false
}

// remove takes w out of the registry, keeping the others' order.
func (r *winRegistry) remove(w *Window) {
	for i := range r.slots {
		if r.slots[i] == w {
			r.slots[i] = nil
		}
	}
	for i := 0; i < r.n; i++ {
		if r.open[i] == w {
			copy(r.open[i:r.n], r.open[i+1:r.n])
			r.n--
			r.open[r.n] = nil
			return
		}
	}
}

// byHWND finds the window of a top-level HWND, nil for none (the app
// window, a dialog, another process's).
func (r *winRegistry) byHWND(hwnd uintptr) *Window {
	for i := 0; i < r.n; i++ {
		if r.open[i].hwnd == hwnd {
			return r.open[i]
		}
	}
	return nil
}

// fill rebuilds s from the open windows' armed waitables, then the app's
// handles (0 entries are skipped). It allocates nothing.
func (r *winRegistry) fill(s *waitSet, app [numAppHandles]uintptr) {
	s.n = 0
	for i := 0; i < r.n; i++ {
		if w := r.open[i]; w.waitable != 0 {
			s.h[s.n], s.win[s.n] = w.waitable, w
			s.n++
		}
	}
	for _, h := range app {
		if h != 0 {
			s.h[s.n], s.win[s.n] = h, nil
			s.n++
		}
	}
}
