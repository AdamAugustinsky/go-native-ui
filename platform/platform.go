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
//	(*Window).SetMenuBar(spec, state)     the application's native menu bar (menu.go; macOS; no-op elsewhere)
//	(*Window).PopUpMenu(*Popup) int       a native context, pull-down or list menu; returns the chosen item (macOS; -1 elsewhere)
//	HasNativeMenus() bool                 whether the OS has native menus (macOS)
//	(*Window).Alert(AlertSpec), OpenPanel(OpenSpec), SavePanel(SaveSpec)
//	                                      native alert and file panel sheets (macOS; ErrUnsupported elsewhere); the answer is an AppEvService from SourceDialog, see dialog.go
//	(*Window).SetTooltipRegions(r)        native tooltips by content rectangle (macOS; no-op elsewhere); diffed, cheap to call every frame
//	(*Window).SetTooltipText(f)           where the tooltip text comes from, asked for on demand when the system shows one
//	(*Window).SetDropKinds(k), SetDragHandler(h), StartDrag(spec)
//	                                      drag and drop in and out (macOS; ErrDragUnsupported elsewhere), see drag.go
//	DragThreshold() float32               how far a press moves before it is a drag, in logical units
//	(*Window).Close()
//	SetSecureInput(on bool)               macOS: bracket EnableSecureEventInput for a password field (balanced, released while the app is inactive); no-op elsewhere
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
// Key bindings. While a text field has focus (SetTextInput), the keys the
// system binds to text editing arrive as EvEdit with the EditCmd instead of
// as EvKey (macOS: the doCommandBySelector: selectors, which follow the user's
// DefaultKeyBinding.dict; see edit.go). Without a focused field every key is an
// EvKey, as ever. Key is the physical key; Char is what it types on the user's
// layout, which shortcuts match by. EvModifiers reports a modifier pressed or
// released on its own.
//
// Gestures. A trackpad's pinch (EvMagnify), two-finger rotation (EvRotate) and
// two-finger double tap (EvSmartMagnify) arrive where the OS has them: macOS
// from the trackpad (with phases), Wayland from the pointer-gestures protocol
// (pinch and rotate, with phases), Windows from a precision touchpad's pinch
// (without phases: PhaseNone). X11 has none. The app's ui latches a gesture
// to the box under its first event, as it does a scroll gesture.
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
	EvScroll                  // DX, DY in physical pixels; FlagPrecise for trackpads; Phase, Momentum; FlagInverted; X, Y with FlagPos
	EvKey                     // Key, Code, Mods, FlagDown, FlagRepeat
	EvText                    // Text: committed UTF-8 text input
	EvPreedit                 // Text: input-method composition (marked text), empty when it ended; SelStart, SelLen: caret/selection inside it
	EvWake                    // a goroutine called Wake
	EvFrame                   // the frame slot armed by RequestFrame is available
	EvPaste                   // the answer to RequestPaste or RequestPasteKinds: Paste (the kind), Text (plain text, PNG bytes or NUL-separated paths), Tag; FlagFailed when there is none (FlagTooBig too when it was over MaxPaste)
	EvCommand                 // a native button or menu item was chosen: Tag is its command id (NativeButton.ID, HostOptions.Command, MenuItem.ID); with FlagStandard, a MenuAction
	EvHostFocus               // the keyboard focus moved to a host (Tag: its HostID) or back to the app's own view (Tag 0)
	EvHostCommand             // a focused host handed back Tab, Shift+Tab, Escape or Enter: Tag is the HostID, Key and Mods the key
	EvModifiers               // the modifier keys changed (a modifier pressed or released on its own): Mods are the new state, Code the raw code of the key that moved when the OS says
	EvEdit                    // the system's key bindings turned a key into a text edit command while a text field has focus (SetTextInput): Tag is the EditCmd; the key's own EvKey is not sent (macOS: doCommandBySelector:)
	EvMagnify                 // a pinch: Amount is the change in scale (0.1 = grow by 10 %), Phase its phase (PhaseNone: a device without phases), X, Y where it is; see Gestures
	EvSmartMagnify            // the two-finger double tap (smart zoom): X, Y where it happened
	EvRotate                  // a two-finger rotation: Amount in degrees, counterclockwise positive as the user sees it, Phase, X, Y
	EvDragEnter               // a drag came over the window (drag.go): X, Y, Code the kinds offered, SelStart the operations allowed, SelLen the items, Tag the drag's number
	EvDragMove                // the pointer moved in a drag over the window: as EvDragEnter
	EvDragLeave               // the drag left the window or was cancelled: Tag
	EvDrop                    // the drag was dropped: Code the kind delivered, Text its data, X, Y, Tag; FlagFailed when it could not be read
	EvDragEnd                 // a drag out ended: Tag is its DragTicket, Code the operation performed (DragOpNone: cancelled)
)

// Flags qualify an Event.
type Flags uint16

const (
	FlagDown     Flags = 1 << iota // key/button pressed, focus gained, became visible
	FlagRepeat                     // key auto-repeat
	FlagPrecise                    // scroll deltas are precise pixels (trackpad)
	FlagFailed                     // EvPaste: nothing of the requested kinds (empty, another kind, over MaxPaste, or the transfer failed)
	FlagStandard                   // EvCommand: Tag is the MenuAction of a standard edit item without a command (menu.go)
	FlagInverted                   // EvScroll: the device's direction is inverted ("natural" scrolling); DX, DY already move the content
	FlagPos                        // EvScroll: X, Y are where the pointer was at the event (a scroll goes to the box under it, not under a later move)
	FlagSelect                     // EvEdit: the command extends the selection instead of moving the caret (macOS: the ...AndModifySelection: selectors)
	FlagTooBig                     // EvPaste, with FlagFailed: the clipboard holds a requested kind, but over MaxPaste (an image's PNG, a file list, text); nothing of it was copied
)

// ScrollPhase is the phase of a trackpad scroll gesture (Event.Phase) or of
// the momentum after it (Event.Momentum), as AppKit reports them.
//
// What each system gives: macOS has both phases on trackpads and the Magic
// Mouse (and precise deltas), none on wheels. Wayland gives a finger gesture's
// start and end (wl_pointer axis_source finger, axis_stop): Began, Changed and
// Ended, no momentum (kinetic scrolling is the client's there, and ui does
// none). Windows gives precise touchpad deltas (WM_MOUSEWHEEL deltas that are
// not whole notches, FlagPrecise) without phases; a high-resolution wheel,
// which sends fractions of a notch too, is told apart from a touchpad by
// nothing and is treated as precise as well. X11 gives only wheel buttons.
type ScrollPhase uint8

const (
	// PhaseNone: not part of a gesture.
	PhaseNone ScrollPhase = iota
	// PhaseMayBegin: fingers came to rest on the trackpad.
	PhaseMayBegin
	// PhaseBegan: the gesture (or the momentum) started.
	PhaseBegan
	// PhaseChanged: the gesture (or the momentum) moved.
	PhaseChanged
	// PhaseEnded: the fingers lifted (or the momentum ran out).
	PhaseEnded
	// PhaseCancelled: the gesture was cancelled.
	PhaseCancelled
	// PhaseStationary: the fingers are down and still.
	PhaseStationary
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

	// More punctuation keys, by physical (US) position.
	KeyComma     = Key9 + 4  // ,
	KeyEqual     = Key9 + 5  // =
	KeyMinus     = Key9 + 6  // -
	KeySlash     = Key9 + 7  // /
	KeySemicolon = Key9 + 8  // ;
	KeyQuote     = Key9 + 9  // '
	KeyBackslash = Key9 + 10 // \
	KeyGrave     = Key9 + 11 // `

	// The numeric keypad: KeyNum0..KeyNum9 are contiguous. KeypadEnter is KeyEnter (Code tells them apart).
	KeyNum0        = Key9 + 12
	KeyNum9        = KeyNum0 + 9
	KeyNumDecimal  = KeyNum9 + 1
	KeyNumAdd      = KeyNum9 + 2
	KeyNumSubtract = KeyNum9 + 3
	KeyNumMultiply = KeyNum9 + 4
	KeyNumDivide   = KeyNum9 + 5
	KeyNumEqual    = KeyNum9 + 6
	KeyNumClear    = KeyNum9 + 7 // Clear (NumLock on a PC)

	// KeyF13..KeyF20 are contiguous (F1..F12 are above).
	KeyF13 = KeyNum9 + 8
	KeyF20 = KeyF13 + 7
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
	Key Key
	// Char is, for an EvKey, the character the key names in a shortcut on
	// the current layout: printable ASCII, lower-cased, and never the shifted
	// one (Shift is a modifier of the shortcut of its own: ⌘⇧ and the key that
	// types [ is Char '[', not '{'). On macOS it is what the layout types with
	// ⌘ held, as AppKit matches key equivalents (so "Dvorak – QWERTY ⌘" gives
	// QWERTY's letters); 0 when that is not ASCII (a layout of another script
	// without a Latin ⌘ table: the shortcut is then the physical key), when
	// the key types none, or when the OS does not say. Key is the physical
	// key; Char is what shortcuts match by (the app's translator,
	// ui.KeyEvent.Code), so that a shortcut sits where the user's layout puts
	// its letter.
	Char   rune
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
	Tag              uint64 // EvPaste: the tag given to RequestPaste or RequestPasteKinds
	// Phase and Momentum are an EvScroll's gesture phase and momentum phase
	// (at most one is not PhaseNone); see ScrollPhase for what each system
	// reports. Without FlagPrecise, DX and DY are wheel lines converted to
	// pixels (on macOS 10 points a line, NSScrollView's line scroll).
	Phase, Momentum ScrollPhase
	// Clicks is the system's click count of an EvPointerButton press (1,
	// 2 for a double click, ...), as the OS counts it with the user's
	// double-click interval; 0 when the OS does not say (the app counts).
	Clicks uint8
	// Amount is the size of an EvMagnify (the change in scale: 0.1 grows by
	// 10 %, negative shrinks) or EvRotate (degrees, counterclockwise positive
	// as the user sees it). The Phase of both is Began, Changed, Ended or
	// Cancelled, or PhaseNone from a device without phases.
	Amount float32
	// Paste is which kind of data an EvPaste carries in Text (PasteText,
	// the zero value, for every other event too). See RequestPasteKinds.
	Paste PasteKind
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
	// top-left), keeping the native button spacing. macOS only so far, and
	// where AppKit has the titlebar metrics for it (platform/lights_darwin.go);
	// elsewhere the buttons stay at AppKit's default place, which
	// Window.SystemButtons reports.
	Titlebar      float32
	TrafficLights [2]float32
	// PublicAPIOnly keeps the window from using any private system
	// interface, as a build with the gnui_publicapi tag does for the whole
	// binary (a Mac App Store build, guideline 2.5.1, uses the tag, which
	// also leaves the private names out of the binary; the option tests the
	// public path without rebuilding). On macOS the window buttons of a
	// custom titlebar then stay at AppKit's default place, not at
	// TrafficLights; Window.SystemButtons reports where, so the app keeps its
	// content clear of them (DECISIONS.md D53).
	PublicAPIOnly bool

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
	// Materials makes the GPU surface transparent (Native.Transparent) so
	// that the material regions (SetMaterials) show through wherever the
	// app paints nothing; the window itself stays opaque, with the system's
	// window background behind the app's pixels, as an AppKit window's
	// content is. macOS; ignored elsewhere (Window.HasMaterials).
	Materials bool
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
