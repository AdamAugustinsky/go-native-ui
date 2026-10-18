//go:build linux

package platform

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// Native carries the OS handles the gpu package needs.
//
//	Wayland: Display = wl_display*, Surface = wl_surface*
//	X11:     Display = Display*,    X11Window = Window id
type Native struct {
	Wayland                     bool
	Display, Surface, X11Window uintptr
}

const (
	defaultWidth  = 960
	defaultHeight = 600
)

// fr is the one ffi.Frame for platform calls. It is only used on the main
// thread, including from inside the Wayland dispatcher callback (nested use
// is safe, see package ffi).
var fr = new(ffi.Frame)

// Window is the (single) native window. It lives in a package variable so
// every pointer the C side keeps into it (argument arrays, buffers) stays
// valid and the dispatcher callback can find it without allocation.
type Window struct {
	q    eventQueue
	open bool
	wl   bool // Wayland backend; otherwise X11

	efd int // eventfd for osWake
	// pfd is the poll set: the display/connection fd, the eventfd, then the
	// descriptors of in-flight clipboard transfers (clipPollFDs); npfd of
	// them are polled.
	pfd  [maxPollFDs]pollfd
	npfd int
	ts   syscall.Timespec
	efdb [8]byte // eventfd drain buffer

	// Logical size, scale in 1/120 units and the derived pixel size.
	lw, lh   int32
	s120     int32
	pw, ph   int32
	repW     int32 // what the app was last told (Size or EvResize)
	repH     int32
	repS     int32
	mods     Mods
	visible  bool // first frame/map was reported
	frameReq bool // X11: RequestFrame pending

	drags dragMap   // SetDragRegions
	clip  clipboard // clipboard_linux.go

	hidden   bool           // X11: opened unmapped, not shown yet (Options.Hidden)
	showBy   int64          // when a hidden window is shown regardless (nanotime)
	first    firstFrame     // SetFirstFrame
	resizes  []ResizeRegion // SetResizeRegions
	closeReq bool           // RequestClose: EvClose at the next Wait

	// Pointer: the app's cursor map, where the pointer is, and the shape shown.
	cur        cursorMap
	ptrIn      bool
	ptrX, ptrY float32 // physical pixels
	curShape   shape
	curSet     bool

	wy wayland
	x  x11
}

var theWindow Window

// maxPollFDs bounds the poll set: connection, eventfd, one paste pipe and
// maxSends clipboard writers.
const maxPollFDs = 3 + maxSends

type pollfd struct {
	fd      int32
	events  int16
	revents int16
}

const (
	pollIn  = 0x1
	pollOut = 0x4
	pollErr = 0x8
	pollHup = 0x10
)

// wakeFD is the eventfd osWake writes to, or -1 before Open.
var wakeFD atomic.Int32

func init() { wakeFD.Store(-1) }

var wakeOne = [8]byte{1}

// osWake makes the eventfd readable; it is safe from any goroutine.
func osWake() {
	if fd := wakeFD.Load(); fd >= 0 {
		syscall.Write(int(fd), wakeOne[:])
	}
}

// Open creates the window. Wayland is tried first, then X11. GNUI_BACKEND=
// wayland|x11 forces one of them.
func Open(o Options) (*Window, error) {
	w := &theWindow
	if w.open {
		return nil, errors.New("platform: window already open")
	}
	if o.Width <= 0 {
		o.Width = defaultWidth
	}
	if o.Height <= 0 {
		o.Height = defaultHeight
	}
	force := os.Getenv("GNUI_BACKEND")
	switch force {
	case "", "wayland", "x11":
	default:
		return nil, fmt.Errorf("platform: GNUI_BACKEND=%q, want wayland or x11", force)
	}

	*w = Window{q: newEventQueue(), efd: -1}
	w.clip.init()
	efd, _, errno := syscall.RawSyscall(syscall.SYS_EVENTFD2, 0, syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if errno != 0 {
		return nil, fmt.Errorf("platform: eventfd: %v", errno)
	}
	w.efd = int(efd)

	var errs []error
	opened := false
	if force == "wayland" || (force == "" && os.Getenv("WAYLAND_DISPLAY") != "") {
		if err := w.openWayland(o); err != nil {
			errs = append(errs, err)
		} else {
			opened = true
		}
	}
	if !opened && (force == "x11" || (force == "" && os.Getenv("DISPLAY") != "")) {
		*w = Window{q: w.q, efd: w.efd}
		w.clip.init()
		if err := w.openX11(o); err != nil {
			errs = append(errs, err)
		} else {
			opened = true
		}
	}
	if !opened {
		syscall.Close(w.efd)
		*w = Window{}
		if len(errs) == 0 {
			return nil, errors.New("platform: no display: neither WAYLAND_DISPLAY nor DISPLAY is set")
		}
		return nil, errors.Join(errs...)
	}

	w.open = true
	w.pfd[1] = pollfd{fd: int32(w.efd), events: pollIn}
	w.repW, w.repH, w.repS = w.pw, w.ph, w.s120
	wakeFD.Store(int32(w.efd))
	if wakePending.Load() { // a Wake raced ahead of Open
		osWake()
	}
	return w, nil
}

// Wait blocks until events, a wake, a frame slot or the timeout (ns; <0
// forever, 0 poll). The returned slice is reused by the next call.
func (w *Window) Wait(timeoutNs int64) []Event {
	w.q.reset()
	w.clipDeliver()
	w.imDeliver()
	if w.closeReq {
		w.closeReq = false
		w.q.push(Event{Kind: EvClose, Time: nanotime()})
	}
	if w.wl {
		w.waitWayland(timeoutNs)
	} else {
		w.waitX11(timeoutNs)
	}
	w.clip.inWait = false
	return w.q.ev
}

// Text returns the UTF-8 bytes of an EvText event from the last Wait.
func (w *Window) Text(r TextRef) []byte { return w.q.textOf(r) }

// RequestFrame arms one EvFrame. Wayland attaches a frame callback to the
// next commit (the gpu swap); X11 reports it on the next Wait.
func (w *Window) RequestFrame() {
	if w.wl {
		w.wlRequestFrame()
	} else {
		w.frameReq = true
	}
}

// Size returns the content size in physical pixels and the scale.
func (w *Window) Size() (int32, int32, float32) {
	w.repW, w.repH, w.repS = w.pw, w.ph, w.s120
	return w.pw, w.ph, float32(w.s120) / 120
}

// Native returns the OS handles for the gpu package.
func (w *Window) Native() Native {
	if w.wl {
		return Native{Wayland: true, Display: w.wy.display, Surface: w.wy.surface}
	}
	return Native{Display: w.x.dpy, X11Window: w.x.win}
}

// Show maps a window opened with Options.Hidden (X11; a Wayland window appears
// with its first buffer, which is content-first by nature, and Show does
// nothing there). If SetFirstFrame gave the first frame, the window is mapped
// with it as its background, so it appears complete. Main thread.
func (w *Window) Show() {
	if !w.wl {
		w.x11Show()
	}
}

// SetFrameHandler is accepted and never called on Linux: neither Wayland nor
// X11 runs an application-blocking modal loop, so resizes arrive as ordinary
// events from Wait (configure / ConfigureNotify), which the app's own loop
// redraws.
func (w *Window) SetFrameHandler(func()) {}

// Pending returns nothing on Linux: events are only queued inside Wait, which
// hands them all out. It exists so frame-handler code is the same on every OS.
func (w *Window) Pending() []Event { return nil }

// SetDragRegions sets the window drag map (see DragRegion). On Wayland without
// server-side decorations a left press in a drag area starts xdg_toplevel.move
// (and is not delivered to the app); with a map set it replaces the fixed top
// strip that serves apps without a titlebar of their own. X11 ignores it for
// now (the window manager's decorations move the window).
func (w *Window) SetDragRegions(r []DragRegion) { w.drags.set(r) }

// SetFrameSource is a no-op on Linux (frame callbacks / swap pacing).
func (w *Window) SetFrameSource(uintptr) {}

// Close destroys the window. Destroy the gpu.Surface first.
func (w *Window) Close() {
	if !w.open {
		return
	}
	wakeFD.Store(-1)
	w.clip.inWait = false
	if w.wl {
		w.closeWayland()
	} else {
		w.closeX11()
	}
	syscall.Close(w.efd)
	w.open = false
}

// pollWait polls the connection fd, the eventfd and the clipboard transfers'
// descriptors. timeoutNs < 0 blocks; a clipboard deadline shortens it. It
// reports how many descriptors are ready (0 on timeout or EINTR).
func (w *Window) pollWait(timeoutNs int64) int {
	w.npfd = 2
	if w.wl {
		w.wlPollFDs()
	}
	if d := w.clipDeadline(); d >= 0 {
		if r := max(d-nanotime(), 0); timeoutNs < 0 || r < timeoutNs {
			timeoutNs = r
		}
	}
	var n uintptr
	var errno syscall.Errno
	if timeoutNs < 0 {
		n, _, errno = syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&w.pfd[0])), uintptr(w.npfd), 0, 0, 0, 0)
	} else {
		w.ts.Sec = timeoutNs / 1e9
		w.ts.Nsec = timeoutNs % 1e9
		n, _, errno = syscall.Syscall6(syscall.SYS_PPOLL, uintptr(unsafe.Pointer(&w.pfd[0])), uintptr(w.npfd), uintptr(unsafe.Pointer(&w.ts)), 0, 0, 0)
	}
	if errno != 0 {
		for i := 0; i < w.npfd; i++ {
			w.pfd[i].revents = 0
		}
		n = 0
	}
	if w.wl {
		w.wlPollDone()
	} else {
		w.x11PollDone()
	}
	return int(n)
}

// drainWake empties the eventfd and reports a pending Wake.
func (w *Window) drainWake() {
	if w.pfd[1].revents&pollIn == 0 {
		return
	}
	syscall.Read(w.efd, w.efdb[:])
	if takeWake() {
		w.q.push(Event{Kind: EvWake, Time: nanotime()})
	}
}

// flushResize reports a size or scale change once per Wait.
func (w *Window) flushResize() {
	if w.pw != w.repW || w.ph != w.repH || w.s120 != w.repS {
		w.repW, w.repH, w.repS = w.pw, w.ph, w.s120
		w.q.push(Event{Kind: EvResize, W: w.pw, H: w.ph, Scale: float32(w.s120) / 120, Time: nanotime()})
	}
}

// physical converts a logical length to pixels at the current scale.
func (w *Window) physical(l int32) int32 { return int32((int64(l)*int64(w.s120) + 60) / 120) }

// ---- C memory and library helpers ----

// ptr turns a C address into an unsafe.Pointer without tripping vet's
// unsafeptr check. C memory is never managed by Go's GC.
func ptr(a uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&a)) }

func peekU32(a uintptr) uint32  { return *(*uint32)(ptr(a)) }
func peekPtr(a uintptr) uintptr { return *(*uintptr)(ptr(a)) }

// cstrIs reports whether the NUL-terminated C string at a equals s.
func cstrIs(a uintptr, s string) bool {
	for i := 0; i < len(s); i++ {
		if *(*byte)(ptr(a + uintptr(i))) != s[i] {
			return false
		}
	}
	return *(*byte)(ptr(a + uintptr(len(s)))) == 0
}

// cstr returns a NUL-terminated copy of s (setup paths only).
func cstr(s string) []byte { return append([]byte(s), 0) }

func dlopen(names ...string) (uintptr, error) {
	var last error
	for _, n := range names {
		h, err := purego.Dlopen(n, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return h, nil
		}
		last = err
	}
	return 0, last
}

type symRef struct {
	name string
	dst  *uintptr
}

func dlsyms(h uintptr, lib string, syms []symRef) error {
	for _, s := range syms {
		p, err := purego.Dlsym(h, s.name)
		if err != nil || p == 0 {
			return fmt.Errorf("%s: missing symbol %s", lib, s.name)
		}
		*s.dst = p
	}
	return nil
}
