package ui

import (
	"unicode/utf8"

	"gonativeui/platform"
)

// Mouse buttons (indices into the Input arrays).
const (
	ButtonLeft = iota
	ButtonRight
	ButtonMiddle
	numButtons
)

// Mods are modifier keys.
type Mods uint8

const (
	// ModShift, ModCtrl and ModAlt are the Shift, Control and Alt (Option on macOS) keys.
	ModShift Mods = 1 << iota
	ModCtrl
	ModAlt
	ModSuper // Cmd on macOS, Windows key elsewhere
)

// PrimaryMod is the modifier of the shortcut chords: Super (⌘) on macOS, Ctrl
// elsewhere.
func PrimaryMod() Mods {
	if macKeys {
		return ModSuper
	}
	return ModCtrl
}

// KeyCode is a platform-independent key identifier. The app maps platform
// key codes to these (letters and digits use their ASCII upper-case value).
type KeyCode uint16

const (
	// KeyEnter and the codes after it are the named non-character keys; letters and digits
	// are not listed here, they use their ASCII upper-case value.
	KeyEnter KeyCode = 0x100 + iota
	KeySpace
	KeyEscape
	KeyTab
	KeyBackspace
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
)

// Punctuation keys, by physical (US) position; their codes are their ASCII values.
const (
	KeyLeftBracket  KeyCode = '['
	KeyRightBracket KeyCode = ']'
	KeyPeriod       KeyCode = '.'
)

// KeyEvent is one key press (Down) or release this frame.
type KeyEvent struct {
	// Code is the key.
	Code KeyCode
	// Mods are the modifiers held at the event.
	Mods Mods
	// Down is true for a press (or auto-repeat), false for a release.
	Down bool
	// Repeat is true for the auto-repeat of a held key.
	Repeat bool
}

// EditAction is an edit command for the focused text field that does not come
// as a key chord: the Edit menu of a native menu bar pushes them
// (Input.PushEdit), and a menu command with an edit role becomes one (package
// menu). The field applies them exactly as it applies the matching chord. The
// zero value is no action.
type EditAction uint8

const (
	EditCopy      EditAction = iota + 1 // copy the selection
	EditCut                             // copy the selection, then delete it
	EditPaste                           // ask for the clipboard text (Context.PasteRequest)
	EditUndo                            // undo the last typing run or edit
	EditRedo                            // redo what was undone
	EditSelectAll                       // select the whole text
	EditDelete                          // delete the selection
)

// SemRequest is an assistive technology's request for an action on a box,
// delivered through Input.Actions: Key names the box, Action which action
// (one bit of SemAction), Value its argument (ActSetValue: the new value of a
// range; the text of a text field is not carried).
type SemRequest struct {
	// Key is the key of the target box.
	Key Key
	// Action is the requested action: one bit of SemAction.
	Action SemAction
	// Value is the argument of ActSetValue.
	Value float64
}

const (
	// MaxKeys is the number of key events per frame.
	MaxKeys = 8
	// MaxEdit is the number of edit actions per frame.
	MaxEdit = 4
	// MaxCommands is the number of menu commands per frame.
	MaxCommands = 8
	// MaxActions is the number of accessibility action requests per frame.
	MaxActions = 4
	// MaxText is the number of bytes of typed text per frame.
	MaxText = 64
	// MaxPreedit is the number of bytes of input-method composition text.
	MaxPreedit = 256
)

// Input is one frame of input, filled by the app from platform events. It is
// a plain value: the app keeps one, clears the per-frame parts with
// ClearFrame after each Begin, and fills it from events before the next.
type Input struct {
	// MouseX, MouseY: pointer position in pixels, origin top left.
	MouseX, MouseY float32
	// Inside is true while the pointer is over the window.
	Inside bool
	// Down is the state of the buttons. Pressed and Released are the edges
	// since the previous frame (both can be set when a click is quicker than
	// a frame).
	//
	// Within one frame a button should have at most one transition, taken at
	// the pointer position in the same Input; the app's translator ends a
	// frame's batch of events where more would change the meaning (press on A,
	// then release over B). If both edges are set with Down true, ui treats it
	// as a release followed by a new press; with Down false, as a click.
	Down     [numButtons]bool
	Pressed  [numButtons]bool
	Released [numButtons]bool
	// Cancelled is the edge of a press that ended without a release: the
	// window lost focus while the button was held. Down is false; ui drops the
	// capture of the box without Released, Clicked or DoubleClicked.
	Cancelled [numButtons]bool
	// Clicks is the click count of a press edge (1, 2 = double click, ...).
	Clicks [numButtons]uint8
	// ScrollX, ScrollY: wheel/trackpad deltas this frame. Positive Y scrolls
	// toward the start of the content (the view moves up), like AppKit and
	// WM_MOUSEWHEEL. With Precise (trackpads) the deltas are pixels and are
	// applied directly; otherwise they count wheel notches. They are the sum
	// of the frame's Scrolls, which ui reads.
	ScrollX, ScrollY float32
	Precise          bool
	// Scrolls[:NScrolls] are the frame's scroll events in order, with their
	// gesture phases (AddScroll, AddScrollEvent): ui latches, axis-locks and
	// rubber-bands trackpad gestures from them.
	Scrolls  [MaxScrolls]ScrollEvent
	NScrolls int
	Mods     Mods
	// Keys[:NKeys] are this frame's key events; Text[:NText] is the UTF-8
	// text typed this frame.
	Keys  [MaxKeys]KeyEvent
	NKeys int
	Text  [MaxText]byte
	NText int
	// Text[CommitOff:CommitOff+CommitLen] is the part of Text that ended an
	// input-method composition (the committed text; 0 length when none). The
	// text fields give it to the field that was composing, wherever the focus
	// is by now, and Context.Input().Text no longer contains it (see
	// Context.Begin); the platform layer sets it.
	CommitOff, CommitLen int
	// Focused is true while the window has keyboard focus.
	Focused bool
	// FocusGained is true for the frame in which the window gained keyboard
	// focus (became the active window). A caller that shows the file system or
	// the like can look again; it costs nothing while nothing happens.
	FocusGained bool

	// Time is the platform clock (monotonic nanoseconds, platform.Now) at the
	// start of the frame. Text fields blink their caret by it; 0 means no
	// clock, and carets stay solid.
	Time int64

	// Preedit[:NPreedit] is the text an input method is composing (marked
	// text), shown by a focused text field at its caret; empty when no
	// composition is in progress. PreeditSel is the caret or selected part
	// inside it: byte offset and length. Unlike Text, which is the text typed
	// during one frame, the preedit is state: it stays until the input method
	// changes or clears it (SetPreedit).
	Preedit    [MaxPreedit]byte
	NPreedit   int
	PreeditSel [2]int

	// HasReplace is set when the first ReplaceN bytes of Text are not typed at
	// the caret but replace a range of the focused field's text (macOS
	// press-and-hold accents: "replace the character before the caret"). The
	// range is ReplaceLen UTF-16 units starting ReplaceStart units from the
	// caret (negative: before it), the units AppKit counts in; the field
	// converts them with its own text. The rest of Text is typed after it as
	// usual. A frame holds at most one replacement and it comes first. Set it
	// with ReplaceText.
	HasReplace   bool
	ReplaceStart int32
	ReplaceLen   int32
	ReplaceN     int

	// Edit[:NEdit] are this frame's edit actions for the focused text field
	// (PushEdit).
	Edit  [MaxEdit]EditAction
	NEdit int
	// Commands[:NCommands] are the ids of the menu commands the system
	// delivered this frame, such as a native menu item (PushCommand); package
	// menu takes them with the commands that matched a shortcut.
	Commands  [MaxCommands]uint16
	NCommands int
	// Actions[:NActions] are this frame's accessibility action requests
	// (PushAction); Signal.Action reports them to the box they name.
	Actions  [MaxActions]SemRequest
	NActions int

	// Paste is the clipboard text that answers a paste request
	// (Context.PasteRequest, platform.Window.RequestPaste); HasPaste is set
	// for the frame it arrives in, PasteTag names the field that asked. Only
	// the text field with that key takes it, and only while it has focus,
	// like an input-method commit; otherwise it is dropped. Set it with
	// SetPaste, which reuses Paste's storage (kept up to MaxKeptPaste).
	Paste    []byte
	PasteTag Key
	HasPaste bool

	// MenuIndex is the option the user chose in the native list menu that
	// the box MenuOwner requested (Context.RequestMenu), for the frame
	// HasMenuChoice is set in, among the options identified by MenuRev
	// (MenuRequest.Rev). Set it with SetMenuChoice.
	MenuOwner     Key
	MenuIndex     int32
	MenuRev       uint64
	HasMenuChoice bool
}

// PushEdit queues an edit action for the focused text field; it reports false
// if the frame's queue is full.
func (in *Input) PushEdit(a EditAction) bool {
	if in.NEdit >= MaxEdit {
		return false
	}
	in.Edit[in.NEdit] = a
	in.NEdit++
	return true
}

// PushCommand queues the menu command with the given id; it reports false if
// the frame's queue is full.
func (in *Input) PushCommand(id uint16) bool {
	if in.NCommands >= MaxCommands {
		return false
	}
	in.Commands[in.NCommands] = id
	in.NCommands++
	return true
}

// PushAction queues an accessibility action request; it reports false if the
// frame's queue is full.
func (in *Input) PushAction(r SemRequest) bool {
	if in.NActions >= MaxActions {
		return false
	}
	in.Actions[in.NActions] = r
	in.NActions++
	return true
}

// SetPaste records the answer to a paste request made by the field with key
// tag: ok is false when the clipboard had no text (nothing is delivered). The
// text is copied into Paste, reusing its capacity, so it does not allocate once
// the buffer has grown to the largest paste. A later answer in the same frame
// replaces an earlier one.
func (in *Input) SetPaste(tag Key, b []byte, ok bool) {
	in.PasteTag, in.HasPaste = tag, ok
	if !ok {
		in.Paste = in.Paste[:0]
		return
	}
	in.Paste = append(in.Paste[:0], b...)
}

// SetButton records a button change as a state and an edge.
func (in *Input) SetButton(b int, down bool, clicks uint8) {
	if b < 0 || b >= numButtons || in.Down[b] == down {
		return
	}
	in.Down[b] = down
	if down {
		in.Pressed[b] = true
		in.Clicks[b] = clicks
	} else {
		in.Released[b] = true
	}
}

// Cancel ends a held button without a release (focus loss): Down goes false and
// Cancelled is set, so the box that holds the capture is not clicked.
func (in *Input) Cancel(b int) {
	if b < 0 || b >= numButtons || !in.Down[b] {
		return
	}
	in.Down[b] = false
	in.Cancelled[b] = true
}

// AddScroll accumulates a scroll event without a phase (a wheel, or precise
// deltas from a device that reports no gesture): ScrollX, ScrollY and a
// record in Scrolls.
func (in *Input) AddScroll(dx, dy float32, precise bool) {
	in.ScrollX += dx
	in.ScrollY += dy
	in.Precise = precise
	in.AddScrollEvent(ScrollEvent{DX: dx, DY: dy, Precise: precise})
}

// AddScrollEvent appends e to Scrolls; it leaves ScrollX and ScrollY alone
// (the caller keeps them). Events are kept one by one, so that how a stream
// of them is split into frames does not change what they do (the axis a
// gesture locks to, the momentum's velocity, the box each goes to). When
// Scrolls is full, the earliest two consecutive events that can be merged
// without changing what they do are (scrollMergeable), to make room; when no
// two can, e is not kept and AddScrollEvent reports false (app keeps it for
// the next frame instead).
func (in *Input) AddScrollEvent(e ScrollEvent) bool {
	if in.NScrolls == MaxScrolls {
		i := 0
		for i < in.NScrolls-1 && !scrollMergeable(&in.Scrolls[i], &in.Scrolls[i+1]) {
			i++
		}
		if i == in.NScrolls-1 {
			if !scrollMergeable(&in.Scrolls[i], &e) {
				return false
			}
			mergeScroll(&in.Scrolls[i], &e)
			return true
		}
		mergeScroll(&in.Scrolls[i], &in.Scrolls[i+1])
		copy(in.Scrolls[i+1:in.NScrolls], in.Scrolls[i+2:in.NScrolls])
		in.NScrolls--
	}
	in.Scrolls[in.NScrolls] = e
	in.NScrolls++
	return true
}

// scrollMergeable reports whether consecutive events a and b do as one event
// what they do one after the other: the same stage of one gesture (the
// fingers moving, the momentum moving) or both plain scrolling at the same
// point (so to the same box), from the same kind of device, in the same
// direction on each axis (so that the axis lock and the clamping at the
// edges see the same thing). Only the velocity of a merged momentum is an
// average.
func scrollMergeable(a, b *ScrollEvent) bool {
	if a.Phase != b.Phase || a.Momentum != b.Momentum || a.Precise != b.Precise || a.Inverted != b.Inverted ||
		a.DX*b.DX < 0 || a.DY*b.DY < 0 {
		return false
	}
	switch {
	case a.Phase == PhaseChanged || a.Phase == PhaseStationary:
		return a.Momentum == PhaseNone
	case a.Phase == PhaseNone && a.Momentum == PhaseChanged:
		return true
	case a.Phase == PhaseNone && a.Momentum == PhaseNone:
		return a.Pos == b.Pos && a.X == b.X && a.Y == b.Y
	}
	return false
}

// mergeScroll adds b, the event after a, to a.
func mergeScroll(a, b *ScrollEvent) {
	a.DX += b.DX
	a.DY += b.DY
	if b.Time != 0 {
		a.Time = b.Time
	}
}

// PushKey queues a key event; it reports false if the frame's queue is full.
func (in *Input) PushKey(e KeyEvent) bool {
	if in.NKeys >= MaxKeys {
		return false
	}
	in.Keys[in.NKeys] = e
	in.NKeys++
	return true
}

// CanReplace reports whether ReplaceText can be used now: the frame has no
// replacement yet and no typed text, which a replacement must precede.
func (in *Input) CanReplace() bool { return !in.HasReplace && in.NText == 0 }

// ReplaceText records a replacement of the range of units UTF-16 units that
// starts start units from the caret (see HasReplace) by s, which may be empty
// (a removal), and queues s as the typed text of the frame. It reports false,
// changing nothing, if the frame cannot take one (CanReplace); text that does
// not fit is dropped whole characters at a time, as AppendText does.
func (in *Input) ReplaceText(start, units int32, s []byte) bool {
	if !in.CanReplace() {
		return false
	}
	n := min(len(s), len(in.Text)-in.NText)
	for n < len(s) && n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	in.NText += copy(in.Text[in.NText:], s[:n])
	in.HasReplace, in.ReplaceStart, in.ReplaceLen, in.ReplaceN = true, start, units, in.NText
	return true
}

// AppendText queues typed text, dropping what does not fit (whole characters
// only: a character that would be cut is dropped with the rest).
func (in *Input) AppendText(s string) {
	n := min(len(s), len(in.Text)-in.NText)
	for n < len(s) && n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	in.NText += copy(in.Text[in.NText:], s[:n])
}

// SetPreedit sets the composition text and the caret or selection inside it
// (byte offset and length into p); an empty p clears the composition. Text that
// does not fit MaxPreedit is cut at a character boundary.
func (in *Input) SetPreedit(p []byte, selStart, selLen int) {
	n := min(len(p), len(in.Preedit))
	for n < len(p) && n > 0 && !utf8.RuneStart(p[n]) {
		n--
	}
	in.NPreedit = copy(in.Preedit[:], p[:n])
	in.PreeditSel = [2]int{min(max(selStart, 0), in.NPreedit), 0}
	in.PreeditSel[1] = min(max(selLen, 0), in.NPreedit-in.PreeditSel[0])
}

// ClearFrame clears the per-frame parts (edges, scroll, keys, text) and
// keeps the persistent state (position, buttons down, modifiers).
func (in *Input) ClearFrame() {
	in.Pressed = [numButtons]bool{}
	in.Released = [numButtons]bool{}
	in.Cancelled = [numButtons]bool{}
	in.Clicks = [numButtons]uint8{}
	in.ScrollX, in.ScrollY, in.NScrolls = 0, 0, 0
	in.NKeys, in.NText = 0, 0
	in.CommitOff, in.CommitLen = 0, 0
	in.FocusGained = false
	in.NEdit, in.NCommands, in.NActions = 0, 0, 0
	in.HasPaste, in.HasReplace, in.ReplaceN = false, false, 0
	in.HasMenuChoice = false
	if cap(in.Paste) > MaxKeptPaste { // do not hold on to more than a supported paste
		in.Paste = nil
	}
	in.Paste = in.Paste[:0]
}

// MaxKeptPaste is the largest storage for pasted text that an Input keeps from
// one frame to the next: the most the platform delivers (platform.MaxPaste,
// 16 MiB), so that repeated supported pastes of any size reuse the storage the
// first one grew and allocate nothing. The storage stays reserved after a
// large paste; ReleasePaste gives it back.
const MaxKeptPaste = platform.MaxPaste

// ReservePaste makes room for a paste of up to n bytes (at most MaxKeptPaste)
// now, so that not even the first one allocates.
func (in *Input) ReservePaste(n int) {
	n = min(max(n, 0), MaxKeptPaste)
	if cap(in.Paste) < n {
		in.Paste = append(make([]byte, 0, n), in.Paste...)
	}
}

// ReleasePaste drops the storage of pasted text; the next paste grows it again.
func (in *Input) ReleasePaste() { in.Paste = nil }

// Signal is what a box reports about the pointer and keyboard this frame.
type Signal struct {
	Hovering      bool      // the pointer is over the box (and nothing else holds the mouse)
	Pressed       bool      // the left button went down on the box this frame
	Released      bool      // the left button went up this frame after going down on the box (the pointer may be elsewhere)
	Clicked       bool      // Released with the pointer still over the box
	DoubleClicked bool      // Clicked, and the press was a double click
	RightClicked  bool      // same as Clicked for the right button
	Dragging      bool      // the left button is held after going down on the box, wherever the pointer is
	Activated     bool      // the box has focus and Enter or Space went down, or an assistive technology asked to press it
	Action        SemAction // the accessibility actions requested of the box this frame (Input.Actions)
	ActionValue   float64   // the argument of the last requested action (ActSetValue)
	DragDelta     [2]float32
	Scroll        [2]float32 // scroll applied to the box this frame, in pixels (positive scrolls the view toward the end)
	Mouse         [2]float32 // pointer position relative to the box's top-left corner (previous frame's rectangle)
}
