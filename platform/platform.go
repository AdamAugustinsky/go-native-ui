// Package platform owns the OS window, the event loop and cross-thread
// wakeups. The process main thread runs the loop: this package locks the
// main goroutine to it in init.
//
// The API is the same on every OS (backends live in *_darwin.go,
// *_windows.go, *_linux.go):
//
//	Open(Options) (*Window, error)        main thread only
//	(*Window).Wait(timeoutNs) []Event     block until events, a wake, a frame slot or the timeout
//	(*Window).Text(TextRef) []byte        UTF-8 bytes of an EvText event
//	(*Window).RequestFrame()              arm one EvFrame at the next vsync opportunity
//	(*Window).Size() (w, h int32, scale)  content size in physical pixels, pixels per logical unit
//	(*Window).Native() Native             OS handles for the gpu package
//	(*Window).SetFrameSource(h)           the GPU surface's frame source: Windows DXGI frame-latency waitable, macOS CAMetalLayer (CAMetalDisplayLink); no-op on Linux.
//	                                      With a frame source set, draw on EvFrame: on macOS, after the first frame,
//	                                      Present outside a frame tick returns false (no drawable is available)
//	(*Window).SetFrameHandler(fn)         fn runs synchronously inside OS modal loops (macOS live resize, the Windows size/move
//	                                      loop) when a frame is due; Wait does not return during them. Never called on Linux.
//	(*Window).Pending() []Event           the events queued so far, without pumping the OS (for the frame handler)
//	(*Window).SetCursor(Cursor)           pointer shape over the content; cheap to call every frame
//	(*Window).Close()
//	Wake()                                any goroutine; coalesced
//	Now() int64                           monotonic ns, same timebase as Event.Time
//
// Frame handler: while the user drags a window edge, macOS and Windows run
// their own loop inside Wait and the app's loop does not run. An app that
// wants its content redrawn in step with the window registers a handler;
// the platform calls it from inside that loop, on the main thread, whenever
// a frame is due (a size change, or a requested frame while the loop is
// running). The handler is expected to take Pending, resize its surface on
// EvResize, build and draw and present exactly as the main loop would. It
// is never re-entered, and it must not call Wait.
//
// Steady-state use allocates nothing: events and text live in buffers owned
// by the Window and reused by every Wait call.
//
// Frame pacing contract: render, call RequestFrame, then present. The
// platform delivers EvFrame once the next frame may be started (display
// link on macOS, frame-latency waitable on Windows, wl_surface.frame done on
// Wayland, immediately on X11 where the swap itself blocks). RequestFrame
// must come before present because on Wayland it attaches a frame callback
// to the commit that present performs.
package platform

import (
	"runtime"
	"sync/atomic"
	_ "unsafe" // go:linkname
)

func init() { runtime.LockOSThread() }

// EventKind identifies an Event.
type EventKind uint8

const (
	EvNone          EventKind = iota
	EvClose                   // the user asked to close the window
	EvResize                  // W, H (physical pixels) and Scale changed
	EvFocus                   // FlagDown: window gained keyboard focus
	EvVisible                 // FlagDown: window became visible (occlusion/map state)
	EvPointerMove             // X, Y
	EvPointerButton           // Button, FlagDown, X, Y
	EvPointerLeave            // pointer left the content area
	EvScroll                  // DX, DY in physical pixels; FlagPrecise for trackpads
	EvKey                     // Key, Code, Mods, FlagDown, FlagRepeat
	EvText                    // Text: committed UTF-8 text input
	EvWake                    // a goroutine called Wake
	EvFrame                   // the frame slot armed by RequestFrame is available
)

// Flags qualify an Event.
type Flags uint16

const (
	FlagDown    Flags = 1 << iota // key/button pressed, focus gained, became visible
	FlagRepeat                    // key auto-repeat
	FlagPrecise                   // scroll deltas are precise pixels (trackpad)
)

// Mods are the modifier keys held during an event.
type Mods uint16

const (
	ModShift Mods = 1 << iota
	ModCtrl
	ModAlt
	ModSuper // Command on macOS, Windows key elsewhere
)

// Button is a pointer button.
type Button uint8

const (
	ButtonNone Button = iota
	ButtonLeft
	ButtonRight
	ButtonMiddle
	ButtonBack
	ButtonForward
)

// Key is a layout-independent key identity (by physical position for
// letters and digits). Code carries the raw OS key code as well.
type Key uint16

const (
	KeyUnknown Key = iota
	KeyEscape
	KeyEnter
	KeyTab
	KeyBackspace
	KeyDelete
	KeySpace
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyA // KeyA..KeyZ are contiguous
	KeyZ = KeyA + 25
	Key0 = KeyZ + 1 // Key0..Key9 are contiguous
	Key9 = Key0 + 9
)

// Cursor is a pointer shape.
type Cursor uint8

const (
	CursorArrow    Cursor = iota // the default arrow
	CursorPointer                // pointing hand (links, tabs, buttons)
	CursorText                   // I-beam
	CursorResizeEW               // horizontal resize (left-right arrows)
	CursorResizeNS               // vertical resize (up-down arrows)
	numCursors
)

// TextRef locates UTF-8 text in the Window's per-Wait text buffer.
type TextRef struct{ Off, Len uint32 }

// Event is one input or window event. It is pointer-free.
type Event struct {
	Kind   EventKind
	Button Button
	Flags  Flags
	Mods   Mods
	Key    Key
	Code   uint32  // raw OS key code (macOS keyCode, Windows scancode, evdev/X11 keycode)
	X, Y   float32 // pointer position, physical pixels, origin at the content's top-left
	DX, DY float32 // scroll delta, physical pixels, positive = content moves right/down
	W, H   int32   // EvResize: content size in physical pixels
	Scale  float32 // EvResize: physical pixels per logical unit
	Text   TextRef // EvText
	Time   int64   // OS event time in Now() nanoseconds; 0 if unknown
}

// Options configure Open.
type Options struct {
	Title         string
	Width, Height int // logical units (points / DIPs)
	// Hidden opens the window without showing it; call Show, or hand Show to
	// the GPU surface (macOS: Surface.RevealOnFirstFrame) so the window
	// appears together with its first frame. Content-first startup is
	// implemented on macOS only so far; elsewhere Hidden is ignored.
	Hidden bool
	// Titlebar > 0 extends the content under a transparent titlebar band of
	// that height in logical units; the app draws the band. The close
	// button's centre goes to TrafficLights (logical units from the window's
	// top-left), keeping the native button spacing. macOS only so far.
	Titlebar      float32
	TrafficLights [2]float32
}

// eventQueue is the reusable per-Window event and text storage.
type eventQueue struct {
	ev   []Event
	text []byte
	pend []Event // what takePending hands out (a copy: later pushes reuse ev)
}

func newEventQueue() eventQueue {
	return eventQueue{ev: make([]Event, 0, 256), text: make([]byte, 0, 4096), pend: make([]Event, 0, 256)}
}

// takePending moves the queued events into the pending slice and clears the
// queue. The text buffer stays: EvText events of the returned slice still
// index it, and it is only recycled once the queue is empty at the next call
// (the previous slice is dead by then) or by reset.
func (q *eventQueue) takePending() []Event {
	if len(q.ev) == 0 {
		q.text = q.text[:0]
		q.pend = q.pend[:0]
		return q.pend
	}
	q.pend = append(q.pend[:0], q.ev...)
	q.ev = q.ev[:0]
	return q.pend
}

func (q *eventQueue) reset() { q.ev, q.text = q.ev[:0], q.text[:0] }

// dropFirst discards the first n events, which were already handed out.
// Events pushed since (a synchronous AppKit callback between two Waits) stay,
// and so does the text buffer they may index.
func (q *eventQueue) dropFirst(n int) {
	if n >= len(q.ev) {
		q.reset()
		return
	}
	q.ev = q.ev[:copy(q.ev, q.ev[n:])]
}

func (q *eventQueue) push(e Event) { q.ev = append(q.ev, e) }

func (q *eventQueue) pushText(b []byte, t int64) {
	off := len(q.text)
	q.text = append(q.text, b...)
	q.push(Event{Kind: EvText, Text: TextRef{uint32(off), uint32(len(b))}, Time: t})
}

func (q *eventQueue) textOf(r TextRef) []byte { return q.text[r.Off : r.Off+r.Len] }

// wakePending coalesces Wake calls: at most one OS wake is in flight. The
// loop clears it before reporting EvWake, so a Wake that races with the
// drain posts again.
var wakePending atomic.Bool

// Wake makes the main loop's Wait return with an EvWake event. It is safe
// to call from any goroutine and cheap when a wake is already pending.
func Wake() {
	if wakePending.CompareAndSwap(false, true) {
		osWake()
	}
}

// takeWake reports whether a wake was pending and clears it.
func takeWake() bool { return wakePending.Swap(false) }

// Now returns monotonic nanoseconds in the same timebase as Event.Time.
func Now() int64 { return nanotime() }

// Stage is one named timestamp (Now) of Open's startup timeline.
type Stage struct {
	Name string
	T    int64
}

var (
	openStages  [16]Stage
	nOpenStages int
)

func markOpen(name string) {
	if nOpenStages < len(openStages) {
		openStages[nOpenStages] = Stage{name, Now()}
		nOpenStages++
	}
}

// OpenTimeline returns the stages the last Open recorded, in order (backends
// that don't record stages return none).
func OpenTimeline() []Stage { return openStages[:nOpenStages] }
