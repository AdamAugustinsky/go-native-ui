// Package platform owns the OS window, the event loop and cross-thread
// wakeups. The process main thread runs the loop: this package locks the
// main goroutine to it in init.
//
// The API is the same on every OS (backends live in *_darwin.go,
// *_windows.go, *_linux.go):
//
//	Open(Options) (*Window, error)        main thread only
//	(*Window).Wait(timeoutNs) []Event     block until events, a wake, a frame slot or the timeout
//	(*Window).Text(TextRef) []byte        UTF-8 bytes of an EvText or EvPreedit event
//	(*Window).RequestFrame()              arm one EvFrame at the next vsync opportunity
//	(*Window).Size() (w, h int32, scale)  content size in physical pixels, pixels per logical unit
//	(*Window).Native() Native             OS handles for the gpu package
//	(*Window).SetFrameSource(h)           the GPU surface's frame source: Windows DXGI frame-latency waitable, macOS CAMetalLayer (CAMetalDisplayLink); no-op on Linux.
//	                                      With a frame source set, draw on EvFrame: on macOS, after the first frame,
//	                                      Present outside a frame tick returns false (no drawable is available)
//	(*Window).SetFrameHandler(fn)         fn runs synchronously inside OS modal loops (macOS live resize, the Windows size/move
//	                                      loop) when a frame is due; Wait does not return during them. Never called on Linux.
//	(*Window).Pending() []Event           the events queued so far, without pumping the OS (for the frame handler)
//	(*Window).SetCursorRegions(r)         pointer shapes by content rectangle (later entries on top); copied, cheap to call every frame
//	(*Window).SetCursorOverride(Cursor)   one shape over the whole content while not CursorArrow (a drag in progress)
//	(*Window).SetDragRegions(r)           custom-titlebar areas that move the window (macOS; no-op elsewhere)
//	(*Window).SetIMERect(x, y, w, h)      where the text caret or composition is (window pixels), for input-method candidate windows; copied, cheap to call every frame
//	(*Window).SetTextInput(active)        whether a text field has focus; losing it cancels a composition in progress
//	(*Window).DiscardComposition()        cancel a composition in progress without committing it (EvPreedit with no text follows)
//	(*Window).Backdrop() Backdrop         the system material behind the content (Options.Backdrop), BackdropNone without; experimental
//	(*Window).HasButtons() bool           whether native buttons are over the content (Options.Buttons); experimental
//	(*Window).SetButtonEnabled(id, on)    enable or disable one; cheap when unchanged (macOS; no-op elsewhere); experimental
//	(*Window).Alert(AlertSpec), OpenPanel(OpenSpec), SavePanel(SaveSpec)
//	                                      native alert and file panel sheets (macOS; ErrUnsupported elsewhere); the answer is an EvDialog, see dialog.go
//	(*Window).SetTooltipRegions(r)        native tooltips by content rectangle (macOS; no-op elsewhere); diffed, cheap to call every frame
//	(*Window).SetTooltipText(f)           where the tooltip text comes from, asked for on demand when the system shows one
//	(*Window).Close()
//	Clipboard() string                    plain UTF-8 text of the system clipboard; main thread
//	SetClipboard(text string)             replace the clipboard with plain text; main thread
//	Wake()                                any goroutine; coalesced
//	Now() int64                           monotonic ns, same timebase as Event.Time
//
// Text input. Typed characters arrive as EvText together with the EvKey of
// the key that produced them. An input method (macOS, Windows) composes text
// first: while it does, EvPreedit reports the not yet committed text and the
// caret inside it (an empty one ends the composition), and the keys the input
// method consumed produce no EvKey. The commit arrives as EvPreedit (empty)
// followed by EvText. SetTextInput(false) and DiscardComposition cancel a
// composition in progress without committing it (the app calls them when its
// text field loses focus or the composition is orphaned; EvPreedit with no
// text follows, no EvText). SetIMERect tells the input method where to put its candidate
// window. An input method that is active composes whether or not the app has a
// field to show it in: an app without one focused sees EvPreedit and no EvKey
// for the keys the input method took, and should focus a field then.
//
// Clipboard text is plain UTF-8 with \n line ends. macOS and Windows use the
// system clipboard. Linux keeps it in the process (clipboard_linux.go): copy
// and paste work within the application only, until the Wayland data-device
// and X11 selection protocols are implemented.
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
	"unicode/utf8"
	_ "unsafe" // go:linkname
)

func init() { runtime.LockOSThread() }

// EventKind identifies an Event.
type EventKind uint8

const (
	// EvNone is no event (the zero value).
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
	EvPreedit                 // Text: input-method composition (marked text), empty when it ended; SelStart, SelLen: caret/selection inside it
	EvWake                    // a goroutine called Wake
	EvFrame                   // the frame slot armed by RequestFrame is available
	EvPaste                   // the answer to RequestPaste: Text (plain text), Tag; FlagFailed when there is none
	EvCommand                 // a native button was clicked: Tag is its command id (NativeButton.ID); experimental
	EvDialog                  // an alert or file panel ended: Tag is its DialogTicket; decode with Window.DialogResult (dialog.go)
)

// Flags qualify an Event.
type Flags uint16

const (
	FlagDown    Flags = 1 << iota // key/button pressed, focus gained, became visible
	FlagRepeat                    // key auto-repeat
	FlagPrecise                   // scroll deltas are precise pixels (trackpad)
	FlagFailed                    // EvPaste: no text (empty, not text, over MaxPaste, or the transfer failed)
)

// Mods are the modifier keys held during an event.
type Mods uint16

const (
	// ModShift, ModCtrl and ModAlt are the Shift, Control and Alt (Option on macOS) keys.
	ModShift Mods = 1 << iota
	ModCtrl
	ModAlt
	ModSuper // Command on macOS, Windows key elsewhere
)

// Button is a pointer button.
type Button uint8

const (
	// ButtonNone is no button (the zero value); ButtonLeft, ButtonRight and ButtonMiddle are the
	// main buttons and ButtonBack and ButtonForward the side buttons.
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
	// KeyUnknown is a key without a name here (Event.Code still identifies it). The keys after it, up to
	// KeyF12, are the named non-character keys; KeyF1..KeyF12 are contiguous.
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

	// Punctuation keys, by physical (US) position like the letters.
	KeyLeftBracket  = Key9 + 1 // [
	KeyRightBracket = Key9 + 2 // ]
	KeyPeriod       = Key9 + 3 // .
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
type TextRef struct {
	// Off is the byte offset into the buffer and Len the length in bytes.
	Off, Len uint32
}

// Event is one input or window event. It is pointer-free.
type Event struct {
	// Kind is the kind of event; it says which of the other fields are set.
	Kind EventKind
	// Button is the button of an EvPointerButton.
	Button Button
	// Flags qualify the event (FlagDown, FlagRepeat, FlagPrecise, FlagFailed).
	Flags Flags
	// Mods are the modifier keys held at the event.
	Mods Mods
	// Key is the key of an EvKey.
	Key    Key
	Code   uint32  // raw OS key code (macOS keyCode, Windows scancode, evdev/X11 keycode)
	X, Y   float32 // pointer position, physical pixels, origin at the content's top-left
	DX, DY float32 // scroll delta, physical pixels, positive = content moves right/down
	W, H   int32   // EvResize: content size in physical pixels
	Scale  float32 // EvResize: physical pixels per logical unit
	Text   TextRef // EvText, EvPreedit
	// EvPreedit: the caret (SelLen 0) or the part being converted inside the
	// composition, as a byte offset and length into the UTF-8 text.
	//
	// EvText (macOS): when not both 0, Text replaces the SelLen UTF-16 units
	// that start SelStart units from the caret (negative: before it), and the
	// caret goes after Text. Press-and-hold accents send {-1, 1}: "replace
	// the character before the caret". Text may be empty (a pure removal).
	// UTF-16 because AppKit counts in it and the platform does not hold the
	// text; the text field converts with its own buffer.
	SelStart, SelLen int32
	Time             int64  // OS event time in Now() nanoseconds; 0 if unknown
	Tag              uint64 // EvPaste: the tag given to RequestPaste
}

// Options configure Open.
type Options struct {
	// Title is the window title.
	Title         string
	Width, Height int // logical units (points / DIPs)
	// MinWidth, MinHeight bound the content size the user can resize the
	// window to (logical units); 0 means no minimum.
	MinWidth, MinHeight int
	// Hidden opens the window without showing it; call Show, or hand Show to
	// the GPU surface (Surface.RevealOnFirstFrame) so the window appears
	// together with its first frame. macOS keeps the window ordered out;
	// Windows shows it cloaked (DWMWA_CLOAK) and uncloaks it after the first
	// present. X11 maps the window with the first frame as its background
	// (startup_x11_linux.go); a Wayland window appears with its first buffer
	// anyway.
	Hidden bool
	// Titlebar > 0 extends the content under a transparent titlebar band of
	// that height in logical units; the app draws the band. The close
	// button's centre goes to TrafficLights (logical units from the window's
	// top-left), keeping the native button spacing. macOS only so far.
	Titlebar      float32
	TrafficLights [2]float32

	// Backdrop puts a system material behind the content, which shows
	// wherever the app paints nothing: the GPU surface is transparent
	// (Native.Transparent) and the app clears to alpha 0. Window.Backdrop
	// says which one the window got; it is BackdropNone where the OS has
	// none (Windows and Linux so far), and the app then draws its own
	// background. Experimental (the phase 6 native-shell spike): D41's
	// material regions are meant to replace it.
	Backdrop Backdrop
	// Buttons are native buttons over the content, above the GPU surface
	// (macOS; ignored elsewhere, see Window.HasButtons): AppKit draws them,
	// tracks their hover and press and gives them their clicks, which arrive
	// as EvCommand with the button's ID. The app keeps its own content out of
	// their rectangles. Experimental: D41's hosted native views are meant to
	// replace them.
	Buttons []NativeButton
}

// Backdrop is a system material behind a window's content (Options.Backdrop).
// Experimental.
type Backdrop uint8

const (
	// BackdropNone is no material: the content is opaque (the default).
	BackdropNone Backdrop = iota
	// BackdropWindow is the window-background material blurred from behind
	// the window (macOS: NSVisualEffectView, under-window background,
	// behind-window blending).
	BackdropWindow
	// BackdropGlass is Liquid Glass (macOS 26 and later: NSGlassEffectView,
	// regular style); BackdropWindow on earlier systems.
	BackdropGlass
	// BackdropGlassClear is the clear Liquid Glass style (more of the desktop
	// shows); BackdropWindow before macOS 26.
	BackdropGlassClear
)

// NativeButton is a native symbol button over the window's content
// (Options.Buttons). Experimental.
type NativeButton struct {
	// ID is the command id a click delivers (EvCommand's Tag) and names the
	// button for SetButtonEnabled.
	ID uint16
	// Symbol is the SF Symbol shown ("plus", "command"); Label the
	// accessibility description and tooltip.
	Symbol, Label string
	// X, Y are the button's centre in logical units from the content's top
	// left; a negative X counts from the right edge, and the button keeps
	// that distance when the window is resized. Size is its width and height
	// (0: 28).
	X, Y, Size float32
	// Style is the look.
	Style ButtonStyle
	// Disabled opens the button disabled.
	Disabled bool
}

// ButtonStyle is the look of a NativeButton. Experimental.
type ButtonStyle uint8

const (
	// ButtonPlain is a borderless symbol that AppKit highlights on hover and
	// press (a titlebar accessory button).
	ButtonPlain ButtonStyle = iota
	// ButtonGlass is a Liquid Glass circle (macOS 26 and later; ButtonPlain
	// before).
	ButtonGlass
)

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

// pushPaste queues an EvPaste with a copy of b (nil b: FlagFailed). The text
// buffer keeps the capacity of the largest paste.
func (q *eventQueue) pushPaste(b []byte, tag uint64, t int64) {
	if b == nil || len(b) > MaxPaste {
		q.push(Event{Kind: EvPaste, Flags: FlagFailed, Tag: tag, Time: t})
		return
	}
	off := len(q.text)
	q.text = append(q.text, b...)
	q.push(Event{Kind: EvPaste, Text: TextRef{uint32(off), uint32(len(b))}, Tag: tag, Time: t})
}

func (q *eventQueue) pushText(b []byte, t int64) {
	off := len(q.text)
	q.text = append(q.text, b...)
	q.push(Event{Kind: EvText, Text: TextRef{uint32(off), uint32(len(b))}, Time: t})
}

// u16ToByte converts an offset in UTF-16 units of b (decoded as UTF-8) to a
// byte offset, clamped to the text.
func u16ToByte(b []byte, units uintptr) int {
	i := 0
	for u := uintptr(0); u < units && i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		i += n
		u++
		if r >= 0x10000 {
			u++
		}
	}
	return i
}

// utf16Len is the length of b (UTF-8) in UTF-16 units.
func utf16Len(b []byte) uintptr {
	var u uintptr
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		i += n
		u++
		if r >= 0x10000 {
			u++
		}
	}
	return u
}

// pushPreedit queues the input-method composition b (empty ends it) with the
// caret or selection inside it.
func (q *eventQueue) pushPreedit(b []byte, selStart, selLen int32, t int64) {
	off := len(q.text)
	q.text = append(q.text, b...)
	q.push(Event{Kind: EvPreedit, Text: TextRef{uint32(off), uint32(len(b))}, SelStart: selStart, SelLen: selLen, Time: t})
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
	// Name names the stage.
	Name string
	// T is the stage's time in Now() nanoseconds.
	T int64
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
